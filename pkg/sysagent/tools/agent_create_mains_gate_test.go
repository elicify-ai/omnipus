package systools_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/session"

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
		"name": "Mains Bot", "description": "joins a team", "soul": "You help.", "model": "test/model", "color": "#6E8BA8",
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
		"name": "Loose Bot", "description": "no team", "soul": "You help.", "model": "test/model", "color": "#6E8BA8",
	})
	if res.IsError {
		t.Fatalf("create failed: %s", res.ForLLM)
	}
	if called {
		t.Fatal("a metadata-only agent has no team to provision mains for")
	}
}

// Final-gate H1, tool side: the shared delete cascade refuses a stale revision
// before any destructive step. delete_agent and DELETE /agents/{id} both run it.
func TestRunAgentDeleteCascade_StaleRevisionDestroysNothing(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	res := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name": "Cascade Bot", "description": "to be deleted", "soul": "You help.", "model": "test/model", "color": "#6E8BA8",
	})
	if res.IsError {
		t.Fatalf("create failed: %s", res.ForLLM)
	}
	agentID, _ := parseSuccess(t, res.ForLLM)["id"].(string)
	if agentID == "" {
		t.Fatal("create result missing id")
	}
	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	out := systools.RunAgentDeleteCascade(home, agentID, strings.Repeat("0", 64), nil, nil)

	if !errors.Is(out.DeletionErr, agentstore.ErrRevisionConflict) {
		t.Fatalf("DeletionErr = %v, want a revision conflict", out.DeletionErr)
	}
	if out.SessionsDeleted != 0 || out.TasksUnassigned != 0 {
		t.Fatalf("a refused delete must clean nothing: sessions=%d tasks=%d", out.SessionsDeleted, out.TasksUnassigned)
	}
	if _, err := os.Stat(filepath.Join(home, "sessions", meta.ID)); err != nil {
		t.Fatalf("the agent's session must survive a refused delete: %v", err)
	}
}
