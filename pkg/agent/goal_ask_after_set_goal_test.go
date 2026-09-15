// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for the same-response set_goal + AskUserQuestion
// refusal (loop.go, the setGoalSucceededThisRound gate in runTurn's tool
// loop).
//
// UAT B-9 run 4 (2026-09-14): on a narrowed goal turn the model's ONE
// response carried a valid set_goal AND an invented AskUserQuestion
// ("Placeholder question - not used"). Both executed; the ask parked the
// turn for 18 minutes even though the goal record was already registered.
// ADR-088 D4's rubric note makes the two doors alternatives ("Call exactly
// ONE of the two, never both in the same response") — this enforces it at
// dispatch.
package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestGoalTurn_SetGoalAndAskInOneResponse_RefusesTheAsk(t *testing.T) {
	// offerGateCaptureProvider (tool_offer_gate_test.go): scripted responses
	// in order, every request's messages recorded.
	provider := &offerGateCaptureProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{
			scriptedToolCall("call_goal", tools.SetGoalToolName,
				`{"definition":"Plan a weekly schedule","criteria":[`+
					`{"text":"a schedule exists","judgment":"boolean"}],`+
					`"assessment":{"clarity":"clear"}}`),
			scriptedToolCall("call_ask", tools.AskUserQuestionToolName,
				`{"questions":[{"header":"Placeholder","question":"Placeholder question - not used",`+
					`"options":[{"label":"A","description":"a"},{"label":"B","description":"b"}]}]}`),
		}},
		{Content: "working on it now"},
	}}
	al, _ := newGoalLoopTestLoop(t, provider, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	require.True(t, ok, "native-agent not registered")
	allowGoalToolsPolicy(agentInst)
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-ask-after-set-goal-1", "plan my week")

	// Wire the REAL pending-question registry so a genuine AskUserQuestion
	// would take its success path (durable card + ParksTurn) — without this
	// the tool fails closed and the test could not tell a refusal from a
	// no-registry error.
	reg := newTestAskUserRegistry(t, store)
	al.SetAskUserRegistry(reg)

	opts := processOptions{
		SessionKey: "goal-ask-after-set-goal-session", Channel: "webchat", ChatID: "c1",
		UserMessage:     "plan my week",
		TranscriptStore: store, TranscriptSessionID: sid,
		DefaultResponse: "done", UserInitiated: true,
	}
	ts := newTurnState(agentInst, opts, al.newTurnEventScope(agentInst.ID, opts.SessionKey))

	result, err := al.runTurn(context.Background(), ts)
	require.NoError(t, err)
	assert.NotEqual(t, TurnEndStatusParked, result.status,
		"a placeholder ask trailing a successful set_goal must never park the turn (UAT B-9 run 4 froze 18 minutes)")
	assert.Equal(t, "working on it now", result.finalContent,
		"the turn must continue to its normal final response")

	rec := mustActiveGoalRecord(t, sid)
	require.NotEmpty(t, rec.Criteria, "the set_goal call itself must still succeed and register the record")
	assert.Equal(t, 0, rec.QuestionRoundsUsed,
		"the refused ask must not spend the FR-010 question-round budget")

	secondMsgs, _, ok := provider.request(1)
	require.True(t, ok, "the turn must reach a second provider request after the refusal")
	askResult, found := toolResultFor(secondMsgs, "call_ask")
	require.True(t, found, "the refused ask must have a paired tool result")
	assert.Contains(t, askResult, "AskUserQuestion was not called",
		"the refusal result must say the ask was not called")
	assert.Contains(t, askResult, "already registered the goal record with set_goal",
		"the refusal result must name the successful set_goal as the reason")
	assert.Contains(t, askResult, "never a placeholder",
		"the refusal result must forbid placeholder questions")
}

func TestGoalTurn_AskAloneStillParks(t *testing.T) {
	// Control for the refusal above: an AskUserQuestion WITHOUT a same-response
	// set_goal keeps its genuine success path (park + budget bump) — the gate
	// must not have broken the real ask door.
	provider := &offerGateCaptureProvider{responses: []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{
			scriptedToolCall("call_ask", tools.AskUserQuestionToolName,
				`{"questions":[{"header":"Scope","question":"Single or multiplayer?",`+
					`"options":[{"label":"Single","description":"one player"},{"label":"Multi","description":"two players"}]}]}`),
		}},
	}}
	al, _ := newGoalLoopTestLoop(t, provider, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	require.True(t, ok, "native-agent not registered")
	allowGoalToolsPolicy(agentInst)
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-ask-alone-1", "build a game")
	al.SetAskUserRegistry(newTestAskUserRegistry(t, store))

	opts := processOptions{
		SessionKey: "goal-ask-alone-session", Channel: "webchat", ChatID: "c1",
		UserMessage:     "build a game",
		TranscriptStore: store, TranscriptSessionID: sid,
		DefaultResponse: "done", UserInitiated: true,
	}
	ts := newTurnState(agentInst, opts, al.newTurnEventScope(agentInst.ID, opts.SessionKey))

	result, err := al.runTurn(context.Background(), ts)
	require.NoError(t, err)
	assert.Equal(t, TurnEndStatusParked, result.status,
		"a genuine ask with no same-response set_goal must still park the turn")
	assert.Equal(t, 1, mustActiveGoalRecord(t, sid).QuestionRoundsUsed,
		"a genuine ask must still spend the question-round budget")
}
