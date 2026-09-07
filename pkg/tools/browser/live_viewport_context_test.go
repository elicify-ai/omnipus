package browser

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

type viewportTargetTestKey struct{}

func viewportContextFixture(t *testing.T) (*BrowserManager, *LiveViewRegistry, *LiveView, *atomic.Int32) {
	t.Helper()
	m := newTestManagerWithFakeTabs(t)
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
