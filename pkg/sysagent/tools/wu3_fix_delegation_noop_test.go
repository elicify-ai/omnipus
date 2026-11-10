package systools_test

import (
	"context"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// architect M01 (#1057 F5): re-sending the saved delegation graph unchanged —
// empty, or the same edges in another order — is a semantic no-op: no bytes
// written, revision kept. A different graph is still a change (control).
func TestWU3UnchangedDelegationGraphIsANoOp(t *testing.T) {
	d, cfg := wu3Deps(t)
	a := wu3CreateAgent(t, d, cfg, "Member A")
	b := wu3CreateAgent(t, d, cfg, "Member B")
	update := systools.NewWorkspaceUpdateTool(d)
	get := systools.NewWorkspaceGetTool(d)

	empty := wu3Success(t, systools.NewWorkspaceCreateTool(d).Execute(context.Background(), map[string]any{"name": "Empty graph"}))
	emptyID := wu3String(t, empty["id"])
	read := wu3Success(t, get.Execute(context.Background(), map[string]any{"id": emptyID}))
	before := wu3FileInventory(t, d.Home)
	body := wu3Success(t, update.Execute(context.Background(), map[string]any{"id": emptyID, "revision": read["revision"], "delegation": []any{}}))
	assert.Equal(t, read["revision"], body["revision"], "empty graph re-sent: revision kept")
	assert.Equal(t, []any{}, body["changed_fields"])
	assert.Equal(t, before, wu3FileInventory(t, d.Home), "empty graph re-sent: nothing written")

	team := wu3Success(t, systools.NewWorkspaceCreateTool(d).Execute(context.Background(), map[string]any{"name": "Team graph", "core_team": []any{a, b}}))
	teamID := wu3String(t, team["id"])
	read = wu3Success(t, get.Execute(context.Background(), map[string]any{"id": teamID}))
	edges, ok := read["delegation"].([]any)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(edges), 2, "fixture: the team has seeded edges")
	reversed := make([]any, 0, len(edges))
	for i := len(edges) - 1; i >= 0; i-- {
		reversed = append(reversed, edges[i])
	}
	before = wu3FileInventory(t, d.Home)
	body = wu3Success(t, update.Execute(context.Background(), map[string]any{"id": teamID, "revision": read["revision"], "delegation": reversed}))
	assert.Equal(t, read["revision"], body["revision"], "same graph re-sent: revision kept")
	assert.Equal(t, []any{}, body["changed_fields"])
	assert.Equal(t, before, wu3FileInventory(t, d.Home), "same graph re-sent: nothing written")

	// Control: dropping an edge is a change.
	changed := wu3Success(t, update.Execute(context.Background(), map[string]any{"id": teamID, "revision": read["revision"], "delegation": edges[:len(edges)-1]}))
	assert.NotEqual(t, read["revision"], changed["revision"])
	assert.Contains(t, changed["changed_fields"], "delegation")
}
