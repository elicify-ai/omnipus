package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Combined-review check from the navigation session (S4-01): a workspace member
// added by create_agent's team join must change the workspace revision the SPA
// uses for 409 conflict detection, or a stale client could overwrite the new
// member. ORACLE: the PUT contract - the revision identifies the state read.
func TestCreateAgent_TeamJoinChangesTheWorkspaceRevision(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	deps.EnsureWorkspaceMains = func(string) error { return nil }
	const wsID = "rev-ws"
	if err := os.MkdirAll(filepath.Join(home, "workspaces"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"` + wsID + `","name":"ws","status":"active","core_team":[]}`
	if err := os.WriteFile(filepath.Join(home, "workspaces", wsID+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := workspacepkg.ReadState(home, wsID)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := workspacepkg.ReadState(home, wsID)
	if again.Revision != before.Revision {
		t.Fatal("instrument check: reading twice must give the same revision")
	}

	res := systools.NewAgentCreateTool(deps).Execute(tools.WithWorkspaceID(context.Background(), wsID), map[string]any{
		"name": "Joiner", "description": "joins", "soul": "You help.", "model": "test/model", "color": "#6E8BA8",
	})
	if res.IsError {
		t.Fatalf("create failed: %s", res.ForLLM)
	}
	after, err := workspacepkg.ReadState(home, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Workspace.CoreTeam) != 1 {
		t.Fatalf("core_team = %v, want the new member", after.Workspace.CoreTeam)
	}
	if after.Revision == before.Revision {
		t.Fatal("a member was added but the workspace revision did not change: a stale client's PUT would overwrite it")
	}
}
