//go:build !cgo

// This test file uses //go:build !cgo so it compiles when CGO is disabled.
// When CGO is enabled, pkg/gateway imports pkg/channels/matrix which requires
// the libolm system library (olm/olm.h). If that library is installed,
// remove this build constraint and run tests normally.

package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestBearerTokenConstantTimeComparison verifies that checkBearerAuth uses
// constant-time comparison (crypto/subtle) to prevent timing attacks.
// Traces to: wave3-skill-ecosystem-spec.md line 846 (Test #16: TestBearerTokenConstantTimeComparison)
// BDD: Given OMNIPUS_BEARER_TOKEN is set,
// When a request with the correct token is sent,
// Then checkBearerAuth returns AuthResult{Authenticated: true} (constant-time comparison used — verified by code review).
// When a request with an incorrect token is sent,
// Then checkBearerAuth returns AuthResult{Authenticated: false} and responds 401.

func TestBearerTokenConstantTimeComparison(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 846 (Test #16)
	const testToken = "test-bearer-token-constant-time-abc123"
	t.Setenv("OMNIPUS_BEARER_TOKEN", testToken)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{}, // empty users — falls back to env var
	}

	tests := []struct {
		name       string
		authHeader string
		wantPass   bool
		wantStatus int
	}{
		// Valid token
		{
			name:       "valid token passes",
			authHeader: "Bearer " + testToken,
			wantPass:   true,
			wantStatus: 0, // auth passes, not checked at this level
		},
		// Wrong token
		{
			name:       "wrong token returns 401",
			authHeader: "Bearer wrong-token",
			wantPass:   false,
			wantStatus: http.StatusUnauthorized,
		},
		// Missing Authorization header
		{
			name:       "missing auth header returns 401",
			authHeader: "",
			wantPass:   false,
			wantStatus: http.StatusUnauthorized,
		},
		// Wrong prefix (no "Bearer " prefix)
		{
			name:       "missing Bearer prefix returns 401",
			authHeader: testToken, // no "Bearer " prefix
			wantPass:   false,
			wantStatus: http.StatusUnauthorized,
		},
		// Empty token value after "Bearer "
		{
			name:       "empty Bearer token returns 401",
			authHeader: "Bearer ",
			wantPass:   false,
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/chat", nil)
			if tc.authHeader != "" {
				r.Header.Set("Authorization", tc.authHeader)
			}

			got := checkBearerAuth(context.Background(), w, r, cfg)
			assert.Equal(t, tc.wantPass, got.Authenticated,
				"checkBearerAuth(%q) result mismatch", tc.authHeader)

			if !tc.wantPass {
				assert.Equal(t, tc.wantStatus, w.Code,
					"failed auth must respond with %d", tc.wantStatus)
			}
		})
	}
}

// TestBearerAuthDisabledWhenTokenUnset verifies that when OMNIPUS_BEARER_TOKEN
// is not set, all requests are allowed (development mode).
// Traces to: wave3-skill-ecosystem-spec.md line 846 (Test #16 — unset token = allow all)
// BDD: Given OMNIPUS_BEARER_TOKEN is not set,
// When any request arrives,
// Then checkBearerAuth returns AuthResult{Authenticated: true} (auth not configured → dev mode).

func TestBearerAuthDisabledWhenTokenUnset(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 846 (Test #16)
	// Unset OMNIPUS_BEARER_TOKEN so os.Getenv returns "" (truly not set).
	// Note: t.Setenv("VAR", "") sets it to the empty string, which is still "set".
	// Using os.Unsetenv ensures the var is absent from the environment.
	os.Unsetenv("OMNIPUS_BEARER_TOKEN")

	cfg := &config.Config{
		Gateway: config.GatewayConfig{DevModeBypass: true}, // empty users, dev mode — falls back to env var (unset)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/chat", nil)
	// Provide "Bearer " prefix — dev mode skips token validation when
	// OMNIPUS_BEARER_TOKEN is unset, but the "Bearer " prefix is always required.
	r.Header.Set("Authorization", "Bearer ")
	got := checkBearerAuth(context.Background(), w, r, cfg)
	assert.True(t, got.Authenticated, "auth must pass when OMNIPUS_BEARER_TOKEN is not configured")
	assert.Equal(t, 200, w.Code, "no 401 written when token not configured")
}

// TestCheckBearerAuth_CLIToken_ValidAndInvalid covers the Gateway.CLIToken
// branch of checkBearerAuth (REST path) — previously untested anywhere in
// pkg/gateway (only exercised end-to-end over WebSocket by
// cmd/omnipus/internal/run/realgateway_integration_test.go). Proves: a
// valid CLI token authenticates over plain REST, an invalid one is rejected
// with 401 rather than silently falling through to the legacy
// OMNIPUS_BEARER_TOKEN env-var path (the fail-fast condition in
// checkBearerAuth — `len(cfg.Gateway.Users) > 0 || cfg.Gateway.CLIToken != nil`
// — is what this test guards: a typo turning that `||` into `&&` would
// still pass every other existing test but let an invalid CLI token fall
// through to the env fallback).
func TestCheckBearerAuth_CLIToken_ValidAndInvalid(t *testing.T) {
	os.Unsetenv("OMNIPUS_BEARER_TOKEN")

	plainToken := "omnipus_" + strings.Repeat("a", 64)
	hash, err := bcrypt.GenerateFromPassword([]byte(plainToken), bcrypt.MinCost)
	require.NoError(t, err)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			CLIToken: &config.TokenEntry{Hash: config.BcryptHash(hash)},
		},
	}

	t.Run("valid CLI token authenticates over REST", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/chat", nil)
		r.Header.Set("Authorization", "Bearer "+plainToken)
		got := checkBearerAuth(context.Background(), w, r, cfg)
		require.True(t, got.Authenticated, "a valid CLI token must authenticate")
		require.NotNil(t, got.User)
		assert.Equal(t, "cli", got.User.Username)
	})

	t.Run("invalid CLI token is rejected, does not fall through to env fallback", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/chat", nil)
		r.Header.Set("Authorization", "Bearer omnipus_"+strings.Repeat("f", 64))
		got := checkBearerAuth(context.Background(), w, r, cfg)
		assert.False(t, got.Authenticated, "an invalid CLI token must be rejected, not silently pass")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

// TestWithOptionalAuth_CLIToken_Authenticates proves the CLIToken check
// added to withOptionalAuth (previously missing entirely — this handler
// only checked Gateway.Users, so a CLI-token bearer on an optional-auth
// route like /api/v1/state silently downgraded to anonymous) now
// recognizes a valid CLI token and injects it into the request context.
func TestWithOptionalAuth_CLIToken_Authenticates(t *testing.T) {
	plainToken := "omnipus_" + strings.Repeat("b", 64)
	hash, err := bcrypt.GenerateFromPassword([]byte(plainToken), bcrypt.MinCost)
	require.NoError(t, err)

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			CLIToken: &config.TokenEntry{Hash: config.BcryptHash(hash)},
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	api := &restAPI{
		agentLoop:     mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{}),
		allowedOrigin: "http://localhost:3000",
	}

	var gotUser *config.UserConfig
	handler := api.withOptionalAuth(func(w http.ResponseWriter, r *http.Request) {
		gotUser, _ = r.Context().Value(UserContextKey{}).(*config.UserConfig)
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	r.Header.Set("Authorization", "Bearer "+plainToken)
	handler(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, gotUser, "a valid CLI token must be recognized, not silently downgraded to anonymous")
	assert.Equal(t, "cli", gotUser.Username)
}
