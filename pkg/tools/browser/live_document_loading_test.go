package browser

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

func TestLiveDocumentProvisionalWaitIsNotPictureFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		statuses := make(chan string, 4)
		f.lv.statusSinks = map[string]StatusSink{"viewer": func(message string) { statuses <- message }}
		f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "pending"})
		time.Sleep(15 * time.Second)
		synctest.Wait()
		require.Empty(t, statuses, "provisional navigation emitted a persistent error")
		f.watch.mu.Lock()
		reported := f.watch.work.failureReported
		f.watch.mu.Unlock()
		require.False(t, reported, "network wait was classified as a picture failure")
		require.False(t, f.cs.AcceptsInputGeneration(f.before.CaptureID, f.before.Generation))
	})
}

func TestLiveDocumentLateCommitGetsFreshBoundedPaintBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "pending"})
		time.Sleep(20 * time.Second)
		committedAt := time.Now()
		f.commit("pending")
		f.watch.mu.Lock()
		deadline := f.watch.work.deadline
		f.watch.mu.Unlock()
		require.Equal(t, committedAt.Add(15*time.Second), deadline)
		<-f.paintEntered
		time.Sleep(14 * time.Second)
		close(f.paint)
		synctest.Wait()
		f.viewportFrameFixture.mu.Lock()
		commands := append([]CaptureFrameState(nil), f.commands...)
		f.viewportFrameFixture.mu.Unlock()
		require.Len(t, commands, 1, "late committed document never reached recapture")
		require.False(t, f.cs.FrameState().Ready, "paint alone authorized input")
		require.True(t, f.cs.CommitFrameBoundary(commands[0].Generation, commands[0].TargetID, 91))
		require.True(t, f.cs.FrameState().Ready)
	})
}

func TestLiveDocumentOldNavigationTimerCannotFailFreshPaint(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		statuses := make(chan string, 4)
		f.lv.statusSinks = map[string]StatusSink{"viewer": func(message string) { statuses <- message }}
		f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "pending"})
		time.Sleep(14 * time.Second)
		f.commit("pending")
		<-f.paintEntered
		time.Sleep(2 * time.Second)
		synctest.Wait()
		require.Empty(t, statuses, "old provisional timer reported against current paint")
		close(f.paint)
	})
}

func TestLiveDocumentStopLoadingRequiresFreshPaintAndMedia(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "pending"})
		err := f.lv.dispatchInputContext(context.Background(), "viewer", LiveInput{Kind: "stop_loading"})
		require.NoError(t, err)
		<-f.paintEntered
		require.Equal(t, int32(1), f.stopCalls.Load(), "Stop did not reach Chrome")
		require.False(t, f.cs.FrameState().Ready, "Stop acknowledgement authorized old picture")
		close(f.paint)
		synctest.Wait()
		f.viewportFrameFixture.mu.Lock()
		commands := append([]CaptureFrameState(nil), f.commands...)
		f.viewportFrameFixture.mu.Unlock()
		require.Len(t, commands, 1)
		require.False(t, f.cs.FrameState().Ready)
		require.True(t, f.cs.CommitFrameBoundary(commands[0].Generation, commands[0].TargetID, 91))
		require.True(t, f.cs.FrameState().Ready)
	})
}

func TestLiveDocumentStopCannotResumeOverNewerNavigation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		previous := f.lv.runCDP
		f.lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
			for _, action := range actions {
				if _, ok := action.(*page.StopLoadingParams); ok {
					f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "newer"})
				}
			}
			return previous(ctx, timeout, actions...)
		}
		require.NoError(t, f.lv.dispatchInputContext(context.Background(), "viewer", LiveInput{Kind: "stop_loading"}))
		synctest.Wait()
		f.watch.mu.Lock()
		require.Equal(t, "newer", string(f.watch.work.loaderID))
		f.watch.mu.Unlock()
		f.viewportFrameFixture.mu.Lock()
		require.Empty(t, f.commands)
		f.viewportFrameFixture.mu.Unlock()
		require.False(t, f.cs.FrameState().Ready)
	})
}

func TestLiveDocumentStopReadsFreshDocumentIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "pending"})
		// The actual committed tree differs from the watcher's cached loader.
		f.mu.Lock()
		f.loader = "current-after-stop"
		f.mu.Unlock()
		close(f.paint)
		require.NoError(t, f.lv.dispatchInputContext(context.Background(), "viewer", LiveInput{Kind: "stop_loading"}))
		synctest.Wait()
		f.viewportFrameFixture.mu.Lock()
		require.Len(t, f.commands, 1, "Stop trusted stale cached document identity")
		f.viewportFrameFixture.mu.Unlock()
		require.False(t, f.cs.FrameState().Ready)
	})
}
