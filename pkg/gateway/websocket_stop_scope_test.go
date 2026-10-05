// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// websocket_stop_scope_test.go pins requestScopedStop's two scoped-Stop
// contracts documented in websocket_stop_scope.go's own header comment
// ("both web Stop scopes end turns, never goals") against the TWO cases
// commit c6bc40804 ("fix(gateway): scope web Stop without ending active
// goals") touches at once:
//
//  1. (regression, RED below) A steered child with a durable steering edge
//     that was only ever queued/parked — never ran a turn — must still be
//     landed LifecycleStopped and report "interrupted:" upward on a web
//     Stop, exactly as pkg/agent's Finding 5
//     (TestSteerGenerationCancel_NeverRanChildUnblocksParent,
//     pkg/agent/steer_cancel_test.go) proves for SteerCanceller.CancelSubtree
//     called directly. requestScopedStop's cancelTurn closure
//     (websocket_stop_scope.go:26-41, the "stopTurn" func literal) calls
//     ONLY h.requestTurnStop -> agentLoop.RequestCancel — never
//     al.SteerGenerationCancel — so SteerCanceller.cascade's cancelStamped
//     (pkg/agent/steer_cancel.go) never reaches terminaliseNeverRanStop for
//     this call chain: the never-ran session is stranded at whatever state
//     it was in before the Stop, forever.
//
//     Oracle, derived from the code's own documented contract — never from
//     running this test against the bug and copying its output:
//     terminaliseNeverRanStop's own call,
//     reportSteeredSessionTerminalUpward(ctx, sessionID, generation,
//     session.LifecycleStopped, steer.OutcomeInterrupted, "interrupted: the
//     session was cancelled") (pkg/agent/steer_cancel.go), and
//     deliverSubagentEnd's own outcome->status switch
//     (pkg/agent/steer_frames.go), which maps steer.OutcomeInterrupted to
//     SubTurnStatusInterrupted ("interrupted").
//
//  2. (positive control, should already be green on current HEAD) An
//     ordinary TurnOnly web Stop on a LIVE in-flight turn must cancel the
//     turn but must NOT end that turn's session-owned goal and must NOT
//     land the record terminal. cancel_stop.go::claimCancel's own comment
//     ("Administrative cancellation continues to end session-owned goals")
//     names the ONE case c6bc40804 deliberately changed, and
//     disposeSteeredTurnResult (pkg/agent/steer_launcher.go) early-returns
//     on ts.stopRequested before ever calling
//     completeSteeredTurn/finishSteeredGoalTurn. This test exists so that a
//     fix for case 1 which routes the never-ran case back through the OLD
//     administrative CancelSubtree cascade — satisfying case 1 while
//     silently reintroducing goal-ending on every ordinary live-turn Stop —
//     cannot pass unnoticed.
package gateway

import (
	"context"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRequestScopedStop_NeverRanChildLandsStoppedAndReportsUpward is the RED
// case. See the file-header comment (case 1) for the oracle and the exact
// call chain this exercises.
func TestRequestScopedStop_NeverRanChildLandsStoppedAndReportsUpward(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, adr093IdleProvider{})
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)

	// The production canceller AND the production upward-delivery wiring
	// (gateway_boot.go::wireSteerDeps) — without the real deliverer, a
	// passing assertion on the parent's transcript would prove nothing.
	setGatewaySteerCanceller(al, agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel))
	classifier := agent.NewSteerRecordClassifier(lifecycle, al.GetSessionStore())
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())

	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	const workspaceID = "ws-stop-scope-never-ran"
	require.NoError(t, al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: strPtr(workspaceID)}))
	parent := &session.LifecycleRecord{
		SessionID:      meta.ID,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    workspaceID,
		AgentID:        "mia",
		Origin:         &session.Origin{Kind: session.OriginKindChat},
	}
	require.NoError(t, lifecycle.Persist(parent))

	const callID = "call-never-ran"
	launched, err := agent.NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: meta.ID,
		TargetAgentID:     "mia",
		Task:              "stopped while queued, never runs",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	require.NoError(t, err, "launch a real steered child (the parent must have a child to stop)")
	childID := launched.SessionID

	before, err := lifecycle.Load(childID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleQueued, before.State, "test setup invalid: the child must be queued, never dispatched")

	h := makeMinimalHandler()
	h.agentLoop = al
	h.msgBus = msgBus
	wc, _ := makeForwarderTestConn(32)
	wc.userID = "user-stop-scope"

	// The real web Stop button, scoped to the child directly (TurnOnly, the
	// "this_turn"/"session" default) — the exact call chain handleCancel ->
	// handleCancelWithScope -> requestScopedStop -> StopTurns -> this
	// file's gateway-local stopTurn closure, not al.SteerGenerationCancel.
	h.handleCancel(wc, childID)

	after, err := lifecycle.Load(childID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleStopped, after.State,
		"a steered child that was only ever queued, never ran a turn, must still be landed Stopped by its own web Stop (terminaliseNeverRanStop, pkg/agent/steer_cancel.go) — requestScopedStop's gateway-local cancelTurn closure never reaches it")
	require.NotNil(t, after.StopNote, "the Stop instruction must leave a durable StopNote even though the live fence is spent")
	assert.Equal(t, session.StopCauseStop, after.StopNote.Cause, "the child was the cascade's own direct target, not a cascaded descendant")
	assert.Nil(t, after.Stop, "landing at Stopped spends the current-generation Stop fence (reportSteeredSessionTerminalUpward's Mutate)")

	entries, err := al.GetSessionStore().ReadTranscript(meta.ID)
	require.NoError(t, err)
	wantSpanID := agent.SubagentSpanID(callID, 1)
	var gotEnd *generated.SubagentEndFrame
	for i := range entries {
		if entries[i].SubagentEnd != nil && entries[i].SubagentEnd.SpanId == wantSpanID {
			gotEnd = entries[i].SubagentEnd
		}
	}
	require.NotNil(t, gotEnd, "the never-ran child's Stop must deliver ONE subagent_end upward to the parent transcript (deliverSubagentEnd) — without it the parent never learns the child is gone and waits for ever")
	assert.Equal(t, string(agent.SubTurnStatusInterrupted), gotEnd.Status,
		"deliverSubagentEnd's own outcome switch (steer_frames.go) maps steer.OutcomeInterrupted -> SubTurnStatusInterrupted; terminaliseNeverRanStop's only call site passes steer.OutcomeInterrupted")
}

// newLiveTurnOnlyScopeFixture is u2ScopeFixture's own setup
// (adr093_web_stop_record_test.go::newU2ScopeFixture), trimmed to exactly
// one root session with a live in-flight turn — no descendants. The
// positive control below needs only that: an ordinary TurnOnly Stop on a
// session that is actually running a turn right now.
func newLiveTurnOnlyScopeFixture(t *testing.T) *u2ScopeFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "u2-scope-provider"}},
			List:     []config.AgentConfig{{ID: "mia", Home: home}},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: 3, MaxDelegationDepth: 2},
		Sandbox:     config.OmnipusSandboxConfig{Mode: config.SandboxModeOff},
	}
	ensureTestWorkspaceMembership(t, cfg)
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	p := &u2ScopeProvider{entered: make(chan context.Context, 8), release: make(chan struct{})}
	al, err := agent.NewAgentLoop(cfg, msgBus, p)
	require.NoError(t, err)
	var closeOnce sync.Once
	closeLoop := func() { closeOnce.Do(al.Close) }
	t.Cleanup(closeLoop)
	t.Cleanup(func() { close(p.release) })
	lifecycle := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lifecycle)
	setGatewaySteerCanceller(al, agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel))
	t.Cleanup(func() { gatewaySteerCancellers.Delete(al) })
	classifier := agent.NewSteerRecordClassifier(lifecycle, al.GetSessionStore())
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())

	h := makeMinimalHandler()
	h.agentLoop, h.msgBus = al, msgBus
	wc, _ := makeForwarderTestConn(32)
	wc.userID = "u2-scope-user"
	f := &u2ScopeFixture{
		al: al, lifecycle: lifecycle, provider: p, closeLoop: closeLoop,
		reader:   &wsHandlerReadLoop{h: h, wc: wc, ctx: context.Background(), chatID: "live-turn-only-chat"},
		contexts: make(map[string]context.Context), before: make(map[string]*session.LifecycleRecord),
	}
	f.parent = f.startRoot(t)
	f.before[f.parent], err = lifecycle.Load(f.parent)
	require.NoError(t, err)
	return f
}

// TestRequestScopedStop_LiveTurnStopDoesNotEndGoalOrLandTerminal is the
// positive control. See the file-header comment (case 2) for the oracle.
func TestRequestScopedStop_LiveTurnStopDoesNotEndGoalOrLandTerminal(t *testing.T) {
	f := newLiveTurnOnlyScopeFixture(t)
	gstore := goal.NewStore(config.OmnipusHomeDir())
	before := f.seedActiveGoal(t, gstore, f.parent)

	f.cancelFrame(t, nil) // omitted scope => "this_turn": TurnOnly, no subtree.

	require.Equal(t, context.Canceled, f.contexts[f.parent].Err(), "the live turn must actually be cancelled by the Stop")

	for _, join := range f.joinScheduled {
		join()
	}
	f.closeLoop()

	after, err := gstore.Get(before.GoalID)
	require.NoError(t, err, "an ordinary TurnOnly web Stop must not remove the session-owned goal")
	assert.Equal(t, generated.GoalStateActive, after.State,
		"c6bc40804: an ordinary TurnOnly web Stop ends the turn, not the goal — the goal must stay Active")
	assert.Equal(t, before, after, "the entire active goal record must be unchanged by an ordinary TurnOnly Stop")

	rec, err := f.lifecycle.Load(f.parent)
	require.NoError(t, err)
	assert.NotEqual(t, session.LifecycleStopped, rec.State,
		"c6bc40804: an ordinary TurnOnly Stop on a live turn must not land the record terminal")
}
