// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Oracles: Task U3 RED cases 1–5 (Q20, Q21=A),
// contracts/components/schemas/DelegateClearGoalAction.yaml (no cascade), and
// contracts/components/schemas/Goal.yaml (cleared is a retained terminal record).
// Only the external model provider is scripted. The registered delegate tool,
// launch/ownership machinery, goal store and session-scoped injection are real.
func TestDelegateClearGoal(t *testing.T) {
	t.Run("clears_helper_goal", testDelegateClearGoalClearsHelper)
	t.Run("leaves_subhelper_goal_untouched", testDelegateClearGoalNoCascade)
	t.Run("injects_helper_decision_prompt", testDelegateClearGoalInjectsPrompt)
	t.Run("allows_working_helper", testDelegateClearGoalWhileWorking)
	t.Run("repeats_one_level_down", testDelegateClearGoalOneLevelDown)
	t.Run("rejects_unauthorized_agent_without_side_effects", testDelegateClearGoalUnauthorizedAgent)
}

type delegateClearGoalHarness struct {
	al       *AgentLoop
	parentID string
	helper   *session.LifecycleRecord
}

func newDelegateClearGoalHarness(t *testing.T) *delegateClearGoalHarness {
	t.Helper()
	// The goal entity store resolves this environment variable per call;
	// isolate it before any launcher creates a goal, and restore after Close.
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t) // Registers Close after its temporary stores exist.
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-clear-goal")
	helper := launchGoalBearingChild(t, al, parentID, "call-clear-goal-helper")
	require.Equal(t, generated.GoalStateActive, mustGoalRecord(t, helper.GoalRef).State,
		"SETUP: the helper must have an open goal")
	return &delegateClearGoalHarness{al: al, parentID: parentID, helper: helper}
}

func (h *delegateClearGoalHarness) clear(t *testing.T, callerID, targetID, specRef string) {
	t.Helper()
	ctx := tools.WithAgentID(
		tools.WithTranscriptSessionID(context.Background(), callerID), testDefaultAgentID)
	result := delegateToolFor(t, h.al).Execute(ctx, map[string]any{
		"action": "clear_goal", "session_id": targetID,
	}) // Contract has no cascade flag; never add one to the test input.
	require.NotNil(t, result, "%s: delegate(clear_goal) must return a result", specRef)
	if result.IsError && strings.Contains(result.ForLLM, `invalid action "clear_goal"`) {
		t.Fatalf("BLOCKED: delegate(clear_goal) dispatch not implemented — required by %s; got %q",
			specRef, result.ForLLM)
	}
	require.False(t, result.IsError, "%s: clearing this helper's goal must succeed, got %q",
		specRef, result.ForLLM)
}

func assertDelegateGoalCleared(t *testing.T, before *goal.Goal) {
	t.Helper()
	after := mustGoalRecord(t, before.GoalID)
	require.Equal(t, generated.GoalStateCleared, after.State,
		"the addressed helper's persisted goal must be cleared, not active or deleted")
	require.Equal(t, before.GoalID, after.GoalID, "clearing retains the goal's identity")
	require.Equal(t, before.OwnerKind, after.OwnerKind, "clearing must not replace the goal's owner kind")
	require.Equal(t, before.OwnerID, after.OwnerID, "clearing must not replace the goal's owner")
	require.Equal(t, before.ActiveSessionID, after.ActiveSessionID,
		"the retained terminal record must still name the session that carried it")
	require.Equal(t, before.Criteria, after.Criteria, "clearing retains the outcome criteria")
	require.Equal(t, before.DoD, after.DoD, "clearing retains the definition of done")
	active, activeErr := activeGoalForSession(before.ActiveSessionID)
	require.NoError(t, activeErr, "activeGoalForSession(after clear) must not fail")
	require.Nil(t, active,
		"the helper must no longer have an active goal for the goal loop to continue")
}

func testDelegateClearGoalClearsHelper(t *testing.T) {
	h := newDelegateClearGoalHarness(t)
	before := mustGoalRecord(t, h.helper.GoalRef)
	require.Nil(t, h.al.getActiveTurnState(h.helper.SessionID), "SETUP: this helper is idle")

	h.clear(t, h.parentID, h.helper.SessionID, "U3 case 1")

	assertDelegateGoalCleared(t, before)
}

func testDelegateClearGoalNoCascade(t *testing.T) {
	h := newDelegateClearGoalHarness(t)
	subhelper := launchGoalBearingChild(t, h.al, h.helper.SessionID, "call-clear-goal-subhelper")
	helperBefore := mustGoalRecord(t, h.helper.GoalRef)
	subhelperBefore := mustGoalRecord(t, subhelper.GoalRef)
	require.Equal(t, generated.GoalStateActive, subhelperBefore.State,
		"SETUP: the subhelper must have its own open goal")

	h.clear(t, h.parentID, h.helper.SessionID, "U3 case 2 / no cascade")

	assertDelegateGoalCleared(t, helperBefore)
	require.Equal(t, subhelperBefore, mustGoalRecord(t, subhelper.GoalRef),
		"clearing the helper must leave the ENTIRE subhelper goal record untouched")
}

// startDelegateGoalClearTurn holds a real dispatched helper inside its first
// provider call. A scripted echo call supplies a deterministic next tool
// boundary, reusing steer_delegated_injection_test.go's fixture pattern.
// The callback has no testing assertions: failures belong to the test goroutine.
func startDelegateGoalClearTurn(t *testing.T, al *AgentLoop, rec *session.LifecycleRecord) (
	*truncationScriptedProvider, *turnState, func(),
) {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok, "SETUP: the helper's agent must be registered")
	registerTruncationEchoTool(al, inst)
	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(resume) }) }
	t.Cleanup(release) // Release before newSteerAL's Close joins live turns.
	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{
			toolCalls:    []providers.ToolCall{truncationEchoCall("call-clear-goal-echo", "boundary")},
			finishReason: "stop",
			onCall:       func() { close(entered); <-resume },
		},
		{content: "The instruction has been considered.", finishReason: "stop"},
	}}
	inst.Provider = provider
	dispatched, err := NewSteerLauncher(al).Dispatch(context.Background(), rec.SessionID, rec.Generation)
	require.NoError(t, err, "SETUP: dispatch the goal-bearing helper")
	require.Equal(t, steer.DispatchRunning, dispatched.State)
	// This is a bounded synchronization guard, not an elapsed-time oracle;
	// the existing delegated-injection test uses the same 10-second bound.
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("SETUP: the helper never entered its provider call")
	}
	ts := al.getActiveTurnState(rec.SessionID)
	require.NotNil(t, ts, "SETUP: the helper must have a registered live turn")
	require.True(t, ts.IsAlive(), "SETUP: the helper is WORKING, not merely stamped running")
	return provider, ts, release
}

// decisionPrompts matches only injected user-role messages, never the agent's
// system instructions. U3 specifies meaning, not a literal full sentence: the
// notice must say the goal is cleared and tell the helper to decide about its
// helpers. These concepts come from the brief, not observed implementation text.
func delegateGoalClearDecisionPrompts(messages []providers.Message) []providers.Message {
	var notices []providers.Message
	for _, message := range messages {
		text := strings.ToLower(message.Content)
		if message.Role == "user" && strings.Contains(text, "goal") &&
			strings.Contains(text, "clear") && strings.Contains(text, "decide") &&
			strings.Contains(text, "helper") {
			notices = append(notices, message)
		}
	}
	return notices
}

func assertDelegateGoalClearPromptInjected(t *testing.T, al *AgentLoop, sub EventSubscription,
	targetID string, provider *truncationScriptedProvider, ts *turnState, release func(),
) {
	t.Helper()
	before := provider.Requests()
	require.Len(t, before, 1, "SETUP: the cleared helper is held in its first round")
	require.Empty(t, delegateGoalClearDecisionPrompts(before[0]),
		"SETUP: a pre-existing prompt must not accidentally satisfy the delivery assertion")
	release()
	select {
	case <-ts.Finished():
	case <-time.After(10 * time.Second):
		t.Fatal("the cleared helper did not finish after its provider was released")
	}
	// Assert both the existing transport's CHILD-scoped injection event and
	// the actual request sent to the external model. Enqueue success alone
	// would miss a notice placed in the parent's or a dead scope's queue.
	injected := waitForSteeringInjectedEvent(t, sub.C, targetID, 3*time.Second)
	require.Equal(t, 1, injected.Count, "exactly one clear-goal notice must be injected into the selected helper")
	requests := provider.Requests()
	require.Len(t, requests, 2, "the scripted next tool boundary must reach a real second provider request")
	require.Len(t, delegateGoalClearDecisionPrompts(requests[1]), 1,
		"the helper's actual next-round input must tell it its goal was cleared and to decide about its own helpers")
	assertDelegateGoalClearDecisionMeaning(t, delegateGoalClearDecisionPrompts(requests[1])[0].Content)
}

func testDelegateClearGoalInjectsPrompt(t *testing.T) {
	h := newDelegateClearGoalHarness(t)
	launchGoalBearingChild(t, h.al, h.helper.SessionID, "call-clear-goal-notice-subhelper")
	// Same bounded event buffer as the existing steering-receipt fixture.
	sub := h.al.SubscribeEvents(32)
	defer h.al.UnsubscribeEvents(sub.ID)
	provider, ts, release := startDelegateGoalClearTurn(t, h.al, h.helper)

	h.clear(t, h.parentID, h.helper.SessionID, "U3 case 3 / helper decision prompt")

	assertDelegateGoalClearPromptInjected(t, h.al, sub, h.helper.SessionID, provider, ts, release)
}

func testDelegateClearGoalWhileWorking(t *testing.T) {
	h := newDelegateClearGoalHarness(t)
	before := mustGoalRecord(t, h.helper.GoalRef)
	_, ts, release := startDelegateGoalClearTurn(t, h.al, h.helper)

	h.clear(t, h.parentID, h.helper.SessionID, "U3 case 4 / Q21=A")

	assertDelegateGoalCleared(t, before) // Check while the provider is still held.
	release()
	select {
	case <-ts.Finished():
	case <-time.After(10 * time.Second):
		t.Fatal("the working helper did not finish after its provider was released")
	}
}

func testDelegateClearGoalOneLevelDown(t *testing.T) {
	h := newDelegateClearGoalHarness(t)
	subhelper := launchGoalBearingChild(t, h.al, h.helper.SessionID, "call-clear-goal-recursive-subhelper")
	leaf := launchGoalBearingChild(t, h.al, subhelper.SessionID, "call-clear-goal-recursive-leaf")
	helperBefore := mustGoalRecord(t, h.helper.GoalRef)
	subhelperBefore := mustGoalRecord(t, subhelper.GoalRef)
	leafBefore := mustGoalRecord(t, leaf.GoalRef)
	require.Equal(t, generated.GoalStateActive, leafBefore.State, "SETUP: the leaf must have its own open goal")
	sub := h.al.SubscribeEvents(32)
	defer h.al.UnsubscribeEvents(sub.ID)
	provider, ts, release := startDelegateGoalClearTurn(t, h.al, subhelper)

	h.clear(t, h.parentID, h.helper.SessionID, "U3 case 5 / Q20 first clear")
	assertDelegateGoalCleared(t, helperBefore)
	require.Equal(t, subhelperBefore, mustGoalRecord(t, subhelper.GoalRef),
		"the first clear must not clear the subhelper's goal")
	h.clear(t, h.helper.SessionID, subhelper.SessionID, "U3 case 5 / Q20 later clear one level down")

	assertDelegateGoalCleared(t, subhelperBefore)
	require.Equal(t, leafBefore, mustGoalRecord(t, leaf.GoalRef),
		"the later subhelper clear must not cascade into its own helper's goal either")
	assertDelegateGoalClearPromptInjected(t, h.al, sub, subhelper.SessionID, provider, ts, release)
}

// F2 / Q20: match affirmative choice clauses, not isolated keywords. A notice
// may explain that nothing happens "automatically", but must never command
// unconditional action or negate the helper's independent decision. The oracle
// comes from the brief; it neither imports nor copies the production constant.
func assertDelegateGoalClearDecisionMeaning(t *testing.T, content string) {
	t.Helper()
	text := strings.ToLower(strings.Join(strings.Fields(content), " "))
	require.NotRegexp(t,
		`(?:^|[.!?;]\s+)(?:(?:you|that helper)\s+(?:must|shall)\s+)?(?:stop|clear)\b|\b(?:automatically|unconditionally)\s+(?:stop|clear)\b`,
		text, "Q20: the notice must not order unconditional or automatic stop/clear actions")
	require.NotRegexp(t,
		`\b(?:do not|don't|never|must not)\s+(?:decide|choose)\b|\bwithout\s+(?:deciding|choosing)\b`,
		text, "Q20: the notice must not forbid or bypass the helper's independent decision")
	require.Regexp(t,
		`(?:^|[.!?;]\s+)decide for yourself whether to stop your own helpers and clear their goals(?:[.!?;]|$)`,
		text, "Q20: the addressed helper must affirmatively choose for itself whether to stop/clear its own helpers")
	require.Regexp(t,
		`\b(?:that|the) helper must decide for itself whether to stop its own helpers and clear their goals\b`,
		text, "Q20: a helper cleared later must make its own independent stop/clear choice too")
	require.Regexp(t, `\b(?:each|every) level\b`, text,
		"Q20: independent choice must repeat at every subsequent level, not end after one helper")
}

// F1 / ADR-091 WP-C US-2/AS-3: the same agent profile does not grant a
// sibling or unrelated session an ancestor's authority. Like the steer action's
// authority-before-enqueue test, include an authorized control on the same tool.
func testDelegateClearGoalUnauthorizedAgent(t *testing.T) {
	for _, callerKind := range []string{"sibling", "unrelated_root"} {
		t.Run(callerKind, func(t *testing.T) {
			h := newDelegateClearGoalHarness(t)
			subhelper := launchGoalBearingChild(t, h.al, h.helper.SessionID, "call-clear-goal-denied-subhelper")
			leaf := launchGoalBearingChild(t, h.al, subhelper.SessionID, "call-clear-goal-denied-leaf")
			var callerID string
			if callerKind == "sibling" {
				callerID = launchGoalBearingChild(t, h.al, h.parentID, "call-clear-goal-denied-sibling").SessionID
			} else {
				callerID = newTestSteeringSession(t, h.al, "ws-clear-goal")
			}
			require.NotEqual(t, h.parentID, callerID, "SETUP: the caller must not be the delegating parent")
			protected := []*session.LifecycleRecord{h.helper, subhelper, leaf}
			before := make(map[string]*goal.Goal, len(protected))
			for _, rec := range protected {
				before[rec.GoalRef] = mustGoalRecord(t, rec.GoalRef)
				require.Equal(t, generated.GoalStateActive, before[rec.GoalRef].State,
					"SETUP: every protected session must have an open goal")
				require.Nil(t, h.al.getActiveTurnState(rec.SessionID),
					"SETUP: idle sessions cannot hide an enqueue by draining their queues")
			}
			require.NotNil(t, h.al.steering, "SETUP: inspect the real steering queue")
			require.Zero(t, h.al.steering.len(), "SETUP: no pre-existing notices may hide the denied call's side effects")
			// Use the same event buffer as the existing injection fixture; there
			// are no live turns, and delivery is synchronous before Execute returns.
			sub := h.al.SubscribeEvents(32)
			defer h.al.UnsubscribeEvents(sub.ID)
			require.NotEqual(t, uint64(0), sub.ID, "SETUP: the notice-event subscription must be live")
			require.Zero(t, len(sub.C), "SETUP: observe only this call's events")

			// Supply an AGENT session, never the authenticated-human exception.
			ctx := tools.WithAgentID(
				tools.WithTranscriptSessionID(context.Background(), callerID), testDefaultAgentID)
			result := delegateToolFor(t, h.al).Execute(ctx, map[string]any{
				"action": "clear_goal", "session_id": h.helper.SessionID,
			})
			require.NotNil(t, result, "F1: the unauthorized agent call must return a result")
			assert.True(t, result.IsError, "F1: an unauthorized %s agent must be refused, got %q", callerKind, result.ForLLM)
			assert.Error(t, result.Err, "F1: refusal must retain its underlying authority error")
			for _, rec := range protected {
				assert.Equal(t, before[rec.GoalRef], mustGoalRecord(t, rec.GoalRef),
					"F1: denial must leave the ENTIRE goal record unchanged for session %s", rec.SessionID)
			}
			assert.Zero(t, h.al.steering.len(), "F1: denial must not queue a decision notice in ANY session")
			assert.Zero(t, len(sub.C), "F1: denial must not emit an enqueue or delivery event")

			// Demonstrate that the same goal, queue and event instruments can
			// observe a legitimate ancestor clear, as in the steer authority test.
			h.clear(t, h.parentID, h.helper.SessionID, "F1 authorized ancestor control")
			assertDelegateGoalCleared(t, before[h.helper.GoalRef])
			require.Equal(t, 1, h.al.steering.len(), "CONTROL: the authorized clear must queue exactly one notice")
			require.Equal(t, 1, h.al.steering.lenScope(h.helper.SessionID),
				"CONTROL: the notice must be queued under the addressed helper, not another session")
			require.Positive(t, len(sub.C), "CONTROL: the same subscription must observe the authorized action")
		})
	}
}
