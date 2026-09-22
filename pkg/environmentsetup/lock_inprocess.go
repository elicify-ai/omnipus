// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package environmentsetup

import (
	"fmt"
	"sync"
)

// In-process install locking for platforms without a cross-process advisory
// lock (the Windows build — fileutil degrades identically, see
// flock_windows.go). The logic lives here WITHOUT a build tag so the exact
// logic the Windows file calls is natively testable on every platform; only
// the file-opening wrapper is Windows-specific. The in-process-only
// limitation itself is platform-specific and documented in lock_unix.go /
// lock_windows.go. The map grows one entry per distinct workspace root and is
// never pruned — bounded by workspace count, no new registry.
var (
	installLockMu sync.Mutex
	installLocks  = map[string]*sync.Mutex{}
)

// acquireInprocessLock returns the per-key mutex LOCKED, refusing a second
// holder immediately with errTargetBusy. installLockMu guards the map and is
// released on every exit path; it is never held across anything that can
// block (TryLock never blocks), so one target's acquisition can never stall
// an acquisition for an independent target.
func acquireInprocessLock(key string) (*sync.Mutex, error) {
	installLockMu.Lock()
	defer installLockMu.Unlock()
	mu := installLocks[key]
	if mu == nil {
		mu = &sync.Mutex{}
		installLocks[key] = mu
	}
	if !mu.TryLock() {
		return nil, fmt.Errorf("%w: %s", errTargetBusy, key)
	}
	return mu, nil
}
