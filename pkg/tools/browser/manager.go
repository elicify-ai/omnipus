// Package browser implements browser automation tools using chromedp (pure Go CDP).
//
// Implements US-4 (managed mode), US-6 (remote CDP mode), US-7 (resource limits)
// from the Wave 4 spec. All navigations are SSRF-checked via pkg/security.SSRFChecker.
package browser

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/security"
)

// DebugPort is the fixed loopback TCP port the managed Chromium binds its
// DevTools WebSocket to. Pinning this lets the gateway's Landlock
// NET_CONNECT_TCP allow-list include it (see pkg/gateway/sandbox_apply.go).
// Without a fixed port, chromedp defaults to remote-debugging-port=0,
// Chrome picks a random ephemeral port, and the gateway's connect-to-
// chromedp dial returns EACCES.
//
// Exposed as a package constant (not a config knob) so the sandbox layer
// and the browser layer agree without a circular import. Operators who
// need a different port today can recompile; a config knob is a v0.3
// follow-up if a real conflict surfaces.
const DebugPort = 9223

// browserDebugPort is the string form passed to chromedp.Flag (which takes
// the flag value as a string). Kept private so callers use DebugPort.
const browserDebugPort = "9223"

// BrowserConfig holds browser automation configuration.
// Mapped from config.json: tools.browser.*
type BrowserConfig struct {
	Enabled        bool          `json:"enabled"`
	Headless       bool          `json:"headless"`
	CDPURL         string        `json:"cdp_url"`         // Remote CDP WebSocket URL (US-6)
	PageTimeout    time.Duration `json:"page_timeout"`    // Per-page load timeout (US-7, default 30s)
	MaxTabs        int           `json:"max_tabs"`        // Max concurrent tabs (US-7, default 5)
	PersistSession bool          `json:"persist_session"` // Persist cookies/localStorage across restarts
	ProfileDir     string        `json:"profile_dir"`     // User data dir (default ~/.omnipus/browser/profiles/default/)
	// ExecPath overrides Chromium discovery. When empty the manager prefers
	// a system chromium/google-chrome on PATH and falls back to a managed
	// install under <ProfileDir>/../chromium/ (downloaded on first use).
	ExecPath string `json:"exec_path,omitempty"`
}

// DefaultConfig returns a BrowserConfig with spec-defined defaults.
// Returns an error if no home directory (OMNIPUS_HOME or user home) can
// be determined. Prefers $OMNIPUS_HOME so the profile lands inside the
// gateway's Landlock-allowed workspace; falls back to the user's home
// directory only when OMNIPUS_HOME is unset.
func DefaultConfig() (BrowserConfig, error) {
	base := os.Getenv("OMNIPUS_HOME")
	if base == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return BrowserConfig{}, fmt.Errorf("browser: cannot determine home directory: %w", err)
		}
		base = filepath.Join(homeDir, ".omnipus")
	}
	return BrowserConfig{
		Enabled:     false,
		Headless:    true,
		PageTimeout: 30 * time.Second,
		MaxTabs:     5,
		ProfileDir:  filepath.Join(base, "browser", "profiles", "default"),
	}, nil
}

// sessionEntry tracks a chromedp tab context that persists across tool calls.
type sessionEntry struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// BrowserManager manages the Chromium lifecycle and tab pool.
// Thread-safe — all methods may be called concurrently.
//
// Session model: tools operate on a persistent "default" session tab so that
// browser.navigate, browser.click, browser.get_text, etc. act on the same page.
// Additional sessions can be created for parallel browsing up to MaxTabs.
type BrowserManager struct {
	cfg         BrowserConfig
	ssrf        *security.SSRFChecker // never nil — enforced by NewBrowserManager
	mu          sync.Mutex
	allocCtx    context.Context    // chromedp allocator context
	allocCancel context.CancelFunc // cancels the allocator (kills browser)
	sessions    map[string]*sessionEntry
	started     bool
}

// NewBrowserManager creates a manager. ssrf must be non-nil — SSRF protection
// is mandatory for browser tools (SEC-24). The browser is not launched until
// the first tool invocation (lazy init).
func NewBrowserManager(cfg BrowserConfig, ssrf *security.SSRFChecker) (*BrowserManager, error) {
	if ssrf == nil {
		return nil, fmt.Errorf(
			"browser: SSRFChecker is required — cannot create browser manager without SSRF protection (SEC-24)",
		)
	}
	return &BrowserManager{
		cfg:      cfg,
		ssrf:     ssrf,
		sessions: make(map[string]*sessionEntry),
	}, nil
}

// blockedSchemes are URL schemes that bypass network-level SSRF and must be
// denied at the application layer. file:// would bypass Landlock restrictions.
var blockedSchemes = map[string]bool{
	"file":             true,
	"javascript":       true,
	"data":             true,
	"chrome":           true,
	"chrome-extension": true,
}

// ValidateURL checks a URL against SSRF rules and blocked schemes.
// Returns an error if navigation should be denied.
func (m *BrowserManager) ValidateURL(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("browser: invalid URL %q: %w", rawURL, err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "" {
		return fmt.Errorf("browser: URL %q has no scheme — use http:// or https://", rawURL)
	}
	if blockedSchemes[scheme] {
		return fmt.Errorf("browser: %s:// URLs are blocked for security reasons", scheme)
	}
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("browser: only http:// and https:// URLs are permitted, got %s://", scheme)
	}

	// SSRF check: resolve host, block private IPs and cloud metadata (SEC-24)
	if err := m.ssrf.CheckURL(ctx, rawURL); err != nil {
		return fmt.Errorf("browser: navigation blocked by SSRF policy: %w", err)
	}

	return nil
}

// ensureStarted lazily initializes the browser. Must be called under m.mu.
func (m *BrowserManager) ensureStarted() error {
	if m.started {
		return nil
	}

	if m.cfg.CDPURL != "" {
		// US-6: Remote CDP mode — connect to external Chromium
		allocCtx, cancel := chromedp.NewRemoteAllocator(context.Background(), m.cfg.CDPURL)
		m.allocCtx = allocCtx
		m.allocCancel = cancel
		m.started = true
		logger.InfoCF("browser", "Connected to remote CDP", map[string]any{
			"url": m.cfg.CDPURL,
		})
		return nil
	}

	// US-4: Managed mode — launch local Chromium
	if err := os.MkdirAll(m.cfg.ProfileDir, 0o700); err != nil {
		return fmt.Errorf("browser: cannot create profile directory %s: %w", m.cfg.ProfileDir, err)
	}

	// Clean up stale SingletonLock files. When Chromium exits ungracefully
	// (kill -9, crash, or the gateway's chromedp allocator canceling mid-
	// startup), `SingletonLock` / `SingletonCookie` / `SingletonSocket`
	// stay behind in the profile dir. The next launch refuses to start with:
	//   "Failed to create .../SingletonLock: File exists (17)
	//    Failed to create a ProcessSingleton for your profile directory."
	// and every subsequent browser.navigate fails. We always own this
	// profile directory exclusively (single chromedp allocator per gateway
	// process; tabs share the same Chromium instance), so it is safe to
	// remove these on each lazy-init. Symlinks (which is what Chrome uses
	// for SingletonLock — a symlink whose target encodes pid + hostname)
	// must be removed with os.Remove; os.RemoveAll would fail to follow
	// them in some edge cases.
	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		path := filepath.Join(m.cfg.ProfileDir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			logger.WarnCF("browser", "Failed to remove stale Chromium singleton file", map[string]any{
				"path":  path,
				"error": err.Error(),
			})
		}
	}

	execPath, err := m.resolveExecPath(context.Background())
	if err != nil {
		return fmt.Errorf("browser: cannot locate chromium: %w", err)
	}

	// Point Chrome's HOME and XDG dirs at the profile directory so any stray
	// writes (Crash Reports, GPUCache, Singleton locks, etc.) land inside
	// the Landlock-allowed workspace instead of $HOME/.config/google-chrome.
	// Use the profile directory itself as a self-contained jail so this
	// stays correct regardless of the configured layout (test tempdirs,
	// custom paths, $OMNIPUS_HOME, etc.).
	chromeHome := m.cfg.ProfileDir

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(execPath),
		chromedp.UserDataDir(m.cfg.ProfileDir),
		chromedp.DisableGPU,
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		// Crash reporting writes to ~/.config/google-chrome/Crash Reports
		// which is outside the Landlock-allowed paths. Disable it to avoid
		// "Permission denied" spam during browser startup.
		chromedp.Flag("disable-crash-reporter", true),
		chromedp.Flag("disable-breakpad", true),
		// Pin the DevTools WebSocket to a fixed loopback port so the
		// gateway's Landlock NET_CONNECT_TCP allow-list can include it
		// (v0.1 fix for "browser.navigate: dial tcp 127.0.0.1:<random>:
		// connect: permission denied"). Without this, chromedp picks an
		// ephemeral port and Landlock blocks the dial because only
		// {53, 80, 443} + dev-server ports are allow-listed by default.
		// 9223 was chosen to sit just above Chrome's traditional 9222
		// debug port (which we avoid to reduce collision risk with
		// operator workstations that may already have a remote-debugged
		// Chrome listening on 9222). pkg/gateway/sandbox_apply.go appends
		// this port to ConnectPortRules when the browser tool is enabled.
		chromedp.Flag("remote-debugging-port", browserDebugPort),
		chromedp.Env(
			"HOME="+chromeHome,
			"XDG_CONFIG_HOME="+filepath.Join(chromeHome, "config"),
			"XDG_CACHE_HOME="+filepath.Join(chromeHome, "cache"),
		),
		chromedp.WindowSize(1280, 720),
	)

	if m.cfg.Headless {
		opts = append(opts, chromedp.Headless)
	}

	// Chromium's zygote sandbox depends on creating new user namespaces, which
	// the gateway's Landlock+PR_SET_NO_NEW_PRIVS policy blocks. The gateway
	// already enforces an outer filesystem and network sandbox, so Chrome's
	// inner sandbox is redundant — disable it by default to avoid the
	// permission-denied init failures. Operators can opt out by setting
	// OMNIPUS_BROWSER_NO_SANDBOX=0 if they run the gateway without Landlock.
	if os.Getenv("OMNIPUS_BROWSER_NO_SANDBOX") != "0" {
		opts = append(opts, chromedp.NoSandbox)
	}

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	m.allocCtx = allocCtx
	m.allocCancel = cancel
	m.started = true

	logger.InfoCF("browser", "Browser allocator ready (managed mode)", map[string]any{
		"headless":    m.cfg.Headless,
		"profile_dir": m.cfg.ProfileDir,
		"exec_path":   execPath,
	})
	return nil
}

// resolveExecPath returns the path to the Chromium binary chromedp should
// launch. Resolution order:
//
//  1. cfg.ExecPath (operator override) — used as-is.
//  2. System chromium/google-chrome on $PATH — preferred when present.
//  3. Managed install under <ProfileDir>/../chromium/ — downloaded from
//     Chrome for Testing on first call. Cached across restarts so the
//     download cost is amortized once per host.
func (m *BrowserManager) resolveExecPath(ctx context.Context) (string, error) {
	if m.cfg.ExecPath != "" {
		if _, err := os.Stat(m.cfg.ExecPath); err != nil {
			return "", fmt.Errorf("configured exec_path %s: %w", m.cfg.ExecPath, err)
		}
		return m.cfg.ExecPath, nil
	}
	// Operators can force the managed install path (skipping the $PATH
	// lookup) by setting OMNIPUS_BROWSER_FORCE_MANAGED=1. Useful for
	// pinning a deterministic Chromium version even when a system
	// google-chrome is present, and for exercising the install flow in
	// CI / staging hosts that already have Chrome installed.
	forceManaged := os.Getenv("OMNIPUS_BROWSER_FORCE_MANAGED") == "1"
	if !forceManaged {
		for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
			if path, err := exec.LookPath(name); err == nil {
				return path, nil
			}
		}
	}
	installRoot := filepath.Join(filepath.Dir(filepath.Clean(m.cfg.ProfileDir)), "..", "chromium")
	installRoot = filepath.Clean(installRoot)
	return EnsureChromium(ctx, installRoot)
}

// Session returns a persistent tab context for the given session ID.
// If the session does not exist, a new tab is created (subject to MaxTabs).
// The "default" session is used by all tools unless a session_id is specified.
func (m *BrowserManager) Session(sessionID string) (context.Context, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureStarted(); err != nil {
		return nil, err
	}

	if s, ok := m.sessions[sessionID]; ok {
		// Verify the session context is still valid
		if s.ctx.Err() == nil {
			return s.ctx, nil
		}
		// Session expired (browser crash, etc.) — clean up and recreate
		s.cancel()
		delete(m.sessions, sessionID)
	}

	if len(m.sessions) >= m.cfg.MaxTabs {
		return nil, fmt.Errorf("maximum concurrent tabs (%d) reached. Close a tab first", m.cfg.MaxTabs)
	}

	ctx, cancel := chromedp.NewContext(m.allocCtx)
	// Eagerly create the target on this ctx. Without this, the first
	// chromedp.Run binds the target to whichever (possibly timeout-wrapped)
	// ctx a tool passes — and when that wrapper is canceled, the tab dies.
	// The next tool call then silently creates a fresh blank tab, so e.g.
	// screenshot-after-navigate returns a blank page.
	if err := chromedp.Run(ctx); err != nil {
		cancel()
		return nil, fmt.Errorf("browser: failed to initialize tab: %w", err)
	}
	m.sessions[sessionID] = &sessionEntry{ctx: ctx, cancel: cancel}
	return ctx, nil
}

// CloseSession closes a specific session tab.
func (m *BrowserManager) CloseSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.sessions[sessionID]; ok {
		s.cancel()
		delete(m.sessions, sessionID)
	}
}

// PageTimeout returns the configured page load timeout.
func (m *BrowserManager) PageTimeout() time.Duration {
	return m.cfg.PageTimeout
}

// Shutdown gracefully shuts down the browser process and all sessions.
func (m *BrowserManager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, s := range m.sessions {
		s.cancel()
		delete(m.sessions, id)
	}

	if m.allocCancel != nil {
		m.allocCancel()
		m.allocCancel = nil
	}
	m.started = false

	logger.InfoCF("browser", "Browser shut down", nil)
}
