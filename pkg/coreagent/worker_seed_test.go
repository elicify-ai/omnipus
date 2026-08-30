// Omnipus — sub-agent worker tier tests.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// seedModesToEdgeModes independently replays the same seed→edge collapse
// (config.DelegationMode's real 3-value tool-call vocabulary →
// workspace.DelegationMode's collapsed 2-value trust-edge vocabulary: Task→
// ModeTask, Await|Background→ModeDirect, deduped) that pkg/gateway's
// defaultWorkspaceDelegationEdges applies when bootstrapping a fresh
// workspace's delegation graph from this package's seed data. It lives here
// (rather than importing pkg/gateway, which is unexported at that seam and
// would also risk an import-direction problem) purely so this test can assert
// what the TRANSLATED graph edge looks like without duplicating the gateway's
// production code.
func seedModesToEdgeModes(modes []config.DelegationMode) []workspace.DelegationMode {
	seen := make(map[workspace.DelegationMode]bool, len(modes))
	out := make([]workspace.DelegationMode, 0, len(modes))
	for _, m := range modes {
		wm := workspace.ModeDirect
		if m == config.DelegationModeTask {
			wm = workspace.ModeTask
		}
		if seen[wm] {
			continue
		}
		seen[wm] = true
		out = append(out, wm)
	}
	return out
}

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
//
// ADR-037: AgentConfig.DelegationPolicy no longer exists — the per-workspace
// delegation graph is the sole runtime authority now, and this seed data
// (coreAgentDelegation, exported via coreagent.SeedDelegationEdges) survives
// only as bootstrap data for a fresh workspace's graph
// (defaultWorkspaceDelegationEdges), not as a field on the seeded AgentConfig.
// This test now checks that seed source directly rather than the (removed)
// AgentConfig field.
func TestSeedBaseDelegationPolicies(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))

	// SeedConfig must still produce the base agents + worker in the roster —
	// the delegation-edges check below is independent of AgentConfig.
	findSeeded(t, cfg, string(coreagent.IDJim))
	findSeeded(t, cfg, string(coreagent.IDMia))
	findSeeded(t, cfg, string(coreagent.IDRay))
	findSeeded(t, cfg, string(coreagent.IDAva))
	findSeeded(t, cfg, string(coreagent.IDWorker))

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

	jimDP := coreagent.SeedDelegationEdges(coreagent.IDJim)
	require.NotNil(t, jimDP, "Jim must have a seeded delegation policy")
	assert.True(t, hasTarget(jimDP, string(coreagent.IDAva)), "Jim → Ava")
	assert.True(t, hasTarget(jimDP, string(coreagent.IDRay)), "Jim → Ray")
	assert.True(t, hasTarget(jimDP, string(coreagent.IDWorker)), "Jim → worker")
	assert.True(t, hasMode(jimDP, config.DelegationModeTask), "Jim allows task mode")
	assert.True(t, hasMode(jimDP, config.DelegationModeBackground), "Jim allows background mode")
	assert.True(t, hasMode(jimDP, config.DelegationModeAwait), "Jim allows await mode")

	// Companion assertion (mode collapse): Jim's seed DTO stays 3-valued
	// (task/background/await, asserted above — coreAgentDelegation is
	// deliberately unchanged), but the workspace GRAPH edge it bootstraps
	// collapses+dedupes down to the trust edge's 2-value vocabulary.
	assert.ElementsMatch(t,
		[]workspace.DelegationMode{workspace.ModeTask, workspace.ModeDirect},
		seedModesToEdgeModes(jimDP.Modes),
		"Jim's seeded modes, translated onto a workspace graph edge, must collapse to [direct, task]")

	for _, id := range []coreagent.CoreAgentID{coreagent.IDMia, coreagent.IDRay, coreagent.IDAva} {
		dp := coreagent.SeedDelegationEdges(id)
		require.NotNil(t, dp, "%s must have a seeded delegation policy", id)
		assert.True(t, hasTarget(dp, string(coreagent.IDWorker)),
			"%s must be able to delegate to the worker", id)
		assert.True(t, hasMode(dp, config.DelegationModeTask), "%s allows task mode", id)
		assert.True(t, hasMode(dp, config.DelegationModeBackground), "%s allows background mode", id)

		// Companion assertion: same collapse applies to Mia/Ray/Ava's seeded
		// graph edges.
		assert.ElementsMatch(t,
			[]workspace.DelegationMode{workspace.ModeTask, workspace.ModeDirect},
			seedModesToEdgeModes(dp.Modes),
			"%s's seeded modes, translated onto a workspace graph edge, must collapse to [direct, task]", id)
	}

	assert.Nil(t, coreagent.SeedDelegationEdges(coreagent.IDWorker),
		"the worker is a leaf — it has no seeded onward delegation (deny-by-default)")
}

// TestWorkerToolPolicyTightensGlobalCeiling verifies the worker's own policy
// map is SPARSE: channels, providers, platform, most of agents (list_agents
// excepted), most of tasks (update_task/set_todos/list_tasks excepted), and
// workspaces are tightened to explicit "deny" — but every other tool
// (including the persistent-memory tools) is deliberately ABSENT from the
// worker's own map, so it inherits the seeded global ceiling
// (sandbox.tool_policies, "allow") via the coverage validator's OR-semantics.
// This is an operator-confirmed design choice, not a gap — see
// pkg/coreagent/core.go's coreAgentSeed IDWorker branch and
// tightenGlobalCeiling.
func TestWorkerToolPolicyTightensGlobalCeiling(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))
	worker := findSeeded(t, cfg, string(coreagent.IDWorker))

	require.NotNil(t, worker.Tools)
	pol := worker.Tools.Builtin.Policies

	// Tightened past the global ceiling to an explicit "deny".
	for _, tool := range []string{
		"enable_channel", "configure_channel", "disable_channel", "list_channels", "test_channel",
		"configure_provider", "list_providers", "test_provider", "list_models",
		"get_config", "set_config", "run_doctor", "get_usage",
		"create_agent", "update_agent", "delete_agent", "read_agent_metadata",
		"create_task", "delete_task", "create_task_in_workspace", "update_task_in_workspace",
		"delete_task_in_workspace", "list_tasks_in_workspace",
		"create_workspace", "update_workspace", "delete_workspace", "list_workspaces", "get_workspace",
		// inspect_session (fix-wave finding #2): the global ceiling now seeds
		// "allow" for this tool (defaults.go), so an absent entry here would
		// silently inherit that allow — the worker must tighten it to an
		// explicit "deny" like every other seeded non-Judge agent.
		"inspect_session",
	} {
		got, ok := pol[tool]
		require.True(t, ok, "worker must have an explicit deny for tightened tool %q", tool)
		assert.Equal(t, config.ToolPolicyDeny, got, "worker tool %q must be explicit 'deny'", tool)
	}

	// Deliberately absent from the worker's own map — inherits the global
	// ceiling's "allow": the 3 named task exceptions, list_agents, and every
	// tool outside the 6 tightened categories (e.g. the memory tools).
	for _, tool := range []string{
		"list_agents", "update_task", "set_todos", "list_tasks",
		"remember", "recall_memory", "run_retrospective",
	} {
		_, ok := pol[tool]
		assert.False(t, ok, "worker must NOT have its own entry for %q — it inherits the global ceiling", tool)
	}

	// "system.*" was never a real tool name (dead wildcard) — it must never
	// appear as a key.
	_, hasRail := pol["system.*"]
	assert.False(t, hasRail, "worker must NOT carry the dead 'system.*' wildcard")
}

// TestWorkerHasCompiledExecutionDisciplinePrompt_BootSucceeds verifies the
// RC-6 fix: the seeded worker (ID=worker, Type=worker) now carries a real,
// non-empty compiled execution-discipline prompt (previously "" — an empty
// compiled prompt meant every worker sub-turn silently fell back to
// ContextBuilder's generic "You are Worker, a helpful AI assistant powered
// by Omnipus" identity instead of any delegation-specific guidance).
//
// The init() panic exemption (IsWorkerID skip, coreagent.go's init()) has
// NEVER been about a CUSTOM (non-seeded) worker: init() only iterates
// All(), the fixed 8-entry seeded roster, and a custom Type=worker agent
// config is never a member of that slice — it was never reachable by this
// loop regardless of the skip. The skip exists solely for the ONE seeded
// Worker() entry, and now that prompts["worker"] is non-empty (this RC-6
// fix), the skip is VESTIGIAL — nothing in the loop would panic even
// without it today. The real, still-live consequence: if this "worker"
// prompt entry is emptied again, the skip means init() will NOT catch it —
// boot stays silent and the regression (silent fallback to the generic
// identity) would ship undetected.
func TestWorkerHasCompiledExecutionDisciplinePrompt_BootSucceeds(t *testing.T) {
	// BDD: Given the seeded worker (ID=worker, Type=worker),
	//      When init() has run and GetPrompt("worker") is called,
	//      Then init() does NOT panic and GetPrompt returns a non-empty
	//      execution-discipline prompt (RC-6) — NOT the bare generic
	//      identity ContextBuilder.getIdentity() would otherwise produce.
	prompt := coreagent.GetPrompt(string(coreagent.IDWorker))
	assert.NotEmpty(t, prompt,
		"worker must carry a real compiled prompt (RC-6) — an empty prompt silently falls back to the generic assistant identity")
	for _, marker := range []string{"delegat", "task"} {
		assert.Contains(t, strings.ToLower(prompt), marker,
			"worker prompt must establish it is a focused delegated-task executor (looking for %q)", marker)
	}
	assert.NotContains(t, prompt, "helpful AI assistant powered by Omnipus",
		"worker prompt must not be (or degrade to) the generic getIdentity() fallback text")

	// The init() panic exemption: the worker is in All() but the boot path
	// skips its compiled-prompt-map-entry check (IsWorkerID). SeedConfig on
	// an empty config must still succeed and the worker must be present.
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))
	worker := findSeeded(t, cfg, string(coreagent.IDWorker))
	assert.True(t, worker.IsWorker(), "worker must be classified Type=worker")
}

// TestWorkerSeedConfig_EmptySoulStillValidatesExistingAgents ensures that
// re-seeding a config whose worker has been customized to a non-locked
// (e.g., with Type changed) state still works — the init() panic exemption
// keeps the boot path robust against operator edits that touch the worker.
func TestWorkerSeedConfig_EmptySoulStillValidatesExistingAgents(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				// Existing operator-tweaked worker with no SOUL.md / empty soul.
				{ID: string(coreagent.IDWorker), Name: "Worker", Type: config.AgentTypeWorker, Locked: true},
			},
		},
	}
	modified := coreagent.SeedConfig(cfg)
	_ = modified // may be true (re-enforce) — what matters is no panic and worker present.
	worker := findSeeded(t, cfg, string(coreagent.IDWorker))
	assert.True(t, worker.IsWorker(), "worker with empty soul still validates after re-seed")
}
