// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// boot_agent_roster_upgrade_test.go — boot-sequence goal_claim persistence
// guarantees, exercised through the real boot seeder and real files on disk.
// Founder decision 2026-09-15, issue #710 (greenfield: no upgrade migrations).

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// copyFixtureHome copies testdata/<name> into a fresh temp directory and
// returns that directory.
func copyFixtureHome(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("testdata", name)
	dst := t.TempDir()
	require.NoError(t, filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	}))
	return dst
}

// loadBootConfig mirrors how boot builds cfg from config.json: the
// operator's JSON unmarshalled over DefaultConfig().
func loadBootConfig(t *testing.T, configPath string) *config.Config {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	cfg := config.DefaultConfig()
	require.NoError(t, json.Unmarshal(raw, cfg))
	return cfg
}

func storedAgentGoalClaim(t *testing.T, home, id string) config.ToolPolicy {
	t.Helper()
	ag, err := agentstore.New(home).Get(id)
	require.NoError(t, err)
	require.NotNil(t, ag.Tools, "agent %q must have a stored tool map", id)
	return ag.Tools.Builtin.Policies[tools.GoalClaimToolName]
}

func diskMarkers(t *testing.T, configPath string) []any {
	t.Helper()
	return diskMarkerList(t, configPath, "seeded_tool_policy_updates")
}

func diskMarkerList(t *testing.T, configPath, key string) []any {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	list, _ := m[key].([]any)
	return list
}

// assertSkillMarkersIntact checks the two marker lists stay separate on disk:
// the skills migrations' markers are still recorded under seeded_skill_grants,
// and the tool-policy marker never lands there.
func assertSkillMarkersIntact(t *testing.T, configPath string) {
	t.Helper()
	skills := diskMarkerList(t, configPath, "seeded_skill_grants")
	assert.Contains(t, skills, coreagent.SkillsMigrationDefineDone,
		"the skills migration marker must still be recorded under seeded_skill_grants")
	assert.Contains(t, skills, coreagent.SkillsMigrationDefineGoalRename,
		"the skills rename marker must still be recorded under seeded_skill_grants")
	assert.NotContains(t, skills, coreagent.ToolPolicyUpdateWorkerGoalClaimAllow,
		"the tool-policy marker must never be written under seeded_skill_grants")
}

// boot_agent_roster_upgrade_test.go — greenfield preservation guarantees for
// goal_claim across boots (founder decision 2026-09-15, issue #710: fresh
// installs are greenfield, no upgrade migrations). SeedConfig seeds
// goal_claim=allow on fresh installs (covered by
// goal_claim_default_allow_test.go); this file pins the OTHER half: an
// operator-stored override is operator data and must survive every boot
// untouched, with no one-shot update marker written behind the operator's
// back. The fixture's name ("upgrade-worker-goal-claim-deny") is historical —
// it models any install whose stored Worker carries an explicit goal_claim
// deny.
func TestBootRoster_StoredGoalClaimOverrides_PreservedAcrossBoots(t *testing.T) {
	home := copyFixtureHome(t, "upgrade-worker-goal-claim-deny")
	configPath := filepath.Join(home, "config.json")

	// Precondition: the Worker and an operator's own agent both store an
	// explicit goal_claim deny, and no marker lists exist.
	require.Equal(t, config.ToolPolicyDeny, storedAgentGoalClaim(t, home, string(coreagent.IDWorker)))
	require.Equal(t, config.ToolPolicyDeny, storedAgentGoalClaim(t, home, "ops-bot"))
	require.Empty(t, diskMarkers(t, configPath))

	// First boot.
	cfg := loadBootConfig(t, configPath)
	require.NoError(t, seedAndPersistAgentRoster(cfg, home, configPath))

	assert.Equal(t, config.ToolPolicyDeny, storedAgentGoalClaim(t, home, string(coreagent.IDWorker)),
		"the Worker's stored goal_claim deny is operator data — a boot must never flip it")
	assert.Equal(t, config.ToolPolicyDeny, storedAgentGoalClaim(t, home, "ops-bot"),
		"another agent's explicit deny is operator data and must be left alone")
	assert.Empty(t, diskMarkers(t, configPath),
		"no goal_claim update marker may be written — the one-shot migration marker is retired; "+
			"fresh installs simply seed allow")
	assertSkillMarkersIntact(t, configPath)

	worker, err := agentstore.New(home).Get(string(coreagent.IDWorker))
	require.NoError(t, err)
	assert.Equal(t, config.ToolPolicyDeny, worker.Tools.Builtin.Policies["set_goal"],
		"a boot must not touch the Worker's other stored entries")
	assert.Equal(t, config.ToolPolicyAsk, worker.Tools.Builtin.Policies["run_task"],
		"a boot must not touch the Worker's other stored entries")

	// Second boot: the overrides must be just as stable.
	cfg2 := loadBootConfig(t, configPath)
	require.NoError(t, seedAndPersistAgentRoster(cfg2, home, configPath))

	assert.Equal(t, config.ToolPolicyDeny, storedAgentGoalClaim(t, home, string(coreagent.IDWorker)),
		"a stored deny must survive every later boot")
	assert.Equal(t, config.ToolPolicyDeny, storedAgentGoalClaim(t, home, "ops-bot"))
	assert.Empty(t, diskMarkers(t, configPath),
		"the marker list must stay absent across boots")
	assertSkillMarkersIntact(t, configPath)
}
