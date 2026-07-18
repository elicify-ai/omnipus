// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import "testing"

// TestValidateBootConfig_PublicURL covers ADR-044 boot-time validation of
// gateway.public_url: canonicalGatewayOrigin() (pkg/gateway/middleware/origin.go)
// returns this value verbatim and uses it as the web_serve/preview link base
// URL, the CSP/CORS/WS-CheckOrigin allowed origin, and the Origin-match
// reference, so a malformed value must be rejected at boot rather than
// silently producing broken preview links and a bogus origin.
func TestValidateBootConfig_PublicURL(t *testing.T) {
	cases := []struct {
		name      string
		publicURL string
		wantErr   bool
	}{
		{name: "empty is valid (default)", publicURL: "", wantErr: false},
		{name: "whitespace-only treated as empty", publicURL: "   ", wantErr: false},
		{name: "valid https with host", publicURL: "https://omnipus.example.com", wantErr: false},
		{name: "valid http with host and port", publicURL: "http://localhost:8080", wantErr: false},
		{name: "non-http(s) scheme rejected", publicURL: "ftp://x", wantErr: true},
		{name: "javascript scheme rejected", publicURL: "javascript:alert(1)", wantErr: true},
		{name: "scheme with no host rejected", publicURL: "https://", wantErr: true},
		{name: "not a url rejected", publicURL: "not a url", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Gateway.PublicURL = tc.publicURL
			err := validateBootConfig(cfg)
			if tc.wantErr && err == nil {
				t.Fatalf("validateBootConfig() with public_url=%q: expected error, got nil", tc.publicURL)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateBootConfig() with public_url=%q: expected nil, got %v", tc.publicURL, err)
			}
		})
	}
}
