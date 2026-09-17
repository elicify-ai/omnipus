// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package filegrep

import (
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

// buildFS builds an in-memory fs.FS from path -> content pairs. A trailing
// "/" on a key creates an empty directory entry (fstest.MapFS otherwise
// infers directories from file paths, which is all most tests need).
func buildFS(files map[string]string) fstest.MapFS {
	out := fstest.MapFS{}
	for p, content := range files {
		if strings.HasSuffix(p, "/") {
			out[strings.TrimSuffix(p, "/")] = &fstest.MapFile{Mode: fs.ModeDir}
			continue
		}
		out[p] = &fstest.MapFile{Data: []byte(content)}
	}
	return out
}

func oneRoot(fsys fs.FS) []Root {
	return []Root{{Name: "", FS: fsys}}
}

// slowFS wraps an fs.FS and sleeps for delay before every ReadDir/Open call,
// so a test can force a real wall-clock deadline breach deterministically
// without depending on machine speed or file-count scale.
type slowFS struct {
	fs.FS
	delay time.Duration
}

func (s slowFS) Open(name string) (fs.File, error) {
	time.Sleep(s.delay)
	v, err := s.FS.Open(name)
	if err != nil {
		return nil, fmt.Errorf("slowFS.Open: %w", err)
	}
	return v, nil
}

func (s slowFS) ReadDir(name string) ([]fs.DirEntry, error) {
	time.Sleep(s.delay)
	if rd, ok := s.FS.(fs.ReadDirFS); ok {
		entries, err := rd.ReadDir(name)
		if err != nil {
			return nil, fmt.Errorf("slowFS.ReadDir: %w", err)
		}
		return entries, nil
	}
	entries, err := fs.ReadDir(s.FS, name)
	if err != nil {
		return nil, fmt.Errorf("slowFS.ReadDir: %w", err)
	}
	return entries, nil
}

func (s slowFS) Stat(name string) (fs.FileInfo, error) {
	if sf, ok := s.FS.(fs.StatFS); ok {
		info, err := sf.Stat(name)
		if err != nil {
			return nil, fmt.Errorf("slowFS.Stat: %w", err)
		}
		return info, nil
	}
	info, err := fs.Stat(s.FS, name)
	if err != nil {
		return nil, fmt.Errorf("slowFS.Stat: %w", err)
	}
	return info, nil
}

// failingFS wraps an fs.FS and injects errors under test control:
//   - failOpen, when non-nil, makes Open fail for any path it approves.
//   - readDirBudget, when > 0, lets that many ReadDir calls succeed; the
//     next one (and everything after) fails, and ALSO makes Stat(".") start
//     failing from that point on — simulating a mount that dies mid-walk
//     (both its directory listings and its own root health probe go dark
//     together), as opposed to one isolated unreadable directory.
//   - failReadDirOnce, when non-nil, fails ReadDir exactly once for any
//     directory name it approves, WITHOUT poisoning Stat(".") — simulating
//     one isolated unreadable subdirectory while the root/mount itself stays
//     perfectly healthy (the "must NOT promote to root_lost" boundary case).
//     Deliberately never a real chmod-000 (permission tests are void when
//     CI runs as root; this seam works identically either way).
type failingFS struct {
	fs.FS
	failOpen        func(name string) bool
	readDirBudget   int32 // <=0 means unlimited (no ReadDir failures injected)
	failReadDirOnce func(name string) bool

	mu           sync.Mutex
	calls        int32
	poisoned     bool
	firedOnceFor map[string]bool
}

func (f *failingFS) Open(name string) (fs.File, error) {
	if f.failOpen != nil && f.failOpen(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	file, err := f.FS.Open(name)
	if err != nil {
		return nil, fmt.Errorf("Open: %w", err)
	}
	return file, nil
}

func (f *failingFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if f.failReadDirOnce != nil && f.failReadDirOnce(name) {
		f.mu.Lock()
		if f.firedOnceFor == nil {
			f.firedOnceFor = map[string]bool{}
		}
		already := f.firedOnceFor[name]
		f.firedOnceFor[name] = true
		f.mu.Unlock()
		if !already {
			return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrPermission}
		}
	}
	if f.readDirBudget > 0 {
		f.mu.Lock()
		f.calls++
		if f.calls > f.readDirBudget {
			f.poisoned = true
			f.mu.Unlock()
			return nil, &fs.PathError{Op: "readdir", Path: name, Err: errMountGone}
		}
		f.mu.Unlock()
	}
	if rd, ok := f.FS.(fs.ReadDirFS); ok {
		entries, err := rd.ReadDir(name)
		if err != nil {
			return nil, fmt.Errorf("ReadDir: %w", err)
		}
		return entries, nil
	}
	entries, err := fs.ReadDir(f.FS, name)
	if err != nil {
		return nil, fmt.Errorf("ReadDir: %w", err)
	}
	return entries, nil
}

func (f *failingFS) Stat(name string) (fs.FileInfo, error) {
	f.mu.Lock()
	poisoned := f.poisoned
	f.mu.Unlock()
	if poisoned {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: errMountGone}
	}
	if sf, ok := f.FS.(fs.StatFS); ok {
		info, err := sf.Stat(name)
		if err != nil {
			return nil, fmt.Errorf("Stat: %w", err)
		}
		return info, nil
	}
	info, err := fs.Stat(f.FS, name)
	if err != nil {
		return nil, fmt.Errorf("Stat: %w", err)
	}
	return info, nil
}

type mountGoneError struct{}

func (mountGoneError) Error() string { return "mount gone" }

var errMountGone = mountGoneError{}

// mustSearch is a small assertion helper for the common "no compile error"
// case used throughout the walk-behavior tests.
func mustSearch(t *testing.T, roots []Root, opts Options) Result {
	t.Helper()
	res, err := Search(t.Context(), roots, opts)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	return res
}
