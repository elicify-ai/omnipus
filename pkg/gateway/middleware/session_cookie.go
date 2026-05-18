//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package middleware

// Session-cookie infrastructure for the SPA's auth surface (path-sandbox-and-
// capability-tiers spec, /..).
//
// Threat model and contract:
//
// 1. The cookie carries the PLAINTEXT session token. The server never sees
// the cookie value at rest — only the request-scoped value travels through
// ResolveUserFromCookie. The disk-persisted artifact is a bcrypt hash on
// UserConfig.SessionTokenHash. This mirrors the existing bearer-token
// pattern (`UserConfig.TokenHash`) and lets `bcrypt.CompareHashAndPassword`
// do the constant-time comparison ( corrected in ).
//
// 2. Cookie attributes:
// - Name: omnipus-session (un-prefixed; we cannot use __Host- because the
// __Host- prefix forces Secure=true which the browser drops on plain
// HTTP, breaking dev). Same trade-off as the CSRF fallback in csrf.go.
// - Secure: requestIsSecure(r) — set when the request reached us via TLS
// (r.TLS != nil) or X-Forwarded-Proto: https. — same posture as
// IssueCSRFCookie so plain-HTTP dev still works.
// - HttpOnly: true. Differs from __Host-csrf (which is HttpOnly: false so
// the SPA can read it for the double-submit echo). The session cookie
// must NOT be readable from JavaScript — that's its whole point.
// - SameSite: Strict. Blocks cross-origin sends. Note: SameSite=Strict
// does NOT protect against same-site (subdomain) attacks; documented
// as the deployment assumption in spec §"Subdomain isolation".
// - Path: /. The session is gateway-wide.
// - Max-Age: 86400 (24 h, ). Browsers retain across tab close and
// restart within that window.
//
// 3. Auth resolution: RequireSessionCookieOrBearer accepts EITHER
// a valid bearer token OR a valid session cookie. When BOTH are present
// and identify different users, the cookie wins (it's the primary path);
// the mismatch is logged at a configurable level. The default is "warn"
// so collisions are visible to operators; tests assert on the message.
//
// 4. Logout: the caller (HandleLogout) is responsible for
// atomically clearing TokenHash AND SessionTokenHash; this middleware
// supplies ClearSessionCookie + ClearCSRFCookie so the response also
// revokes both browser-side cookies.
//
// 5. The middleware NEVER allows a request through when neither auth source
// is present. Fail-closed semantics, same as withAuth.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/gateway/ctxkey"
)

// SessionCookieName is the name of the HttpOnly auth cookie issued at login.
//
// Un-prefixed (no __Host-) so it survives plain-HTTP deployments — same trade-
// off the CSRF fallback makes (CSRFCookieNameHTTP). The cookie's protections
// come from SameSite=Strict + HttpOnly + Path=/, not the cookie-prefix layer.
const SessionCookieName = "omnipus-session"

// SessionCookieMaxAge is the cookie lifetime in seconds (24 h per ).
// Browsers persist the cookie across tab/window close within this window.
const SessionCookieMaxAge = 86400

// sessionTokenBytes is the entropy of a fresh session token (256 bits) before
// base64 encoding. Matches generateUserToken's payload size in rest_auth.go.
const sessionTokenBytes = 32

// ErrSessionNotFound is returned by ResolveUserFromCookie when the cookie is
// missing or its plaintext value does not bcrypt-match any user's stored
// SessionTokenHash. Callers MUST treat this as a 401 condition.
//
// This is package-local — distinct from pkg/tools.ErrSessionNotFound (which
// covers shell sessions, an unrelated concept). No import collision because
// the two packages never import each other.
var ErrSessionNotFound = errors.New("session not found")

// RequestIsSecure reports whether the request reached the gateway over TLS,
// either directly (r.TLS != nil) or via an ingress that forwards
// X-Forwarded-Proto: https.
//
// Exported so HandleLogin / HandleRegisterAdmin in pkg/gateway can match the
// CSRF cookie's Secure-flag posture exactly. Internally the same check
// drives IssueCSRFCookie's pick of __Host-csrf vs csrf.
func RequestIsSecure(r *http.Request) bool {
	return requestIsSecure(r)
}

// generateSessionToken creates a fresh 32-byte cryptographically-random
// session token, base64-RawURL encoded. Same payload size as the bearer-token
// generator in pkg/gateway/rest_auth.go:655 (generateUserToken). Encoded
// length is 43 ASCII characters.
//
// The token is the PLAINTEXT cookie value the server sends to the client.
// The server stores bcrypt(token) at rest; the client carries the plaintext.
// This is the same pattern as the bearer token (rest_auth.go:243).
func generateSessionToken() (string, error) {
	buf := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("session: rand.Read: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// MintSessionToken creates a fresh 32-byte cryptographically-random session
// token and its bcrypt hash. Returns (plaintext, hash, error).
//
// The returned plaintext is what goes into the cookie value; the hash is what
// the caller persists to disk (UserConfig.SessionTokenHash). Separating minting
// from I/O lets callers batch the hash write with other disk operations in a
// single safeUpdateConfigJSON call.
func MintSessionToken() (string, []byte, error) {
	token, err := generateSessionToken()
	if err != nil {
		return "", nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		return "", nil, fmt.Errorf("session: bcrypt: %w", err)
	}
	return token, hash, nil
}

// WriteSessionCookie writes the omnipus-session cookie to w carrying the
// given plaintext token. This is header-only (no disk I/O) and cannot fail.
// Callers MUST persist the corresponding bcrypt hash to disk BEFORE calling
// this function so the cookie is valid on the next request.
func WriteSessionCookie(w http.ResponseWriter, r *http.Request, plaintext string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    plaintext,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   SessionCookieMaxAge,
	})
}

// IssueSessionCookie mints a fresh session token, persists bcrypt(token) to
// the named user's SessionTokenHash via configMutator (which is expected to
// hold the same mutex as bearer-token issuance — typically
// safeUpdateConfigJSON), and writes the omnipus-session cookie to w.
//
// On any error (RNG failure, bcrypt failure, config-write failure, user
// lookup failure) IssueSessionCookie returns the error and DOES NOT mutate w.
// Callers MUST surface a 500 in that case.
//
// Returns the plaintext token so callers may log it at debug or pass it to
// tests; production callers do not need it after the cookie has been written.
//
// The configMutator follows safeUpdateConfigJSON's signature:
//
//	configMutator(func(m map[string]any) error)
//
// The closure must locate gateway.users[<username>] and set
// "session_token_hash" to the bcrypt(token) string. If the user is missing
// from the config the closure must return an error and IssueSessionCookie
// surfaces it untouched — no cookie is written.
func IssueSessionCookie(
	w http.ResponseWriter,
	r *http.Request,
	username string,
	configMutator func(func(map[string]any) error) error,
) (string, error) {
	if username == "" {
		return "", fmt.Errorf("session: username is required")
	}
	if configMutator == nil {
		return "", fmt.Errorf("session: configMutator is required")
	}

	token, err := generateSessionToken()
	if err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("session: bcrypt: %w", err)
	}

	// Persist hash via the caller-supplied mutator. The mutator owns the
	// configMu read-modify-write contract; we just supply the closure.
	mutateErr := configMutator(func(m map[string]any) error {
		gw, ok := m["gateway"].(map[string]any)
		if !ok {
			return fmt.Errorf("session: gateway config not found")
		}
		usersRaw, ok := gw["users"].([]any)
		if !ok {
			return fmt.Errorf("session: gateway.users is not an array")
		}
		for _, u := range usersRaw {
			um, ok := u.(map[string]any)
			if !ok {
				continue
			}
			if uname, _ := um["username"].(string); uname == username {
				um["session_token_hash"] = string(hash)
				return nil
			}
		}
		return fmt.Errorf("session: user %q not found", username)
	})
	if mutateErr != nil {
		// configMutator failed — DO NOT write the cookie. Caller will 500.
		return "", mutateErr
	}

	// Persistence succeeded. Issue the cookie.
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   SessionCookieMaxAge,
	})
	return token, nil
}

// ResolveUserFromCookie reads the omnipus-session cookie and bcrypt-compares
// its plaintext value against each user's SessionTokenHash, returning the
// matching user or ErrSessionNotFound.
//
// Implementation note: the loop is O(N) in the user count and bcrypt is
// intentionally slow (~50–100 ms at DefaultCost). For deployments with many
// users this is acceptable because it runs only on cookie-bearing requests —
// the typical SPA session generates one cookie lookup per request, and the
// gateway's user count is bounded (admin + a handful of operators in the
// open-source target). If user count grows, an indexed lookup can replace
// the linear scan without changing the public contract.
//
// Each iteration calls bcrypt.CompareHashAndPassword which is itself a
// constant-time compare; the outer loop is NOT constant-time across users
// (an early match returns earlier). This matches the existing bearer-token
// pattern at rest_auth.go:243; the threat model treats username enumeration
// via timing as out of scope (bcrypt's per-attempt cost dominates).
func ResolveUserFromCookie(r *http.Request, users []config.UserConfig) (*config.UserConfig, error) {
	if r == nil {
		return nil, ErrSessionNotFound
	}
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie == nil || cookie.Value == "" {
		return nil, ErrSessionNotFound
	}
	for i := range users {
		user := users[i]
		if user.SessionTokenHash.IsZero() {
			continue
		}
		if user.SessionTokenHash.Verify(cookie.Value) == nil {
			return &user, nil
		}
	}
	return nil, ErrSessionNotFound
}

// ClearSessionCookie writes a Set-Cookie header that revokes the session
// cookie (Max-Age=0). Defense-in-depth: the server-side hash is also cleared
// by HandleLogout so even a stale cookie value will fail to authenticate.
//
// Mirrors the Secure-flag posture of IssueSessionCookie — the browser
// matches Secure=true cookies only against TLS origins, so the deletion
// must use the same flag the issuance did.
func ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1, // -1 → Max-Age=0 in the wire encoding (immediate expiry)
	})
}

// ClearCSRFCookie writes a Set-Cookie header that revokes the CSRF cookie
// (per — defense-in-depth on logout). Picks the cookie name
// based on requestIsSecure so it matches whichever flavor IssueCSRFCookie
// originally wrote (__Host-csrf on TLS, csrf on plain HTTP).
//
// HttpOnly stays false (matching IssueCSRFCookie) so the browser treats the
// deletion as the same cookie it stored. The browser's deletion semantics
// require an exact attribute match on Name + Path + Domain.
func ClearCSRFCookie(w http.ResponseWriter, r *http.Request) {
	if requestIsSecure(r) {
		http.SetCookie(w, &http.Cookie{
			Name:     CSRFCookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: false,
			Secure:   true,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieNameHTTP,
		Value:    "",
		Path:     "/",
		HttpOnly: false,
		Secure:   false,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// resolveAuthMismatchLogger picks the slog log function matching the
// configured AuthMismatchLogLevel. Default "warn"; allowed "debug", "info",
// "warn". Unknown values fall back to warn (fail-loud — operators see the
// collision rather than missing it).
func resolveAuthMismatchLogger(level string) func(msg string, args ...any) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.Debug
	case "info":
		return slog.Info
	case "warn", "":
		return slog.Warn
	default:
		// Unrecognized — log at warn (the default). Boot-time validator
		// in pkg/config also rejects unknown values, so this branch is
		// defense-in-depth for races or legacy configs.
		return slog.Warn
	}
}

// hasOmnipusSessionCookie reports whether the request carries the
// omnipus-session cookie (regardless of its value's validity). Used by
// RequireSessionCookieOrBearer to distinguish "no cookie sent at all"
// from "cookie sent but didn't match any user" ( — replay attack
// detection signal). r.Cookie returns http.ErrNoCookie when the named
// cookie is absent; any other return path means a cookie WAS present.
func hasOmnipusSessionCookie(r *http.Request) bool {
	if r == nil {
		return false
	}
	_, err := r.Cookie(SessionCookieName)
	return err == nil
}

// resolveBearerUser returns the user matching an Authorization: Bearer
// token, or nil if there is no bearer header or the token doesn't match a
// known user. This is intentionally a parallel implementation of
// pkg/gateway.checkBearerAuth's bearer branch — we cannot import
// pkg/gateway from middleware (cyclic), and the caller-supplied config is
// already in scope.
//
// Only checks the per-user list. Legacy OMNIPUS_BEARER_TOKEN env var is
// out of scope for the cookie-or-bearer middleware: that fallback is for
// API/CLI clients that have NEVER used the SPA, and the env-var path does
// not have a UserConfig to attach to the request context.
func resolveBearerUser(r *http.Request, users []config.UserConfig) *config.UserConfig {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return nil
	}
	rawToken := strings.TrimPrefix(auth, prefix)
	if rawToken == "" {
		return nil
	}
	for i := range users {
		user := users[i]
		if user.TokenHash.IsZero() {
			continue
		}
		if user.TokenHash.Verify(rawToken) == nil {
			return &user
		}
	}
	return nil
}

// RequireSessionCookieOrBearer returns a middleware that admits a request
// authenticated by EITHER a valid omnipus-session cookie OR a valid bearer
// token. On success the matching *config.UserConfig is stored on
// the context under ctxkey.UserContextKey{} (matching withAuth's convention).
//
// Resolution rules:
//
// 1. Resolve cookie → cookieUser (may be nil).
// 2. Resolve bearer → bearerUser (may be nil).
// 3. If both are nil → 401.
// 4. If exactly one is non-nil → that user is the principal.
// 5. If both are non-nil and identify the SAME username → that user is the
// principal (no log entry; common case).
// 6. If both are non-nil and identify DIFFERENT users → COOKIE WINS, log at
// cfg.Gateway.AuthMismatchLogLevel ("warn" by default) with the message:
// "auth: cookie+bearer identify different users; cookie wins;
// cookie_user=<a> bearer_user=<b>"
//
// The getCfg argument is a closure rather than a captured pointer because
// the gateway's config pointer is swapped on hot-reload; the closure
// returns the current pointer at request time. Tests pass a function that
// returns a stable test config.
//
// Failure mode: if getCfg returns nil (production never permits this; tests
// might) the middleware returns 500 — fail-closed. We don't fall back to a
// "no users configured" allow path because that would defeat the purpose of
// the middleware.
func RequireSessionCookieOrBearer(getCfg func() *config.Config) func(http.Handler) http.Handler {
	if getCfg == nil {
		// Programmer error — building the middleware without a config
		// accessor is unrecoverable. Panic at construction so it surfaces
		// during boot or test wiring rather than as a 500 in production.
		panic("middleware.RequireSessionCookieOrBearer: getCfg is required")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cfg := getCfg()
			if cfg == nil {
				// No config available — fail-closed.
				writeJSONError(w, http.StatusInternalServerError, "auth: config unavailable")
				return
			}
			users := cfg.Gateway.Users

			cookieUser, cookieErr := ResolveUserFromCookie(r, users)
			// (silent-failure-hunter): distinguish "no cookie / not
			// found" (ErrSessionNotFound — expected, falls through to
			// bearer auth) from unexpected errors (bcrypt internal failure,
			// etc.) AND from "cookie present but didn't match any user"
			// (replay attack / stale cookie campaign). Without this branch,
			// a corrupted cookie or a probe campaign is indistinguishable
			// from "no cookie" — invisible to operators.
			if cookieErr != nil && !errors.Is(cookieErr, ErrSessionNotFound) {
				logFn := resolveAuthMismatchLogger(cfg.Gateway.AuthMismatchLogLevel)
				logFn("auth: cookie resolution unexpected error",
					"error", cookieErr, "remote_addr", r.RemoteAddr)
			} else if errors.Is(cookieErr, ErrSessionNotFound) && hasOmnipusSessionCookie(r) {
				// Cookie was sent but didn't bcrypt-match any stored hash.
				// This is the signal of a replay attempt or a stale cookie
				// after a server-side hash rotation. Log so operators can
				// correlate spikes with attacks or password resets.
				logFn := resolveAuthMismatchLogger(cfg.Gateway.AuthMismatchLogLevel)
				logFn("auth: cookie present but invalid; falling back to bearer",
					"remote_addr", r.RemoteAddr)
			}
			bearerUser := resolveBearerUser(r, users)

			var principal *config.UserConfig
			switch {
			case cookieUser == nil && bearerUser == nil:
				writeJSONError(w, http.StatusUnauthorized, "unauthorized")
				return
			case cookieUser != nil && bearerUser == nil:
				principal = cookieUser
			case cookieUser == nil && bearerUser != nil:
				principal = bearerUser
			case cookieUser != nil && bearerUser != nil:
				if cookieUser.Username != bearerUser.Username {
					// Collision — cookie wins, log at configured level.
					logFn := resolveAuthMismatchLogger(cfg.Gateway.AuthMismatchLogLevel)
					logFn(
						"auth: cookie+bearer identify different users; cookie wins; cookie_user="+
							cookieUser.Username+" bearer_user="+bearerUser.Username,
						"cookie_user", cookieUser.Username,
						"bearer_user", bearerUser.Username,
					)
				}
				principal = cookieUser
			}

			ctx := context.WithValue(r.Context(), ctxkey.UserContextKey{}, principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// writeJSONError writes a 4xx/5xx JSON error response in the gateway's
// canonical { "error": "..." } shape. Local mirror of jsonErr in pkg/gateway
// (cannot import that package here without a cycle).
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		slog.Debug("writeJSONError: encode failed", "error", err)
	}
}
