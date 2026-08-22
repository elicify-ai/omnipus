// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
)

func TestVerifierSessionRegistry_RegisterLookupUnregister(t *testing.T) {
	r := NewVerifierSessionRegistry()

	if _, ok := r.Lookup("plan-1"); ok {
		t.Fatal("Lookup on an unregistered unit should report ok=false")
	}

	if err := r.Register("plan-1", "verifier-sess-1"); err != nil {
		t.Fatalf("first Register(plan-1) returned err: %v", err)
	}
	got, ok := r.Lookup("plan-1")
	if !ok || got != "verifier-sess-1" {
		t.Fatalf("Lookup(plan-1) = (%q, %v), want (verifier-sess-1, true)", got, ok)
	}

	// CAS (corr-MAJOR-3): a second Register with a DIFFERENT non-empty
	// session id against a LIVE entry must be REJECTED — the existing live
	// session is preserved, not clobbered (G-1 exactly-once). This is the
	// behavior change from the old blind-upsert.
	if err := r.Register("plan-1", "verifier-sess-2"); !errors.Is(err, ErrVerifierSessionHeld) {
		t.Fatalf("Register(plan-1, verifier-sess-2) over a live different session = err=%v, want ErrVerifierSessionHeld", err)
	}
	got, ok = r.Lookup("plan-1")
	if !ok || got != "verifier-sess-1" {
		t.Fatalf("after rejected CAS: Lookup(plan-1) = (%q, %v), want (verifier-sess-1, true) — entry must NOT be clobbered", got, ok)
	}

	// Idempotent re-registration of the SAME live session id is allowed (not
	// an error) — the placeholder-upgrade and dispatch-side re-confirm paths
	// rely on this.
	if err := r.Register("plan-1", "verifier-sess-1"); err != nil {
		t.Fatalf("idempotent Register(plan-1, verifier-sess-1) = err=%v, want nil", err)
	}

	r.Unregister("plan-1")
	if _, ok := r.Lookup("plan-1"); ok {
		t.Fatal("Lookup after Unregister should report ok=false")
	}

	// Unregistering an absent unit is a no-op, not an error/panic.
	r.Unregister("never-registered")
	r.Unregister("")
	if err := r.Register("", "should-be-ignored"); err != nil {
		t.Fatalf("Register with an empty unit must be a no-op (nil), got %v", err)
	}
	if _, ok := r.Lookup(""); ok {
		t.Fatal("Register with an empty unit must be a no-op")
	}
}

// TestVerifierSessionRegistry_EmptyPlaceholder pins the "claimed, pending"
// placeholder contract (plan_engine.go's beginPlanJudgeRound uses this
// exact pattern for its own round-in-flight liveness marker — see
// verifier_registry.go's package doc): Register with an empty session id
// makes Lookup report ok=true (something is claimed/in-flight for this
// unit) while SessionsFor correctly omits it (nothing live to cancel yet).
func TestVerifierSessionRegistry_EmptyPlaceholder(t *testing.T) {
	r := NewVerifierSessionRegistry()

	r.Register("plan-1", "")
	got, ok := r.Lookup("plan-1")
	if !ok || got != "" {
		t.Fatalf("Lookup(plan-1) after empty-session Register = (%q, %v), want (\"\", true)", got, ok)
	}
	if sessions := r.SessionsFor("plan-1"); len(sessions) != 0 {
		t.Fatalf("SessionsFor with only an empty-session placeholder = %v, want empty", sessions)
	}

	// Upsert the real session id — this is the "register BEFORE dispatch"
	// moment (FR-029/FR-037): the placeholder is replaced, not duplicated.
	r.Register("plan-1", "verifier-sess-1")
	if sessions := r.SessionsFor("plan-1"); len(sessions) != 1 || sessions[0] != "verifier-sess-1" {
		t.Fatalf("SessionsFor after upsert = %v, want [verifier-sess-1]", sessions)
	}
}

// TestVerifierSessionRegistry_SessionsFor_DedupesAndFiltersEmpty exercises
// the primitive PlanEngine.StopPlan/StopTask's fan-out uses to gather every
// relevant verifier session (plan-level + every member) in one call.
func TestVerifierSessionRegistry_SessionsFor_DedupesAndFiltersEmpty(t *testing.T) {
	r := NewVerifierSessionRegistry()
	r.Register("plan-1", "verifier-plan")
	r.Register("task-1", "verifier-member-a")
	r.Register("task-2", "verifier-member-a") // same verifier session shared across two units
	r.Register("task-3", "")                  // pending placeholder, must be filtered
	// "task-4" never registered at all.

	got := r.SessionsFor("plan-1", "task-1", "task-2", "task-3", "task-4")
	sort.Strings(got)
	want := []string{"verifier-member-a", "verifier-plan"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("SessionsFor = %v, want %v", got, want)
	}

	// Units with no registrations at all -> empty, not nil-panic.
	if got := r.SessionsFor(); len(got) != 0 {
		t.Fatalf("SessionsFor() with no units = %v, want empty", got)
	}
	if got := r.SessionsFor("nothing-registered"); len(got) != 0 {
		t.Fatalf("SessionsFor(nothing-registered) = %v, want empty", got)
	}
}

// TestVerifierSessionRegistry_RegisterBeforeDispatchOrdering pins the
// register-before-dispatch contract (FR-029/FR-037) at the registry's own
// concurrency-safety level: a goroutine that Registers a unit and ONLY THEN
// signals "dispatched" must always be observable (via SessionsFor) to a
// concurrent reader that waits on that same signal — i.e. the registry
// itself introduces no reordering/visibility hole a Stop fan-out could fall
// through. Runs many iterations (mirrors spec Test 10's >=100-iteration
// stress bar) with the race detector able to catch any missing
// synchronization.
func TestVerifierSessionRegistry_RegisterBeforeDispatchOrdering(t *testing.T) {
	const iterations = 200
	for i := 0; i < iterations; i++ {
		r := NewVerifierSessionRegistry()
		unit := fmt.Sprintf("unit-%d", i)
		sessionID := fmt.Sprintf("verifier-sess-%d", i)

		dispatched := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			// Simulate a verifier dispatcher: register BEFORE the turn
			// "starts" (the close(dispatched) below stands in for the
			// verifier's own turn beginning).
			r.Register(unit, sessionID)
			close(dispatched)
		}()

		var found []string
		go func() {
			defer wg.Done()
			<-dispatched
			found = r.SessionsFor(unit)
		}()

		wg.Wait()
		if len(found) != 1 || found[0] != sessionID {
			t.Fatalf("iteration %d: SessionsFor(%q) after dispatched-signal = %v, want [%s] — "+
				"a Stop landing right after dispatch would have missed this session (escape)",
				i, unit, found, sessionID)
		}
	}
}

// TestVerifierSessionRegistry_ConcurrentRegisterUnregister races many
// goroutines Register/Unregister/Lookup/SessionsFor-ing the same handful of
// units — must never panic or deadlock under -race.
func TestVerifierSessionRegistry_ConcurrentRegisterUnregister(t *testing.T) {
	r := NewVerifierSessionRegistry()
	units := []string{"plan-1", "task-1", "task-2", "task-3"}

	var wg sync.WaitGroup
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				unit := units[i%len(units)]
				sessionID := fmt.Sprintf("sess-%d-%d", g, i)
				r.Register(unit, sessionID)
				_, _ = r.Lookup(unit)
				_ = r.SessionsFor(units...)
				r.Unregister(unit)
			}
		}(g)
	}
	wg.Wait()
}
