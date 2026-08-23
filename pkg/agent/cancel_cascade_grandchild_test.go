// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// cancel_cascade_grandchild_test.go — ADR-057 D8/R-13 gap-closure regression.
//
// THE BUG (UAT gap-closure report, 2026-08-03, docs/internal/uat/uat-report-
// adr057-gap-closure-2026-08-03.md, "GAP 2"): a real jim -> ray -> worker
// delegation chain was built live against a deployed build. ray (the middle
// delegate) made ONE synchronous nested `delegate` call to worker, whose task
// started a detached background HTTP server and then blocked in a foreground
// bash call. Hard-cancelling ray reported success and durably transitioned
// ray's OWN lifecycle record to cancelled — but worker (ray's own child) kept
// running for almost three more minutes: it posted new assistant messages, ran
// a NEW tool call, and reached a NATURAL "completed" terminal state, never
// "cancelled". Its background HTTP server was still externally reachable
// minutes after the cancel. This is the exact "leak" ADR-057 D8/R-13 (AC-8)
// state `delegate action=cancel` must close: cancelling a child must reach
// that child's own descendants, not just the one named session.
//
// ROOT CAUSE, as traced by the UAT report and independently re-verified here
// against the actual code (see this file's own investigation, not just the
// report's word for it):
//
//  1. pkg/tools/delegate.go's executeCancel called killChildBackgroundShells
//     with ONLY the single named session id — KillAllForSessions never saw
//     a descendant's background shell at all, regardless of how the turn-
//     level cascade behaved. Fixed in this same change (killChildBackground
//     Shells / collectCancelDescendantSessionIDs, delegate.go) by walking the
//     durable ParentDurableKey edge before the kill call, mirroring
//     agent.CollectDescendantSessionIDs's (pkg/agent/cancel.go) walk exactly
//     (duplicated rather than shared because pkg/tools cannot import
//     pkg/agent).
//  2. The in-memory LIVE-TURN cascade (Interrupt/InterruptSessionHard with
//     ScopeSubtree, steering.go) was ALREADY wired correctly at this
//     package's HEAD by an earlier same-day commit (b51dae84, "close
//     ADR-057 cancel-cascade leaks in delegate scope, descendant walk, and
//     comments") — session_messaging_wire.go's SetCancelHooks closures call
//     al.Interrupt/al.InterruptSessionHard with ScopeSubtree, not
//     ScopeSelfOnly. That commit's own regression test
//     (TestDelegateCancel_WiredThroughRealAgentLoop_ReachesGrandchild,
//     cancel_scope_fix_test.go) proves the ScopeSubtree mechanism against a
//     SYNTHETIC fixture (u19FixtureTurns manually registers parent/child/
//     grandchild turnStates with pre-wired parentTurnID links) — it does not
//     drive an actual nested `delegate` tool call through spawnSubTurn twice.
//     THIS test closes that gap: it builds the grandchild via a REAL nested
//     delegate call (ray's own scripted provider issues a genuine
//     `delegate(agent_id="worker", async=false)` tool call, which dispatches
//     through the real DelegateTool.executeSync -> spawner.SpawnSubTurn ->
//     spawnSubTurn a SECOND time), proving the SAME real code path the live
//     UAT chain exercised — not a re-assertion of the fixture-based proof
//     that commit already has.
//
// Both halves (turn-level interrupt AND background-shell kill) are asserted
// here against ONE real chain, because the UAT report's reproduction bundled
// them together (a synchronous nested delegate whose child owns both a live
// turn AND a background shell) and a fix that only closed one half would
// still leave the other half of the exact reported defect live.
//
// Per binding Rule 4, every positive assertion below has a companion negative
// (sibling-session) assertion in the same test, so a mechanism that reaches
// everything indiscriminately would fail exactly as loudly as one that
// reaches nothing.
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

// cascadeFakePIDBase mirrors pkg/tools/session_kill_all_for_session_test.go's
// fakeUnusedPIDBase precedent (a PID value picked well above any real process
// ID this sandbox will ever allocate, so killProcessGroup's underlying
// syscall.Kill(-pid, ...) reliably returns ESRCH — treated as success). Kept
// as its own constant (not imported — it's unexported in a different
// package's _test.go file) and offset from that package's range purely so a
// developer reading a failure never has to wonder whether the two ranges
// collided; they cannot, in-process, since this is a real syscall against a
// nonexistent PID either way.
const cascadeFakePIDBase = 999992000

// newCancelCascadeAgentLoop builds a real *AgentLoop with two native agents,
// "ray" and "worker", a workspace delegation edge ray -> worker (all modes),
// and session-messaging enabled (required for action="cancel" — FR-196's
// kill switch gates every session-messaging action, isSessionMessagingAction,
// delegate.go). Mirrors newNestedDelegationAgentLoop's shape
// (subturn_delegate_nesting_test.go) with the agent names this test needs and
// SessionMessaging.Enabled turned on for the cancel call under test.
func newCancelCascadeAgentLoop(t *testing.T) (*AgentLoop, *session.LifecycleStore) {
	t.Helper()

	seedWorkspaceGraph(t, "ws-cancel-cascade", true, []graphEdge{
		edge("ray", "worker", nil, nil),
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
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Provider: "mock", Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{
					// The outermost delegator needs a real registered instance.
					// It used to come from GetDefaultAgent's last-resort rung,
					// which returned ANY agent including a worker; that rung is
					// gone (ADR-064 §7) and every other agent here IS a worker,
					// so spawnSubTurn failed with "parent turnState has no agent
					// instance".
					ID:    "jim",
					Home:  tmpDir,
					Model: &config.AgentModelConfig{Primary: "test-model"},
				},
				{
					ID:    "ray",
					Type:  config.AgentTypeWorker,
					Home:  tmpDir,
					Model: &config.AgentModelConfig{Primary: "test-model"},
					Tools: allowDelegate,
				},
				{
					ID:    "worker",
					Type:  config.AgentTypeWorker,
					Home:  tmpDir,
					Model: &config.AgentModelConfig{Primary: "test-model"},
					Tools: allowDelegate,
				},
			},
		},
	}
	cfg.SessionMessaging.Enabled = boolPtr(true)

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(al.Close)

	lifecycleStore := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(nil, lifecycleStore)

	_, ok := al.GetRegistry().GetAgent("ray")
	require.True(t, ok, "ray must be registered")
	_, ok = al.GetRegistry().GetAgent("worker")
	require.True(t, ok, "worker must be registered")

	return al, lifecycleStore
}

// findSubTurnSpawnByAgentID scans c's collected events for the FIRST
// EventKindSubTurnSpawn whose AgentID matches agentID and returns its Label
// (== the spawned child's own delegateSessionID / activeTurnStates key — see
// subturn.go's emitEvent call site, "Label: childID"). Used to learn worker's
// real, dynamically-minted session id (executeRun mints it via uuid.NewString
// — there is no way to pre-specify it from a scripted tool-call argument) so
// this test can attach a real background ProcessSession to it before firing
// the cancel.
func findSubTurnSpawnByAgentID(c *eventCollector, agentID string) (SubTurnSpawnPayload, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.events {
		if e.Kind != EventKindSubTurnSpawn {
			continue
		}
		p, ok := e.Payload.(SubTurnSpawnPayload)
		if ok && p.AgentID == agentID {
			return p, true
		}
	}
	return SubTurnSpawnPayload{}, false
}

// TestDelegateCancelHard_RealNestedChain_ReachesGrandchildTurnAndBackgroundShell
// is the end-to-end proof for the ADR-057 D8/R-13 gap-closure fix, driven
// through a REAL two-level delegation chain (jim(fixture) -> ray(real
// spawnSubTurn) -> worker(ray's OWN real nested delegate tool call)):
//
//	Given ray is genuinely blocked mid-turn on its own synchronous nested
//	  delegate call to worker, and worker's own in-flight LLM call is
//	  genuinely blocked (not yet returned), and worker owns a REAL,
//	  running background ProcessSession (its "detached background HTTP
//	  server" stand-in),
//	When the delegating caller invokes delegate(action="cancel",
//	  session_id=<ray's own delegateSessionID>, hard=true),
//	Then worker's OWN in-flight LLM-call context is actually canceled
//	  (the live-turn half of the cascade), AND worker's own background
//	  ProcessSession is actually killed/relabeled canceled (the
//	  background-shell half) — within seconds, not left running for
//	  minutes as the live UAT reproduction observed.
//
// Negative controls (Rule 4): an unrelated sibling background session (owned
// by a session id that is NOT part of ray's subtree) must remain untouched —
// proving the new descendant walk does not over-reach into a same-owner-map
// but different-subtree session.
func TestDelegateCancelHard_RealNestedChain_ReachesGrandchildTurnAndBackgroundShell(t *testing.T) {
	al, lifecycleStore := newCancelCascadeAgentLoop(t)

	// ray: a single scripted response — a genuine nested delegate tool call
	// to worker, synchronous (async=false), so ray's own turn stays blocked
	// on it for the whole test (matching the UAT reproduction's "one
	// synchronous nested delegate call" shape exactly).
	raySeq := newScriptedProvider(
		&providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{
				ID:   "ray-cascade-call-1",
				Name: "delegate",
				Arguments: map[string]any{
					"task":     "start background work and wait",
					"agent_id": "worker",
					"async":    false,
				},
			}},
		},
	)
	setAgentProvider(t, al, "ray", raySeq)

	// worker: a REAL in-flight LLM call that blocks until its ctx is
	// canceled — the same rigor pkg/agent/interrupt_by_session_key_test.go's
	// #577 regression uses ("proof positive the cancellation actually
	// reached the in-flight call", not merely a flag check).
	workerProvider := newCtxCancelObservingProvider()
	setAgentProvider(t, al, "worker", workerProvider)

	collector, cleanupCollector := newEventCollector(t, al)
	defer cleanupCollector()

	parent := buildParentTurnState(t, al)
	const rayChildID = "test-cascade-ray-B"

	// Seed ray's own lifecycle record exactly as delegate.go's `run` action
	// would have minted it for a real (non-test-harness) top-level
	// delegation — required because hop 1 (jim -> ray) is driven directly
	// via spawnSubTurn (bypassing DelegateTool.executeRun, which is what
	// would normally mint this), mirroring cancel_scope_fix_test.go's
	// TestDelegateCancel_WiredThroughRealAgentLoop_ReachesGrandchild fixture
	// pattern. worker's OWN record, by contrast, is minted for real by
	// executeRun when ray's nested delegate call actually fires (below) —
	// not seeded here.
	require.NoError(t, lifecycleStore.Persist(&session.LifecycleRecord{
		SessionID:        rayChildID,
		State:            session.LifecycleRunning,
		OwnerScopeKind:   session.OwnerScopeParentSession,
		OwnerScopeID:     parent.transcriptSessionID,
		ParentDurableKey: parent.transcriptSessionID,
		AgentID:          "ray",
	}))

	spawnDone := make(chan struct{})
	var spawnErr error
	go func() {
		defer close(spawnDone)
		spawnCtx := withSpawnToolCallID(context.Background(), "jim-to-ray-cascade-call")
		_, spawnErr = spawnSubTurn(spawnCtx, al, parent, SubTurnConfig{
			Model:             "test-model",
			SystemPrompt:      "hop 1: await-delegate to ray, which itself await-delegates to worker",
			TargetAgentID:     "ray",
			Async:             false,
			Timeout:           30 * time.Second,
			DelegateSessionID: rayChildID,
		})
	}()

	// Wait for worker's LLM call to actually be in flight (blocked on its own
	// ctx.Done()) before doing anything else — the exact window the D8/R-13
	// leak lived in.
	select {
	case <-workerProvider.entered:
	case <-spawnDone:
		t.Fatalf("spawnSubTurn(ray) returned before worker's LLM call ever started (rayErr=%v) — "+
			"the nested chain never actually ran, so there is nothing for delegate(cancel) to cascade to", spawnErr)
	case <-time.After(5 * time.Second):
		t.Fatal("worker's LLM call never started")
	}

	// Learn worker's REAL, dynamically-minted session id from its own
	// SubTurnSpawn event (executeRun mints it via uuid.NewString — it cannot
	// be pre-specified from the scripted tool-call arguments above).
	var workerSpawn SubTurnSpawnPayload
	require.Eventually(t, func() bool {
		var ok bool
		workerSpawn, ok = findSubTurnSpawnByAgentID(collector, "worker")
		return ok
	}, testEventTimeout, testEventPoll, "expected a SubTurnSpawn event for worker (ray's own nested delegate call)")
	workerSessionID := workerSpawn.Label
	require.NotEmpty(t, workerSessionID, "worker's SubTurnSpawn event must carry its own session id as Label")

	// Attach a REAL background ProcessSession to worker's session id — the
	// stand-in for the UAT chain's detached background HTTP server. A real
	// fake-but-out-of-range PID (session_kill_all_for_session_test.go's
	// established pattern) makes the kill syscall genuinely execute
	// (ESRCH-as-success), not merely flip a struct field a spy recorded.
	sm := tools.GetSharedSessionManager()
	const workerBgID = "test-cascade-worker-bg"
	sm.Add(&tools.ProcessSession{
		ID:             workerBgID,
		OwnerSessionID: workerSessionID,
		Status:         tools.StatusRunning,
		PID:            cascadeFakePIDBase + 1,
		StartTime:      time.Now().Unix(),
	})
	t.Cleanup(func() { sm.Remove(workerBgID) })

	// Negative control: an UNRELATED sibling background session, owned by a
	// session id that is not part of ray's subtree at all. Must remain
	// untouched by the cancel below — proving the new descendant walk does
	// not over-reach.
	const siblingOwnerID = "test-cascade-unrelated-sibling-owner"
	const siblingBgID = "test-cascade-sibling-bg"
	sm.Add(&tools.ProcessSession{
		ID:             siblingBgID,
		OwnerSessionID: siblingOwnerID,
		Status:         tools.StatusRunning,
		PID:            cascadeFakePIDBase + 2,
		StartTime:      time.Now().Unix(),
	})
	t.Cleanup(func() { sm.Remove(siblingBgID) })

	// The real call site: delegate(action="cancel", session_id=rayChildID,
	// hard=true), via the default agent's own real, production-registered
	// DelegateTool — mirroring TestDelegateCancel_WiredThroughRealAgentLoop_
	// ReachesGrandchild and the #577 tests' exact pattern (the calling
	// agent's own tool instance is irrelevant; cancel hooks/lifecycle/
	// session-manager are wired identically and shared across every agent).
	defAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defAgent, "test harness must seed a default agent")
	toolIface, ok := defAgent.Tools.Get("delegate")
	require.True(t, ok, "default agent must have a real delegate tool registered")
	dt, ok := toolIface.(*tools.DelegateTool)
	require.True(t, ok, "delegate tool must be a *tools.DelegateTool")

	cancelCtx := tools.WithTranscriptSessionID(context.Background(), parent.transcriptSessionID)
	result := dt.Execute(cancelCtx, map[string]any{
		"action":     "cancel",
		"session_id": rayChildID,
		"hard":       true,
	})
	require.NotNil(t, result)
	require.False(t, result.IsError, "delegate cancel(hard=true) on ray must succeed: %s", result.ForLLM)

	// THE CENTRAL ASSERTIONS.

	// (a) Live-turn half: worker's OWN in-flight LLM-call context must
	// actually be canceled — not merely that ray's own turn ended.
	select {
	case gotErr := <-workerProvider.canceled:
		require.ErrorIs(t, gotErr, context.Canceled,
			"worker's (the grandchild's) in-flight LLM-call context must have been canceled by the cascade "+
				"rooted at ray, not merely timed out or errored some other way")
	case <-time.After(3 * time.Second):
		t.Fatal("worker's (the grandchild's) context was NOT canceled within 3s of delegate cancel(hard=true) on " +
			"ray — this is the ADR-057 D8/R-13 live-turn cascade gap: cancelling the middle delegate must also " +
			"reach that delegate's own child")
	}

	// (b) Background-shell half, positive: worker's background ProcessSession
	// must transition to the terminal "canceled" status (KillAndRelabel,
	// session.go) — proving a REAL kill was attempted and landed, not just a
	// flag being set.
	require.Eventually(t, func() bool {
		s, err := sm.Get(workerBgID)
		return err == nil && s.GetStatus() == string(tools.StatusCanceled)
	}, 3*time.Second, 50*time.Millisecond,
		"worker's background ProcessSession must be killed and relabeled canceled by cancelling ray — "+
			"before the fix, executeCancel's killChildBackgroundShells call only ever named ray's own session "+
			"id, never worker's, so this ProcessSession would be left at StatusRunning forever (the exact "+
			"'background HTTP server still reachable minutes later' UAT finding)")

	// (c) Background-shell half, negative (Rule 4 companion): the unrelated
	// sibling session must be untouched — the cascade must not have killed
	// every RUNNING session in the shared SessionManager indiscriminately.
	siblingSession, err := sm.Get(siblingBgID)
	require.NoError(t, err, "sibling ProcessSession must still be present")
	assert.Equal(t, string(tools.StatusRunning), siblingSession.GetStatus(),
		"an unrelated sibling's background session must remain untouched by a cancel scoped to ray's own subtree — "+
			"a cascade that kills everything indiscriminately is exactly as wrong as one that reaches nothing")

	// ray's own outer spawnSubTurn call must unwind promptly once its
	// blocked nested call to worker returns (interrupted).
	select {
	case <-spawnDone:
	case <-time.After(3 * time.Second):
		t.Fatal("ray's own spawnSubTurn call did not return promptly after worker's context was canceled")
	}
}
