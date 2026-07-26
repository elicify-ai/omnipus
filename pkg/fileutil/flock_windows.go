// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package fileutil

import (
	"log/slog"
	"os"
	"sync"
)

// On Windows there is NO cross-process locking at all: fn is called without
// OS-level locking as graceful degradation per hard constraint 4.
//
// DO NOT read this as "the single-writer goroutine pattern covers it." An
// audit during ADR-054 (see its §5.1) checked all six file-store packages —
// pkg/task, pkg/plan, pkg/session, pkg/credentials, pkg/auth, pkg/agentstore
// — and found that NONE of them implements a single-writer goroutine. They
// all pair an in-process mutex with this same WithFlock call. So on Windows
// every one of them has in-process protection only, and two Omnipus processes
// sharing one $OMNIPUS_HOME can interleave a read-modify-write and lose data.
// The single-instance pidfile that ADR-054 D4 leans on for this story was
// never built either (daemon.Status is a soft CLI convenience check).
//
// Closing this needs LockFileEx here (which fixes every caller at once) or the
// O_EXCL approach already shipped in pkg/tools/browser/coordinator_lock_other.go.
func flockExclusive(_ *os.File) error { return nil }
func flockUnlock(_ *os.File) error    { return nil }

// warnOnce ensures the advisory-flock degradation warning is emitted at most
// once per process lifetime to avoid log spam on every WithFlock call.
var warnOnce sync.Once

// WithFlock on Windows calls fn directly without opening or locking the
// target file. Opening the file on Windows would leave a read/write handle
// open for the duration of fn, which prevents WriteFileAtomic from renaming
// the temp file over the destination (Windows denies rename when any handle is
// open on the destination file). Callers therefore get their in-process mutex
// and NOTHING more — there is no cross-process guarantee on this platform.
//
// Per hard constraint 4 (graceful degradation), this is logged once at Warn
// level so operators are aware that OS-level advisory locking is inactive.
func WithFlock(path string, fn func() error) error {
	warnOnce.Do(func() {
		slog.Warn("fileutil: Windows lacks advisory flock; NO cross-process write protection — "+
			"concurrent Omnipus processes sharing one OMNIPUS_HOME can lose writes",
			"file", path)
	})
	return fn()
}
