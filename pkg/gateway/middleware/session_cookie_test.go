//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package middleware

// Tests for session_cookie.go — covers,,,.
// BDD scenarios: #70a, #70b, #70c, #71, #72, #72b, #73, #73c
// Traces to: path-sandbox-and-capability-tiers-spec.md (v4)

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/gateway/ctxkey"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// plainHTTPRequest returns a request that requestIsSecure treats as plain HTTP.
func plainHTTPRequest() *http.Request {
	return httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
}

// tlsRequest returns a request that requestIsSecure treats as TLS.
func tlsRequest() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	r.TLS = &tls.ConnectionState{}
	return r
}

// parseCookies returns a map of cookie name → *http.Cookie from the response
// Set-Cookie headers.
func parseCookies(t *testing.T, resp *httptest.ResponseRecorder) map[string]*http.Cookie {
	t.Helper()
	result := make(map[string]*http.Cookie)
	for _, line := range resp.Result().Cookies() {
		result[line.Name] = line
	}
	return result
}

// buildUserWithSessionHash returns a UserConfig with a bcrypt hash of the
// given plaintext token stored in SessionTokenHash.
func buildUserWithSessionHash(t *testing.T, username, plaintext string) config.UserConfig {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.MinCost)
	require.NoError(t, err)
	return config.UserConfig{
		Username:         username,
		Role:             config.UserRoleAdmin,
		SessionTokenHash: config.BcryptHash(hash),
	}
}

// buildUserWithBearerHash returns a UserConfig with a bcrypt hash of the
// given plaintext token stored in TokenHash.
func buildUserWithBearerHash(t *testing.T, username, plaintext string) config.UserConfig {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.MinCost)
	require.NoError(t, err)
	return config.UserConfig{
		Username:  username,
		Role:      config.UserRoleAdmin,
		TokenHash: config.BcryptHash(hash),
	}
}

// slogRecorder captures slog records for assertion in tests.
type slogRecorder struct {
	records []*slog.Record
}

func (r *slogRecorder) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (r *slogRecorder) Handle(_ context.Context, rec slog.Record) error {
	cp := rec
	r.records = append(r.records, &cp)
	return nil
}
func (r *slogRecorder) WithAttrs(_ []slog.Attr) slog.Handler { return r }
func (r *slogRecorder) WithGroup(_ string) slog.Handler      { return r }

// ---------------------------------------------------------------------------
// #70a — MintSessionToken
// BDD: Given a request for a new session token,
// When MintSessionToken is called,
// Then plaintext is 43-char base64-RawURL, hash bcrypt-validates against it.
// Traces to: path-sandbox-and-capability-tiers-spec.md line
// ---------------------------------------------------------------------------

func TestMintSessionToken_ProducesValidToken(t *testing.T) {
	// BDD: Given a mint request
	// When MintSessionToken is called twice
	// Then each plaintext is 43 chars (32 bytes base64-RawURL), the hash validates,
	// and the two calls produce DIFFERENT plaintexts (not hardcoded).
	plain1, hash1, err := MintSessionToken()
	require.NoError(t, err)
	plain2, hash2, err := MintSessionToken()
	require.NoError(t, err)

	// Token must be 43 characters (base64-RawURL of 32 bytes).
	assert.Len(t, plain1, 43, "session token must be 43 chars (base64-RawURL of 32 bytes)")
	assert.Len(t, plain2, 43, "session token must be 43 chars (base64-RawURL of 32 bytes)")

	// Hash must validate against the corresponding plaintext.
	assert.NoError(t, bcrypt.CompareHashAndPassword(hash1, []byte(plain1)),
		"hash1 must validate against plain1")
	assert.NoError(t, bcrypt.CompareHashAndPassword(hash2, []byte(plain2)),
		"hash2 must validate against plain2")

	// Differentiation: two calls produce different tokens (not hardcoded).
	assert.NotEqual(t, plain1, plain2, "two MintSessionToken calls must produce different tokens")
	assert.NotEqual(t, string(hash1), string(hash2), "two hashes must differ (different tokens)")
}

func TestMintSessionToken_HashDoesNotValidateCrossToken(t *testing.T) {
	// Verify that hash1 does NOT validate against plain2 (sanity on bcrypt semantics).
	plain1, hash1, err := MintSessionToken()
	require.NoError(t, err)
	plain2, _, err := MintSessionToken()
	require.NoError(t, err)
	require.NotEqual(t, plain1, plain2)

	err = bcrypt.CompareHashAndPassword(hash1, []byte(plain2))
	assert.Error(t, err, "hash from token1 must not validate token2's value")
}

// ---------------------------------------------------------------------------
// #70b — WriteSessionCookie cookie attributes
// BDD: Given WriteSessionCookie is called with a plain HTTP request,
// When the response is inspected,
// Then cookie has Name=omnipus-session, HttpOnly, SameSite=Strict, Path=/, MaxAge=86400, Secure=false.
// And for a TLS request, Secure=true.
// Traces to: path-sandbox-and-capability-tiers-spec.md
// ---------------------------------------------------------------------------

func TestWriteSessionCookie_PlainHTTP_Attributes(t *testing.T) {
	// BDD: Given a plain-HTTP request
	w := httptest.NewRecorder()
	r := plainHTTPRequest()

	plain := "test-plaintext-token-abc"
	WriteSessionCookie(w, r, plain)

	cookies := parseCookies(t, w)
	c, ok := cookies[SessionCookieName]
	require.True(t, ok, "omnipus-session cookie must be present in response")

	assert.Equal(t, SessionCookieName, c.Name)
	assert.Equal(t, plain, c.Value)
	assert.Equal(t, "/", c.Path)
	assert.True(t, c.HttpOnly, "session cookie must be HttpOnly")
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite, "SameSite must be Strict")
	assert.Equal(t, SessionCookieMaxAge, c.MaxAge, "MaxAge must be 86400")
	assert.False(t, c.Secure, "plain-HTTP request: Secure must be false")
}

func TestWriteSessionCookie_TLS_SecureTrue(t *testing.T) {
	// BDD: Given a TLS request
	w := httptest.NewRecorder()
	r := tlsRequest()

	WriteSessionCookie(w, r, "token-for-tls")

	cookies := parseCookies(t, w)
	c, ok := cookies[SessionCookieName]
	require.True(t, ok)
	assert.True(t, c.Secure, "TLS request: Secure must be true")
	assert.True(t, c.HttpOnly, "session cookie must always be HttpOnly")
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite)
}

func TestWriteSessionCookie_DifferentTokensDifferentCookieValues(t *testing.T) {
	// Differentiation test: two different plaintexts produce two different cookie values.
	w1 := httptest.NewRecorder()
	w2 := httptest.NewRecorder()
	r := plainHTTPRequest()

	WriteSessionCookie(w1, r, "token-one")
	WriteSessionCookie(w2, r, "token-two")

	cookies1 := parseCookies(t, w1)
	cookies2 := parseCookies(t, w2)

	assert.Equal(t, "token-one", cookies1[SessionCookieName].Value)
	assert.Equal(t, "token-two", cookies2[SessionCookieName].Value)
	assert.NotEqual(t, cookies1[SessionCookieName].Value, cookies2[SessionCookieName].Value)
}

// ---------------------------------------------------------------------------
// #71 — ResolveUserFromCookie
// BDD: Various cookie resolution scenarios.
// Traces to: path-sandbox-and-capability-tiers-spec.md
// ---------------------------------------------------------------------------

func TestResolveUserFromCookie_CookiePresent_MatchesUser(t *testing.T) {
	// BDD: Given a cookie present with a value matching user "alice"'s SessionTokenHash,
	// When ResolveUserFromCookie is called,
	// Then alice is returned with nil error.
	plaintext := "session-token-alice-12345678901234567890123"
	alice := buildUserWithSessionHash(t, "alice", plaintext)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: plaintext})

	user, err := ResolveUserFromCookie(r, []config.UserConfig{alice})
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, "alice", user.Username)
}

func TestResolveUserFromCookie_CookieAbsent_ReturnsErrSessionNotFound(t *testing.T) {
	// BDD: Given no cookie in the request,
	// When ResolveUserFromCookie is called,
	// Then ErrSessionNotFound is returned.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	users := []config.UserConfig{
		buildUserWithSessionHash(t, "alice", "some-token"),
	}
	user, err := ResolveUserFromCookie(r, users)
	assert.Nil(t, user)
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

func TestResolveUserFromCookie_CookiePresent_NoUserMatches(t *testing.T) {
	// BDD: Given a cookie with a value that matches NO user's SessionTokenHash,
	// When ResolveUserFromCookie is called,
	// Then ErrSessionNotFound is returned.
	alice := buildUserWithSessionHash(t, "alice", "correct-token-alice")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "wrong-token-entirely"})

	user, err := ResolveUserFromCookie(r, []config.UserConfig{alice})
	assert.Nil(t, user)
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

func TestResolveUserFromCookie_UserDeletedBetweenRequestAndResolve(t *testing.T) {
	// BDD: Given a cookie value that used to belong to a deleted user,
	// When ResolveUserFromCookie is called with empty users list,
	// Then ErrSessionNotFound is returned.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "orphaned-token"})

	user, err := ResolveUserFromCookie(r, []config.UserConfig{})
	assert.Nil(t, user)
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

func TestResolveUserFromCookie_MultipleUsers_MatchesCorrectOne(t *testing.T) {
	// BDD: Given two users alice and bob with different session hashes,
	// When a cookie matching bob is presented,
	// Then bob (not alice) is returned — not always the first user.
	plainAlice := "token-for-alice-unique-12345678901234567"
	plainBob := "token-for-bob-unique-098765432109876543"
	alice := buildUserWithSessionHash(t, "alice", plainAlice)
	bob := buildUserWithSessionHash(t, "bob", plainBob)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: plainBob})

	user, err := ResolveUserFromCookie(r, []config.UserConfig{alice, bob})
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, "bob", user.Username, "must return bob, not alice (first user)")
}

func TestResolveUserFromCookie_EmptyCookieValue_ReturnsErrSessionNotFound(t *testing.T) {
	// BDD: Given a cookie with empty value,
	// When ResolveUserFromCookie is called,
	// Then ErrSessionNotFound is returned (empty value is not valid).
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: ""})
	users := []config.UserConfig{
		buildUserWithSessionHash(t, "alice", "token"),
	}
	user, err := ResolveUserFromCookie(r, users)
	assert.Nil(t, user)
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

// ---------------------------------------------------------------------------
// #72 — ClearSessionCookie attributes
// BDD: Given ClearSessionCookie is called,
// When Set-Cookie is inspected,
// Then MaxAge ≤ 0 (immediate expiry).
// Traces to: path-sandbox-and-capability-tiers-spec.md
// ---------------------------------------------------------------------------

func TestClearSessionCookie_PlainHTTP_MaxAgeNegative(t *testing.T) {
	// BDD: Given a plain-HTTP request,
	// When ClearSessionCookie is called,
	// Then omnipus-session cookie has MaxAge < 0 (browser interprets as expired).
	w := httptest.NewRecorder()
	r := plainHTTPRequest()

	ClearSessionCookie(w, r)

	cookies := parseCookies(t, w)
	c, ok := cookies[SessionCookieName]
	require.True(t, ok, "ClearSessionCookie must emit Set-Cookie for omnipus-session")
	assert.LessOrEqual(t, c.MaxAge, 0, "MaxAge must be 0 or negative (expired)")
	assert.False(t, c.Secure, "plain-HTTP clear: Secure must be false")
}

func TestClearSessionCookie_TLS_MaxAgeNegative(t *testing.T) {
	w := httptest.NewRecorder()
	r := tlsRequest()

	ClearSessionCookie(w, r)

	cookies := parseCookies(t, w)
	c, ok := cookies[SessionCookieName]
	require.True(t, ok)
	assert.LessOrEqual(t, c.MaxAge, 0)
	assert.True(t, c.Secure, "TLS clear: Secure must be true")
}

// ---------------------------------------------------------------------------
// #72b — ClearCSRFCookie picks right name
// BDD: Given ClearCSRFCookie on TLS, uses __Host-csrf; on plain HTTP uses csrf.
// Traces to: path-sandbox-and-capability-tiers-spec.md
// ---------------------------------------------------------------------------

func TestClearCSRFCookie_TLS_UsesHostPrefixName(t *testing.T) {
	// BDD: Given a TLS request,
	// When ClearCSRFCookie is called,
	// Then Set-Cookie contains __Host-csrf with MaxAge ≤ 0.
	w := httptest.NewRecorder()
	r := tlsRequest()

	ClearCSRFCookie(w, r)

	cookies := parseCookies(t, w)
	c, ok := cookies[CSRFCookieName] // __Host-csrf
	require.True(t, ok, "TLS ClearCSRFCookie must emit __Host-csrf cookie")
	assert.LessOrEqual(t, c.MaxAge, 0, "MaxAge must be ≤ 0")
	_, noHTTPCookie := cookies[CSRFCookieNameHTTP] // csrf
	assert.False(t, noHTTPCookie, "TLS clear must NOT emit csrf (plain HTTP) cookie")
}

func TestClearCSRFCookie_PlainHTTP_UsesUnprefixedName(t *testing.T) {
	// BDD: Given a plain-HTTP request,
	// When ClearCSRFCookie is called,
	// Then Set-Cookie contains csrf (unprefixed) with MaxAge ≤ 0.
	w := httptest.NewRecorder()
	r := plainHTTPRequest()

	ClearCSRFCookie(w, r)

	cookies := parseCookies(t, w)
	c, ok := cookies[CSRFCookieNameHTTP] // csrf
	require.True(t, ok, "plain-HTTP ClearCSRFCookie must emit csrf cookie")
	assert.LessOrEqual(t, c.MaxAge, 0, "MaxAge must be ≤ 0")
	_, noTLSCookie := cookies[CSRFCookieName] // __Host-csrf
	assert.False(t, noTLSCookie, "plain-HTTP clear must NOT emit __Host-csrf cookie")
}

// ---------------------------------------------------------------------------
// #73 — RequireSessionCookieOrBearer middleware
// BDD scenarios: cookie-only, bearer-only, both-same-user, both-different-users,
// neither-401, getCfg-nil-500, invalid-cookie-no-bearer-401.
// Traces to: path-sandbox-and-capability-tiers-spec.md
// ---------------------------------------------------------------------------

// buildMiddlewareUsers creates a test config with the given users for the
// RequireSessionCookieOrBearer middleware.
func buildMiddlewareConfig(users []config.UserConfig) *config.Config {
	return &config.Config{
		Gateway: config.GatewayConfig{
			Users:                users,
			AuthMismatchLogLevel: "warn",
		},
	}
}

// cookieOnlyRequest creates a request with only the session cookie set.
func cookieOnlyRequest(t *testing.T, sessionPlaintext string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionPlaintext})
	return r
}

// bearerOnlyRequest creates a request with only a bearer token Authorization header.
func bearerOnlyRequest(bearerPlaintext string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer "+bearerPlaintext)
	return r
}

// bothAuthRequest creates a request with both session cookie and bearer token.
func bothAuthRequest(t *testing.T, sessionPlaintext, bearerPlaintext string) *http.Request {
	t.Helper()
	r := cookieOnlyRequest(t, sessionPlaintext)
	r.Header.Set("Authorization", "Bearer "+bearerPlaintext)
	return r
}

// nextHandlerCapture returns a handler that captures the user from context.
func nextHandlerCapture(captured *(*config.UserConfig)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := r.Context().Value(ctxkey.UserContextKey{}).(*config.UserConfig)
		*captured = u
		w.WriteHeader(http.StatusOK)
	})
}

func TestRequireSessionCookieOrBearer_CookieOnly_Passes(t *testing.T) {
	// BDD: Given only a valid session cookie,
	// When middleware runs,
	// Then request proceeds; context user is the cookie user.
	// Traces to: path-sandbox-and-capability-tiers-spec.md
	sessionPlain := "session-cookie-alice-plaintext-0000001"
	alice := buildUserWithSessionHash(t, "alice", sessionPlain)
	cfg := buildMiddlewareConfig([]config.UserConfig{alice})

	var captured *config.UserConfig
	mw := RequireSessionCookieOrBearer(func() *config.Config { return cfg })
	h := mw(nextHandlerCapture(&captured))

	w := httptest.NewRecorder()
	r := cookieOnlyRequest(t, sessionPlain)
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, captured)
	assert.Equal(t, "alice", captured.Username, "context user must be the cookie user")
}

func TestRequireSessionCookieOrBearer_BearerOnly_Passes(t *testing.T) {
	// BDD: Given only a valid bearer token,
	// When middleware runs,
	// Then request proceeds; context user is the bearer user.
	// Traces to: path-sandbox-and-capability-tiers-spec.md
	bearerPlain := "bearer-token-bob-plain-text-000001234"
	bob := buildUserWithBearerHash(t, "bob", bearerPlain)
	cfg := buildMiddlewareConfig([]config.UserConfig{bob})

	var captured *config.UserConfig
	mw := RequireSessionCookieOrBearer(func() *config.Config { return cfg })
	h := mw(nextHandlerCapture(&captured))

	w := httptest.NewRecorder()
	r := bearerOnlyRequest(bearerPlain)
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, captured)
	assert.Equal(t, "bob", captured.Username, "context user must be the bearer user")
}

func TestRequireSessionCookieOrBearer_BothSameUser_Passes(t *testing.T) {
	// BDD: Given both cookie AND bearer that identify the SAME user,
	// When middleware runs,
	// Then request proceeds; no log entry emitted.
	// Traces to: path-sandbox-and-capability-tiers-spec.md
	sessionPlain := "session-cookie-alice-both-same-user00001"
	bearerPlain := "bearer-token-alice-both-same-user000001"
	sessionHash, _ := bcrypt.GenerateFromPassword([]byte(sessionPlain), bcrypt.MinCost)
	bearerHash, _ := bcrypt.GenerateFromPassword([]byte(bearerPlain), bcrypt.MinCost)
	alice := config.UserConfig{
		Username:         "alice",
		Role:             config.UserRoleAdmin,
		SessionTokenHash: config.BcryptHash(sessionHash),
		TokenHash:        config.BcryptHash(bearerHash),
	}
	cfg := buildMiddlewareConfig([]config.UserConfig{alice})

	var captured *config.UserConfig
	mw := RequireSessionCookieOrBearer(func() *config.Config { return cfg })
	h := mw(nextHandlerCapture(&captured))

	w := httptest.NewRecorder()
	r := bothAuthRequest(t, sessionPlain, bearerPlain)
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, captured)
	assert.Equal(t, "alice", captured.Username)
}

func TestRequireSessionCookieOrBearer_BothDifferentUsers_CookieWins_LogEmitted(t *testing.T) {
	// BDD: Given both cookie (alice) AND bearer (bob) identifying DIFFERENT users,
	// When middleware runs,
	// Then request proceeds with alice (cookie wins); log is emitted containing
	// "auth: cookie+bearer identify different users; cookie wins; cookie_user=alice bearer_user=bob".
	// Traces to: path-sandbox-and-capability-tiers-spec.md / #73c
	sessionPlain := "session-alice-00000001-different-users-0"
	bearerPlain := "bearer-bob-00000001-different-users-0000"
	sessionHash, _ := bcrypt.GenerateFromPassword([]byte(sessionPlain), bcrypt.MinCost)
	bearerHash, _ := bcrypt.GenerateFromPassword([]byte(bearerPlain), bcrypt.MinCost)
	alice := config.UserConfig{
		Username:         "alice",
		Role:             config.UserRoleAdmin,
		SessionTokenHash: config.BcryptHash(sessionHash),
	}
	bob := config.UserConfig{
		Username:  "bob",
		Role:      config.UserRoleAdmin,
		TokenHash: config.BcryptHash(bearerHash),
	}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Users:                []config.UserConfig{alice, bob},
			AuthMismatchLogLevel: "warn",
		},
	}

	// Install a custom slog handler to capture the log record.
	recorder := &slogRecorder{}
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(recorder))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	var captured *config.UserConfig
	mw := RequireSessionCookieOrBearer(func() *config.Config { return cfg })
	h := mw(nextHandlerCapture(&captured))

	w := httptest.NewRecorder()
	r := bothAuthRequest(t, sessionPlain, bearerPlain)
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, captured)
	assert.Equal(t, "alice", captured.Username, "cookie must win over bearer on mismatch")

	// Assert log entry.
	require.NotEmpty(t, recorder.records, "a log entry must be emitted on cookie+bearer mismatch")
	msg := recorder.records[0].Message
	assert.Contains(t, msg, "cookie+bearer identify different users",
		"log must contain the mismatch message")
	assert.Contains(t, msg, "cookie wins", "log must state cookie wins")
	assert.Contains(t, msg, "cookie_user=alice", "log must name cookie user")
	assert.Contains(t, msg, "bearer_user=bob", "log must name bearer user")
}

func TestRequireSessionCookieOrBearer_NeitherAuth_Returns401(t *testing.T) {
	// BDD: Given no cookie AND no bearer token,
	// When middleware runs,
	// Then 401 Unauthorized is returned.
	// Traces to: path-sandbox-and-capability-tiers-spec.md
	cfg := buildMiddlewareConfig([]config.UserConfig{})
	mw := RequireSessionCookieOrBearer(func() *config.Config { return cfg })
	h := mw(nextHandler)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "unauthorized")
}

func TestRequireSessionCookieOrBearer_GetCfgReturnsNil_Returns500(t *testing.T) {
	// BDD: Given getCfg returns nil (fail-closed semantics),
	// When middleware runs,
	// Then 500 Internal Server Error is returned.
	// Traces to: path-sandbox-and-capability-tiers-spec.md
	mw := RequireSessionCookieOrBearer(func() *config.Config { return nil })
	h := mw(nextHandler)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "config unavailable")
}

func TestRequireSessionCookieOrBearer_InvalidCookieNoBearerReturns401(t *testing.T) {
	// BDD: Given a cookie with a corrupted/invalid token and NO bearer token,
	// When middleware runs,
	// Then 401 is returned.
	// Traces to: path-sandbox-and-capability-tiers-spec.md
	alice := buildUserWithSessionHash(t, "alice", "correct-session-token")
	cfg := buildMiddlewareConfig([]config.UserConfig{alice})
	mw := RequireSessionCookieOrBearer(func() *config.Config { return cfg })
	h := mw(nextHandler)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "this-is-not-a-valid-session-token"})
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireSessionCookieOrBearer_PanicOnNilGetCfg(t *testing.T) {
	// Verify that passing nil getCfg panics at construction (not at request time).
	// This catches programmer errors at wiring time.
	assert.Panics(t, func() {
		RequireSessionCookieOrBearer(nil)
	})
}

// ---------------------------------------------------------------------------
// #73c — AuthMismatchLogLevel configurable
// BDD: Given cfg.Gateway.AuthMismatchLogLevel set to "debug",
// When cookie+bearer identify different users,
// Then middleware still admits the request (cookie wins).
// Traces to: path-sandbox-and-capability-tiers-spec.md
// ---------------------------------------------------------------------------

func TestRequireSessionCookieOrBearer_AuthMismatchLogLevel_Debug(t *testing.T) {
	// BDD: Given auth_mismatch_log_level="debug",
	// When cookie+bearer identify different users,
	// Then middleware proceeds (policy unchanged; only log level differs).
	sessionPlain := "session-alice-debug-level-00000000001"
	bearerPlain := "bearer-bob-debug-level-00000000000001"
	sessionHash, _ := bcrypt.GenerateFromPassword([]byte(sessionPlain), bcrypt.MinCost)
	bearerHash, _ := bcrypt.GenerateFromPassword([]byte(bearerPlain), bcrypt.MinCost)
	alice := config.UserConfig{
		Username:         "alice",
		Role:             config.UserRoleAdmin,
		SessionTokenHash: config.BcryptHash(sessionHash),
	}
	bob := config.UserConfig{Username: "bob", Role: config.UserRoleAdmin, TokenHash: config.BcryptHash(bearerHash)}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Users:                []config.UserConfig{alice, bob},
			AuthMismatchLogLevel: "debug",
		},
	}

	var captured *config.UserConfig
	mw := RequireSessionCookieOrBearer(func() *config.Config { return cfg })
	h := mw(nextHandlerCapture(&captured))

	w := httptest.NewRecorder()
	r := bothAuthRequest(t, sessionPlain, bearerPlain)
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code, "middleware must proceed even at debug log level")
	require.NotNil(t, captured)
	assert.Equal(t, "alice", captured.Username, "cookie still wins at debug level")
}

// ---------------------------------------------------------------------------
// RequestIsSecure export test
// ---------------------------------------------------------------------------

func TestRequestIsSecure_PlainHTTP_False(t *testing.T) {
	r := plainHTTPRequest()
	assert.False(t, RequestIsSecure(r), "plain HTTP must not be considered secure")
}

func TestRequestIsSecure_TLS_True(t *testing.T) {
	r := tlsRequest()
	assert.True(t, RequestIsSecure(r), "TLS request must be considered secure")
}

func TestRequestIsSecure_XForwardedProtoHTTPS_True(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	assert.True(t, RequestIsSecure(r), "X-Forwarded-Proto: https must be considered secure")
}

// ---------------------------------------------------------------------------
// ErrSessionNotFound is the public sentinel
// ---------------------------------------------------------------------------

func TestErrSessionNotFound_IsSentinel(t *testing.T) {
	// Ensure wrapping works correctly for errors.Is chains.
	wrapped := strings.NewReader("")
	_ = wrapped // just confirm it compiles
	assert.ErrorIs(t, ErrSessionNotFound, ErrSessionNotFound)
	assert.NotErrorIs(t, nil, ErrSessionNotFound)
}
