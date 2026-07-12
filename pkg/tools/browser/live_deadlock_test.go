package browser

// live_deadlock_test.go — regression coverage for the ADR-038 live-view
// deadlock postmortem (UAT finding, 2026-07-11):
//
//   While a live-view screencast was active, a heavy page caused
//   browser_screenshot to time out ("context deadline exceeded",
//   duration=30000ms). Every subsequent browser tool call — even a trivial
//   browser_navigate — then hung indefinitely: no completion, no error, no
//   log line, and "Stop" (which only cancels the agent turn's context)
//   could not recover it.
//
// Root cause: LiveView.attach() held lv.mu (via defer lv.mu.Unlock()) across
// the blocking page.StartScreencast() chromedp.Run call, which itself ran on
// the bare tabCtx with no timeout of its own. Every browser tool's
// controlledResult() check (tools.go) takes lv.mu via a plain,
// non-context-aware sync.Mutex.Lock() with no deadline — so once that CDP
// call hung (plausible under a heavy page, and made more likely by the
// separately-fixed unbounded ack-goroutine pile-up in
// handleScreencastEvent), lv.mu never unlocked and every tool call blocked
// forever with no error and no log line. See attach()'s and
// handleScreencastEvent's doc comments in live.go for the full analysis.
//
// Neither test below needs a real Chromium/CDP connection: TestLiveView_
// Attach_DoesNotHoldMutexAcrossCDPCall substitutes LiveView.runCDP with a
// controllable stand-in to deterministically simulate a slow/hung CDP round
// trip (impossible to reproduce with a real chromedp.Run against a
// non-chromedp context, which fails near-instantly with
// chromedp.ErrInvalidContext instead of hanging). TestLiveView_QueueAck_
// NeverBlocksAndDoesNotPileUpGoroutines proves the ack-path fix directly.
// Both would fail (hang past their own deadlines, or show goroutine
// pile-up) against the pre-fix code.

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// TestLiveView_Attach_DoesNotHoldMutexAcrossCDPCall is the direct regression
// guard for the deadlock: while attach()'s simulated CDP call is still in
// flight (proven via a synchronization channel, not a sleep), a concurrent
// lv.mu.TryLock() must succeed promptly. Against the pre-fix code (mutex
// held via defer spanning the whole function) this would time out — the
// TryLock would never succeed until the simulated CDP call itself
// completed, which is exactly the bug: a slow/hung CDP call froze lv.mu for
// everyone.
func TestLiveView_Attach_DoesNotHoldMutexAcrossCDPCall(t *testing.T) {
	cdpStarted := make(chan struct{})
	cdpRelease := make(chan struct{})
	simulatedErr := errors.New("simulated slow/hung CDP call")

	mgr := &BrowserManager{cfg: BrowserConfig{PageTimeout: 5 * time.Second}}
	lv := &LiveView{
		mgr:          mgr,
		sessionID:    "s1",
		viewers:      make(map[string]FrameSink),
		statusSinks:  make(map[string]StatusSink),
		controlSinks: make(map[string]ControlSink),
		ackCh:        make(chan int64, 1),
		runCDP: func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
			close(cdpStarted)
			select {
			case <-cdpRelease:
			case <-ctx.Done():
			}
			return simulatedErr
		},
	}

	// attach() calls chromedp.ListenTarget(listenCtx, ...) before reaching
	// lv.runCDP, and ListenTarget panics on a context that was never passed
	// through chromedp.NewContext (see chromedp.go's FromContext check) —
	// so tabCtx must be a real (if never-dialed) chromedp context, not a
	// bare context.Background(). NewContext alone doesn't allocate or dial
	// a browser; that only happens on Run, which lv.runCDP is stubbed out
	// here to never call.
	tabCtx, tabCancel := chromedp.NewContext(context.Background())
	defer tabCancel()

	attachDone := make(chan error, 1)
	go func() {
		_, err := lv.attach(tabCtx, "viewer1", func(LiveFrame) {}, nil, nil, nil)
		attachDone <- err
	}()

	select {
	case <-cdpStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("attach() never reached the CDP call")
	}

	// attach()'s simulated CDP call is now blocked in flight. lv.mu MUST be
	// free at this point — this is the invariant the deadlock postmortem
	// requires. TryLock with a bounded wait turns a regression into a fast
	// test failure instead of a hung test.
	lockAcquired := make(chan bool, 1)
	go func() { lockAcquired <- lv.mu.TryLock() }()

	select {
	case ok := <-lockAcquired:
		require.True(t, ok, "lv.mu must not be held while attach()'s CDP call is in flight "+
			"(ADR-038 deadlock postmortem: a mutex held via defer across a blocking chromedp.Run "+
			"call is exactly what froze every browser tool)")
		if ok {
			lv.mu.Unlock()
		}
	case <-time.After(2 * time.Second):
		t.Fatal("lv.mu appears to be held across the in-flight CDP call — deadlock regression")
	}

	// Also verify a concurrent controlledResult()-style read (getController)
	// completes promptly while the CDP call is still in flight — this is
	// the exact call path every browser_navigate/click/type/evaluate makes
	// before doing anything else.
	controllerDone := make(chan string, 1)
	go func() { controllerDone <- lv.getController() }()
	select {
	case got := <-controllerDone:
		require.Equal(t, "", got)
	case <-time.After(2 * time.Second):
		t.Fatal("getController() hung while attach()'s CDP call was in flight — this is the exact " +
			"path controlledResult() uses, i.e. every browser_navigate/click/type/evaluate call")
	}

	close(cdpRelease)

	select {
	case err := <-attachDone:
		require.ErrorIs(t, err, simulatedErr)
	case <-time.After(2 * time.Second):
		t.Fatal("attach() did not return after its CDP call completed")
	}

	// State must have been rolled back cleanly on failure.
	lv.mu.Lock()
	defer lv.mu.Unlock()
	require.Empty(t, lv.viewers, "a failed attach() must not leave the viewer registered")
	require.Nil(t, lv.listenCtx, "a failed attach() must not leave listenCtx set")
}

// TestLiveView_QueueAck_NeverBlocksAndDoesNotPileUpGoroutines proves the
// second half of the fix: handleScreencastEvent's ack hand-off (queueAck)
// never blocks and never spawns a goroutine, regardless of frame volume or
// whether anything is draining lv.ackCh. Under the pre-fix code (one
// `go func() { chromedp.Run(...) }()` per frame in handleScreencastEvent),
// this would show goroutine count growing roughly linearly with the number
// of frames.
func TestLiveView_QueueAck_NeverBlocksAndDoesNotPileUpGoroutines(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink), ackCh: make(chan int64, 1)}

	runtime.GC()
	before := runtime.NumGoroutine()

	const frameCount = 2000
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < frameCount; i++ {
			lv.queueAck(int64(i))
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("queueAck must never block even with nobody draining ackCh (ADR-038 deadlock " +
			"postmortem: the old handleScreencastEvent spawned a chromedp.Run goroutine per frame " +
			"here instead of a bounded, coalescing hand-off)")
	}

	// Give any (incorrectly) spawned goroutines a moment to actually start
	// before sampling, so this assertion isn't a false negative on a slow
	// CI box.
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()
	require.Less(t, after-before, 20,
		"queueAck must not spawn a goroutine per frame — got %d extra goroutines for %d frames",
		after-before, frameCount,
	)
}

// TestLiveView_RunAckWorker_CoalescesToLatestFrame proves the worker side of
// the fix: queueAck's overwrite semantics mean a slow worker only ever acks
// the most recently queued frame, never a stale backlog — the mechanism
// that keeps runAckWorker from falling behind under sustained frame volume.
func TestLiveView_RunAckWorker_CoalescesToLatestFrame(t *testing.T) {
	var mu sync.Mutex
	var ackedSessionIDs []int64
	firstCallStarted := make(chan struct{})
	releaseFirst := make(chan struct{})

	lv := &LiveView{
		sessionID: "s1",
		ackCh:     make(chan int64, 1),
		runCDP: func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
			require.Len(t, actions, 1)
			ackParams, ok := actions[0].(*page.ScreencastFrameAckParams)
			require.True(t, ok, "runAckWorker must call runCDP with a ScreencastFrameAck action")

			mu.Lock()
			ackedSessionIDs = append(ackedSessionIDs, ackParams.SessionID)
			isFirst := len(ackedSessionIDs) == 1
			mu.Unlock()

			if isFirst {
				close(firstCallStarted)
				<-releaseFirst // hold the worker here so frames 2-99 pile up behind queueAck's coalescing
			}
			return nil
		},
	}

	workerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go lv.runAckWorker(context.Background(), workerCtx)

	lv.queueAck(1)
	select {
	case <-firstCallStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never picked up the first queued frame")
	}

	// While the worker is stuck acking frame 1, queue many more — queueAck
	// must coalesce these to the latest rather than blocking or queueing
	// all of them individually.
	for i := int64(2); i <= 100; i++ {
		lv.queueAck(i)
	}

	close(releaseFirst)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(ackedSessionIDs) == 2
	}, 2*time.Second, 10*time.Millisecond, "expected exactly 2 ack calls total: frame 1, then the coalesced latest")

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []int64{1, 100}, ackedSessionIDs,
		"the worker must ack frame 1 (already in flight) then the coalesced latest frame (100), never 2-99")
}
