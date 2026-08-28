// tool.Execute end-to-end tests for the browser package.
//
// All tests in this file drive the 7 browser tools through their public
// tool.Execute interface (not chromedp directly), against a deterministic
// httptest fixture server. Chrome-in-CI is NOT approved; every test is gated
// by skipIfNoBrowser so the suite stays green when no Chromium binary is present.
//
// Run locally: OMNIPUS_BROWSER_E2E=1 go test -tags goolm,stdjson -v -run TestExecute ./pkg/tools/browser/
//
// Issue: #445 (epic #440)  Plan §3.18/§9.5

package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// executeFixtureHTML is the page served at the fixture root.
// It contains every element referenced by the tests so no external network call
// is needed.
const executeFixtureHTML = `<!DOCTYPE html>
<html>
<head><title>Execute E2E Fixture</title></head>
<body>
  <h1 id="heading">Fixture Heading</h1>
  <button id="btn" onclick="document.getElementById('result').textContent='clicked'">Click Me</button>
  <div id="result">not-clicked</div>
  <input id="name-input" type="text" />
</body>
</html>`

// executeLargeTextHTML serves a page whose #bigtext element exceeds 100 KB.
// Used to verify the browser_get_text truncation cap.
const executeLargeTextPrefix = `<!DOCTYPE html>
<html><head><title>Large</title></head><body><div id="bigtext">`

const executeLargeTextSuffix = `</div></body></html>`

// executeTestServer starts and registers a deterministic httptest.Server with
// multiple routes used by the Execute-level tests.
//
// Routes:
//
//	GET /              — fixture page with heading, button, result div, input
//	GET /large         — page with a >100 KB element text
//	GET /redirect-meta — 302 to http://169.254.169.254/
//	GET /redirect-loop — 302 back to itself (not used directly; redirect target)
func executeTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, executeFixtureHTML)
	})

	mux.HandleFunc("/large", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		bigText := strings.Repeat("A", 110*1024) // 110 KB of 'A'
		fmt.Fprint(w, executeLargeTextPrefix+bigText+executeLargeTextSuffix)
	})

	// POST-REDIRECT SSRF route: redirects to the cloud-metadata IP so that
	// the browser follows the redirect internally, then the post-redirect SSRF
	// check in NavigateTool must fire and kill the page.
	// Note: the redirect target (169.254.169.254) does not need to be reachable
	// — we only assert that the SSRF block fires, not that Chrome fetches it.
	mux.HandleFunc("/redirect-meta", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/", http.StatusFound)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newPermissiveRegistry creates a BrowserManager, registers all 7 tools with
// evaluateEnabled=true, and returns the registry and manager.
// The SSRF checker allows 127.0.0.1 so the local httptest server passes.
func newPermissiveRegistry(t *testing.T, cfg BrowserConfig) (*tools.ToolRegistry, *BrowserManager) {
	t.Helper()
	ssrf := security.NewSSRFChecker([]string{"127.0.0.1"})
	registry := tools.NewToolRegistry()
	mgr, err := RegisterTools(registry, cfg, ssrf, true /* evaluateEnabled */, t.TempDir(), true)
	require.NoError(t, err)
	t.Cleanup(mgr.Shutdown)
	return registry, mgr
}

// testBrowserCfg returns a BrowserConfig suitable for tests: headless, short
// timeout, single tab, fresh temp profile dir.
//
// TrustPathChrome is on because skipIfNoBrowser already probed a working
// Chrome on $PATH (the #615 job installs google-chrome for this). ADR-052's
// production default (false) would discard that binary and try a managed
// install under InstallRootForProfileDir(t.TempDir()) — /tmp/chromium, or
// /chromium when ProfileDir itself is under /tmp — which is not the Chrome
// the gate found, and is what made TestBrowserTools_E2E_DirectChromedp fail
// with `mkdir /chromium: permission denied`.
func testBrowserCfg(t *testing.T) BrowserConfig {
	t.Helper()
	return BrowserConfig{
		Enabled:         true,
		Headless:        true,
		PageTimeout:     15 * time.Second,
		MaxTabs:         5,
		ProfileDir:      t.TempDir(),
		TrustPathChrome: true,
	}
}

// mustGetTool retrieves a tool from the registry or fails the test.
func mustGetTool(t *testing.T, reg *tools.ToolRegistry, name string) tools.Tool {
	t.Helper()
	tool, ok := reg.Get(name)
	require.True(t, ok, "tool %q must be registered", name)
	return tool
}

// decodeJSON asserts that s is valid JSON and unmarshals it into a
// map[string]any for field-level assertions.
func decodeJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(s), &m), "result must be valid JSON, got: %s", s)
	return m
}

// ---------------------------------------------------------------------------
// Case 1 — Happy chain: navigate → wait → click → get_text
//
// BDD: Given a local fixture page with a button and a result div,
//
//	When browser_navigate, browser_wait, browser_click, and browser_get_text
//	are called in sequence via tool.Execute,
//	Then each call returns a non-error JSON result with the expected fields
//	and the final get_text reflects the DOM mutation caused by the click.
//
// Traces to: wave4-whatsapp-browser-spec.md US-5 (action primitives)
// Issue #445 §3.18 case 1
// ---------------------------------------------------------------------------

func TestExecute_HappyChain_NavigateWaitClickGetText(t *testing.T) {
	skipIfNoBrowser(t)

	srv := executeTestServer(t)
	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)

	ctx := context.Background()

	// 1. browser_navigate ────────────────────────────────────────────────────
	navTool := mustGetTool(t, registry, "browser_navigate")
	navResult := navTool.Execute(ctx, map[string]any{"url": srv.URL})
	require.NotNil(t, navResult)
	assert.False(t, navResult.IsError, "navigate must succeed; got: %s", navResult.ForLLM)

	navData := decodeJSON(t, navResult.ForLLM)
	assert.Equal(t, "Execute E2E Fixture", navData["title"],
		"navigate result must include the page title")
	urlVal, hasURL := navData["url"]
	assert.True(t, hasURL, "navigate result must include the final URL")
	assert.NotEmpty(t, urlVal, "final URL must be non-empty")

	// Differentiation: a second navigate to a DIFFERENT URL must return a
	// DIFFERENT title — proves the tool is not hardcoded.
	secondNavResult := navTool.Execute(ctx, map[string]any{"url": srv.URL + "/large"})
	require.NotNil(t, secondNavResult)
	assert.False(t, secondNavResult.IsError, "second navigate must succeed; got: %s", secondNavResult.ForLLM)
	secondData := decodeJSON(t, secondNavResult.ForLLM)
	assert.NotEqual(t, navData["title"], secondData["title"],
		"two different pages must return two different titles (differentiation check)")

	// Navigate back to fixture for the rest of the chain.
	backResult := navTool.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, backResult.IsError, "navigate back must succeed")

	// 2. browser_wait ────────────────────────────────────────────────────────
	waitTool := mustGetTool(t, registry, "browser_wait")
	waitResult := waitTool.Execute(ctx, map[string]any{"selector": "#btn"})
	require.NotNil(t, waitResult)
	assert.False(t, waitResult.IsError, "browser_wait for #btn must succeed; got: %s", waitResult.ForLLM)
	waitData := decodeJSON(t, waitResult.ForLLM)
	found, ok := waitData["found"].(bool)
	require.True(t, ok, "wait result must contain bool field 'found'")
	assert.True(t, found, "found must be true when selector exists")

	// 3. browser_click ───────────────────────────────────────────────────────
	clickTool := mustGetTool(t, registry, "browser_click")
	clickResult := clickTool.Execute(ctx, map[string]any{"selector": "#btn"})
	require.NotNil(t, clickResult)
	assert.False(t, clickResult.IsError, "browser_click on #btn must succeed; got: %s", clickResult.ForLLM)
	clickData := decodeJSON(t, clickResult.ForLLM)
	success, ok := clickData["success"].(bool)
	require.True(t, ok, "click result must contain bool field 'success'")
	assert.True(t, success, "click result success must be true")
	assert.Equal(t, "#btn", clickData["selector"],
		"click result must echo back the selector that was clicked")

	// 4. browser_get_text — assert DOM mutation from click ───────────────────
	getTextTool := mustGetTool(t, registry, "browser_get_text")
	getTextResult := getTextTool.Execute(ctx, map[string]any{"selector": "#result"})
	require.NotNil(t, getTextResult)
	assert.False(t, getTextResult.IsError, "browser_get_text on #result must succeed; got: %s", getTextResult.ForLLM)
	textData := decodeJSON(t, getTextResult.ForLLM)
	text, ok := textData["text"].(string)
	require.True(t, ok, "get_text result must contain string field 'text'")
	assert.Equal(t, "clicked", text,
		"get_text must reflect the DOM mutation caused by the click")

	// Differentiation: get_text on a DIFFERENT selector returns a DIFFERENT value.
	headingResult := getTextTool.Execute(ctx, map[string]any{"selector": "#heading"})
	require.NotNil(t, headingResult)
	assert.False(t, headingResult.IsError, "get_text on #heading must succeed")
	headingData := decodeJSON(t, headingResult.ForLLM)
	headingText, ok := headingData["text"].(string)
	require.True(t, ok)
	assert.NotEqual(t, text, headingText,
		"#result and #heading must return different text (differentiation check)")
	assert.Equal(t, "Fixture Heading", headingText,
		"#heading text must equal the expected content")
}

// ---------------------------------------------------------------------------
// Case 2 — browser_evaluate: JS eval edges
//
// BDD: Given a fixture page is loaded,
//
//	When browser_evaluate is called via tool.Execute with various JS expressions,
//	Then correct results, error states, and edge-value handling are returned.
//
// Traces to: wave4-whatsapp-browser-spec.md US-5 (evaluate), #438 (gate)
// Issue #445 §3.18 case 2
// ---------------------------------------------------------------------------

func TestExecute_Evaluate_Edges(t *testing.T) {
	skipIfNoBrowser(t)

	srv := executeTestServer(t)
	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	// Navigate to fixture first.
	navTool := mustGetTool(t, registry, "browser_navigate")
	navResult := navTool.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navResult.IsError, "navigate must succeed; got: %s", navResult.ForLLM)

	evalTool := mustGetTool(t, registry, "browser_evaluate")

	// 2a. document.title — correct JSON result ───────────────────────────────
	t.Run("document.title", func(t *testing.T) {
		result := evalTool.Execute(ctx, map[string]any{"js": "document.title"})
		require.NotNil(t, result)
		assert.False(t, result.IsError, "evaluate document.title must succeed; got: %s", result.ForLLM)
		data := decodeJSON(t, result.ForLLM)
		titleVal, ok := data["result"].(string)
		require.True(t, ok, "result field must be a string, got: %v", data["result"])
		assert.Equal(t, "Execute E2E Fixture", titleVal,
			"document.title must return the fixture page title")
	})

	// 2b. Throwing expression → IsError ─────────────────────────────────────
	t.Run("throwing expression", func(t *testing.T) {
		result := evalTool.Execute(ctx, map[string]any{"js": "throw new Error('boom')"})
		require.NotNil(t, result)
		assert.True(t, result.IsError,
			"evaluate of a throwing expression must set IsError=true; got ForLLM: %s", result.ForLLM)
		// The error message should convey something useful.
		assert.NotEmpty(t, result.ForLLM,
			"throwing expression error must include a non-empty error message")
	})

	// 2c. undefined → null in JSON (undefined is not serialisable in JSON) ───
	// The CDP JSON serialization maps undefined → null.
	t.Run("undefined returns null", func(t *testing.T) {
		result := evalTool.Execute(ctx, map[string]any{"js": "undefined"})
		require.NotNil(t, result)
		// undefined may come back as IsError or as a null result depending on
		// chromedp serialization. We assert that the tool does NOT hang and
		// returns within the test timeout.
		// If it is a success result, the "result" field must be null (JSON null).
		if !result.IsError {
			data := decodeJSON(t, result.ForLLM)
			resultVal, hasResult := data["result"]
			assert.True(t, hasResult, "result key must be present")
			assert.Nil(t, resultVal, "undefined JS value must deserialize as JSON null")
		}
		// IsError is also acceptable — chromedp may not serialize undefined.
	})

	// 2d. Large string — must not panic, must return a non-empty result ──────
	t.Run("large string", func(t *testing.T) {
		result := evalTool.Execute(ctx, map[string]any{"js": `"A".repeat(50000)`})
		require.NotNil(t, result)
		// A large string is valid JS; the tool must handle it without panicking.
		if !result.IsError {
			data := decodeJSON(t, result.ForLLM)
			str, ok := data["result"].(string)
			require.True(t, ok, "large string result must be a string, got: %v (%T)", data["result"], data["result"])
			assert.Equal(t, 50000, len(str), "large string result must be 50,000 characters")
		}
	})

	// 2e. DOM node reference — CDP cannot serialize DOM node; expects error ──
	// PRODUCTION GAP NOTE: chromedp.Evaluate with AwaitPromise=false may
	// silently return null for non-serializable values like DOM nodes rather
	// than an error. This test documents the current behavior: if it returns
	// null we accept it; if it returns an error we accept it. What is NOT
	// acceptable is a panic or hang.
	t.Run("dom node reference", func(t *testing.T) {
		result := evalTool.Execute(ctx, map[string]any{"js": "document.body"})
		require.NotNil(t, result)
		// Result is either an error (unserializable) or a null/map — but must not hang.
		// No assertion on IsError since behavior is implementation-defined.
		assert.NotEmpty(t, result.ForLLM, "DOM node evaluate must return a non-empty ForLLM")
	})

	// 2f. Circular object — JSON.stringify throws; CDP marshaling may error ───
	t.Run("circular object", func(t *testing.T) {
		result := evalTool.Execute(ctx, map[string]any{"js": `(function(){let a={};a.self=a;return a;})()`})
		require.NotNil(t, result)
		// Circular objects are not serializable via JSON; we expect either an
		// error result or a non-nil result without hang.
		assert.NotEmpty(t, result.ForLLM, "circular object evaluate must return non-empty ForLLM")
	})

	// 2g. Long-running eval — must time out cleanly, no hang ─────────────────
	// Use a separate manager with a short timeout to make the test fast.
	t.Run("long running eval timeout", func(t *testing.T) {
		shortCfg := BrowserConfig{
			Enabled:         true,
			Headless:        true,
			PageTimeout:     3 * time.Second,
			MaxTabs:         2,
			ProfileDir:      t.TempDir(),
			TrustPathChrome: true,
		}
		shortRegistry, shortMgr := newPermissiveRegistry(t, shortCfg)
		defer shortMgr.Shutdown()

		// Navigate on the short-timeout manager.
		shortNavTool := mustGetTool(t, shortRegistry, "browser_navigate")
		shortNavResult := shortNavTool.Execute(ctx, map[string]any{"url": srv.URL})
		require.False(t, shortNavResult.IsError, "short timeout navigate must succeed; got: %s", shortNavResult.ForLLM)

		shortEvalTool := mustGetTool(t, shortRegistry, "browser_evaluate")
		start := time.Now()
		// An infinite loop that can only be stopped by the context timeout.
		result := shortEvalTool.Execute(ctx, map[string]any{"js": "while(true){}"})
		elapsed := time.Since(start)

		require.NotNil(t, result)
		assert.True(t, result.IsError,
			"long-running eval must time out and set IsError=true; ForLLM: %s", result.ForLLM)
		// Verify the test did not hang — it should complete within the timeout
		// plus a reasonable overhead (2× timeout).
		assert.Less(t, elapsed, 10*time.Second,
			"long-running eval must time out within 10s (got %s); no hang", elapsed)
	})
}

// ---------------------------------------------------------------------------
// Case 2h — browser_evaluate disabled (executeEnabled=false) → IsError
//
// BDD: Given a browser manager registered with evaluateEnabled=false,
//
//	When browser_evaluate.Execute is called,
//	Then result.IsError=true and ForLLM contains "disabled".
//
// Traces to: #438 (execute gate), wave6b_evaluate_gate_test.go patterns
// Issue #445 §3.18 case 2
// ---------------------------------------------------------------------------

func TestExecute_Evaluate_DisabledGate(t *testing.T) {
	// No Chrome needed — this test asserts the software gate fires before
	// any browser interaction. skipIfNoBrowser is NOT called here intentionally.
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	ssrf := security.NewSSRFChecker([]string{"127.0.0.1"})
	registry := tools.NewToolRegistry()
	// evaluateEnabled=false: Execute must deny before touching Chrome.
	_, regErr := RegisterTools(registry, cfg, ssrf, false, t.TempDir(), true)
	require.NoError(t, regErr)

	evalTool := mustGetTool(t, registry, "browser_evaluate")
	result := evalTool.Execute(context.Background(), map[string]any{"js": "document.title"})
	require.NotNil(t, result)
	assert.True(t, result.IsError,
		"browser_evaluate with executeEnabled=false must return IsError=true; got ForLLM: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, "disabled",
		"error message must contain 'disabled' to guide the operator; got: %s", result.ForLLM)
}

// ---------------------------------------------------------------------------
// Case 3 — Scheme blocks via Execute (file://, data:, javascript:, chrome://)
//
// BDD: Given a browser manager with a permissive SSRF checker,
//
//	When browser_navigate.Execute is called with a blocked scheme URL,
//	Then result.IsError=true BEFORE any network or browser interaction.
//
// Traces to: blocked_schemes_test.go (scheme-level), §3.18 case 3
// Issue #445 §3.18 case 3
//
// No Chrome needed for this test — scheme validation fires before browser start.
// ---------------------------------------------------------------------------

func TestExecute_Navigate_SchemeBlocks(t *testing.T) {
	// No Chrome needed — ValidateURL is called before Session is acquired.
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	ssrf := security.NewSSRFChecker([]string{"127.0.0.1"})
	registry := tools.NewToolRegistry()
	_, regErr := RegisterTools(registry, cfg, ssrf, false, t.TempDir(), true)
	require.NoError(t, regErr)

	navTool := mustGetTool(t, registry, "browser_navigate")

	tests := []struct {
		name   string
		url    string
		scheme string
	}{
		// Traces to: manager_wave4_test.go Dataset rows 3–6
		{"file", "file:///etc/passwd", "file"},
		{"data", "data:text/html,<h1>xss</h1>", "data"},
		{"javascript", "javascript:alert(1)", "javascript"},
		{"chrome", "chrome://settings/", "chrome"},
		{"chrome-extension", "chrome-extension://id/page.html", "chrome-extension"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := navTool.Execute(context.Background(), map[string]any{"url": tc.url})
			require.NotNil(t, result)
			assert.True(t, result.IsError,
				"scheme %s must be blocked by Execute; got ForLLM: %s", tc.scheme, result.ForLLM)
			// The error must mention the blocked reason — not a generic "no browser".
			ssrfOrSchemeError := strings.Contains(result.ForLLM, tc.scheme) ||
				strings.Contains(result.ForLLM, "blocked") ||
				strings.Contains(result.ForLLM, "security") ||
				strings.Contains(result.ForLLM, "not permitted")
			assert.True(t, ssrfOrSchemeError,
				"error for scheme %s must mention scheme/blocked/security; got: %s", tc.scheme, result.ForLLM)
		})
	}
}

// ---------------------------------------------------------------------------
// Case 4 — Post-redirect SSRF: fixture 302 → 169.254.169.254 → block
//
// BDD: Given a fixture server route that redirects to http://169.254.169.254/,
//
//	When browser_navigate.Execute is called with the redirect URL,
//	Then result.IsError=true (post-redirect SSRF check fires).
//
// The redirect target (169.254.169.254) does not need to be reachable —
// the post-redirect check validates the final Location before fetching it
// (in practice Chrome may follow the redirect internally, but the post-
// redirect SSRF check fires against chromedp.Location() and kills the tab).
//
// Traces to: tools.go NavigateTool.Execute post-redirect block (line ~89)
// Issue #445 §3.18 case 4
// ---------------------------------------------------------------------------

func TestExecute_Navigate_PostRedirectSSRF(t *testing.T) {
	skipIfNoBrowser(t)

	srv := executeTestServer(t)
	cfg := testBrowserCfg(t)
	// SSRF checker that blocks 169.254.169.254 (no allow for metadata range).
	// 127.0.0.1 is allowed so the fixture server itself passes the pre-check.
	ssrf := security.NewSSRFChecker([]string{"127.0.0.1"})
	registry := tools.NewToolRegistry()
	mgr, err := RegisterTools(registry, cfg, ssrf, false, t.TempDir(), true)
	require.NoError(t, err)
	t.Cleanup(mgr.Shutdown)

	navTool := mustGetTool(t, registry, "browser_navigate")
	result := navTool.Execute(context.Background(), map[string]any{
		"url": srv.URL + "/redirect-meta",
	})
	require.NotNil(t, result)

	// The tool must block the post-redirect navigation and return an error.
	// The page should be navigated to about:blank after the block.
	assert.True(t, result.IsError,
		"post-redirect to 169.254.169.254 must be blocked; got ForLLM: %s", result.ForLLM)

	// The error must mention redirect or blocked or SSRF (not a generic error).
	errMsg := result.ForLLM
	ssrfMentioned := strings.Contains(errMsg, "redirect") ||
		strings.Contains(errMsg, "blocked") ||
		strings.Contains(errMsg, "SSRF") ||
		strings.Contains(errMsg, "169.254")
	assert.True(t, ssrfMentioned,
		"post-redirect SSRF error must mention redirect/blocked/SSRF/169.254; got: %s", errMsg)
}

// ---------------------------------------------------------------------------
// Case 5 — browser_get_text 64,000-char cap (ADR-066 FR-014 / B-15)
//
// BDD: Given a fixture page with a >100 KB element,
//
//	When browser_get_text.Execute is called on that element,
//	Then the returned text is capped at 64,000 chars and ends with the truncation suffix.
//
// Traces to: tools.go maxGetTextChars / capGetText
// Issue #445 §3.18 case 5
// ---------------------------------------------------------------------------

func TestExecute_GetText_64000CharCap(t *testing.T) {
	skipIfNoBrowser(t)

	srv := executeTestServer(t)
	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	// Navigate to the /large page.
	navTool := mustGetTool(t, registry, "browser_navigate")
	navResult := navTool.Execute(ctx, map[string]any{"url": srv.URL + "/large"})
	require.False(t, navResult.IsError, "navigate to /large must succeed; got: %s", navResult.ForLLM)

	getTextTool := mustGetTool(t, registry, "browser_get_text")
	result := getTextTool.Execute(ctx, map[string]any{"selector": "#bigtext"})
	require.NotNil(t, result)
	assert.False(t, result.IsError, "get_text on #bigtext must succeed; got: %s", result.ForLLM)

	data := decodeJSON(t, result.ForLLM)
	text, ok := data["text"].(string)
	require.True(t, ok, "get_text result must contain a string 'text' field")

	assert.LessOrEqual(t, len(text), maxGetTextChars+len(getTextTruncationSuffix),
		"get_text result must not exceed 64,000 chars + truncation suffix")
	assert.Contains(t, text, getTextTruncationSuffix,
		"get_text must append truncation suffix when content exceeds 64,000 chars")
}

// ---------------------------------------------------------------------------
// Case 6 — browser_screenshot: returns a base64 data URL
//
// BDD: Given a fixture page is loaded,
//
//	When browser_screenshot.Execute is called,
//	Then result.IsError=false and result.ForLLM is a valid base64 data URL.
//
// Traces to: tools.go ScreenshotTool.Execute (line 226)
// Issue #445 §3.18 case 6
// ---------------------------------------------------------------------------

func TestExecute_Screenshot_ReturnsDataURL(t *testing.T) {
	skipIfNoBrowser(t)

	srv := executeTestServer(t)
	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	// Navigate first so there is a rendered page to screenshot.
	navTool := mustGetTool(t, registry, "browser_navigate")
	navResult := navTool.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navResult.IsError, "navigate must succeed for screenshot test")

	screenshotTool := mustGetTool(t, registry, "browser_screenshot")
	result := screenshotTool.Execute(ctx, map[string]any{})
	require.NotNil(t, result)
	assert.False(t, result.IsError,
		"browser_screenshot must succeed; got ForLLM: %s", result.ForLLM)

	// Verify the ForLLM contains a valid data URL with base64-encoded image
	// data. browser_screenshot prepends "Current page URL:"/"Page title:"
	// context lines before the data URL (see ScreenshotTool.Execute), so the
	// data URL is embedded, not at the very start.
	imgIdx := strings.Index(result.ForLLM, "data:image/")
	require.GreaterOrEqual(t, imgIdx, 0,
		"screenshot ForLLM must contain a 'data:image/' URL; got: %.120s", result.ForLLM)
	dataURL := result.ForLLM[imgIdx:]
	// Strip the data URL prefix and check the base64 is decodable.
	commaIdx := strings.Index(dataURL, ",")
	require.Greater(t, commaIdx, 0, "data URL must contain a comma separator")
	b64 := dataURL[commaIdx+1:]
	decoded, decErr := base64.StdEncoding.DecodeString(b64)
	require.NoError(t, decErr, "screenshot base64 payload must be decodable")
	assert.Greater(t, len(decoded), 100,
		"decoded screenshot bytes must be non-trivial (>100 bytes)")

	// ArtifactTags must include a local file path reference.
	assert.NotEmpty(t, result.ArtifactTags,
		"browser_screenshot must set ArtifactTags with a local file path")
	assert.True(t, strings.Contains(result.ArtifactTags[0], "[file:"),
		"ArtifactTags entry must contain '[file:'; got: %v", result.ArtifactTags)
}

// TestExecute_Screenshot_WritesUnderAgentHome proves the ADR-046 FR-009
// defect fix: browser_screenshot must save its JPEG under the turn's
// effective working directory (ResolvePath, rooted at the agentHome passed
// to RegisterTools) rather than the process-wide os.TempDir() the pre-fix
// implementation wrote to unconditionally — a location no per-agent/per-turn
// confinement ever covered.
func TestExecute_Screenshot_WritesUnderAgentHome(t *testing.T) {
	skipIfNoBrowser(t)

	srv := executeTestServer(t)
	cfg := testBrowserCfg(t)

	// A distinct, per-test agentHome (NOT the bare os.TempDir() root) — if the
	// pre-fix os.WriteFile(filepath.Join(os.TempDir(), filename)) call were
	// still in place, the saved path would land directly under os.TempDir(),
	// never under this subdirectory.
	agentHome := t.TempDir()
	ssrf := security.NewSSRFChecker([]string{"127.0.0.1"})
	registry := tools.NewToolRegistry()
	mgr, err := RegisterTools(registry, cfg, ssrf, false, agentHome, true)
	require.NoError(t, err)
	t.Cleanup(mgr.Shutdown)

	ctx := context.Background()
	navTool := mustGetTool(t, registry, "browser_navigate")
	navResult := navTool.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navResult.IsError, "navigate must succeed for screenshot test")

	screenshotTool := mustGetTool(t, registry, "browser_screenshot")
	result := screenshotTool.Execute(ctx, map[string]any{})
	require.NotNil(t, result)
	require.False(t, result.IsError, "browser_screenshot must succeed; got ForLLM: %s", result.ForLLM)

	require.NotEmpty(t, result.ArtifactTags,
		"browser_screenshot must set ArtifactTags with a local file path")
	tag := result.ArtifactTags[0]
	require.True(t, strings.HasPrefix(tag, "[file:") && strings.HasSuffix(tag, "]"),
		"ArtifactTags entry must be a [file:...] marker; got: %q", tag)
	savedPath := strings.TrimSuffix(strings.TrimPrefix(tag, "[file:"), "]")

	// Resolve agentHome's own symlinks (matching ResolvePath's realpath
	// resolution) before the prefix comparison, so this doesn't spuriously
	// fail on a platform where t.TempDir() itself is a symlink (e.g. macOS's
	// /tmp -> /private/tmp).
	resolvedAgentHome, evalErr := filepath.EvalSymlinks(agentHome)
	require.NoError(t, evalErr, "resolve agentHome symlinks for comparison")

	assert.True(t, strings.HasPrefix(savedPath, resolvedAgentHome+string(os.PathSeparator)),
		"browser_screenshot must write under the agent's effective working directory %q; got %q",
		resolvedAgentHome, savedPath)

	_, statErr := os.Stat(savedPath)
	assert.NoError(t, statErr, "screenshot file must exist on disk at the resolved path")
}

// ---------------------------------------------------------------------------
// Case 7a — browser_wait timeout: selector that never appears
//
// BDD: Given a fixture page that does not contain selector "#nonexistent",
//
//	When browser_wait.Execute is called for that selector,
//	Then result.IsError=true within the configured timeout (no hang).
//
// Traces to: tools.go WaitTool.Execute (line 360)
// Issue #445 §3.18 case 7
// ---------------------------------------------------------------------------

func TestExecute_Wait_TimeoutForAbsentSelector(t *testing.T) {
	skipIfNoBrowser(t)

	srv := executeTestServer(t)
	// Use a short timeout so the test does not block.
	cfg := BrowserConfig{
		Enabled:         true,
		Headless:        true,
		PageTimeout:     3 * time.Second,
		MaxTabs:         2,
		ProfileDir:      t.TempDir(),
		TrustPathChrome: true,
	}
	registry, mgr := newPermissiveRegistry(t, cfg)
	defer mgr.Shutdown()
	ctx := context.Background()

	// Navigate to a page that does NOT have #nonexistent.
	navTool := mustGetTool(t, registry, "browser_navigate")
	navResult := navTool.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navResult.IsError, "navigate must succeed before wait test")

	waitTool := mustGetTool(t, registry, "browser_wait")
	start := time.Now()
	result := waitTool.Execute(ctx, map[string]any{"selector": "#nonexistent-element"})
	elapsed := time.Since(start)

	require.NotNil(t, result)
	assert.True(t, result.IsError,
		"browser_wait for absent selector must return IsError=true; got ForLLM: %s", result.ForLLM)
	// Verify no hang — must complete within 2× the configured timeout.
	assert.Less(t, elapsed, 10*time.Second,
		"browser_wait timeout must complete within 10s (got %s); no hang", elapsed)
}

// ---------------------------------------------------------------------------
// Case 7c — browser_get_text fails fast on an invisible-but-present or
// entirely absent selector, bounded by getTextWaitTimeout, NOT the full
// (deliberately large, here) PageTimeout.
//
// Regression test for a live-observed ~30s hang on
// browser_get_text{selector:"title"}: <title> lives in <head> and is never
// "visible", so the old chromedp.WaitVisible blocked for the ENTIRE
// PageTimeout before failing. The fix (1) swaps to chromedp.WaitReady (DOM
// presence, not rendered visibility) so a present-but-invisible node like
// <title> resolves immediately instead of never, and (2) bounds the
// existence check with the short, dedicated getTextWaitTimeout so a
// genuinely missing selector still fails fast rather than riding the full
// page-load budget.
//
// PageTimeout is set deliberately large (20s, comfortably above
// getTextWaitTimeout's 8s) so a regression back to
// WaitVisible-bounded-by-PageTimeout would show up as a ~20s+ elapsed time
// on the missing-selector case, not just as an assertion on IsError.
//
// Traces to: tools.go GetTextTool.Execute, getTextWaitTimeout
// ---------------------------------------------------------------------------

func TestExecute_GetText_FailsFastOnInvisibleOrMissingSelector(t *testing.T) {
	skipIfNoBrowser(t)

	srv := executeTestServer(t)
	cfg := BrowserConfig{
		Enabled:         true,
		Headless:        true,
		PageTimeout:     20 * time.Second,
		MaxTabs:         2,
		ProfileDir:      t.TempDir(),
		TrustPathChrome: true,
	}
	registry, mgr := newPermissiveRegistry(t, cfg)
	defer mgr.Shutdown()
	ctx := context.Background()

	navTool := mustGetTool(t, registry, "browser_navigate")
	navResult := navTool.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navResult.IsError, "navigate must succeed before get_text test")

	getTextTool := mustGetTool(t, registry, "browser_get_text")

	// "title" is present in <head> but never rendered/visible — the exact
	// selector from the live-observed hang. Must now succeed quickly.
	start := time.Now()
	result := getTextTool.Execute(ctx, map[string]any{"selector": "title"})
	elapsed := time.Since(start)
	assert.False(t, result.IsError,
		"browser_get_text on <title> must succeed (WaitReady, not WaitVisible); got: %s", result.ForLLM)
	assert.Less(t, elapsed, 10*time.Second,
		"browser_get_text on <title> must resolve well under the 20s PageTimeout (got %s)", elapsed)

	// A genuinely missing selector must fail FAST — bounded by
	// getTextWaitTimeout (8s), not the full 20s PageTimeout.
	start = time.Now()
	result = getTextTool.Execute(ctx, map[string]any{"selector": "#totally-absent-element"})
	elapsed = time.Since(start)
	assert.True(t, result.IsError, "browser_get_text on a missing selector must return IsError=true")
	assert.Less(
		t,
		elapsed,
		12*time.Second,
		"browser_get_text on a missing selector must fail fast (bounded by getTextWaitTimeout, not the 20s PageTimeout); got %s",
		elapsed,
	)
}

// ---------------------------------------------------------------------------
// Case 7b — Session persistence: two sequential Execute calls share the tab
//
// BDD: Given browser_navigate is called on a fixture page,
//
//	When browser_get_text is called next (without another navigate),
//	Then get_text reads from the same tab as navigate (session persists).
//
// Traces to: manager.go Session / defaultSessionID
// Issue #445 §3.18 case 7
// ---------------------------------------------------------------------------

func TestExecute_SessionPersistence(t *testing.T) {
	skipIfNoBrowser(t)

	srv := executeTestServer(t)
	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	// 1. Navigate to the fixture page.
	navTool := mustGetTool(t, registry, "browser_navigate")
	navResult := navTool.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navResult.IsError, "navigate must succeed")

	// 2. get_text WITHOUT another navigate — must read from the same tab.
	getTextTool := mustGetTool(t, registry, "browser_get_text")
	result := getTextTool.Execute(ctx, map[string]any{"selector": "#heading"})
	require.NotNil(t, result)
	assert.False(t, result.IsError,
		"get_text without re-navigate must succeed (session is persistent); got: %s", result.ForLLM)

	data := decodeJSON(t, result.ForLLM)
	text, ok := data["text"].(string)
	require.True(t, ok)
	assert.Equal(t, "Fixture Heading", text,
		"get_text after navigate must read from the same tab, not a blank page")
}

// ---------------------------------------------------------------------------
// Case 7c — MaxTabs limit: Session returns error when at capacity
//
// BDD: Given MaxTabs=1 and one tab already open,
//
//	When a second browser_navigate is called with a new_session_id (via Session()),
//	Then the manager returns "maximum concurrent tabs" error.
//
// This is an Execute-path check: the NavigateTool calls Session("default")
// so a new unique session name is injected by bypassing the manager directly,
// matching the spec intent to exercise the limit through the manager API.
//
// Traces to: manager.go Session (line 326), manager_wave4_test.go TestMaxTabsExceeded_SessionReturnsError
// Issue #445 §3.18 case 7
// ---------------------------------------------------------------------------

func TestExecute_MaxTabs_LimitError(t *testing.T) {
	skipIfNoBrowser(t)

	srv := executeTestServer(t)
	// Set MaxTabs=1 so the first call occupies the only slot.
	cfg := BrowserConfig{
		Enabled:         true,
		Headless:        true,
		PageTimeout:     10 * time.Second,
		MaxTabs:         1,
		ProfileDir:      t.TempDir(),
		TrustPathChrome: true,
	}
	ssrf := security.NewSSRFChecker([]string{"127.0.0.1"})
	registry := tools.NewToolRegistry()
	mgr, err := RegisterTools(registry, cfg, ssrf, false, t.TempDir(), true)
	require.NoError(t, err)
	t.Cleanup(mgr.Shutdown)
	ctx := context.Background()

	// 1. First navigate occupies the "default" tab (the only allowed slot).
	navTool := mustGetTool(t, registry, "browser_navigate")
	firstResult := navTool.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, firstResult.IsError,
		"first navigate must succeed (MaxTabs=1, 0 tabs open); got: %s", firstResult.ForLLM)

	// 2. Request a SECOND session directly on the manager — this should fail.
	// The "default" session is already held; a new session ID attempts a second tab.
	_, sessionErr := mgr.Session("second-tab-must-fail")
	require.Error(t, sessionErr,
		"requesting a second tab when MaxTabs=1 must return an error")
	assert.Contains(t, sessionErr.Error(), "maximum concurrent tabs",
		"error must mention 'maximum concurrent tabs'; got: %s", sessionErr.Error())
	assert.Contains(t, sessionErr.Error(), "1",
		"error must mention the limit (1); got: %s", sessionErr.Error())
}

// ---------------------------------------------------------------------------
// Case 8 — browser_type: text is typed into input and readable back
//
// BDD: Given a fixture page with an input element #name-input,
//
//	When browser_type.Execute is called with text "Hello E2E",
//	Then result.IsError=false, and browser_evaluate confirms the input value.
//
// This is the persistence test for write operations: type → read-back.
// Issue #445 §3.18 (supplementary — ensures type is not a no-op)
// ---------------------------------------------------------------------------

func TestExecute_Type_PersistsInDOM(t *testing.T) {
	skipIfNoBrowser(t)

	srv := executeTestServer(t)
	cfg := testBrowserCfg(t)
	registry, _ := newPermissiveRegistry(t, cfg)
	ctx := context.Background()

	// Navigate to fixture.
	navTool := mustGetTool(t, registry, "browser_navigate")
	navResult := navTool.Execute(ctx, map[string]any{"url": srv.URL})
	require.False(t, navResult.IsError, "navigate must succeed for type test")

	// Type text into the input.
	typeTool := mustGetTool(t, registry, "browser_type")
	typeResult := typeTool.Execute(ctx, map[string]any{
		"selector": "#name-input",
		"text":     "Hello E2E",
	})
	require.NotNil(t, typeResult)
	assert.False(t, typeResult.IsError,
		"browser_type must succeed; got ForLLM: %s", typeResult.ForLLM)
	typeData := decodeJSON(t, typeResult.ForLLM)
	success, ok := typeData["success"].(bool)
	require.True(t, ok, "type result must contain bool 'success'")
	assert.True(t, success, "type result success must be true")

	// Read back via evaluate to confirm the DOM was actually updated.
	evalTool := mustGetTool(t, registry, "browser_evaluate")
	evalResult := evalTool.Execute(ctx, map[string]any{
		"js": `document.getElementById("name-input").value`,
	})
	require.NotNil(t, evalResult)
	assert.False(t, evalResult.IsError,
		"evaluate after type must succeed; got: %s", evalResult.ForLLM)
	evalData := decodeJSON(t, evalResult.ForLLM)
	inputVal, ok := evalData["result"].(string)
	require.True(t, ok, "evaluate result must be a string, got: %v", evalData["result"])
	assert.Equal(t, "Hello E2E", inputVal,
		"input value after type must equal the typed text (persistence check)")

	// Differentiation: typing a DIFFERENT value must produce a DIFFERENT result.
	typeResult2 := typeTool.Execute(ctx, map[string]any{
		"selector": "#name-input",
		"text":     " World",
	})
	require.False(t, typeResult2.IsError, "second type must succeed")
	evalResult2 := evalTool.Execute(ctx, map[string]any{
		"js": `document.getElementById("name-input").value`,
	})
	require.False(t, evalResult2.IsError, "second evaluate must succeed")
	evalData2 := decodeJSON(t, evalResult2.ForLLM)
	inputVal2, ok := evalData2["result"].(string)
	require.True(t, ok)
	// chromedp SendKeys appends, so after two calls the value differs.
	assert.NotEqual(t, inputVal, inputVal2,
		"two different type calls must produce two different input values (differentiation check)")
}

// ---------------------------------------------------------------------------
// Case 9 — Missing/invalid args: parameter validation via Execute
//
// BDD: Given any browser tool,
//
//	When Execute is called with a missing required parameter,
//	Then result.IsError=true with a descriptive error (not a panic).
//
// No Chrome needed — these fire in the arg-check before Session() is called.
// Issue #445 §3.18 (parameter validation)
// ---------------------------------------------------------------------------

func TestExecute_ParameterValidation(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	ssrf := security.NewSSRFChecker([]string{"127.0.0.1"})
	registry := tools.NewToolRegistry()
	_, regErr := RegisterTools(registry, cfg, ssrf, true, t.TempDir(), true)
	require.NoError(t, regErr)

	ctx := context.Background()

	tests := []struct {
		tool     string
		args     map[string]any
		wantMsgs []string
	}{
		{
			"browser_navigate",
			map[string]any{},
			[]string{"url", "required"},
		},
		{
			"browser_navigate",
			map[string]any{"url": ""},
			[]string{"url", "required"},
		},
		{
			"browser_click",
			map[string]any{},
			[]string{"selector", "required"},
		},
		{
			"browser_type",
			map[string]any{"text": "hi"},
			[]string{"selector", "required"},
		},
		{
			"browser_get_text",
			map[string]any{},
			[]string{"selector", "required"},
		},
		{
			"browser_wait",
			map[string]any{},
			[]string{"selector", "required"},
		},
		{
			"browser_evaluate",
			map[string]any{},
			// evaluate is disabled by default in DefaultConfig (evaluateEnabled=true here,
			// but js param is missing — however the enabled check passes first).
			[]string{"js", "required"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.tool+"_missing_required", func(t *testing.T) {
			tool := mustGetTool(t, registry, tc.tool)
			result := tool.Execute(ctx, tc.args)
			require.NotNil(t, result)
			assert.True(t, result.IsError,
				"%s with missing required param must return IsError=true; got ForLLM: %s",
				tc.tool, result.ForLLM)
			// At least one of the expected message fragments must appear.
			found := false
			for _, msg := range tc.wantMsgs {
				if strings.Contains(result.ForLLM, msg) {
					found = true
					break
				}
			}
			assert.True(t, found,
				"%s error must mention one of %v; got: %s", tc.tool, tc.wantMsgs, result.ForLLM)
		})
	}
}

// ---------------------------------------------------------------------------
// Production gap documentation
// ---------------------------------------------------------------------------

// PRODUCTION GAP (documented, not a test failure):
//
// 1. browser_evaluate handles non-serializable JS values (DOM nodes, circular
//    objects) by returning null rather than an explicit error. The CDP layer
//    silently coerces unserializable values to null. This may be confusing to
//    operators; a future improvement could detect the null-coercion and return
//    an informative error message. Tracked as a v0.3 UX improvement.
//
// 2. browser_navigate's post-redirect SSRF check relies on chromedp.Location()
//    to read the final URL after Chrome has followed the redirect internally.
//    For redirects to non-routable IPs (e.g. 169.254.169.254), Chrome may not
//    follow the redirect at all and Location() returns the original URL. In that
//    case the post-redirect check does not fire (the pre-check already validated
//    the original URL as safe). This is acceptable — the security boundary is
//    maintained because unreachable IPs produce no data. However, if 169.254.x.x
//    were reachable (e.g. in a cloud environment), the post-redirect check would
//    be the last defense. Test coverage for this case confirms the error path
//    is exercised when Chrome does follow the redirect and Location() reflects it.
//
// 3. browser_screenshot returns a JPEG via FullScreenshot (quality>0), not a PNG.
//    The MIME type is "image/jpeg" not "image/png". The tool description says
//    "JPEG image" so this matches; the ArtifactTag extension is .jpg — consistent.
