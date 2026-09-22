// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 landing order I-3/FR-A-013 — registerTurnIfAbsent's compare-and-set
// guarantee, and I-6's generation-aware cancel refusal.

package agent

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestRegisterTurnIfAbsent_OneWins is TDD plan test 16: 100 concurrent
// registrations for ONE session key must resolve to exactly one admitted
// turn (FR-A-013).
func TestRegisterTurnIfAbsent_OneWins(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	const n = 100
	const key = "sess-race-1"
	var admitted atomic.Int64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			ts := &turnState{sessionKey: key, agentID: "worker"}
			if al.registerTurnIfAbsent(ts) {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := admitted.Load(); got != 1 {
		t.Fatalf("admitted = %d concurrent registrations for one key, want exactly 1", got)
	}
}

// TestRegisterTurnIfAbsent_DifferentKeysBothAdmitted proves the guard is
// per-key, not a global single-turn lock.
func TestRegisterTurnIfAbsent_DifferentKeysBothAdmitted(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	ts1 := &turnState{sessionKey: "sess-a", agentID: "worker"}
	ts2 := &turnState{sessionKey: "sess-b", agentID: "worker"}
	if !al.registerTurnIfAbsent(ts1) {
		t.Fatal("registerTurnIfAbsent(sess-a) = false, want true (first registration for this key)")
	}
	if !al.registerTurnIfAbsent(ts2) {
		t.Fatal("registerTurnIfAbsent(sess-b) = false, want true (different key, must not be blocked by sess-a)")
	}
}

// TestRegisterTurnIfAbsent_SecondCallForSameKeyRefused proves a second,
// sequential registration for an already-registered key is refused (not
// just the concurrent race case).
func TestRegisterTurnIfAbsent_SecondCallForSameKeyRefused(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	first := &turnState{sessionKey: "sess-c", agentID: "worker"}
	second := &turnState{sessionKey: "sess-c", agentID: "worker"}
	if !al.registerTurnIfAbsent(first) {
		t.Fatal("first registerTurnIfAbsent = false, want true")
	}
	if al.registerTurnIfAbsent(second) {
		t.Fatal("second registerTurnIfAbsent for the same still-registered key = true, want false")
	}
	// The FIRST turnState must still be the one reachable under the key —
	// registerTurnIfAbsent must never silently replace an already-admitted
	// turn.
	if got := al.getActiveTurnState("sess-c"); got != first {
		t.Fatalf("getActiveTurnState(sess-c) = %p, want the first-admitted turnState %p", got, first)
	}
}

// TestRequestCancelForGeneration_MatchingGenerationFires proves the
// generation-aware cancel primitive fires a hard abort when the registered
// turn's generation matches the caller's.
func TestRequestCancelForGeneration_MatchingGenerationFires(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	fired := false
	ts := &turnState{
		sessionKey: "sess-gen-1",
		agentID:    "worker",
		generation: 2,
		turnCancel: func() { fired = true },
	}
	if !al.registerTurnIfAbsent(ts) {
		t.Fatal("registerTurnIfAbsent = false, want true")
	}

	ok, reason := al.requestCancelForGeneration("sess-gen-1", 2)
	if !ok {
		t.Fatalf("requestCancelForGeneration(matching gen) ok=false, reason=%q, want ok=true", reason)
	}
	if !fired {
		t.Fatal("requestCancelForGeneration(matching gen) did not fire the turn's cancel func")
	}
}

// TestRequestCancelForGeneration_StaleGenerationRefused proves I-6's core
// safety property: a cancel carrying an OLDER generation than the
// registered turn's current one must never fire — the turn a concurrent
// revival already moved past is not the turn this cancel meant to stop.
func TestRequestCancelForGeneration_StaleGenerationRefused(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	fired := false
	ts := &turnState{
		sessionKey: "sess-gen-2",
		agentID:    "worker",
		generation: 5, // revived past the caller's stale generation
		turnCancel: func() { fired = true },
	}
	if !al.registerTurnIfAbsent(ts) {
		t.Fatal("registerTurnIfAbsent = false, want true")
	}

	ok, reason := al.requestCancelForGeneration("sess-gen-2", 4)
	if ok {
		t.Fatal("requestCancelForGeneration(stale gen 4 vs registered gen 5) ok=true, want false")
	}
	if reason == "" {
		t.Fatal("requestCancelForGeneration(stale gen) returned no reason")
	}
	if fired {
		t.Fatal("requestCancelForGeneration(stale gen) fired the turn's cancel func — must never fire on a generation mismatch")
	}
}

// TestRequestCancelForGeneration_NoActiveTurnRefused proves the primitive
// refuses cleanly (never panics) when nothing is registered for the key —
// the caller (I-6 CancelSubtree) reports this as SkippedTerminal/not-running.
func TestRequestCancelForGeneration_NoActiveTurnRefused(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	ok, reason := al.requestCancelForGeneration("sess-gen-none", 1)
	if ok {
		t.Fatal("requestCancelForGeneration on an unregistered key = ok=true, want false")
	}
	if reason == "" {
		t.Fatal("requestCancelForGeneration on an unregistered key returned no reason")
	}
}
