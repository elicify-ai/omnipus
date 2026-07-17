package browser

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// fakeCDP is the hermetic CDP execution seam capture_test.go injects in
// place of a real Chromium: startCapture's runCDP calls land on fake.run
// (recording every action instead of talking to a browser) and its
// listenTarget call lands on fake.listen (capturing the handler so tests can
// synthesize page.EventScreencastFrame events via fake.emit). fake.emit
// mirrors chromedp's own event-dispatch filtering (chromedp@v0.15.1
// util.go's runListeners: a listener whose ctx is Done is dropped before its
// fn is invoked) so TestCapture_StopStopsScreencast exercises the same
// cancellation contract the real chromedp.ListenTarget provides in
// production.
type fakeCDP struct {
	mu sync.Mutex

	startCalls      int
	lastStartParams *page.StartScreencastParams
	ackCalls        []int64 // one entry per ScreencastFrameAck(sessionID) call, in order
	stopCalls       int
	ackErr          error // if set, returned by every ScreencastFrameAck call

	listenCtx context.Context
	handler   func(ev any)

	listenerOnce sync.Once
	listenerCh   chan struct{}
}

func newFakeCDP() *fakeCDP {
	return &fakeCDP{listenerCh: make(chan struct{})}
}

func (f *fakeCDP) run(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range actions {
		switch p := a.(type) {
		case *page.StartScreencastParams:
			f.startCalls++
			f.lastStartParams = p
		case *page.ScreencastFrameAckParams:
			f.ackCalls = append(f.ackCalls, p.SessionID)
			if f.ackErr != nil {
				return f.ackErr
			}
		case *page.StopScreencastParams:
			f.stopCalls++
		}
	}
	return nil
}

func (f *fakeCDP) listen(ctx context.Context, handler func(ev any)) {
	f.mu.Lock()
	f.listenCtx = ctx
	f.handler = handler
	f.mu.Unlock()
	f.listenerOnce.Do(func() { close(f.listenerCh) })
}

// emit synthesizes a Page.screencastFrame event as if Chrome had sent it,
// dropping it (never calling the handler) once the ctx passed to listen has
// been canceled — exactly chromedp's own runListeners contract.
func (f *fakeCDP) emit(ev *page.EventScreencastFrame) {
	f.mu.Lock()
	ctx := f.listenCtx
	h := f.handler
	f.mu.Unlock()
	if h == nil || ctx == nil {
		return
	}
	select {
	case <-ctx.Done():
		return
	default:
		h(ev)
	}
}

func (f *fakeCDP) waitForListener(t *testing.T) {
	t.Helper()
	select {
	case <-f.listenerCh:
	case <-time.After(2 * time.Second):
		t.Fatal("listener was never registered (startCapture did not call listenTarget)")
	}
}

type captureResult struct {
	driver *CaptureDriver
	err    error
}

func startCaptureAsync(
	ctx context.Context,
	opts CaptureOptions,
	onFrame func(jpeg []byte, seq uint32, tsMillis uint64),
	fake *fakeCDP,
) <-chan captureResult {
	resultCh := make(chan captureResult, 1)
	go func() {
		d, err := startCapture(ctx, opts, onFrame, fake.run, fake.listen)
		resultCh <- captureResult{driver: d, err: err}
	}()
	return resultCh
}

// waitForFrameNotify blocks until notify has fired once (i.e. ackWorker has
// finished processing one frame — see the doc comment on
// TestCapture_StartAckForward's frameNotify channel), failing the test if it
// doesn't arrive within 2s. Exercising the worker path deliberately: since
// FIX 1 moved ack+decode+onFrame off the chromedp dispatch goroutine and onto
// ackWorker (capture.go), fake.emit() returning no longer means onFrame has
// run for that frame — only that the frame was handed off to frameCh. Tests
// that need to observe onFrame's effects must synchronize on it explicitly
// instead of relying on the old (pre-fix) synchronous-inline-ack behavior.
func waitForFrameNotify(t *testing.T, notify <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-notify:
	case <-time.After(2 * time.Second):
		t.Fatalf(
			"onFrame was not invoked for %s within the timeout — ackWorker may be stuck or not draining frameCh",
			what,
		)
	}
}

func TestCapture_StartAckForward(t *testing.T) {
	fake := newFakeCDP()

	type recordedFrame struct {
		jpeg     []byte
		seq      uint32
		tsMillis uint64
	}
	var mu sync.Mutex
	var frames []recordedFrame
	// frameNotify fires once per onFrame invocation, AFTER the frame has
	// been appended to `frames` — the only reliable way for this test to
	// know ackWorker (running on its own goroutine, off the fake's
	// synchronous emit() call) has actually finished processing a given
	// frame, now that the ack/decode/onFrame pipeline is no longer
	// inline in the ListenTarget handler (FIX 1).
	frameNotify := make(chan struct{}, 4)
	onFrame := func(jpeg []byte, seq uint32, tsMillis uint64) {
		mu.Lock()
		frames = append(frames, recordedFrame{jpeg: append([]byte(nil), jpeg...), seq: seq, tsMillis: tsMillis})
		mu.Unlock()
		frameNotify <- struct{}{}
	}

	opts := CaptureOptions{
		Format:         "jpeg",
		Quality:        60,
		MaxWidth:       1280,
		MaxHeight:      720,
		EveryNthFrame:  1,
		BringupTimeout: 2 * time.Second,
	}

	resultCh := startCaptureAsync(context.Background(), opts, onFrame, fake)
	fake.waitForListener(t)

	// startScreencast must be issued (with the configured params) before
	// any frame can arrive.
	fake.mu.Lock()
	if fake.startCalls != 1 {
		fake.mu.Unlock()
		t.Fatalf("expected startScreencast issued once before first frame, got %d", fake.startCalls)
	}
	got := fake.lastStartParams
	fake.mu.Unlock()
	if got == nil {
		t.Fatal("startScreencast params were not recorded")
	}
	if got.Format != page.ScreencastFormatJpeg {
		t.Errorf("format = %q, want jpeg", got.Format)
	}
	if got.Quality != 60 || got.MaxWidth != 1280 || got.MaxHeight != 720 || got.EveryNthFrame != 1 {
		t.Errorf("startScreencast params = %+v, want Quality=60 MaxWidth=1280 MaxHeight=720 EveryNthFrame=1", got)
	}

	payload1 := []byte("first-jpeg-bytes")
	fake.emit(&page.EventScreencastFrame{
		Data:      base64.StdEncoding.EncodeToString(payload1),
		SessionID: 101,
	})

	var res captureResult
	select {
	case res = <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("StartCapture did not return after the first frame arrived")
	}
	if res.err != nil {
		t.Fatalf("StartCapture returned an error: %v", res.err)
	}
	driver := res.driver
	if driver == nil {
		t.Fatal("StartCapture returned a nil driver with a nil error")
	}
	defer driver.Stop()

	// StartCapture unblocks the instant ackWorker closes firstFrame, which
	// happens BEFORE ackWorker's own onFrame(frame1) call in the same
	// worker iteration (see capture.go's ackWorker) — so resultCh firing
	// does not itself guarantee onFrame(frame1) has run yet. Wait for it
	// explicitly before trusting `frames`.
	waitForFrameNotify(t, frameNotify, "the first frame")

	payload2 := []byte("second-jpeg-bytes")
	fake.emit(&page.EventScreencastFrame{
		Data:      base64.StdEncoding.EncodeToString(payload2),
		SessionID: 102,
	})
	waitForFrameNotify(t, frameNotify, "the second frame")

	mu.Lock()
	defer mu.Unlock()
	if len(frames) != 2 {
		t.Fatalf("expected 2 forwarded frames, got %d: %+v", len(frames), frames)
	}
	if string(frames[0].jpeg) != string(payload1) {
		t.Errorf("frame 0 payload = %q, want %q (base64 decode of the synthetic event)", frames[0].jpeg, payload1)
	}
	if string(frames[1].jpeg) != string(payload2) {
		t.Errorf("frame 1 payload = %q, want %q", frames[1].jpeg, payload2)
	}
	if frames[0].seq != 0 || frames[1].seq != 1 {
		t.Errorf("expected monotonically increasing seq 0,1; got %d,%d", frames[0].seq, frames[1].seq)
	}
	if frames[1].tsMillis < frames[0].tsMillis {
		t.Errorf("expected non-decreasing tsMillis; got %d then %d", frames[0].tsMillis, frames[1].tsMillis)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.ackCalls) != 2 || fake.ackCalls[0] != 101 || fake.ackCalls[1] != 102 {
		t.Errorf("expected exactly one ack per frame in order [101 102], got %v", fake.ackCalls)
	}
}

func TestCapture_StopStopsScreencast(t *testing.T) {
	fake := newFakeCDP()

	var frameCount atomic.Int32
	// frameNotify fires after each onFrame call — needed to deterministically
	// know ackWorker has finished processing frame 1 BEFORE calling Stop(),
	// now that ack/decode/onFrame run on ackWorker's own goroutine rather
	// than inline in the ListenTarget handler (FIX 1) — see
	// waitForFrameNotify's doc comment.
	frameNotify := make(chan struct{}, 4)
	onFrame := func(_ []byte, _ uint32, _ uint64) {
		frameCount.Add(1)
		frameNotify <- struct{}{}
	}

	opts := CaptureOptions{BringupTimeout: 2 * time.Second}
	resultCh := startCaptureAsync(context.Background(), opts, onFrame, fake)
	fake.waitForListener(t)

	fake.emit(&page.EventScreencastFrame{
		Data:      base64.StdEncoding.EncodeToString([]byte("f1")),
		SessionID: 1,
	})

	var res captureResult
	select {
	case res = <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("StartCapture did not return after the first frame arrived")
	}
	if res.err != nil {
		t.Fatalf("StartCapture returned an error: %v", res.err)
	}
	driver := res.driver

	// See frameNotify's doc comment: resultCh firing only guarantees
	// ackWorker closed firstFrame, not that it has also finished calling
	// onFrame(f1) yet (that happens right after, same worker iteration).
	waitForFrameNotify(t, frameNotify, "the first frame")

	driver.Stop()

	fake.mu.Lock()
	stopCalls := fake.stopCalls
	fake.mu.Unlock()
	if stopCalls != 1 {
		t.Fatalf("expected exactly 1 stopScreencast call after Stop(), got %d", stopCalls)
	}

	// A frame "arriving" after Stop() must never reach onFrame — mirrors
	// chromedp's own cancelableListener contract (a canceled listener's fn
	// is never invoked again).
	fake.emit(&page.EventScreencastFrame{
		Data:      base64.StdEncoding.EncodeToString([]byte("f2")),
		SessionID: 2,
	})
	if got := frameCount.Load(); got != 1 {
		t.Fatalf("expected exactly 1 forwarded frame (post-Stop emit must be dropped), got %d", got)
	}

	// Stop must be idempotent and must not hang.
	stopped := make(chan struct{})
	go func() {
		driver.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("second Stop() call did not return — Stop() must be idempotent")
	}
	fake.mu.Lock()
	stopCalls = fake.stopCalls
	fake.mu.Unlock()
	if stopCalls != 1 {
		t.Fatalf("Stop() must not re-issue stopScreencast on a second call, got %d total calls", stopCalls)
	}
}

func TestCapture_BringupTimeout(t *testing.T) {
	fake := newFakeCDP()
	onFrame := func(_ []byte, _ uint32, _ uint64) {
		t.Error("onFrame must never be called when no frame arrives before the bring-up timeout")
	}

	opts := CaptureOptions{BringupTimeout: 50 * time.Millisecond}

	start := time.Now()
	d, err := startCapture(context.Background(), opts, onFrame, fake.run, fake.listen)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error when no frame arrives within the bring-up timeout")
	}
	if d != nil {
		t.Fatal("expected a nil *CaptureDriver on bring-up timeout")
	}
	if elapsed < opts.BringupTimeout {
		t.Fatalf("StartCapture returned before the bring-up timeout elapsed (%s < %s)", elapsed, opts.BringupTimeout)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.startCalls != 1 {
		t.Errorf("expected startScreencast to have been issued once, got %d", fake.startCalls)
	}
	if fake.stopCalls != 1 {
		t.Errorf("expected a best-effort stopScreencast on bring-up timeout, got %d calls", fake.stopCalls)
	}
}

func TestCapture_ValidationErrors(t *testing.T) {
	fake := newFakeCDP()
	noopOnFrame := func(_ []byte, _ uint32, _ uint64) {}

	t.Run("nil ctx", func(t *testing.T) {
		//nolint:staticcheck // deliberately exercising the nil-ctx guard
		d, err := startCapture(nil, CaptureOptions{}, noopOnFrame, fake.run, fake.listen)
		if err == nil || d != nil {
			t.Fatalf("expected (nil, error) for a nil ctx, got (%v, %v)", d, err)
		}
	})

	t.Run("nil onFrame", func(t *testing.T) {
		d, err := startCapture(context.Background(), CaptureOptions{}, nil, fake.run, fake.listen)
		if err == nil || d != nil {
			t.Fatalf("expected (nil, error) for a nil onFrame callback, got (%v, %v)", d, err)
		}
	})

	t.Run("unsupported format", func(t *testing.T) {
		d, err := startCapture(context.Background(), CaptureOptions{Format: "png"}, noopOnFrame, fake.run, fake.listen)
		if err == nil || d != nil {
			t.Fatalf("expected (nil, error) for an unsupported format, got (%v, %v)", d, err)
		}
	})

	t.Run("missing CDP seam", func(t *testing.T) {
		d, err := startCapture(context.Background(), CaptureOptions{}, noopOnFrame, nil, nil)
		if err == nil || d != nil {
			t.Fatalf("expected (nil, error) for a missing CDP execution seam, got (%v, %v)", d, err)
		}
	})
}

// errAckFailed exercises the ack-failure path (StartCapture must not treat a
// single failed ack as fatal to the whole capture — see the doc comment on
// startCapture's handler).
var errAckFailed = errors.New("simulated ack failure")

func TestCapture_AckFailureDoesNotForwardButDoesNotAbortCapture(t *testing.T) {
	fake := newFakeCDP()
	fake.ackErr = errAckFailed

	var frameCount atomic.Int32
	onFrame := func(_ []byte, _ uint32, _ uint64) {
		frameCount.Add(1)
	}

	opts := CaptureOptions{BringupTimeout: 200 * time.Millisecond}
	resultCh := startCaptureAsync(context.Background(), opts, onFrame, fake)
	fake.waitForListener(t)

	fake.emit(&page.EventScreencastFrame{
		Data:      base64.StdEncoding.EncodeToString([]byte("f1")),
		SessionID: 1,
	})

	res := <-resultCh
	if res.err == nil {
		t.Fatal(
			"expected the bring-up timeout error — the only frame's ack failed, so no frame should have been forwarded",
		)
	}
	if res.driver != nil {
		t.Fatal("expected a nil driver when the only frame's ack failed before bring-up timed out")
	}
	if frameCount.Load() != 0 {
		t.Fatalf("onFrame must not be invoked for a frame whose ack failed, got %d calls", frameCount.Load())
	}
}
