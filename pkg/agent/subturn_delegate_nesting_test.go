// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// subturn_delegate_nesting_test.go — live UAT regression, 2026-07-12.
//
// THE BUG: a delegated sub-turn agent could never itself call `delegate`
// again, no matter how the per-workspace trust graph, mode, and depth caps
// were configured. Reported repro: jim (await) delegates to ray (await),
// instructing ray to delegate onward to planner (await) via an explicitly
// wired, unrestricted ray -> planner edge. The jim -> ray hop worked; ray's
// own nested `delegate(agent_id="planner", ...)` call — even after a
// successful `load_tool(names=["delegate"])` — failed immediately with:
//
//	{"error":"permission_denied","message":"Tool execution denied by policy.","tool":"delegate"}
//
// NOT the delegation_denied shape DelegateTool.Execute's own trust-graph gate
// returns. That is the tell: the call never reached DelegateTool.Execute at
// all. Reproduced identically for async=true and async=false, and for both a
// brand-new edge and a long-pre-existing fully-unrestricted edge — ruling out
// a trust-graph/mode/depth problem. Chatting directly with ray (not nested)
// and delegating to planner worked fine — ruling out anything about ray's own
// policy or the edge itself.
//
// ROOT CAUSE: pkg/agent/subturn.go's spawnSubTurn built every delegated
// child's tool registry via:
//
//	agent.Tools = execSource.Tools.CloneExcept(tools.ExcludedDelegate, tools.ExcludedHandoff)
//
// — a registry-level filter that made "delegate" ENTIRELY ABSENT from the map
// backing the child's own ts.agent.Tools (FR-H-006, "one level only", owner
// decision 2026-04-20). That predates the per-edge depth-cap + trust-graph
// delegation system that exists today (workspace.DelegationEdge.Depth,
// config.SubTurn.MaxDepth, resolveEffectiveDelegationDepth /
// enforceEdgeModeAndDepth) and is the ACTUAL intended gate for multi-hop
// chains — the registry-level block pre-empted it entirely, unconditionally,
// regardless of how permissive the wired edges were.
//
// The load_tool "success" the UAT report observed was a red herring: the
// unified `load_tool` infra tool (pkg/tools/tools_tool.go) is constructed
// once, at the TOP-LEVEL persistent agent's registration time
// (tools.NewToolsTool(agent.Tools, ...) in pkg/agent/loop.go), and its
// canLoad/markLoaded closures re-resolve "the calling agent" via
// al.registry.GetAgent(callerID) — the PERSISTENT top-level AgentInstance —
// not the ephemeral per-turn child AgentInstance CloneExcept constructed
// (which is never stored in al.registry, only in al.activeTurnStates). So
// load_tool promoted/reported success against the TOP-LEVEL agent's own
// (unfiltered) registry, which already had "delegate", while the child's OWN
// ts.agent.Tools — the object loop.go's per-iteration filterTimePolicyMap
// (line ~5633) and the FR-079 TOCTOU re-check (resolveToolPolicyAtExec, line
// ~9204) actually consult — never gained the tool, because CloneExcept had
// already permanently omitted it from that map. A tool absent from
// filterTimePolicyMap is treated as deny "regardless of live policy" by
// resolveToolPolicyAtExec's documented contract — so the very next call to
// "delegate" failed closed, BEFORE DelegateTool.Execute (and therefore its
// real trust-set/mode/depth gate) was ever reached.
//
// THE FIX: spawnSubTurn now calls CloneExcept(tools.ExcludedHandoff) only.
// "delegate" is retained in every delegated child's own tool registry from
// the moment it is cloned (delegate is registered via Register(), so it is
// IsCore=true and always present in GetAll() — no TTL/promotion needed), so
// filterTimePolicyMap and resolveToolPolicyAtExec see it immediately, and a
// nested delegate call reaches DelegateTool.Execute's real trust-set/mode/
// depth gate (delegationDenyBackground/Await, SetDelegationDepthResolver) —
// exactly the checks that MUST still apply and now finally get the chance to.
// "hand_off" remains excluded (a distinct, still-valid concern: a nested
// sub-turn hijacking the ACTIVE parent session's agent).
//
// THIS TEST proves the fix end-to-end through the REAL production path: a
// real spawnSubTurn call (jim -> ray) whose child (ray) runs a REAL runTurn
// loop (scripted via a fake LLM provider) that itself calls load_tool then
// delegate, targeting a THIRD agent (planner) it is authorized to reach via
// an explicit workspace delegation edge. Success is proven by observing BOTH
// hops' real EventKindSubTurnSpawn events (ray's from the jim->ray hop this
// test drives directly, and planner's from ray's OWN nested delegate call) —
// and by asserting no ToolExecSkipped/"permission_denied" event was ever
// emitted for "delegate", which is exactly the signature the live bug left
// behind. Covers both async=false (await, blocking) and async=true
// (background) — the bug was mode-independent, but the two dispatch paths
// (DelegateTool.executeSync vs executeAsync) differ enough in control flow
// that both are exercised explicitly per the bug report's own repro matrix.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// testEventTimeout and testEventPoll are used for require.Eventually polling.
// Defined HERE (not in sprint_h_subturn_test.go, which is //go:build !cgo) so
// they are visible under BOTH build configurations — sprint_h_subturn_test.go
// is excluded under CGO_ENABLED=1 (i.e. -race), and this file has no build
// constraint, so without this definition the references below were undefined
// under -race (pre-existing break unblocking the corr-MAJOR-3 -race gate).
const (
	testEventTimeout = 2 * time.Second
	testEventPoll    = 10 * time.Millisecond
)

// newNestedDelegationAgentLoop builds an AgentLoop with two native agents,
// "ray" and "planner", both policy-allowed to call "delegate", plus a
// workspace whose delegation graph carries a single, fully-unrestricted
// ray -> planner edge (mirroring the UAT repro's "wired ray -> planner
// (unrestricted)" setup). "jim" is not a registered config agent — the test
// drives the jim -> ray hop via a direct spawnSubTurn call (matching the
// established pattern in approval_grant_delegation_test.go), so jim's own
// identity/policy is never exercised; only ray's nested call is under test.
func newNestedDelegationAgentLoop(t *testing.T) *AgentLoop {
	t.Helper()

	// Seed a default workspace whose delegation graph authorizes ray -> planner
	// for every mode (nil Modes) with no per-edge depth override (nil Depth,
	// so the global SubTurn.MaxDepth / defaultMaxSubTurnDepth backstop governs).
	seedWorkspaceGraph(t, "ws-nested-delegation", true, []graphEdge{
		edge("ray", "planner", nil, nil),
	})

	tmpDir := t.TempDir()
	allowDelegate := &config.AgentToolsCfg{
		Builtin: config.AgentBuiltinToolsCfg{
			Policies: map[string]config.ToolPolicy{"delegate": config.ToolPolicyAllow},
		},
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Provider:          "mock",
				Home:              tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{
					ID:    "ray",
					Type:  config.AgentTypeWorker,
					Home:  tmpDir,
					Model: &config.AgentModelConfig{Primary: "test-model"},
					Tools: allowDelegate,
				},
				{
					ID:    "planner",
					Type:  config.AgentTypeWorker,
					Home:  tmpDir,
					Model: &config.AgentModelConfig{Primary: "test-model"},
					Tools: allowDelegate,
				},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})

	// Sanity: ray and planner must both be registered before the caller
	// installs their scripted providers via setAgentProvider.
	_, ok := al.GetRegistry().GetAgent("ray")
	require.True(t, ok, "ray must be registered")
	_, ok = al.GetRegistry().GetAgent("planner")
	require.True(t, ok, "planner must be registered")

	return al
}

// setAgentProvider installs provider as agentID's live LLM provider, guarded
// by the agent's own mutex (the same field ApplyAgentModel mutates in
// production, under the same lock discipline).
func setAgentProvider(t *testing.T, al *AgentLoop, agentID string, provider providers.LLMProvider) {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent(agentID)
	require.True(t, ok, "agent %q must be registered", agentID)
	inst.mu.Lock()
	inst.Provider = provider
	inst.mu.Unlock()
}

// findSubTurnSpawnAgentIDs returns the AgentID carried by every
// EventKindSubTurnSpawn event collected so far.
func findSubTurnSpawnAgentIDs(c *eventCollector) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var ids []string
	for _, e := range c.events {
		if e.Kind != EventKindSubTurnSpawn {
			continue
		}
		if p, ok := e.Payload.(SubTurnSpawnPayload); ok {
			ids = append(ids, p.AgentID)
		}
	}
	return ids
}

// assertNoDelegateDeniedByPolicy fails the test if any ToolExecSkipped event
// was emitted for the "delegate" tool — the exact signature the live bug left
// behind (a mid_turn_policy_change / permission_denied TOCTOU deny, emitted
// BEFORE DelegateTool.Execute's own trust-graph gate is ever reached).
func assertNoDelegateDeniedByPolicy(t *testing.T, c *eventCollector) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.events {
		if e.Kind != EventKindToolExecSkipped {
			continue
		}
		p, ok := e.Payload.(ToolExecSkippedPayload)
		if !ok {
			continue
		}
		assert.NotEqual(t, "delegate", p.Tool,
			"delegate must never be denied by the FR-079 TOCTOU policy gate inside a "+
				"delegated sub-turn — reason: %q (this is the exact live-bug signature)", p.Reason)
	}
}

// buildParentTurnState hand-constructs a depth-0 turnState representing the
// outermost delegator ("jim" in the bug report), mirroring the established
// pattern in approval_grant_delegation_test.go. jim is never itself run
// through al.runTurn — only the REAL spawnSubTurn(TargetAgentID="ray") call
// that follows exercises production code.
func buildParentTurnState(al *AgentLoop) *turnState {
	return &turnState{
		ctx:                 context.Background(),
		turnID:              "jim-parent-turn",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		session:             &ephemeralSessionStore{},
		agent:               al.GetRegistry().GetDefaultAgent(),
		transcriptSessionID: "S-nested-delegation",
		agentID:             "jim",
	}
}

// TestNestedDelegate_Await verifies the FIX for async=false (await/blocking):
// ray, itself running as a delegated sub-turn (spawned by a real spawnSubTurn
// call from "jim"), calls load_tool then delegate(agent_id="planner",
// async=false) and the call reaches and passes the real trust-graph gate —
// spawning planner as a genuine third-level sub-turn — instead of failing
// closed with permission_denied before DelegateTool.Execute is ever reached.
func TestNestedDelegate_Await(t *testing.T) {
	al := newNestedDelegationAgentLoop(t)

	// ray: load_tool(names=["delegate"]) -> delegate(agent_id="planner", async=false) -> final text.
	raySeq := newScriptedProvider(
		&providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{
				ID:        "ray-call-1",
				Name:      "load_tool",
				Arguments: map[string]any{"names": []string{"delegate"}},
			}},
		},
		&providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{
				ID:   "ray-call-2",
				Name: "delegate",
				Arguments: map[string]any{
					"task":     "hop 2: await-delegate to planner",
					"agent_id": "planner",
					"async":    false,
				},
			}},
		},
		&providers.LLMResponse{Content: "ray: done delegating to planner"},
	)
	setAgentProvider(t, al, "ray", raySeq)

	// planner: a single final-text response, no tool calls — its turn just
	// needs to complete so ray's blocking await call can return.
	plannerSeq := newScriptedProvider(
		&providers.LLMResponse{Content: "planner: task received and done"},
	)
	setAgentProvider(t, al, "planner", plannerSeq)

	collector, cleanup := newEventCollector(t, al)
	defer cleanup()

	parent := buildParentTurnState(al)
	ctx := withSpawnToolCallID(context.Background(), "jim-to-ray-await-call")
	_, err := spawnSubTurn(ctx, al, parent, SubTurnConfig{
		Model:         "test-model",
		SystemPrompt:  "hop 1: await-delegate to ray, then ray await-delegates to planner",
		TargetAgentID: "ray",
		Async:         false,
		Timeout:       5 * time.Second,
	})
	require.NoError(t, err, "hop 1 (jim -> ray) spawnSubTurn must succeed")

	// THE PROOF: both hops' SubTurnSpawn events must be present — "ray" (this
	// test's own direct hop) AND "planner" (ray's OWN nested delegate call,
	// which is only reachable if the FR-079 TOCTOU gate did NOT fail closed).
	require.Eventually(t, func() bool {
		ids := findSubTurnSpawnAgentIDs(collector)
		return len(ids) >= 2
	}, testEventTimeout, testEventPoll,
		"expected two SubTurnSpawn events (ray, then planner); "+
			"only one means ray's nested delegate call never reached spawnSubTurn for planner")

	ids := findSubTurnSpawnAgentIDs(collector)
	assert.Contains(t, ids, "ray", "must have spawned ray (hop 1)")
	assert.Contains(t, ids, "planner", "must have spawned planner (hop 2, ray's own nested delegate call)")

	assertNoDelegateDeniedByPolicy(t, collector)

	assert.Equal(t, 0, raySeq.Remaining(), "ray's full scripted sequence must have been consumed")
	assert.Equal(t, 0, plannerSeq.Remaining(), "planner's full scripted sequence must have been consumed")
}

// TestNestedDelegate_Background verifies the FIX for async=true (background,
// non-blocking): identical scenario to TestNestedDelegate_Await, but ray's
// nested delegate call uses async=true. DelegateTool.executeAsync launches
// the grandchild spawn in a goroutine and returns a task_id immediately
// rather than blocking, so this exercises a materially different control-flow
// path inside DelegateTool than the await test — the bug report explicitly
// reproduced with both modes, so both are pinned here.
func TestNestedDelegate_Background(t *testing.T) {
	al := newNestedDelegationAgentLoop(t)

	// ray: load_tool -> delegate(agent_id="planner", async=true) -> final text.
	// The delegate call returns a task_id immediately (non-blocking), so ray's
	// final response follows right away while planner's spawn runs in the
	// background on its OWN independent scripted provider.
	raySeq := newScriptedProvider(
		&providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{
				ID:        "ray-call-1",
				Name:      "load_tool",
				Arguments: map[string]any{"names": []string{"delegate"}},
			}},
		},
		&providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{
				ID:   "ray-call-2",
				Name: "delegate",
				Arguments: map[string]any{
					"task":     "hop 2: background-delegate to planner",
					"agent_id": "planner",
					"async":    true,
				},
			}},
		},
		&providers.LLMResponse{Content: "ray: kicked off background delegation to planner"},
	)
	setAgentProvider(t, al, "ray", raySeq)

	plannerSeq := newScriptedProvider(
		&providers.LLMResponse{Content: "planner: background task received and done"},
	)
	setAgentProvider(t, al, "planner", plannerSeq)

	collector, cleanup := newEventCollector(t, al)
	defer cleanup()

	parent := buildParentTurnState(al)
	ctx := withSpawnToolCallID(context.Background(), "jim-to-ray-background-call")
	_, err := spawnSubTurn(ctx, al, parent, SubTurnConfig{
		Model:         "test-model",
		SystemPrompt:  "hop 1: await-delegate to ray, then ray background-delegates to planner",
		TargetAgentID: "ray",
		Async:         false,
		Timeout:       5 * time.Second,
	})
	require.NoError(t, err, "hop 1 (jim -> ray) spawnSubTurn must succeed")

	// planner's spawn happens in a background goroutine from inside ray's
	// turn, so poll for it rather than assuming it landed by the time hop 1's
	// own spawnSubTurn call returned.
	require.Eventually(t, func() bool {
		ids := findSubTurnSpawnAgentIDs(collector)
		return len(ids) >= 2
	}, testEventTimeout, testEventPoll,
		"expected two SubTurnSpawn events (ray, then planner); "+
			"only one means ray's nested async delegate call never reached spawnSubTurn for planner")

	ids := findSubTurnSpawnAgentIDs(collector)
	assert.Contains(t, ids, "ray", "must have spawned ray (hop 1)")
	assert.Contains(t, ids, "planner", "must have spawned planner (hop 2, ray's own nested async delegate call)")

	assertNoDelegateDeniedByPolicy(t, collector)

	// planner's turn runs in a detached background goroutine (Critical: true,
	// per DelegateTool.executeAsync's doc comment) — give it a moment to
	// consume its single scripted response before asserting exhaustion.
	require.Eventually(t, func() bool {
		return plannerSeq.Remaining() == 0
	}, testEventTimeout, testEventPoll, "planner's scripted response must be consumed")
	assert.Equal(t, 0, raySeq.Remaining(), "ray's full scripted sequence must have been consumed")
}
