package tools

// Typed cancellation capability boundary, not a runtime-admission fixture.
// D2/T27 and e275's published SoftCancelFunc contract: an unwired soft
// capability is a visible error, never a fresh hard fallback or state write.
import (
	"context"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestDelegateStopAll_UnwiredSoftCapabilityRefusesWithoutStateOrControlWrite(t *testing.T) {
	tool, lifecycle, _, _ := newADR053TestTool(t)
	// Reuse the existing tool-boundary record shape; no execution/control
	// identity or real runtime-admission claim is invented by this fixture.
	const child = "child-cancel"
	if err := lifecycle.Persist(&session.LifecycleRecord{
		SessionID: child, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID:    "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed existing tool-boundary record: %v", err)
	}
	before, err := lifecycle.Load(child)
	if err != nil {
		t.Fatalf("load before refused capability: %v", err)
	}
	beforeEffects, err := lifecycle.AcceptedStopEffects(child)
	if err != nil {
		t.Fatalf("read original control inventory: %v", err)
	}
	called := false
	tool.SetCancelHooks(nil, func(string, steer.Principal, string) ([]string, error) {
		called = true
		return []string{child}, nil
	})
	result := tool.Execute(WithTranscriptSessionID(context.Background(), "parent-1"),
		map[string]any{"action": "stop_all", "session_id": child, "hard": false})
	if result == nil || !result.IsError || result.ForLLM != "delegate: no soft-cancel hook configured" {
		t.Fatalf("unwired soft capability result=%+v, want exact visible error", result)
	}
	if called {
		t.Error("unwired soft capability invoked a fresh hard fallback")
	}
	after, err := lifecycle.Load(child)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Errorf("refused unwired capability changed original lifecycle: before=%+v after=%+v err=%v", before, after, err)
	}
	afterEffects, err := lifecycle.AcceptedStopEffects(child)
	if err != nil || !reflect.DeepEqual(afterEffects, beforeEffects) {
		t.Errorf("refused unwired capability accepted a control: before=%+v after=%+v err=%v", beforeEffects, afterEffects, err)
	}
}
