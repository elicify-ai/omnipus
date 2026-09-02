// Omnipus — ToolSearch structural-floor seeding tests.
//
// CLAUDE.md hard constraint 6 (no default-policy fallback) used to carry one
// exception: pkg/tools/compositor.go force-allowed ToolSearch unconditionally
// for every agent, regardless of any seeded policy data — an operator who saw
// "ToolSearch": "deny" in their own config had no way to know that value was
// silently ignored at runtime. That bypass has been removed. ToolSearch is
// now seeded "allow" as real, explicit literal data for every agent (every
// core/system agent case in coreAgentSeed/systemAgentSeed, and
// NewCustomAgentToolsCfg for freshly created agents) — no exceptions.
//
// This file pins that outcome across the WHOLE seeded roster, resolved
// through the REAL production compositor (tools.ResolveEffectivePolicy), not
// by reading the raw seed maps — the same discipline
// tool_policy_effective_resolution_test.go and list_jobs_seed_test.go use,
// after this branch's reviews found several controls whose tests were green
// while the control itself was broken because the test read the seed map
// instead of the merge.
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

// TestToolSearch_ResolvedAllowAcrossSeededRoster is the load-bearing
// assertion: every agent produced by a fresh SeedConfig — core (Mia/Jim/Ava/
// Ray), the delegation-only tier (Worker/Planner/Explorer/Researcher), and
// the System Agents (Judge/PlanSupervisor) — resolves "allow" for ToolSearch
// through the real compositor merge. This is a complete sweep of
// cfg.Agents.List, not a hardcoded id list, so a new agent added to
// coreAgentSeed/systemAgentSeed is covered the day it lands.
func TestToolSearch_ResolvedAllowAcrossSeededRoster(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))
	require.NotEmpty(t, cfg.Agents.List, "precondition: the roster must be seeded")

	checked := 0
	for _, a := range cfg.Agents.List {
		if a.Tools == nil {
			continue
		}
		checked++
		assert.Equalf(t, "allow", resolveFor(t, cfg, a.ID, "ToolSearch", nil),
			"(%s, ToolSearch) must RESOLVE allow through the real compositor merge — "+
				"every agent needs it to reach any tiered (lazy/search-only) tool at all "+
				"(CLAUDE.md constraint 6: seeded data, not a code-level force-allow)", a.ID)
	}
	require.GreaterOrEqual(t, checked, 9,
		"sanity: the sweep must have covered the full seeded roster (4 core + worker + "+
			"3 subagent-tier + judge + plansupervisor = 9), not silently skipped everything")
}

// TestToolSearch_ResolvedAllow_FreshCustomAgent verifies the tenth seed
// location named by the operator directive: NewCustomAgentToolsCfg, the seed
// used for every newly created custom/subagent/subagent_3p agent (both the
// REST POST /api/v1/agents handler and the LLM-driven system.agent.create
// tool). A fresh custom agent must resolve ToolSearch "allow" from day one,
// exactly like the built-in roster.
func TestToolSearch_ResolvedAllow_FreshCustomAgent(t *testing.T) {
	custom := coreagent.NewCustomAgentToolsCfg()
	require.NotNil(t, custom, "NewCustomAgentToolsCfg must return a non-nil cfg")

	polCfg := &tools.ToolPolicyCfg{
		Policies: custom.Builtin.Policies,
	}
	assert.Equal(t, "allow", tools.ResolveEffectivePolicy(polCfg, "ToolSearch"),
		"a freshly created custom agent must resolve ToolSearch allow from its own seeded policy")
}

// TestToolSearch_Worker_InheritsGlobalCeiling_NoRedundantEntry pins the one
// deliberate exception named in the operator directive: the Worker's sparse
// tightenGlobalCeiling map does NOT name ToolSearch explicitly — it inherits
// the tool from the global ceiling (pkg/config/defaults.go seeds
// "ToolSearch": "allow" there), matching the Worker's whole design principle
// ("sparse map, inherit the ceiling for everything not deliberately
// tightened"). This asserts BOTH halves: the seed literal has no ToolSearch
// key, and the RESOLVED policy is still allow — so an operator lowering the
// global ceiling controls the Worker's ToolSearch access, not a redundant
// per-agent entry that would silently stop tracking the ceiling.
func TestToolSearch_Worker_InheritsGlobalCeiling_NoRedundantEntry(t *testing.T) {
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg))

	worker := findSeeded(t, cfg, string(coreagent.IDWorker))
	require.NotNil(t, worker.Tools, "Worker must carry an explicit tools policy")
	_, hasExplicitEntry := worker.Tools.Builtin.Policies["ToolSearch"]
	assert.False(t, hasExplicitEntry,
		"Worker's sparse map must NOT name ToolSearch explicitly — it is meant to inherit "+
			"the global ceiling like every other untightened tool")

	assert.Equal(t, "allow", resolveFor(t, cfg, string(coreagent.IDWorker), "ToolSearch", nil),
		"(Worker, ToolSearch) must still RESOLVE allow, purely via ceiling inheritance")
}
