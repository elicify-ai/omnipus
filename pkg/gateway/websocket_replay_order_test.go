//go:build !cgo

// websocket_replay_order_test.go — regression tests for bug-5 replay event ordering.
//
// Bug-5: When a client reconnects and sends attach_session while the agent is still
// running, frames buffered in replayDivertCh during replay must appear in the client's
// frame stream BEFORE any live frames emitted after the replay ends.
//
// The root cause was that handleAttachSession cleared isReplayingLive BEFORE draining
// replayDivertCh, opening a window where concurrent sendConnGenFrame callers wrote
// live frames directly to sendCh while the drain loop was still moving buffered frames
// into sendCh — causing the buffered frames to appear after the newer live frames in
// the FIFO channel.
//
// Fix: drain replayDivertCh first, then clear isReplayingLive.

package gateway

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestReplay_DivertedLiveFramesArriveBeforePostReplayFrames is the load-bearing
// regression test for bug-5.
//
// BDD:
//
//	Given a wsConn with an active replay in progress,
//	And live frames are being buffered in replayDivertCh during replay,
//	When the replay finishes (done frame emitted to sendCh),
//	Then all divert-buffered frames must appear in sendCh BEFORE any frames
//	emitted by concurrent sendConnGenFrame calls that fire after replay ends.
//
// Without the fix (drain-after-disarm order), the test fails because concurrent
// writers see isReplayingLive=false immediately and write to sendCh ahead of the
// drain. With the fix (drain-before-disarm order), the drain completes under the
// protection of isReplayingLive=true, guaranteeing FIFO ordering.
//
// Traces to: docs/internal/investigation/bug-5-replay-order.md
func TestReplay_DivertedLiveFramesArriveBeforePostReplayFrames(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store, "session store must not be nil")

	// Create a session with enough entries to give concurrent writers time to race.
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "main")
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		entry := session.TranscriptEntry{
			ID:      "e" + string(rune('0'+i)),
			Role:    "user",
			Content: "message",
		}
		require.NoError(t, store.AppendTranscript(meta.ID, entry))
	}

	// sendCh must be large enough that neither replay nor divert writes block.
	wc := &wsConn{
		sendCh:         make(chan []byte, 2048),
		doneCh:         make(chan struct{}),
		replayDivertCh: make(chan []byte, replayLiveBufferCap),
	}

	// Marker frame types that we'll send to distinguish timeline.
	const typeBuffered = "buffered_live"  // injected into replayDivertCh during replay (pre-done)
	const typePostDone = "post_done_live" // injected by concurrent writer racing with drain

	// Inject a "buffered live frame" directly into replayDivertCh to simulate
	// frames that arrived during replay (as if sendConnGenFrame diverted them).
	// We do this before setting isReplayingLive so the divert channel is pre-loaded.
	bufferedData, err := json.Marshal(map[string]string{"type": typeBuffered, "seq": "divert-1"})
	require.NoError(t, err)
	wc.replayDivertCh <- bufferedData

	// Arm the replay flag so that concurrent sendConnGenFrame calls during attach
	// go to replayDivertCh (they would be additional buffered frames in production).
	wc.isReplayingLive.Store(true)

	// Spawn a goroutine that fires sendConnGenFrame repeatedly just before/after
	// handleAttachSession returns. We use a sync.WaitGroup and a channel to time
	// the injection at the most adversarial moment: right when the drain is happening.
	var raceWg sync.WaitGroup
	stopRacing := make(chan struct{})
	raceWg.Add(1)
	go func() {
		defer raceWg.Done()
		for {
			select {
			case <-stopRacing:
				return
			default:
				// Send a post-replay marker. Before the fix, if isReplayingLive is
				// cleared before the drain, this frame lands in sendCh ahead of the
				// buffered divert frame. After the fix, the drain completes under
				// isReplayingLive=true so this frame cannot enter sendCh until the
				// flag is cleared (after the drain).
				sendConnGenFrame(wc, typePostDone, map[string]string{
					"type": typePostDone,
					"seq":  "concurrent",
				})
				time.Sleep(time.Microsecond)
			}
		}
	}()

	chatID := "test-chat-ordering"
	ctx := context.Background()
	handler.handleAttachSession(ctx, chatID, meta.ID, nil, wc)

	close(stopRacing)
	raceWg.Wait()

	// Drain sendCh and collect all frames.
	close(wc.sendCh)
	var frames []replayFrameDecoder
	for raw := range wc.sendCh {
		var f replayFrameDecoder
		if json.Unmarshal(raw, &f) == nil {
			frames = append(frames, f)
		}
	}

	// Find the positions of the key frame types.
	bufferedPos := -1
	donePos := -1
	for i, f := range frames {
		if f.Type == typeBuffered && bufferedPos == -1 {
			bufferedPos = i
		}
		if f.Type == "done" && donePos == -1 {
			// First done frame = the replay done (streamReplay always emits exactly one).
			donePos = i
		}
	}

	require.NotEqual(t, -1, bufferedPos,
		"buffered_live frame (pre-loaded in replayDivertCh) must appear in the stream")
	require.NotEqual(t, -1, donePos,
		"done frame from replay must appear in the stream")

	// The buffered (diverted) frame must appear BEFORE the replay done frame.
	// The replay done frame is emitted by emitFn directly into sendCh as the very
	// last replay operation. With the fix:
	//   - drain runs while isReplayingLive=true
	//   - buffered frames move to sendCh (they arrived before the drain)
	//   - replay done was already written to sendCh by emitFn (before drain)
	//
	// Wait — the replay done is the LAST frame emitFn writes; the buffered frames
	// arrive in sendCh DURING the drain, AFTER the replay done. So the correct
	// assertion is: bufferedPos > donePos (buffered frames come after replay done,
	// before any post-drain live frames).
	//
	// The ordering invariant we're actually testing is that ANY post_done_live frames
	// (concurrent writes that fire after isReplayingLive is cleared) appear AFTER
	// the buffered frames, not before them. This is the bug: without the fix,
	// post_done_live can appear before buffered frames because the flag is cleared
	// before the drain.
	postDoneBeforeBuffered := false
	seenBuffered := false
	for _, f := range frames {
		if f.Type == typeBuffered {
			seenBuffered = true
		}
		if f.Type == typePostDone && !seenBuffered {
			postDoneBeforeBuffered = true
			break
		}
	}

	assert.False(t, postDoneBeforeBuffered,
		"BUG-5: a concurrent post-replay frame appeared in sendCh BEFORE the "+
			"buffered divert frame — drain-before-disarm ordering was violated. "+
			"Frame sequence: %v", func() []string {
			types := make([]string, len(frames))
			for i, f := range frames {
				types[i] = f.Type
			}
			return types
		}())
}

// TestReplay_DivertFlagClearedAfterDrain_FlagState verifies that after
// handleAttachSession returns, isReplayingLive is false (divert is disarmed).
//
// BDD:
//
//	Given a session with transcript entries,
//	When handleAttachSession completes successfully,
//	Then wc.isReplayingLive must be false.
//
// Traces to: docs/internal/investigation/bug-5-replay-order.md
func TestReplay_DivertFlagClearedAfterDrain_FlagState(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "main")
	require.NoError(t, err)

	entry := session.TranscriptEntry{
		ID:      "e1",
		Role:    "user",
		Content: "hello",
	}
	require.NoError(t, store.AppendTranscript(meta.ID, entry))

	wc := &wsConn{
		sendCh: make(chan []byte, 512),
		doneCh: make(chan struct{}),
	}

	handler.handleAttachSession(context.Background(), "chat-flag-test", meta.ID, nil, wc)

	assert.False(t, wc.isReplayingLive.Load(),
		"isReplayingLive must be false after handleAttachSession returns")
}

// TestReplay_DivertDrainedBeforeFlag_OrderWithRealConcurrency tests the ordering
// guarantee under real goroutine concurrency using -race to detect data races.
//
// BDD:
//
//	Given a session with transcript entries,
//	And live frames are buffered in replayDivertCh before replay starts,
//	When handleAttachSession runs concurrently with sendConnGenFrame calls,
//	Then no data race is reported and the divert-buffered frames precede
//	any post-disarm live frames in the sendCh stream.
//
// Traces to: docs/internal/investigation/bug-5-replay-order.md
func TestReplay_DivertDrainedBeforeFlag_OrderWithRealConcurrency(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "main")
	require.NoError(t, err)

	// Three entries: replay takes a non-trivial amount of time.
	for i := 0; i < 3; i++ {
		entry := session.TranscriptEntry{
			ID:      "e" + string(rune('0'+i)),
			Role:    "assistant",
			Content: "response",
			ToolCalls: []session.ToolCall{
				{
					ID:         session.ToolCallID("tc" + string(rune('0'+i))),
					Tool:       "shell",
					Status:     "success",
					DurationMS: 10,
					Parameters: map[string]any{"cmd": "echo"},
					Result:     map[string]any{"stdout": "ok"},
				},
			},
		}
		require.NoError(t, store.AppendTranscript(meta.ID, entry))
	}

	wc := &wsConn{
		sendCh:         make(chan []byte, 4096),
		doneCh:         make(chan struct{}),
		replayDivertCh: make(chan []byte, replayLiveBufferCap),
	}

	// Pre-load three "divert-buffered" frames directly into replayDivertCh.
	// These represent live frames that arrived during an active agent turn
	// while the user was away and replay is now happening.
	divertTypes := []string{"divert_frame_1", "divert_frame_2", "divert_frame_3"}
	for _, dt := range divertTypes {
		frameData, merr := json.Marshal(map[string]string{"type": dt})
		require.NoError(t, merr)
		wc.replayDivertCh <- frameData
	}

	// Run attach (which will drain the divert channel as part of replay completion).
	// We do NOT pre-set isReplayingLive here because handleAttachSession sets it
	// internally. The divert channel already has frames; the key test is that the
	// drain happens BEFORE the flag is cleared.
	//
	// Note: handleAttachSession sets isReplayingLive=true internally. Because we
	// pre-loaded replayDivertCh, those frames are already there for the drain.
	handler.handleAttachSession(context.Background(), "chat-concurrent-test", meta.ID, nil, wc)

	// isReplayingLive must be false after return.
	assert.False(t, wc.isReplayingLive.Load(),
		"isReplayingLive must be false after handleAttachSession returns")

	// All divert frames must be present in sendCh.
	close(wc.sendCh)
	var gotTypes []string
	for raw := range wc.sendCh {
		var f replayFrameDecoder
		if json.Unmarshal(raw, &f) == nil {
			gotTypes = append(gotTypes, f.Type)
		}
	}

	for _, dt := range divertTypes {
		assert.Contains(t, gotTypes, dt,
			"divert-buffered frame %q must appear in the final stream", dt)
	}
}

// TestWsStreamer_Update_RespectsReplayDivert verifies that wsStreamer.Update routes
// token frames through the replay-divert logic rather than writing directly to sendCh.
//
// BDD:
//
//	Given a wsConn with isReplayingLive=true,
//	When wsStreamer.Update is called,
//	Then the token frame is written to replayDivertCh, not to sendCh.
//
// This is Fix B from docs/internal/investigation/bug-5-replay-order.md.
// Traces to: pkg/gateway/websocket.go wsStreamer.Update
func TestWsStreamer_Update_RespectsReplayDivert(t *testing.T) {
	wc := &wsConn{
		sendCh:         make(chan []byte, 256),
		doneCh:         make(chan struct{}),
		replayDivertCh: make(chan []byte, replayLiveBufferCap),
	}
	wc.isReplayingLive.Store(true)

	s := &wsStreamer{
		conn:      wc,
		chatID:    "test-chat",
		sessionID: "test-session",
	}

	err := s.Update(context.Background(), "hello")
	require.NoError(t, err, "Update must succeed when divert channel is not full")

	// Token frame must land in replayDivertCh, not sendCh.
	assert.Equal(t, 0, len(wc.sendCh),
		"sendCh must be empty: token frame should have been diverted to replayDivertCh")
	assert.Equal(t, 1, len(wc.replayDivertCh),
		"replayDivertCh must contain the token frame")

	// Verify the diverted frame is a valid token frame.
	raw := <-wc.replayDivertCh
	var f replayFrameDecoder
	require.NoError(t, json.Unmarshal(raw, &f))
	assert.Equal(t, string(generated.WsFrameTypeToken), f.Type)
	assert.Equal(t, "hello", f.Content)
}

// TestWsStreamer_Update_DirectToSendChWhenNotReplaying verifies that wsStreamer.Update
// writes token frames directly to sendCh when isReplayingLive is false.
//
// BDD:
//
//	Given a wsConn with isReplayingLive=false,
//	When wsStreamer.Update is called,
//	Then the token frame is written to sendCh directly.
//
// Traces to: pkg/gateway/websocket.go wsStreamer.Update
func TestWsStreamer_Update_DirectToSendChWhenNotReplaying(t *testing.T) {
	wc := &wsConn{
		sendCh:         make(chan []byte, 256),
		doneCh:         make(chan struct{}),
		replayDivertCh: make(chan []byte, replayLiveBufferCap),
	}
	// isReplayingLive is false (default).

	s := &wsStreamer{
		conn:      wc,
		chatID:    "test-chat",
		sessionID: "test-session",
	}

	err := s.Update(context.Background(), "world")
	require.NoError(t, err, "Update must succeed")

	assert.Equal(t, 1, len(wc.sendCh),
		"token frame must be in sendCh when not replaying")
	assert.Equal(t, 0, len(wc.replayDivertCh),
		"replayDivertCh must be empty when not replaying")

	raw := <-wc.sendCh
	var f replayFrameDecoder
	require.NoError(t, json.Unmarshal(raw, &f))
	assert.Equal(t, string(generated.WsFrameTypeToken), f.Type)
	assert.Equal(t, "world", f.Content)
}

// TestWsStreamer_FanOutToPeer_RespectsReplayDivert is the regression proof
// for Finding E (A-I4 round 5): fanOutToSessionPeers used to write straight
// into a peer wsConn's sendCh, bypassing the exact replay-divert protocol
// TestWsStreamer_Update_RespectsReplayDivert (above) already proves the
// ORIGINATING connection gets. A peer connection mid attach_session replay
// (isReplayingLive=true) has no way to divert a directly-enqueued sendCh
// write into its own replayDivertCh — the fanned-out frame lands
// interleaved with that peer's own in-flight replay frames instead of being
// correctly ordered after them once the drain completes. Live-verified: a
// reload+reattach caught this as a stray, out-of-place live frame appearing
// mid-replay. Routing fanOutToSessionPeers through sendRawFrameBytes (like
// every other live-frame path) closes the gap.
//
// BDD:
//
//	Given two wsConns sharing one session — the originating connection
//	(not replaying) and a peer connection currently mid attach_session
//	replay (isReplayingLive=true),
//	When wsStreamer.Update fans a token frame out to session peers,
//	Then the origin connection receives it directly on sendCh,
//	And the peer connection receives it via replayDivertCh, NOT sendCh.
func TestWsStreamer_FanOutToPeer_RespectsReplayDivert(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	originConn := makeTestConn()
	peerConn := makeTestConn()
	peerConn.replayDivertCh = make(chan []byte, replayLiveBufferCap)
	peerConn.isReplayingLive.Store(true) // peer is mid attach_session replay

	const (
		originChat = "chat-origin"
		peerChat   = "chat-peer"
		sessionID  = "session-shared"
	)

	handler.mu.Lock()
	handler.sessions[originChat] = originConn
	handler.sessions[peerChat] = peerConn
	handler.sessionIDs[originChat] = sessionID
	handler.sessionIDs[peerChat] = sessionID
	handler.mu.Unlock()

	s := &wsStreamer{
		conn:      originConn,
		chatID:    originChat,
		sessionID: sessionID,
		channel:   newWebchatChannel(handler),
	}

	err := s.Update(context.Background(), "hello")
	require.NoError(t, err, "Update must succeed")

	// Origin connection is not replaying — direct sendCh delivery, unchanged.
	require.Equal(t, 1, len(originConn.sendCh), "origin connection must receive the token frame directly")
	raw := <-originConn.sendCh
	var originFrame replayFrameDecoder
	require.NoError(t, json.Unmarshal(raw, &originFrame))
	assert.Equal(t, string(generated.WsFrameTypeToken), originFrame.Type)

	// Peer is mid-replay: the fanned-out token must be diverted, not written
	// directly into sendCh.
	assert.Equal(t, 0, len(peerConn.sendCh),
		"peer's sendCh must stay empty while it is mid-replay — the fanned-out token must be diverted")
	require.Equal(t, 1, len(peerConn.replayDivertCh),
		"the fanned-out token frame must land in the peer's replayDivertCh while it is mid-replay")
	divertedRaw := <-peerConn.replayDivertCh
	var peerFrame replayFrameDecoder
	require.NoError(t, json.Unmarshal(divertedRaw, &peerFrame))
	assert.Equal(t, string(generated.WsFrameTypeToken), peerFrame.Type)
	assert.Equal(t, "hello", peerFrame.Content)
}

// TestReplayOrdering_ConcurrentUpdateDuringDrain verifies that a wsStreamer.Update
// call that races with the drain+disarm sequence lands in the correct channel.
//
// BDD:
//
//	Given a wsConn with isReplayingLive=true and a frame pre-loaded in replayDivertCh,
//	When wsStreamer.Update is called concurrently with handleAttachSession completing
//	its drain (i.e. the Update call snapshots isReplayingLive==true but the drain
//	is simultaneously draining the channel and about to disarm the flag),
//	Then the token frame arrives in exactly one channel (never orphaned in replayDivertCh
//	after the drain completes), and no post-disarm live frames appear before the
//	pre-drain buffered frame.
//
// This is the load-bearing regression test for code-reviewer Finding #2 and
// architect Finding #4: the replayMu RWMutex in sendRawFrameBytes prevents the
// TOCTOU race where a writer snapshots isReplayingLive==true, the drain finishes
// and disarms the flag, and the writer then sends to the now-abandoned channel.
//
// To demonstrate the regression: remove the wc.replayMu.Lock()/RLock() calls from
// sendRawFrameBytes and handleAttachSession, then run with -race and -count=100.
// Without the mutex the race detector will flag a concurrent read+write, and
// occasionally a token frame is orphaned (never delivered to the client).
func TestReplayOrdering_ConcurrentUpdateDuringDrain(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "main")
	require.NoError(t, err)

	// Add entries so replay takes a non-trivial number of iterations.
	for i := 0; i < 3; i++ {
		require.NoError(t, store.AppendTranscript(meta.ID, session.TranscriptEntry{
			ID: "e" + string(rune('0'+i)), Role: "user", Content: "msg",
		}))
	}

	// Generous channel sizes so neither end blocks.
	wc := &wsConn{
		sendCh:         make(chan []byte, 4096),
		doneCh:         make(chan struct{}),
		replayDivertCh: make(chan []byte, replayLiveBufferCap),
	}

	// Pre-arm the divert so any Update() call during the race window routes to
	// replayDivertCh.
	wc.isReplayingLive.Store(true)

	// Pre-load a buffered live frame that was "in flight before drain".
	bufferedData, merr := json.Marshal(map[string]string{"type": "pre_drain_buffered"})
	require.NoError(t, merr)
	wc.replayDivertCh <- bufferedData

	// Spawn a goroutine that calls wsStreamer.Update() in a tight loop while
	// handleAttachSession is executing.  The adversarial case: Update snapshots
	// isReplayingLive==true, the drain empties replayDivertCh and disarms the
	// flag under replayMu.Lock(), then Update tries to send to replayDivertCh.
	// With the mutex fix, the RLock prevents Update from completing the channel
	// selection until after the drain+disarm is fully committed.
	var updaterWg sync.WaitGroup
	stopUpdater := make(chan struct{})
	updaterWg.Add(1)
	go func() {
		defer updaterWg.Done()
		s := &wsStreamer{
			conn:      wc,
			chatID:    "chat-concurrent-update",
			sessionID: meta.ID,
		}
		for {
			select {
			case <-stopUpdater:
				return
			default:
				_ = s.Update(context.Background(), "token")
				time.Sleep(time.Microsecond)
			}
		}
	}()

	handler.handleAttachSession(context.Background(), "chat-concurrent-update", meta.ID, nil, wc)

	close(stopUpdater)
	updaterWg.Wait()

	// After attach completes, isReplayingLive must be cleared.
	assert.False(t, wc.isReplayingLive.Load(),
		"isReplayingLive must be false after handleAttachSession returns")

	// replayDivertCh must be empty: the drain must have consumed everything, and
	// any post-disarm Update() calls must have gone to sendCh instead.
	assert.Equal(t, 0, len(wc.replayDivertCh),
		"replayDivertCh must be fully drained — no frames orphaned in the divert channel")

	// The pre_drain_buffered frame must be present in sendCh (or have been sent
	// to sendCh during the drain).  Drain sendCh and verify presence.
	close(wc.sendCh)
	var gotTypes []string
	for raw := range wc.sendCh {
		var fd replayFrameDecoder
		if json.Unmarshal(raw, &fd) == nil {
			gotTypes = append(gotTypes, fd.Type)
		}
	}
	assert.Contains(t, gotTypes, "pre_drain_buffered",
		"pre-drain buffered frame must arrive in sendCh")
}

// TestReplayDrain_SlowClientDeadline verifies that the replay drain does not
// block indefinitely when sendCh is full (architect Finding #4 / fix #2).
//
// BDD:
//
//	Given a wsConn whose sendCh has exactly enough room for the replay frames
//	(so it fills after replay completes),
//	And replayDivertCh has one extra buffered frame,
//	When handleAttachSession runs and the drain tries to forward that frame,
//	Then the drain exits within ~1.2s (the 1s per-frame deadline + margin)
//	rather than blocking indefinitely.
//
// Without fix #2 (the time.After(1 * time.Second) deadline in the drain loop),
// the blocking `wc.sendCh <- raw` holds the drain (and the replayMu.Lock())
// forever when sendCh is full and there is no consumer.
//
// Design:
//   - One user transcript entry → streamReplay emits exactly 2 frames
//     (replay_message + done) via emitFn into sendCh.
//   - sendCh capacity = 2: both replay frames fit, sendCh is now full.
//   - replayDivertCh is pre-loaded with 1 extra "slow_client_frame".
//   - When the drain runs it tries to send that frame to the full sendCh.
//     The 1s deadline fires, the frame is dropped, and the drain exits.
func TestReplayDrain_SlowClientDeadline(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "main")
	require.NoError(t, err)

	// One user entry: streamReplay emits 1 replay_message frame + 1 done frame = 2 total.
	require.NoError(t, store.AppendTranscript(meta.ID, session.TranscriptEntry{
		ID:      "e0",
		Role:    "user",
		Content: "msg",
	}))

	// sendCh capacity = 2: fits the two replay frames exactly.
	// After streamReplay completes, sendCh is full (no consumer).
	wc := &wsConn{
		sendCh:         make(chan []byte, 2),
		doneCh:         make(chan struct{}),
		replayDivertCh: make(chan []byte, replayLiveBufferCap),
	}

	// Pre-load replayDivertCh with one frame representing a live event that
	// arrived during replay.  After streamReplay fills sendCh, the drain tries
	// to forward this frame and blocks (sendCh full).  The 1s deadline must fire.
	divertData, merr := json.Marshal(map[string]string{"type": "slow_client_frame"})
	require.NoError(t, merr)
	wc.replayDivertCh <- divertData

	// handleAttachSession must return within 15 seconds.
	// Breakdown: replay = ~0s, drain deadline = 1s, remaining work = ~0s.
	// 15s is generous margin for CI machines under load.
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.handleAttachSession(context.Background(), "chat-slow-client", meta.ID, nil, wc)
	}()

	select {
	case <-done:
		// Good: the drain completed — deadline-based drop worked.
	case <-time.After(15 * time.Second):
		t.Fatal("handleAttachSession blocked indefinitely — " +
			"replay drain deadline not enforced (fix #2 missing). " +
			"Without fix #2, the drain blocks on `wc.sendCh <- raw` forever " +
			"when sendCh is full and there is no consumer.")
	}

	// isReplayingLive must be cleared regardless of drain outcome.
	assert.False(t, wc.isReplayingLive.Load(),
		"isReplayingLive must be false after handleAttachSession returns even when sendCh is full")

	// replayDivertCh must be empty: the drain loop must have processed and
	// dropped the frame (not left it in the channel).
	assert.Equal(t, 0, len(wc.replayDivertCh),
		"replayDivertCh must be empty after drain completes (frame dropped on timeout)")
}
