// Real-Chromium end-to-end test for ADR-041 tab adoption: a browser_click on a
// target="_blank" link must open a NEW tab, adopt it, switch to it, and report
// it back to the caller. This is the exact failure the ADR set out to fix (the
// Cal.com booking button that opens a new tab the tools never followed).
//
// Gated by skipIfNoBrowser so the suite stays green where no working Chromium is
// present. Uses a local httptest fixture (SSRF allows 127.0.0.1) so it is
// deterministic and needs no external network.

package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sameChromedpContext reports whether two chromedp session contexts are the
// SAME context value, compared by interface identity rather than by deep
// structural equality.
//
// Do NOT go back to testify's Equal/NotEqual on these. Those route through
// reflect.DeepEqual, which walks the whole context chain down into chromedp's
// *Target — whose fields are mutated concurrently by chromedp's own event
// goroutines (Target.Execute cancels a per-call sub-context, and
// context.cancelCtx.cancel does an atomic.AddInt32 on it). Under -race that is
// a genuine data race between the assertion goroutine and the browser event
// loop, and it is exactly what the #615 race-gate widening surfaced the first
// time this package was ever run under -race:
//
//	WARNING: DATA RACE
//	Read at ... reflect.Value.Int() <- reflect.deepValueEqual <- require.NotEqual
//	  tab_adoption_e2e_test.go:146
//	Previous write at ... sync/atomic.AddInt32 <- context.(*cancelCtx).cancel
//	  <- chromedp.(*Target).Execute.func1 <- chromedp.runListeners
//
// Identity is also what these assertions actually mean — "the same tab's
// context" or "a distinct tab's context". Structural equality of a live
// context is not a stable property in the first place.
func sameChromedpContext(a, b context.Context) bool { return a == b }

// targetBlankServer serves a page whose only link opens a second page in a NEW
// tab via target="_blank" — the canonical "book a slot →" shape.
func targetBlankServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Contact</title></head>` +
			`<body><h1>Contact</h1>` +
			`<a id="book" class="btn-primary" href="/booked" target="_blank" rel="noopener">Pick a 30-min slot &rarr;</a>` +
			`</body></html>`))
	})
	mux.HandleFunc("/booked", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Booked</title></head>` +
			`<body><h1 id="sched">Scheduling</h1></body></html>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestExecute_TargetBlankClick_AdoptsNewTab is the ADR-041 headline acceptance
// test against real Chromium: navigate → click a target="_blank" link → the new
// tab is adopted, becomes active, and browser_click reports it; browser_list_tabs
// shows both tabs with the booked page active.
func TestExecute_TargetBlankClick_AdoptsNewTab(t *testing.T) {
	skipIfNoBrowser(t)

	srv := targetBlankServer(t)
	cfg := testBrowserCfg(t)
	registry, mgr := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	// 1. Land on the opener page.
	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.NotNil(t, navRes)
	require.False(t, navRes.IsError, "navigate must succeed; got: %s", navRes.ForLLM)

	// Sanity: exactly one tab before the click.
	tabs0, active0, err := mgr.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabs0, 1, "exactly one tab before the target=_blank click")
	require.Equal(t, 0, active0)

	// 2. Click the target="_blank" link — this must open + adopt a new tab.
	click := mustGetTool(t, registry, "browser_click")
	clickRes := click.Execute(ctx, map[string]any{"selector": "#book"})
	require.NotNil(t, clickRes)
	require.False(t, clickRes.IsError, "click must succeed; got: %s", clickRes.ForLLM)
	clickData := decodeJSON(t, clickRes.ForLLM)

	// browser_click must REPORT the new tab (ADR-041 D2/D3 reporting).
	assert.Equal(t, true, clickData["opened_new_tab"],
		"click on a target=_blank link must report opened_new_tab=true; got: %s", clickRes.ForLLM)
	if url, ok := clickData["new_tab_url"].(string); ok {
		assert.True(t, strings.Contains(url, "/booked"),
			"new_tab_url should point at the booked page; got %q", url)
	}

	// 3. The tab set now has TWO tabs and the booked page is active.
	tabs, active, err := mgr.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2, "the adopted target=_blank tab must be in the tab set")
	require.GreaterOrEqual(t, active, 0)
	require.Less(t, active, len(tabs))
	assert.True(t, tabs[active].Active)
	assert.True(t, strings.Contains(tabs[active].URL, "/booked"),
		"the active tab must be the newly-opened booked page; active tab URL=%q, tabs=%+v",
		tabs[active].URL, tabs)

	// 4. browser_list_tabs (the agent-facing tool) reflects the same two tabs.
	listTool := mustGetTool(t, registry, "browser_list_tabs")
	listRes := listTool.Execute(ctx, map[string]any{})
	require.NotNil(t, listRes)
	require.False(t, listRes.IsError, "browser_list_tabs must succeed; got: %s", listRes.ForLLM)
	assert.True(t, strings.Contains(listRes.ForLLM, "/booked"),
		"browser_list_tabs must include the booked tab; got: %s", listRes.ForLLM)
}

// TestOpenTab_RealChromium_SecondTabSharesSameBrowser is the browserCtx
// lifetime fix's second real-Chromium regression guard: OpenTab must add a
// SECOND tab to the SAME running browser, not try to launch a second
// Chromium process. Before the fix, OpenTab's "append an additional tab"
// branch reused m.allocCtx for the 2nd+ tab — chromedp treats a fresh
// context off the raw allocator as "launch a brand new browser" (see
// createTab's and sessionEntry.browserCtx's doc comments in manager.go), and
// with the managed-mode fixed debug port already held by the first browser,
// that second launch failed outright with "chrome failed to start".
//
// Both tabs must be independently usable (navigate + read distinguishing
// content) — proving they are two live tabs of ONE browser, not one tab
// replacing/hiding the other.
func TestOpenTab_RealChromium_SecondTabSharesSameBrowser(t *testing.T) {
	skipIfNoBrowser(t)

	srv := targetBlankServer(t)
	cfg := testBrowserCfg(t)
	_, mgr := newPermissiveRegistry(t, cfg)

	tab0Ctx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab0Ctx, chromedp.Navigate(srv.URL)))

	tab1, err := mgr.OpenTab(testSessionID)
	require.NoError(t, err,
		"OpenTab must open a second tab in the SAME running browser, not try to launch a second Chromium")
	assert.Equal(t, 1, tab1.Index)
	assert.True(t, tab1.Active)

	tabs, activeIdx, err := mgr.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2, "OpenTab must result in exactly 2 tabs in the SAME browsing context")
	assert.Equal(t, 1, activeIdx)

	// Tab 1 (now active) must be independently usable — navigate + read the
	// resulting title.
	tab1Ctx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	require.False(t, sameChromedpContext(tab0Ctx, tab1Ctx), "tab 0 and tab 1 must be distinct chromedp contexts")
	var title string
	require.NoError(t, chromedp.Run(tab1Ctx, chromedp.Navigate(srv.URL), chromedp.Title(&title)),
		"tab 1 must be able to navigate — it is a real, independently-usable tab in the running browser")
	assert.Equal(t, "Contact", title)

	// Switch back to tab 0 and confirm IT is STILL independently usable too
	// (proves both tabs stayed alive in the same browser this whole time,
	// rather than tab 1's creation having torn down and replaced tab 0).
	_, err = mgr.SwitchTab(testSessionID, 0)
	require.NoError(t, err)
	tab0CtxAgain, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	assert.True(t, sameChromedpContext(tab0Ctx, tab0CtxAgain), "Session must follow SwitchTab back to the original tab 0 context")
	var heading string
	require.NoError(t, chromedp.Run(tab0CtxAgain, chromedp.Text("h1", &heading, chromedp.ByQuery)))
	assert.Equal(t, "Contact", heading)
}

// TestCloseTab_RealChromium_ClosingTab0KeepsBrowserAndSurvivorAlive is the
// browserCtx lifetime fix's core real-Chromium regression guard: closing tab
// 0 out of 2+ open tabs must NOT tear down the browser or the other tab.
//
// Before the fix, tab 0 (the first tab created for a browsing context) was
// itself the chromedp context chromedp binds the running *Browser to (its
// "c.first" target) — canceling THAT context is what chromedp.Cancel's own
// doc comment calls "graceful[ly] clos[ing]" the whole browser. So closing
// tab 0 specifically (as opposed to any other tab) used to kill the ENTIRE
// Chromium process, taking every sibling tab down with it. The browserCtx
// design fixes this by making EVERY user tab — tab 0 included — a
// non-"first" child of a dedicated, owner-only browser-owning context, so no
// single tab's close can ever take the browser down.
func TestCloseTab_RealChromium_ClosingTab0KeepsBrowserAndSurvivorAlive(t *testing.T) {
	skipIfNoBrowser(t)

	srv := targetBlankServer(t)
	cfg := testBrowserCfg(t)
	registry, mgr := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.NotNil(t, navRes)
	require.False(t, navRes.IsError, "navigate must succeed; got: %s", navRes.ForLLM)

	_, err := mgr.OpenTab(testSessionID)
	require.NoError(t, err, "opening a second tab must succeed")

	tabsBefore, _, err := mgr.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabsBefore, 2, "sanity: two tabs open before closing tab 0")

	// Close tab 0 specifically — NOT the active/last tab; tab 1 survives.
	closedTabs, activeIdx, err := mgr.CloseTab(testSessionID, 0)
	require.NoError(t, err, "closing tab 0 must succeed and must NOT kill the browser")
	require.Len(t, closedTabs, 1, "one tab remains after closing tab 0 out of 2")
	assert.Equal(t, 0, activeIdx, "the surviving tab slides into index 0 and becomes active")

	// The surviving tab (the one that WAS tab 1, now shifted to index 0)
	// must still be independently usable — navigate and read text via raw
	// chromedp — proving the browser (and this tab) are genuinely still
	// alive, not orphaned/dead.
	survivorCtx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	var title string
	require.NoError(
		t,
		chromedp.Run(survivorCtx, chromedp.Navigate(srv.URL+"/booked"), chromedp.Title(&title)),
		"the surviving tab must still be able to navigate after tab 0 was closed — the browser must not have died",
	)
	assert.Equal(t, "Booked", title)
	var heading string
	require.NoError(t, chromedp.Run(survivorCtx, chromedp.Text("#sched", &heading, chromedp.ByQuery)))
	assert.Equal(t, "Scheduling", heading)

	// And via the actual browser_get_text TOOL path too (not just raw
	// chromedp against the manually-resolved ctx) — proves the manager's
	// testSessionID plumbing still works end-to-end post-close.
	getText := mustGetTool(t, registry, "browser_get_text")
	getTextRes := getText.Execute(ctx, map[string]any{"selector": "#sched"})
	require.NotNil(t, getTextRes)
	require.False(t, getTextRes.IsError, "browser_get_text must still work on the survivor; got: %s", getTextRes.ForLLM)
	data := decodeJSON(t, getTextRes.ForLLM)
	assert.Equal(t, "Scheduling", data["text"])
}

// TestSwitchTab_RealChromium_SessionFollowsActiveTab confirms Session(default)
// follows whichever tab is active across a real SwitchTab call, and that
// each tab retains its OWN independent navigation state — the two tabs are
// genuinely separate, live pages in the same browser, not one tab's state
// leaking into the other.
func TestSwitchTab_RealChromium_SessionFollowsActiveTab(t *testing.T) {
	skipIfNoBrowser(t)

	srv := targetBlankServer(t)
	cfg := testBrowserCfg(t)
	_, mgr := newPermissiveRegistry(t, cfg)

	tab0Ctx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab0Ctx, chromedp.Navigate(srv.URL)))

	_, err = mgr.OpenTab(testSessionID)
	require.NoError(t, err)
	tab1Ctx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab1Ctx, chromedp.Navigate(srv.URL+"/booked")))

	require.False(t, sameChromedpContext(tab0Ctx, tab1Ctx), "tab 0 and tab 1 must be distinct chromedp contexts")

	_, err = mgr.SwitchTab(testSessionID, 0)
	require.NoError(t, err)
	followedCtx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	assert.True(t, sameChromedpContext(tab0Ctx, followedCtx), "Session(default) must follow SwitchTab back to tab 0")
	var title string
	require.NoError(t, chromedp.Run(followedCtx, chromedp.Title(&title)))
	assert.Equal(t, "Contact", title, "tab 0 must still show its OWN page (Contact), not tab 1's (Booked)")

	_, err = mgr.SwitchTab(testSessionID, 1)
	require.NoError(t, err)
	followedCtx2, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	assert.True(t, sameChromedpContext(tab1Ctx, followedCtx2), "Session(default) must follow SwitchTab to tab 1")
	var title2 string
	require.NoError(t, chromedp.Run(followedCtx2, chromedp.Title(&title2)))
	assert.Equal(t, "Booked", title2, "tab 1 must retain ITS own navigation state independent of tab 0")
}

// TestCloseTab_RealChromium_ActiveTabClose_LiveViewFollowsSurvivorNoFalseDeath
// is the real-Chromium regression guard for the live-UAT fix ("closing the
// ACTIVE tab fires a false 'session ended' banner and leaves the live view
// stuck on stale content", confirmed 2/2 by two independent live testers
// WITH A VIEWER ATTACHED). It attaches the real ADR-038 live-view engine
// (mgr.Live().Attach — the same path pkg/gateway/browser_ws.go drives) to
// the active tab, closes it, and proves against REAL Chromium tab-context
// resolution that (a) no false "session ended" status ever reaches the
// viewer, and (b) the live view's tabCtx is genuinely re-bound to the
// surviving tab's REAL chromedp context (ADR-061: video is carried
// exclusively by WebRTC now, so there is no screencast frame left to prove
// this via — the live view's own tabCtx/listenCtx bookkeeping, compared
// against mgr.Session()'s real post-close resolution, is the mechanism that
// actually needs to be right).
func TestCloseTab_RealChromium_ActiveTabClose_LiveViewFollowsSurvivorNoFalseDeath(t *testing.T) {
	skipIfNoBrowser(t)

	srv := targetBlankServer(t)
	cfg := testBrowserCfg(t)
	_, mgr := newPermissiveRegistry(t, cfg)

	tab0Ctx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab0Ctx, chromedp.Navigate(srv.URL)))

	tab1, err := mgr.OpenTab(testSessionID)
	require.NoError(t, err)
	require.Equal(t, 1, tab1.Index)
	require.True(t, tab1.Active)

	var statusMu sync.Mutex
	var statusMsgs []string
	onStatus := func(msg string) {
		statusMu.Lock()
		statusMsgs = append(statusMsgs, msg)
		statusMu.Unlock()
	}

	controlledByOther, err := mgr.Live().Attach(testSessionID, "viewer1", onStatus, nil, nil)
	require.NoError(t, err)
	require.False(t, controlledByOther)
	t.Cleanup(func() { mgr.Live().Detach(testSessionID, "viewer1") })

	tab1Ctx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	lv := mgr.Live().view(testSessionID)
	lv.mu.Lock()
	require.True(t, sameChromedpContext(tab1Ctx, lv.tabCtx),
		"sanity: the live view must be bound to tab 1 (the active tab) before the close")
	lv.mu.Unlock()

	// Close the ACTIVE tab (index 1) — the exact live-UAT repro. Tab 0
	// survives and becomes active.
	closedTabs, activeIdx, err := mgr.CloseTab(testSessionID, 1)
	require.NoError(t, err)
	require.Len(t, closedTabs, 1)
	require.Equal(t, 0, activeIdx)

	survivorCtx, err := mgr.Session(testSessionID)
	require.NoError(t, err)

	// The live view must rebind to the REAL surviving tab context — not stay
	// stuck on the closed tab, and not go dead (nil listenCtx).
	require.Eventually(t, func() bool {
		lv.mu.Lock()
		defer lv.mu.Unlock()
		return lv.listenCtx != nil && sameChromedpContext(survivorCtx, lv.tabCtx)
	}, 5*time.Second, 10*time.Millisecond,
		"the live view must rebind to the surviving tab's real chromedp context after the active tab "+
			"closes — it must not stay bound to the closed tab or go dead")

	statusMu.Lock()
	got := append([]string(nil), statusMsgs...)
	statusMu.Unlock()
	assert.Empty(t, got,
		"closing the ACTIVE tab (browser and survivor alive) must never emit a false 'session ended' "+
			"status to an attached viewer: %v", got)
}

// TestExecute_OpenTab_RealChromium_OpensBlankTab is the real-Chromium
// acceptance test for browser_open_tab's no-url form: it must open a NEW
// tab (not reuse the current one, unlike browser_navigate), make it active,
// and leave the original tab's content untouched.
func TestExecute_OpenTab_RealChromium_OpensBlankTab(t *testing.T) {
	skipIfNoBrowser(t)

	srv := targetBlankServer(t)
	cfg := testBrowserCfg(t)
	registry, mgr := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.NotNil(t, navRes)
	require.False(t, navRes.IsError, "navigate must succeed; got: %s", navRes.ForLLM)

	openTab := mustGetTool(t, registry, "browser_open_tab")
	openRes := openTab.Execute(ctx, map[string]any{})
	require.NotNil(t, openRes)
	require.False(t, openRes.IsError, "browser_open_tab (no url) must succeed; got: %s", openRes.ForLLM)
	openData := decodeJSON(t, openRes.ForLLM)
	assert.Equal(t, true, openData["success"])
	assert.EqualValues(t, 1, openData["active_index"], "the new tab must be tab index 1")

	tabs, activeIdx, err := mgr.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2, "browser_open_tab must ADD a tab, not reuse the current one")
	assert.Equal(t, 1, activeIdx, "the new tab must become active")
	assert.True(t, tabs[1].Active)

	// Tab 0's content must be untouched — proves this genuinely opened a
	// SECOND tab rather than navigating the existing one away.
	_, err = mgr.SwitchTab(testSessionID, 0)
	require.NoError(t, err)
	tab0Ctx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	var heading string
	require.NoError(t, chromedp.Run(tab0Ctx, chromedp.Text("h1", &heading, chromedp.ByQuery)))
	assert.Equal(
		t,
		"Contact",
		heading,
		"tab 0 must still show its own page — browser_open_tab must not have navigated it away",
	)
}

// TestExecute_OpenTab_RealChromium_NavigatesToURL is the real-Chromium
// acceptance test for browser_open_tab{url}: the new tab must actually load
// the given URL and report its title/url back, exactly like browser_navigate
// does for the current tab.
func TestExecute_OpenTab_RealChromium_NavigatesToURL(t *testing.T) {
	skipIfNoBrowser(t)

	srv := targetBlankServer(t)
	cfg := testBrowserCfg(t)
	registry, mgr := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.NotNil(t, navRes)
	require.False(t, navRes.IsError, "navigate must succeed; got: %s", navRes.ForLLM)

	openTab := mustGetTool(t, registry, "browser_open_tab")
	openRes := openTab.Execute(ctx, map[string]any{"url": srv.URL + "/booked"})
	require.NotNil(t, openRes)
	require.False(t, openRes.IsError, "browser_open_tab{url} must succeed; got: %s", openRes.ForLLM)
	openData := decodeJSON(t, openRes.ForLLM)
	assert.Equal(t, "Booked", openData["title"])
	openURL, ok := openData["url"].(string)
	require.True(t, ok, "openData[\"url\"] must be a string, got %T", openData["url"])
	assert.True(t, strings.Contains(openURL, "/booked"))

	tabs, activeIdx, err := mgr.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	assert.True(t, strings.Contains(tabs[activeIdx].URL, "/booked"))

	// The new tab is genuinely usable — read its content via the actual
	// browser_get_text TOOL path (proves testSessionID plumbing follows
	// the newly-opened, newly-active tab end to end).
	getText := mustGetTool(t, registry, "browser_get_text")
	getTextRes := getText.Execute(ctx, map[string]any{"selector": "#sched"})
	require.NotNil(t, getTextRes)
	require.False(t, getTextRes.IsError, "browser_get_text must work on the new tab; got: %s", getTextRes.ForLLM)
	data := decodeJSON(t, getTextRes.ForLLM)
	assert.Equal(t, "Scheduling", data["text"])
}

// TestExecute_OpenTab_SSRFBlockedURL_NoTabConsumed proves a blocked url is
// rejected BEFORE any tab is opened — exactly like browser_navigate's own
// SSRF gate — so a malicious/blocked target never wastes a slot against
// the memory gate nor leaves a half-opened tab behind. Does not need skipIfNoBrowser:
// ValidateURL runs before BrowserManager.OpenTab is ever called, so this
// never touches chromedp (mirrors TestExecute_Navigate_SchemeBlocks's own
// no-browser-needed rationale).
func TestExecute_OpenTab_SSRFBlockedURL_NoTabConsumed(t *testing.T) {
	cfg := testBrowserCfg(t)
	registry, mgr := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	openTab := mustGetTool(t, registry, "browser_open_tab")
	openRes := openTab.Execute(ctx, map[string]any{"url": "file:///etc/passwd"})
	require.NotNil(t, openRes)
	assert.True(t, openRes.IsError, "a blocked scheme must error, not silently no-op")
	assert.Contains(t, openRes.ForLLM, "blocked")

	tabs, _, err := mgr.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Empty(t, tabs, "a blocked url must not consume a tab — no browsing context should exist yet")
}

// realChromePageTargetIDs queries Chrome DIRECTLY via CDP's Target.getTargets
// (chromedp.Targets, the same primitive ReconcileTabs uses in manager.go) and
// returns the TargetIDs of every "page"-type target the browser ACTUALLY has
// open right now — ground truth, independent of this package's own se.tabs
// bookkeeping. ctx may be any live tab's chromedp context; Targets queries the
// whole browser the context is attached to, not just that one tab (see
// chromedp.Targets' doc comment: "lists all the targets in the browser
// attached to the given context").
func realChromePageTargetIDs(t *testing.T, ctx context.Context) map[target.ID]bool {
	t.Helper()
	infos, err := chromedp.Targets(ctx)
	require.NoError(t, err, "querying Chrome's real target list via CDP must succeed")
	ids := make(map[target.ID]bool, len(infos))
	for _, info := range infos {
		if info == nil || info.Type != "page" {
			continue
		}
		ids[info.TargetID] = true
	}
	return ids
}

// waitUntilChromeTargetGone polls Chrome's real page-target list until id is
// gone or the budget expires. CloseTab's cancel() returns before Chrome has
// necessarily dropped the target from Target.getTargets.
func waitUntilChromeTargetGone(t *testing.T, ctx context.Context, id target.ID, budget time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(budget)
	for {
		if !realChromePageTargetIDs(t, ctx)[id] {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// chromeTargetIDOf extracts the real CDP TargetID a chromedp tab context is
// bound to — the same identifier Chrome itself uses in Target.getTargets —
// so a test can assert on Chrome's OWN notion of "which target is this",
// rather than only on this package's tabEntry.targetID bookkeeping.
func chromeTargetIDOf(t *testing.T, ctx context.Context) target.ID {
	t.Helper()
	cc := chromedp.FromContext(ctx)
	require.NotNil(t, cc, "chromedp context must carry a *chromedp.Context")
	require.NotNil(t, cc.Target, "tab context must already be attached to a CDP target")
	require.NotEmpty(t, cc.Target.TargetID, "attached target must have a non-empty TargetID")
	return cc.Target.TargetID
}

// TestCloseTab_RealChromium_TargetGenuinelyClosedInChrome answers, WITH
// EVIDENCE rather than by reading chromedp's source, the exact question the
// operator raised: does BrowserManager.CloseTab's `closing.cancel()` actually
// tell CHROME to close the target (Target.closeTarget over CDP), or does it
// merely detach our own chromedp client and leave the page resident in the
// browser?
//
// This project has been burned before by assuming a mechanism instead of
// measuring it (ADR-061's JPEG screencast, the focus-emulation episode) — an
// `<img>` swapped fast enough looks like video, and a chromedp context that
// stops responding to OUR calls looks exactly like a closed tab from inside
// this package's own bookkeeping (se.tabs), whether or not Chrome's process
// still has the page open. The only way to tell the difference is to ask
// Chrome directly, which is what this test does: after every CloseTab call it
// enumerates Chrome's REAL "page" targets via Target.getTargets (the same CDP
// call ReconcileTabs uses) and asserts the closed tab's TargetID is actually
// gone — not merely absent from mgr.ListTabs.
//
// Exercises BOTH CloseTab code paths that call closing.cancel():
//  1. Closing one of SEVERAL open tabs (the len(se.tabs) > 1 branch).
//  2. Closing the LAST remaining tab, which is replaced via createFirstTab
//     reusing the same browserCtx (ADR-041 D3 "never leaves zero tabs") — this
//     additionally proves the replacement is a genuinely NEW real Chrome
//     target, and that the real target COUNT stays at exactly 1 (no leaked
//     ghost target sitting alongside the replacement, no zero-tab gap).
func TestCloseTab_RealChromium_TargetGenuinelyClosedInChrome(t *testing.T) {
	skipIfNoBrowser(t)

	srv := targetBlankServer(t)
	cfg := testBrowserCfg(t)
	_, mgr := newPermissiveRegistry(t, cfg)

	// --- Set up two real tabs and record Chrome's OWN target IDs for both. ---
	tab0Ctx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab0Ctx, chromedp.Navigate(srv.URL)))
	tab0ID := chromeTargetIDOf(t, tab0Ctx)

	_, err = mgr.OpenTab(testSessionID)
	require.NoError(t, err)
	tab1Ctx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab1Ctx, chromedp.Navigate(srv.URL+"/booked")))
	tab1ID := chromeTargetIDOf(t, tab1Ctx)
	require.NotEqual(t, tab0ID, tab1ID, "sanity: two distinct tabs must have two distinct real CDP TargetIDs")

	// Sanity against ground truth BEFORE closing anything: Chrome must
	// already show both real targets, and only those two.
	before := realChromePageTargetIDs(t, tab1Ctx)
	require.True(t, before[tab0ID], "sanity: Chrome must show tab0's real target before any close")
	require.True(t, before[tab1ID], "sanity: Chrome must show tab1's real target before any close")
	// NOTE: no assertion on the TOTAL target count. chromedp.Targets lists
	// every page target in the whole browser, and in ADR-043 shared-Chrome
	// mode that includes other browsing contexts (other agents' sessions, a
	// concurrently-running test's tabs). Measured here: 4 targets present
	// where this test had created 2. The question this test exists to answer
	// is about IDENTITY -- is THIS closed tab's target gone from Chrome --
	// so every assertion below is keyed on the specific TargetIDs this test
	// created, never on how many targets the shared browser happens to hold.

	// --- Case 1: close tab 0 out of 2 (the len(se.tabs) > 1 branch). ---
	closedTabs, activeIdx, err := mgr.CloseTab(testSessionID, 0)
	require.NoError(t, err)
	require.Len(t, closedTabs, 1, "one tab remains after closing tab 0 out of 2")
	require.Equal(t, 0, activeIdx)

	survivorCtx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	survivorID := chromeTargetIDOf(t, survivorCtx)
	assert.Equal(t, tab1ID, survivorID, "the surviving tab must still be Chrome's original tab1 target")

	// CloseTarget is in-flight when cancel() returns — listing immediately
	// flaked on CI (2026-08-16 #615: tab0 still present, count still 4).
	// Wait on the identity condition, never a fixed delay. Do NOT assert
	// the total target count: the comment above already records that a
	// shared Chrome can hold extra page targets, so a count that stays
	// put is not evidence the closed tab leaked.
	require.True(t, waitUntilChromeTargetGone(t, survivorCtx, tab0ID, 2*time.Second),
		"MEASURED: tab0's real CDP target (%s) must be GONE from Chrome's own target list after CloseTab — "+
			"if this is still present, CloseTab only detached our client and leaked the tab in Chrome", tab0ID)
	afterFirstClose := realChromePageTargetIDs(t, survivorCtx)
	assert.True(t, afterFirstClose[tab1ID], "the surviving tab's real target must still be present")

	// --- Case 2: close the LAST remaining tab (createFirstTab replacement). ---
	closedTabs2, activeIdx2, err := mgr.CloseTab(testSessionID, 0)
	require.NoError(t, err, "closing the last tab must succeed and produce a replacement (ADR-041 D3)")
	require.Len(t, closedTabs2, 1, "ADR-041 D3: closing the last tab must never leave zero tabs")
	require.Equal(t, 0, activeIdx2)

	replacementCtx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	replacementID := chromeTargetIDOf(t, replacementCtx)
	assert.NotEqual(t, tab1ID, replacementID,
		"the last-tab replacement must be a genuinely NEW real Chrome target, not a relabeled survivor")

	require.True(t, waitUntilChromeTargetGone(t, replacementCtx, tab1ID, 2*time.Second),
		"MEASURED: tab1's real CDP target (%s) must be GONE from Chrome's own target list after closing the "+
			"last tab — if still present, the last-tab-replacement path leaked it", tab1ID)
	afterSecondClose := realChromePageTargetIDs(t, replacementCtx)
	assert.True(t, afterSecondClose[replacementID], "the replacement tab's real target must be present")
	assert.False(t, afterSecondClose[tab0ID],
		"tab0's target must still be gone after the second close — it must not reappear")
}
