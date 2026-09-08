package browser

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

type documentEventFixture struct {
	*viewportFrameFixture
	watch        *liveDocumentWatch
	mu           sync.Mutex
	loader       cdp.LoaderID
	paint        chan struct{}
	paintEntered chan struct{}
	paintOnce    sync.Once
}

func newDocumentEventFixture(t *testing.T) *documentEventFixture {
	t.Helper()
	f := &documentEventFixture{viewportFrameFixture: newViewportFrameFixture(t), loader: "original", paint: make(chan struct{}), paintEntered: make(chan struct{})}
	ctx, cancel := context.WithCancel(f.lv.tabCtx)
	t.Cleanup(cancel)
	f.watch = &liveDocumentWatch{lv: f.lv, ctx: ctx, target: f.lv.tabCtx, frameID: "main", loaderID: "original"}
	f.lv.documentWatch = f.watch
	previous := f.lv.runCDP
	executor := liveInputExecutor(func(ctx context.Context, method string, params, result any) error {
		switch method {
		case "Page.getFrameTree":
			f.mu.Lock()
			loader := f.loader
			f.mu.Unlock()
			result.(*page.GetFrameTreeReturns).FrameTree = &page.FrameTree{Frame: &cdp.Frame{ID: "main", LoaderID: loader}}
		case "Page.createIsolatedWorld":
			result.(*page.CreateIsolatedWorldReturns).ExecutionContextID = 71
		case "Runtime.evaluate":
			f.paintOnce.Do(func() { close(f.paintEntered) })
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-f.paint:
			}
		case "Page.getNavigationHistory":
			result.(*page.GetNavigationHistoryReturns).CurrentIndex = 0
		default:
			return fmt.Errorf("unexpected document protocol command %s", method)
		}
		return nil
	})
	f.lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
		for _, action := range actions {
			switch action.(type) {
			case documentPaintAction, chromedp.ActionFunc, historyBackInputAction:
				if err := action.Do(cdp.WithExecutor(ctx, executor)); err != nil {
					return err
				}
			default:
				if err := previous(ctx, timeout, action); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return f
}

func (f *documentEventFixture) commit(loader cdp.LoaderID) {
	f.mu.Lock()
	f.loader = loader
	f.mu.Unlock()
	f.watch.onEvent(&page.EventFrameNavigated{Frame: &cdp.Frame{ID: "main", LoaderID: loader}})
}

func TestLiveDocumentEventsRetireOldWorkAndAwaitPaint(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "subframe", LoaderID: "ignored"})
		require.Equal(t, f.before, f.cs.FrameState(), "subframe retired main picture")
		f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "first"})
		require.False(t, f.cs.AcceptsInputGeneration(f.before.CaptureID, f.before.Generation))
		f.commit("first")
		<-f.paintEntered
		require.Zero(t, f.cs.FrameState().Width, "navigation acknowledgement authorized unpainted document")
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		release, err := f.mgr.acquireLiveTabCommand(ctx, testSessionID)
		require.NoError(t, err, "paint wait retained command admission")
		release()
		cancel()
		f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "second"})
		second := f.cs.FrameState()
		f.watch.onEvent(&page.EventFrameNavigated{Frame: &cdp.Frame{ID: "main", LoaderID: "first"}})
		synctest.Wait()
		require.Equal(t, second, f.cs.FrameState(), "late first commit altered replacement")
		f.commit("second")
		close(f.paint)
		synctest.Wait()
		f.viewportFrameFixture.mu.Lock()
		commands := append([]CaptureFrameState(nil), f.commands...)
		f.viewportFrameFixture.mu.Unlock()
		require.Len(t, commands, 1, "retired paint sent recapture or new paint never completed")
		require.Equal(t, 1000, commands[0].Width)
		require.Equal(t, 700, commands[0].Height)
		require.Equal(t, 1.25, commands[0].Scale)
		require.Greater(t, commands[0].Generation, second.Generation)
		require.False(t, f.cs.FrameState().Ready, "paint itself claimed viewer readiness")
		require.True(t, f.cs.CommitFrameBoundary(commands[0].Generation, commands[0].TargetID, 91))
		require.True(t, f.cs.FrameState().Ready)
	})
}

func TestLiveDocumentNoHistoryRecoversUnchangedPageAfterPaint(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		require.NoError(t, f.lv.dispatchInputContext(context.Background(), "viewer", LiveInput{Kind: "navigate_back"}))
		<-f.paintEntered
		require.False(t, f.cs.FrameState().Ready)
		close(f.paint)
		synctest.Wait()
		f.viewportFrameFixture.mu.Lock()
		defer f.viewportFrameFixture.mu.Unlock()
		require.Len(t, f.commands, 1, "Back with no history left unchanged page permanently pending")
		require.Greater(t, f.commands[0].Generation, f.before.Generation)
	})
}

func TestLiveDocumentOnlyCurrentFailedRequestRecoversOldPage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		for _, loader := range []cdp.LoaderID{"first", "second"} {
			f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: loader})
			f.watch.onEvent(&network.EventRequestWillBeSent{FrameID: "main", LoaderID: loader, RequestID: network.RequestID(loader), Type: network.ResourceTypeDocument})
		}
		f.watch.onEvent(&network.EventLoadingFailed{RequestID: "first", Canceled: true})
		synctest.Wait()
		select {
		case <-f.paintEntered:
			t.Fatal("old network failure resumed a newer provisional document")
		default:
		}
		f.watch.onEvent(&network.EventLoadingFailed{RequestID: "second", Canceled: true})
		<-f.paintEntered
		close(f.paint)
		synctest.Wait()
		f.viewportFrameFixture.mu.Lock()
		defer f.viewportFrameFixture.mu.Unlock()
		require.Len(t, f.commands, 1, "current canceled navigation did not recover unchanged page")
	})
}

func TestLiveDocumentPendingResizeWaitsForMeasuredCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "next"})
		applied, err := f.reg.SetViewportContext(context.Background(), testSessionID, 1000, 700, 1.25)
		require.NoError(t, err)
		require.True(t, applied)
		require.Zero(t, f.cs.FrameState().Width)
		f.viewportFrameFixture.mu.Lock()
		require.Empty(t, f.commands, "resize bypassed document paint")
		f.viewportFrameFixture.mu.Unlock()
		f.commit("next")
		close(f.paint)
		synctest.Wait()
		f.viewportFrameFixture.mu.Lock()
		defer f.viewportFrameFixture.mu.Unlock()
		require.Len(t, f.commands, 1)
		require.Equal(t, 1000, f.commands[0].Width)
		require.Equal(t, 700, f.commands[0].Height)
	})
}

func TestLiveDocumentQueuedEventBlocksInputBeforeWorkerRuns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		f.watch.events = make(chan liveDocumentEvent, 2)
		f.watch.enqueue(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "next"})
		err := f.lv.dispatchInputContext(context.Background(), "viewer", inputWithTestPicture(f.before, LiveInput{Kind: "text", Text: "old picture"}))
		require.Error(t, err)
		require.True(t, IsBenignLiveInputError(err))
		go f.watch.processEvents()
		synctest.Wait()
		require.False(t, f.watch.inputPending(), "processed event remained queued")
		require.False(t, f.cs.FrameState().Ready, "processed navigation failed to retire old picture")
	})
}

func TestLiveDocumentListenerNeverWaitsForManagerAndFailsClosedOnOverflow(t *testing.T) {
	f := newDocumentEventFixture(t)
	f.watch.events = make(chan liveDocumentEvent, 1)
	ctx, cancel := context.WithCancel(f.watch.ctx)
	defer cancel()
	f.watch.ctx, f.watch.cancel = ctx, cancel
	f.mgr.mu.Lock()
	done := make(chan struct{})
	go func() {
		f.watch.enqueue(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "first"})
		f.watch.enqueue(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "second"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		f.mgr.mu.Unlock()
		t.Fatal("protocol reader waited for a browser command's manager lock")
	}
	f.mgr.mu.Unlock()
	require.True(t, f.watch.failed.Load())
	require.ErrorIs(t, f.watch.ctx.Err(), context.Canceled)
	require.True(t, f.watch.inputPending(), "overflow silently accepted incomplete document history")
}

func TestLiveDocumentInitializationRetainsItsOriginalWork(t *testing.T) {
	for _, scenario := range []string{"new input", "ignored subframe", "no history", "failure after replacement"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newDocumentEventFixture(t)
				f.watch.frameID, f.watch.loaderID = "", ""
				f.watch.events = make(chan liveDocumentEvent, 16)
				entered, finishRead, initialized := make(chan struct{}), make(chan struct{}), make(chan struct{})
				statuses := make(chan string, 4)
				f.lv.statusSinks = map[string]StatusSink{"viewer": func(message string) { statuses <- message }}
				var first atomic.Bool
				previous := f.lv.runCDP
				f.lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
					if len(actions) == 1 {
						if action, ok := actions[0].(chromedp.ActionFunc); ok && first.CompareAndSwap(false, true) {
							close(entered)
							<-finishRead
							if scenario == "failure after replacement" {
								return errors.New("original initialization failed")
							}
							return action.Do(cdp.WithExecutor(ctx, liveInputExecutor(func(_ context.Context, method string, params, result any) error {
								if method != "Page.getFrameTree" {
									return fmt.Errorf("unexpected initial command %s", method)
								}
								result.(*page.GetFrameTreeReturns).FrameTree = &page.FrameTree{Frame: &cdp.Frame{ID: "main", LoaderID: "original"}}
								return nil
							})))
						}
					}
					return previous(ctx, timeout, actions...)
				}
				go func() { f.watch.initialize(); close(initialized); f.watch.processEvents() }()
				<-entered
				var newer CaptureFrameState
				switch scenario {
				case "new input":
					_, _, err := f.lv.beginInputDocument(f.lv.tabCtx)
					require.NoError(t, err)
					newer = f.cs.FrameState()
				case "ignored subframe":
					f.watch.enqueue(&page.EventFrameNavigated{Frame: &cdp.Frame{ID: "child", ParentID: "main", LoaderID: "child-doc"}})
				case "no history":
					require.NoError(t, f.lv.dispatchInputContext(context.Background(), "viewer", LiveInput{Kind: "navigate_back"}))
				case "failure after replacement":
					replacement, _ := adapterFixture(t)
					f.mgr.captureMu.Lock()
					f.mgr.captures[testSessionID] = replacement
					f.mgr.captureMu.Unlock()
				}
				close(finishRead)
				<-initialized
				close(f.paint)
				synctest.Wait()
				f.viewportFrameFixture.mu.Lock()
				defer f.viewportFrameFixture.mu.Unlock()
				switch scenario {
				case "new input":
					require.Equal(t, newer, f.cs.FrameState(), "old discovery snapshot completed newer UI navigation")
					require.Empty(t, f.commands)
				case "ignored subframe", "no history":
					require.Len(t, f.commands, 1, "initial discovery left a safe unchanged page pending")
				case "failure after replacement":
					require.Empty(t, statuses, "old initialization error was reported against replacement capture")
				}
			})
		})
	}
}

func TestLiveDocumentTransitionCancelsAdmittedOldPictureInput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		entered := make(chan struct{})
		previous := f.lv.runCDP
		f.lv.runCDP = func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
			if len(actions) == 1 {
				if _, ok := actions[0].(*input.InsertTextParams); ok {
					close(entered)
					<-ctx.Done()
					return ctx.Err()
				}
			}
			return previous(ctx, timeout, actions...)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result := make(chan error, 1)
		go func() {
			result <- f.lv.dispatchInputContext(ctx, "viewer", inputWithTestPicture(f.before, LiveInput{Kind: "text", Text: "old picture"}))
		}()
		<-entered
		f.watch.onEvent(&page.EventFrameStartedNavigating{FrameID: "main", LoaderID: "next"})
		synctest.Wait()
		select {
		case err := <-result:
			require.ErrorIs(t, err, context.Canceled)
			require.True(t, IsBenignLiveInputError(err), "ordinary page replacement must not force a connection error")
		default:
			cancel()
			<-result
			t.Error("retired picture left admitted input alive until its unrelated request timeout")
		}
	})
}

// Err may observe cancellation just before it returns. This controlled context
// pauses after that observation, exposing a stale pre-publication observation.
type documentSampledContext struct {
	context.Context
	once            sync.Once
	sampled, resume chan struct{}
}

func (c *documentSampledContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.sampled); <-c.resume })
	return err
}

func TestLiveDocumentBeginRechecksWatchAtPublication(t *testing.T) {
	for _, retired := range []bool{false, true} {
		t.Run(fmt.Sprint(retired), func(t *testing.T) {
			f := newDocumentEventFixture(t)
			ctx, cancel := context.WithCancel(f.watch.ctx)
			defer cancel()
			sampled := &documentSampledContext{Context: ctx, sampled: make(chan struct{}), resume: make(chan struct{})}
			f.watch.ctx = sampled
			result := make(chan error, 1)
			go func() {
				f.watch.mu.Lock()
				_, err := f.watch.beginLocked("next")
				f.watch.mu.Unlock()
				result <- err
			}()
			<-sampled.sampled
			if retired {
				cancel()
			}
			close(sampled.resume)
			err := <-result
			if retired {
				require.ErrorIs(t, err, ErrStaleCaptureFrame)
				require.Equal(t, f.before, f.cs.FrameState(), "retired observer changed the current picture")
			} else {
				require.NoError(t, err)
				require.Greater(t, f.cs.FrameState().Generation, f.before.Generation)
				require.False(t, f.cs.FrameState().Ready)
			}
		})
	}
}

func TestLiveDocumentLateInitializationCannotReplaceEarlierNavigation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newDocumentEventFixture(t)
		f.watch.frameID, f.watch.loaderID = "", ""
		f.watch.events = make(chan liveDocumentEvent, 16)
		_, work, err := f.lv.beginInputDocument(f.lv.tabCtx)
		require.NoError(t, err)
		require.NotNil(t, work)
		pending := f.cs.FrameState()
		f.watch.initialize()
		go f.watch.processEvents()
		close(f.paint)
		synctest.Wait()
		require.Equal(t, pending, f.cs.FrameState(), "late initialization replaced an already admitted navigation")
		require.True(t, f.cs.documentTransitionCurrent(work.token))
		f.viewportFrameFixture.mu.Lock()
		defer f.viewportFrameFixture.mu.Unlock()
		require.Empty(t, f.commands, "discovery of the old page issued a recapture for pending navigation")
	})
}
