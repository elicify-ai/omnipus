// websocket_pump_test.go: tests for write pump and event forwarding to the client.

package gateway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// TestWritePumpEnforcesWriteDeadline_TextMessage proves that writePump's
// TextMessage write call (wc.conn.WriteMessage(websocket.TextMessage, msg))
// is bounded by wsWriteWait rather than blocking forever when the client
// stops reading.
//
// BDD:
//
//	Given a WS server connection whose client never reads again,
//	When writePump attempts its first write over the (unbuffered, nobody-
//	  reading) net.Pipe() transport,
//	Then the blocked WriteMessage call returns a deadline error within
//	  wsWriteWait (+ scheduling slack), and writePump's goroutine exits —
//	  it does not hang indefinitely.
//
// Traces to: pkg/gateway/websocket.go writePump (TextMessage branch).
func TestWritePumpEnforcesWriteDeadline_TextMessage(t *testing.T) {
	wc, wpDone := setupBackpressureWS(t)

	payload := make([]byte, 256*1024) // 256 KiB text frame, reused every send

	start := time.Now()
	feederDone := make(chan struct{})
	go func() {
		defer close(feederDone)
		for {
			select {
			case wc.sendCh <- payload:
			case <-wpDone:
				return
			}
		}
	}()

	select {
	case <-wpDone:
		elapsed := time.Since(start)
		assert.LessOrEqualf(t, elapsed, 15*time.Second,
			"writePump must return within wsWriteWait(%s)+slack once the client stops reading, took %s — "+
				"a write deadline that isn't firing means the single writer goroutine can stall forever",
			wsWriteWait, elapsed)
		assert.GreaterOrEqualf(t, elapsed, 5*time.Second,
			"writePump returned after only %s — expected it to actually block until close to "+
				"wsWriteWait(%s) before the deadline fires; a near-instant return suggests the test "+
				"isn't exercising real backpressure (the pipe write never actually blocked) rather than confirming the fix",
			elapsed, wsWriteWait)
	case <-time.After(25 * time.Second):
		t.Fatal("writePump did not return within 25s of a stalled client on the TextMessage path — " +
			"the write deadline is not being enforced (regression: missing SetWriteDeadline before " +
			"wc.conn.WriteMessage(websocket.TextMessage, msg) in writePump)")
	}
	<-feederDone
}

// TestWritePumpEnforcesWriteDeadline_Ping proves that writePump's
// PingMessage write call (wc.conn.WriteMessage(websocket.PingMessage, nil),
// triggered by the nil sentinel on wc.sendCh) is bounded by wsWriteWait
// rather than blocking forever when the client stops reading.
//
// This is the call site most directly implicated in the production bug:
// the keepalive ping is what has to keep firing every wsPingPeriod to beat
// the reverse proxy's idle timeout, and it is exactly the frame that got
// silently starved when an earlier write on the same single writer
// goroutine blocked forever.
//
// setupBackpressureWS's net.Pipe() transport has no OS buffer to overflow,
// so the FIRST ping write attempt blocks immediately — the elapsed time is
// governed purely by wsWriteWait's own deadline, not by how many 2-byte
// ping frames it takes to organically overflow a platform-specific,
// best-effort-shrunk OS buffer (the previous, flakier mechanism — see the
// package doc comment above for the full root-cause account). The feeder
// below still enqueues PURE ping sentinels (no payload mixed in), so the
// write that ultimately blocks and times out is still, specifically,
// writePump's PingMessage branch.
//
// Traces to: pkg/gateway/websocket.go writePump (PingMessage branch).
func TestWritePumpEnforcesWriteDeadline_Ping(t *testing.T) {
	wc, wpDone := setupBackpressureWS(t)

	start := time.Now()
	feederDone := make(chan struct{})
	go func() {
		defer close(feederDone)
		for {
			select {
			case wc.sendCh <- wsPingMsg: // nil sentinel -> PingMessage write in writePump
			case <-wpDone:
				return
			}
		}
	}()

	select {
	case <-wpDone:
		elapsed := time.Since(start)
		assert.LessOrEqualf(t, elapsed, wsWriteWait+15*time.Second,
			"writePump must return within wsWriteWait(%s)+slack once the unbuffered pipe makes the "+
				"first ping write block, took %s", wsWriteWait, elapsed)
		assert.GreaterOrEqualf(t, elapsed, wsWriteWait-3*time.Second,
			"writePump returned after only %s — expected it to actually block until close to "+
				"wsWriteWait(%s) before the deadline fires; a near-instant return suggests the pipe "+
				"write never actually blocked before the feeder started", elapsed, wsWriteWait)
	case <-time.After(wsWriteWait + 30*time.Second):
		t.Fatal("writePump did not return within wsWriteWait+30s while flooded with ping frames against " +
			"a non-reading client over an unbuffered pipe — the ping write deadline is not being enforced " +
			"(regression: missing SetWriteDeadline before wc.conn.WriteMessage(websocket.PingMessage, " +
			"nil) in writePump)")
	}
	<-feederDone
}
