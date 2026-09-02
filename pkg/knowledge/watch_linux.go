//go:build linux

// Linux platform backend for filesystem watching (watch.go): inotify.
//
// One inotify instance (one fd) covers the whole collection; a watch is
// registered per DIRECTORY (design §6: "on Linux watches are per-directory,
// so 385 against a typical limit of 65,536+ — two orders of magnitude of
// headroom" for the measured real vault). Every event inotify delivers
// already names the exact child that changed, so unlike the kqueue backend
// this never needs to re-list a directory and diff it against a cached
// listing to work out what happened.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// linuxDirWatchMask is what every watched directory is registered with.
// IN_ONLYDIR guards against a TOCTOU race (the path was a directory when
// scanned, a file by the time inotify_add_watch runs) rather than silently
// watching the wrong kind of thing.
const linuxDirWatchMask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MODIFY |
	unix.IN_MOVED_FROM | unix.IN_MOVED_TO | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF |
	unix.IN_ONLYDIR

func startPlatformWatch(root string, out chan<- fsEvent, overflow chan<- struct{}, stop <-chan struct{}) (<-chan error, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, &WatchUnavailableError{Reason: "inotify_init1 failed", Err: err}
	}

	lw := &linuxWatcher{
		fd:      fd,
		root:    root,
		wdToRel: make(map[int32]string),
		relToWd: make(map[string]int32),
	}
	// out/stop are nil at startup: the caller's own startup sweep already
	// covers whatever already exists under root, so there is no need to
	// (redundantly) report every pre-existing file here too. See addTree's
	// comment for why they are non-nil when a NEW directory appears later.
	if err := lw.addTree("", nil, nil); err != nil {
		closeFDQuietly(fd, "inotify instance (failed startup)")
		return nil, &WatchUnavailableError{Reason: fmt.Sprintf("failed to watch collection root %s", root), Err: err}
	}

	runErr := make(chan error, 1)
	go lw.loop(out, overflow, stop, runErr)
	return runErr, nil
}

// linuxWatcher owns one inotify fd and the wd<->relPath mapping every event
// is translated through. wdToRel/relToWd are guarded by mu because addTree
// (called both at startup and, recursively, whenever a new directory
// appears) and the read loop's cleanup on IN_IGNORED both mutate them, and
// they run on different goroutines when a new directory is discovered
// (handle runs on the loop goroutine, which is the only place addTree is
// ever called after startup — so in practice there is exactly one writer at
// a time, but the map is still read from the same goroutine concurrently
// with no other writer possible, and Go's map is not otherwise safe to read
// during a concurrent write from a different call site, so this documents
// the guarantee rather than removing the lock as an optimisation).
type linuxWatcher struct {
	fd   int
	root string

	mu      sync.Mutex
	wdToRel map[int32]string
	relToWd map[string]int32
}

func (lw *linuxWatcher) absPath(rel string) string {
	if rel == "" {
		return lw.root
	}
	return filepath.Join(lw.root, filepath.FromSlash(rel))
}

// addTree registers a watch on startRel and, best-effort, on every
// non-skipped subdirectory beneath it (mirroring scan.go's own walk rules:
// symlinked directories are never followed, scanSkippedDirNames is never
// descended into). Only a failure to watch startRel ITSELF is returned as an
// error — a failure deeper in the tree (a permission hole, the inotify
// watch-count limit reached partway through) is logged and skipped, because
// one unwatchable subdirectory must not cost the rest of the tree its
// instant coverage; that subtree simply falls back to the periodic sweep,
// same as any other missed event (the governing principle this whole file
// is built on).
//
// out and stop, when non-nil, additionally report every regular file already
// found while walking startRel as an fsEvent (not removed) — finding 4: a
// directory that appears with files already in it (a `mv` bulk import, a git
// checkout materialising a folder) otherwise gets its watches registered but
// no report of the files already inside, leaving them invisible until the
// next periodic sweep, which is exactly the bulk-import case a user is most
// likely to try immediately. Pass nil, nil from startPlatformWatch's initial
// call — the caller's own startup sweep already covers whatever exists under
// root at boot, so emitting events there would be pure duplicate work, not a
// correctness gain.
func (lw *linuxWatcher) addTree(startRel string, out chan<- fsEvent, stop <-chan struct{}) error {
	startAbs := lw.absPath(startRel)
	return filepath.WalkDir(startAbs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == startAbs {
				return err
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			if out == nil {
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			rel, relErr := filepath.Rel(lw.root, path)
			if relErr != nil {
				return nil //nolint:nilerr // deliberate skip-and-continue, see the directory branch's identical comment below
			}
			rel = filepath.ToSlash(rel)
			if isWatchSyncArtifact(filepath.Base(rel)) {
				return nil
			}
			sendEvent(out, fsEvent{relPath: rel, removed: false}, stop)
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return filepath.SkipDir
		}
		rel, relErr := filepath.Rel(lw.root, path)
		if relErr != nil {
			// path always comes from WalkDir under lw.root, so this cannot
			// actually fail in practice; skipping rather than aborting the
			// whole walk matches this file's "one bad path must not cost the
			// rest of the tree" contract if it somehow ever did.
			return nil //nolint:nilerr // deliberate skip-and-continue, not a swallowed failure
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			rel = ""
		}
		if path != lw.root {
			if _, skip := scanSkippedDirNames[filepath.Base(path)]; skip {
				return filepath.SkipDir
			}
		}
		if addErr := lw.addDir(rel, path); addErr != nil {
			if path == startAbs {
				return addErr
			}
			slog.Default().Warn("knowledge: watcher could not watch a subdirectory; the periodic sweep still covers it",
				"path", path, "error", addErr)
		}
		return nil
	})
}

func (lw *linuxWatcher) addDir(rel, abs string) error {
	wd, err := unix.InotifyAddWatch(lw.fd, abs, linuxDirWatchMask)
	if err != nil {
		return fmt.Errorf("inotify_add_watch %s: %w", abs, err)
	}
	lw.mu.Lock()
	lw.wdToRel[int32(wd)] = rel
	lw.relToWd[rel] = int32(wd)
	lw.mu.Unlock()
	return nil
}

// loop reads inotify events until stop is closed or the fd itself fails.
// It polls with a short timeout rather than blocking in Read forever so it
// can notice stop being closed without needing a self-pipe or racing a
// concurrent Close of the fd from another goroutine.
func (lw *linuxWatcher) loop(out chan<- fsEvent, overflow chan<- struct{}, stop <-chan struct{}, runErr chan<- error) {
	defer close(runErr)
	defer closeFDQuietly(lw.fd, "inotify instance")

	buf := make([]byte, 64*1024)
	for {
		select {
		case <-stop:
			return
		default:
		}

		pfds := []unix.PollFd{{Fd: int32(lw.fd), Events: unix.POLLIN}}
		n, err := unix.Poll(pfds, 200)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			reportRunErr(runErr, fmt.Errorf("knowledge: inotify poll: %w", err))
			return
		}
		if n == 0 {
			continue
		}

		nRead, err := unix.Read(lw.fd, buf)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
				continue
			}
			reportRunErr(runErr, fmt.Errorf("knowledge: inotify read: %w", err))
			return
		}
		if nRead <= 0 {
			continue
		}

		for _, ev := range parseInotifyEvents(buf[:nRead]) {
			lw.handle(ev, out, overflow, stop)
		}
	}
}

// closeFDQuietly closes fd, logging rather than discarding a failure — an
// fd close failing is rare and never actionable by a caller here (there is
// no one left holding a reference to retry against), but it must still be
// OBSERVABLE rather than silently dropped.
func closeFDQuietly(fd int, what string) {
	if err := unix.Close(fd); err != nil {
		slog.Default().Warn("knowledge: watcher failed to close a file descriptor", "what", what, "fd", fd, "error", err)
	}
}

type rawInotifyEvent struct {
	wd   int32
	mask uint32
	name string
}

// parseInotifyEvents unpacks the variable-length record format inotify's
// read(2) returns: a fixed unix.InotifyEvent header immediately followed by
// Len bytes holding a NUL-padded name, repeated back to back for as many
// events as fit in the read buffer. This is a path-name parse, never a file
// content read — the bytes it looks at come from inotify's own kernel
// buffer, not from the watched files.
func parseInotifyEvents(buf []byte) []rawInotifyEvent {
	var out []rawInotifyEvent
	off := 0
	for off+unix.SizeofInotifyEvent <= len(buf) {
		raw := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off])) //nolint:gosec // fixed-layout kernel record, bounds-checked by the loop condition
		nameLen := int(raw.Len)
		if nameLen < 0 || off+unix.SizeofInotifyEvent+nameLen > len(buf) {
			break // a malformed/truncated tail; stop rather than read out of bounds
		}
		var name string
		if nameLen > 0 {
			nameBytes := buf[off+unix.SizeofInotifyEvent : off+unix.SizeofInotifyEvent+nameLen]
			end := len(nameBytes)
			for i, b := range nameBytes {
				if b == 0 {
					end = i
					break
				}
			}
			name = string(nameBytes[:end])
		}
		out = append(out, rawInotifyEvent{wd: raw.Wd, mask: raw.Mask, name: name})
		off += unix.SizeofInotifyEvent + nameLen
	}
	return out
}

func (lw *linuxWatcher) handle(ev rawInotifyEvent, out chan<- fsEvent, overflow chan<- struct{}, stop <-chan struct{}) {
	if ev.mask&unix.IN_Q_OVERFLOW != 0 {
		select {
		case overflow <- struct{}{}:
		case <-stop:
		default:
		}
		return
	}

	if ev.mask&unix.IN_IGNORED != 0 {
		// The watch itself is gone (explicitly removed, or its directory no
		// longer exists). Forget the mapping; anything still remembered by
		// the manifest under this path is reconciled by the next sweep, not
		// by this watcher (governing principle again).
		lw.mu.Lock()
		if r, ok := lw.wdToRel[ev.wd]; ok {
			delete(lw.wdToRel, ev.wd)
			delete(lw.relToWd, r)
		}
		lw.mu.Unlock()
		return
	}

	lw.mu.Lock()
	dirRel, known := lw.wdToRel[ev.wd]
	lw.mu.Unlock()
	if !known || ev.name == "" {
		return
	}
	if isWatchSyncArtifact(ev.name) {
		return
	}

	rel := joinRelPath(dirRel, ev.name)

	if ev.mask&unix.IN_ISDIR != 0 {
		if ev.mask&(unix.IN_CREATE|unix.IN_MOVED_TO) != 0 {
			if _, skip := scanSkippedDirNames[ev.name]; !skip {
				if err := lw.addTree(rel, out, stop); err != nil {
					slog.Default().Warn("knowledge: watcher could not watch a newly created directory; the periodic sweep still covers it",
						"path", rel, "error", err)
				}
			}
		}
		// IN_DELETE/IN_MOVED_FROM for a directory needs no handling here:
		// that directory's OWN watch (if it had one) separately receives
		// IN_DELETE_SELF/IN_MOVE_SELF and is cleaned up via IN_IGNORED
		// above.
		return
	}

	switch {
	case ev.mask&(unix.IN_CREATE|unix.IN_MODIFY|unix.IN_MOVED_TO) != 0:
		sendEvent(out, fsEvent{relPath: rel, removed: false}, stop)
	case ev.mask&(unix.IN_DELETE|unix.IN_MOVED_FROM) != 0:
		sendEvent(out, fsEvent{relPath: rel, removed: true}, stop)
	}
}
