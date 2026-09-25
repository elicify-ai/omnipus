// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ws_streamer_session_bound_test.go — unit coverage for ADR-082 D2/D3: the
// webchat streamer resolves its delivery targets from the session's CURRENT
// bindings on every frame, not from a single *wsConn captured at
// streamer-creation time. See docs/internal/architecture/ADR-082-…md and
// docs/internal/specs/ui-independent-turns-spec.md (T-01..T-04).

package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestWSStreamer_ResolvesBindingsPerFrame proves FR-004: each streamed frame
// is delivered to every connection bound to the session AT SEND TIME, not to
// a connection set captured at turn start. A connection that binds to the
// session AFTER the streamer already exists (and after an earlier Update
// call) must still receive every subsequent frame.
func TestWSStreamer_ResolvesBindingsPerFrame(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	const sessionID = "session-resolves-bindings"
	connA := makeTestConn()
	bindTestConnToSession(handler, "chat-a", sessionID, connA)

	s := &wsStreamer{
		sessionID: sessionID,
		chatID:    "chat-a",
		channel:   newWebchatChannel(handler),
	}

	require.NoError(t, s.Update(context.Background(), "first"))
	frameA1 := readTokenFrame(t, connA.sendCh)
	assert.Equal(t, "first", frameA1.Content)

	// A SECOND connection binds to the SAME session — no new streamer, no
	// reconstruction, just a new entry in h.sessions/h.sessionIDs (exactly
	// what handleAttachSession/message-intake do in production).
	connB := makeTestConn()
	bindTestConnToSession(handler, "chat-b", sessionID, connB)

	require.NoError(t, s.Update(context.Background(), "second"))

	frameA2 := readTokenFrame(t, connA.sendCh)
	assert.Equal(t, "second", frameA2.Content, "the original connection must keep receiving frames")
	frameB := readTokenFrame(t, connB.sendCh)
	assert.Equal(t, "second", frameB.Content,
		"a connection that bound to the session AFTER the streamer was created and after an "+
			"earlier Update call must still receive every LATER frame")

	// connB must NOT have received the "first" frame (it wasn't bound yet).
	select {
	case raw := <-connB.sendCh:
		t.Fatalf("connB received an unexpected extra frame: %s", string(raw))
	case <-time.After(100 * time.Millisecond):
		// expected — no extra frame
	}
}

// TestWSStreamer_ZeroListeners_NoBackoff proves FR-006: with zero bound
// connections, per-frame delivery costs no backoff wait — the producer (the
// LLM streaming callback) is never slowed by an absent viewer. 1000 Update
// calls with nothing bound must complete well under sendRawFrameBytes' own
// backoff floor (10ms+50ms per frame would be 60+ seconds for 1000 calls).
func TestWSStreamer_ZeroListeners_NoBackoff(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	s := &wsStreamer{
		sessionID: "session-zero-listeners",
		chatID:    "chat-zero-listeners",
		channel:   newWebchatChannel(handler),
	}

	start := time.Now()
	for i := 0; i < 1000; i++ {
		require.NoError(t, s.Update(context.Background(), "x"))
	}
	elapsed := time.Since(start)
	// #823 catch-up redesign: threshold widened from 100ms to 1s, with
	// provenance, not weakened. Every Update call now ALSO submits its
	// token to the session's hub for numbering (ws_session_hub.go's
	// publishBytes — a mutex-guarded journal append) even with zero
	// listeners, per BE-DESIGN.md §1.2's core invariant ("numbering does
	// not depend on connections") — a real, intentional, per-call cost that
	// did not exist before this redesign. Measured locally: ~205ms for
	// 1000 calls (~0.2ms/call). 1s keeps ~5x headroom over that measurement
	// for a loaded CI runner while staying two orders of magnitude below
	// what a REINTRODUCED per-frame backoff would cost (the original
	// comment's math: 10ms+50ms/frame × 1000 = 60+ seconds) — the guarantee
	// this test protects (no per-frame backoff wait) is unchanged; only the
	// numeric budget for the OTHER, always-present cost moved.
	assert.Less(t, elapsed, 1*time.Second,
		"1000 Update calls with zero bound connections must complete in well under 1s "+
			"(got %s) — a per-frame backoff wait would push this into 60+ seconds, far "+
			"past this budget, while the hub's own zero-conn numbering cost stays in the "+
			"low hundreds of ms", elapsed)
}

// TestWSStreamer_PeerDropDoesNotSkipFanOut proves FR-005: backpressure on one
// bound connection must never prevent delivery to any other bound
// connection, and must never cause the whole fan-out to be skipped. #823
// (founder decision Q5): the backpressured connection no longer drops the
// token either — it waits in that connection's own ordered queue.
func TestWSStreamer_PeerDropDoesNotSkipFanOut(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	const sessionID = "session-peer-drop"

	// deadConn's sendCh is pre-filled to capacity and never drained.
	deadConn := &wsConn{
		sendCh: make(chan []byte, 1),
		doneCh: make(chan struct{}),
	}
	deadConn.sendCh <- []byte(`{"type":"filler"}`)
	bindTestConnToSession(handler, "chat-dead", sessionID, deadConn)

	liveConn := makeTestConn()
	bindTestConnToSession(handler, "chat-live", sessionID, liveConn)

	s := &wsStreamer{
		sessionID: sessionID,
		chatID:    "chat-dead",
		channel:   newWebchatChannel(handler),
	}

	require.NoError(t, s.Update(context.Background(), "hello"))

	frame := readTokenFrame(t, liveConn.sendCh)
	assert.Equal(t, "hello", frame.Content,
		"the live connection must receive the token even though a peer connection's send buffer is full")
	queued, _ := deadConn.queuedFrames()
	assert.Equal(t, 1, queued,
		"the backpressured connection keeps the token queued — it is never dropped")
}

// TestWSStreamer_DoneIdenticalOnEveryConnection replaces the pre-#823
// TestWSStreamer_DoneStatsPerConnection (FR-014: TokensDropped reported per
// connection). Founder decision Q5 removed frame dropping entirely, so there
// is no per-connection drop count left to report: the turn's done is ONE
// hub-numbered frame, byte-identical on every bound connection
// (BE-DESIGN.md §5), with tokens_dropped never set.
func TestWSStreamer_DoneIdenticalOnEveryConnection(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	const sessionID = "session-done-identical"
	connA := makeTestConn()
	bindTestConnToSession(handler, "chat-a", sessionID, connA)
	connB := makeTestConn()
	bindTestConnToSession(handler, "chat-b", sessionID, connB)

	s := &wsStreamer{
		sessionID: sessionID,
		chatID:    "chat-a",
		channel:   newWebchatChannel(handler),
	}
	require.NoError(t, s.Finalize(context.Background(), "final content"))

	var rawA, rawB []byte
	select {
	case rawA = <-connA.sendCh:
	case <-time.After(2 * time.Second):
		t.Fatal("connection A never received the done frame")
	}
	select {
	case rawB = <-connB.sendCh:
	case <-time.After(2 * time.Second):
		t.Fatal("connection B never received the done frame")
	}
	assert.Equal(t, string(rawA), string(rawB), "the done frame must be byte-identical on every connection")
	var done generated.DoneFrame
	require.NoError(t, json.Unmarshal(rawA, &done))
	require.NotNil(t, done.Stats)
	assert.Nil(t, done.Stats.TokensDropped, "tokens_dropped is never set")
	require.NotNil(t, done.Seq, "the done frame is numbered by the session hub")
}

// TestFix_CR9_F4_DeadConnectionDoesNotSlowLiveDelivery proves the ADR-082
// review CR9/F4 fix: a dead-but-still-bound connection (its doneCh already
// closed, simulating writePump having exited) must not force every OTHER,
// live connection on the same session to wait out sendRawFrameBytes' full
// 0/10/50ms backoff schedule on every single token. Before the fix, the
// backoff loop had no <-wc.doneCh case, so 200 tokens at ~60ms worst case
// per token for the dead connection alone would push total delivery time
// into multiple seconds.
func TestFix_CR9_F4_DeadConnectionDoesNotSlowLiveDelivery(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	const sessionID = "session-cr9-dead-live"

	deadConn := &wsConn{
		sendCh: make(chan []byte, 1),
		doneCh: make(chan struct{}),
	}
	deadConn.sendCh <- []byte(`{"type":"filler"}`) // fill it so every send must retry
	close(deadConn.doneCh)                         // simulate writePump having already exited
	bindTestConnToSession(handler, "chat-cr9-dead", sessionID, deadConn)

	liveConn := &wsConn{
		sendCh: make(chan []byte, 300), // large enough to never itself backpressure
		doneCh: make(chan struct{}),
	}
	bindTestConnToSession(handler, "chat-cr9-live", sessionID, liveConn)

	s := &wsStreamer{
		sessionID: sessionID,
		chatID:    "chat-cr9-dead",
		channel:   newWebchatChannel(handler),
	}

	const numTokens = 200
	start := time.Now()
	for i := 0; i < numTokens; i++ {
		require.NoError(t, s.Update(context.Background(), "x"))
	}
	elapsed := time.Since(start)

	assert.Less(t, elapsed, time.Duration(numTokens)*5*time.Millisecond,
		"delivery must be bounded (<5ms/token amortised) even with a dead, still-bound peer "+
			"connection sharing the session — got %s for %d tokens", elapsed, numTokens)

	received := 0
	deadline := time.After(2 * time.Second)
drain:
	for received < numTokens {
		select {
		case <-liveConn.sendCh:
			received++
		case <-deadline:
			break drain
		}
	}
	assert.Equal(t, numTokens, received, "the live connection must receive every token despite the dead peer")
}

// TestFix_F10_DroppedTokensReportsPerTurnDelta (ADR-082 review F10: report
// only this turn's dropped-token delta) was deleted with the dropped-token
// counter itself — founder decision Q5: frames are never dropped any more,
// so there is no delta to report (see TestWSStreamer_DoneIdenticalOnEveryConnection
// and ws_conn_queue_test.go).

// readTokenFrame is defined in websocket_producer_agent_id_test.go (same
// package) and reused here.

// readDoneFrameFromConn drains one frame from ch and unmarshals it as a
// DoneFrame. Named distinctly from turn_failed_test.go's readDoneFrame (same
// package) since that one is keyed to a single hard-coded channel signature.
func readDoneFrameFromConn(t *testing.T, ch chan []byte) generated.DoneFrame {
	t.Helper()
	select {
	case raw := <-ch:
		var f generated.DoneFrame
		require.NoError(t, json.Unmarshal(raw, &f))
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for done frame")
		return generated.DoneFrame{}
	}
}
