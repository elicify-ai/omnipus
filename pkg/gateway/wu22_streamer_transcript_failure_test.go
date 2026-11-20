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
//
// #1428 step 2: a turn's live-only turn_started frame reaches the bound user
// but is never numbered or journaled, so it is set aside from the
// journal-equality oracle and pinned on its own: unnumbered, absent from the
// journal, after the previous done and before any token of its turn, and
// followed by that turn's done. Every other live frame must still equal the
// journal exactly. The returned live slice keeps every frame in arrival order.
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
	journaled := 0
	for _, raw := range live {
		if !wu22IsTurnStarted(t, raw) {
			journaled++
		}
	}
	deadline := time.NewTimer(5 * time.Second) // hang guard, not a latency assertion
	defer deadline.Stop()
	for journaled < len(journal) {
		select {
		case raw := <-h.conn.sendCh:
			live = append(live, raw)
			if !wu22IsTurnStarted(t, raw) {
				journaled++
			}
		case <-deadline.C:
			t.Fatalf("retained gateway frames did not reach the bound user: got %d of %d", journaled, len(journal))
		}
	}
	retained := make([][]byte, 0, len(journal))
	for _, raw := range live {
		if !wu22IsTurnStarted(t, raw) {
			retained = append(retained, raw)
		}
	}
	assert.Equal(t, journal, retained, "the bound user receives the exact retained gateway frames")
	wu22AssertTurnStartedPlacement(t, live, journal)
	return live, journal
}

func wu22IsTurnStarted(t *testing.T, raw []byte) bool {
	t.Helper()
	var head struct {
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(raw, &head))
	return head.Type == string(generated.WsFrameTypeTurnStarted)
}

// wu22AssertTurnStartedPlacement pins every live turn_started to the design's
// slot: never numbered, never retained in the journal, no token between the
// previous done (or the start of the stream) and it, and a done after it.
func wu22AssertTurnStartedPlacement(t *testing.T, live, journal [][]byte) {
	t.Helper()
	tokenSinceDone := false
	for i, raw := range live {
		var head struct {
			Type string `json:"type"`
			Seq  *int64 `json:"seq"`
		}
		require.NoError(t, json.Unmarshal(raw, &head))
		switch head.Type {
		case "token":
			tokenSinceDone = true
		case "done":
			tokenSinceDone = false
		case string(generated.WsFrameTypeTurnStarted):
			assert.Nil(t, head.Seq, "turn_started is live-only: never numbered")
			for _, kept := range journal {
				assert.NotEqual(t, string(kept), string(raw), "turn_started must never be journaled")
			}
			assert.False(t, tokenSinceDone, "turn_started must precede every token of its turn (live index %d)", i)
			doneAfter := false
			for _, later := range live[i+1:] {
				if len(framesOfType(t, [][]byte{later}, "done")) == 1 {
					doneAfter = true
					break
				}
			}
			assert.True(t, doneAfter, "the named turn's done must follow its turn_started (live index %d)", i)
		}
	}
}

// wu22GatewayTurnStarted decodes the live turn_started frames, in order.
func wu22GatewayTurnStarted(t *testing.T, live [][]byte) []generated.TurnStartedFrame {
	t.Helper()
	raws := framesOfType(t, live, string(generated.WsFrameTypeTurnStarted))
	out := make([]generated.TurnStartedFrame, 0, len(raws))
	for _, raw := range raws {
		var frame generated.TurnStartedFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		out = append(out, frame)
	}
	return out
}

// wu22AssertOneTurnNamed pins that the single engine turn was named exactly
// once to the bound tab, for this chat, by the agent that ran it, and that the
// name matches the turn its done frame closes.
func wu22AssertOneTurnNamed(t *testing.T, live [][]byte, sid string) {
	t.Helper()
	started := wu22GatewayTurnStarted(t, live)
	require.Len(t, started, 1, "one engine turn, named exactly once to the bound tab")
	assert.Equal(t, sid, started[0].SessionId)
	assert.Equal(t, "mia", started[0].ActiveTurn.AgentId)
	assert.NotEmpty(t, started[0].ActiveTurn.TurnId)
	_, err := time.Parse(time.RFC3339, started[0].ActiveTurn.StartedAt)
	assert.NoError(t, err, "started_at is RFC3339")
	done := wu22GatewayDoneFrames(t, live)
	require.Len(t, done, 1)
	require.NotNil(t, done[0].TurnId, "instrument: the live done names its turn")
	assert.Equal(t, *done[0].TurnId, started[0].ActiveTurn.TurnId, "turn_started names the turn that done closes")
}

func wu22GatewayDoneFrames(t *testing.T, frames [][]byte) []generated.DoneFrame {
	t.Helper()
	doneRaw := framesOfType(t, frames, "done")
	done := make([]generated.DoneFrame, 0, len(doneRaw))
	for _, raw := range doneRaw {
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
	assert.Empty(t, wu22GatewayTurnStarted(t, live), "a bare streamer finalize runs no engine turn, so nothing names one")
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
	assert.Empty(t, wu22GatewayTurnStarted(t, live), "a bare streamer finalize runs no engine turn, so nothing names one")
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
