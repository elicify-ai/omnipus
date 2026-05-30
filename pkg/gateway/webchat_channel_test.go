//go:build !cgo

// This test file uses //go:build !cgo so it compiles when CGO is disabled.
// When CGO is enabled, pkg/gateway imports pkg/channels/matrix which requires
// the libolm system library (olm/olm.h). If that library is installed,
// remove this build constraint and run tests normally.

package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapicom-ai/omnipus/pkg/bus"
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
