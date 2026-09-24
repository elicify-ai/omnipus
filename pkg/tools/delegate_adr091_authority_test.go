package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func adr091SteeredRecord(id, parent string) *session.LifecycleRecord {
	return &session.LifecycleRecord{
		SessionID:      id,
		Generation:     1,
		AgentID:        "worker",
		ParentAgentID:  "parent-agent",
		WorkspaceID:    "ws-1",
		OwnerScopeKind: session.OwnerScopeHuman,
		State:          session.LifecycleRunning,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: parent, RootSessionID: parent},
	}
}

func TestSteeringAuthority_AncestorHumanAndOutsider(t *testing.T) {
	store := session.NewLifecycleStore(t.TempDir())
	tool := NewDelegateTool("test", 0, 0)
	tool.SetLifecycleStore(store)
	tool.SetOwnershipWalkMaxDepth(8)

	parent := adr091SteeredRecord("parent", "root")
	if err := store.Persist(parent); err != nil {
		t.Fatal(err)
	}
	target := adr091SteeredRecord("target", "parent")

	for name, ctx := range map[string]context.Context{
		"parent":      WithTranscriptSessionID(context.Background(), "parent"),
		"grandparent": WithTranscriptSessionID(context.Background(), "root"),
		"human": WithDelegatePrincipal(context.Background(), steer.Principal{
			Kind: steer.PrincipalKindHuman, ID: "founder",
		}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tool.verifyCallerPrincipal(ctx, target); err != nil {
				t.Fatalf("authorized principal rejected: %v", err)
			}
		})
	}

	for _, caller := range []string{"sibling", "unrelated"} {
		if _, err := tool.verifyCallerPrincipal(WithTranscriptSessionID(context.Background(), caller), target); err == nil {
			t.Fatalf("caller %q unexpectedly authorized", caller)
		}
	}
}

func TestSteer_ExternalSessionReturnsNamedError(t *testing.T) {
	tool, store, _, _ := newADR053TestTool(t)
	rec := adr091SteeredRecord("external", "parent")
	rec.Is3P = true
	if err := store.Persist(rec); err != nil {
		t.Fatal(err)
	}
	result := tool.Execute(WithTranscriptSessionID(context.Background(), "parent"), map[string]any{
		"action": "steer", "session_id": "external", "text": "continue",
	})
	if !result.IsError || !strings.Contains(result.ForLLM, "not_steerable") {
		t.Fatalf("got (%v, %q), want named not_steerable error", result.IsError, result.ForLLM)
	}
}

func TestSteer_DeliversInArrivalOrder(t *testing.T) {
	tool, store, _, sink := newADR053TestTool(t)
	if err := store.Persist(adr091SteeredRecord("child", "parent")); err != nil {
		t.Fatal(err)
	}
	ctx := WithTranscriptSessionID(context.Background(), "parent")
	for _, text := range []string{"first", "second"} {
		result := tool.Execute(ctx, map[string]any{"action": "steer", "session_id": "child", "text": text})
		if result.IsError {
			t.Fatalf("steer %q: %s", text, result.ForLLM)
		}
	}
	if len(sink.delivered) != 2 || sink.delivered[0].Content != "first" || sink.delivered[1].Content != "second" {
		t.Fatalf("delivery order = %#v", sink.delivered)
	}
}

func TestSteer_VerifiesAuthorityBeforeEnqueue(t *testing.T) {
	tool, store, _, sink := newADR053TestTool(t)
	if err := store.Persist(adr091SteeredRecord("child", "parent-session")); err != nil {
		t.Fatal(err)
	}
	outsider := WithTranscriptSessionID(context.Background(), "outsider-session")
	result := tool.Execute(outsider, map[string]any{"action": "steer", "session_id": "child", "text": "intrude"})
	if !result.IsError {
		t.Fatal("unauthorized steering unexpectedly succeeded")
	}
	if len(sink.delivered) != 0 {
		t.Fatalf("unauthorized steering enqueued %d messages", len(sink.delivered))
	}

	ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "parent-session"), "shared-agent-profile")
	result = tool.Execute(ctx, map[string]any{"action": "steer", "session_id": "child", "text": "continue"})
	if result.IsError {
		t.Fatalf("steer: %s", result.ForLLM)
	}
	if len(sink.delivered) != 1 || sink.delivered[0].Content != "continue" {
		t.Fatalf("authorized steering deliveries = %#v, want one verified message", sink.delivered)
	}
}
