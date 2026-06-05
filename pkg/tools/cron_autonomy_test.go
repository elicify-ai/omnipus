package tools

import (
	"context"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/cron"
)

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
