package browser

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// --- TestURLSchemeValidation ---
// Traces to: wave4-whatsapp-browser-spec.md line 699 (Scenario Outline: Browser navigate URL schemes)
// BDD: Given a managed Chromium instance, When browser_navigate(<url>) is called,
// Then allowed URLs succeed and blocked schemes return an error.

func TestURLSchemeValidation(t *testing.T) {
	// Traces to: wave4-whatsapp-browser-spec.md Dataset: URL Scheme Validation rows 1–10
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	ssrf := security.NewSSRFChecker(nil)
	m, err := NewBrowserManager(cfg, ssrf)
	require.NoError(t, err)

	tests := []struct {
		name        string
		url         string
		wantErr     bool
		errContains string
	}{
		// Dataset row 1 — Standard HTTPS (allowed)
		{name: "https allowed", url: "https://example.com", wantErr: false},
		// Dataset row 2 — HTTP allowed
		{name: "http allowed", url: "http://example.com", wantErr: false},
		// Dataset row 3 — file:// blocked
		{name: "file:// blocked", url: "file:///etc/passwd", wantErr: true, errContains: "file"},
		// Dataset row 4 — javascript: blocked
		{name: "javascript: blocked", url: "javascript:alert(1)", wantErr: true, errContains: "javascript"},
		// Dataset row 5 — data: blocked
		{name: "data: blocked", url: "data:text/html,<h1>Hi</h1>", wantErr: true, errContains: "data"},
		// Dataset row 6 — chrome: blocked
		{name: "chrome: blocked", url: "chrome://settings", wantErr: true, errContains: "chrome"},
		// Dataset row 7 — HTTP uppercase allowed (case-insensitive scheme)
		{name: "HTTP uppercase allowed", url: "HTTP://EXAMPLE.COM", wantErr: false},
		// Dataset row 8 — empty URL blocked
		{name: "empty url blocked", url: "", wantErr: true},
		// Dataset row 9 — ftp: blocked (non-web protocol)
		{name: "ftp: blocked", url: "ftp://files.example.com", wantErr: true, errContains: "ftp"},
		// Dataset row 10 — HTTPS with port and query (allowed)
		{name: "https with port and query", url: "https://example.com:8080/path?q=1", wantErr: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := m.ValidateURL(context.Background(), tc.url)
			if tc.wantErr {
				assert.Error(t, err, "URL %q should be blocked", tc.url)
				if tc.errContains != "" {
					assert.Contains(t, err.Error(), tc.errContains)
				}
			} else {
				assert.NoError(t, err, "URL %q should be allowed", tc.url)
			}
		})
	}
}

// --- TestSSRFURLCheckByBrowserManager ---
// Traces to: wave4-whatsapp-browser-spec.md line 565 (Scenario: SSRF protection blocks private IP navigation)
// BDD: Given SSRF protection is active (SEC-24),
// When browser_navigate("http://169.254.169.254/...") is called,
// Then navigation is blocked before the request is sent with SSRF error.

func TestSSRFURLCheckByBrowserManager(t *testing.T) {
	// Traces to: wave4-whatsapp-browser-spec.md Dataset: SSRF URL Validation rows 1–5
	ssrf := security.NewSSRFChecker(nil)
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	m, err := NewBrowserManager(cfg, ssrf)
	require.NoError(t, err)

	tests := []struct {
		name string
		url  string
	}{
		// Dataset row 1 — RFC 1918 Class A
		{name: "private 10.0.0.1", url: "http://10.0.0.1"},
		// Dataset row 2 — RFC 1918 Class B
		{name: "private 172.16.0.1", url: "http://172.16.0.1"},
		// Dataset row 3 — RFC 1918 Class C
		{name: "private 192.168.1.1", url: "http://192.168.1.1"},
		// Dataset row 4 — AWS metadata endpoint
		{name: "metadata 169.254.169.254", url: "http://169.254.169.254/latest/meta-data/"},
		// Dataset row 5 — loopback
		{name: "loopback 127.0.0.1", url: "http://127.0.0.1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := m.ValidateURL(context.Background(), tc.url)
			require.Error(t, err, "private URL %q must be blocked by SSRF", tc.url)
			assert.Contains(t, err.Error(), "SSRF",
				"error must mention SSRF for traceability to SEC-24")
		})
	}
}

// --- TestTabCounter_Limits ---
// Traces to: wave4-whatsapp-browser-spec.md line 995 (Test #5: TestTabCounter_Limits)
// BDD Scenario Outline: Tab limit enforcement
// Dataset: Tab Limit Enforcement rows 1–6

// --- TestMaxTabsExceeded_AcquireTabReturnsError ---
// Traces to: wave4-whatsapp-browser-spec.md line 776 (Scenario: Maximum tabs exceeded)
// BDD: Given max_tabs: 3 and 3 tabs open,
// When a 4th browser_navigate with new_tab: true is called,
// Then error: "maximum concurrent tabs (3) reached."

// --- TestPageTimeoutConfig ---
// Traces to: wave4-whatsapp-browser-spec.md line 997 (Test #6: TestPageTimeoutConfig)
// BDD: Given tools.browser.page_timeout: 10s,
// When a page load exceeds 10s, Then navigation is aborted with timeout error.

func TestPageTimeoutConfig(t *testing.T) {
	// Traces to: wave4-whatsapp-browser-spec.md line 762 (Scenario: Page load timeout)
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, cfg.PageTimeout,
		"default page timeout must be 30s per FR-013")

	// Custom timeout is preserved
	cfg.PageTimeout = 10 * time.Second
	ssrf := security.NewSSRFChecker(nil)
	m, err := NewBrowserManager(cfg, ssrf)
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, m.PageTimeout(),
		"configured page timeout must be honored")
}

// --- TestBrowserConfigParsing ---
// Traces to: wave4-whatsapp-browser-spec.md line 998 (Test #8: TestBrowserConfigParsing)
// BDD: Verify browser config fields parse correctly with spec-defined defaults.

func TestBrowserConfigParsing(t *testing.T) {
	// Traces to: wave4-whatsapp-browser-spec.md line 527 (Scenario: Launch managed Chromium)
	cfg, err := DefaultConfig()
	require.NoError(t, err)

	assert.False(t, cfg.Enabled, "browser disabled by default (deny-by-default per CLAUDE.md)")
	assert.True(t, cfg.Headless, "browser headless by default per FR-009")
	assert.Equal(t, 30*time.Second, cfg.PageTimeout, "default page timeout 30s per FR-013")
	assert.Equal(t, leaseWaitTimeout, cfg.LeaseWait, "default write-lease wait per FR-023")
	assert.False(t, cfg.PersistSession, "session persistence disabled by default (explicit non-behavior)")
	assert.Contains(t, cfg.ProfileDir, ".omnipus", "profile dir under ~/.omnipus per FR-018")
	assert.Contains(t, cfg.ProfileDir, "browser", "profile dir under browser subdirectory")
	assert.Contains(t, cfg.ProfileDir, "profiles", "profile dir contains profiles segment")
}

// --- TestGetTextWaitTimeout_ShorterThanDefaultPageTimeout ---
//
// Unit-only wiring guard (no Chromium required) for the browser_get_text /
// browser_wait fail-fast fix: a live, browser-gated proof lives in
// execute_e2e_test.go's TestExecute_GetText_FailsFastOnInvisibleOrMissingSelector,
// which SKIPs in any environment without a working Chromium/Chrome binary
// (this devpod included — see skipIfNoBrowser). This test has no such gate,
// so it still catches the class of regression that matters most here: someone
// widening getTextWaitTimeout back up to (or past) PageTimeout, which would
// silently reintroduce the ~30s hang the fix closes, without needing a real
// browser to detect it.
func TestGetTextWaitTimeout_ShorterThanDefaultPageTimeout(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)

	require.Greater(t, cfg.PageTimeout, getTextWaitTimeout,
		"getTextWaitTimeout must stay a SHORT, dedicated bound — well under the full "+
			"page-load budget (PageTimeout) — or browser_get_text/browser_wait "+
			"regress to blocking for the entire PageTimeout on a present-but-invisible "+
			"or missing selector")
	assert.Equal(t, 8*time.Second, getTextWaitTimeout,
		"getTextWaitTimeout changed — update this assertion deliberately, and re-check "+
			"it is still comfortably shorter than PageTimeout")
}

// --- TestBrowserShutdown ---
// Traces to: wave4-whatsapp-browser-spec.md line 577 (Scenario: Graceful browser shutdown)
// BDD: Given managed Chromium instance, When gateway shuts down (SIGTERM),
// Then Chromium process terminates gracefully.

func TestBrowserShutdown(t *testing.T) {
	// Traces to: wave4-whatsapp-browser-spec.md line 577 (Scenario: Graceful browser shutdown)
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	ssrf := security.NewSSRFChecker(nil)
	m, err := NewBrowserManager(cfg, ssrf)
	require.NoError(t, err)

	// Un-started manager must shut down without panic (defensive shutdown)
	assert.NotPanics(t, func() { m.Shutdown() }, "shutdown of un-started manager must not panic")
	assert.False(t, m.started, "started must be false after shutdown")
	assert.Empty(t, m.sessions, "sessions must be empty after shutdown")
}

// --- TestNewBrowserManager_NilSSRF ---
// Verifies SEC-24 enforcement: SSRF protection cannot be bypassed.

func TestNewBrowserManager_NilSSRF(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)

	_, err = NewBrowserManager(cfg, nil)
	require.Error(t, err, "nil SSRFChecker must be rejected")
	assert.Contains(t, err.Error(), "SSRFChecker is required")
}
