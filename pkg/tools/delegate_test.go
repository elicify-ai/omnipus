package tools

import (
	"context"
	"strings"
	"testing"
)

func TestDelegateTool_Metadata(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	if got := tool.Name(); got != "delegate" {
		t.Fatalf("Name() = %q, want delegate", got)
	}
	if got := tool.Description(); got == "" || !strings.Contains(got, "returns its session_id") {
		t.Fatalf("Description() must explain immediate session launch, got %q", got)
	}

	params := tool.Parameters()
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("Parameters().properties is not an object")
	}
	for _, name := range []string{"task", "label", "agent_id", "action", "task_id", "session_id", "snapshot", "requested_skill", "timeout_seconds", "critical"} {
		if _, found := props[name]; !found {
			t.Errorf("Parameters() is missing %q", name)
		}
	}
	for _, retired := range []string{"async", "allow_blocking_question"} {
		if _, found := props[retired]; found {
			t.Errorf("Parameters() still publishes retired argument %q", retired)
		}
	}
}

func TestDelegateTool_ExecuteRejectsInvalidRunInput(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{name: "missing task", args: map[string]any{"label": "test"}, want: "task is required"},
		{name: "blank task", args: map[string]any{"task": " \t\n"}, want: "task is required"},
		{name: "wrong task type", args: map[string]any{"task": 123}, want: "task is required"},
		{name: "retired async", args: map[string]any{"task": "work", "async": true}, want: "invalid_argument: async"},
		{name: "invalid action", args: map[string]any{"action": "bogus"}, want: "invalid action"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.Execute(context.Background(), tc.args)
			if result == nil || !result.IsError || !strings.Contains(result.ForLLM, tc.want) {
				t.Fatalf("Execute(%v) = %+v, want error containing %q", tc.args, result, tc.want)
			}
		})
	}
}

func TestDelegateTool_StatusRejectsNonStringTaskID(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	result := tool.Execute(context.Background(), map[string]any{"action": "status", "task_id": 42})
	if result == nil || !result.IsError || !strings.Contains(result.ForLLM, "task_id must be a string") {
		t.Fatalf("status result = %+v, want task_id type error", result)
	}
}

func TestDelegateTool_KillSwitchGatesMessagingActionsOnly(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetSessionMessagingEnabled(func() bool { return false })
	for _, action := range []string{"inbox", "inbox_ack", "steer", "respond", "cancel", "follow_up", "peek"} {
		result := tool.Execute(context.Background(), map[string]any{"action": action})
		if result == nil || !strings.Contains(result.ForLLM, "session-messaging plane is disabled") {
			t.Fatalf("action %q result = %+v, want disabled-plane error", action, result)
		}
	}

	result := tool.Execute(context.Background(), map[string]any{"action": "status", "task_id": "missing"})
	if result == nil || strings.Contains(result.ForLLM, "session-messaging plane is disabled") {
		t.Fatalf("status must not be gated by the messaging kill switch: %+v", result)
	}
}
