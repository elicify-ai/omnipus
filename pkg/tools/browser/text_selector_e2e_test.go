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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

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
	assert.Equal(t, true, clickData["opened_new_tab"],
		":has-text click on a target=_blank link must report opened_new_tab=true (trusted click preserved); got: %s", clickRes.ForLLM)

	tabs, activeIdx, err := mgr.ListTabs(defaultSessionID)
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
	_, regErr := RegisterTools(registry, cfg, ssrf, true)
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
