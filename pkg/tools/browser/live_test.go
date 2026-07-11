package browser

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// These tests deliberately avoid a real Chromium/CDP connection (Chrome-in-CI
// is not approved for this repo — see execute_e2e_test.go's skipIfNoBrowser
// convention). They exercise the pure engine logic — frame relay, ack
// dispatch, control-lock gating, rate limiting, and viewer refcounting — by
// driving LiveView/LiveViewRegistry directly (same package, unexported
// fields/methods) rather than through Attach's chromedp.Run(StartScreencast)
// path. The CDP wire calls themselves (StartScreencast/ScreencastFrameAck/
// StopScreencast, Input.dispatch*) are spike-proven (ADR-038 context) and
// covered by the existing OMNIPUS_BROWSER_E2E-gated suite for the 7 tool
// Execute paths; nothing here needs a live browser to be a real,
// non-trivial test of this file's logic.

// --- frame relay + seq assignment + metadata fallback ---

func TestLiveView_DeliverFansOutAndAssignsSeq(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink)}

	var mu sync.Mutex
	var got1, got2 []LiveFrame
	lv.viewers["v1"] = func(f LiveFrame) { mu.Lock(); got1 = append(got1, f); mu.Unlock() }
	lv.viewers["v2"] = func(f LiveFrame) { mu.Lock(); got2 = append(got2, f); mu.Unlock() }

	lv.deliver(&page.EventScreencastFrame{
		Data: "AAAA",
		Metadata: &page.ScreencastFrameMetadata{
			DeviceWidth: 800, DeviceHeight: 600, PageScaleFactor: 1.5,
			OffsetTop: 10, ScrollOffsetX: 5, ScrollOffsetY: 7,
		},
	})
	lv.deliver(&page.EventScreencastFrame{Data: "BBBB"}) // no metadata -> defaults

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, got1, 2, "both viewers must receive every frame")
	require.Len(t, got2, 2)

	require.Equal(t, 1, got1[0].Seq, "seq must be monotonic starting at 1")
	require.Equal(t, 2, got1[1].Seq)
	require.Equal(t, "AAAA", got1[0].Data)

	require.Equal(t, 800, got1[0].Width)
	require.Equal(t, 600, got1[0].Height)
	require.InDelta(t, 1.5, got1[0].PageScale, 0.0001)
	require.InDelta(t, 10.0, got1[0].OffsetTop, 0.0001)
	require.InDelta(t, 5.0, got1[0].ScrollOffsetX, 0.0001)
	require.InDelta(t, 7.0, got1[0].ScrollOffsetY, 0.0001)

	// A frame with no metadata still needs a schema-valid (>=1) width/height
	// (generated.BrowserScreencastFrame requires it — Constraint #8), so
	// deliver falls back to the configured screencast max dimensions.
	require.Equal(t, screencastMaxWidth, got1[1].Width)
	require.Equal(t, screencastMaxHeight, got1[1].Height)
}

func TestLiveView_DeliverWithNoViewers(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink)}
	// Must not panic when nobody is attached (defensive: deliver can race a
	// detach that empties the viewers map between the CDP callback firing
	// and the lock being taken).
	require.NotPanics(t, func() {
		lv.deliver(&page.EventScreencastFrame{Data: "X"})
	})
}

// --- ack dispatched off the event-dispatch goroutine ---

func TestLiveView_HandleScreencastEvent_DeliversAndDoesNotBlockOnAck(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink), ackCh: make(chan int64, 1)}
	received := make(chan LiveFrame, 1)
	lv.viewers["v1"] = func(f LiveFrame) { received <- f }

	// handleScreencastEvent must never block: it runs synchronously on
	// chromedp's own CDP event-dispatch goroutine (see its doc comment), so
	// the ack is only ever queued (queueAck, non-blocking, coalescing) for a
	// separate ack worker to consume — never dispatched inline and never via
	// a fresh goroutine per frame (ADR-038 deadlock postmortem: the latter
	// piled up under a heavy page and helped saturate the shared CDP
	// transport). Nobody is draining lv.ackCh in this test, which is exactly
	// the point: it must still return immediately, and the frame must still
	// reach the viewer synchronously.
	start := time.Now()
	lv.handleScreencastEvent(context.Background(), &page.EventScreencastFrame{
		Data:      "CCCC",
		Metadata:  &page.ScreencastFrameMetadata{DeviceWidth: 100, DeviceHeight: 50},
		SessionID: 42,
	})
	elapsed := time.Since(start)
	require.Less(t, elapsed, 500*time.Millisecond,
		"handleScreencastEvent must return immediately regardless of ack worker state")

	select {
	case f := <-received:
		require.Equal(t, "CCCC", f.Data)
		require.Equal(t, 1, f.Seq)
	case <-time.After(2 * time.Second):
		t.Fatal("frame was not delivered to the viewer")
	}

	// The ack was queued for the (absent, in this test) worker to consume.
	select {
	case sessionID := <-lv.ackCh:
		require.Equal(t, int64(42), sessionID)
	default:
		t.Fatal("frame's session ID was not queued for ack")
	}
}

func TestLiveView_HandleScreencastEvent_IgnoresOtherEventTypes(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink)}
	called := false
	lv.viewers["v1"] = func(LiveFrame) { called = true }

	require.NotPanics(t, func() {
		lv.handleScreencastEvent(context.Background(), "not-a-screencast-event")
	})
	require.False(t, called, "a non-screencast CDP event must not be delivered as a frame")
}

// --- input gated by control (ADR-038 D6) ---

func TestLiveView_DispatchInput_RequiresControl(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink)}

	err := lv.dispatchInput("viewerA", LiveInput{Kind: "mouse_move", X: 1, Y: 2})
	require.Error(t, err, "input must be refused when nobody holds control")
	require.Contains(t, err.Error(), "does not hold control")

	require.True(t, lv.takeControl("viewerA"))

	// viewerB never took control — still refused even though SOMEONE holds it.
	err = lv.dispatchInput("viewerB", LiveInput{Kind: "mouse_move", X: 1, Y: 2})
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not hold control")

	// viewerA holds control but the session was never attached (tabCtx is
	// nil) — proves the request passed the control gate and reached the
	// dispatch step, which then fails closed on the missing session rather
	// than panicking or silently no-op'ing.
	err = lv.dispatchInput("viewerA", LiveInput{Kind: "mouse_move", X: 1, Y: 2})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not attached")
}

func TestLiveView_DispatchInput_RateLimited(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink)}
	require.True(t, lv.takeControl("viewerA"))

	// Exhaust the per-second budget; every call fails closed on "not
	// attached" (nil tabCtx) EXCEPT once the rate limit itself trips, at
	// which point the error message changes to the rate-limit message —
	// proving allowInputLocked is consulted before the tabCtx check.
	var lastErr error
	for i := 0; i < maxInputEventsPerSecond+5; i++ {
		lastErr = lv.dispatchInput("viewerA", LiveInput{Kind: "mouse_move"})
	}
	require.Error(t, lastErr)
	require.Contains(t, lastErr.Error(), "rate limit")
}

// --- navigate: ADR-039 D-A2 SSRF gate on the live-WS input path ---
//
// Unlike the agent's browser_navigate tool (tools.go's NavigateTool.Execute,
// which calls BrowserManager.ValidateURL before ever touching CDP), the
// live-WS input path had no URL gate of its own before this feature — a
// user-driven "navigate" input went straight to CDP. dispatchInput now runs
// the SAME ValidateURL check before dispatch (see live.go's dispatchInput).
// These tests use IP-literal URLs so CheckHost's fail-closed private-range
// check applies with no DNS lookup — no live network dependency, matching
// this file's "no real Chromium/CDP connection" convention (see the package
// doc comment at the top of this file).

// newNavigateTestLiveView builds a LiveView backed by a real BrowserManager
// (so ValidateURL's SSRF/scheme logic is genuinely exercised, not mocked)
// with a stub runCDP so no real chromedp/Chromium connection is needed.
func newNavigateTestLiveView(t *testing.T, runCDP func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error) *LiveView {
	t.Helper()
	mgr, err := NewBrowserManager(BrowserConfig{PageTimeout: 5 * time.Second}, security.NewSSRFChecker(nil))
	require.NoError(t, err)
	return &LiveView{
		mgr:       mgr,
		sessionID: "s1",
		viewers:   make(map[string]FrameSink),
		tabCtx:    context.Background(),
		runCDP:    runCDP,
	}
}

func TestLiveView_DispatchInput_Navigate_SSRFBlocked_PrivateIPNotDispatched(t *testing.T) {
	var dispatched bool
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
		dispatched = true
		return nil
	})
	require.True(t, lv.takeControl("viewerA"))

	err := lv.dispatchInput("viewerA", LiveInput{Kind: "navigate", URL: "http://127.0.0.1/admin"})
	require.Error(t, err)
	require.False(t, IsBenignLiveInputError(err),
		"a blocked navigate URL must be a REAL error so the gateway surfaces browser_status(error) — never silently dropped as benign")
	require.Contains(t, err.Error(), "navigate blocked")
	require.False(t, dispatched, "an SSRF-blocked URL must never reach CDP dispatch")
}

func TestLiveView_DispatchInput_Navigate_BlockedSchemeNotDispatched(t *testing.T) {
	var dispatched bool
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
		dispatched = true
		return nil
	})
	require.True(t, lv.takeControl("viewerA"))

	err := lv.dispatchInput("viewerA", LiveInput{Kind: "navigate", URL: "javascript:alert(1)"})
	require.Error(t, err)
	require.False(t, IsBenignLiveInputError(err))
	require.Contains(t, err.Error(), "navigate blocked")
	require.False(t, dispatched, "a blocked scheme must never reach CDP dispatch")
}

// TestLiveView_DispatchInput_Navigate_DataSchemeNotDispatched covers the one
// blocked scheme (data:) not exercised elsewhere at this call site — the
// "javascript:" case above proves the blocked-scheme gate fires at all, but
// data: URLs are a distinct XSS/exfiltration vector (inline HTML/script
// payload, no network fetch involved) worth its own regression guard
// (7-reviewer MEDIUM finding: test coverage).
func TestLiveView_DispatchInput_Navigate_DataSchemeNotDispatched(t *testing.T) {
	var dispatched bool
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
		dispatched = true
		return nil
	})
	require.True(t, lv.takeControl("viewerA"))

	err := lv.dispatchInput("viewerA", LiveInput{Kind: "navigate", URL: "data:text/html,<script>alert(1)</script>"})
	require.Error(t, err)
	require.False(t, IsBenignLiveInputError(err))
	require.Contains(t, err.Error(), "navigate blocked")
	require.False(t, dispatched, "a data: URL must never reach CDP dispatch")
}

// blockingResolver is a security.Resolver stub that never returns on its
// own — it blocks until its ctx is Done, exactly the worst case a
// blackholed/slow-DNS hostname can produce against the real resolver.
type blockingResolver struct{}

func (blockingResolver) LookupIPAddr(ctx context.Context, _ string) ([]net.IPAddr, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestLiveView_DispatchInput_Navigate_DNSResolutionIsBounded is the
// regression guard for the 7-reviewer BLOCKER finding: dispatchInput used to
// pass the bare tabCtx straight into BrowserManager.ValidateURL, whose SSRF
// host check does DNS resolution with no deadline of its own. tabCtx here is
// deliberately context.Background() — never-expiring, mirroring the REAL
// tabCtx dispatchInput receives (the live agent tab's own context, not a
// per-call timeout) — so against blockingResolver (which only ever returns
// once ITS ctx is canceled) this test would hang forever against the pre-fix
// code: nothing would ever cancel a context.Background(). dispatchInput now
// wraps the ValidateURL call in context.WithTimeout(tabCtx,
// lv.mgr.PageTimeout()), so the call is bounded even though tabCtx itself
// never expires — proven here via a short PageTimeout and a test-level
// deadline well above it.
func TestLiveView_DispatchInput_Navigate_DNSResolutionIsBounded(t *testing.T) {
	ssrf := security.NewSSRFChecker(nil)
	ssrf.SetResolver(blockingResolver{})

	mgr, err := NewBrowserManager(BrowserConfig{PageTimeout: 200 * time.Millisecond}, ssrf)
	require.NoError(t, err)

	var dispatched bool
	lv := &LiveView{
		mgr:       mgr,
		sessionID: "s1",
		viewers:   make(map[string]FrameSink),
		tabCtx:    context.Background(), // deliberately never-expiring — see doc comment
		runCDP: func(context.Context, time.Duration, ...chromedp.Action) error {
			dispatched = true
			return nil
		},
	}
	require.True(t, lv.takeControl("viewerA"))

	errCh := make(chan error, 1)
	go func() {
		errCh <- lv.dispatchInput("viewerA", LiveInput{Kind: "navigate", URL: "http://blackholed.invalid.example/"})
	}()

	select {
	case dispatchErr := <-errCh:
		require.Error(t, dispatchErr, "a blackholed DNS lookup must fail closed, not succeed")
		require.False(t, IsBenignLiveInputError(dispatchErr))
		require.Contains(t, dispatchErr.Error(), "navigate blocked")
	case <-time.After(2 * time.Second):
		t.Fatal("dispatchInput hung well past PageTimeout — the DNS resolution call has no bounded " +
			"deadline of its own (the exact unbounded-wait hazard the ADR-038 deadlock postmortem exists " +
			"to prevent: a hung call here freezes the whole browser-ws connection's single readLoop goroutine)")
	}
	require.False(t, dispatched, "a blackholed-DNS navigate must never reach CDP dispatch")
}

func TestLiveView_DispatchInput_Navigate_MalformedURLNotDispatched(t *testing.T) {
	var dispatched bool
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
		dispatched = true
		return nil
	})
	require.True(t, lv.takeControl("viewerA"))

	err := lv.dispatchInput("viewerA", LiveInput{Kind: "navigate", URL: "http://a b.com/"})
	require.Error(t, err)
	require.False(t, IsBenignLiveInputError(err))
	require.Contains(t, err.Error(), "navigate blocked")
	require.False(t, dispatched, "a malformed URL must never reach CDP dispatch")
}

func TestLiveView_DispatchInput_Navigate_EmptyURLNotDispatched(t *testing.T) {
	var dispatched bool
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
		dispatched = true
		return nil
	})
	require.True(t, lv.takeControl("viewerA"))

	err := lv.dispatchInput("viewerA", LiveInput{Kind: "navigate", URL: ""})
	require.Error(t, err)
	require.False(t, IsBenignLiveInputError(err))
	require.Contains(t, err.Error(), "non-empty")
	require.False(t, dispatched, "an empty URL must never reach CDP dispatch")
}

func TestLiveView_DispatchInput_Navigate_ValidURLPassesSSRFAndDispatches(t *testing.T) {
	var dispatched bool
	var gotActions []chromedp.Action
	lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		dispatched = true
		gotActions = actions
		return nil
	})
	require.True(t, lv.takeControl("viewerA"))

	// A public IP literal: not in any blocked/private range, and (being an
	// IP literal) CheckHost resolves it with no DNS lookup.
	err := lv.dispatchInput("viewerA", LiveInput{Kind: "navigate", URL: "http://8.8.8.8/"})
	require.NoError(t, err)
	require.True(t, dispatched, "an unblocked URL must reach CDP dispatch")
	require.Len(t, gotActions, 1, "dispatchInput must build and dispatch exactly one navigate action")
}

// TestLiveView_DispatchInput_Navigate_RequiresControl proves the pre-existing
// control-lock gate (checked at the very top of dispatchInput) still applies
// to the new "navigate" kind exactly like every other kind — and, since the
// control check runs before the SSRF check, an uncontrolled/wrong-viewer
// navigate never even reaches ValidateURL or CDP.
func TestLiveView_DispatchInput_Navigate_RequiresControl(t *testing.T) {
	var dispatched bool
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
		dispatched = true
		return nil
	})

	// Nobody holds control yet.
	err := lv.dispatchInput("viewerA", LiveInput{Kind: "navigate", URL: "http://8.8.8.8/"})
	require.Error(t, err)
	require.True(t, IsBenignLiveInputError(err), "not-controller is the benign, high-frequency rejection kind")
	require.Contains(t, err.Error(), "does not hold control")
	require.False(t, dispatched)

	require.True(t, lv.takeControl("viewerA"))

	// viewerB never took control — still refused even though someone holds
	// it, and the SSRF gate is never reached even for a URL that would
	// otherwise pass it.
	err = lv.dispatchInput("viewerB", LiveInput{Kind: "navigate", URL: "http://8.8.8.8/"})
	require.Error(t, err)
	require.True(t, IsBenignLiveInputError(err))
	require.Contains(t, err.Error(), "does not hold control")
	require.False(t, dispatched)
}

func TestLiveView_AllowInputLocked_CapsPerSecond(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink)}

	lv.mu.Lock()
	defer lv.mu.Unlock()

	allowed := 0
	for i := 0; i < maxInputEventsPerSecond+10; i++ {
		if lv.allowInputLocked() {
			allowed++
		}
	}
	require.Equal(t, maxInputEventsPerSecond, allowed,
		"allowInputLocked must allow exactly the configured cap within one window")

	// Simulate the window rolling over: a stale window start must reset.
	lv.inputWindowStart = time.Now().Add(-2 * time.Second)
	require.True(t, lv.allowInputLocked(), "a new window must reset the counter")
}

// --- viewer refcount: screencast stops only when the last viewer detaches ---

func TestLiveView_Detach_RefCountsViewers(t *testing.T) {
	// mgr is needed because detach()'s teardown path (the last viewer
	// leaving) reads mgr.PageTimeout() to bound its StopScreencast call —
	// see the ADR-038 deadlock postmortem in live.go. Production LiveViews
	// always go through LiveViewRegistry.view(), which sets mgr; this
	// hand-built test LiveView needs it set explicitly.
	mgr := &BrowserManager{cfg: BrowserConfig{PageTimeout: 5 * time.Second}}
	lv := &LiveView{mgr: mgr, sessionID: "s1", viewers: make(map[string]FrameSink), runCDP: runCDPWithTimeout}

	// Fake an "active" screencast the same way attach() would have left it,
	// without a real chromedp.Run(StartScreencast) call (spike-proven CDP
	// mechanics are covered elsewhere; this test is about the refcount
	// bookkeeping, not the wire call).
	listenCtx, cancel := context.WithCancel(context.Background())
	lv.tabCtx = context.Background()
	lv.listenCtx = listenCtx
	lv.stopListen = cancel
	lv.viewers["v1"] = func(LiveFrame) {}
	lv.viewers["v2"] = func(LiveFrame) {}

	lv.mu.Lock()
	require.True(t, lv.isActiveLocked())
	lv.mu.Unlock()

	lv.detach("v1")

	lv.mu.Lock()
	require.Len(t, lv.viewers, 1, "detaching one of two viewers must not remove the other")
	require.True(t, lv.isActiveLocked(), "screencast must stay active while a viewer remains")
	lv.mu.Unlock()

	lv.detach("v2")

	lv.mu.Lock()
	require.Empty(t, lv.viewers)
	require.False(t, lv.isActiveLocked(), "screencast must stop once the last viewer detaches")
	lv.mu.Unlock()
	require.Error(t, listenCtx.Err(), "detaching the last viewer must cancel the listen subscription")
}

func TestLiveView_Detach_UnknownViewerIsNoop(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink)}
	lv.viewers["v1"] = func(LiveFrame) {}
	require.NotPanics(t, func() { lv.detach("never-attached") })
	lv.mu.Lock()
	defer lv.mu.Unlock()
	require.Len(t, lv.viewers, 1, "detaching an unknown viewer must not disturb existing viewers")
}

func TestLiveView_Detach_ReleasesControl(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink)}
	lv.viewers["v1"] = func(LiveFrame) {}
	require.True(t, lv.takeControl("v1"))
	require.Equal(t, "v1", lv.getController())

	lv.detach("v1")

	require.Equal(t, "", lv.getController(), "a departing controller must never leave the lock dangling")
}

// --- control lock: take/release/query, no preemption in v1 ---

func TestLiveView_TakeReleaseControl(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink)}

	require.Equal(t, "", lv.getController())
	require.True(t, lv.takeControl("viewerA"))
	require.Equal(t, "viewerA", lv.getController())

	// Re-taking control you already hold is idempotent.
	require.True(t, lv.takeControl("viewerA"))

	// A second viewer cannot preempt (ADR-038 D6: cooperative, no preemption).
	require.False(t, lv.takeControl("viewerB"))
	require.Equal(t, "viewerA", lv.getController())

	// Releasing as the wrong viewer is a safe no-op.
	lv.releaseControl("viewerB")
	require.Equal(t, "viewerA", lv.getController())

	lv.releaseControl("viewerA")
	require.Equal(t, "", lv.getController())

	// Now viewerB can take it.
	require.True(t, lv.takeControl("viewerB"))
}

// requireControlBroadcast reads the next value off ch, failing the test if
// none arrives within a generous bound. Needed because broadcastControl (B2,
// live.go) dispatches each ControlSink on its own goroutine rather than
// invoking it inline — takeControl/releaseControl/detach all return before
// their broadcast(s) are actually delivered, so a test asserting on a
// ControlSink's output must synchronize on the channel, not on the
// take/release call returning.
func requireControlBroadcast(t *testing.T, ch <-chan bool, want bool, msg string) {
	t.Helper()
	select {
	case got := <-ch:
		require.Equal(t, want, got, msg)
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for control broadcast: %s", msg)
	}
}

// requireNoControlBroadcast asserts nothing arrives on ch within a short
// bounded window. This is a ceiling on how long an (incorrect) broadcast
// would take to arrive, not a synchronization mechanism — every EXPECTED
// broadcast preceding this call has already been drained via
// requireControlBroadcast, so there is nothing legitimately in flight this
// could race against; a value landing here would only ever be an erroneous
// extra broadcast the fix under test must not produce.
func requireNoControlBroadcast(t *testing.T, ch <-chan bool, msg string) {
	t.Helper()
	select {
	case got := <-ch:
		t.Fatalf("unexpected control broadcast %v: %s", got, msg)
	case <-time.After(150 * time.Millisecond):
	}
}

// TestLiveView_TakeReleaseControl_BroadcastsToOtherViewers is the focused
// regression test for ADR-039 UAT finding BE-1 ("two viewers of the same
// live browser session disagree about who's driving"): the server has
// always single-controller-locked (takeControl/releaseControl above), but
// before this fix no OTHER attached connection ever learned about a
// take/release — each one kept showing whatever status frame it happened to
// receive last. Simulates two fake connections (conn A, conn B) attached to
// the same session via their registered ControlSinks — no CDP/chromedp
// needed, this is pure LiveView bookkeeping (see the file header comment).
// Uses buffered channels (not a captured slice) because broadcastControl
// (B2) delivers asynchronously, on its own goroutine per sink.
func TestLiveView_TakeReleaseControl_BroadcastsToOtherViewers(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink), controlSinks: make(map[string]ControlSink)}

	gotA := make(chan bool, 4)
	gotB := make(chan bool, 4)
	lv.controlSinks["connA"] = func(controlledByOther bool) { gotA <- controlledByOther }
	lv.controlSinks["connB"] = func(controlledByOther bool) { gotB <- controlledByOther }

	require.True(t, lv.takeControl("connA"))

	requireControlBroadcast(t, gotB, true, "conn B (not the new controller) must be told someone else now controls")
	requireNoControlBroadcast(t, gotA, "the acting connection is never broadcast to — it gets its own direct browser_status response instead")

	lv.releaseControl("connA")

	requireControlBroadcast(t, gotB, false, "conn B must be told control was freed")
	requireNoControlBroadcast(t, gotA, "the acting connection is still never broadcast to on its own release")
}

// TestLiveView_TakeReleaseControl_BroadcastSkippedOnDeniedTake verifies a
// DENIED take (someone else already controls) never fans out — the control
// state didn't actually change, so no OTHER viewer's display should move.
func TestLiveView_TakeReleaseControl_BroadcastSkippedOnDeniedTake(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink), controlSinks: make(map[string]ControlSink)}

	gotC := make(chan bool, 4)
	lv.controlSinks["connC"] = func(controlledByOther bool) { gotC <- controlledByOther }

	require.True(t, lv.takeControl("connA"))
	requireControlBroadcast(t, gotC, true, "connA's successful take must broadcast true to connC")

	// connB's take is denied (connA already controls) — must not re-notify connC.
	require.False(t, lv.takeControl("connB"))
	requireNoControlBroadcast(t, gotC, "a denied take must not re-broadcast")

	// connB releasing (it never held control) is a no-op — must not broadcast.
	lv.releaseControl("connB")
	requireNoControlBroadcast(t, gotC, "releasing as a non-controller must not broadcast")
}

// TestLiveView_Detach_ImplicitReleaseBroadcastsToOtherViewers is the coverage
// gap the review flagged (B3): detach() broadcasts controlledByOther=false to
// other viewers when the CONTROLLING connection disconnects WITHOUT ever
// calling releaseControl() itself — a distinct code path from
// TestLiveView_TakeReleaseControl_BroadcastsToOtherViewers above (which only
// exercises the explicit-release branch). Mirrors
// TestLiveView_Detach_ReleasesControl's setup but adds a second viewer (B)
// with a registered ControlSink to observe the implicit-release fan-out.
func TestLiveView_Detach_ImplicitReleaseBroadcastsToOtherViewers(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink), controlSinks: make(map[string]ControlSink)}
	lv.viewers["viewerA"] = func(LiveFrame) {}
	lv.viewers["viewerB"] = func(LiveFrame) {}

	gotB := make(chan bool, 4)
	lv.controlSinks["viewerB"] = func(controlledByOther bool) { gotB <- controlledByOther }

	require.True(t, lv.takeControl("viewerA"))
	require.Equal(t, "viewerA", lv.getController())

	// viewerA disconnects without ever sending browser_control{action:"release"}.
	lv.detach("viewerA")

	require.Equal(t, "", lv.getController(), "a departing controller must never leave the lock dangling")
	requireControlBroadcast(t, gotB, false, "viewerB must learn the lock was implicitly freed by viewerA's detach, not just an explicit release")
}

// TestLiveView_Attach_ReturnsControlledByOtherForNewViewer covers the
// "attaches while already controlled" half of ADR-039 UAT BE-1: a NEW
// connection attaching to a session some other viewer already controls must
// see controlled_by_other=true on its very first status frame, not only on
// the next take/release broadcast. Exercises the piggyback attach path (a
// screencast already active) so no chromedp.Run(StartScreencast) call is
// needed — see attach()'s isActiveLocked branch.
func TestLiveView_Attach_ReturnsControlledByOtherForNewViewer(t *testing.T) {
	lv := &LiveView{
		sessionID:    "s1",
		viewers:      make(map[string]FrameSink),
		statusSinks:  make(map[string]StatusSink),
		controlSinks: make(map[string]ControlSink),
	}
	// Fake an already-active screencast so attach() takes the piggyback path
	// (no CDP call) — same technique as TestLiveView_Detach_RefCountsViewers.
	listenCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lv.tabCtx = context.Background()
	lv.listenCtx = listenCtx

	require.True(t, lv.takeControl("viewerA"))

	controlledByOther, err := lv.attach(context.Background(), "viewerB", func(LiveFrame) {}, nil, nil)
	require.NoError(t, err)
	require.True(t, controlledByOther, "a new viewer attaching while another viewer already controls must see controlled_by_other=true")

	// The controller itself sees controlled_by_other=false on its own (re-)attach.
	controlledByOther, err = lv.attach(context.Background(), "viewerA", func(LiveFrame) {}, nil, nil)
	require.NoError(t, err)
	require.False(t, controlledByOther, "the controller's own attach must never report itself as 'someone else'")

	// A third viewer attaching after control was released sees false.
	lv.releaseControl("viewerA")
	controlledByOther, err = lv.attach(context.Background(), "viewerC", func(LiveFrame) {}, nil, nil)
	require.NoError(t, err)
	require.False(t, controlledByOther, "an uncontrolled session must report controlled_by_other=false to a new attach")
}

// --- LiveViewRegistry: session keying, default-session resolution, and the
//     public surface the gateway's WS handler drives. ---

func newTestRegistry() (*BrowserManager, *LiveViewRegistry) {
	mgr := &BrowserManager{sessions: make(map[string]*sessionEntry)}
	reg := newLiveViewRegistry(mgr)
	return mgr, reg
}

func TestResolveSessionID_DefaultsEmptyToDefaultSession(t *testing.T) {
	require.Equal(t, defaultSessionID, resolveSessionID(""))
	require.Equal(t, "custom-session", resolveSessionID("custom-session"))
}

func TestLiveViewRegistry_ControlLifecycle(t *testing.T) {
	_, reg := newTestRegistry()

	require.False(t, reg.IsControlled("s1"))
	require.Equal(t, "", reg.Controller("s1"))

	require.True(t, reg.TakeControl("s1", "viewerA"))
	require.True(t, reg.IsControlled("s1"))
	require.Equal(t, "viewerA", reg.Controller("s1"))

	require.False(t, reg.TakeControl("s1", "viewerB"), "no preemption in v1")

	reg.ReleaseControl("s1", "viewerB") // not the controller — no-op
	require.Equal(t, "viewerA", reg.Controller("s1"))

	reg.ReleaseControl("s1", "viewerA")
	require.False(t, reg.IsControlled("s1"))
	require.True(t, reg.TakeControl("s1", "viewerB"), "control is available again after release")
}

func TestLiveViewRegistry_ControlIsPerSession(t *testing.T) {
	_, reg := newTestRegistry()

	require.True(t, reg.TakeControl("s1", "viewerA"))
	require.True(t, reg.TakeControl("s2", "viewerA"), "control on one session must not affect another")
	require.True(t, reg.IsControlled("s1"))
	require.True(t, reg.IsControlled("s2"))

	reg.ReleaseControl("s1", "viewerA")
	require.False(t, reg.IsControlled("s1"))
	require.True(t, reg.IsControlled("s2"), "releasing s1 must not release s2")
}

func TestLiveViewRegistry_DetachReleasesControl(t *testing.T) {
	_, reg := newTestRegistry()

	require.True(t, reg.TakeControl("s1", "viewerA"))
	// Simulate an attached viewer without starting a real screencast (see
	// TestLiveView_Detach_RefCountsViewers for why this is the right level
	// to test refcounting at).
	reg.view("s1").viewers["viewerA"] = func(LiveFrame) {}

	reg.Detach("s1", "viewerA")

	require.False(t, reg.IsControlled("s1"), "detaching the controller must release control")
}

func TestLiveViewRegistry_DetachUnknownSessionIsNoop(t *testing.T) {
	_, reg := newTestRegistry()
	require.NotPanics(t, func() { reg.Detach("never-attached-session", "viewerA") })
}

func TestLiveViewRegistry_InputWithNoLiveView(t *testing.T) {
	_, reg := newTestRegistry()
	err := reg.Input("s1", "viewerA", LiveInput{Kind: "mouse_move"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no active live view")
}

// --- buildInputAction / mouseButton: pure CDP-action mapping (no browser) ---

func TestBuildInputAction(t *testing.T) {
	tests := []struct {
		name    string
		in      LiveInput
		wantErr bool
	}{
		{"mouse_move", LiveInput{Kind: "mouse_move", X: 1, Y: 2, HasXY: true}, false},
		{"mouse_down", LiveInput{Kind: "mouse_down", X: 1, Y: 2, HasXY: true, Button: "left"}, false},
		{"mouse_up", LiveInput{Kind: "mouse_up", X: 1, Y: 2, HasXY: true, Button: "right"}, false},
		{"wheel", LiveInput{Kind: "wheel", X: 1, Y: 2, HasXY: true, DeltaX: 3, DeltaY: 4}, false},
		{"key_down", LiveInput{Kind: "key_down", Key: "a", Code: "KeyA"}, false},
		{"key_up", LiveInput{Kind: "key_up", Key: "a", Code: "KeyA"}, false},
		{"text", LiveInput{Kind: "text", Text: "hello"}, false},
		{"text requires non-empty", LiveInput{Kind: "text", Text: ""}, true},
		// ADR-039 D-A2: navigate mirrors the pre-existing "text" empty guard —
		// buildInputAction rejects an empty URL on its own (no BrowserManager
		// needed for this check); the SSRF/scheme gate is a separate step in
		// dispatchInput, covered by the Navigate_* tests below.
		{"navigate", LiveInput{Kind: "navigate", URL: "http://example.com/path"}, false},
		{"navigate requires non-empty url", LiveInput{Kind: "navigate", URL: ""}, true},
		// 7-reviewer LOW finding (type-safety defense-in-depth): a navigate
		// input must not also carry mouse coordinates — see LiveInput.URL's
		// doc comment.
		{"navigate must not carry coordinates", LiveInput{Kind: "navigate", URL: "http://example.com/", HasXY: true}, true},
		{"unknown kind", LiveInput{Kind: "bogus"}, true},
		// ADR-038 finding #5: per-kind validation added alongside the
		// pre-existing "text" guard — mouse/wheel kinds need real
		// coordinates (HasXY unset must be rejected, not silently dispatched
		// at (0,0)), key kinds need at least a key or a code.
		{"mouse_move without coordinates", LiveInput{Kind: "mouse_move"}, true},
		{"mouse_down without coordinates", LiveInput{Kind: "mouse_down"}, true},
		{"mouse_up without coordinates", LiveInput{Kind: "mouse_up"}, true},
		{"wheel without coordinates", LiveInput{Kind: "wheel"}, true},
		{"key_down without key or code", LiveInput{Kind: "key_down"}, true},
		{"key_up without key or code", LiveInput{Kind: "key_up"}, true},
		{"key_down with only code", LiveInput{Kind: "key_down", Code: "KeyA"}, false},
		{"key_up with only key", LiveInput{Kind: "key_up", Key: "a"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action, err := buildInputAction(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, action)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, action)
		})
	}
}

// TestBuildInputAction_ClampsModifiers guards against a malformed
// out-of-range modifiers bitmask reaching cdproto directly (ADR-038 finding
// #5) — schema validation (finding #3) catches this on the wire when
// gateway.validate_inbound=true, but this is the defense-in-depth backstop
// for when it's off, or for any future caller that bypasses the WS frame
// entirely.
func TestBuildInputAction_ClampsModifiers(t *testing.T) {
	require.Equal(t, 0, clampModifiers(-5))
	require.Equal(t, 0, clampModifiers(0))
	require.Equal(t, 15, clampModifiers(15))
	require.Equal(t, 15, clampModifiers(999))

	// End-to-end through buildInputAction: an out-of-range value must not
	// error (it's clamped, not rejected) and must produce a valid action.
	action, err := buildInputAction(LiveInput{Kind: "text", Text: "x", Modifiers: 999})
	require.NoError(t, err)
	require.NotNil(t, action)
}

// TestIsBenignLiveInputError verifies the benign/real classification
// (ADR-038 finding #4) that browser_ws.go's handleInput relies on to decide
// whether a dispatchInput failure is worth a browser_status(error) frame.
func TestIsBenignLiveInputError(t *testing.T) {
	require.True(t, IsBenignLiveInputError(benignInputError("x")))
	require.False(t, IsBenignLiveInputError(realInputError("x")))
	require.False(t, IsBenignLiveInputError(nil), "a nil error is not a benign LiveInputError")
	require.False(t, IsBenignLiveInputError(fmt.Errorf("plain error")),
		"an unclassified error must be treated as real (fail-safe direction)")
}

func TestMouseButton(t *testing.T) {
	require.Equal(t, input.Left, mouseButton("left"))
	require.Equal(t, input.Middle, mouseButton("middle"))
	require.Equal(t, input.Right, mouseButton("right"))
	require.Equal(t, input.Back, mouseButton("back"))
	require.Equal(t, input.Forward, mouseButton("forward"))
	require.Equal(t, input.None, mouseButton(""))
	require.Equal(t, input.None, mouseButton("not-a-button"))
}

// --- BrowserManager.Live() accessor ---

func TestBrowserManager_Live(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	mgr, err := NewBrowserManager(cfg, security.NewSSRFChecker(nil))
	require.NoError(t, err)

	require.NotNil(t, mgr.Live())
	require.Same(t, mgr.Live(), mgr.Live(), "Live() must return the same registry instance every call")
}

// --- controlledResult: ADR-038 D6 tool-side gate ---

func TestControlledResult(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	mgr, err := NewBrowserManager(cfg, security.NewSSRFChecker(nil))
	require.NoError(t, err)

	require.Nil(t, controlledResult(mgr, "browser_click"), "an uncontrolled session must not defer")

	require.True(t, mgr.Live().TakeControl(defaultSessionID, "viewer1"))

	result := controlledResult(mgr, "browser_click")
	require.NotNil(t, result, "a controlled session must defer the interactive tool")
	require.False(t, result.IsError, "deferral is not a tool failure")
	require.Contains(t, result.ForLLM, "browser_click")
	require.Contains(t, result.ForLLM, "human is currently controlling")

	mgr.Live().ReleaseControl(defaultSessionID, "viewer1")
	require.Nil(t, controlledResult(mgr, "browser_click"), "releasing control must un-gate the tool again")
}
