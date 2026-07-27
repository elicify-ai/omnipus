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

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	tabs0, active0, err := mgr.ListTabs(defaultSessionID)
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
	tabs, active, err := mgr.ListTabs(defaultSessionID)
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

	tab0Ctx, err := mgr.Session(defaultSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab0Ctx, chromedp.Navigate(srv.URL)))

	tab1, err := mgr.OpenTab(defaultSessionID)
	require.NoError(t, err,
		"OpenTab must open a second tab in the SAME running browser, not try to launch a second Chromium")
	assert.Equal(t, 1, tab1.Index)
	assert.True(t, tab1.Active)

	tabs, activeIdx, err := mgr.ListTabs(defaultSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2, "OpenTab must result in exactly 2 tabs in the SAME browsing context")
	assert.Equal(t, 1, activeIdx)

	// Tab 1 (now active) must be independently usable — navigate + read the
	// resulting title.
	tab1Ctx, err := mgr.Session(defaultSessionID)
	require.NoError(t, err)
	require.NotEqual(t, tab0Ctx, tab1Ctx, "tab 0 and tab 1 must be distinct chromedp contexts")
	var title string
	require.NoError(t, chromedp.Run(tab1Ctx, chromedp.Navigate(srv.URL), chromedp.Title(&title)),
		"tab 1 must be able to navigate — it is a real, independently-usable tab in the running browser")
	assert.Equal(t, "Contact", title)

	// Switch back to tab 0 and confirm IT is STILL independently usable too
	// (proves both tabs stayed alive in the same browser this whole time,
	// rather than tab 1's creation having torn down and replaced tab 0).
	_, err = mgr.SwitchTab(defaultSessionID, 0)
	require.NoError(t, err)
	tab0CtxAgain, err := mgr.Session(defaultSessionID)
	require.NoError(t, err)
	assert.Equal(t, tab0Ctx, tab0CtxAgain, "Session must follow SwitchTab back to the original tab 0 context")
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

	_, err := mgr.OpenTab(defaultSessionID)
	require.NoError(t, err, "opening a second tab must succeed")

	tabsBefore, _, err := mgr.ListTabs(defaultSessionID)
	require.NoError(t, err)
	require.Len(t, tabsBefore, 2, "sanity: two tabs open before closing tab 0")

	// Close tab 0 specifically — NOT the active/last tab; tab 1 survives.
	closedTabs, activeIdx, err := mgr.CloseTab(defaultSessionID, 0)
	require.NoError(t, err, "closing tab 0 must succeed and must NOT kill the browser")
	require.Len(t, closedTabs, 1, "one tab remains after closing tab 0 out of 2")
	assert.Equal(t, 0, activeIdx, "the surviving tab slides into index 0 and becomes active")

	// The surviving tab (the one that WAS tab 1, now shifted to index 0)
	// must still be independently usable — navigate and read text via raw
	// chromedp — proving the browser (and this tab) are genuinely still
	// alive, not orphaned/dead.
	survivorCtx, err := mgr.Session(defaultSessionID)
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
	// DefaultSessionID plumbing still works end-to-end post-close.
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

	tab0Ctx, err := mgr.Session(defaultSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab0Ctx, chromedp.Navigate(srv.URL)))

	_, err = mgr.OpenTab(defaultSessionID)
	require.NoError(t, err)
	tab1Ctx, err := mgr.Session(defaultSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab1Ctx, chromedp.Navigate(srv.URL+"/booked")))

	require.NotEqual(t, tab0Ctx, tab1Ctx, "tab 0 and tab 1 must be distinct chromedp contexts")

	_, err = mgr.SwitchTab(defaultSessionID, 0)
	require.NoError(t, err)
	followedCtx, err := mgr.Session(defaultSessionID)
	require.NoError(t, err)
	assert.Equal(t, tab0Ctx, followedCtx, "Session(default) must follow SwitchTab back to tab 0")
	var title string
	require.NoError(t, chromedp.Run(followedCtx, chromedp.Title(&title)))
	assert.Equal(t, "Contact", title, "tab 0 must still show its OWN page (Contact), not tab 1's (Booked)")

	_, err = mgr.SwitchTab(defaultSessionID, 1)
	require.NoError(t, err)
	followedCtx2, err := mgr.Session(defaultSessionID)
	require.NoError(t, err)
	assert.Equal(t, tab1Ctx, followedCtx2, "Session(default) must follow SwitchTab to tab 1")
	var title2 string
	require.NoError(t, chromedp.Run(followedCtx2, chromedp.Title(&title2)))
	assert.Equal(t, "Booked", title2, "tab 1 must retain ITS own navigation state independent of tab 0")
}

// TestCloseTab_RealChromium_ActiveTabClose_LiveViewFollowsSurvivorNoFalseDeath
// is the real-Chromium regression guard for the live-UAT fix ("closing the
// ACTIVE tab fires a false 'session ended' banner and leaves the screencast
// frozen on stale content", confirmed 2/2 by two independent live testers
// WITH A VIEWER ATTACHED). It attaches the real ADR-038 live-view engine
// (mgr.Live().Attach — the same path pkg/gateway/browser_ws.go drives) to
// the active tab, closes it, and proves against a REAL CDP screencast that
// (a) no false "session ended" status ever reaches the viewer, and (b) the
// screencast is genuinely re-bound to the surviving tab — proven by
// navigating the survivor and observing a FRESH frame arrive, which is only
// possible if a real, working screencast is bound to that live tab (the
// pre-fix code left the live view with no active epoch at all after the
// false-death broadcast, so no further frame would ever arrive).
func TestCloseTab_RealChromium_ActiveTabClose_LiveViewFollowsSurvivorNoFalseDeath(t *testing.T) {
	skipIfNoBrowser(t)

	srv := targetBlankServer(t)
	cfg := testBrowserCfg(t)
	_, mgr := newPermissiveRegistry(t, cfg)

	tab0Ctx, err := mgr.Session(defaultSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tab0Ctx, chromedp.Navigate(srv.URL)))

	tab1, err := mgr.OpenTab(defaultSessionID)
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
	frameCh := make(chan struct{}, 1)
	onFrame := func(LiveFrame) {
		select {
		case frameCh <- struct{}{}:
		default:
		}
	}

	controlledByOther, err := mgr.Live().Attach(defaultSessionID, "viewer1", onFrame, onStatus, nil, nil)
	require.NoError(t, err)
	require.False(t, controlledByOther)
	t.Cleanup(func() { mgr.Live().Detach(defaultSessionID, "viewer1") })

	// Wait for a real screencast frame so we know the live view is genuinely
	// bound and streaming from tab 1 (the active tab) before closing it.
	select {
	case <-frameCh:
	case <-time.After(10 * time.Second):
		t.Fatal("never received an initial screencast frame from the active tab")
	}
	// Drain any further already-queued frames so the post-close check below
	// only observes a frame that arrives AFTER the close.
	for drained := true; drained; {
		select {
		case <-frameCh:
		default:
			drained = false
		}
	}

	// Close the ACTIVE tab (index 1) — the exact live-UAT repro. Tab 0
	// survives and becomes active.
	closedTabs, activeIdx, err := mgr.CloseTab(defaultSessionID, 1)
	require.NoError(t, err)
	require.Len(t, closedTabs, 1)
	require.Equal(t, 0, activeIdx)

	// Navigate the surviving tab — this can only produce a delivered
	// screencast frame if the live view's screencast is genuinely re-bound
	// to it (a real, working CDP subscription on a live, open target). Under
	// the pre-fix bug, the false-death broadcast left the live view with no
	// active epoch at all, so no frame would ever arrive here.
	survivorCtx, err := mgr.Session(defaultSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(survivorCtx, chromedp.Navigate(srv.URL+"/booked")))

	select {
	case <-frameCh:
	case <-time.After(10 * time.Second):
		t.Fatal("live view did not deliver a frame for the survivor's post-close navigation — the " +
			"screencast is not genuinely bound to the surviving tab (stuck on stale/closed-tab content, " +
			"or torn down entirely by a false death broadcast)")
	}

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

	tabs, activeIdx, err := mgr.ListTabs(defaultSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2, "browser_open_tab must ADD a tab, not reuse the current one")
	assert.Equal(t, 1, activeIdx, "the new tab must become active")
	assert.True(t, tabs[1].Active)

	// Tab 0's content must be untouched — proves this genuinely opened a
	// SECOND tab rather than navigating the existing one away.
	_, err = mgr.SwitchTab(defaultSessionID, 0)
	require.NoError(t, err)
	tab0Ctx, err := mgr.Session(defaultSessionID)
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
	assert.True(t, strings.Contains(openData["url"].(string), "/booked"))

	tabs, activeIdx, err := mgr.ListTabs(defaultSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	assert.True(t, strings.Contains(tabs[activeIdx].URL, "/booked"))

	// The new tab is genuinely usable — read its content via the actual
	// browser_get_text TOOL path (proves DefaultSessionID plumbing follows
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
// MaxTabs nor leaves a half-opened tab behind. Does not need skipIfNoBrowser:
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

	tabs, _, err := mgr.ListTabs(defaultSessionID)
	require.NoError(t, err)
	assert.Empty(t, tabs, "a blocked url must not consume a tab — no browsing context should exist yet")
}
