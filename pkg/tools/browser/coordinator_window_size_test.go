package browser

// coordinator_window_size_test.go — regression coverage for D17
// (fix/uat-v0.1.1-defects): the live-browser panel rendered at an
// inconsistent size across sessions — sometimes filling the panel,
// sometimes shrinking to a small letterboxed rectangle — because the
// per-agent OS window BrowserCoordinator.Register creates via
// target.CreateTarget(...).WithNewWindow(true) had no explicit
// Width/Height, so it fell back to Chrome's own version/platform-dependent
// new-window default instead of the screencast's fixed cap (live.go's
// screencastMaxWidth/screencastMaxHeight, 1280x720). That size was then
// cached for the agent's whole lifetime (c.contexts), so which size an
// agent got depended only on "which agent/session happened to create its
// window first" — no visible trigger from the user's perspective.
//
// Two tests cover this:
//
//   - TestCoordinator_Register_CreateTargetParams_PinsWindowToScreencastCap
//     is the REVERT-PROOF unit test: it uses the createTargetParamsForTest
//     seam (coordinator.go) to capture the actual outgoing
//     target.CreateTargetParams and assert Width/Height are set, with no
//     live Chrome involved. This is necessary (not just convenient): on
//     the Chrome build this fix shipped from, headless new-window sizing
//     already happens to DEFAULT to 1280x720 with no Width/Height set at
//     all, so a live-Chrome window-bounds assertion cannot by itself
//     distinguish fixed from unfixed code — see the sibling test below.
//   - TestCoordinator_Register_NewAgentWindow_MatchesScreencastCap is a
//     live-Chrome smoke test (needs a real Chrome, forces two separate
//     agent windows) verifying the ACTUAL resulting behavior via the real
//     CDP Browser.getWindowForTarget call — one of the two verification
//     methods the RCA named. It passes both before and after this fix in
//     this environment (see its own doc comment), so it is not itself
//     revert-proof, but it is real end-to-end evidence the fix does not
//     break window creation and that 1280x720 is what agents actually get.

import (
	"context"
	"testing"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// TestCoordinator_Register_CreateTargetParams_PinsWindowToScreencastCap is
// the REVERT-PROOF test for D17. It hooks createTargetParamsForTest
// (coordinator.go) to capture the target.CreateTargetParams Register
// builds for the per-agent window, before it is sent over CDP — no live
// Chrome, no network, fully hermetic. Pre-fix, CreateTarget's Width/Height
// fields are simply never set (zero value), so this assertion fails
// deterministically regardless of what any particular Chrome
// build/platform happens to default an unset size to.
func TestCoordinator_Register_CreateTargetParams_PinsWindowToScreencastCap(t *testing.T) {
	var captured []*target.CreateTargetParams
	prev := createTargetParamsForTest
	createTargetParamsForTest = func(p *target.CreateTargetParams) {
		captured = append(captured, p)
	}
	t.Cleanup(func() { createTargetParamsForTest = prev })

	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg, 30)
	t.Cleanup(coord.Shutdown)

	mgrA := newTestManager(t, cfg)
	mgrA.AttachSharedChrome(coord, "agent-params-a")
	mgrB := newTestManager(t, cfg)
	mgrB.AttachSharedChrome(coord, "agent-params-b")

	if _, _, err := coord.Register(context.Background(), "agent-params-a", mgrA); err != nil {
		t.Fatalf("Register A: %v", err)
	}
	if _, _, err := coord.Register(context.Background(), "agent-params-b", mgrB); err != nil {
		t.Fatalf("Register B: %v", err)
	}

	if len(captured) != 2 {
		t.Fatalf("expected 2 captured CreateTargetParams (one per agent), got %d", len(captured))
	}
	for i, p := range captured {
		if !p.NewWindow {
			t.Errorf(
				"captured params[%d]: NewWindow = false, want true (precondition for Width/Height per cdproto doc comment)",
				i,
			)
		}
		if p.Width != screencastMaxWidth {
			t.Errorf("captured params[%d]: Width = %d, want %d (D17 regression)", i, p.Width, screencastMaxWidth)
		}
		if p.Height != screencastMaxHeight {
			t.Errorf("captured params[%d]: Height = %d, want %d (D17 regression)", i, p.Height, screencastMaxHeight)
		}
	}
}

// windowBoundsForSession resolves the on-screen window bounds for the
// window hosting the already-attached tab context tabCtx (as returned by
// BrowserManager.Session — the same context every real browser tool call
// runs against). browser.GetWindowForTarget() with no explicit TargetID
// resolves it "as part of session" (cdproto's own doc comment on
// WithTargetID) — running it via chromedp.Run(tabCtx, ...) executes
// through that tab's own, fully-attached CDP session, avoiding the
// "No web contents in the target" (-32000) error a bare/ad-hoc executor
// hit against a freshly-created, not-yet-fully-attached target during
// development of this test.
func windowBoundsForSession(t *testing.T, tabCtx context.Context) *browser.Bounds {
	t.Helper()

	var bounds *browser.Bounds
	err := chromedp.Run(tabCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, b, gerr := browser.GetWindowForTarget().Do(ctx)
		if gerr != nil {
			return gerr
		}
		bounds = b
		return nil
	}))
	if err != nil {
		t.Fatalf("browser.GetWindowForTarget: %v", err)
	}
	if bounds == nil {
		t.Fatal("browser.GetWindowForTarget returned nil Bounds")
	}
	return bounds
}

// TestCoordinator_Register_NewAgentWindow_MatchesScreencastCap is the
// live-Chrome end-to-end companion to the unit test above: it forces two
// separate agents to open their first real tab (mirroring exactly how
// every production browser tool call gets its context, via
// BrowserManager.Session) and asserts BOTH agents' windows are exactly
// 1280x720 via the real CDP Browser.getWindowForTarget call. On the Chrome
// build available in this environment this passes both before and after
// the fix (see file doc comment) — it is real evidence of correct
// end-to-end behavior, not the revert-proof (that is the unit test above).
func TestCoordinator_Register_NewAgentWindow_MatchesScreencastCap(t *testing.T) {
	if testing.Short() {
		t.Skip("needs a real Chrome")
	}
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg, 30)
	t.Cleanup(coord.Shutdown)

	mgrA := newTestManager(t, cfg)
	mgrA.AttachSharedChrome(coord, "agent-window-a")
	mgrB := newTestManager(t, cfg)
	mgrB.AttachSharedChrome(coord, "agent-window-b")

	// Session (not a bare Register call) is what every real browser tool
	// call goes through: it drives ensureStarted -> coordinator.Register ->
	// createFirstTab, producing a fully chromedp-attached tab context in
	// the SAME per-agent window Register creates.
	tabA, err := mgrA.Session("default")
	if err != nil {
		t.Fatalf("agent-window-a Session: %v", err)
	}
	tabB, err := mgrB.Session("default")
	if err != nil {
		t.Fatalf("agent-window-b Session: %v", err)
	}

	boundsA := windowBoundsForSession(t, tabA)
	boundsB := windowBoundsForSession(t, tabB)

	for name, b := range map[string]*browser.Bounds{"agent-window-a": boundsA, "agent-window-b": boundsB} {
		if b.Width != screencastMaxWidth || b.Height != screencastMaxHeight {
			t.Errorf(
				"%s: window bounds = %dx%d, want %dx%d (D17 regression — new-window size not pinned to the screencast cap)",
				name,
				b.Width,
				b.Height,
				screencastMaxWidth,
				screencastMaxHeight,
			)
		}
	}
}
