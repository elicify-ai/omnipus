package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestResolveApprovalToolPolicy_ToolSearchSeededAllow proves the CURRENT fix
// for the two-gate infra reachability bug: the tool-approval exec gate must
// resolve "allow" for ToolSearch for a deny-by-default agent (e.g. Ava), or
// every lazy/load-on-demand tool becomes unreachable at execution time.
//
// This used to be an unconditional, code-level "infra force-allow" inside
// resolveApprovalToolPolicy (bypassing real policy data entirely) — a
// CLAUDE.md hard-constraint-6 violation (a hardcoded allow fallback, invisible
// to an operator reading their own config) and has been removed. ToolSearch
// now resolves "allow" because it is seeded as real, explicit policy data on
// the agent (mirroring what pkg/coreagent/core.go's seed functions grant
// every real agent), exactly like any other allowed tool — the "ava" agent
// below carries an explicit "ToolSearch": allow entry rather than relying on
// a bypass. The resolution must still stay NARROW — it must not widen any
// other tool the agent's policy denies.
func TestResolveApprovalToolPolicy_ToolSearchSeededAllow(t *testing.T) {
	// Ava: deny-by-default; create_agent/list_models/ToolSearch explicitly
	// allowed (real seeded data, not a bypass). There is no DefaultPolicy
	// field any more (CLAUDE.md hard constraint 6) — "exec" gets an explicit
	// "deny" entry below (replacing the removed default-deny fallback) so the
	// "must not widen a denied tool" assertion below exercises an explicit
	// policy decision rather than incidentally relying on the fail-closed "no
	// entry on either side" path.
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{
					ID: "ava",
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
							Policies: map[string]config.ToolPolicy{
								"create_agent": config.ToolPolicyAllow,
								"list_models":  config.ToolPolicyAllow,
								"ToolSearch":   config.ToolPolicyAllow,
								"exec":         config.ToolPolicyDeny,
							},
						},
					},
				},
			},
		},
	}

	// ToolSearch resolves "allow" from its own real seeded policy entry.
	assert.Equal(t, "allow", resolveApprovalToolPolicy(cfg, "ToolSearch", "ava"),
		"ToolSearch must resolve allow for a deny-default agent that seeds it explicitly")
	// An allowed lazy tool stays allowed.
	assert.Equal(t, "allow", resolveApprovalToolPolicy(cfg, "create_agent", "ava"))
	// A tool the agent denies is STILL denied — ToolSearch's grant must not widen it.
	assert.Equal(t, "deny", resolveApprovalToolPolicy(cfg, "exec", "ava"))
	// A nil config has zero seeded policy data of any kind (CLAUDE.md hard
	// constraint 6 — no code-level fallback), so EVERY tool — ToolSearch
	// included — defaults to interactive approval, not a language-level allow.
	assert.Equal(t, "ask", resolveApprovalToolPolicy(nil, "ToolSearch", "ava"))
	assert.Equal(t, "ask", resolveApprovalToolPolicy(nil, "exec", "ava"))
}
