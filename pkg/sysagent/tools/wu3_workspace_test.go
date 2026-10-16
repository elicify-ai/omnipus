package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1057 / binding Q4: empty is invalid; omitted is preserved.
func TestWU3WorkspaceExplicitBlankNameIsRefused(t *testing.T) {
	for _, name := range []string{"", " \t\n"} {
		t.Run(name, func(t *testing.T) {
			d, _ := wu3Deps(t)
			state := wu3Workspace(t, d, "sales")
			path := filepath.Join(d.Home, "workspaces", "sales.json")
			before := wu3ReadFile(t, path)
			result := systools.NewWorkspaceUpdateTool(d).Execute(context.Background(), map[string]any{"id": "sales", "revision": state.Revision, "name": name})
			body := wu3Error(t, result, "INVALID_INPUT")
			assert.Equal(t, "name is required", body["message"])
			assert.Equal(t, before, wu3ReadFile(t, path), "invalid empty name must not advance revision or rewrite record")
		})
	}
}

func TestWU3WorkspaceUnknownMembersAreRejectedBeforeAnyWrite(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
			d, cfg := wu3Deps(t)
			member := wu3CreateAgent(t, d, cfg, "Known member")
			state := wu3Workspace(t, d, "sales", member)
			path := filepath.Join(d.Home, "workspaces", "sales.json")
			before := wu3ReadFile(t, path)
			var tool tools.Tool = systools.NewWorkspaceCreateTool(d)
			args := map[string]any{"name": "Invalid team", "core_team": []any{member, "no-such-agent-xyz"}}
			if operation == "update" {
				args = map[string]any{"id": "sales", "revision": state.Revision, "core_team": []any{member, "no-such-agent-xyz"}}
				tool = systools.NewWorkspaceUpdateTool(d)
			}
			result := tool.Execute(context.Background(), args)
			body := wu3Error(t, result, "INVALID_INPUT")
			assert.Contains(t, body["message"], "no-such-agent-xyz", "#1057 refusal names the unknown member")
			assert.Equal(t, before, wu3ReadFile(t, path))
			entries, err := os.ReadDir(filepath.Join(d.Home, "workspaces"))
			require.NoError(t, err)
			count := 0
			for _, entry := range entries {
				if filepath.Ext(entry.Name()) == ".json" {
					count++
				}
			}
			assert.Equal(t, 1, count, "rejected create leaves only the fixture workspace")
		})
	}
}

// #1057 F4: returned token is the post-write read token, never the old token.
func TestWU3WorkspaceMutationEchoesCommittedRevision(t *testing.T) {
	d, _ := wu3Deps(t)
	created := wu3Success(t, systools.NewWorkspaceCreateTool(d).Execute(context.Background(), map[string]any{"name": "Created workspace"}))
	id := wu3String(t, created["id"])
	readTool := systools.NewWorkspaceGetTool(d)
	read := wu3Success(t, readTool.Execute(context.Background(), map[string]any{"id": id}))
	assert.Equal(t, read["revision"], created["revision"], "create_workspace echoes revision")
	updated := wu3Success(t, systools.NewWorkspaceUpdateTool(d).Execute(context.Background(), map[string]any{"id": id, "revision": read["revision"], "description": "Changed description"}))
	after := wu3Success(t, readTool.Execute(context.Background(), map[string]any{"id": id}))
	assert.Equal(t, after["revision"], updated["revision"], "update_workspace echoes revision")
	assert.NotEqual(t, read["revision"], updated["revision"], "real changed content advances token")
	assert.Equal(t, "Created workspace", after["name"], "omitted name is unchanged")
}

// FR-032 / BDD-9.5: invalid endpoints cause zero durable effects.
func TestWU3WorkspaceGraphRejectsNonMemberWithTruthfulOutcome(t *testing.T) {
	d, cfg := wu3Deps(t)
	member := wu3CreateAgent(t, d, cfg, "Graph member")
	other := wu3CreateAgent(t, d, cfg, "Nonmember agent")
	state := wu3Workspace(t, d, "sales", member)
	path := filepath.Join(d.Home, "workspaces", "sales.json")
	before := wu3ReadFile(t, path)
	result := systools.NewWorkspaceUpdateTool(d).Execute(context.Background(), map[string]any{
		"id": "sales", "revision": state.Revision, "description": "Must not land",
		"delegation": []any{map[string]any{"from_agent": member, "to_agent": other, "modes": []any{"direct"}, "depth": 1}},
	})
	wu3Error(t, result, "INVALID_DELEGATION_EDGE")
	assert.Equal(t, before, wu3ReadFile(t, path), "graph refusal must not save any part")
	wu3Outcome(t, result, "failed: nothing saved")
}

func TestWU3WorkspaceValidGraphReadbackReportsWorking(t *testing.T) {
	d, cfg := wu3Deps(t)
	first := wu3CreateAgent(t, d, cfg, "Lead fixture")
	second := wu3CreateAgent(t, d, cfg, "Worker fixture")
	state := wu3Workspace(t, d, "sales", first, second)
	result := systools.NewWorkspaceUpdateTool(d).Execute(context.Background(), map[string]any{
		"id": "sales", "revision": state.Revision,
		"delegation": []any{map[string]any{"from_agent": first, "to_agent": second, "modes": []any{"direct"}, "depth": 1}},
	})
	wu3Success(t, result)
	read := wu3Success(t, systools.NewWorkspaceGetTool(d).Execute(context.Background(), map[string]any{"id": "sales"}))
	assert.Equal(t, []any{first, second}, read["core_team"])
	edges, ok := read["delegation"].([]any)
	require.True(t, ok)
	require.Len(t, edges, 1, "read-back contains exactly the reviewed edge")
	assert.Equal(t, first, wu3Object(t, edges[0])["from_agent"])
	assert.Equal(t, second, wu3Object(t, edges[0])["to_agent"])
	body := wu3Outcome(t, result, "working")
	assert.Contains(t, body["check_scope"], "graph")
	assert.Empty(t, body["missing_parts"])
}
