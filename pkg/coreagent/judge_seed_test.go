// Omnipus — Core Agents
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// TestSeed_JudgeSystemAgent verifies ADR-049 D3 / US-4 Acceptance Scenario 1: a
// fresh SeedConfig produces EXACTLY ONE type:system Judge that is locked,
// non-default, not a chat target, carries an editable rubric, and has an
// explicit all-deny tool policy enumerating every static builtin.
func TestSeed_JudgeSystemAgent(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg), "fresh SeedConfig must report modified=true")

	var systemAgents []config.AgentConfig
	for _, a := range cfg.Agents.List {
		if a.Type == config.AgentTypeSystem {
			systemAgents = append(systemAgents, a)
		}
	}
	require.Len(t, systemAgents, 1, "exactly one type:system agent must be seeded (the Judge)")

	j := systemAgents[0]
	assert.Equal(t, "judge", j.ID, "the System Agent must be the Judge")
	assert.Equal(t, config.AgentTypeSystem, j.Type)
	assert.True(t, j.Locked, "Judge must be locked")
	assert.False(t, j.Default, "Judge must never be the default agent")
	assert.False(t, j.IsChatTarget(), "Judge must not be a chat target")
	assert.True(t, j.IsSystem(), "Judge must report IsSystem()==true")
	assert.NotEmpty(t, j.Rubric, "Judge must be seeded with a default rubric (its editable system prompt)")

	// Explicit all-deny over the ENTIRE static builtin catalog, no gaps, no
	// non-deny entries (the Judge executes as a no-tools structured call).
	require.NotNil(t, j.Tools, "Judge must carry an explicit tools policy")
	pol := j.Tools.Builtin.Policies
	catalog := coreagent.AllStaticToolNames()
	require.Len(t, pol, len(catalog),
		"Judge policy must be exactly the static builtin catalog, one literal entry each")
	for _, name := range catalog {
		p, ok := pol[name]
		require.Truef(t, ok, "Judge policy must enumerate tool %q (Constraint #6, no default fallback)", name)
		assert.Equalf(t, config.ToolPolicyDeny, p, "Judge policy for %q must be deny", name)
	}
}

// TestSystemAgent_Constraint6_BootCoverage verifies US-4 Acceptance Scenario 2:
// with the Judge seeded, ValidateToolPolicyCoverage reports ZERO gaps FOR THE
// JUDGE, so the Constraint #6 boot agent×tool matrix stays total. (Whole-config
// coverage across every seeded agent is separately guarded by
// TestSeedConfig_FreshInstall_ZeroToolPolicyCoverageGaps.)
func TestSystemAgent_Constraint6_BootCoverage(t *testing.T) {
	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)

	known := make(map[string]struct{})
	for _, n := range coreagent.AllStaticToolNames() {
		known[n] = struct{}{}
	}
	gaps := config.ValidateToolPolicyCoverage(cfg, known)
	for _, g := range gaps {
		assert.NotEqualf(t, "judge", g.AgentID,
			"Judge must have ZERO coverage gaps; found gap for tool %q", g.ToolName)
	}
}

// TestSeed_JudgeReEnforced_Tamper verifies the boot re-enforcement (tamper
// protection): a tampered Judge (unlocked, marked default, granted a tool, wrong
// type) is repaired on the next SeedConfig, while its operator-editable rubric
// and model are PRESERVED.
func TestSeed_JudgeReEnforced_Tamper(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))

	// Tamper with the seeded Judge.
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID != "judge" {
			continue
		}
		cfg.Agents.List[i].Locked = false
		cfg.Agents.List[i].Default = true
		cfg.Agents.List[i].Type = config.AgentTypeCustom
		cfg.Agents.List[i].Tools.Builtin.Policies["bash"] = config.ToolPolicyAllow
		cfg.Agents.List[i].Rubric = "operator-customized rubric"
		cfg.Agents.List[i].Model = &config.AgentModelConfig{Primary: "operator/model"}
	}

	require.True(t, coreagent.SeedConfig(cfg), "re-enforcement must report modified=true after tamper")

	var j *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == "judge" {
			j = &cfg.Agents.List[i]
		}
	}
	require.NotNil(t, j)
	// Non-editable fields re-enforced.
	assert.True(t, j.Locked, "Locked re-enforced")
	assert.False(t, j.Default, "stray Default cleared")
	assert.Equal(t, config.AgentTypeSystem, j.Type, "Type re-enforced to system")
	assert.Equal(t, config.ToolPolicyDeny, j.Tools.Builtin.Policies["bash"],
		"granted tool re-enforced back to deny (no-tools invariant)")
	// Operator-editable fields preserved.
	assert.Equal(t, "operator-customized rubric", j.Rubric, "operator rubric edit must survive re-enforcement")
	require.NotNil(t, j.Model)
	assert.Equal(t, "operator/model", j.Model.Primary, "operator model edit must survive re-enforcement")
}

// TestSystemAgents_RosterDisjointFromAll asserts the Judge is seeded via the
// System-Agents path and is NEVER classified as core (ByID/IsCoreAgent iterate
// All(), which must exclude every System Agent).
func TestSystemAgents_RosterDisjointFromAll(t *testing.T) {
	require.True(t, coreagent.IsSystemAgentID(coreagent.IDJudge))
	assert.False(t, coreagent.IsCoreAgent(string(coreagent.IDJudge)),
		"the Judge must NOT be classified as a core agent")
	assert.Nil(t, coreagent.ByID(coreagent.IDJudge),
		"ByID (which iterates All()) must not find a System Agent")
	require.NotNil(t, coreagent.SystemAgentByID(coreagent.IDJudge))
	for _, a := range coreagent.All() {
		assert.NotEqual(t, coreagent.IDJudge, a.ID, "All() must not contain the Judge")
	}
}
