// switch_agent_todefault_frame_test.go — regression coverage for the two
// switch_agent bugs fixed alongside ADR-071 D4 (hand_off + return_to_default
// merged into switch_agent):
//
//  1. pkg/tools/handoff.go's "default" sentinel is now matched
//     case-insensitively (strings.EqualFold), matching the collision rule
//     pkg/gateway/rest.go already enforces at the agent create/update
//     boundary. Before the fix, switch_agent(target:"Default") (capital D)
//     fell through to a named-agent lookup, found nothing, and returned a
//     confusing "agent not found" error instead of routing to the default
//     agent.
//  2. The WS agent_switched frame builder (pkg/gateway/websocket.go) now
//     reports the tool's own toDefault intent (tools.HandoffEvent.ToDefault,
//     threaded through onHandoffFrontend to AgentLoop.lastSwitchToDefault /
//     GetLastSwitchToDefault) instead of re-deriving "was this a return to
//     default" by comparing the resulting active agent id against the
//     configured default agent id. That comparison misreported an explicit
//     switch_agent(target:"<id>") naming the CURRENT default agent as a
//     return-to-default.
//
// These tests exercise the tool through the real production wiring
// (mustNewAgentLoop -> wireExecToolDeps -> onHandoffFrontend), not a
// hand-rolled stub, so they cover the exact path the WS frame builder reads.
package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// buildSwitchAgentTestLoop constructs a real AgentLoop with two agents —
// "mia" (configured as the default agent) and "ray" — so switch_agent runs
// through the actual production wiring rather than a test double.
func buildSwitchAgentTestLoop(t *testing.T) *AgentLoop {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Name: "Mia", Type: config.AgentTypeCore},
		{ID: "ray", Name: "Ray", Type: config.AgentTypeCore},
	}
	cfg.Agents.Defaults.DefaultAgentID = "mia"

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })
	return al
}

// switchAgentTool resolves the real, production-wired switch_agent tool
// registered on the given agent.
func switchAgentTool(t *testing.T, al *AgentLoop, agentID string) tools.Tool {
	t.Helper()
	ag, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || ag == nil || ag.Tools == nil {
		t.Fatalf("agent %q not registered with a tool registry", agentID)
	}
	tool, ok := ag.Tools.Get("switch_agent")
	if !ok {
		t.Fatalf("switch_agent tool not registered on agent %q", agentID)
	}
	return tool
}

func switchAgentCtx(sessionID, chatID, callingAgentID string) context.Context {
	ctx := context.Background()
	ctx = tools.WithSessionKey(ctx, sessionID)
	ctx = tools.WithToolContext(ctx, "webchat", chatID)
	ctx = tools.WithAgentID(ctx, callingAgentID)
	return ctx
}

// newRealSessionID creates a REAL session in the AgentLoop's shared store —
// SwitchAgentTool.Execute's SwitchAgent step requires the session to already
// exist on disk (unified_store: read meta.json), so a bare literal session id
// with no backing session fails before ever reaching the toDefault logic
// this file exists to cover.
func newRealSessionID(t *testing.T, al *AgentLoop, agentID string) string {
	t.Helper()
	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("AgentLoop has no shared session store")
	}
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return meta.ID
}

// TestSwitchAgent_NamedSwitchToConfiguredDefaultAgent_NotFlaggedToDefault
// covers FIX 2: an explicit switch_agent(target:"mia") where Mia happens to
// be the configured default agent must be reported as a NAMED switch, not a
// return-to-default — GetLastSwitchToDefault must report false, not derive
// true from activeAgent == defaultAgentID.
func TestSwitchAgent_NamedSwitchToConfiguredDefaultAgent_NotFlaggedToDefault(t *testing.T) {
	al := buildSwitchAgentTestLoop(t)
	tool := switchAgentTool(t, al, "ray")

	sessionID := newRealSessionID(t, al, "ray")
	ctx := switchAgentCtx(sessionID, "chat-1", "ray")

	result := tool.Execute(ctx, map[string]any{"target": "mia", "note": "handing off"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	active, ok := al.GetSessionActiveAgent(sessionID)
	if !ok || active != "mia" {
		t.Fatalf("expected active agent mia, got %q (ok=%v)", active, ok)
	}

	toDefault, sawToDefault := al.GetLastSwitchToDefault(sessionID)
	if !sawToDefault {
		t.Fatal("expected a toDefault record after a successful switch_agent call")
	}
	if toDefault {
		t.Error("named switch_agent(target:\"mia\") must NOT be flagged toDefault just because mia is the configured default agent")
	}

	// One-shot semantics: a second read finds nothing left (LoadAndDelete).
	if _, sawAgain := al.GetLastSwitchToDefault(sessionID); sawAgain {
		t.Error("GetLastSwitchToDefault should be one-shot (LoadAndDelete)")
	}
}

// TestSwitchAgent_ReturnToDefault_MixedCaseTarget_FlaggedToDefault covers
// FIX 1 and FIX 2 together: switch_agent(target:"Default") (mixed case) must
// (a) resolve to the return-to-default branch rather than "agent not found",
// and (b) be flagged toDefault=true, the input the WS frame builder needs to
// emit the correct "returned to default" frame shape.
func TestSwitchAgent_ReturnToDefault_MixedCaseTarget_FlaggedToDefault(t *testing.T) {
	al := buildSwitchAgentTestLoop(t)
	tool := switchAgentTool(t, al, "ray")

	sessionID := newRealSessionID(t, al, "ray")
	ctx := switchAgentCtx(sessionID, "chat-1", "ray")

	result := tool.Execute(ctx, map[string]any{"target": "Default"})
	if result.IsError {
		t.Fatalf("expected mixed-case \"Default\" to resolve to the return-to-default branch, got error: %s", result.ForLLM)
	}

	active, ok := al.GetSessionActiveAgent(sessionID)
	if !ok || active != "mia" {
		t.Fatalf("expected active agent mia (the configured default), got %q (ok=%v)", active, ok)
	}

	toDefault, sawToDefault := al.GetLastSwitchToDefault(sessionID)
	if !sawToDefault {
		t.Fatal("expected a toDefault record after a successful switch_agent call")
	}
	if !toDefault {
		t.Error("switch_agent(target:\"Default\") must be flagged toDefault")
	}
}
