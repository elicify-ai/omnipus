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
	if got := tool.Description(); got == "" || !strings.Contains(got, "returns at once with the child's session_id") {
		t.Fatalf("Description() must explain immediate session launch, got %q", got)
	}

	params := tool.Parameters()
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("Parameters().properties is not an object")
	}
	for _, name := range []string{"task", "label", "agent_id", "action", "session_id", "criteria", "dod", "snapshot", "requested_skill", "timeout_seconds", "critical"} {
		if _, found := props[name]; !found {
			t.Errorf("Parameters() is missing %q", name)
		}
	}
	for _, retired := range []string{"async", "allow_blocking_question", "task_id", "goal"} {
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
		{name: "retired async", args: map[string]any{"task": "work", "agent_id": "worker", "async": true}, want: "invalid_argument: async"},
		{name: "invalid action", args: map[string]any{"action": "bogus"}, want: "invalid action"},
		// ADR-20261004: follow_up and cancel are gone with NO alias — both must
		// hit the invalid-action error, and that error must point at the new
		// verbs (resume/redirect/stop_all), never echo the retired ones.
		{name: "retired verb follow_up has no alias", args: map[string]any{"action": "follow_up", "session_id": "s"}, want: `invalid action "follow_up"`},
		{name: "retired verb cancel has no alias", args: map[string]any{"action": "cancel", "session_id": "s"}, want: `invalid action "cancel"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.Execute(context.Background(), tc.args)
			if result == nil || !result.IsError || !strings.Contains(result.ForLLM, tc.want) {
				t.Fatalf("Execute(%v) = %+v, want error containing %q", tc.args, result, tc.want)
			}
		})
	}
}

// TestDelegateTool_InvalidActionErrorNamesTheNewVerbs pins the error text the
// agent reads when it uses a retired verb: it must advertise the CURRENT
// action set (stop_all/resume/redirect among them) and must not echo
// follow_up or cancel back as if they were choices (ADR-20261004: no alias,
// and the tool text must not teach the old steering verbs).
func TestDelegateTool_InvalidActionErrorNamesTheNewVerbs(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	result := tool.Execute(context.Background(), map[string]any{"action": "cancel", "session_id": "s"})
	if result == nil || !result.IsError {
		t.Fatalf("retired verb must be refused, got: %+v", result)
	}
	for _, verb := range []string{"stop_all", "resume", "redirect"} {
		if !strings.Contains(result.ForLLM, verb) {
			t.Errorf("invalid-action error must name the current verb %q, got: %s", verb, result.ForLLM)
		}
	}
	// The verb LIST (after "must be one of") must not offer the retired verbs;
	// the offending action's own name is echoed before that marker.
	choices := result.ForLLM
	if _, after, found := strings.Cut(result.ForLLM, "must be one of"); found {
		choices = after
	}
	for _, retired := range []string{"cancel", "follow_up"} {
		if strings.Contains(choices, retired) {
			t.Errorf("invalid-action error must not offer the retired verb %q as a choice, got: %s", retired, result.ForLLM)
		}
	}
}

func TestDelegateTool_StatusRequiresSessionID(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	result := tool.Execute(context.Background(), map[string]any{"action": "status"})
	if result == nil || !result.IsError || !strings.Contains(result.ForLLM, "session_id is required") {
		t.Fatalf("status result = %+v, want a session_id-required error — session_id is the only way to "+
			"address a child post-ADR-091", result)
	}
}

func TestDelegateTool_KillSwitchGatesMessagingActionsOnly(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetSessionMessagingEnabled(func() bool { return false })
	// The full isSessionMessagingAction set (delegate.go): every
	// session-messaging-plane action is gated; run/status are not.
	for _, action := range []string{"inbox", "inbox_ack", "steer", "respond", "stop_all", "clear_goal", "resume", "redirect", "peek"} {
		result := tool.Execute(context.Background(), map[string]any{"action": action})
		if result == nil || !strings.Contains(result.ForLLM, "session-messaging plane is disabled") {
			t.Fatalf("action %q result = %+v, want disabled-plane error", action, result)
		}
	}

	result := tool.Execute(context.Background(), map[string]any{"action": "status", "session_id": "missing"})
	if result == nil || strings.Contains(result.ForLLM, "session-messaging plane is disabled") {
		t.Fatalf("status must not be gated by the messaging kill switch: %+v", result)
	}
}
