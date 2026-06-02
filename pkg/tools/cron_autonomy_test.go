package tools

import (
	"context"
	"fmt"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/cron"
)

// fakeExecutor is a minimal JobExecutor for ExecuteJob tests.
type fakeExecutor struct {
	reply string
	err   error
	calls int
}

func (f *fakeExecutor) ProcessDirectWithChannel(
	ctx context.Context, content, sessionKey, channel, chatID string,
) (string, error) {
	f.calls++
	return f.reply, f.err
}

func newCronToolWithExecutor(t *testing.T, exec JobExecutor) *CronTool {
	t.Helper()
	tool := newTestCronTool(t)
	tool.executor = exec
	return tool
}

// TestSchedule_OwnerDefaultsToCreatingAgent verifies the cron tool defaults a
// new job's owner to the calling agent (FR-002).
func TestSchedule_OwnerDefaultsToCreatingAgent(t *testing.T) {
	tool := newTestCronTool(t)
	ctx := WithAgentID(WithToolContext(context.Background(), "cli", "direct"), "mia")

	res := tool.Execute(ctx, map[string]any{
		"action":     "add",
		"message":    "summarize PRs",
		"at_seconds": float64(600),
	})
	if res.IsError {
		t.Fatalf("add failed: %s", res.ForLLM)
	}

	jobs := tool.cronService.ListJobs(true)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].AgentID != "mia" {
		t.Fatalf("owner = %q, want mia", jobs[0].AgentID)
	}
	if jobs[0].SessionMode != cron.SessionModeIsolated {
		t.Fatalf("session_mode = %q, want isolated (default)", jobs[0].SessionMode)
	}
}

// TestSchedule_ExplicitOwnerAndMode persists explicit owner/session_mode/timeout.
func TestSchedule_ExplicitOwnerAndMode(t *testing.T) {
	tool := newTestCronTool(t)
	ctx := WithAgentID(WithToolContext(context.Background(), "cli", "direct"), "mia")

	res := tool.Execute(ctx, map[string]any{
		"action":          "add",
		"message":         "standup",
		"every_seconds":   float64(3600),
		"owner":           "max",
		"session_mode":    "continue",
		"timeout_seconds": float64(120),
	})
	if res.IsError {
		t.Fatalf("add failed: %s", res.ForLLM)
	}

	jobs := tool.cronService.ListJobs(true)
	j := jobs[0]
	if j.AgentID != "max" || j.SessionMode != cron.SessionModeContinue || j.TimeoutSeconds != 120 {
		t.Fatalf("got owner=%q mode=%q timeout=%d, want max/continue/120", j.AgentID, j.SessionMode, j.TimeoutSeconds)
	}
}

// TestSchedule_InvalidSessionModeRejected rejects an unknown session_mode.
func TestSchedule_InvalidSessionModeRejected(t *testing.T) {
	tool := newTestCronTool(t)
	ctx := WithAgentID(WithToolContext(context.Background(), "cli", "direct"), "mia")
	res := tool.Execute(ctx, map[string]any{
		"action":       "add",
		"message":      "x",
		"at_seconds":   float64(60),
		"session_mode": "bogus",
	})
	if !res.IsError {
		t.Fatal("expected invalid session_mode to be rejected")
	}
}

// TestExecuteJob_ReturnsErrorOnAgentFailure verifies ExecuteJob propagates a run
// failure as an error (W-4 / M-2), not a stringified success.
func TestExecuteJob_ReturnsErrorOnAgentFailure(t *testing.T) {
	exec := &fakeExecutor{err: fmt.Errorf("provider down")}
	tool := newCronToolWithExecutor(t, exec)

	job := &cron.CronJob{ID: "j1"}
	job.Payload.Message = "do it"
	job.Payload.Channel = "cli"
	job.Payload.To = "direct"

	sess, err := tool.ExecuteJob(context.Background(), job)
	if err == nil {
		t.Fatal("expected ExecuteJob to return an error on agent failure")
	}
	if sess == "" {
		t.Error("expected a linked session id even on failure")
	}
	if exec.calls != 1 {
		t.Errorf("executor calls = %d, want 1", exec.calls)
	}
}

// TestExecuteJob_SuccessReturnsSession verifies the happy path returns a session
// id and no error.
func TestExecuteJob_SuccessReturnsSession(t *testing.T) {
	exec := &fakeExecutor{reply: "done"}
	tool := newCronToolWithExecutor(t, exec)

	job := &cron.CronJob{ID: "j2"}
	job.Payload.Message = "do it"
	job.Payload.Channel = "cli"
	job.Payload.To = "direct"

	sess, err := tool.ExecuteJob(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess != "cron-j2" {
		t.Errorf("session id = %q, want cron-j2", sess)
	}
}

// TestExecuteJob_DeliverTrueNoAgentTurn verifies deliver=true sends directly and
// does not invoke the agent (FR-014).
func TestExecuteJob_DeliverTrueNoAgentTurn(t *testing.T) {
	exec := &fakeExecutor{}
	tool := newCronToolWithExecutor(t, exec)

	job := &cron.CronJob{ID: "j3"}
	job.Payload.Message = "hello"
	job.Payload.Channel = "cli"
	job.Payload.To = "direct"
	job.Payload.Deliver = true

	_, err := tool.ExecuteJob(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exec.calls != 0 {
		t.Errorf("deliver=true should not invoke the agent, calls=%d", exec.calls)
	}

	// Drain the published message.
	select {
	case msg := <-tool.msgBus.OutboundChan():
		if msg.Content != "hello" {
			t.Errorf("delivered content = %q, want hello", msg.Content)
		}
	default:
		t.Error("expected an outbound message for deliver=true")
	}
}
