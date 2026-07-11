package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// inspect_test.go — ADR-039 D-B3 (best-effort DOM inspect) tests for
// pkg/tools/browser/inspect.go.
//
// Covers the logic that does NOT require a real Chromium (formatCoord,
// InspectPoint's session-resolution error path — forced via an invalid
// exec_path so no network/browser is touched) as fast unit tests, plus a
// real-Chromium E2E pair gated by skipIfNoBrowser (browser_e2e_test.go's
// established convention) for the actual DOM resolution behavior. The
// latter SKIP in any environment without a working Chromium/Chrome binary —
// exactly this devpod, whose /usr/bin/chromium-browser is an Ubuntu snap
// stub that fails --version — so full coverage of the real
// document.elementFromPoint path is a UAT-matrix item (issue tracked
// separately per ADR-039's implementation plan), not something this suite
// can assert without a real browser.
//
// Traces to: docs/internal/architecture/ADR-039-user-initiated-browsing-and-annotate.md (D-B3)

// --- formatCoord (pure function) ---

func TestFormatCoord(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want string
	}{
		{"zero", 0, "0"},
		{"integer", 42, "42"},
		{"fractional", 12.5, "12.5"},
		{"large", 1920.75, "1920.75"},
		{"tiny fraction", 0.001, "0.001"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatCoord(c.in)
			assert.Equal(t, c.want, got)
			// Must always be usable, unquoted, inside
			// document.elementFromPoint(X, Y) — never quotes/apostrophes that
			// could break out of the numeric literal position.
			assert.NotContains(t, got, `"`)
			assert.NotContains(t, got, `'`)
		})
	}
}

// --- InspectPoint: session resolution failure (no real browser needed) ---

// TestInspectPoint_SessionError verifies that when the manager cannot even
// produce a tab session (ensureStarted fails — forced here via an invalid
// exec_path, so no real Chromium process or network call is ever attempted),
// InspectPoint returns a non-nil, wrapped error rather than silently
// reporting Ok:false. This is the ONE failure mode InspectPoint treats as a
// hard error rather than a best-effort "nothing resolved" — see
// InspectPoint's doc comment: every OTHER failure (no element, a CDP/eval
// error against an already-resolved tab) degrades to Ok:false, err:nil.
func TestInspectPoint_SessionError(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := BrowserConfig{
		ProfileDir:  filepath.Join(tmpDir, "profile"),
		ExecPath:    filepath.Join(tmpDir, "no-such-chromium-binary"),
		PageTimeout: 2 * time.Second,
		MaxTabs:     5,
	}
	mgr, err := NewBrowserManager(cfg, security.NewSSRFChecker(nil))
	require.NoError(t, err)

	result, err := mgr.InspectPoint(10, 10)
	require.Error(t, err, "an unresolvable tab session must surface as a real error, not a soft ok:false")
	assert.Contains(t, err.Error(), "browser: inspect: cannot resolve session")
	assert.False(t, result.Ok)
	assert.Empty(t, result.Tag)
	assert.Empty(t, result.Text)
	assert.Empty(t, result.Html)
}

// --- InspectPoint: real DOM resolution (requires a working Chromium) ---

const inspectTestHTML = `<!DOCTYPE html>
<html>
<head><title>Inspect Test</title></head>
<body style="margin:0">
  <button id="target" style="position:absolute;left:10px;top:10px;width:100px;height:40px;">Click me</button>
</body>
</html>`

func startInspectTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, inspectTestHTML)
	})
	return httptest.NewServer(mux)
}

// newInspectTestManager builds a real BrowserManager (managed/headless mode)
// against a temp profile dir. skipIfNoBrowser must have already been called
// by the caller.
func newInspectTestManager(t *testing.T) *BrowserManager {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := BrowserConfig{
		ProfileDir:  filepath.Join(tmpDir, "profile"),
		Headless:    true,
		PageTimeout: 15 * time.Second,
		MaxTabs:     5,
	}
	mgr, err := NewBrowserManager(cfg, security.NewSSRFChecker(nil))
	require.NoError(t, err)
	t.Cleanup(mgr.Shutdown)
	return mgr
}

// TestInspectPoint_ResolvesElementAtPoint is a real-Chromium E2E check
// (gated by skipIfNoBrowser, matching this package's established convention
// — see browser_e2e_test.go) that InspectPoint resolves the actual button
// element at its known on-screen coordinate: correct tag, innerText, and an
// outerHTML that actually contains the element.
// BDD: Given a page with a <button> at a known box,
// When InspectPoint is called with a point inside that box,
// Then Ok=true, tag="button", text contains "Click me", html contains "<button".
func TestInspectPoint_ResolvesElementAtPoint(t *testing.T) {
	skipIfNoBrowser(t)

	srv := startInspectTestServer(t)
	defer srv.Close()

	mgr := newInspectTestManager(t)

	tabCtx, err := mgr.Session(DefaultSessionID)
	require.NoError(t, err)
	navCtx, cancel := context.WithTimeout(tabCtx, mgr.PageTimeout())
	defer cancel()
	require.NoError(t, chromedp.Run(navCtx, chromedp.Navigate(srv.URL)))

	result, err := mgr.InspectPoint(30, 20) // inside the button's [10,10]-[110,50] box
	require.NoError(t, err)
	require.True(t, result.Ok)
	assert.Equal(t, "button", result.Tag)
	assert.Contains(t, result.Text, "Click me")
	assert.Contains(t, strings.ToLower(result.Html), "<button")
}

// TestInspectPoint_NoElementAtPoint verifies the best-effort "nothing here"
// outcome: a point with no element under it (deep off-page) resolves to
// InspectResult{Ok:false}, err=nil — never an error — per InspectPoint's
// documented best-effort contract.
// BDD: Given a loaded page,
// When InspectPoint is called with a point far outside the viewport,
// Then Ok=false and err is nil.
func TestInspectPoint_NoElementAtPoint(t *testing.T) {
	skipIfNoBrowser(t)

	srv := startInspectTestServer(t)
	defer srv.Close()

	mgr := newInspectTestManager(t)

	tabCtx, err := mgr.Session(DefaultSessionID)
	require.NoError(t, err)
	navCtx, cancel := context.WithTimeout(tabCtx, mgr.PageTimeout())
	defer cancel()
	require.NoError(t, chromedp.Run(navCtx, chromedp.Navigate(srv.URL)))

	result, err := mgr.InspectPoint(999999, 999999)
	require.NoError(t, err, "no element at the point is a soft outcome, not an error")
	assert.False(t, result.Ok)
	assert.Empty(t, result.Tag)
}
