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
// successful `ToolSearch(names=["delegate"])` — failed immediately with:
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
//	agent.Tools = execSource.Tools.CloneExcept(tools.ExcludedDelegate, tools.ExcludedSwitchAgent)
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
// The ToolSearch "success" the UAT report observed was a red herring: the
// unified `ToolSearch` infra tool (pkg/tools/tools_tool.go) is constructed
// once, at the TOP-LEVEL persistent agent's registration time
// (tools.NewToolsTool(agent.Tools, ...) in pkg/agent/loop.go), and its
// canLoad/markLoaded closures re-resolve "the calling agent" via
// al.registry.GetAgent(callerID) — the PERSISTENT top-level AgentInstance —
// not the ephemeral per-turn child AgentInstance CloneExcept constructed
// (which is never stored in al.registry, only in al.activeTurnStates). So
// ToolSearch promoted/reported success against the TOP-LEVEL agent's own
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
// THE FIX: spawnSubTurn now calls CloneExcept(tools.ExcludedSwitchAgent) only.
// "delegate" is retained in every delegated child's own tool registry from
// the moment it is cloned (delegate is registered via Register(), so it is
// IsCore=true and always present in GetAll() — no TTL/promotion needed), so
// filterTimePolicyMap and resolveToolPolicyAtExec see it immediately, and a
// nested delegate call reaches DelegateTool.Execute's real trust-set/mode/
// depth gate (delegationDenyBackground/Await, SetDelegationDepthResolver) —
// exactly the checks that MUST still apply and now finally get the chance to.
// "switch_agent" (ADR-071 D4 renamed hand_off + return_to_default to this
// one tool) remains excluded (a distinct, still-valid concern: a nested
// sub-turn hijacking the ACTIVE parent session's agent).
//
// THIS TEST proves the fix end-to-end through the REAL production path: a
// real spawnSubTurn call (jim -> ray) whose child (ray) runs a REAL runTurn
// loop (scripted via a fake LLM provider) that itself calls ToolSearch then
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
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// testEventTimeout and testEventPoll are used for require.Eventually polling.
// Defined HERE rather than in sprint_h_subturn_test.go for historical reasons:
// that file used to carry //go:build !cgo, so it was excluded under
// CGO_ENABLED=1 (i.e. -race) and left these references undefined. That tag has
// since been removed package-wide; the placement is kept to avoid churn.
//
// testEventTimeout was 2s, which is tight for what it actually waits on:
// TestNestedDelegate_Background's two require.Eventually calls poll for a
// SECOND, detached background goroutine (ray's nested async delegate to
// planner, and planner's own turn draining its scripted provider) to reach
// observable state via the event collector / scripted-provider counter —
// there is no cheaper synchronization primitive available since that work
// is intentionally fire-and-forget from the test's point of view. Under
// -race's ~10x slowdown plus CI package-parallelism contention, 2s was not
// always enough scheduler time for that second goroutine to run at all,
// which matches the "failed once, passed on isolated re-run" signature CI
// reported — the poll target was already correct (real state, not a
// sleep), only the ceiling was too tight. 10s keeps the same
// fail-fast intent (still far below the 5s/30s per-turn timeouts these
// tests configure) while giving the background goroutine real headroom.
const (
	testEventTimeout = 10 * time.Second
	testEventPoll    = 10 * time.Millisecond
)

// newNestedDelegationAgentLoop builds an AgentLoop with two native agents,
// "ray" and "planner", both policy-allowed to call "delegate", plus a
// workspace whose delegation graph carries a single, fully-unrestricted
// ray -> planner edge (mirroring the UAT repro's "wired ray -> planner
// (unrestricted)" setup), plus "jim" as the outermost delegator.
//
// jim used to be left UNREGISTERED here, on the reasoning that the test drives
// the jim -> ray hop via a direct spawnSubTurn call so jim's own identity is
// never exercised. That worked only because the parent turnState's agent
// instance came from GetDefaultAgent's last-resort rung, which would return
// ANY registered agent — including a worker. That rung is gone (ADR-064 §7:
// resolving a worker or System Agent as the chat default could route real user
// messages at an agent that must never receive them), and with ray and planner
// both being workers there was nothing left to resolve, so spawnSubTurn failed
// with "parent turnState has no agent instance".
//
// A parent needs a real instance. Registering jim says that plainly instead of
// depending on a fallback that should never have been load-bearing.
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
					ID:    "jim",
					Home:  tmpDir,
					Model: &config.AgentModelConfig{Primary: "test-model"},
					Tools: allowDelegate,
				},
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

	// ADR-057 U14 fixture repair: NewAgentLoop registers every agent's own
	// DelegateTool fail-closed against a nil lifecycle store (real boot only
	// wires it later via the gateway's SetSessionMessagingStores call, per
	// session_messaging_wire.go). ray's nested delegate call to planner goes
	// through THIS loop's own internally-registered "delegate" tool (not a
	// test-constructed one), so it needs the same re-wiring mustNewAgentLoop's
	// callers get in production — mirrors
	// cancel_orchestration_adr057_test.go's al.SetSessionMessagingStores(nil, lifecycleStore) pattern.
	al.SetSessionMessagingStores(nil, session.NewLifecycleStore(t.TempDir()))

	// Sanity: ray and planner must both be registered before the caller
	// installs their scripted providers via setAgentProvider.
	_, ok := al.GetRegistry().GetAgent("ray")
	require.True(t, ok, "ray must be registered")
	_, ok = al.GetRegistry().GetAgent("planner")
	require.True(t, ok, "planner must be registered")

	// Pre-existing flake fix (found while adding ADR-058 coverage to this
	// file, not caused by it — reproduced against Wave-1 HEAD with only the
	// two ORIGINAL tests in this file, ~2/5 runs): every test built on this
	// helper drives a REAL nested delegate spawn, and the async=true path
	// (TestNestedDelegate_Background) explicitly runs "planner's spawn ...
	// in a background goroutine ... from inside ray's turn" per that test's
	// own doc comment. Nothing previously bounded that goroutine's lifetime
	// against this function's t.TempDir() calls (tmpDir above, and the
	// LifecycleStore's own TempDir just above this comment) — Go's testing
	// package removes a t.TempDir() the moment the test function returns,
	// racing a still-running background turn that keeps writing into the
	// agents' Home dirs (observed failure: "TempDir RemoveAll cleanup:
	// unlinkat ...: directory not empty"). This is registered LAST, on
	// purpose: t.Cleanup runs LIFO, so al.Close() — which this codebase
	// already uses for exactly this purpose (see its own "so nothing writes
	// after Close() returns to race temp-dir cleanup" doc comment, loop.go)
	// — drains before either TempDir's removal fires, for every test built
	// on this helper (Await, Background, and the ADR-058 trust-graph test
	// added alongside it).
	t.Cleanup(al.Close)

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
//
// ADR-057 FR-005/FR-096 fixture repair: spawnSubTurn's
// sharedStore.CreateSessionWithID(childID, parentTS.transcriptSessionID, ...)
// now requires the parent id to resolve to a REAL session in
// al.GetSessionStore() — a hand-picked literal like "S-nested-delegation"
// that was never created there fails loudly instead of silently. Mint a real
// session via store.NewSession and use its id, and set routingSessionID
// alongside transcriptSessionID (FR-011/FR-015: the role-B predicates and
// pendingSpawnKeys now key on routingSessionID, not transcriptSessionID).
func buildParentTurnState(t *testing.T, al *AgentLoop) *turnState {
	t.Helper()
	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")
	meta, err := store.NewSession(session.SessionTypeChat, "test-channel", "jim")
	require.NoError(t, err)
	return &turnState{
		ctx:                 context.Background(),
		turnID:              "jim-parent-turn",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		session:             &ephemeralSessionStore{},
		agent:               al.GetRegistry().GetDefaultAgent(),
		transcriptSessionID: meta.ID,
		routingSessionID:    session.RoutingSessionID(meta.ID),
		transcriptStore:     store,
		agentID:             "jim",
	}
}

// TestNestedDelegate_Await verifies the FIX for async=false (await/blocking):
// ray, itself running as a delegated sub-turn (spawned by a real spawnSubTurn
// call from "jim"), calls ToolSearch then delegate(agent_id="planner",
// async=false) and the call reaches and passes the real trust-graph gate —
// spawning planner as a genuine third-level sub-turn — instead of failing
// closed with permission_denied before DelegateTool.Execute is ever reached.
func TestNestedDelegate_Await(t *testing.T) {
	al := newNestedDelegationAgentLoop(t)

	// ray: ToolSearch(names=["delegate"]) -> delegate(agent_id="planner", async=false) -> final text.
	raySeq := newScriptedProvider(
		&providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{
				ID:        "ray-call-1",
				Name:      "ToolSearch",
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

	parent := buildParentTurnState(t, al)
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

	// ray: ToolSearch -> delegate(agent_id="planner", async=true) -> final text.
	// The delegate call returns a task_id immediately (non-blocking), so ray's
	// final response follows right away while planner's spawn runs in the
	// background on its OWN independent scripted provider.
	raySeq := newScriptedProvider(
		&providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{
				ID:        "ray-call-1",
				Name:      "ToolSearch",
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

	parent := buildParentTurnState(t, al)
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

// TestNestedDelegate_TrustGraphDenialDoesNotPoisonSubsequentCalls is an
// ADR-058 (tool-denial semantics) regression, added in the same wave that
// wired a new per-turn quarantine ledger into runTurn's dispatch loop
// (FR-058-10/11): a tool that produces one PERMANENT approval-policy denial
// is short-circuited from a cache for the rest of the turn, with no further
// hook call, policy re-resolution, or approval round-trip.
//
// ADR-058 §1 names pkg/tools/result.go::DelegationDeniedResult explicitly
// OUT OF SCOPE for that classification/quarantine machinery: a delegation
// denial from DelegateTool.Execute's own trust-set gate is a REAL ToolResult
// returned by a REAL Execute call, not one of loop.go's three synthetic
// permission_denied emit sites — it never reaches ClassifyDenial,
// denialPayloadJSON, or turnState.recordToolDenial at all. If a future change
// ever blurred that boundary (e.g. routing every error-shaped ToolResult
// through the same ledger "for consistency"), the FIRST time "delegate" hit
// an unauthorized target it would get quarantined for the rest of the turn —
// reintroducing, via a brand-new mechanism, exactly the failure class this
// file's own file-level doc comment describes as THE BUG: a legitimate,
// authorized nested delegate call failing closed before DelegateTool.Execute
// is ever reached.
//
// Scenario: ray (delegate-allowed, with a trust edge to "planner" but NONE to
// "outsider") is scripted to (1) ToolSearch, (2) delegate to "outsider" —
// denied by the real trust-graph gate — then (3) delegate to "planner", which
// DOES have a trust edge and must still succeed in the SAME turn. If the
// trust-graph denial had incorrectly fed the ADR-058 ledger, step 3 would
// come back permission_denied (quarantined) instead of spawning planner.
func TestNestedDelegate_TrustGraphDenialDoesNotPoisonSubsequentCalls(t *testing.T) {
	al := newNestedDelegationAgentLoop(t)

	// ray: ToolSearch -> delegate(outsider) [denied, no trust edge] ->
	// delegate(planner) [authorized] -> final text.
	raySeq := newScriptedProvider(
		&providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{
				ID:        "ray-call-1",
				Name:      "ToolSearch",
				Arguments: map[string]any{"names": []string{"delegate"}},
			}},
		},
		&providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{
				ID:   "ray-call-2",
				Name: "delegate",
				Arguments: map[string]any{
					"task":     "attempt an UNAUTHORIZED delegate to outsider",
					"agent_id": "outsider",
					"async":    false,
				},
			}},
		},
		&providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{
				ID:   "ray-call-3",
				Name: "delegate",
				Arguments: map[string]any{
					"task":     "retry with the AUTHORIZED target planner",
					"agent_id": "planner",
					"async":    false,
				},
			}},
		},
		&providers.LLMResponse{Content: "ray: outsider was refused, planner succeeded"},
	)
	setAgentProvider(t, al, "ray", raySeq)

	// planner: a single final-text response — it must still get the chance
	// to run at all, which is the whole point of this test.
	plannerSeq := newScriptedProvider(
		&providers.LLMResponse{Content: "planner: task received and done"},
	)
	setAgentProvider(t, al, "planner", plannerSeq)

	collector, cleanup := newEventCollector(t, al)
	defer cleanup()

	parent := buildParentTurnState(t, al)
	ctx := withSpawnToolCallID(context.Background(), "jim-to-ray-trust-boundary-call")
	_, err := spawnSubTurn(ctx, al, parent, SubTurnConfig{
		Model:         "test-model",
		SystemPrompt:  "hop 1: await-delegate to ray; ray tries outsider (denied) then planner (allowed)",
		TargetAgentID: "ray",
		Async:         false,
		Timeout:       5 * time.Second,
	})
	require.NoError(t, err, "hop 1 (jim -> ray) spawnSubTurn must succeed")

	// THE PROOF: planner must still be spawned — showing ray's SECOND
	// delegate call (to an authorized target) was not blocked by the FIRST
	// call's trust-graph denial of a DIFFERENT target.
	require.Eventually(t, func() bool {
		return len(findSubTurnSpawnAgentIDs(collector)) >= 2
	}, testEventTimeout, testEventPoll,
		"expected two SubTurnSpawn events (ray, then planner); planner missing means the "+
			"trust-graph denial for 'outsider' incorrectly quarantined 'delegate' for the rest of the turn")

	ids := findSubTurnSpawnAgentIDs(collector)
	assert.Contains(t, ids, "ray", "must have spawned ray (hop 1)")
	assert.Contains(t, ids, "planner", "must have spawned planner (hop 2b, the authorized retry)")
	assert.NotContains(t, ids, "outsider",
		"outsider must NEVER be spawned — the trust-graph gate must refuse it before "+
			"DelegateTool.Execute ever reaches spawnSubTurn")

	// ADR-058's classification/quarantine machinery must never observe this
	// denial: it is a real ToolResult from DelegateTool.Execute's own
	// trust-set gate, not one of loop.go's three synthetic permission_denied
	// sites (nor the new quarantine-replay branch), so it can never produce a
	// ToolExecSkipped event for "delegate" — this is the DelegationDeniedResult
	// out-of-scope boundary named by ADR-058 §1.
	assertNoDelegateDeniedByPolicy(t, collector)

	assert.Equal(t, 0, raySeq.Remaining(),
		"ray's full 4-step scripted sequence must have been consumed — a stuck remainder "+
			"means the outsider denial was mishandled (e.g. the turn aborted early)")
	assert.Equal(t, 0, plannerSeq.Remaining(), "planner's scripted response must be consumed")
}
