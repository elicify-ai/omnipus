package browser

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

func TestLiveDocumentStaleRecoveryIsBoundedAndFailureReportedOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		statuses := make(chan string, 4)
		f.lv.statusSinks = map[string]StatusSink{"viewer": func(message string) { statuses <- message }}
		var paints atomic.Int32
		previous := f.lv.runCDP
		f.lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
			if len(actions) == 1 {
				if _, ok := actions[0].(documentPaintAction); ok {
					attempt := paints.Add(1)
					f.mu.Lock()
					f.loader = cdp.LoaderID(fmt.Sprintf("replacement-%d", attempt))
					f.mu.Unlock()
				}
			}
			return previous(ctx, timeout, actions...)
		}
		f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "first"})
		f.commit("first")
		synctest.Wait()
		require.Equal(t, int32(2), paints.Load(), "reconciliation must attempt fresh paint once, not abandon or loop")
		require.Len(t, statuses, 1, "terminal paint failure not reported exactly once")
		time.Sleep(15 * time.Second)
		synctest.Wait()
		require.Len(t, statuses, 1, "watchdog repeated an already reported terminal failure")
		require.False(t, f.cs.FrameState().Ready)
	})
}

func TestLiveDocumentCurrentStalePaintReconcilesOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "first"})
		f.commit("first")
		<-f.paintEntered
		f.watch.mu.Lock()
		work := f.watch.work
		deadline := work.deadline
		f.watch.mu.Unlock()
		time.Sleep(250 * time.Millisecond)
		// Chrome advanced its actual committed document while the first paint
		// was outstanding. The watcher has no newer provisional navigation.
		f.mu.Lock()
		f.loader = "replacement"
		f.mu.Unlock()
		close(f.paint)
		synctest.Wait()
		f.viewportFrameFixture.mu.Lock()
		commands := append([]CaptureFrameState(nil), f.commands...)
		f.viewportFrameFixture.mu.Unlock()
		require.Len(t, commands, 1, "current stale paint left the document permanently pending")
		require.Equal(t, deadline, work.deadline, "recovery renewed the original document budget")
		require.False(t, f.cs.FrameState().Ready, "paint recovery authorized input before a media boundary")
		require.False(t, f.cs.AcceptsInputGeneration(f.before.CaptureID, f.before.Generation))
		require.True(t, f.cs.CommitFrameBoundary(commands[0].Generation, commands[0].TargetID, 91))
		require.True(t, f.cs.FrameState().Ready)
	})
}

func TestLiveDocumentStalePaintCannotAdoptOverNewerNavigation(t *testing.T) {
	for _, pending := range []string{"processed", "queued"} {
		t.Run(pending, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newDocumentEventFixture(t)
				f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "first"})
				f.commit("first")
				<-f.paintEntered
				f.mu.Lock()
				f.loader = "replacement"
				f.mu.Unlock()
				if pending == "processed" {
					f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "newer-provisional"})
				} else {
					f.watch.events = make(chan liveDocumentEvent, 1)
					f.watch.enqueue(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "newer-provisional"})
				}
				close(f.paint)
				synctest.Wait()
				f.viewportFrameFixture.mu.Lock()
				defer f.viewportFrameFixture.mu.Unlock()
				require.Empty(t, f.commands, "stale recovery recaptured an older page over newer navigation")
				require.False(t, f.cs.FrameState().Ready)
			})
		})
	}
}

func TestLiveDocumentStaleAfterPaintReconcilesBeforeCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		close(f.paint)
		previous := f.lv.runCDP
		var paints atomic.Int32
		f.lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
			err := previous(ctx, timeout, actions...)
			if len(actions) == 1 {
				if _, ok := actions[0].(documentPaintAction); ok && paints.Add(1) == 1 {
					f.mu.Lock()
					f.loader = "replacement"
					f.mu.Unlock()
				}
			}
			return err
		}
		f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "first"})
		f.commit("first")
		synctest.Wait()
		f.viewportFrameFixture.mu.Lock()
		defer f.viewportFrameFixture.mu.Unlock()
		require.Len(t, f.commands, 1, "stale verification after paint never recovered")
		require.Equal(t, int32(2), paints.Load(), "recovery skipped the fresh paint proof")
		require.False(t, f.cs.FrameState().Ready)
	})
}

func TestLiveDocumentRecoveryFencesEventsProcessedDuringRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		previous := f.lv.runCDP
		var paintFailed atomic.Bool
		f.lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
			if len(actions) == 1 {
				if _, ok := actions[0].(documentPaintAction); ok {
					paintFailed.Store(true)
					f.mu.Lock()
					f.loader = "replacement"
					f.mu.Unlock()
					return ErrStaleCaptureFrame
				}
			}
			err := previous(ctx, timeout, actions...)
			if paintFailed.Load() {
				// Even an event fully processed during the read invalidates its
				// ordering snapshot; merely checking an empty queue is insufficient.
				seq := f.watch.observed.Add(1)
				f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "subframe", LoaderID: "ignored"})
				f.watch.processed.Store(seq)
			}
			return err
		}
		f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "first"})
		f.commit("first")
		synctest.Wait()
		f.watch.mu.Lock()
		loader := f.watch.loaderID
		f.watch.mu.Unlock()
		require.Equal(t, "first", string(loader), "reconciliation adopted a tree across intervening events")
		f.viewportFrameFixture.mu.Lock()
		defer f.viewportFrameFixture.mu.Unlock()
		require.Empty(t, f.commands)
	})
}
