package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const testHTML = `<!DOCTYPE html>
<html>
<head><title>Omnipus E2E Test Page</title></head>
<body>
  <h1 id="heading">Hello from Omnipus</h1>
  <button id="toggle" onclick="document.getElementById('result').style.display='block'">Show Result</button>
  <div id="result" style="display:none">Toggle worked!</div>
  <form action="/submitted" method="GET">
    <input id="name" name="name" type="text" placeholder="Enter name" />
    <button type="submit" id="submit">Submit</button>
  </form>
</body>
</html>`

const submittedHTML = `<!DOCTYPE html>
<html>
<head><title>Submitted</title></head>
<body>
  <p id="greeting">Hello, %s</p>
</body>
</html>`

func skipIfNoBrowser(t *testing.T) {
	t.Helper()
	// CI environments often have Chrome installed but the sandbox fails in
	// containers (Zygote initialization crash). Skip in CI unless the operator
	// explicitly opts in via OMNIPUS_BROWSER_E2E=1.
	if os.Getenv("CI") != "" && os.Getenv("OMNIPUS_BROWSER_E2E") == "" {
		t.Skip("skipping browser E2E test in CI — set OMNIPUS_BROWSER_E2E=1 to enable")
	}
	// LookPath finds candidate executables, but Ubuntu ships a snap stub at
	// /usr/bin/chromium-browser that exits 1 on launch (it only prints a
	// "please install via snap" message). Probe each candidate with --version
	// and only accept it if the probe exits 0. Skip when no candidate probes
	// successfully so tests SKIP instead of failing with a "Zygote" crash.
	for _, name := range []string{"chromium-browser", "chromium", "google-chrome", "google-chrome-stable"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		probe := exec.Command(path, "--version")
		if probe.Run() == nil {
			return // real browser found
		}
	}
	t.Skip("skipping browser E2E test: no working Chromium/Chrome binary found in PATH")
}

func startTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, testHTML)
	})
	mux.HandleFunc("/submitted", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, submittedHTML, name)
	})
	return httptest.NewServer(mux)
}

// TestBrowserTools_E2E_DirectChromedp exercises the browser manager and all browser
// actions (navigate, get_text, click, wait, type, screenshot, evaluate) using chromedp
// directly through the BrowserManager. This proves the managed Chromium lifecycle,
// SSRF validation, session management, and all DOM interactions work end-to-end.
//
// BDD: Given a local HTTP server with a heading, toggle button, and form,
//
//	When chromedp actions are executed via BrowserManager sessions,
//	Then navigation loads the page, text extraction reads DOM content,
//	click toggles element visibility, type fills input fields,
//	form submission navigates to the result page with correct greeting,
//	screenshot produces a real PNG file, and JS evaluation returns values.
//
// Traces to: wave4-whatsapp-browser-spec.md US-4 (managed mode), US-5 (action primitives)
func TestBrowserTools_E2E_DirectChromedp(t *testing.T) {
	skipIfNoBrowser(t)

	srv := startTestServer(t)
	defer srv.Close()

	// Use a manual temp dir instead of t.TempDir() because Chrome's profile
	// directory may have lingering files that t.TempDir cleanup can't remove.
	profileDir, err := os.MkdirTemp("", "omnipus-browser-e2e-*")
	require.NoError(t, err)
	defer os.RemoveAll(profileDir)

	cfg := BrowserConfig{
		Enabled:     true,
		Headless:    true,
		PageTimeout: 15 * time.Second,
		MaxTabs:     3,
		ProfileDir:  profileDir,
	}
	ssrf := security.NewSSRFChecker([]string{"127.0.0.1"})

	mgr, err := NewBrowserManager(cfg, ssrf)
	require.NoError(t, err)
	defer mgr.Shutdown()

	// Get a session (creates a browser tab).
	tabCtx, err := mgr.Session("e2e-test")
	require.NoError(t, err)

	// 1. Navigate
	var title string
	err = chromedp.Run(tabCtx,
		chromedp.Navigate(srv.URL),
		chromedp.Title(&title),
	)
	require.NoError(t, err, "navigate must succeed")
	assert.Equal(t, "Omnipus E2E Test Page", title)
	t.Log("1. navigate: OK")

	// 2. Get text from heading
	var headingText string
	err = chromedp.Run(tabCtx,
		chromedp.Text("#heading", &headingText, chromedp.ByQuery),
	)
	require.NoError(t, err, "get_text heading must succeed")
	assert.Equal(t, "Hello from Omnipus", headingText)
	t.Log("2. get_text(heading): OK")

	// 3. Click toggle button
	err = chromedp.Run(tabCtx,
		chromedp.Click("#toggle", chromedp.ByQuery),
	)
	require.NoError(t, err, "click toggle must succeed")
	t.Log("3. click(toggle): OK")

	// 4. Wait for revealed element
	err = chromedp.Run(tabCtx,
		chromedp.WaitVisible("#result", chromedp.ByQuery),
	)
	require.NoError(t, err, "wait for #result must succeed")
	t.Log("4. wait(result): OK")

	// 5. Read revealed content
	var resultText string
	err = chromedp.Run(tabCtx,
		chromedp.Text("#result", &resultText, chromedp.ByQuery),
	)
	require.NoError(t, err, "get_text result must succeed")
	assert.Equal(t, "Toggle worked!", resultText)
	t.Log("5. get_text(result): OK")

	// 6. Type into form input
	err = chromedp.Run(tabCtx,
		chromedp.SendKeys("#name", "Omnipus", chromedp.ByQuery),
	)
	require.NoError(t, err, "type into #name must succeed")
	t.Log("6. type(name): OK")

	// 7. Submit form
	err = chromedp.Run(tabCtx,
		chromedp.Click("#submit", chromedp.ByQuery),
		chromedp.WaitReady("body", chromedp.ByQuery),
	)
	require.NoError(t, err, "click submit must succeed")
	t.Log("7. click(submit): OK")

	// 8. Read greeting after form submission
	var greeting string
	err = chromedp.Run(tabCtx,
		chromedp.Text("#greeting", &greeting, chromedp.ByQuery),
	)
	require.NoError(t, err, "get_text greeting must succeed")
	assert.Equal(t, "Hello, Omnipus", greeting)
	t.Log("8. get_text(greeting): OK")

	// 9. Screenshot
	var screenshotBuf []byte
	err = chromedp.Run(tabCtx,
		chromedp.FullScreenshot(&screenshotBuf, 90),
	)
	require.NoError(t, err, "screenshot must succeed")
	assert.Greater(t, len(screenshotBuf), 100, "screenshot must produce non-trivial PNG data")
	t.Log("9. screenshot: OK")

	// 10. Evaluate JS
	var evalTitle string
	err = chromedp.Run(tabCtx,
		chromedp.Evaluate("document.title", &evalTitle),
	)
	require.NoError(t, err, "evaluate must succeed")
	assert.Equal(t, "Submitted", evalTitle)
	t.Log("10. evaluate(document.title): OK")
}

// TestBrowserToolRegistration_WithScope verifies that all 7 registered browser tools
// have the correct scope (ScopeCore) for the per-agent tool visibility system.
//
// Traces to: PR #41 tool visibility, PR #22 browser wiring
func TestBrowserToolRegistration_WithScope(t *testing.T) {
	registry := tools.NewToolRegistry()
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	ssrf := security.NewSSRFChecker(nil)
	// evaluateEnabled=true: include browser_evaluate in the expected tool set.
	_, err = RegisterTools(registry, cfg, ssrf, true, t.TempDir(), true)
	require.NoError(t, err)

	expectedTools := []string{
		"browser_navigate", "browser_click", "browser_type",
		"browser_screenshot", "browser_get_text", "browser_wait", "browser_evaluate",
	}
	for _, name := range expectedTools {
		tool, ok := registry.Get(name)
		require.True(t, ok, "tool %q must be registered", name)
		assert.Equal(t, tools.ScopeCore, tool.Scope(),
			"tool %q must have ScopeCore for per-agent visibility", name)
	}
}

// TestBrowserToolsAlwaysRegisterRegardlessOfLegacyFlag verifies the post-refactor
// contract: RegisterTools always adds browser tools to the registry. The legacy
// BrowserConfig.Enabled flag is retained for back-compat but has no runtime
// effect on registration. Whether an agent can actually INVOKE browser tools is
// decided by the policy engine (pkg/policy).
func TestBrowserToolsAlwaysRegisterRegardlessOfLegacyFlag(t *testing.T) {
	cfg := BrowserConfig{Enabled: false}
	ssrf := security.NewSSRFChecker(nil)
	registry := tools.NewToolRegistry()

	// RegisterTools succeeds; the browser manager is created but Chromium is
	// not launched until the first tool is invoked (lazy start).
	// evaluateEnabled=true: explicitly opt in to browser_evaluate registration.
	mgr, err := RegisterTools(registry, cfg, ssrf, true, t.TempDir(), true)
	require.NoError(t, err)
	require.NotNil(t, mgr)

	// The tools are registered regardless of the legacy Enabled flag — the live
	// tool-name policy (pkg/tools.FilterToolsByPolicy, allow/ask/deny) governs
	// access for ordinary browser tools; browser_evaluate additionally has its own
	// executeEnabled gate (see below, #438).
	tool, ok := registry.Get("browser_navigate")
	assert.True(t, ok, "RegisterTools registers tools regardless of Enabled flag")
	assert.NotNil(t, tool)

	// browser_evaluate is always registered (so the LLM sees it); invocation is
	// gated solely by the tool's executeEnabled check (deny-by-default, #438, #70).
	evalTool, ok := registry.Get("browser_evaluate")
	assert.True(t, ok, "browser_evaluate stays registered when evaluateEnabled=true; executeEnabled gates invocation")
	assert.NotNil(t, evalTool)
}

// TestSSRFBlocksPrivateNavigation verifies that browser_navigate rejects private IPs.
func TestSSRFBlocksPrivateNavigation(t *testing.T) {
	skipIfNoBrowser(t)

	cfg := BrowserConfig{
		Enabled:     true,
		Headless:    true,
		PageTimeout: 5 * time.Second,
		MaxTabs:     1,
		ProfileDir:  t.TempDir(),
	}
	// SSRF checker with NO whitelist — private IPs are blocked.
	ssrf := security.NewSSRFChecker(nil)

	registry := tools.NewToolRegistry()
	// evaluateEnabled=false: this test only uses browser_navigate, so no need
	// to register browser_evaluate.
	mgr, err := RegisterTools(registry, cfg, ssrf, false, t.TempDir(), true)
	require.NoError(t, err)
	defer mgr.Shutdown()

	tool, ok := registry.Get("browser_navigate")
	require.True(t, ok)

	result := tool.Execute(context.Background(), map[string]any{
		"url": "http://192.168.1.1/admin",
	})
	require.NotNil(t, result)
	assert.True(t, result.IsError, "navigating to a private IP must be blocked by SSRF checker")
	msg := result.ForLLM
	ssrfBlocked := strings.Contains(msg, "SSRF") ||
		strings.Contains(msg, "blocked") ||
		strings.Contains(msg, "private")
	assert.True(t, ssrfBlocked, "error should mention SSRF/blocked/private, got: %s", msg)
}
