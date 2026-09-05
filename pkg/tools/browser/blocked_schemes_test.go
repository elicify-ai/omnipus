// Contract test: Plan 3 §1 acceptance decision — browser tool must reject file://,
// javascript:, data:, and chrome:// URLs regardless of SSRF permissiveness.
//
// BDD: Given any browser URL scheme other than http:// or https://,
//
//	When browser_navigate attempts to validate the URL,
//	Then it returns an error blocking navigation.
//
// Acceptance decision: Plan 3 §1 "Browser URL block: both layers enforced (hard-coded schemes + SSRF checker)"
// Traces to: temporal-puzzling-melody.md §4 Axis-1, pkg/tools/browser/blocked_schemes_test.go

package browser

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// TestBlockedSchemesRejectedRegardlessOfSSRF verifies that URL schemes on the hard-coded
// block list are rejected at the application layer even when the SSRF checker would
// otherwise allow the request (e.g., it is lenient or disabled).
//
// Traces to: temporal-puzzling-melody.md §4 Axis-1 — TestBlockedSchemesRejectedRegardlessOfSSRF
func TestBlockedSchemesRejectedRegardlessOfSSRF(t *testing.T) {
	// Use a permissive SSRF checker (nil allow-internal list) so it would pass
	// any host — this isolates the scheme-level block.
	ssrf := security.NewSSRFChecker(nil)
	mgr, err := NewBrowserManager(BrowserConfig{Headless: true}, ssrf)
	require.NoError(t, err, "NewBrowserManager must succeed with non-nil SSRFChecker")

	ctx := context.Background()

	blockedURLs := []struct {
		url    string
		scheme string
	}{
		// file:// carries its own, more useful refusal (FR-019) — it names
		// serve_web and the /preview/ route rather than stopping at "blocked".
		// Asserted in full by TestValidateURL_FileScheme_NamesServeWeb; here
		// it only has to still be BLOCKED.
		{"file:///etc/passwd", "file"},
		{"javascript:alert(1)", "javascript"},
		{"data:text/html,<h1>xss</h1>", "data"},
		{"chrome://settings/", "chrome"},
		{"chrome-extension://id/page.html", "chrome-extension"},
	}

	for _, tc := range blockedURLs {
		t.Run(tc.scheme, func(t *testing.T) {
			// BDD: When ValidateURL is called with a blocked scheme.
			err := mgr.ValidateURL(ctx, tc.url)

			// BDD: Then an error is returned (navigation is blocked).
			assert.Errorf(t, err,
				"URL with scheme %q must be blocked: %s", tc.scheme, tc.url)

			if err != nil {
				// The error message must mention the scheme or "blocked" so it's actionable.
				errStr := err.Error()
				assert.Truef(t,
					containsAny(errStr, tc.scheme, "blocked", "security", "not permitted"),
					"error message %q must mention the scheme or reason for blocking", errStr)
			}
		})
	}

	// Differentiation: http:// and https:// must NOT be blocked by the scheme check.
	// (SSRF may still block specific hosts — but the scheme itself is allowed.)
	allowedSchemes := []string{
		"http://example.com",
		"https://example.com",
	}
	for _, url := range allowedSchemes {
		// ValidateURL may fail due to SSRF (example.com is public, but the SSRF checker
		// in test mode may or may not allow it). We only care that the error is NOT
		// a scheme-blocked error. A nil error (allowed) also passes.
		err := mgr.ValidateURL(ctx, url)
		if err != nil {
			errStr := err.Error()
			assert.NotContainsf(t, errStr, "://",
				"http/https scheme must not produce a scheme-blocked error; got: %s", errStr)
		}
	}
}

// containsAny returns true if s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if sub != "" && strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
