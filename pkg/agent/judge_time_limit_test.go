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
)

// TestJudgeTimeLimit_IsNotReportedAsAProviderTimeout pins SFH F5 (issue
// #1306): when the Judge's own check time limit (judge.timeout_sec) ends its
// verification turn, the outcome is judge_time_limit with honest copy — not
// turn_timed_out ("the model provider didn't answer"). A plain deadline with
// no such cause still means a provider request timeout.
func TestJudgeTimeLimit_IsNotReportedAsAProviderTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeoutCause(context.Background(), time.Nanosecond, ErrJudgeTimeLimit)
	defer cancel()
	<-ctx.Done()

	judgeErr := withDeadlineCause(ctx, context.DeadlineExceeded)
	if got := TranslateTurnError(judgeErr); got.Code != CodeJudgeTimeLimit {
		t.Fatalf("Judge time limit translated to %q, want %q", got.Code, CodeJudgeTimeLimit)
	}
	if got := TranslateTurnError(judgeErr); got.Retryable {
		t.Error("judge_time_limit must not be retryable as-is (the operator raises the limit)")
	}
	if !errors.Is(judgeErr, context.DeadlineExceeded) {
		t.Error("the raw deadline must stay in the chain for callers that errors.Is it")
	}
	cause := judgeRetryCause(judgeErr)
	if !strings.Contains(cause, "check time limit") || strings.Contains(cause, "did not answer") {
		t.Fatalf("judge retry cause = %q, want the Judge's own check time limit, not a provider answer", cause)
	}
	if msg := UserMessageForCode(CodeJudgeTimeLimit); strings.Contains(msg, "provider") || msg == UserMessageForCode(CodeUnknown) {
		t.Fatalf("judge_time_limit copy = %q, want its own Judge-specific sentence", msg)
	}

	plain := withDeadlineCause(context.Background(), context.DeadlineExceeded)
	if got := TranslateTurnError(plain); got.Code != CodeTurnTimedOut {
		t.Fatalf("a deadline with no Judge cause translated to %q, want %q", got.Code, CodeTurnTimedOut)
	}
}

// judgeBlocksUntilCtxDone never answers: it waits for its call context to end
// and returns the context's error, exactly as a real provider call cut off by
// the context would.
type judgeBlocksUntilCtxDone struct{}

func (judgeBlocksUntilCtxDone) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (judgeBlocksUntilCtxDone) GetDefaultModel() string { return "judge-blocks" }

// TestJudgeTimeLimit_RealVerifierTurnReportsItsOwnTimeLimit drives the real
// JudgeCriteria → verifier turn path with judge.timeout_sec = 1 and a Judge
// model that never answers. The adjudication's reason must name the Judge's
// own check time limit — never "its model did not answer in time".
func TestJudgeTimeLimit_RealVerifierTurnReportsItsOwnTimeLimit(t *testing.T) {
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })
	judgeSleepFn = func(context.Context, time.Duration) error { return errors.New("test: no backoff wait") }

	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.Judge.TimeoutSec = 1
	})
	judgeInst.Provider = judgeBlocksUntilCtxDone{}

	res := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-1306-judge-time-limit",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "the report exists")},
		Attempt:         1,
		ClaimText:       "wrote the report",
	})
	if !res.Unavailable {
		t.Fatalf("a Judge that never answers produced a verdict: %+v", res.Verdict)
	}
	if !strings.Contains(res.Reason, "check time limit") || strings.Contains(res.Reason, "did not answer") {
		t.Fatalf("Judge reason = %q, want its own check time limit, not a provider non-answer", res.Reason)
	}
}
