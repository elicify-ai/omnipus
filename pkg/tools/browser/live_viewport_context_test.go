package browser

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type viewportTargetTestKey struct{}

func viewportContextFixture(t *testing.T) (*BrowserManager, *LiveViewRegistry, *LiveView, *atomic.Int32) {
	t.Helper()
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = func(int) (bool, bool) { return false, true }
	t.Cleanup(m.Shutdown)
	tabCtx, err := m.Session(testSessionID)
	require.NoError(t, err)
	calls := &atomic.Int32{}
	lv := &LiveView{mgr: m, sessionID: testSessionID, tabCtx: tabCtx, viewers: make(map[string]struct{})}
	lv.runCDP = func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		calls.Add(1)
		if a, ok := actions[0].(layoutMetricsAction); ok {
			*a.w, *a.h = 800, 600
		}
		return nil
	}
	reg := &LiveViewRegistry{views: map[string]*LiveView{testSessionID: lv}}
	return m, reg, lv, calls
}

func TestViewportContextCanceledAdmissionDoesNotMutate(t *testing.T) {
	for _, gate := range []string{"already canceled", "manager", "input"} {
		t.Run(gate, func(t *testing.T) {
			m, reg, lv, calls := viewportContextFixture(t)
			lv.cssViewportW, lv.cssViewportH, lv.cssViewportScale = 900, 700, 2
			caller, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			switch gate {
			case "already canceled":
				cancel()
			case "manager":
				release, err := m.acquireLiveTabCommand(context.Background(), testSessionID)
				require.NoError(t, err)
				defer release()
			case "input":
				lv.mu.Lock()
				state := lv.inputStateLocked()
				lv.mu.Unlock()
				state.gate <- struct{}{}
				defer func() { <-state.gate }()
			}
			applied, err := reg.SetViewportContext(caller, testSessionID, 800, 600, 1)
			require.Error(t, err)
			require.False(t, applied)
			require.Zero(t, calls.Load(), "canceled admission must never reach browser")
			require.Zero(t, lv.lastRequestedW, "rejected command must not become future replay")
			require.Equal(t, 900, lv.cssViewportW, "canceled waiters must preserve verified geometry")
			require.Equal(t, 700, lv.cssViewportH)
			require.Equal(t, float64(2), lv.cssViewportScale)
		})
	}
}

func TestViewportContextRejectsObsoleteTarget(t *testing.T) {
	m, _, lv, calls := viewportContextFixture(t)
	old := lv.tabCtx
	_, err := m.OpenTab(testSessionID)
	require.NoError(t, err)
	applied, err := lv.applyViewportContext(context.Background(), old, 800, 600, 1)
	require.Error(t, err)
	require.False(t, applied)
	require.Zero(t, calls.Load(), "old target must be rejected before first browser mutation")
	require.Zero(t, lv.lastRequestedW)
}

func TestViewportContextCancelsInFlightBoundsWithoutRetry(t *testing.T) {
	_, reg, lv, calls := viewportContextFixture(t)
	caller, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	lv.runCDP = func(ctx context.Context, _ time.Duration, _ ...chromedp.Action) error {
		calls.Add(1)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
			return context.DeadlineExceeded
		}
	}
	started := time.Now()
	applied, err := reg.SetViewportContext(caller, testSessionID, 800, 600, 1)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, applied)
	require.Equal(t, int32(1), calls.Load(), "a canceled command must not retry an uncertain resize")
	require.Less(t, time.Since(started), 150*time.Millisecond)
}

func TestViewportContextHoldsManagerAdmissionThroughBrowserWork(t *testing.T) {
	m, reg, lv, _ := viewportContextFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	base := lv.runCDP
	var first atomic.Bool
	lv.runCDP = func(ctx context.Context, d time.Duration, actions ...chromedp.Action) error {
		if first.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		return base(ctx, d, actions...)
	}
	done := make(chan error, 1)
	go func() {
		_, err := reg.SetViewportContext(context.Background(), testSessionID, 800, 600, 1)
		done <- err
	}()
	<-entered
	caller, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	_, err := m.SwitchTabContext(caller, testSessionID, 0)
	cancel()
	close(release)
	require.NoError(t, <-done)
	require.ErrorIs(t, err, context.DeadlineExceeded, "tab mutation must wait until viewport sequence finishes")
}

func TestViewportContextCancellationAfterBoundsStopsLaterStages(t *testing.T) {
	for _, phase := range []string{"scale", "measurement"} {
		t.Run(phase, func(t *testing.T) {
			_, reg, lv, calls := viewportContextFixture(t)
			caller, cancel := context.WithCancel(context.Background())
			defer cancel()
			lv.cssViewportW, lv.cssViewportH, lv.cssViewportScale = 900, 700, 2
			lv.runCDP = func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
				n := calls.Add(1)
				if (phase == "scale" && n == 2) || (phase == "measurement" && n == 3) {
					cancel()
					return context.Canceled
				}
				if a, ok := actions[0].(layoutMetricsAction); ok {
					*a.w, *a.h = 800, 600
				}
				return nil
			}
			applied, err := reg.SetViewportContext(caller, testSessionID, 800, 600, 1)
			require.True(t, applied, "acknowledged bounds remain applied even if a later stage is canceled")
			require.ErrorIs(t, err, context.Canceled)
			expected := int32(2)
			if phase == "measurement" {
				expected = 3
			}
			require.Equal(t, expected, calls.Load(), "cancellation must stop all later browser work")
			require.Zero(t, lv.cssViewportW, "unverified geometry must not survive cancellation")
			require.Zero(t, lv.cssViewportH)
			require.Zero(t, lv.cssViewportScale)
		})
	}
}

func TestViewportContextCanceledCompensationDoesNotStartAnotherRead(t *testing.T) {
	_, reg, lv, _ := viewportContextFixture(t)
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	var bounds, reads, readsAtCancel int
	lv.runCDP = func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		switch a := actions[0].(type) {
		case windowBoundsAction:
			bounds++
			if bounds == 2 {
				readsAtCancel = reads
				cancel()
			}
		case layoutMetricsAction:
			reads++
			*a.w, *a.h = 700, 500
		}
		return nil
	}
	applied, err := reg.SetViewportContext(caller, testSessionID, 800, 600, 1)
	require.True(t, applied)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 2, bounds, "shortfall must reach the one compensation pass")
	require.Positive(t, readsAtCancel)
	require.Equal(t, readsAtCancel, reads, "canceled compensation must not start another measurement stage")
	require.Zero(t, lv.cssViewportW)
}

func TestViewportContextManualCancellationReachesRunningCDP(t *testing.T) {
	_, reg, lv, calls := viewportContextFixture(t)
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := false
	lv.runCDP = func(ctx context.Context, _ time.Duration, _ ...chromedp.Action) error {
		calls.Add(1)
		cancel()
		select {
		case <-ctx.Done():
			observed = true
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
			return context.DeadlineExceeded
		}
	}
	applied, err := reg.SetViewportContext(caller, testSessionID, 800, 600, 1)
	require.False(t, applied)
	require.ErrorIs(t, err, context.Canceled)
	require.True(t, observed, "manual caller cancellation must reach the running target-derived CDP context")
	require.Equal(t, int32(1), calls.Load())
}

func TestViewportContextRechecksTargetAfterAdmissionWait(t *testing.T) {
	m, _, lv, calls := viewportContextFixture(t)
	old := lv.tabCtx
	_, err := m.OpenTab(testSessionID)
	require.NoError(t, err)
	_, err = m.SwitchTab(testSessionID, 0)
	require.NoError(t, err)
	release, err := m.acquireLiveTabCommand(context.Background(), testSessionID)
	require.NoError(t, err)
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	done := make(chan error, 1)
	go func() { _, err := lv.applyViewportContext(context.Background(), old, 800, 600, 1); done <- err }()
	require.Eventually(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.tabCommands[testSessionID].users == 2
	}, time.Second, time.Millisecond, "resize must wait for the active tab command")
	// Commit the admitted owner's target change before allowing resize through.
	m.mu.Lock()
	m.sessions[testSessionID].activeIdx = 1
	m.mu.Unlock()
	release()
	released = true
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("obsolete resize did not finish after admission release")
	}
	require.Zero(t, calls.Load(), "a target checked only before waiting can become obsolete")
	require.Zero(t, lv.lastRequestedW)
}

// --- moved from live.go tests 2026-09-15 ---

func TestSetViewport_RetriesOnceOnDeadlineTimeout(t *testing.T) {
	var applyAttempts int
	var calls int
	runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		calls++
		if _, isApply := actions[0].(windowBoundsAction); isApply {
			applyAttempts++
			if applyAttempts == 1 {
				return fmt.Errorf("get window for target: %w", context.DeadlineExceeded)
			}
			return nil
		}
		// The device-scale override is now its own call (see
		// viewportScaleTimeout), so the sequence is bounds -> scale ->
		// read-back and this stub must not assume every non-bounds call is a
		// read-back.
		if lm, ok := actions[0].(layoutMetricsAction); ok {
			*lm.w, *lm.h = 615, 744
		}
		return nil
	}
	reg, _ := newViewportTestLiveView(runCDP)

	applied, err := reg.SetViewport("s1", 615, 744, 1)
	require.NoError(t, err, "SetViewport must succeed when the single retry succeeds")
	require.True(t, applied)
	require.Equal(t, 2, applyAttempts, "exactly one retry after the timeout, never a loop")
}

func TestSetViewport_NoRetryOnNonDeadlineError(t *testing.T) {
	realErr := fmt.Errorf("target closed")
	var applyAttempts int
	runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		if _, isApply := actions[0].(windowBoundsAction); isApply {
			applyAttempts++
			return realErr
		}
		t.Fatal("read-back must not run after a failed apply")
		return nil
	}
	reg, _ := newViewportTestLiveView(runCDP)

	_, err := reg.SetViewport("s1", 615, 744, 1)
	require.ErrorContains(t, err, "target closed", "a non-deadline failure must surface immediately")
	require.Equal(t, 1, applyAttempts, "non-deadline errors must NOT be retried")
}

func TestSetViewport_SecondDeadlineStillFailsLoudly(t *testing.T) {
	var applyAttempts int
	runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		if _, isApply := actions[0].(windowBoundsAction); isApply {
			applyAttempts++
			return context.DeadlineExceeded
		}
		t.Fatal("read-back must not run after a failed apply")
		return nil
	}
	reg, _ := newViewportTestLiveView(runCDP)

	_, err := reg.SetViewport("s1", 615, 744, 1)
	require.ErrorIs(t, err, context.DeadlineExceeded, "two consecutive timeouts must fail loudly")
	require.ErrorContains(t, err, "after retry")
	require.Equal(t, 2, applyAttempts, "exactly two attempts, never a retry loop")
}

// A resize that HAS ALREADY HAPPENED must not be reported to the user as a
// failure because the renderer was slow to answer a sharpness request.
//
// Measured 2026-08-15 with the renderer blocked for 7s: getWindowForTarget
// 53ms, setWindowBounds 75ms — the window was resized 128ms in — and
// setDeviceMetricsOverride 6825ms. Bundled under one 5s budget that was a
// DeadlineExceeded, a full retry, up to 10s of stalling, and the operator's
// "could not resize the browser viewport" toast for a resize that had
// succeeded. It also returned before the read-back, so the cached viewport
// kept describing the PRE-resize tab and mis-aimed every later click.
func TestSetViewport_SlowScaleOverrideDoesNotFailAResizeThatLanded(t *testing.T) {
	var boundsCalls []windowBoundsAction
	var scaleCalls int
	runCDP := func(_ context.Context, timeout time.Duration, actions ...chromedp.Action) error {
		switch a := actions[0].(type) {
		case windowBoundsAction:
			require.Len(t, actions, 1,
				"the window resize must be issued alone, so a renderer-bound call cannot spend its budget")
			boundsCalls = append(boundsCalls, a)
			return nil
		case layoutMetricsAction:
			*a.w, *a.h = 615, 744
			return nil
		}
		if isScaleAction(actions[0]) {
			scaleCalls++
			require.Equal(t, viewportScaleTimeout, timeout,
				"the scale override must be spent against its OWN budget")
			return context.DeadlineExceeded
		}
		return nil
	}
	reg, lv := newViewportTestLiveView(runCDP)

	applied, err := reg.SetViewport("s1", 615, 744, 2)

	require.NoError(t, err,
		"a timed-out sharpness setting must never surface as a failed resize — the window is already the right size")
	require.True(t, applied)
	require.Len(t, boundsCalls, 1,
		"the resize landed on the first attempt; a slow scale override must not trigger the resize retry")
	require.Equal(t, 1, scaleCalls, "the scale override is attempted once and then given up on")

	lv.mu.Lock()
	defer lv.mu.Unlock()
	require.Equal(t, 615, lv.cssViewportW,
		"the sequence must continue to the read-back, so the cache describes the tab AFTER the resize")
	require.Equal(t, 744, lv.cssViewportH)
	require.Zero(t, lv.cssViewportScale,
		"the override did not land, so the scale is genuinely unknown and must not be recorded as if it had")
}

// The "resolution collapse" class: a read-back LARGER than the request.
//
// `width + (width - actual)` assumed the tab always comes back SHORT. Against a
// stale 2560-wide read for a 633-wide request it computes -1294, which
// clampViewportDim floors at 1 — a one-pixel-wide browser window. An overshoot
// needs no correction at all: the tab is already at least as big as asked.
func TestSetViewport_ReadBackLargerThanRequestIsNotCompensated(t *testing.T) {
	var boundsCalls []windowBoundsAction
	runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		switch a := actions[0].(type) {
		case windowBoundsAction:
			boundsCalls = append(boundsCalls, a)
		case layoutMetricsAction:
			*a.w, *a.h = 2560, 1440 // the launch geometry, still in force
		}
		return nil
	}
	reg, lv := newViewportTestLiveView(runCDP)

	applied, err := reg.SetViewport("s1", 633, 686, 1)
	require.NoError(t, err)
	require.True(t, applied)

	require.Len(t, boundsCalls, 1,
		"an overshoot must not be 'corrected' — there is nothing to correct")
	require.Equal(t, 633, boundsCalls[0].width)
	require.Equal(t, 686, boundsCalls[0].height)
	for _, b := range boundsCalls {
		require.Greater(t, b.width, 1, "a compensation must never ask for a one-pixel-wide window")
		require.Greater(t, b.height, 1, "a compensation must never ask for a one-pixel-tall window")
	}

	lv.mu.Lock()
	defer lv.mu.Unlock()
	require.Equal(t, 2560, lv.cssViewportW, "the cache must record the tab as it really is")
	require.Equal(t, 1440, lv.cssViewportH)
}

// Browser.setWindowBounds returns as soon as the browser process accepts the
// bounds; the renderer relays out 40-120ms later (idle) or ~350ms later (busy).
// A single read taken immediately therefore records the PRE-resize size about
// as often as the real one — and then compensates against a phantom shortfall,
// and maps every click through a number that was never true.
func TestSetViewport_ReadBackWaitsForTheTabToCatchUp(t *testing.T) {
	var boundsCalls int
	var reads int
	runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		switch a := actions[0].(type) {
		case windowBoundsAction:
			boundsCalls++
		case layoutMetricsAction:
			reads++
			if reads <= 3 {
				*a.w, *a.h = 1280, 720 // the tab has not relaid out yet
				return nil
			}
			*a.w, *a.h = 615, 744 // settled
		}
		return nil
	}
	reg, lv := newViewportTestLiveView(runCDP)

	applied, err := reg.SetViewport("s1", 615, 744, 1)
	require.NoError(t, err)
	require.True(t, applied)

	require.Greater(t, reads, 1, "the read-back must poll, not read once")
	require.Equal(t, 1, boundsCalls,
		"the resize did land — waiting for it must not be mistaken for a shortfall and 'compensated'")

	lv.mu.Lock()
	defer lv.mu.Unlock()
	require.Equal(t, 615, lv.cssViewportW, "the SETTLED read is what gets cached, never the early one")
	require.Equal(t, 744, lv.cssViewportH)
}

// Device-matrix regression coverage for the live-panel viewport.
//
// WHY THIS FILE EXISTS: every viewport bug the operator hit only manifested at
// deviceScaleFactor 2 (a Retina client), while every pre-existing test ran at
// an implicit DPR 1 — so the whole class was invisible in CI. Measured on the
// operator's own session, the server logged:
//
//	requested 654 -> actual 511   (-143)
//	requested 512 -> actual 369   (-143)
//	requested 575 -> actual 432   (-143)
//
// a flat -143px gap WITH compensated:true. Reconstructing what compensation
// actually asked for shows the real mechanism:
//
//	orig 654 -> compensated ask 797 -> got 511   (286 short)
//	orig 512 -> compensated ask 655 -> got 369   (286 short)
//	orig 575 -> compensated ask 718 -> got 432   (286 short)
//
// The true chrome delta is 286 = 143 x 2 (the deviceScaleFactor), but the
// compensator sizes its correction from the 143 visible in the first read-back
// — so it under-corrects by exactly half, on every attempt, forever. Single-shot
// therefore never converged at DSF 2 and the panel rendered short and shrank.
//
// These tests model that: a deficit applied to EVERY request including the
// compensated one, across the DPR values real clients report.
func TestSetViewport_DeviceMatrix_BoundedWhenChromeRefuses(t *testing.T) {
	for _, dpr := range []float64{1, 2, 3} {
		t.Run("dpr"+trimFloat(dpr), func(t *testing.T) {
			var bounds int
			runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
				if _, ok := actions[0].(windowBoundsAction); ok {
					bounds++
					return nil
				}
				if lm, ok := actions[0].(layoutMetricsAction); ok {
					*lm.w, *lm.h = 603, 300 // immovable: never grows, whatever we ask
					return nil
				}
				return nil
			}
			reg, lv := newViewportTestLiveView(runCDP)

			applied, err := reg.SetViewport("s1", 603, 900, dpr)
			require.NoError(t, err)
			require.True(t, applied, "an unreachable target must still apply, not error")

			// Exactly two window-bounds calls: the initial apply plus ONE
			// compensation attempt. Not iterated — the v52 diagnostics showed a
			// second setWindowBounds changes nothing (post-compensation
			// read-back == pre-compensation read-back), so a loop would repeat
			// a no-op. Asserted as an exact value: a loose upper bound would
			// pass against a loop and guard nothing.
			assert.Equal(t, 2, bounds,
				"compensation must be a single pass — the initial apply plus one attempt, got %d", bounds)

			lv.mu.Lock()
			gotH := lv.cssViewportH
			lv.mu.Unlock()
			assert.Equal(t, 300, gotH,
				"when the target is unreachable the cache must hold the REAL viewport, so input mapping "+
					"stays correct even though the panel cannot reach its requested size")
		})
	}
}

// TestSetViewport_IgnoredResize_KeepsTheTrueViewport pins the MEASURED failure
// (v52 diagnostics, deviceScaleFactor 1):
//
//	requested 587 -> first read-back 444 -> asked 730 -> still 444
//	requested 564 -> first read-back 421 -> asked 707 -> still 421
//
// The compensating setWindowBounds changes NOTHING — the post-compensation
// read-back equals the pre-compensation one exactly. Chrome ignores the resize
// rather than partially honoring it, which is why iterating cannot help and a
// convergence loop was twice built and twice reverted.
//
// What MUST hold regardless: the cached CSS viewport records the tab's TRUE
// size. That value feeds input-coordinate mapping, so getting it right is the
// difference between "the panel looks small" (cosmetic) and "clicks land in the
// wrong place" (the operator's actual complaint).
func TestSetViewport_IgnoredResize_KeepsTheTrueViewport(t *testing.T) {
	for _, dpr := range []float64{1, 1.5, 2, 3} {
		t.Run("dpr"+trimFloat(dpr), func(t *testing.T) {
			const trueH = 444 // what the tab really is, whatever we ask for
			runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
				if _, ok := actions[0].(windowBoundsAction); ok {
					return nil // accepted, and silently ignored — the measured behavior
				}
				if lm, ok := actions[0].(layoutMetricsAction); ok {
					*lm.w, *lm.h = 603, trueH
					return nil
				}
				return nil
			}
			reg, lv := newViewportTestLiveView(runCDP)

			applied, err := reg.SetViewport("s1", 603, 587, dpr)
			require.NoError(t, err)
			require.True(t, applied, "an ignored resize must still report applied, not error")

			lv.mu.Lock()
			gotH := lv.cssViewportH
			lv.mu.Unlock()
			assert.Equal(t, trueH, gotH,
				"the cache must hold the tab's TRUE height (%d), never the height we asked for — "+
					"input coordinates are mapped through this value, so a wrong number here is "+
					"exactly the mis-aimed-click bug", trueH)
		})
	}
}
