// Omnipus — ADR-052 (autonomous agent plan execution) tool-policy seeding
// tests (spec DS-6 / Test 2 / Test 19 / FR-005 / FR-006 / FR-027).
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

// planExecutionTools are the three new ADR-052 plan-execution tools whose
// seed matrix DS-6 pins: Jim allow, every other seeded agent + the global
// ceiling explicit ask (never absent, never deny).
var planExecutionTools = []string{"create_plan", "execute_plan", "run_task"}

// TestToolPolicy_ExecutePlanSeed verifies DS-6 (Test 2, FR-005/FR-006): a
// fresh SeedConfig grants Jim "allow" for execute_plan/create_plan/run_task
// while every other seeded agent gets explicit "ask" (never absent, never
// deny), the global ceiling (config.DefaultConfig()) also seeds explicit
// "ask" for the three tools and explicit "deny" for inspect_session, and a
// config missing an entry for these tools is backfilled to explicit "deny"
// (boots, WARN-logged) rather than aborting.
func TestToolPolicy_ExecutePlanSeed(t *testing.T) {
	t.Run("Jim seeded allow", func(t *testing.T) {
		cfg := &config.Config{}
		require.True(t, coreagent.SeedConfig(cfg))
		jim := findSeeded(t, cfg, string(coreagent.IDJim))
		require.NotNil(t, jim.Tools)
		for _, tool := range planExecutionTools {
			assert.Equalf(t, config.ToolPolicyAllow, jim.Tools.Builtin.Policies[tool],
				"Jim must be seeded allow for %q (R2-06)", tool)
		}
	})

	t.Run("every other seeded core/subagent-tier agent gets explicit ask, never absent, never deny", func(t *testing.T) {
		cfg := &config.Config{}
		require.True(t, coreagent.SeedConfig(cfg))
		others := []coreagent.CoreAgentID{
			coreagent.IDMia, coreagent.IDRay, coreagent.IDAva,
			coreagent.IDPlanner, coreagent.IDExplorer, coreagent.IDResearcher,
		}
		for _, id := range others {
			ac := findSeeded(t, cfg, string(id))
			require.NotNilf(t, ac.Tools, "agent %q must carry an explicit tools policy", id)
			for _, tool := range planExecutionTools {
				p, ok := ac.Tools.Builtin.Policies[tool]
				require.Truef(t, ok, "agent %q must have an explicit (never absent) entry for %q", id, tool)
				assert.Equalf(t, config.ToolPolicyAsk, p,
					"agent %q must resolve ask (never deny) for %q", id, tool)
			}
		}
	})

	t.Run("Worker deliberately absent from its own map, inherits ceiling ask", func(t *testing.T) {
		cfg := &config.Config{}
		require.True(t, coreagent.SeedConfig(cfg))
		worker := findSeeded(t, cfg, string(coreagent.IDWorker))
		require.NotNil(t, worker.Tools)
		for _, tool := range planExecutionTools {
			_, ok := worker.Tools.Builtin.Policies[tool]
			assert.Falsef(t, ok,
				"Worker's own map must NOT have an explicit entry for %q — it inherits the global ceiling (ask)", tool)
		}
	})

	t.Run("global ceiling seeds explicit ask for the three tools, explicit allow for inspect_session", func(t *testing.T) {
		cfg := config.DefaultConfig()
		for _, tool := range planExecutionTools {
			assert.Equalf(t, "ask", cfg.Sandbox.ToolPolicies[tool],
				"global ceiling must seed 'ask' for %q", tool)
		}
		// fix-wave finding #2 (architect F2 half 1): the ceiling seeds
		// "allow" for inspect_session — under strictest-wins
		// (pkg/tools/compositor.go), a ceiling "deny" would OVERRULE the
		// Judge's own seeded "allow" and resolve the Judge to deny (the
		// landed defect). Every seeded non-Judge agent carries an explicit
		// per-agent "deny" instead (asserted by
		// TestToolPolicy_InspectSession_JudgeOnly below), so the resolved
		// posture is unchanged for everyone but the Judge.
		assert.Equal(t, "allow", cfg.Sandbox.ToolPolicies["inspect_session"],
			"global ceiling must seed 'allow' for inspect_session (raises the ceiling only; per-agent deny does the real gating)")
	})

	t.Run("missing entry backfilled to explicit deny, boots (no abort)", func(t *testing.T) {
		cfg := config.DefaultConfig()
		require.True(t, coreagent.SeedConfig(cfg))

		// Simulate a config that predates ADR-052: strip the seeded entries
		// for the new tools from one agent (Mia) and the global ceiling, then
		// confirm coverage validation finds the gap and the repair backfills
		// it to explicit "deny" rather than aborting boot (GS-03).
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID != string(coreagent.IDMia) {
				continue
			}
			for _, tool := range planExecutionTools {
				delete(cfg.Agents.List[i].Tools.Builtin.Policies, tool)
			}
		}
		for _, tool := range planExecutionTools {
			delete(cfg.Sandbox.ToolPolicies, tool)
		}

		known := make(map[string]struct{})
		for _, n := range coreagent.AllStaticToolNames() {
			known[n] = struct{}{}
		}

		gapsBefore := config.ValidateToolPolicyCoverage(cfg, known)
		require.NotEmpty(t, gapsBefore, "stripping both sides must produce a coverage gap")

		repaired := config.RepairIncompleteToolPolicyCoverage(cfg, known)
		require.NotEmpty(t, repaired, "repair must backfill the induced gap")

		gapsAfter := config.ValidateToolPolicyCoverage(cfg, known)
		assert.Empty(t, gapsAfter, "coverage must be complete (boots) after repair — no abort")

		mia := findSeeded(t, cfg, string(coreagent.IDMia))
		for _, tool := range planExecutionTools {
			assert.Equalf(t, config.ToolPolicyDeny, mia.Tools.Builtin.Policies[tool],
				"backfilled gap for %q must resolve to explicit deny (fail-closed), never an implicit allow", tool)
		}
	})
}

// TestToolPolicy_InspectSession_JudgeOnly verifies DS-6/FR-027, updated by
// fix-wave finding #2 (architect F2 half 1): inspect_session resolves allow
// ONLY for the Judge. Since the global ceiling now seeds "allow" for
// inspect_session (defaults.go — necessary so the Judge's own "allow"
// resolves under strictest-wins), EVERY other seeded agent (core + subagent
// tier + worker) MUST carry an EXPLICIT per-agent "deny" — an absent entry
// would now silently inherit the ceiling's "allow", which is exactly the
// class of gap this test locks shut.
func TestToolPolicy_InspectSession_JudgeOnly(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))

	for _, a := range cfg.Agents.List {
		// Only the Judge is exempt — it is the one agent inspect_session is
		// FOR, and it is asserted separately by TestSeed_JudgeSystemAgent.
		// This used to skip every Type=system agent, which silently widened
		// into a hole the moment the System-Agents roster grew past the Judge
		// (ADR-055's PlanSupervisor); PlanSupervisor must be held to the same
		// explicit-deny rule as everyone else.
		if a.ID == string(coreagent.IDJudge) {
			continue
		}
		require.NotNilf(t, a.Tools, "agent %q must carry an explicit tools policy", a.ID)
		p, ok := a.Tools.Builtin.Policies["inspect_session"]
		require.Truef(t, ok,
			"non-Judge agent %q must carry an EXPLICIT inspect_session entry (never absent) — "+
				"an absent entry would silently inherit the ceiling's allow", a.ID)
		assert.Equalf(t, config.ToolPolicyDeny, p,
			"non-Judge agent %q must not be granted inspect_session", a.ID)
	}
}

// TestSeedConfig_FreshInstall_ADR052ToolsFullyCovered is a targeted
// regression guard (mirrors TestMemoryTools_AllInCoverageUniverseAndSeed's
// rationale in pkg/gateway): the four ADR-052 tool names must be present in
// BOTH AllStaticToolNames() (the seed universe) and produce zero coverage
// gaps across the full fresh-install seed, so an omission on either side
// can't silently deny-by-default every install (FR-006/FR-027).
func TestSeedConfig_FreshInstall_ADR052ToolsFullyCovered(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	known := make(map[string]struct{})
	for _, n := range coreagent.AllStaticToolNames() {
		known[n] = struct{}{}
	}
	for _, tool := range append(append([]string{}, planExecutionTools...), "inspect_session") {
		_, inCatalog := known[tool]
		assert.Truef(t, inCatalog, "%q must be in coreagent.AllStaticToolNames()", tool)
	}

	gaps := config.ValidateToolPolicyCoverage(cfg, known)
	assert.Empty(t, gaps, "fresh install must have zero coverage gaps across the ADR-052 tools")
}
