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

// packageChromeRootForTest is a test-only seam for packageChromeRoot.
// When non-empty, packageChromeRoot returns its value verbatim; when empty
// (the production default), packageChromeRoot falls back to its
// os.Executable()-derived computation. Used by
// exec_resolver_phase1_test.go's withPackageChromeRoot helper to pin the
// package root to a temp directory without symlinking the test binary into
// a realistic layout.
var packageChromeRootForTest string

// packageChromeRoot returns the directory the package-build pipeline is
// contracted (ADR-052 D2) to drop the pinned full Chrome-for-Testing build
// into — a SIBLING of the running binary, NOT a per-profile installRoot and
// NOT something computed at build time via ldflags. Computed at runtime from
// os.Executable() so the SAME binary works on every package layout (the
// build-variance memory omnipus-build-variance-48mb.md warns against any
// per-package variant of the binary itself).
//
// Layout decision (ADR-052 M5):
//
//   - Linux/macOS: <filepath>("..", "chromium") under os.Executable()'s dir.
//     The goreleaser archive layout puts the binary at <archive>/omnipus and
//     the chrome payload at <archive>/chromium/ — so this resolves to that
//     chromium/ directory when the package is unpacked.
//   - Windows: same shape ("..", "chromium") for Phase 1. Phase 4 (Windows
//     Service / .msi packaging, ADR-052) may need to revisit — the .msi
//     conventional layout nests under Program Files\Omnipus\, where
//     filepath.Dir() lands one level above the binary's parent — but Phase
//     1 is Linux-only and Phase 4 will land its own allocator work and
//     archive re-layout. Keeping the shape identical across all three OSes
//     in Phase 1 means there is ONE rule to document, not three.
//
// Returns the empty string on any os.Executable() error (defensive — never
// panics; callers must treat "" as "no package Chrome available"). The result
// is deterministic and idempotent: same process state → same path.
func packageChromeRoot() string {
	if packageChromeRootForTest != "" {
		return packageChromeRootForTest
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "..", "chromium")
}

// findPackageChrome inspects root (typically from packageChromeRoot()) for a
// pinned full Chrome-for-Testing binary and its companion chrome.sha256
// integrity manifest. Returns the binary's absolute path and the manifest's
// absolute path; either is the empty string when absent.
//
// SEC-ADR052-005/006 hardening:
//   - root is Lstat-checked before anything inside it is touched (refuses a
//     symlinked root, refuses a world-writable root per the install-root-
//     ownership check below).
//   - the binary is Lstat-checked (refuses a leaf symlink, refuses a non-
//     executable POSIX file).
//   - chrome.sha256 must EXIST and be readable (SEC-ADR052-001 fail-closed).
//     Unlike findInstalledBuild's permissive-missing behavior on the
//     managed install root (which has historical pre-Phase-1 installs
//     without a manifest), the package Chrome is a Phase-1-only construct —
//     the only legitimate reason for a missing manifest at the package root
//     is a pipeline failure or tampering, both of which are release blockers
//     (per the security review: "the only legitimate causes of a missing
//     manifest are pipeline failures (already a release blocker) and
//     operator tampering (already a hard-stop)").
//
// Returns "" for both halves on any refusal (including missing manifest) so
// the caller falls through to the managed download path.
func findPackageChrome(root string) (binaryPath, shaPath string) {
	if root == "" {
		return "", ""
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return "", ""
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		// A symlinked package root is suspicious — refuse. Use Lstat (not
		// Stat) so we see the leaf, not the target.
		return "", ""
	}
	if !rootInfo.IsDir() {
		return "", ""
	}
	// SEC-ADR052-005/006: refuse a world-writable install root. On a
	// multi-tenant host a 0777 install root lets any local user substitute
	// chrome / chrome.sha256 between resolve and launch; the package
	// integrity guarantee collapses. The root-owned-by-a-package-manager
	// invariant ADR-052 D4 names (systemd StateDirectory, /usr/bin/...) is
	// what we're defending here.
	if rootInfo.Mode()&0o002 != 0 {
		return "", ""
	}
	// Use fullChromeBinaryRelPath() — the same per-OS naming
	// EnsureChromiumBuild / findInstalledBuild use — so the package Chrome
	// sits at exactly the on-disk layout ClassifyVideoCapability's
	// findInstalledBuild already inspects (Linux: chromium/chrome; macOS:
	// chromium/Google Chrome for Testing.app/...; Windows: chromium/chrome.exe).
	// Phase 1 only validates the Linux layout.
	bin := filepath.Join(root, fullChromeBinaryRelPath())
	info, statErr := os.Lstat(bin)
	if statErr != nil {
		return "", ""
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", ""
	}
	if info.IsDir() {
		return "", ""
	}
	// POSIX exec-bit check mirrors the resolve() ExecPath guard; Windows
	// FileMode carries no Unix exec bits, so this guard is skipped there.
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return "", ""
	}
	sha := filepath.Join(root, "chrome.sha256")
	// SEC-ADR052-005: refuse a symlinked chrome.sha256 at the leaf (an
	// attacker who can write the package root can symlink the manifest to
	// a known-good digest elsewhere on disk, defeating the integrity check).
	if shaInfo, shaErr := os.Lstat(sha); shaErr != nil {
		// Fail-closed: chrome.sha256 must exist at the package root. Per
		// SEC-ADR052-001, returning "" here is the explicit refusal signal —
		// the caller treats it as "no package Chrome available" and falls
		// through to the managed download path.
		return "", ""
	} else if shaInfo.Mode()&os.ModeSymlink != 0 {
		return "", ""
	}
	return bin, sha
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
		"--window-size=1280,720",
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
func managedExecAllocatorOpts(cfg BrowserConfig) managedChromeCmdline {
	// Point Chrome's HOME and XDG dirs at the profile directory so stray
	// writes (Crash Reports, GPUCache, Singleton locks) land inside the
	// Landlock-allowed workspace instead of $HOME/.config/google-chrome.
	chromeHome := cfg.ProfileDir

	args := chromeHardeningBaseFlags()
	if cfg.Headless {
		args = append(args, "--headless", "--hide-scrollbars")
	}
	if cfg.ExtensionID != "" {
		args = append(args,
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
type execPathCaches struct {
	mu        sync.Mutex
	success   string
	failErr   error
	failUntil time.Time
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
				"configured exec_path %s is a directory, not an executable file", cfg.ExecPath)
		}
		// Exec-bit check is POSIX-only: on Windows os.FileMode does not carry
		// Unix execute bits, so this guard would wrongly reject every .exe.
		if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
			return "", fmt.Errorf(
				"configured exec_path %s is not executable (check its file mode)", cfg.ExecPath)
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
	pathResolved := ""
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
	if pathResolved != "" && !cfg.TrustPathChrome {
		logger.WarnCF("browser",
			"WARN-BROWSER-007: system Chrome on $PATH ignored — operator must set tools.browser.trust_path_chrome=true to use a non-package Chrome",
			map[string]any{
				"path_resolved": pathResolved,
				"policy":        "trust_path_chrome=false",
			})
		pathResolved = ""
	}

	// Step 3 (ADR-052 D2 — package Chrome): inspect the runtime-computed
	// package root. When cfg.PreferPackaged is true the package Chrome
	// outranks $PATH (regardless of TrustPathChrome). When PreferPackaged
	// is false but $PATH either missed or was discarded above, the package
	// Chrome is the floor.
	if pathResolved == "" || cfg.PreferPackaged {
		pkgRoot := packageChromeRoot()
		if pkgRoot != "" {
			pkgBin, pkgSHA := findPackageChrome(pkgRoot)
			if pkgBin != "" {
				// SEC-ADR052-001 + SEC-ADR052-004: chrome.sha256 is REQUIRED
				// for the package Chrome path (findPackageChrome refused
				// the binary when the manifest is missing). Verify with
				// the hardened parser + constant-time compare.
				if verr := verifyChromeSHA256(pkgBin, pkgSHA); verr != nil {
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
	// verifyChromeSHA256 when chrome.sha256 is present (ADR-052 M2), so the
	// downloaded build's integrity is verified before it can ever launch.
	installRoot := InstallRootForProfileDir(cfg.ProfileDir)
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

// cachedPath returns the last successfully-resolved Chromium binary path, or
// "" if nothing has been resolved yet (or the prior success was invalidated,
// e.g. by cacheFailure or a stat-miss in resolve). Guarded by mu.
//
// This is a plain, non-blocking field read — unlike resolve(), it NEVER
// re-validates the path with os.Stat, never probes $PATH, and never reaches
// the network for the Chrome-for-Testing manifest. It exists for callers on
// hot request paths (BrowserManager.VideoCapability, manager.go) that need a
// best-effort snapshot of "what Chromium binary, if any, has already been
// resolved for a real launch" without risking resolve()'s up-to-4-candidate
// PATH probe (5s timeout each) or a CfT manifest fetch.
func (e *execPathCaches) cachedPath() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.success
}
