package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WU-22 / #1178 oracles come from L1-AMENDMENTS-20261013.md §4.2.
// The store, archive, turn stages and event bus are real. Only outgoing
// streaming and the external provider/tool are fixtures. No SessionWriter fake.
type wu22Harness struct {
	al     *AgentLoop
	agent  *AgentInstance
	store  *session.UnifiedStore
	sid    string
	ts     *turnState
	stream *wu22Stream
	mu     sync.Mutex
	events []Event
}

// The capture is the transport edge receiving the production SetTurnFailed
// call which drives DoneStats.TurnFailed. It does not supply a turn outcome.
type wu22Stream struct {
	mu        sync.Mutex
	failed    []bool
	finalized []string
}

func (*wu22Stream) Update(context.Context, string) error { return nil }
func (*wu22Stream) Cancel(context.Context)               {}
func (s *wu22Stream) Finalize(_ context.Context, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalized = append(s.finalized, content)
	return nil
}
func (s *wu22Stream) SetTurnFailed(failed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = append(s.failed, failed)
}

func wu22NewHarness(t *testing.T, provider providers.LLMProvider) *wu22Harness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	al, inst, store, sid := newTruncationTestHarness(t, provider, 4, 0)
	h := &wu22Harness{al: al, agent: inst, store: store, sid: sid, stream: &wu22Stream{}}
	h.ts = newTurnState(inst, processOptions{
		SessionKey: sid, Channel: "webchat", ChatID: sid,
		UserMessage: "WU22 input", DefaultResponse: defaultResponse,
		TranscriptSessionID: sid, TranscriptStore: store,
	}, al.newTurnEventScope(inst.ID, sid))
	require.NoError(t, h.ts.windowError, "SETUP: healthy real window")
	h.ts.setLastStreamer(h.stream)
	al.SetEventSyncTap(func(event Event) {
		h.mu.Lock()
		h.events = append(h.events, event)
		h.mu.Unlock()
	})
	t.Cleanup(func() { al.SetEventSyncTap(nil) })
	return h
}

func (h *wu22Harness) responseStage() *agentLoopRunTurnResponse {
	rt := &agentLoopRunTurn{al: h.al, ts: h.ts, turnCtx: context.Background(), iteration: 1, llmModel: h.agent.Model}
	rf := &agentLoopRunTurnFallbacks{rt: rt}
	ri := &agentLoopRunTurnIteration{rf: rf, cfg: h.al.cfg, activeModel: h.agent.Model, turnStatus: TurnEndStatusCompleted}
	return &agentLoopRunTurnResponse{rq: &agentLoopRunTurnRequest{ri: ri}}
}

// A file path becomes a directory, so the append fails under root as well.
// Preserve any index bytes and restore explicitly before checking history.
func wu22BlockModelWrite(t *testing.T, store *session.UnifiedStore, sid string) func() {
	t.Helper()
	path := filepath.Join(store.BaseDir(), sid, "ordinal_rows.log")
	backup := filepath.Join(t.TempDir(), "ordinal_rows.saved")
	_, err := os.Stat(path)
	hadFile := err == nil
	if hadFile {
		require.NoError(t, os.Rename(path, backup))
	} else {
		require.True(t, errors.Is(err, os.ErrNotExist), "SETUP: stat model index: %v", err)
	}
	require.NoError(t, os.MkdirAll(path, 0o700))
	file, openErr := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if file != nil {
		require.NoError(t, file.Close())
	}
	require.Error(t, openErr, "instrument: model append path must be unwriteable")
	var once sync.Once
	restore := func() {
		once.Do(func() {
			require.NoError(t, os.Remove(path))
			if hadFile {
				require.NoError(t, os.Rename(backup, path))
			}
		})
	}
	t.Cleanup(restore)
	return restore
}

func (h *wu22Harness) assertSaveFailure(t *testing.T, err error) {
	t.Helper()
	var notSaved *messageNotSavedError
	assert.ErrorAs(t, err, &notSaved, "§4.2: failed archive write must reach the caller as messageNotSavedError, not success/another error")
	if err != nil {
		assert.Equal(t, messageNotSavedReply, TranslateTurnError(err).Message, "§4.2: existing notice, never raw filesystem detail")
		var cause *os.PathError
		assert.ErrorAs(t, err, &cause, "original filesystem cause must survive wrapping")
	}
	assert.True(t, h.ts.turnFailed, "§4.2: storage failure must call markTurnFailed, including direct stage exits")
	h.ts.finalizeStreamer(context.Background())
	h.stream.mu.Lock()
	assert.Equal(t, []bool{true}, h.stream.failed, "DoneStats.TurnFailed must be true at the outgoing transport edge")
	h.stream.mu.Unlock()
	h.assertNotice(t)
	h.assertNoSuccessfulOutcome(t)
	h.assertSiblingWritable(t)
}

func (h *wu22Harness) assertNotice(t *testing.T) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	notices := 0
	for _, event := range h.events {
		if event.Kind == EventKindError {
			payload, ok := event.Payload.(ErrorPayload)
			require.True(t, ok)
			if payload.Message == messageNotSavedReply {
				notices++
				assert.Equal(t, h.sid, payload.SessionID, "save error belongs only to the failed session")
			}
		}
	}
	assert.Equal(t, 1, notices, "§4.2: one could-not-save error event must be visible")
}

func (h *wu22Harness) assertNoSuccessfulOutcome(t *testing.T) {
	t.Helper()
	entries, err := h.store.ReadTranscript(h.sid)
	require.NoError(t, err, "instrument: transcript remains readable after model-only fault")
	savedNotices := 0
	for _, entry := range entries {
		assert.NotEqual(t, "done", entry.Status, "a failed window write must not be persisted as done")
		assert.NotEqual(t, "completed", entry.Status, "a failed window write must not be persisted as completed")
		if entry.Status == "error" && entry.Content == messageNotSavedReply {
			savedNotices++
			assert.Equal(t, string(CodeUnknown), entry.ErrorCode, "reload must retain the classified save error")
		}
	}
	assert.Equal(t, 1, savedNotices, "§4.2: classified save failure must survive reload, not just be logged")
	assert.NotEqual(t, TurnPhaseCompleted, h.ts.phase, "no successful finalization after failed save")
}

func (h *wu22Harness) assertSiblingWritable(t *testing.T) {
	t.Helper()
	sibling, err := h.store.NewSession(session.SessionTypeChat, "webchat", h.agent.ID)
	require.NoError(t, err)
	h.store.AddMessage(sibling.ID, "assistant", "WU22 sibling answer")
	require.NoError(t, h.store.Save(sibling.ID))
	assert.Equal(t, []providers.Message{{Role: "assistant", Content: "WU22 sibling answer"}}, h.store.GetHistory(sibling.ID), "one session's write failure must not poison a sibling")
}

// Reflection is used only to observe a changed method's optional return value;
// production tests keep building both before and after the interface change.
// The healthy controls therefore exercise the old void writer too.
func wu22HealthyProvider() providers.LLMProvider {
	return &asyncResultStreamingProvider{content: "WU22 exact final answer"}
}

var _ bus.Streamer = (*wu22Stream)(nil)
