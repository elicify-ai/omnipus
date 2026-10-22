package agent

import (
	"context"
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A1 counts actual failed automatic REMINDER turns, not every failed lifecycle,
// park, publication, or system turn sharing the goal-loop sender.
func TestGoalReminderFailureBrakeExcludesNonReminderOutcomes(t *testing.T) {
	t.Run("RealReceiptFailureBeforeTurnDoesNotCount", r2PreTurnFailureExcluded)
	t.Run("RealQuestionParkHasNoFailedOutcome", r2QuestionParkExcluded)
	t.Run("RealNonReminderSendbackProducerFailureDoesNotCount", r2SendbackFailureExcluded)
}

func r2TwoFailuresAtNextWake(t *testing.T) (*grHarness, *r2FailureProvider, bus.InboundMessage) {
	t.Helper()
	h := grNewRecordedBrake(t)
	provider := r2InstallFailureProvider(t, h)
	grSetStreak(t, h, 2)
	for failure := 0; failure < 2; failure++ {
		r2FailedReminder(t, h, provider, r2ReplyProviderError)
	}
	h.age(t)
	msg := h.reminder(t) // assess the second actual failure; publication is NOT a third failed turn
	require.Nil(t, r6WarnPendingOutcome(h.goalID))
	r2AssertNoFailurePause(t, h)
	return h, provider, msg
}

func r2AssertNoFailurePause(t *testing.T, h *grHarness) {
	t.Helper()
	assert.Empty(t, r2FailurePauseRows(t, h))
	assert.NotEqual(t, r2FailedTurnPause, goalRecordForSession(t, h.id).LatestReason)
	assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes, "excluded outcomes must not spend the separate five-completed-outcome counter")
	assert.Equal(t, h.goalID, goalRecordForSession(t, h.id).GoalID)
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
}

func r2RequireThirdActualFailurePauses(t *testing.T, h *grHarness, provider *r2FailureProvider) {
	t.Helper()
	live := r2FailureNotices(t, h)
	r2FailedReminder(t, h, provider, r2ReplyProviderError)
	for tick := 0; tick < 2; tick++ {
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		r2NoFourthWake(t, h)
		r2AssertFailurePause(t, h, live)
	}
}

func r2PreTurnFailureExcluded(t *testing.T) {
	h, provider, msg := r2TwoFailuresAtNextWake(t)
	before := h.root.count()
	prior := h.load(t)
	r6GateRequireStorageHealth(t, h)
	paths := r6GateFaultReceipt(t, h)
	for _, id := range []string{"r2-a1-preturn-fresh-a", "r2-a1-preturn-fresh-b"} {
		written, err := r6GateProbeAppend(h, id)
		require.ErrorIs(t, err, os.ErrPermission, "SETUP: prove the actual receipt-only permission fault")
		require.False(t, written)
	}
	r6GateRequireStorageHealth(t, h)
	sub := h.al.SubscribeEvents(64)
	require.NotZero(t, sub.ID)
	t.Cleanup(func() { h.al.UnsubscribeEvents(sub.ID) })
	run := h.system(msg)
	t.Cleanup(func() { h.root.openAll(); grJoin(t, run) })
	result := r2JoinPreTurnReceiver(t, h, run)
	var preTurn *ordinaryGoalPreTurnError
	var save *goalReminderChatSaveError
	require.ErrorAs(t, result.err, &preTurn)
	require.ErrorAs(t, result.err, &save)
	require.ErrorIs(t, result.err, os.ErrPermission)
	assert.Equal(t, before, h.root.count(), "a receipt/pre-turn failure must not enter the provider")
	r6GateRequireFailedReceiptIdentity(t, h, prior)
	var liveErrors []ErrorPayload
	for _, event := range collectEventStream(sub.C) {
		payload, ok := event.Payload.(ErrorPayload)
		if event.Kind == EventKindError && ok && payload.SessionID == h.id && payload.Stage == "goal_reminder_admission" {
			liveErrors = append(liveErrors, payload)
		}
	}
	require.Len(t, liveErrors, 1, "the actual pre-turn error must reach this same session, not only a log")
	assert.Contains(t, liveErrors[0].Message, "chat event could not be saved")
	assert.Nil(t, r6WarnPendingOutcome(h.goalID), "a failed admission is not an actual failed reminder-turn outcome")
	r2AssertNoFailurePause(t, h)
	r6GateRestoreReceipt(t, paths)
	written, err := r6GateProbeAppend(h, "r2-a1-preturn-exact-repair")
	require.NoError(t, err)
	require.True(t, written, "SETUP: repair must perform a fresh real append")
	r6GateRequireStorageHealth(t, h)
	for tick := 0; tick < 2; tick++ {
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		r2AssertNoFailurePause(t, h)
		assert.Nil(t, r6WarnPendingOutcome(h.goalID))
	}
	r2RequireThirdActualFailurePauses(t, h, provider)
}

func r2JoinPreTurnReceiver(t *testing.T, h *grHarness, run *grPending) grResult {
	t.Helper()
	watchdog := time.NewTimer(grWatchdog)
	defer watchdog.Stop()
	for {
		select {
		case call := <-h.root.requests:
			// Let an erroneously entered real provider finish, so its actual
			// entry/error assertions fail rather than manufacturing a hang.
			h.root.release(call)
		case <-run.done:
			return run.result
		case <-watchdog.C:
			t.Fatal("SETUP: actual pre-turn receiver neither returned nor reached its external edge")
		}
	}
}

// Only the external provider response is scripted. The existing real tool,
// registry, durable card and ParksTurn handling all execute unchanged.
type r2QuestionProvider struct {
	base  *grProvider
	calls atomic.Int32
}

func (p *r2QuestionProvider) GetDefaultModel() string { return p.base.GetDefaultModel() }
func (p *r2QuestionProvider) Chat(ctx context.Context, messages []providers.Message, defs []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	response, err := p.base.Chat(ctx, messages, defs, model, options)
	if err != nil {
		return nil, err
	}
	if p.calls.Add(1) != 1 {
		return response, nil
	}
	return &providers.LLMResponse{FinishReason: "tool_calls", ToolCalls: []providers.ToolCall{{
		ID: "r2-a1-real-question-park", Name: tools.AskUserQuestionToolName,
		Arguments: map[string]any{"questions": []any{map[string]any{
			"header": "Format", "question": "Choose the requested presentation format.", "default_safe": false,
			"options": []any{map[string]any{"label": "Compact", "description": "A short presentation"}, map[string]any{"label": "Detailed", "description": "A detailed presentation"}},
		}}},
	}}}, nil
}

func r2QuestionParkExcluded(t *testing.T) {
	h, _, msg := r2TwoFailuresAtNextWake(t)
	registry := newTestAskUserRegistry(t, h.al.GetSessionStore())
	h.al.SetAskUserRegistry(registry)
	t.Cleanup(registry.Quiesce)
	inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	provider := &r2QuestionProvider{base: h.root}
	inst.Provider = provider
	sub := h.al.SubscribeEvents(64)
	require.NotZero(t, sub.ID)
	t.Cleanup(func() { h.al.UnsubscribeEvents(sub.ID) })
	run := h.system(msg)
	call := grAwaitCall(t, h.root, run)
	require.False(t, call.human)
	require.Equal(t, h.goalID, call.turn.opts.goalReminderGoalID)
	h.root.release(call)
	result := grJoin(t, run)
	require.NoError(t, result.err)
	grAwaitOwner(t, call)
	h.al.lastTurnResultMu.Lock()
	actual := h.al.lastTurnResult
	h.al.lastTurnResultMu.Unlock()
	// Park's private result may omit turnID. Keep strict producing identity
	// through the REAL common event envelope instead; never accept an empty ID.
	r2RequireActualParkEvent(t, h, call, collectEventStream(sub.C))
	require.Equal(t, TurnEndStatusParked, actual.status, "SETUP: positively observe real ParksTurn, not a refused question or prose claim")
	require.False(t, actual.turnFailed)
	require.Equal(t, int32(1), provider.calls.Load())
	card, found := registry.PendingForSession(h.id)
	require.True(t, found, "SETUP: the actual question tool must create a real durable pending card")
	require.NotEmpty(t, card.CardID)
	require.Equal(t, h.id, card.TranscriptSessionID)
	r2RequirePersistedParkCard(t, h, card)
	before := h.root.count()
	for tick := 0; tick < 3; tick++ {
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		r2AssertNoFailurePause(t, h)
		assert.Nil(t, r6WarnPendingOutcome(h.goalID), "a genuine park must never be installed as a failed reminder outcome")
		assert.True(t, h.al.goalHasParkedCard(h.id))
		still, ok := registry.PendingForSession(h.id)
		require.True(t, ok)
		assert.Equal(t, card.CardID, still.CardID)
		assert.Equal(t, before, h.root.count())
		grNoPublishedReminder(t, h.al)
	}
	h.root.drainRequests()
}

func r2RequireActualParkEvent(t *testing.T, h *grHarness, call *grCall, events []Event) {
	t.Helper()
	require.NotNil(t, call.turn)
	require.NotEmpty(t, call.turn.turnID)
	var ends []Event
	for _, event := range events {
		payload, ok := event.Payload.(TurnEndPayload)
		if event.Kind == EventKindTurnEnd && ok && payload.SessionID == h.id {
			ends = append(ends, event)
		}
	}
	require.Len(t, ends, 1, "exactly this actual producing turn must report its park")
	assert.Equal(t, call.turn.turnID, ends[0].Meta.TurnID)
	assert.Equal(t, testDefaultAgentID, ends[0].Meta.AgentID)
	assert.Equal(t, TurnEndStatusParked, ends[0].Payload.(TurnEndPayload).Status)
}

func r2RequirePersistedParkCard(t *testing.T, h *grHarness, card *askuser.PendingSet) {
	t.Helper()
	fresh, err := session.NewUnifiedStore(h.al.GetSessionStore().BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, fresh.Close()) })
	meta, err := fresh.GetMeta(h.id)
	require.NoError(t, err)
	require.NotEmpty(t, meta.PendingAskJSON)
	var persisted askuser.PendingSet
	require.NoError(t, json.Unmarshal([]byte(meta.PendingAskJSON), &persisted))
	assert.Equal(t, card.CardID, persisted.CardID, "the card must actually survive a fresh session-store read")
	assert.Equal(t, h.id, persisted.TranscriptSessionID)
	assert.Equal(t, askuser.StatusPending, persisted.Status)
	require.Len(t, persisted.Questions, 1)
	assert.False(t, persisted.Questions[0].DefaultSafe, "SETUP: this real card must not arm an unattended timer/resume")
}

// This isolates the real non-reminder sendback PRODUCER boundary: actual
// dispatchGoalAsyncFollowUp -> notifier -> bus -> ordinary admission. It does
// not claim that a separate Judge's provider/adjudication pipeline ran here.
func r2SendbackFailureExcluded(t *testing.T) {
	h, provider, unexecutedReminder := r2TwoFailuresAtNextWake(t)
	require.Equal(t, "true", inboundMetadata(unexecutedReminder, "goal_reminder"))
	// A different actual input supersedes that published-but-unexecuted wake.
	// Never rewrite the old wake's generation/metadata to make it current.
	h.al.dispatchGoalAsyncFollowUp(h.id, h.goalID, goalClaimDeferredSourceKind, "Continue after the deferred review; the claim is not yet supported.")
	var sendback bus.InboundMessage
	select {
	case sendback = <-h.al.bus.InboundChan():
	default:
		t.Fatal("SETUP: the actual non-reminder sendback producer did not publish its notifier input")
	}
	require.Equal(t, "false", inboundMetadata(sendback, "goal_reminder"))
	require.Equal(t, h.id, sendback.AsyncTranscriptSessionID)
	require.Equal(t, "system:goal_loop", sendback.Sender.CanonicalID)
	require.False(t, sendback.UserInitiated)
	provider.mode.Store(r2ReplyProviderError)
	run := h.system(sendback)
	call := grAwaitCall(t, h.root, run)
	require.False(t, call.human)
	require.Empty(t, call.turn.opts.goalReminderGoalID)
	require.NotNil(t, call.turn.opts.goalWake)
	require.False(t, call.turn.opts.goalWake.reminder)
	h.root.release(call)
	result := grJoin(t, run)
	require.ErrorIs(t, result.err, r2ProviderFailure)
	grAwaitOwner(t, call)
	call.turn.mu.RLock()
	failed := call.turn.turnFailed
	call.turn.mu.RUnlock()
	require.True(t, failed, "SETUP: the non-reminder turn must really fail at the external provider")
	h.root.drainRequests()
	assert.Nil(t, r6WarnPendingOutcome(h.goalID), "same sender/session alone must not turn a failed sendback into a reminder outcome")
	for tick := 0; tick < 2; tick++ {
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		r2AssertNoFailurePause(t, h)
		assert.Nil(t, r6WarnPendingOutcome(h.goalID))
	}
	r2RequireThirdActualFailurePauses(t, h, provider)
}
