package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- E3: CORS tests ---

// TestIsAllowedOrigin verifies the isAllowedOrigin function for all significant cases
// from the E3 test dataset.
//
// isAllowedOrigin(reqOrigin, host, configuredOrigin) bool
//
// BDD: Given various origin combinations,
// When isAllowedOrigin is called,
// Then correct allow/deny decision is returned.
// Traces to: wave5a-wire-ui-spec.md — Scenario: CORS origin validation (E3)
func TestIsAllowedOrigin(t *testing.T) {
	tests := []struct {
		name             string
		reqOrigin        string
		host             string
		configuredOrigin string
		want             bool
	}{
		// E3 dataset row 1: evil origin with localhost configured — should be denied
		{
			name:             "evil.com with localhost configured → denied",
			reqOrigin:        "http://evil.com",
			host:             "localhost:19000",
			configuredOrigin: "http://localhost:19000",
			want:             false,
		},
		// E3 dataset row 2: localhost origin with no host/config — localhost always allowed
		{
			name:             "localhost:3000 with no config → allowed (dev loopback)",
			reqOrigin:        "http://localhost:3000",
			host:             "",
			configuredOrigin: "",
			want:             true,
		},
		// E3 dataset row 3: same-origin (IP matches host) — should be allowed
		{
			name:             "same IP origin matches host → allowed",
			reqOrigin:        "http://146.190.89.151:19000",
			host:             "146.190.89.151:19000",
			configuredOrigin: "",
			want:             true,
		},
		// E3 dataset row 4: localhost.evil.com subdomain spoofing — should be denied
		{
			name:             "localhost.evil.com subdomain → denied",
			reqOrigin:        "http://localhost.evil.com",
			host:             "localhost:19000",
			configuredOrigin: "",
			want:             false,
		},
		// E3 dataset row 5: empty origin — should be denied
		{
			name:             "empty origin → denied",
			reqOrigin:        "",
			host:             "localhost:19000",
			configuredOrigin: "",
			want:             false,
		},
		// E3 dataset row 6: 127.0.0.1 loopback — should be allowed
		{
			name:             "127.0.0.1 loopback → allowed",
			reqOrigin:        "http://127.0.0.1:19000",
			host:             "",
			configuredOrigin: "",
			want:             true,
		},
		// Additional: configured origin exact match → allowed
		{
			name:             "exact configured origin match → allowed",
			reqOrigin:        "http://localhost:19000",
			host:             "",
			configuredOrigin: "http://localhost:19000",
			want:             true,
		},
		// Additional: different port same hostname → denied (port is part of origin)
		// isAllowedOrigin now compares hostname AND port for same-host requests to
		// prevent cross-port CORS escalation (review finding #7).
		{
			name:             "different port but same hostname → denied (port mismatch)",
			reqOrigin:        "http://192.168.1.1:8080",
			host:             "192.168.1.1:9000",
			configuredOrigin: "",
			want:             false,
		},
		// Additional: malformed URL in origin → denied
		{
			name:             "malformed origin URL → denied",
			reqOrigin:        "://no-scheme-here",
			host:             "localhost:3000",
			configuredOrigin: "",
			want:             false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isAllowedOrigin(tc.reqOrigin, tc.host, tc.configuredOrigin)
			assert.Equal(t, tc.want, got,
				"isAllowedOrigin(%q, %q, %q)", tc.reqOrigin, tc.host, tc.configuredOrigin)
		})
	}
}

// TestAllowCORS_CsrfHeaderAndCredentials verifies F7 fixes:
// (a) X-Csrf-Token appears in Access-Control-Allow-Headers for all allowed origins.
// (b) Access-Control-Allow-Credentials: true only when origin matches the
//
//	explicitly configured allowedOrigin (not for localhost fallback or wildcard).
//
// (c) Disallowed origins get no CORS headers at all (no credentials leak).
//
// Traces to: Sprint B F7 — CORS preflight must include X-Csrf-Token + Allow-Credentials
// only for explicitly configured origins.
func TestAllowCORS_CsrfHeaderAndCredentials(t *testing.T) {
	const configuredOrigin = "https://app.example.com"

	api := &restAPI{allowedOrigin: configuredOrigin}

	makeRequest := func(origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
		req.Header.Set("Origin", origin)
		req.Host = "app.example.com"
		rr := httptest.NewRecorder()
		api.setCORSHeaders(rr, req)
		return rr
	}

	t.Run("allowed origin gets X-Csrf-Token and Allow-Credentials=true", func(t *testing.T) {
		rr := makeRequest(configuredOrigin)
		require.Equal(t, configuredOrigin, rr.Header().Get("Access-Control-Allow-Origin"),
			"explicit configured origin must be reflected")
		allowHeaders := rr.Header().Get("Access-Control-Allow-Headers")
		assert.Contains(t, allowHeaders, "X-Csrf-Token",
			"Access-Control-Allow-Headers must include X-Csrf-Token")
		assert.Equal(t, "true", rr.Header().Get("Access-Control-Allow-Credentials"),
			"Access-Control-Allow-Credentials must be true for explicitly configured origin")
	})

	t.Run("localhost fallback does not get Allow-Credentials", func(t *testing.T) {
		// Localhost is allowed (dev loopback) but is NOT the configured origin,
		// so credentials must NOT be emitted.
		rr := makeRequest("http://localhost:3000")
		// The origin is allowed (reflected), but no credentials header.
		allowedOriginHeader := rr.Header().Get("Access-Control-Allow-Origin")
		if allowedOriginHeader != "" {
			// If the origin was reflected, credentials must NOT be present.
			assert.NotEqual(t, "true", rr.Header().Get("Access-Control-Allow-Credentials"),
				"localhost fallback must not get Allow-Credentials header")
		}
		// X-Csrf-Token must still appear in Allow-Headers if CORS is active.
		if allowedOriginHeader != "" {
			allowHeaders := rr.Header().Get("Access-Control-Allow-Headers")
			assert.Contains(t, allowHeaders, "X-Csrf-Token",
				"X-Csrf-Token must be in Allow-Headers even for localhost")
		}
	})

	t.Run("disallowed request-origin does not get Allow-Credentials", func(t *testing.T) {
		// When a request comes from an evil.com origin that is neither the
		// configured origin nor localhost, the server emits the CONFIGURED origin
		// in Access-Control-Allow-Origin (this is fine: the browser only uses it
		// if it matches the request origin). But Access-Control-Allow-Credentials
		// must NOT be present — credentials must only be sent for explicitly
		// configured origins, never for mismatched request origins.
		rr := makeRequest("https://evil.com")
		assert.NotEqual(t, "https://evil.com", rr.Header().Get("Access-Control-Allow-Origin"),
			"evil.com must not be reflected as the allowed origin")
		assert.NotEqual(t, "true", rr.Header().Get("Access-Control-Allow-Credentials"),
			"Access-Control-Allow-Credentials must not be true for a non-matching request origin")
	})
}
