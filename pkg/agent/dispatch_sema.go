// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"sync"
)

// DispatchSemaphore is a counting semaphore that gates concurrent task/subagent
// dispatch. It is distinct from AdmissionController (which gates inbound session
// workers) and lives on the task-executor dispatch path only.
//
// The capacity can be resized at runtime via Resize: the new capacity takes
// effect lazily as tokens are released — permits already in-flight are not
// recalled.
//
// All methods are safe for concurrent use.
type DispatchSemaphore struct {
	mu  sync.Mutex
	cap int
	cur int
	ch  chan struct{}
}

// newDispatchSemaphore returns a semaphore with the given capacity (>= 1).
func newDispatchSemaphore(capacity int) *DispatchSemaphore {
	if capacity < 1 {
		capacity = 1
	}
	ds := &DispatchSemaphore{
		cap: capacity,
		ch:  make(chan struct{}, 1),
	}
	return ds
}

// TryAcquire attempts to claim one slot without blocking.
// Returns true and a release function if a slot was available.
// Returns false when the semaphore is at capacity.
//
// The returned release is a FRESH, once-guarded closure per call (fix-wave
// finding #5, via newRelease) — it previously returned the shared ds.release
// method value directly, so a caller that (by bug or defensive double-defer)
// invoked its release twice would decrement ds.cur twice, freeing a slot that
// some OTHER, still-legitimately-running holder currently occupies. Mirrors
// RootDelegationAdmission.TryAdmit's identical once-guarded release
// (admission.go): a double-release must never under-count cur.
func (ds *DispatchSemaphore) TryAcquire() (bool, func()) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if ds.cur >= ds.cap {
		return false, nil
	}
	ds.cur++
	return true, ds.newRelease()
}

// newRelease returns a fresh, once-guarded release closure for one just-claimed
// slot (fix-wave finding #5). Shared by TryAcquire and WaitAndAcquire so every
// acquisition gets its OWN independent, idempotent release — never a shared
// method value two different holders' callers could conflate into a
// double-release that frees a slot out from under someone else. newRelease
// itself touches no shared state (it only closes over a fresh `released`
// flag), so it may be called with or without ds.mu held; the closure IT
// returns takes ds.mu itself, on invocation, before touching ds.cur.
func (ds *DispatchSemaphore) newRelease() func() {
	released := false
	return func() {
		ds.mu.Lock()
		defer ds.mu.Unlock()
		if released {
			return // idempotent: a double-release must never under-count cur
		}
		released = true
		if ds.cur > 0 {
			ds.cur--
		}
		// Non-blocking signal so a goroutine waiting in WaitAndAcquire can wake.
		select {
		case ds.ch <- struct{}{}:
		default:
		}
	}
}

// WaitAndAcquire blocks until a slot is available, then claims it.
// The caller MUST call the returned release function (typically via defer)
// when the dispatched work completes.
// ctx cancellation is not supported in this path — callers that need
// cancellation should use TryAcquire with a retry loop or a separate goroutine.
func (ds *DispatchSemaphore) WaitAndAcquire() func() {
	for {
		ds.mu.Lock()
		if ds.cur < ds.cap {
			ds.cur++
			release := ds.newRelease()
			ds.mu.Unlock()
			return release
		}
		ds.mu.Unlock()
		// Drain the notification channel (may already have a pending signal).
		<-ds.ch
	}
}

// Cap returns the current capacity.
func (ds *DispatchSemaphore) Cap() int {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.cap
}

// InFlight returns the number of currently held slots.
func (ds *DispatchSemaphore) InFlight() int {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.cur
}

// Resize sets a new capacity. Does not affect currently held slots.
// A reduced cap takes effect lazily as slots are released.
// A grown cap may immediately allow waiting callers to proceed.
func (ds *DispatchSemaphore) Resize(newCap int) {
	if newCap < 1 {
		newCap = 1
	}
	ds.mu.Lock()
	ds.cap = newCap
	ds.mu.Unlock()
	// Wake any goroutine blocked in WaitAndAcquire so it can re-check capacity.
	select {
	case ds.ch <- struct{}{}:
	default:
	}
}
