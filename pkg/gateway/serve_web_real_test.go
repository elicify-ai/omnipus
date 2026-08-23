// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — serve_web real-serving integration tests (§3.2 / §9.3).
//
// Part B of the tool-test-plan-2026-06.md §3.2 (serve_web) requirement:
// "real static site + real vite build output through the preview listener".
//
// B1: Real static site — temp dir with index.html + app.js + style.css,
//     registered via ServedSubdirs.Register, served through HandlePreview.
//     Asserts HTTP 200, body content, and correct Content-Type per asset.
//
// B2: Real vite build output — fixtures committed in testdata/vite-dist/
//     (index.html + assets/index-<hash>.js + hashed CSS).  Served the same
//     way; fetch index.html, then the hashed JS asset.  Asserts JS MIME + 200.
//
//     An env-gated variant (OMNIPUS_VITE_E2E=1) runs a real "npx vite build"
//     on a tiny app fixture — kept out of the normal test run for CI speed.
//
// B3: Token expiry → 404; per-agent replacement (A 404s after B registers);
//     unknown token → 404.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.2 lines 65-69.
// Epic: #440 / issue: #443.

package gateway

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// testDataViteDist returns the absolute path to the committed vite dist
// fixture under testdata/vite-dist/.
func testDataViteDist(t *testing.T) string {
	t.Helper()
	// __file__ is in pkg/gateway/; testdata is a sibling.
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Join(filepath.Dir(file), "testdata", "vite-dist")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("vite-dist fixture not found at %s: %v", dir, err)
	}
	return dir
}

// newServeWebTestAPI returns a minimal restAPI wired with a real ServedSubdirs.
func newServeWebTestAPI(t *testing.T) (*restAPI, *agent.ServedSubdirs) {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	ss := agent.NewServedSubdirs()
	t.Cleanup(ss.Stop)

	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		servedSubdirs: ss,
	}
	return api, ss
}

// getFromAPI issues a GET request against HandlePreview and returns the
// recorder.
func getFromAPI(api *restAPI, urlPath string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, urlPath, nil)
	rec := httptest.NewRecorder()
	api.HandlePreview(rec, req)
	return rec
}

// ---------------------------------------------------------------------------
// B1: Real static site (temp dir with multiple assets)
// ---------------------------------------------------------------------------

// TestServeWeb_RealStaticSite_IndexHTML verifies that a temp dir registered
// via ServedSubdirs is served correctly through HandlePreview, returning 200
// with the correct body content and text/html Content-Type.
//
// Traces to: tool-test-plan-2026-06.md §3.2 bullet 1 (real static site E2E)
func TestServeWeb_RealStaticSite_IndexHTML(t *testing.T) {
	// BDD: Given a temp dir with index.html, app.js, and style.css
	// BDD: When registered via ServedSubdirs.Register and fetched via HandlePreview
	// BDD: Then GET /preview/{agent}/{token}/ → 200, body contains HTML content
	// BDD: Then the Content-Type is text/html
	// Traces to: tool-test-plan-2026-06.md §3.2 line 66

	api, ss := newServeWebTestAPI(t)

	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "index.html"),
		[]byte("<!doctype html><html><body>Hello Real Site</body></html>"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "app.js"),
		[]byte(`console.log("app loaded");`),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "style.css"),
		[]byte(`body { margin: 0; color: #333; }`),
		0o644,
	))

	token, _, err := ss.Register("site-agent-1", workDir, time.Hour)
	require.NoError(t, err)

	// --- Fetch index.html (implicit) ---
	rec := getFromAPI(api, "/preview/site-agent-1/"+token+"/")
	assert.Equal(t, http.StatusOK, rec.Code, "root URL must return 200")
	assert.Contains(t, rec.Body.String(), "Hello Real Site",
		"body must contain the index.html content")
	ct := rec.Header().Get("Content-Type")
	assert.Contains(t, ct, "text/html",
		"index.html must be served with text/html Content-Type")
}

// TestServeWeb_RealStaticSite_AppJS verifies that app.js is served with the
// correct application/javascript Content-Type.
//
// Traces to: tool-test-plan-2026-06.md §3.2 bullet 1 (per-asset Content-Type)
func TestServeWeb_RealStaticSite_AppJS(t *testing.T) {
	// BDD: Given a static site with app.js
	// BDD: When fetched via HandlePreview by path
	// BDD: Then Content-Type is application/javascript and body contains JS source
	// Traces to: tool-test-plan-2026-06.md §3.2 line 66

	api, ss := newServeWebTestAPI(t)

	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "index.html"),
		[]byte("<html><body>x</body></html>"),
		0o644,
	))
	jsContent := `console.log("hello from app.js");`
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "app.js"),
		[]byte(jsContent),
		0o644,
	))

	token, _, err := ss.Register("site-agent-2", workDir, time.Hour)
	require.NoError(t, err)

	rec := getFromAPI(api, "/preview/site-agent-2/"+token+"/app.js")
	assert.Equal(t, http.StatusOK, rec.Code, "app.js must return 200")
	assert.Contains(t, rec.Body.String(), "console.log",
		"body must contain JS source")
	ct := rec.Header().Get("Content-Type")
	assert.Contains(t, ct, "javascript",
		"app.js must be served with application/javascript Content-Type")
}

// TestServeWeb_RealStaticSite_StyleCSS verifies that style.css is served
// with text/css Content-Type.
//
// Traces to: tool-test-plan-2026-06.md §3.2 bullet 1 (per-asset Content-Type)
func TestServeWeb_RealStaticSite_StyleCSS(t *testing.T) {
	// BDD: Given a static site with style.css
	// BDD: When fetched via HandlePreview by path
	// BDD: Then Content-Type is text/css and body contains CSS rules
	// Traces to: tool-test-plan-2026-06.md §3.2 line 66

	api, ss := newServeWebTestAPI(t)

	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "index.html"),
		[]byte("<html><body>x</body></html>"),
		0o644,
	))
	cssContent := `body { margin: 0; } .app { color: red; }`
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "style.css"),
		[]byte(cssContent),
		0o644,
	))

	token, _, err := ss.Register("site-agent-3", workDir, time.Hour)
	require.NoError(t, err)

	rec := getFromAPI(api, "/preview/site-agent-3/"+token+"/style.css")
	assert.Equal(t, http.StatusOK, rec.Code, "style.css must return 200")
	assert.Contains(t, rec.Body.String(), "margin",
		"body must contain CSS rules")
	ct := rec.Header().Get("Content-Type")
	assert.Contains(t, ct, "css",
		"style.css must be served with text/css Content-Type")
}

// TestServeWeb_RealStaticSite_TwoAgentsDifferentContent is the differentiation
// test: two different agents with different content are served independently,
// proving the registry is not hardcoded.
//
// Traces to: anti-shortcut rule (differentiation test)
func TestServeWeb_RealStaticSite_TwoAgentsDifferentContent(t *testing.T) {
	// BDD: Given two agents each with different static sites registered
	// BDD: When each is fetched via HandlePreview
	// BDD: Then each returns its own distinct content (not the same response)
	// Traces to: tool-test-plan-2026-06.md §3.2 (differentiation test)

	api, ss := newServeWebTestAPI(t)

	dirA := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dirA, "index.html"),
		[]byte("Site Alpha"),
		0o644,
	))
	dirB := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dirB, "index.html"),
		[]byte("Site Beta"),
		0o644,
	))

	tokenA, _, err := ss.Register("alpha-agent", dirA, time.Hour)
	require.NoError(t, err)
	tokenB, _, err := ss.Register("beta-agent", dirB, time.Hour)
	require.NoError(t, err)

	recA := getFromAPI(api, "/preview/alpha-agent/"+tokenA+"/")
	recB := getFromAPI(api, "/preview/beta-agent/"+tokenB+"/")

	assert.Equal(t, http.StatusOK, recA.Code)
	assert.Equal(t, http.StatusOK, recB.Code)
	assert.Contains(t, recA.Body.String(), "Site Alpha")
	assert.Contains(t, recB.Body.String(), "Site Beta")

	if recA.Body.String() == recB.Body.String() {
		t.Fatal("differentiation failure: both agents returned the same content — hardcoded response bug")
	}
}

// ---------------------------------------------------------------------------
// B2: Real vite build output (committed fixture in testdata/vite-dist/)
// ---------------------------------------------------------------------------

// TestServeWeb_ViteBuildFixture_IndexHTML verifies that the committed vite-dist
// fixture's index.html is served correctly via HandlePreview with text/html
// Content-Type.
//
// Traces to: tool-test-plan-2026-06.md §3.2 bullet 2 (vite build output)
func TestServeWeb_ViteBuildFixture_IndexHTML(t *testing.T) {
	// BDD: Given the committed vite-dist fixture registered via ServedSubdirs
	// BDD: When the root path is fetched via HandlePreview
	// BDD: Then 200 is returned with the index.html content and text/html MIME
	// Traces to: tool-test-plan-2026-06.md §3.2 line 67

	api, ss := newServeWebTestAPI(t)
	viteDist := testDataViteDist(t)

	token, _, err := ss.Register("vite-agent", viteDist, time.Hour)
	require.NoError(t, err)

	rec := getFromAPI(api, "/preview/vite-agent/"+token+"/")
	assert.Equal(t, http.StatusOK, rec.Code, "vite dist index.html must return 200")
	body := rec.Body.String()
	assert.Contains(t, body, "Omnipus Test Vite App",
		"body must contain the vite app title from index.html")
	assert.Contains(t, body, "script",
		"body must contain a script tag referencing the JS bundle")
	ct := rec.Header().Get("Content-Type")
	assert.Contains(t, ct, "text/html",
		"index.html must be served with text/html Content-Type")
}

// TestServeWeb_ViteBuildFixture_HashedJS verifies that the hashed JS bundle
// (assets/index-BgFvOBkY.js) is served with application/javascript and 200.
// This is the critical case: the exact hashed filename must be served correctly.
//
// Traces to: tool-test-plan-2026-06.md §3.2 bullet 2 (hashed asset by exact path)
func TestServeWeb_ViteBuildFixture_HashedJS(t *testing.T) {
	// BDD: Given the committed vite-dist fixture registered
	// BDD: When the hashed JS asset is fetched by its exact path
	// BDD: Then 200 is returned with application/javascript Content-Type
	// Traces to: tool-test-plan-2026-06.md §3.2 line 67

	api, ss := newServeWebTestAPI(t)
	viteDist := testDataViteDist(t)

	token, _, err := ss.Register("vite-js-agent", viteDist, time.Hour)
	require.NoError(t, err)

	// The exact hashed filename from the fixture.
	rec := getFromAPI(api, "/preview/vite-js-agent/"+token+"/assets/index-BgFvOBkY.js")
	assert.Equal(t, http.StatusOK, rec.Code, "hashed JS asset must return 200")
	body := rec.Body.String()
	assert.Contains(t, body, "Omnipus Test Vite App",
		"body must contain JS content from the fixture")
	ct := rec.Header().Get("Content-Type")
	assert.Contains(t, ct, "javascript",
		"hashed JS asset must be served with application/javascript Content-Type")
}

// TestServeWeb_ViteBuildFixture_HashedCSS verifies that the hashed CSS bundle
// is served with text/css and 200.
//
// Traces to: tool-test-plan-2026-06.md §3.2 bullet 2 (hashed asset by exact path)
func TestServeWeb_ViteBuildFixture_HashedCSS(t *testing.T) {
	// BDD: Given the committed vite-dist fixture registered
	// BDD: When the hashed CSS asset is fetched by its exact path
	// BDD: Then 200 is returned with text/css Content-Type
	// Traces to: tool-test-plan-2026-06.md §3.2 line 67

	api, ss := newServeWebTestAPI(t)
	viteDist := testDataViteDist(t)

	token, _, err := ss.Register("vite-css-agent", viteDist, time.Hour)
	require.NoError(t, err)

	rec := getFromAPI(api, "/preview/vite-css-agent/"+token+"/assets/index-DrFu6DHB.css")
	assert.Equal(t, http.StatusOK, rec.Code, "hashed CSS asset must return 200")
	body := rec.Body.String()
	assert.Contains(t, body, "font-family",
		"body must contain CSS content from the fixture")
	ct := rec.Header().Get("Content-Type")
	assert.Contains(t, ct, "css",
		"hashed CSS asset must be served with text/css Content-Type")
}

// ---------------------------------------------------------------------------
// B2 env-gated: real `vite build` run (only when OMNIPUS_VITE_E2E=1)
// ---------------------------------------------------------------------------

// TestServeWeb_RealViteBuild_Gated is an env-gated test that runs a real
// `npx vite build` on a tiny fixture and serves the output through HandlePreview.
// Skipped unless OMNIPUS_VITE_E2E=1 is set.
//
// Traces to: tool-test-plan-2026-06.md §3.2 bullet 2 (real vite build case)
func TestServeWeb_RealViteBuild_Gated(t *testing.T) {
	// BDD: Given OMNIPUS_VITE_E2E=1 is set in the environment
	// BDD: Given a tiny vite app fixture on disk
	// BDD: When `npx vite build` is run and the dist/ output is registered
	// BDD: When the root URL is fetched via HandlePreview
	// BDD: Then 200 is returned with proper HTML content
	// Traces to: tool-test-plan-2026-06.md §3.2 line 67 (T3 real vite build)

	if os.Getenv("OMNIPUS_VITE_E2E") != "1" {
		t.Skip("skipping real vite build test; set OMNIPUS_VITE_E2E=1 to run")
	}

	// Write a minimal vite app fixture.
	appDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(appDir, "index.html"), []byte(`<!doctype html>
<html><head><title>Vite E2E</title><script type="module" src="/src/main.js"></script></head>
<body><div id="root">Vite E2E Works</div></body></html>`), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(appDir, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(appDir, "src", "main.js"),
		[]byte(`document.getElementById("root").textContent="Vite E2E Loaded";`),
		0o644))
	require.NoError(t, os.WriteFile(filepath.Join(appDir, "vite.config.js"),
		[]byte(`import { defineConfig } from "vite"; export default defineConfig({ build: { outDir: "dist" } });`),
		0o644))
	// Minimal package.json for vite.
	require.NoError(t, os.WriteFile(filepath.Join(appDir, "package.json"),
		[]byte(`{"name":"vite-e2e","private":true,"devDependencies":{"vite":"latest"}}`),
		0o644))

	// Install vite and build.
	t.Logf("running npm install in %s", appDir)
	npmInstall := exec.Command("npm", "install")
	npmInstall.Dir = appDir
	if out, err := npmInstall.CombinedOutput(); err != nil {
		t.Fatalf("npm install failed: %v\n%s", err, out)
	}
	t.Logf("running npx vite build in %s", appDir)
	viteBuild := exec.Command("npx", "vite", "build")
	viteBuild.Dir = appDir
	if out, err := viteBuild.CombinedOutput(); err != nil {
		t.Fatalf("vite build failed: %v\n%s", err, out)
	}

	distDir := filepath.Join(appDir, "dist")
	if _, err := os.Stat(distDir); err != nil {
		t.Fatalf("vite dist/ not found after build: %v", err)
	}

	api, ss := newServeWebTestAPI(t)
	token, _, err := ss.Register("vite-e2e-agent", distDir, time.Hour)
	require.NoError(t, err)

	rec := getFromAPI(api, "/preview/vite-e2e-agent/"+token+"/")
	assert.Equal(t, http.StatusOK, rec.Code, "vite build output must return 200")
	ct := rec.Header().Get("Content-Type")
	assert.Contains(t, ct, "text/html", "vite index.html must have text/html MIME")
}

// ---------------------------------------------------------------------------
// B3: Token expiry → 404; per-agent replacement; unknown token → 404
// ---------------------------------------------------------------------------

// TestServeWeb_TokenExpiry_Returns404 verifies that once a registration's
// deadline has passed, HandlePreview returns 404.
//
// Traces to: tool-test-plan-2026-06.md §3.2 bullet 3 (token expiry → 404)
func TestServeWeb_TokenExpiry_Returns404(t *testing.T) {
	// BDD: Given a registration with a 1ms lifetime
	// BDD: When the deadline passes and the URL is fetched
	// BDD: Then HandlePreview returns 404
	// Traces to: tool-test-plan-2026-06.md §3.2 line 68

	api, ss := newServeWebTestAPI(t)

	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "index.html"),
		[]byte("expire me"),
		0o644,
	))

	// Register with a very short TTL. FIX: this used to be time.Millisecond,
	// which raced the "before expiry" check below against the deadline
	// itself — constructing the httptest.ResponseRecorder, routing through
	// api.ServeHTTP, and resolving ServedSubdirs.Lookup all cost real wall
	// clock time, and on a slower/loaded CI runner (confirmed by the
	// Cross-Platform CI matrix, macos-latest arm64, where this whole
	// package's suite ran ~230s vs a much faster amd64 leg) that cost alone
	// could exceed 1ms, making the token already-expired by the time the
	// "must return 200 before expiry" request even fires — a pure
	// test-timing race, not an OS-specific filesystem/network semantic
	// (every other Register call in this file uses time.Hour). 150ms gives
	// the first request a comfortable margin while still keeping the test
	// fast and the BDD intent (a short-lived token expires, and the expired
	// token then 404s) unchanged.
	token, _, err := ss.Register("expire-agent", workDir, 150*time.Millisecond)
	require.NoError(t, err)

	// Ensure it works before expiry.
	recBefore := getFromAPI(api, "/preview/expire-agent/"+token+"/")
	assert.Equal(t, http.StatusOK, recBefore.Code, "must return 200 before expiry")

	// Wait past the deadline.
	time.Sleep(250 * time.Millisecond)

	// Now it must be 404.
	recAfter := getFromAPI(api, "/preview/expire-agent/"+token+"/")
	assert.Equal(t, http.StatusNotFound, recAfter.Code,
		"expired token must return 404 (ServedSubdirs.Lookup returns nil for expired entries)")
}

// TestServeWeb_PerAgentReplacement_OldToken404 verifies the per-agent cap:
// when agent A registers a new site, the previous token 404s immediately.
//
// Traces to: tool-test-plan-2026-06.md §3.2 bullet 3 (per-agent replacement)
func TestServeWeb_PerAgentReplacement_OldToken404(t *testing.T) {
	// BDD: Given agent A with an active registration (tokenA)
	// BDD: When agent A registers a new site (tokenB)
	// BDD: Then tokenA 404s immediately (per-agent cap: old registration invalidated)
	// BDD: And tokenB returns 200
	// Traces to: tool-test-plan-2026-06.md §3.2 line 68

	api, ss := newServeWebTestAPI(t)

	dirA := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dirA, "index.html"),
		[]byte("First site"),
		0o644,
	))
	dirB := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dirB, "index.html"),
		[]byte("Second site"),
		0o644,
	))

	// Register first site.
	tokenA, _, err := ss.Register("replace-agent", dirA, time.Hour)
	require.NoError(t, err)

	// Confirm first site works.
	recA1 := getFromAPI(api, "/preview/replace-agent/"+tokenA+"/")
	assert.Equal(t, http.StatusOK, recA1.Code, "first token must return 200 before replacement")

	// Register second site — atomically replaces tokenA.
	tokenB, _, err := ss.Register("replace-agent", dirB, time.Hour)
	require.NoError(t, err)

	// tokenA must now 404.
	recA2 := getFromAPI(api, "/preview/replace-agent/"+tokenA+"/")
	assert.Equal(t, http.StatusNotFound, recA2.Code,
		"old token (tokenA) must 404 after per-agent replacement")

	// tokenB must return 200 with the new content.
	recB := getFromAPI(api, "/preview/replace-agent/"+tokenB+"/")
	assert.Equal(t, http.StatusOK, recB.Code, "new token (tokenB) must return 200")
	assert.Contains(t, recB.Body.String(), "Second site",
		"new token must serve the new site content")
}

// TestServeWeb_UnknownToken_Returns404 verifies that an unregistered token
// always returns 404.
//
// Traces to: tool-test-plan-2026-06.md §3.2 bullet 3 (unknown token → 404)
func TestServeWeb_UnknownToken_Returns404(t *testing.T) {
	// BDD: Given a manager with no matching registration
	// BDD: When a random unknown token is fetched
	// BDD: Then HandlePreview returns 404
	// Traces to: tool-test-plan-2026-06.md §3.2 line 68

	api, _ := newServeWebTestAPI(t)

	rec := getFromAPI(api, "/preview/any-agent/totally-unknown-token-xyz123/")
	assert.Equal(t, http.StatusNotFound, rec.Code, "unknown token must return 404")
}

// ---------------------------------------------------------------------------
// B4: Real preview listener on an ephemeral port (end-to-end HTTP round-trip)
// ---------------------------------------------------------------------------

// TestServeWeb_PreviewListenerEndToEnd binds the preview handler on an
// ephemeral loopback port and performs a real HTTP GET, verifying that the
// full network round-trip works — not just httptest.NewRecorder.
//
// Traces to: tool-test-plan-2026-06.md §3.2 bullet 1 (bind preview listener)
func TestServeWeb_PreviewListenerEndToEnd(t *testing.T) {
	// BDD: Given a static site registered via ServedSubdirs
	// BDD: Given a real HTTP server bound on an ephemeral loopback port
	// BDD: When a real HTTP GET is issued to /preview/{agent}/{token}/
	// BDD: Then the response is HTTP 200 with the correct body
	// Traces to: tool-test-plan-2026-06.md §3.2 line 66

	api, ss := newServeWebTestAPI(t)

	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "index.html"),
		[]byte("<!doctype html><html><body>End-To-End Preview Works</body></html>"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "bundle.js"),
		[]byte(`window.__ready=true;`),
		0o644,
	))

	token, _, err := ss.Register("e2e-agent", workDir, time.Hour)
	require.NoError(t, err)

	// Bind a real listener on an ephemeral port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	lnAddr, lnAddrOk := ln.Addr().(*net.TCPAddr)
	require.True(t, lnAddrOk, "listener address must be a *net.TCPAddr")
	port := lnAddr.Port

	mux := http.NewServeMux()
	mux.HandleFunc("/preview/", api.HandlePreview)
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Fetch index.html
	resp, err := http.Get(baseURL + "/preview/e2e-agent/" + token + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), "End-To-End Preview Works")
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")

	// Fetch bundle.js
	resp2, err := http.Get(baseURL + "/preview/e2e-agent/" + token + "/bundle.js")
	require.NoError(t, err)
	defer resp2.Body.Close()
	body2, err := io.ReadAll(resp2.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
	assert.Contains(t, string(body2), "__ready")
	assert.Contains(t, resp2.Header.Get("Content-Type"), "javascript")

	// Unknown token must 404 even on the real listener.
	resp3, err := http.Get(baseURL + "/preview/e2e-agent/no-such-token-abc/")
	require.NoError(t, err)
	defer resp3.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp3.StatusCode)
}

// ---------------------------------------------------------------------------
// B5: Differentiation test for Content-Type: different file extensions produce
//     different MIME types, proving the content-type logic is not hardcoded.
// ---------------------------------------------------------------------------

// TestServeWeb_ContentType_DifferentExtensionsDifferentMIME verifies that
// different file extensions served by the same registration produce different
// Content-Type values — the hardcoded-response anti-pattern check.
//
// Traces to: anti-shortcut rule (differentiation test for Content-Type)
func TestServeWeb_ContentType_DifferentExtensionsDifferentMIME(t *testing.T) {
	// BDD: Given a site with index.html, app.js, and style.css
	// BDD: When each is fetched via HandlePreview
	// BDD: Then each returns a different Content-Type header
	// Traces to: tool-test-plan-2026-06.md §3.2 (anti-shortcut differentiation)

	api, ss := newServeWebTestAPI(t)

	workDir := t.TempDir()
	files := map[string]string{
		"index.html": "<!doctype html><html>hello</html>",
		"app.js":     `console.log("js");`,
		"style.css":  `body{margin:0}`,
	}
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(workDir, name), []byte(content), 0o644))
	}

	token, _, err := ss.Register("mime-test-agent", workDir, time.Hour)
	require.NoError(t, err)

	tests := []struct {
		path        string
		wantMIME    string
		wantNotMIME string
	}{
		{
			path:        "/preview/mime-test-agent/" + token + "/",
			wantMIME:    "text/html",
			wantNotMIME: "javascript",
		},
		{
			path:        "/preview/mime-test-agent/" + token + "/app.js",
			wantMIME:    "javascript",
			wantNotMIME: "text/html",
		},
		{
			path:        "/preview/mime-test-agent/" + token + "/style.css",
			wantMIME:    "css",
			wantNotMIME: "javascript",
		},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			rec := getFromAPI(api, tc.path)
			assert.Equal(t, http.StatusOK, rec.Code)
			ct := rec.Header().Get("Content-Type")
			assert.Contains(t, ct, tc.wantMIME,
				"expected Content-Type to contain %q for %s", tc.wantMIME, tc.path)
			assert.NotContains(t, ct, tc.wantNotMIME,
				"Content-Type must NOT contain %q for %s", tc.wantNotMIME, tc.path)
		})
	}

	// Final guard: all three Content-Types must be distinct.
	mimes := make([]string, 0, len(tests))
	for _, tc := range tests {
		rec := getFromAPI(api, tc.path)
		mimes = append(mimes, rec.Header().Get("Content-Type"))
	}
	seen := make(map[string]bool)
	for _, m := range mimes {
		seen[strings.Split(m, ";")[0]] = true
	}
	if len(seen) < 3 {
		t.Fatalf("expected 3 distinct Content-Types, got %v — hardcoded MIME bug", mimes)
	}
}
