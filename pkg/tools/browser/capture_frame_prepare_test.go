package browser

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

func preparedCaptureFixture(t *testing.T) (*CaptureSession, *BrowserManager, context.Context) {
	t.Helper()
	mgr := newTestManagerWithFakeTabs(t)
	t.Cleanup(mgr.Shutdown)
	active, err := mgr.Session(testSessionID)
	if err != nil {
		t.Fatal(err)
	}
	mgr.mu.Lock()
	mgr.sessions[testSessionID].active().targetID = target.ID("verified-target")
	mgr.mu.Unlock()
	cs, _ := newRecoveryTestSession(t, &fakeRelay{})
	cs.mgr, cs.panelSessionID = mgr, testSessionID
	return cs, mgr, active
}

func TestPrepareEncoderFrameUsesMeasuredActiveTarget(t *testing.T) {
	cs, _, active := preparedCaptureFixture(t)
	calls := 0
	frame, err := cs.prepareEncoderFrame(context.Background(), func(ctx context.Context) (int, int, float64, error) {
		calls++
		if chromedp.FromContext(ctx) != chromedp.FromContext(active) {
			t.Error("measured a different browser target")
		}
		return 641, 479, 1.25, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || frame.Generation != 1 || frame.TargetID != "verified-target" || frame.Width != 641 || frame.Height != 479 || frame.Scale != 1.25 || frame.Ready || frame.CaptureID == "" {
		t.Fatalf("initial capture lacks one verified target/layout: calls=%d frame=%+v", calls, frame)
	}
	if active.Err() != nil {
		t.Fatal("snapshot preparation canceled the persistent target")
	}
}

func TestPrepareEncoderFrameRejectsUnmeasuredGeometry(t *testing.T) {
	for _, dimensions := range [][2]int{{0, 600}, {0, 0}, {800, 0}, {-1, 600}, {800, 16385}} {
		cs, _, _ := preparedCaptureFixture(t)
		_, err := cs.prepareEncoderFrame(context.Background(), func(context.Context) (int, int, float64, error) { return dimensions[0], dimensions[1], 1, nil })
		if err == nil || cs.FrameState().Generation != 0 {
			t.Fatalf("unmeasured geometry published: %v err=%v", dimensions, err)
		}
	}
}

func TestPrepareEncoderFrameStopCancelsMeasurement(t *testing.T) {
	cs, _, active := preparedCaptureFixture(t)
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := cs.prepareEncoderFrame(caller, func(ctx context.Context) (int, int, float64, error) {
			close(entered)
			<-ctx.Done()
			return 0, 0, 0, ctx.Err()
		})
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("preparation never reached measurement")
	}
	cs.Stop()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("capture stop did not cancel measurement: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		cancel()
		<-result
		t.Fatal("capture stop left startup measurement running")
	}
	if active.Err() != nil || cs.FrameState().Generation != 0 {
		t.Fatal("stopped preparation changed shared target or picture identity")
	}
}

func TestPrepareEncoderFrameRetainsMeasurementFailure(t *testing.T) {
	cs, _, _ := preparedCaptureFixture(t)
	failure := errors.New("layout measurement unavailable")
	_, err := cs.prepareEncoderFrame(context.Background(), func(context.Context) (int, int, float64, error) { return 0, 0, 0, failure })
	if !errors.Is(err, failure) || cs.FrameState().Generation != 0 {
		t.Fatalf("measurement failure swallowed: frame=%+v err=%v", cs.FrameState(), err)
	}
}

func TestPrepareEncoderFrameRejectsTargetReplacementDuringMeasurement(t *testing.T) {
	cs, mgr, active := preparedCaptureFixture(t)
	_, err := cs.prepareEncoderFrame(context.Background(), func(context.Context) (int, int, float64, error) {
		// Lifecycle removal may race measurement even though normal tab commands
		// use admission. A snapshot cannot be committed into a replacement set.
		mgr.mu.Lock()
		old := mgr.sessions[testSessionID]
		mgr.sessions[testSessionID] = &sessionEntry{tabs: []*tabEntry{{ctx: active, cancel: old.active().cancel, targetID: "replacement-target"}}, browserCtx: old.browserCtx, browserCancel: old.browserCancel}
		mgr.mu.Unlock()
		return 800, 600, 1, nil
	})
	if err == nil || cs.FrameState().Generation != 0 {
		t.Fatalf("retired target authorized: frame=%+v err=%v", cs.FrameState(), err)
	}
}

func TestPrepareEncoderFrameWaitUsesCallerBudget(t *testing.T) {
	cs, mgr, _ := preparedCaptureFixture(t)
	release, err := mgr.acquireLiveTabCommand(context.Background(), testSessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	calls := 0
	started := time.Now()
	_, err = cs.prepareEncoderFrame(ctx, func(context.Context) (int, int, float64, error) { calls++; return 800, 600, 1, nil })
	if !errors.Is(err, context.DeadlineExceeded) || calls != 0 || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("startup bypassed a pending tab command: calls=%d err=%v", calls, err)
	}
}

func TestPrepareEncoderFrameCancellationStopsMeasurement(t *testing.T) {
	cs, _, active := preparedCaptureFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := cs.prepareEncoderFrame(ctx, func(measureCtx context.Context) (int, int, float64, error) {
			close(entered)
			<-measureCtx.Done()
			return 0, 0, 0, measureCtx.Err()
		})
		result <- err
	}()
	select {
	case <-entered:
	case err := <-result:
		t.Fatalf("startup skipped measurement: %v", err)
	case <-time.After(time.Second):
		t.Fatal("startup never reached measurement")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not stop measurement")
	}
	if active.Err() != nil || cs.FrameState().Generation != 0 {
		t.Fatal("canceled preparation mutated persistent target or capture identity")
	}
}
