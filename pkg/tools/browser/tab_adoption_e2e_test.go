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
