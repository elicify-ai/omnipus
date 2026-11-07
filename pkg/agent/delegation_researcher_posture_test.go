package agent

// Researcher delegate posture pin — issue #1661, founder decision 2026-11-07
// 04:00Z ("Researcher B").
//
// Researcher KEEPS the tools documented in docs/tools.md: the Worker core,
// whose "Delegate and inspect work" row carries delegate Allow, applied to
// Researcher by "Planner, Researcher and General Purpose use the Worker core".
// On a fresh install the sparse seed (pkg/coreagent role_policies_adr090.go::
// adr090SparseRolePolicies) therefore stores NO delegate entry for Researcher
// — an entry is persisted only where intent differs from the ceiling — and
// strictest-wins resolution (pkg/tools/compositor.go::resolveEffectivePolicyWith)
// rides the global Allow. The onward-delegation guard is NOT the tool policy:
// it is the per-workspace delegation graph, where Researcher seeds exactly ONE
// edge, its own self-edge (pkg/coreagent/seed.go::coreAgentDelegation default
// arm; the operator exclusion list names only judge and plansupervisor). A
// delegate call to any OTHER existing agent has no authorizing edge and is
// refused at the gate (pkg/agent/loop_delegation.go::findDelegationEdge) with
// a trust_set denial — the outcome the re-anchored e2e test (a) narrates via
// the visible "Delegation refused" event line
// (src/lib/delegationEvents.ts::runEvents).
//
// This is a PIN, not a RED test: it asserts the posture the founder kept, and
// it passes on the code that exists today. Oracles come from the founder
// decision and docs/tools.md, never from observed gate output. The self-target
// negative control proves the refusal is edge-driven, not a blanket deny — a
// checker that refused every target would still pass the worker assertion.

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testWS, seedWorkspaceGraph, graphEdge and ctxWS are this package's shared
// delegation-graph test helpers (delegation_enforce_test.go /
// delegation_graph_testhelper_test.go — same test package).

func TestResearcherDelegate_FreshSeedRidesCeilingAllow_NonEdgeTargetRefusedByGraph(t *testing.T) {
	// ── Half 1: the fresh-seeded posture is delegate: allow, riding the ceiling.
	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg), "SeedConfig on a fresh config must seed the roster")

	// The raw ceiling map holds plain strings (map[string]string — values are
	// validated at the write boundary, not typed here); the TYPED comparison
	// happens on the resolved policy further down.
	require.Equal(t, string(config.ToolPolicyAllow), cfg.Sandbox.ToolPolicies["delegate"],
		"precondition: the shipped global ceiling for delegate is Allow (pkg/config/defaults.go) — the allow below rides THIS entry")

	var researcher config.AgentConfig
	found := false
	for _, ac := range cfg.Agents.List {
		if ac.ID == "researcher" {
			researcher, found = ac, true
			break
		}
	}
	require.True(t, found, "BLOCKED: fresh seed does not create the researcher roster entry (pkg/coreagent/seed.go)")
	require.NotNil(t, researcher.Tools, "seeded researcher must carry a tools config")
	assert.NotContains(t, researcher.Tools.Builtin.Policies, "delegate",
		"the sparse seed stores entries only where intent differs from the ceiling: with the Worker core kept "+
			"(founder 2026-11-07), researcher must NOT carry an explicit delegate entry — a persisted deny here would silently flip the posture back")
	// BuildFallbackPolicyCfg's second result is the agent TYPE (string), not
	// an error — discarded here as in default_tools_q22_test.go's own rows.
	pc, _ := tools.BuildFallbackPolicyCfg(cfg, "researcher")
	assert.Equal(t, string(config.ToolPolicyAllow), tools.ResolveEffectivePolicy(pc, "delegate"),
		"fresh-seeded researcher resolves delegate: allow (docs/tools.md Worker core; with no agent-side entry, the ceiling alone decides)")

	// ── Half 2: the fresh workspace graph seeds exactly one researcher edge —
	// the self-edge — so a delegate call to another EXISTING agent is refused.
	ceiling, err := cfg.Performance.EffectiveMaxDelegationDepth()
	require.NoError(t, err, "the default performance config must carry a valid delegation depth")
	rows := workspace.SelfEdgeSeedRows([]string{"researcher"}, nil, cfg.SelfEdgeExcludeAgentIDSet(), ceiling)
	require.Len(t, rows, 1,
		"the fresh researcher graph is exactly one row: the self-edge (seed.go::coreAgentDelegation default arm — no researcher→worker edge may ever be seeded)")
	require.Equal(t, "researcher", rows[0].FromAgent)
	require.Equal(t, "researcher", rows[0].ToAgent)
	require.NotNil(t, rows[0].Depth, "a freshly seeded self-edge carries an explicit depth (ADR-090 FR-006)")
	assert.Equal(t, coreagent.FreshSelfDelegationMaxDepth, *rows[0].Depth,
		"fresh self-edge depth is the ADR-090 FR-006 pin of 3, or the lower live ceiling")

	edges := make([]graphEdge, 0, len(rows))
	for _, r := range rows {
		modes := make([]string, 0, len(r.Modes))
		for _, m := range r.Modes {
			modes = append(modes, string(m))
		}
		edges = append(edges, graphEdge{FromAgent: r.FromAgent, ToAgent: r.ToAgent, Modes: modes, Depth: r.Depth})
	}
	seedWorkspaceGraph(t, testWS, true, edges)

	// The production delegate gate (registerSharedTools wires the background
	// variant; the non-edge refusal fires before mode is consulted, so the pin
	// holds for every mode).
	check := buildDelegationDenyCheckerForDelegate("researcher", config.PerformanceConfig{}, config.DelegationModeBackground)

	denial := check(ctxWS(testWS, 0), "worker")
	require.NotNil(t, denial,
		"researcher→worker has no edge in the fresh graph — the gate must refuse it (worker EXISTS, so this is the no-edge refusal, not the unknown-agent one)")
	assert.Equal(t, tools.DenyTrustSet, denial.Policy, "a no-edge refusal is a trust_set denial")
	assert.Equal(t, "worker", denial.TargetAgentID)
	assert.Equal(t, `delegation to agent "worker" is not permitted in this workspace`,
		denial.Reason, "the exact no-edge reason findDelegationEdge returns for an existing, un-edged target — this string is what the SPA's refused event line renders")

	// Negative control: the seeded self-edge stays legal at depth 0 < cap 3 —
	// the refusal above is edge-driven, not a blanket deny.
	if selfDenial := check(ctxWS(testWS, 0), "researcher"); selfDenial != nil {
		t.Fatalf("researcher→researcher must stay legal (the seeded self-edge, depth 0 < cap %d), got denial: %+v",
			coreagent.FreshSelfDelegationMaxDepth, selfDenial)
	}
}
