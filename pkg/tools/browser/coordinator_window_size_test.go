package browser

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"
)

// TestChromeLaunchFlags_WindowSizePinnedToAgentWindowSize is the REVERT-PROOF
// test for D17 after ADR-075 FR-031.
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
				"ADR-075 FR-031 removed the per-agent CreateTarget path)",
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
