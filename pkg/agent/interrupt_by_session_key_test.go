// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for GitHub issue #577: delegate(action="cancel")
// (both soft and hard) returned a success message but had zero effect on
// the target sub-agent, because the cancel hooks wired onto DelegateTool
// (pkg/agent/session_messaging_wire.go) called AgentLoop.InterruptSession/
// InterruptSessionHard — which match a turnState by transcriptSessionID
// (deliberately the PARENT's shared chat id, subturn.go's FR-6a) — with the
// caller-supplied delegateSessionID (a fresh UUID, minted by
// pkg/tools/delegate.go, that can never equal any turnState's
// transcriptSessionID). Zero Range matches is InterruptSession's documented
// non-error no-op, so the cancel silently did nothing while still reporting
// success 100% of the time.
//
// The fix adds InterruptBySessionKey/InterruptBySessionKeyHard
// (pkg/agent/steering.go), which do a direct activeTurnStates.Load(sessionKey)
// instead of the transcriptSessionID-filtered Range, and rewires
// session_messaging_wire.go's SetCancelHooks call to use them.
//
// This test proves the fix end-to-end through the REAL production call
// path: a real AgentLoop, a real spawned sub-turn (spawnSubTurn), the real
// wiring installed by SetSessionMessagingStores/wireSessionMessagingForAgent,
// and the real public DelegateTool.Execute("cancel") entry point — the exact
// surface `delegate(action="cancel", ...)` reaches in production. The central
// assertion is not "a hook fired" (the existing mock-hook tests in
// pkg/tools/delegate_adr053_test.go already proved that much and still
// missed this bug) but that the target sub-turn's OWN in-flight LLM-call
// context was ACTUALLY canceled as a direct result of the call.
package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ctxCancelObservingProvider is a minimal providers.LLMProvider whose Chat
// call NEVER completes on its own — it blocks until its ctx is Done, then
// reports the observed ctx.Err() on a buffered channel. Unlike a
// time.After-raced "slow" mock provider, this gives a direct, unambiguous
// observation of the EXACT context object the target sub-turn's blocked LLM
// call received: the only way Chat ever returns is via that context being
// canceled, so a successful receive on `canceled` is proof positive the
// cancellation actually reached the in-flight call — independent of how
// spawnSubTurn's own cleanup defer subsequently classifies/labels the
// outcome (which is a separate, orthogonal bookkeeping concern from "did the
// cancel actually reach the target").
type ctxCancelObservingProvider struct {
	entered  chan struct{}
	enterOne sync.Once
	canceled chan error
}

func newCtxCancelObservingProvider() *ctxCancelObservingProvider {
	return &ctxCancelObservingProvider{
		entered:  make(chan struct{}),
		canceled: make(chan error, 1),
	}
}

func (p *ctxCancelObservingProvider) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.enterOne.Do(func() { close(p.entered) })
	<-ctx.Done()
	err := ctx.Err()
	select {
	case p.canceled <- err:
	default:
	}
	return nil, err
}

func (p *ctxCancelObservingProvider) GetDefaultModel() string {
	return "ctx-cancel-observer"
}

// TestDelegateCancelHard_RealSubTurn_ActuallyCancelsTargetContext is the
// integration-style regression test for #577. It drives:
//
//  1. A REAL *AgentLoop (mustNewAgentLoop) with a REAL default agent whose
//     "delegate" tool was registered by registerSharedTools exactly as in
//     production.
//  2. The REAL wiring entrypoint (AgentLoop.SetSessionMessagingStores ->
//     wireSessionMessagingForAgent), which installs
//     dt.SetCancelHooks(al.InterruptBySessionKey, al.InterruptBySessionKeyHard)
//     onto that real DelegateTool — i.e. this test would still exercise the
//     PRE-fix wiring (al.InterruptSession/InterruptSessionHard) if the
//     session_messaging_wire.go rewire were reverted, making it a faithful
//     red/green gate for the fix.
//  3. A REAL spawned sub-turn (spawnSubTurn) with a caller-controlled
//     DelegateSessionID (childID) — mirroring the delegateSessionID
//     pkg/tools/delegate.go's `run` action mints and hands back to the
//     caller for all subsequent status/steer/respond/cancel/peek calls.
//  4. The REAL public entry point delegate(action="cancel", session_id=childID,
//     hard=true) via DelegateTool.Execute — including the FAIL-CLOSED
//     caller-ownership check (verifyCallerOwnsSession) a genuine call goes
//     through.
//
// BDD:
//
//	Given a delegated sub-turn is genuinely running (its LLM call is blocked
//	  in flight) and registered under its own delegateSessionID,
//	When the delegating caller invokes delegate(action="cancel",
//	  session_id=<that delegateSessionID>, hard=true),
//	Then the target sub-turn's OWN in-flight LLM-call context is actually
//	  canceled within seconds — not silently no-op'd while still reporting
//	  success.
//
// Negative-test discipline (red/green): confirmed to hang past the 3s
// cancellation-wait deadline (t.Fatal on the "context was NOT canceled"
// branch) when session_messaging_wire.go's SetCancelHooks call is reverted
// to wire al.InterruptSession/al.InterruptSessionHard instead — see the
// delivery report for the exact revert/re-apply steps used to verify this.
func TestDelegateCancelHard_RealSubTurn_ActuallyCancelsTargetContext(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "mock"}},
			List:     []config.AgentConfig{{ID: "mia"}},
		},
	}
	// #577's cancel path is gated behind the FR-196 session-messaging kill
	// switch (isSessionMessagingAction("cancel") == true) — must be enabled
	// or DelegateTool.Execute short-circuits before ever reaching
	// executeCancel.
	cfg.SessionMessaging.Enabled = boolPtr(true)

	msgBus := bus.NewMessageBus()
	provider := newCtxCancelObservingProvider()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	// Real wiring (ingredient #2 above): install a real, non-nil
	// LifecycleStore via the exact production entrypoint
	// (SetSessionMessagingStores -> wireSessionMessagingForAgent) that sets
	// dt.SetCancelHooks(al.InterruptBySessionKey, al.InterruptBySessionKeyHard)
	// on every registered agent's real delegate tool.
	lifecycleStore := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(nil, lifecycleStore)

	defAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defAgent, "test harness must seed a default agent")
	toolIface, ok := defAgent.Tools.Get("delegate")
	require.True(t, ok, "default agent must have a real delegate tool registered by registerSharedTools")
	dt, ok := toolIface.(*tools.DelegateTool)
	require.True(t, ok, "delegate tool must be a *tools.DelegateTool")

	const childID = "test-577-delegate-child"

	// ADR-057 FR-005/FR-096 fixture repair: spawnSubTurn's
	// sharedStore.CreateSessionWithID(childID, parentTS.transcriptSessionID, ...)
	// requires the parent id to resolve to a REAL session in
	// al.GetSessionStore() — an empty/unset transcriptSessionID (the pre-fix
	// literal here) fails validateSessionID and spawnSubTurn returns an error
	// before ever reaching runTurn, so the target's LLM call never starts.
	// Mint a real parent session and use its id as the caller/lifecycle key.
	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")
	parentMeta, err := store.NewSession(session.SessionTypeChat, "web", defAgent.ID)
	require.NoError(t, err)
	callerKey := parentMeta.ID

	// Seed the lifecycle record delegate.go's `run` action would have
	// minted for a real delegation, so executeCancel's FAIL-CLOSED ownership
	// check (verifyCallerOwnsSession) passes exactly as it would for a
	// genuine parent->child cancel — not bypassed for the test.
	require.NoError(t, lifecycleStore.Persist(&session.LifecycleRecord{
		SessionID:        childID,
		State:            session.LifecycleRunning,
		OwnerScopeKind:   session.OwnerScopeHuman,
		ParentDurableKey: callerKey,
		AgentID:          defAgent.ID,
	}))

	parentTS := &turnState{
		agent:               defAgent,
		turnID:              "parent-577",
		depth:               0,
		session:             newEphemeralSession(nil),
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		al:                  al,
		transcriptSessionID: callerKey,
		routingSessionID:    session.RoutingSessionID(callerKey),
		transcriptStore:     store,
	}
	parentTS.ctx, parentTS.cancelFunc = context.WithCancel(context.Background())

	spawnDone := make(chan struct{})
	var spawnErr error
	go func() {
		defer close(spawnDone)
		spawnCtx := withSpawnToolCallID(parentTS.ctx, "call-577-1")
		subCfg := SubTurnConfig{
			Model:             "ctx-cancel-observer",
			Async:             true,
			Critical:          true,
			Timeout:           30 * time.Second,
			DelegateSessionID: childID,
		}
		_, spawnErr = spawnSubTurn(spawnCtx, al, parentTS, subCfg)
	}()

	// Wait for the target's LLM call to actually be in flight (blocked on
	// its own ctx.Done()) before firing the cancel — the exact window the
	// #577 bug lived in: a cancel arriving while the target is genuinely
	// running, which used to be silently swallowed.
	//
	// The <-spawnDone branch (in addition to the plain 5s timeout) is a
	// deliberate diagnostic improvement, not a relaxation of the assertion:
	// a genuine hang still only fails via the timeout branch below, exactly
	// as before. Root-cause investigation of a real failure here (2026-08-03)
	// found spawnSubTurn actually returns almost immediately with an error —
	// e.g. CreateSessionWithID's FR-096 collision guard refusing a re-used
	// childID directory left on disk by a prior test run — and that fast,
	// silent error return was indistinguishable from a genuine 5s hang
	// because the goroutine discarded it (`_, _ = spawnSubTurn(...)`).
	// Surfacing spawnErr here turns that class of failure into an immediate,
	// actionable message instead of a misleading "LLM call never started"
	// after a needless 5s wait.
	select {
	case <-provider.entered:
	case <-spawnDone:
		t.Fatalf("spawnSubTurn returned before the target's LLM call ever started (err=%v) — "+
			"the sub-turn never actually ran, so there is nothing for delegate(cancel) to cancel", spawnErr)
	case <-time.After(5 * time.Second):
		t.Fatal("target sub-turn's LLM call never started")
	}
	require.Eventually(t, func() bool {
		return al.getActiveTurnState(childID) != nil
	}, 2*time.Second, 5*time.Millisecond,
		"child sub-turn must be registered in activeTurnStates under its delegateSessionID (childID)")

	// The real call site: delegate(action="cancel", session_id=childID,
	// hard=true) — DelegateTool.executeCancel calls t.cancelHard(sessionID,
	// hint), which the wiring above set to al.InterruptBySessionKeyHard.
	cancelCtx := tools.WithTranscriptSessionID(context.Background(), callerKey)
	result := dt.Execute(cancelCtx, map[string]any{
		"action":     "cancel",
		"session_id": childID,
		"hard":       true,
	})
	require.NotNil(t, result)
	require.False(t, result.IsError, "delegate cancel(hard=true) must succeed: %s", result.ForLLM)

	// THE central assertion: the specific ctx object the target's blocked
	// Chat call received was ACTUALLY canceled as a direct result of the
	// delegate.cancel call above. Before the fix, the pre-fix wiring
	// (al.InterruptSession/InterruptSessionHard) filters on
	// transcriptSessionID, which a delegateSessionID can never match — zero
	// matches, so this channel would never receive and the target would run
	// to completion untouched.
	select {
	case gotErr := <-provider.canceled:
		require.ErrorIs(t, gotErr, context.Canceled,
			"target sub-turn's LLM-call context must have been canceled, not merely timed out or errored some other way")
	case <-time.After(3 * time.Second):
		t.Fatal("target sub-turn's context was NOT canceled within 3s of delegate cancel(hard=true) — " +
			"this is the #577 regression: the cancel hook did not reach the turn registered under this sessionKey")
	}

	select {
	case <-spawnDone:
	case <-time.After(3 * time.Second):
		t.Fatal("spawnSubTurn did not return promptly after its context was canceled")
	}
}

// TestDelegateCancelSoft_RealSubTurn_ActuallyCancelsTargetContext is the
// soft-cancel (hard=false) counterpart of the test above, proving
// InterruptBySessionKey (not just its Hard sibling) fires the target's
// providerCancel for real (FR-12a — the graceful cascade still aborts the
// CURRENT in-flight LLM call immediately even though it does not tear down
// the whole turn). Mirrors the hard-cancel test's setup; see its doc comment
// for the full #577 rationale.
func TestDelegateCancelSoft_RealSubTurn_ActuallyCancelsTargetContext(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "mock"}},
			List:     []config.AgentConfig{{ID: "mia"}},
		},
	}
	cfg.SessionMessaging.Enabled = boolPtr(true)

	msgBus := bus.NewMessageBus()
	provider := newCtxCancelObservingProvider()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	lifecycleStore := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(nil, lifecycleStore)

	defAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defAgent, "test harness must seed a default agent")
	toolIface, ok := defAgent.Tools.Get("delegate")
	require.True(t, ok, "default agent must have a real delegate tool registered by registerSharedTools")
	dt, ok := toolIface.(*tools.DelegateTool)
	require.True(t, ok, "delegate tool must be a *tools.DelegateTool")

	const childID = "test-577-delegate-child-soft"

	// ADR-057 FR-005/FR-096 fixture repair: see the identical note in
	// TestDelegateCancelHard_RealSubTurn_ActuallyCancelsTargetContext above.
	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")
	parentMeta, err := store.NewSession(session.SessionTypeChat, "web", defAgent.ID)
	require.NoError(t, err)
	callerKey := parentMeta.ID

	require.NoError(t, lifecycleStore.Persist(&session.LifecycleRecord{
		SessionID:        childID,
		State:            session.LifecycleRunning,
		OwnerScopeKind:   session.OwnerScopeHuman,
		ParentDurableKey: callerKey,
		AgentID:          defAgent.ID,
	}))

	parentTS := &turnState{
		agent:               defAgent,
		turnID:              "parent-577-soft",
		depth:               0,
		session:             newEphemeralSession(nil),
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		al:                  al,
		transcriptSessionID: callerKey,
		routingSessionID:    session.RoutingSessionID(callerKey),
		transcriptStore:     store,
	}
	parentTS.ctx, parentTS.cancelFunc = context.WithCancel(context.Background())

	spawnDone := make(chan struct{})
	var spawnErr error
	go func() {
		defer close(spawnDone)
		spawnCtx := withSpawnToolCallID(parentTS.ctx, "call-577-2")
		subCfg := SubTurnConfig{
			Model:             "ctx-cancel-observer",
			Async:             true,
			Critical:          true,
			Timeout:           30 * time.Second,
			DelegateSessionID: childID,
		}
		_, spawnErr = spawnSubTurn(spawnCtx, al, parentTS, subCfg)
	}()

	// See the identical <-spawnDone diagnostic branch in
	// TestDelegateCancelHard_RealSubTurn_ActuallyCancelsTargetContext above
	// for why this exists: it turns a fast, silent spawnSubTurn error return
	// into an immediate, actionable failure instead of a misleading 5s
	// timeout. The plain timeout branch below still covers a genuine hang,
	// unchanged.
	select {
	case <-provider.entered:
	case <-spawnDone:
		t.Fatalf("spawnSubTurn returned before the target's LLM call ever started (err=%v) — "+
			"the sub-turn never actually ran, so there is nothing for delegate(cancel) to cancel", spawnErr)
	case <-time.After(5 * time.Second):
		t.Fatal("target sub-turn's LLM call never started")
	}
	require.Eventually(t, func() bool {
		return al.getActiveTurnState(childID) != nil
	}, 2*time.Second, 5*time.Millisecond,
		"child sub-turn must be registered in activeTurnStates under its delegateSessionID (childID)")

	// The real call site: delegate(action="cancel", session_id=childID,
	// hard=false) — DelegateTool.executeCancel calls t.cancelSoft(sessionID,
	// hint), which the wiring above set to al.InterruptBySessionKey.
	cancelCtx := tools.WithTranscriptSessionID(context.Background(), callerKey)
	result := dt.Execute(cancelCtx, map[string]any{
		"action":     "cancel",
		"session_id": childID,
		"hard":       false,
	})
	require.NotNil(t, result)
	require.False(t, result.IsError, "delegate cancel(hard=false) must succeed: %s", result.ForLLM)

	select {
	case gotErr := <-provider.canceled:
		require.ErrorIs(t, gotErr, context.Canceled,
			"target sub-turn's LLM-call context must have been canceled by the soft-cancel path (FR-12a's immediate providerCancel)")
	case <-time.After(3 * time.Second):
		t.Fatal("target sub-turn's context was NOT canceled within 3s of delegate cancel(hard=false) — " +
			"this is the #577 regression: the cancel hook did not reach the turn registered under this sessionKey")
	}
}
