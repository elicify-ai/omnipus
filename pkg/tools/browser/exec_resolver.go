package browser

// exec_resolver.go holds the shared Chromium-binary resolution logic
// (resolveExecPath, refactored out of manager.go) used by BOTH the
// BrowserManager (Preprovision + the no-coordinator fallback launch) and the
// BrowserCoordinator (the shared-Chrome launch). The behavior is byte-for-byte
// identical to the pre-ADR-043 manager methods; only the storage was extracted
// into a reusable struct so the coordinator can resolve without owning a
// *BrowserManager.
//
// WebRTC build (browser-video-2, W1-A): the LAUNCH side of this file (below
// execPathCaches) was reworked from a fixed-TCP-debug-port chromedp
// ExecAllocator to Chromium's --remote-debugging-pipe transport
// (pkg/tools/browser/cdppipe — no TCP port, no /json HTTP surface; ported
// verbatim from the archive feature/live-browser-video-streaming branch,
// EC-3/CRIT-001). managedExecAllocatorOpts now renders RAW Chrome argv/env
// instead of a []chromedp.ExecAllocatorOption, because cdppipe drives Chrome
// directly by argv — there is no chromedp.NewExecAllocator in this path
// anymore. Both the coordinator's launch path (coordinator.go) and the
// manager's no-coordinator managed-mode fallback (manager.go's
// ensureStarted) render their cmdline through managedExecAllocatorOpts and
// launch through launchManagedPipe, so the two never diverge (MAJ-001).

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/cdppipe"
)

// InstallRootForProfileDir computes the managed-Chromium install root
// (installer.go's EnsureChromium/EnsureChromiumFullBuild + capability.go's
// ClassifyVideoCapability all key off this same directory) from a
// BrowserConfig's ProfileDir. Extracted from resolveBrowserExecPath's inline
// computation (WebRTC build W2-A) so callers outside this file — the
// gateway's WebRTC gate ladder needs the SAME install root
// ClassifyVideoCapability must inspect, without duplicating (and risking
// drifting from) this path arithmetic — can compute it identically via
// BrowserManager.InstallRoot().
func InstallRootForProfileDir(profileDir string) string {
	return filepath.Clean(filepath.Join(filepath.Dir(filepath.Clean(profileDir)), "..", "chromium"))
}

// managedChromeCmdline is the rendered command line + environment for a
// managed Chrome launch over the CDP pipe transport (cdppipe). It REPLACES
// the pre-pipe []chromedp.ExecAllocatorOption return: cdppipe drives Chrome
// directly by argv (there is no chromedp.NewExecAllocator anymore), so the
// flag set is rendered to raw Chrome flags here and fed to
// cdppipe.PipeOptions.Args, and the process env to cdppipe.PipeOptions.Env.
// There is NO --remote-debugging-port — CDP flows over the inherited fd 3/4
// pipe (no TCP surface; EC-3/CRIT-001).
type managedChromeCmdline struct {
	Args []string
	Env  []string
}

// chromeHardeningBaseFlags returns the hardening flag set shared by every
// managed Chrome process this package launches, rendered as raw argv so
// cdppipe can pass them straight to exec.Command. The set mirrors
// chromedp.DefaultExecAllocatorOptions (the Puppeteer hardening defaults
// chromedp applies automatically under NewExecAllocator) plus the pre-video
// omnipus additions (crash-reporter/breakpad off, stealth pair, window size,
// forced software/SwiftShader rendering) and the conditional --no-sandbox —
// i.e. the exact effective flag set the pre-pipe managedExecAllocatorOpts
// produced via chromedp.DefaultExecAllocatorOptions[:] + its own appends,
// now hand-rendered because the pipe transport drives Chrome by argv, not
// chromedp options.
//
// Two deliberate DIVERGENCES from that prior effective set (WebRTC build
// task W1-A, both required for the tabCapture MV3 extension capture path):
//
//   - --disable-extensions is OMITTED. chromedp.DefaultExecAllocatorOptions
//     includes Flag("disable-extensions", true), so it WAS in the prior
//     effective flag set. It is dropped here because the coordinator now
//     supports loading the gateway's capture extension via
//     Extensions.loadUnpacked (see launchChrome / LoadExtension in
//     coordinator.go) — --disable-extensions would defeat that even with
//     --allowlisted-extension-id set.
//   - --mute-audio is OMITTED from the headless flags this file adds (see
//     managedExecAllocatorOpts). chromedp.Headless (also folded into
//     DefaultExecAllocatorOptions) sets --mute-audio alongside --headless
//     and --hide-scrollbars, so it WAS also in the prior effective set —
//     implicitly, not via an explicit chromedp.Flag call, which is why a
//     grep-level read of the pre-pipe code can miss it. It is dropped here
//     to keep audio rendering intact for tabCapture (wave-plan key decision
//     6 / spike Q2: capture is proven to work whether or not --mute-audio is
//     present, so this is a safe, deliberate choice, not a regression risk).
//
// cdppipe itself contributes the pipe-specific flags (--remote-debugging-pipe,
// --no-first-run, --no-default-browser-check, --user-data-dir, about:blank),
// so they are deliberately omitted here (cdppipe/allocator.go's buildArgs).
func chromeHardeningBaseFlags() []string {
	args := []string{
		"--disable-background-networking",
		"--enable-features=NetworkService,NetworkServiceInProcess",
		"--disable-background-timer-throttling",
		"--disable-backgrounding-occluded-windows",
		"--disable-breakpad",
		"--disable-client-side-phishing-detection",
		"--disable-default-apps",
		"--disable-dev-shm-usage",
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
		// chromedp.DisableGPU: force software rendering (+ SwiftShader
		// fallback, required on Chromium 139+).
		"--disable-gpu",
		"--enable-unsafe-swiftshader",
		// omnipus additions (pre-pipe managedExecAllocatorOpts).
		"--disable-crash-reporter",
		"--disable-blink-features=AutomationControlled",
		"--lang=en-US",
		// Launch geometry doubles as headless Chrome's virtual SCREEN size,
		// and a window can never exceed it. At 1280x720 a panel asking for a
		// 512-CSS-px-tall viewport at deviceScaleFactor 2 needs 1024 device
		// px of screen — more than 720 — so Chrome silently clamped it and
		// the live panel visibly shrank moments after opening and stayed
		// shrunk (operator report + gateway log "window resize not fully
		// reflected in the tab's CSS viewport", requested_height 512 ->
		// actual_height 425, device_scale_factor 2, 2026-08-03). The existing
		// chrome-delta compensation could not converge because the ceiling is
		// the screen, not a constant chrome offset.
		//
		// 2560x1440 leaves headroom for a full-height panel on a 2x display
		// without pre-allocating an absurd framebuffer; the per-request
		// physical-pixel ceiling (maxViewportPhysicalPixels, live.go) still
		// bounds what any single viewport may ask for.
		"--window-size=2560,1440",
	}

	// Chromium's zygote sandbox depends on new user namespaces, which the
	// gateway's Landlock+PR_SET_NO_NEW_PRIVS policy blocks. The gateway
	// already enforces an outer sandbox, so Chrome's inner sandbox is
	// redundant.
	if os.Getenv("OMNIPUS_BROWSER_NO_SANDBOX") != "0" {
		args = append(args, "--no-sandbox")
	}
	return args
}

// managedExecAllocatorOpts renders the Chrome command line for a managed
// launch over the CDP pipe. Shared by the coordinator's launch path
// (coordinator.go) and the manager's no-coordinator fallback (ensureStarted
// managed-mode) so the two never diverge (MAJ-001 "every managed launch
// rides one transport").
//
// cfg.Headless is honored directly (append --headless + --hide-scrollbars
// only when true) rather than the pre-pipe code's redundant
// chromedp.DefaultExecAllocatorOptions-always-includes-Headless() +
// conditional-re-append shape, which made cfg.Headless a no-op in practice
// (Chrome was always headless regardless of its value). DefaultConfig sets
// Headless:true and nothing in the codebase sets it false, so this is a
// behavior-preserving cleanup, not a functional change.
//
// cfg.ExtensionID gates --allowlisted-extension-id + the CDP
// Extensions.loadUnpacked precondition flag --enable-unsafe-extension-
// debugging (WebRTC build W1-A item 3): both are required together for the
// coordinator's post-launch Extensions.loadUnpacked call (see
// coordinator.go's LoadExtension) to succeed. Requires cfg.ExtensionID
// specifically (not just ExtensionDir) because the wave-plan's extension-ID
// model pins a deterministic ID via the manifest's "key" before launch — the
// caller always knows the ID by the time it sets ExtensionDir.
// chromeMajorCache memoises chromeMajorVersion per resolved binary path. The
// probe spawns a process, and the launch path can run repeatedly (every agent
// registration); the version of a given binary cannot change without the path
// changing, so one probe per path is sufficient.
var chromeMajorCache sync.Map // path -> string

// chromeMajorVersion returns the major version of the Chrome at path ("151"),
// or "" if it cannot be determined. Never fatal: callers degrade to no
// launch-level User-Agent override.
//
// Bounded by chromiumProbeTimeout for the same reason probeChromiumBinary is —
// a hung binary must not stall a launch.
func chromeMajorVersion(ctx context.Context, path string) string {
	if path == "" {
		return ""
	}
	if cached, ok := chromeMajorCache.Load(path); ok {
		return cached.(string)
	}
	probeCtx, cancel := context.WithTimeout(ctx, chromiumProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, path, "--version").Output()
	major := ""
	if err == nil {
		major = chromeMajorFromVersionOutput(string(out))
	}
	chromeMajorCache.Store(path, major)
	return major
}

// chromeMajorFromVersionOutput extracts the major version from `chrome
// --version` output ("Google Chrome for Testing 151.0.7922.71" → "151").
// Returns "" when the shape is unrecognised, which callers treat as "no
// launch-level User-Agent override" rather than guessing a number.
func chromeMajorFromVersionOutput(out string) string {
	for _, field := range strings.Fields(out) {
		if field == "" || field[0] < '0' || field[0] > '9' {
			continue
		}
		major, _, _ := strings.Cut(field, ".")
		if major == "" {
			continue
		}
		for _, r := range major {
			if r < '0' || r > '9' {
				return ""
			}
		}
		return major
	}
	return ""
}

// desktopUserAgent renders the User-Agent a normal desktop Chrome on Linux
// sends, for the given major version.
//
// Why this is set at LAUNCH rather than only per-tab: Chrome's built-in
// headless User-Agent literally contains the token "HeadlessChrome", which is
// the single most obvious bot signal a site can read — Google gates on it
// directly. applyStealth (manager.go) already rewrites it per tab via
// Emulation.setUserAgentOverride, but that has two gaps measured live on UAT
// v46 (`navigator.userAgent` still reported "HeadlessChrome/151.0.0.0"):
//
//  1. COVERAGE — applyStealth runs from createTab only. The coordinator builds
//     each agent's FIRST window through its own CreateTarget path, so the tab
//     the user actually browses in never received the override.
//  2. RACE — even where it does run, it lands after the target is bound, so a
//     page that reads navigator.userAgent early can still see the headless
//     string.
//
// A launch flag closes both: no tab can ever start with the headless token, on
// any creation path. applyStealth stays as the belt-and-braces layer.
func desktopUserAgent(major string) string {
	return "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) " +
		"Chrome/" + major + ".0.0.0 Safari/537.36"
}

func managedExecAllocatorOpts(cfg BrowserConfig, chromeMajor string) managedChromeCmdline {
	// Point Chrome's HOME and XDG dirs at the profile directory so stray
	// writes (Crash Reports, GPUCache, Singleton locks) land inside the
	// Landlock-allowed workspace instead of $HOME/.config/google-chrome.
	chromeHome := cfg.ProfileDir

	args := chromeHardeningBaseFlags()
	if cfg.Headless {
		// --headless=new, NOT bare --headless (which Chrome still resolves to
		// OLD headless). Old headless is a separate, cut-down engine: it is
		// the single strongest automation fingerprint a detector can read, it
		// sets navigator.webdriver non-overridably (so stealthInitScript and
		// --disable-blink-features=AutomationControlled cannot mask it — see
		// stealthInitScript's own "effectiveness caveat"), and it lacks the
		// full rendering/media stack.
		//
		// The rest of this package already ASSUMED new headless — live.go's
		// screencast attach path documents it as "the WebRTC-capable build
		// ADR-047 D2 switched managed launches to", and coordinator.go calls
		// it "new headless" — while this launch site quietly asked for the old
		// one. Paired with the runtime image now shipping full
		// Chrome-for-Testing rather than codec-less Alpine Chromium
		// (docker/Dockerfile.heavy), this is the other half of the operator's
		// "captchas on google and youtube, video not working" report.
		args = append(args, "--headless=new", "--hide-scrollbars")
		// Kill the "HeadlessChrome" User-Agent token at the source — see
		// desktopUserAgent's doc comment for why the per-tab override alone
		// left it leaking. Only when the version probe produced a major:
		// a wrong/hardcoded version is its own mismatch signal (UA claiming a
		// version the binary does not have), so "unknown" degrades to Chrome's
		// own UA plus the existing per-tab rewrite rather than guessing.
		if chromeMajor != "" {
			args = append(args, "--user-agent="+desktopUserAgent(chromeMajor))
		}
	}
	if cfg.ExtensionID != "" {
		args = append(
			args,
			"--allowlisted-extension-id="+cfg.ExtensionID,
			"--enable-unsafe-extension-debugging",
		)
	}

	env := []string{
		"HOME=" + chromeHome,
		"XDG_CONFIG_HOME=" + filepath.Join(chromeHome, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(chromeHome, "cache"),
	}
	return managedChromeCmdline{Args: args, Env: env}
}

// pipeLaunchConfig is the argv/env/profile a managed Chrome launch feeds to
// the CDP pipe transport (cdppipe.NewPipeAllocator).
type pipeLaunchConfig struct {
	args        []string
	env         []string
	userDataDir string
}

// pipeLaunchResult is what a managed CDP-pipe launch hands back. cmd is the
// captured Chrome *exec.Cmd — the ONLY source of the PID/crash handle,
// because chromedp.Browser.Process() returns nil under the pipe allocator
// (see cdppipe/doc.go). browser is the shared *chromedp.Browser whose
// LostConnection channel callers watch for crashes; it may be nil in test
// seams.
type pipeLaunchResult struct {
	rootCtx context.Context
	cancel  context.CancelFunc
	cmd     *exec.Cmd
	browser *chromedp.Browser
}

// launchManagedPipe is the real managed-Chrome launcher: it starts Chrome
// under --remote-debugging-pipe via cdppipe (NO TCP port; EC-3/CRIT-001) and
// returns a chromedp root context ready for child contexts, the teardown
// CancelFunc, the captured *exec.Cmd (PID/crash handle), and the shared
// *chromedp.Browser. It is the default launcher for BOTH the coordinator's
// pipeLauncher seam and the manager's pipeLauncherFn seam; tests inject fakes
// so they never spawn real Chrome.
//
// ctx is accepted for seam symmetry but the browser's lifetime is bound to
// context.Background() (per cdppipe's contract, and because the shared
// Chrome must outlive any single request/tool call — exactly as the pre-pipe
// chromedp.NewExecAllocator(context.Background(), ...) did).
func launchManagedPipe(ctx context.Context, execPath string, cfg pipeLaunchConfig) (*pipeLaunchResult, error) {
	_ = ctx
	var captured *exec.Cmd
	rootCtx, cancel, err := cdppipe.NewPipeAllocator(context.Background(), execPath, cdppipe.PipeOptions{
		Args:        cfg.args,
		Env:         cfg.env,
		UserDataDir: cfg.userDataDir,
		// Capture the *exec.Cmd — the only PID/crash handle under the pipe
		// allocator (chromedp.Browser.Process() is nil here). cdppipe wires
		// the fd 3/4 ExtraFiles AFTER this hook, so it is safe to only read
		// cmd here.
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
//
// B2c(ii) fix: failPath additionally records the SINGLE on-disk binary path
// the cached failure is about, when the failure has one (the managed-download
// probeChromiumBinary failure at the bottom of resolve() — a mid-download or
// corrupt-extraction probe failure whose error text is a raw, present-tense
// os/exec error like "no such file or directory"). Before replaying failErr
// verbatim, resolve() cheaply re-os.Stat's failPath: if the binary now
// exists, the cached failure is stale (the install finished, or was fixed,
// since the error was cached) and is evicted instead of being replayed as
// still-true. Without this, an agent could tell a user a binary "does not
// exist" while it demonstrably does (the field-observed symptom this fixes).
// Other failure modes (network/manifest errors from EnsureChromium itself)
// have no single associated path — failPath is "" for those and the
// existing TTL-bounded replay is unchanged, except the replayed message is
// now annotated with its cache age (see resolve()'s negative-cache branch)
// so it is never presented as a fresh, present-tense fact.
type execPathCaches struct {
	mu        sync.Mutex
	success   string
	failErr   error
	failUntil time.Time
	failPath  string
}

// resolve returns the path to the Chromium binary chromedp should launch.
// Full resolution order (ADR-052 D2/M1 — Phase 1, security review applied):
//
//  1. operator exec_path override            — explicit, trusted; always wins
//  2. system Chrome on $PATH                 — operator's deliberate choice;
//     gated by cfg.TrustPathChrome (SEC-ADR052-002 — opt-in to trust a
//     non-package binary, default false). When TrustPathChrome is false
//     (the default), a $PATH resolution is recorded at WARN-BROWSER-007
//     and the resolver falls through to step 3 (package Chrome). When
//     TrustPathChrome is true, $PATH wins above the package Chrome (the
//     legacy M1 "operator autonomy" path, explicitly opted into).
//  3. package-managed Chrome                 — NEW (ADR-052 D2): the
//     pinned full Chrome-for-Testing that ships in the per-OS package, at
//     <os.Executable()>/../chromium/. Verified against chrome.sha256 with
//     constant-time compare (SEC-ADR052-001 fail-closed on missing/
//     mismatched/malformed manifest). Outranks $PATH when cfg.PreferPackaged
//     is true (M1).
//  4. managed download (first-use)           — fallback for bare-binary /
//     no-package installs.
//  5. remote CDP                             — handled elsewhere (step 1's
//     ExecPath is the explicit hook).
//
// SEC-ADR052-005/006: step 3 fails closed on missing chrome.sha256, a
// symlinked binary/manifest, or a world-writable package root — any of
// those makes the package Chrome unverifiable and the resolver falls
// through to step 4.
//
// See BrowserManager.resolveExecPath's (manager.go) doc comment for the
// legacy rationale; this is that exact logic, factored onto a reusable
// struct plus the new step 3. Safe to call without the caller's bookkeeping
// lock held; the only state it touches is this struct's own mu-guarded
// caches.
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
	failPath := e.failPath
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
		// B2c(ii): before replaying failErr verbatim, cheaply re-verify —
		// os.Stat is orders of magnitude cheaper than the full PATH probe /
		// CfT manifest fetch this branch exists to avoid. If the specific
		// binary the cached failure was about now exists (a download that
		// was mid-flight when the failure was cached has since completed;
		// an operator removed and replaced a corrupt install), the cached
		// failure is stale — evict it and fall through to a full
		// re-resolution instead of repeating a claim that is no longer
		// true.
		if failPath != "" {
			info, statErr := os.Stat(failPath)
			switch {
			case statErr == nil && !info.IsDir():
				logger.InfoCF("browser",
					"cached chromium resolution failure is stale — binary now exists, evicting and re-resolving",
					map[string]any{"path": failPath})
				e.mu.Lock()
				// Only clear if nothing else already superseded this entry
				// (e.g. a concurrent resolve() already succeeded/failed
				// again) — compare-and-clear on failUntil avoids clobbering
				// a newer cache entry with a stale eviction.
				if e.failUntil.Equal(failUntil) {
					e.failErr = nil
					e.failUntil = time.Time{}
					e.failPath = ""
				}
				e.mu.Unlock()
				// Fall through to a full re-resolution below rather than
				// returning here — the freshly-discovered binary still
				// needs the same --version probe every other candidate
				// gets before being trusted (FIX-CRIT-001 discipline).
			case statErr != nil && !os.IsNotExist(statErr):
				// The re-stat itself failed with something OTHER than a
				// plain "not exist" — permission revoked on a parent dir,
				// an I/O error, a symlink loop, etc. This branch used to
				// discard statErr silently and with NO log at all (unlike
				// both siblings: the "stale, now exists" branch above logs
				// InfoCF, the no-failPath branch below logs DebugCF). Main
				// risk stays closed either way — an unreadable path can
				// never be misread as "binary exists", so this still
				// conservatively falls through to replaying the cached
				// failure — but if the TRUE CAUSE changed after the
				// failure was cached (exactly the misleading-stale-
				// diagnosis family B2c(ii) already fixed once for the
				// "now exists" direction), the operator was left with only
				// the stale original diagnosis, age-annotated, for the
				// rest of the TTL, with no signal that the re-check itself
				// hit something new and unexpected. Log it and surface it
				// in the returned error instead of dropping it.
				logger.DebugCF("browser",
					"cached chromium resolution failure replayed — re-checking the cached path hit an "+
						"unexpected error (not a plain not-exist); the underlying cause may have "+
						"changed since the failure was cached",
					map[string]any{
						"path":     failPath,
						"stat_err": statErr.Error(),
					})
				return "", cacheAgeAnnotateWithStatErr(failErr, failUntil, statErr)
			default:
				// Plain ENOENT (or the no-error-but-is-a-directory corner
				// case) — the expected, genuine-failure case: the cached
				// diagnosis still holds and there is nothing new to
				// report beyond the standard age annotation. Unchanged
				// from before this fix.
				return "", cacheAgeAnnotate(failErr, failUntil)
			}
		} else {
			logger.DebugCF("browser", "chromium resolution still negative-cached — short-circuiting",
				map[string]any{
					"ttl_remaining_seconds": int64(time.Until(failUntil).Seconds()),
				})
			return "", cacheAgeAnnotate(failErr, failUntil)
		}
	}

	// Operators can force the managed install path (skipping the $PATH lookup)
	// by setting OMNIPUS_BROWSER_FORCE_MANAGED=1.
	forceManaged := os.Getenv("OMNIPUS_BROWSER_FORCE_MANAGED") == "1"
	pathResolved := ""
	if !forceManaged {
		for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
			path, err := exec.LookPath(name)
			if err != nil {
				continue
			}
			if ok, reason := probeChromiumBinary(ctx, path); !ok {
				logger.WarnCF("browser", "chromium candidate on PATH did not execute successfully — skipping",
					map[string]any{
						"name":   name,
						"path":   path,
						"reason": reason,
					})
				continue
			}
			pathResolved = path
			break
		}
	}

	// SEC-ADR052-002: gate the $PATH resolution by TrustPathChrome (default
	// false — the security-hardened default). A false-default + unverified
	// $PATH binary is the "trusted RCE-engine origin" the security review
	// flagged: a multi-tenant developer box, a CI runner, or a compromised
	// developer machine can plant a google-chrome script earlier on $PATH
	// and the runtime would execute it before the verified package Chrome
	// even gets considered. When TrustPathChrome is false, log WARN-BROWSER-
	// 007 and discard the $PATH resolution — fall through to the verified
	// package Chrome. Operators who actually want a custom Chrome set
	// trust_path_chrome: true (or set cfg.ExecPath, which always wins).
	//
	// FIX-HIGH-004: remember what (if anything) was discarded here so a
	// LATER failure of every remaining resolution step (package Chrome
	// absent/unverifiable, managed download failing — e.g. an air-gapped
	// or offline host) can name it in the error actually returned to the
	// caller/agent. Before this fix the discard was explained ONLY in the
	// WARN log below: an operator whose managed download fails offline saw
	// a bare network/manifest error with no hint that a working Chrome was
	// sitting right there on $PATH the whole time, or which setting to
	// flip to use it.
	rejectedPathChrome := ""
	if pathResolved != "" && !cfg.TrustPathChrome {
		logger.WarnCF(
			"browser",
			"WARN-BROWSER-007: system Chrome on $PATH ignored — operator must set tools.browser.trust_path_chrome=true to use a non-package Chrome",
			map[string]any{
				"path_resolved": pathResolved,
				"policy":        "trust_path_chrome=false",
			},
		)
		rejectedPathChrome = pathResolved
		pathResolved = ""
	}

	// Step 3 (ADR-052 D2 — package Chrome): inspect the runtime-computed
	// package root. When cfg.PreferPackaged is true the package Chrome
	// outranks $PATH (regardless of TrustPathChrome). When PreferPackaged
	// is false but $PATH either missed or was discarded above, the package
	// Chrome is the floor.
	if pathResolved == "" || cfg.PreferPackaged {
		pkgRoot, pkgStatus := packageChromeRootProbe()
		if pkgStatus == ProbeUsable && pkgRoot != "" {
			pkgBin, pkgSHA := findPackageChrome(pkgRoot)
			if pkgBin != "" {
				// SEC-ADR052-001 + SEC-ADR052-004: chrome.sha256 is REQUIRED
				// for the package Chrome path (findPackageChrome refused
				// the binary when the manifest is missing). Verify with
				// the hardened parser + constant-time compare.
				if verr := cachedVerifyChromeSHA256(pkgBin, pkgSHA); verr != nil {
					logger.WarnCF("browser",
						"package Chrome failed integrity verification — falling through to managed download",
						map[string]any{
							"binary":     pkgBin,
							"sha256_man": pkgSHA,
							"error":      verr.Error(),
							"reason":     "WARN-CFTSHA-001",
						})
					// fall through to step 4
				} else {
					logger.InfoCF("browser", "using package-managed Chrome (ADR-052 D2 step 3)",
						map[string]any{
							"binary":          pkgBin,
							"prefer_packaged": cfg.PreferPackaged,
							"path_was_set":    pathResolved != "",
						})
					e.cacheSuccess(pkgBin)
					return pkgBin, nil
				}
			}
		}
	}

	// If step 2 found a system Chrome on $PATH and step 3 didn't override it
	// (either because the package root was empty, or the package binary was
	// absent, or PreferPackaged was false), return the PATH result now.
	if pathResolved != "" {
		e.cacheSuccess(pathResolved)
		return pathResolved, nil
	}

	// Step 4 — managed download (first-use): bare-binary / no-package
	// installs land here. The installer-side findInstalledBuild also runs
	// chromeintegrity.VerifyChromeSHA256 when chrome.sha256 is present (ADR-052 M2), so the
	// downloaded build's integrity is verified before it can ever launch.
	installRoot := InstallRootForProfileDir(cfg.ProfileDir)
	managedPath, err := EnsureChromium(ctx, installRoot)
	if err != nil {
		err = augmentWithRejectedPathChrome(err, rejectedPathChrome)
		// No single on-disk binary path to re-verify later (this is an
		// install/network-level failure, not a probe of a specific file) —
		// cacheFailure's "" path means the replay path skips the B2c(ii)
		// re-stat and relies solely on the cache-age annotation.
		e.cacheFailure(err, "")
		return "", err
	}
	// FIX-CRIT-001: mirror step 2's $PATH probe. EnsureChromium resolving a
	// path — whether freshly downloaded or already installed on disk —
	// only proves the file exists with the executable bit set: the exact
	// filesystem-only check that let a partial/corrupted extraction get
	// cached as a working install forever (see installer.go's
	// EnsureChromiumBuild doc comment on cleanupPartialInstall). Actually
	// EXECUTE the binary before trusting it as resolved, exactly like
	// every $PATH candidate above — there is no further fallback after
	// this step, so a broken managed binary must surface as a clear,
	// actionable error rather than being handed to chromedp to fail later
	// with an opaque exec error.
	if ok, reason := probeChromiumBinary(ctx, managedPath); !ok {
		probeErr := fmt.Errorf(
			"managed chromium binary %s did not execute successfully (--version probe failed: %s); the install may be corrupt — remove %s and retry",
			managedPath,
			reason,
			installRoot,
		)
		probeErr = augmentWithRejectedPathChrome(probeErr, rejectedPathChrome)
		// B2c(ii): record managedPath as the specific binary this failure is
		// about, so the negative-cache replay branch above can cheaply
		// re-os.Stat it and evict a now-stale "does not exist"/"corrupt"
		// verdict instead of replaying it as still true.
		e.cacheFailure(probeErr, managedPath)
		return "", probeErr
	}
	e.cacheSuccess(managedPath)
	return managedPath, nil
}

// augmentWithRejectedPathChrome (FIX-HIGH-004) wraps a managed-download or
// probe failure with a mention of a working $PATH Chrome this resolution
// discarded earlier under the default tools.browser.trust_path_chrome=false
// policy, so the error actually surfaced to the caller/agent — not just the
// WARN-BROWSER-007 log line — names both the rejected binary and the
// setting an operator needs to flip to use it. A no-op when nothing was
// rejected (rejectedPathChrome == "").
func augmentWithRejectedPathChrome(err error, rejectedPathChrome string) error {
	if rejectedPathChrome == "" {
		return err
	}
	return fmt.Errorf(
		"%w (a working system Chrome was found at %s but was not used because tools.browser.trust_path_chrome=false; set tools.browser.trust_path_chrome=true to allow it)",
		err,
		rejectedPathChrome,
	)
}

// cacheSuccess records path as the resolved Chromium binary + clears any prior
// failure cache entry (a successful resolution supersedes a stale negative
// cache). Guarded by mu.
func (e *execPathCaches) cacheSuccess(path string) {
	e.mu.Lock()
	e.success = path
	e.failErr = nil
	e.failUntil = time.Time{}
	e.failPath = ""
	e.mu.Unlock()
}

// cacheFailure stores err as the last resolution failure, arms the negative-
// cache deadline (execPathNegativeCacheTTL from now), clears any prior success
// cache entry, and logs the WARN exactly once per fresh failure. Guarded by mu.
//
// failPath (B2c(ii)) is the single on-disk binary path this failure is
// ABOUT, when there is one — the managed-download probe failure passes
// managedPath so a later replay of this cached error can cheaply re-verify
// (os.Stat) whether the binary now exists before repeating a stale verdict.
// Pass "" for failures with no single associated path (e.g. EnsureChromium's
// own install/network errors) — the replay path then relies solely on the
// cache-age annotation (cacheAgeAnnotate) rather than a re-stat.
func (e *execPathCaches) cacheFailure(err error, failPath string) {
	e.mu.Lock()
	e.failErr = err
	e.failUntil = time.Now().Add(execPathNegativeCacheTTL)
	e.failPath = failPath
	e.success = ""
	e.mu.Unlock()
	logger.WarnCF("browser",
		"chromium resolution failed — negative-caching for the TTL to avoid re-probing on every browser_* call",
		map[string]any{
			"ttl_seconds": int64(execPathNegativeCacheTTL.Seconds()),
			"error":       err.Error(),
		})
}

// invalidate clears both resolution caches so the next resolution observes a
// changed runtime policy immediately. Guarded by the cache mutex.
func (e *execPathCaches) invalidate() {
	e.mu.Lock()
	e.success = ""
	e.failErr = nil
	e.failUntil = time.Time{}
	e.failPath = ""
	e.mu.Unlock()
}

// cacheAgeAnnotate (B2c(ii)) wraps a replayed negative-cache error with its
// age, so a caller that surfaces it verbatim (an agent narrating a tool
// result to a user, a log line, an API error field) can never present a
// TTL-bounded, potentially-stale cached verdict as a fresh, present-tense
// fact — e.g. "does not exist" — without qualification. Applied to every
// negative-cache replay, including the failPath=="" case (no re-stat
// possible) and the case where a re-stat confirmed the binary genuinely
// still does not exist.
func cacheAgeAnnotate(err error, failUntil time.Time) error {
	age := time.Since(failUntil.Add(-execPathNegativeCacheTTL)).Round(time.Second)
	if age < 0 {
		age = 0
	}
	return fmt.Errorf(
		"%w (cached result from %s ago; re-checked automatically after the negative-cache TTL expires)",
		err,
		age,
	)
}

// cacheAgeAnnotateWithStatErr is cacheAgeAnnotate plus a second annotation for
// the case where the cheap re-stat performed before replaying a cached
// failure (B2c(ii)) itself failed with something OTHER than a plain
// not-exist — a permission change, an I/O error, a symlink loop, etc. That
// distinction matters: a plain not-exist means the original diagnosis is
// still accurate (nothing has changed), whereas an unexpected re-stat error
// means the true cause MAY have changed since the failure was cached, and
// the replayed original error text cannot reflect that. Surfacing it here
// (in addition to the DebugCF log at the call site) means a caller that
// prints the returned error verbatim — an agent narrating a tool result, an
// API error field — still carries the signal even if logs aren't consulted.
func cacheAgeAnnotateWithStatErr(err error, failUntil time.Time, statErr error) error {
	return fmt.Errorf(
		"%w; additionally, re-checking the cached path just now failed unexpectedly (%w) rather than confirming it is simply still missing — the underlying cause may have changed since this failure was cached",
		cacheAgeAnnotate(err, failUntil),
		statErr,
	)
}

// cachedPath returns the last successfully-resolved Chromium binary path, or
// "" if nothing has been resolved yet. It is a non-blocking snapshot and does
// not probe the filesystem or network.
func (e *execPathCaches) cachedPath() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.success
}
