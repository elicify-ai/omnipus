// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// Founder rule (2026-10-06): a session does not fail because of a restart.
// What an agent reads through list_jobs and delegate status/peek for a
// failed(interrupted) record must therefore say "interrupted", while a
// genuinely failed record (any other failed_reason) still says "failed".

func TestNormalizeSubagent_RestartInterruptedIsNotFailed(t *testing.T) {
	got := normalizeSubagent(&session.LifecycleRecord{
		State: session.LifecycleFailed, FailedReason: session.FailedReasonInterrupted,
	})
	if got.status == jobStatusFailed {
		t.Fatalf("a restart-cut session must not normalise to %q", jobStatusFailed)
	}
	if got.status != jobStatusBlocked || got.attention != attentionCaller {
		t.Errorf("restart-cut session: status/attention = %q/%q, want %q/%q (resumable by the caller)",
			got.status, got.attention, jobStatusBlocked, attentionCaller)
	}
	if strings.Contains(got.nativeStatus, "failed") || !strings.Contains(got.nativeStatus, "interrupted") {
		t.Errorf("native_status = %q, want it to say interrupted and never failed", got.nativeStatus)
	}
	if got.stopped || got.unmapped {
		t.Errorf("stopped/unmapped = %v/%v, want false/false", got.stopped, got.unmapped)
	}

	genuine := normalizeSubagent(&session.LifecycleRecord{
		State: session.LifecycleFailed, FailedReason: "judge_rounds_exhausted",
	})
	if genuine.status != jobStatusFailed || genuine.attention != attentionNone {
		t.Errorf("a genuine failure must stay failed/none, got %q/%q", genuine.status, genuine.attention)
	}
}

func TestDelegateStatusAndPeek_RestartInterruptedReadsInterruptedNotFailed(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	for id, reason := range map[string]string{
		"child-restart": session.FailedReasonInterrupted,
		"child-real":    "judge_rounds_exhausted",
	} {
		if err := lc.Persist(&session.LifecycleRecord{
			SessionID: id, Generation: 1, State: session.LifecycleFailed, FailedReason: reason,
			OwnerScopeKind: session.OwnerScopeHuman,
			SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
			WorkspaceID:    "ws-1", AgentID: "worker",
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	for id, want := range map[string]string{"child-restart": "interrupted", "child-real": "failed"} {
		status := tool.Execute(ctx, map[string]any{"action": "status", "session_id": id})
		if status.IsError || !strings.HasPrefix(status.ForLLM, want+",") {
			t.Errorf("status %s = %q (error=%v), want it to start with %q", id, status.ForLLM, status.IsError, want+",")
		}
		peek := tool.Execute(ctx, map[string]any{"action": "peek", "session_id": id})
		var resp struct {
			State string `json:"state"`
		}
		if peek.IsError || json.Unmarshal([]byte(peek.ForLLM), &resp) != nil || resp.State != want {
			t.Errorf("peek %s = %q (error=%v), want state %q", id, peek.ForLLM, peek.IsError, want)
		}
	}
}
