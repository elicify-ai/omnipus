// Omnipus — ADR-081 D11 (unified-search-and-grep-spec.md FR-009/MV-8): the
// grep agent tool's ADR-077 two-layer governance. Spec test 27
// (TestGrep_PolicyAllTiersAndDriftBackfill) — every seeded agent tier
// carries an EXPLICIT "grep": allow (the founder ruling: no posture left to
// silent inheritance anywhere in the roster), and the upgrade-path drift
// backfill writes ALLOW for grep on every pre-existing agent, core-seeded
// AND operator-created alike — the recorded exception to the backfill's
// generic deny-by-default baseline (MV-8).
//
// MERGE NOTE (2026-09-15, release/v0.1.1 + library-improvements): the
// upgrade-simulation half of this suite (writePreGrepHome / loadUpgradedNoGrep /
// customAgentFromOlderReleaseNoGrep, modelled on the retired
// seed_upgrade_catalog_drift_test.go) was removed under the founder's
// greenfield ruling — no upgrade path, no migration. Only the fresh-install
// cases remain.
//
// FOUNDER RULING (2026-09-07): "There must not be any tool default to deny —
// the global policy sets the default, not any hardcoded default." Applied to
// grep specifically, this closed a gap this suite's ORIGINAL version left
// open on purpose (documented at length in the prior report as a deliberate,
// narrower scope decision): a BRAND-NEW custom agent — created fresh, never
// having predated grep's release — used to still get a hardcoded per-agent
// "grep": deny from coreagent.NewCustomAgentToolsCfg(), unlike every
// pre-existing agent's upgrade-path backfill (which already resolved
// allow). NewCustomAgentToolsCfg now seeds "grep": allow explicitly (see its
// own doc comment in core.go) — the fresh-create and upgrade-backfill paths
// agree again. TestGrep_PolicyAllTiersAndDriftBackfill's new
// "fresh custom agent creation" subtest below pins this.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// grepRoster is the full set of agents whose seed MUST carry an explicit
// "grep" posture: the 4 base agents + the Worker + the 3 seeded specialists
// (coreagent.All()) plus the 2 System Agents (coreagent.SystemAgents()) —
// i.e. every agent SeedConfig produces. Read from the real exported
// enumerations, never hand-copied, so a future roster addition is picked up
// automatically rather than silently skipped by this suite.
func grepRoster() []string {
	ids := make([]string, 0, len(coreagent.All())+len(coreagent.SystemAgents()))
	for _, ca := range coreagent.All() {
		ids = append(ids, string(ca.ID))
	}
	for _, ca := range coreagent.SystemAgents() {
		ids = append(ids, string(ca.ID))
	}
	return ids
}

// TestGrep_PolicyAllTiersAndDriftBackfill is spec test 27 (MV-8).
func TestGrep_PolicyAllTiersAndDriftBackfill(t *testing.T) {
	t.Run("fresh install: every seeded tier carries an explicit grep=allow entry", func(t *testing.T) {
		cfg := config.DefaultConfig()
		require.True(t, coreagent.SeedConfig(cfg))

		for _, id := range grepRoster() {
			ac := findSeeded(t, cfg, id)
			require.NotNilf(t, ac.Tools, "agent %q must carry an explicit tools policy", id)
			p, ok := ac.Tools.Builtin.Policies["grep"]
			require.Truef(t, ok,
				"agent %q must carry an EXPLICIT grep entry (Constraint #6, no default fallback)", id)
			assert.Equalf(t, config.ToolPolicyAllow, p,
				"agent %q must seed grep=allow — ADR-081 D11 FR-009's founder ruling grants it "+
					"to every agent tier with no posture left to silent inheritance", id)
		}
	})

	t.Run("fresh install: grep resolves allow through the real compositor for every tier", func(t *testing.T) {
		cfg := config.DefaultConfig()
		require.True(t, coreagent.SeedConfig(cfg))

		for _, id := range grepRoster() {
			assert.Equalf(t, "allow", resolveFor(t, cfg, id, "grep", nil),
				"(%s, grep) must RESOLVE allow through the real production compositor merge", id)
		}
	})

	// MERGE 2026-09-15: the "upgrade: drift backfill writes ALLOW for grep on
	// every pre-existing agent" subtest is dropped. It asserted the per-agent
	// upgrade backfill (tool_policy_catalog_drift.go), which release/v0.1.1
	// retired under ADR-077: an agent with no entry rides the reconciled global
	// ceiling by design (CLAUDE.md constraint 6), and ADR-076's
	// ReconcileToolPolicyCeiling carries the new tool's shipped default there.

	t.Run("fresh install: TestBoot_ZeroToolPolicyGaps precondition holds for grep specifically", func(t *testing.T) {
		cfg := config.DefaultConfig()
		require.True(t, coreagent.SeedConfig(cfg))

		known := make(map[string]struct{})
		for _, n := range coreagent.AllStaticToolNames() {
			known[n] = struct{}{}
		}
		gaps := config.ValidateToolPolicyCoverage(cfg, known)
		for _, g := range gaps {
			assert.NotEqualf(t, "grep", g.ToolName,
				"grep must have ZERO coverage gaps after SeedConfig; found gap for agent %q", g.AgentID)
		}

		// Review finding F6 (docs/internal/false-green-patterns.md: "a for
		// loop continues past a condition with no assertion after the
		// loop"): the loop above only fires if ValidateToolPolicyCoverage
		// actually returns a "grep" gap. If it ever came back empty for the
		// WRONG reason — e.g. a future refactor drops "grep" from
		// coreagent.AllStaticToolNames(), so ValidateToolPolicyCoverage
		// treats it as an unknown tool and stops checking its coverage
		// entirely — this subtest would report green having verified
		// nothing about grep specifically, despite being titled a
		// precondition on exactly that. These two blocks re-derive the same
		// property directly from ground truth, independent of
		// ValidateToolPolicyCoverage's own machinery, so a regression in
		// EITHER path still fails this subtest.
		_, grepKnown := known["grep"]
		require.True(t, grepKnown,
			"\"grep\" must be a known static tool name, or ValidateToolPolicyCoverage silently stops "+
				"checking its coverage at all — the loop above would then report green unconditionally")
		require.NotEmpty(t, cfg.Agents.List, "SeedConfig must have seeded at least one agent")
		for i := range cfg.Agents.List {
			ac := cfg.Agents.List[i]
			require.NotNilf(t, ac.Tools, "agent %q must carry a tools config after SeedConfig", ac.ID)
			p, ok := ac.Tools.Builtin.Policies["grep"]
			require.Truef(t, ok, "agent %q has no explicit grep policy entry after SeedConfig", ac.ID)
			assert.Equalf(t, config.ToolPolicyAllow, p,
				"agent %q's seeded grep policy must be allow (FR-009: no posture left to silent "+
					"inheritance for any agent tier), got %q", ac.ID, p)
		}
	})

	t.Run("fresh custom agent creation: grep resolves allow, not a hardcoded deny (founder ruling 2026-09-07)", func(t *testing.T) {
		// coreagent.NewCustomAgentToolsCfg() is the SINGLE shared seed both
		// agent-creation paths call (POST /api/v1/agents' createAgent and the
		// LLM-driven system.agent.create tool) whenever the caller submits no
		// tools_cfg of its own — i.e. what a genuinely brand-new custom agent
		// gets with zero configuration. Before this founder ruling, "grep"
		// was absent from its overrides and therefore took
		// denyAllThenOverride's base "deny" like every other unopted-in
		// tool — a hardcoded per-agent deny that BEATS the global ceiling's
		// "allow" under strictest-wins, contradicting FR-009's "no posture
		// left to silent inheritance ... explicit allow for every agent
		// tier" for the one tier (fresh custom agents) that ruling's first
		// pass deliberately left out of scope.
		toolsCfg := coreagent.NewCustomAgentToolsCfg()
		require.NotNil(t, toolsCfg)
		p, ok := toolsCfg.Builtin.Policies["grep"]
		require.Truef(t, ok,
			"a freshly created custom agent must carry an EXPLICIT grep entry — "+
				"NewCustomAgentToolsCfg is built via denyAllThenOverride, which fully "+
				"enumerates every static builtin name (Constraint #6); there is no sparse "+
				"variant of this constructor for an entry to be silently absent from")
		assert.Equalf(t, config.ToolPolicyAllow, p,
			"a freshly created custom agent must seed grep=allow — founder ruling (2026-09-07): "+
				"\"there must not be any tool default to deny — the global policy sets the "+
				"default, not any hardcoded default\"")

		// Resolve through the REAL compositor merge against the real
		// fresh-install ceiling — the same strictest-wins global x agent
		// merge the agent loop and gateway approval hook both resolve
		// through (mirrors seed_upgrade_catalog_drift_test.go's
		// resolveUpgraded, applied to a raw not-yet-persisted AgentToolsCfg
		// rather than one already in cfg.Agents.List).
		cfg := config.DefaultConfig()
		global := make(map[string]config.ToolPolicy, len(cfg.Sandbox.ToolPolicies))
		for k, v := range cfg.Sandbox.ToolPolicies {
			global[k] = config.ToolPolicy(v)
		}
		resolved := tools.ResolveEffectivePolicy(&tools.ToolPolicyCfg{
			Policies:       toolsCfg.Builtin.Policies,
			GlobalPolicies: global,
		}, "grep")
		assert.Equal(t, "allow", resolved,
			"a freshly created custom agent's grep must RESOLVE allow through the real "+
				"compositor, matching every other agent tier")
	})
}
