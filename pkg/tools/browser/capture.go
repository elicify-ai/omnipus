package browser

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// Wave 2 component L (live-browser-video-streaming-spec.md FR-001, FR-016;
// live-browser-video-streaming-IMPLEMENTATION-PLAN.md row L). CaptureDriver
// is the server-driven screencast capture leg of the WebCodecs relay: it
// drives CDP Page.startScreencast (mechanism (b), Gate 0 §EC-1/EC-2 — no
// page-callable capture API, so an agent-navigated page cannot invoke it) on
// a single agent tab and hands each decoded JPEG frame to a sink callback.
// The gateway/orchestration layer (W2-M, stream.go) wires that callback to
// the encoder page's ingest (FeedFrame): createImageBitmap → VideoFrame →
// WebCodecs VideoEncoder. This file owns ONLY the capture leg — it does not
// know about the encoder page, the ingest WS, or stream orchestration.

const (
	// defaultCaptureActionTimeout bounds each individual CDP round trip
	// (start screencast, ack, stop screencast) issued by CaptureDriver.
	// Mirrors live.go's runCDPWithTimeout rationale: an unbounded
	// chromedp.Run has no deadline of its own and can hang forever under a
	// wedged/overloaded CDP transport.
	defaultCaptureActionTimeout = 5 * time.Second

	// defaultBringupTimeout bounds how long StartCapture waits for the
	// FIRST screencast frame before failing (FR-018: "the system MUST
	// apply capture-, display-, and mid-stream-liveness timeouts; expiry
	// MUST move affected viewers to the unavailable state"). The caller
	// (orchestration) maps a StartCapture error to that unavailable
	// state.
	defaultBringupTimeout = 3 * time.Second
)

// CaptureOptions configures one CDP Page.startScreencast capture session.
type CaptureOptions struct {
	// Format is the screencast image format. Only "jpeg" is supported —
	// the encoder page's createImageBitmap→VideoFrame pipeline consumes
	// JPEG (spec Overview, "encoder page" data-flow). Empty defaults to
	// "jpeg".
	Format string

	// Quality is the JPEG compression quality, 0-100 (~60 per the spec'd
	// default — same tradeoff live.go's screencastQuality documents:
	// legible detail at reasonable bandwidth).
	Quality int

	// MaxWidth / MaxHeight bound the captured frame to the live-view
	// panel size; CDP scales the composited frame down to fit (never
	// up).
	MaxWidth, MaxHeight int

	// EveryNthFrame throttles capture; 1 = every composited frame — the
	// smooth 30fps path Gate 0 proved (spec §Gate 0 / EC-1: "measured 30
	// fps ... incl. software --disable-gpu"). Values <= 0 default to 1.
	EveryNthFrame int

	// ActionTimeout bounds each CDP round trip (start/ack/stop). Zero
	// defaults to defaultCaptureActionTimeout.
	ActionTimeout time.Duration

	// BringupTimeout bounds how long StartCapture waits for the first
	// frame before returning an error (FR-018). Zero defaults to
	// defaultBringupTimeout.
	BringupTimeout time.Duration
}

// runCDPFunc executes a bounded chromedp CDP round trip. Signature matches
// live.go's runCDPWithTimeout / LiveView.runCDP field — the production seam
// (startCDPAction, below) drives real chromedp actions; capture_test.go
// substitutes a fake so TestCapture_* never touches a real Chromium.
type runCDPFunc func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error

// listenTargetFunc registers handler to receive every CDP target event
// delivered on ctx until ctx is done. Signature matches
// chromedp.ListenTarget — the production seam is chromedp.ListenTarget
// itself; capture_test.go substitutes a fake that lets tests synthesize
// page.EventScreencastFrame events without a real chromedp target (real
// chromedp.ListenTarget panics on a ctx that wasn't built via
// chromedp.NewContext, which a hermetic test cannot provide).
type listenTargetFunc func(ctx context.Context, handler func(ev any))

// CaptureDriver owns one CDP Page.startScreencast session on an agent tab.
// Callers get one back only after the first frame has arrived (or an error
// otherwise) — see StartCapture.
type CaptureDriver struct {
	tabCtx        context.Context
	cancel        context.CancelFunc
	runCDP        runCDPFunc
	actionTimeout time.Duration
	done          chan struct{}
	stopOnce      sync.Once
}

// StartCapture issues CDP Page.startScreencast on ctx — the agent tab's own
// chromedp context (a chromedp.NewContext child of the coordinator's shared
// rootCtx; CDP itself rides the pure-Go pipe transport underneath, which is
// transparent to chromedp's public API) — and streams frames to onFrame
// until Stop is called, ctx is canceled, or the target closes. It blocks
// until the first frame arrives or opts.BringupTimeout elapses (FR-018),
// returning an error in either failure case for the orchestrator to map to
// the unavailable state.
func StartCapture(ctx context.Context, opts CaptureOptions, onFrame func(jpeg []byte, seq uint32, tsMillis uint64)) (*CaptureDriver, error) {
	return startCapture(ctx, opts, onFrame, startCDPAction, chromedp.ListenTarget)
}

// startCapture is StartCapture's implementation with the CDP execution seam
// (runCDP, listenTarget) injected explicitly. capture_test.go calls this
// directly with fakes to drive the full start/ack/forward/stop lifecycle
// hermetically.
func startCapture(
	ctx context.Context,
	opts CaptureOptions,
	onFrame func(jpeg []byte, seq uint32, tsMillis uint64),
	runCDP runCDPFunc,
	listenTarget listenTargetFunc,
) (*CaptureDriver, error) {
	if ctx == nil {
		return nil, errors.New("browser capture: ctx is required")
	}
	if onFrame == nil {
		return nil, errors.New("browser capture: onFrame callback is required")
	}
	if runCDP == nil || listenTarget == nil {
		return nil, errors.New("browser capture: CDP execution seam (runCDP, listenTarget) is required")
	}

	format := opts.Format
	if format == "" {
		format = "jpeg"
	}
	if format != "jpeg" {
		return nil, fmt.Errorf("browser capture: unsupported format %q (only jpeg is supported)", format)
	}

	everyNth := opts.EveryNthFrame
	if everyNth <= 0 {
		everyNth = 1
	}

	actionTimeout := opts.ActionTimeout
	if actionTimeout <= 0 {
		actionTimeout = defaultCaptureActionTimeout
	}

	bringupTimeout := opts.BringupTimeout
	if bringupTimeout <= 0 {
		bringupTimeout = defaultBringupTimeout
	}

	captureCtx, cancel := context.WithCancel(ctx)

	d := &CaptureDriver{
		tabCtx:        ctx,
		cancel:        cancel,
		runCDP:        runCDP,
		actionTimeout: actionTimeout,
		done:          make(chan struct{}),
	}

	// watcher is the sole goroutine this driver spawns; it exists purely
	// to close d.done once captureCtx ends (Stop(), a parent ctx
	// cancellation, or the bring-up-timeout path below), so Stop() has
	// something deterministic to wait on and no goroutine is ever leaked
	// — captureCtx is always eventually canceled on every exit path.
	go func() {
		<-captureCtx.Done()
		close(d.done)
	}()

	var seq atomic.Uint32
	// captureStart anchors tsMillis to Go's monotonic clock reading
	// (time.Since below reads the monotonic component of captureStart,
	// immune to wall-clock/NTP adjustments) while still producing an
	// absolute millisecond value compatible with the wire's ts:u64 unit
	// (spec m-3/DS-2) — "source-side monotonic clock" per the task spec.
	captureStart := time.Now()
	captureStartMillis := uint64(captureStart.UnixMilli())

	var firstFrameOnce sync.Once
	firstFrame := make(chan struct{})

	handler := func(ev any) {
		frame, ok := ev.(*page.EventScreencastFrame)
		if !ok {
			return
		}

		// Ack immediately — Chrome will not send the next screencast
		// frame until the previous one is acked (spec: "the ack is
		// required for the next frame"). Unlike live.go's
		// coalescing ack worker (which only needs the newest frame
		// for a human-viewed JPEG preview and can safely drop
		// stale acks), this driver feeds a video encoder that needs
		// every frame in order, so each frame is acked and forwarded
		// synchronously here, bounded by actionTimeout so a
		// wedged/overloaded transport cannot hang this indefinitely.
		if err := runCDP(captureCtx, actionTimeout, page.ScreencastFrameAck(frame.SessionID)); err != nil {
			logger.WarnCF("browser", "capture: frame ack failed", map[string]any{
				"error":      err.Error(),
				"session_id": frame.SessionID,
			})
			return
		}

		jpegBytes, err := base64.StdEncoding.DecodeString(frame.Data)
		if err != nil {
			logger.WarnCF("browser", "capture: frame payload was not valid base64 — dropping frame", map[string]any{
				"error":      err.Error(),
				"session_id": frame.SessionID,
			})
			return
		}

		frameSeq := seq.Add(1) - 1 // first frame is seq 0, then monotonically increasing
		tsMillis := captureStartMillis + uint64(time.Since(captureStart).Milliseconds())

		firstFrameOnce.Do(func() { close(firstFrame) })

		onFrame(jpegBytes, frameSeq, tsMillis)
	}

	// Register the listener before issuing Page.startScreencast so no
	// frame emitted immediately after Chrome accepts the command can be
	// missed.
	listenTarget(captureCtx, handler)

	err := runCDP(captureCtx, actionTimeout,
		page.StartScreencast().
			WithFormat(page.ScreencastFormatJpeg).
			WithQuality(int64(opts.Quality)).
			WithMaxWidth(int64(opts.MaxWidth)).
			WithMaxHeight(int64(opts.MaxHeight)).
			WithEveryNthFrame(int64(everyNth)),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("browser capture: failed to start screencast: %w", err)
	}

	select {
	case <-firstFrame:
		return d, nil
	case <-time.After(bringupTimeout):
		// FR-018: no first frame within the bring-up window. Best-effort
		// stop (the screencast command did succeed above, so Chrome may
		// still be trying to send frames) then tear down; the caller
		// maps this error to the unavailable state.
		if stopErr := runCDP(d.tabCtx, actionTimeout, page.StopScreencast()); stopErr != nil {
			logger.WarnCF("browser", "capture: bring-up timeout — stop screencast also failed", map[string]any{
				"error": stopErr.Error(),
			})
		}
		cancel()
		return nil, fmt.Errorf("browser capture: no frame received within bring-up timeout (%s)", bringupTimeout)
	case <-captureCtx.Done():
		cancel()
		return nil, fmt.Errorf("browser capture: context canceled during bring-up: %w", captureCtx.Err())
	}
}

// Stop tears down the capture session: issues CDP Page.stopScreencast
// (best-effort — the tab/target may already be gone) and deregisters the
// event listener, then blocks until the internal watcher goroutine has
// exited so callers never observe a partially-torn-down driver. Idempotent
// — safe to call more than once or after the target/context has already
// ended on its own.
func (d *CaptureDriver) Stop() {
	d.stopOnce.Do(func() {
		if err := d.runCDP(d.tabCtx, d.actionTimeout, page.StopScreencast()); err != nil {
			logger.WarnCF("browser", "capture: stop screencast failed (target may already be closed)", map[string]any{
				"error": err.Error(),
			})
		}
		d.cancel()
	})
	<-d.done
}

// startCDPAction is the production runCDPFunc: a bounded chromedp.Run call,
// matching live.go's runCDPWithTimeout exactly (kept as a separate function
// here, rather than reusing runCDPWithTimeout directly, so this file's only
// dependency on live.go's internals is the shared package, not a specific
// unexported symbol — see the file-ownership note at the top of this file).
func startCDPAction(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error {
	boundedCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return chromedp.Run(boundedCtx, actions...)
}
