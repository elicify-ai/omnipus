package browser

// exec_resolver.go holds the shared Chromium-binary resolution logic
// (resolveExecPath, refactored out of manager.go) used by BOTH the
// BrowserManager (Preprovision + the no-coordinator fallback launch) and the
// BrowserCoordinator (the shared-Chrome launch). The behavior is byte-for-byte
// identical to the pre-ADR-043 manager methods; only the storage was extracted
// into a reusable struct so the coordinator can resolve without owning a
// *BrowserManager.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/cdppipe"
)

// managedLaunchParams is reserved for future per-launch agent-Chrome options.
// The ADR-044 Amendment (single full-Chrome, encoder-as-tab) collapsed the
// dedicated encoder browser process into a tab hosted by this same
// coordinator Chrome (see pkg/gateway/browser_stream.go's coordinatorRoot
// seam) — there is no separate encoder process or cmdline anymore, and this
// struct still carries no fields today. Kept as a named type (rather than
// dropping the parameter) so managedExecAllocatorOpts's signature stays
// stable for its callers (coordinator.go, manager.go), which pass the zero
// value.
type managedLaunchParams struct{}

// managedChromeCmdline is the rendered command line + environment for a managed
// Chrome launch over the CDP pipe transport (cdppipe). It REPLACES the
// pre-CRIT-001 []chromedp.ExecAllocatorOption return: cdppipe drives Chrome
// directly by argv (there is no chromedp.NewExecAllocator anymore), so the flag
// set is rendered to raw Chrome flags here and fed to cdppipe.PipeOptions.Args,
// and the process env to cdppipe.PipeOptions.Env. There is NO
// --remote-debugging-port — CDP flows over the inherited fd 3/4 pipe (no TCP
// surface; EC-3/CRIT-001).
type managedChromeCmdline struct {
	Args []string
	Env  []string
}

// chromeHardeningBaseFlags returns the hardening flag set shared by EVERY
// managed Chrome process this package launches. Under the ADR-044 Amendment
// (single full-Chrome, encoder-as-tab) there is only ONE such process per
// agent: the coordinator's full-Chrome launch (managedExecAllocatorOpts),
// which now also hosts the WebCodecs encoder tab in its default browser
// context (a tab launched via pkg/gateway/browser_stream.go's coordinatorRoot
// seam — see encoder_launch.go's LaunchEncoderPage). Keeping the base in one
// place means the single agent+encoder Chrome has one security posture to
// audit, not two that could drift apart.
//
// The flag set mirrors chromedp.DefaultExecAllocatorOptions (the Puppeteer
// hardening set) rendered as raw flags, PLUS the pre-video omnipus additions
// (crash-reporter/breakpad off, stealth pair, window size, forced
// software/SwiftShader rendering) and the conditional --no-sandbox. It is kept
// in sync with chromedp's defaults on purpose — the CDP pipe drives Chrome by
// argv, not chromedp options, so the option list cannot be reused directly.
// cdppipe contributes the pipe-specific flags (--remote-debugging-pipe,
// --no-first-run, --no-default-browser-check, --user-data-dir, about:blank), so
// they are deliberately omitted here.
func chromeHardeningBaseFlags() []string {
	// chromedp.DefaultExecAllocatorOptions (Puppeteer defaults) as raw flags.
	// enable-automation is deliberately OMITTED (the pre-video path overrode the
	// chromedp default of true back to false, which chromedp renders as "drop
	// the flag") in favor of the stealth pair below.
	args := []string{
		"--disable-background-networking",
		"--enable-features=NetworkService,NetworkServiceInProcess",
		"--disable-background-timer-throttling",
		"--disable-backgrounding-occluded-windows",
		"--disable-breakpad",
		"--disable-client-side-phishing-detection",
		"--disable-default-apps",
		"--disable-dev-shm-usage",
		"--disable-extensions",
		"--disable-features=site-per-process,Translate,BlinkGenPropertyTrees",
		"--disable-hang-monitor",
		"--disable-ipc-flooding-protection",
		"--disable-popup-blocking",
		"--disable-prompt-on-repost",
		"--disable-renderer-backgrounding",
		"--disable-sync",
		"--force-color-profile=srgb",
		"--metrics-recording-only",
		"--safebrowsing-disable-auto-update",
		"--password-store=basic",
		"--use-mock-keychain",
		// chromedp.DisableGPU: force software rendering (+ SwiftShader fallback,
		// required on Chromium 139+ and for the encoder tab's full-quality
		// WebCodecs path hosted in this same process — it has no real GPU
		// available).
		"--disable-gpu",
		"--enable-unsafe-swiftshader",
		// omnipus additions (pre-video managedExecAllocatorOpts).
		"--disable-crash-reporter",
		"--disable-blink-features=AutomationControlled",
		"--lang=en-US",
		"--window-size=1280,720",
	}

	// Chromium's zygote sandbox depends on new user namespaces, which the
	// gateway's Landlock+PR_SET_NO_NEW_PRIVS policy blocks. The gateway already
	// enforces an outer sandbox, so Chrome's inner sandbox is redundant. Applies
	// to the single agent+encoder Chrome process.
	if os.Getenv("OMNIPUS_BROWSER_NO_SANDBOX") != "0" {
		args = append(args, "--no-sandbox")
	}
	return args
}

// managedExecAllocatorOpts renders the Chrome command line for the agent's
// managed launch over the CDP pipe. Shared by the coordinator's launch path
// (coordinator.go) and the manager's no-coordinator fallback (ensureStarted
// managed-mode) so the two never diverge — the MAJ-001 "every managed launch
// rides one transport" invariant.
//
// ADR-044 Amendment (single full-Chrome, encoder-as-tab): the binary this
// launches is now full Chrome (see installer.go's selectDownloadBuild), not
// chrome-headless-shell — full Chrome is required because this SAME process
// now also hosts the WebCodecs encoder tab (a default-context tab launched
// via pkg/gateway/browser_stream.go's coordinatorRoot seam; see
// encoder_launch.go's LaunchEncoderPage). The flags below are UNCHANGED by
// this amendment — there is still no video-capable branch here, no DISPLAY,
// and no PULSE_SERVER: this process renders offscreen and (phase 1) never
// wires audio. chromeHardeningBaseFlags already carries the --disable-gpu
// --enable-unsafe-swiftshader pair the encoder tab's software WebCodecs path
// needs, so no new flags were required to add video capability.
func managedExecAllocatorOpts(cfg BrowserConfig, params managedLaunchParams) managedChromeCmdline {
	_ = params // reserved for future per-launch agent options; none exist today

	// Point Chrome's HOME and XDG dirs at the profile directory so stray writes
	// (Crash Reports, GPUCache, Singleton locks) land inside the Landlock-
	// allowed workspace instead of $HOME/.config/google-chrome.
	chromeHome := cfg.ProfileDir

	args := chromeHardeningBaseFlags()
	args = append(args, "--headless", "--hide-scrollbars", "--mute-audio")

	env := []string{
		"HOME=" + chromeHome,
		"XDG_CONFIG_HOME=" + filepath.Join(chromeHome, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(chromeHome, "cache"),
	}
	return managedChromeCmdline{Args: args, Env: env}
}

// pipeLaunchConfig is the argv/env/profile a managed Chrome launch feeds to the
// CDP pipe transport (cdppipe.NewPipeAllocator).
type pipeLaunchConfig struct {
	args        []string
	env         []string
	userDataDir string
}

// pipeLaunchResult is what a managed CDP-pipe launch hands back. cmd is the
// captured Chrome *exec.Cmd — the ONLY source of the PID/crash handle, because
// chromedp.Browser.Process() returns nil under the pipe allocator (see
// cdppipe.doc). browser is the shared *chromedp.Browser whose LostConnection
// channel the coordinator watches for crashes; it may be nil in test seams.
type pipeLaunchResult struct {
	rootCtx context.Context
	cancel  context.CancelFunc
	cmd     *exec.Cmd
	browser *chromedp.Browser
}

// launchManagedPipe is the real managed-Chrome launcher: it starts Chrome under
// --remote-debugging-pipe via cdppipe (NO TCP port; EC-3/CRIT-001) and returns a
// chromedp root context ready for child contexts, the teardown CancelFunc, the
// captured *exec.Cmd (PID/crash handle), and the shared *chromedp.Browser. It is
// the default for BOTH the coordinator's pipeLauncher seam and the manager's
// managedPipeLaunch seam; tests inject fakes so they never spawn real Chrome.
//
// ctx is accepted for seam symmetry but the browser's lifetime is bound to
// context.Background() (per cdppipe's contract, and because the shared Chrome
// must outlive any single request/tool call, exactly as the pre-pipe
// chromedp.NewExecAllocator(context.Background(), ...) did).
func launchManagedPipe(ctx context.Context, execPath string, cfg pipeLaunchConfig) (*pipeLaunchResult, error) {
	_ = ctx
	var captured *exec.Cmd
	rootCtx, cancel, err := cdppipe.NewPipeAllocator(context.Background(), execPath, cdppipe.PipeOptions{
		Args:        cfg.args,
		Env:         cfg.env,
		UserDataDir: cfg.userDataDir,
		// Capture the *exec.Cmd — the only PID/crash handle under the pipe
		// allocator (chromedp.Browser.Process() is nil here). cdppipe wires the
		// fd 3/4 ExtraFiles AFTER this hook, so it is safe to only read cmd here.
		ModifyCmd: func(cmd *exec.Cmd) { captured = cmd },
		Logf:      func(f string, a ...any) { logger.DebugCF("browser", fmt.Sprintf(f, a...), nil) },
		Errf:      func(f string, a ...any) { logger.WarnCF("browser", fmt.Sprintf(f, a...), nil) },
	})
	if err != nil {
		return nil, err
	}
	var browser *chromedp.Browser
	if cc := chromedp.FromContext(rootCtx); cc != nil {
		browser = cc.Browser
	}
	return &pipeLaunchResult{rootCtx: rootCtx, cancel: cancel, cmd: captured, browser: browser}, nil
}

// execPathCaches holds the success/failure caches for Chromium-binary
// resolution. A dedicated lock (mu), deliberately separate from any
// BrowserManager/BrowserCoordinator bookkeeping lock, so the (potentially slow)
// PATH probe or chrome-for-testing download never blocks tab/session
// bookkeeping (ADR-038 discipline). Two caches, mutually exclusive:
//
//   - success: the last successfully-resolved binary path. Re-validated with
//     os.Stat on each hit (a cached path whose binary was since deleted would
//     otherwise make every launch fail with a generic chromedp exec error until
//     restart); on stat miss/dir the entry is dropped and resolution re-runs.
//   - failErr / failUntil (negative cache): the last resolution ERROR, returned
//     verbatim (without re-probing) until failUntil. Within the TTL a subsequent
//     resolve returns the SAME error — on a dead host a full resolution
//     otherwise re-probes all 4 PATH candidates (~20s) and re-hits the CfT
//     manifest (~30s) on every browser_* call, forever.
type execPathCaches struct {
	mu        sync.Mutex
	success   string
	failErr   error
	failUntil time.Time
}

// resolve returns the path to the Chromium binary chromedp should launch. See
// BrowserManager.resolveExecPath's (manager.go) doc comment for the full
// resolution order + rationale — this is that exact logic, factored onto a
// reusable struct. Safe to call without the caller's bookkeeping lock held; the
// only state it touches is this struct's own mu-guarded caches.
func (e *execPathCaches) resolve(ctx context.Context, cfg BrowserConfig) (string, error) {
	if cfg.ExecPath != "" {
		info, err := os.Stat(cfg.ExecPath)
		if err != nil {
			return "", fmt.Errorf("configured exec_path %s: %w", cfg.ExecPath, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf(
				"configured exec_path %s is a directory, not an executable file", cfg.ExecPath,
			)
		}
		// Exec-bit check is POSIX-only: on Windows os.FileMode does not carry
		// Unix execute bits, so this guard would wrongly reject every .exe.
		if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
			return "", fmt.Errorf(
				"configured exec_path %s is not executable (check its file mode)", cfg.ExecPath,
			)
		}
		return cfg.ExecPath, nil
	}

	e.mu.Lock()
	cached := e.success
	failErr := e.failErr
	failUntil := e.failUntil
	e.mu.Unlock()

	// Success cache hit — validate the binary is still on disk (L2). A cached
	// path whose binary was since deleted would otherwise make every launch fail
	// with a generic chromedp exec error that never names the real cause.
	if cached != "" {
		if info, statErr := os.Stat(cached); statErr == nil && !info.IsDir() {
			return cached, nil
		}
		e.mu.Lock()
		e.success = ""
		e.mu.Unlock()
	}

	// Negative cache (L1): within the TTL, return the SAME error without
	// re-probing. On a dead host a full resolution re-probes all 4 PATH
	// candidates + re-hits fetchCFTManifest on every browser_* call.
	if failErr != nil && time.Now().Before(failUntil) {
		logger.DebugCF("browser", "chromium resolution still negative-cached — short-circuiting",
			map[string]any{
				"ttl_remaining_seconds": int64(time.Until(failUntil).Seconds()),
			})
		return "", failErr
	}

	// Operators can force the managed install path (skipping the $PATH lookup)
	// by setting OMNIPUS_BROWSER_FORCE_MANAGED=1.
	forceManaged := os.Getenv("OMNIPUS_BROWSER_FORCE_MANAGED") == "1"
	if !forceManaged {
		for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
			path, err := exec.LookPath(name)
			if err != nil {
				continue
			}
			if !probeChromiumBinary(ctx, path) {
				logger.WarnCF("browser", "chromium candidate on PATH did not execute successfully — skipping",
					map[string]any{
						"name": name,
						"path": path,
					})
				continue
			}
			e.cacheSuccess(path)
			return path, nil
		}
	}
	installRoot := filepath.Join(filepath.Dir(filepath.Clean(cfg.ProfileDir)), "..", "chromium")
	installRoot = filepath.Clean(installRoot)
	managedPath, err := EnsureChromium(ctx, installRoot)
	if err != nil {
		e.cacheFailure(err)
		return "", err
	}
	e.cacheSuccess(managedPath)
	return managedPath, nil
}

// cacheSuccess records path as the resolved Chromium binary + clears any prior
// failure cache entry (a successful resolution supersedes a stale negative
// cache). Guarded by mu.
func (e *execPathCaches) cacheSuccess(path string) {
	e.mu.Lock()
	e.success = path
	e.failErr = nil
	e.failUntil = time.Time{}
	e.mu.Unlock()
}

// cacheFailure stores err as the last resolution failure, arms the negative-
// cache deadline (execPathNegativeCacheTTL from now), clears any prior success
// cache entry, and logs the WARN exactly once per fresh failure. Guarded by mu.
func (e *execPathCaches) cacheFailure(err error) {
	e.mu.Lock()
	e.failErr = err
	e.failUntil = time.Now().Add(execPathNegativeCacheTTL)
	e.success = ""
	e.mu.Unlock()
	logger.WarnCF("browser",
		"chromium resolution failed — negative-caching for the TTL to avoid re-probing on every browser_* call",
		map[string]any{
			"ttl_seconds": int64(execPathNegativeCacheTTL.Seconds()),
			"error":       err.Error(),
		})
}
