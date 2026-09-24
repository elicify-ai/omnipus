// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression tests for two verified boot/reload robustness bugs:
//
//  1. persistSeededCoreAgents (extracted from RunContextWithOptions) must not
//     abort boot when a single core-agent entity record is corrupt/
//     unparseable — a store.Get() parse error must never be misclassified as
//     "record does not exist" (which previously drove a doomed
//     store.Create -> entity.ErrAlreadyExists -> hard boot abort, inverting
//     ADR-054 D7's "unparseable record -> skip + ERROR + mark degraded").
//  2. populateAgentsListFromEntityStoreStrict must REJECT (never silently
//     empty) the in-memory agent roster on a genuine entity-store failure,
//     because pkg/agent/registry.go's always-registered "main" sentinel
//     AgentConfig (no Tools/Policies at all) combined with
//     pkg/tools/compositor.go's global×agent policy merge means an empty
//     roster silently promotes ALL routed traffic to the permissive global
//     tool-policy floor (pkg/config/defaults.go) instead of any real agent's
//     own deny-heavy policy — a verified privilege-escalation chain, not a
//     theoretical one.
package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
)

// TestEmptyAgentRosterWouldResolveToPermissiveGlobalFloor documents, using
// the REAL production merge logic (pkg/tools.ResolveEffectivePolicy) and the
// REAL seeded global defaults (pkg/config.DefaultConfig), exactly why an
// empty/wiped agent roster is a privilege-escalation risk and not merely a
// UX gap: an agent with no per-agent tool-policy entries at all (nil
// Policies map — precisely what pkg/agent/registry.go's always-registered
// "main" sentinel AgentConfig has, since it is built fresh with no Tools
// field ever set) resolves security-sensitive tools to "allow" via the
// global floor alone, because there is no agent-side entry to out-rank it.
// This is the scenario populateAgentsListFromEntityStoreStrict exists to
// prevent a live gateway from ever silently falling into (see its own doc
// comment for the full causal chain: NewAgentRegistry's sentinel ->
// compositor's global×agent merge -> defaults.go's seeded floor ->
// repairAndValidateToolPolicyCoverage's vacuous pass on an empty roster).
func TestEmptyAgentRosterWouldResolveToPermissiveGlobalFloor(t *testing.T) {
	defaultCfg := config.DefaultConfig()
	globalPolicies := make(map[string]config.ToolPolicy, len(defaultCfg.Sandbox.ToolPolicies))
	for k, v := range defaultCfg.Sandbox.ToolPolicies {
		globalPolicies[k] = config.ToolPolicy(v)
	}

	// No per-agent Policies at all — mirrors registry.go's sentinel
	// AgentConfig{ID: DefaultAgentID}, which never has Tools set.
	polCfg := &tools.ToolPolicyCfg{GlobalPolicies: globalPolicies}

	// Expected floor per tool, taken from pkg/config/defaults.go's seeded
	// global ceiling. "bash" is intentionally NOT "allow" here (ADR-092,
	// founder decision 2026-09-23): the global ceiling ships "ask" for bash
	// so the D1 Ask/Auto/God Mode selector and the D7/D8 pre-flights engage
	// on a fresh install — see defaults.go's own comment beside the "bash":
	// "ask" seed. That is a stricter floor than the other sensitive tools
	// below, not a weaker one; the point this test documents — a roster with
	// no per-agent policy entry falls through to the seeded global floor,
	// not to any agent's own deny-heavy policy — holds regardless of what
	// that floor's value is for a given tool.
	expected := map[string]string{
		"bash":       "ask",
		"write_file": "allow",
		"edit_file":  "allow",
		"delegate":   "allow",
		"send_email": "allow",
	}

	for _, sensitive := range []string{"bash", "write_file", "edit_file", "delegate", "send_email"} {
		got := tools.ResolveEffectivePolicy(polCfg, sensitive)
		assert.Equal(t, expected[sensitive], got,
			"tool %q: an agent with no per-agent policy entry falls through to the seeded global "+
				"floor — this is exactly why populateAgentsListFromEntityStoreStrict must never let "+
				"the roster go silently empty", sensitive)
	}
}
