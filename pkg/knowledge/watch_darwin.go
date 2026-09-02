//go:build darwin

// macOS platform backend for filesystem watching (watch.go): kqueue.
//
// One kqueue (one fd, opened via unix.Kqueue) is the single "handle" design
// §6 refers to: every watched vnode is registered INTO that one queue via
// EVFILT_VNODE, and one kevent(2) call drains events for all of them at
// once, rather than needing one pollable fd per watched path.
//
// A watch is registered per DIRECTORY, exactly like the Linux backend — see
// watch_linux.go's header for why that is sized by the measured 385
// directories on the real vault, not the 3,002 files. What differs from
// inotify is what a directory's own event tells you, and that difference is
// the whole reason this file is more involved than watch_linux.go:
//
//   - inotify's IN_CREATE/IN_DELETE/IN_MODIFY/... on a directory watch name
//     the exact child that changed, INCLUDING a write to that child's own
//     content — a plain in-place os.WriteFile (truncate + rewrite the same
//     inode, no rename involved) is reported directly.
//   - kqueue's NOTE_WRITE on a directory only means "this directory's own
//     entries changed" (an add, a remove, a rename). It does NOT fire for a
//     write to an existing file's content that leaves the directory's entry
//     list untouched — which is exactly what a plain in-place save is. A
//     directory-only watch would therefore MISS every editor that saves by
//     truncating and rewriting rather than by temp-file-plus-rename, which
//     is common enough that this gap showed up immediately in this
//     backend's own tests (TestWatcher_EditedFile_OldStopsNewMatches,
//     TestWatcher_DebounceCollapsesRapidSaves) against b2WriteFile, which
//     is exactly that pattern (os.WriteFile with O_TRUNC).
//
// So this backend ALSO opens and registers a watch on every regular file
// inside each watched directory — fsnotify's kqueue backend does the same,
// for the same reason. A file's own NOTE_WRITE/NOTE_EXTEND/NOTE_ATTRIB
// reports an in-place edit directly; its own NOTE_DELETE/NOTE_RENAME means
// the inode we had open is gone from that name (a real delete, or an
// atomic-replace rename landing on it), resolved by re-Lstat-ing the path
// (handleFileGone). The directory-level listing diff (handleDirChanged)
// still exists and still matters: it is what notices a brand new file
// appearing (so it can be watched for the first time) or a tracked file
// disappearing without ever producing its own NOTE_DELETE (a directory
// watch that failed to register in the first place, or an inode-diff safety
// net for a watch this backend never got to open).
//
// design §6 says "no watch cap" — watching every file rather than only every
// directory is what that rule actually costs on this platform, and
// raiseOpenFileLimit exists because of it: the real vault's 3,002 files plus
// 385 directories can exceed a shell's default 256-fd soft limit, so this
// backend asks the kernel for its own reported hard limit before opening
// anything. If that still is not enough for some files, watchFile/watchDir
// failing on them is logged and non-fatal (see addTree's contract) — those
// specific paths just fall back to the periodic sweep, same as any other
// gap this file cannot close, never a reason to refuse to start.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// darwinDirWatchFflags: EV_CLEAR re-arms the event after each delivery (kqueue
// is level-triggered by default for NOTE_WRITE on a busy directory; without
// EV_CLEAR a second, unrelated change could be coalesced away). NOTE_REVOKE
// covers the vnode being forcibly invalidated (an unmount underneath it).
const darwinDirWatchFflags = unix.NOTE_WRITE | unix.NOTE_DELETE | unix.NOTE_RENAME | unix.NOTE_REVOKE

// darwinFileWatchFflags additionally carries NOTE_EXTEND (a write that grows
// the file without necessarily reporting NOTE_WRITE on every filesystem) and
// NOTE_ATTRIB (a metadata-only change; harmless to over-report given
// UpdatePath's own content hash makes a no-op change free).
const darwinFileWatchFflags = unix.NOTE_WRITE | unix.NOTE_EXTEND | unix.NOTE_ATTRIB |
	unix.NOTE_DELETE | unix.NOTE_RENAME | unix.NOTE_REVOKE

func startPlatformWatch(root string, out chan<- fsEvent, overflow chan<- struct{}, stop <-chan struct{}) (<-chan error, error) {
	kq, err := unix.Kqueue()
	if err != nil {
		return nil, &WatchUnavailableError{Reason: "kqueue failed", Err: err}
	}

	raiseOpenFileLimit()

	dw := &darwinWatcher{
		kq:        kq,
		root:      root,
		fdToEntry: make(map[int]watchedEntry),
		relToFd:   make(map[string]int),
		listing:   make(map[string]map[string]dirChild),
	}
	if err := dw.addTree(""); err != nil {
		dw.closeAll()
		return nil, &WatchUnavailableError{Reason: fmt.Sprintf("failed to watch collection root %s", root), Err: err}
	}

	runErr := make(chan error, 1)
	go dw.loop(out, overflow, stop, runErr)
	return runErr, nil
}

// raiseOpenFileLimit asks the kernel to raise this process's open-file soft
// limit to whatever hard limit it already reports, so a real vault's file
// count (thousands, once every file — not just every directory — is
// individually watched; see the package comment above) is less likely to
// exhaust a shell-inherited default. Best-effort and never fatal: design §6
// forbids a watch CAP, but it cannot repeal rlimit, so a failure here is
// logged and this backend proceeds with whatever limit it already has —
// some files simply fall back to the periodic sweep, same as any other
// per-path watch failure this file already tolerates.
func raiseOpenFileLimit() {
	var rl unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &rl); err != nil {
		slog.Default().Warn("knowledge: watcher could not read the open-file limit; proceeding with the current limit", "error", err)
		return
	}
	if rl.Cur >= rl.Max {
		return
	}
	newLimit := unix.Rlimit{Cur: rl.Max, Max: rl.Max}
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &newLimit); err != nil {
		slog.Default().Warn("knowledge: watcher could not raise the open-file limit; some files may not get instant watch coverage",
			"current", rl.Cur, "wanted", rl.Max, "error", err)
	}
}

// dirChild is what darwinWatcher remembers about one entry of a watched
// directory, just enough to detect an add, a remove, or a same-name
// atomic-replace between two listings.
type dirChild struct {
	isDir bool
	ino   uint64
}

// watchedEntry is what one open, kqueue-registered fd is watching.
type watchedEntry struct {
	rel   string
	isDir bool
}

// darwinWatcher owns the kqueue fd, one open fd per watched directory AND
// per watched regular file (kqueue registers EVFILT_VNODE against a file
// descriptor, not a bare path), and the last directory listing seen for
// each watched directory. mu guards every map because the read loop (the
// only writer after startup) and any concurrent Close both touch them.
type darwinWatcher struct {
	kq   int
	root string

	mu        sync.Mutex
	fdToEntry map[int]watchedEntry
	relToFd   map[string]int
	listing   map[string]map[string]dirChild // directories only
}

func (dw *darwinWatcher) absPath(rel string) string {
	if rel == "" {
		return dw.root
	}
	return filepath.Join(dw.root, filepath.FromSlash(rel))
}

func (dw *darwinWatcher) hasWatch(rel string) bool {
	dw.mu.Lock()
	defer dw.mu.Unlock()
	_, ok := dw.relToFd[rel]
	return ok
}

// addTree registers startRel and, best-effort, every non-skipped
// subdirectory beneath it — the same "only the starting directory's own
// failure is fatal" contract as watch_linux.go's addTree, for the same
// reason: one unwatchable subdirectory must not cost the rest of the tree
// its instant coverage. watchDir (called for each directory found) is what
// additionally registers a watch on that directory's own files.
func (dw *darwinWatcher) addTree(startRel string) error {
	startAbs := dw.absPath(startRel)
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
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return filepath.SkipDir
		}
		rel, relErr := filepath.Rel(dw.root, path)
		if relErr != nil {
			// path always comes from WalkDir under dw.root, so this cannot
			// actually fail in practice; skipping rather than aborting the
			// whole walk matches this file's "one bad path must not cost the
			// rest of the tree" contract if it somehow ever did.
			return nil //nolint:nilerr // deliberate skip-and-continue, not a swallowed failure
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			rel = ""
		}
		if path != dw.root {
			if _, skip := scanSkippedDirNames[filepath.Base(path)]; skip {
				return filepath.SkipDir
			}
		}
		if wErr := dw.watchDir(rel, path); wErr != nil {
			if path == startAbs {
				return wErr
			}
			slog.Default().Warn("knowledge: watcher could not watch a subdirectory; the periodic sweep still covers it",
				"path", path, "error", wErr)
		}
		return nil
	})
}

// watchDir opens abs (O_EVTONLY: notification only, no read/write access
// needed and no interference with unmounting), registers it with the
// kqueue, snapshots its current listing as the baseline the first
// NOTE_WRITE will diff against, and registers a per-file watch (watchFile)
// on every regular file already in it — see the package comment for why a
// directory-only watch is not enough on this platform.
func (dw *darwinWatcher) watchDir(rel, abs string) error {
	fd, err := unix.Open(abs, unix.O_EVTONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", abs, err)
	}

	kev := unix.Kevent_t{
		Ident:  uint64(fd),
		Filter: unix.EVFILT_VNODE,
		Flags:  unix.EV_ADD | unix.EV_CLEAR,
		Fflags: darwinDirWatchFflags,
	}
	if _, kerr := unix.Kevent(dw.kq, []unix.Kevent_t{kev}, nil, nil); kerr != nil {
		closeFDQuietly(fd, "watch fd (failed kevent register, directory)")
		return fmt.Errorf("kevent register %s: %w", abs, kerr)
	}

	listing, lerr := dw.listDir(abs)
	if lerr != nil {
		// Non-fatal: we are watching from here on; we just start from an
		// empty baseline, so the first NOTE_WRITE sees every current entry
		// as "new" and (re-)indexes it, which is correct if slightly
		// redundant.
		listing = map[string]dirChild{}
	}

	dw.mu.Lock()
	dw.fdToEntry[fd] = watchedEntry{rel: rel, isDir: true}
	dw.relToFd[rel] = fd
	dw.listing[rel] = listing
	dw.mu.Unlock()

	for name, child := range listing {
		if child.isDir || isWatchSyncArtifact(name) {
			continue
		}
		childRel := joinRelPath(rel, name)
		if wErr := dw.watchFile(childRel, filepath.Join(abs, name)); wErr != nil {
			slog.Default().Warn("knowledge: watcher could not watch a file; the periodic sweep still covers it",
				"path", childRel, "error", wErr)
		}
	}
	return nil
}

// watchFile opens and registers a watch on one regular file, so an in-place
// content edit (no rename involved) is reported directly rather than relying
// on its parent directory's listing diff, which cannot see it — see the
// package comment.
func (dw *darwinWatcher) watchFile(rel, abs string) error {
	fd, err := unix.Open(abs, unix.O_EVTONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", abs, err)
	}
	kev := unix.Kevent_t{
		Ident:  uint64(fd),
		Filter: unix.EVFILT_VNODE,
		Flags:  unix.EV_ADD | unix.EV_CLEAR,
		Fflags: darwinFileWatchFflags,
	}
	if _, kerr := unix.Kevent(dw.kq, []unix.Kevent_t{kev}, nil, nil); kerr != nil {
		closeFDQuietly(fd, "watch fd (failed kevent register, file)")
		return fmt.Errorf("kevent register %s: %w", abs, kerr)
	}
	dw.mu.Lock()
	dw.fdToEntry[fd] = watchedEntry{rel: rel, isDir: false}
	dw.relToFd[rel] = fd
	dw.mu.Unlock()
	return nil
}

func (dw *darwinWatcher) listDir(abs string) (map[string]dirChild, error) {
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]dirChild, len(entries))
	for _, e := range entries {
		var st unix.Stat_t
		var ino uint64
		if lerr := unix.Lstat(filepath.Join(abs, e.Name()), &st); lerr == nil {
			ino = st.Ino
		}
		out[e.Name()] = dirChild{isDir: e.IsDir(), ino: ino}
	}
	return out, nil
}

func (dw *darwinWatcher) loop(out chan<- fsEvent, overflow chan<- struct{}, stop <-chan struct{}, runErr chan<- error) {
	defer close(runErr)
	defer dw.closeAll()

	events := make([]unix.Kevent_t, 64)
	for {
		select {
		case <-stop:
			return
		default:
		}

		timeout := unix.NsecToTimespec(int64(200 * time.Millisecond))
		n, err := unix.Kevent(dw.kq, nil, events, &timeout)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			reportRunErr(runErr, fmt.Errorf("knowledge: kevent: %w", err))
			return
		}
		for i := 0; i < n; i++ {
			dw.handle(events[i], out, stop)
		}
	}
}

func (dw *darwinWatcher) handle(ev unix.Kevent_t, out chan<- fsEvent, stop <-chan struct{}) {
	fd := int(ev.Ident)

	dw.mu.Lock()
	entry, known := dw.fdToEntry[fd]
	dw.mu.Unlock()
	if !known {
		return
	}

	if entry.isDir {
		if ev.Fflags&(unix.NOTE_DELETE|unix.NOTE_RENAME|unix.NOTE_REVOKE) != 0 {
			// The watched directory itself is gone, renamed away, or
			// revoked (e.g. its volume unmounted). Forget it and everything
			// nested under it — files included; the next sweep reconciles
			// anything the manifest still remembers there.
			dw.forgetSubtree(entry.rel)
			return
		}
		if ev.Fflags&unix.NOTE_WRITE != 0 {
			dw.handleDirChanged(entry.rel, dw.absPath(entry.rel), out, stop)
		}
		return
	}

	// A file's own watch.
	if isWatchSyncArtifact(filepath.Base(entry.rel)) {
		return
	}
	if ev.Fflags&(unix.NOTE_DELETE|unix.NOTE_RENAME|unix.NOTE_REVOKE) != 0 {
		dw.handleFileGone(fd, entry.rel, out, stop)
		return
	}
	if ev.Fflags&(unix.NOTE_WRITE|unix.NOTE_EXTEND|unix.NOTE_ATTRIB) != 0 {
		sendEvent(out, fsEvent{relPath: entry.rel, removed: false}, stop)
	}
}

// handleFileGone runs when a watched FILE's own vnode reports NOTE_DELETE,
// NOTE_RENAME, or NOTE_REVOKE: the inode this fd was watching is no longer
// reachable at rel. That is ambiguous on its own — it is what BOTH a real
// delete and an atomic-replace rename landing on the same name look like
// from the old inode's point of view — so this resolves it the only way
// that is actually authoritative: Lstat the path itself. Gone means removed;
// present means something new was just put there (design §7), which gets a
// fresh watch registered on ITS inode so a further in-place edit is still
// caught.
func (dw *darwinWatcher) handleFileGone(oldFD int, rel string, out chan<- fsEvent, stop <-chan struct{}) {
	abs := dw.absPath(rel)

	dw.mu.Lock()
	delete(dw.fdToEntry, oldFD)
	delete(dw.relToFd, rel)
	dw.mu.Unlock()
	closeFDQuietly(oldFD, "watch fd (file gone or replaced)")

	if _, statErr := os.Lstat(abs); statErr != nil {
		sendEvent(out, fsEvent{relPath: rel, removed: true}, stop)
		return
	}

	if wErr := dw.watchFile(rel, abs); wErr != nil {
		slog.Default().Warn("knowledge: watcher could not re-watch a replaced file; the periodic sweep still covers it",
			"path", rel, "error", wErr)
	}
	sendEvent(out, fsEvent{relPath: rel, removed: false}, stop)
}

// handleDirChanged re-lists rel and reports what changed against the
// previous listing: a name that appeared (file: watch it and report
// "changed"; directory: watch it and recurse), a name that disappeared
// (file: "removed"; directory: forget its whole subtree), or — a safety net
// for a file this backend never got an individual watch registered on — a
// name that stayed but now points at a different inode.
func (dw *darwinWatcher) handleDirChanged(rel, abs string, out chan<- fsEvent, stop <-chan struct{}) {
	newListing, err := dw.listDir(abs)
	if err != nil {
		// The directory may already be gone; its own NOTE_DELETE/NOTE_RENAME
		// handles that. A transient listing failure here is "nothing
		// observed this pass", not fatal.
		return
	}

	dw.mu.Lock()
	oldListing := dw.listing[rel]
	dw.listing[rel] = newListing
	dw.mu.Unlock()

	for name, child := range newListing {
		if isWatchSyncArtifact(name) {
			continue
		}
		old, existed := oldListing[name]
		childRel := joinRelPath(rel, name)
		childAbs := filepath.Join(abs, name)
		switch {
		case !existed:
			if child.isDir {
				if _, skip := scanSkippedDirNames[name]; !skip {
					if wErr := dw.addTree(childRel); wErr != nil {
						slog.Default().Warn("knowledge: watcher could not watch a newly created directory; the periodic sweep still covers it",
							"path", childRel, "error", wErr)
					}
				}
				continue
			}
			if wErr := dw.watchFile(childRel, childAbs); wErr != nil {
				slog.Default().Warn("knowledge: watcher could not watch a newly created file; the periodic sweep still covers it",
					"path", childRel, "error", wErr)
			}
			sendEvent(out, fsEvent{relPath: childRel, removed: false}, stop)
		case !child.isDir && !old.isDir && old.ino != child.ino:
			if !dw.hasWatch(childRel) {
				if wErr := dw.watchFile(childRel, childAbs); wErr != nil {
					slog.Default().Warn("knowledge: watcher could not re-watch a replaced file; the periodic sweep still covers it",
						"path", childRel, "error", wErr)
				}
			}
			sendEvent(out, fsEvent{relPath: childRel, removed: false}, stop)
		}
	}

	for name, old := range oldListing {
		if _, still := newListing[name]; still {
			continue
		}
		if isWatchSyncArtifact(name) {
			continue
		}
		childRel := joinRelPath(rel, name)
		if old.isDir {
			dw.forgetSubtree(childRel)
			continue
		}
		// A tracked file's own NOTE_DELETE (handleFileGone) is the primary
		// path for this and will usually have already fired and cleaned up
		// its watch; this is the safety net for a name this backend never
		// got an individual watch on. Reporting "removed" twice for the
		// same path is harmless (UpdatePath/RemovePath are idempotent).
		sendEvent(out, fsEvent{relPath: childRel, removed: true}, stop)
	}
}

// forgetSubtree closes and drops every watch at rel or nested beneath it —
// directories and files alike, since both share the fdToEntry/relToFd maps.
// It does not itself emit any fsEvent — a subtree disappearing wholesale is
// reconciled by the next sweep, not enumerated file-by-file here.
func (dw *darwinWatcher) forgetSubtree(rel string) {
	dw.mu.Lock()
	defer dw.mu.Unlock()
	prefix := rel + "/"
	for r, fd := range dw.relToFd {
		if r == rel || strings.HasPrefix(r, prefix) {
			closeFDQuietly(fd, "watch fd (subtree forgotten)")
			delete(dw.relToFd, r)
			delete(dw.fdToEntry, fd)
			delete(dw.listing, r)
		}
	}
}

func (dw *darwinWatcher) closeAll() {
	dw.mu.Lock()
	for fd := range dw.fdToEntry {
		closeFDQuietly(fd, "watch fd (shutdown)")
	}
	dw.fdToEntry = map[int]watchedEntry{}
	dw.relToFd = map[string]int{}
	dw.listing = map[string]map[string]dirChild{}
	dw.mu.Unlock()
	closeFDQuietly(dw.kq, "kqueue fd")
}

// closeFDQuietly closes fd, logging rather than discarding a failure — see
// watch_linux.go's identical helper for why this is logged, not silently
// dropped, even though nothing here can act on the error.
func closeFDQuietly(fd int, what string) {
	if err := unix.Close(fd); err != nil {
		slog.Default().Warn("knowledge: watcher failed to close a file descriptor", "what", what, "fd", fd, "error", err)
	}
}
