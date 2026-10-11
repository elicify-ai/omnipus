package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Final-gate H2: an agent that create_agent adds to a workspace team must get
// its main chat through the same provisioning path workspace saves use. The tool
// cannot reach the gateway's provisioner directly, so Deps carries it as
// EnsureWorkspaceMains; the tool calls it for the workspace it joined, and only
// then.
func TestCreateAgent_InContext_ProvisionsTheTeamMains(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	var calls []string
	deps.EnsureWorkspaceMains = func(workspaceID string) error { calls = append(calls, workspaceID); return nil }
	wsID := "mains-ws"
	if err := os.MkdirAll(filepath.Join(home, "workspaces"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"` + wsID + `","name":"ws","status":"active","core_team":[]}`
	if err := os.WriteFile(filepath.Join(home, "workspaces", wsID+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	res := systools.NewAgentCreateTool(deps).Execute(tools.WithWorkspaceID(context.Background(), wsID), map[string]any{
		"name": "Mains Bot", "description": "joins a team", "soul": "You help.", "model": "test/model", "color": "#22C55E",
	})
	if res.IsError {
		t.Fatalf("create failed: %s", res.ForLLM)
	}
	if len(calls) != 1 || calls[0] != wsID {
		t.Fatalf("EnsureWorkspaceMains calls = %v, want exactly [%q]: the new member's main must be provisioned", calls, wsID)
	}
}

// Control: no workspace context means a metadata-only agent: nothing to provision.
func TestCreateAgent_NoWorkspace_ProvisionsNothing(t *testing.T) {
	deps, _ := newTestDepsWithHome(t)
	called := false
	deps.EnsureWorkspaceMains = func(string) error { called = true; return nil }

	res := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name": "Loose Bot", "description": "no team", "soul": "You help.", "model": "test/model", "color": "#22C55E",
	})
	if res.IsError {
		t.Fatalf("create failed: %s", res.ForLLM)
	}
	if called {
		t.Fatal("a metadata-only agent has no team to provision mains for")
	}
}
