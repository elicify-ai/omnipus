package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// silent-failure-hunter SF-07 / architect F07: a core_team entry is admitted
// only when it IS an agent under exact identity. A file that merely sits at
// the expected record name is not proof: a record storing another id, or a
// case variant a case-insensitive volume resolves, is refused as unknown; an
// unreadable record is refused as READ_FAILED, not as a typo. Covers both
// create_workspace and update_workspace.
func TestWU3TeamAdmissionRequiresExactAgentIdentity(t *testing.T) {
	d, cfg := wu3Deps(t)
	wu3Workspace(t, d, "default")
	// A fixed, valid v4 UUID that contains letters, so its upper-case variant
	// is a different id on every run (CHECK R2-05: a digits-only random id made
	// the case-negative equal the valid id).
	const memberID = "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d"
	systools.SetAgentIDGeneratorForTest(t, func(string) string { return memberID })
	member := wu3CreateAgent(t, d, cfg, "Real member")
	require.Equal(t, memberID, member)
	require.NotEqual(t, member, strings.ToUpper(member), "fixture: the case variant differs")
	cfg.Agents.List = nil // only the entity store can vouch for members here
	agentsDir := filepath.Join(d.Home, "entities", "agents")
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "impostor.json"), []byte(`{"id":"someone-else","name":"x"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "broken.json"), []byte(`{not json`), 0o600))

	create := systools.NewWorkspaceCreateTool(d)
	for id, code := range map[string]string{
		"impostor":              "INVALID_INPUT",
		strings.ToUpper(member): "INVALID_INPUT",
		"broken":                "READ_FAILED",
	} {
		before := wu3FileInventory(t, d.Home)
		res := create.Execute(context.Background(), map[string]any{"name": "Team " + id, "core_team": []any{id}})
		body := wu3Error(t, res, code)
		assert.Contains(t, body["message"], id)
		wu3Outcome(t, res, "failed: nothing saved")
		assert.Equal(t, before, wu3FileInventory(t, d.Home), "refused admission saved nothing")
	}

	state := wu3Workspace(t, d, "sales")
	update := systools.NewWorkspaceUpdateTool(d)
	res := update.Execute(context.Background(), map[string]any{
		"id": "sales", "revision": state.Revision, "core_team": []any{"impostor"},
	})
	wu3Error(t, res, "INVALID_INPUT")
	after, err := workspace.ReadState(d.Home, "sales")
	require.NoError(t, err)
	assert.Empty(t, after.Workspace.CoreTeam)

	// Control: the exact id of a real agent is admitted.
	ok := create.Execute(context.Background(), map[string]any{"name": "Real team", "core_team": []any{member}})
	require.False(t, ok.IsError, ok.ForLLM)
}
