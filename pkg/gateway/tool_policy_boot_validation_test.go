// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// These tests pin what boot-time tool-policy validation ACTUALLY does, because
// a UAT round (2026-09-02, batch 4 S84) reported that it "does not abort boot on
// a real on-disk gap" and CLAUDE.md hard constraint 6 claims it does ("at boot
// (aborts with a listed `agent × tool` report on any gap)"). Both statements are
// partly right, and the difference is exactly what these tests make explicit so
// the next reader does not have to re-derive it:
//
//  1. The validator DOES run and its abort IS live code — see the caller in
//     gateway.go's RunContextWithOptions (boot) and executeReload (hot-reload),
//     both of which return an error on any remaining gap.
//
//  2. But config.RepairIncompleteToolPolicyCoverage runs immediately BEFORE it
//     and closes every gap it can see, backfilling to explicit "deny". Since
//     gaps are derived from cfg.Agents.List itself, there is no gap the repair
//     cannot close — so in practice the abort never fires. That is the code's
//     own stated intent ("the repair IS the fix"), and it is narrower than
//     CLAUDE.md's wording.
//
//  3. S84's specific reproduction — deleting `plan_correct` from ONE agent's
//     stored map — was never a gap by the definition CLAUDE.md itself gives
//     ("global sandbox.tool_policies AND/OR an agent's tools.builtin.policies"),
//     because pkg/config/defaults.go seeds the global ceiling with
//     `plan_correct: allow`. Not aborting was correct there.
//
//  4. The state that IS dangerous, and was invisible to every check, is the
//     one-sided hole: an agent that HAS its own policy map but is missing an
//     entry, so the permissive ceiling decides alone. That is what emptied a
//     `bash: deny` agent's map into a working bash call in UAT batch 2, and it
//     is what config.ValidateAgentOwnToolPolicyCoverage now names at Error.
package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestRepairAndValidate_BothSidesGap_IsRepairedNotAborted documents point 2
// above with a live assertion rather than a comment: given a tool with NO entry
// on either side, the shared boot/reload helper backfills it to explicit "deny"
// and returns ZERO remaining gaps — so the caller's abort branch is not taken.
//
// This is the behaviour that makes CLAUDE.md's "aborts on any gap" wording an
// over-claim. Pinning it means a future change that removes the repair (making
// the abort reachable again) fails here loudly and deliberately, instead of
// silently turning every catalog addition into a boot-brick on upgraded
// installs — the exact reason the repair exists.
func TestRepairAndValidate_BothSidesGap_IsRepairedNotAborted(t *testing.T) {
	cfg := &config.Config{
		// A deliberately EMPTY global ceiling: without it, every static tool is
		// a genuine both-sides gap for this agent.
		Sandbox: config.OmnipusSandboxConfig{ToolPolicies: map[string]string{}},
		Agents: config.AgentsConfig{List: []config.AgentConfig{{
			ID:    "legacy-agent",
			Tools: &config.AgentToolsCfg{},
		}}},
	}

	gaps := repairAndValidateToolPolicyCoverage(cfg)
	assert.Empty(t, gaps,
		"the repair closes every closable gap before validation runs, so the boot abort is "+
			"unreachable in practice — if this ever becomes non-empty, the abort semantics changed")

	require.NotNil(t, cfg.Agents.List[0].Tools)
	policies := cfg.Agents.List[0].Tools.Builtin.Policies
	require.NotEmpty(t, policies, "the repair must have materialized explicit entries")
	assert.Equal(t, config.ToolPolicyDeny, policies["bash"],
		"the backfill direction must be fail-closed deny, never allow")

	// And the repaired config is genuinely complete by the coverage definition.
	assert.Empty(t, config.ValidateToolPolicyCoverage(cfg, buildKnownBuiltinToolNames()))
}

// TestRepairAndValidate_PerAgentHoleUnderSeededCeiling_IsDetected is S84's
// scenario, run against the REAL seeded global ceiling: an agent whose own map
// is complete except for one deliberately deleted key.
//
// Coverage validation reports nothing (correctly — the ceiling covers it), the
// repair therefore backfills nothing, and boot proceeds. What must NOT happen is
// that the state goes entirely unnoticed: ValidateAgentOwnToolPolicyCoverage has
// to name the exact (agent, tool) pair, because that tool's policy is now
// decided by the ceiling alone and any per-agent tightening for it is dead.
func TestRepairAndValidate_PerAgentHoleUnderSeededCeiling_IsDetected(t *testing.T) {
	known := buildKnownBuiltinToolNames()
	require.Contains(t, known, "plan_correct", "fixture depends on the tool S84 deleted")

	policies := make(map[string]config.ToolPolicy, len(known))
	for name := range known {
		policies[name] = config.ToolPolicyAllow
	}
	// The hand-edit S84 performed on disk.
	delete(policies, "plan_correct")

	seeded := config.DefaultConfig()
	cfg := &config.Config{
		Sandbox: config.OmnipusSandboxConfig{ToolPolicies: seeded.Sandbox.ToolPolicies},
		Agents: config.AgentsConfig{List: []config.AgentConfig{{
			ID:    "tampered-agent",
			Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: policies}},
		}}},
	}
	require.NotEmpty(t, cfg.Sandbox.ToolPolicies,
		"this test is only meaningful against the real seeded ceiling")
	require.Contains(t, cfg.Sandbox.ToolPolicies, "plan_correct",
		"the seeded ceiling covers plan_correct — which is precisely why S84 saw no boot abort")

	// Coverage is satisfied, so boot does not abort. Asserting this explicitly
	// records WHY, rather than leaving the UAT's "it should have aborted" claim
	// standing unexamined.
	assert.Empty(t, repairAndValidateToolPolicyCoverage(cfg),
		"the global ceiling covers plan_correct, so this is not a coverage gap and boot "+
			"correctly proceeds — S84's expectation of an abort did not match the documented "+
			"'global and/or per-agent' definition")

	// The finding that DOES matter, and that nothing reported before.
	own := config.ValidateAgentOwnToolPolicyCoverage(cfg, known)
	require.Len(t, own, 1, "exactly the one deleted key must be reported")
	assert.Equal(t, "tampered-agent", own[0].AgentID)
	assert.Equal(t, "plan_correct", own[0].ToolName)
}

// TestRepairAndValidate_HealthySeededConfig_ProducesNoFindings guards against
// the new Error-level report crying wolf on every boot: a config whose agents
// carry complete maps must produce zero own-coverage findings. Without this,
// the detector could be trivially "correct" by reporting everything always, and
// operators would learn to ignore it.
func TestRepairAndValidate_HealthySeededConfig_ProducesNoFindings(t *testing.T) {
	known := buildKnownBuiltinToolNames()
	policies := make(map[string]config.ToolPolicy, len(known))
	for name := range known {
		policies[name] = config.ToolPolicyAsk
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{List: []config.AgentConfig{{
			ID:    "healthy-agent",
			Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: policies}},
		}}},
	}
	assert.Empty(t, config.ValidateAgentOwnToolPolicyCoverage(cfg, known))
	assert.Empty(t, repairAndValidateToolPolicyCoverage(cfg))
}
