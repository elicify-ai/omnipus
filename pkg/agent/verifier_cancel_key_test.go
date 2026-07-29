// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_cancel_key_test.go is the real-seam regression test for the
// 7-reviewer gate BLOCKER (ADR-052 FR-037/G1/G8): runVerifierAdjudication
// (verifier_adjudication.go) must register the verifier's own
// TranscriptSessionID (chatID) in the verifier-session registry, NOT
// sessionKey (the activeTurnStates map storage key) — RequestCancelForSession
// / GetActiveTurnHookForSession (turn.go) match a turn on
// ts.transcriptSessionID, never on the map storage key, so registering
// sessionKey left every Stop/`/goal clear` cancel path structurally unable
// to interrupt an in-flight verifier LLM call.
//
// Unlike the existing plan_stop tests (plan_stop_test.go), which drive Stop
// through a fakeSessionCanceller that just RECORDS whatever session id it
// was called with — never attempting a real match — this test never fakes
// the cancel path: it drives a REAL verifier turn via AgentLoop.JudgeCriteria,
// discovers the session id the REAL VerifierSessionRegistry actually stored
// (via SessionsFor, the exact primitive plan_engine.go's Stop fan-out and
// goal_loop.go's `/goal clear` use), and feeds THAT value into the REAL
// RequestCancelForSession — then asserts the in-flight provider call's own
// ctx was actually canceled. A fakeSessionCanceller structurally cannot
// catch a key-mismatch regression; this test cannot pass unless the
// registered key really is the one RequestCancelForSession/
// GetActiveTurnHookForSession match turns on.
package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestRunVerifierAdjudication_RealCancelReachesInFlightVerifierTurn is the
// end-to-end proof: with the fix in place, RequestCancelForSession(the id
// the registry actually stored) both (a) fires (claims an active turn) and
// (b) interrupts the in-flight provider call — proving the registered id
// really is ts.transcriptSessionID. Manually verified to FAIL both
// assertions when runVerifierAdjudication's registry.Register call is
// reverted to register(unitID, sessionKey) (the pre-fix drift) instead of
// register(unitID, chatID): RequestCancelForSession(sessionKey) then matches
// no turnState (fired=false) and cancelSeen never fires.
func TestRunVerifierAdjudication_RealCancelReachesInFlightVerifierTurn(t *testing.T) {
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })
	// Never really sleep on a D7 backoff — this test only cares about the
	// FIRST dispatch attempt; a same-process retry (should a second
	// dispatch's own ctx race ahead of the outer-ctx cancel below) must
	// never stall the test for a real 60s+.
	judgeSleepFn = func(ctx context.Context, _ time.Duration) error {
		return ctx.Err()
	}

	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	started := make(chan struct{})
	var startedOnce sync.Once
	cancelSeen := make(chan struct{})
	fake := &fakeJudgeProvider{}
	fake.chatFn = func(callNum int) (*providers.LLMResponse, error) {
		if callNum != 1 {
			// Any retry attempt resolves immediately — this test asserts the
			// FIRST dispatch's ctx was interrupted; a retry must never hang
			// the test regardless of how the outer-ctx-cancel race below
			// lands.
			return &providers.LLMResponse{
				Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
			}, nil
		}
		startedOnce.Do(func() { close(started) })
		callCtx := fake.capturedCtx()
		<-callCtx.Done()
		close(cancelSeen)
		return nil, callCtx.Err()
	}
	judgeInst.Provider = fake

	outerCtx, cancelOuter := context.WithCancel(context.Background())
	t.Cleanup(cancelOuter)

	const taskID = "t-cancel-key-drift"
	resultCh := make(chan JudgeCriteriaResult, 1)
	go func() {
		resultCh <- al.JudgeCriteria(outerCtx, JudgeCriteriaInput{
			Scope:           task.VerdictScopeTask,
			TaskID:          taskID,
			AssigneeAgentID: "native-agent",
			Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "x")},
			Attempt:         1,
			ClaimText:       "done",
		})
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("verifier turn never dispatched (provider.Chat never called)")
	}

	// Look up the session id the REAL registry actually stored — the same
	// primitive plan_engine.go's Stop fan-out and goal_loop.go's `/goal
	// clear` use (SessionsFor) — rather than assuming any particular value
	// or byte-for-byte construction. currentVerifierSessionRegistry()'s
	// static type is the narrower VerifierSessionPublisher (Register/
	// Unregister only, verifier_adjudication.go's own publish-side seam);
	// the concrete instance installed by default (NewVerifierSessionRegistry,
	// package init) also implements the richer VerifierSessionRegistry
	// (Lookup/SessionsFor) that plan_engine.go's Stop fan-out consumes via
	// PlanEngine.VerifierRegistry() — asserted to here since this harness
	// has no PlanEngine to source that accessor from.
	seam := currentVerifierSessionRegistry()
	registry, ok := seam.(VerifierSessionRegistry)
	if !ok {
		t.Fatalf("verifier-session registry (%T) does not implement VerifierSessionRegistry (no SessionsFor)", seam)
	}
	unit := verifierUnitForTask(taskID)
	sessions := registry.SessionsFor(unit)
	if len(sessions) != 1 {
		t.Fatalf("VerifierSessionRegistry.SessionsFor(%q) = %v, want exactly one registered session", unit, sessions)
	}
	registeredSessionID := sessions[0]

	fired, _, err := al.RequestCancelForSession(context.Background(), registeredSessionID, "tester", "test")
	if err != nil {
		t.Fatalf("RequestCancelForSession: %v", err)
	}
	if !fired {
		t.Fatalf("RequestCancelForSession(%q) did not fire — the registered session id does not match any "+
			"active turn's transcriptSessionID (cancel-key drift regression, FR-037/G1/G8)", registeredSessionID)
	}

	select {
	case <-cancelSeen:
		// The in-flight provider call's own ctx really was canceled — proof
		// the cancel signal reached the LIVE verifier turn, not merely that
		// RequestCancelForSession returned fired=true against unrelated
		// bookkeeping.
	case <-time.After(5 * time.Second):
		t.Fatal("RequestCancelForSession fired, but the in-flight verifier turn's provider call was never " +
			"interrupted — cancel-key drift regression (FR-037/G1/G8)")
	}

	// Let the outer ctx go too, so a same-process retry (if the scheduler
	// let attempt 2 slip in ahead of this) can never real-sleep on backoff.
	cancelOuter()

	select {
	case <-resultCh:
		// Either a canceled/Unavailable outcome or a fast-resolving retry
		// verdict — both are acceptable here; the assertions above already
		// proved the regression this test exists to catch.
	case <-time.After(10 * time.Second):
		t.Fatal("JudgeCriteria never returned after cancellation")
	}
}
