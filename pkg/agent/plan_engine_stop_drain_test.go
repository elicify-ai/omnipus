// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// plan_engine_stop_drain_test.go is the regression suite for PlanEngine.Stop's
// bounded drain of in-flight WAKE turns.
//
// The defect: Stop() opened with `if !pe.started { return }`, so an engine that
// was never Start()ed skipped the judgeWG/wakeWG drain entirely — while
// StopPlan (and failPlanLocked, and processPlan) dispatch REAL agent turns on
// their own goroutines regardless of whether the engine was ever started. Every
// plan-engine test harness in this package constructs an unstarted engine, so a
// wake turn dispatched by a test outlived its own test: a real turn writing a
// real session transcript into a directory the test framework had already
// removed ("…/state.json: no such file or directory" in CI).
//
// ⚠ Every assertion here is an OUTCOME, in the spirit of
// plan_wake_delivery_test.go's header. Specifically:
//
//   - "Stop drained the wake turn" means STOP DID NOT RETURN UNTIL THE TURN
//     COULD HAVE FINISHED — observed by releasing the turn from a separate
//     goroutine on a timer and checking, after Stop returns, that the release
//     had already happened. It does NOT mean "wakeWG.Wait was called".
//   - "no goroutine survived" is goleak against a snapshot taken immediately
//     before the wake is dispatched — not a counter, not a flag.
//   - "shutdown stays bounded" means STOP ACTUALLY RETURNED while the wake turn
//     was still wedged inside the provider with nothing to unblock it.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/elicify-ai/omnipus/pkg/plan"
)

// originLessRunningPlan creates a running plan with NO chat origin, owned by
// the harness's owner agent. Origin-less is the case that matters here: it is
// the leg wakeOwner dispatches DIRECTLY through dispatchPlanTurn (the only path
// that registers on wakeWG). A plan with a chat origin takes the notifier/bus
// leg instead, which is a different goroutine lineage entirely.
func originLessRunningPlan(t *testing.T, h *planWakeHarness, id string) *plan.Plan {
	t.Helper()
	p := runningPlanWithDoD(id, "", "")
	if err := h.plans.Create(p); err != nil {
		t.Fatalf("create plan %q: %v", id, err)
	}
	return p
}

// --- The regression: an unstarted engine still drains its wake turns --------

// TestPlanEngineStop_DrainsWakeTurnDispatchedByNeverStartedEngine is the defect
// itself, expressed as the property the old code violated.
//
// The sequence is exactly the one every harness in this package performs and
// the one the CI failure fingerprint pointed at: build an engine, never call
// Start, call StopPlan (which wakes the owner with a real agent turn), then
// tear down. The turn is held inside the owner's provider so it is
// unambiguously in flight when Stop is called, and it is released from a timer
// goroutine that records the fact. If Stop returns before that release, Stop
// did not drain — the goroutine is loose in the next test's world.
func TestPlanEngineStop_DrainsWakeTurnDispatchedByNeverStartedEngine(t *testing.T) {
	h := newPlanWakeHarness(t)

	// The engine is deliberately NOT started. This is the harness's real
	// shape, not an artificial one.
	h.pe.mu.Lock()
	started := h.pe.started
	h.pe.mu.Unlock()
	if started {
		t.Fatal("precondition: this test is about the never-started engine; the harness started it")
	}

	gate := make(chan struct{})
	h.owner.gate = gate
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	t.Cleanup(release) // never wedge the harness's own wakeWG drain on a failure path

	originLessRunningPlan(t, h, "p1")

	// Snapshot AFTER the harness (agent loop, bus pump, session stores) is warm
	// and BEFORE the wake is dispatched, so the only new long-lived goroutine
	// in scope is the one Stop is supposed to drain.
	//
	// The three bleve/scorch loops are excluded by name, not by IgnoreCurrent:
	// the memory subsystem opens its index LAZILY, inside the turn, so they
	// appear after the snapshot. They belong to a process-lifetime index the
	// memory store owns and closes — nothing PlanEngine.Stop has, or should
	// have, any authority over. (pkg/gateway/replay_test.go excludes the same
	// family for the same reason.) Excluding them by top function keeps the
	// check tight on the goroutine lineage under test: a leaked plan wake turn
	// is parked in the agent loop, never in a bleve loop.
	noLeaks := []goleak.Option{
		goleak.IgnoreCurrent(),
		goleak.IgnoreTopFunction("github.com/blevesearch/bleve/v2/index/scorch.(*Scorch).introducerLoop"),
		goleak.IgnoreTopFunction("github.com/blevesearch/bleve/v2/index/scorch.(*Scorch).persisterLoop"),
		goleak.IgnoreTopFunction("github.com/blevesearch/bleve/v2/index/scorch.(*Scorch).mergerLoop"),
	}

	if _, err := h.pe.StopPlan(context.Background(), "p1", "tester", "cli"); err != nil {
		t.Fatalf("StopPlan: %v", err)
	}

	// The wake turn is genuinely in flight — it is blocked INSIDE the owner
	// agent's own provider, which is positive evidence a turn ran for that
	// agent, not evidence that a dispatch was recorded.
	select {
	case <-h.owner.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("no owner wake turn ever reached the owner's provider; " +
			"this test cannot say anything about draining a turn that never ran")
	}

	// Hold the turn for a window far longer than an un-drained Stop takes to
	// return (microseconds), then release it and record that we did.
	const hold = 300 * time.Millisecond
	var released atomic.Bool
	go func() {
		time.Sleep(hold)
		released.Store(true)
		release()
	}()

	start := time.Now()
	h.pe.Stop()
	elapsed := time.Since(start)

	// THE assertion. Stop can only have returned after the provider call
	// returned, and the provider call can only return after `released` is set.
	if !released.Load() {
		t.Fatalf("Stop() returned in %v, while the wake turn it was supposed to drain was STILL "+
			"blocked inside the owner's provider — an unstarted engine skipped the wakeWG drain, "+
			"so that turn (a real agent turn writing a real session transcript) outlives this test",
			elapsed)
	}
	if elapsed < hold {
		t.Fatalf("Stop() returned after %v but the wake turn was held for %v — Stop did not wait for it",
			elapsed, hold)
	}

	// And nothing is left running. goleak retries with backoff, so a goroutine
	// that is merely mid-exit is absorbed; a goroutine that is still parked is
	// reported with its stack.
	goleak.VerifyNone(t, noLeaks...)
}

// --- The bound still bounds -------------------------------------------------

// TestPlanEngineStop_BoundsDrainOnStartedEngineWithWedgedWakeTurn covers the
// half of Stop that already worked and must keep working: a STARTED engine
// whose in-flight wake turn never finishes still shuts down, on the budget,
// rather than blocking teardown forever.
//
// The turn here is wedged with nothing to unblock it (the gate is closed only
// in cleanup, after the assertions), so "Stop returned" is only possible via
// the timeout limb. stopDrainTimeout is shortened purely so the test does not
// burn the 30 s production budget; the limb under test is the same one.
func TestPlanEngineStop_BoundsDrainOnStartedEngineWithWedgedWakeTurn(t *testing.T) {
	h := newPlanWakeHarness(t)

	const budget = 250 * time.Millisecond
	h.pe.stopDrainTimeout = budget
	// Start requires a dispatcher; the harness leaves it nil because its own
	// tests never dispatch a member task. Nothing in this test dispatches one
	// either — this only satisfies Start's wiring check.
	h.pe.dispatcher = &fakePlanDispatcher{store: h.tasks}
	if err := h.pe.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	gate := make(chan struct{})
	h.owner.gate = gate
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(gate) }) })

	// Created AFTER Start so boot reconciliation does not adjudicate it.
	originLessRunningPlan(t, h, "p1")

	if _, err := h.pe.StopPlan(context.Background(), "p1", "tester", "cli"); err != nil {
		t.Fatalf("StopPlan: %v", err)
	}
	select {
	case <-h.owner.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("no owner wake turn ever reached the owner's provider")
	}

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		h.pe.Stop()
		done <- time.Since(start)
	}()

	select {
	case elapsed := <-done:
		if elapsed < budget {
			t.Fatalf("Stop() returned in %v, faster than its own %v drain budget, with a wake turn "+
				"still in flight — the drain is not being entered on the started path", elapsed, budget)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Stop() never returned with a wedged wake turn in flight — shutdown is hostage to a " +
			"stuck turn; the bounded drain is gone")
	}

	// The turn really was still wedged the whole time: it never returned from
	// the provider, and it was not cancelled out from under itself either.
	if h.owner.cancelledFromInsideTheTurn() {
		t.Fatal("the wake turn ended by context cancellation, so Stop was not actually bounded by " +
			"the drain timeout — this test proved nothing")
	}
}

// --- What the `started` guard legitimately protects --------------------------

// TestPlanEngineStop_IsSafeUnstartedAndRepeated pins the reason the guard could
// not simply be deleted. `stopCh` is nil until Start creates it, and it is
// closed exactly once; `subID` is never zeroed on stop. A blanket removal of
// the early return panics on close(nil) for case (1) and double-closes the same
// channel for case (2).
func TestPlanEngineStop_IsSafeUnstartedAndRepeated(t *testing.T) {
	t.Run("never started, nothing dispatched", func(t *testing.T) {
		h := newTestPlanEngine(t)
		done := make(chan struct{})
		go func() { defer close(done); h.pe.Stop() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Stop() on an engine that was never started and dispatched nothing did not return")
		}
	})

	t.Run("stopped twice after a start", func(t *testing.T) {
		h := newTestPlanEngine(t)
		h.pe.stopDrainTimeout = time.Second
		if err := h.pe.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		h.pe.Stop()
		done := make(chan struct{})
		go func() { defer close(done); h.pe.Stop() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("a second Stop() did not return")
		}
	})

	t.Run("start again after a stop", func(t *testing.T) {
		h := newTestPlanEngine(t)
		h.pe.stopDrainTimeout = time.Second
		if err := h.pe.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		h.pe.Stop()
		// Stop must not poison the engine: the drain is not a terminal state
		// transition, and a restart has to get a fresh stop channel.
		if err := h.pe.Start(context.Background()); err != nil {
			t.Fatalf("Start after Stop: %v", err)
		}
		h.pe.Stop()
	})
}
