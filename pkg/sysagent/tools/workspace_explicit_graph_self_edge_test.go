// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Session-core spec FR-014/015/016 (founder ruling 2026-10-10): every agent
// added to a workspace gets its ordinary self-edge, by every add path. An
// explicit graph carried in the same call must not suppress the self-edge of a
// member that call introduces.
package systools

import (
	"context"
	"testing"

	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

func explicitEdge(from, to string) map[string]any {
	return map[string]any{"from_agent": from, "to_agent": to, "modes": []any{"direct"}}
}

// TestUpdateWorkspace_ExplicitGraphStillSeedsNewMemberSelfEdge: update_workspace
// adds "mia" together with an explicit graph that lacks mia's self-line. mia's
// self-edge must exist afterwards; the continuing member jim, whose self-line
// the explicit graph deliberately omits, must NOT get one back (deletion is
// authoritative for continuing members); the explicit edge is kept verbatim.
func TestUpdateWorkspace_ExplicitGraphStillSeedsNewMemberSelfEdge(t *testing.T) {
	deps, _, home := u5aDeps(t, nil, "jim", "mia")
	wsID := "wu-explicit-ws"
	u5aWriteWorkspace(t, home, wsID, []string{"jim"})
	state, err := workspacepkg.ReadState(home, wsID)
	if err != nil {
		t.Fatalf("read workspace revision: %v", err)
	}

	res := NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":         wsID,
		"revision":   state.Revision,
		"core_team":  []any{"jim", "mia"},
		"delegation": []any{explicitEdge("jim", "mia")},
	})
	if res.IsError {
		t.Fatalf("update_workspace failed: %s", res.ForLLM)
	}
	if _, ok := u5aSelfEdge(t, home, wsID, "mia"); !ok {
		edges, _ := workspacepkg.LoadDelegation(home, wsID)
		t.Fatalf("newly added member mia has no self-edge after an update carrying an explicit graph; edges=%+v", edges)
	}
	if _, ok := u5aSelfEdge(t, home, wsID, "jim"); ok {
		t.Fatal("continuing member jim's self-line was omitted by the explicit graph; it must not be resurrected")
	}
	edges, _ := workspacepkg.LoadDelegation(home, wsID)
	found := false
	for _, e := range edges {
		if e.FromAgent == "jim" && e.ToAgent == "mia" {
			found = true
		}
	}
	if !found || len(edges) != 2 {
		t.Fatalf("explicit edge jim->mia must be kept alongside exactly one seeded self-edge; edges=%+v", edges)
	}
}

// TestUpdateWorkspace_ExplicitGraph_ExcludedAgentStillGetsNoSelfEdge keeps the
// operator exclusion data authoritative on the explicit-graph path.
func TestUpdateWorkspace_ExplicitGraph_ExcludedAgentStillGetsNoSelfEdge(t *testing.T) {
	deps, _, home := u5aDeps(t, []string{"mia"}, "jim", "mia")
	wsID := "wu-explicit-excl-ws"
	u5aWriteWorkspace(t, home, wsID, []string{"jim"})
	state, err := workspacepkg.ReadState(home, wsID)
	if err != nil {
		t.Fatalf("read workspace revision: %v", err)
	}
	res := NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":         wsID,
		"revision":   state.Revision,
		"core_team":  []any{"jim", "mia"},
		"delegation": []any{explicitEdge("jim", "mia")},
	})
	if res.IsError {
		t.Fatalf("update_workspace failed: %s", res.ForLLM)
	}
	if _, ok := u5aSelfEdge(t, home, wsID, "mia"); ok {
		t.Fatal("operator-excluded agent mia must get no self-edge even on the explicit-graph path")
	}
}

// TestCreateWorkspace_ExplicitGraphStillSeedsEveryMemberSelfEdge: a fresh
// workspace introduces its whole roster, so an explicit graph without
// self-lines still yields one per eligible member.
func TestCreateWorkspace_ExplicitGraphStillSeedsEveryMemberSelfEdge(t *testing.T) {
	deps, _, home := u5aDeps(t, nil, "jim", "mia")
	res := NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":       "explicit",
		"core_team":  []any{"jim", "mia"},
		"delegation": []any{explicitEdge("jim", "mia")},
	})
	if res.IsError {
		t.Fatalf("create_workspace failed: %s", res.ForLLM)
	}
	body := u5aParseJSON(t, res.ForLLM)
	wsID, _ := body["id"].(string)
	if wsID == "" {
		t.Fatalf("create_workspace returned no id: %s", res.ForLLM)
	}
	for _, id := range []string{"jim", "mia"} {
		if _, ok := u5aSelfEdge(t, home, wsID, id); !ok {
			edges, _ := workspacepkg.LoadDelegation(home, wsID)
			t.Fatalf("member %s has no self-edge on a workspace created with an explicit graph; edges=%+v", id, edges)
		}
	}
}
