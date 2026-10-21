package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// Wiring for legacy keeper tests that now drive a REAL reminder outcome.
// Never forge a running record to make an old notification-only fixture pass.
func grPrepareLegacyReminderRuntime(t *testing.T, al *AgentLoop) {
	t.Helper()
	if al.GetSessionLifecycleStore() == nil {
		installTestSessionLauncher(t, al)
	}
	pwWireRuntime(t, al)
}

func grTakePublishedReminder(t *testing.T, al *AgentLoop, sid string) bus.InboundMessage {
	t.Helper()
	select {
	case msg := <-al.bus.InboundChan():
		if msg.Channel != "system" || msg.AsyncTranscriptSessionID != sid || msg.Sender.CanonicalID != "system:goal_loop" {
			t.Fatalf("keeper published %+v, want one real system goal reminder for %s", msg, sid)
		}
		return msg
	default:
		t.Fatalf("keeper published no real reminder for %s", sid)
		return bus.InboundMessage{}
	}
}

func grNoPublishedReminder(t *testing.T, al *AgentLoop) {
	t.Helper()
	select {
	case msg := <-al.bus.InboundChan():
		t.Fatalf("keeper must publish no reminder, got %+v", msg)
	default:
	}
}

func grDrivePublishedReminder(t *testing.T, al *AgentLoop, sid string) {
	t.Helper()
	msg := grTakePublishedReminder(t, al, sid)
	if _, err := al.processSystemMessage(context.Background(), msg); err != nil {
		t.Fatalf("real published keeper reminder failed: %v", err)
	}
}
