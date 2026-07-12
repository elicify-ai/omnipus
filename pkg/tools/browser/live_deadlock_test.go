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
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
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

// ---------------------------------------------------------------------------
// ADR-041 F1/F2 — rebindScreencast ordering + failure-notification regression
// coverage (7-reviewer fix wave, 2026-07-12).
//
// F1's bug: rebindScreencast used to capture oldListenCtx under lock, then
// release the lock and call oldStopListen() (canceling it) WITHOUT first
// nilling lv.listenCtx — leaving lv.listenCtx pointing at the about-to-die
// context for the entire teardown window. watchForUnexpectedDeath (armed for
// that epoch by the earlier attach()) treats "my watched listenCtx died
// while STILL installed as lv.listenCtx" as a genuine external death, so on
// every ordinary tab switch it could race in, fire a FALSE "session ended
// unexpectedly" broadcast to every viewer, and nil lv.listenCtx itself —
// which then made rebindScreencast's own re-lock guard see a mismatch and
// bail out without ever starting the new screencast. The fix mirrors
// detach()'s discipline: nil lv.listenCtx/lv.stopListen under lv.mu BEFORE
// releasing the lock and calling oldStopListen(), turning "the watcher never
// observes it as still installed" into a genuine happens-before guarantee
// rather than a race that's merely unlikely to lose.
// ---------------------------------------------------------------------------

// TestLiveView_RebindScreencast_NoFalseDeathBroadcast is the regression
// guard for F1. It proves two things about a successful rebind:
//  1. By the time the OLD screencast's StopScreencast CDP call is made,
//     lv.listenCtx is ALREADY nil — the load-bearing ordering the fix
//     requires (checked directly inside the runCDP stub, not inferred from
//     timing).
//  2. The real background watchForUnexpectedDeath goroutine attach() started
//     for the OLD epoch never fires a status broadcast — because of (1) this
//     is a deterministic non-race, not a best-effort "unlikely to catch it"
//     timing assumption, so this assertion is safe from flaking.
//
// It also proves the second half of the postmortem: the new epoch (a fresh
// listenCtx bound to the new active tab) is actually installed, so the live
// view doesn't sit dead after the switch.
func TestLiveView_RebindScreencast_NoFalseDeathBroadcast(t *testing.T) {
	mgr := &BrowserManager{cfg: BrowserConfig{PageTimeout: 5 * time.Second}}

	var statusMu sync.Mutex
	var statusMessages []string
	onStatus := func(msg string) {
		statusMu.Lock()
		statusMessages = append(statusMessages, msg)
		statusMu.Unlock()
	}

	stopCalledWithNilListenCtx := make(chan bool, 1)

	// Declared before assigning runCDP (rather than inline in the composite
	// literal) so the closure below can close over lv itself — a `lv :=
	// &LiveView{runCDP: func(...) { lv... } }` self-reference inside its own
	// initializer would not compile (lv isn't in scope yet at that point).
	lv := &LiveView{
		mgr:          mgr,
		sessionID:    "s1",
		viewers:      make(map[string]FrameSink),
		statusSinks:  make(map[string]StatusSink),
		controlSinks: make(map[string]ControlSink),
		tabsSinks:    make(map[string]TabsSink),
		ackCh:        make(chan int64, 1),
	}
	lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
		for _, a := range actions {
			if _, ok := a.(*page.StopScreencastParams); ok {
				// The load-bearing F1 assertion: oldStopListen() (which
				// wakes the OLD epoch's watchForUnexpectedDeath) is called
				// BEFORE this runCDP invocation in rebindScreencast — so by
				// now lv.listenCtx must already be nil if the fix's
				// ordering is correct.
				lv.mu.Lock()
				stopCalledWithNilListenCtx <- lv.listenCtx == nil
				lv.mu.Unlock()
			}
		}
		return nil
	}

	oldTabCtx, oldCancel := chromedp.NewContext(context.Background())
	defer oldCancel()

	controlledByOther, err := lv.attach(oldTabCtx, "viewer1", func(LiveFrame) {}, onStatus, nil, nil)
	require.NoError(t, err)
	require.False(t, controlledByOther)

	lv.mu.Lock()
	oldListenCtx := lv.listenCtx
	lv.mu.Unlock()
	require.NotNil(t, oldListenCtx, "attach() must have installed an active epoch")

	newTabCtx, newCancel := chromedp.NewContext(context.Background())
	defer newCancel()

	lv.rebindScreencast(newTabCtx)

	select {
	case ok := <-stopCalledWithNilListenCtx:
		require.True(t, ok, "lv.listenCtx must already be nil by the time the OLD screencast's "+
			"StopScreencast CDP call is made — the fix requires nilling it BEFORE oldStopListen()/the "+
			"CDP stop call, not after (F1)")
	case <-time.After(2 * time.Second):
		t.Fatal("rebindScreencast never reached the StopScreencast CDP call")
	}

	// Give the REAL background watcher goroutine (started by attach() for
	// the old epoch) every opportunity to misfire before asserting it
	// didn't. Per the fix, this is a genuine happens-before guarantee, not
	// an unlikely race — see the file-level comment above.
	time.Sleep(100 * time.Millisecond)

	statusMu.Lock()
	got := append([]string(nil), statusMessages...)
	statusMu.Unlock()
	assert.Empty(t, got, "an ordinary tab switch must never emit a false "+
		"'session ended unexpectedly' broadcast to attached viewers: %v", got)

	lv.mu.Lock()
	defer lv.mu.Unlock()
	require.NotNil(t, lv.listenCtx, "rebindScreencast must install a new epoch's listenCtx after a "+
		"successful switch, not leave the live view dead (F1)")
	assert.True(t, lv.listenCtx != oldListenCtx, "the new epoch's listenCtx must be a fresh context, not the old one")
	assert.Equal(t, newTabCtx, lv.tabCtx, "lv.tabCtx must now point at the newly active tab")
}

// TestLiveView_RebindScreencast_StartFailure_NotifiesStatusSink is the
// regression guard for F2: when rebindScreencast's attempt to start the
// screencast on the NEW active tab fails, every attached viewer's
// StatusSink must be notified — before the fix, this failure branch only
// logged a warning and cleared lv.listenCtx, so no StatusSink ever fired
// (watchForUnexpectedDeath is only armed AFTER a successful StartScreencast,
// never reached on this branch) and viewers were left silently staring at
// the stale OLD tab's cached last frame under a tab strip claiming a
// DIFFERENT tab was now active.
func TestLiveView_RebindScreencast_StartFailure_NotifiesStatusSink(t *testing.T) {
	mgr := &BrowserManager{cfg: BrowserConfig{PageTimeout: 5 * time.Second}}

	var statusMu sync.Mutex
	var statusMessages []string
	onStatus := func(msg string) {
		statusMu.Lock()
		statusMessages = append(statusMessages, msg)
		statusMu.Unlock()
	}

	startScreencastErr := errors.New("simulated start-screencast failure on the new tab")
	var startCalls int32

	lv := &LiveView{
		mgr:          mgr,
		sessionID:    "s1",
		viewers:      make(map[string]FrameSink),
		statusSinks:  make(map[string]StatusSink),
		controlSinks: make(map[string]ControlSink),
		tabsSinks:    make(map[string]TabsSink),
		ackCh:        make(chan int64, 1),
		runCDP: func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
			for _, a := range actions {
				if _, ok := a.(*page.StartScreencastParams); ok {
					if atomic.AddInt32(&startCalls, 1) == 1 {
						return nil // attach()'s initial screencast succeeds
					}
					return startScreencastErr // rebind's new-tab attempt fails
				}
			}
			return nil // StopScreencast / ack calls succeed
		},
	}

	oldTabCtx, oldCancel := chromedp.NewContext(context.Background())
	defer oldCancel()
	_, err := lv.attach(oldTabCtx, "viewer1", func(LiveFrame) {}, onStatus, nil, nil)
	require.NoError(t, err)

	newTabCtx, newCancel := chromedp.NewContext(context.Background())
	defer newCancel()

	lv.rebindScreencast(newTabCtx)

	require.Eventually(t, func() bool {
		statusMu.Lock()
		defer statusMu.Unlock()
		return len(statusMessages) > 0
	}, 2*time.Second, 10*time.Millisecond,
		"F2: a rebind that fails to start the new tab's screencast must notify attached viewers' "+
			"StatusSink, not leave them silently frozen on the stale old frame")

	statusMu.Lock()
	got := append([]string(nil), statusMessages...)
	statusMu.Unlock()
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "re-attach", "the failure message must tell the viewer how to recover")

	lv.mu.Lock()
	defer lv.mu.Unlock()
	assert.Nil(t, lv.listenCtx, "a failed rebind must leave listenCtx cleared (no half-installed epoch)")
}
