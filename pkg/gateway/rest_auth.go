//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/gateway/middleware"
)

// Sentinel errors for HandleLogin error handling.
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserNotFound       = errors.New("user not found")
	ErrAdminAlreadyExists = errors.New("admin already registered")
)

// loginRateLimiter tracks failed login attempts per IP+username to prevent brute force attacks.
//
// Rate limiting configuration:
//   - limit: maximum failed attempts before blocking (5 attempts)
//   - window: time window for counting attempts (15 minutes)
//   - block: duration of block after exceeding limit (15 minutes)
type loginRateLimiter struct {
	mu       sync.Mutex
	attempts map[string]*loginAttempt
	limit    int
	window   time.Duration
	block    time.Duration
}

type loginAttempt struct {
	count   int
	firstAt time.Time
	blocked time.Time
}

func newLoginRateLimiter() *loginRateLimiter {
	return &loginRateLimiter{
		attempts: make(map[string]*loginAttempt),
		limit:    5,
		window:   15 * time.Minute,
		block:    15 * time.Minute,
	}
}

// check records a login attempt and returns true if allowed, false if rate limited.
func (l *loginRateLimiter) check(ip, username string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := ip + ":" + username
	now := time.Now()
	a, ok := l.attempts[key]
	if !ok {
		l.attempts[key] = &loginAttempt{count: 1, firstAt: now}
		return true
	}
	// Reset if window expired — delete the old entry to free memory, then
	// create a fresh one for this request.
	if now.Sub(a.firstAt) > l.window {
		delete(l.attempts, key)
		l.attempts[key] = &loginAttempt{count: 1, firstAt: now}
		return true
	}
	// Still blocked
	if !a.blocked.IsZero() && now.Sub(a.blocked) < l.block {
		return false
	}
	// Over limit
	if a.count >= l.limit {
		a.blocked = now
		slog.Warn("auth: rate limit hit", "ip", ip, "username", username)
		return false
	}
	a.count++
	return true
}

// recordSuccess removes the rate limit entry on successful login.
func (l *loginRateLimiter) recordSuccess(ip, username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, ip+":"+username)
}

var globalLoginLimiter = newLoginRateLimiter()

// apiRateLimiter is a general-purpose per-IP rate limiter using a sliding window.
// Unlike loginRateLimiter (which tracks IP+username and blocks after N failures),
// this limiter counts all requests per IP regardless of outcome.
type apiRateLimiter struct {
	mu      sync.Mutex
	windows map[string]*slidingWindow
	limit   int           // max requests in window
	window  time.Duration // sliding window duration
}

type slidingWindow struct {
	timestamps []time.Time
}

func newAPIRateLimiter(limit int, window time.Duration) *apiRateLimiter {
	return &apiRateLimiter{
		windows: make(map[string]*slidingWindow),
		limit:   limit,
		window:  window,
	}
}

// allow checks whether the given IP is within rate limits. Returns true if
// the request is allowed, false if it should be rejected.
func (l *apiRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	sw, ok := l.windows[ip]
	if !ok {
		sw = &slidingWindow{}
		l.windows[ip] = sw
	}

	// Evict timestamps outside the window.
	cutoff := now.Add(-l.window)
	start := 0
	for start < len(sw.timestamps) && sw.timestamps[start].Before(cutoff) {
		start++
	}
	sw.timestamps = sw.timestamps[start:]

	if len(sw.timestamps) >= l.limit {
		return false
	}
	sw.timestamps = append(sw.timestamps, now)
	return true
}

// retryAfter returns the number of seconds until the oldest entry in the window
// expires, giving the caller a Retry-After value.
func (l *apiRateLimiter) retryAfter(ip string) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	sw, ok := l.windows[ip]
	if !ok || len(sw.timestamps) == 0 {
		return 0
	}
	oldest := sw.timestamps[0]
	expires := oldest.Add(l.window)
	wait := time.Until(expires)
	if wait <= 0 {
		return 0
	}
	secs := int(wait.Seconds()) + 1 // round up
	return secs
}

// Global rate limiters for auth-sensitive endpoints.
var (
	// /api/v1/auth/validate — 30 requests/minute per IP.
	validateLimiter = newAPIRateLimiter(30, 1*time.Minute)
	// /api/v1/onboarding/complete — 3 requests/minute per IP (highly sensitive).
	onboardingCompleteLimiter = newAPIRateLimiter(3, 1*time.Minute)
	// /api/v1/config — 30 requests/minute per IP.
	configLimiter = newAPIRateLimiter(30, 1*time.Minute)
	// /api/v1/auth/register-admin — 3 requests/minute per IP (highly sensitive).
	registerAdminLimiter = newAPIRateLimiter(3, 1*time.Minute)
)

// clientIP extracts the client IP from the request, checking X-Forwarded-For first.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if idx := strings.Index(fwd, ","); idx != -1 {
			fwd = strings.TrimSpace(fwd[:idx])
		}
		return fwd
	}
	return r.RemoteAddr
}

// withRateLimit wraps a handler with per-IP rate limiting. On limit exceeded,
// returns 429 with a Retry-After header and JSON error body.
func withRateLimit(limiter *apiRateLimiter, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !limiter.allow(ip) {
			retryAfter := limiter.retryAfter(ip)
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
			slog.Warn("api: rate limit exceeded", "ip", ip, "path", r.URL.Path, "retry_after", retryAfter)
			jsonErr(
				w,
				http.StatusTooManyRequests,
				fmt.Sprintf("rate limit exceeded, retry after %d seconds", retryAfter),
			)
			return
		}
		handler(w, r)
	}
}

// withOptionalAuth is like withAuth but allows unauthenticated requests to pass through.
// Authenticated requests get role injected into context; anonymous requests get a context
// without any role so downstream handlers can distinguish.
//
// B1.1 backend half: every route wrapped here gets a 1 MiB body cap so an
// anonymous client cannot pin the gateway with an unbounded POST body. All
// routes registered with withOptionalAuth are JSON or GET-only (state,
// providers, media-serve, uploads-serve, onboarding, login, register-admin)
// — none legitimately exceed 1 MiB. Routes that need a larger body (binary
// uploads) use withUploadAuth instead.
func (a *restAPI) withOptionalAuth(handler http.HandlerFunc) http.HandlerFunc {
	const optionalAuthBodyLimit int64 = 1 << 20 // 1 MiB
	return func(w http.ResponseWriter, r *http.Request) {
		if a.handlePreflight(w, r) {
			return
		}
		// Prefer config snapshot from configSnapshotMiddleware (race-free during
		// hot-reload). Fall back to GetConfig() if middleware was not applied.
		cfg := configFromContext(r.Context())
		if cfg == nil {
			slog.Warn("configFromContext returned nil — configSnapshotMiddleware may not be applied")
			cfg = a.agentLoop.GetConfig()
		}
		r.Body = http.MaxBytesReader(w, r.Body, optionalAuthBodyLimit)
		authHeader := r.Header.Get("Authorization")
		prefix := "Bearer "
		if strings.HasPrefix(authHeader, prefix) {
			rawToken := strings.TrimPrefix(authHeader, prefix)
			// Check per-user list (token hash via bcrypt)
			if len(cfg.Gateway.Users) > 0 {
				for _, user := range cfg.Gateway.Users {
					if user.TokenHash.Verify(rawToken) == nil {
						ctx := context.WithValue(r.Context(), RoleContextKey{}, user.Role)
						ctx = context.WithValue(ctx, UserContextKey{}, &user)
						a.setCORSHeaders(w, r)
						handler(w, r.WithContext(ctx))
						return
					}
				}
				// Token present but not found in user list — treat as anonymous (optional auth)
			}
			// Legacy env var fallback
			required := os.Getenv("OMNIPUS_BEARER_TOKEN")
			if required != "" && subtle.ConstantTimeCompare([]byte(rawToken), []byte(required)) == 1 {
				ctx := context.WithValue(r.Context(), RoleContextKey{}, config.UserRoleAdmin)
				a.setCORSHeaders(w, r)
				handler(w, r.WithContext(ctx))
				return
			}
		}
		// No auth or invalid token in dev mode — pass through unauthenticated.
		a.setCORSHeaders(w, r)
		handler(w, r)
	}
}

// HandleLogin handles POST /api/v1/auth/login.
func (a *restAPI) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ip := clientIP(r)

	var body gen.LoginRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "LoginRequest", &body, validateEnabled) {
		return
	}
	if body.Username == "" || body.Password == "" {
		jsonErr(w, http.StatusBadRequest, "username and password are required")
		return
	}

	// Check rate limit before processing login.
	if !globalLoginLimiter.check(ip, body.Username) {
		jsonErr(w, http.StatusTooManyRequests, "too many login attempts, try again later")
		return
	}

	// Generate bearer token and session token before the atomic update so we
	// can return the bearer token in the response and issue the session cookie
	// after the disk write succeeds.
	token, err := generateUserToken(body.Username)
	if err != nil {
		slog.Error("auth: generate token failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "login failed")
		return
	}
	// Mint session token + bcrypt hash outside the config lock — bcrypt is
	// intentionally slow and must not hold configMu for its duration.
	sessionToken, sessionHash, err := middleware.MintSessionToken()
	if err != nil {
		slog.Error("auth: mint session token failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "login failed")
		return
	}

	var foundRole string
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		gw, ok := m["gateway"].(map[string]any)
		if !ok {
			return fmt.Errorf("gateway config is not a map")
		}
		usersRaw, ok := gw["users"].([]any)
		if !ok {
			return fmt.Errorf("gateway.users is not an array")
		}
		for _, u := range usersRaw {
			userMap, ok := u.(map[string]any)
			if !ok {
				continue
			}
			usernameStr, ok := userMap["username"].(string)
			if !ok {
				continue
			}
			if usernameStr != body.Username {
				continue
			}
			passwordHash, ok := userMap["password_hash"].(string)
			if !ok {
				return fmt.Errorf("user password_hash is not a string")
			}
			if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(body.Password)) != nil {
				return ErrInvalidCredentials
			}
			// Update both token hashes atomically via safeUpdateConfigJSON.
			tokenHash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
			if err != nil {
				return fmt.Errorf("token hash failed: %w", err)
			}
			userMap["token_hash"] = string(tokenHash)
			userMap["session_token_hash"] = string(sessionHash)
			foundRole, _ = userMap["role"].(string)
			return nil
		}
		return ErrUserNotFound
	}); err != nil {
		if errors.Is(err, ErrInvalidCredentials) || errors.Is(err, ErrUserNotFound) {
			jsonErr(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		slog.Error("auth: login failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "login failed")
		return
	}

	// Validate role is present.
	if foundRole == "" {
		slog.Error("auth: login succeeded but user role is missing", "username", body.Username)
		jsonErr(w, http.StatusInternalServerError, "login failed: user role corrupted")
		return
	}

	// Reload in-memory config so withAuth middleware picks up the new token hash.
	// Reload failure is non-fatal for login — the token is on disk and will be
	// picked up on the next config poll.
	if err := a.triggerReloadAndWait(); err != nil {
		slog.Warn("auth: hot-reload after login failed; token active after next restart", "error", err)
	}

	// Reset rate limit counter on successful login.
	globalLoginLimiter.recordSuccess(ip, body.Username)

	// Issue the omnipus-session HttpOnly cookie (session-cookie auth path).
	middleware.WriteSessionCookie(w, r, sessionToken)

	// Issue a fresh __Host-csrf cookie so the SPA can echo it on subsequent
	// state-changing requests (issue #97). Login is the canonical moment to
	// rotate CSRF tokens — it coincides with a new bearer token.
	// Cookie mint failure means the OS RNG is broken — refuse to return the
	// bearer token to avoid issuing a session that cannot make CSRF-protected
	// requests.
	if err := middleware.IssueCSRFCookie(w, r); err != nil {
		slog.Error("auth: issue CSRF cookie failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "session init failed")
		return
	}

	jsonOK(w, gen.LoginResponse{
		Token:    token,
		Role:     gen.LoginResponseRole(foundRole),
		Username: body.Username,
	})
}

// HandleValidateToken handles GET /api/v1/auth/validate.
// Returns user info if token is valid, 401 otherwise.
func (a *restAPI) HandleValidateToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// Get user from context (set by withAuth middleware)
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "invalid token")
		return
	}
	jsonOK(w, gen.ValidateTokenResponse{
		Username: user.Username,
		Role:     gen.ValidateTokenResponseRole(user.Role),
	})
}

// HandleRegisterAdmin handles POST /api/v1/auth/register-admin.
// Creates the first admin user — fails with 409 if an admin already exists.
//
// The entire check-create-token sequence runs inside safeUpdateConfigJSON so
// concurrent requests cannot both pass the "no admin yet" check (TOCTOU fix).
func (a *restAPI) HandleRegisterAdmin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body gen.RegisterAdminRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "RegisterAdminRequest", &body, validateEnabled) {
		return
	}
	if body.Username == "" || body.Password == "" {
		jsonErr(w, http.StatusBadRequest, "username and password are required")
		return
	}
	if len(body.Password) < 8 {
		jsonErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	// Generate all bcrypt hashes outside the config lock — bcrypt is
	// intentionally slow and must not hold configMu for its duration.
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("auth: hash password failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not register admin")
		return
	}
	token, err := generateUserToken(body.Username)
	if err != nil {
		slog.Error("auth: generate token failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not register admin")
		return
	}
	tokenHash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("auth: hash token failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not register admin")
		return
	}
	// Mint a session token so the newly-registered admin is immediately logged
	// in via the session-cookie auth path (no second round-trip required).
	sessionToken, sessionHash, err := middleware.MintSessionToken()
	if err != nil {
		slog.Error("auth: mint session token failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not register admin")
		return
	}

	// Atomically: check for existing admin, append new user entry.
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		gw, ok := m["gateway"].(map[string]any)
		if !ok {
			// gateway key absent — initialize it so we can add the user.
			gw = map[string]any{}
			m["gateway"] = gw
		}

		// Normalise users array: may be nil/absent on a fresh config.
		users := make([]any, 0, 1)
		if raw, exists := gw["users"]; exists && raw != nil {
			users, ok = raw.([]any)
			if !ok {
				return fmt.Errorf("gateway.users is not an array")
			}
		}

		// Check for any existing admin — this is now race-free because we hold configMu.
		for _, u := range users {
			um, ok := u.(map[string]any)
			if !ok {
				continue
			}
			if role, _ := um["role"].(string); role == string(config.UserRoleAdmin) {
				return ErrAdminAlreadyExists
			}
		}

		// Append the new admin entry (all hashes already computed above).
		newUser := map[string]any{
			"username":           body.Username,
			"password_hash":      string(passwordHash),
			"token_hash":         string(tokenHash),
			"session_token_hash": string(sessionHash),
			"role":               string(config.UserRoleAdmin),
		}
		gw["users"] = append(users, newUser)
		return nil
	}); err != nil {
		if errors.Is(err, ErrAdminAlreadyExists) {
			jsonErr(w, http.StatusConflict, "admin already registered")
			return
		}
		slog.Error("auth: register admin failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not register admin")
		return
	}

	// Reload in-memory config so withAuth middleware picks up the new token hash immediately.
	// Reload failure is non-fatal — token is on disk and active after next config poll.
	if err := a.triggerReloadAndWait(); err != nil {
		slog.Warn("auth: hot-reload after register-admin failed; token active after next restart", "error", err)
	}

	// Issue the omnipus-session HttpOnly cookie (session-cookie auth path).
	middleware.WriteSessionCookie(w, r, sessionToken)

	// Issue a __Host-csrf cookie so the newly-registered admin can
	// immediately make state-changing requests without being blocked by the
	// CSRF middleware (issue #97). Cookie mint failure means the OS RNG is
	// broken — refuse to return the bearer token to prevent an unusable session.
	if err := middleware.IssueCSRFCookie(w, r); err != nil {
		slog.Error("auth: issue CSRF cookie failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "session init failed")
		return
	}

	slog.Info("auth: admin user registered", "username", body.Username)
	jsonOK(w, gen.LoginResponse{
		Token:    token,
		Role:     gen.LoginResponseRole(config.UserRoleAdmin),
		Username: body.Username,
	})
}

// HandleLogout handles POST /api/v1/auth/logout.
// Invalidates the authenticated user's token by clearing token_hash and
// session_token_hash in config.json, then revokes both browser-side cookies.
func (a *restAPI) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		gw, ok := m["gateway"].(map[string]any)
		if !ok {
			return fmt.Errorf("gateway config not found")
		}
		users, ok := gw["users"].([]any)
		if !ok {
			return fmt.Errorf("users not found")
		}
		for _, u := range users {
			um, ok := u.(map[string]any)
			if !ok {
				continue
			}
			if um["username"] == user.Username {
				um["token_hash"] = ""
				um["session_token_hash"] = ""
				return nil
			}
		}
		return fmt.Errorf("user not found in config")
	}); err != nil {
		slog.Error("auth: logout failed", "error", err, "username", user.Username)
		jsonErr(w, http.StatusInternalServerError, "logout failed")
		return
	}
	if err := a.triggerReloadAndWait(); err != nil {
		slog.Warn("auth: hot-reload after logout failed", "error", err)
	}
	// Revoke both browser-side cookies (session + CSRF). Defense-in-depth:
	// the server-side hashes were already cleared above.
	middleware.ClearSessionCookie(w, r)
	middleware.ClearCSRFCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// HandleChangePassword handles POST /api/v1/auth/change-password.
// Validates the current password then replaces the password hash in config.json.
func (a *restAPI) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var body gen.ChangePasswordRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "ChangePasswordRequest", &body, validateEnabled) {
		return
	}
	if body.CurrentPassword == "" || body.NewPassword == "" {
		jsonErr(w, http.StatusBadRequest, "current_password and new_password are required")
		return
	}
	if len(body.NewPassword) < 8 {
		jsonErr(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}
	// Pre-compute the new hash outside the lock to avoid holding configMu
	// for ~100ms during a bcrypt operation.
	newHash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("auth: bcrypt hash failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "password change failed")
		return
	}
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		gw, ok := m["gateway"].(map[string]any)
		if !ok {
			return fmt.Errorf("gateway config not found")
		}
		users, ok := gw["users"].([]any)
		if !ok {
			return fmt.Errorf("users not found")
		}
		for _, u := range users {
			um, ok := u.(map[string]any)
			if !ok {
				continue
			}
			if um["username"] != user.Username {
				continue
			}
			passwordHash, ok := um["password_hash"].(string)
			if !ok {
				return fmt.Errorf("password_hash is not a string")
			}
			if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(body.CurrentPassword)) != nil {
				return ErrInvalidCredentials
			}
			um["password_hash"] = string(newHash)
			// Invalidate all existing sessions so the user must re-authenticate
			// with the new password. Clearing both hashes mimics HandleLogout.
			um["token_hash"] = ""
			um["session_token_hash"] = ""
			return nil
		}
		return ErrUserNotFound
	}); err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			jsonErr(w, http.StatusUnauthorized, "current password is incorrect")
			return
		}
		if errors.Is(err, ErrUserNotFound) {
			jsonErr(w, http.StatusNotFound, "user not found")
			return
		}
		slog.Error("auth: change-password failed", "error", err, "username", user.Username)
		jsonErr(w, http.StatusInternalServerError, "password change failed")
		return
	}
	if err := a.triggerReloadAndWait(); err != nil {
		slog.Warn("auth: hot-reload after change-password failed", "error", err)
	}
	// Revoke the caller's browser-side cookies. Old bearer tokens and session
	// cookies are now invalid (hashes cleared above). The SPA must redirect to
	// login. Cookie-clearing headers must be written before the JSON body.
	middleware.ClearSessionCookie(w, r)
	middleware.ClearCSRFCookie(w, r)
	jsonOK(w, gen.OperationResult{Success: true})
}

// generateUserToken creates a random bearer token for authentication.
func generateUserToken(_ string) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("rand read failed: %w", err)
	}
	return "omnipus_" + hex.EncodeToString(bytes), nil
}

// triggerReloadAndWait triggers a config reload and polls until the in-memory
// config has been updated (up to a 5-second deadline). Returns an error when
// the reload fails to start; reload-completion timeout is treated as best-effort
// (we return nil so callers are not blocked indefinitely).
//
// The special case "reload not configured" (reloadFunc == nil) is treated as a
// no-op rather than an error: this condition is normal in unit tests where the
// full gateway reload pipeline is not wired. Production always configures the
// reload function during startup.
func (a *restAPI) triggerReloadAndWait() error {
	if err := a.agentLoop.TriggerReload(); err != nil {
		if errors.Is(err, agent.ErrReloadNotConfigured) {
			// Unit-test environment — no reload pipeline wired; treat as no-op.
			return nil
		}
		if errors.Is(err, agent.ErrReloadAlreadyInProgress) {
			// Another reload is in flight; poll until it completes rather than
			// returning an error — the result will include our config change.
			// Fall through to the polling loop below.
		} else {
			slog.Error("config reload failed", "error", err)
			return err
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !a.agentLoop.IsReloadPending() {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Reload may still be running; return nil so callers are not blocked indefinitely.
	return nil
}
