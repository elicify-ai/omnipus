// rest_workspace_team_test.go: tests for workspace team over REST — built-in roster back-fill, core_team validation and dangling-member repair, workspaceless-agent diagnostics, and the member_configs wire translation

package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from rest_workspaces.go tests 2026-09-15 ---

// TestLogWorkspacelessAgents_WarnsOnlyForNonMembers (ADR-046 P1, FR-007/008)
// proves the boot-time diagnostic: given three configured agents — one a
// member of a real on-disk workspace, two members of nothing — exactly one
// WARN log line is emitted naming both non-member IDs (sorted), and the
// member is never mentioned. Also proves it is a genuine no-op (no warning
// at all) when every configured agent already has a workspace.
func TestLogWorkspacelessAgents_WarnsOnlyForNonMembers(t *testing.T) {
	home := t.TempDir()

	wsDir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	wsJSON := `{"id":"ws-1","core_team":["member-agent"]}`
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "ws-1.json"), []byte(wsJSON), 0o644))

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "member-agent"},
				{ID: "orphan-b"},
				{ID: "orphan-a"},
			},
		},
	}

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	logWorkspacelessAgents(home, cfg)

	var found map[string]any
	var lines int
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		lines++
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), "parse log line %q", line)
		found = entry
	}
	require.Equal(t, 1, lines, "expected exactly one log line; got:\n%s", buf.String())
	require.NotNil(t, found)
	assert.Equal(t, "orphan-a,orphan-b", found["agent_ids"],
		"must name both non-member agents, sorted, and never the member")
	assert.NotContains(t, fmt.Sprint(found["agent_ids"]), "member-agent")
	assert.EqualValues(t, 2, found["count"])

	// No configured agent is workspace-less: must be a silent no-op.
	buf.Reset()
	cfg2 := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{ID: "member-agent"}},
		},
	}
	logWorkspacelessAgents(home, cfg2)
	assert.Empty(t, buf.String(), "must not log anything when every configured agent has a workspace")
}

// --- ADR-054 D6: referential integrity (validateCoreTeamMembers / RepairDanglingCoreTeamMembers) ---

// TestValidateCoreTeamMembers_Unit exercises the pure validation function
// directly: a fully-valid list passes, a dangling (unregistered) id is
// rejected, a System Agent id is rejected, and an empty/nil list is always a
// no-op (nothing to validate).
func TestValidateCoreTeamMembers_Unit(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Type: config.AgentTypeCore},
				{ID: "judge", Type: config.AgentTypeSystem},
			},
		},
	}

	assert.NoError(t, validateCoreTeamMembers(cfg, nil), "nil coreTeam must be a no-op")
	assert.NoError(t, validateCoreTeamMembers(cfg, []string{}), "empty coreTeam must be a no-op")
	assert.NoError(t, validateCoreTeamMembers(cfg, []string{"mia"}), "a valid, registered, non-system id must pass")

	err := validateCoreTeamMembers(cfg, []string{"mia", "ghost"})
	require.Error(t, err, "an unregistered id must be rejected")
	assert.Contains(t, err.Error(), "not a registered agent")

	err = validateCoreTeamMembers(cfg, []string{"mia", "judge"})
	require.Error(t, err, "a System Agent id must be rejected")
	assert.Contains(t, err.Error(), "System Agent")

	assert.Error(t, validateCoreTeamMembers(nil, []string{"anything"}), "a nil cfg with a non-empty coreTeam must error, not panic")
}

// TestRepairDanglingCoreTeamMembers_Unit verifies the ADR-054 D6 rule 3
// repair primitive: dangling (unregistered) ids are dropped, valid ids are
// kept in their original order, and a System Agent id (which exists, so is
// not "dangling") is left untouched — repair only removes references that no
// longer resolve to anything, it does not re-implement
// validateCoreTeamMembers' separate System Agent rejection.
func TestRepairDanglingCoreTeamMembers_Unit(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Type: config.AgentTypeCore},
				{ID: "jim", Type: config.AgentTypeCore},
				{ID: "judge", Type: config.AgentTypeSystem},
			},
		},
	}

	repaired, dropped := RepairDanglingCoreTeamMembers(cfg, []string{"mia", "ghost-a", "jim", "ghost-b"})
	assert.Equal(t, []string{"mia", "jim"}, repaired, "surviving members must keep their original order")
	assert.Equal(t, []string{"ghost-a", "ghost-b"}, dropped, "dangling members must be reported, in order")

	// A System Agent id is not dangling (it exists) — repair must not drop it.
	repaired, dropped = RepairDanglingCoreTeamMembers(cfg, []string{"mia", "judge"})
	assert.Equal(t, []string{"mia", "judge"}, repaired, "an existing System Agent id is not dangling; repair must not remove it")
	assert.Empty(t, dropped)

	// Nothing dangling: repaired == input, dropped is empty.
	repaired, dropped = RepairDanglingCoreTeamMembers(cfg, []string{"mia", "jim"})
	assert.Equal(t, []string{"mia", "jim"}, repaired)
	assert.Empty(t, dropped)

	// Nil/empty input and nil cfg edge cases.
	repaired, dropped = RepairDanglingCoreTeamMembers(cfg, nil)
	assert.Nil(t, repaired)
	assert.Nil(t, dropped)

	repaired, dropped = RepairDanglingCoreTeamMembers(nil, []string{"mia", "jim"})
	assert.Nil(t, repaired, "a nil cfg has nothing to resolve against — every id is dropped")
	assert.Equal(t, []string{"mia", "jim"}, dropped)
}
