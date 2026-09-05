package browser

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// These tests deliberately avoid a real Chromium/CDP connection (Chrome-in-CI
// is not approved for this repo — see execute_e2e_test.go's skipIfNoBrowser
// convention). They exercise the pure engine logic — control-lock gating,
// rate limiting, viewer refcounting, and tab-following — by driving
// LiveView/LiveViewRegistry directly (same package, unexported
// fields/methods). Video is carried exclusively by WebRTC (ADR-061); this
// package's LiveView has no CDP screencast path left to exercise here. The
// CDP wire calls that remain (Input.dispatch*, Page.getLayoutMetrics,
// Browser.setWindowBounds) are spike-proven (ADR-038 context) and covered by
// the existing OMNIPUS_BROWSER_E2E-gated suite for the tool Execute paths;
// nothing here needs a live browser to be a real, non-trivial test of this
// file's logic.

// --- input is NEVER gated by control (operator directive, 2026-08-03) ---
//
// This suite previously asserted the opposite (ADR-038 D6's exclusive
// single-controller lock: "input must be refused when nobody holds control").
// That model was removed: the live panel is a REAL BROWSER the human uses
// normally, and the agent can steer it too — concurrently. The lock made a
// second panel, a pop-out, or a stale automation session silently disable the
// actual user's mouse, keyboard and omnibox while the UI said "Someone else is
// driving".

func TestLiveView_DispatchInput_NeverRequiresControl(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]struct{})}

	// Nobody holds control at all. The input must still reach the dispatch
	// step — proven by it failing on the UNATTACHED SESSION (nil tabCtx)
	// rather than on a control rejection.
	err := lv.dispatchInput("viewerA", LiveInput{Kind: "mouse_move", X: 1, Y: 2})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not attached",
		"input must never be refused for lack of a control lock — it has to reach dispatch")
	require.NotContains(t, err.Error(), "does not hold control")

	// Someone else holds control. A different viewer's input must STILL get
	// through: this is precisely the case that left a real human with a dead
	// mouse and keyboard.
	require.True(t, lv.takeControl("viewerA"))
	err = lv.dispatchInput("viewerB", LiveInput{Kind: "mouse_move", X: 1, Y: 2})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not attached",
		"a viewer must be able to act while ANOTHER viewer holds control — control is shared, not exclusive")
	require.NotContains(t, err.Error(), "does not hold control")
}

func TestLiveView_DispatchInput_RateLimited(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]struct{})}
	require.True(t, lv.takeControl("viewerA"))

	// Exhaust the per-second budget; every call fails closed on "not
	// attached" (nil tabCtx) EXCEPT once the rate limit itself trips, at
	// which point the error message changes to the rate-limit message —
	// proving allowInputLocked is consulted before the tabCtx check.
	var lastErr error
	for i := 0; i < maxCoalescibleInputEventsPerSecond+5; i++ {
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
func newNavigateTestLiveView(
	t *testing.T,
	runCDP func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error,
) *LiveView {
	t.Helper()
	mgr, err := NewBrowserManager(BrowserConfig{PageTimeout: 5 * time.Second}, security.NewSSRFChecker(nil))
	require.NoError(t, err)
	return &LiveView{
		mgr:       mgr,
		sessionID: "s1",
		viewers:   make(map[string]struct{}),
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
	require.False(
		t,
		IsBenignLiveInputError(err),
		"a blocked navigate URL must be a REAL error so the gateway surfaces browser_status(error) — never silently dropped as benign",
	)
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
		viewers:   make(map[string]struct{}),
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
func TestLiveView_DispatchInput_Navigate_NeverRequiresControl(t *testing.T) {
	var dispatched bool
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
		dispatched = true
		return nil
	})

	// Nobody holds control — navigation must still go through. This is the
	// omnibox case from the 2026-08-03 recording: the user typed a URL and
	// pressing Enter did nothing at all, because submit was gated on the lock.
	err := lv.dispatchInput("viewerA", LiveInput{Kind: "navigate", URL: "http://8.8.8.8/"})
	require.NoError(t, err)
	require.True(t, dispatched, "navigate must dispatch without any control lock")

	// Someone ELSE holds control — a second viewer's navigation must still
	// work. Control is shared.
	dispatched = false
	require.True(t, lv.takeControl("viewerA"))
	err = lv.dispatchInput("viewerB", LiveInput{Kind: "navigate", URL: "http://8.8.8.8/"})
	require.NoError(t, err)
	require.True(t, dispatched,
		"a viewer must be able to navigate while ANOTHER viewer holds control")
}

func TestLiveView_AllowInputLocked_CapsPerSecond(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]struct{})}

	lv.mu.Lock()
	defer lv.mu.Unlock()

	allowed := 0
	for i := 0; i < maxCoalescibleInputEventsPerSecond+10; i++ {
		if lv.allowInputLocked("mouse_move") {
			allowed++
		}
	}
	require.Equal(t, maxCoalescibleInputEventsPerSecond, allowed,
		"allowInputLocked must allow exactly the configured cap within one window")

	// Simulate the window rolling over: a stale window start must reset.
	lv.inputWindowStart = time.Now().Add(-2 * time.Second)
	require.True(t, lv.allowInputLocked("mouse_move"), "a new window must reset the counter")
}

// TestLiveView_AllowInputLocked_MoveFloodCannotStarveButtonRelease pins the
// reason the budget is split at all.
//
// A dropped mouse_move is self-healing — the next one supersedes it. A dropped
// mouse_up is not: the remote page goes on believing the button is held, so the
// user gets a stuck drag or a runaway selection and nothing in the UI says why.
// Under the previous single shared counter, a sustained pointer stream (which
// now legitimately comes from several concurrent drivers at once, the exclusive
// control lock having been removed) could exhaust the whole allowance and the
// button release that followed was refused.
func TestLiveView_AllowInputLocked_MoveFloodCannotStarveButtonRelease(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]struct{})}

	lv.mu.Lock()
	defer lv.mu.Unlock()

	// Saturate the coalescible bucket well past its cap.
	for i := 0; i < maxCoalescibleInputEventsPerSecond*2; i++ {
		lv.allowInputLocked("mouse_move")
	}
	require.False(t, lv.allowInputLocked("wheel"),
		"wheel shares the coalescible bucket and must be throttled once it is exhausted")

	require.True(t, lv.allowInputLocked("mouse_up"),
		"a button RELEASE must survive a move flood — this is the stuck-button bug")
	require.True(t, lv.allowInputLocked("mouse_down"), "and so must a press")
	require.True(t, lv.allowInputLocked("key_down"), "and key transitions")
}

// The discrete bucket is still bounded — splitting the budget must not create
// an unmetered path for a runaway client.
func TestLiveView_AllowInputLocked_DiscreteBucketStillBounded(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]struct{})}

	lv.mu.Lock()
	defer lv.mu.Unlock()

	allowed := 0
	for i := 0; i < maxDiscreteInputEventsPerSecond+25; i++ {
		if lv.allowInputLocked("mouse_down") {
			allowed++
		}
	}
	require.Equal(t, maxDiscreteInputEventsPerSecond, allowed,
		"discrete transitions must remain capped, just on their own budget")
}

// --- viewer refcount: the death watch stops only when the last viewer detaches ---

func TestLiveView_Detach_RefCountsViewers(t *testing.T) {
	mgr := &BrowserManager{cfg: BrowserConfig{PageTimeout: 5 * time.Second}}
	lv := &LiveView{mgr: mgr, sessionID: "s1", viewers: make(map[string]struct{}), runCDP: runCDPWithTimeout}

	// Fake an "active" watch the same way attach() would have left it, with
	// no real goroutine watching (this test is about the refcount
	// bookkeeping, not watchForUnexpectedDeath's own behavior — covered
	// separately).
	listenCtx, cancel := context.WithCancel(context.Background())
	lv.tabCtx = context.Background()
	lv.listenCtx = listenCtx
	lv.stopListen = cancel
	lv.viewers["v1"] = struct{}{}
	lv.viewers["v2"] = struct{}{}

	lv.mu.Lock()
	require.True(t, lv.isActiveLocked())
	lv.mu.Unlock()

	lv.detach("v1")

	lv.mu.Lock()
	require.Len(t, lv.viewers, 1, "detaching one of two viewers must not remove the other")
	require.True(t, lv.isActiveLocked(), "the watch must stay active while a viewer remains")
	lv.mu.Unlock()

	lv.detach("v2")

	lv.mu.Lock()
	require.Empty(t, lv.viewers)
	require.False(t, lv.isActiveLocked(), "the watch must stop once the last viewer detaches")
	lv.mu.Unlock()
	require.Error(t, listenCtx.Err(), "detaching the last viewer must cancel the watch")
}

func TestLiveView_Detach_UnknownViewerIsNoop(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]struct{})}
	lv.viewers["v1"] = struct{}{}
	require.NotPanics(t, func() { lv.detach("never-attached") })
	lv.mu.Lock()
	defer lv.mu.Unlock()
	require.Len(t, lv.viewers, 1, "detaching an unknown viewer must not disturb existing viewers")
}

func TestLiveView_Detach_ReleasesControl(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]struct{})}
	lv.viewers["v1"] = struct{}{}
	require.True(t, lv.takeControl("v1"))
	require.Equal(t, "v1", lv.getController())

	lv.detach("v1")

	require.Equal(t, "", lv.getController(), "a departing controller must never leave the lock dangling")
}

// --- control lock: take/release/query, no preemption in v1 ---

func TestLiveView_TakeReleaseControl(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]struct{})}

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
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]struct{}), controlSinks: make(map[string]ControlSink)}

	gotA := make(chan bool, 4)
	gotB := make(chan bool, 4)
	lv.controlSinks["connA"] = func(controlledByOther bool) { gotA <- controlledByOther }
	lv.controlSinks["connB"] = func(controlledByOther bool) { gotB <- controlledByOther }

	require.True(t, lv.takeControl("connA"))

	requireControlBroadcast(t, gotB, true, "conn B (not the new controller) must be told someone else now controls")
	requireNoControlBroadcast(
		t,
		gotA,
		"the acting connection is never broadcast to — it gets its own direct browser_status response instead",
	)

	lv.releaseControl("connA")

	requireControlBroadcast(t, gotB, false, "conn B must be told control was freed")
	requireNoControlBroadcast(t, gotA, "the acting connection is still never broadcast to on its own release")
}

// TestLiveView_TakeReleaseControl_BroadcastSkippedOnDeniedTake verifies a
// DENIED take (someone else already controls) never fans out — the control
// state didn't actually change, so no OTHER viewer's display should move.
func TestLiveView_TakeReleaseControl_BroadcastSkippedOnDeniedTake(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]struct{}), controlSinks: make(map[string]ControlSink)}

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
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]struct{}), controlSinks: make(map[string]ControlSink)}
	lv.viewers["viewerA"] = struct{}{}
	lv.viewers["viewerB"] = struct{}{}

	gotB := make(chan bool, 4)
	lv.controlSinks["viewerB"] = func(controlledByOther bool) { gotB <- controlledByOther }

	require.True(t, lv.takeControl("viewerA"))
	require.Equal(t, "viewerA", lv.getController())

	// takeControl's own broadcast to viewerB ("someone else now controls")
	// must be drained before asserting on detach's broadcast below — both
	// land on the same buffered gotB channel, in order, and an un-drained
	// "true" here would otherwise be misread by requireControlBroadcast as
	// detach's "false" broadcast (a channel-ordering bug, not a real
	// assertion failure) — caught by -race, which perturbs goroutine
	// scheduling enough to make the two broadcasts' relative timing (and
	// thus this pre-existing bug) actually surface.
	requireControlBroadcast(t, gotB, true, "viewerB must first learn viewerA took control")

	// viewerA disconnects without ever sending browser_control{action:"release"}.
	lv.detach("viewerA")

	require.Equal(t, "", lv.getController(), "a departing controller must never leave the lock dangling")
	requireControlBroadcast(
		t,
		gotB,
		false,
		"viewerB must learn the lock was implicitly freed by viewerA's detach, not just an explicit release",
	)
}

// TestLiveView_Attach_ReturnsControlledByOtherForNewViewer covers the
// "attaches while already controlled" half of ADR-039 UAT BE-1: a NEW
// connection attaching to a session some other viewer already controls must
// see controlled_by_other=true on its very first status frame, not only on
// the next take/release broadcast. Exercises the piggyback attach path (a
// watch already active) — see attach()'s isActiveLocked branch.
func TestLiveView_Attach_ReturnsControlledByOtherForNewViewer(t *testing.T) {
	lv := &LiveView{
		sessionID:    "s1",
		viewers:      make(map[string]struct{}),
		statusSinks:  make(map[string]StatusSink),
		controlSinks: make(map[string]ControlSink),
	}
	// Fake an already-active watch so attach() takes the piggyback path —
	// same technique as TestLiveView_Detach_RefCountsViewers.
	listenCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lv.tabCtx = context.Background()
	lv.listenCtx = listenCtx

	require.True(t, lv.takeControl("viewerA"))

	controlledByOther, err := lv.attach(context.Background(), "viewerB", nil, nil, nil)
	require.NoError(t, err)
	require.True(
		t,
		controlledByOther,
		"a new viewer attaching while another viewer already controls must see controlled_by_other=true",
	)

	// The controller itself sees controlled_by_other=false on its own (re-)attach.
	controlledByOther, err = lv.attach(context.Background(), "viewerA", nil, nil, nil)
	require.NoError(t, err)
	require.False(t, controlledByOther, "the controller's own attach must never report itself as 'someone else'")

	// A third viewer attaching after control was released sees false.
	lv.releaseControl("viewerA")
	controlledByOther, err = lv.attach(context.Background(), "viewerC", nil, nil, nil)
	require.NoError(t, err)
	require.False(t, controlledByOther, "an uncontrolled session must report controlled_by_other=false to a new attach")
}

// --- LiveViewRegistry: session keying, default-session resolution, and the
//     public surface the gateway's WS handler drives. ---

func newTestRegistry() (*BrowserManager, *LiveViewRegistry) {
	mgr := &BrowserManager{sessions: make(map[string]*sessionEntry), key: testKey}
	reg := newLiveViewRegistry(mgr)
	return mgr, reg
}

// An omitted session id on a gateway-originated live-panel frame resolves to
// the WORKSPACE-OWNED tab set — the operator's own tabs (ADR-075 §0.2a). It
// used to resolve to the deleted shared session constant, which is the merge
// FR-080 exists to prevent; and it must NOT become ErrNoTabOwner, which is the
// right answer for a TOOL with no transcript session and the wrong one for the
// operator's own panel.
func TestResolveSessionID_DefaultsEmptyToTheOperatorsOwnTabs(t *testing.T) {
	mgr, reg := newTestRegistry()
	require.Equal(t, mgr.OperatorSessionID(), reg.resolveSessionID(""))
	require.NotEqual(t, testSessionID, reg.resolveSessionID(""),
		"the operator's tabs and a chat session's tabs are different sets")
	require.Equal(t, "custom-session", reg.resolveSessionID("custom-session"))
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
	reg.view("s1").viewers["viewerA"] = struct{}{}

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
		{
			"navigate must not carry coordinates",
			LiveInput{Kind: "navigate", URL: "http://example.com/", HasXY: true},
			true,
		},
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

// TestBuildInputAction_KeyEventCarriesVirtualKeyCode guards the UAT keyboard
// fix: an editing/navigation key (Backspace, Delete, Enter, arrows) and
// modifier shortcuts (Ctrl+A) only PERFORM their action in the page when the
// CDP Input.dispatchKeyEvent carries the Windows/native virtual key code —
// key/code alone deliver an event that does nothing. Assert the built action's
// params thread KeyCode through to both fields for key_down and key_up.
func TestBuildInputAction_KeyEventCarriesVirtualKeyCode(t *testing.T) {
	for _, kind := range []string{"key_down", "key_up"} {
		action, err := buildInputAction(LiveInput{Kind: kind, Key: "Backspace", Code: "Backspace", KeyCode: 8})
		require.NoError(t, err)
		p, ok := action.(*input.DispatchKeyEventParams)
		require.True(t, ok, "%s must build a *input.DispatchKeyEventParams", kind)
		require.Equal(t, int64(8), p.WindowsVirtualKeyCode, "%s WindowsVirtualKeyCode", kind)
		require.Equal(t, int64(8), p.NativeVirtualKeyCode, "%s NativeVirtualKeyCode", kind)
	}
}

// TestBuildInputAction_KeyDown_EnterPerformsDefaultAction — live UAT
// 2026-07-31: typing into a remote search box then pressing Enter did
// nothing, because key_down always dispatched CDP "rawKeyDown" with empty
// text. rawKeyDown delivers the DOM event only; "keyDown" with text is what
// runs text processing and default actions (form submit). Mirrors
// Puppeteer's convention: Enter synthesizes text "\r" and upgrades to
// keyDown; textless keys stay rawKeyDown; key_up is unaffected.
func TestBuildInputAction_KeyDown_EnterPerformsDefaultAction(t *testing.T) {
	// Enter with no client-supplied text: synthesized "\r", type keyDown.
	action, err := buildInputAction(LiveInput{Kind: "key_down", Key: "Enter", Code: "Enter", KeyCode: 13})
	require.NoError(t, err)
	p, ok := action.(*input.DispatchKeyEventParams)
	require.True(t, ok, "Enter key_down must build a *input.DispatchKeyEventParams")
	require.Equal(t, input.KeyDown, p.Type, "Enter must dispatch as keyDown, not rawKeyDown")
	require.Equal(t, "\r", p.Text, "Enter must carry the CR text that triggers default actions")

	// Client-supplied text also upgrades to keyDown, verbatim.
	action, err = buildInputAction(LiveInput{Kind: "key_down", Key: "a", Code: "KeyA", KeyCode: 65, Text: "a"})
	require.NoError(t, err)
	p, ok = action.(*input.DispatchKeyEventParams)
	require.True(t, ok, "key_down with client-supplied text must build a *input.DispatchKeyEventParams")
	require.Equal(t, input.KeyDown, p.Type)
	require.Equal(t, "a", p.Text)

	// A textless non-Enter key stays rawKeyDown (no text processing to run).
	action, err = buildInputAction(LiveInput{Kind: "key_down", Key: "ArrowDown", Code: "ArrowDown", KeyCode: 40})
	require.NoError(t, err)
	p, ok = action.(*input.DispatchKeyEventParams)
	require.True(t, ok, "textless key_down must build a *input.DispatchKeyEventParams")
	require.Equal(t, input.KeyRawDown, p.Type)
	require.Empty(t, p.Text)

	// key_up never synthesizes text, even for Enter.
	action, err = buildInputAction(LiveInput{Kind: "key_up", Key: "Enter", Code: "Enter", KeyCode: 13})
	require.NoError(t, err)
	p, ok = action.(*input.DispatchKeyEventParams)
	require.True(t, ok, "Enter key_up must build a *input.DispatchKeyEventParams")
	require.Equal(t, input.KeyUp, p.Type)
	require.Empty(t, p.Text)
}

// TestRescaleInputCoords covers the pure-math half of the root-cause doc's
// Fault 3 fix (docs/internal/browser-viewport-input-rootcause-2026-07-31.md):
// mapping a viewer's capture-frame pixel coordinates into the tab's CSS
// pixel space. No CDP/Chromium involved — see rescaleInputCoords's doc
// comment for why the math is factored out on its own.
func TestRescaleInputCoords(t *testing.T) {
	tests := []struct {
		name         string
		x, y         float64
		capW, capH   float64
		cssW, cssH   float64
		wantX, wantY float64
	}{
		{
			name: "identity when capture size equals CSS viewport size",
			x:    100, y: 200,
			capW: 1280, capH: 720,
			cssW: 1280, cssH: 720,
			wantX: 100, wantY: 200,
		},
		{
			// The exact root-cause doc measurement: a 319x158 capture stream
			// against a real ~1280x720 page — clicks were landing ~4x off.
			name: "root-cause scenario: 319x158 capture vs 1280x720 css",
			x:    100, y: 79, // roughly mid-frame in capture space
			capW: 319, capH: 158,
			cssW: 1280, cssH: 720,
			wantX: 100 * 1280.0 / 319.0, wantY: 79 * 720.0 / 158.0,
		},
		{
			// DPR-style: capture delivered at 2x the CSS viewport (a
			// high-DPI capture path), so coordinates must be halved.
			name: "capture at 2x css (DPR-style downscale)",
			x:    400, y: 300,
			capW: 2560, capH: 1440,
			cssW: 1280, cssH: 720,
			wantX: 200, wantY: 150,
		},
		{
			name: "zero origin stays at zero origin regardless of scale",
			x:    0, y: 0,
			capW: 319, capH: 158,
			cssW: 1280, cssH: 720,
			wantX: 0, wantY: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotX, gotY := rescaleInputCoords(tc.x, tc.y, tc.capW, tc.capH, tc.cssW, tc.cssH)
			require.InDelta(t, tc.wantX, gotX, 0.0001, "x")
			require.InDelta(t, tc.wantY, gotY, 0.0001, "y")
		})
	}
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

	require.Nil(t, controlledResult(mgr, testKey, testOwner, "browser_click"),
		"an uncontrolled session must not defer")

	require.True(t, mgr.Live().TakeControl(testSessionID, "viewer1"))

	result := controlledResult(mgr, testKey, testOwner, "browser_click")
	require.NotNil(t, result, "a controlled session must defer the interactive tool")
	require.False(t, result.IsError, "deferral is not a tool failure")
	require.Contains(t, result.ForLLM, "browser_click")
	require.Contains(t, result.ForLLM, "human is currently controlling")

	mgr.Live().ReleaseControl(testSessionID, "viewer1")
	require.Nil(t, controlledResult(mgr, testKey, testOwner, "browser_click"),
		"releasing control must un-gate the tool again")
}

// ---------------------------------------------------------------------------
// QA regression-wave item 10: lifecycle wiring TRIGGERS for
// CaptureSession.Stop()/Recapture() — TestCaptureSession_RecapturePropagatesToRelayAndIngest
// (capture_session_test.go) already covers what Recapture()/Stop() DO once
// called; these two tests cover whether live.go's two call sites actually
// call them at the right moment.
// ---------------------------------------------------------------------------

// TestLiveView_WatchForUnexpectedDeath_GenuineBrowserDeath_StopsCaptureSession
// proves the watchForUnexpectedDeath -> cs.Stop() wire (live.go, wave-plan
// W2-A item 5: "also on browser_status-relevant lifecycle: browser death ->
// stop session"). mgr.browserAlive("s1") is made to report false (genuinely
// dead) simply by never populating mgr.sessions at all — browserAlive's own
// implementation treats "no sessionEntry for this id" as not-alive, which is
// exactly the "whole browsing context is gone" case this trigger targets
// (as opposed to a mere tab close/switch, which watchForUnexpectedDeath
// deliberately leaves alone — see its doc comment).
func TestLiveView_WatchForUnexpectedDeath_GenuineBrowserDeath_StopsCaptureSession(t *testing.T) {
	// NewBrowserManager (not a bare &BrowserManager{} literal) so mgr.sessions
	// starts as an empty map, which is exactly what this test wants:
	// browserAlive("s1") reports false (no sessionEntry at all) without any
	// further setup.
	mgr, err := NewBrowserManager(BrowserConfig{}, security.NewSSRFChecker(nil))
	require.NoError(t, err)
	relay := &fakeRelay{}
	var calls int32
	cs, err := NewCaptureSessionWithDeps(mgr, "agent-death", relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	mgr.capture = cs

	lv := &LiveView{
		mgr:          mgr,
		sessionID:    "s1",
		viewers:      make(map[string]struct{}),
		statusSinks:  make(map[string]StatusSink),
		controlSinks: make(map[string]ControlSink),
		tabsSinks:    make(map[string]TabsSink),
	}
	watchedCtx, cancel := context.WithCancel(context.Background())
	lv.listenCtx = watchedCtx
	lv.stopListen = cancel

	done := make(chan struct{})
	go func() {
		lv.watchForUnexpectedDeath(watchedCtx)
		close(done)
	}()

	// Simulate the whole browsing context dying (BrowserManager.Shutdown or a
	// genuine crash) — the tab's own context (and everything derived from
	// it, including this epoch's listenCtx) dies WITHOUT a clean detach().
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchForUnexpectedDeath never returned")
	}

	require.Equal(t, 1, relay.closeCount(), "a genuine browser death must call cs.Stop(), which closes the relay")
}

// TestLiveView_OnTabsChanged_ActiveTabSwitch_TriggersCaptureSessionRecapture
// proves the onTabsChanged -> cs.Recapture() wire (live.go, wave-plan W2-A
// item 5: "recapture on active-tab switch"). lastKnownActiveCtx is
// pre-seeded to a DIFFERENT context than the manager's actual active tab, so
// this single onTabsChanged call observes activeTabChanged=true on its very
// first real comparison (mirroring a genuine second call after an initial
// baseline one). Deliberately leaves lv.listenCtx unset (hasEpochLocked()
// stays false) so onTabsChanged's separate needsRebind/rebindWatch branch is
// never reached; this test is scoped to the Recapture trigger alone.
func TestLiveView_OnTabsChanged_ActiveTabSwitch_TriggersCaptureSessionRecapture(t *testing.T) {
	tabOld, cancelOld := context.WithCancel(context.Background())
	t.Cleanup(cancelOld)
	tabNew, cancelNew := context.WithCancel(context.Background())
	t.Cleanup(cancelNew)

	mgr := &BrowserManager{
		started: true,
		sessions: map[string]*sessionEntry{
			"s1": {
				tabs:      []*tabEntry{{ctx: tabNew, cancel: cancelNew}},
				activeIdx: 0,
			},
		},
	}
	relay := &fakeRelay{}
	var calls int32
	cs, err := NewCaptureSessionWithDeps(mgr, "agent-recapture", relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	mgr.capture = cs

	lv := &LiveView{
		mgr:                mgr,
		sessionID:          "s1",
		viewers:            make(map[string]struct{}),
		statusSinks:        make(map[string]StatusSink),
		controlSinks:       make(map[string]ControlSink),
		tabsSinks:          make(map[string]TabsSink),
		lastKnownActiveCtx: tabOld,
	}

	lv.onTabsChanged(nil, 0)

	require.Equal(
		t,
		1,
		relay.recaptureCount(),
		"an active-tab switch must call cs.Recapture(), which signals the relay",
	)
}

// ---------------------------------------------------------------------------
// 2026-07-31 fix wave (post-UAT-v24 live evidence + 5-reviewer gate):
//   1. SetViewport chrome-delta compensation (the 87px HEIGHT deficit fix)
//   2. cache invalidation on a failed/degenerate SetViewport read-back
//   3. rescaleToCSSViewport's cache-miss fetch timeout + failure backoff
//   4. windowBoundsAction/layoutMetricsAction test seams (routes SetViewport
//      through lv.runCDP; makes the folded CDP calls inspectable/scriptable)
// See docs/internal/browser-viewport-input-rootcause-2026-07-31.md.
// ---------------------------------------------------------------------------

// findMouseEvent locates the *input.DispatchMouseEventParams among the
// actions captured by a scripted runCDP stub, failing the test if none (or
// more than one) is found.
func findMouseEvent(t *testing.T, actions []chromedp.Action) *input.DispatchMouseEventParams {
	t.Helper()
	var found *input.DispatchMouseEventParams
	for _, a := range actions {
		if p, ok := a.(*input.DispatchMouseEventParams); ok {
			require.Nil(t, found, "expected exactly one dispatched mouse event action")
			found = p
		}
	}
	require.NotNil(t, found, "no *input.DispatchMouseEventParams was dispatched")
	return found
}

// TestLiveView_DispatchInput_RescaleGate is the table test for item 6a: which
// LiveInput kinds dispatchInput actually routes through rescaleToCSSViewport,
// and the two degenerate-CaptureWidth/Height guards (zero-valued — an older
// client — and one-dim-present) that must dispatch unscaled rather than
// divide by zero or apply a bogus scale factor. cssViewportW/H is
// pre-populated on every case so a pointer-position kind that DOES rescale
// hits the cache (no extra runCDP call), keeping "exactly one action
// dispatched" a valid proxy for "no rescale fetch happened" across every
// case, not just the non-pointer kinds.
//
// Capture dimensions changed 2026-08-16 from 319x158 to 320x180 — an
// aspect-preserving downscale of the 1280x720 cache. The old pair had a shape
// the tab could not have, which now (correctly) costs one Page.getLayoutMetrics
// round trip to ask the tab which of the two is right, so it stopped isolating
// the property this table is about. Shape disagreement has its own tests in
// live_viewport_basis_test.go and live_viewport_resize_test.go.
func TestLiveView_DispatchInput_RescaleGate(t *testing.T) {
	const cssW, cssH = 1280.0, 720.0

	tests := []struct {
		name  string
		in    LiveInput
		check func(t *testing.T, actions []chromedp.Action)
	}{
		{
			name: "wheel rescales X/Y but leaves DeltaX/DeltaY untouched",
			in: LiveInput{
				Kind: "wheel", HasXY: true, X: 100, Y: 79,
				DeltaX: 3, DeltaY: -4,
				CaptureWidth: 320, CaptureHeight: 180,
			},
			check: func(t *testing.T, actions []chromedp.Action) {
				require.Len(t, actions, 1, "a cache hit must never trigger an extra rescale fetch")
				p := findMouseEvent(t, actions)
				require.InDelta(t, 100*cssW/320.0, p.X, 0.0001, "X must be rescaled")
				require.InDelta(t, 79*cssH/180.0, p.Y, 0.0001, "Y must be rescaled")
				require.InDelta(
					t,
					3.0,
					p.DeltaX,
					0.0001,
					"DeltaX must never be rescaled (it's a delta, not a position)",
				)
				require.InDelta(t, -4.0, p.DeltaY, 0.0001, "DeltaY must never be rescaled")
			},
		},
		{
			name: "mouse_move rescales X/Y",
			in: LiveInput{
				Kind: "mouse_move", HasXY: true, X: 100, Y: 79,
				CaptureWidth: 320, CaptureHeight: 180,
			},
			check: func(t *testing.T, actions []chromedp.Action) {
				require.Len(t, actions, 1)
				p := findMouseEvent(t, actions)
				require.InDelta(t, 100*cssW/320.0, p.X, 0.0001)
				require.InDelta(t, 79*cssH/180.0, p.Y, 0.0001)
			},
		},
		{
			name: "key_down never rescales (no coordinates to rescale)",
			in:   LiveInput{Kind: "key_down", Key: "a", Code: "KeyA", CaptureWidth: 320, CaptureHeight: 180},
			check: func(t *testing.T, actions []chromedp.Action) {
				require.Len(t, actions, 1, "key_down must never trigger a layout-metrics rescale fetch")
			},
		},
		{
			name: "text never rescales (no coordinates to rescale)",
			in:   LiveInput{Kind: "text", Text: "hi", CaptureWidth: 320, CaptureHeight: 180},
			check: func(t *testing.T, actions []chromedp.Action) {
				require.Len(t, actions, 1, "text must never trigger a layout-metrics rescale fetch")
			},
		},
		{
			name: "navigate never rescales (no coordinates to rescale)",
			in:   LiveInput{Kind: "navigate", URL: "http://8.8.8.8/", CaptureWidth: 320, CaptureHeight: 180},
			check: func(t *testing.T, actions []chromedp.Action) {
				require.Len(t, actions, 1, "navigate must never trigger a layout-metrics rescale fetch")
			},
		},
		{
			name: "zero-valued capture dims (older client) dispatch unscaled, no divide-by-zero",
			in: LiveInput{
				Kind: "mouse_move", HasXY: true, X: 42, Y: 17,
				CaptureWidth: 0, CaptureHeight: 0,
			},
			check: func(t *testing.T, actions []chromedp.Action) {
				require.Len(t, actions, 1, "zero capture dims must never trigger a rescale fetch")
				p := findMouseEvent(t, actions)
				require.InDelta(t, 42.0, p.X, 0.0001, "zero capture dims must dispatch the raw X unscaled")
				require.InDelta(t, 17.0, p.Y, 0.0001, "zero capture dims must dispatch the raw Y unscaled")
			},
		},
		{
			name: "one-dim-present (width only) dispatches unscaled",
			in: LiveInput{
				Kind: "mouse_down", HasXY: true, X: 42, Y: 17, Button: "left",
				CaptureWidth: 319, CaptureHeight: 0,
			},
			check: func(t *testing.T, actions []chromedp.Action) {
				require.Len(t, actions, 1, "one-dim-present must never trigger a rescale fetch")
				p := findMouseEvent(t, actions)
				require.InDelta(t, 42.0, p.X, 0.0001)
				require.InDelta(t, 17.0, p.Y, 0.0001)
			},
		},
		{
			name: "one-dim-present (height only) dispatches unscaled",
			in: LiveInput{
				Kind: "mouse_up", HasXY: true, X: 42, Y: 17, Button: "left",
				CaptureWidth: 0, CaptureHeight: 158,
			},
			check: func(t *testing.T, actions []chromedp.Action) {
				require.Len(t, actions, 1, "one-dim-present must never trigger a rescale fetch")
				p := findMouseEvent(t, actions)
				require.InDelta(t, 42.0, p.X, 0.0001)
				require.InDelta(t, 17.0, p.Y, 0.0001)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var actions []chromedp.Action
			lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, acts ...chromedp.Action) error {
				mu.Lock()
				actions = append(actions, acts...)
				mu.Unlock()
				return nil
			})
			lv.cssViewportW = int(cssW)
			lv.cssViewportH = int(cssH)
			require.True(t, lv.takeControl("viewerA"))

			err := lv.dispatchInput("viewerA", tc.in)
			require.NoError(t, err)

			mu.Lock()
			defer mu.Unlock()
			tc.check(t, actions)
		})
	}
}

// newViewportTestLiveView builds a bare LiveView + LiveViewRegistry pair for
// SetViewport's orchestration tests: tabCtx is a plain context.Background()
// (never dialed — SetViewport's own bounds validation happens before any CDP
// call, and every CDP call itself goes through the scripted runCDP stub, so
// no real chromedp/Chromium connection is needed).
func newViewportTestLiveView(
	runCDP func(ctx context.Context, timeout time.Duration, actions ...chromedp.Action) error,
) (*LiveViewRegistry, *LiveView) {
	lv := &LiveView{
		sessionID: "s1",
		viewers:   make(map[string]struct{}),
		tabCtx:    context.Background(),
		runCDP:    runCDP,
	}
	reg := &LiveViewRegistry{views: map[string]*LiveView{"s1": lv}}
	return reg, lv
}

// TestLiveViewRegistry_SetViewport_Orchestration is the table test for
// SetViewport's full orchestration — the happy path, the chrome-delta
// compensation re-apply triggered by an over-tolerance SHORTFALL (live
// evidence, UAT v24: requested 615x744, actual 615x657 — an 87px HEIGHT
// deficit), and the two cache-invalidation paths (hard read-back failure,
// degenerate read-back).
//
// Rewritten 2026-08-16 to script runCDP by the TYPE of the action rather than
// by the position of the call. Position-scripting encoded two things that are
// no longer true: that the window resize and the device-scale override share
// one call (they are now separately budgeted — see viewportScaleTimeout), and
// that the read-back is a single read (it is now a settle poll — see
// settleCSSViewport). Type-scripting states what each call MEANS, so it
// survives changes in how many of them there are.
func TestLiveViewRegistry_SetViewport_Orchestration(t *testing.T) {
	t.Run("happy path: cache equals the actual read-back, no compensation", func(t *testing.T) {
		var bounds []windowBoundsAction
		var readBacks int
		runCDP := func(_ context.Context, timeout time.Duration, actions ...chromedp.Action) error {
			switch a := actions[0].(type) {
			case windowBoundsAction:
				require.Equal(t, viewportSetTimeout, timeout)
				require.Len(t, actions, 1,
					"the window resize must be its own call, never bundled with the renderer-bound scale override")
				bounds = append(bounds, a)
			case layoutMetricsAction:
				readBacks++
				*a.w, *a.h = 615, 744
			}
			return nil
		}
		reg, lv := newViewportTestLiveView(runCDP)
		lv.cssViewportW, lv.cssViewportH = 999, 999 // stale — must be overwritten, not merely left alone

		applied, err := reg.SetViewport("s1", 615, 744, 1)
		require.NoError(t, err)
		require.True(t, applied)
		require.Len(t, bounds, 1, "an on-target read-back must not trigger compensation")
		require.Equal(t, 1, readBacks, "a read-back already on target must settle on the first poll")

		lv.mu.Lock()
		defer lv.mu.Unlock()
		require.Equal(t, 615, lv.cssViewportW)
		require.Equal(t, 744, lv.cssViewportH)
	})

	t.Run("shortfall over tolerance triggers exactly one compensation and caches the final read-back", func(t *testing.T) {
		var bounds []windowBoundsAction
		runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
			switch a := actions[0].(type) {
			case windowBoundsAction:
				bounds = append(bounds, a)
			case layoutMetricsAction:
				// Before compensation the tab is 87px short (the live UAT v24
				// evidence); after it, the request is met exactly.
				if len(bounds) < 2 {
					*a.w, *a.h = 615, 657
				} else {
					*a.w, *a.h = 615, 744
				}
			}
			return nil
		}
		reg, lv := newViewportTestLiveView(runCDP)

		applied, err := reg.SetViewport("s1", 615, 744, 1)
		require.NoError(t, err)
		require.True(t, applied)
		require.Len(t, bounds, 2,
			"a shortfall over tolerance must trigger exactly one compensation re-apply, never a loop")

		require.Equal(t, 615, bounds[1].width,
			"width was already exact, so its correction must be zero — not a negative one")
		require.Equal(t, 744+87, bounds[1].height,
			"compensated height = requested + shortfall = 744 + (744-657)")

		lv.mu.Lock()
		defer lv.mu.Unlock()
		require.Equal(t, 615, lv.cssViewportW, "cache must hold the FINAL (post-compensation) read-back")
		require.Equal(t, 744, lv.cssViewportH)
	})

	t.Run("read-back failure invalidates a stale cache instead of leaving it", func(t *testing.T) {
		simulatedErr := fmt.Errorf("simulated GetLayoutMetrics failure")
		runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
			if _, ok := actions[0].(layoutMetricsAction); ok {
				return simulatedErr
			}
			return nil
		}
		reg, lv := newViewportTestLiveView(runCDP)
		lv.cssViewportW, lv.cssViewportH = 1280, 720 // pre-existing, stale cache

		applied, err := reg.SetViewport("s1", 615, 744, 1)
		require.NoError(t, err, "a read-back failure is best-effort — SetViewport itself must not error")
		require.True(t, applied, "the window resize itself already succeeded")

		lv.mu.Lock()
		defer lv.mu.Unlock()
		require.Equal(t, 0, lv.cssViewportW, "a failed read-back must invalidate the cache, not leave the stale value")
		require.Equal(t, 0, lv.cssViewportH)
	})

	t.Run("zero-valued read-back invalidates a stale cache", func(t *testing.T) {
		runCDP := func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
			if lm, ok := actions[0].(layoutMetricsAction); ok {
				*lm.w, *lm.h = 0, 0 // degenerate — e.g. cssLayoutViewport was nil
			}
			return nil
		}
		reg, lv := newViewportTestLiveView(runCDP)
		lv.cssViewportW, lv.cssViewportH = 1280, 720

		applied, err := reg.SetViewport("s1", 615, 744, 1)
		require.NoError(t, err)
		require.True(t, applied)

		lv.mu.Lock()
		defer lv.mu.Unlock()
		require.Equal(t, 0, lv.cssViewportW, "a degenerate read-back must invalidate the cache")
		require.Equal(t, 0, lv.cssViewportH)
	})
}

// TestLiveView_RescaleToCSSViewport is the branch test for item 6c: cache
// hit (no CDP call at all), cache miss + fetch success (cache populated,
// coordinates scaled), and cache miss + fetch failure (unscaled coordinates,
// cache stays empty, and the failure backoff suppresses an immediate retry).
func TestLiveView_RescaleToCSSViewport(t *testing.T) {
	t.Run("cache hit never calls runCDP", func(t *testing.T) {
		var calls int
		lv := &LiveView{
			sessionID:    "s1",
			cssViewportW: 1280,
			cssViewportH: 720,
			runCDP: func(context.Context, time.Duration, ...chromedp.Action) error {
				calls++
				return nil
			},
		}
		// The capture is an aspect-preserving downscale of the cached viewport
		// (320x180 of 1280x720), which is the ordinary case: the encoder shrinks
		// the stream under load but still depicts the same surface. Changed from
		// the original 319x158 on 2026-08-16 — those numbers describe a capture
		// whose SHAPE differs from the tab's, which now (correctly) costs one
		// round trip to ask the tab which of the two is right, so they no longer
		// isolate the property this subtest is about. The shape-disagreement
		// behaviour has its own tests in live_viewport_basis_test.go and
		// live_viewport_resize_test.go.
		x, y, ok := lv.rescaleToCSSViewport(context.Background(), 100, 79, 320, 180)
		require.True(t, ok, "a cache hit must be mappable")
		require.InDelta(t, 100*1280.0/320.0, x, 0.0001)
		require.InDelta(t, 79*720.0/180.0, y, 0.0001)
		require.Zero(t, calls, "a cache hit must never call runCDP")
	})

	t.Run("cache miss + fetch success populates the cache and scales", func(t *testing.T) {
		var calls int
		lv := &LiveView{
			sessionID: "s1",
			runCDP: func(_ context.Context, timeout time.Duration, actions ...chromedp.Action) error {
				calls++
				require.Equal(t, viewportInputFetchTimeout, timeout,
					"the input-path fetch must use the short viewportInputFetchTimeout, not viewportSetTimeout")
				lm, ok := actions[0].(layoutMetricsAction)
				require.True(t, ok, "runCDP action must be a layoutMetricsAction")
				*lm.w, *lm.h = 1280, 720
				return nil
			},
		}
		// Aspect-preserving capture, for the same reason as the subtest above.
		x, y, ok := lv.rescaleToCSSViewport(context.Background(), 100, 79, 320, 180)
		require.True(t, ok, "a successful fetch must be mappable")
		require.InDelta(t, 100*1280.0/320.0, x, 0.0001)
		require.InDelta(t, 79*720.0/180.0, y, 0.0001)
		require.Equal(t, 1, calls, "one fetch, and no probe — the capture's shape agrees with the tab's")

		lv.mu.Lock()
		defer lv.mu.Unlock()
		require.Equal(t, 1280, lv.cssViewportW)
		require.Equal(t, 720, lv.cssViewportH)
	})

	// Rewritten 2026-08-03. This previously asserted that a failed fetch
	// "dispatches unscaled", on the reasoning that a slightly-off click beats a
	// dead panel. Measurement disproved the premise: the capture frame and the
	// CSS viewport differed by 562 vs 369 px, so an unscaled coordinate lands
	// ~34% off — reliably on the WRONG element. A mis-aimed click can navigate
	// away, delete or submit; a dropped one is a no-op the user retries.
	t.Run("cache miss + fetch failure DROPS the event and backs off the next call", func(t *testing.T) {
		var calls int
		lv := &LiveView{
			sessionID: "s1",
			runCDP: func(context.Context, time.Duration, ...chromedp.Action) error {
				calls++
				return fmt.Errorf("simulated CDP hiccup")
			},
		}
		_, _, ok := lv.rescaleToCSSViewport(context.Background(), 100, 79, 319, 158)
		require.False(t, ok, "a failed fetch must report the event as UNMAPPABLE, not dispatch it unscaled")
		require.Equal(t, 1, calls)

		lv.mu.Lock()
		require.Equal(t, 0, lv.cssViewportW, "cache must stay empty after a failed fetch")
		require.Equal(t, 0, lv.cssViewportH)
		lv.mu.Unlock()

		// Immediately call again — the backoff window must suppress a retry.
		_, _, ok2 := lv.rescaleToCSSViewport(context.Background(), 50, 30, 319, 158)
		require.False(t, ok2, "inside the backoff window the event must still be dropped, not dispatched unscaled")
		require.Equal(t, 1, calls, "a second call within the backoff window must not re-invoke runCDP")
	})
}
