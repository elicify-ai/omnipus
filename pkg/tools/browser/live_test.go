package browser

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
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
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink)}
	received := make(chan LiveFrame, 1)
	lv.viewers["v1"] = func(f LiveFrame) { received <- f }

	// tabCtx is a plain context.Background(), not a real chromedp context, so
	// the ack's chromedp.Run(tabCtx, page.ScreencastFrameAck(...)) call
	// returns chromedp.ErrInvalidContext instead of dialing a browser. What
	// this test proves is the contract that matters here: handleScreencastEvent
	// itself returns immediately (the ack goroutine is fire-and-forget) and the
	// frame still reaches the viewer synchronously — i.e. delivery is never
	// gated on the ack succeeding.
	start := time.Now()
	lv.handleScreencastEvent(context.Background(), &page.EventScreencastFrame{
		Data:      "CCCC",
		Metadata:  &page.ScreencastFrameMetadata{DeviceWidth: 100, DeviceHeight: 50},
		SessionID: 42,
	})
	elapsed := time.Since(start)
	require.Less(t, elapsed, 500*time.Millisecond,
		"handleScreencastEvent must return immediately — the ack is dispatched off-goroutine (ADR-038 D3)")

	select {
	case f := <-received:
		require.Equal(t, "CCCC", f.Data)
		require.Equal(t, 1, f.Seq)
	case <-time.After(2 * time.Second):
		t.Fatal("frame was not delivered to the viewer")
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
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink)}

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
		{"mouse_move", LiveInput{Kind: "mouse_move", X: 1, Y: 2}, false},
		{"mouse_down", LiveInput{Kind: "mouse_down", X: 1, Y: 2, Button: "left"}, false},
		{"mouse_up", LiveInput{Kind: "mouse_up", X: 1, Y: 2, Button: "right"}, false},
		{"wheel", LiveInput{Kind: "wheel", X: 1, Y: 2, DeltaX: 3, DeltaY: 4}, false},
		{"key_down", LiveInput{Kind: "key_down", Key: "a", Code: "KeyA"}, false},
		{"key_up", LiveInput{Kind: "key_up", Key: "a", Code: "KeyA"}, false},
		{"text", LiveInput{Kind: "text", Text: "hello"}, false},
		{"text requires non-empty", LiveInput{Kind: "text", Text: ""}, true},
		{"unknown kind", LiveInput{Kind: "bogus"}, true},
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
