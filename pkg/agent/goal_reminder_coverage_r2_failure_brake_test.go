package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Independent literal: chief's A1-DEFINITION-CLARIFICATION, Q1=A.
const r2FailedTurnPause = "3 reminder turns failed in a row — goal paused. Reply to continue."

// Fixture response modes at the external provider boundary, not product
// categories/counters. No test writes failure/owner/pending/lifecycle facts.
const (
	r2ReplySuccess int32 = iota
	r2ReplyProviderError
	r2ReplyEmpty
)

var errR2ProviderFailure = errors.New("controlled provider request failure for the reminder-turn contract")

type r2FailureProvider struct {
	base *grProvider
	mode atomic.Int32
}

func (p *r2FailureProvider) GetDefaultModel() string { return p.base.GetDefaultModel() }
func (p *r2FailureProvider) Chat(ctx context.Context, messages []providers.Message, defs []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	response, err := p.base.Chat(ctx, messages, defs, model, options)
	if err != nil {
		return nil, err
	}
	switch p.mode.Load() {
	case r2ReplyProviderError:
		return nil, errR2ProviderFailure
	case r2ReplyEmpty:
		return &providers.LLMResponse{FinishReason: "stop"}, nil
	default:
		return response, nil
	}
}

func r2InstallFailureProvider(t *testing.T, h *grHarness) *r2FailureProvider {
	t.Helper()
	inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	provider := &r2FailureProvider{base: h.root}
	inst.Provider = provider
	return provider
}

// A provider error and an exhausted empty reply both produce the real
// turnFailed flag. Empty fallback need not return a Go error or mark the
// lifecycle failed: it still was NOT a successful automatic reminder turn.
func r2FailedReminder(t *testing.T, h *grHarness, provider *r2FailureProvider, mode int32) *grCall {
	t.Helper()
	provider.mode.Store(mode)
	h.age(t)
	prior := h.load(t)
	msg := h.reminder(t)
	run := h.system(msg)
	call := grAwaitCall(t, h.root, run)
	require.NoError(t, call.readErr)
	require.NotNil(t, call.record)
	require.NotNil(t, call.record.ExecutionID)
	require.NotNil(t, call.owner)
	require.NotNil(t, call.turn)
	require.False(t, call.human, "SETUP: failure must belong to a real automatic reminder")
	require.Equal(t, h.id, call.sessionID)
	require.Equal(t, h.goalID, call.turn.opts.goalReminderGoalID)
	require.Equal(t, prior.Generation+1, call.record.Generation, "SETUP: each failure is a new actual turn, not another provider retry")
	require.NotEqual(t, prior.ExecutionID.RunID, call.record.ExecutionID.RunID)
	h.root.release(call)
	result := grJoin(t, run)
	grAwaitOwner(t, call)
	if mode == r2ReplyProviderError {
		require.ErrorIs(t, result.err, errR2ProviderFailure, "SETUP: the actual provider failure must reach the real receiver")
	}
	require.NotErrorIs(t, result.err, context.Canceled)
	call.turn.mu.RLock()
	failed := call.turn.turnFailed
	call.turn.mu.RUnlock()
	require.True(t, failed, "SETUP: count an actual failed turn, not an error-looking successful response")
	require.False(t, call.turn.stopRequested.Load(), "SETUP: this failure was not cancelled or stopped")
	require.Equal(t, call.record.ExecutionID, h.load(t).ExecutionID, "SETUP: the actual producing identity must settle")
	require.Nil(t, h.al.executionDispositionFor(call.claim), "SETUP: the real producing owner must retire")
	require.Nil(t, h.al.ownLiveTurn(h.id))
	h.root.drainRequests() // retries are already joined, not extra failed turns
	return call
}

func r2FailureNotices(t *testing.T, h *grHarness) func() []GoalStatusChangedPayload {
	t.Helper()
	var mu sync.Mutex
	var notices []GoalStatusChangedPayload
	h.al.SetEventSyncTap(func(event Event) {
		payload, ok := event.Payload.(GoalStatusChangedPayload)
		if event.Kind != EventKindGoalStatusChanged || !ok || payload.SessionID != h.id || payload.GoalID != h.goalID ||
			payload.ChatNotice == nil || payload.ChatNotice.Content != r2FailedTurnPause {
			return
		}
		mu.Lock()
		notices = append(notices, payload)
		mu.Unlock()
	})
	t.Cleanup(func() { h.al.SetEventSyncTap(nil) })
	return func() []GoalStatusChangedPayload {
		mu.Lock()
		defer mu.Unlock()
		return append([]GoalStatusChangedPayload(nil), notices...)
	}
}

func r2NoFourthWake(t *testing.T, h *grHarness) {
	t.Helper()
	select {
	case <-h.al.bus.InboundChan():
		t.Error("three consecutive actual failed reminder turns must not publish a fourth automatic wake")
	default:
	}
}

func r2AssertFailurePause(t *testing.T, h *grHarness, live func() []GoalStatusChangedPayload) {
	t.Helper()
	var rows []session.TranscriptEntry
	for _, row := range h.visibleHistory(t) {
		if row.Content == r2FailedTurnPause {
			rows = append(rows, row)
		}
	}
	assert.Len(t, rows, 1, "third consecutive failure must write the exact cause-neutral system notice once")
	if len(rows) == 1 {
		assert.Equal(t, session.EntryTypeSystem, rows[0].Type)
		assert.Equal(t, "system", rows[0].Role)
		assert.Equal(t, h.goalID, rows[0].GoalID)
		assert.NotEmpty(t, rows[0].ID)
	}
	events := live()
	assert.Len(t, events, 1, "the same-chat failure pause must use the existing live generated carrier once")
	if len(events) == 1 {
		assert.Equal(t, goalPillWaitingOnUser, events[0].State)
		assert.Equal(t, "replay_message", events[0].ChatNotice.Type)
		assert.Equal(t, h.id, events[0].ChatNotice.SessionId)
		assert.Equal(t, "system", events[0].ChatNotice.Role)
		require.NotNil(t, events[0].ChatNotice.GoalId)
		assert.Equal(t, h.goalID, *events[0].ChatNotice.GoalId)
		if len(rows) == 1 {
			require.NotNil(t, events[0].ChatNotice.Id)
			assert.Equal(t, rows[0].ID, *events[0].ChatNotice.Id)
		}
	}
	assert.True(t, h.al.goalIsWaitingOnUser(h.goalID), "the third failed turn must hold the same active goal for a genuine reply")
	assert.Equal(t, h.goalID, goalRecordForSession(t, h.id).GoalID)
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
	assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes, "failed turns never spend the separate completed-unproductive counter")
	assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine), "the original five-outcome brake must not be substituted for A1")
}

func TestGoalReminderFailedTurnBrake(t *testing.T) {
	t.Run("ThreeProviderFailuresPauseAndHumanResumes", func(t *testing.T) {
		r2ThreeFailures(t, []int32{r2ReplyProviderError, r2ReplyProviderError, r2ReplyProviderError}, true)
	})
	t.Run("EmptyReplyIsAnActualFailureNotAProviderOnlyCategory", func(t *testing.T) {
		r2ThreeFailures(t, []int32{r2ReplyEmpty, r2ReplyProviderError, r2ReplyProviderError}, false)
	})
	t.Run("SuccessfulReminderPreventsLifetimeFailureQuota", r2FailureSuccessReset)
	t.Run("RealStopIsNotTheThirdCountedFailure", r2FailureStopExcluded)
}

func r2ThreeFailures(t *testing.T, modes []int32, humanResume bool) {
	t.Helper()
	h := grNewRecordedBrake(t)
	provider := r2InstallFailureProvider(t, h)
	live := r2FailureNotices(t, h)
	grSetStreak(t, h, 2) // independent already-assessed unproductive streak
	for i, mode := range modes {
		r2FailedReminder(t, h, provider, mode)
		assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes)
		if i < 2 {
			assert.False(t, h.al.goalIsWaitingOnUser(h.goalID), "fewer than three actual failed turns cannot pause")
			assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), r2FailedTurnPause))
		}
	}
	before := h.root.count()
	for tick := 0; tick < 2; tick++ {
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		r2NoFourthWake(t, h)
		r2AssertFailurePause(t, h, live)
		assert.Equal(t, before, h.root.count())
	}
	if humanResume {
		provider.mode.Store(r2ReplySuccess)
		run := h.human(t, "Continue this same goal after the failed automatic turns.")
		call := grAwaitCall(t, h.root, run)
		require.True(t, call.human)
		h.finish(t, run, call)
		grAwaitOwner(t, call)
		assert.False(t, h.al.goalIsWaitingOnUser(h.goalID))
		assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
		h.age(t)
		grTextReminder(t, h, h.reminder(t))
		assert.Equal(t, h.goalID, goalRecordForSession(t, h.id).GoalID)
		assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
	}
}

func r2FailureSuccessReset(t *testing.T) {
	h := grNewRecordedBrake(t)
	provider := r2InstallFailureProvider(t, h)
	for i := 0; i < 2; i++ {
		r2FailedReminder(t, h, provider, r2ReplyProviderError)
		assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
	}
	provider.mode.Store(r2ReplySuccess)
	h.age(t)
	grTextReminder(t, h, h.reminder(t))
	for i := 0; i < 2; i++ {
		r2FailedReminder(t, h, provider, r2ReplyProviderError)
		assert.False(t, h.al.goalIsWaitingOnUser(h.goalID), "a successful reminder breaks the earlier pair of failures")
		assert.Equal(t, 1, goalRecordForSession(t, h.id).ZeroOutputPushes, "only the actual completed text outcome spends the no-progress counter")
		assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), r2FailedTurnPause))
	}
	provider.mode.Store(r2ReplySuccess)
	h.age(t)
	grTextReminder(t, h, h.reminder(t)) // permitted despite four lifetime failures
}

func r2FailureStopExcluded(t *testing.T) {
	h := grNewRecordedBrake(t)
	provider := r2InstallFailureProvider(t, h)
	grSetStreak(t, h, 2)
	for i := 0; i < 2; i++ {
		r2FailedReminder(t, h, provider, r2ReplyProviderError)
	}
	h.age(t)
	run := h.system(h.reminder(t))
	call := grAwaitCall(t, h.root, run)
	require.False(t, call.human)
	grStop(t, h)
	h.root.release(call)
	grJoin(t, run)
	grAwaitOwner(t, call)
	require.True(t, call.turn.stopRequested.Load(), "SETUP: actual Stop must select this real automatic execution")
	require.Equal(t, session.LifecycleStopped, h.load(t).State)
	for tick := 0; tick < 2; tick++ {
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		grNoPublishedReminder(t, h.al)
		assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), r2FailedTurnPause), "Stop/cancel is not a third non-cancelled failure")
		assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes)
	}
}
