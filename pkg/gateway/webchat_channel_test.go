package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// TestWebchatChannel_SendSkipsWhenStreamed verifies that webchatChannel.Send is a no-op
// when the chatID has been marked as streamed via markStreamed.
// BDD: Given a webchatChannel whose chatID "chat-streamed" has been marked as streamed,
// When Send is called with that chatID,
// Then no frame is placed on the wsConn's sendCh (the response is already delivered).
// Traces to: pkg/gateway/webchat_channel.go — webchatChannel.Send alreadyStreamed guard
func TestWebchatChannel_SendSkipsWhenStreamed(t *testing.T) {
	t.Helper()

	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	wc := makeTestConn()

	chatID := "chat-streamed"

	// Register the connection in the handler's session map so Send can look it up.
	handler.mu.Lock()
	handler.sessions[chatID] = wc
	handler.mu.Unlock()

	ch := newWebchatChannel(handler)

	// Mark the chatID as already streamed — this simulates wsStreamer.Finalize having run.
	ch.markStreamed(chatID)

	// Call Send — it must detect alreadyStreamed and return without writing to sendCh.
	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  chatID,
		Content: "should not appear",
	})

	// Send returns nil (no error) because the streamed path is a deliberate no-op.
	assert.NoError(t, err, "Send must return nil when response was already streamed")

	// Verify that no frame was placed on sendCh.
	select {
	case frame := <-wc.sendCh:
		t.Fatalf("unexpected frame on sendCh — Send must be a no-op when streamed, got: %s", string(frame))
	case <-time.After(100 * time.Millisecond):
		// Correct — sendCh must remain empty.
	}
}

// TestWebchatChannel_SendBroadcastsToSecondAttachedTab verifies that an outbound
// response is delivered to every connection bound to the same session, not just
// the originating chatID. Underpins the attach-during-active-turn E2E test
// (#133): when a second browser tab attaches to a session mid-turn, the final
// token/done frames must reach it via session-id matching even though the
// turn was triggered by the first tab's chatID.
func TestWebchatChannel_SendBroadcastsToSecondAttachedTab(t *testing.T) {
	t.Helper()

	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	originConn := makeTestConn()
	attachedConn := makeTestConn()

	const (
		originChat = "chat-origin"
		secondChat = "chat-second"
		sessionID  = "session-shared"
	)

	handler.mu.Lock()
	handler.sessions[originChat] = originConn
	handler.sessions[secondChat] = attachedConn
	handler.sessionIDs[originChat] = sessionID
	handler.sessionIDs[secondChat] = sessionID
	handler.mu.Unlock()

	ch := newWebchatChannel(handler)
	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  originChat,
		Content: "shared response body",
	})
	assert.NoError(t, err)

	// Both connections must receive token + done frames.
	for _, conn := range []*wsConn{originConn, attachedConn} {
		gotToken := false
		gotDone := false
		for i := 0; i < 2; i++ {
			select {
			case frame := <-conn.sendCh:
				switch {
				case bytesContains(frame, "\"type\":\"token\""):
					gotToken = true
				case bytesContains(frame, "\"type\":\"done\""):
					gotDone = true
				}
			case <-time.After(100 * time.Millisecond):
				t.Fatalf("connection did not receive expected frame %d", i)
			}
		}
		assert.True(t, gotToken, "each session-attached connection must receive the token frame")
		assert.True(t, gotDone, "each session-attached connection must receive the done frame")
	}
}

// TestWebchatSend_BySessionID_ZeroConnsSucceeds proves ADR-082 D6/FR-012: a
// webchat Send with zero bound connections MUST succeed (no ErrSendFailed,
// no drop notice) — the content is already durable in the transcript and
// will replay on the next attach_session. This closes E5 (keeper-originated
// turns whose only connection has since disconnected/reconnected elsewhere
// used to fail twice per fire).
func TestWebchatSend_BySessionID_ZeroConnsSucceeds(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	ch := newWebchatChannel(handler)

	// No connection registered anywhere for either the chat id or the
	// session id — simulating a keeper follow-up dispatched after every
	// viewer disconnected.
	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:    "chat-nobody-here",
		SessionID: "session-nobody-here",
		Content:   "the answer, delivered to nobody",
	})
	assert.NoError(t, err, "Send with zero bound connections must succeed, not return ErrSendFailed")
}

// TestWebchatSend_ResolvesBySessionIDFirst proves the D6 resolution order:
// msg.SessionID is consulted BEFORE the chatID→sessionID fallback — a stale
// ChatID (E5: the goal keeper's captured GoalRouteChatID from an
// since-reconnected-elsewhere session) must not prevent delivery when the
// correct, current session id is supplied directly.
func TestWebchatSend_ResolvesBySessionIDFirst(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	liveConn := makeTestConn()
	const (
		staleChatID = "chat-stale-from-old-connection"
		liveChatID  = "chat-live-after-reconnect"
		sessionID   = "session-that-moved-connections"
	)
	// Only the LIVE chat id is currently bound to the session — the stale
	// chat id (what a keeper dispatch would carry) is not registered at all.
	handler.mu.Lock()
	handler.sessions[liveChatID] = liveConn
	handler.sessionIDs[liveChatID] = sessionID
	handler.mu.Unlock()

	ch := newWebchatChannel(handler)
	err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:    staleChatID,
		SessionID: sessionID,
		Content:   "keeper follow-up after reconnect",
	})
	require.NoError(t, err)

	select {
	case frame := <-liveConn.sendCh:
		assert.True(t, bytesContains(frame, "\"type\":\"token\""))
	case <-time.After(2 * time.Second):
		t.Fatal("the live, session-bound connection must receive the message despite the stale chat id")
	}
}

// TestFix_CR7_SendMedia_ZeroConnsSucceeds proves the ADR-082 review CR7 fix:
// SendMedia with zero bound connections must succeed (nil, not
// channels.ErrSendFailed) — matching Send's ADR-082 D6 "zero bound
// connections is not a failure" semantics exactly. The media itself is
// durable via the caller's own toolResult.Media references; returning an
// error here used to make pkg/agent/loop.go's tool-media-delivery block
// REPLACE the tool result with a plain error, discarding the media
// reference from the transcript entirely.
func TestFix_CR7_SendMedia_ZeroConnsSucceeds(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	ch := newWebchatChannel(handler)
	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID:    "chat-nobody-here",
		SessionID: "session-nobody-here",
		Parts:     []bus.MediaPart{{Ref: "media://abc123", Type: "image"}},
	})
	assert.NoError(t, err, "SendMedia with zero bound connections must succeed, not return ErrSendFailed")
}

// TestFix_CR7_SendMedia_ResolvesBySessionAfterReconnect proves SendMedia now
// resolves via the SAME session-bound connection set Send uses: a stale
// ChatID (the connection that originally triggered the tool call, since
// reconnected under a different chatID per ADR-082 D2) must not prevent
// delivery when the current session id resolves to a live connection.
func TestFix_CR7_SendMedia_ResolvesBySessionAfterReconnect(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	liveConn := makeTestConn()
	const (
		staleChatID = "chat-stale-media-origin"
		liveChatID  = "chat-live-media-after-reconnect"
		sessionID   = "session-media-moved-connections"
	)
	handler.mu.Lock()
	handler.sessions[liveChatID] = liveConn
	handler.sessionIDs[liveChatID] = sessionID
	handler.mu.Unlock()

	ch := newWebchatChannel(handler)
	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID:    staleChatID,
		SessionID: sessionID,
		Parts:     []bus.MediaPart{{Ref: "media://xyz", Type: "image", Filename: "shot.png"}},
	})
	require.NoError(t, err)

	select {
	case frame := <-liveConn.sendCh:
		assert.True(t, bytesContains(frame, "\"type\":\"media\""))
	case <-time.After(2 * time.Second):
		t.Fatal("the live, session-bound connection must receive the media despite the stale chat id")
	}
}

// TestFix_CR7_SendMedia_BroadcastsToEverySessionBoundConnection proves the
// unification with Send's own fan-out: a second tab attached to the same
// session must also receive the media frame.
func TestFix_CR7_SendMedia_BroadcastsToEverySessionBoundConnection(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	originConn := makeTestConn()
	secondConn := makeTestConn()
	const (
		originChat = "chat-media-origin"
		secondChat = "chat-media-second"
		sessionID  = "session-media-shared"
	)
	handler.mu.Lock()
	handler.sessions[originChat] = originConn
	handler.sessions[secondChat] = secondConn
	handler.sessionIDs[originChat] = sessionID
	handler.sessionIDs[secondChat] = sessionID
	handler.mu.Unlock()

	ch := newWebchatChannel(handler)
	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID:    originChat,
		SessionID: sessionID,
		Parts:     []bus.MediaPart{{Ref: "media://shared", Type: "image"}},
	})
	require.NoError(t, err)

	for _, conn := range []*wsConn{originConn, secondConn} {
		select {
		case frame := <-conn.sendCh:
			assert.True(t, bytesContains(frame, "\"type\":\"media\""))
		case <-time.After(2 * time.Second):
			t.Fatal("every session-bound connection must receive the media frame")
		}
	}
}

func TestWebchatChannel_MediaRefURL(t *testing.T) {
	assert.Equal(t, "/api/v1/media/workspace/ws-1/abc", mediaRefURL("media://workspace/ws-1/abc"))
	assert.Equal(t, "/api/v1/media/uuid-1", mediaRefURL("media://uuid-1"))
}

func bytesContains(haystack []byte, needle string) bool {
	return len(haystack) >= len(needle) && stringIndex(string(haystack), needle) >= 0
}

func stringIndex(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
