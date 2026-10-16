package systools_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1057 F5: semantic no-op leaves both content and revision untouched. The
// historical timestamp makes an erroneous updated_at-only write observable
// without sleeping or using wall-clock timing as the oracle.
func TestWU3WorkspaceNoOpKeepsRecordAndRevisionUnchanged(t *testing.T) {
	d, _ := wu3Deps(t)
	require.NoError(t, workspace.SaveRecord(d.Home, workspace.Workspace{ID: "sales", Name: "Sales", Description: "unchanged", Status: "active", CreatedAt: "2000-01-01T00:00:00Z", UpdatedAt: "2000-01-01T00:00:00Z"}))
	read := wu3Success(t, systools.NewWorkspaceGetTool(d).Execute(context.Background(), map[string]any{"id": "sales"}))
	before := wu3ReadFile(t, filepath.Join(d.Home, "workspaces", "sales.json"))
	result := systools.NewWorkspaceUpdateTool(d).Execute(context.Background(), map[string]any{"id": "sales", "revision": read["revision"], "name": "Sales", "description": "unchanged"})
	body := wu3Success(t, result)
	assert.Equal(t, before, wu3ReadFile(t, filepath.Join(d.Home, "workspaces", "sales.json")), "no-op must not bump updated_at or publish any bytes")
	assert.Equal(t, read["revision"], body["revision"], "idempotent retry retains token")
}

// #1057 F6 / Workspace.yaml: empty fields have one consistent typed shape in
// list/get/create/update; array fields cannot be null under their schema.
func TestWU3WorkspaceEmptyFieldsHaveOneCanonicalShape(t *testing.T) {
	d, _ := wu3Deps(t)
	created := wu3Success(t, systools.NewWorkspaceCreateTool(d).Execute(context.Background(), map[string]any{"name": "Canonical fixture"}))
	id := wu3String(t, created["id"])
	read := wu3Success(t, systools.NewWorkspaceGetTool(d).Execute(context.Background(), map[string]any{"id": id}))
	updated := wu3Success(t, systools.NewWorkspaceUpdateTool(d).Execute(context.Background(), map[string]any{"id": id, "revision": read["revision"], "description": ""}))
	listed := wu3Success(t, systools.NewWorkspaceListTool(d).Execute(context.Background(), map[string]any{}))
	items, ok := listed["workspaces"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	for _, view := range []struct {
		name  string
		value map[string]any
	}{{"create", created}, {"get", read}, {"update", updated}, {"list", wu3Object(t, items[0])}} {
		t.Run(view.name, func(t *testing.T) {
			assert.Equal(t, "", view.value["description"], "description always present and empty string")
			assert.Equal(t, []any{}, view.value["core_team"], "empty roster always []")
			assert.Equal(t, []any{}, view.value["delegation"], "empty graph always []")
		})
	}
}

// #1064 A3 was removed, not fixed by reviving an icon registry. The public
// argument schema and actual refusal both prove the old input is not advertised.
func TestWU3RetiredIconInputIsNeitherAdvertisedNorAccepted(t *testing.T) {
	d, _ := wu3Deps(t)
	for _, tool := range []interface{ Parameters() map[string]any }{systools.NewAgentCreateTool(d), systools.NewAgentUpdateTool(d)} {
		properties, ok := tool.Parameters()["properties"].(map[string]any)
		require.True(t, ok)
		assert.NotContains(t, properties, "icon")
		assert.Contains(t, properties, "figure", "real current visual identity remains discoverable")
	}
	args := wu3CreateArgs("Icon typo fixture")
	args["icon"] = "zzznotrealicon"
	body := wu3Error(t, systools.NewAgentCreateTool(d).Execute(context.Background(), args), "INVALID_INPUT")
	assert.Contains(t, body["message"], "icon")
}

// #1064 A5: permission fields moved from update_agent to the Ask-gated tool in
// WU-2, but a correctly clamped write still has to signal its effective result.
func TestWU3PermissionWriteReportsGlobalCeilingClamp(t *testing.T) {
	d, cfg := wu3Deps(t)
	id := wu3CreateAgent(t, d, cfg, "Policy subject")
	cfg.Sandbox.ToolPolicies["read_file"] = "ask"
	before := wu3AgentRead(t, d, id)
	result := systools.NewAgentPermissionsTool(d).Execute(context.Background(), map[string]any{"id": id, "revision": before["revision"], "tool_policy_changes": map[string]any{"set": map[string]any{"read_file": "allow"}}})
	body := wu3Success(t, result)
	read := wu3Success(t, systools.NewAgentGetToolsTool(d).Execute(context.Background(), map[string]any{"id": id}))
	assert.Equal(t, "allow", wu3Object(t, read["stored_tool_overrides"])["read_file"])
	assert.Equal(t, "ask", wu3Object(t, read["effective_policies"])["read_file"])
	// Chief's binding carrier A: an exact, resource-specific clamp row in
	// both the mutation response and its public tool-policy read-back.
	want := []any{map[string]any{"tool": "read_file", "requested": "allow", "effective": "ask"}}
	assert.Equal(t, want, body["clamped"], "mutation must report exactly the policy the ceiling clamped")
	assert.Equal(t, want, read["clamped"], "read-back must report the same persisted/effective distinction")
	assert.NotEmpty(t, strings.TrimSpace(wu3String(t, body["message"])), "structured clamp data keeps a human-readable sentence")
}

// The unclamped control kills an implementation that always emits a clamp row
// or uses null/omission instead of the required empty array.
func TestWU3PermissionWriteWithoutCeilingClampReturnsEmptyArray(t *testing.T) {
	d, cfg := wu3Deps(t)
	id := wu3CreateAgent(t, d, cfg, "Policy subject")
	cfg.Sandbox.ToolPolicies["read_file"] = "allow"
	before := wu3AgentRead(t, d, id)
	result := systools.NewAgentPermissionsTool(d).Execute(context.Background(), map[string]any{"id": id, "revision": before["revision"], "tool_policy_changes": map[string]any{"set": map[string]any{"read_file": "allow"}}})
	body := wu3Success(t, result)
	read := wu3Success(t, systools.NewAgentGetToolsTool(d).Execute(context.Background(), map[string]any{"id": id}))
	assert.Equal(t, "allow", wu3Object(t, read["stored_tool_overrides"])["read_file"])
	assert.Equal(t, "allow", wu3Object(t, read["effective_policies"])["read_file"])
	assert.Equal(t, []any{}, body["clamped"], "no ceiling clamp means an empty array, never null or omitted")
	assert.Equal(t, []any{}, read["clamped"], "unclamped read-back has the same exact empty-array shape")
}
