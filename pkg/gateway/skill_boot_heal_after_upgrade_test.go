//go:build goolm && stdjson

package gateway

// Team-lead round after handoff-9, item 2 (Skills & Tools #1963): the boot's
// skill-map cleanup follows the same ordering rule as architect ruling 1.
// Spec line 154: the self-heal "runs after the one-time migrations of C-19".
// If the upgrade stops before its config write at boot, the boot cleanup
// must write nothing: a key it stored at Ask would differ from both the old
// and the planned value, so the next boot's replay would read it as a
// person's change (line 405) and keep it, losing the planned Deny.
//
// Oracles: S-16 example 4 (old global Skill deny, agent none: every skill's
// global permission is deny), line 154, line 405.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skillperm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type upgradeStoppedBeforeConfigWrite struct{}

// runUpgradeStoppingBeforeConfigWrite runs the real upgrade and stops it at
// its last durable step before the config write (the ledger), as a failed
// config write at boot leaves it.
func runUpgradeStoppingBeforeConfigWrite(t *testing.T, home string) {
	t.Helper()
	defer func() {
		r := recover()
		_, stopped := r.(upgradeStoppedBeforeConfigWrite)
		require.True(t, stopped, "setup: the upgrade stops at the ledger, got %v", r)
	}()
	_ = skillperm.Run(home, skillperm.Options{Boundary: func(point, _ string) {
		if point == skillperm.PointLedger {
			panic(upgradeStoppedBeforeConfigWrite{})
		}
	}})
}

func storedGlobalSkillPolicy(t *testing.T, configPath, key string) (string, bool) {
	t.Helper()
	sandbox := waitingChild(t, waitingReadJSON(t, configPath), "sandbox")
	policies, _ := sandbox["skill_policies"].(map[string]any)
	v, ok := policies[key].(string)
	return v, ok
}

func TestBootSkillHeal_WaitsForTheUpgrade(t *testing.T) {
	home := seedOldShapeHome(t, "allow")
	configPath := filepath.Join(home, "config.json")
	// S-16 example 4: the old global Skill value is deny.
	document := waitingReadJSON(t, configPath)
	waitingChild(t, waitingChild(t, document, "sandbox"), "tool_policies")["Skill"] = "deny"
	raw, err := json.Marshal(document)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, raw, 0o600))

	// Boot 1: the upgrade stops before its config write; the boot goes on
	// with the config load and the boot cleanup.
	runUpgradeStoppingBeforeConfigWrite(t, home)
	before, err := os.ReadFile(configPath)
	require.NoError(t, err)
	cfg, err := config.LoadConfig(configPath)
	require.NoError(t, err)
	healSkillPolicyMaps(cfg, home, configPath)
	after, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after),
		"line 154: the boot cleanup writes nothing while the C-19 upgrade is not done")
	_, inMemory := cfg.Sandbox.SkillPolicies[waitingSkillKey]
	assert.False(t, inMemory, "the boot cleanup adds no key to the loaded config before the upgrade is done")

	// Boot 2: the upgrade finishes; the planned Deny is kept.
	require.NoError(t, skillperm.Run(home, skillperm.Options{}))
	v, ok := storedGlobalSkillPolicy(t, configPath, waitingSkillKey)
	require.True(t, ok, "the finished upgrade stores %s", waitingSkillKey)
	assert.Equal(t, "deny", v, "S-16: old global Skill deny gives every skill a global deny; nothing passed for a person's change")
}
