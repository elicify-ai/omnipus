// Omnipus — sub-agent worker tier tests.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
)

// findSeeded locates a seeded agent by id, or fails the test.
func findSeeded(t *testing.T, cfg *config.Config, id string) config.AgentConfig {
	t.Helper()
	for _, a := range cfg.Agents.List {
		if a.ID == id {
			return a
		}
	}
	require.FailNowf(t, "agent not seeded", "expected seeded agent %q in config", id)
	return config.AgentConfig{}
}

// TestSeedWorker verifies the seeded general-purpose worker: present, Type=worker,
// carries a native executor, locked, NOT default, and NOT a chat target.
//
// BDD: Given an empty config, When SeedConfig is called,
//
//	Then a worker agent exists with Type=worker, a native executor, Locked=true,
//	Default=false, and IsChatTarget()==false.
func TestSeedWorker(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg), "SeedConfig on empty config must modify")

	worker := findSeeded(t, cfg, string(coreagent.IDWorker))

	assert.Equal(t, config.AgentTypeWorker, worker.Type, "worker must have Type=worker")
	assert.True(t, worker.IsWorker(), "IsWorker() must be true for the worker")
	assert.False(t, worker.IsChatTarget(), "a worker must NOT be a chat target")
	assert.True(t, worker.Locked, "worker must be Locked like the core agents")
	assert.False(t, worker.Default, "worker must never be the default agent")

	require.NotNil(t, worker.Subagents, "worker must carry a Subagents config holding the executor")
	require.NotNil(t, worker.Subagents.Executor, "worker must carry an executor (Spec-4 field)")
	assert.Equal(t, config.ExecutorKindNative, worker.Subagents.Executor.EffectiveKind(),
		"the seeded general-purpose worker runs native")

	require.NotNil(t, worker.Enabled)
	assert.True(t, *worker.Enabled, "worker must be enabled by default")
}

// TestWorkerNotCoreAgent verifies the worker is classified as its own tier and is
// NOT treated as a core agent by IsCoreAgent (so type inference never mislabels it).
func TestWorkerNotCoreAgent(t *testing.T) {
	assert.False(t, coreagent.IsCoreAgent(string(coreagent.IDWorker)),
		"the worker is NOT a core agent — it is a distinct delegation-only tier")
	assert.True(t, coreagent.IsWorkerID(coreagent.IDWorker), "IsWorkerID must identify the worker")

	for _, base := range coreagent.BaseAgents() {
		assert.True(t, coreagent.IsCoreAgent(string(base.ID)),
			"base agent %q must be a core agent", base.ID)
		assert.False(t, coreagent.IsWorkerID(base.ID),
			"base agent %q must not be a worker", base.ID)
	}
}

// TestSeedBaseDelegationPolicies verifies the seeded trust graph so orchestration +
// worker fan-out work out of the box:
//
//	Jim → [ava, ray, worker]   modes: [task, background, await]
//	Mia, Ray, Ava → [worker]   modes: [task, background]
//
// And the worker itself has no onward delegation (leaf, deny-by-default).
func TestSeedBaseDelegationPolicies(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))

	hasTarget := func(p *config.DelegationPolicy, id string) bool {
		if p == nil {
			return false
		}
		for _, r := range p.To {
			if r.ID == id {
				return true
			}
		}
		return false
	}
	hasMode := func(p *config.DelegationPolicy, m config.DelegationMode) bool {
		if p == nil {
			return false
		}
		for _, x := range p.Modes {
			if x == m {
				return true
			}
		}
		return false
	}

	jim := findSeeded(t, cfg, string(coreagent.IDJim))
	require.NotNil(t, jim.DelegationPolicy, "Jim must have a seeded delegation policy")
	assert.True(t, hasTarget(jim.DelegationPolicy, string(coreagent.IDAva)), "Jim → Ava")
	assert.True(t, hasTarget(jim.DelegationPolicy, string(coreagent.IDRay)), "Jim → Ray")
	assert.True(t, hasTarget(jim.DelegationPolicy, string(coreagent.IDWorker)), "Jim → worker")
	assert.True(t, hasMode(jim.DelegationPolicy, config.DelegationModeTask), "Jim allows task mode")
	assert.True(t, hasMode(jim.DelegationPolicy, config.DelegationModeBackground), "Jim allows background mode")
	assert.True(t, hasMode(jim.DelegationPolicy, config.DelegationModeAwait), "Jim allows await mode")

	for _, id := range []coreagent.CoreAgentID{coreagent.IDMia, coreagent.IDRay, coreagent.IDAva} {
		a := findSeeded(t, cfg, string(id))
		require.NotNil(t, a.DelegationPolicy, "%s must have a seeded delegation policy", id)
		assert.True(t, hasTarget(a.DelegationPolicy, string(coreagent.IDWorker)),
			"%s must be able to delegate to the worker", id)
		assert.True(t, hasMode(a.DelegationPolicy, config.DelegationModeTask), "%s allows task mode", id)
		assert.True(t, hasMode(a.DelegationPolicy, config.DelegationModeBackground), "%s allows background mode", id)
	}

	worker := findSeeded(t, cfg, string(coreagent.IDWorker))
	assert.Nil(t, worker.DelegationPolicy,
		"the worker is a leaf — it has no seeded onward delegation (deny-by-default)")
}

// TestSeedConfig_DoesNotReseedExplicitEmptyDelegation locks the architect's
// load-bearing safety property for the delegation editor: an operator who has
// explicitly set an agent's delegation policy to "deny all" (a non-nil but EMPTY
// To list) must NOT have the seed trust graph re-applied on the next boot. The
// re-seed migration only fires when DelegationPolicy is nil; a non-nil empty
// policy is a deliberate operator choice and is preserved.
//
// Without this invariant, a tightening edit (revoke all delegation) would be
// silently undone on every restart — the exact regression class the delegation
// editor's reload fix guards against at runtime.
func TestSeedConfig_DoesNotReseedExplicitEmptyDelegation(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{
					ID:   string(coreagent.IDJim),
					Name: "Jim — Orchestrator",
					Type: config.AgentTypeCore,
					// Operator chose "deny all delegation": non-nil, empty To.
					DelegationPolicy: &config.DelegationPolicy{To: []config.AgentRef{}},
				},
			},
		},
	}

	coreagent.SeedConfig(cfg)

	jim := findSeeded(t, cfg, string(coreagent.IDJim))
	require.NotNil(t, jim.DelegationPolicy, "Jim's explicit policy must survive seeding")
	assert.NotNil(t, jim.DelegationPolicy.To,
		"explicit empty To must remain non-nil (deny-all is authoritative, not 'unset')")
	assert.Empty(t, jim.DelegationPolicy.To,
		"SeedConfig must NOT re-seed the trust graph over an operator's explicit deny-all policy")
}

// TestWorkerHasNoPersistentMemoryTools verifies the worker gets ephemeral memory
// only: the persistent-memory tools are denied so it never relies on a private
// memory room (scope: no persistent room required for workers).
func TestWorkerHasNoPersistentMemoryTools(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))
	worker := findSeeded(t, cfg, string(coreagent.IDWorker))

	require.NotNil(t, worker.Tools)
	pol := worker.Tools.Builtin.Policies
	for _, tool := range []string{"remember", "recall_memory", "retrospective"} {
		assert.Equal(t, config.ToolPolicyDeny, pol[tool],
			"worker must deny persistent-memory tool %q (ephemeral memory only)", tool)
	}
	// The system.* deny rail is preserved.
	assert.Equal(t, config.ToolPolicyDeny, pol["system.*"],
		"worker keeps the system.* deny rail")
}
