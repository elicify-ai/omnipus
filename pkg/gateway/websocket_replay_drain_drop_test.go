// websocket_replay_drain_drop_test.go — regression tests for the silent loss of a
// live message dropped during the post-replay divert drain.
//
// The defect: drainReplayDivert's predecessor (an inline loop in
// handleAttachSession) dropped a buffered live frame on a 1-second send deadline
// and recorded it with nothing but `wc.droppedFrames.Add(1)`. That counter is
// only ever READ by sendRawFrameBytes' "connection degraded" threshold check, and
// every success path in sendRawFrameBytes calls `wc.droppedFrames.Store(0)` — so
// the next frame that got through erased the evidence before the threshold could
// fire. The user saw a truncated reply on reconnect with no error of any kind.
//
// The fix: count drops locally in the drain, then — once the drain has finished
// and the divert is disarmed — log at Error level and send a user-visible "error"
// frame naming the count. "error" is on sendRawFrameBytes' isCritical list, so the
// report takes the blocking path rather than being dropped by the same
// backpressure that caused the loss.
//
// Governing rule (CLAUDE.md, the JPEG-screencast entry): a fallback nobody can
// detect hides the real defect indefinitely.

package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainDropReaderDelay is how long the simulated client waits before it starts
// draining wc.sendCh.
//
// Timing contract (all three values are independent, with ~1 s of slack each way):
//   - drainReplayDivert drops a buffered frame after a hardcoded 1 s send deadline.
//   - This reader frees the first sendCh slot at 2 s — a full second AFTER that
//     deadline, so the drop is guaranteed to have happened.
//   - The "error" report is a critical frame with a 5 s blocking budget, which
//     starts at ~1 s and therefore still has ~4 s left when the slot frees at 2 s.
const drainDropReaderDelay = 2 * time.Second

// newFullSendChConn builds a wsConn whose sendCh is already at capacity, so the
// very next send into it blocks — the backpressure condition that makes the drain
// drop a frame.
func newFullSendChConn(t *testing.T) *wsConn {
	t.Helper()
	wc := &wsConn{
		sendCh:         make(chan []byte, 1),
		doneCh:         make(chan struct{}),
		replayDivertCh: make(chan []byte, replayLiveBufferCap),
	}
	filler, err := json.Marshal(map[string]string{"type": "filler"})
	require.NoError(t, err)
	wc.sendCh <- filler
	require.Len(t, wc.sendCh, 1, "sendCh must be full before the drain runs")
	return wc
}

// startDelayedReader simulates a client that is stalled during the drain and then
// resumes reading. It returns a snapshot accessor and a stop function.
func startDelayedReader(wc *wsConn, delay time.Duration) (snapshot func() []replayFrameDecoder, stop func()) {
	var mu sync.Mutex
	var got []replayFrameDecoder
	stopCh := make(chan struct{})
	var once sync.Once
	done := make(chan struct{})

	go func() {
		defer close(done)
		select {
		case <-time.After(delay):
		case <-stopCh:
			return
		}
		for {
			select {
			case raw := <-wc.sendCh:
				var f replayFrameDecoder
				if json.Unmarshal(raw, &f) == nil {
					mu.Lock()
					got = append(got, f)
					mu.Unlock()
				}
			case <-stopCh:
				return
			}
		}
	}()

	snapshot = func() []replayFrameDecoder {
		mu.Lock()
		defer mu.Unlock()
		return append([]replayFrameDecoder(nil), got...)
	}
	stop = func() {
		once.Do(func() { close(stopCh) })
		<-done
	}
	return snapshot, stop
}

// findDrainFrame returns the first collected frame of the given type.
func findDrainFrame(frames []replayFrameDecoder, frameType string) (replayFrameDecoder, bool) {
	for _, f := range frames {
		if f.Type == frameType {
			return f, true
		}
	}
	return replayFrameDecoder{}, false
}

// TestReplayDrain_DroppedFrameIsReportedToTheClient is the load-bearing regression
// test.
//
// BDD:
//
//	Given a post-replay drain with a live frame buffered in replayDivertCh,
//	And a client that is not draining sendCh, so sendCh is full,
//	When the drain's 1-second send deadline expires and the frame is dropped,
//	Then an "error" frame naming the number of lost updates reaches the client,
//	And it tells the user to reopen the conversation to reload the transcript.
//
// Verified to fail without the fix: with the post-drain sendConnGenFrame emit in
// drainReplayDivert removed, this test fails on the "must reach the client"
// require — which is exactly the production behaviour before the fix.
func TestReplayDrain_DroppedFrameIsReportedToTheClient(t *testing.T) {
	wc := newFullSendChConn(t)

	// One live frame arrived during replay and is waiting to be drained.
	buffered, err := json.Marshal(map[string]string{"type": "token", "content": "lost"})
	require.NoError(t, err)
	wc.replayDivertCh <- buffered
	wc.isReplayingLive.Store(true)

	snapshot, stop := startDelayedReader(wc, drainDropReaderDelay)
	defer stop()

	const attachID = "sess-drain-drop"
	require.True(t, drainReplayDivert(context.Background(), wc, attachID, "chat-drain-drop"),
		"drain must report success: the context was never cancelled")

	// The divert must be disarmed regardless of the drop.
	assert.False(t, wc.isReplayingLive.Load(),
		"isReplayingLive must be cleared after the drain")

	require.Eventually(t, func() bool {
		_, ok := findDrainFrame(snapshot(), "error")
		return ok
	}, 10*time.Second, 20*time.Millisecond,
		"a dropped live frame must reach the user as an error frame — a silent drop is the defect")

	errFrame, ok := findDrainFrame(snapshot(), "error")
	require.True(t, ok)

	assert.Equal(t, attachID, errFrame.SessionID,
		"the report must be tagged with the session it belongs to, so the SPA routes it to the right conversation")
	assert.Contains(t, errFrame.Message, "1 live update",
		"the message must name how many updates were lost")
	assert.Contains(t, strings.ToLower(errFrame.Message), "reopen this conversation",
		"the message must tell the user how to recover the missing content")

	// The drop must NOT be recorded on the shared counter: sendRawFrameBytes
	// zeroes that on its next success, which is precisely how the original
	// defect erased its own evidence.
	assert.Zero(t, wc.droppedFrames.Load(),
		"a drain drop must be counted locally, not on wc.droppedFrames (which the next successful send resets to 0)")
}

// TestReplayDrain_CleanDrainSendsNoErrorFrame is the negative control.
//
// Without it, TestReplayDrain_DroppedFrameIsReportedToTheClient would still pass
// if drainReplayDivert emitted an error frame unconditionally — which would make
// every ordinary reconnect show a bogus data-loss warning.
func TestReplayDrain_CleanDrainSendsNoErrorFrame(t *testing.T) {
	wc := &wsConn{
		sendCh:         make(chan []byte, 8),
		doneCh:         make(chan struct{}),
		replayDivertCh: make(chan []byte, replayLiveBufferCap),
	}

	buffered, err := json.Marshal(map[string]string{"type": "token", "content": "delivered"})
	require.NoError(t, err)
	wc.replayDivertCh <- buffered
	wc.isReplayingLive.Store(true)

	require.True(t, drainReplayDivert(context.Background(), wc, "sess-clean", "chat-clean"))
	assert.False(t, wc.isReplayingLive.Load())

	close(wc.sendCh)
	var frames []replayFrameDecoder
	for raw := range wc.sendCh {
		var f replayFrameDecoder
		if json.Unmarshal(raw, &f) == nil {
			frames = append(frames, f)
		}
	}

	require.Len(t, frames, 1, "the one buffered frame must be delivered and nothing else")
	assert.Equal(t, "token", frames[0].Type)
	_, hasErr := findDrainFrame(frames, "error")
	assert.False(t, hasErr,
		"a drain that lost nothing must not warn the user about data loss")
}

// TestReplayDrain_CancelledContextAbortsAttach covers the ctx.Done() branch: the
// drain must disarm the divert, report failure so handleAttachSession abandons the
// rest of the attach, and not block trying to report to a connection that is gone.
func TestReplayDrain_CancelledContextAbortsAttach(t *testing.T) {
	wc := newFullSendChConn(t)

	buffered, err := json.Marshal(map[string]string{"type": "token", "content": "never sent"})
	require.NoError(t, err)
	wc.replayDivertCh <- buffered
	wc.isReplayingLive.Store(true)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	require.False(t, drainReplayDivert(ctx, wc, "sess-cancelled", "chat-cancelled"),
		"a cancelled context must abort the attach")
	assert.Less(t, time.Since(start), 5*time.Second,
		"a cancelled drain must not block on the full sendCh")
	assert.False(t, wc.isReplayingLive.Load(),
		"the divert must be disarmed even on the cancellation path")
}
