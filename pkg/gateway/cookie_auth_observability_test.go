// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// cookie_auth_observability_test.go — SFH-1 (preview-on-main-listener pass-2
// fix round) regression coverage.
//
// Before this fix, the three LIVE cookie-fallback auth sites — checkBearerAuth
// (auth.go), WSHandler.authenticateWS (websocket.go), and
// BrowserWSHandler.authenticate (browser_ws.go) — each called
// middleware.ResolveUserFromCookie and, on a miss, silently fell through to
// their next auth mechanism with NO log line at all — even when the request
// carried an omnipus-session cookie that failed to bcrypt-match any
// configured user (a replay/probe/stale-cookie signal, distinct from the
// routine "no cookie at all" case). The equivalent detection already existed
// inside middleware.RequireSessionCookieOrBearer (which is not wired into any
// live handler chain), so the fix extracts it into the exported
// middleware.LogInvalidSessionCookiePresent helper and wires that helper into
// all three sites without changing any auth outcome.
//
// This file proves two things:
//  1. The helper itself has the right logs-vs-silent behavior in isolation.
//  2. Each of the three real call sites is actually wired to it — not just
//     that the helper works standalone.

package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// ---------------------------------------------------------------------------
// Test scaffolding
// ---------------------------------------------------------------------------

// cookieAuthLogRecorder captures slog record messages for assertion,
// installed via slog.SetDefault. This mirrors the equivalent unexported
// slogRecorder in pkg/gateway/middleware/session_cookie_test.go — that type
// isn't importable here across the package boundary, so this is a small,
// intentional, test-local duplication rather than a shared export.
type cookieAuthLogRecorder struct {
	mu      sync.Mutex
	records []string
}

func (r *cookieAuthLogRecorder) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (r *cookieAuthLogRecorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec.Message)
	return nil
}

func (r *cookieAuthLogRecorder) WithAttrs(_ []slog.Attr) slog.Handler { return r }
func (r *cookieAuthLogRecorder) WithGroup(_ string) slog.Handler      { return r }

// contains reports whether any captured log message contains substr.
func (r *cookieAuthLogRecorder) contains(substr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, msg := range r.records {
		if strings.Contains(msg, substr) {
			return true
		}
	}
	return false
}

// empty reports whether no log messages were captured at all.
func (r *cookieAuthLogRecorder) empty() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.records) == 0
}

// installCookieAuthLogRecorder swaps slog's default logger for a capturing
// recorder for the duration of the test and restores the original on cleanup.
func installCookieAuthLogRecorder(t *testing.T) *cookieAuthLogRecorder {
	t.Helper()
	recorder := &cookieAuthLogRecorder{}
	old := slog.Default()
	slog.SetDefault(slog.New(recorder))
	t.Cleanup(func() { slog.SetDefault(old) })
	return recorder
}

// mustBcryptHash bcrypt-hashes plaintext at the fast MinCost (test speed —
// mirrors the convention used throughout this package's other auth tests).
func mustBcryptHash(t *testing.T, plaintext string) config.BcryptHash {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.MinCost)
	require.NoError(t, err)
	return config.BcryptHash(hash)
}

// ---------------------------------------------------------------------------
// 1. Direct unit tests of middleware.LogInvalidSessionCookiePresent
// ---------------------------------------------------------------------------

// TestLogInvalidSessionCookiePresent_LogsWhenCookiePresentButInvalid proves
// the helper logs when the request carries an omnipus-session cookie whose
// value bcrypt-matches no configured user.
// BDD: Given a request with an invalid omnipus-session cookie, When
// LogInvalidSessionCookiePresent is called, Then a log entry is emitted
// containing "cookie present but invalid".
func TestLogInvalidSessionCookiePresent_LogsWhenCookiePresentButInvalid(t *testing.T) {
	recorder := installCookieAuthLogRecorder(t)

	cfg := &config.Config{Gateway: config.GatewayConfig{AuthMismatchLogLevel: "warn"}}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "not-a-real-session-token"})

	middleware.LogInvalidSessionCookiePresent(r, cfg)

	assert.True(t, recorder.contains("cookie present but invalid"),
		"a present-but-invalid omnipus-session cookie must be logged")
}

// TestLogInvalidSessionCookiePresent_SilentWhenNoCookie proves the helper is
// silent (emits nothing) when the request carries no omnipus-session cookie
// at all — the routine "not authenticated via cookie" case (e.g. a
// bearer-only/CLI request), which is NOT a security signal and must not be
// logged.
// BDD: Given a request with no omnipus-session cookie, When
// LogInvalidSessionCookiePresent is called, Then no log entry is emitted.
func TestLogInvalidSessionCookiePresent_SilentWhenNoCookie(t *testing.T) {
	recorder := installCookieAuthLogRecorder(t)

	cfg := &config.Config{Gateway: config.GatewayConfig{AuthMismatchLogLevel: "warn"}}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	// Deliberately no cookie at all.

	middleware.LogInvalidSessionCookiePresent(r, cfg)

	assert.True(t, recorder.empty(), "no cookie at all must never produce a log entry")
}

// TestLogInvalidSessionCookiePresent_NilConfigStillLogs is a defensive-path
// test: a nil *config.Config (production never passes one at this point, but
// the helper must not assume) must not panic and must still surface the
// cookie-present-but-invalid signal at the resolver's default level ("warn")
// rather than silently skipping the log because cfg was unavailable.
func TestLogInvalidSessionCookiePresent_NilConfigStillLogs(t *testing.T) {
	recorder := installCookieAuthLogRecorder(t)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "not-a-real-session-token"})

	require.NotPanics(t, func() { middleware.LogInvalidSessionCookiePresent(r, nil) })
	assert.True(t, recorder.contains("cookie present but invalid"),
		"a nil cfg must still log the cookie-present-but-invalid signal")
}

// ---------------------------------------------------------------------------
// 2. Wiring proof: the three LIVE cookie-fallback call sites actually invoke
// the helper — not just the helper working correctly in isolation.
// ---------------------------------------------------------------------------

// TestCheckBearerAuth_LogsInvalidSessionCookie proves checkBearerAuth
// (auth.go) — the REST auth path — calls LogInvalidSessionCookiePresent when
// a request carries an invalid omnipus-session cookie, and that the fail-
// closed 401 outcome is completely unchanged.
func TestCheckBearerAuth_LogsInvalidSessionCookie(t *testing.T) {
	recorder := installCookieAuthLogRecorder(t)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Users: []config.UserConfig{
				{Username: "real-user", SessionTokenHash: mustBcryptHash(t, "the-real-session-token")},
			},
			AuthMismatchLogLevel: "warn",
		},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	r.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "an-invalid-session-cookie-value"})
	// Deliberately no Authorization header — isolating the cookie-fallback path.

	got := checkBearerAuth(context.Background(), w, r, cfg)

	assert.False(t, got.Authenticated, "an invalid cookie must still fail closed (401) — SFH-1 is log-only")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.True(t, recorder.contains("cookie present but invalid"),
		"checkBearerAuth must log the SFH-1 signal for a present-but-invalid cookie")
}

// TestCheckBearerAuth_NoLogWhenNoCookieAtAll is the negative companion: a
// request with neither a cookie nor a Bearer header must NOT emit the SFH-1
// log line — that is the routine, silent "no credential presented" case.
func TestCheckBearerAuth_NoLogWhenNoCookieAtAll(t *testing.T) {
	recorder := installCookieAuthLogRecorder(t)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Users: []config.UserConfig{
				{Username: "real-user", SessionTokenHash: mustBcryptHash(t, "the-real-session-token")},
			},
			AuthMismatchLogLevel: "warn",
		},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	// Deliberately no cookie, no Authorization header.

	got := checkBearerAuth(context.Background(), w, r, cfg)

	assert.False(t, got.Authenticated)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.False(t, recorder.contains("cookie present but invalid"),
		"no cookie at all must never emit the SFH-1 signal")
}

// TestAuthenticateWS_LogsInvalidSessionCookie proves WSHandler.authenticateWS
// (websocket.go, the chat WS handshake) calls LogInvalidSessionCookiePresent
// when the upgrade request carries an invalid omnipus-session cookie, even
// though the connection goes on to authenticate successfully via the
// frame-based fallback (proving the log call doesn't disturb that fallback).
func TestAuthenticateWS_LogsInvalidSessionCookie(t *testing.T) {
	os.Unsetenv("OMNIPUS_BEARER_TOKEN")
	recorder := installCookieAuthLogRecorder(t)

	bearerPlain := "omnipus_" + strings.Repeat("9", 64)
	bearerHash, err := bcrypt.GenerateFromPassword([]byte(bearerPlain), bcrypt.MinCost)
	require.NoError(t, err)

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Host: "127.0.0.1", Port: 8080,
			Users: []config.UserConfig{
				{Username: "ws-frame-user", Tokens: []config.TokenEntry{{Hash: config.BcryptHash(bearerHash)}}},
			},
			AuthMismatchLogLevel: "warn",
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			// An explicitly registered agent. There is no implicit "main"
			// sentinel to fall back on (ADR-064), and handleChatMessage now
			// REFUSES a chat frame it cannot resolve an agent for rather than
			// publishing a message owned by nobody — so with an empty roster
			// nothing ever reaches the bus and this test waits out its 30s.
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newWSHandler(msgBus, al, "")
	t.Cleanup(handler.Wait)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Dial carrying an invalid cookie — authenticateWS must miss the cookie
	// path and fall through to the blocking frame read.
	conn := dialTestWSWithCookie(t, srv, "an-invalid-session-cookie-value")
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness

	authFrame := wsClientFrameTestHelper{Type: "auth", Token: bearerPlain}
	authData, err := json.Marshal(authFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, authData),
		"the frame-based fallback must still authenticate after an invalid cookie")

	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "hello after invalid cookie"}
	msgData, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, msgData))

	msg := awaitInboundMessage(t, msgBus, "frame-fallback auth must complete and publish the message")
	assert.Equal(t, "ws-frame-user", msg.GatewayUserID,
		"the connection must still authenticate via the frame fallback")

	assert.True(t, recorder.contains("cookie present but invalid"),
		"authenticateWS must log the SFH-1 signal for a present-but-invalid cookie before falling "+
			"through to the frame-based auth path")
}

// TestBrowserWSAuthenticate_LogsInvalidSessionCookie proves
// BrowserWSHandler.authenticate (browser_ws.go) calls
// LogInvalidSessionCookiePresent when the upgrade request carries an invalid
// omnipus-session cookie, mirroring the authenticateWS proof above for the
// browser-live socket.
func TestBrowserWSAuthenticate_LogsInvalidSessionCookie(t *testing.T) {
	os.Unsetenv("OMNIPUS_BEARER_TOKEN")
	recorder := installCookieAuthLogRecorder(t)

	bearerPlain := "omnipus_" + strings.Repeat("8", 64)
	bearerHash, err := bcrypt.GenerateFromPassword([]byte(bearerPlain), bcrypt.MinCost)
	require.NoError(t, err)

	handler, _ := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Gateway.Users = []config.UserConfig{
			{Username: "browser-frame-user", Tokens: []config.TokenEntry{{Hash: config.BcryptHash(bearerHash)}}},
		}
		cfg.Gateway.AuthMismatchLogLevel = "warn"
	})
	t.Cleanup(handler.Wait)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Dial carrying an invalid cookie (dialBrowserTestWS doesn't take
	// headers, so this mirrors its logic directly with a Cookie header set).
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/browser/ws"
	header := http.Header{}
	header.Set("Cookie", middleware.SessionCookieName+"=an-invalid-session-cookie-value")
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, httpResp, err := dialer.Dial(wsURL, header)
	if httpResp != nil {
		httpResp.Body.Close()
	}
	require.NoError(t, err, "browser WebSocket dial must succeed")
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness

	writeBrowserAuthFrame(t, conn, bearerPlain)
	assertBrowserConnProceeds(t, conn)

	assert.True(t, recorder.contains("cookie present but invalid"),
		"BrowserWSHandler.authenticate must log the SFH-1 signal for a present-but-invalid cookie "+
			"before falling through to the frame-based auth path")
}
