// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// plan_judge_pause_test.go covers the judge-unavailability VISIBILITY fix
// (2026-09-13): JudgeCriteria retries an unavailable Judge forever inside one
// round on the 60/120/300s D7 schedule, bounded only by the caller's ctx
// (planJudgeRoundTimeout, 10 minutes). Before this fix the plan sat at
// state=running / plan_phase=judging for that entire window with NOTHING on
// the plan saying why — indistinguishable from a wedge to a human on the
// board and to the conformance E2E oracle alike (Conformance_t3b failed 3/3
// twice on exactly that, reporting "observed state=running phase=judging").
//
// Two halves, tested separately:
//   - judge side (JudgeCriteriaInput.OnUnavailable/OnRecovered fire at the
//     right moments, and retry semantics are untouched);
//   - plan-engine side (those hooks become Plan.PausedReason, cleared on
//     every exit path, never clobbering an owner-disabled pause).
package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- Judge side: the hooks themselves ---------------------------------------

// unavailabilityRecorder collects OnUnavailable/OnRecovered firings. Guarded
// by a mutex because the hooks are invoked from whichever goroutine is running
// the adjudication.
type unavailabilityRecorder struct {
	mu        sync.Mutex
	reasons   []string
	backoffs  []time.Duration
	recovered int
}

func (r *unavailabilityRecorder) onUnavailable(reason string, backoff time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reasons = append(r.reasons, reason)
	r.backoffs = append(r.backoffs, backoff)
}

func (r *unavailabilityRecorder) onRecovered() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recovered++
}

func (r *unavailabilityRecorder) snapshot() ([]string, []time.Duration, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.reasons...), append([]time.Duration(nil), r.backoffs...), r.recovered
}

// TestJudgeCriteria_OnUnavailable_FiresBeforeEveryBackoff pins the judge half
// of the contract: the hook fires once per backoff, BEFORE the sleep, carrying
// the same cause the WARN log gets and the exact interval about to be waited
// out — and it fires on the schedule judgeRetryBackoff dictates, unchanged.
// OnRecovered must NOT fire when the call ends Unavailable: the caller's own
// end-of-round cleanup owns that case, and firing here would retract a pause
// marker while the plan is still stuck.
func TestJudgeCriteria_OnUnavailable_FiresBeforeEveryBackoff(t *testing.T) {
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })

	var mu sync.Mutex
	sleeps := 0
	// Ordering oracle: record how many hook firings had happened at the
	// moment each sleep began. The hook must ALWAYS run first, so entry i
	// must read i+1.
	var hookCountAtSleep []int
	rec := &unavailabilityRecorder{}
	judgeSleepFn = func(_ context.Context, _ time.Duration) error {
		mu.Lock()
		sleeps++
		n := sleeps
		mu.Unlock()
		reasons, _, _ := rec.snapshot()
		mu.Lock()
		hookCountAtSleep = append(hookCountAtSleep, len(reasons))
		mu.Unlock()
		if n >= 2 {
			return errors.New("test: give up after 2 backoff waits")
		}
		return nil
	}

	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return nil, errors.New("simulated provider outage")
	}}

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-hook-unavailable",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "x")},
		Attempt:         1,
		ClaimText:       "done",
		OnUnavailable:   rec.onUnavailable,
		OnRecovered:     rec.onRecovered,
	})

	if !result.Unavailable {
		t.Fatalf("expected Unavailable=true when the judge keeps failing, got %+v", result)
	}

	reasons, backoffs, recovered := rec.snapshot()
	if len(reasons) != 2 {
		t.Fatalf("OnUnavailable fired %d time(s), want 2 (one per backoff): %v", len(reasons), reasons)
	}
	for i, r := range reasons {
		if !strings.Contains(r, "simulated provider outage") {
			t.Errorf("OnUnavailable[%d] reason = %q, want it to carry the real provider cause", i, r)
		}
	}
	// The intervals are judgeRetryBackoff's own, in order — proof the hook
	// reports the REAL wait, not a placeholder, and that adding the hook did
	// not perturb the schedule.
	want := []time.Duration{judgeRetryBackoff[0], judgeRetryBackoff[1]}
	for i, d := range want {
		if backoffs[i] != d {
			t.Errorf("OnUnavailable[%d] backoff = %v, want %v", i, backoffs[i], d)
		}
	}
	if recovered != 0 {
		t.Errorf("OnRecovered fired %d time(s) on an Unavailable outcome, want 0 — the judge never came back",
			recovered)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(hookCountAtSleep) != 2 {
		t.Fatalf("observed %d sleep(s), want 2", len(hookCountAtSleep))
	}
	for i, n := range hookCountAtSleep {
		if n != i+1 {
			t.Errorf("at sleep %d the hook had fired %d time(s), want %d — OnUnavailable must fire BEFORE "+
				"the backoff sleep, otherwise the pause is invisible for the whole interval it describes",
				i, n, i+1)
		}
	}
}

// TestJudgeCriteria_OnRecovered_FiresOnceTheJudgeComesBack covers the other
// half: an outage that RESOLVES inside the same round must retract itself
// before the verdict reaches the caller.
func TestJudgeCriteria_OnRecovered_FiresOnceTheJudgeComesBack(t *testing.T) {
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })
	judgeSleepFn = func(context.Context, time.Duration) error { return nil } // no real sleep

	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(callNum int) (*providers.LLMResponse, error) {
		if callNum == 1 {
			return nil, errors.New("simulated provider outage")
		}
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
		}, nil
	}}

	rec := &unavailabilityRecorder{}
	// Every assertion below reads the recorder AFTER JudgeCriteria returned,
	// which is also the ordering guarantee being checked: OnRecovered fires
	// before the result is handed back, so a caller clearing a pause marker in
	// the hook has provably cleared it by the time it inspects the verdict.
	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-hook-recovered",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "x")},
		Attempt:         1,
		ClaimText:       "done",
		OnUnavailable:   rec.onUnavailable,
		OnRecovered:     rec.onRecovered,
	})

	if result.Unavailable {
		t.Fatalf("expected a real verdict once the judge recovered, got Unavailable: %s", result.Reason)
	}
	if result.Verdict == nil || !result.Verdict.Met {
		t.Fatalf("verdict = %+v, want Met=true", result.Verdict)
	}
	reasons, _, recovered := rec.snapshot()
	if len(reasons) != 1 {
		t.Errorf("OnUnavailable fired %d time(s), want exactly 1 (one outage, one backoff): %v", len(reasons), reasons)
	}
	if recovered != 1 {
		t.Errorf("OnRecovered fired %d time(s), want exactly 1 — a pause that is never retracted is worse "+
			"than one that is never shown", recovered)
	}
}

// TestJudgeCriteria_OnRecovered_SilentWhenNothingEverPaused keeps "recovered"
// honest: a first-try success never told the caller anything was wrong, so it
// has nothing to retract.
func TestJudgeCriteria_OnRecovered_SilentWhenNothingEverPaused(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
		}, nil
	}}

	rec := &unavailabilityRecorder{}
	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-hook-no-pause",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "x")},
		Attempt:         1,
		ClaimText:       "done",
		OnUnavailable:   rec.onUnavailable,
		OnRecovered:     rec.onRecovered,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	reasons, _, recovered := rec.snapshot()
	if len(reasons) != 0 || recovered != 0 {
		t.Errorf("hooks fired on a clean first-try adjudication: OnUnavailable=%v OnRecovered=%d, want none",
			reasons, recovered)
	}
}

// --- Plan-engine side: the hooks become Plan.PausedReason -------------------

// mustPausedReason reads planID's current PausedReason, failing the test on a
// store error.
func mustPausedReason(t *testing.T, ps *plan.Store, planID string) string {
	t.Helper()
	p, err := ps.Get(planID)
	if err != nil {
		t.Fatalf("get plan %q: %v", planID, err)
	}
	return p.PausedReason
}

// newJudgingPlanHarness builds a harness with one running plan ("p1"), one
// done member task, and a prose DoD — the exact shape that makes processPlan
// open a real plan-level judge round.
func newJudgingPlanHarness(t *testing.T) *planEngineHarness {
	t.Helper()
	h := newTestPlanEngine(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "Plan 1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateRunning,
		SourceChannel: "telegram", SourceChatID: "chat-p1",
		DoD: []task.AcceptanceCriterion{planProseCriterion("The thing is done")},
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone, Result: "all good",
	})
	return h
}

// TestPlanJudgeRound_UnavailableJudge_PausesTheVisiblePlanThenClearsIt is the
// headline regression: while the round is waiting out a D7 backoff the plan
// must SAY SO on the field the board and the WS frame already read, and the
// moment the judge comes back that statement must be gone.
//
// The fake judge stands in for JudgeCriteria's internals — it invokes the same
// two hooks the real one does (judge.go's judgeBackoffWait / the OnRecovered
// defer in runVerifierAdjudication, both pinned by the judge-side tests
// above) and samples the plan at each point, which is the only way to observe
// a state that exists solely DURING the call.
func TestPlanJudgeRound_UnavailableJudge_PausesTheVisiblePlanThenClearsIt(t *testing.T) {
	h := newJudgingPlanHarness(t)

	var duringPause, afterRecovery string
	var frames []string
	var framesMu sync.Mutex
	h.plans.OnChange = func(p *plan.Plan) {
		framesMu.Lock()
		frames = append(frames, p.PausedReason)
		framesMu.Unlock()
	}

	h.judge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		in.notifyUnavailable("provider outage: 503 upstream", 60*time.Second)
		duringPause = mustPausedReason(t, h.plans, "p1")
		in.notifyRecovered()
		afterRecovery = mustPausedReason(t, h.plans, "p1")
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{
			Met:          true,
			PerCriterion: []task.CriterionVerdict{{CriterionID: in.Criteria[0].ID, Met: true, Reason: "confirmed"}},
		}}
	}

	h.pe.processPlan(context.Background(), "p1")
	h.pe.judgeWG.Wait()

	if !strings.HasPrefix(duringPause, plan.PausedReasonJudgeUnavailable) {
		t.Fatalf("PausedReason during the backoff = %q, want a value prefixed %q — without it the plan is "+
			"state=running/plan_phase=judging with nothing saying why, which is the whole defect",
			duringPause, plan.PausedReasonJudgeUnavailable)
	}
	// The detail matters as much as the prefix: a human reading the chip needs
	// the cause and the retry interval, or "paused" is just a second way of
	// saying nothing.
	if !strings.Contains(duringPause, "provider outage: 503 upstream") {
		t.Errorf("PausedReason = %q, want it to name the real cause", duringPause)
	}
	if !strings.Contains(duringPause, "retrying in 1m0s") {
		t.Errorf("PausedReason = %q, want it to state the retry interval", duringPause)
	}
	if afterRecovery != "" {
		t.Errorf("PausedReason after OnRecovered = %q, want empty — the judge is back, the pause is stale",
			afterRecovery)
	}
	if got := mustPausedReason(t, h.plans, "p1"); got != "" {
		t.Errorf("PausedReason after the round = %q, want empty", got)
	}

	// The pause and its retraction each went through plan.Store.Update, which
	// is the single choke point gateway.go wires plan_status WS emission to —
	// so the SPA is told about both without any new emit call.
	framesMu.Lock()
	defer framesMu.Unlock()
	sawPausedFrame, sawClearedFrame := false, false
	for _, f := range frames {
		if strings.HasPrefix(f, plan.PausedReasonJudgeUnavailable) {
			sawPausedFrame = true
		} else if sawPausedFrame && f == "" {
			sawClearedFrame = true
		}
	}
	if !sawPausedFrame || !sawClearedFrame {
		t.Errorf("plan store change hook saw %v — want a change carrying the judge pause and a later one "+
			"clearing it (this hook is exactly what gateway.go turns into plan_status frames)", frames)
	}
}

// TestPlanJudgeRound_AbandonedRound_ClearsTheJudgePause covers the
// applyJudgeRoundOutcome Unavailable branch: the round gave up (ctx timeout),
// the phase reverts to dispatching, and the pause marker must go with it —
// otherwise processPlan's `p.PausedReason != ""` early return freezes the plan
// permanently, which is strictly worse than the silent stall.
func TestPlanJudgeRound_AbandonedRound_ClearsTheJudgePause(t *testing.T) {
	h := newJudgingPlanHarness(t)

	h.judge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		in.notifyUnavailable("judge_not_configured: Judge System Agent is not registered", 300*time.Second)
		// No OnRecovered: this is the ctx-cancelled-mid-backoff shape.
		return JudgeCriteriaResult{Unavailable: true, Reason: "context deadline exceeded"}
	}

	h.pe.processPlan(context.Background(), "p1")
	h.pe.judgeWG.Wait()

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PausedReason != "" {
		t.Fatalf("PausedReason = %q after an abandoned round, want empty — a plan left paused on a judge "+
			"nobody is waiting for any more never dispatches and never judges again", got.PausedReason)
	}
	if got.PlanPhase != plan.PhaseDispatching {
		t.Errorf("plan_phase = %q, want dispatching (D7 revert)", got.PlanPhase)
	}
	if got.JudgeRounds != 0 {
		t.Errorf("judge_rounds = %d, want 0 — an unavailable judge consumes no round (D7)", got.JudgeRounds)
	}
}

// TestPlanJudgeRound_StopDuringPause_StillClearsTheJudgePause exercises the
// LAST-RESORT guard specifically: runPlanJudgeRound's unconditional defer.
// Here the outcome is DROPPED entirely (a Stop landed mid-round, so
// applyJudgeRoundOutcome bails before reaching either the Unavailable branch
// or the verdict path) — the defer is the only thing left that can retract the
// marker, and a plan that stays paused forever on a stale reason is exactly
// what this test would catch.
func TestPlanJudgeRound_StopDuringPause_StillClearsTheJudgePause(t *testing.T) {
	h := newJudgingPlanHarness(t)

	var pausedMidRound string
	h.judge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		in.notifyUnavailable("simulated provider outage", 60*time.Second)
		pausedMidRound = mustPausedReason(t, h.plans, "p1")
		// The plan leaves `running` while the round is still in flight — the
		// Stop path's terminal transition.
		failed := plan.StateFailed
		reason := plan.FailedReasonStoppedByUser
		if _, err := h.plans.Update("p1", plan.Patch{State: &failed, FailedReason: &reason}); err != nil {
			t.Errorf("simulate stop: %v", err)
		}
		return JudgeCriteriaResult{Unavailable: true, Reason: "context canceled"}
	}

	h.pe.processPlan(context.Background(), "p1")
	h.pe.judgeWG.Wait()

	if !strings.HasPrefix(pausedMidRound, plan.PausedReasonJudgeUnavailable) {
		t.Fatalf("setup: PausedReason mid-round = %q, want the judge-unavailability pause (nothing to clear "+
			"otherwise, so this test would pass vacuously)", pausedMidRound)
	}
	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PausedReason != "" {
		t.Fatalf("PausedReason = %q after the round ended on a stopped plan, want empty — "+
			"runPlanJudgeRound's defer is the only cleanup that runs on this path", got.PausedReason)
	}
}

// TestPlanJudgePause_NeverTouchesAnOwnerDisabledPause is the prefix-guard
// test. FR-065's owner-disabled pause is the operator-actionable one ("go
// re-enable the agent"); a judge round running on that same plan must neither
// overwrite it with a transient judge message nor — far worse — CLEAR it on
// recovery, which would silently resume dispatch for a plan whose owner is
// still disabled.
func TestPlanJudgePause_NeverTouchesAnOwnerDisabledPause(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	ownerDisabled := pausedReasonOwnerDisabled
	if _, err := h.plans.Update("p1", plan.Patch{PausedReason: &ownerDisabled}); err != nil {
		t.Fatalf("setup: pause p1 for owner_disabled: %v", err)
	}

	h.pe.noteJudgeUnavailable("p1", "simulated provider outage", 60*time.Second)
	if got := mustPausedReason(t, h.plans, "p1"); got != pausedReasonOwnerDisabled {
		t.Fatalf("PausedReason = %q after a judge-unavailability pause attempt, want %q left intact — "+
			"the disabled owner is the reason an operator can act on", got, pausedReasonOwnerDisabled)
	}

	h.pe.clearJudgeUnavailablePause("p1")
	if got := mustPausedReason(t, h.plans, "p1"); got != pausedReasonOwnerDisabled {
		t.Fatalf("PausedReason = %q after judge recovery, want %q still set — clearing it here would resume "+
			"a plan whose owner is still disabled (FR-065 bypass)", got, pausedReasonOwnerDisabled)
	}

	// Symmetry check: with no competing pause the same two calls DO write and
	// DO clear, so the assertions above are the guard talking, not a no-op.
	mustCreateRunningPlan(t, h.plans, "p2", "owner")
	h.pe.noteJudgeUnavailable("p2", "simulated provider outage", 60*time.Second)
	if got := mustPausedReason(t, h.plans, "p2"); !strings.HasPrefix(got, plan.PausedReasonJudgeUnavailable) {
		t.Fatalf("control plan PausedReason = %q, want the judge-unavailability pause", got)
	}
	h.pe.clearJudgeUnavailablePause("p2")
	if got := mustPausedReason(t, h.plans, "p2"); got != "" {
		t.Fatalf("control plan PausedReason = %q after recovery, want empty", got)
	}
}

// TestBuildJudgeUnavailablePausedReason pins the operator-facing string: a
// stable prefix (the SPA chip and the conformance E2E oracle both match on
// it), a one-line capped cause, and the retry interval. It must also survive
// pkg/plan's closed-set validation — a PausedReason the store rejects is a
// pause nobody ever sees.
func TestBuildJudgeUnavailablePausedReason(t *testing.T) {
	cases := []struct {
		name    string
		cause   string
		backoff time.Duration
		want    string
	}{
		{"cause_and_backoff", "provider outage", 60 * time.Second,
			"judge temporarily unavailable: provider outage; retrying in 1m0s"},
		{"multiline_cause_squashed", "provider outage\n\tat upstream", 120 * time.Second,
			"judge temporarily unavailable: provider outage at upstream; retrying in 2m0s"},
		{"no_cause", "", 300 * time.Second,
			"judge temporarily unavailable; retrying in 5m0s"},
		{"no_backoff", "provider outage", 0,
			"judge temporarily unavailable: provider outage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildJudgeUnavailablePausedReason(tc.cause, tc.backoff)
			if got != tc.want {
				t.Fatalf("buildJudgeUnavailablePausedReason(%q, %v) = %q, want %q", tc.cause, tc.backoff, got, tc.want)
			}
			if !plan.IsValidPausedReason(got) {
				t.Fatalf("%q is rejected by plan.IsValidPausedReason — plan.Store.Update would refuse the "+
					"write and the pause would never reach the board", got)
			}
			if !plan.IsJudgeUnavailablePausedReason(got) {
				t.Fatalf("%q is not recognised as a judge-unavailability reason — the prefix guards that stop "+
					"it clobbering owner_disabled would all mis-fire", got)
			}
		})
	}

	// An unbounded provider error must not become an unbounded board chip.
	long := buildJudgeUnavailablePausedReason(strings.Repeat("x", 400), 60*time.Second)
	if len(long) > judgeUnavailableReasonCap+80 {
		t.Errorf("PausedReason for a 400-char cause is %d chars — want it capped near %d",
			len(long), judgeUnavailableReasonCap)
	}
	if !plan.IsJudgeUnavailablePausedReason(long) {
		t.Errorf("truncation broke the prefix: %q", long)
	}

	// The owner-disabled reason must NOT be swept up by the prefix rule.
	if plan.IsJudgeUnavailablePausedReason(pausedReasonOwnerDisabled) {
		t.Errorf("%q must not match the judge-unavailability prefix", pausedReasonOwnerDisabled)
	}
}
