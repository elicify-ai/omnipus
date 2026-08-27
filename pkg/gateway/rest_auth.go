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

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// Sentinel errors for HandleLogin error handling.
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserNotFound       = errors.New("user not found")
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
	// /api/v1/config and /api/v1/workspaces* (incl. read GETs: list, single,
	// milestones, delegation) — 240 requests/minute per IP. Raised from 30: the
	// SPA fires a burst of workspace reads on every navigation (list + milestones
	// + delegation + tasks), and 30/min throttled legitimate rapid workspace
	// switching — surfacing as transient "Failed to load workspace" (429) in the
	// e2e calendar suite and for heavy real users. Mutations stay rate-limited;
	// this only widens the per-IP read budget.
	configLimiter = newAPIRateLimiter(240, 1*time.Minute)
	// /api/v1/auth/reauth — 10 requests/minute per IP. A password re-verification
	// (sensitive), but a legitimate user may mistype a few times; tighter than
	// login, not punitive (Spec-6 FR-12.2).
	reauthLimiter = newAPIRateLimiter(10, 1*time.Minute)
	// /api/v1/system/cli-validate — 20 requests/minute per IP. DEDICATED limiter
	// (ADR-030 §11 FR-013), distinct from validateLimiter (the /auth/validate
	// limiter). Tighter than the read-only endpoints because each call spawns a
	// caller-supplied path (<cli> --version); validate-on-blur is debounced on
	// the SPA so 20/min is ample for legitimate editing.
	cliValidateLimiter = newAPIRateLimiter(20, 1*time.Minute)
	// /api/v1/tasks/occurrences — 240 requests/minute per IP. A DEDICATED
	// limiter (Calendar Recurrence Redesign spec, "Occurrence expansion
	// endpoint" — MAJ-201/FR-008), distinct from configLimiter (that
	// bucket's 429s already broke the calendar once) and from the
	// existing task CRUD routes (/api/v1/tasks, /api/v1/tasks/{id}, …),
	// which remain plain withAuth with no limiter at all. Matches
	// configLimiter's post-incident ceiling, which calendar navigation
	// cadence is known to fit.
	taskReadLimiter = newAPIRateLimiter(240, 1*time.Minute)
	// /api/v1/providers/{id}/sign-in — 10 requests/minute per IP. ADR-068
	// FR-008: "rate-limited like the auth endpoints" — matches
	// reauthLimiter's ceiling. Starting a NEW device-code (or reading a
	// cli_login instruction) is rare in legitimate use; unlike polling it
	// has no ongoing-cadence requirement.
	signInStartLimiter = newAPIRateLimiter(10, 1*time.Minute)
	// /api/v1/providers/{id}/sign-in/poll — 60 requests/minute per IP. A
	// DEDICATED, more generous limiter (FR-044 sets no explicit rate but a
	// device-code dialog legitimately polls at its vendor interval_seconds
	// — typically ~5s — for up to the 15-minute session ceiling, i.e. up to
	// ~180 polls per session; 10/min would 429 a single honest sign-in
	// attempt). Still bounds a runaway/abusive client well below what a
	// human could trigger by hand.
	signInPollLimiter = newAPIRateLimiter(60, 1*time.Minute)
	// /api/v1/providers/{id}/sign-in/status — 60 requests/minute per IP.
	// This route looked read-only enough to leave unlimited and is not:
	// for a device_code provider it reaches the stored-OAuth token source,
	// which REFRESHES against the vendor when the stored token is within 5
	// minutes of expiry (ADR-068 FR-046), and for github-copilot it spawns
	// a bounded Copilot CLI invocation (T068-15) — so an unauthenticated
	// caller (FR-050 makes it pre-auth reachable while onboarding is
	// incomplete) could drive outbound vendor traffic or process spawns at
	// will. Shares signInPollLimiter's ceiling rather than the tighter
	// start/auth one because the sign-in dialog legitimately re-reads
	// status alongside every poll.
	signInStatusLimiter = newAPIRateLimiter(60, 1*time.Minute)
	// /api/v1/providers/openai-chatgpt/sign-in/import — 10 requests/minute
	// per IP (M2). This route was called BARE while its four FR-050 siblings
	// were all wrapped. It is the most write-heavy of the five: every call
	// re-reads the Codex CLI credential, rewrites the whole encrypted
	// credentials.json, and re-registers every OAuth value with the
	// sensitive-value replacer. Matches signInStartLimiter's ceiling — an
	// import is a one-off operator action with no polling cadence at all.
	signInImportLimiter = newAPIRateLimiter(10, 1*time.Minute)
	// DELETE /api/v1/providers/{id}/sign-in — 10 requests/minute per IP
	// (M2). Also previously bare. Each sign-out nils the process-wide
	// sensitive-data replacer cache, so the next scrub pays a full
	// reflection walk of Config under a write lock; an unbounded anonymous
	// caller could hold the gateway in permanent rebuild churn. Signing out
	// is a one-off operator action, so it shares the start/import ceiling
	// rather than the polling one.
	signInSignOutLimiter = newAPIRateLimiter(10, 1*time.Minute)
	// GET /api/v1/providers (the list branch) for UNAUTHENTICATED callers
	// only — 60 requests/minute per IP (C1). Post-onboarding the list is
	// 401 for anonymous callers and this limiter is never reached;
	// pre-onboarding it bounds an anonymous client that would otherwise be
	// able to drive the branch's per-provider upstream /models fetches with
	// no ceiling whatsoever. Authenticated callers (the Settings screen,
	// which re-lists on every provider edit) are deliberately not limited
	// here — they already passed the auth gate.
	providerListAnonLimiter = newAPIRateLimiter(60, 1*time.Minute)
)

// clientIP extracts the client IP from the request for rate-limiting and
// audit-log purposes.
//
// SEC hardening: X-Forwarded-For is honored ONLY when the live config has
// gateway.trust_xff enabled — mirrors canonicalRemoteIP's contract
// (rest_preview_audit.go), which fixed the identical trust-XFF gap for
// preview-serve audit logging (F-14). Before this fix, clientIP trusted
// X-Forwarded-For unconditionally: on any deployment without a trusted
// reverse proxy in front of it, an attacker could send a different spoofed
// X-Forwarded-For value on every request to /api/v1/auth/login (or any
// withRateLimit-wrapped endpoint) and receive a brand-new rate-limit bucket
// each time, completely defeating globalLoginLimiter's brute-force
// protection. gateway.trust_xff (config.GatewayConfig.TrustXFF) defaults to
// false, so by default this function now ignores the header entirely and
// keys on r.RemoteAddr instead, with the port stripped — also mirroring
// canonicalRemoteIP, since keying on RemoteAddr's ephemeral port would let a
// client that opens a fresh TCP connection per request evade rate limiting
// even without touching any header.
//
// trustXFF is resolved from the request's context config snapshot, set by
// configSnapshotMiddleware which wraps the entire HTTP handler chain before
// mux dispatch (see the WrapHTTPHandler call in setupAndStartServices,
// gateway.go). Every real security-deciding caller of clientIP —
// withRateLimit, HandleLogin, cliValidateCallerKey/HandleSystemCliValidate —
// runs after that point, so they always see a live snapshot in production.
//
// The one caller that runs earlier is the CSRF double-submit-cookie
// middleware's mismatch reporter (wired in gateway.go), which executes
// before configSnapshotMiddleware in the wrap order — that reporter uses
// (*restAPI).clientIPWithLiveFallback instead of this bare function, since it
// has receiver access to fall back to a live config read (mirroring
// withOptionalAuth's pattern) rather than silently defaulting to
// trustXFF=false. clientIP itself always defaults to false when no context
// snapshot is present, which is correct (fail-closed) for every one of its
// actual callers.
//
// Delegates to canonicalRemoteIP (rest_preview_audit.go) for the actual
// extraction logic so the two IP-resolution helpers in this package cannot
// drift apart.
func clientIP(r *http.Request) string {
	var trustXFF bool
	if cfg := configFromContext(r.Context()); cfg != nil {
		trustXFF = cfg.Gateway.TrustXFF
	}
	return canonicalRemoteIP(r, trustXFF)
}

// clientIPWithLiveFallback resolves the client IP exactly like clientIP, but
// for the one caller — the CSRF-mismatch audit reporter (gateway.go) — that
// runs before configSnapshotMiddleware has injected a config snapshot into
// the request context. Without this fallback, an operator who explicitly
// configured gateway.trust_xff=true (a reverse-proxied deployment) would see
// every csrf_mismatch audit entry's source_ip silently record the reverse
// proxy's own address instead of the real client — not a security hole (the
// request is already rejected by the time this runs), but a silent
// degradation of the exact forensic signal SEC-15 exists to produce, in
// precisely the topology where trust_xff matters. Falls back to a live
// config read (mirroring withOptionalAuth's a.agentLoop.GetConfig() pattern)
// so the real configured trust_xff setting is honored even pre-snapshot.
func (a *restAPI) clientIPWithLiveFallback(r *http.Request) string {
	if cfg := configFromContext(r.Context()); cfg != nil {
		return canonicalRemoteIP(r, cfg.Gateway.TrustXFF)
	}
	if a.agentLoop != nil {
		if liveCfg := a.agentLoop.GetConfig(); liveCfg != nil {
			return canonicalRemoteIP(r, liveCfg.Gateway.TrustXFF)
		}
	}
	return canonicalRemoteIP(r, false)
}

// preAuthOnboardingWindowOpen reports whether the ADR-068 FR-050 pre-auth
// window is open — that is, whether a request carrying NO authenticated
// identity may still reach a provider route that would otherwise require one.
//
// FR-050 exists for exactly one reason: onboarding step 3 needs a working
// provider/sign-in flow BEFORE any admin account exists to authenticate as.
// The window must therefore close the instant either half of that premise
// stops holding, and it must close on UNCERTAINTY too.
//
// This replaces the bare `a.onboardingMgr != nil && a.onboardingMgr.IsComplete()`
// test, which failed OPEN (M3). onboarding.NewManager keeps the zero value —
// OnboardingComplete=false, i.e. "fresh install" — on ANY load failure: an
// unreadable state.json logs one WARN and proceeds, and an unparseable one is
// renamed aside and reset. On a long-onboarded instance that silently reopened
// all five sign-in routes, DELETE (destroys the OAuth grant) and import
// (writes to the credential store) included, unauthenticated, for the whole
// process lifetime. Three independent signals now have to agree before the
// window is treated as open:
//
//  1. onboardingStateUnknown — captured in gateway.go BEFORE the manager is
//     constructed (the manager renames a corrupt file away, so the ambiguity
//     is unobservable afterwards). An unreadable or unparseable state.json is
//     "unknown", never "fresh install". A MISSING file stays a genuine fresh
//     install: that is what a first launch actually looks like.
//  2. The onboarding manager itself must not report completion.
//  3. The instance must have no authentication authority yet — no configured
//     users and no OMNIPUS_BEARER_TOKEN. This is the signal a corrupt
//     state.json cannot erase, because it lives in config.json/the
//     environment: if somebody CAN authenticate here, the "no admin account
//     exists yet" premise is false whatever state.json says.
//
// A nil onboardingMgr (test constructions only — gateway.go always sets one)
// is treated as signal 2 satisfied, leaving signals 1 and 3 to decide.
func (a *restAPI) preAuthOnboardingWindowOpen(r *http.Request) bool {
	if a.onboardingStateUnknown {
		return false
	}
	if a.onboardingMgr != nil && a.onboardingMgr.IsComplete() {
		return false
	}
	return !a.hasAuthenticationAuthority(r)
}

// hasAuthenticationAuthority reports whether this gateway has anything a
// caller could authenticate AS: a configured user, or the OMNIPUS_BEARER_TOKEN
// env credential. It mirrors the two authorities checkBearerAuth consults
// (auth.go) — deliberately not dev_mode_bypass, which is an authentication
// BYPASS rather than an authority and must never widen a pre-auth window.
func (a *restAPI) hasAuthenticationAuthority(r *http.Request) bool {
	cfg := configFromContext(r.Context())
	if cfg == nil && a.agentLoop != nil {
		cfg = a.agentLoop.GetConfig()
	}
	if cfg != nil && len(cfg.Gateway.Users) > 0 {
		return true
	}
	return strings.TrimSpace(os.Getenv("OMNIPUS_BEARER_TOKEN")) != ""
}

// requireAuthOutsideOnboarding is the shared gate for the provider routes that
// FR-050 makes reachable pre-auth. It writes 401 and returns false when the
// caller is anonymous and the pre-auth window is closed (or of unknown state).
func (a *restAPI) requireAuthOutsideOnboarding(w http.ResponseWriter, r *http.Request) bool {
	if r.Context().Value(UserContextKey{}) != nil {
		return true
	}
	if a.preAuthOnboardingWindowOpen(r) {
		return true
	}
	jsonErr(w, http.StatusUnauthorized, "authentication required")
	return false
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
// Authenticated requests get the *config.UserConfig injected into context; anonymous
// requests get a context without a user so downstream handlers can distinguish.
//
// B1.1 backend half: every route wrapped here gets a 1 MiB body cap so an
// anonymous client cannot pin the gateway with an unbounded POST body. All
// routes registered with withOptionalAuth are JSON or GET-only (state,
// providers, media-serve, uploads-serve, onboarding, login) — none legitimately
// exceed 1 MiB. Routes that need a larger body (binary uploads) use
// withUploadAuth instead.
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
			// Resolve against configured identities via the shared resolver
			// (resolveBearerIdentity, auth.go) — the same lookup checkBearerAuth
			// and authenticateWS use. Unlike those two, a non-match here is NOT
			// rejected: it falls through to the legacy env-token check below and
			// then to anonymous pass-through, by design (this is the
			// *optional*-auth path). CLITokenContextKey:true marks a CLI-token
			// identity as NOT backed by a Gateway.Users row (see that key's doc
			// for why callers like HandleLogout must check it before treating
			// Username as a Gateway.Users lookup key).
			if user, viaCLIToken, matched := resolveBearerIdentity(cfg, rawToken); matched {
				ctx := context.WithValue(r.Context(), UserContextKey{}, user)
				if viaCLIToken {
					ctx = context.WithValue(ctx, CLITokenContextKey{}, true)
				}
				a.setCORSHeaders(w, r)
				handler(w, r.WithContext(ctx))
				return
			}
			// Token present but matched neither — treat as anonymous (optional auth).
			// Legacy env var fallback
			required := os.Getenv("OMNIPUS_BEARER_TOKEN")
			if required != "" && subtle.ConstantTimeCompare([]byte(rawToken), []byte(required)) == 1 {
				a.setCORSHeaders(w, r)
				handler(w, r)
				return
			}
		}
		// Cookie fallback (ADR-044, FR-009): the SPA no longer sends a bearer
		// token — it authenticates via the omnipus-session HttpOnly cookie. Resolve
		// it here (the same lookup checkBearerAuth/authenticateWS/browser_ws use) so
		// optional-auth routes that DO require a user post-onboarding — e.g.
		// PUT /providers/{id} and POST /providers/{id}/test, whose
		// handlers 401 when UserContextKey is nil — see the logged-in identity
		// instead of falling through to anonymous. Like a non-matching bearer above,
		// a cookie-parse error or no match is NOT a hard 401 here: it falls through
		// to anonymous pass-through, keeping this the *optional*-auth path.
		if user, err := middleware.ResolveUserFromCookie(r, cfg.Gateway.Users); err == nil && user != nil {
			ctx := context.WithValue(r.Context(), UserContextKey{}, user)
			a.setCORSHeaders(w, r)
			handler(w, r.WithContext(ctx))
			return
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

	// Pre-compute the bearer-token bcrypt hash and its embedded ID outside the
	// config lock (bcrypt is intentionally slow; must not hold configMu).
	// SEC-1: bcrypt only the secret body (config.TokenSecret) so the input stays
	// under bcrypt's 72-byte ceiling — the ID prefix is non-secret routing data.
	tokenHash, err := bcrypt.GenerateFromPassword([]byte(config.TokenSecret(token)), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("auth: hash token failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "login failed")
		return
	}
	tokenID := config.TokenIDFromRaw(token)
	createdAt := time.Now().UTC().Format(time.RFC3339)

	var evictedTokens int
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
			// SEC-1 / UAT #399: APPEND to the bearer-token set — never overwrite.
			// Existing tokens for other tabs/devices/clients stay valid; the cap
			// evicts only the oldest entries beyond config.MaxUserTokens.
			evictedTokens = appendUserToken(userMap, tokenID, string(tokenHash), createdAt)
			// Session-cookie token remains single-slot (one browser session
			// cookie per login is the existing contract); overwrite as before.
			userMap["session_token_hash"] = string(sessionHash)
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

	if evictedTokens > 0 {
		slog.Info("auth: evicted oldest bearer tokens over cap",
			"username", body.Username, "evicted", evictedTokens, "cap", config.MaxUserTokens)
	}

	// The new token is already live in memory: safeUpdateConfigJSON above ran
	// refreshConfigAndRewireServices → SwapConfig, so withAuth reads the updated
	// Gateway.Users immediately. We deliberately do NOT call triggerReloadAndWait here: a
	// full service reload for a token append needlessly restarts channels/cron
	// and can cancel an in-flight scheduled run (#412 — every login churned a
	// reload that canceled Run-now turns).

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
	})
}

// HandleLogout handles POST /api/v1/auth/logout.
// Revokes ONLY the caller's presented bearer token (SEC-1 / UAT #399) — the
// user's other tokens stay valid, so concurrent sessions on other tabs/devices
// are unaffected. It also clears the single-slot session_token_hash in
// config.json and revokes both browser-side cookies (session + CSRF).
// A CLI-token-authenticated caller (CLITokenContextKey) short-circuits
// before any of that — it has no Gateway.Users row to look up.
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

	// A CLI-token-authenticated caller's synthetic "cli" identity is not
	// backed by any Gateway.Users row (see CLITokenContextKey's doc) — the
	// Gateway.Users JSON lookup below would either find nothing (500, the
	// bug this branch fixes) or, worse, match a same-named human account.
	// There is nothing to revoke in Gateway.Users for this caller: CLI-token
	// revocation is a separate concern already handled by
	// cmd/omnipus/internal/clitoken's ResetCLIToken. Still clear the
	// browser-side cookies (a harmless no-op for a non-browser CLI caller)
	// and report success like any other logout.
	if viaCLI, _ := r.Context().Value(CLITokenContextKey{}).(bool); viaCLI {
		middleware.ClearSessionCookie(w, r)
		middleware.ClearCSRFCookie(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// dev_mode_bypass synthetic identity: like the CLI caller above, the
	// "_dev_bypass" user (checkBearerAuth's bypass short-circuit, auth.go) has no
	// Gateway.Users row, so the username lookup below would fail with "user not
	// found" (500) and skip clearing the browser cookies entirely. Expiring the
	// browser-side cookies is the only meaningful logout action for a synthetic
	// identity, so do that and report success — matching the CLI-token path.
	// Only reachable when gateway.dev_mode_bypass=true (never in production,
	// where bypass is off and the request authenticates as a real cookie/bearer
	// user with a genuine Gateway.Users row to revoke).
	if user.Username == devBypassUser.Username {
		middleware.ClearSessionCookie(w, r)
		middleware.ClearCSRFCookie(w, r)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// SEC-1 / UAT #399: revoke ONLY the caller's presented bearer token, not
	// every token the user holds — concurrent sessions on other tabs/devices
	// must remain valid. Recover the presented token from the Authorization
	// header to locate its entry in the token set.
	var presentedToken string
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		presentedToken = strings.TrimPrefix(auth, "Bearer ")
	}
	presentedID := config.TokenIDFromRaw(presentedToken)
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
			revokeUserToken(um, presentedToken, presentedID)
			// Always clear the single-slot session-cookie hash — logout ends
			// this browser session regardless of the bearer-token path.
			um["session_token_hash"] = ""
			return nil
		}
		return fmt.Errorf("user not found in config")
	}); err != nil {
		slog.Error("auth: logout failed", "error", err, "username", user.Username)
		jsonErr(w, http.StatusInternalServerError, "logout failed")
		return
	}
	// Token revocation is already live via safeUpdateConfigJSON → SwapConfig
	// above; no full service reload needed (avoids channel/cron churn — #412).
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
			// with the new password. SEC-1 / UAT #399: login now appends bearer
			// tokens to the "tokens" set rather than the legacy single
			// "token_hash" — clearing only the legacy field would leave every
			// active bearer token in "tokens" live, so the password change would
			// NOT log old sessions out. Clear the whole token set plus both
			// legacy single-slot hashes.
			um["tokens"] = []any{}
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
	if confirmed, err := a.triggerReloadAndWaitOutcome(); err != nil {
		slog.Warn("auth: hot-reload after change-password failed", "error", err)
	} else if !confirmed {
		// FIX 4: previously silent — a timeout was indistinguishable from a
		// confirmed reload (both returned a nil error), so nothing was ever
		// logged for this case even though it means the in-memory config
		// (and therefore the freshly-cleared token set) may not be fully
		// live on every code path yet. gen.OperationResult below has no
		// field to surface this to the HTTP caller (see
		// triggerReloadAndWaitOutcome's doc comment) — this log line is the
		// only durable record until that response type grows one.
		slog.Warn("auth: hot-reload after change-password did not confirm within the poll window",
			"username", user.Username)
	}
	// Revoke the caller's browser-side cookies. Old bearer tokens and session
	// cookies are now invalid (hashes cleared above). The SPA must redirect to
	// login. Cookie-clearing headers must be written before the JSON body.
	middleware.ClearSessionCookie(w, r)
	middleware.ClearCSRFCookie(w, r)
	jsonOK(w, gen.OperationResult{Success: true})
}

// generateUserToken creates a random bearer token for authentication.
//
// SEC-1 / UAT #399: the token embeds a short non-secret ID prefix
// ("omnipus_<id>_<body>") so verification can index directly to the matching
// hash in the user's token SET instead of bcrypt-looping every entry. Use
// config.TokenIDFromRaw to recover the embedded ID and config.TokenSecret to
// recover the bcrypt-hashed body when storing the token's hash in a token-set
// entry. Token entropy is 256-bit (32 bytes) in the secret body, as before —
// the 8-hex-char (32-bit) ID is non-secret routing metadata, not part of the
// authentication secret.
//
// The signature accepts a (currently unused) username parameter so existing call
// sites compile unchanged; the token is user-agnostic — the caller stamps the ID
// after the fact via config.TokenIDFromRaw.
func generateUserToken(_ string) (string, error) {
	idBytes := make([]byte, 4) // 32-bit non-secret routing prefix
	if _, err := rand.Read(idBytes); err != nil {
		return "", fmt.Errorf("rand read failed: %w", err)
	}
	body := make([]byte, 32) // 256-bit secret body
	if _, err := rand.Read(body); err != nil {
		return "", fmt.Errorf("rand read failed: %w", err)
	}
	return "omnipus_" + hex.EncodeToString(idBytes) + "_" + hex.EncodeToString(body), nil
}

// appendUserToken adds a new token entry to a user's JSON token set (the
// "tokens" array on the userMap), evicting the oldest entries beyond
// config.MaxUserTokens. It returns the number of evicted entries so the caller
// can log the eviction. Login appends, never evicts the caller's own token
// unless the cap forces it.
func appendUserToken(userMap map[string]any, id, hash, createdAt string) (evicted int) {
	var tokens []any
	if raw, ok := userMap["tokens"].([]any); ok {
		tokens = raw
	}
	tokens = append(tokens, map[string]any{
		"id":         id,
		"hash":       hash,
		"created_at": createdAt,
	})
	// Cap: keep the newest config.MaxUserTokens entries (slice is append-order,
	// so the oldest are at the front).
	if len(tokens) > config.MaxUserTokens {
		evicted = len(tokens) - config.MaxUserTokens
		tokens = tokens[evicted:]
	}
	userMap["tokens"] = tokens
	return evicted
}

// revokeUserToken removes exactly ONE token entry — the one matching the
// presented raw token — from a user's JSON token set, leaving every other
// concurrent session intact (SEC-1 / UAT #399).
//
// Matching strategy:
//   - When the presented token carries an embedded ID, remove the entry whose
//     "id" equals it (and whose hash verifies, defending against a forged ID
//     prefix that does not actually authenticate).
//   - Otherwise (legacy token with no ID), bcrypt-scan the set and remove the
//     first entry that verifies, and clear the legacy single token_hash if it
//     verifies.
//
// A no-op (no matching live token) is acceptable — the cookies are still
// cleared by the caller, so the browser session ends regardless.
func revokeUserToken(userMap map[string]any, presentedToken, presentedID string) {
	// Legacy single token_hash was computed over the FULL raw token; clear it
	// if the presented token verifies.
	if legacy, ok := userMap["token_hash"].(string); ok && legacy != "" {
		if config.BcryptHash(legacy).Verify(presentedToken) == nil {
			userMap["token_hash"] = ""
		}
	}

	raw, ok := userMap["tokens"].([]any)
	if !ok || len(raw) == 0 {
		return
	}
	// Token-set entry hashes are computed over the secret body only (SEC-1).
	secret := config.TokenSecret(presentedToken)
	kept := make([]any, 0, len(raw))
	removed := false
	for _, e := range raw {
		entry, ok := e.(map[string]any)
		if !ok {
			continue // drop malformed entries
		}
		if !removed {
			entryID, _ := entry["id"].(string)
			entryHash, _ := entry["hash"].(string)
			match := false
			if presentedID != "" && entryID == presentedID {
				// ID matches — confirm with a bcrypt verify so a spoofed ID
				// prefix on a non-matching body cannot revoke someone's token.
				match = config.BcryptHash(entryHash).Verify(secret) == nil
			} else if presentedID == "" {
				// Legacy token (no ID): match by bcrypt verify over the body
				// (which for a legacy token equals the full token).
				match = config.BcryptHash(entryHash).Verify(secret) == nil
			}
			if match {
				removed = true
				continue // drop this entry
			}
		}
		kept = append(kept, entry)
	}
	userMap["tokens"] = kept
}

// reloadWaitTimeout bounds how long a config-write handler waits for the
// in-memory rebuild to land — the poll window triggerReloadAndWaitOutcome
// waits for IsReloadPending() to clear before reporting confirmed=false.
//
// This was 5 SECONDS, and that was a release blocker in its own right. A reload
// stops and restarts every channel, cron, the plan engine, the task/loop
// schedulers and the provider, then rebuilds the whole AgentRegistry. On an
// idle gateway that is milliseconds; under real load (a busy plan engine, live
// LLM turns — the llm-conformance e2e shard) it is TENS OF SECONDS. Every
// reload that overran 5s hit the silent-nil return in triggerReloadAndWait, so
// POST /agents answered 201 with no warning while the registry still had no
// such agent, and the caller's next POST /tasks or POST /workspaces failed
// with `agent "x" not found` / `core_team member "x" is not a registered agent`.
//
// The value is DERIVED from what a reload can actually cost rather than picked:
// handleConfigReload drains every service with serviceShutdownTimeout and then
// rebuilds the provider under providerReloadTimeout, and those can serialise.
// A caller must not give up on a reload that is still legitimately progressing,
// so the bound is that worst case plus slack. Deriving it also keeps this
// correct if either constant is retuned.
//
// This is a bound on being WRONG, not a target: the normal path returns as soon
// as the rebuild completes (milliseconds on an idle gateway), and coalescing
// (services.beginReload) guarantees a reload that post-dates the caller's write
// is actually queued.
//
// A var, not a const, only so the expiry BEHAVIOUR can be tested without a
// 90-second test. Never reassign it outside tests.
var reloadWaitTimeout = serviceShutdownTimeout + providerReloadTimeout + 30*time.Second

// triggerReloadAndWaitOutcome is triggerReloadAndWait's richer sibling: same
// trigger-and-poll sequence, but the return additionally distinguishes a
// CONFIRMED reload (IsReloadPending cleared before the deadline) from one
// that merely timed out still pending.
//
// FIX 4 (HIGH, live re-review): triggerReloadAndWait's plain `error` return
// made these two outcomes indistinguishable — both returned nil — and all
// 16 existing call sites across this package treat err == nil as "the
// change is live," returning 200 accordingly. executeReload (tool-policy
// validation, credential re-injection, channel restarts) can plausibly
// exceed the poll window on a busy gateway, so god-mode, global tool
// policies, rate limits, prompt guard, mailbox, agent CRUD, etc. could all
// report "applied" while the OLD config was still in force.
//
// triggerReloadAndWait itself keeps its EXACT original signature and
// behavior (confirmed is discarded below; err == nil on both a confirmed
// reload and an unconfirmed timeout) rather than being changed in place:
// 15 of its 16 call sites live in files outside this fix's scope (rest.go,
// rest_god_mode.go, rest_mailbox.go, rest_rate_limits.go,
// rest_session_scope.go, rest_onboarding.go, rest_prompt_guard.go,
// rest_tool_policies.go), so changing the shared signature would force a
// mechanical edit to every one of them just to keep the package compiling.
// Worse, most respond with a wire type that has no field to carry the
// distinction at all — HandleChangePassword's own gen.OperationResult
// (Success/Error/Validation only) is one example; only
// rest_prompt_guard.go's response type happens to already have a
// RequiresRestart/Warning slot, and giving the others one would need a
// contracts/openapi.yaml change (Constraint #8), disproportionate to this
// fix. triggerReloadAndWaitOutcome exists so a caller that CAN act on the
// distinction has a real signal to read instead of only nil — today that is
// HandleChangePassword's own log line (see its call site); a future pass
// touching one of the other 15 callers can adopt it for that caller's
// response too.
func (a *restAPI) triggerReloadAndWaitOutcome() (confirmed bool, err error) {
	if err := a.agentLoop.TriggerReload(); err != nil {
		if errors.Is(err, agent.ErrReloadNotConfigured) {
			// Unit-test environment — no reload pipeline wired; nothing is
			// pending to confirm, so this is a confirmed no-op, not an
			// unconfirmed one.
			return true, nil
		}
		if errors.Is(err, agent.ErrReloadAlreadyInProgress) {
			// Another reload is in flight; poll until it completes rather than
			// returning an error — the result will include our config change.
			// Fall through to the polling loop below.
		} else {
			slog.Error("config reload failed", "error", err)
			return false, err
		}
	}
	deadline := time.Now().Add(reloadWaitTimeout)
	for time.Now().Before(deadline) {
		if !a.agentLoop.IsReloadPending() {
			return true, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Reload may still be running — the exact ambiguity FIX 4 closes.
	// confirmed=false with a nil error: callers that only check err keep
	// their original "not blocked indefinitely" behavior; callers that
	// check confirmed can now tell the two outcomes apart. triggerReloadAndWait
	// below is NOT one of those callers — it turns confirmed=false into a real
	// error, per reloadWaitTimeout's own "MUST NOT be swallowed" contract.
	return false, nil
}

// triggerReloadAndWait triggers a config reload and polls until
// IsReloadPending() clears (indicating the in-memory config has been updated),
// up to reloadWaitTimeout.
//
// Returns an error when the reload fails to start AND when the wait times out.
// The timeout MUST NOT be swallowed: it used to `return nil`, which told the
// caller the rebuild had landed when it had not — callers turn that nil into a
// 201 with no warning (createAgent) or a plain 200 (rotateGatewayToken,
// HandleProviders), so a handler reported success for a change that was not yet
// live. Persisted-but-not-live is a real, caller-visible state and it has to
// surface as one; see reloadWaitTimeout's own doc comment for the incident
// this caused.
//
// The special case "reload not configured" (reloadFunc == nil) is treated as a
// no-op rather than an error: this condition is normal in unit tests where the
// full gateway reload pipeline is not wired. Production always configures the
// reload function during startup.
func (a *restAPI) triggerReloadAndWait() error {
	confirmed, err := a.triggerReloadAndWaitOutcome()
	if err != nil {
		return err
	}
	if !confirmed {
		slog.Error("config reload did not complete within the wait deadline; "+
			"the change is persisted on disk but is not yet live in memory",
			"timeout", reloadWaitTimeout)
		return fmt.Errorf(
			"config reload did not complete within %s; the change is saved but not yet active",
			reloadWaitTimeout,
		)
	}
	return nil
}
