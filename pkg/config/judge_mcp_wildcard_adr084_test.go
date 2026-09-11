// Omnipus — Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_mcp_wildcard_adr084_test.go — ADR-084 revision 9, JUDGE-FR-058's
// two "loose ends to close, both assertions rather than changes" (wave T4,
// round 7 of the ADR-084/085/086 joint delivery plan §3):
//
//	"(1) ValidateToolPolicyCoverage and ValidateSubmittedToolPolicyMap now
//	see a NON-CATALOG key in a System Agent's policy map; the spec requires
//	a test asserting neither rejects it and neither logs it as drift."
//
// FR-058 itself requires systemAgentSeed(IDJudge) to stamp an explicit
// "mcp_*": deny wildcard directly onto the map denyAllThenOverride returns
// (pkg/coreagent/core.go). That production change is deliberately NOT part
// of this file's write-set (this wave writes tests only — see this
// package's own git history / the joint delivery plan for why the stamp
// itself is not landed by T4). What this file proves instead is the
// PRECONDITION the joint delivery plan's C5/FR-058 resolution depends on:
// the two validators this package owns already tolerate a non-catalog
// "mcp_*" key on a system agent's policy map, so landing the stamp will not
// newly trip either one.
//
// Oracle discipline: every expected value below is derived from CLAUDE.md
// hard constraint 6's own wording ("Exception — MCP tools: … per-server
// mcp_<server>_* wildcard bulk policies remain the mechanism there. The
// no-wildcard rule applies to the static builtin catalog only") and from
// ValidateSubmittedToolPolicyMap's own doc comment (the MCPToolPolicyKeyPrefix
// carve-out), never from reading systemAgentSeed's behaviour back — this
// wave may not touch pkg/coreagent/core.go at all.
package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// judgeMCPWildcardKnownTools is a small stand-in static catalog — just
// enough to exercise "every catalog tool covered, plus one extra
// non-catalog wildcard key" without depending on the real (98-tool)
// allStaticToolNames() catalog, which pkg/coreagent owns and this package
// must not import (it would be a config -> coreagent import cycle:
// pkg/coreagent already imports pkg/config).
func judgeMCPWildcardKnownTools() map[string]struct{} {
	return testKnownTools("read_file", "list_directory", "inspect_session", "ToolSearch", "Skill", "bash")
}

// judgeSeededPolicyMapWithMCPWildcard builds the Judge's policy map exactly
// the shape FR-058 requires: every catalog tool has an explicit literal
// entry (verifier set allow, everything else deny — Constraint #6, no
// default fallback), PLUS the one extra "mcp_*": deny wildcard key FR-058
// adds. This is what systemAgentSeed(IDJudge) must produce once the stamp
// lands; this test does not require it to exist yet.
func judgeSeededPolicyMapWithMCPWildcard() map[string]ToolPolicy {
	allow := ToolPolicyAllow
	deny := ToolPolicyDeny
	return map[string]ToolPolicy{
		"read_file":       allow,
		"list_directory":  allow,
		"inspect_session": allow,
		"ToolSearch":      allow,
		"Skill":           allow,
		"bash":            deny,
		// JUDGE-FR-058 (D10, C5): stamped directly onto the map, not passed
		// through denyAllThenOverride — "mcp_*" is a wildcard and must NOT
		// be a member of the static catalog.
		"mcp_*": deny,
	}
}

// TestToolPolicyValidators_AcceptNonCatalogWildcardOnSystemAgent is the
// Test Matrix's named oracle for FR-058 (judge-active-reviewer-spec.md,
// "TestToolPolicyValidators_AcceptNonCatalogWildcardOnSystemAgent"): neither
// ValidateToolPolicyCoverage nor ValidateSubmittedToolPolicyMap rejects, or
// drift-logs, the Judge's "mcp_*" key.
//
//   - ValidateToolPolicyCoverage: reports "drift" exclusively via its
//     returned []CoverageGap slice — there is no separate logging channel to
//     capture, so "does not drift-log it" is proven by the gap slice
//     containing no entry naming "mcp_*" (and, for the fully-seeded case
//     below, containing no gap at all).
//   - ValidateSubmittedToolPolicyMap: "mcp_*" must land in neither Missing
//     (it is not a catalog name, so it was never expected as a literal key)
//     nor Invalid (the MCPToolPolicyKeyPrefix carve-out accepts it).
func TestToolPolicyValidators_AcceptNonCatalogWildcardOnSystemAgent(t *testing.T) {
	known := judgeMCPWildcardKnownTools()
	judgePolicies := judgeSeededPolicyMapWithMCPWildcard()

	t.Run("ValidateToolPolicyCoverage reports no gap for the Judge, and none names mcp_*", func(t *testing.T) {
		cfg := &Config{
			Agents: AgentsConfig{
				List: []AgentConfig{
					{
						ID:   "judge",
						Type: AgentTypeSystem,
						Tools: &AgentToolsCfg{
							Builtin: AgentBuiltinToolsCfg{Policies: judgePolicies},
						},
					},
				},
			},
		}

		gaps := ValidateToolPolicyCoverage(cfg, known)
		assert.Empty(t, gaps,
			"the Judge's policy map covers every catalog tool explicitly; the extra mcp_* key must not "+
				"itself be reported as a coverage gap (there is no coverage gap mechanism for a key that "+
				"is not in knownTools at all — mcp_* is simply not iterated)")
		for _, g := range gaps {
			assert.NotEqual(t, "mcp_*", g.ToolName,
				"ValidateToolPolicyCoverage must never name the non-catalog mcp_* key as a gap")
		}
	})

	t.Run("ValidateSubmittedToolPolicyMap treats mcp_* as the documented carve-out, not a defect", func(t *testing.T) {
		// Submitted as map[string]string, mirroring how a REST write body
		// normalizes wire enums — ValidateSubmittedToolPolicyMap is generic
		// over ~string precisely so both shapes are covered by one function.
		submitted := make(map[string]string, len(judgePolicies))
		for k, v := range judgePolicies {
			submitted[k] = string(v)
		}

		defects := ValidateSubmittedToolPolicyMap(submitted, known)
		assert.True(t, defects.Empty(),
			"a complete catalog map plus the mcp_* carve-out key must pass cleanly: %s", defects.String())
		assert.NotContains(t, defects.Missing, "mcp_*",
			"mcp_* is not a catalog name, so it can never be reported Missing")
		assert.NotContains(t, defects.Invalid, "mcp_*",
			"mcp_* carries the MCPToolPolicyKeyPrefix carve-out and must never be reported Invalid")
	})

	t.Run("ValidateSubmittedToolPolicyMap[ToolPolicy] (the typed map shape) also accepts it", func(t *testing.T) {
		// The real seed produces map[string]config.ToolPolicy directly (the
		// shape stored on AgentConfig.Tools.Builtin.Policies) — pin the
		// generic instantiation the seed itself would exercise, not only
		// the wire-normalized map[string]string one above.
		defects := ValidateSubmittedToolPolicyMap(judgePolicies, known)
		assert.True(t, defects.Empty(),
			"the typed ToolPolicy map instantiation must accept mcp_* identically to the string one: %s",
			defects.String())
	})
}

// TestToolPolicyValidators_MissingMCPWildcardStillReportsRealGaps is the
// negative control (Rule 4): the validators above must not have gone
// permissive across the board — a genuinely uncovered catalog tool is still
// reported, both with and without the mcp_* key present. Without this, a
// validator that had quietly stopped checking ANYTHING would also "pass"
// the positive test.
func TestToolPolicyValidators_MissingMCPWildcardStillReportsRealGaps(t *testing.T) {
	known := judgeMCPWildcardKnownTools()

	incomplete := judgeSeededPolicyMapWithMCPWildcard()
	delete(incomplete, "bash") // drop one real catalog entry

	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				{
					ID:   "judge",
					Type: AgentTypeSystem,
					Tools: &AgentToolsCfg{
						Builtin: AgentBuiltinToolsCfg{Policies: incomplete},
					},
				},
			},
		},
	}
	gaps := ValidateToolPolicyCoverage(cfg, known)
	require.Len(t, gaps, 1, "dropping bash from the Judge's map (with no global ceiling entry either) must still surface as a real gap")
	assert.Equal(t, "bash", gaps[0].ToolName)
	assert.Equal(t, "judge", gaps[0].AgentID)

	submitted := make(map[string]string, len(incomplete))
	for k, v := range incomplete {
		submitted[k] = string(v)
	}
	defects := ValidateSubmittedToolPolicyMap(submitted, known)
	require.False(t, defects.Empty(), "a submitted map missing a real catalog tool must still be rejected")
	assert.Equal(t, []string{"bash"}, defects.Missing,
		"the mcp_* carve-out must not mask an unrelated missing catalog entry")
	assert.Empty(t, defects.Invalid, "mcp_* itself is still not a defect in the negative-control map")
}
