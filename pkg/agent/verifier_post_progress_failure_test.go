// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// verifier_post_progress_failure_test.go pins ADR-084 D9 prerequisite 3
// (revision 3 R3-c; spec FR-053/FR-054/FR-054a/FR-055,
// judge-active-reviewer-spec.md US-7 AC-3/AC-4):
//
//	"Reclassify a timeout that occurred after the turn made progress as
//	 unable_to_verify (D2a) rather than Unavailable, so it consumes a round
//	 and reaches an honest failure instead of looping."
//
// FR-054: "A post-progress failure MUST NOT re-enter the judgeBackoffWait +
// continue retry loop. It MUST exit that loop and return ... every prose
// criterion as unable_to_verify with unavailable=false." — with JudgeCriteria
// still withholding the first K occurrences (round not consumed) per
// FR-018/FR-019, so the honest failure arrives within K+1 adjudications.
//
// The UAT shape (E-14 run 2): a Judge turn that produced output and made tool
// calls, then hit the (configurable, default 420 s) timeout, was retried with
// backoff indefinitely — each retry a full tool-using turn. The oracle here is
// the provider call count: with judgeSleepFn neutralised (every backoff
// "elapses" instantly), a regression to retrying shows up as the provider
// being called again after the failed turn; the fix returns without another
// call.
package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// instantReadFileTool is a read_file double that succeeds immediately — a
// verifier turn whose first LLM round emits a tool call that ADMITS a result,
// which is FR-053's progress predicate (VerifierBudget.RecordToolCall ran).
type instantReadFileTool struct{}

func (instantReadFileTool) Name() string               { return "read_file" }
func (instantReadFileTool) Description() string        { return "instant read_file test double (FR-053)" }
func (instantReadFileTool) Parameters() map[string]any { return nil }
func (instantReadFileTool) Scope() tools.ToolScope     { return tools.ScopeGeneral }
func (instantReadFileTool) Category() tools.ToolCategory {
	return tools.CategoryFilesystem
}
func (instantReadFileTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	return &tools.ToolResult{ForLLM: "the file says hello", IsError: false}
}

// setUpPostProgressJudge wires a Judge whose turn makes real progress (one
// read_file call) and then hangs on its next provider call until the judge
// turn timeout (1 s) kills it. judgeSleepFn is neutralised so any code path
// that DID re-enter the backoff would dispatch another turn immediately —
// making a retry observable as a growing provider-call count, not a slow test.
func setUpPostProgressJudge(t *testing.T, taskID string) (*AgentLoop, *fakeJudgeProvider) {
	t.Helper()
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })
	judgeSleepFn = func(context.Context, time.Duration) error { return nil }

	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.Judge.TimeoutSec = 1
	})
	allowReadFilePolicy(judgeInst)
	judgeInst.Tools.RegisterReplacing(instantReadFileTool{})

	var judge *fakeJudgeProvider
	judge = &fakeJudgeProvider{chatFn: func(n int) (*providers.LLMResponse, error) {
		if n%2 == 1 {
			// Progress: ask for one tool call, which admits a result.
			return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
				ID: "verifier-read-fr053", Name: "read_file",
				Arguments: map[string]any{"path": "README.md"},
			}}}, nil
		}
		// Then hang until the 1 s judge-turn deadline kills the turn — the
		// post-progress timeout under test. fakeJudgeProvider.Chat stores the
		// current call's ctx in lastCtx BEFORE invoking chatFn, so this is the
		// hung call's own deadline.
		<-judge.capturedCtx().Done()
		return nil, judge.capturedCtx().Err()
	}}
	judgeInst.Provider = judge
	return al, judge
}

// TestVerifierPostProgressFailure_DoesNotRetry_AndWithholdsRound: FR-054's
// core — exit, don't back off — plus FR-018/019's first-occurrence behaviour:
// the adjudication is Unavailable (no round consumed) with the
// judge_unfinished reason, and no further Judge turn runs.
func TestVerifierPostProgressFailure_DoesNotRetry_AndWithholdsRound(t *testing.T) {
	const taskID = "t-fr054-post-progress"
	al, judge := setUpPostProgressJudge(t, taskID)

	res := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          taskID,
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "the file was reviewed")},
		Attempt:         1,
		ClaimText:       "done",
	})

	if !res.Unavailable {
		t.Fatalf("the first post-progress failure must be withheld (Unavailable, round not consumed — FR-018/019), got verdict=%v reason=%q",
			res.Verdict != nil, res.Reason)
	}
	if res.Verdict != nil {
		t.Errorf("a withheld adjudication carries no verdict, got %+v", res.Verdict)
	}
	if !strings.Contains(res.Reason, JudgeUnfinishedReasonPrefix) || !strings.Contains(res.Reason, "could not finish") {
		t.Errorf("reason %q must say the Judge could not finish (the visible honest outcome), prefixed %q",
			res.Reason, JudgeUnfinishedReasonPrefix)
	}
	// The no-retry oracle: exactly the two provider calls of ONE turn (the
	// tool-call round and the hung round). A retry re-enters with backoff, and
	// judgeSleepFn is neutralised, so it would appear as a third call.
	if n := judge.callCount(); n != 2 {
		t.Errorf("the Judge's provider was called %d times; want 2 — a post-progress failure must not re-enter the backoff retry loop (FR-054)", n)
	}
}

// TestVerifierPostProgressFailure_ScoresHonestFailureAtKBound: after K
// consecutive post-progress failures (UnableToVerifyMaxRerunsDefault = 3) the
// (K+1)th adjudication is SCORED — every criterion unmet with a
// could-not-verify reason, the round consumed. D9: "consumes a round and
// reaches an honest failure instead of looping."
func TestVerifierPostProgressFailure_ScoresHonestFailureAtKBound(t *testing.T) {
	const taskID = "t-fr054-k-bound"
	al, judge := setUpPostProgressJudge(t, taskID)

	in := JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          taskID,
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "the file was reviewed")},
		Attempt:         1,
		ClaimText:       "done",
	}

	withheld := 0
	var scored JudgeCriteriaResult
	for i := 0; i < UnableToVerifyMaxRerunsDefault+1; i++ {
		scored = al.JudgeCriteria(context.Background(), in)
		if scored.Unavailable {
			withheld++
		}
	}
	if withheld != UnableToVerifyMaxRerunsDefault {
		t.Fatalf("%d adjudications withheld; want exactly K=%d before the honest failure (FR-018/019)",
			withheld, UnableToVerifyMaxRerunsDefault)
	}
	if scored.Unavailable || scored.Verdict == nil {
		t.Fatalf("the (K+1)th adjudication must be scored, got unavailable=%v verdict=%v reason=%q",
			scored.Unavailable, scored.Verdict, scored.Reason)
	}
	if scored.Verdict.Met {
		t.Errorf("the scored verdict must be unmet (honest failure), got met")
	}
	if !strings.Contains(scored.Reason, "could not verify") {
		t.Errorf("reason %q must partition the failure as could-not-verify, never as a judgment about the work", scored.Reason)
	}
	// Each adjudication ran exactly ONE turn (2 provider calls); the bound came
	// from the K tracker, not from retrying inside one adjudication.
	if n := judge.callCount(); n != 2*(UnableToVerifyMaxRerunsDefault+1) {
		t.Errorf("the Judge's provider was called %d times; want %d (one turn per adjudication, none retried)",
			n, 2*(UnableToVerifyMaxRerunsDefault+1))
	}
}

// TestVerifierZeroProgressFailure_KeepsBackoffBehaviour (FR-055): a turn that
// made NO progress keeps today's D7 behaviour exactly — Unavailable with the
// backoff-and-retry path taken (the sleep seam is invoked), never the FR-054
// no-retry exit. The zero-progress failure is a first-call timeout (no tool
// call ever ran); the sleep seam breaks the backoff so the test is bounded —
// its INVOCATION is the oracle that the backoff path was taken.
func TestVerifierZeroProgressFailure_KeepsBackoffBehaviour(t *testing.T) {
	const taskID = "t-fr055-zero-progress"
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })
	slept := 0
	judgeSleepFn = func(context.Context, time.Duration) error {
		slept++
		return errors.New("test: break the backoff after observing it")
	}

	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.Judge.TimeoutSec = 1
	})
	var judge *fakeJudgeProvider
	judge = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		// Zero progress: the FIRST provider call hangs until the 1 s judge
		// turn deadline kills it — no tool call ever ran.
		<-judge.capturedCtx().Done()
		return nil, judge.capturedCtx().Err()
	}}
	judgeInst.Provider = judge

	res := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          taskID,
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "the file was reviewed")},
		Attempt:         1,
		ClaimText:       "done",
	})

	if !res.Unavailable || res.Verdict != nil {
		t.Fatalf("a zero-progress failure stays Unavailable with no verdict (FR-055), got verdict=%v reason=%q", res.Verdict != nil, res.Reason)
	}
	if slept < 1 {
		t.Errorf("the backoff seam was never invoked — a zero-progress failure must keep the D7 backoff-and-retry path exactly (FR-055)")
	}
	if strings.Contains(res.Reason, JudgeUnfinishedReasonPrefix) {
		t.Errorf("reason %q must not carry the post-progress prefix for a zero-progress failure", res.Reason)
	}
	if n := judge.callCount(); n != 1 {
		t.Errorf("the Judge's provider was called %d times; want 1 (the hung zero-progress call)", n)
	}
}
