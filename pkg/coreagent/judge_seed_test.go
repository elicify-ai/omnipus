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

// judgeAllowedTools is the Judge's exact seeded verifier-role allow-set
// (ADR-052 R3-2/FR-011/FR-012/FR-033): read-only file inspection plus the
// verifier-scoped inspect_session tool. Every other static builtin name must
// resolve to explicit deny.
var judgeAllowedTools = map[string]bool{
	"read_file":       true,
	"list_directory":  true,
	"inspect_session": true,
}

// TestSeed_JudgeSystemAgent verifies ADR-049 D3 / US-4 Acceptance Scenario 1,
// redefined by ADR-052 R3-2/FR-027 (DS-6, Test 19): a fresh SeedConfig
// produces EXACTLY ONE type:system Judge that is locked, non-default, not a
// chat target, carries an editable rubric, and has an explicit tool policy
// enumerating every static builtin — EXACTLY the seeded verifier set allow
// (read_file/list_directory/inspect_session), everything else deny (no
// longer "all-deny").
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

	// Explicit EXACT-SET policy over the ENTIRE static builtin catalog, no
	// gaps: read_file/list_directory/inspect_session allow, everything else
	// deny (ADR-052 — the Judge is a real agent in a verifier role, not a
	// no-tools structured call).
	require.NotNil(t, j.Tools, "Judge must carry an explicit tools policy")
	pol := j.Tools.Builtin.Policies
	catalog := coreagent.AllStaticToolNames()
	require.Len(t, pol, len(catalog),
		"Judge policy must be exactly the static builtin catalog, one literal entry each")
	for _, name := range catalog {
		p, ok := pol[name]
		require.Truef(t, ok, "Judge policy must enumerate tool %q (Constraint #6, no default fallback)", name)
		if judgeAllowedTools[name] {
			assert.Equalf(t, config.ToolPolicyAllow, p, "Judge policy for %q must be allow (verifier set)", name)
		} else {
			assert.Equalf(t, config.ToolPolicyDeny, p, "Judge policy for %q must be deny", name)
		}
	}
}

// TestSeed_JudgeExactSetReEnforced verifies DS-6 / Test 19's exact-set
// re-enforcement invariant (ADR-052 R3-2): a tampered Judge tool policy —
// whether an unwanted grant OUTSIDE the verifier set, or a stray DENY on a
// tool INSIDE the verifier set — is repaired back to exactly the seeded set
// on the next SeedConfig, same as the pre-ADR-052 all-deny invariant it
// replaces.
func TestSeed_JudgeExactSetReEnforced(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))

	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID != "judge" {
			continue
		}
		// Tamper: grant something outside the verifier set, and revoke
		// something inside it.
		cfg.Agents.List[i].Tools.Builtin.Policies["bash"] = config.ToolPolicyAllow
		cfg.Agents.List[i].Tools.Builtin.Policies["create_plan"] = config.ToolPolicyAllow
		cfg.Agents.List[i].Tools.Builtin.Policies["inspect_session"] = config.ToolPolicyDeny
		cfg.Agents.List[i].Tools.Builtin.Policies["read_file"] = config.ToolPolicyAsk
	}

	require.True(t, coreagent.SeedConfig(cfg), "re-enforcement must report modified=true after tamper")

	var j *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == "judge" {
			j = &cfg.Agents.List[i]
		}
	}
	require.NotNil(t, j)
	pol := j.Tools.Builtin.Policies
	for _, name := range coreagent.AllStaticToolNames() {
		if judgeAllowedTools[name] {
			assert.Equalf(t, config.ToolPolicyAllow, pol[name],
				"tampered verifier-set tool %q must be re-enforced back to allow", name)
		} else {
			assert.Equalf(t, config.ToolPolicyDeny, pol[name],
				"tampered non-verifier tool %q must be re-enforced back to deny (no stray grant survives)", name)
		}
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
