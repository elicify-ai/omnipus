package browser

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// A tab switch owes one measured recapture (ADR-047); installing its new
// document listener must not replace that picture. Real capture transitions,
// admission, listener installation, and event processing remain active. The
// fixture replaces only browser protocol execution and encoder transport.
func TestLiveDocumentRebindPreservesMeasuredSwitch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		target := f.lv.tabCtx
		// Stage the legal ordering where the callback's measured refresh wins
		// scheduling before replacement-listener initialization starts.
		_, err := f.cs.BeginFrameTransition("previous-target", 800, 600, 1)
		require.NoError(t, err)
		require.NoError(t, f.reg.RefreshCaptureFrameContext(target, testSessionID, f.cs))
		measured := f.cs.FrameState()
		f.viewportFrameFixture.mu.Lock()
		commands := append([]CaptureFrameState(nil), f.commands...)
		f.viewportFrameFixture.mu.Unlock()
		require.Equal(t, []CaptureFrameState{measured}, commands)

		oldTarget, cancelOld := context.WithCancel(context.Background())
		t.Cleanup(cancelOld)
		f.lv.tabCtx, f.lv.listenCtx, f.lv.stopListen = oldTarget, oldTarget, cancelOld
		close(f.paint)
		f.lv.rebindWatch(target, false)
		synctest.Wait()

		require.Equal(t, measured, f.cs.FrameState(), "listener discovery replaced the switch's measured picture")
		f.viewportFrameFixture.mu.Lock()
		defer f.viewportFrameFixture.mu.Unlock()
		require.Equal(t, []CaptureFrameState{measured}, f.commands, "one tab switch sent a duplicate encoder recapture")
	})
}

func TestLiveDocumentRebindStillInvalidatesRealNavigation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		target := f.lv.tabCtx
		oldTarget, cancelOld := context.WithCancel(context.Background())
		t.Cleanup(cancelOld)
		f.lv.tabCtx, f.lv.listenCtx, f.lv.stopListen = oldTarget, oldTarget, cancelOld
		entered, allowDiscovery := make(chan struct{}), make(chan struct{})
		var first atomic.Bool
		previous := f.lv.runCDP
		f.lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
			if len(actions) == 1 {
				if _, ok := actions[0].(chromedp.ActionFunc); ok && first.CompareAndSwap(false, true) {
					close(entered)
					<-allowDiscovery
				}
			}
			return previous(ctx, timeout, actions...)
		}
		f.lv.rebindWatch(target, false)
		<-entered
		f.lv.mu.Lock()
		watch := f.lv.documentWatch
		f.lv.mu.Unlock()
		require.NotNil(t, watch)
		before := f.cs.FrameState()
		f.mu.Lock()
		f.loader = "next"
		f.mu.Unlock()
		watch.enqueue(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "next"})
		watch.enqueue(&page.EventFrameNavigated{Frame: &cdp.Frame{ID: "main", LoaderID: "next"}})
		close(allowDiscovery)
		synctest.Wait()
		pending := f.cs.FrameState()
		require.Greater(t, pending.Generation, before.Generation)
		require.False(t, pending.Ready)
		require.Zero(t, pending.Width, "navigation must mask the old picture before paint")
		close(f.paint)
		synctest.Wait()
		f.viewportFrameFixture.mu.Lock()
		defer f.viewportFrameFixture.mu.Unlock()
		require.Len(t, f.commands, 1, "actual navigation still owes one measured recapture")
		require.Equal(t, 1000, f.commands[0].Width)
		require.Equal(t, 700, f.commands[0].Height)
	})
}
