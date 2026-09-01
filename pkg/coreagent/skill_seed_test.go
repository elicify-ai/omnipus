// Omnipus — Skill structural-floor seeding tests (ADR-072 D1).
//
// The Skill tool (pkg/tools/skill.go, ADR-072 D1) is this codebase's second
// instance of the "index in context, content on demand" pattern ADR-071
// established for ToolSearch — see tool_search_seed_test.go's own header for
// the CLAUDE.md hard constraint 6 background this mirrors. Skill went through
// the identical wiring gap ToolSearch went through: the tool shipped
// (pkg/tools/skill.go) with its own header explicitly deferring registration
// into GeneralBuiltinMetadata() and per-agent seeding to "a later integration
// phase" — this file pins the seeding half of that phase's outcome across the
// whole seeded roster, resolved through the REAL production compositor
// (tools.ResolveEffectivePolicy), not by reading the raw seed maps — the same
// discipline tool_search_seed_test.go and tool_policy_effective_resolution_test.go
// use, after this branch's reviews found several controls whose tests were
// green while the control itself was broken because the test read the seed
// map instead of the merge.
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

// TestSkill_ResolvedAllowAcrossSeededRoster is the load-bearing assertion:
// every agent produced by a fresh SeedConfig — core (Mia/Jim/Ava/Ray), the
// delegation-only tier (Worker/Planner/Explorer/Researcher), and the System
// Agents (Judge/PlanSupervisor) — resolves "allow" for Skill through the real
// compositor merge. This is a complete sweep of cfg.Agents.List, not a
// hardcoded id list, so a new agent added to coreAgentSeed/systemAgentSeed is
// covered the day it lands.
func TestSkill_ResolvedAllowAcrossSeededRoster(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))
	require.NotEmpty(t, cfg.Agents.List, "precondition: the roster must be seeded")

	checked := 0
	for _, a := range cfg.Agents.List {
		if a.Tools == nil {
			continue
		}
		checked++
		assert.Equalf(t, "allow", resolveFor(t, cfg, a.ID, "Skill", nil),
			"(%s, Skill) must RESOLVE allow through the real compositor merge — "+
				"every agent needs it to load any skill's content at all "+
				"(ADR-072 D1, mirroring CLAUDE.md constraint 6's ToolSearch structural floor)", a.ID)
	}
	require.GreaterOrEqual(t, checked, 9,
		"sanity: the sweep must have covered the full seeded roster (4 core + worker + "+
			"3 subagent-tier + judge + plansupervisor = 9), not silently skipped everything")
}

// TestSkill_ResolvedAllow_FreshCustomAgent verifies a newly created custom
// agent — with NO explicit Skill policy in its own request payload — still
// resolves Skill "allow" purely from NewCustomAgentToolsCfg's seed, the
// shared structural-floor entry used by BOTH agent-creation paths (the REST
// POST /api/v1/agents handler and the LLM-driven system.agent.create tool).
// Mirrors TestToolSearch_ResolvedAllow_FreshCustomAgent's identical shape for
// ToolSearch.
func TestSkill_ResolvedAllow_FreshCustomAgent(t *testing.T) {
	custom := coreagent.NewCustomAgentToolsCfg()
	require.NotNil(t, custom, "NewCustomAgentToolsCfg must return a non-nil cfg")

	polCfg := &tools.ToolPolicyCfg{
		Policies: custom.Builtin.Policies,
	}
	assert.Equal(t, "allow", tools.ResolveEffectivePolicy(polCfg, "Skill"),
		"a freshly created custom agent must resolve Skill allow from its own seeded policy")
}

// TestSkill_Worker_InheritsGlobalCeiling_NoRedundantEntry pins the same
// deliberate exception ToolSearch carries: the Worker's sparse
// tightenGlobalCeiling map does NOT name Skill explicitly — it inherits the
// tool from the global ceiling (pkg/config/defaults.go seeds "Skill":
// "allow" there), matching the Worker's whole design principle ("sparse map,
// inherit the ceiling for everything not deliberately tightened"). This
// asserts BOTH halves: the seed literal has no Skill key, and the RESOLVED
// policy is still allow — so an operator lowering the global ceiling
// controls the Worker's Skill access, not a redundant per-agent entry that
// would silently stop tracking the ceiling.
func TestSkill_Worker_InheritsGlobalCeiling_NoRedundantEntry(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	worker := findSeeded(t, cfg, string(coreagent.IDWorker))
	require.NotNil(t, worker.Tools, "Worker must carry an explicit tools policy")
	_, hasExplicitEntry := worker.Tools.Builtin.Policies["Skill"]
	assert.False(t, hasExplicitEntry,
		"Worker's sparse map must NOT name Skill explicitly — it is meant to inherit "+
			"the global ceiling like every other untightened tool")

	assert.Equal(t, "allow", resolveFor(t, cfg, string(coreagent.IDWorker), "Skill", nil),
		"(Worker, Skill) must still RESOLVE allow, purely via ceiling inheritance")
}
