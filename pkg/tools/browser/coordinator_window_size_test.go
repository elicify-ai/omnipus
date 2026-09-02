package browser

// coordinator_window_size_test.go — regression coverage for D17
// (fix/uat-v0.1.1-defects): the live-browser panel rendered at an
// inconsistent size across sessions — sometimes filling the panel,
// sometimes shrinking to a small letterboxed rectangle — because the
// OS window an agent's tab lived in had no explicit size, so it fell back
// to Chrome's own version/platform-dependent new-window default instead of
// the screencast's fixed cap (live.go's agentWindowWidth/agentWindowHeight).
// Which size an agent got depended only on "which agent/session happened to
// create its window first" — no visible trigger from the user's perspective.
//
// WHERE THE PIN LIVES NOW, and why this file was rewritten. D17 originally
// fixed a SECOND window-creation path: BrowserCoordinator.Register built each
// agent its own CDP browser context plus that context's own window via
// target.CreateTarget(...).WithNewWindow(true), and the launch-time
// --window-size flag only ever sizes the FIRST window Chrome opens at process
// start. So Register had to pass Width/Height itself, and the revert-proof
// test captured those outgoing CreateTargetParams through a
// createTargetParamsForTest seam in coordinator.go.
//
// ADR-072 FR-031 deleted the per-agent CDP browser context — every session now
// bootstraps into Chrome's DEFAULT context, and isolation moved down to one
// Chrome process and one --user-data-dir per workspace. The per-agent window,
// the CreateTarget call and the seam went with it. Tabs are now created only by
// chromedp.NewContext (manager.go's bootstrapBrowserCtx/createTab), and Chrome
// sizes those from --window-size, which in headless is also the virtual screen
// a window can never exceed.
//
// That leaves exactly ONE place the size is set — the launch flag — and one
// invariant worth guarding: that flag and live.go's agentWindowWidth/Height
// must stay in lockstep. Both files say so in prose; nothing enforced it. The
// old unit test could not be repointed at Register (there is nothing there to
// observe any more), so it is replaced by a test of the surviving mechanism.
//
// Two tests cover this:
//
//   - TestChromeLaunchFlags_WindowSizePinnedToAgentWindowSize is the
//     REVERT-PROOF unit test: hermetic, no Chrome, no network. It reads the
//     real launch flags and asserts --window-size is present exactly once and
//     carries exactly agentWindowWidth,agentWindowHeight. It fails if either
//     number is changed without the other, and if the flag is dropped.
//   - TestCoordinator_Register_NewAgentWindow_MatchesAgentWindowSize is the
//     live-Chrome end-to-end test (needs a real Chrome, drives two separate
//     agents) verifying the ACTUAL resulting behavior via the real CDP
//     Browser.getWindowForTarget call — one of the two verification methods
//     the RCA named. It is the evidence that the launch flag really does reach
//     every agent's window now that no second creation path exists.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"
)

// TestChromeLaunchFlags_WindowSizePinnedToAgentWindowSize is the REVERT-PROOF
// test for D17 after ADR-072 FR-031.
//
// It is deliberately an assertion about the launch flags rather than about a
// running Chrome: on the Chrome builds this project runs against, headless
// new-window sizing already happens to land on plausible values with no flag
// at all, so a live window-bounds assertion cannot by itself distinguish
// pinned from unpinned code (that is the sibling test's documented limitation
// too). Reading the flag the launcher actually passes can.
//
// It fails on all three ways the pin can regress: the flag deleted, the flag's
// numbers edited away from the screencast cap, or agentWindowWidth/Height
// bumped in live.go without the flag following (the 2026-08-03 1280x720 ->
// 2560x1440 change had to touch both files by hand).
func TestChromeLaunchFlags_WindowSizePinnedToAgentWindowSize(t *testing.T) {
	const flagPrefix = "--window-size="

	var found []string
	for _, arg := range chromeHardeningBaseFlags() {
		if strings.HasPrefix(arg, flagPrefix) {
			found = append(found, arg)
		}
	}

	if len(found) != 1 {
		t.Fatalf(
			"chromeHardeningBaseFlags(): found %d %s flags %q, want exactly 1 "+
				"(D17 regression — window size is pinned ONLY here now that "+
				"ADR-072 FR-031 removed the per-agent CreateTarget path)",
			len(found), flagPrefix, found,
		)
	}

	want := fmt.Sprintf("%s%d,%d", flagPrefix, agentWindowWidth, agentWindowHeight)
	if found[0] != want {
		t.Errorf(
			"chromeHardeningBaseFlags(): window-size flag = %q, want %q "+
				"(D17 regression — exec_resolver.go's --window-size and live.go's "+
				"agentWindowWidth/agentWindowHeight must stay in lockstep: the flag "+
				"is headless Chrome's virtual SCREEN, and a window can never exceed it)",
			found[0], want,
		)
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

// TestCoordinator_Register_NewAgentWindow_MatchesAgentWindowSize is the
// live-Chrome end-to-end companion to the unit test above: it forces two
// separate agents to open their first real tab (mirroring exactly how
// every production browser tool call gets its context, via
// BrowserManager.Session) and asserts BOTH agents' windows are exactly
// 1280x720 via the real CDP Browser.getWindowForTarget call. On the Chrome
// build available in this environment this passes both before and after
// the fix (see file doc comment) — it is real evidence of correct
// end-to-end behavior, not the revert-proof (that is the unit test above).
func TestCoordinator_Register_NewAgentWindow_MatchesAgentWindowSize(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)
	t.Cleanup(coord.Shutdown)

	mgrA := newTestManager(t, cfg)
	mgrA.AttachSharedChrome(coord, browserTestKey("agent-window-a"))
	mgrB := newTestManager(t, cfg)
	mgrB.AttachSharedChrome(coord, browserTestKey("agent-window-b"))

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
		if b.Width != agentWindowWidth || b.Height != agentWindowHeight {
			t.Errorf(
				"%s: window bounds = %dx%d, want %dx%d (D17 regression — new-window size not pinned to the screencast cap)",
				name,
				b.Width,
				b.Height,
				agentWindowWidth,
				agentWindowHeight,
			)
		}
	}
}
