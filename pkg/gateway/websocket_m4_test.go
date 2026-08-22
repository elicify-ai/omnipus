// M4 WebSocket resilience tests.
//
// Covers three new behaviors introduced in the M4 milestone:
//   1. 5 MB read limit — frames exceeding wsMaxMessageBytes are rejected and
//      the connection is closed by gorilla/websocket.
//   2. Exponential-backoff send / droppedFrames counter — non-critical frames
//      are dropped after 3 attempts (immediate, 10 ms, 50 ms) when the send
//      channel is full; droppedFrames resets on a successful send.
//   3. Degraded-warning threshold — after droppedFramesWarnThreshold (20)
//      consecutive drops a "connection degraded" error frame is injected.

package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// M4-1: 5 MB message size limit (wsMaxMessageBytes / SetReadLimit)
// ---------------------------------------------------------------------------

// TestWSReadLimit_ConstantIs5MB verifies that wsMaxMessageBytes is exactly 5 MiB.
// BDD: Given the gateway WebSocket handler,
// When wsMaxMessageBytes is inspected,
// Then its value must be exactly 5 * 1024 * 1024 bytes.
// Traces to: pkg/gateway/websocket.go — const wsMaxMessageBytes
func TestWSReadLimit_ConstantIs5MB(t *testing.T) {
	const expected int64 = 5 * 1024 * 1024
	assert.Equal(t, expected, int64(wsMaxMessageBytes),
		"wsMaxMessageBytes must equal 5 MiB (5242880 bytes)")
}

// TestWSReadLimit_RejectsOversizedFrame verifies that sending a frame larger than
// wsMaxMessageBytes causes the server to close the connection.
// BDD: Given an authenticated WebSocket connection,
// When the client sends a frame whose payload exceeds 5 MB,
// Then the server closes the connection (ReadMessage returns an error on the client).
// Traces to: pkg/gateway/websocket.go — readLoop SetReadLimit + CloseMessageTooBig handler
func TestWSReadLimit_RejectsOversizedFrame(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	// Enable dev-mode bypass so authenticateWS passes on the first auth frame
	// without requiring a real token or OMNIPUS_BEARER_TOKEN to be set.
	handler.agentLoop.GetConfig().Gateway.DevModeBypass = true

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	// Authenticate first — authenticateWS always reads the first frame.
	sendWSAuthFrameDevMode(t, conn)

	// Build a raw text payload that exceeds the 5 MB limit by 1 byte.
	// gorilla/websocket enforces the limit on the raw frame payload, so we can
	// send random bytes wrapped in the minimal {"type":"message","content":"..."} envelope.
	prefix := []byte(`{"type":"message","content":"`)
	suffix := []byte(`"}`)
	// The oversized portion: fill the content field so the total JSON is > 5 MB.
	oversizedContent := make([]byte, wsMaxMessageBytes)
	for i := range oversizedContent {
		oversizedContent[i] = 'A'
	}
	payload := append(append(prefix, oversizedContent...), suffix...)

	conn.SetWriteDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	// WriteMessage may succeed at the client side; the server-side ReadMessage will
	// then hit the SetReadLimit and close the connection.
	_ = conn.WriteMessage(websocket.TextMessage, payload)

	// The server must close the connection after receiving the oversized frame.
	// Drain any legitimate server-initiated frames (e.g. session_state emitted on
	// connect — FR-052, FR-081) before asserting the close error.  The connection
	// must eventually close with an error.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	var err error
	for {
		var msgType int
		msgType, _, err = conn.ReadMessage()
		if err != nil {
			// Connection closed — expected outcome.
			break
		}
		// Received a legitimate server frame (session_state, error notice, etc.).
		// Ignore it and keep reading; the server will close after processing the
		// oversized frame.
		_ = msgType
	}
	assert.Error(t, err,
		"connection must be closed by the server after receiving a frame larger than 5 MB")
}

// TestWSReadLimit_AcceptsSmallFrame verifies that normal-sized frames do not trigger
// the size-limit guard (boundary / happy-path test).
// BDD: Given an authenticated WebSocket connection,
// When the client sends a small text frame well under 5 MB,
// Then the server does NOT close the connection.
// Traces to: pkg/gateway/websocket.go — readLoop SetReadLimit
func TestWSReadLimit_AcceptsSmallFrame(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	handler.agentLoop.GetConfig().Gateway.DevModeBypass = true

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	// Normal-sized ping frame (< 5 MB) — must be accepted without closing.
	// Use "ping" type (not "message") to avoid triggering session creation which
	// writes to the temp dir and causes a cleanup race in the test.
	pingFrame := wsClientFrameTestHelper{Type: "ping"}
	pingData, err := json.Marshal(pingFrame)
	require.NoError(t, err)
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, pingData),
		"small ping frame must be written without error")

	// Connection must remain open: send a second frame successfully.
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	pingFrame2 := wsClientFrameTestHelper{Type: "ping"}
	pingData2, err := json.Marshal(pingFrame2)
	require.NoError(t, err)
	err = conn.WriteMessage(websocket.TextMessage, pingData2)
	assert.NoError(t, err, "connection must remain open after a small frame")
}

// ---------------------------------------------------------------------------
// M4-2: Exponential-backoff send / droppedFrames counter (sendConnGenFrame)
// ---------------------------------------------------------------------------

// TestSendConnGenFrame_ResetOnSuccess verifies that droppedFrames is reset to 0
// after a successful non-critical send.
// BDD: Given a wsConn with a pre-set droppedFrames=5 and a drained sendCh,
// When sendConnGenFrame delivers a non-critical "token" frame successfully,
// Then wc.droppedFrames is 0.
// Traces to: pkg/gateway/websocket.go — sendRawFrameBytes default branch droppedFrames reset
func TestSendConnGenFrame_ResetOnSuccess(t *testing.T) {
	wc := makeTestConn()
	// Pre-populate so we can confirm the reset.
	wc.droppedFrames.Store(5)

	sendConnGenFrame(wc, string(generated.WsFrameTypeToken), generated.TokenFrame{
		Type:      string(generated.WsFrameTypeToken),
		Content:   "hello",
		SessionId: "sess-test",
	})

	assert.Equal(t, int32(0), wc.droppedFrames.Load(),
		"droppedFrames must be reset to 0 after a successful non-critical send")
}

// TestSendConnGenFrame_IncrementsDroppedFramesOnFullChannel verifies that droppedFrames
// increments when all three backoff attempts are exhausted.
// BDD: Given a wsConn with a zero-capacity send channel (always full),
// When sendConnGenFrame is called with a non-critical "token" frame,
// Then wc.droppedFrames increments by 1.
// Traces to: pkg/gateway/websocket.go — sendRawFrameBytes backoff exhaustion counter
func TestSendConnGenFrame_IncrementsDroppedFramesOnFullChannel(t *testing.T) {
	// Zero-capacity channel: every send attempt fails immediately.
	wc := &wsConn{
		sendCh: make(chan []byte),
		doneCh: make(chan struct{}),
	}

	before := wc.droppedFrames.Load()
	// sendConnGenFrame spends up to ~60 ms on backoff attempts — acceptable in a unit test.
	sendConnGenFrame(wc, string(generated.WsFrameTypeToken), generated.TokenFrame{
		Type:      string(generated.WsFrameTypeToken),
		Content:   "overflow",
		SessionId: "sess-test",
	})

	assert.Equal(t, before+1, wc.droppedFrames.Load(),
		"droppedFrames must increment by 1 after all three backoff attempts fail")
}

// TestSendConnGenFrame_CriticalFrameBypassesBackoff verifies that "error" frames use
// the blocking critical path and do not increment droppedFrames.
// This is the differentiation test: critical vs non-critical frame types must produce
// different channel-send behavior.
// BDD: Given a wsConn with a drained sendCh,
// When sendConnGenFrame is called with a critical "error" frame,
// Then the frame is enqueued and droppedFrames remains 0.
// BDD: Given a wsConn with a drained sendCh,
// When sendConnGenFrame is called with a non-critical "token" frame with different content,
// Then the frame is enqueued and its content differs from the "error" frame.
// Traces to: pkg/gateway/websocket.go — sendRawFrameBytes critical vs non-critical paths
func TestSendConnGenFrame_CriticalFrameBypassesBackoff(t *testing.T) {
	// Critical "error" frame.
	wcCrit := makeTestConn()
	sendConnGenFrame(wcCrit, string(generated.WsFrameTypeError), generated.ErrorFrame{
		Type:    string(generated.WsFrameTypeError),
		Message: "critical-message-A",
	})

	select {
	case raw := <-wcCrit.sendCh:
		var f replayFrameDecoder
		require.NoError(t, json.Unmarshal(raw, &f), "critical frame must be valid JSON")
		assert.Equal(t, "error", f.Type)
		assert.Equal(t, "critical-message-A", f.Message,
			"critical frame content must match exactly — not hardcoded")
	default:
		t.Fatal("critical 'error' frame was not enqueued on sendCh")
	}
	assert.Equal(t, int32(0), wcCrit.droppedFrames.Load(), "critical frame must not increment droppedFrames")

	// Non-critical "token" frame — different type, different content.
	wcNonCrit := makeTestConn()
	sendConnGenFrame(wcNonCrit, string(generated.WsFrameTypeToken), generated.TokenFrame{
		Type:      string(generated.WsFrameTypeToken),
		Content:   "stream-content-B",
		SessionId: "sess-test",
	})

	select {
	case raw := <-wcNonCrit.sendCh:
		var f replayFrameDecoder
		require.NoError(t, json.Unmarshal(raw, &f), "non-critical frame must be valid JSON")
		assert.Equal(t, "token", f.Type,
			"non-critical frame type must be 'token' — different from critical path")
		assert.Equal(t, "stream-content-B", f.Content,
			"non-critical frame content must match exactly — not hardcoded")
	default:
		t.Fatal("non-critical 'token' frame was not enqueued on sendCh")
	}
}

// ---------------------------------------------------------------------------
// M4-3: Degraded-warning threshold (droppedFramesWarnThreshold)
// ---------------------------------------------------------------------------

// TestDroppedFramesWarnThreshold_Is20 verifies the threshold is exactly 20.
// BDD: Given the gateway WebSocket handler,
// When droppedFramesWarnThreshold is inspected,
// Then its value must be exactly 20.
// Traces to: pkg/gateway/websocket.go — const droppedFramesWarnThreshold
func TestDroppedFramesWarnThreshold_Is20(t *testing.T) {
	assert.Equal(t, 20, droppedFramesWarnThreshold,
		"droppedFramesWarnThreshold must be 20 per M4 spec")
}

// TestSendConnGenFrame_DegradedWarningAfterThreshold verifies that when droppedFrames
// reaches droppedFramesWarnThreshold (20), a "connection degraded" error frame is
// injected into the send channel.
//
// BDD: Given a wsConn with droppedFrames=19 and a full send channel,
// When the 20th non-critical frame is dropped,
// Then an ErrorFrame{type:"error", message contains "degraded"} is sent.
// Traces to: pkg/gateway/websocket.go — sendRawFrameBytes degraded warning injection
func TestSendConnGenFrame_DegradedWarningAfterThreshold(t *testing.T) {
	wc := &wsConn{
		sendCh: make(chan []byte, 1),
		doneCh: make(chan struct{}),
	}
	wc.droppedFrames.Store(int32(droppedFramesWarnThreshold - 1)) // 19 already dropped

	// Fill the single slot so all three backoff attempts fail.
	wc.sendCh <- []byte(`{"type":"dummy"}`)

	// After ~150ms (after the ~60ms backoff exhausts and the warning send blocks),
	// drain the channel so the blocking degraded warning can land.
	receivedFrames := make(chan []byte, 4)
	go func() {
		time.Sleep(150 * time.Millisecond)
		for {
			select {
			case data, ok := <-wc.sendCh:
				if !ok {
					return
				}
				receivedFrames <- data
			case <-time.After(2 * time.Second):
				return
			}
		}
	}()

	// Trigger the 20th drop — blocks for ~60ms backoff + warning send.
	sendConnGenFrame(wc, string(generated.WsFrameTypeToken), generated.TokenFrame{
		Type:      string(generated.WsFrameTypeToken),
		Content:   "trigger-degraded",
		SessionId: "sess-test",
	})

	// Give the goroutine time to receive and forward the degraded frame.
	deadline := time.After(3 * time.Second)
	var degradedFound bool
outer:
	for {
		select {
		case raw := <-receivedFrames:
			var f replayFrameDecoder
			if json.Unmarshal(raw, &f) != nil {
				continue
			}
			if f.Type == "error" && strings.Contains(f.Message, "degraded") {
				degradedFound = true
				break outer
			}
		case <-deadline:
			break outer
		}
	}

	assert.True(t, degradedFound,
		"a 'connection degraded' error frame must be injected after %d consecutive drops",
		droppedFramesWarnThreshold)
}

// TestSendConnGenFrame_DroppedFramesResetAfterDegradedWarning verifies that after the
// degraded warning fires, droppedFrames is reset to 0.
// BDD: Given a wsConn at threshold-1 drops,
// When the 20th drop fires the degraded warning,
// Then wc.droppedFrames is reset to 0.
// Traces to: pkg/gateway/websocket.go — sendRawFrameBytes wc.droppedFrames = 0 after warning
func TestSendConnGenFrame_DroppedFramesResetAfterDegradedWarning(t *testing.T) {
	wc := &wsConn{
		sendCh: make(chan []byte, 1),
		doneCh: make(chan struct{}),
	}
	wc.droppedFrames.Store(int32(droppedFramesWarnThreshold - 1)) // 19
	wc.sendCh <- []byte(`{"type":"dummy"}`)

	// Drain so the degraded frame can land and the blocking select unblocks.
	// Closes sendCh on cleanup so the drainer goroutine exits with the test.
	go func() {
		for range wc.sendCh {
		}
	}()
	t.Cleanup(func() {
		// Brief wait so any in-flight sendConnGenFrame finishes before close.
		time.Sleep(50 * time.Millisecond)
		close(wc.sendCh)
	})

	sendConnGenFrame(wc, string(generated.WsFrameTypeToken), generated.TokenFrame{
		Type:      string(generated.WsFrameTypeToken),
		Content:   "trigger-reset",
		SessionId: "sess-test",
	})

	// Give the degraded warning send time to complete.
	time.Sleep(300 * time.Millisecond)

	assert.Equal(t, int32(0), wc.droppedFrames.Load(),
		"droppedFrames must be reset to 0 after the degraded warning fires")
}
