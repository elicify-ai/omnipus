package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R5: these literal expectations come from the founder's self-start decision.
// The provider only asks for the tool; the real registered tool, real loop,
// activation engine, goal store, transcript and event publisher do all work.
const (
	ssgDefinition = "Produce an independently reviewed implementation report."
	ssgCriterion  = "The implementation report contains independent review evidence."
	ssgDoD        = "Every reported result cites its evidence."
	ssgCallID     = "self-start-set-goal"
)

func ssgArgs() map[string]any {
	return map[string]any{
		"definition": ssgDefinition,
		"criteria":   []any{map[string]any{"text": ssgCriterion, "judgment": "boolean"}},
		"dod":        []any{map[string]any{"text": ssgDoD, "judgment": "boolean", "provenance": "stated"}},
	}
}

type ssgProvider struct {
	mu       sync.Mutex
	args     map[string]any
	calls    int
	toolText string
}

func (*ssgProvider) GetDefaultModel() string { return "self-start-contract-provider" }

func (p *ssgProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls == 1 {
		return &providers.LLMResponse{FinishReason: "tool_calls", ToolCalls: []providers.ToolCall{{
			ID: ssgCallID, Name: "set_goal", Arguments: p.args,
		}}}, nil
	}
	for _, message := range messages {
		if message.Role == "tool" && message.ToolCallID == ssgCallID {
			p.toolText = message.Content
		}
	}
	return &providers.LLMResponse{Content: "The goal record has been processed."}, nil
}

type ssgEvents struct {
	mu     sync.Mutex
	frames []GoalStatusChangedPayload
}

func ssgObserve(t *testing.T, al *AgentLoop) *ssgEvents {
	t.Helper()
	capture := &ssgEvents{}
	al.SetEventSyncTap(func(event Event) {
		if event.Kind != EventKindGoalStatusChanged {
			return
		}
		if payload, ok := event.Payload.(GoalStatusChangedPayload); ok {
			capture.mu.Lock()
			capture.frames = append(capture.frames, payload)
			capture.mu.Unlock()
		}
	})
	t.Cleanup(func() { al.SetEventSyncTap(nil) })
	return capture
}

func (e *ssgEvents) snapshot() []GoalStatusChangedPayload {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]GoalStatusChangedPayload(nil), e.frames...)
}

func ssgExecuteTurn(t *testing.T, h *grHarness) (*ssgProvider, []GoalStatusChangedPayload) {
	t.Helper()
	inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	wired, ok := inst.Tools.Get("set_goal")
	if !ok || wired == nil {
		t.Fatal("BLOCKED: real wired set_goal not implemented — required by R5")
	}
	p := &ssgProvider{args: ssgArgs()}
	inst.Provider = p
	events := ssgObserve(t, h.al)
	msg := h.humanInput(t, "Choose a goal for this work and register it yourself.")
	reply, _, err := h.al.processMessage(h.ctx, msg)
	require.NoError(t, err, "the real owner turn must execute its tool request")
	assert.Equal(t, "The goal record has been processed.", reply)
	assert.Equal(t, 2, p.calls, "one real tool call followed by one final model answer")
	return p, events.snapshot()
}

// This is a test observation of the existing result, not a new wire type.
type ssgPayload struct {
	Mode          string                     `json:"mode"`
	GoalID        string                     `json:"goal_id"`
	Definition    string                     `json:"definition"`
	CriteriaCount int                        `json:"criteria_count"`
	DoDCount      int                        `json:"dod_count"`
	Criteria      []task.AcceptanceCriterion `json:"criteria"`
	DoD           []task.AcceptanceCriterion `json:"dod"`
}

func ssgReadPayload(t *testing.T, text string) ssgPayload {
	t.Helper()
	var payload ssgPayload
	require.NoError(t, json.Unmarshal([]byte(text), &payload), "R5: successful set_goal must return its normal structured card payload, not a no-active-goal refusal: %s", text)
	assert.Equal(t, ssgDefinition, payload.Definition)
	assert.Equal(t, 1, payload.CriteriaCount)
	assert.Equal(t, 1, payload.DoDCount)
	require.Len(t, payload.Criteria, 1)
	require.Len(t, payload.DoD, 1)
	assert.Equal(t, ssgCriterion, payload.Criteria[0].Text)
	assert.Equal(t, ssgDoD, payload.DoD[0].Text)
	assert.Equal(t, task.JudgmentBoolean, payload.Criteria[0].Judgment)
	assert.Equal(t, task.ProvenanceStated, payload.DoD[0].Provenance)
	return payload
}

func TestSelfStartedGoalContract(t *testing.T) {
	t.Run("NoActiveGoalRealWiredSetGoalActivatesAndRegisters", ssgNoActiveGoal)
	t.Run("SelfStartEmitsNormalGoalStatusPillAndPersistedGoalCard", ssgPillAndCard)
	t.Run("ExistingActiveGoalKeepsItsIdentityAndOrdinaryUpdatePath", ssgExistingGoal)
	t.Run("InvalidInputNeverActivatesAGoal", ssgInvalidInputs)
	t.Run("DelegatedAndMissingSessionGuardsRemain", ssgScopeGuards)
	t.Run("RealGoalReadFailureDoesNotBecomeAnActivation", ssgReadFailure)
}

func ssgNoActiveGoal(t *testing.T) {
	h := newGRHarness(t)
	h.completedRoot(t)
	store := goal.NewStore(config.OmnipusHomeDir())
	before, err := store.ListActive()
	require.NoError(t, err)
	require.Empty(t, before, "SETUP: no active goal before the real set_goal call")
	p, _ := ssgExecuteTurn(t, h)
	active, err := goal.NewStore(config.OmnipusHomeDir()).ListActive()
	require.NoError(t, err)
	require.Len(t, active, 1, "R5: the agent's real wired set_goal must activate one goal when none exists; tool result: %s", p.toolText)
	g := active[0]
	assert.Equal(t, generated.GoalStateActive, g.State)
	assert.Equal(t, generated.GoalOwnerKindSession, g.OwnerKind)
	assert.Equal(t, h.id, g.OwnerID)
	assert.Equal(t, h.id, g.ActiveSessionID)
	assert.Equal(t, ssgDefinition, g.Definition)
	assert.Equal(t, ssgDefinition, g.Prompt, "self-start intent comes from the submitted goal definition")
	require.NotNil(t, g.StartedAt)
	assert.False(t, g.StartedAt.IsZero())
	payload := ssgReadPayload(t, p.toolText)
	assert.Equal(t, "register", payload.Mode)
	assert.Equal(t, g.GoalID, payload.GoalID, "R5: result must reread the freshly activated ID, not retain the old empty ID")
	assert.Equal(t, payload.Criteria, g.Criteria)
	assert.Equal(t, payload.DoD, g.DoD)
	assert.Equal(t, 0, g.Round, "self-start must not consume a Judge round")
}

func ssgPillAndCard(t *testing.T) {
	h := newGRHarness(t)
	h.completedRoot(t)
	p, frames := ssgExecuteTurn(t, h)
	assert.NotEmpty(t, frames, "R5: self-start must emit normal goal_status events for the existing pill consumer")
	var registered []GoalStatusChangedPayload
	for _, frame := range frames {
		assert.Equal(t, h.id, frame.SessionID, "goal_status must target the actual owner chat")
		assert.NotEmpty(t, frame.GoalID, "goal_status must carry a stable activated goal ID")
		assert.Equal(t, "active", frame.State, "self-start is instant activation, never a queued/confirm state")
		if frame.Definition == ssgDefinition && len(frame.Criteria) == 1 && len(frame.DoD) == 1 {
			registered = append(registered, frame)
		}
	}
	assert.NotEmpty(t, registered, "R5: at least one ordinary active status must carry the registered criteria and DoD")
	var cards []session.ToolCall
	for _, entry := range h.visibleHistory(t) {
		for _, call := range entry.ToolCalls {
			if call.Tool == "set_goal" {
				cards = append(cards, call)
			}
		}
	}
	require.Len(t, cards, 1, "one real set_goal invocation must produce one durable card, not a duplicate engine anchor")
	assert.Equal(t, ssgCallID, string(cards[0].ID))
	assert.Equal(t, "success", cards[0].Status, "R5: self-start card must record successful registration, not the old refusal")
	text, ok := cards[0].Result["text"].(string)
	require.True(t, ok, "normal card uses the existing replay result envelope")
	assert.Equal(t, p.toolText, text, "live tool result and durable card must describe the same goal")
	payload := ssgReadPayload(t, text)
	for _, frame := range frames {
		assert.Equal(t, payload.GoalID, frame.GoalID, "pill and card must reference the same new goal")
	}
}

func ssgExistingGoal(t *testing.T) {
	h := newGRHarness(t)
	h.completedRoot(t)
	h.arm(t)
	before := goalRecordForSession(t, h.id)
	p, frames := ssgExecuteTurn(t, h)
	after := goalRecordForSession(t, h.id)
	assert.Equal(t, before.GoalID, after.GoalID, "R5: an existing goal is updated through the ordinary path, never reactivated")
	assert.Equal(t, before.StartedAt, after.StartedAt)
	assert.Equal(t, before.Prompt, after.Prompt)
	assert.Equal(t, before.OwnerID, after.OwnerID)
	assert.Equal(t, before.Source, after.Source)
	assert.Equal(t, ssgDefinition, after.Definition)
	payload := ssgReadPayload(t, p.toolText)
	assert.Equal(t, "update", payload.Mode, "existing register normalizes to the existing update behavior")
	assert.Equal(t, before.GoalID, payload.GoalID)
	active, err := resolveGoalRecordStore().ListActive()
	require.NoError(t, err)
	assert.Len(t, active, 1)
	for _, frame := range frames {
		assert.Equal(t, before.GoalID, frame.GoalID)
	}
}

func ssgToolContext(h *grHarness) context.Context {
	ctx := tools.WithTranscriptSessionID(context.Background(), h.id)
	return tools.WithAgentID(ctx, testDefaultAgentID)
}

func ssgInvalidInputs(t *testing.T) {
	cases := []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"MissingDefinition", func(args map[string]any) { delete(args, "definition") }, "definition is required"},
		{"EmptyCriteria", func(args map[string]any) { args["criteria"] = []any{} }, "criteria is required, at least one"},
		{"MissingJudgment", func(args map[string]any) { args["criteria"] = []any{map[string]any{"text": ssgCriterion}} }, "judgment is required"},
		{"InvalidMode", func(args map[string]any) { args["mode"] = "activate-anyway" }, "mode \"activate-anyway\" is not \"register\" or \"update\""},
		{"MissingDoDProvenance", func(args map[string]any) { args["dod"] = []any{map[string]any{"text": ssgDoD, "judgment": "boolean"}} }, "provenance is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newGRHarness(t)
			inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
			require.True(t, ok)
			tool, ok := inst.Tools.Get("set_goal")
			require.True(t, ok)
			events := ssgObserve(t, h.al)
			args := ssgArgs()
			tc.edit(args)
			result := tool.Execute(ssgToolContext(h), args)
			require.NotNil(t, result)
			assert.True(t, result.IsError)
			assert.Contains(t, result.ForLLM, tc.want, "R5: absence of a goal is no longer a scope refusal; return the actual invalid-input diagnostic")
			active, err := resolveGoalRecordStore().ListActive()
			require.NoError(t, err)
			assert.Empty(t, active, "R5: validate completely before activation, not create-and-then-reject")
			assert.Empty(t, events.snapshot(), "rejected input must not emit a false active pill")
		})
	}
}

func ssgScopeGuards(t *testing.T) {
	h := newGRHarness(t)
	inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	tool, ok := inst.Tools.Get("set_goal")
	require.True(t, ok)
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"Delegated", tools.WithDelegationDepth(ssgToolContext(h), 1), "owner-session-only"},
		{"NoSession", tools.WithAgentID(context.Background(), testDefaultAgentID), "no session context"},
		{"NoAgentIdentity", tools.WithTranscriptSessionID(context.Background(), h.id), "no calling-agent identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.Execute(tc.ctx, ssgArgs())
			require.NotNil(t, result)
			assert.True(t, result.IsError)
			assert.Contains(t, result.ForLLM, tc.want)
			active, err := resolveGoalRecordStore().ListActive()
			require.NoError(t, err)
			assert.Empty(t, active)
		})
	}
}

func ssgReadFailure(t *testing.T) {
	h := newGRHarness(t)
	h.arm(t)
	before := goalRecordForSession(t, h.id)
	inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	tool, ok := inst.Tools.Get("set_goal")
	require.True(t, ok)
	restore, cause := failGoalRecordListReads(t, goal.NewStore(config.OmnipusHomeDir()))
	result := tool.Execute(ssgToolContext(h), ssgArgs())
	restore()
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "could not read this session's goal state")
	var returned *os.PathError
	assert.True(t, errors.As(result.Err, &returned))
	assert.ErrorIs(t, result.Err, cause.Err)
	assert.Equal(t, before, goalRecordForSession(t, h.id), "R5: a genuine read failure must never be treated as no goal or replace the existing goal")
}
