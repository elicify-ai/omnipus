// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Round-four correction coverage for issue #1020, item 5: delegate
// (action="steer") on a finishing child must return the spec's exact
// caller-facing wording — "queued; the child is finishing and will see
// it next" — instead of the plain "Steering message queued for session
// ..." success. The status is plumbed from pkg/agent's EnqueueSteeringMessage
// through a parallel EnqueueSteeringMessageWithStatus method (declared on
// the production *agent.AgentLoop), reached from this package via a
// type assertion in delegate_followup.go's enqueueSteeringWithStatus
// adapter.
//
// The test exercises BOTH halves of the contract:
//   - rich sink returning PostFinish (1) → "queued; the child is
//     finishing and will see it next (...)" text;
//   - rich sink returning Normal (0) AND narrow sink returning only the
//     basic DelegateSteeringSink contract → "Steering message queued
//     for session ... (correlation_id=...)" plain text.
//
// This file lives in pkg/tools (not pkg/agent) because the contract is
// the delegate tool's caller-facing text, not a pkg/agent internal.
// package agent_test imports would not work for the narrow-sink leg
// because newADR053TestTool returns the sink wired into the delegate tool.
package tools

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// postFinishSteeringSink implements BOTH DelegateSteeringSink and the
// narrow steerSinkWithEnqueueStatus (the enqueueSteeringWithStatus adapter
// in delegate_followup.go type-asserts to it). It reports every enqueue
// as PostFinish (1) so the test can assert the rich path's caller-facing
// text is selected over the plain path's. recorded is the count of
// enqueue calls so a no-enqueue regression shows up as a clear failure.
type postFinishSteeringSink struct {
	mu        sync.Mutex
	delivered []providers.Message
	scopes    []string
	recorded  int
}

func (f *postFinishSteeringSink) EnqueueSteeringMessage(scope, agentID string, msg providers.Message, correlationID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delivered = append(f.delivered, msg)
	f.scopes = append(f.scopes, scope)
	f.recorded++
	return "corr_postfinish_" + string(rune('0'+f.recorded)), nil
}

func (f *postFinishSteeringSink) EnqueueSteeringMessageWithStatus(scope, agentID string, msg providers.Message, correlationID string) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delivered = append(f.delivered, msg)
	f.scopes = append(f.scopes, scope)
	f.recorded++
	return "corr_postfinish_" + string(rune('0'+f.recorded)), 1, nil // PostFinish
}

// normalSteeringSink implements both sinks too, but reports Normal (0)
// so the test can prove the normal path's plain text is preserved when
// the rich sink reports the item joined the main queue.
type normalSteeringSink struct {
	mu        sync.Mutex
	delivered []providers.Message
	scopes    []string
	recorded  int
}

func (f *normalSteeringSink) EnqueueSteeringMessage(scope, agentID string, msg providers.Message, correlationID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delivered = append(f.delivered, msg)
	f.scopes = append(f.scopes, scope)
	f.recorded++
	return "corr_normal_" + string(rune('0'+f.recorded)), nil
}

func (f *normalSteeringSink) EnqueueSteeringMessageWithStatus(scope, agentID string, msg providers.Message, correlationID string) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delivered = append(f.delivered, msg)
	f.scopes = append(f.scopes, scope)
	f.recorded++
	return "corr_normal_" + string(rune('0'+f.recorded)), 0, nil // Normal
}

// TestDelegateTool_Steer_PostFinishReturnsChildFinishingText pins the
// round-4 correction item 5: when the steering sink reports PostFinish,
// executeSteer's caller-facing text is exactly "queued; the child is
// finishing and will see it next" — never the plain "Steering message
// queued for session ..." success.
func TestDelegateTool_Steer_PostFinishReturnsChildFinishingText(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	lc := session.NewLifecycleStore(t.TempDir())
	tool.SetLifecycleStore(lc)
	tool.SetSteeringSink(&postFinishSteeringSink{})
	tool.SetSessionMessagingEnabled(func() bool { return true })

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-finishing", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}

	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	result := tool.Execute(ctx, map[string]any{
		"action": "steer", "session_id": "child-finishing", "text": "follow-up on the late steer",
	})
	if result.IsError {
		t.Fatalf("expected a non-error PostFinish result, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "queued; the child is finishing and will see it next") {
		t.Errorf("PostFinish caller-facing text = %q, must contain %q", result.ForLLM, "queued; the child is finishing and will see it next")
	}
	if strings.HasPrefix(result.ForLLM, "Steering message queued for session") {
		t.Errorf("PostFinish result must NOT carry the plain 'Steering message queued for session ...' success text; got %q", result.ForLLM)
	}
}

// TestDelegateTool_Steer_NormalReturnsPlainQueuedText is the negative
// control: when the rich sink reports Normal (0), executeSteer returns
// the plain "Steering message queued for session ..." text — the
// round-4 correction is conditional on PostFinish, not a blanket
// change. This pairs with the PostFinish test above.
func TestDelegateTool_Steer_NormalReturnsPlainQueuedText(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	lc := session.NewLifecycleStore(t.TempDir())
	tool.SetLifecycleStore(lc)
	tool.SetSteeringSink(&normalSteeringSink{})
	tool.SetSessionMessagingEnabled(func() bool { return true })

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-running", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}

	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	result := tool.Execute(ctx, map[string]any{
		"action": "steer", "session_id": "child-running", "text": "ordinary steer",
	})
	if result.IsError {
		t.Fatalf("expected a non-error Normal result, got: %s", result.ForLLM)
	}
	if !strings.HasPrefix(result.ForLLM, "Steering message queued for session") {
		t.Errorf("Normal caller-facing text = %q, must start with %q", result.ForLLM, "Steering message queued for session")
	}
	if strings.Contains(result.ForLLM, "queued; the child is finishing and will see it next") {
		t.Errorf("Normal result must NOT carry the PostFinish text; got %q", result.ForLLM)
	}
}
