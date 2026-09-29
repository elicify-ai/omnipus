// Omnipus — exact-key membership lock, independent of striped note locks.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"sync"
	"time"
)

// A reference covers both holders and waiters. Remove a lock only after its
// last reference leaves, so two callers can never acquire distinct mutexes
// for the same collection while another caller is waiting.
type viewMembershipMutex struct {
	mu   sync.Mutex
	refs int
}

//nolint:gochecknoglobals // process-wide, shared by importer, gateway and tools.
var viewMembershipLocks = struct {
	sync.Mutex
	entries map[string]*viewMembershipMutex
}{entries: make(map[string]*viewMembershipMutex)}

func borrowViewMembershipMutex(root string) *viewMembershipMutex {
	viewMembershipLocks.Lock()
	defer viewMembershipLocks.Unlock()
	entry := viewMembershipLocks.entries[root]
	if entry == nil {
		entry = &viewMembershipMutex{}
		viewMembershipLocks.entries[root] = entry
	}
	entry.refs++
	return entry
}

func returnViewMembershipMutex(root string, entry *viewMembershipMutex) {
	viewMembershipLocks.Lock()
	defer viewMembershipLocks.Unlock()
	entry.refs--
	if entry.refs == 0 {
		delete(viewMembershipLocks.entries, root)
	}
}

// withExactViewMembershipLock keeps the record's in-process mutex separate
// from noteWriteLocks, which can hash a view and the membership key into one
// non-reentrant stripe. Its cross-process lock is the existing per-record
// advisory file lock; both acquisitions share the same bounded deadline.
func withExactViewMembershipLock(root, lockDir string, fn func() error) error {
	bound := DefaultLockBound
	deadline := time.Now().Add(bound)
	entry := borrowViewMembershipMutex(root)
	defer returnViewMembershipMutex(root, entry)
	if !acquireMutexByDeadline(&entry.mu, deadline) {
		return &LockTimeoutError{Path: viewMembershipLockKey, Bound: bound}
	}
	defer entry.mu.Unlock()

	lockPath, err := noteLockPathFor(lockDir, viewMembershipLockKey)
	if err != nil {
		return err
	}
	return withFileLockByDeadline(lockPath, viewMembershipLockKey, bound, deadline, fn)
}
