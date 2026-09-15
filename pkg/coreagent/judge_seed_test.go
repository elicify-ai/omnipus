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
// verifier-scoped inspect_session tool, plus ToolSearch and Skill — the two
// structural floors every agent gets (CLAUDE.md constraint 6; ADR-072 D1) so
// it can reach any tiered (lazy/search-only) tool or any skill's content at
// all, applying even to a System Agent. Every other static builtin name must
// resolve to explicit deny.
var judgeAllowedTools = map[string]bool{
	"read_file":       true,
	"list_directory":  true,
	"inspect_session": true,
	"ToolSearch":      true,
	"Skill":           true,
}

// TestSeed_JudgeSystemAgent verifies ADR-049 D3 / US-4 Acceptance Scenario 1,
// redefined by ADR-052 R3-2/FR-027 (DS-6, Test 19): a fresh SeedConfig
// produces EXACTLY ONE type:system Judge that is locked, non-default, not a
// chat target, and has an explicit tool policy enumerating every static
// builtin — EXACTLY the seeded verifier set allow (read_file/list_directory/
// inspect_session), everything else deny (no longer "all-deny"). ADR-052
// FR-038 deleted AgentConfig.Rubric (soul unification): the Judge's prompt is
// now its SOUL.md, lazily seeded from coreagent.JudgeDefaultRubric by
// pkg/agent's ensureVerifierSoul on first real verifier dispatch — not a
// config-struct field SeedConfig populates, so there is nothing rubric-shaped
// left to assert here.
func TestSeed_JudgeSystemAgent(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg), "fresh SeedConfig must report modified=true")

	var systemAgents []config.AgentConfig
	var judges []config.AgentConfig
	for _, a := range cfg.Agents.List {
		if a.Type == config.AgentTypeSystem {
			systemAgents = append(systemAgents, a)
		}
		if a.ID == string(coreagent.IDJudge) {
			judges = append(judges, a)
		}
	}
	// The System-Agents roster grew past the Judge with ADR-055
	// (PlanSupervisor), so this asserts the roster matches SystemAgents()
	// exactly rather than the old "exactly one" — a count literal here would
	// have to be edited by every future System Agent, and asserting against
	// the roster catches the real failure (a System Agent seeded with the
	// wrong Type, or seeded twice) instead of merely a changed number.
	require.Len(t, systemAgents, len(coreagent.SystemAgents()),
		"every agent in SystemAgents() must be seeded exactly once with Type=system")
	require.Len(t, judges, 1, "the Judge must be seeded exactly once")

	j := judges[0]
	assert.Equal(t, "judge", j.ID, "the System Agent must be the Judge")
	assert.Equal(t, config.AgentTypeSystem, j.Type)
	assert.True(t, j.Locked, "Judge must be locked")
	assert.False(t, j.Default, "Judge must never be the default agent")
	assert.False(t, j.IsChatTarget(), "Judge must not be a chat target")
	assert.True(t, j.IsSystem(), "Judge must report IsSystem()==true")

	// Explicit EXACT-SET policy over the ENTIRE static builtin catalog, no
	// gaps: read_file/list_directory/inspect_session allow, everything else
	// deny (ADR-052 — the Judge is a real agent in a verifier role, not a
	// no-tools structured call).
	require.NotNil(t, j.Tools, "Judge must carry an explicit tools policy")
	pol := j.Tools.Builtin.Policies
	catalog := coreagent.AllStaticToolNames()
	for _, name := range catalog {
		p, ok := pol[name]
		require.Truef(t, ok, "Judge policy must enumerate tool %q (Constraint #6, no default fallback)", name)
		if judgeAllowedTools[name] {
			assert.Equalf(t, config.ToolPolicyAllow, p, "Judge policy for %q must be allow (verifier set)", name)
		} else {
			assert.Equalf(t, config.ToolPolicyDeny, p, "Judge policy for %q must be deny", name)
		}
	}
	// JUDGE-FR-058 (T4, ADR-084/085/086 joint delivery plan §3): the Judge's
	// policy map is the static catalog PLUS exactly one extra, non-catalog
	// key — "mcp_*" — stamped directly onto the map (not a member of
	// AllStaticToolNames(), since it is a wildcard and Constraint #6's
	// no-wildcard rule applies only to the static builtin catalog). This
	// replaces the old exact "len(pol) == len(catalog)" assertion, which
	// left no room for that key at all and would have gone red the moment
	// systemAgentSeed(IDJudge) started stamping it (see that function's own
	// comment on why the stamp is deliberately not landed yet). Asserting
	// "at most one extra key, and if present it must be exactly mcp_*:deny"
	// keeps Constraint #6's no-stray-key discipline intact while making
	// room for the one documented exception.
	assertJudgePolicyHasOnlyCatalogPlusMCPWildcard(t, pol, catalog)
}

// assertJudgePolicyHasOnlyCatalogPlusMCPWildcard asserts pol contains no key
// beyond catalog except, optionally, "mcp_*" — and if "mcp_*" is present, it
// must be deny (JUDGE-FR-058). Shared by TestSeed_JudgeSystemAgent and
// TestSeed_JudgeExactSetReEnforced so both stay in lockstep with each other
// as this wave's forward-compatible shape.
func assertJudgePolicyHasOnlyCatalogPlusMCPWildcard(t *testing.T, pol map[string]config.ToolPolicy, catalog []string) {
	t.Helper()
	known := make(map[string]bool, len(catalog))
	for _, name := range catalog {
		known[name] = true
	}
	var stray []string
	for name := range pol {
		if known[name] || name == "mcp_*" {
			continue
		}
		stray = append(stray, name)
	}
	assert.Emptyf(t, stray,
		"Judge policy must carry no key outside the static catalog other than the FR-058 mcp_* wildcard, got stray key(s): %v", stray)
	if mcpPolicy, ok := pol["mcp_*"]; ok {
		assert.Equalf(t, config.ToolPolicyDeny, mcpPolicy, "JUDGE-FR-058: the mcp_* wildcard, when present, must be deny")
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
	catalog := coreagent.AllStaticToolNames()
	for _, name := range catalog {
		if judgeAllowedTools[name] {
			assert.Equalf(t, config.ToolPolicyAllow, pol[name],
				"tampered verifier-set tool %q must be re-enforced back to allow", name)
		} else {
			assert.Equalf(t, config.ToolPolicyDeny, pol[name],
				"tampered non-verifier tool %q must be re-enforced back to deny (no stray grant survives)", name)
		}
	}
	// JUDGE-FR-058 (T4): re-enforcement must not leave any OTHER stray key
	// behind either — see assertJudgePolicyHasOnlyCatalogPlusMCPWildcard's
	// own doc comment for why "mcp_*" alone is the one tolerated exception.
	assertJudgePolicyHasOnlyCatalogPlusMCPWildcard(t, pol, catalog)
}

// TestJudgeSeed_MCPWildcardDenied is the Test Matrix's named oracle for
// JUDGE-FR-058 (judge-active-reviewer-spec.md's
// "TestJudgeSeed_MCPWildcardDenied", extended here per the ADR-084/085/086
// joint delivery plan §3 T4 row): systemAgentSeed(IDJudge) MUST include an
// explicit "mcp_*": deny wildcard, stamped directly onto the map
// denyAllThenOverride returns (C5) — "mcp_*" is a wildcard key and must NOT
// be a member of AllStaticToolNames(), so it cannot be expressed through
// denyAllThenOverride's normal "one literal entry per catalog name"
// mechanism.
//
// BLOCKED (reported, not silently narrowed): as of this wave, core.go's own
// comment on systemAgentSeed's IDJudge case states the stamp is
// "deliberately NOT implemented here" — production code has not landed it.
// This test is written to the CORRECT, spec-derived oracle and is expected
// to FAIL until a backend wave lands the stamp; see this wave's final
// report for the full explanation of why that landing could not happen in
// THIS wave (Must-NOT-touch: all production code) and which wave in the
// joint delivery plan's graph is missing it.
func TestJudgeSeed_MCPWildcardDenied(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))

	var j *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == "judge" {
			j = &cfg.Agents.List[i]
		}
	}
	require.NotNil(t, j, "Judge must be seeded")
	require.NotNil(t, j.Tools, "Judge must carry an explicit tools policy")
	pol := j.Tools.Builtin.Policies

	mcpPolicy, ok := pol["mcp_*"]
	require.True(t, ok,
		"JUDGE-FR-058: systemAgentSeed(IDJudge) must stamp an explicit \"mcp_*\": deny wildcard onto the "+
			"Judge's policy map. Without this literal key, resolveEffectivePolicyWith's wildcard matcher "+
			"has nothing to match on, and any MCP-namespaced tool falls through to the global ceiling "+
			"alone with no Judge-side opinion at all — exactly the gap FR-058 exists to close.")
	assert.Equal(t, config.ToolPolicyDeny, mcpPolicy, "JUDGE-FR-058: the mcp_* wildcard must resolve deny")
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
// type) is repaired on the next SeedConfig, while its operator-editable model
// is PRESERVED. (ADR-052 FR-038 deleted AgentConfig.Rubric — the Judge's
// operator-editable prompt now lives in SOUL.md, outside SeedConfig's
// config-struct-only mutation path, so there is no rubric field left to
// tamper/preserve here.)
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
		"granted tool re-enforced back to deny (exact-set invariant: bash is outside the verifier read-only set)")
	// Operator-editable fields preserved.
	require.NotNil(t, j.Model)
	assert.Equal(t, "operator/model", j.Model.Primary, "operator model edit must survive re-enforcement")
}

// TestSeed_JudgeMemoryDisabled verifies ADR-052 FR-039 (fix-wave finding #1):
// a fresh SeedConfig seeds the Judge with MemoryEnabled explicitly false (not
// nil/omitted) — memory-off is an impartiality property (reproducible
// verdicts: same evidence -> same verdict), not a preference, and the
// ContextBuilder's memory injection would otherwise include the shared
// workspace memory room, making verdicts non-reproducible across
// adjudications.
func TestSeed_JudgeMemoryDisabled(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))

	var j *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == "judge" {
			j = &cfg.Agents.List[i]
		}
	}
	require.NotNil(t, j, "Judge must be seeded")
	require.NotNil(t, j.MemoryEnabled, "Judge's MemoryEnabled must be explicitly set, not left nil")
	assert.False(t, *j.MemoryEnabled, "Judge's MemoryEnabled must be explicit false")
	assert.False(t, j.MemoryEnabledEffective(), "Judge's MemoryEnabledEffective() must resolve false")
}

// TestSeed_JudgeMemoryReEnforced_BothDirections verifies the tamper
// re-enforcement is bidirectional (fix-wave finding #1): an operator/tamper
// flip to EITHER nil (which would resolve true via MemoryEnabledEffective's
// "unset defaults to true" rule) OR an explicit true is repaired back to
// explicit false on the next SeedConfig — this is an impartiality property,
// not an operator preference, so unlike Model/Provider it does NOT survive
// re-enforcement.
func TestSeed_JudgeMemoryReEnforced_BothDirections(t *testing.T) {
	t.Run("tampered to explicit true", func(t *testing.T) {
		cfg := &config.Config{}
		require.True(t, coreagent.SeedConfig(cfg))
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == "judge" {
				enabled := true
				cfg.Agents.List[i].MemoryEnabled = &enabled
			}
		}
		require.True(t, coreagent.SeedConfig(cfg), "re-enforcement must report modified=true after tamper")

		var j *config.AgentConfig
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == "judge" {
				j = &cfg.Agents.List[i]
			}
		}
		require.NotNil(t, j)
		require.NotNil(t, j.MemoryEnabled)
		assert.False(t, *j.MemoryEnabled, "tampered explicit-true must be repaired back to explicit false")
	})

	t.Run("tampered/reset to nil", func(t *testing.T) {
		cfg := &config.Config{}
		require.True(t, coreagent.SeedConfig(cfg))
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == "judge" {
				cfg.Agents.List[i].MemoryEnabled = nil
			}
		}
		require.True(t, coreagent.SeedConfig(cfg), "re-enforcement must report modified=true after reset to nil")

		var j *config.AgentConfig
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == "judge" {
				j = &cfg.Agents.List[i]
			}
		}
		require.NotNil(t, j)
		require.NotNil(t, j.MemoryEnabled, "nil must be repaired back to an explicit value")
		assert.False(t, *j.MemoryEnabled, "repaired value must be false")
	})

	t.Run("already-correct explicit false is a no-op (no spurious modified=true)", func(t *testing.T) {
		cfg := &config.Config{}
		require.True(t, coreagent.SeedConfig(cfg))
		// Re-seeding an already-correctly-seeded config must not report
		// modified=true purely due to MemoryEnabled (it was already false).
		modifiedAgain := coreagent.SeedConfig(cfg)
		assert.False(t, modifiedAgain, "re-seeding an already-correct config must be a no-op")
	})
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
