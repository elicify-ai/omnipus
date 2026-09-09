package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/captureext"
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

// macOSChromeAppPaths lists the standard install locations for Chrome/
// Chromium's .app bundle executable on macOS (Homebrew cask, manual
// download). Unlike Linux, these are essentially never on $PATH — a plain
// `exec.LookPath("google-chrome")` always misses them — so they need their
// own direct probe rather than relying on the PATH-name loop below (#615/
// #617/#618 hardening review, F1: the original candidate list had "no macOS
// .app entry").
var macOSChromeAppPaths = []string{
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
}

func skipIfNoBrowser(t *testing.T) {
	t.Helper()
	// CI environments often have Chrome installed but the sandbox fails in
	// containers (Zygote initialization crash). Skip in CI unless the operator
	// explicitly opts in via OMNIPUS_BROWSER_E2E=1 — the gateway's own
	// --no-sandbox default (chromeHardeningBaseFlags, exec_resolver.go)
	// mitigates the crash for the managed launch path, so this opt-in exists
	// to keep an operator's explicit consent for spending CI minutes on a
	// real Chrome launch, not because the crash is still expected.
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
	// macOS: probe the .app bundle locations directly (never reached via
	// LookPath above).
	if runtime.GOOS == "darwin" {
		for _, path := range macOSChromeAppPaths {
			probe := exec.Command(path, "--version")
			if probe.Run() == nil {
				return
			}
		}
	}
	// Nothing usable on PATH (or, on macOS, in the standard .app locations).
	// Fall back to the same managed-install/download resolution the
	// coordinator tests already use (resolveTestBinary, coordinator_test.go):
	// it prefers an already-installed ~/.omnipus/browser/chromium before
	// ever touching the network, and its result is cached (sync.Once) for
	// the whole test binary run, so this costs nothing extra when another
	// test in the same run has already resolved (or downloaded) a binary.
	//
	// This closes the local-developer regression #615 introduced: before
	// this fallback, a developer with a working MANAGED Chrome install (no
	// PATH entry at all — the common case, since `omnipus` never adds one)
	// was silently skipped by the PATH-only probe above, even though the
	// exact same binary was already one call away. resolveTestBinary itself
	// calls t.Skipf (via this t) when even the managed/download path comes
	// up empty, so this call either returns having found something, or ends
	// the test here with a clear skip reason — there is no path back into
	// this function afterward.
	resolveTestBinary(t)
}

// requireBrowserOrFail is skipIfNoBrowser's counterpart for MEASUREMENT
// GATES: it resolves a real Chrome the same three ways skipIfNoBrowser does,
// but when none can be obtained it calls t.Fatalf — it NEVER calls t.Skip.
// It returns the resolved executable path so the caller can pin
// BrowserConfig.ExecPath to the exact binary that was proven to run.
//
// Why a second helper rather than a flag on the first: skipIfNoBrowser is
// right for ordinary coverage (an absent browser is an environment fact, not
// a code defect, and skipping keeps a laptop without Chrome usable). It is
// exactly wrong for a gate. A gate exists to answer a yes/no question about
// reality before a design is allowed to proceed; a gate that reports green
// because it never ran has answered nothing while looking identical to an
// answer. skipIfNoBrowser has TWO independent skip paths — the
// CI-without-OMNIPUS_BROWSER_E2E branch, and resolveTestBinary's own
// t.Skipf when even the managed install/download comes up empty — and both
// of them exit 0. Any gate wired through it can therefore report success
// having launched no browser at all.
//
// Deliberate differences from skipIfNoBrowser:
//   - No CI / OMNIPUS_BROWSER_E2E branch. A gate's verdict must not depend
//     on an opt-in env var: unset, the answer would be "skipped", not "no".
//   - resolveTestBinary is NOT called, even though this duplicates its two
//     resolution steps. That helper's failure path is t.Skipf, and calling
//     it would reintroduce the exact skip this helper exists to remove.
//   - It resolves through the same STABLE shared install dirs
//     resolveTestBinary uses (~/.omnipus/browser/chromium first, then
//     os.TempDir()/omnipus-shared-test-chromium), so a run that has already
//     paid for a managed Chrome download reuses it and costs nothing extra.
//
// Note for scripts/check-browser-tests-gated.sh: that guard requires every
// test calling newCoordinatorTestConfig / resolveTestBinary /
// resolveTestBinaryHeadlessShell to also call skipIfNoBrowser. Gate tests
// using this helper must call NONE of those three (build the BrowserConfig
// literal inline instead) — not to evade the guard, but because a gate must
// not be gated by a skip. The guard's purpose (no undeclared ~100 MB
// download from an ordinary test) is preserved here: this helper prefers an
// already-installed browser and only downloads as its last resort, in a
// test whose whole job is to launch real Chrome.
func requireBrowserOrFail(t *testing.T) string {
	t.Helper()

	// Source 1: $PATH, probed with --version (an Ubuntu snap stub resolves
	// but exits 1 — see skipIfNoBrowser's note).
	for _, name := range []string{"chromium-browser", "chromium", "google-chrome", "google-chrome-stable"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if exec.Command(path, "--version").Run() == nil {
			return path
		}
	}

	// Source 2: macOS .app bundles, which LookPath never finds.
	if runtime.GOOS == "darwin" {
		for _, path := range macOSChromeAppPaths {
			if exec.Command(path, "--version").Run() == nil {
				return path
			}
		}
	}

	// Source 3a: this codebase's managed install root.
	if home, err := os.UserHomeDir(); err == nil {
		if platform, perr := cftPlatform(); perr == nil {
			installRoot := filepath.Join(home, ".omnipus", "browser", "chromium")
			if bin := findInstalledBinary(installRoot, platform); bin != "" {
				return bin
			}
		}
	}

	// Source 3b: download Chrome for Testing into the same stable shared dir
	// resolveTestBinary uses, so repeat runs reuse the install.
	bin, err := EnsureChromium(
		context.Background(), filepath.Join(os.TempDir(), "omnipus-shared-test-chromium"),
	)
	if err != nil {
		t.Fatalf(
			"requireBrowserOrFail: no real Chrome could be obtained from $PATH, the macOS .app "+
				"locations, the managed install root, or a Chrome-for-Testing download: %v\n"+
				"This is a measurement gate: it must FAIL rather than skip, because a gate that "+
				"did not run has answered nothing.", err,
		)
	}
	if bin == "" {
		t.Fatal(
			"requireBrowserOrFail: Chrome-for-Testing resolution returned an empty path with no error " +
				"— refusing to report a gate as passed against a browser that does not exist",
		)
	}
	return bin
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
		Enabled:         true,
		Headless:        true,
		PageTimeout:     15 * time.Second,
		ProfileDir:      profileDir,
		TrustPathChrome: true, // skipIfNoBrowser already probed $PATH Chrome
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
	err = chromedp.Run(
		tabCtx,
		chromedp.Navigate(srv.URL),
		chromedp.Title(&title),
	)
	require.NoError(t, err, "navigate must succeed")
	assert.Equal(t, "Omnipus E2E Test Page", title)
	t.Log("1. navigate: OK")

	// 2. Get text from heading
	var headingText string
	err = chromedp.Run(
		tabCtx,
		chromedp.Text("#heading", &headingText, chromedp.ByQuery),
	)
	require.NoError(t, err, "get_text heading must succeed")
	assert.Equal(t, "Hello from Omnipus", headingText)
	t.Log("2. get_text(heading): OK")

	// 3. Click toggle button
	err = chromedp.Run(
		tabCtx,
		chromedp.Click("#toggle", chromedp.ByQuery),
	)
	require.NoError(t, err, "click toggle must succeed")
	t.Log("3. click(toggle): OK")

	// 4. Wait for revealed element
	err = chromedp.Run(
		tabCtx,
		chromedp.WaitVisible("#result", chromedp.ByQuery),
	)
	require.NoError(t, err, "wait for #result must succeed")
	t.Log("4. wait(result): OK")

	// 5. Read revealed content
	var resultText string
	err = chromedp.Run(
		tabCtx,
		chromedp.Text("#result", &resultText, chromedp.ByQuery),
	)
	require.NoError(t, err, "get_text result must succeed")
	assert.Equal(t, "Toggle worked!", resultText)
	t.Log("5. get_text(result): OK")

	// 6. Type into form input
	err = chromedp.Run(
		tabCtx,
		chromedp.SendKeys("#name", "Omnipus", chromedp.ByQuery),
	)
	require.NoError(t, err, "type into #name must succeed")
	t.Log("6. type(name): OK")

	// 7. Submit form
	err = chromedp.Run(
		tabCtx,
		chromedp.Click("#submit", chromedp.ByQuery),
		chromedp.WaitReady("body", chromedp.ByQuery),
	)
	require.NoError(t, err, "click submit must succeed")
	t.Log("7. click(submit): OK")

	// 8. Read greeting after form submission
	var greeting string
	err = chromedp.Run(
		tabCtx,
		chromedp.Text("#greeting", &greeting, chromedp.ByQuery),
	)
	require.NoError(t, err, "get_text greeting must succeed")
	assert.Equal(t, "Hello, Omnipus", greeting)
	t.Log("8. get_text(greeting): OK")

	// 9. Screenshot
	var screenshotBuf []byte
	err = chromedp.Run(
		tabCtx,
		chromedp.FullScreenshot(&screenshotBuf, 90),
	)
	require.NoError(t, err, "screenshot must succeed")
	assert.Greater(t, len(screenshotBuf), 100, "screenshot must produce non-trivial PNG data")
	t.Log("9. screenshot: OK")

	// 10. Evaluate JS
	var evalTitle string
	err = chromedp.Run(
		tabCtx,
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
	_, err = registerToolsForTest(t, registry, cfg, ssrf, true, t.TempDir(), true)
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
	mgr, err := registerToolsForTest(t, registry, cfg, ssrf, true, t.TempDir(), true)
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
		Enabled:         true,
		Headless:        true,
		PageTimeout:     5 * time.Second,
		ProfileDir:      t.TempDir(),
		TrustPathChrome: true, // skipIfNoBrowser already probed $PATH Chrome
	}
	// SSRF checker with NO whitelist — private IPs are blocked.
	ssrf := security.NewSSRFChecker(nil)

	registry := tools.NewToolRegistry()
	// evaluateEnabled=false: this test only uses browser_navigate, so no need
	// to register browser_evaluate.
	mgr, err := registerToolsForTest(t, registry, cfg, ssrf, false, t.TempDir(), true)
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

// ---------------------------------------------------------------------------
// G-2 — the mechanical gate for the per-workspace browser pool (ADR-075 /
// browser-workspace-ownership-spec FR-045).
// ---------------------------------------------------------------------------

// spikeCaptureResult is what TestSpike_CaptureAgainstSecondChrome's in-page
// probe hands back. Every field is reported in the failure message, because
// the only thing worse than this gate failing is this gate failing without
// saying which step failed.
type spikeCaptureResult struct {
	OK          bool     `json:"ok"`
	Error       string   `json:"error"`
	StreamID    string   `json:"streamId"`
	VideoTracks int      `json:"videoTracks"`
	ReadyState  string   `json:"readyState"`
	TrackLabel  string   `json:"trackLabel"`
	TargetURL   string   `json:"targetUrl"`
	TabURLs     []string `json:"tabUrls"`
}

// spikeLaunchChrome launches ONE real Chrome process with its OWN
// --user-data-dir and the real capture extension configured, and returns the
// coordinator plus the profile directory that became that process's
// --user-data-dir (coordinator.go's launchChrome passes cfg.ProfileDir
// straight through to pipeLaunchConfig.userDataDir).
//
// It deliberately builds the BrowserConfig literal inline rather than calling
// newCoordinatorTestConfig: that helper is one of the three markers
// scripts/check-browser-tests-gated.sh keys on, and a gate must not be gated
// by skipIfNoBrowser. ExecPath is pinned to the binary requireBrowserOrFail
// already proved runs, so no resolution happens here at all.
//
// The profile dir uses os.MkdirTemp + an explicit RemoveAll rather than
// t.TempDir(), matching TestBrowserTools_E2E_DirectChromedp: a Chrome profile
// can outlive the test with files t.TempDir()'s cleanup then fails on.
func spikeLaunchChrome(t *testing.T, label, execPath, extDir string) (*BrowserCoordinator, string) {
	t.Helper()

	home, err := os.MkdirTemp("", "omnipus-spike-"+label+"-*")
	require.NoError(t, err, "temp home for %s Chrome", label)
	t.Cleanup(func() { _ = os.RemoveAll(home) })

	profileDir := filepath.Join(home, "browser", "profiles", "default")
	cfg := BrowserConfig{
		Enabled:     true,
		Headless:    true,
		PageTimeout: 30 * time.Second,
		ProfileDir:  profileDir,
		ExecPath:    execPath,
		// The REAL capture extension, not a minimal stand-in: the gate's
		// question is about chrome.tabCapture as this product actually calls
		// it, from the page this product actually loads.
		ExtensionDir:    extDir,
		ExtensionID:     captureext.ExtensionID,
		TrustPathChrome: true, // requireBrowserOrFail already probed this binary
	}

	coord := NewBrowserCoordinator(home, cfg)
	t.Cleanup(coord.Shutdown)

	launchCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	require.NoError(t, coord.ensureLaunched(launchCtx), "launch the %s Chrome process", label)

	return coord, profileDir
}

// TestSpike_CaptureAgainstSecondChrome is gate G-2 (FR-045, SC-012a). It
// blocks the entire per-workspace browser pool: if it fails, the pool's
// central design premise is false and the design must change, not the test.
//
// The premise under test. ADR-048 established, against real Chrome, that
// chrome.tabCapture CANNOT capture a tab living in a CDP-CREATED browser
// context (Target.createBrowserContext): getMediaStreamId fails with
// "Invalid tab specified." regardless of enableInIncognito. That is why the
// current single-Chrome design has to put both the encoder page and the
// agent's own tab in the ONE default browser context, and it is the reason
// per-agent CDP contexts cannot be the isolation mechanism. ADR-075's pool
// replaces CDP contexts with one Chrome PROCESS per workspace, each with its
// own --user-data-dir profile — and rests entirely on the claim that a tab in
// a SEPARATE PROCESS's own default context does NOT inherit that restriction.
// Nothing had ever tested that claim. This test does.
//
// What it proves, in order:
//  1. Two Chrome PROCESSES are alive at once (distinct, non-zero pids), each
//     having written into its OWN --user-data-dir.
//  2. The second Chrome loaded the real capture extension.
//  3. The extension in Chrome #2 sees ONLY Chrome #2's tabs — Chrome #1's tab
//     (marked in=chrome-one) is not in chrome.tabs.query's result. Process
//     separation is real, not nominal.
//  4. chrome.tabCapture.getMediaStreamId SUCCEEDS for a tab in Chrome #2's
//     DEFAULT browser context — the only context there is, since ADR-075
//     FR-031 deleted the CDP-browser-context mechanism outright — and the
//     returned id yields a getUserMedia MediaStream carrying a LIVE video
//     track.
//
// (4) is the load-bearing assertion. The stream id alone is not enough: the
// id is what the historical failure mode rejected, but a live track is what
// proves the capture pipeline actually attached to the tab's compositor.
//
// Audio is deliberately out of scope. Chrome's --headless audio capture on
// darwin is unverified (capability.go's darwinAudioVerified is still false),
// so requesting audio here would make the gate fail for a reason unrelated to
// the question being asked. The pool's premise is about tabCapture reaching a
// second process's tab at all.
//
// This test uses requireBrowserOrFail, NEVER skipIfNoBrowser: a skipped
// result is a FAILED gate, not a pass. It runs as its own step in the
// browser-e2e job (.github/workflows/pr.yml) with its own -run filter, so it
// cannot be swallowed by the whole-package step's >=180 pass floor.
func TestSpike_CaptureAgainstSecondChrome(t *testing.T) {
	// The gate belongs to the browser-e2e job, which installs a real Chrome via
	// browser-actions/setup-chrome. The plain "Tests" job does not: it runs
	// `go test ./...` on a runner whose only browser is /usr/bin/chromium-browser,
	// a system build that never completes the CDP liveness probe over a pipe —
	// so the gate failed there with "context deadline exceeded" while PASSING in
	// the job that owns it (OK:true, VideoTracks:1, ReadyState:live).
	//
	// Skipping OUTSIDE its own job is not the "a skipped gate is a failed gate"
	// hole requireBrowserOrFail exists to close. That rule is about the gate
	// silently not running INSIDE browser-e2e, where pr.yml asserts exactly one
	// "--- PASS" and zero "--- SKIP" and fails the step otherwise. Here the
	// alternative is not a stricter gate, it is a gate that reports the runner's
	// chromium instead of the question it was written to answer.
	if os.Getenv("OMNIPUS_BROWSER_E2E") != "1" {
		t.Skip("G-2 runs in the browser-e2e job, which provides a real Chrome; " +
			"that job sets OMNIPUS_BROWSER_E2E=1 and fails on a skip")
	}
	execPath := requireBrowserOrFail(t)
	t.Logf("G-2 spike: resolved Chrome at %s", execPath)

	srv := startTestServer(t)
	t.Cleanup(srv.Close)

	extDir, err := captureext.Seed(t.TempDir())
	require.NoError(t, err, "seed the real capture extension")

	// --- 1. two Chrome processes, two --user-data-dirs ----------------------
	coord1, profile1 := spikeLaunchChrome(t, "one", execPath, extDir)
	coord2, profile2 := spikeLaunchChrome(t, "two", execPath, extDir)

	pid1, pid2 := coord1.PID(), coord2.PID()
	require.NotZero(t, pid1, "first Chrome must report a live pid")
	require.NotZero(t, pid2, "second Chrome must report a live pid")
	require.NotEqual(t, pid1, pid2, "the two Chromes must be SEPARATE processes")
	require.NotEqual(t, profile1, profile2, "the two Chromes must have distinct --user-data-dir values")
	t.Logf("G-2 spike: chrome#1 pid=%d profile=%s", pid1, profile1)
	t.Logf("G-2 spike: chrome#2 pid=%d profile=%s", pid2, profile2)

	for label, dir := range map[string]string{"one": profile1, "two": profile2} {
		entries, rerr := os.ReadDir(dir)
		require.NoError(t, rerr, "chrome#%s must have created its own user-data-dir at %s", label, dir)
		require.NotEmpty(
			t, entries,
			"chrome#%s's user-data-dir %s is empty — that process did not actually use its own profile",
			label, dir,
		)
	}

	// --- 2. the second Chrome has the real capture extension ---------------
	if coord2.LoadedExtensionID() != captureext.ExtensionID {
		loadCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		id, lerr := coord2.LoadExtension(loadCtx)
		cancel()
		require.NoError(t, lerr, "load the capture extension into the SECOND Chrome")
		require.Equal(t, captureext.ExtensionID, id, "extension id must be the pinned, manifest-key-derived id")
	}
	require.Equal(
		t, captureext.ExtensionID, coord2.LoadedExtensionID(),
		"the second Chrome must report the capture extension as loaded",
	)

	// --- 3. one tab per Chrome, both in each process's DEFAULT context ------
	//
	// chromedp.NewContext off the coordinator's rootCtx creates a plain
	// Target.createTarget with NO browserContextId — i.e. the default browser
	// context. Since ADR-075 FR-031 there is no other kind, which
	// TestNoCDPBrowserContextIsEverCreated asserts structurally so this
	// e2e does not have to re-prove it against a live Chrome.
	oneURL := srv.URL + "/?in=chrome-one"
	twoURL := srv.URL + "/?in=chrome-two"

	root1, ok := coord1.RootContext()
	require.True(t, ok, "first Chrome must expose a live root context")
	tab1, cancelTab1 := chromedp.NewContext(root1)
	t.Cleanup(cancelTab1)
	nav1Ctx, cancelNav1 := context.WithTimeout(tab1, 60*time.Second)
	defer cancelNav1()
	require.NoError(
		t,
		chromedp.Run(nav1Ctx, chromedp.Navigate(oneURL), chromedp.WaitReady("#heading", chromedp.ByQuery)),
		"navigate the FIRST Chrome's tab",
	)

	root2, ok := coord2.RootContext()
	require.True(t, ok, "second Chrome must expose a live root context")
	tab2, cancelTab2 := chromedp.NewContext(root2)
	t.Cleanup(cancelTab2)
	nav2Ctx, cancelNav2 := context.WithTimeout(tab2, 60*time.Second)
	defer cancelNav2()
	require.NoError(
		t,
		chromedp.Run(nav2Ctx, chromedp.Navigate(twoURL), chromedp.WaitReady("#heading", chromedp.ByQuery)),
		"navigate the SECOND Chrome's tab",
	)

	// --- 4. capture, from the extension page inside the SECOND Chrome ------
	encCtx, cancelEnc := chromedp.NewContext(root2)
	t.Cleanup(cancelEnc)
	encURL := "chrome-extension://" + captureext.ExtensionID + "/encoder.html"
	loadEncCtx, cancelLoadEnc := context.WithTimeout(encCtx, 60*time.Second)
	defer cancelLoadEnc()
	require.NoError(
		t, chromedp.Run(loadEncCtx, chromedp.Navigate(encURL)),
		"open the capture extension's own page in the SECOND Chrome (%s)", encURL,
	)

	// encoder.js only starts its capture/WebRTC flow when
	// window.__omnipusCapture has been injected; it is not, so the page is
	// inert and this probe drives chrome.tabCapture itself — the same two
	// calls encoder.js's captureActiveTabStream makes, in the same order.
	probe := fmt.Sprintf(`(async () => {
  const out = {ok:false, error:"", streamId:"", videoTracks:0, readyState:"", trackLabel:"", targetUrl:"", tabUrls:[]};
  try {
    const tabs = await chrome.tabs.query({});
    out.tabUrls = tabs.map(t => t.url || "");
    const target = tabs.find(t => (t.url || "").indexOf(%q) !== -1);
    if (!target) { out.error = "target tab not visible to this Chrome's extension"; return out; }
    out.targetUrl = target.url;
    const streamId = await chrome.tabCapture.getMediaStreamId({targetTabId: target.id});
    out.streamId = streamId || "";
    const stream = await navigator.mediaDevices.getUserMedia({
      video: {mandatory: {chromeMediaSource: "tab", chromeMediaSourceId: streamId, maxFrameRate: 30}}
    });
    const tracks = stream.getVideoTracks();
    out.videoTracks = tracks.length;
    if (tracks.length) { out.readyState = tracks[0].readyState; out.trackLabel = tracks[0].label; }
    tracks.forEach(t => t.stop());
    out.ok = out.videoTracks > 0 && out.readyState === "live";
  } catch (e) {
    out.error = String((e && e.message) ? e.message : e);
  }
  return out;
})()`, "in=chrome-two")

	var res spikeCaptureResult
	evalCtx, cancelEval := context.WithTimeout(encCtx, 90*time.Second)
	defer cancelEval()
	require.NoError(
		t,
		chromedp.Run(evalCtx, chromedp.Evaluate(probe, &res, func(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
			return p.WithAwaitPromise(true)
		})),
		"the in-page tabCapture probe must complete (a transport/eval failure is not an answer to the gate's question)",
	)
	t.Logf("G-2 spike: probe result %+v", res)

	// Process isolation: Chrome #2's extension must not see Chrome #1's tab.
	for _, u := range res.TabURLs {
		require.NotContains(
			t, u, "in=chrome-one",
			"the second Chrome's extension saw the FIRST Chrome's tab (%s) — the two processes are not isolated", u,
		)
	}

	require.Empty(
		t, res.Error,
		"chrome.tabCapture failed against the SECOND Chrome's default-context tab. "+
			"If this is \"Invalid tab specified.\", the per-workspace browser POOL DESIGN IS WRONG: "+
			"a separate Chrome process does not lift the ADR-048 capture restriction and ADR-075 must be "+
			"revisited before any pool code is written. Full probe result: %+v", res,
	)
	require.NotEmpty(t, res.StreamID, "getMediaStreamId must return a non-empty stream id; probe result: %+v", res)
	require.Contains(t, res.TargetURL, "in=chrome-two", "the captured tab must be the SECOND Chrome's tab")
	require.GreaterOrEqual(
		t, res.VideoTracks, 1,
		"getUserMedia must yield at least one video track — a stream id with no track is not proof of capture; probe result: %+v", res,
	)
	require.Equal(
		t, "live", res.ReadyState,
		"the captured video track must be LIVE, not ended; probe result: %+v", res,
	)
	require.True(t, res.OK, "probe reported failure; result: %+v", res)

	// The first Chrome must still be alive: the gate's claim is about a
	// SECOND concurrent process, not about a replacement for the first.
	require.True(
		t, pidAlive(pid1),
		"the first Chrome (pid %d) must still be running — the gate is about two CONCURRENT processes", pid1,
	)

	t.Logf(
		"G-2 PASS: chrome.tabCapture reached a tab in the SECOND Chrome's default context "+
			"(pid %d, profile %s); streamId=%q video tracks=%d readyState=%q label=%q",
		pid2, profile2, res.StreamID, res.VideoTracks, res.ReadyState, res.TrackLabel,
	)
}
