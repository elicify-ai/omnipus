package browser

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// A fresh view has not received a metadata callback. Its first real switch
// must still retire the old picture and request the measured selected target.
func TestFreshLiveAttachmentFirstSwitchRecapturesMeasuredTarget(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = func(int) (bool, bool) { return false, true }
	t.Cleanup(m.Shutdown)
	first, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, firstID, err := m.activeTargetSnapshot(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	_, secondID, err := m.activeTargetSnapshot(testSessionID)
	require.NoError(t, err)
	require.NotEqual(t, firstID, secondID)
	_, exists := m.live.lookup(testSessionID)
	require.False(t, exists, "fixture must not prime a live-view baseline before the fresh attachment")
	lv := m.live.view(testSessionID)
	// Install the protocol fixture before attachment starts asynchronous discovery.
	executor := liveInputExecutor(func(_ context.Context, method string, _, result any) error {
		switch method {
		case "Page.getFrameTree":
			fixtureValue[*page.GetFrameTreeReturns](result).FrameTree = &page.FrameTree{Frame: &cdp.Frame{ID: "main", LoaderID: "loaded"}}
		case "Page.createIsolatedWorld":
			fixtureValue[*page.CreateIsolatedWorldReturns](result).ExecutionContextID = 71
		case "Runtime.evaluate":
		default:
			return fmt.Errorf("unexpected browser protocol command %s", method)
		}
		return nil
	})
	lv.runCDP = func(ctx context.Context, _ time.Duration, actions ...chromedp.Action) error {
		for _, action := range actions {
			switch measured := action.(type) {
			case viewportFrameGeometryAction:
				if chromedp.FromContext(ctx) != chromedp.FromContext(first) {
					return fmt.Errorf("measurement reached a different target")
				}
				*measured.width, *measured.height, *measured.scale = 913, 617, 1.25
			case documentPaintAction, chromedp.ActionFunc:
				if runErr := action.Do(cdp.WithExecutor(ctx, executor)); runErr != nil {
					return runErr
				}
			default:
				return fmt.Errorf("unexpected browser action %T", action)
			}
		}
		return nil
	}
	_, err = m.live.AttachContext(context.Background(), testSessionID, "fresh-viewer", nil, nil, nil)
	require.NoError(t, err)
	_, exists = m.live.lookup(testSessionID)
	require.True(t, exists)
	cs, err := NewCaptureSessionWithDeps(m, "fresh-view", &adapterRelay{nextToken: 40}, fakeEncoderStarter(new(int32), nil), nil)
	require.NoError(t, err)
	t.Cleanup(cs.Stop)
	cs.panelSessionID = testSessionID
	_, err = m.EnsureCaptureSessionForPanel(testSessionID, func() (*CaptureSession, error) { return cs, nil })
	require.NoError(t, err)
	before, err := cs.BeginFrameTransition(string(secondID), 800, 600, 1)
	require.NoError(t, err)
	received := make(chan CaptureFrameState, 2)
	_, _, err = cs.BindIngestRecaptureContext(context.Background(), func(string, *string, int, int, int) error { return nil }, func(ctx context.Context, frame CaptureFrameState, current func() bool) error {
		if ctx.Err() != nil || !current() {
			return context.Canceled
		}
		select {
		case received <- frame:
			return nil
		default:
			return fmt.Errorf("unexpected repeated recapture")
		}
	}, func() {})
	require.NoError(t, err)

	_, err = m.SwitchTab(testSessionID, 0)
	require.NoError(t, err)
	select {
	case frame := <-received:
		require.Equal(t, string(firstID), frame.TargetID)
		require.Equal(t, 913, frame.Width)
		require.Equal(t, 617, frame.Height)
		require.Equal(t, 1.25, frame.Scale)
		require.Greater(t, frame.Generation, before.Generation)
		require.False(t, frame.Ready, "a recapture request is not yet a displayed frame")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first switch after fresh attachment never requested its measured target")
	}
	select {
	case duplicate := <-received:
		t.Fatalf("one switch sent an extra recapture: %+v", duplicate)
	case <-time.After(50 * time.Millisecond):
	}
}

// Observe the actual browserAlive boundary without adding a production hook.
// Capture and LiveView operations remain available while this check waits.
type reviewDeathContext struct {
	context.Context
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (c *reviewDeathContext) Err() error {
	c.once.Do(func() { close(c.entered) })
	<-c.resume
	return context.Canceled
}

func TestLiveDeathWatcherRetainsOriginalSource(t *testing.T) {
	for _, scenario := range []string{"current_death", "replacement_watch_and_capture", "pending_capture_same_watch", "new_generation_same_capture", "pending_capture_before_death"} {
		t.Run(scenario, func(t *testing.T) {
			m := newTestManagerWithFakeTabs(t)
			m.memoryPressureFn = func(int) (bool, bool) { return false, true }
			t.Cleanup(m.Shutdown)
			_, err := m.live.AttachContext(context.Background(), testSessionID, "original-viewer", nil, nil, nil)
			require.NoError(t, err)
			lv, exists := m.live.lookup(testSessionID)
			require.True(t, exists)
			targetCtx, targetID, err := m.activeTargetSnapshot(testSessionID)
			require.NoError(t, err)
			original, err := NewCaptureSessionWithDeps(nil, "original", &fakeRelay{}, nil, nil)
			require.NoError(t, err)
			t.Cleanup(original.Stop)
			_, err = original.BeginFrameTransition(string(targetID), 800, 600, 1)
			require.NoError(t, err)
			_, err = m.EnsureCaptureSessionForPanel(testSessionID, func() (*CaptureSession, error) { return original, nil })
			require.NoError(t, err)
			replacement, err := NewCaptureSessionWithDeps(nil, "replacement", &fakeRelay{}, nil, nil)
			require.NoError(t, err)
			t.Cleanup(replacement.Stop)
			installReplacement := func() { m.captureMu.Lock(); m.captures[testSessionID] = replacement; m.captureMu.Unlock() }
			if scenario == "pending_capture_before_death" {
				installReplacement()
			}

			status := make(chan string, 2)
			watched, cancelWatch := context.WithCancel(targetCtx)
			t.Cleanup(cancelWatch)
			lv.mu.Lock()
			previousCancel := lv.stopListen
			lv.listenCtx, lv.stopListen = watched, cancelWatch
			lv.statusSinks["original-viewer"] = func(message string) {
				select {
				case status <- message:
				default:
				}
			}
			lv.mu.Unlock()
			previousCancel() // Its goroutine sees a different installed listen context and exits.
			observed := &reviewDeathContext{Context: context.Background(), entered: make(chan struct{}), resume: make(chan struct{})}
			m.mu.Lock()
			previousBrowserCtx := m.sessions[testSessionID].browserCtx
			m.sessions[testSessionID].browserCtx = observed
			m.mu.Unlock()
			var resumeOnce sync.Once
			resume := func() { resumeOnce.Do(func() { close(observed.resume) }) }
			t.Cleanup(resume)
			var cancelNext context.CancelFunc
			finished := make(chan struct{})
			go func() { defer close(finished); lv.watchForUnexpectedDeath(watched) }()
			t.Cleanup(func() {
				resume()
				select {
				case <-finished:
				case <-time.After(time.Second):
					t.Error("old watcher did not drain")
				}
				m.mu.Lock()
				m.sessions[testSessionID].browserCtx = previousBrowserCtx
				m.mu.Unlock()
				if cancelNext != nil {
					cancelNext()
				}
			})
			cancelWatch()
			select {
			case <-observed.entered:
			case <-time.After(time.Second):
				t.Fatal("watcher never reached the real browser death observation")
			}
			switch scenario {
			case "replacement_watch_and_capture":
				installReplacement()
				var nextTarget context.Context
				nextTarget, cancelNext = chromedp.NewContext(context.Background())
				lv.rebindWatch(nextTarget, true)
			case "pending_capture_same_watch":
				installReplacement()
			case "new_generation_same_capture":
				_, err = original.BeginFrameTransition("new-target", 901, 701, 1.5)
				require.NoError(t, err)
			}
			resume()
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("watcher did not complete")
			}
			if scenario == "current_death" {
				select {
				case <-original.Done():
				default:
					t.Error("current dead source was not stopped")
				}
				select {
				case <-status:
				default:
					t.Error("current browser death was not reported")
				}
			} else {
				protected := original
				if scenario != "new_generation_same_capture" {
					protected = replacement
				}
				select {
				case <-protected.Done():
					t.Error("old watcher stopped a replacement or changed source")
				default:
				}
				select {
				case message := <-status:
					t.Errorf("old watcher published death for a replacement: %s", message)
				default:
				}
			}
		})
	}
}
