// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"go.uber.org/goleak"
)

// Issue #1725 and its RED brief require a prompt, cancelling Stop, well inside
// the 20-second reload budget. Two seconds is the brief's suggested bound;
// it is not copied from Stop's existing drain timeout (which is NOT overridden).
const planStop1725Bound = 2 * time.Second

// Setup/cleanup watchdogs are not product-response oracles. Channel handshakes,
// rather than sleeps, establish that the external call is actually in flight.
const planStop1725Watchdog = 10 * time.Second

type planStop1725Entry struct {
	ctx      context.Context
	entryErr error
	planID   string
}

// Only the external model/judge call is faked. The independent release is used
// after assertions on RED cleanup, never to make the Stop/cancellation pass.
type planStop1725Blocker struct {
	entered     chan planStop1725Entry
	finished    chan struct{}
	release     chan struct{}
	enterOnce   sync.Once
	finishOnce  sync.Once
	releaseOnce sync.Once
}

func newPlanStop1725Blocker() *planStop1725Blocker {
	return &planStop1725Blocker{
		entered:  make(chan planStop1725Entry, 1),
		finished: make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (b *planStop1725Blocker) wait(ctx context.Context, planID string) error {
	b.enterOnce.Do(func() {
		b.entered <- planStop1725Entry{ctx: ctx, entryErr: ctx.Err(), planID: planID}
	})
	defer b.finishOnce.Do(func() { close(b.finished) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.release:
		return nil
	}
}

func (b *planStop1725Blocker) unblock() {
	b.releaseOnce.Do(func() { close(b.release) })
}

func (b *planStop1725Blocker) awaitEntry(t *testing.T) planStop1725Entry {
	t.Helper()
	select {
	case entry := <-b.entered:
		if entry.entryErr != nil {
			t.Fatalf("setup: external call entered with context error %v, want nil", entry.entryErr)
		}
		return entry
	case <-time.After(planStop1725Watchdog):
		t.Fatal("setup: plan work never reached the blocking external call")
		return planStop1725Entry{}
	}
}

type planStop1725Provider struct {
	blocker *planStop1725Blocker
}

func (p *planStop1725Provider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	if err := p.blocker.wait(ctx, ""); err != nil {
		return nil, err
	}
	return &providers.LLMResponse{Content: "wake finished"}, nil
}

func (*planStop1725Provider) GetDefaultModel() string { return "test-model" }

type planStop1725Judge struct {
	blocker *planStop1725Blocker
}

func (j *planStop1725Judge) JudgeCriteria(ctx context.Context, in JudgeCriteriaInput) JudgeCriteriaResult {
	if err := j.blocker.wait(ctx, in.PlanID); err != nil {
		return JudgeCriteriaResult{Unavailable: true, Reason: err.Error()}
	}
	return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{Met: true}}
}

func waitPlanStop1725Signal(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(planStop1725Watchdog):
		t.Fatalf("%s did not finish within the %v cleanup/setup watchdog", what, planStop1725Watchdog)
	}
}

// Join complete goroutines, not their last store writes. This is also used on
// failure paths, after releasing the external fake, before fixture teardown.
func joinPlanStop1725Work(pe *PlanEngine) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		pe.stoppedWG.Wait()
		pe.judgeWG.Wait()
		pe.wakeWG.Wait()
		close(done)
	}()
	return done
}

func cleanupPlanStop1725(t *testing.T, pe *PlanEngine, blocker *planStop1725Blocker) {
	t.Helper()
	t.Cleanup(func() {
		blocker.unblock()
		done := make(chan struct{})
		go func() { pe.Stop(); close(done) }()
		waitPlanStop1725Signal(t, done, "cleanup Stop")
		waitPlanStop1725Signal(t, joinPlanStop1725Work(pe), "engine goroutine join")
	})
}

// A short drain timeout alone cannot satisfy this test: return, actual context
// cancellation, external-call exit, and the complete engine-work join must all
// happen within the SAME budget. Only cleanup may release the blocking fake.
func assertPlanStop1725(t *testing.T, pe *PlanEngine, ctx context.Context, blocker *planStop1725Blocker) {
	t.Helper()
	deadline := time.NewTimer(planStop1725Bound)
	defer deadline.Stop()
	stopDone := make(chan struct{})
	go func() { pe.Stop(); close(stopDone) }()
	joined := joinPlanStop1725Work(pe)
	t.Cleanup(func() {
		if blocker != nil {
			blocker.unblock()
		}
		waitPlanStop1725Signal(t, stopDone, "original Stop goroutine")
		waitPlanStop1725Signal(t, joined, "original engine-work join")
	})

	var cancelled, finished <-chan struct{}
	if ctx != nil {
		cancelled = ctx.Done()
		finished = blocker.finished
	}
	stopPending, joinPending := (<-chan struct{})(stopDone), joined
	cancelPending, finishPending := ctx != nil, ctx != nil
	for stopPending != nil || joinPending != nil || cancelPending || finishPending {
		select {
		case <-stopPending:
			stopPending = nil
		case <-joinPending:
			joinPending = nil
		case <-cancelled:
			cancelled, cancelPending = nil, false
		case <-finished:
			finished, finishPending = nil, false
		case <-deadline.C:
			if stopPending != nil {
				t.Errorf("Stop did not return within %v while plan work was in flight (#1725 reload stall)", planStop1725Bound)
			}
			if ctx != nil && ctx.Err() != context.Canceled {
				t.Errorf("in-flight context error = %v, want context.Canceled within %v of Stop", ctx.Err(), planStop1725Bound)
			}
			if finishPending {
				t.Errorf("context-cooperative external call did not exit within %v of Stop", planStop1725Bound)
			}
			if joinPending != nil {
				t.Errorf("engine-owned goroutines did not finish within %v of Stop", planStop1725Bound)
			}
			t.FailNow()
		}
	}
	if ctx != nil && ctx.Err() != context.Canceled {
		t.Fatalf("in-flight context error = %v, want exactly context.Canceled", ctx.Err())
	}
	t.Logf("Stop returned and engine work joined within %v; had in-flight context: %t", planStop1725Bound, ctx != nil)
}

func TestPlanEngineStop1725_CancelsInFlightWakeTurn(t *testing.T) {
	for _, started := range []bool{true, false} {
		name := "never_started"
		if started {
			name = "started"
		}
		t.Run(name, func(t *testing.T) {
			h := newPlanWakeHarness(t)
			blocker := newPlanStop1725Blocker()
			owner, ok := h.al.GetRegistry().GetAgent(testPlanOwnerAgentID)
			if !ok {
				t.Fatal("setup: plan owner is not registered")
			}
			owner.Provider = &planStop1725Provider{blocker: blocker}
			h.pe.dispatcher = &fakePlanDispatcher{store: h.tasks}
			h.pe.tickInterval = time.Hour // This test explicitly dispatches its one wake.

			// As in the established wake-drain suite, ignore only the lazy,
			// process-owned Bleve index loops, never a plan/turn/Stop goroutine.
			noLeaks := []goleak.Option{
				goleak.IgnoreCurrent(),
				goleak.IgnoreTopFunction("github.com/blevesearch/bleve/v2/index/scorch.(*Scorch).introducerLoop"),
				goleak.IgnoreTopFunction("github.com/blevesearch/bleve/v2/index/scorch.(*Scorch).persisterLoop"),
				goleak.IgnoreTopFunction("github.com/blevesearch/bleve/v2/index/scorch.(*Scorch).mergerLoop"),
			}
			t.Cleanup(func() { goleak.VerifyNone(t, noLeaks...) })
			cleanupPlanStop1725(t, h.pe, blocker)
			if started {
				if err := h.pe.Start(context.Background()); err != nil {
					t.Fatalf("setup Start: %v", err)
				}
			}
			p := originLessRunningPlan(t, h, "stop-wake-1725")
			h.pe.planDecisionMu.Lock()
			h.pe.wakeOwner(p, "finish the plan outcome", "plan_done")
			h.pe.planDecisionMu.Unlock()
			entry := blocker.awaitEntry(t)
			assertPlanStop1725(t, h.pe, entry.ctx, blocker)
		})
	}
}

func TestPlanEngineStop1725_CancelsInFlightJudgeRound(t *testing.T) {
	for _, started := range []bool{true, false} {
		name := "never_started"
		if started {
			name = "started"
		}
		t.Run(name, func(t *testing.T) {
			h := newTestPlanEngine(t)
			blocker := newPlanStop1725Blocker()
			h.pe.judge = &planStop1725Judge{blocker: blocker}
			h.pe.tickInterval = time.Hour // No automatic tick can add a second round.
			noLeaks := goleak.IgnoreCurrent()
			t.Cleanup(func() { goleak.VerifyNone(t, noLeaks) })
			cleanupPlanStop1725(t, h.pe, blocker)
			if started {
				if err := h.pe.Start(context.Background()); err != nil {
					t.Fatalf("setup Start: %v", err)
				}
			}
			p := mustCreateRunningPlan(t, h.plans, "stop-judge-1725", "owner")
			mustCreateTask(t, h.tasks, &task.Task{
				Title: "completed member", WorkspaceID: p.WorkspaceID, PlanID: p.ID, Status: task.StatusDone,
			})
			h.pe.processPlan(context.Background(), p.ID)
			entry := blocker.awaitEntry(t)
			if entry.planID != p.ID {
				t.Fatalf("setup: judge received plan %q, want %q", entry.planID, p.ID)
			}
			assertPlanStop1725(t, h.pe, entry.ctx, blocker)
		})
	}
}

func TestPlanEngineStop1725_IdleRepeatedAndRestart(t *testing.T) {
	h := newTestPlanEngine(t)
	blocker := newPlanStop1725Blocker()
	blocker.unblock() // This control has no blocked external work.
	h.pe.judge = &planStop1725Judge{blocker: blocker}
	h.pe.tickInterval = time.Hour
	noLeaks := goleak.IgnoreCurrent()
	t.Cleanup(func() { goleak.VerifyNone(t, noLeaks) })
	cleanupPlanStop1725(t, h.pe, blocker)

	assertPlanStop1725(t, h.pe, nil, nil) // Safe without any Start.
	assertPlanStop1725(t, h.pe, nil, nil) // Idempotent before Start.
	if err := h.pe.Start(context.Background()); err != nil {
		t.Fatalf("initial Start: %v", err)
	}
	assertPlanStop1725(t, h.pe, nil, nil) // Idle, started engine.
	assertPlanStop1725(t, h.pe, nil, nil) // Idempotent after a started Stop.

	// New work is added WHILE stopped. A real restart must discover and run it
	// via Start's boot reconciliation, not a direct test call to processPlan.
	p := mustCreateRunningPlan(t, h.plans, "restart-1725", "owner")
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "completed member", WorkspaceID: p.WorkspaceID, PlanID: p.ID, Status: task.StatusDone,
	})
	if err := h.pe.Start(context.Background()); err != nil {
		t.Fatalf("Start after Stop: %v", err)
	}
	entry := blocker.awaitEntry(t)
	if entry.planID != p.ID {
		t.Fatalf("restart judge received plan %q, want fresh plan %q", entry.planID, p.ID)
	}
	waitPlanStop1725Signal(t, blocker.finished, "fresh judge call")
	// Start has returned, so its wait-group Adds precede this complete-round
	// join. A provider returning is not itself proof its owning round ended.
	roundDone := make(chan struct{})
	go func() { h.pe.judgeWG.Wait(); close(roundDone) }()
	waitPlanStop1725Signal(t, roundDone, "fresh judge round")
	assertPlanStop1725(t, h.pe, nil, nil)
	assertPlanStop1725(t, h.pe, nil, nil)
}
