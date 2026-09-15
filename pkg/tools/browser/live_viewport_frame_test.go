package browser

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
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
