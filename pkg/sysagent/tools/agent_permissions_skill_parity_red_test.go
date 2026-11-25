// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 15.
// TestSetAgentPermissions_SkillPolicyChangesParity: set_agent_permissions
// accepts skill_policy_changes with the same {set, remove} shape as
// tool_policy_changes, stores the per-agent skill override, and reports the
// clamp sentence naming the global value (S-70).
//
// Oracle: skills-tools-redesign-spec.md S-70 ("Omnipus calls
// set_agent_permissions for Mia with skill_policy_changes: {set: {plan:
// allow}} … Mia's override plan: allow is stored … the result carries the
// clamp sentence saying the global value Ask applies") and C-21 (one parity
// shape across capability writers). FR-025's Omnipus refusal is order 14's
// S-22 row, not this test.
//
// The tool accepts only {id, revision, tool_policy_changes, mcp_servers}
// today (pkg/sysagent/tools/agent_permissions.go), so a skill_policy_changes
// call is refused as an unknown field — the RED reason is that refusal, not
// a missing handler.

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/tests/helpers/agentrecord"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"

	"github.com/stretchr/testify/require"
)

// setGlobalSkillPolicyForTest is the test-local seam for the C-2 global
// skill policy storage (S-70's Given: global `plan` at Ask). GREEN wires it
// to the storage it adds; nil keeps the RED run loud (BLOCKED), because the
// parity call and the clamp sentence both need the global value to exist.
var setGlobalSkillPolicyForTest func(cfg *config.Config, key, value string)

// TestSetAgentPermissions_SkillPolicyChangesParity (order 15, S-70).
// Traces to: BDD S-70; spec C-21.
func TestSetAgentPermissions_SkillPolicyChangesParity(t *testing.T) {
	nativebinding.Require(t, "pkg/config", setGlobalSkillPolicyForTest,
		"C-2/C-21/S-70: native global skill map input; actual writer/store is the observer")
	deps, _ := newTestDepsWithRealSave(t)
	t.Setenv("OMNIPUS_HOME", deps.Home)
	setGlobalSkillPolicyForTest(deps.GetCfg(), "plan", "ask")

	ac := config.AgentConfig{ID: "subject", Name: "Subject", Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg()}
	store := agentstore.New(deps.Home)
	require.NoError(t, store.Create("subject", &ac))
	before, err := store.ReadState("subject")
	require.NoError(t, err)

	permission, ok := systools.BuildRegistry(deps).Get("set_agent_permissions")
	require.True(t, ok, "BLOCKED: set_agent_permissions not implemented — required by FR-024")

	res := permission.Execute(context.Background(), map[string]any{
		"id":       "subject",
		"revision": before.Revision,
		"skill_policy_changes": map[string]any{
			"set": map[string]any{"plan": "allow"},
		},
	})
	require.False(t, res.IsError,
		"S-70: skill_policy_changes must be accepted with the tool_policy_changes parity shape; got: %s",
		res.ForLLM)

	// Stored: Mia's override plan: allow on the agent's tools (C-4 shape). The
	// record is the flat AgentConfig the store writes — tools.skills.policies at
	// the top level, no outer wrapper (tests/helpers/agentrecord documents the
	// carrier and shows the reader against a record the real store wrote).
	stored := agentrecord.Policies(t, deps.Home, "subject", agentrecord.Skills)
	require.Equal(t, "allow", stored["plan"],
		"S-70: the override is stored; entity: %s", agentrecord.Raw(t, deps.Home, "subject"))

	// The clamp sentence names the global value (S-70): the result carries a
	// note that the global Ask applies to plan while the override is allow.
	lower := strings.ToLower(res.ForLLM)
	require.Contains(t, lower, "plan", "the clamp note names the skill: %s", res.ForLLM)
	require.Contains(t, lower, "ask", "the clamp note says the global value Ask applies (S-70): %s", res.ForLLM)
}
