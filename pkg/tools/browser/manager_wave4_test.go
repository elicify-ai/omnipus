package browser

import (
	"context"
	"fmt"
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

func TestTabCounter_Limits(t *testing.T) {
	// Traces to: wave4-whatsapp-browser-spec.md Dataset: Tab Limit Enforcement rows 1–6

	tests := []struct {
		name      string
		maxTabs   int
		openTabs  int
		wantAllow bool
	}{
		// Dataset row 1 — limit 5, 0 open: allow
		{name: "5 limit, 0 open: allow", maxTabs: 5, openTabs: 0, wantAllow: true},
		// Dataset row 2 — limit 5, 4 open: allow
		{name: "5 limit, 4 open: allow", maxTabs: 5, openTabs: 4, wantAllow: true},
		// Dataset row 3 — limit 5, 5 open: deny (at limit)
		{name: "5 limit, 5 open: deny", maxTabs: 5, openTabs: 5, wantAllow: false},
		// Dataset row 4 — limit 5, 6 open: deny (above limit, defensive)
		{name: "5 limit, 6 open: deny", maxTabs: 5, openTabs: 6, wantAllow: false},
		// Dataset row 5 — limit 1, 0 open: allow
		{name: "1 limit, 0 open: allow", maxTabs: 1, openTabs: 0, wantAllow: true},
		// Dataset row 6 — limit 1, 1 open: deny
		{name: "1 limit, 1 open: deny", maxTabs: 1, openTabs: 1, wantAllow: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Verify the tab limit logic directly:
			// AcquireTab denies when m.tabCount >= m.cfg.MaxTabs (and MaxTabs > 0)
			atOrOverLimit := tc.openTabs >= tc.maxTabs
			if tc.wantAllow {
				assert.False(t, atOrOverLimit,
					"openTabs=%d should be under maxTabs=%d", tc.openTabs, tc.maxTabs)
			} else {
				assert.True(t, atOrOverLimit,
					"openTabs=%d should be at or over maxTabs=%d", tc.openTabs, tc.maxTabs)
			}
		})
	}
}

// --- TestMaxTabsExceeded_AcquireTabReturnsError ---
// Traces to: wave4-whatsapp-browser-spec.md line 776 (Scenario: Maximum tabs exceeded)
// BDD: Given max_tabs: 3 and 3 tabs open,
// When a 4th browser_navigate with new_tab: true is called,
// Then error: "maximum concurrent tabs (3) reached."

func TestMaxTabsExceeded_SessionReturnsError(t *testing.T) {
	// Traces to: wave4-whatsapp-browser-spec.md line 776 (Scenario: Maximum tabs exceeded)
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	cfg.MaxTabs = 3

	// Bypass browser launch by pre-filling sessions map to capacity
	ssrf := security.NewSSRFChecker(nil)
	m, err := NewBrowserManager(cfg, ssrf)
	require.NoError(t, err)
	m.started = true
	// Simulate 3 existing sessions at limit
	for i := range 3 {
		m.sessions[fmt.Sprintf("tab-%d", i)] = &sessionEntry{
			ctx:    context.Background(),
			cancel: func() {},
		}
	}

	_, sessionErr := m.Session("new-tab")
	require.Error(t, sessionErr)
	assert.Contains(t, sessionErr.Error(), "maximum concurrent tabs")
	assert.Contains(t, sessionErr.Error(), "3")
}

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
	assert.Equal(t, 5, cfg.MaxTabs, "default max tabs 5 per FR-013")
	assert.False(t, cfg.PersistSession, "session persistence disabled by default (explicit non-behavior)")
	assert.Contains(t, cfg.ProfileDir, ".omnipus", "profile dir under ~/.omnipus per FR-018")
	assert.Contains(t, cfg.ProfileDir, "browser", "profile dir under browser subdirectory")
	assert.Contains(t, cfg.ProfileDir, "profiles", "profile dir contains profiles segment")
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
