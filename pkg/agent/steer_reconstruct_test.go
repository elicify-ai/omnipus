package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestReconstructSteeredTurn_UsesChildTranscriptAndAddress(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	launcher := NewSteerLauncher(al)
	parentID := newTestSteeringSession(t, al, "ws-1")

	launched, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     testDefaultAgentID,
		Task:              "inspect the child transcript",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-reconstruct"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := al.GetSessionLifecycleStore().Load(launched.SessionID)
	if err != nil {
		t.Fatalf("Load child lifecycle: %v", err)
	}
	// Make the upward address visibly different from the child's own meta.
	rec.SteeredBy.ReportingTarget.Channel = "telegram"
	rec.SteeredBy.ReportingTarget.ChatID = "parent-chat"

	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	if ts.transcriptSessionID != launched.SessionID {
		t.Fatalf("transcript session = %q, want child %q", ts.transcriptSessionID, launched.SessionID)
	}
	if ts.transcriptStore != al.GetSessionStore() {
		t.Fatal("transcript store is not the shared unified session store")
	}
	if ts.channel == "telegram" || ts.chatID == "parent-chat" {
		t.Fatalf("child execution address reused upward reporting target: channel=%q chat=%q", ts.channel, ts.chatID)
	}
}

// TestReconstructSteeredTurn_ExcludedToolCannotBeCalled is finding 2's
// required test (ADR-091 seven-reviewer gate, 2026-09): SteeredBy.
// ToolExclusions was set by the delegate tool, persisted, exposed on the
// wire, and asserted by a serialisation test — but never actually READ at
// reconstruction, so a delegated child could still call switch_agent, which
// D2 and the long-standing identity rule forbid. This drives a REAL launch
// (through SteerLauncher.Launch, which sets LifecycleRecord.SteeredBy.
// ToolExclusions from LaunchRequest.ToolExclusions — steer_launcher.go)
// followed by a real reconstructSteeredTurn, then proves both halves of the
// fix: the excluded tool is gone from the CHILD's own turn, and every OTHER
// session sharing the same agent (including the parent that launched this
// child) is completely unaffected — the fix must not mutate the shared,
// process-wide agent registry.
func TestReconstructSteeredTurn_ExcludedToolCannotBeCalled(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	launcher := NewSteerLauncher(al)
	parentID := newTestSteeringSession(t, al, "ws-1")

	launched, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     testDefaultAgentID,
		Task:              "do the thing, but never switch agents",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-exclusion"},
		ToolExclusions:    []string{"switch_agent"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := al.GetSessionLifecycleStore().Load(launched.SessionID)
	if err != nil {
		t.Fatalf("Load child lifecycle: %v", err)
	}
	if len(rec.SteeredBy.ToolExclusions) == 0 {
		t.Fatal("precondition failed: Launch did not persist ToolExclusions onto the lifecycle record")
	}

	// Sanity check: the SHARED agent instance (what every session using
	// testDefaultAgentID sees before any reconstruction) still has
	// switch_agent registered — proves the fixture actually exercises the
	// exclusion rather than testing an agent that never had the tool.
	sharedAgent, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatalf("test agent %q not found in registry", testDefaultAgentID)
	}
	if _, ok := sharedAgent.Tools.Get("switch_agent"); !ok {
		t.Fatal("precondition failed: switch_agent is not registered on the test agent at all")
	}

	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}

	// The child's OWN turn must not be able to reach switch_agent.
	if _, ok := ts.agent.Tools.Get("switch_agent"); ok {
		t.Fatal("BUG REGRESSION: a child launched with switch_agent excluded can still Get() it")
	}
	result := ts.agent.Tools.ExecuteWithContext(context.Background(), "switch_agent", map[string]any{}, "", "", nil)
	if !result.IsError {
		t.Fatal("BUG REGRESSION: switch_agent executed successfully for a child that excludes it")
	}
	if !strings.Contains(result.ForLLM, "switch_agent") {
		t.Fatalf("refusal must name the excluded tool; got %q", result.ForLLM)
	}

	// The SHARED agent instance (e.g. what the PARENT's own turn, or any
	// sibling child, would use) must be completely untouched.
	if _, ok := sharedAgent.Tools.Get("switch_agent"); !ok {
		t.Fatal("BUG REGRESSION: excluding a tool for one child leaked onto the shared agent instance")
	}
}
