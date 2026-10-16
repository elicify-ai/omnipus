package systools_test

import (
	"context"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CHECK matrix gap (Q2=A): create_workspace and delete_workspace carry the
// FR-030 envelope on their validation refusals and on success, like the other
// change families.
func TestWU3WorkspaceCreateDeleteCarryTheOutcomeEnvelope(t *testing.T) {
	d, _ := wu3Deps(t)
	wu3Workspace(t, d, "default")
	create := systools.NewWorkspaceCreateTool(d)
	del := systools.NewWorkspaceDeleteTool(d)

	before := wu3DurableInventory(t, d.Home)
	for label, args := range map[string]map[string]any{
		"create without name": {},
		"create blank name":   {"name": "   "},
	} {
		res := create.Execute(context.Background(), args)
		require.True(t, res.IsError, "%s: %s", label, res.ForLLM)
		body := wu3Outcome(t, res, "failed: nothing saved")
		assert.Equal(t, []any{}, body["changed_fields"], label)
	}
	requireNoDurableChange(t, d.Home, before, "create refusals saved nothing")

	res := create.Execute(context.Background(), map[string]any{"name": "Envelope Fixture"})
	require.False(t, res.IsError, res.ForLLM)
	body := wu3Verdict(t, res, "working",
		"workspace record and delegation graph re-read from disk; graph validity and new edges' membership checked",
		"a delegation between the members")
	assert.NotEmpty(t, body["changed_fields"])
	id, ok := body["id"].(string)
	require.True(t, ok)
	state, err := workspace.ReadState(d.Home, id)
	require.NoError(t, err)

	before = wu3DurableInventory(t, d.Home)
	for label, args := range map[string]map[string]any{
		"delete without id":        {"revision": state.Revision, "confirm": true},
		"delete unconfirmed":       {"id": id, "revision": state.Revision, "confirm": false},
		"delete unsafe id":         {"id": "../default", "revision": state.Revision, "confirm": true},
		"delete stale revision":    {"id": id, "revision": "stale-revision", "confirm": true},
		"delete missing workspace": {"id": "no-such-workspace", "revision": state.Revision, "confirm": true},
	} {
		refusal := del.Execute(context.Background(), args)
		require.True(t, refusal.IsError, "%s: %s", label, refusal.ForLLM)
		refused := wu3Outcome(t, refusal, "failed: nothing saved")
		assert.Equal(t, []any{}, refused["changed_fields"], label)
	}
	requireNoDurableChange(t, d.Home, before, "delete refusals saved nothing")

	res = del.Execute(context.Background(), map[string]any{"id": id, "revision": state.Revision, "confirm": true})
	require.False(t, res.IsError, res.ForLLM)
	body = wu3Verdict(t, res, "working",
		"workspace record deleted; its tasks, mount store, delegation store and folder removed")
	assert.Equal(t, []any{"workspace"}, body["changed_fields"])
	_, err = workspace.ReadState(d.Home, id)
	assert.Error(t, err, "the workspace is really gone")
}

// CHECK matrix gap: list_skills' explicit workspace_id gets the same
// pre-read refusals as the setup verbs — never a silent fallback to the
// conversation's own shelf.
func TestWU3ListSkillsRefusesInvalidExplicitTarget(t *testing.T) {
	d, ctx, _, _ := wu3ProjectTarget(t)
	list := systools.NewSkillListTool(d)
	for label, tc := range map[string]struct {
		args map[string]any
		code string
	}{
		"unknown workspace": {map[string]any{"scope": "management", "workspace_id": "ghost"}, "WORKSPACE_NOT_FOUND"},
		"empty workspace":   {map[string]any{"scope": "management", "workspace_id": "  "}, "INVALID_INPUT"},
		"unsafe workspace":  {map[string]any{"scope": "management", "workspace_id": "../sales"}, "INVALID_INPUT"},
		"wrong type":        {map[string]any{"scope": "management", "workspace_id": float64(7)}, "INVALID_INPUT"},
		"usable scope":      {map[string]any{"scope": "usable", "workspace_id": "sales"}, "INVALID_INPUT"},
	} {
		res := list.Execute(ctx, tc.args)
		body := wu3Error(t, res, tc.code)
		assert.NotContains(t, res.ForLLM, "default-only-body", "%s must not list the anchor's shelf", label)
		assert.NotEmpty(t, body["message"], label)
	}

	// Archived target: refused like an unknown one.
	require.NoError(t, workspace.SaveRecord(d.Home, workspace.Workspace{ID: "old", Name: "Old", Status: "archived"}))
	wu3Error(t, list.Execute(ctx, map[string]any{"scope": "management", "workspace_id": "old"}), "WORKSPACE_NOT_FOUND")

	// A non-Omnipus caller cannot read another workspace's management shelf.
	other := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "ava"), "default")
	res := list.Execute(other, map[string]any{"scope": "management", "workspace_id": "sales"})
	require.True(t, res.IsError, res.ForLLM)
	assert.NotContains(t, res.ForLLM, "sales-only-body")

	// Control: the valid explicit target lists the Sales shelf.
	ok := wu3Success(t, list.Execute(ctx, map[string]any{"scope": "management", "name": "crm", "workspace_id": "sales"}))
	listed, isList := ok["skills"].([]any)
	require.True(t, isList)
	require.Len(t, listed, 1)
	assert.Contains(t, wu3Object(t, listed[0])["content"], "sales-only-body")
}
