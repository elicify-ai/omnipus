// Omnipus — ADR-081 D11 (unified-search-and-grep-spec.md FR-009/MV-8): the
// grep agent tool's ADR-077 two-layer governance. Spec test 27
// (TestGrep_PolicyAllTiersAndDriftBackfill) — every seeded agent tier
// carries an EXPLICIT "grep": allow (the founder ruling: no posture left to
// silent inheritance anywhere in the roster), and the upgrade-path drift
// backfill writes ALLOW for grep on every pre-existing agent, core-seeded
// AND operator-created alike — the recorded exception to the backfill's
// generic deny-by-default baseline (MV-8).
//
// Modelled on seed_upgrade_catalog_drift_test.go's ADR-068 upgrade-simulation
// pattern (writePreADR068Home / loadUpgraded / resolveUpgraded /
// findUpgradedAgent), narrowed to the one new tool name this suite is about.
// findUpgradedAgent and resolveUpgraded are fully generic (no
// knowledge-specific logic) and are reused directly from that file — same
// package, same test binary. writePreGrepHome / loadUpgradedNoGrep /
// customAgentFromOlderReleaseNoGrep are this file's own, because the
// existing ADR-068 helpers hardcode the six knowledge-tool names in their
// preconditions and would fail (not vacuously pass) against a store that
// still carries those six but is missing only "grep".
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// grepRoster is the full set of agents whose seed MUST carry an explicit
// "grep" posture: the 4 base agents + the Worker + the 3 seeded specialists
// (coreagent.All()) plus the 2 System Agents (coreagent.SystemAgents()) —
// i.e. every agent SeedConfig produces. Read from the real exported
// enumerations, never hand-copied, so a future roster addition is picked up
// automatically rather than silently skipped by this suite.
func grepRoster() []string {
	var ids []string
	for _, ca := range coreagent.All() {
		ids = append(ids, string(ca.ID))
	}
	for _, ca := range coreagent.SystemAgents() {
		ids = append(ids, string(ca.ID))
	}
	return ids
}

// writePreGrepHome materialises an $OMNIPUS_HOME in the shape a release that
// predates ADR-081's grep tool would have left on disk: every seeded agent's
// persisted policy map, and the global ceiling, both missing "grep" entirely
// — mirrors seed_upgrade_catalog_drift_test.go's writePreADR068Home, one name
// instead of six.
func writePreGrepHome(t *testing.T, extra ...config.AgentConfig) string {
	t.Helper()

	home := t.TempDir()

	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg), "fresh seed must populate the roster")

	store := agentstore.New(home)
	for i := range cfg.Agents.List {
		ac := cfg.Agents.List[i]
		if ac.Tools != nil {
			delete(ac.Tools.Builtin.Policies, "grep")
		}
		require.NoError(t, store.Create(ac.ID, &ac))
	}
	for i := range extra {
		ac := extra[i]
		require.NoError(t, store.Create(ac.ID, &ac))
	}

	delete(cfg.Sandbox.ToolPolicies, "grep")
	cfg.Agents.List = nil

	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), data, 0o600))

	return home
}

// loadUpgradedNoGrep performs the same boot-simulation steps as
// seed_upgrade_catalog_drift_test.go's loadUpgraded, with preconditions
// scoped to "grep" alone: the current binary's DefaultConfig() must
// reintroduce the global ceiling at "allow" (encoding/json merges the old
// file's own sandbox.tool_policies INTO that non-nil map rather than
// replacing it, so the current binary's default for a name absent from the
// old file survives the merge — the same mechanism
// TestUpgrade_KnowledgeTools_* already exercises for six other names).
//
// loadUpgradeConfig performs only the mechanical load (no precondition
// assertions) so a caller that deliberately seeded an operator override for
// "grep" on one agent (simulating a decision made on the old release) is not
// tripped up by a blanket "no agent may carry a grep entry" precondition
// that was never true for its own fixture. loadUpgradedNoGrep wraps it with
// that precondition for every OTHER case in this file, where it is a
// meaningful fixture-sanity check.
func loadUpgradeConfig(t *testing.T, home string) *config.Config {
	t.Helper()

	cfg, err := config.LoadConfig(filepath.Join(home, "config.json"))
	require.NoError(t, err)

	agents, skipped, err := agentstore.New(home).List()
	require.NoError(t, err)
	require.Empty(t, skipped, "no agent record written by the setup may fail to parse")
	require.NotEmpty(t, agents, "the upgrade roster must be non-empty")
	cfg.Agents.List = agents
	return cfg
}

// loadUpgradedNoGrep is loadUpgradeConfig plus the precondition that no
// agent record read from the store carries a per-agent "grep" entry, and
// that the current binary's default global ceiling for grep survives the
// merge over the old file at "allow" (encoding/json merges the old file's
// own sandbox.tool_policies INTO DefaultConfig()'s non-nil map rather than
// replacing it — the same mechanism TestUpgrade_KnowledgeTools_* already
// exercises for six other names).
func loadUpgradedNoGrep(t *testing.T, home string) *config.Config {
	t.Helper()

	cfg := loadUpgradeConfig(t, home)

	require.Equalf(t, "allow", cfg.Sandbox.ToolPolicies["grep"],
		"precondition: the current binary's default global ceiling for grep must survive "+
			"the merge over the old file at \"allow\" — without it this case no longer "+
			"reproduces the finding this suite guards")
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		if ac.Tools == nil {
			continue
		}
		_, present := ac.Tools.Builtin.Policies["grep"]
		require.Falsef(t, present,
			"precondition: agent %q must arrive from the old store with NO entry for grep",
			ac.ID)
	}
	return cfg
}

// customAgentFromOlderReleaseNoGrep returns an operator-created agent as a
// release predating grep would have persisted it:
// coreagent.NewCustomAgentToolsCfg()'s fully-enumerated deny-by-default map,
// minus the "grep" entry that release's catalog did not contain — mirrors
// seed_upgrade_catalog_drift_test.go's customAgentFromOlderRelease.
func customAgentFromOlderReleaseNoGrep(id string) config.AgentConfig {
	toolsCfg := coreagent.NewCustomAgentToolsCfg()
	delete(toolsCfg.Builtin.Policies, "grep")
	return config.AgentConfig{
		ID:    id,
		Name:  "Notes Bot",
		Type:  config.AgentTypeCustom,
		Tools: toolsCfg,
	}
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

	t.Run("upgrade: drift backfill writes ALLOW for grep on every pre-existing agent, core and custom alike", func(t *testing.T) {
		const customID = "notes-bot"
		home := writePreGrepHome(t, customAgentFromOlderReleaseNoGrep(customID))
		cfg := loadUpgradedNoGrep(t, home)

		coreagent.SeedConfig(cfg)

		for _, id := range grepRoster() {
			assert.Equalf(t, "allow", resolveUpgraded(t, cfg, id, "grep"),
				"(%s, grep) after an upgrade boot must resolve allow — the seed's own posture, "+
					"not the deny an unrelated tool would backfill to", id)
		}
		// The operator-created custom agent is the recorded exception this
		// spec test exists to pin: the generic drift-backfill deny baseline
		// does NOT apply to grep (MV-8), unlike every other unnamed tool.
		assert.Equalf(t, "allow", resolveUpgraded(t, cfg, customID, "grep"),
			"a custom agent predating grep's release must backfill to allow, not the generic "+
				"deny-by-default baseline every other tool gets")

		// Every agent must also carry an EXPLICIT persisted entry after the
		// boot — not merely resolve correctly via an absent key silently
		// inheriting the ceiling (Constraint #6's other half).
		for i := range cfg.Agents.List {
			ac := &cfg.Agents.List[i]
			require.NotNilf(t, ac.Tools, "agent %q must carry a tools config after the upgrade boot", ac.ID)
			_, present := ac.Tools.Builtin.Policies["grep"]
			assert.Truef(t, present,
				"agent %q has no explicit grep entry after the upgrade boot — it would resolve "+
					"from the global ceiling, the silent runtime default Constraint #6 bans",
				ac.ID)
		}
	})

	t.Run("upgrade: an operator's own grep decision is never overwritten by the backfill", func(t *testing.T) {
		home := writePreGrepHome(t)
		store := agentstore.New(home)
		_, err := store.Update(string(coreagent.IDMia), func(ac *config.AgentConfig) error {
			ac.Tools.Builtin.Policies["grep"] = config.ToolPolicyDeny
			return nil
		})
		require.NoError(t, err)
		// loadUpgradeConfig, not loadUpgradedNoGrep: this fixture
		// deliberately gives Mia a "grep" entry (the operator's own prior
		// decision), so the blanket "no agent may carry one" precondition
		// does not apply to this case.
		cfg := loadUpgradeConfig(t, home)

		coreagent.SeedConfig(cfg)

		mia := findUpgradedAgent(t, cfg, string(coreagent.IDMia))
		assert.Equal(t, config.ToolPolicyDeny, mia.Tools.Builtin.Policies["grep"],
			"the operator's own grep=deny must survive the upgrade backfill unchanged — the "+
				"backfill fills gaps only, it never overwrites an existing entry")
		assert.Equal(t, "deny", resolveUpgraded(t, cfg, string(coreagent.IDMia), "grep"),
			"and must still resolve deny through the real compositor")
	})

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
	})
}
