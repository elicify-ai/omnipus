package agent

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func wfR1OrdinaryHarness(t *testing.T) (*wu22Harness, *scriptChatProvider) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	provider := &scriptChatProvider{fallback: "healthy ordinary parent reply"}
	al, _ := newChatTargetAskUserLoop(t, provider)
	gate1WireSessionMessaging(t, al)
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	agent, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	h := &wu22Harness{al: al, agent: agent, store: al.GetSessionStore(), sid: meta.ID}
	al.SetEventSyncTap(func(event Event) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.events = append(h.events, event)
	})
	t.Cleanup(func() { al.SetEventSyncTap(nil) })
	return h, provider
}

func wfR1OrdinaryMessage(h *wu22Harness) bus.InboundMessage {
	return bus.InboundMessage{Channel: "webchat", ChatID: h.sid, SessionID: h.sid,
		Content: "Continue with the held helper report.", UserInitiated: true,
		Sender: bus.SenderInfo{CanonicalID: "webchat_user"}, GatewayUserID: "daniel",
		Metadata: map[string]string{"agent_id": "mia"}}
}

func wfR1SavedReport(t *testing.T, h *wu22Harness, outcome string) (*session.ArchiveDayStore, session.ArchiveAddress, []byte) {
	t.Helper()
	addr, err := h.store.SaveChatInput(h.sid, "saved-input-"+outcome, h.agent.ID, "held helper report: "+outcome)
	require.NoError(t, err)
	archive, err := session.NewArchiveDayStore(h.store.BaseDir(), h.sid)
	require.NoError(t, err)
	raw, _, err := archive.ReadAtRaw(addr)
	require.NoError(t, err)
	pending, err := h.store.PendingChatInputs(context.Background(), h.sid)
	require.NoError(t, err)
	require.Len(t, pending, 1, "SETUP: the real held report is pending before corruption")
	return archive, addr, raw
}

func wfR1AssertOrdinaryFailure(t *testing.T, h *wu22Harness, provider *scriptChatProvider, err error) {
	t.Helper()
	var notSaved *messageNotSavedError
	require.ErrorAs(t, err, &notSaved)
	require.Equal(t, "user", notSaved.role)
	require.Zero(t, provider.callCount(), "a storage refusal must make zero model calls")
	require.Equal(t, wu22NotSavedNotice, TranslateTurnError(err).Message)
	h.assertNotice(t)
	h.mu.Lock()
	defer h.mu.Unlock()
	var statuses []TurnEndStatus
	for _, event := range h.events {
		if event.Kind == EventKindTurnEnd {
			payload, ok := event.Payload.(TurnEndPayload)
			require.True(t, ok)
			statuses = append(statuses, payload.Status)
		}
	}
	require.Equal(t, []TurnEndStatus{TurnEndStatusError}, statuses)
}

func TestSavedWakeReviewR1Boundaries(t *testing.T) {
	t.Run("ordinary_parent_zero_byte_index_is_visible_PTA01", func(t *testing.T) {
		h, provider := wfR1OrdinaryHarness(t)
		archive, addr, before := wfR1SavedReport(t, h, "ordinary-held")
		require.NoError(t, os.Truncate(filepath.Join(h.store.BaseDir(), h.sid, "saved_inputs.json"), 0))

		_, _, err := h.al.processMessageAsPrimaryForTest(context.Background(), wfR1OrdinaryMessage(h))

		wfR1AssertOrdinaryFailure(t, h, provider, err)
		var syntaxErr *json.SyntaxError
		require.ErrorAs(t, err, &syntaxErr, "the JSON parse cause must survive ordinary-turn wrapping")
		after, _, readErr := archive.ReadAtRaw(addr)
		require.NoError(t, readErr)
		require.Equal(t, before, after)
		entries, readErr := h.store.ReadTranscript(h.sid)
		require.NoError(t, readErr)
		notices := 0
		for _, entry := range entries {
			if entry.Status == "error" && entry.Content == wu22NotSavedNotice {
				notices++
			}
		}
		require.Equal(t, 1, notices, "the exact sender notice must survive reload")
	})

	t.Run("ordinary_absent_index_control", func(t *testing.T) {
		h, provider := wfR1OrdinaryHarness(t)
		_, err := os.Stat(filepath.Join(h.store.BaseDir(), h.sid, "saved_inputs.json"))
		require.ErrorIs(t, err, fs.ErrNotExist)
		answer, _, err := h.al.processMessageAsPrimaryForTest(context.Background(), wfR1OrdinaryMessage(h))
		require.NoError(t, err)
		require.Equal(t, "healthy ordinary parent reply", answer)
		require.Equal(t, 1, provider.callCount(), "positive control: ordinary routing reaches the real provider edge")
	})

	t.Run("indexed_archive_missing_is_not_empty_SFH3", func(t *testing.T) {
		h, provider := wfR1OrdinaryHarness(t)
		wfR1SavedReport(t, h, "missing-archive")
		ts := newTurnState(h.agent, processOptions{SessionKey: h.sid, TranscriptSessionID: h.sid,
			TranscriptStore: h.store}, h.al.newTurnEventScope(h.agent.ID, h.sid))
		require.NoError(t, os.Remove(filepath.Join(h.store.BaseDir(), h.sid, "transcript.jsonl")))

		placementErr := h.al.placePendingSavedWakeInputs(context.Background(), ts)
		require.ErrorIs(t, placementErr, fs.ErrNotExist, "missing indexed content is not a missing index")
		_, _, err := h.al.processMessageAsPrimaryForTest(context.Background(), wfR1OrdinaryMessage(h))
		wfR1AssertOrdinaryFailure(t, h, provider, err)
		require.ErrorIs(t, err, fs.ErrNotExist)
	})

	t.Run("idle_saved_wake_publishes_pending_notice_SFH1", func(t *testing.T) {
		h, provider := wfR1OrdinaryHarness(t)
		_, _, err := h.al.processMessageAsPrimaryForTest(context.Background(), wfR1OrdinaryMessage(h))
		require.NoError(t, err)
		const outcome = "idle-helper:1:final"
		archive, addr, before := wfR1SavedReport(t, h, outcome)
		rec, err := h.al.GetSessionLifecycleStore().Load(h.sid)
		require.NoError(t, err)
		require.NoError(t, os.Truncate(filepath.Join(h.store.BaseDir(), h.sid, "saved_inputs.json"), 0))
		h.mu.Lock()
		h.events = nil
		h.mu.Unlock()
		calls := provider.callCount()

		_, err = h.al.processSteeredSystemWake(context.Background(), bus.InboundMessage{
			Channel: "system", AsyncTranscriptSessionID: h.sid,
			Metadata: map[string]string{"steer_message_id": outcome, "steer_generation": strconv.Itoa(rec.Generation)},
		})

		var syntaxErr *json.SyntaxError
		require.ErrorAs(t, err, &syntaxErr)
		require.Equal(t, calls, provider.callCount(), "no idle turn starts after the read error")
		after, _, readErr := archive.ReadAtRaw(addr)
		require.NoError(t, readErr)
		require.Equal(t, before, after)
		h.mu.Lock()
		defer h.mu.Unlock()
		var frames []generated.HelperEventFrame
		for _, event := range h.events {
			if event.Kind == EventKindHelperEvent {
				frame, ok := event.Payload.(generated.HelperEventFrame)
				require.True(t, ok)
				frames = append(frames, frame)
			}
		}
		require.Len(t, frames, 1, "the idle-wake error must reach the user's chat, not only a log")
		require.Equal(t, h.sid, frames[0].SessionId)
		require.Equal(t, "A helper report for this chat could not be read yet. Omnipus will retry. This is not a failure of the helper's work.", frames[0].HelperEvent.Text)
	})

	t.Run("shared_context_exit_logs_raw_cause_SFH5", func(t *testing.T) {
		h := wu22NewHarness(t, wu22HealthyProvider())
		wfR1SavedReport(t, h, "native-helper-held")
		require.NoError(t, os.Truncate(filepath.Join(h.store.BaseDir(), h.sid, "saved_inputs.json"), 0))
		_, parseErr := h.store.PendingChatInputs(context.Background(), h.sid)
		require.Error(t, parseErr)
		logPath := wu22AppLog(t)
		logger.ErrorCF("wf-r1-instrument", "diagnostic capture control", map[string]any{"session_id": h.sid})
		h.ts.turnID = "wf-r1-native-context-turn"
		cause := &messageNotSavedError{role: "user", cause: parseErr}

		_, status, err := h.al.contextWindowTurnExit(h.ts, 1, h.agent.Model, cause)

		require.Same(t, cause, err)
		require.Equal(t, TurnEndStatusError, status)
		h.assertNotice(t)
		data, readErr := os.ReadFile(logPath)
		require.NoError(t, readErr)
		control, diagnostics := 0, 0
		for _, row := range wu22LogRows(t, string(data)) {
			if row["component"] == "wf-r1-instrument" {
				control++
			}
			if row["level"] == "error" && row["session_id"] == h.sid && row["error"] == cause.Error() {
				require.Equal(t, h.agent.ID, row["agent_id"])
				require.Equal(t, h.ts.turnID, row["turn_id"])
				diagnostics++
			}
		}
		require.Equal(t, 1, control, "positive control: the real structured sink captured an error")
		require.Equal(t, 1, diagnostics, "shared context exit must retain the raw cause and execution identity")
	})
}
