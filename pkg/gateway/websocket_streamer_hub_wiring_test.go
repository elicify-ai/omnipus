// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// websocket_streamer_hub_wiring_test.go — #823 catch-up redesign: proves
// wsStreamer.Update actually routes every token through WSHandler.hubs
// (ws_session_hub.go) for numbering, and stamps turn_id/message_id, rather
// than just not regressing the pre-#823 delivery behavior (already covered
// by the existing ws_streamer_session_bound_test.go suite, unmodified and
// still green).

package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWsStreamer_Update_SeqComesFromTheSessionHub proves the token path is
// wired to the hub (BE-DESIGN.md §1.1/§1.2), not just marshaling a frame
// directly: three Update calls (with zero bound connections — see
// TestWSStreamer_ZeroListeners_NoBackoff for why that must cost no backoff)
// must still leave three journaled, monotonically-numbered entries in
// h.hubs' hub for this session, and a fourth Update delivered to a NOW-bound
// connection must carry seq 4 — proving the hub's counter kept incrementing
// even while nothing was listening (H1's exact guarantee, exercised through
// the real wiring instead of the standalone hub tests).
func TestWsStreamer_Update_SeqComesFromTheSessionHub(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	const sessionID = "session-hub-wiring-seq"
	s := &wsStreamer{
		sessionID: sessionID,
		chatID:    "chat-no-conn-yet",
		channel:   newWebchatChannel(handler),
	}

	// Zero connections bound — mirrors the design's H1 invariant: numbering
	// must not depend on any connection being attached.
	require.NoError(t, s.Update(context.Background(), "a"))
	require.NoError(t, s.Update(context.Background(), "b"))
	require.NoError(t, s.Update(context.Background(), "c"))

	hub := handler.hubs.lookup(sessionID)
	require.NotNil(t, hub, "Update must have created the session's hub even with zero listeners")
	if got := hub.snapshotHead(); got != 3 {
		t.Fatalf("hub head = %d, want 3 (three tokens published with 0 conns)", got)
	}

	// Now bind a connection and send a fourth token — it must be delivered
	// with seq 4, proving the counter kept incrementing while unobserved.
	conn := makeTestConn()
	bindTestConnToSession(handler, "chat-no-conn-yet", sessionID, conn)
	require.NoError(t, s.Update(context.Background(), "d"))

	frame := readTokenFrame(t, conn.sendCh)
	assert.Equal(t, "d", frame.Content)
	require.NotNil(t, frame.Seq, "TokenFrame.seq must be set once the hub is wired")
	assert.Equal(t, int64(4), *frame.Seq)
}

// TestWsStreamer_Update_StampsTurnIDAndMessageID proves SetTurnID/
// SetMessageID (the latter added by the #823 Lane B cherry-pick,
// pkg/agent/turn_stream.go's stampStreamerMessageID) land on the wire
// TokenFrame, not just on the persisted transcript entry.
func TestWsStreamer_Update_StampsTurnIDAndMessageID(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	const sessionID = "session-hub-wiring-ids"
	conn := makeTestConn()
	bindTestConnToSession(handler, "chat-ids", sessionID, conn)

	s := &wsStreamer{
		sessionID: sessionID,
		chatID:    "chat-ids",
		channel:   newWebchatChannel(handler),
	}
	s.SetTurnID("turn-42")
	s.SetMessageID("msg-99")

	require.NoError(t, s.Update(context.Background(), "hello"))
	frame := readTokenFrame(t, conn.sendCh)

	require.NotNil(t, frame.TurnId)
	assert.Equal(t, "turn-42", *frame.TurnId)
	require.NotNil(t, frame.MessageId)
	assert.Equal(t, "msg-99", *frame.MessageId)
}

// TestWsStreamer_SetMessageID_EmptyIsNoop mirrors
// TestWsStreamer_SetTurnID_EmptyIsNoop (websocket_producer_agent_id_test.go)
// exactly: an empty messageID must not blank out an already-stamped value.
func TestWsStreamer_SetMessageID_EmptyIsNoop(t *testing.T) {
	s := &wsStreamer{}
	s.SetMessageID("first")
	s.SetMessageID("")
	s.statsMu.Lock()
	got := s.messageID
	s.statsMu.Unlock()
	assert.Equal(t, "first", got, "an empty SetMessageID call must not clear an already-stamped id")
}
