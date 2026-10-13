package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Chief's F01 ruling: amendment §4.1 applies to the late streamed-transcript
// write, not only the earlier model-window append. Freeze the existing notice
// independently; changing production copy must not adapt this oracle.
const wu22GatewaySaveNotice = "Sorry, I couldn't save your message — please try again later."
const wu22GatewayFinalAnswer = "WU22 exact streamed final answer"

type wu22GatewayFinalizeHarness struct {
	handler  *WSHandler
	store    *session.UnifiedStore
	streamer *wsStreamer
	conn     *wsConn
	sid      string
	first    [][]byte
}

// All gateway, streamer, hub and store code is real. Only the socket edge is
// the existing buffered connection fixture; no replacement Finalize method or
// SessionWriter is involved. A successful real model append precedes the fault.
func wu22GatewayFinalizeFixture(t *testing.T) *wu22GatewayFinalizeHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	// Match gateway boot wiring, including stream ownership and Send dedup.
	handler.webchatCh = newWebchatChannel(handler)
	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, store.AddMessage(meta.ID, "assistant", wu22GatewayFinalAnswer), "SETUP: the earlier model-window write succeeds")
	require.NoError(t, store.Save(meta.ID))
	require.Equal(t, []providers.Message{{Role: "assistant", Content: wu22GatewayFinalAnswer}}, store.GetHistory(meta.ID))
	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	require.Empty(t, entries, "SETUP: model-only append is not the final user-facing transcript entry")

	conn := makeTestConn()
	bindTestConnToSession(handler, "wu22-gateway-chat", meta.ID, conn)
	stream, ok := handler.GetStreamer(context.Background(), "webchat", "wu22-gateway-chat", meta.ID)
	require.True(t, ok, "real gateway streaming door is reachable")
	streamer, ok := stream.(*wsStreamer)
	require.True(t, ok, "fixture must exercise the production wsStreamer")
	streamer.SetProducerAgentID("mia")
	streamer.SetTurnID("wu22-gateway-turn")
	streamer.SetMessageID("wu22-gateway-answer")
	streamer.SetTurnFailed(false) // the turn was healthy before this late write
	require.NoError(t, streamer.Update(context.Background(), wu22GatewayFinalAnswer))
	first := readFramesUntil(t, conn, 1, func(frame map[string]any) bool { return frame["type"] == "token" })
	require.Len(t, first, 1, "instrument: actual streamed text reaches the bound user before the fault")
	var token generated.TokenFrame
	require.NoError(t, json.Unmarshal(first[0], &token))
	require.Equal(t, wu22GatewayFinalAnswer, token.Content)
	require.Equal(t, meta.ID, token.SessionId)
	return &wu22GatewayFinalizeHarness{handler: handler, store: store, streamer: streamer, conn: conn, sid: meta.ID, first: first}
}

// Preserve the successful model append's file, then block only the late
// transcript append. Directory-at-file faults work regardless of uid.
func wu22GatewayBlockTranscript(t *testing.T, h *wu22GatewayFinalizeHarness) func() {
	t.Helper()
	path := filepath.Join(h.store.BaseDir(), h.sid, "transcript.jsonl")
	backup := filepath.Join(t.TempDir(), "saved-archive.jsonl")
	require.NoError(t, os.Rename(path, backup))
	require.NoError(t, os.Mkdir(path, 0o700))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if file != nil {
		require.NoError(t, file.Close())
	}
	require.Error(t, err, "instrument: late archive append really fails on the filesystem")
	var once sync.Once
	restore := func() {
		once.Do(func() {
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Rename(backup, path))
		})
	}
	t.Cleanup(restore)
	return restore
}

// Finalize publishes synchronously into the real hub. Its retained journal
// determines the finite frame count to read from the asynchronous socket queue,
// so neither an empty poll nor a stopwatch is used to prove notice absence.
func wu22GatewayFinalFrames(t *testing.T, h *wu22GatewayFinalizeHarness) ([][]byte, [][]byte) {
	t.Helper()
	hub := h.handler.hubs.lookup(h.sid)
	require.NotNil(t, hub)
	hub.mu.Lock()
	journal := make([][]byte, len(hub.journal))
	for i, entry := range hub.journal {
		journal[i] = append([]byte(nil), entry.bytes...)
	}
	hub.mu.Unlock()
	require.Greater(t, len(journal), len(h.first), "instrument: completion must produce a retained frame")
	live := append([][]byte(nil), h.first...)
	deadline := time.NewTimer(5 * time.Second) // hang guard, not a latency assertion
	defer deadline.Stop()
	for len(live) < len(journal) {
		select {
		case raw := <-h.conn.sendCh:
			live = append(live, raw)
		case <-deadline.C:
			t.Fatalf("retained gateway frames did not reach the bound user: got %d of %d", len(live), len(journal))
		}
	}
	assert.Equal(t, journal, live, "the bound user receives the exact retained gateway frames")
	return live, journal
}

func wu22GatewayDoneFrames(t *testing.T, frames [][]byte) []generated.DoneFrame {
	t.Helper()
	var done []generated.DoneFrame
	for _, raw := range framesOfType(t, frames, "done") {
		var frame generated.DoneFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		done = append(done, frame)
	}
	return done
}

func wu22GatewayNoticeCount(t *testing.T, frames [][]byte) int {
	t.Helper()
	count := 0
	for _, raw := range framesOfType(t, frames, "error") {
		var frame generated.ErrorFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame.Message == wu22GatewaySaveNotice {
			count++
		}
	}
	for _, raw := range framesOfType(t, frames, "token") {
		var frame generated.TokenFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame.Content == wu22GatewaySaveNotice {
			count++
		}
	}
	return count
}

func TestWU22GatewayFinalizeTranscriptFailureReturnsErrorAndFailedDone(t *testing.T) {
	h := wu22GatewayFinalizeFixture(t)
	restore := wu22GatewayBlockTranscript(t, h)
	finalErr := h.streamer.Finalize(context.Background(), wu22GatewayFinalAnswer)
	var cause *os.PathError
	assert.ErrorAs(t, finalErr, &cause, "§4.1: caller must receive the late transcript-write failure, never log-only nil")
	if cause != nil {
		assert.Equal(t, filepath.Join(h.store.BaseDir(), h.sid, "transcript.jsonl"), cause.Path)
		assert.Equal(t, "open", cause.Op, "original append-open cause survives")
	}
	live, retained := wu22GatewayFinalFrames(t, h)
	for label, frames := range map[string][][]byte{"live user": live, "retained completion": retained} {
		done := wu22GatewayDoneFrames(t, frames)
		require.Len(t, done, 1, "%s: failure still closes the stream once", label)
		assert.Equal(t, h.sid, done[0].SessionId)
		require.NotNil(t, done[0].Stats)
		if assert.NotNil(t, done[0].Stats.TurnFailed, "%s: no apparently successful done after failed persistence", label) {
			assert.True(t, *done[0].Stats.TurnFailed, "%s: terminal outcome must report failure", label)
		}
	}
	assert.Equal(t, 1, wu22GatewayNoticeCount(t, live), "§4.1: existing could-not-save notice must reach the user exactly once")

	// A fault on one chat must not prevent a healthy sibling from finalizing.
	sibling, err := h.store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	other, ok := h.handler.GetStreamer(context.Background(), "webchat", "wu22-sibling-chat", sibling.ID)
	require.True(t, ok)
	require.NoError(t, other.Finalize(context.Background(), "WU22 healthy sibling"))
	otherEntries, err := h.store.ReadTranscript(sibling.ID)
	require.NoError(t, err)
	require.Len(t, otherEntries, 1)
	assert.Equal(t, "WU22 healthy sibling", otherEntries[0].Content)

	restore()
	entries, err := h.store.ReadTranscript(h.sid)
	require.NoError(t, err)
	assert.Empty(t, entries, "failed final append must not persist a successful final chat entry")
	assert.Equal(t, []providers.Message{{Role: "assistant", Content: wu22GatewayFinalAnswer}}, h.store.GetHistory(h.sid), "the preceding successful model write is not retroactively deleted")
}

func TestWU22GatewayFinalizeHealthyTranscriptPersistsAndCompletes(t *testing.T) {
	h := wu22GatewayFinalizeFixture(t)
	require.NoError(t, h.streamer.Finalize(context.Background(), wu22GatewayFinalAnswer))
	live, retained := wu22GatewayFinalFrames(t, h)
	for _, frames := range [][][]byte{live, retained} {
		done := wu22GatewayDoneFrames(t, frames)
		require.Len(t, done, 1)
		assert.Equal(t, h.sid, done[0].SessionId)
		require.NotNil(t, done[0].Stats)
		if done[0].Stats.TurnFailed != nil {
			assert.False(t, *done[0].Stats.TurnFailed, "healthy control must not report failed completion")
		}
	}
	assert.Equal(t, 0, wu22GatewayNoticeCount(t, live), "same healthy finalize must not invent a save failure")
	entries, err := h.store.ReadTranscript(h.sid)
	require.NoError(t, err)
	require.Len(t, entries, 1, "healthy final answer is durably user-visible")
	assert.Equal(t, "wu22-gateway-answer", entries[0].ID)
	assert.Equal(t, "assistant", entries[0].Role)
	assert.Equal(t, wu22GatewayFinalAnswer, entries[0].Content)
	assert.Equal(t, "mia", entries[0].AgentID)
	assert.Equal(t, "wu22-gateway-turn", entries[0].TurnID)
}
