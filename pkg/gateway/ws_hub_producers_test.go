// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// readFramesUntil reads raw frames from wc until pred has matched want
// frames, returning every frame read (matching or not) in arrival order.
func readFramesUntil(t *testing.T, wc *wsConn, want int, pred func(map[string]any) bool) [][]byte {
	t.Helper()
	var all [][]byte
	matched := 0
	deadline := time.After(3 * time.Second)
	for matched < want {
		select {
		case raw := <-wc.sendCh:
			all = append(all, raw)
			var m map[string]any
			require.NoError(t, json.Unmarshal(raw, &m))
			if pred(m) {
				matched++
			}
		case <-deadline:
			t.Fatalf("timed out: matched %d of %d frames; read so far: %s", matched, want, joinFrames(all))
		}
	}
	return all
}

func joinFrames(frames [][]byte) string {
	out := ""
	for _, f := range frames {
		out += string(f) + "\n"
	}
	return out
}

func framesOfType(t *testing.T, frames [][]byte, frameType string) [][]byte {
	t.Helper()
	var out [][]byte
	for _, raw := range frames {
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		if m["type"] == frameType {
			out = append(out, raw)
		}
	}
	return out
}

func frameSeq(t *testing.T, raw []byte) (int64, bool) {
	t.Helper()
	var m struct {
		Seq *int64 `json:"seq"`
	}
	require.NoError(t, json.Unmarshal(raw, &m))
	if m.Seq == nil {
		return 0, false
	}
	return *m.Seq, true
}

// mintSessionFor sends a first message from wc on chatID so the handler mints
// a session, and returns its id.
func mintSessionFor(t *testing.T, h *WSHandler, chatID string, wc *wsConn) string {
	t.Helper()
	h.handleChatMessageWithClientID(context.Background(), chatID, "", "first", "", nil,
		"", "", false, "client-mint", nil, wc)
	frames := readMessageStatusFrames(t, wc, 1)
	require.NotEmpty(t, frames[0].SessionId)
	return frames[0].SessionId
}

// TestHub_H13_UserMessageAndTicks_ReachEveryTab pins BE-DESIGN.md §1.2/§4.7
// (founder decision Q1) and H13: a user's message is echoed as a numbered
// user_message frame, and its received/working ticks are numbered too — all
// published once through the session hub and delivered byte-identically to
// EVERY tab bound to the session, not only to the sender. Before #823 a
// second tab never saw the user's message live, and the ticks went to the
// sender's connection only.
func TestHub_H13_UserMessageAndTicks_ReachEveryTab(t *testing.T) {
	h, _ := newTestWSHandlerForModelName(t, bus.NewMessageBus())
	sender := makeTestConn()
	sender.sendCh = make(chan []byte, 256)
	sid := mintSessionFor(t, h, "chat-sender", sender)
	for len(sender.sendCh) > 0 {
		<-sender.sendCh
	}
	bindTestConnToSession(h, "chat-sender", sid, sender)
	other := makeTestConn()
	other.sendCh = make(chan []byte, 256)
	bindTestConnToSession(h, "chat-other", sid, other)

	h.handleChatMessageWithClientID(context.Background(), "chat-sender", sid, "second message", "", nil,
		"", "", false, "client-2", nil, sender)

	isReceived := func(m map[string]any) bool {
		return m["type"] == "message_status" && m["state"] == "received" && m["client_message_id"] == "client-2"
	}
	senderFrames := readFramesUntil(t, sender, 1, isReceived)
	otherFrames := readFramesUntil(t, other, 1, isReceived)

	for name, frames := range map[string][][]byte{"sender": senderFrames, "other tab": otherFrames} {
		echoes := framesOfType(t, frames, "user_message")
		require.Len(t, echoes, 1, "%s must receive exactly one user_message echo", name)
		var echo generated.UserMessageFrame
		require.NoError(t, json.Unmarshal(echoes[0], &echo))
		assert.Equal(t, "second message", echo.Content)
		require.NotNil(t, echo.ClientMessageId)
		assert.Equal(t, "client-2", *echo.ClientMessageId)
		assert.NotEmpty(t, echo.Id)
		echoSeq, ok := frameSeq(t, echoes[0])
		require.True(t, ok, "%s: user_message must carry the hub's seq", name)
		ticks := framesOfType(t, frames, "message_status")
		require.NotEmpty(t, ticks)
		tickSeq, ok := frameSeq(t, ticks[len(ticks)-1])
		require.True(t, ok, "%s: the received tick must carry the hub's seq", name)
		assert.Equal(t, echoSeq+1, tickSeq, "%s: user_message comes directly before its received tick", name)
	}
	assert.Equal(t, string(framesOfType(t, senderFrames, "user_message")[0]),
		string(framesOfType(t, otherFrames, "user_message")[0]),
		"both tabs receive the identical user_message bytes (one publish)")

	// The persisted entry carries the client's message id (replay reconciles
	// the pending bubble with it) and the same id the live echo carried.
	entries, err := h.resolveSessionStore(sid).ReadTranscript(sid)
	require.NoError(t, err)
	var echo generated.UserMessageFrame
	require.NoError(t, json.Unmarshal(framesOfType(t, senderFrames, "user_message")[0], &echo))
	var found bool
	for _, e := range entries {
		if e.ID == echo.Id {
			found = true
			assert.Equal(t, "client-2", e.ClientMessageID)
		}
	}
	assert.True(t, found, "the live user_message id must be the persisted entry's id")

	// "working" for the queued message reaches the other tab too (review
	// finding 12) — the pending entry no longer remembers a connection.
	_, ok := h.GetStreamer(context.Background(), "webchat", "chat-sender", sid)
	require.True(t, ok)
	readFramesUntil(t, other, 1, func(m map[string]any) bool {
		return m["type"] == "message_status" && m["state"] == "working"
	})
}

// TestHub_CancelStage_ReachesEveryTab_AndUnboundRequester pins BE-DESIGN.md
// §1.2's cancel_stage route: numbered through the session hub for every tab
// bound to the session, plus an unsequenced copy for a requesting tab that is
// not bound to that session (§1.4 alsoUnsequencedTo=conn).
func TestHub_CancelStage_ReachesEveryTab_AndUnboundRequester(t *testing.T) {
	h := makeMinimalHandler()
	tabA, chA := makeForwarderTestConn(16)
	tabB, chB := makeForwarderTestConn(16)
	bindTestConnToSession(h, "chat-a", "sess-cancel", tabA)
	bindTestConnToSession(h, "chat-b", "sess-cancel", tabB)
	requester, chR := makeForwarderTestConn(16)
	bindTestConnToSession(h, "chat-r", "sess-elsewhere", requester)

	h.sendCancelStageFrame(requester, "sess-cancel", "graceful")

	require.Len(t, chA, 1)
	require.Len(t, chB, 1)
	require.Len(t, chR, 1)
	a, b, r := <-chA, <-chB, <-chR
	assert.Equal(t, string(a), string(b), "bound tabs get identical numbered bytes")
	_, ok := frameSeq(t, a)
	assert.True(t, ok, "cancel_stage to a bound tab carries the hub's seq")
	_, ok = frameSeq(t, r)
	assert.False(t, ok, "the unbound requester's copy is unsequenced — it must never move a cursor")
	var f generated.CancelStageFrame
	require.NoError(t, json.Unmarshal(r, &f))
	assert.Equal(t, "graceful", f.Stage)
	assert.Equal(t, "sess-cancel", f.SessionId)
}

// TestFinalize_PersistsAnswerUnderItsLiveMessageID pins BE-DESIGN.md §6.3: the
// final streamed answer is persisted under the SAME id its live token/done
// frames carried, and replay exposes that id on replay_message.id — so a
// client can merge the persisted and live copies of one message by id.
// Before #823 Lane A, Finalize minted a fresh uuid for the entry.
func TestFinalize_PersistsAnswerUnderItsLiveMessageID(t *testing.T) {
	h, _ := newTestWSHandlerForModelName(t, bus.NewMessageBus())
	wc := makeTestConn()
	wc.sendCh = make(chan []byte, 64)
	sid := mintSessionFor(t, h, "chat-final", wc)

	st, ok := h.GetStreamer(context.Background(), "webchat", "chat-final", sid)
	require.True(t, ok)
	ws, ok := st.(*wsStreamer)
	require.True(t, ok)
	ws.SetTurnID("turn-final")
	ws.SetMessageID("msg-final-1")
	require.NoError(t, ws.Update(context.Background(), "the final answer"))
	require.NoError(t, ws.Finalize(context.Background(), "the final answer"))

	entries, err := h.resolveSessionStore(sid).ReadTranscript(sid)
	require.NoError(t, err)
	var persisted *session.TranscriptEntry
	for i := range entries {
		if entries[i].Role == "assistant" {
			persisted = &entries[i]
		}
	}
	require.NotNil(t, persisted, "the streamed answer must be persisted")
	assert.Equal(t, "msg-final-1", persisted.ID, "persisted under the live message_id")

	sink := &sliceSink{}
	_, err = streamReplay(t.Context(), sid, entries, computeReplayStats(entries), sink.emit, nil, nil, nil, nil)
	require.NoError(t, err)
	var sawReplayID, sawClientID bool
	for _, raw := range sink.frames {
		var f generated.ReplayMessageFrame
		require.NoError(t, json.Unmarshal(raw, &f))
		if f.Type != "replay_message" {
			continue
		}
		if f.Role == "assistant" && f.Id != nil && *f.Id == "msg-final-1" {
			sawReplayID = true
		}
		if f.Role == "user" && f.ClientMessageId != nil && *f.ClientMessageId == "client-mint" {
			sawClientID = true
		}
	}
	assert.True(t, sawReplayID, "replay_message.id must equal the live message_id")
	assert.True(t, sawClientID, "a replayed user message carries its client_message_id")
}

// TestChatMessage_RetriedClientMessageID_IsIdempotent pins #823 review item
// 6: a message the client re-sends because it never saw the server's
// acknowledgement (same session, same client_message_id) must NOT become a
// second user message and a second turn. The server answers the retry by
// re-sending that message's echo and its current status to the sender only
// (unsequenced — nothing is published twice to the session).
func TestChatMessage_RetriedClientMessageID_IsIdempotent(t *testing.T) {
	msgBus := bus.NewMessageBus()
	h, _ := newTestWSHandlerForModelName(t, msgBus)
	wc := makeTestConn()
	wc.sendCh = make(chan []byte, 256)
	sid := mintSessionFor(t, h, "chat-retry", wc)
	drainInbound := func() int {
		n := 0
		for {
			select {
			case <-msgBus.InboundChan():
				n++
			case <-time.After(200 * time.Millisecond):
				return n
			}
		}
	}
	require.Equal(t, 1, drainInbound(), "the minting message starts one turn")
	for len(wc.sendCh) > 0 {
		<-wc.sendCh
	}

	h.handleChatMessageWithClientID(context.Background(), "chat-retry", sid, "do the thing", "", nil,
		"", "", false, "client-retry", nil, wc)
	require.Equal(t, 1, drainInbound(), "the first send starts one turn")
	hub := h.hubs.lookup(sid)
	require.NotNil(t, hub)
	echoesBefore := len(journalFramesOfType(t, hub, "user_message"))
	for len(wc.sendCh) > 0 {
		<-wc.sendCh
	}

	// The client never saw the ack and retries the SAME message.
	h.handleChatMessageWithClientID(context.Background(), "chat-retry", sid, "do the thing", "", nil,
		"", "", false, "client-retry", nil, wc)
	assert.Equal(t, 0, drainInbound(), "a retried client_message_id must not start a second turn")
	assert.Equal(t, echoesBefore, len(journalFramesOfType(t, hub, "user_message")),
		"a retry publishes nothing new to the session")

	entries, err := h.resolveSessionStore(sid).ReadTranscript(sid)
	require.NoError(t, err)
	persisted := 0
	for _, e := range entries {
		if e.Role == "user" && e.ClientMessageID == "client-retry" {
			persisted++
		}
	}
	assert.Equal(t, 1, persisted, "the retried message is persisted exactly once")

	frames := readFramesUntil(t, wc, 1, func(m map[string]any) bool {
		return m["type"] == "message_status" && m["client_message_id"] == "client-retry"
	})
	echoes := framesOfType(t, frames, "user_message")
	require.Len(t, echoes, 1, "the sender gets the original echo back so its pending bubble resolves")
	_, hasSeq := frameSeq(t, echoes[0])
	assert.False(t, hasSeq, "the re-sent echo is unsequenced — it must never move a cursor")
	var status generated.MessageStatusFrame
	require.NoError(t, json.Unmarshal(framesOfType(t, frames, "message_status")[0], &status))
	assert.NotEqual(t, "failed", status.State, "an accepted message is never reported failed on retry")
}
