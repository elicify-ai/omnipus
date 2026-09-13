// Omnipus — Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// compositor_judge_mcp_adr084_test.go — ADR-084 revision 9, JUDGE-FR-058
// (wave T4, round 7 of the ADR-084/085/086 joint delivery plan §3): "the
// Judge's per-agent 'mcp_*': deny wildcard must resolve deny for a real MCP
// tool name EVEN WHEN the global ceiling allows it" — the whole point of
// FR-058 is that no operator-set global permissiveness can ever let the
// Judge reach an MCP server's tools.
//
// This exercises the SAME merge primitive compositor_mcp_policy_test.go
// already covers for ordinary builtin/MCP wildcard interactions
// (resolveEffectivePolicyWith via the public ResolveEffectivePolicy), with
// one addition: a GLOBAL entry that ALLOWS, opposite the agent-side DENY, so
// the assertion is genuinely about strictest-wins across sides — not merely
// "an agent-side wildcard matches its own tools."
//
// This test does NOT require pkg/coreagent's systemAgentSeed to actually
// carry "mcp_*": deny yet (that production change is out of this wave's
// write-set — see pkg/coreagent/judge_seed_test.go's own note). It proves
// the underlying merge mechanism FR-058 depends on works correctly in
// isolation, by constructing the Judge-shaped policy map directly.
//
// Oracle discipline: every expected value is derived from FR-058's own text
// ("MUST include an explicit 'mcp_*': deny wildcard") and from
// resolveEffectivePolicyWith's own documented contract (deny > ask > allow,
// most-specific-wins WITHIN one side, but either side's deny always wins
// the merge regardless of specificity) — never from reading the merge
// function's behaviour back.
package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestJudgeEffectivePolicy_MCPToolDeniedDespiteAllowCeiling is the Test
// Matrix's named oracle for FR-058
// (judge-active-reviewer-spec.md's "TestJudgeEffectivePolicy_MCPTool
// DeniedDespiteAllowCeiling").
func TestJudgeEffectivePolicy_MCPToolDeniedDespiteAllowCeiling(t *testing.T) {
	// The Judge's own per-agent map, shaped as FR-058 requires it: every
	// static builtin tool explicit (elided here — irrelevant to this
	// specific tool's resolution, since resolveFromMap only ever consults
	// the entries that actually match) plus the FR-058 wildcard.
	judgePolicies := map[string]config.ToolPolicy{
		"mcp_*": config.ToolPolicyDeny,
	}

	t.Run("global EXACT allow for one MCP tool is still overridden by the Judge's mcp_* deny", func(t *testing.T) {
		cfg := &ToolPolicyCfg{
			Policies:       judgePolicies,
			GlobalPolicies: map[string]config.ToolPolicy{"mcp_context7_query": config.ToolPolicyAllow},
		}
		got := ResolveEffectivePolicy(cfg, "mcp_context7_query")
		assert.Equal(t, "deny", got,
			"JUDGE-FR-058: an MCP tool must resolve deny for the Judge even when the operator's global "+
				"ceiling grants it allow by exact name — deny always wins the merge, regardless of which "+
				"side's match is more specific")
	})

	t.Run("global WILDCARD allow for the whole server is still overridden by the Judge's mcp_* deny", func(t *testing.T) {
		cfg := &ToolPolicyCfg{
			Policies:       judgePolicies,
			GlobalPolicies: map[string]config.ToolPolicy{"mcp_context7_*": config.ToolPolicyAllow},
		}
		got := ResolveEffectivePolicy(cfg, "mcp_context7_querydoc")
		assert.Equal(t, "deny", got,
			"JUDGE-FR-058: a global per-server bulk allow must not reopen MCP access for the Judge either")
	})

	t.Run("differentiation: a DIFFERENT MCP server/tool name is independently denied too, not by coincidence of the first case's name", func(t *testing.T) {
		cfg := &ToolPolicyCfg{
			Policies:       judgePolicies,
			GlobalPolicies: map[string]config.ToolPolicy{"mcp_search_index": config.ToolPolicyAllow},
		}
		got := ResolveEffectivePolicy(cfg, "mcp_search_index")
		assert.Equal(t, "deny", got,
			"the denial must generalize across MCP tool names, not be a special case of one fixture string")
	})

	t.Run("negative control: WITHOUT the Judge's mcp_* wildcard, the same global allow resolves allow", func(t *testing.T) {
		// Proves the deny above is actually caused by the Judge's own
		// wildcard, not by some other always-deny default — an agent with
		// no per-agent opinion on this tool at all must inherit the global
		// ceiling's allow, exactly as CLAUDE.md constraint 6 describes
		// ("a missing side is not treated as an implicit allow" cuts the
		// OTHER way: a missing side just means the one present side alone
		// decides).
		cfg := &ToolPolicyCfg{
			Policies:       map[string]config.ToolPolicy{}, // no agent-side opinion at all
			GlobalPolicies: map[string]config.ToolPolicy{"mcp_context7_query": config.ToolPolicyAllow},
		}
		got := ResolveEffectivePolicy(cfg, "mcp_context7_query")
		assert.Equal(t, "allow", got,
			"sanity check: without the Judge's own mcp_* deny, the same global allow must actually resolve "+
				"allow — otherwise the positive assertions above would pass for the wrong reason (a hardcoded "+
				"deny somewhere, not FR-058's wildcard)")
	})

	t.Run("god mode still floors every tool at allow, even for the Judge's mcp_* deny (documented O14 precedence, unaffected by FR-058)", func(t *testing.T) {
		// Not a gap in FR-058 — god mode's override is documented to
		// short-circuit BEFORE the global x agent merge for every tool and
		// every agent uniformly (resolveEffectivePolicyWith's own doc
		// comment). Pinned here so a future change to FR-058's wildcard
		// cannot accidentally special-case the Judge out of god mode's
		// documented (if dangerous) behaviour without a test noticing.
		cfg := &ToolPolicyCfg{
			Policies: judgePolicies,
			GodMode:  true,
		}
		got := ResolveEffectivePolicy(cfg, "mcp_context7_query")
		assert.Equal(t, "allow", got,
			"god mode floors every tool at allow uniformly; FR-058 does not (and structurally cannot, per "+
				"resolveEffectivePolicyWith) carve out an exception for MCP tools under god mode")
	})
}
