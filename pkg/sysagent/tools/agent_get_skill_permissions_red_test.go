// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 16.
// TestGetAgent_SkillPermissions: get_agent accepts
// fields: ["skill_permissions"] and reports one row per skill the agent can
// use — skill, effective, override (C-9's AgentSkillPermission; S-74).
//
// Oracle: skills-tools-redesign-spec.md S-74 ("Mia granted `plan` (global
// Ask, override deny) and `deep-research` (global Allow, no override)" … "it
// lists `plan` effective deny override deny, and `deep-research` effective
// allow override null") and the generated AgentDetail.skill_permissions
// contract (`Returned for get_agent fields ["skill_permissions"].`).
//
// Part H (round 10) rewrote the Given. The first version took it from a
// test-local variable, `seedSkillSettingsForAgent func(t testing.TB, home,
// agentID)`, that the production code could never fill: its type is a test
// type, so no production function has that shape and S-74 could not pass
// whatever the product did (the backend's hand-off 8 named it). The Given is
// now stored through the product's own writers:
//   - the global values: config.SetGlobalSkillPolicy (it changes the loaded
//     config only; "persisting it is the caller's write", its doc says) and
//     config.SaveConfig, the writer every other test of this package uses for
//     the config file get_agent reads (config.ReadPersistedSkillSettings);
//   - Mia's own value: the agent record the store persists
//     (AgentConfig.Tools.Skills.Policies through agentstore.Create).
// Both are then read back from disk by an independent reader (the raw
// config.json and tests/helpers/agentrecord) so a Given that landed where
// nothing reads it fails as a fixture failure, never as a green or a red
// about get_agent.
//
// Trust. S-74 names no trust value and does not say whether the two skills
// are verified. The fixture's skills are plain folders (unverified), and under
// the default trust Ask the trust check of C-3/C-12 would apply to them. The
// type of the rows is defined in C-9 (AgentSkillPermission: skill, effective,
// override); what `effective` means is written in the contract's own
// description (contracts/components/schemas/AgentSkillPermission.yaml): "The
// permission that applies to this agent's loads of the skill (strictest wins,
// trust floor, God Mode)". This test is about the permission rows, so the Given
// sets trust Allow, which C-3 defines for an unverified skill's load as
// "follows the skill's own permission only". That is a stated assumption,
// reported as a spec gap in the Part H pack, not a change of the oracle.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/tests/helpers/agentrecord"

	"github.com/stretchr/testify/require"
)

// writePlanSkillFixture installs the plan and deep-research skills and grants
// both to the agent through the real grant path; the agent carries its own
// Deny for plan (S-74's override) in the record the store persists.
func writePlanSkillFixture(t *testing.T, home, agentID string) {
	t.Helper()
	for name, display := range map[string]string{"plan": "Plan", "deep-research": "Deep Research"} {
		dir := filepath.Join(home, "skills", name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		body := "---\nname: " + name + "\ndescription: " + display + " helper.\nmetadata:\n  display_name: " + display + "\n---\nBody."
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644))
	}
	ac := config.AgentConfig{ID: agentID, Name: "Mia", Type: config.AgentTypeCustom,
		Tools: coreagent.NewCustomAgentToolsCfg(), Skills: []string{"plan", "deep-research"}}
	ac.Tools.Skills = &config.AgentSkillsCfg{Policies: map[string]config.ToolPolicy{"plan": config.ToolPolicyDeny}}
	require.NoError(t, agentstore.New(home).Create(agentID, &ac))
}

// storeGlobalSkillSettings stores S-74's global values and the trust value the
// fixture assumes (see the header), then reads them back from the file.
func storeGlobalSkillSettings(t *testing.T, cfg *config.Config, cfgPath string) {
	t.Helper()
	config.SetGlobalSkillPolicy(cfg, "plan", "ask")
	config.SetGlobalSkillPolicy(cfg, "deep-research", "allow")
	cfg.Sandbox.SkillTrust = config.SkillTrustAllow
	require.NoError(t, config.SaveConfig(cfgPath, cfg))
	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var stored struct {
		Sandbox struct {
			SkillPolicies map[string]string `json:"skill_policies"`
			SkillTrust    string            `json:"skill_trust"`
		} `json:"sandbox"`
	}
	require.NoError(t, json.Unmarshal(raw, &stored))
	require.Equal(t, "ask", stored.Sandbox.SkillPolicies["plan"], "fixture: the global value of plan is stored in config.json")
	require.Equal(t, "allow", stored.Sandbox.SkillPolicies["deep-research"], "fixture: the global value of deep-research is stored in config.json")
	require.Equal(t, "allow", stored.Sandbox.SkillTrust, "fixture: the trust assumption is stored in config.json")
}

// TestGetAgent_SkillPermissions (order 16, S-74).
// Traces to: BDD S-74; spec C-9; generated AgentDetail.skill_permissions.
func TestGetAgent_SkillPermissions(t *testing.T) {
	deps, cfgPath := newTestDepsWithRealSave(t)
	t.Setenv("OMNIPUS_HOME", deps.Home)
	writePlanSkillFixture(t, deps.Home, "mia")
	storeGlobalSkillSettings(t, deps.GetCfg(), cfgPath)
	require.Equal(t, map[string]any{"plan": "deny"}, agentrecord.Policies(t, deps.Home, "mia", agentrecord.Skills),
		"fixture: Mia's own value is stored at the carrier the store keeps, and only for plan")

	get := systools.NewAgentGetTool(deps)
	res := get.Execute(context.Background(), map[string]any{
		"id":     "mia",
		"fields": []any{"skill_permissions"},
	})
	require.False(t, res.IsError,
		"C-9: skill_permissions must be a selectable get_agent field; got: %s", res.ForLLM)
	rows, ok := parseSuccess(t, res.ForLLM)["skill_permissions"].([]any)
	require.True(t, ok, "the result carries the skill_permissions list: %s", res.ForLLM)
	require.Len(t, rows, 2, "one row per skill the agent can use (S-74): %s", res.ForLLM)
	bySkill := map[string]map[string]any{}
	for _, r := range rows {
		row, rowIsObject := r.(map[string]any)
		require.True(t, rowIsObject, "each skill_permissions row is an object: %v", r)
		name, nameIsString := row["skill"].(string)
		require.True(t, nameIsString, "each skill_permissions row names its skill as a string: %v", row)
		bySkill[name] = row
	}
	require.Equal(t, "deny", bySkill["plan"]["effective"], "S-74: plan effective deny")
	require.Equal(t, "deny", bySkill["plan"]["override"], "S-74: plan override deny")
	require.Equal(t, "allow", bySkill["deep-research"]["effective"], "S-74: deep-research effective allow")
	require.Nil(t, bySkill["deep-research"]["override"], "S-74: deep-research override null")
}
