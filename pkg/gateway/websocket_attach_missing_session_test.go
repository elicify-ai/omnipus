package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Oracles: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1090/receipts/combo-silent.md,
// COMBO-SF-1, and /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1090/design-1090.md,
// D7. A deleted chat's early attach refusal identifies the validated session_id,
// but must not fabricate a first-message outcome, mutate bindings, recreate the
// chat, or publish a turn. Existing-chat replay is the passing instrument control.
// Real: authenticated WebSocket dispatch, resolver, filesystem store, and replay.
// No production hooks or reducer mocks. Numeric/message-size boundaries and live
// browser acceptance are outside this narrow regression. GREEN and mutations are
// deferred to CHECK (probes: omit error correlation, bind the missing chat, or
// fabricate a first-message receipt).

func attachSF1Socket(t *testing.T, handler *WSHandler) *websocket.Conn {
	t.Helper()
	const token = "combo-sf1-authenticated-attach-token"
	t.Setenv("OMNIPUS_BEARER_TOKEN", token)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.WriteJSON(generated.AuthFrame{
		Type: string(generated.WsFrameTypeAuth), Token: token,
	}))

	// The connection-open session_state is a positive authentication barrier:
	// no attach is sent until the real server has accepted this token.
	var opened generated.SessionStateFrame
	require.NoError(t, json.Unmarshal(readAttachSF1Frame(t, conn), &opened))
	require.Equal(t, string(generated.WsFrameTypeSessionState), opened.Type,
		"fixture: valid token must reach the authenticated connection-open frame")
	require.Nil(t, opened.SessionId, "fixture: authentication alone must not attach a chat")
	return conn
}

func readAttachSF1Frame(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()
	// This existing package failsafe bounds a missing event, not its latency.
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(busDeliveryTimeout)))
	_, raw, err := conn.ReadMessage()
	require.NoError(t, err, "real attach response must arrive")
	return raw
}

func seedAttachSF1Session(t *testing.T, store *session.UnifiedStore) (string, session.TranscriptEntry) {
	t.Helper()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	// A fixed timestamp and whitespace-bearing text make replay exact and
	// independent of the machine clock or an observed implementation result.
	entry := session.TranscriptEntry{
		ID: "combo-sf1-saved-entry", ClientMessageID: "combo-sf1-original-client",
		Role: "user", Content: "Keep this saved first message\nand its trailing spaces.  ",
		AgentID: "mia", Timestamp: time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC),
	}
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, entry))
	return meta.ID, entry
}

func TestWS_AttachDeletedSession_CorrelatesRefusalWithoutSideEffects(t *testing.T) {
	handler, msgBus, al := newTestWSHandler(t)
	store := al.GetSessionStore()
	require.NotNil(t, store)
	deletedID, entry := seedAttachSF1Session(t, store)
	saved, err := store.ReadTranscript(deletedID)
	require.NoError(t, err)
	require.Equal(t, []session.TranscriptEntry{entry}, saved,
		"fixture: the original first user entry really was saved before another tab deleted its chat")
	conn := attachSF1Socket(t, handler)

	// Deterministic deletion interleave: acknowledgement/reconnect remembered
	// this valid ID, but the actual store no longer owns it when attach arrives.
	require.NoError(t, store.DeleteSession(deletedID))
	require.Nil(t, handler.resolveSessionStore(deletedID), "fixture: deleted chat must reach the early missing-store refusal")
	require.NoError(t, conn.WriteJSON(generated.AttachSessionFrame{
		Type: string(generated.WsFrameTypeAttachSession), SessionId: deletedID,
	}))
	raw := readAttachSF1Frame(t, conn)
	var refusal generated.ErrorFrame
	require.NoError(t, json.Unmarshal(raw, &refusal))
	require.Equal(t, string(generated.WsFrameTypeError), refusal.Type)
	require.Equal(t, "session not found", refusal.Message,
		"COMBO-SF-1: exercise the missing-store refusal, not ID validation or authentication")
	expected, err := json.Marshal(generated.ErrorFrame{
		Type: string(generated.WsFrameTypeError), Message: "session not found", SessionId: &deletedID,
	})
	require.NoError(t, err)
	assert.JSONEq(t, string(expected), string(raw),
		"COMBO-SF-1: early attach refusal must carry the validated session_id and no invented client ID, receipt, or cursor")

	// A protocol pong is a same-read-loop barrier, not a sleep-based absence
	// check. Any fabricated session_started/snapshot/receipt before it fails.
	require.NoError(t, conn.WriteJSON(generated.PingFrame{Type: string(generated.WsFrameTypePing)}))
	pong, err := json.Marshal(generated.PongFrame{Type: string(generated.WsFrameTypePong)})
	require.NoError(t, err)
	assert.JSONEq(t, string(pong), string(readAttachSF1Frame(t, conn)),
		"COMBO-SF-1: refusal leaves the socket usable without acknowledging or replaying a nonexistent chat")

	handler.mu.Lock()
	assert.Empty(t, handler.sessionIDs, "COMBO-SF-1: refusal cannot bind any connection/session ID")
	assert.Empty(t, handler.taskChatIDs, "COMBO-SF-1: refusal cannot change live-event routing")
	assert.Len(t, handler.sessions, 1, "COMBO-SF-1: the authenticated connection stays open")
	handler.mu.Unlock()
	handler.hubs.mu.RLock()
	assert.Empty(t, handler.hubs.m, "COMBO-SF-1: missing attach cannot create a session hub")
	handler.hubs.mu.RUnlock()
	remaining, err := store.ListSessions()
	require.NoError(t, err)
	assert.Empty(t, remaining, "COMBO-SF-1: attach cannot remint the deliberately deleted chat")
	assert.Nil(t, handler.resolveSessionStore(deletedID), "COMBO-SF-1: deleted transcript must remain unavailable")
	assert.Len(t, msgBus.InboundChan(), 0, "COMBO-SF-1: an attach refusal cannot append or start a new answer turn")
}

func TestWS_AttachExistingSession_ReplaysSavedEntry(t *testing.T) {
	handler, msgBus, al := newTestWSHandler(t)
	store := al.GetSessionStore()
	require.NotNil(t, store)
	sid, entry := seedAttachSF1Session(t, store)
	conn := attachSF1Socket(t, handler)
	require.NoError(t, conn.WriteJSON(generated.AttachSessionFrame{
		Type: string(generated.WsFrameTypeAttachSession), SessionId: sid,
	}))

	var snapshot generated.SessionSnapshotFrame
	require.NoError(t, json.Unmarshal(readAttachSF1Frame(t, conn), &snapshot))
	require.Equal(t, string(generated.WsFrameTypeSessionSnapshot), snapshot.Type,
		"control: an existing chat must take the real cursor-free snapshot attach path")
	require.Equal(t, sid, snapshot.SessionId)
	var state generated.SessionStateFrame
	require.NoError(t, json.Unmarshal(readAttachSF1Frame(t, conn), &state))
	require.Equal(t, string(generated.WsFrameTypeSessionState), state.Type)
	require.Equal(t, &sid, state.SessionId)

	var replayed []generated.ReplayMessageFrame
	for {
		raw := readAttachSF1Frame(t, conn)
		var frame generated.ReplayMessageFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		require.NotEqual(t, string(generated.WsFrameTypeError), frame.Type, "control: existing attach cannot be refused: %s", raw)
		if frame.Type == string(generated.WsFrameTypeReplayMessage) {
			replayed = append(replayed, frame)
		}
		if frame.Type == string(generated.WsFrameTypeCatchUpComplete) {
			var complete generated.CatchUpCompleteFrame
			require.NoError(t, json.Unmarshal(raw, &complete))
			require.Equal(t, sid, complete.SessionId)
			require.Equal(t, "snapshot", complete.Mode)
			break
		}
	}
	require.Len(t, replayed, 1, "control: replay returns exactly the one genuinely saved entry")
	assert.Equal(t, "user", replayed[0].Role)
	assert.Equal(t, sid, replayed[0].SessionId)
	assert.Equal(t, &entry.ID, replayed[0].Id)
	assert.Equal(t, &entry.ClientMessageID, replayed[0].ClientMessageId)
	assert.Equal(t, entry.Content, replayed[0].Content)
	stored, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	assert.Equal(t, []session.TranscriptEntry{entry}, stored, "control: attachment must not append the saved first entry again")
	assert.Len(t, msgBus.InboundChan(), 0, "control: replay must not admit another answer turn")
}
