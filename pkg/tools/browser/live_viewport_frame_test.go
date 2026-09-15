package browser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type viewportFrameFixture struct {
	mgr                  *BrowserManager
	reg                  *LiveViewRegistry
	lv                   *LiveView
	cs                   *CaptureSession
	before               CaptureFrameState
	mu                   sync.Mutex
	commands             []CaptureFrameState
	bounds               int
	measuredW, measuredH int
	measuredScale        float64
	writeErr             error
	measureErr           error
	atWrite              func()
	atSend               func()
}

func newViewportFrameFixture(t *testing.T) *viewportFrameFixture {
	t.Helper()
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = func(int) (bool, bool) { return false, true }
	t.Cleanup(m.Shutdown)
	tabCtx, err := m.Session(testSessionID)
	require.NoError(t, err)
	lv := &LiveView{mgr: m, sessionID: testSessionID, tabCtx: tabCtx, viewers: make(map[string]struct{})}
	reg := &LiveViewRegistry{views: map[string]*LiveView{testSessionID: lv}}
	m.live = reg
	cs, _ := adapterFixture(t)
	cs.mgr, cs.panelSessionID = m, testSessionID
	m.captures = map[string]*CaptureSession{testSessionID: cs}
	_, targetID, err := m.activeTargetSnapshot(testSessionID)
	require.NoError(t, err)
	before, err := cs.BeginFrameTransition(string(targetID), 800, 600, 1)
	require.NoError(t, err)
	require.True(t, cs.CommitFrameBoundary(before.Generation, before.TargetID, 17))
	f := &viewportFrameFixture{mgr: m, reg: reg, lv: lv, cs: cs, before: cs.FrameState(), measuredW: 1000, measuredH: 700, measuredScale: 1.25}
	_, _, err = cs.BindIngestRecaptureContext(context.Background(), func(string, *string, int, int, int) error { return nil }, func(ctx context.Context, frame CaptureFrameState, current func() bool) error {
		if ctx.Err() != nil || !current() {
			return context.Canceled
		}
		f.mu.Lock()
		f.commands = append(f.commands, frame)
		f.mu.Unlock()
		if f.atSend != nil {
			f.atSend()
		}
		return nil
	}, func() {})
	require.NoError(t, err)
	lv.runCDP = func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		for _, action := range actions {
			switch a := action.(type) {
			case windowBoundsAction:
				f.bounds++
				if f.atWrite != nil {
					f.atWrite()
				}
				if f.writeErr != nil {
					return f.writeErr
				}
			case layoutMetricsAction:
				*a.w, *a.h = int64(f.measuredW), int64(f.measuredH)
			case viewportFrameGeometryAction:
				if f.measureErr != nil {
					return f.measureErr
				}
				*a.width, *a.height, *a.scale = f.measuredW, f.measuredH, f.measuredScale
			}
		}
		return nil
	}
	return f
}

func TestViewportFrameInvalidatesBeforeWriteAndSendsMeasuredOnce(t *testing.T) {
	f := newViewportFrameFixture(t)
	f.atWrite = func() {
		frame := f.cs.FrameState()
		require.False(t, frame.Ready, "old picture remained authorized at browser write")
		require.Greater(t, frame.Generation, f.before.Generation)
		require.Zero(t, frame.Width)
		require.Zero(t, frame.Height)
	}
	f.atSend = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		release, err := f.mgr.acquireLiveTabCommand(ctx, testSessionID)
		require.NoError(t, err, "recapture transport waited while manager admission was held")
		if release != nil {
			release()
		}
		f.lv.mu.Lock()
		gate := f.lv.inputStateLocked().gate
		f.lv.mu.Unlock()
		select {
		case gate <- struct{}{}:
			<-gate
		default:
			t.Error("recapture transport retained the input gate")
		}
	}
	applied, err := f.reg.SetViewportContext(context.Background(), testSessionID, 1000, 700, 2)
	require.NoError(t, err)
	require.True(t, applied)
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Len(t, f.commands, 1, "one measured source requires exactly one recapture")
	got := f.commands[0]
	require.Equal(t, 1000, got.Width)
	require.Equal(t, 700, got.Height)
	require.Equal(t, 1.25, got.Scale, "requested scale is not observed DPR")
	require.Equal(t, f.before.TargetID, got.TargetID)
	require.Greater(t, got.Generation, f.before.Generation)
	require.False(t, got.Ready, "a resize acknowledgement is not a presented frame")
}

func TestViewportFrameUnchangedRequestPreservesSource(t *testing.T) {
	f := newViewportFrameFixture(t)
	f.measuredW, f.measuredH, f.measuredScale = 800, 600, 1
	applied, err := f.reg.SetViewportContext(context.Background(), testSessionID, 800, 600, 1)
	require.NoError(t, err)
	require.True(t, applied)
	require.Zero(t, f.bounds, "identical measured viewport must not rewrite the browser")
	require.Equal(t, f.before, f.cs.FrameState())
	require.Empty(t, f.commands)
}

func TestViewportFrameFailureStaysPending(t *testing.T) {
	for _, phase := range []string{"bounds", "measurement"} {
		t.Run(phase, func(t *testing.T) {
			f := newViewportFrameFixture(t)
			fault := errors.New("injected browser failure")
			if phase == "bounds" {
				f.writeErr = fault
			} else {
				f.measureErr = fault
			}
			_, err := f.reg.SetViewportContext(context.Background(), testSessionID, 1000, 700, 2)
			require.Error(t, err)
			frame := f.cs.FrameState()
			require.False(t, frame.Ready)
			require.Greater(t, frame.Generation, f.before.Generation)
			require.Zero(t, frame.Width)
			require.Zero(t, frame.Height)
			require.Empty(t, f.commands, "unmeasured frame reached encoder transport")
		})
	}
}

func TestViewportFrameCanceledAdmissionPreservesPicture(t *testing.T) {
	f := newViewportFrameFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.reg.SetViewportContext(ctx, testSessionID, 1000, 700, 2)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, f.before, f.cs.FrameState())
	require.Zero(t, f.bounds)
	require.Empty(t, f.commands)
}

func TestViewportFrameRefreshRequiresOriginalPanelCapture(t *testing.T) {
	f := newViewportFrameFixture(t)
	other, _ := adapterFixture(t)
	err := f.reg.RefreshCaptureFrameContext(context.Background(), testSessionID, other)
	require.Error(t, err)
	require.Equal(t, f.before, f.cs.FrameState())
	require.Empty(t, f.commands)
}

func TestViewportTabRefreshMeasuresNewTargetWithoutPriorViewport(t *testing.T) {
	f := newViewportFrameFixture(t)
	f.lv.lastKnownActiveCtx = f.lv.tabCtx
	next, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f.mgr.mu.Lock()
	se := f.mgr.sessions[testSessionID]
	se.tabs = append(se.tabs, &tabEntry{ctx: next, targetID: "second-target"})
	se.activeIdx = 1
	tabs := snapshotTabsLocked(se)
	f.mgr.mu.Unlock()
	sent := make(chan struct{}, 1)
	f.atSend = func() { sent <- struct{}{} }
	f.lv.onTabsChanged(tabs, 1)
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("tab change without a viewport did not send a measured source")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Len(t, f.commands, 1)
	require.Equal(t, "second-target", f.commands[0].TargetID)
	require.Equal(t, 1000, f.commands[0].Width)
	require.Equal(t, 700, f.commands[0].Height)
	require.Equal(t, 1.25, f.commands[0].Scale)
	require.Greater(t, f.commands[0].Generation, f.before.Generation)
}

func TestViewportSameTabCommandRecapturesOnlyOriginalPanel(t *testing.T) {
	for _, route := range []string{"legacy", "context"} {
		t.Run(route, func(t *testing.T) {
			f := newViewportFrameFixture(t)
			_, own, _ := newPanelPicture(t, f.lv, testSessionID, "own-target")
			_, foreign, _ := newPanelPicture(t, f.lv, f.mgr.OperatorSessionID(), "foreign-target")
			f.mgr.tabFocusFn = func(context.Context, ...chromedp.Action) error { return nil }
			var err error
			if route == "context" {
				_, err = f.mgr.SwitchTabContext(context.Background(), testSessionID, 0)
			} else {
				_, err = f.mgr.SwitchTab(testSessionID, 0)
			}
			require.NoError(t, err)
			require.Equal(t, 1, own.recaptureCount(), "same-tab refresh missed its original panel")
			require.Zero(t, foreign.recaptureCount(), "same-tab refresh leaked to operator panel")
		})
	}
}

// --- moved from live.go tests 2026-09-15 ---

// The other half of F1, isolated: even with the coalescing above, an apply
// that started against tab B finishes AFTER the user has moved to C, and its
// final act was an unconditional write of B's geometry into the cache. A
// stale-but-positive cache is worse than an empty one — it passes
// rescaleToCSSViewport's cache-hit guard, so every click from then on is
// silently mapped through the dimensions of a tab the user has left.
func TestApplyViewport_DiscardsAMeasurementTheActiveTabHasMovedPast(t *testing.T) {
	tabB, cancelB := context.WithCancel(context.Background())
	t.Cleanup(cancelB)
	tabC, cancelC := context.WithCancel(context.Background())
	t.Cleanup(cancelC)

	lv := &LiveView{
		sessionID:          "s1",
		viewers:            make(map[string]struct{}),
		lastKnownActiveCtx: tabB,
	}
	lv.runCDP = func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		if a, ok := actions[0].(layoutMetricsAction); ok {
			// The user switches to C while this very read-back is happening.
			lv.mu.Lock()
			lv.lastKnownActiveCtx = tabC
			lv.mu.Unlock()
			*a.w, *a.h = 800, 600
		}
		return nil
	}

	applied, err := lv.applyViewport(tabB, 800, 600, 1)
	require.NoError(t, err)
	require.True(t, applied, "the resize itself did happen — only the cache write is in question")

	lv.mu.Lock()
	defer lv.mu.Unlock()
	assert.Zero(t, lv.cssViewportW,
		"B's geometry must not be cached while C is the active tab — the next input event re-fetches "+
			"from the live tab instead of trusting a number that describes a tab nobody is looking at")
	assert.Zero(t, lv.cssViewportH)
}

// --- F5: a degradation the user is told about -------------------------------

// The deviceScaleFactor override is renderer-bound: it only ever times out on
// a loaded box, which in practice means only on hosted Linux. The user was
// left with a persistently soft picture, no message, no control and no stated
// recovery, while the same build on macOS never showed the branch at all —
// a parity break and an ADR-061 break at once (this project's gateway log is
// WARN-only in production, so the existing WarnCF reaches nobody watching).
func TestApplyViewport_TellsTheViewerWhenTheSharpnessSettingTimedOut(t *testing.T) {
	msgs := make(chan string, 8)
	scaleFails := true
	lv := &LiveView{
		sessionID:   "s1",
		viewers:     map[string]struct{}{"v1": {}},
		statusSinks: map[string]StatusSink{"v1": func(m string) { msgs <- m }},
	}
	lv.runCDP = func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		switch a := actions[0].(type) {
		case layoutMetricsAction:
			*a.w, *a.h = 633, 686
			return nil
		case windowBoundsAction:
			return nil
		}
		if isScaleAction(actions[0]) && scaleFails {
			return context.DeadlineExceeded
		}
		return nil
	}

	applied, err := lv.applyViewport(context.Background(), 633, 686, 2)
	require.NoError(t, err, "the window did resize — the sharpness setting is cosmetic and must never fail it")
	require.True(t, applied)

	var got string
	select {
	case got = <-msgs:
	case <-time.After(3 * time.Second):
		t.Fatal("the viewer was told nothing at all — a soft picture with no message, no control " +
			"and no stated recovery is exactly the silent degradation ADR-061 forbids")
	}
	assert.Contains(t, strings.ToLower(got), "soft",
		"the message must name what the user is seeing")
	assert.Contains(t, strings.ToLower(got), "resize",
		"and how it recovers")

	// Throttled: the SPA re-sends a viewport frame throughout a panel drag, so
	// a renderer wedged for a few seconds must produce one banner, not one per
	// drag frame.
	for i := 0; i < 5; i++ {
		_, err = lv.applyViewport(context.Background(), 633, 686, 2)
		require.NoError(t, err)
	}
	select {
	case extra := <-msgs:
		t.Fatalf("a continuing degradation must not re-notify on every apply: %q", extra)
	case <-time.After(200 * time.Millisecond):
	}

	// ...but a recovery re-arms it: the next degradation is a NEW event and
	// must be reported rather than swallowed by the previous one's window.
	scaleFails = false
	_, err = lv.applyViewport(context.Background(), 633, 686, 2)
	require.NoError(t, err)
	scaleFails = true
	_, err = lv.applyViewport(context.Background(), 633, 686, 2)
	require.NoError(t, err)

	select {
	case <-msgs:
	case <-time.After(3 * time.Second):
		t.Fatal("after the picture re-sharpened, a fresh degradation must be reported again")
	}
}

// TestRescaleInputCoords covers the pure-math half of the root-cause doc's
// Fault 3 fix (docs/internal/browser-viewport-input-rootcause-2026-07-31.md):
// mapping a viewer's capture-frame pixel coordinates into the tab's CSS
// pixel space. No CDP/Chromium involved — see rescaleInputCoords's doc
// comment for why the math is factored out on its own.
func TestRescaleInputCoords(t *testing.T) {
	tests := []struct {
		name         string
		x, y         float64
		capW, capH   float64
		cssW, cssH   float64
		wantX, wantY float64
	}{
		{
			name: "identity when capture size equals CSS viewport size",
			x:    100, y: 200,
			capW: 1280, capH: 720,
			cssW: 1280, cssH: 720,
			wantX: 100, wantY: 200,
		},
		{
			// The exact root-cause doc measurement: a 319x158 capture stream
			// against a real ~1280x720 page — clicks were landing ~4x off.
			name: "root-cause scenario: 319x158 capture vs 1280x720 css",
			x:    100, y: 79, // roughly mid-frame in capture space
			capW: 319, capH: 158,
			cssW: 1280, cssH: 720,
			wantX: 100 * 1280.0 / 319.0, wantY: 79 * 720.0 / 158.0,
		},
		{
			// DPR-style: capture delivered at 2x the CSS viewport (a
			// high-DPI capture path), so coordinates must be halved.
			name: "capture at 2x css (DPR-style downscale)",
			x:    400, y: 300,
			capW: 2560, capH: 1440,
			cssW: 1280, cssH: 720,
			wantX: 200, wantY: 150,
		},
		{
			name: "zero origin stays at zero origin regardless of scale",
			x:    0, y: 0,
			capW: 319, capH: 158,
			cssW: 1280, cssH: 720,
			wantX: 0, wantY: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotX, gotY := rescaleInputCoords(tc.x, tc.y, tc.capW, tc.capH, tc.cssW, tc.cssH)
			require.InDelta(t, tc.wantX, gotX, 0.0001, "x")
			require.InDelta(t, tc.wantY, gotY, 0.0001, "y")
		})
	}
}

// invalidateCSSViewportCache must clear the scale too: a scale left behind
// from a previous viewport would be applied to a capture it no longer
// describes.
func TestInvalidateCSSViewportCacheClearsScale(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]struct{})}
	lv.cssViewportW, lv.cssViewportH = 633, 686
	lv.cssViewportScale = 2

	lv.invalidateCSSViewportCache()

	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.cssViewportScale != 0 {
		t.Fatalf("cssViewportScale = %v after invalidation, want 0", lv.cssViewportScale)
	}
}

// Belt-and-braces on the settle poll's contract: a poll that reads successfully
// but never reaches the requested size is NOT a failure — the tab really is
// that size, and recording it is what keeps clicks aimed while the panel
// renders smaller than asked. Only a poll that never read anything at all may
// invalidate.
func TestSettleCSSViewport_RecordsTheTruthWhenItCannotReachTheTarget(t *testing.T) {
	lv := &LiveView{
		sessionID: "s1",
		runCDP: func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
			if lm, ok := actions[0].(layoutMetricsAction); ok {
				*lm.w, *lm.h = 603, 300
			}
			return nil
		},
	}
	w, h, err := lv.settleCSSViewport(context.Background(), 603, 900)
	require.NoError(t, err, "an unreachable target is not a read failure")
	require.Equal(t, int64(603), w)
	require.Equal(t, int64(300), h)

	lv.runCDP = func(context.Context, time.Duration, ...chromedp.Action) error {
		return fmt.Errorf("transport wedged")
	}
	_, _, err = lv.settleCSSViewport(context.Background(), 603, 900)
	require.Error(t, err, "a poll that never read anything must report failure so the cache is invalidated")
}
