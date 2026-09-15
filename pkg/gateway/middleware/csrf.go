// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package middleware

// CSRF protection implements the double-submit cookie pattern: a random value
// is stored in an HttpOnly=false __Host-csrf cookie and must be echoed in the
// X-Csrf-Token request header on every state-changing method. Cross-origin
// JavaScript cannot read the cookie (same-origin policy applies to cookies
// bound to our origin), so an attacker cannot forge the header even if they
// can trick a browser into sending the request.
//
// References:
//   - OWASP CSRF cheat sheet: https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html
//   - __Host- cookie prefix: https://developer.mozilla.org/en-US/docs/Web/HTTP/Cookies#cookie_prefixes

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/gateway/pathredact"
)

// CSRFCookieName is the name of the double-submit cookie under TLS.
//
// The __Host- prefix enforces three properties at the browser layer:
//   - Secure must be set
//   - Domain must be absent (attaches only to the exact host that issued it)
//   - Path must be /
//
// If any of those are violated the browser refuses to store the cookie.
// This is defense-in-depth against a weaker cookie slipping out.
const CSRFCookieName = "__Host-csrf"

// CSRFCookieNameHTTP is the fallback cookie name used when the gateway is
// reached over plain HTTP. The __Host- prefix requires Secure=true, which
// browsers enforce by DROPPING the cookie on non-TLS origins (except
// localhost). On plain-HTTP deployments (dev, self-hosted behind a
// non-terminating proxy, public-IP test instances without a cert) the
// __Host- cookie therefore never installs and every double-submit check
// fails with "csrf cookie missing". Using an un-prefixed name plus
// Secure=false lets the double-submit pattern still work on HTTP.
// SameSite=Strict still blocks cross-origin sends — the protection the
// middleware actually cares about survives the downgrade.
const CSRFCookieNameHTTP = "csrf"

// CSRFHeaderName is the header that clients must echo with the cookie value.
//
// The value is stored in Go's canonical MIME header form (X-Csrf-Token). HTTP
// headers are case-insensitive on the wire, and Go normalizes incoming header
// names via http.CanonicalMIMEHeaderKey before r.Header.Get looks them up, so
// browsers or SDKs that send "X-CSRF-Token" or "x-csrf-token" will all match.
// The canonical spelling here satisfies the canonicalheader linter.
const CSRFHeaderName = "X-Csrf-Token"

// csrfTokenBytes is the entropy of a fresh token (256 bits).
const csrfTokenBytes = 32

// defaultExemptPaths are the bootstrap / operational routes bypassed by the
// gate when the caller has NOT supplied any WithExemptPath / WithExemptPaths /
// WithDefaultExempts option. Calling CSRFMiddleware() with no options yields
// this default set.
//
// Invariant: every path in this set that is a POST/PUT/PATCH/DELETE MUST
// call IssueCSRFCookie on successful response. The exemption exists because
// those endpoints ARE the cookie-issuing path — requiring a pre-existing
// cookie on them would be a circular dependency (chicken-and-egg: no cookie
// exists until the handler runs). If you add a path here, wire IssueCSRFCookie
// into the success branch of the handler. If you remove IssueCSRFCookie from
// one of these handlers, remove the entry here too so the gate still applies.
//
// Bootstrap / cookie-issuer endpoints (DO reach the REST mux and thus DO pass
// through this middleware):
//   - /api/v1/onboarding/complete — called on fresh install before any auth
//     exists. Issues the cookie so the SPA's first post-onboarding request
//     can pass the gate.
//   - /api/v1/auth/login — the SPA reaches this with no cookie on first load
//     of an existing install (refresh, new tab). Issues the cookie on 200.
//
// Operational endpoints (/health, /ready, /reload):
//   - These are defense-in-depth for the case where a future refactor mounts
//     these on the REST mux. Currently they are served on the separate
//     health-server mux (pkg/health/server.go) and this gate never runs for
//     them. Keeping the entries here means that if a maintainer accidentally
//     moves them onto the main mux, liveness probes and the operator reload
//     trigger still function without a rebuild of the middleware chain.
//
// Plan reference: temporal-puzzling-melody.md §1, PR-H.
var defaultExemptPaths = []string{
	"/api/v1/onboarding/complete",
	"/api/v1/onboarding/probe-provider",
	"/api/v1/auth/login",
	"/health",
	"/ready",
	"/reload",
}

// defaultExemptPrefixes are path PREFIXES exempt from the CSRF check for ALL
// methods, unconditionally — unlike defaultExemptPaths (a bootstrap
// convenience set a caller can opt out of via WithExemptPath), this set
// encodes a structural security boundary and is always installed regardless
// of whether the caller customizes the exact-match exempt set.
//
//   - /preview/ — the reverse-proxied dev-preview surface (rest_preview.go
//     HandlePreview). Preview URLs are tokenized
//     (/preview/<agent>/<token>/…), so an exact-path exemption can never
//     match; a previewed app must be able to POST through the proxy to its
//     own dev server without knowing the gateway's CSRF token. This prefix
//     deliberately does not overlap /api/v1/* — that surface keeps full CSRF
//     enforcement (FR-012).
//
//   - /webhook/ — platform-originated webhook receivers (Google Chat
//     /webhook/google-chat, LINE /webhook/line, and their /health
//     sub-paths). Inbound webhook POSTs are sent BY the upstream platform,
//     not by a browser holding a CSRF cookie, so they carry neither the
//     __Host-csrf cookie nor the X-Csrf-Token header. Webhook handlers rely
//     on their OWN signature verification (hmac.Equal — see
//     webhook_signature_test.go's hygiene gate) rather than the double-submit
//     CSRF check, so exempting this prefix does not weaken security: every
//     WebhookPath()-bearing channel is statically required to implement
//     verifySignature. Like /preview/, /webhook/ never overlaps /api/v1/*.
//
//   - /library-preview/ — the ADR-067 Library preview-token surface
//     (gateway.registerLibraryPreviewRoutes). Its URLs are tokenized
//     (/library-preview/<token>/<relative-path>), so an exact-path exemption
//     can never match. TWO separate reasons, and the second is the one that
//     bites in production:
//
//     (a) FR-003j requires every non-GET/HEAD method on that prefix to answer
//     405 with `Allow: GET, HEAD`, and §10.3 requires EVERY response on the
//     prefix to carry the measured isolation policy byte for byte. Without the
//     exemption a cookie-less POST — the commonest browser case — is answered
//     by this gate with a policy-free 403 JSON body before the router can 405
//     it, so neither requirement holds for the case that actually occurs.
//
//     (b) A previewed document is bound to an OPAQUE origin (that is the whole
//     of ADR-067's P0 control), which makes every subresource request it issues
//     cross-site. The CSRF cookie is SameSite=Strict, so it is never sent — and
//     the safe-method re-mint branch below fires on EVERY stylesheet, script,
//     font, image and media request the previewed page makes, stamping a fresh
//     Set-Cookie onto each preview response and rotating the operator's live
//     CSRF cookie at a rate the attacker-authored page chooses. That is exactly
//     the "preview responses picking up a stray Set-Cookie" hazard /preview/ is
//     exempted for; /library-preview/ reproduces it byte for byte.
//
//     Exempting costs nothing: the route serves GET and HEAD only, never reads
//     a request body, and mutates no state — which is FR-003f's argument for
//     why it needs no CSRF protection in the first place. Like the two prefixes
//     above, it never overlaps /api/v1/*, so the authenticated API surface
//     keeps full enforcement. The MINT endpoint, which is a real POST that
//     issues a credential, lives at /api/v1/library/preview-token and is
//     therefore NOT exempt.
var defaultExemptPrefixes = []string{
	PreviewPathPrefix,
	WebhookPathPrefix,
	LibraryPreviewPathPrefix,
}

// LibraryPreviewPathPrefix is the bare, path-token-authenticated prefix the
// ADR-067 Library preview serves from (§10.5). See defaultExemptPrefixes for
// why it is CSRF-exempt for all methods.
//
// It is defined HERE, in the middleware package, and imported by pkg/gateway —
// not the other way round — because pkg/gateway already imports this package,
// and two independent spellings of a security-boundary prefix drift silently:
// the middleware would keep exempting "/library-preview/" while the router
// served something else.
const LibraryPreviewPathPrefix = "/library-preview/"

// WebhookPathPrefix is the path prefix under which platform-originated
// webhook receivers are mounted (e.g. /webhook/google-chat, /webhook/line,
// /webhook/google-chat/health). See defaultExemptPrefixes for why it is
// CSRF-exempt.
const WebhookPathPrefix = "/webhook/"

// stateChangingMethods lists the HTTP verbs that trigger CSRF enforcement.
// GET/HEAD/OPTIONS are RFC-safe methods and MUST NOT be gated — doing so
// breaks preflight and simple reads. PATCH is included because it mutates.
var stateChangingMethods = map[string]struct{}{
	http.MethodPost:   {},
	http.MethodPut:    {},
	http.MethodPatch:  {},
	http.MethodDelete: {},
}

// MismatchReporter is called when a state-changing request passes the cookie
// presence check but the cookie and header don't match. Implementations
// typically write to the audit log (SEC-15). Passing nil disables reporting;
// the middleware still rejects the request.
//
// route is the raw r.URL.Path, sourceIP is the remote IP extracted by the
// caller's convention (X-Forwarded-For, RemoteAddr, etc.). The middleware
// deliberately does not parse IP itself — that concern belongs to the
// gateway's existing clientIP helper.
type MismatchReporter func(r *http.Request, sourceIP, route string)

// csrfConfig holds resolved middleware state. It is internal to this package
// and is not reachable by callers — configuration is expressed exclusively
// through Option values passed to CSRFMiddleware. This prevents a caller
// from mutating (for example) the exempt-path set after construction:
// once CSRFMiddleware returns, the configuration is frozen inside a closure.
type csrfConfig struct {
	exempt map[string]struct{}
	// exemptPrefixes holds path PREFIXES exempt from the CSRF check for ALL
	// methods (e.g. "/preview/"). Unlike exempt (exact match), this set is
	// always seeded with defaultExemptPrefixes regardless of
	// customExemptSet — it is a structural boundary, not a bootstrap
	// convenience the caller can opt out of.
	exemptPrefixes map[string]struct{}
	reporter       MismatchReporter
	clientIP       func(r *http.Request) string
	// customExemptSet reports whether any WithExemptPath/WithExemptPaths/
	// WithDefaultExempts option was applied. When false, the default set is
	// installed at build time.
	customExemptSet bool
}

// Option mutates a csrfConfig during construction. Options are applied in
// the order they are passed to CSRFMiddleware.
//
// Exempt-path options (WithExemptPath, WithExemptPaths, WithDefaultExempts)
// are additive: each call extends the set. To opt out of the default bootstrap
// paths, simply pass only the WithExemptPath options you want. To explicitly
// include the defaults alongside custom paths, combine WithDefaultExempts with
// other WithExemptPath calls.
type Option func(*csrfConfig)

// WithExemptPath appends a single path to the exempt set. Exact match only —
// no prefix or glob. Calling this option (with or without paths) marks the
// exempt set as "caller-customized"; the built-in default set will NOT be
// installed unless WithDefaultExempts is also passed.
func WithExemptPath(path string) Option {
	return func(c *csrfConfig) {
		// Empty path is almost always a config wiring bug (typo or
		// unset-var-interpolated-to-""). Panic at build time to surface
		// it loudly — the alternative is silently dropping the default
		// bootstrap exempts and breaking onboarding/login.
		if path == "" {
			panic("middleware.WithExemptPath: empty path; pass a non-empty route or omit the option")
		}
		c.customExemptSet = true
		if c.exempt == nil {
			c.exempt = make(map[string]struct{})
		}
		c.exempt[path] = struct{}{}
	}
}

// WithExemptPaths appends multiple paths to the exempt set. Exact match only.
// Equivalent to calling WithExemptPath once per path. Marks the exempt set
// as "caller-customized"; the default set is NOT installed unless
// WithDefaultExempts is also passed.
func WithExemptPaths(paths ...string) Option {
	return func(c *csrfConfig) {
		c.customExemptSet = true
		if c.exempt == nil {
			c.exempt = make(map[string]struct{})
		}
		for _, p := range paths {
			if p == "" {
				continue
			}
			c.exempt[p] = struct{}{}
		}
	}
}

// WithDefaultExempts explicitly installs the built-in bootstrap exempt set
// (/api/v1/onboarding/complete, /api/v1/auth/login, /health, /ready, /reload).
// Use this when you want the defaults AND additional custom paths —
// otherwise passing WithExemptPath alone would drop the defaults.
//
// Calling CSRFMiddleware with zero options is equivalent to calling it with
// only WithDefaultExempts.
func WithDefaultExempts() Option {
	return func(c *csrfConfig) {
		if c.exempt == nil {
			c.exempt = make(map[string]struct{})
		}
		for _, p := range defaultExemptPaths {
			c.exempt[p] = struct{}{}
		}
	}
}

// WithReporter installs the mismatch reporter. If reporter is nil, the
// middleware rejects mismatches silently (still with 403, but without invoking
// any callback). Passing this option with a nil reporter is equivalent to
// not passing it.
func WithReporter(reporter MismatchReporter) Option {
	return func(c *csrfConfig) {
		c.reporter = reporter
	}
}

// WithClientIPFunc installs the client-IP extractor used to populate the
// Reporter's sourceIP argument. If no extractor is set (or nil is passed),
// the middleware falls back to r.RemoteAddr (which may include the port).
// This avoids a hard dependency on the gateway's clientIP helper while still
// allowing production code to plug in the real extractor.
func WithClientIPFunc(f func(r *http.Request) string) Option {
	return func(c *csrfConfig) {
		c.clientIP = f
	}
}

// CSRFMiddleware returns an HTTP middleware that enforces the double-submit
// cookie CSRF check on state-changing requests.
//
// Default behavior (when called with no options) is equivalent to passing
// WithDefaultExempts: the six bootstrap / operational routes listed in
// defaultExemptPaths are exempt, no Reporter is installed, and r.RemoteAddr
// supplies the client IP.
//
// Semantics:
//   - GET, HEAD, OPTIONS: pass through unchanged.
//   - Exempt paths: pass through. The handler is expected to call
//     IssueCSRFCookie to seed a cookie for the client.
//   - POST, PUT, PATCH, DELETE on non-exempt paths:
//     1. If the __Host-csrf cookie is missing → 403 "csrf cookie missing".
//     2. If the X-CSRF-Token header is missing → 403 "csrf header missing".
//     3. If cookie and header don't match (constant-time compare) →
//     403 "csrf token mismatch", Reporter invoked.
//     4. Match → request proceeds to next.
//
// The middleware NEVER allows a state-changing request through when the
// check fails. Fail-closed is the only correct behavior for a gate.
//
// The resolved configuration is captured in the returned closure. Any option
// value passed in (including the paths in WithExemptPaths) is deep-copied
// into the closure — a caller cannot mutate the exempt set after construction
// by retaining a reference to the slice they passed in.
func CSRFMiddleware(opts ...Option) func(http.Handler) http.Handler {
	cfg := &csrfConfig{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(cfg)
	}

	// No option touched the exempt set → install the default. When a caller
	// HAS called WithExemptPath / WithExemptPaths without WithDefaultExempts,
	// the defaults are intentionally omitted; customExemptSet records that
	// intent.
	if !cfg.customExemptSet {
		if cfg.exempt == nil {
			cfg.exempt = make(map[string]struct{})
		}
		for _, p := range defaultExemptPaths {
			cfg.exempt[p] = struct{}{}
		}
	}
	// Replace a nil map with an empty one so the closure's membership check
	// never panics on a zero-option caller who passed only WithExemptPath("").
	if cfg.exempt == nil {
		cfg.exempt = make(map[string]struct{})
	}

	// defaultExemptPrefixes are ALWAYS installed, unconditionally — this is
	// a structural security boundary (FR-012), not a bootstrap convenience
	// gated by customExemptSet like defaultExemptPaths.
	if cfg.exemptPrefixes == nil {
		cfg.exemptPrefixes = make(map[string]struct{})
	}
	for _, p := range defaultExemptPrefixes {
		cfg.exemptPrefixes[p] = struct{}{}
	}

	// Deep-copy the exempt set into a private map so post-construction mutation
	// of any option argument is impossible. Callers cannot reach `exempt` from
	// outside this closure.
	exempt := make(map[string]struct{}, len(cfg.exempt))
	for k := range cfg.exempt {
		exempt[k] = struct{}{}
	}
	exemptPrefixes := make(map[string]struct{}, len(cfg.exemptPrefixes))
	for k := range cfg.exemptPrefixes {
		exemptPrefixes[k] = struct{}{}
	}

	reporter := cfg.reporter
	clientIP := cfg.clientIP
	if clientIP == nil {
		clientIP = func(r *http.Request) string { return r.RemoteAddr }
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Prefix-exempt routes (e.g. /preview/<agent>/<token>/…) bypass
			// the CSRF gate ENTIRELY, for ALL methods — checked first so a
			// preview request never falls into the safe-method re-mint
			// branch below (FR-002: /preview/ skips CSRF checks outright;
			// it must not pick up a stray Set-Cookie from this middleware
			// on a response that a reverse proxy is about to overwrite with
			// the dev server's own headers). /api/v1/* never matches this
			// prefix set, so full CSRF enforcement there is unaffected.
			for prefix := range exemptPrefixes {
				if strings.HasPrefix(r.URL.Path, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// Safe methods — RFC 7231 §4.2.1: GET, HEAD, OPTIONS are side-effect-free.
			if _, safe := stateChangingMethods[r.Method]; !safe {
				// FR-019: re-mint the CSRF cookie on any safe request that
				// lacks one. Without this, a returning user with a valid
				// session but an expired/absent CSRF cookie (session
				// outlives the CSRF cookie's prior no-MaxAge lifetime, or a
				// fresh browser profile) has no way to ever acquire one —
				// every state-changing request 403s forever. Re-minting
				// ONLY on safe methods preserves the double-submit
				// invariant: a state-changing request can never mint itself
				// a fresh token to pass its own check.
				if csrfCookieValue(r) == "" {
					if err := IssueCSRFCookie(w, r); err != nil {
						slog.Warn("csrf: re-mint on safe request failed",
							"path", pathredact.RequestPath(r.URL.Path), "method", r.Method, "error", err)
					}
				}
				next.ServeHTTP(w, r)
				return
			}

			// Exempt route — the handler itself is responsible for seeding
			// a cookie. See IssueCSRFCookie.
			if _, ok := exempt[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}

			// Bearer-token requests are not a CSRF target.
			//
			// CSRF protection exists to defend AMBIENT credentials —
			// cookies the browser auto-attaches to cross-origin requests.
			// An Authorization: Bearer <token> header is NEVER auto-sent
			// by a browser to a different origin, so a malicious site
			// cannot cause a victim's browser to include it. Programmatic
			// clients (curl, API SDKs, mobile apps) supply the header
			// explicitly and are not susceptible to CSRF either.
			//
			// Skipping the cookie/header double-submit check for Bearer
			// callers fixes the plain-HTTP deployment story: operators
			// using `curl -H "Authorization: Bearer …"` no longer have
			// to juggle an impossible-to-install __Host-csrf cookie, and
			// admin tools that use Bearer tokens work identically on
			// HTTP and HTTPS.
			if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
				next.ServeHTTP(w, r)
				return
			}

			// Accept either the TLS-only __Host-csrf cookie OR the
			// plain-HTTP fallback `csrf` cookie. IssueCSRFCookie picks
			// which one to issue based on the request that triggered
			// issuance (TLS → __Host-; HTTP → fallback).
			cookieValue := csrfCookieValue(r)
			if cookieValue == "" {
				writeCSRFError(w, "csrf cookie missing")
				return
			}

			header := r.Header.Get(CSRFHeaderName)
			if header == "" {
				writeCSRFError(w, "csrf header missing")
				return
			}

			if subtle.ConstantTimeCompare([]byte(cookieValue), []byte(header)) != 1 {
				if reporter != nil {
					reporter(r, clientIP(r), r.URL.Path)
				}
				writeCSRFError(w, "csrf token mismatch")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// csrfCookieValue returns the double-submit cookie's value, checking the
// TLS-only __Host-csrf cookie first and falling back to the plain-HTTP
// `csrf` cookie. Returns "" if neither is present (or present but empty),
// which callers treat as "no CSRF cookie on this request" — used both by
// the state-changing enforcement check and the safe-method re-mint check
// (FR-019), so the two branches can never disagree about what "has a
// cookie" means.
func csrfCookieValue(r *http.Request) string {
	if c, err := r.Cookie(CSRFCookieName); err == nil && c.Value != "" {
		return c.Value
	}
	if c, err := r.Cookie(CSRFCookieNameHTTP); err == nil && c.Value != "" {
		return c.Value
	}
	return ""
}

// IssueCSRFCookie generates a fresh 256-bit random token, base64-url encodes
// it, and writes it as the __Host-csrf cookie on the response.
//
// Cookie attributes (all required for double-submit + __Host- prefix):
//   - HttpOnly: false — the SPA must read this cookie to echo the header.
//   - Secure: true — TLS only. Required by __Host-.
//   - SameSite: Strict — prevents cross-origin sends. Defense-in-depth on
//     top of the header check.
//   - Path: /. Required by __Host-.
//   - Domain: unset. Required by __Host-.
//
// Returns an error if the OS RNG fails (practically impossible but the
// contract is honest). Callers should surface a 500 if so.
//
// On a dev-mode server bound to plain HTTP (no TLS) the browser will refuse
// to store a Secure cookie. For localhost this is usually fine because
// modern browsers treat 127.0.0.1/localhost as a "potentially trustworthy
// origin" and honor Secure cookies over http. For arbitrary hosts over
// plain HTTP we fall back to an un-prefixed `csrf` cookie with Secure=false.
//
// The caller supplies the originating request so the issuer can see whether
// the client is TLS-connected; this is best-effort — a reverse proxy that
// terminates TLS must either forward via H2C/HTTPS or set
// X-Forwarded-Proto=https so the middleware picks the secure variant.
func IssueCSRFCookie(w http.ResponseWriter, r *http.Request) error {
	buf := make([]byte, csrfTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("csrf: rand.Read: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)

	if requestIsSecure(r) {
		http.SetCookie(w, &http.Cookie{
			Name:     CSRFCookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: false,
			Secure:   true,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   SessionCookieMaxAge,
			// No Domain — __Host- forbids it.
			// MaxAge matches the session cookie (24h, FR-019) so a
			// returning user's CSRF cookie doesn't expire out from under a
			// still-valid session — see the safe-method re-mint above for
			// the remaining recovery path if it still somehow goes missing.
		})
		return nil
	}

	// Plain-HTTP fallback. Same SameSite=Strict protection (the invariant
	// CSRF cares about). HttpOnly=false so the SPA can read it, Path=/ so
	// it attaches to every endpoint, no Domain. MaxAge matches the session
	// cookie (24h, FR-019).
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieNameHTTP,
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		Secure:   false,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   SessionCookieMaxAge,
	})
	return nil
}

// requestIsSecure detects whether the request reached us over TLS, either
// directly (r.TLS != nil) or via an ingress that forwards X-Forwarded-Proto.
// Used by IssueCSRFCookie to pick the cookie flavor.
func requestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return false
}

// writeCSRFError writes a 403 JSON error response. The response shape matches
// the rest of the gateway's { "error": "..." } convention so the SPA's
// existing error path handles it uniformly.
func writeCSRFError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		slog.Debug("writeCSRFError: encode failed", "error", err)
	}
}
