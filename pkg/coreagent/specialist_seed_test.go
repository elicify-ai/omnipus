// Omnipus — specialist subagent tier tests (M5/M6).
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

// TestSeedSpecialists verifies Planner / Explorer / Researcher seed as the
// subagent tier: Type=worker, native executor, locked, never default, never a
// chat target, and present with their identity.
func TestSeedSpecialists(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg), "SeedConfig on empty config must modify")

	for _, id := range []coreagent.CoreAgentID{coreagent.IDPlanner, coreagent.IDExplorer, coreagent.IDResearcher} {
		t.Run(string(id), func(t *testing.T) {
			ac := findSeeded(t, cfg, string(id))
			assert.Equal(t, config.AgentTypeWorker, ac.Type, "specialist must have Type=worker")
			assert.True(t, ac.IsWorker(), "IsWorker() must be true for a specialist")
			assert.False(t, ac.IsChatTarget(), "a specialist must NOT be a chat target")
			assert.True(t, ac.Locked, "specialist must be Locked")
			assert.False(t, ac.Default, "specialist must never be the default agent")
			require.NotNil(t, ac.Subagents)
			require.NotNil(t, ac.Subagents.Executor)
			assert.Equal(t, config.ExecutorKindNative, ac.Subagents.Executor.EffectiveKind(),
				"specialists run native")
			assert.True(t, coreagent.IsSpecialistID(id))
			assert.True(t, coreagent.IsSubagentTierID(id))
			assert.False(t, coreagent.IsCoreAgent(string(id)),
				"a specialist must NOT be classified as a core agent")
			assert.Equal(t, coreagent.ToWireType(ac), coreagent.ToWireType(ac))
		})
	}
}

// TestPlannerBoundedDelegation verifies the Planner's SEED delegation policy
// (coreagent.SeedDelegationEdges — the only surviving representation post
// ADR-037, since AgentConfig.DelegationPolicy no longer exists) is bounded
// onward to Explorer + Researcher (the bounded subagent-delegation unlock,
// M5), while Explorer and Researcher remain leaves. This is bootstrap-seed
// data consumed by defaultWorkspaceDelegationEdges at workspace-creation
// time, not a field on the persisted AgentConfig.
func TestPlannerBoundedDelegation(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))

	// Confirm the specialists still seed (identity/type), then check the
	// SEPARATE seed-delegation-edges source directly — it is no longer a
	// field on the seeded AgentConfig itself.
	findSeeded(t, cfg, string(coreagent.IDPlanner))

	plannerDP := coreagent.SeedDelegationEdges(coreagent.IDPlanner)
	require.NotNil(t, plannerDP, "Planner must seed a delegation policy (onward delegation)")
	require.NotNil(t, plannerDP.Depth, "Planner delegation must be depth-bounded")
	assert.Equal(t, 2, *plannerDP.Depth)

	targets := make(map[string]bool)
	for _, ref := range plannerDP.To {
		assert.Equal(t, config.AgentRefKindLocal, ref.Kind)
		targets[ref.ID] = true
	}
	assert.True(t, targets["explorer"], "Planner must delegate to Explorer")
	assert.True(t, targets["researcher"], "Planner must delegate to Researcher")

	// Explorer and Researcher are leaves: no onward delegation seeded.
	assert.Nil(t, coreagent.SeedDelegationEdges(coreagent.IDExplorer), "Explorer must be a delegation leaf")
	assert.Nil(t, coreagent.SeedDelegationEdges(coreagent.IDResearcher), "Researcher must be a delegation leaf")
}

// TestSpecialistsKeepMemoryTools verifies specialists (unlike the generic worker)
// retain the persistent-memory tools — Explorer reads/writes memory, Planner
// records decompositions.
func TestSpecialistsKeepMemoryTools(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))

	for _, id := range []coreagent.CoreAgentID{coreagent.IDPlanner, coreagent.IDExplorer, coreagent.IDResearcher} {
		ac := findSeeded(t, cfg, string(id))
		require.NotNil(t, ac.Tools)
		pol := ac.Tools.Builtin.Policies
		assert.NotEqual(t, config.ToolPolicyDeny, pol["recall_memory"],
			"specialist %s must keep recall_memory (not denied like the worker)", id)
		assert.NotEqual(t, config.ToolPolicyDeny, pol["remember"],
			"specialist %s must keep remember", id)
	}
}
