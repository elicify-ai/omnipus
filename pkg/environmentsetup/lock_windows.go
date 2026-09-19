// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package environmentsetup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// installLock is the workspace prefix lifetime lock on Windows. This repo has
// no cross-process advisory lock on Windows (fileutil degrades identically —
// flock_windows.go), so the guarantee is in-process only, matching the
// documented graceful-degradation posture (Hard Constraint 4). The locking
// logic itself is the portable, natively-tested helper in lock_inprocess.go;
// only this file-open wrapper is Windows-specific.
type installLock struct {
	mu *sync.Mutex
}

// acquireInstallLock takes the in-process lock for root/rel. Busy is refused
// immediately via errTargetBusy, never blocked before a session_id exists.
// A lock file is still created for layout parity; no OS-level lock exists.
func acquireInstallLock(root *os.Root, rel string) (*installLock, error) {
	f, err := root.OpenFile(rel, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", rel, err)
	}
	_ = f.Close()
	key := strings.ToLower(filepath.ToSlash(filepath.Join(root.Name(), rel)))
	mu, err := acquireInprocessLock(key)
	if err != nil {
		return nil, err
	}
	return &installLock{mu: mu}, nil
}

// release drops the in-process lock. Nil-safe for shared-scope targets.
func (l *installLock) release() {
	if l == nil || l.mu == nil {
		return
	}
	l.mu.Unlock()
	l.mu = nil
}
