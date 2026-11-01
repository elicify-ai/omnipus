package coreagent

// RED tests — landing/skills (T9): the T-E9b retired-tools gate's
// coreagent-owned surfaces (§15 item 1): the static tool-name catalog, the
// management-tools map, every role policy map and the custom-agent baseline
// must not carry create_skill or remove_skill.
// Traces to: skills-spec-r15.md §15 item 1, §17.1 row 4, SC-002.
//
// RED expectation: the seed catalog and the policy maps still name the
// retired tools — every assertion below fails on the baseline.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// retiredToolNamesUnderGate are the two names §15 retires.
var retiredToolNamesUnderGate = []string{"create_skill", "remove_skill"}

// allCoreAgentIDs enumerates every core agent id for the role-policy sweep.
var allCoreAgentIDs = []CoreAgentID{
	IDJim, IDMia, IDOmnipus, IDWorker,
	IDPlanner, IDResearcher, IDJudge, IDPlanSupervisor,
}

// TestRetiredTools_GoneFromAllStaticToolNames (§15 item 1).
// BDD: allStaticToolNames contains neither retired name.
// Traces to: skills-spec-r15.md §15 item 1, T-E9b.
func TestRetiredTools_GoneFromAllStaticToolNames(t *testing.T) {
	names := AllStaticToolNames()
	require.NotEmpty(t, names, "the static catalog enumerates tools")
	for _, name := range retiredToolNamesUnderGate {
		assert.NotContains(t, names, name,
			"%s must be gone from the static tool catalog (RED: it is still seeded)", name)
	}
}

// TestRetiredTools_GoneFromManagementTools (§15 item 1).
// BDD: the management-tools map (FR-025's create_agent refusal ceiling)
// lists neither retired name.
// Traces to: skills-spec-r15.md §15 item 1, T-E9b.
func TestRetiredTools_GoneFromManagementTools(t *testing.T) {
	require.NotEmpty(t, managementTools, "the management-tools map is populated")
	for _, name := range retiredToolNamesUnderGate {
		assert.NotContains(t, managementTools, name,
			"%s must be gone from managementTools (RED: it is still listed)", name)
	}
}

// TestRetiredTools_GoneFromRolePolicies (§15 item 1).
// BDD: every core agent's role policy map and the omnipusSupportedTools
// roster name neither retired tool.
// Traces to: skills-spec-r15.md §15 item 1, T-E9b.
func TestRetiredTools_GoneFromRolePolicies(t *testing.T) {
	for _, name := range retiredToolNamesUnderGate {
		assert.NotContains(t, omnipusSupportedTools, name,
			"%s must be gone from omnipusSupportedTools (RED: it is still listed)", name)
	}
	for _, id := range allCoreAgentIDs {
		policies := adr090SparseRolePolicies(id)
		for _, name := range retiredToolNamesUnderGate {
			assert.NotContains(t, policies, name,
				"%s must be gone from role policies of %s", name, id)
		}
	}
}

// TestRetiredTools_GoneFromCustomAgentBaseline (§15 item 1).
// BDD: the custom-agent baseline policy seed (NewCustomAgentToolsCfg) does
// not carry either retired name — new agents are never offered them.
// Traces to: skills-spec-r15.md §15 item 1, T-E9b.
func TestRetiredTools_GoneFromCustomAgentBaseline(t *testing.T) {
	cfg := NewCustomAgentToolsCfg()
	require.NotNil(t, cfg)
	require.NotEmpty(t, cfg.Builtin.Policies, "the custom-agent baseline seeds policies")
	for _, name := range retiredToolNamesUnderGate {
		assert.NotContains(t, cfg.Builtin.Policies, name,
			"%s must be gone from the custom-agent baseline (RED: it is still seeded)", name)
	}
}
