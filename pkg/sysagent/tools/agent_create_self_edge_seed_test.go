// agent_create_self_edge_seed_test.go — RED pack, session-core U5a condition 1:
// the in-context create writer. When create_agent runs inside a workspace's
// turn context it joins that workspace's core_team; under the U5a condition-1
// shape it must ALSO seed the new agent's ordinary self-row (one shared seed
// computation for every writer — seed-owner-decision ::consumed by ... create_agent's
// membership join).
//
// Current code: AgentCreateTool.joinWorkspaceTeam appends the agent to
// core_team and writes nothing to the delegation graph, so no self-row exists.

package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

func TestCreateAgent_InContext_SeedsSelfRow(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	wsID := "ctx-ws"
	wsDir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir workspaces: %v", err)
	}
	wsBody := `{"id":"` + wsID + `","name":"ws","status":"active","core_team":[]}`
	if err := os.WriteFile(filepath.Join(wsDir, wsID+".json"), []byte(wsBody), 0o644); err != nil {
		t.Fatalf("write workspace: %v", err)
	}

	ctx := tools.WithWorkspaceID(context.Background(), wsID)
	result := systools.NewAgentCreateTool(deps).Execute(ctx, map[string]any{
		"name":        "Ctx Bot",
		"description": "created in a workspace context",
		"soul":        "You help.",
		"model":       "test/model",
		"color":       "#22C55E",
	})
	if result.IsError {
		t.Fatalf("create failed: %s", result.ForLLM)
	}
	resp := parseSuccess(t, result.ForLLM)
	agentID, _ := resp["id"].(string)
	if agentID == "" {
		t.Fatal("create result missing id")
	}

	edges, ok := workspacepkg.LoadDelegation(home, wsID)
	if !ok {
		t.Fatalf("BLOCKED: no delegation graph was written for workspace %q — the in-context "+
			"create writer must seed the new agent's self-row (U5a condition-1 shape)", wsID)
	}
	for _, e := range edges {
		if e.FromAgent == agentID && e.ToAgent == agentID {
			return
		}
	}
	t.Fatalf("the newly created agent %q has no seeded self-row in workspace %q; edges = %+v",
		agentID, wsID, edges)
}
