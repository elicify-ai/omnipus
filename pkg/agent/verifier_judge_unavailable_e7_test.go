// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_judge_unavailable_e7_test.go pins UAT scenario E-7 ("Judge
// unavailable → judge_unavailable pill, not done, not silent, no wasted
// rounds") against the two ways a verifier dispatch used to be misclassified:
//
//   - a Judge that cannot run until an operator fixes it (unknown provider,
//     rejected credentials) was retried on judgeRetryBackoff — 60/120/300 s —
//     until the round timeout, holding the goal card on "judging" for up to
//     ten minutes before anything was surfaced;
//   - a Judge whose verdict was cut off at its output-token limit returned the
//     partial text as a finished answer; the parser failed on it ("unclosed
//     JSON object"), every prose criterion was scored unmet as
//     criterion_unjudgeable, the goal consumed a round, and the worker was
//     steered with a raw parser error.
//
// Every test drives the REAL dispatch (al.JudgeCriteria / runGoalAdjudication →
// runVerifierAdjudication → dispatchVerifierTurn → runTurn) with only the
// Judge's provider scripted. The backoff sleep is stubbed so a pre-fix retry
// loop terminates (after three waits) instead of hanging the test.
package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// e7BackoffRecorder records every judgeRetryBackoff wait the dispatch asks
// for. It never sleeps, and it refuses the third wait so a retry loop that
// should not exist still ends.
type e7BackoffRecorder struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (r *e7BackoffRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.waits)
}

func e7RecordJudgeBackoffWaits(t *testing.T) *e7BackoffRecorder {
	t.Helper()
	origSleep, origBackoff := judgeSleepFn, judgeRetryBackoff
	t.Cleanup(func() { judgeSleepFn, judgeRetryBackoff = origSleep, origBackoff })
	judgeRetryBackoff = []time.Duration{60 * time.Second, 120 * time.Second, 300 * time.Second}
	r := &e7BackoffRecorder{}
	judgeSleepFn = func(_ context.Context, d time.Duration) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.waits = append(r.waits, d)
		if len(r.waits) >= 3 {
			return errors.New("e7 test: refusing the third backoff wait so a retry loop terminates")
		}
		return nil
	}
	return r
}

// e7BreakJudgeModel reproduces the UAT tester's action: the Judge's model and
// provider both point at values that do not exist.
func e7BreakJudgeModel(cfg *config.Config) {
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == string(coreagent.IDJudge) {
			cfg.Agents.List[i].Model = &config.AgentModelConfig{
				Primary:  "nonexistent-vendor/no-such-model-e7",
				Provider: "no-such-provider-e7",
			}
		}
	}
}

// TestJudgeUnavailableE7_ProviderUnknown_WithheldWithoutBackoff: a Judge whose
// provider does not exist is refused by runTurn's pre-turn gate before any
// upstream call. That refusal is unavailable (no verdict, attempt not
// consumed), it is reported with the judge_misconfigured prefix, and it is
// dispatched exactly once — no backoff wait at all.
func TestJudgeUnavailableE7_ProviderUnknown_WithheldWithoutBackoff(t *testing.T) {
	waits := e7RecordJudgeBackoffWaits(t)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, e7BreakJudgeModel)
	if needs, _ := judgeInst.needsProviderSnapshot(); !needs {
		t.Fatal("BLOCKED: the setup did not produce a Judge whose provider is unknown; the test cannot prove the E-7 path")
	}
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: verdictJSON}, nil
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), proseInputForTask("t-e7-provider-unknown"))

	if !result.Unavailable {
		t.Fatalf("a Judge with an unknown provider must be unavailable, got verdict %+v (reason %q)", result.Verdict, result.Reason)
	}
	if result.Verdict != nil {
		t.Errorf("no verdict may be recorded for an unavailable Judge, got %+v", result.Verdict)
	}
	wantPrefix := JudgeMisconfiguredReasonPrefix + string(CodeNeedsProvider) + ": "
	if !strings.HasPrefix(result.Reason, wantPrefix) {
		t.Errorf("Reason = %q, want prefix %q so the operator sees an actionable, distinct state", result.Reason, wantPrefix)
	}
	// Observed live on the E-7 reproduction: the shared needs_provider copy
	// told the operator their "sign-in for this provider expired". An unknown
	// provider is a configuration problem, and the message must say so.
	if lower := strings.ToLower(result.Reason); strings.Contains(lower, "sign-in") || strings.Contains(lower, "sign in") {
		t.Errorf("Reason = %q tells the operator to sign in again; the Judge's provider is unknown, not signed out", result.Reason)
	}
	if !strings.Contains(result.Reason, "not configured") {
		t.Errorf("Reason = %q, want it to say the Judge's provider is not configured", result.Reason)
	}
	if n := waits.count(); n != 0 {
		t.Errorf("the dispatch waited on the retry backoff %d time(s), want 0 — an unknown provider does not recover by waiting", n)
	}
	if n := fake.callCount(); n != 0 {
		t.Errorf("the Judge's provider was called %d time(s), want 0 — the pre-turn gate refuses before any upstream call", n)
	}
}

// TestJudgeUnavailableE7_ProviderAuthRejected_WithheldWithoutBackoff: the
// provider rejecting the Judge's credentials (HTTP 401) is also an
// operator-only fix. The Reason carries the translated message, never the raw
// provider body.
func TestJudgeUnavailableE7_ProviderAuthRejected_WithheldWithoutBackoff(t *testing.T) {
	waits := e7RecordJudgeBackoffWaits(t)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	const providerBody = `{"error":{"message":"e7 raw provider body: no auth credentials found"}}`
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return nil, &common.ProviderError{Status: 401, Body: providerBody, BodyPreview: providerBody}
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), proseInputForTask("t-e7-auth-rejected"))

	if !result.Unavailable || result.Verdict != nil {
		t.Fatalf("a Judge whose credentials are rejected must be unavailable with no verdict, got unavailable=%v verdict=%+v",
			result.Unavailable, result.Verdict)
	}
	wantPrefix := JudgeMisconfiguredReasonPrefix + string(CodeProviderAuthFailed) + ": "
	if !strings.HasPrefix(result.Reason, wantPrefix) {
		t.Errorf("Reason = %q, want prefix %q", result.Reason, wantPrefix)
	}
	if strings.Contains(result.Reason, "e7 raw provider body") {
		t.Errorf("Reason = %q carries the raw provider body; it must carry the translated message only", result.Reason)
	}
	if n := waits.count(); n != 0 {
		t.Errorf("the dispatch waited on the retry backoff %d time(s), want 0", n)
	}
}

// TestJudgeUnavailableE7_TransientOutageStillBacksOff is the control that keeps
// the classification narrow: an unclassified provider outage is not an
// operator-only fix and must still take the retry backoff.
func TestJudgeUnavailableE7_TransientOutageStillBacksOff(t *testing.T) {
	waits := e7RecordJudgeBackoffWaits(t)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return nil, errors.New("e7 simulated transient provider outage")
	}}

	result := al.JudgeCriteria(context.Background(), proseInputForTask("t-e7-transient"))

	if !result.Unavailable {
		t.Fatalf("a provider outage must be unavailable, got verdict %+v", result.Verdict)
	}
	if strings.HasPrefix(result.Reason, JudgeMisconfiguredReasonPrefix) {
		t.Errorf("Reason = %q: a transient outage was misclassified as an operator-only refusal", result.Reason)
	}
	if n := waits.count(); n != 3 {
		t.Errorf("backoff waits = %d, want 3 (the recorder refuses the third) — a transient outage must keep retrying", n)
	}
}

// e7CutOffVerdict is a verdict the Judge started writing and the output-token
// limit cut mid-object: the opening fence arrived, the closing fence and the
// closing braces never did. This is the only shape that yields parseJudgeResponse's
// "unclosed JSON object" — the text the UAT worker narrated.
const e7CutOffVerdict = "I listed the workspace and read the file.\n\n```json\n" +
	`{"met": true, "criteria": [{"id": "c1", "met": true, "reason": "the file ex`

// TestJudgeUnavailableE7_TruncatedVerdict_WithheldNotScoredUnmet: a verdict
// truncated at the output-token limit is unavailable — no verdict, no unmet
// criterion, no parser error in the Reason — and the Judge is dispatched once.
func TestJudgeUnavailableE7_TruncatedVerdict_WithheldNotScoredUnmet(t *testing.T) {
	waits := e7RecordJudgeBackoffWaits(t)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: e7CutOffVerdict, FinishReason: "length"}, nil
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), proseInputForTask("t-e7-truncated"))

	if !result.Unavailable {
		t.Fatalf("a truncated verdict must be withheld as unavailable, got verdict %+v (reason %q)", result.Verdict, result.Reason)
	}
	if result.Verdict != nil {
		t.Errorf("a truncated verdict must record nothing, got %+v", result.Verdict)
	}
	if !strings.HasPrefix(result.Reason, JudgeOutputTruncatedReasonPrefix) {
		t.Errorf("Reason = %q, want prefix %q", result.Reason, JudgeOutputTruncatedReasonPrefix)
	}
	if strings.Contains(result.Reason, "unclosed") || strings.Contains(result.Reason, "criterion_unjudgeable") {
		t.Errorf("Reason = %q leaks the parser failure the truncation used to be misread as", result.Reason)
	}
	if n := waits.count(); n != 0 {
		t.Errorf("backoff waits = %d, want 0 — the same request truncates the same way again", n)
	}
	if n, most := fake.callCount(), 1+maxTruncationContinuations; n < 1 || n > most {
		t.Errorf("the Judge's provider was called %d time(s), want 1..%d — one verifier turn (with its own ADR-087 "+
			"continuations), never a second dispatch", n, most)
	}
}

// TestJudgeUnavailableE7_TruncatedAfterCompleteVerdict_IsScored is the control
// for the one exception: the verdict object closed and names every criterion,
// and only prose written after it was cut. That verdict is complete and must
// be scored, not thrown away with a costly Judge turn.
func TestJudgeUnavailableE7_TruncatedAfterCompleteVerdict_IsScored(t *testing.T) {
	e7RecordJudgeBackoffWaits(t)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	const completeThenCut = "```json\n" +
		`{"met": true, "criteria": [{"id": "c1", "met": true, "reason": "read the file", "evidence_quote": "banana"}]}` +
		"\n```\nFurther notes on how I checked th"
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: completeThenCut, FinishReason: "length"}, nil
	}}

	result := al.JudgeCriteria(context.Background(), proseInputForTask("t-e7-complete-then-cut"))

	if result.Unavailable {
		t.Fatalf("a complete verdict followed by truncated prose must be scored, got unavailable (%q)", result.Reason)
	}
	if result.Verdict == nil || !result.Verdict.Met {
		t.Fatalf("verdict = %+v, want the complete met verdict", result.Verdict)
	}
}

// TestJudgeUnavailableE7_GoalClaim_PillNoRoundNoUnmet is E-7 end to end at the
// goal layer: a claim adjudicated against a Judge whose model and provider do
// not exist ends on the judge_unavailable pill carrying the actionable reason,
// consumes no round, records no verdict, marks no criterion unmet, delivers no
// steer, and never waits on the retry backoff.
func TestJudgeUnavailableE7_GoalClaim_PillNoRoundNoUnmet(t *testing.T) {
	resetGoalTriggerStateForTest()
	waits := e7RecordJudgeBackoffWaits(t)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, e7BreakJudgeModel)
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: verdictJSON}, nil
	}}
	judgeInst.Provider = fake

	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	setGoalRoundsArmedRecorded(t, store, sid, "e7-marker.txt exists and contains banana", 0, time.Now())
	rec := goalRecordForSession(t, sid)

	c, cleanup := newEventCollector(t, al)
	steered := false
	met := al.runGoalAdjudication(context.Background(), agentInst, "", sid, store, rec,
		"[goal:evidence] wrote e7-marker.txt and read it back", func(string) { steered = true })
	cleanup()

	if met {
		t.Fatal("an unavailable Judge must never report the goal met")
	}
	if steered {
		t.Error("a steer was delivered — an unavailable Judge must not send the worker back to redo finished work")
	}
	payloads := goalStatusPayloadsFor(c, sid)
	if len(payloads) == 0 {
		t.Fatal("no goal status frames emitted — the unavailable state was silent")
	}
	last := payloads[len(payloads)-1]
	if last.State != goalPillJudgeUnavailable {
		t.Errorf("last goal status state = %q, want %q", last.State, goalPillJudgeUnavailable)
	}
	if !strings.HasPrefix(last.LatestReason, JudgeMisconfiguredReasonPrefix) {
		t.Errorf("last goal status reason = %q, want prefix %q", last.LatestReason, JudgeMisconfiguredReasonPrefix)
	}

	after := goalRecordForSession(t, sid)
	if after.Round != rec.Round {
		t.Errorf("goal Round = %d after an unavailable Judge, want %d (no round consumed)", after.Round, rec.Round)
	}
	if after.LatestVerdict != nil {
		t.Errorf("goal LatestVerdict = %+v, want nil (no verdict recorded)", after.LatestVerdict)
	}
	for _, cr := range append(append([]task.AcceptanceCriterion(nil), after.Criteria...), after.DoD...) {
		if cr.Status == task.CritUnmet {
			t.Errorf("criterion %q is %q after an unavailable Judge; nothing was judged", cr.ID, cr.Status)
		}
	}
	if n := waits.count(); n != 0 {
		t.Errorf("the adjudication waited on the retry backoff %d time(s), want 0", n)
	}
	if n := fake.callCount(); n != 0 {
		t.Errorf("the Judge's provider was called %d time(s), want 0", n)
	}
}
