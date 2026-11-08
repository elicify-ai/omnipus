// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

// write_order.go — the per-task coordination lock (tool-parity spec r17
// §7.2 TK-03 r7/r8/r9, TK-30). Every writer that must order a task write with
// its post-write reconciliation (the paired goal's prompt, the scheduler
// registration) holds this lock across the whole sequence, so writers of one
// task are strictly ordered and a stale snapshot can never overwrite newer
// reconciled state.
//
// It is NOT the store's own file lock (TaskFileLock): Store.Update takes that
// lock itself, so the coordination lock wraps it from outside and is never
// taken from inside a store call. Lock order: coordination lock → store file
// lock → scheduler-internal locks, never the reverse. The lock is never held
// while acquiring another task's coordination lock.
//
// One mutex per task id, created on first use and released when its last
// holder or waiter leaves, so the map stays bounded by in-flight writers and
// two different tasks never contend.

import "sync"

type writeOrderEntry struct {
	mu   sync.Mutex
	refs int
}

var writeOrder = struct {
	mu      sync.Mutex
	entries map[string]*writeOrderEntry
}{entries: make(map[string]*writeOrderEntry)}

// LockTaskWriteOrder acquires the coordination lock of task id and returns
// its release function. The release must be called exactly once.
func LockTaskWriteOrder(id string) (unlock func()) {
	writeOrder.mu.Lock()
	e := writeOrder.entries[id]
	if e == nil {
		e = &writeOrderEntry{}
		writeOrder.entries[id] = e
	}
	e.refs++
	writeOrder.mu.Unlock()

	e.mu.Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Unlock()
			writeOrder.mu.Lock()
			e.refs--
			if e.refs == 0 {
				delete(writeOrder.entries, id)
			}
			writeOrder.mu.Unlock()
		})
	}
}
