// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 16.
// TestGetAgent_SkillPermissions: get_agent accepts
// fields: ["skill_permissions"] and reports one row per skill the agent can
// use — skill, effective, override (C-9's AgentSkillPermission; S-74).
//
// Oracle: skills-tools-redesign-spec.md S-74 ("Mia granted plan (global
// Ask, override deny) and deep-research (global Allow, no override) … it
// lists plan effective deny override deny, and deep-research effective
// allow override null") and the generated AgentDetail.skill_permissions
// contract ("Returned for get_agent fields [\"skill_permissions\"]").
//
// Two missing pieces at this commit: the field is not in
// pkg/tools/fieldselect.go::AgentFieldNames (the call is refused as an
// unknown field — the observable RED today), and the C-2 global skill
// policy storage the S-74 winners need does not exist (the setup seam below
// keeps that part loud).

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"

	"github.com/stretchr/testify/require"
)

// seedSkillSettingsForAgent is the test-local seam for the C-2 global skill
// settings and the per-agent override store, as S-74's Given needs them:
// global plan=ask, agent plan=deny override; global deep-research=allow, no
// override. GREEN wires it to the storage it adds; nil keeps the RED run
// loud (BLOCKED).
var seedSkillSettingsForAgent func(t testing.TB, home string, agentID string)

// writePlanSkillFixture installs the plan and deep-research skills and
// grants both to the agent through the real grant path.
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
	require.NoError(t, agentstore.New(home).Create(agentID, &ac))
}

// TestGetAgent_SkillPermissions (order 16, S-74).
// Traces to: BDD S-74; spec C-9; generated AgentDetail.skill_permissions.
func TestGetAgent_SkillPermissions(t *testing.T) {
	nativebinding.Require(t, "pkg/config", seedSkillSettingsForAgent,
		"C-2/C-4/C-9/S-74: native persisted settings inputs; actual get_agent is the observer")
	deps, _ := newTestDepsWithRealSave(t)
	t.Setenv("OMNIPUS_HOME", deps.Home)
	writePlanSkillFixture(t, deps.Home, "mia")
	seedSkillSettingsForAgent(t, deps.Home, "mia")

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
