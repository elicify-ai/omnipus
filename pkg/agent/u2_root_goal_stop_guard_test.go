// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// u2_root_goal_stop_guard_test.go — CHECK round 2 finding 1 (HIGH,
// coordination/logs/fix890-opus/u2-check2/37-U2-CHECK-AUDIT.md): the committed
// U2 pack's goal-preservation subtest
// (pkg/gateway/adr093_web_stop_record_test.go::TestU2CancelFrameScope's
// "stop_all_keeps_parent_and_every_helper_goal_unchanged") starts its fixture
// turns via ProcessScheduled, which structurally sets UserInitiated=false
// (loop_inbound.go::userInitiated's own doc comment) and SenderID="" — so
// checkEligibility's origin gate (goal_loop.go:704) already rejects that
// fixture's turns BEFORE the stopped-result guard (goal_loop.go:681,
// `gl.result.stopped`) is ever reached. Removing the stopped-result guard
// alone therefore still passed the committed U2 test for the wrong reason:
// no otherwise-eligible turn ever exercised it.
//
// This test drives a REAL user-chat-origin turn (bus.InboundMessage.
// UserInitiated=true, exactly what the gateway webchat WS handler sets —
// websocket_chat.go::wsHandlerHandleChatMessage.buildInboundMessage) through
// the real al.processMessage entry point, stops it through the real
// production Stop path (al.RequestCancelForSession, the same adapter
// TestKeeperStopPause_RequestCancelPausesTheSession already exercises against
// a genuinely live turn), and lets the REAL turnResult propagate into
// checkGoalLoopAfterTurn — no manually-constructed turnResult{stopped: true}
// (that shortcut is exactly what CHECK's report calls "oracle-contaminated
// corroboration" and excludes from certifying the committed pack).
//
// Oracle: docs/internal/architecture/ADR-093-open-conversation-must-keep-
// delegation.md / GOAL-FR-013 — a Stop is a pause, not an ending; the active
// goal's claim and activity clock must be untouched by a turn that ended only
// because it was stopped.
package agent

import (
	"context"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// u2RootGoalStopGuardProvider blocks inside Chat until the test releases it,
// then returns a normal (non-tool-calling) completion — modelling a graceful
// Stop that lets the CURRENT LLM round finish naturally and quickly, well
// inside the multi-second hard-abort escalation window, rather than the
// turn being force-aborted. finalContent deliberately carries no
// GOAL_STATUS marker: this is an ORDINARY worker turn, the one
// checkGoalLoopAfterTurn's default branch (bumpGoalActivityOnTurn) would
// otherwise advance.
type u2RootGoalStopGuardProvider struct {
	entered chan struct{}
	release chan struct{}
}

func (p *u2RootGoalStopGuardProvider) Chat(
	context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any,
) (*providers.LLMResponse, error) {
	close(p.entered)
	<-p.release
	return &providers.LLMResponse{Content: "Stopping here as asked.", ToolCalls: nil}, nil
}

func (*u2RootGoalStopGuardProvider) GetDefaultModel() string { return "u2-root-goal-stop-guard" }

// TestU2RootGoalStopGuard_UserInitiatedStopLeavesGoalUnchanged proves CHECK
// round 2 finding 1: an otherwise goal-update-eligible, user-chat-origin turn
// that is stopped mid-flight must leave its session's bound active goal
// completely unchanged — no claim, no activity bump, no state change.
func TestU2RootGoalStopGuard_UserInitiatedStopLeavesGoalUnchanged(t *testing.T) {
	provider := &u2RootGoalStopGuardProvider{entered: make(chan struct{}), release: make(chan struct{})}
	al, agentInst := newGoalLoopTestLoop(t, provider, nil)
	_, sid := newGoalTestSession(t, al, agentInst.ID)

	// A real, session-owned active goal with non-empty Criteria/DoD — the
	// same construction adr093_web_stop_record_test.go::seedActiveGoal uses —
	// so an ordinary (guard-bypassed) turn falls straight into
	// checkGoalLoopAfterTurn's default branch (bumpGoalActivityOnTurn) rather
	// than also tripping maybeNudgeUnregisteredGoal's separate
	// recordless-goal path (which only fires when Criteria is empty).
	criteria := []task.AcceptanceCriterion{{
		ID: "u2-report-ready", Kind: task.KindProse, Judgment: task.JudgmentBoolean,
		Text: "The report is delivered", Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "u2-root-goal-user"},
	}}
	dod := []task.AcceptanceCriterion{{
		ID: "u2-report-safe", Kind: task.KindProse, Judgment: task.JudgmentBoolean, Provenance: task.ProvenanceFloor,
		Text: "No secrets are leaked", Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: agentInst.ID},
	}}
	g, err := goal.New(generated.GoalOwnerKindSession, sid, generated.GoalSourceChatCompiled,
		"Deliver the report", "Deliver the report", criteria, dod, 1, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, g.Activate(sid, time.Now().UTC()))
	gs := goal.NewStore(config.OmnipusHomeDir())
	require.NoError(t, gs.Create(g))
	before, err := gs.Get(g.GoalID)
	require.NoError(t, err)
	require.Equal(t, generated.GoalStateActive, before.State)
	require.Nil(t, before.LatestClaim, "fixture must start with no claim")

	// Start the real, user-chat-origin turn — the msg-based path
	// loop_inbound.go::userInitiated actually reads (bus.InboundMessage.
	// UserInitiated), unlike ProcessScheduled's directly-built processOptions.
	turnDone := make(chan struct{})
	var turnErr error
	go func() {
		defer close(turnDone)
		_, _, turnErr = al.processMessage(context.Background(), bus.InboundMessage{
			Channel:       "webchat",
			Sender:        bus.SenderInfo{CanonicalID: "webchat_user"},
			ChatID:        sid,
			Content:       "please write the report",
			SessionID:     sid,
			GatewayUserID: "u2-root-goal-user",
			UserInitiated: true,
			// Explicit agent_id metadata — exactly what websocket_chat.go::
			// wsHandlerHandleChatMessage.buildInboundMessage stamps when the
			// client names an agent — so resolveMessageRoute has a target
			// without depending on channel-binding/default-agent config this
			// minimal harness does not set up.
			Metadata: map[string]string{"agent_id": agentInst.ID},
		})
	}()

	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("BLOCKED: the real user-chat turn never entered the provider")
	}

	// The real production web-Stop path — CancelScope{TurnOnly: true} is
	// exactly what pkg/gateway/websocket_stop_scope.go::
	// (*WSHandler).requestTurnStop builds for a real web Stop click
	// (non-terminal: "never follows routing-root identities or changes the
	// session's goal lifecycle" per CancelScope.TurnOnly's own doc comment).
	// RequestCancelForSession's plain CancelScope{SessionID: sid} (TurnOnly
	// false) is the ADMINISTRATIVE cancel shape instead, which does not set
	// turnState.stopRequested — confirmed empirically: a baseline run using
	// RequestCancelForSession here produced a turn with result.stopped=false,
	// so checkGoalLoopAfterTurn (correctly) advanced the goal even on
	// unmodified production code, a false positive for this test's purpose.
	outcome, cancelErr := al.RequestCancel(context.Background(),
		CancelScope{SessionID: sid, TurnOnly: true},
		CancelCanceller{UserID: "u2-root-goal-user", Channel: "web"},
		CancelHooks{})
	require.NoError(t, cancelErr)
	require.True(t, outcome.Fired, "RequestCancel must fire against the live turn")

	close(provider.release)
	select {
	case <-turnDone:
	case <-time.After(5 * time.Second):
		t.Fatal("BLOCKED: the stopped turn never finished")
	}
	require.NoError(t, turnErr)

	after, err := gs.Get(g.GoalID)
	require.NoError(t, err)
	assert.Equal(t, before, after,
		"a stopped user-chat-origin turn must not advance the goal loop — the whole active goal record must be unchanged")
}
