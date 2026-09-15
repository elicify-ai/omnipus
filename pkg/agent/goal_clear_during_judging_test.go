// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_clear_during_judging_test.go pins UAT scenario E-14 ("no Stop while a
// goal is being judged") at the backend: when `/goal clear` lands while the
// Judge is adjudicating a claim in the BACKGROUND (JUDGE-FR-098's deferred
// dispatch — the worker's turn has already ended), the adjudication must stop
// there. judge-active-reviewer-spec.md, edge case E-34: "`/goal clear` or a
// Stop lands while a background adjudication is in flight → The verifier
// registry's session handle is cancelled exactly as today; the adjudication
// is discarded whole and produces no verdict (FR-103)". FR-082: "A cancelled
// adjudication is discarded whole."
//
// Two windows are covered, because a real adjudication spends time in both:
//
//   - mid-turn: the Judge's turn is waiting on its provider;
//   - between attempts: the Judge's previous turn failed transiently and
//     runVerifierAdjudication is sleeping on judgeRetryBackoff (60/120/300 s).
//     In UAT E-14 run 2 this was 180 s of an 8-minute `judging` pill, with no
//     turn registered at all for a cancel to claim.
//
// Every oracle is the spec's observable outcome: no further Judge turn runs
// after the clear, the goal record stays `cleared` with no verdict on it, no
// verdict is written into the user's transcript, and no goal pill is painted
// over the `cleared` one. Shares goalClaimAwaitingJudge with
// goal_judge_retry_visibility_test.go.
package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func (g *goalClaimAwaitingJudge) clearGoal(t *testing.T) {
	t.Helper()
	opts := g.opts
	matched, handled, reply := g.al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal clear", UserInitiated: true}, g.agent, &opts)
	if !matched || !handled || !strings.Contains(reply, "cleared") {
		t.Fatalf("/goal clear: matched=%v handled=%v reply=%q", matched, handled, reply)
	}
}

// assertAdjudicationDiscardedWhole checks E-34's observable outcome once the
// deferred dispatch has returned.
func (g *goalClaimAwaitingJudge) assertAdjudicationDiscardedWhole(t *testing.T, judge *fakeJudgeProvider) {
	t.Helper()

	if n := judge.callCount(); n != 1 {
		t.Errorf("the Judge's provider was called %d times; want 1 — no Judge turn may run after `/goal clear` "+
			"(E-34/FR-082: the adjudication is discarded whole)", n)
	}

	rec := mustGoalRecord(t, g.goalID)
	if rec.State != generated.GoalStateCleared {
		t.Errorf("goal record state = %q; want %q — the user's clear must stand", rec.State, generated.GoalStateCleared)
	}
	if rec.LatestVerdict != nil {
		t.Errorf("a cleared goal carries a verdict (met=%v) — a verdict from an adjudication the user stopped was recorded",
			rec.LatestVerdict.Met)
	}

	entries, err := g.store.ReadTranscript(g.sid)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	for _, e := range entries {
		if e.Type == session.EntryTypeJudgeVerdict {
			t.Errorf("a judge verdict was written into the user's transcript after `/goal clear`: %s", e.Content)
		}
	}

	states, reasons := g.goalPillStates()
	sawCleared := false
	for i, s := range states {
		if s == goalPillCleared {
			sawCleared = true
			continue
		}
		if sawCleared {
			t.Errorf("goal pill %q was emitted after `cleared` (reason %q) — it would repaint a goal the user stopped",
				s, reasons[i])
		}
	}
	if !sawCleared {
		t.Errorf("no `cleared` goal pill was emitted by `/goal clear` (states %v)", states)
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// TestGoalClear_DuringJudgeBackoff_DiscardsAdjudication: the clear lands while
// the adjudication sleeps between attempts after a transient Judge failure.
func TestGoalClear_DuringJudgeBackoff_DiscardsAdjudication(t *testing.T) {
	origSleep, origTTL := judgeSleepFn, cancelPreArmTTL
	t.Cleanup(func() { judgeSleepFn, cancelPreArmTTL = origSleep, origTTL })
	// The real backoff (60 s) far outlasts the pre-registration cancel latch
	// (5 s) that `/goal clear` arms when no Judge turn is registered. Shrink the
	// latch so it expires within this test's backoff window, as it does live.
	cancelPreArmTTL = 10 * time.Millisecond

	inBackoff := make(chan struct{})
	release := make(chan struct{})
	backoffs := 0
	judgeSleepFn = func(ctx context.Context, _ time.Duration) error {
		backoffs++
		if backoffs == 1 {
			close(inBackoff)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}

	judge := &fakeJudgeProvider{chatFn: func(n int) (*providers.LLMResponse, error) {
		if n == 1 {
			return nil, errors.New("simulated transient provider failure")
		}
		return &providers.LLMResponse{Content: cannedJudgeVerdictJSON(true, "tests pass")}, nil
	}}
	g := setUpGoalClaimAwaitingJudge(t, judge)

	done := make(chan struct{})
	go func() {
		defer close(done)
		g.al.dispatchDeferredGoalAdjudication(g.work)
	}()

	waitClosed(t, inBackoff, "the adjudication to enter its backoff wait")
	g.clearGoal(t)
	time.Sleep(50 * time.Millisecond) // let the armed latch age out, as the real backoff does
	close(release)
	waitClosed(t, done, "the deferred adjudication to return after `/goal clear`")

	g.assertAdjudicationDiscardedWhole(t, judge)
}

// TestGoalClear_MidJudgeTurn_DiscardsAdjudication: the clear lands while the
// Judge's turn is waiting on its provider.
func TestGoalClear_MidJudgeTurn_DiscardsAdjudication(t *testing.T) {
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })
	// No real sleep: a retry after the cancelled turn would run straight away,
	// exactly as it runs 60 s later in production.
	judgeSleepFn = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }

	inCall := make(chan struct{})
	proceed := make(chan struct{})
	judge := &fakeJudgeProvider{chatFn: func(n int) (*providers.LLMResponse, error) {
		if n == 1 {
			close(inCall)
			<-proceed
		}
		return &providers.LLMResponse{Content: cannedJudgeVerdictJSON(true, "tests pass")}, nil
	}}
	g := setUpGoalClaimAwaitingJudge(t, judge)

	done := make(chan struct{})
	go func() {
		defer close(done)
		g.al.dispatchDeferredGoalAdjudication(g.work)
	}()

	waitClosed(t, inCall, "the Judge's turn to reach its provider")
	g.clearGoal(t)
	close(proceed)
	waitClosed(t, done, "the deferred adjudication to return after `/goal clear`")

	g.assertAdjudicationDiscardedWhole(t, judge)
}
