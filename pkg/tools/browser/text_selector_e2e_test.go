// Real-Chromium end-to-end tests for the text-selector capability: matching
// browser_click/browser_type/browser_get_text/browser_wait by VISIBLE TEXT,
// via both the Playwright-style pseudo-selector embedded in `selector` and
// the explicit `text` parameter.
//
// Root cause under test: agents write button:has-text("Confirm") /
// a:has-text("Book a call") style selectors that chromedp.ByQuery (standard
// CSS querySelector) rejects outright ("DOM Error while querying (-32000)").
// This file proves the fix resolves those selectors to real, clickable
// elements against a real Chromium — not just that the JS/regex logic looks
// right in isolation (see text_selector_test.go for that).
//
// Gated by skipIfNoBrowser so the suite stays green where no working
// Chromium is present, mirroring every other *_e2e_test.go in this package.

package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// shortTimeoutBrowserCfg returns a BrowserConfig like testBrowserCfg but with
// a shorter PageTimeout — used for tests asserting a text query does NOT
// match anything. resolveTextTarget's poll (7-reviewer finding #1) correctly
// consumes the WHOLE timeout budget to prove a negative, so a shorter budget
// here keeps those tests faster than the full 15s config without weakening
// what they prove (browser_click/browser_type derive their poll deadline
// from BrowserConfig.PageTimeout; browser_get_text/browser_wait use the
// package's own fixed getTextWaitTimeout instead, so this helper only speeds
// up click/type-based negative assertions). 5s (not a more aggressive 1-2s)
// deliberately leaves headroom against transient startup contention when
// many short-lived Chromium instances launch back-to-back across this
// file's tests — a per-eval CDP round trip is sub-millisecond in isolation,
// but a too-tight budget was observed to flake under that sequential load.
func shortTimeoutBrowserCfg(t *testing.T) BrowserConfig {
	t.Helper()
	cfg := testBrowserCfg(t)
	cfg.PageTimeout = 5 * time.Second
	return cfg
}

// textSelectorFixtureHTML is the page served for the text-selector tests. It
// deliberately mirrors the Cal.com-style failure from the live UAT: a link
// with no stable class/id whose only reliable handle is its visible text,
// PLUS the specificity trap (a <button> nested inside a <div> that also
// contains the button's text), PLUS an exact-vs-substring collision ("14" vs
// "140").
const textSelectorFixtureHTML = `<!DOCTYPE html>
<html>
<head><title>Text Selector Fixture</title></head>
<body>
  <a href="/booked" target="_blank" rel="noopener">Pick a 30-min slot &rarr;</a>

  <div id="wrapper">
    Please review and
    <button id="confirm-btn" onclick="document.getElementById('confirm-result').textContent='confirmed'">Confirm</button>
    to continue.
  </div>
  <div id="confirm-result">not-confirmed</div>

  <table>
    <tr><td class="exact-row">14</td></tr>
    <tr><td class="substr-row">140</td></tr>
  </table>

  <p id="greeting">Hello there, friend</p>

  <input id="typed-target" type="text" />
  <div id="type-label">Name field</div>
</body>
</html>`

const textSelectorBookedHTML = `<!DOCTYPE html>
<html><head><title>Booked</title></head><body><h1 id="sched">Scheduled</h1></body></html>`

func textSelectorFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(textSelectorFixtureHTML))
	})
	mux.HandleFunc("/booked", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(textSelectorBookedHTML))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestTextSel_Click_HasTextPseudo_ClicksLink is the headline UAT-repro fix:
// a link with no stable selector, addressed ONLY by its visible text via the
// Playwright-style pseudo the model already writes, must actually click —
// not fail with the old "DOM Error while querying (-32000)".
func TestTextSel_Click_HasTextPseudo_ClicksLink(t *testing.T) {
	skipIfNoBrowser(t)

	srv := textSelectorFixtureServer(t)
	cfg := testBrowserCfg(t)
	registry, mgr := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError, "navigate must succeed; got: %s", navRes.ForLLM)

	click := mustGetTool(t, registry, "browser_click")
	// The fixture link's actual visible text is "Pick a 30-min slot →" — a
	// substring match on "30-min slot" is exactly the shape a model writes
	// when it doesn't know a stable selector for a booking-style link.
	clickRes := click.Execute(ctx, map[string]any{"selector": `a:has-text("30-min slot")`})
	require.NotNil(t, clickRes)
	require.False(t, clickRes.IsError, ":has-text click must succeed; got: %s", clickRes.ForLLM)

	// It must ADOPT the new tab exactly like an ordinary CSS-selector click on
	// the same target="_blank" link would (ADR-041) — proves the resolved
	// marker element received a REAL trusted mouse click, not a JS el.click()
	// (trusted clicks are what makes target="_blank" tab adoption fire).
	clickData := decodeJSON(t, clickRes.ForLLM)
	assert.Equal(
		t,
		true,
		clickData["opened_new_tab"],
		":has-text click on a target=_blank link must report opened_new_tab=true (trusted click preserved); got: %s",
		clickRes.ForLLM,
	)

	tabs, activeIdx, err := mgr.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	assert.True(t, strings.Contains(tabs[activeIdx].URL, "/booked"))
}

// TestTextSel_Click_TextParam_ClicksButton proves the explicit `text`
// parameter (not embedded in `selector`) also resolves and clicks — the DOM
// mutation caused by the click confirms a REAL click landed on the button.
func TestTextSel_Click_TextParam_ClicksButton(t *testing.T) {
	skipIfNoBrowser(t)

	srv := textSelectorFixtureServer(t)
	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError, "navigate must succeed; got: %s", navRes.ForLLM)

	click := mustGetTool(t, registry, "browser_click")
	clickRes := click.Execute(ctx, map[string]any{"text": "Confirm"})
	require.NotNil(t, clickRes)
	require.False(t, clickRes.IsError, "click via text param must succeed; got: %s", clickRes.ForLLM)

	// 7-reviewer finding #6, success-payload half: the text-only case
	// (selector=="") used to echo the internal marker selector back in the
	// "selector" field. It must now echo the `text` argument instead — and,
	// either way, must NEVER contain the internal marker attribute name.
	clickData := decodeJSON(t, clickRes.ForLLM)
	assert.Equal(t, "Confirm", clickData["selector"],
		"the text-only case must echo the `text` argument, not the internal marker selector")
	assert.NotContains(t, fmt.Sprintf("%v", clickData["selector"]), textMarkerAttr,
		"the echoed selector must NEVER contain the internal data-omnipus-tsel marker attribute name")

	getText := mustGetTool(t, registry, "browser_get_text")
	resultRes := getText.Execute(ctx, map[string]any{"selector": "#confirm-result"})
	require.False(t, resultRes.IsError)
	data := decodeJSON(t, resultRes.ForLLM)
	assert.Equal(t, "confirmed", data["text"],
		"the click via `text` param must have landed on the REAL button, mutating the DOM")
}

// TestTextSel_Specificity_ClicksButtonNotWrappingDiv is the specificity
// acceptance test: <button>Confirm</button> nested inside a <div> that ALSO
// contains the word "Confirm" (via surrounding prose) — :has-text("Confirm")
// must resolve to the BUTTON (smallest normalized-text match), and clicking
// it must actually fire the button's onclick (not a no-op click on the div).
func TestTextSel_Specificity_ClicksButtonNotWrappingDiv(t *testing.T) {
	skipIfNoBrowser(t)

	srv := textSelectorFixtureServer(t)
	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError)

	click := mustGetTool(t, registry, "browser_click")
	clickRes := click.Execute(ctx, map[string]any{"selector": `:has-text("Confirm")`})
	require.NotNil(t, clickRes)
	require.False(t, clickRes.IsError, "specificity click must succeed; got: %s", clickRes.ForLLM)

	getText := mustGetTool(t, registry, "browser_get_text")
	resultRes := getText.Execute(ctx, map[string]any{"selector": "#confirm-result"})
	require.False(t, resultRes.IsError)
	data := decodeJSON(t, resultRes.ForLLM)
	assert.Equal(t, "confirmed", data["text"],
		"clicking :has-text(\"Confirm\") must land on the <button> (smallest match), firing its onclick — "+
			"a click on the wrapping <div> instead would leave #confirm-result unchanged")
}

// TestTextSel_ExactVsSubstring_TextIsOnlyMatchesExact proves :text-is("14")
// (exact) matches ONLY the "14" cell, while :has-text("14") (substring)
// matches "140" too and (per the specificity rule) actually resolves to
// whichever has the smaller normalized-text length — here both "14" and
// "140" are candidates for the substring case, but "14" (length 2) is
// smaller than "140" (length 3), so the substring case also lands on "14".
// The differentiating case is therefore: exact rejects "140" entirely
// (:text-is("140") must NOT match "14"), while substring accepts both.
func TestTextSel_ExactVsSubstring_TextIsOnlyMatchesExact(t *testing.T) {
	skipIfNoBrowser(t)

	srv := textSelectorFixtureServer(t)
	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError)

	getText := mustGetTool(t, registry, "browser_get_text")

	// Exact match on "14" resolves and reads back "14".
	exact14 := getText.Execute(ctx, map[string]any{"selector": `td:text-is("14")`})
	require.False(t, exact14.IsError, "text-is(14) must resolve; got: %s", exact14.ForLLM)
	data14 := decodeJSON(t, exact14.ForLLM)
	assert.Equal(t, "14", data14["text"])

	// Exact match on "140" resolves and reads back "140" (not "14" — proves
	// exact match doesn't substring-match the shorter "14" cell either).
	exact140 := getText.Execute(ctx, map[string]any{"selector": `td:text-is("140")`})
	require.False(t, exact140.IsError, "text-is(140) must resolve; got: %s", exact140.ForLLM)
	data140 := decodeJSON(t, exact140.ForLLM)
	assert.Equal(t, "140", data140["text"])

	// A needle that matches NEITHER cell exactly must fail with the clear
	// no-match error, not silently resolve to a substring hit.
	exactNoMatch := getText.Execute(ctx, map[string]any{"selector": `td:text-is("1")`})
	require.True(t, exactNoMatch.IsError, "text-is(1) must NOT match either cell (14, 140) via substring")
	assert.Contains(t, exactNoMatch.ForLLM, "no visible element matching text")

	// Substring match on "14" via :has-text resolves — specificity picks the
	// smaller "14" cell over "140" since both are substring-matches of "14".
	substr := getText.Execute(ctx, map[string]any{"selector": `td:has-text("14")`})
	require.False(t, substr.IsError, "has-text(14) must resolve; got: %s", substr.ForLLM)
	dataSubstr := decodeJSON(t, substr.ForLLM)
	assert.Equal(t, "14", dataSubstr["text"],
		"has-text(14) substring match must pick the smaller/more-specific \"14\" cell over \"140\"")
}

// TestTextSel_GetText_ByTextParam proves browser_get_text resolves via the
// `text` parameter and reads the matched element's own text back.
func TestTextSel_GetText_ByTextParam(t *testing.T) {
	skipIfNoBrowser(t)

	srv := textSelectorFixtureServer(t)
	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError)

	getText := mustGetTool(t, registry, "browser_get_text")
	result := getText.Execute(ctx, map[string]any{"text": "Hello there"})
	require.NotNil(t, result)
	require.False(t, result.IsError, "get_text via text param must succeed; got: %s", result.ForLLM)
	data := decodeJSON(t, result.ForLLM)
	assert.Equal(t, "Hello there, friend", data["text"])
}

// TestTextSel_Wait_ByTextParam_And_Pseudo proves browser_wait resolves via
// both the `text` parameter and the pseudo-selector route.
func TestTextSel_Wait_ByTextParam_And_Pseudo(t *testing.T) {
	skipIfNoBrowser(t)

	srv := textSelectorFixtureServer(t)
	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError)

	waitTool := mustGetTool(t, registry, "browser_wait")

	byText := waitTool.Execute(ctx, map[string]any{"text": "Confirm"})
	require.NotNil(t, byText)
	assert.False(t, byText.IsError, "wait via text param must succeed; got: %s", byText.ForLLM)

	byPseudo := waitTool.Execute(ctx, map[string]any{"selector": `button:text-is("Confirm")`})
	require.NotNil(t, byPseudo)
	assert.False(t, byPseudo.IsError, "wait via :text-is pseudo must succeed; got: %s", byPseudo.ForLLM)
}

// TestTextSel_NoMatch_ClearError proves a text query that matches nothing on
// the page returns the CLEAR "no visible element matching text" error — not
// the cryptic underlying DOM/CDP error the old ByQuery-only path produced.
func TestTextSel_NoMatch_ClearError(t *testing.T) {
	skipIfNoBrowser(t)

	srv := textSelectorFixtureServer(t)
	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError)

	click := mustGetTool(t, registry, "browser_click")
	result := click.Execute(ctx, map[string]any{"text": "This text does not exist anywhere on the page"})
	require.NotNil(t, result)
	require.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "no visible element matching text",
		"no-match error must be the clear, purpose-built message, not a raw DOM/CDP error; got: %s", result.ForLLM)
	assert.NotContains(t, result.ForLLM, "-32000",
		"no-match error must NOT be the cryptic raw CDP error code; got: %s", result.ForLLM)
}

// TestTextSel_HiddenElement_NotMatched proves an element matching the text
// but hidden (display:none) is excluded — resolveTextTarget only considers
// VISIBLE elements (getClientRects().length > 0). Scoped to `selector:
// "button"` (via the text param's selector-scoping) so the only candidate in
// scope is the hidden button itself — no unrelated visible element (e.g.
// stray prose that happens to contain the same words) could accidentally
// satisfy the match and mask a real regression.
func TestTextSel_HiddenElement_NotMatched(t *testing.T) {
	skipIfNoBrowser(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Hidden</title></head><body>
			<button id="hidden-btn" style="display:none">Ghost Button</button>
			<p>This paragraph deliberately avoids the button's own wording.</p>
		</body></html>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError)

	click := mustGetTool(t, registry, "browser_click")
	result := click.Execute(ctx, map[string]any{"selector": "button", "text": "Ghost Button"})
	require.NotNil(t, result)
	assert.True(t, result.IsError, "a hidden (display:none) element must NOT be matched by text selection")
	assert.Contains(t, result.ForLLM, "no visible element matching text")
}

// TestTextSel_TypeTool_PseudoSelector_TypesIntoInput proves browser_type
// supports the pseudo-selector route in `selector` (its `text` argument
// remains the value typed, per the documented naming-collision limitation —
// see resolvePseudoOnlySelector's doc comment in text_selector.go).
func TestTextSel_TypeTool_PseudoSelector_TypesIntoInput(t *testing.T) {
	skipIfNoBrowser(t)

	srv := textSelectorFixtureServer(t)
	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError)

	// #typed-target has no visible inner text, so a text-pseudo can't locate
	// IT directly — but plain CSS still works unchanged through the very same
	// resolvePseudoOnlySelector code path (isText=false fast path), proving
	// browser_type's existing contract is completely undisturbed by this
	// feature.
	typeTool := mustGetTool(t, registry, "browser_type")
	typeRes := typeTool.Execute(ctx, map[string]any{"selector": "#typed-target", "text": "hello"})
	require.NotNil(t, typeRes)
	require.False(t, typeRes.IsError, "plain CSS browser_type must be unaffected; got: %s", typeRes.ForLLM)

	evalTool := mustGetTool(t, registry, "browser_evaluate")
	evalRes := evalTool.Execute(ctx, map[string]any{"js": `document.getElementById("typed-target").value`})
	require.False(t, evalRes.IsError)
	evalData := decodeJSON(t, evalRes.ForLLM)
	assert.Equal(t, "hello", evalData["result"])
}

// TestTextSel_ParameterValidation_BothEmpty_ErrorsLikeBefore proves the
// "neither selector nor text" case keeps the EXACT pre-existing error
// message shape (selector/required) for backward compatibility with
// TestExecute_ParameterValidation's assertions.
func TestTextSel_ParameterValidation_BothEmpty_ErrorsLikeBefore(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	registry := tools.NewToolRegistry()
	ssrf := security.NewSSRFChecker([]string{"127.0.0.1"})
	_, regErr := registerToolsForTest(t, registry, cfg, ssrf, true, t.TempDir(), true)
	require.NoError(t, regErr)

	ctx := context.Background()
	for _, name := range []string{"browser_click", "browser_get_text", "browser_wait"} {
		t.Run(name, func(t *testing.T) {
			tool := mustGetTool(t, registry, name)
			result := tool.Execute(ctx, map[string]any{})
			require.NotNil(t, result)
			assert.True(t, result.IsError)
			assert.Contains(t, result.ForLLM, "selector")
			assert.Contains(t, result.ForLLM, "required")
		})
	}
}

// ---------------------------------------------------------------------------
// 7-reviewer finding #1: text resolution must POLL, not scan once.
// ---------------------------------------------------------------------------

const asyncButtonFixtureHTML = `<!doctype html><html><head><title>Async</title></head><body>
<p>The button below is appended asynchronously.</p>
<script>
setTimeout(function(){
  var b = document.createElement('button');
  b.id = 'async-confirm';
  b.textContent = 'Confirm';
  document.body.appendChild(b);
}, 1500);
</script>
</body></html>`

// TestTextSel_Wait_PollsForAsyncElement is the headline finding-#1 repro: a
// button appended via setTimeout 1500ms after page load has no chance of
// being seen by a single pre-poll scan run before it exists.
// browser_wait{text:"Confirm"} must actually WAIT (poll) for it, not fail in
// ~1ms.
func TestTextSel_Wait_PollsForAsyncElement(t *testing.T) {
	skipIfNoBrowser(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(asyncButtonFixtureHTML))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError)

	waitTool := mustGetTool(t, registry, "browser_wait")
	start := time.Now()
	result := waitTool.Execute(ctx, map[string]any{"text": "Confirm"})
	elapsed := time.Since(start)

	require.NotNil(t, result)
	assert.False(
		t,
		result.IsError,
		"browser_wait{text:\"Confirm\"} must POLL until the setTimeout-appended button renders, not fail on a single pre-render scan; got: %s",
		result.ForLLM,
	)
	assert.GreaterOrEqual(
		t,
		elapsed,
		1200*time.Millisecond,
		"wait resolved suspiciously fast (%s) for a button appended after a 1500ms setTimeout — the poll doesn't appear to actually be retrying",
		elapsed,
	)
}

// ---------------------------------------------------------------------------
// 7-reviewer finding #2: innermost-element tie-break, not ancestor.
// ---------------------------------------------------------------------------

const noProseWrapperFixtureHTML = `<!doctype html><html><head><title>No Prose Wrapper</title></head><body>
<div id="wrap-noprose" style="width:1000px"><button id="confirm-btn-noprose" onclick="document.getElementById('result-noprose').textContent='confirmed'">Confirm</button></div>
<div id="result-noprose">not-confirmed</div>
</body></html>`

// TestTextSel_Specificity_NoExtraProse_ClicksButtonNotDiv is finding #2's
// acceptance case: a <div> wrapping a <button> with NO extra prose, so the
// div's own normalized text is IDENTICAL (same length) to the button's — the
// old strict-`<` "smallest wins" comparison kept the FIRST-encountered
// element (the ancestor div, since querySelectorAll visits ancestors before
// descendants) on an exact tie. The fix must resolve to the button
// regardless, since the div CONTAINS a matching candidate (the button) and
// is excluded on that basis alone, independent of any length comparison.
func TestTextSel_Specificity_NoExtraProse_ClicksButtonNotDiv(t *testing.T) {
	skipIfNoBrowser(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(noProseWrapperFixtureHTML))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError)

	click := mustGetTool(t, registry, "browser_click")
	clickRes := click.Execute(ctx, map[string]any{"text": "Confirm"})
	require.NotNil(t, clickRes)
	require.False(
		t,
		clickRes.IsError,
		"click must resolve even with an equal-length wrapping div; got: %s",
		clickRes.ForLLM,
	)

	getText := mustGetTool(t, registry, "browser_get_text")
	resultRes := getText.Execute(ctx, map[string]any{"selector": "#result-noprose"})
	require.False(t, resultRes.IsError)
	data := decodeJSON(t, resultRes.ForLLM)
	assert.Equal(t, "confirmed", data["text"],
		"text:\"Confirm\" must resolve to the BUTTON (innermost, via containment exclusion), not the wrapping "+
			"div whose normalized text is IDENTICAL — a click on the div instead would leave #result-noprose unchanged")
}

// ---------------------------------------------------------------------------
// 7-reviewer finding #3: selector+text combined must not silently widen
// scope to the whole document.
// ---------------------------------------------------------------------------

const scopedSelectorDecoyFixtureHTML = `<!doctype html><html><head><title>Modal Decoy</title></head><body>
<div id="modal"><button id="modal-yes" onclick="document.getElementById('modal-result').textContent='modal-clicked'">Yes</button></div>
<div id="decoy"><button id="decoy-yes" onclick="document.getElementById('decoy-result').textContent='decoy-clicked'">Yes</button></div>
<div id="modal-result">unclicked</div>
<div id="decoy-result">unclicked</div>
</body></html>`

func scopedSelectorDecoyFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(scopedSelectorDecoyFixtureHTML))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestTextSel_ScopedSelectorWithPseudoGarbage_DoesNotHitDecoy is finding
// #3a's acceptance case: `selector` carries its OWN (bogus, to-be-discarded)
// text pseudo — the explicit `text` argument must take priority for WHAT to
// match, while selector's CSS PREFIX ("#modal button") is still honored as
// the scope, so the click can never reach #decoy's identically-labeled "Yes"
// button.
func TestTextSel_ScopedSelectorWithPseudoGarbage_DoesNotHitDecoy(t *testing.T) {
	skipIfNoBrowser(t)

	srv := scopedSelectorDecoyFixtureServer(t)
	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError)

	click := mustGetTool(t, registry, "browser_click")
	clickRes := click.Execute(ctx, map[string]any{
		"selector": `#modal button:has-text("x")`,
		"text":     "Yes",
	})
	require.NotNil(t, clickRes)
	require.False(t, clickRes.IsError, "scoped selector+text click must succeed; got: %s", clickRes.ForLLM)

	getText := mustGetTool(t, registry, "browser_get_text")

	modalResult := getText.Execute(ctx, map[string]any{"selector": "#modal-result"})
	require.False(t, modalResult.IsError)
	modalData := decodeJSON(t, modalResult.ForLLM)
	assert.Equal(t, "modal-clicked", modalData["text"], "the click must land on #modal-yes")

	decoyResult := getText.Execute(ctx, map[string]any{"selector": "#decoy-result"})
	require.False(t, decoyResult.IsError)
	decoyData := decodeJSON(t, decoyResult.ForLLM)
	assert.Equal(t, "unclicked", decoyData["text"], "the click must NEVER have reached #decoy-yes")
}

// TestTextSel_InvalidScopeCSS_ReturnsClearError is finding #3b's acceptance
// case: a malformed CSS scope (no trailing pseudo involved at all — the raw
// `selector` itself is invalid CSS) must surface a clear "invalid selector
// scope" error. The JS must never silently fall back to scanning the whole
// document on a querySelectorAll exception, which could otherwise land an
// action on an unrelated element and report success.
func TestTextSel_InvalidScopeCSS_ReturnsClearError(t *testing.T) {
	skipIfNoBrowser(t)

	srv := textSelectorFixtureServer(t)
	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError)

	click := mustGetTool(t, registry, "browser_click")
	// "###bad" is syntactically invalid CSS (a selector can have at most one
	// leading "#" per compound) — querySelectorAll throws
	// "'###bad' is not a valid selector" on it (verified against real
	// Chromium; some other malformed-looking strings, e.g. an unterminated
	// "[foo", are parsed leniently and do NOT throw).
	result := click.Execute(ctx, map[string]any{"selector": "###bad", "text": "Confirm"})
	require.NotNil(t, result)
	require.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "invalid selector scope",
		"malformed CSS scope must surface a clear error, not a silent whole-document fallback; got: %s", result.ForLLM)
}

// ---------------------------------------------------------------------------
// 7-reviewer finding #4 (visibility must exclude hidden/opacity-0/clipped)
// and finding #5 (innerText only, never a textContent fallback that leaks
// <script>/<style> source).
// ---------------------------------------------------------------------------

// btn-sr-only's width/height/padding/margin/border are ALL zeroed
// explicitly: a <button> carries non-zero UA-stylesheet default
// padding/border, so setting only width:0;height:0 (without also zeroing
// padding/border) still yields a small but non-zero getClientRects() box —
// verified empirically against real Chromium — which would defeat the very
// check this fixture exists to exercise. The <script> tag exercises finding
// #5 (its source text must never leak into matching) on the SAME page —
// findings #4 and #5 are combined into one fixture/one test (below) so the
// whole group shares a single Chromium instance instead of four separate
// ones; sequential Chromium launch/teardown was observed to be a real
// contention source for this test file's total wall-clock reliability in a
// resource-constrained CI/devpod environment.
const invisibilityFixtureHTML = `<!doctype html><html><head><title>Invisibility</title>
<script>var x = 'SecretNeedle123';</script>
</head><body>
<button id="btn-vis-hidden" style="visibility:hidden">Ghost Alpha</button>
<button id="btn-opacity-zero" style="opacity:0">Ghost Beta</button>
<button id="btn-sr-only" style="position:absolute;width:0;height:0;padding:0;margin:0;border:0;overflow:hidden;">Ghost Gamma</button>
<p>This paragraph deliberately avoids the ghost buttons' own wording or the script's secret.</p>
</body></html>`

func invisibilityFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(invisibilityFixtureHTML))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestTextSel_Invisibility_And_ScriptSource_NotMatched covers findings #4
// and #5's negative-match cases against ONE shared page/browser instance:
// visibility:hidden, opacity:0, a zero-size sr-only-style clip (finding #4),
// and a <script> tag's source text (finding #5) must all be excluded from
// text matching.
func TestTextSel_Invisibility_And_ScriptSource_NotMatched(t *testing.T) {
	skipIfNoBrowser(t)

	srv := invisibilityFixtureServer(t)
	cfg := shortTimeoutBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError)

	click := mustGetTool(t, registry, "browser_click")

	cases := []struct {
		name    string
		args    map[string]any
		wantWhy string
	}{
		{
			"visibility:hidden",
			map[string]any{"selector": "button", "text": "Ghost Alpha"},
			"a visibility:hidden element must NOT be matched by text selection",
		},
		{
			"opacity:0",
			map[string]any{"selector": "button", "text": "Ghost Beta"},
			"an opacity:0 element must NOT be matched by text selection",
		},
		{
			"zero-size sr-only-style clip",
			map[string]any{"selector": "button", "text": "Ghost Gamma"},
			"a zero-size (sr-only-style clipped) element must NOT be matched by text selection",
		},
		{
			"script tag source",
			map[string]any{"text": "SecretNeedle123"},
			"a <script> tag's SOURCE text must never leak into text matching via a textContent fallback",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := click.Execute(ctx, tc.args)
			require.NotNil(t, result)
			assert.True(t, result.IsError, tc.wantWhy)
			assert.Contains(t, result.ForLLM, "no visible element matching text")
		})
	}
}

// ---------------------------------------------------------------------------
// 7-reviewer finding #7: ambiguous ties must error, not silently pick first.
// ---------------------------------------------------------------------------

const ambiguousSiblingsFixtureHTML = `<!doctype html><html><head><title>Ambiguous</title></head><body>
<ul>
  <li>Row A <button class="del-btn" onclick="this.dataset.clicked='1'">Delete</button></li>
  <li>Row B <button class="del-btn" onclick="this.dataset.clicked='1'">Delete</button></li>
</ul>
</body></html>`

// TestTextSel_AmbiguousSiblings_ReturnsClearError is finding #7's
// acceptance case: two sibling <button>Delete</button> elements — neither
// containing the other — tie for the smallest/most-specific match. This
// must surface a clear "N elements match" error, not silently resolve to
// the first one in DOM order.
func TestTextSel_AmbiguousSiblings_ReturnsClearError(t *testing.T) {
	skipIfNoBrowser(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(ambiguousSiblingsFixtureHTML))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError)

	click := mustGetTool(t, registry, "browser_click")
	result := click.Execute(ctx, map[string]any{"text": "Delete"})
	require.NotNil(t, result)
	require.True(
		t,
		result.IsError,
		"two indistinguishable sibling matches must be reported as ambiguous, not silently resolved to the first",
	)
	assert.Contains(
		t,
		result.ForLLM,
		"2 elements match text",
		"ambiguity error must name the count; got: %s",
		result.ForLLM,
	)
	assert.Contains(t, result.ForLLM, `"Delete"`, "ambiguity error must name the needle; got: %s", result.ForLLM)
}

// ---------------------------------------------------------------------------
// Additional coverage: case-insensitivity + whitespace normalization,
// marker cleanup, browser_type via a REAL pseudo, and the resolve-then-act
// race (7-reviewer finding #6).
// ---------------------------------------------------------------------------

const caseWhitespaceFixtureHTML = "<!doctype html><html><head><title>Case Whitespace</title></head><body>\n" +
	"<p id=\"messy\">  Hello    there,\n   friend  </p>\n" +
	"</body></html>"

// TestTextSel_CaseInsensitive_And_WhitespaceNormalized proves both
// normalization rules at once: the needle is a DIFFERENT case than the DOM
// text, and the DOM text itself has irregular internal whitespace (multiple
// spaces + a newline) that must collapse to single spaces before comparison.
func TestTextSel_CaseInsensitive_And_WhitespaceNormalized(t *testing.T) {
	skipIfNoBrowser(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(caseWhitespaceFixtureHTML))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError)

	getText := mustGetTool(t, registry, "browser_get_text")
	result := getText.Execute(ctx, map[string]any{"text": "HELLO THERE, FRIEND"})
	require.NotNil(t, result)
	require.False(
		t,
		result.IsError,
		"a differently-cased, single-spaced needle must match DOM text with irregular internal whitespace; got: %s",
		result.ForLLM,
	)
	data := decodeJSON(t, result.ForLLM)
	assert.Contains(t, data["text"], "Hello")
}

// TestTextSel_MarkerAttributeCleanedUpAfterAction proves the internal
// data-omnipus-tsel marker attribute is removed from the DOM once the
// resolved action completes — it must never be left behind as an observable
// side effect on the page.
func TestTextSel_MarkerAttributeCleanedUpAfterAction(t *testing.T) {
	skipIfNoBrowser(t)

	srv := textSelectorFixtureServer(t)
	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError)

	click := mustGetTool(t, registry, "browser_click")
	clickRes := click.Execute(ctx, map[string]any{"text": "Confirm"})
	require.False(t, clickRes.IsError)

	evalTool := mustGetTool(t, registry, "browser_evaluate")
	evalRes := evalTool.Execute(ctx, map[string]any{
		"js": `document.querySelector('[data-omnipus-tsel]') === null`,
	})
	require.False(t, evalRes.IsError)
	evalData := decodeJSON(t, evalRes.ForLLM)
	assert.Equal(t, true, evalData["result"],
		"the internal data-omnipus-tsel marker attribute must be removed from the DOM after the action completes")
}

const contentEditableFixtureHTML = `<!doctype html><html><head><title>Editable</title></head><body>
<div id="editable-note" contenteditable="true">Edit this note</div>
</body></html>`

// TestTextSel_TypeTool_PseudoSelector_TypesIntoContentEditable exercises
// browser_type's isText=true path (resolvePseudoOnlySelector) end to end
// against a REAL Playwright-style pseudo selector — the pre-existing
// TestTextSel_TypeTool_PseudoSelector_TypesIntoInput only used plain CSS
// (isText=false fast path) and never actually drove the pseudo-resolution
// route. A contenteditable element (not a bare <input>) is used because a
// bare input has no visible text of its own to match by — see finding #9's
// documented limitation on TypeTool.Description().
func TestTextSel_TypeTool_PseudoSelector_TypesIntoContentEditable(t *testing.T) {
	skipIfNoBrowser(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(contentEditableFixtureHTML))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	nav := mustGetTool(t, registry, "browser_navigate")
	navRes := nav.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navRes.IsError)

	typeTool := mustGetTool(t, registry, "browser_type")
	typeRes := typeTool.Execute(ctx, map[string]any{
		"selector": `div:has-text("Edit this note")`,
		"text":     " appended",
	})
	require.NotNil(t, typeRes)
	require.False(
		t,
		typeRes.IsError,
		"typing via a REAL pseudo selector (isText=true path) must succeed; got: %s",
		typeRes.ForLLM,
	)

	evalTool := mustGetTool(t, registry, "browser_evaluate")
	evalRes := evalTool.Execute(ctx, map[string]any{"js": `document.getElementById("editable-note").textContent`})
	require.False(t, evalRes.IsError)
	evalData := decodeJSON(t, evalRes.ForLLM)
	assert.Contains(t, evalData["result"], "appended",
		"SendKeys via the pseudo-resolved marker must have typed into the REAL contenteditable element")
}

// TestTextSel_ResolveThenActRace_ErrorNamesOriginalLocator is finding #6's
// acceptance case, driven deterministically (not via wall-clock racing)
// against the real production functions: resolve a marker via
// resolveActionSelector, then remove the resolved element from the DOM
// BEFORE running the caller's own follow-up action — exactly the race
// window finding #6 describes — and prove the resulting error, after
// scrubMarkerFromError, names the ORIGINAL locator and never leaks the
// internal data-omnipus-tsel marker attribute or selector.
func TestTextSel_ResolveThenActRace_ErrorNamesOriginalLocator(t *testing.T) {
	skipIfNoBrowser(t)

	srv := textSelectorFixtureServer(t)
	cfg := testBrowserCfg(t)
	_, mgr := newPermissiveRegistry(t, cfg)

	tabCtx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	require.NoError(t, chromedp.Run(tabCtx, chromedp.Navigate(srv.URL)))

	// Resolve deterministically — the button exists right now.
	target, cleanup, rerr := resolveActionSelector(tabCtx, "browser_click", "", "Confirm", mgr.PageTimeout())
	defer cleanup()
	require.NoError(t, rerr)
	require.NotEqual(t, "", target)
	require.NotEqual(t, "Confirm", target, "target should be the internal marker selector, not the plain locator")

	// Deterministically remove the resolved element BEFORE the caller's own
	// follow-up action runs.
	removeScript := fmt.Sprintf(`document.querySelector(%q).remove()`, target)
	require.NoError(t, chromedp.Run(tabCtx, chromedp.Evaluate(removeScript, nil)))

	displayTarget := displayLocator(Locator{Text: "Confirm"})
	boundedCtx, cancel := context.WithTimeout(tabCtx, 5*time.Second)
	defer cancel()
	actionErr := chromedp.Run(boundedCtx, chromedp.WaitVisible(target, chromedp.ByQuery))
	require.Error(t, actionErr, "the resolved element was removed — the follow-up action must fail")

	// A bare context-deadline timeout from chromedp.WaitVisible does NOT
	// embed the selector in its own error text at all (verified empirically
	// against real Chromium) — so scrubMarkerFromError alone (a substring
	// replace) has nothing to substitute in that case. This is exactly why
	// ClickTool.Execute (and Type/GetText/Wait) now explicitly NAME
	// displayTarget in their outer message rather than relying solely on
	// scrubbing; reproduce that same outer wrapping here to prove the
	// end-to-end guarantee.
	msg := fmt.Sprintf("browser_click: element %q not found or not clickable: %s",
		displayTarget, scrubMarkerFromError(actionErr, target, displayTarget))

	assert.Contains(t, msg, "Confirm", "the final error message must name the ORIGINAL locator")
	assert.NotContains(t, msg, textMarkerAttr,
		"the final error message must NEVER leak the internal marker attribute name")
	assert.NotContains(t, msg, target,
		"the final error message must NEVER leak the raw marker selector")
}
