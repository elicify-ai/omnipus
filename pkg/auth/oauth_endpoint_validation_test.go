package auth

import "testing"

// The OAuth device-code flow dials an endpoint built from
// OAuthProviderConfig.Issuer. That value is a constant for OpenAI and an
// operator-set environment variable for xAI, so it is not request-derived —
// but validateOAuthEndpoint makes that a structural property rather than a
// claim in a comment, which is what the per-call `#nosec G107` annotations
// were standing in for before the four call sites were consolidated.
//
// The loopback exception is load-bearing: 14 tests in this package drive the
// flows against httptest servers on 127.0.0.1, and an operator may point a
// local xAI-compatible issuer at localhost.
func TestValidateOAuthEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
		wantErr  bool
	}{
		{"vendor https is accepted", "https://auth.openai.com/api/accounts/deviceauth/token", false},
		{"xai default issuer is accepted", "https://accounts.x.ai/oauth2/token", false},
		{"loopback http is accepted (httptest, local issuer)", "http://127.0.0.1:54321/token", false},
		{"localhost http is accepted", "http://localhost:8080/token", false},
		{"ipv6 loopback http is accepted", "http://[::1]:8080/token", false},

		{"plain http on a public host is refused", "http://auth.openai.com/token", true},
		{"http on a non-loopback private host is refused", "http://10.0.0.5/token", true},
		{"file scheme is refused", "file:///etc/passwd", true},
		{"gopher scheme is refused", "gopher://evil.example/", true},
		{"scheme-less value is refused (no host)", "auth.openai.com/token", true},
		{"empty endpoint is refused", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOAuthEndpoint(tc.endpoint)
			if tc.wantErr && err == nil {
				t.Fatalf("validateOAuthEndpoint(%q) = nil, want an error", tc.endpoint)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateOAuthEndpoint(%q) = %v, want nil", tc.endpoint, err)
			}
		})
	}
}
