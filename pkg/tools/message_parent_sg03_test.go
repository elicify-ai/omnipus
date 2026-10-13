package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// SG-03 sweep of the message_parent half of the message-tool family: a
// server-side failure reaches the child as fixed wording; the path and the OS
// cause stay in the log. The typed cause remains on ToolResult.Err for control
// flow.

type failingUpwardDeliverer struct{ err error }

func (f failingUpwardDeliverer) Deliver(context.Context, steer.UpwardEvent) (steer.Delivery, error) {
	return steer.Delivery{}, f.err
}

func sg03NoLeak(t *testing.T, text string, frags ...string) {
	t.Helper()
	lower := strings.ToLower(text)
	for _, f := range append([]string{"permission denied", "/secret/", ".jsonl", "no such file", "/var/", "/tmp"}, frags...) {
		if strings.Contains(lower, strings.ToLower(f)) {
			t.Errorf("the model-facing text must not carry %q: %q", f, text)
		}
	}
}

func TestSG03_MessageParent_DeliveryFailureIsFixedText(t *testing.T) {
	lc := session.NewLifecycleStore(t.TempDir())
	tool := NewMessageParentTool(failingUpwardDeliverer{
		err: errors.New("inbox append: open /secret/home/inbox/parent-1.jsonl: permission denied")}, lc)
	tool.SetSessionMessagingEnabled(func() bool { return true })
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-1", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: "parent-delegate-id",
		SteeredBy:   &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatal(err)
	}

	res := tool.Execute(withChildContext("child-1"), map[string]any{"kind": "progress", "text": "halfway", "pct": 50})

	if !res.IsError {
		t.Fatalf("a failed delivery must be an error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "could not be delivered") {
		t.Errorf("the failure must stay actionable: %q", res.ForLLM)
	}
	sg03NoLeak(t, res.ForLLM+" "+res.ForUser)
	if res.Err == nil {
		t.Error("the typed cause stays on Err for control flow")
	}
}

func TestSG03_MessageParent_UnreadableSessionRecordIsFixedText(t *testing.T) {
	dir := t.TempDir()
	lc := session.NewLifecycleStore(dir)
	tool := NewMessageParentTool(failingUpwardDeliverer{}, lc)
	tool.SetSessionMessagingEnabled(func() bool { return true })
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-1", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: "parent-delegate-id",
		SteeredBy:   &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(dir, "child-1.jsonl")
	if err := os.Chmod(journal, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(journal, 0o600) })

	res := tool.Execute(withChildContext("child-1"), map[string]any{"kind": "progress", "text": "halfway", "pct": 50})

	if !res.IsError {
		t.Fatalf("an unreadable record must refuse: %s", res.ForLLM)
	}
	sg03NoLeak(t, res.ForLLM, dir, "child-1")
}
