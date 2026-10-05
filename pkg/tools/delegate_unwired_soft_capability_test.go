package tools

// Typed stop capability boundary, not a runtime-admission fixture.
// D2/T27 plus the founder one-stop decision (2026-10-05): the tool has ONE stop
// hook; when it is not wired, stop_all is a visible error and the tool never
// falls back to its own state write or a second mechanism.
import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestDelegateStopAll_UnwiredStopHookRefusesWithoutStateOrControlWrite(t *testing.T) {
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
	// No stop hook is installed: the tool's one stop capability is unwired.
	result := tool.Execute(WithTranscriptSessionID(context.Background(), "parent-1"),
		map[string]any{"action": "stop_all", "session_id": child})
	if result == nil || !result.IsError {
		t.Fatalf("unwired stop hook result=%+v, want a visible error", result)
	}
	// Exact wording is the implementer's; it must name the missing stop
	// capability so the calling agent can tell what is wrong.
	if !strings.Contains(strings.ToLower(result.ForLLM), "stop") {
		t.Errorf("unwired stop hook error %q does not mention the stop capability", result.ForLLM)
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
