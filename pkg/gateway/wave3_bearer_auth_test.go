//go:build !cgo

// This test file uses //go:build !cgo so it compiles when CGO is disabled.
// When CGO is enabled, pkg/gateway imports pkg/channels/matrix which requires
// the libolm system library (olm/olm.h). If that library is installed,
// remove this build constraint and run tests normally.

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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
			// DevModeBypass: true is load-bearing for the "invalid CLI token"
			// subtest below — see its comment. It has no effect on the "valid
			// CLI token" subtest, which authenticates before reaching the
			// fail-fast condition or the env-fallback branch at all.
			DevModeBypass: true,
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
		// This subtest's fixture sets Gateway.DevModeBypass: true (see cfg
		// above) so the correct `||` path and the hypothetical `&&`-bug path
		// diverge OBSERVABLY:
		//   - correct `||`: len(cfg.Gateway.Users) > 0 (false) || cfg.Gateway.CLIToken != nil (true)
		//     = true → fail-fast rejects immediately with 401, regardless of
		//     DevModeBypass (that branch returns before step 3 is ever reached).
		//   - hypothetical `&&`-bug: len(cfg.Gateway.Users) > 0 (false) && cfg.Gateway.CLIToken != nil (true)
		//     = false → falls through to step 3 (env fallback); OMNIPUS_BEARER_TOKEN
		//     is unset, and because DevModeBypass is true, THAT branch allows the
		//     request (Authenticated: true, 200) instead of rejecting it.
		// Without DevModeBypass: true, both paths produced the same 401 (just via
		// different internal branches — "invalid Bearer token" vs "no users
		// configured, complete onboarding first") and this test could not tell
		// them apart. Verified by a temporary || -> && edit in auth.go: this
		// subtest goes RED (Authenticated flips to true, w.Code flips to 200)
		// under the bug and GREEN again once reverted.
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

// --- Second-account (Gateway.Users[1]) regression coverage ---
//
// Commit 9e7789ab restored a loop over ALL cfg.Gateway.Users entries in
// checkBearerAuth (auth.go), authenticateWS (websocket.go), and
// withOptionalAuth (rest_auth.go) — replacing an earlier design that only
// checked Gateway.Users[0] and would silently, permanently lock out any
// second configured account. Despite this being the stated purpose of that
// fix, no test anywhere in the repo proved a second account actually
// authenticates on any of the three call sites: every existing 2-user-config
// test (TestHandleLogin_BothCookiesEmitted,
// TestHandleLogin_DifferentInputProducesDifferentToken) only exercises
// HandleLogin (the password/cookie login flow), never a subsequent bearer
// check. Gap identified by pr-test-analyzer during the fix-wave review pass.
//
// Each test below builds a 2-account roster with genuine bcrypt-hashed
// bearer tokens and authenticates as the SECOND entry specifically,
// asserting the resolved identity is that second account (not the first).
// This is the actual regression guard: if any one of the three call sites
// were reverted to a Users[0]-only check, the second account's token would
// no longer match anything, and (because Gateway.Users is non-empty) each
// site's fail-fast/no-match branch would reject the request outright —
// flipping Authenticated to false / closing the WS connection / leaving the
// optional-auth context anonymous. These tests would go red on any one of
// the three sites regressing independently, even if the other two still
// pass.

// TestCheckBearerAuth_SecondAccountAuthenticates proves that Gateway.Users[1]
// (not just Users[0]) successfully authenticates through checkBearerAuth
// (the REST path, pkg/gateway/auth.go).
// BDD: Given Gateway.Users holds two accounts with distinct bearer tokens,
// When a request presents the SECOND account's token,
// Then checkBearerAuth returns AuthResult{Authenticated: true} with User
// resolved to that second account specifically.
// Traces to: fix-wave gap identified by pr-test-analyzer (guards the loop
// restored by commit 9e7789ab in checkBearerAuth).
func TestCheckBearerAuth_SecondAccountAuthenticates(t *testing.T) {
	os.Unsetenv("OMNIPUS_BEARER_TOKEN")

	token1 := "omnipus_" + strings.Repeat("1", 64)
	hash1, err := bcrypt.GenerateFromPassword([]byte(token1), bcrypt.MinCost)
	require.NoError(t, err)

	token2 := "omnipus_" + strings.Repeat("2", 64)
	hash2, err := bcrypt.GenerateFromPassword([]byte(token2), bcrypt.MinCost)
	require.NoError(t, err)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Users: []config.UserConfig{
				{Username: "account-one", Tokens: []config.TokenEntry{{Hash: config.BcryptHash(hash1)}}},
				{Username: "account-two", Tokens: []config.TokenEntry{{Hash: config.BcryptHash(hash2)}}},
			},
		},
	}

	t.Run("first account (Users[0]) authenticates", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/chat", nil)
		r.Header.Set("Authorization", "Bearer "+token1)
		got := checkBearerAuth(context.Background(), w, r, cfg)
		require.True(t, got.Authenticated)
		require.NotNil(t, got.User)
		assert.Equal(t, "account-one", got.User.Username)
	})

	t.Run("second account (Users[1]) authenticates", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/chat", nil)
		r.Header.Set("Authorization", "Bearer "+token2)
		got := checkBearerAuth(context.Background(), w, r, cfg)
		require.True(t, got.Authenticated,
			"Users[1] must authenticate — a Users[0]-only regression would reject this token")
		require.NotNil(t, got.User)
		assert.Equal(t, "account-two", got.User.Username,
			"resolved identity must be the SECOND account, not the first")
	})
}

// TestAuthenticateWS_SecondAccountAuthenticates proves that Gateway.Users[1]
// (not just Users[0]) successfully authenticates over the WebSocket auth
// handshake (authenticateWS, pkg/gateway/websocket.go). The resolved
// identity is carried as InboundMessage.GatewayUserID — the ONLY carrier of
// the WS-authenticated principal (FR-017, websocket.go ~line 1271, set at
// auth time via wc.userID) — so this test authenticates over a real
// WebSocket connection, sends a message frame, and inspects the message
// published to the bus.
// BDD: Given Gateway.Users holds two accounts with distinct bearer tokens,
// When a WebSocket client sends the auth frame with the SECOND account's
// token and then a message frame,
// Then the connection is accepted (message send succeeds) and the
// published InboundMessage.GatewayUserID is that second account's username.
// Traces to: fix-wave gap identified by pr-test-analyzer (guards the loop
// restored by commit 9e7789ab in authenticateWS).
func TestAuthenticateWS_SecondAccountAuthenticates(t *testing.T) {
	os.Unsetenv("OMNIPUS_BEARER_TOKEN")

	token1 := "omnipus_" + strings.Repeat("3", 64)
	hash1, err := bcrypt.GenerateFromPassword([]byte(token1), bcrypt.MinCost)
	require.NoError(t, err)

	token2 := "omnipus_" + strings.Repeat("4", 64)
	hash2, err := bcrypt.GenerateFromPassword([]byte(token2), bcrypt.MinCost)
	require.NoError(t, err)

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Host: "127.0.0.1", Port: 8080,
			Users: []config.UserConfig{
				{Username: "ws-account-one", Tokens: []config.TokenEntry{{Hash: config.BcryptHash(hash1)}}},
				{Username: "ws-account-two", Tokens: []config.TokenEntry{{Hash: config.BcryptHash(hash2)}}},
			},
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: tmpDir, ModelName: "test-model", MaxTokens: 4096},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newWSHandler(msgBus, al, "")
	t.Cleanup(handler.Wait)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	// Authenticate as the SECOND account.
	authFrame := wsClientFrameTestHelper{Type: "auth", Token: token2}
	authData, err := json.Marshal(authFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, authData))

	// Listen on the bus before sending the message.
	received := make(chan bus.InboundMessage, 1)
	go func() {
		select {
		case msg := <-msgBus.InboundChan():
			received <- msg
		case <-time.After(3 * time.Second):
		}
	}()

	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "hello from second account"}
	msgData, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, msgData),
		"message send must succeed after valid second-account auth — a Users[0]-only "+
			"regression would reject this token and close the connection")

	select {
	case msg := <-received:
		assert.Equal(t, "ws-account-two", msg.GatewayUserID,
			"authenticateWS must resolve the SECOND account, not the first")
	case <-time.After(3 * time.Second):
		t.Fatal("message was not published to bus within 3 seconds — authentication as Users[1] did not succeed")
	}
}

// TestWithOptionalAuth_SecondAccountAuthenticates proves that Gateway.Users[1]
// (not just Users[0]) successfully authenticates through withOptionalAuth
// (pkg/gateway/rest_auth.go) and is injected into the request context as
// the resolved identity — not silently downgraded to anonymous, and not
// misattributed to the first account.
// BDD: Given Gateway.Users holds two accounts with distinct bearer tokens,
// When an optional-auth request presents the SECOND account's token,
// Then the handler observes a non-nil *config.UserConfig in context whose
// Username is that second account's, and the request succeeds (200).
// Traces to: fix-wave gap identified by pr-test-analyzer (guards the loop
// restored by commit 9e7789ab in withOptionalAuth).
func TestWithOptionalAuth_SecondAccountAuthenticates(t *testing.T) {
	token1 := "omnipus_" + strings.Repeat("5", 64)
	hash1, err := bcrypt.GenerateFromPassword([]byte(token1), bcrypt.MinCost)
	require.NoError(t, err)

	token2 := "omnipus_" + strings.Repeat("6", 64)
	hash2, err := bcrypt.GenerateFromPassword([]byte(token2), bcrypt.MinCost)
	require.NoError(t, err)

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Users: []config.UserConfig{
				{Username: "opt-account-one", Tokens: []config.TokenEntry{{Hash: config.BcryptHash(hash1)}}},
				{Username: "opt-account-two", Tokens: []config.TokenEntry{{Hash: config.BcryptHash(hash2)}}},
			},
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
	r.Header.Set("Authorization", "Bearer "+token2)
	handler(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, gotUser,
		"a valid second-account token must be recognized, not silently downgraded to anonymous")
	assert.Equal(t, "opt-account-two", gotUser.Username,
		"resolved identity must be the SECOND account, not the first")
}
