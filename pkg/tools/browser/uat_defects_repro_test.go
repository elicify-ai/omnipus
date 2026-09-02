package browser

// uat_defects_repro_test.go — live reproduction harness for the 2026-08-26
// live-UAT report on browser_open_tab / browser_switch_tab / browser_close_tab
// (backend-lead investigation, release/v0.1.1). Drives the REAL LLM-facing
// Tool.Execute() code paths (OpenTabTool/SwitchTabTool/CloseTabTool/
// ListTabsTool) against a REAL headless Chrome through the SAME
// BrowserCoordinator/BrowserManager wiring pkg/agent/loop.go's
// registerSharedTools uses in production (ADR-043 shared-Chrome mode) — not
// the fakeTabFactory unit doubles in tabs_test.go, which cannot exercise a
// real CDP session dying.
//
// Investigation result: none of the reported symptoms reproduced against a
// REAL Chrome session through the coordinator-mode path (see the backend-lead
// report). Kept as regression coverage against exactly this symptom class
// (real-CDP session survival across open/switch/close, tab-content identity
// after a switch, close-last-tab replacement, and live-view interaction) —
// gated by skipIfNoBrowser/OMNIPUS_BROWSER_E2E like the rest of this
// package's real-Chrome suite, so it costs nothing in the default CI run.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// newUATReproManager builds a BrowserManager wired to a fresh, dedicated
// BrowserCoordinator exactly the way registerSharedTools does per agent in
// production — the mode the real live-UAT chat session actually ran in.
func newUATReproManager(t *testing.T, agentID string) (*BrowserManager, *BrowserCoordinator) {
	t.Helper()
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg, 30)
	t.Cleanup(coord.Shutdown)

	registry := tools.NewToolRegistry()
	mgr, err := RegisterTools(registry, cfg, security.NewSSRFChecker(nil), true, home, false)
	require.NoError(t, err)
	mgr.AttachSharedChrome(coord, agentID)
	return mgr, coord
}

func decodeToolJSON(t *testing.T, res *tools.ToolResult) map[string]any {
	t.Helper()
	require.NotNil(t, res)
	require.False(t, res.IsError, "tool returned an error result: %s", res.ForLLM)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &out), "tool result was not JSON: %s", res.ForLLM)
	return out
}

// TestRepro_Defect1_OpenTab_SessionSurvives reproduces (or refutes) Defect 1:
// "browser_open_tab reports success:true while killing the whole browser
// session." Opens a tab via the real tool, then proves the session is still
// alive by driving browser_navigate-equivalent chromedp calls through
// mgr.Session(defaultSessionID) and calling browser_list_tabs again — exactly
// what the UAT's "subsequent calls to other browser tools" symptom checks.
func TestRepro_Defect1_OpenTab_SessionSurvives(t *testing.T) {
	skipIfNoBrowser(t)
	mgr, _ := newUATReproManager(t, "agent-defect1")

	// Establish tab 0 first (mirrors a real chat session's first browser_*
	// call, e.g. browser_navigate) so OpenTab hits the "append" branch, not
	// the lazy-create branch.
	firstCtx, err := mgr.Session(defaultSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(firstCtx, chromedp.Navigate("about:blank")))

	openTool := &OpenTabTool{mgr: mgr}
	res := openTool.Execute(context.Background(), map[string]any{})
	out := decodeToolJSON(t, res)
	require.Equal(t, true, out["success"], "browser_open_tab must report success:true only when it really succeeded")
	t.Logf("browser_open_tab result: %+v", out)

	// Defect 1 claims the WHOLE session dies as a side effect. Prove or
	// refute it the same way a subsequent agent tool call would: resolve the
	// session again and drive real CDP through it.
	sessCtx, err := mgr.Session(defaultSessionID)
	require.NoError(t, err, "browser session must still resolve after browser_open_tab")
	var title string
	err = chromedp.Run(sessCtx, chromedp.Navigate("about:blank"), chromedp.Title(&title))
	require.NoError(t, err, "a subsequent browser tool call must still work after browser_open_tab — this is Defect 1's exact symptom")

	listTool := &ListTabsTool{mgr: mgr}
	res2 := listTool.Execute(context.Background(), map[string]any{})
	out2 := decodeToolJSON(t, res2)
	require.Equal(t, true, out2["browser_started"])
	tabsList, ok := out2["tabs"].([]any)
	require.True(t, ok)
	require.Len(t, tabsList, 2, "expected 2 tabs open after one browser_open_tab call")
}

// TestRepro_Defect2_SwitchAndClose_NoActiveSessionOrMismatch reproduces (or
// refutes) Defect 2's two symptoms: "no active session" errors from
// browser_switch_tab/browser_close_tab, and returned content not matching the
// tab actually switched to.
func TestRepro_Defect2_SwitchAndClose_NoActiveSessionOrMismatch(t *testing.T) {
	skipIfNoBrowser(t)
	mgr, _ := newUATReproManager(t, "agent-defect2")

	// Tab 0: about:blank (title "").
	tab0Ctx, err := mgr.Session(defaultSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab0Ctx, chromedp.Navigate("about:blank")))

	// Tab 1: opened via the real tool, given a DISTINCT, identifiable page so
	// a content mismatch after switching is unambiguous.
	openTool := &OpenTabTool{mgr: mgr}
	openRes := openTool.Execute(context.Background(), map[string]any{})
	openOut := decodeToolJSON(t, openRes)
	require.Equal(t, true, openOut["success"])

	tab1Ctx, err := mgr.Session(defaultSessionID) // now resolves the NEW active tab (tab 1)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab1Ctx,
		chromedp.Navigate("data:text/html,<title>TAB-ONE-MARKER</title><h1>tab one</h1>")))

	// Switch back to tab 0 via the real LLM-facing tool.
	switchTool := &SwitchTabTool{mgr: mgr}
	switchRes := switchTool.Execute(context.Background(), map[string]any{"index": float64(0)})
	require.False(t, switchRes.IsError, "browser_switch_tab reported an error (possible 'no active session'): %s", switchRes.ForLLM)
	switchOut := decodeToolJSON(t, switchRes)
	t.Logf("browser_switch_tab(0) result: %+v", switchOut)
	require.NotEqual(t, "TAB-ONE-MARKER", switchOut["title"],
		"content-mismatch defect: browser_switch_tab(0) returned tab 1's content")

	// The manager's own Session() must now resolve tab 0's real content too —
	// not just the tool's self-reported title/url.
	activeCtx, err := mgr.Session(defaultSessionID)
	require.NoError(t, err, "no active session after browser_switch_tab — reproduces Defect 2")
	var liveTitle string
	require.NoError(t, chromedp.Run(activeCtx, chromedp.Title(&liveTitle)))
	require.NotEqual(t, "TAB-ONE-MARKER", liveTitle,
		"content-mismatch defect: Session() still resolves tab 1's context after switching to tab 0")

	// Close tab 1 (the non-active tab) via the real LLM-facing tool.
	closeTool := &CloseTabTool{mgr: mgr}
	closeRes := closeTool.Execute(context.Background(), map[string]any{"index": float64(1)})
	require.False(t, closeRes.IsError, "browser_close_tab reported an error (possible 'no active session'): %s", closeRes.ForLLM)
	closeOut := decodeToolJSON(t, closeRes)
	t.Logf("browser_close_tab(1) result: %+v", closeOut)

	// Session must still resolve after the close.
	_, err = mgr.Session(defaultSessionID)
	require.NoError(t, err, "no active session after browser_close_tab — reproduces Defect 2")
}

// TestRepro_Defect2_CloseLastTab_ReplacementBehavior verifies
// CloseTabTool's own doc-comment claim: "The last remaining tab is never
// left closed — a fresh blank tab opens in its place instead." The UAT
// report claims this does NOT actually happen.
func TestRepro_Defect2_CloseLastTab_ReplacementBehavior(t *testing.T) {
	skipIfNoBrowser(t)
	mgr, _ := newUATReproManager(t, "agent-defect2-lasttab")

	tab0Ctx, err := mgr.Session(defaultSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab0Ctx, chromedp.Navigate("about:blank")))

	closeTool := &CloseTabTool{mgr: mgr}
	res := closeTool.Execute(context.Background(), map[string]any{"index": float64(0)})
	require.False(t, res.IsError, "browser_close_tab(last tab) errored: %s", res.ForLLM)
	out := decodeToolJSON(t, res)
	t.Logf("browser_close_tab(0) [last tab] result: %+v", out)

	tabsList, ok := out["tabs"].([]any)
	require.True(t, ok)
	if len(tabsList) == 0 {
		t.Fatalf("DEFECT CONFIRMED: closing the last remaining tab left ZERO tabs — doc comment promises a fresh blank replacement")
	}
	require.Len(t, tabsList, 1, "expected exactly one fresh blank replacement tab")

	// And the manager must still consider a session resolvable afterward.
	_, err = mgr.Session(defaultSessionID)
	require.NoError(t, err, "no active session after closing the last remaining tab")
}

// TestRepro_Defect1And2_WithLiveViewerAttached repeats the open/switch/close
// sequence with a REAL live-view viewer attached (LiveViewRegistry.Attach) —
// the "real product UI in a real browser" angle of the UAT: watching the live
// browser panel while the agent drives tab tools. Checks for the same three
// symptoms via the live-view callback path (onStatus firing = the session
// died unexpectedly; onTabs snapshots = what the panel would show) in
// addition to the tool-level checks the other tests already cover.
func TestRepro_Defect1And2_WithLiveViewerAttached(t *testing.T) {
	skipIfNoBrowser(t)
	mgr, _ := newUATReproManager(t, "agent-liveview")

	tab0Ctx, err := mgr.Session(defaultSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab0Ctx, chromedp.Navigate("about:blank")))

	var mu sync.Mutex
	var statusEvents []string
	var tabsSnapshots [][]Tab

	controlledByOther, err := mgr.Live().Attach(
		defaultSessionID, "viewer-uat",
		func(msg string) {
			mu.Lock()
			defer mu.Unlock()
			statusEvents = append(statusEvents, msg)
		},
		nil,
		func(tabs []Tab, activeIdx int) {
			mu.Lock()
			defer mu.Unlock()
			snap := make([]Tab, len(tabs))
			copy(snap, tabs)
			tabsSnapshots = append(tabsSnapshots, snap)
			_ = activeIdx
		},
	)
	require.NoError(t, err)
	require.False(t, controlledByOther)
	defer mgr.Live().Detach(defaultSessionID, "viewer-uat")

	// A plain (non-control-taking) viewer must NOT block the agent's own tab
	// tools via controlledResult — only an explicit TakeControl would.
	openTool := &OpenTabTool{mgr: mgr}
	openRes := openTool.Execute(context.Background(), map[string]any{})
	require.False(t, openRes.IsError, "browser_open_tab errored with a live viewer attached: %s", openRes.ForLLM)
	openOut := decodeToolJSON(t, openRes)
	require.Equal(t, true, openOut["success"])

	newTabCtx, err := mgr.Session(defaultSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(newTabCtx,
		chromedp.Navigate("data:text/html,<title>TAB-ONE-MARKER</title>")))

	switchTool := &SwitchTabTool{mgr: mgr}
	switchRes := switchTool.Execute(context.Background(), map[string]any{"index": float64(0)})
	require.False(t, switchRes.IsError, "browser_switch_tab errored with a live viewer attached: %s", switchRes.ForLLM)

	closeTool := &CloseTabTool{mgr: mgr}
	closeRes := closeTool.Execute(context.Background(), map[string]any{"index": float64(1)})
	require.False(t, closeRes.IsError, "browser_close_tab errored with a live viewer attached: %s", closeRes.ForLLM)

	// Give the async onTabs/onStatus callbacks a moment to land.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(tabsSnapshots) >= 2
	}, 5*time.Second, 50*time.Millisecond, "expected at least the initial + open-tab onTabs snapshots")

	mu.Lock()
	defer mu.Unlock()
	t.Logf("status events: %v", statusEvents)
	for _, ev := range statusEvents {
		require.NotContains(t, ev, "died", "live view reported the session died unexpectedly during tab ops: %s", ev)
	}
	// Every snapshot's active tab must exist in that same snapshot — a
	// snapshot claiming an active index into a different tab set than what
	// it reports IS the "content mismatch" defect surfacing through the live
	// view path.
	for i, snap := range tabsSnapshots {
		foundActive := false
		for _, tab := range snap {
			if tab.Active {
				foundActive = true
			}
		}
		require.True(t, foundActive, "onTabs snapshot #%d has no active tab: %+v", i, snap)
	}

	_, err = mgr.Session(defaultSessionID)
	require.NoError(t, err, "no active session after tab ops with a live viewer attached")
}
