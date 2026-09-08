package browser

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

func captureResolutionFixture(t *testing.T, warm bool) (*CaptureSession, *BrowserManager, context.Context) {
	t.Helper()
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = func(int) (bool, bool) { return false, true }
	m.cfg.PageTimeout = time.Second
	t.Cleanup(m.Shutdown)
	var active context.Context
	if warm {
		var err error
		active, err = m.Session(testSessionID)
		if err != nil {
			t.Fatal(err)
		}
	}
	cs, _ := newRecoveryTestSession(t, &fakeRelay{})
	cs.mgr, cs.panelSessionID = m, testSessionID
	return cs, m, active
}

func captureGateHasWaiter(m *BrowserManager) bool {
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		m.mu.Lock()
		gate := m.tabCommands[testSessionID]
		queued := gate != nil && gate.users >= 2
		m.mu.Unlock()
		if queued {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

func TestPrepareColdFrameCancellationDoesNotCreateSession(t *testing.T) {
	for _, stopCapture := range []bool{false, true} {
		name := "caller"
		if stopCapture {
			name = "capture_stop"
		}
		t.Run(name, func(t *testing.T) {
			cs, m, _ := captureResolutionFixture(t, false)
			release, err := m.acquireLiveTabCommand(context.Background(), testSessionID)
			if err != nil {
				t.Fatal(err)
			}
			var once sync.Once
			releaseGate := func() { once.Do(release) }
			defer releaseGate()
			caller, cancel := context.WithCancel(context.Background())
			defer cancel()
			var measurements atomic.Int32
			done := make(chan error, 1)
			go func() {
				_, err := cs.prepareEncoderFrame(caller, func(context.Context) (int, int, float64, error) { measurements.Add(1); return 800, 600, 1, nil })
				done <- err
			}()
			queued := captureGateHasWaiter(m)
			if stopCapture {
				cs.Stop()
			} else {
				cancel()
			}
			var got error
			prompt := false
			select {
			case got = <-done:
				prompt = true
			case <-time.After(200 * time.Millisecond):
			}
			releaseGate()
			if !prompt {
				got = <-done
			}
			if !queued || !prompt || !errors.Is(got, context.Canceled) {
				t.Errorf("cold preparation queued=%t prompt=%t error=%v", queued, prompt, got)
			}
			m.mu.Lock()
			sessions := len(m.sessions)
			m.mu.Unlock()
			if sessions != 0 || measurements.Load() != 0 || cs.FrameState().Generation != 0 {
				t.Errorf("canceled preparation published state: sessions=%d measures=%d frame=%+v", sessions, measurements.Load(), cs.FrameState())
			}
		})
	}
}

func TestCaptureFocusCancellationStopsPendingCDP(t *testing.T) {
	for _, stopCapture := range []bool{false, true} {
		name := "caller"
		if stopCapture {
			name = "capture_stop"
		}
		t.Run(name, func(t *testing.T) {
			cs, m, active := captureResolutionFixture(t, true)
			entered, finished := make(chan struct{}), make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			releaseCDP := func() { once.Do(func() { close(release) }) }
			defer releaseCDP()
			var effects atomic.Int32
			m.tabFocusFn = func(ctx context.Context, _ ...chromedp.Action) error {
				defer close(finished)
				close(entered)
				select {
				case <-ctx.Done():
				case <-release:
				}
				if ctx.Err() == nil {
					effects.Add(1)
				}
				return ctx.Err()
			}
			caller, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan bool, 1)
			go func() { done <- cs.bringAgentTabToFront(caller) }()
			<-entered
			if stopCapture {
				cs.Stop()
			} else {
				cancel()
			}
			landed, prompt := false, false
			select {
			case landed = <-done:
				prompt = true
			case <-time.After(200 * time.Millisecond):
			}
			drained := false
			select {
			case <-finished:
				drained = true
			case <-time.After(200 * time.Millisecond):
			}
			releaseCDP()
			if !prompt {
				landed = <-done
			}
			if !drained {
				<-finished
			}
			if !prompt || !drained || landed || effects.Load() != 0 {
				t.Errorf("canceled focus prompt=%t drained=%t landed=%t late effects=%d", prompt, drained, landed, effects.Load())
			}
			if active.Err() != nil {
				t.Error("focus cancellation killed the persistent target")
			}
		})
	}
}

func TestCaptureFocusCanceledAdmissionCannotDispatchLater(t *testing.T) {
	for _, stopCapture := range []bool{false, true} {
		name := "caller"
		if stopCapture {
			name = "capture_stop"
		}
		t.Run(name, func(t *testing.T) {
			cs, m, _ := captureResolutionFixture(t, true)
			release, err := m.acquireLiveTabCommand(context.Background(), testSessionID)
			if err != nil {
				t.Fatal(err)
			}
			var once sync.Once
			releaseGate := func() { once.Do(release) }
			defer releaseGate()
			called := make(chan struct{}, 1)
			m.tabFocusFn = func(context.Context, ...chromedp.Action) error { called <- struct{}{}; return nil }
			caller, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan bool, 1)
			go func() { done <- cs.bringAgentTabToFront(caller) }()
			queued := captureGateHasWaiter(m)
			if stopCapture {
				cs.Stop()
			} else {
				cancel()
			}
			landed, prompt := false, false
			select {
			case landed = <-done:
				prompt = true
			case <-time.After(200 * time.Millisecond):
			}
			releaseGate()
			if !prompt {
				landed = <-done
			}
			late := false
			select {
			case <-called:
				late = true
			case <-time.After(100 * time.Millisecond):
			}
			if !queued || !prompt || landed || late {
				t.Errorf("canceled admission queued=%t prompt=%t landed=%t late focus=%t", queued, prompt, landed, late)
			}
		})
	}
}

func TestCaptureFocusNeverCreatesMissingOrDeadTarget(t *testing.T) {
	for _, dead := range []bool{false, true} {
		name := "missing"
		if dead {
			name = "dead"
		}
		t.Run(name, func(t *testing.T) {
			cs, m, _ := captureResolutionFixture(t, dead)
			create := m.createTabFn
			var creates, focuses atomic.Int32
			m.createTabFn = func(ctx context.Context, id target.ID) (*tabEntry, error) { creates.Add(1); return create(ctx, id) }
			m.tabFocusFn = func(context.Context, ...chromedp.Action) error { focuses.Add(1); return nil }
			if dead {
				m.mu.Lock()
				tab := m.sessions[testSessionID].active()
				deadCtx, cancelDead := context.WithCancel(tab.ctx)
				tab.ctx = deadCtx
				m.mu.Unlock()
				// Kill the target context without double-canceling the fake
				// chromedp allocator during the manager's subsequent cleanup.
				cancelDead()
			}
			landed := cs.bringAgentTabToFront(context.Background())
			if landed || creates.Load() != 0 || focuses.Load() != 0 {
				t.Errorf("focus manufactured or used invalid target: landed=%t creates=%d focuses=%d", landed, creates.Load(), focuses.Load())
			}
		})
	}
}

func TestCaptureFocusUsesSelectedTargetWithoutOwningItsLifetime(t *testing.T) {
	cs, m, _ := captureResolutionFixture(t, true)
	if _, err := m.OpenTab(testSessionID); err != nil {
		t.Fatal(err)
	}
	active, _, err := m.activeTargetSnapshot(testSessionID)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	m.tabFocusFn = func(ctx context.Context, actions ...chromedp.Action) error {
		calls++
		if chromedp.FromContext(ctx) != chromedp.FromContext(active) {
			t.Error("focus targeted a different tab")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			t.Error("focus omitted its existing five-second budget")
		}
		if len(actions) != 2 {
			t.Errorf("focus action count=%d", len(actions))
			return errors.New("unexpected focus actions")
		}
		if _, ok := actions[0].(*page.BringToFrontParams); !ok {
			t.Errorf("first action=%T", actions[0])
		}
		if focus, ok := actions[1].(*emulation.SetFocusEmulationEnabledParams); !ok || !focus.Enabled {
			t.Errorf("focus emulation action=%#v", actions[1])
		}
		return nil
	}
	caller, cancel := context.WithCancel(context.Background())
	landed := cs.bringAgentTabToFront(caller)
	cancel()
	if !landed || calls != 1 || active.Err() != nil {
		t.Errorf("valid focus landed=%t calls=%d persistent target error=%v", landed, calls, active.Err())
	}
}
