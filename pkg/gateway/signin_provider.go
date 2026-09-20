// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// signin_provider.go — the sign-in provider seam (ADR-0010 WP2 phase 2).
//
// Phase 1 (auth_mode.go) made the engine compose its auth routes per mode at
// registration time. Platform mode's handlers still lived IN the engine tree
// (rest_platform_auth.go, platform_discovery.go), written straight against
// restAPI's private fields — every upstream merge had to carry them by hand.
// This file is the other half: a public interface a package OUTSIDE the
// engine implements, and a Host interface that hands that package only what
// it needs — never a private restAPI field, never a private pkg/gateway
// function.
//
// Host exposes exactly the seven things WP2's brief named: a config getter
// (Config), the safe config mutator (MutateConfig), an audit logger
// (Auditor), session cookie issuance (IssueSession / WriteSessionToken /
// ResolveSessionUser / IssueCSRFCookie), the caller's client IP (ClientIP), a
// rate-limiter constructor (NewRateLimiter / NewReadSizedRateLimiter) and the
// CSRF-exempt registration (ProviderRoute.CSRFExempt, declared by the
// provider itself). DecodeAndValidate, WriteError and WriteJSON are the
// inbound/outbound wire-contract glue WP2a decided stays in the engine
// (contracts/** does not move) — a provider needs SOME way to decode a
// request against the engine's own schema and answer in the engine's own
// JSON envelope, and this is that way.
//
// restAPIHost is Host's only production implementation. It is the ONLY thing
// in this file allowed to reach into restAPI's private fields — that is the
// entire point of the seam: a moved provider's code review never has to ask
// "does this reach past the interface", because it cannot compile if it does.
//
// RegisterSignInProvider is called once, by editions/cmd/omnipus/main.go,
// before app.Main() runs. The open-source engine's own cmd/omnipus registers
// nothing, so platformAuthMode below falls back to the "no provider
// registered" branch there — which is also what a hosted/desktop binary sees
// if its own registration is ever accidentally skipped: fail closed, never
// fail open onto some other mode's behaviour.

import (
	"net/http"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// PlatformAuthStartPath is the one platform-mode route the engine ALWAYS
// registers, provider or not (WP2 deliverable 2): a signed-out SPA gets one
// stable answer — 503 with no provider registered, the real flow with one —
// rather than a 404 it cannot tell apart from "this build doesn't have
// sign-in at all". Exported so a provider package can pin the same literal
// independently (see editions/platform.StartPath's doc comment — the two
// copies cannot share a Go value across the module boundary the way
// middleware/csrf.go's platformAuthStartPathForCSRF constant already
// explains for the same reason).
const PlatformAuthStartPath = "/api/v1/auth/platform/start"

// ProviderRouteWrap names how the engine wraps one of a provider's routes at
// registration time — a provider declares its intent; auth_mode.go turns it
// into the same authWrapXxx composition local mode's own routes use.
type ProviderRouteWrap int

const (
	// ProviderRouteOptionalAuth (the zero value) is the ordinary posture: an
	// unauthenticated caller passes through, same as authWrapOptionalAuth.
	ProviderRouteOptionalAuth ProviderRouteWrap = iota
	// ProviderRouteBare registers the handler with NO auth wrapper at all —
	// for a route a plain browser navigation reaches with no session and no
	// API envelope, like the platform callback page.
	ProviderRouteBare
)

// ProviderRoute is one HTTP route a registered SignInProvider contributes.
// Handler is expected to already carry whatever rate limiting the provider
// needs (built via Host.NewRateLimiter/NewReadSizedRateLimiter) — the engine
// applies no limiter of its own to a provider route, unlike local mode's
// authRoute.limiter field, because "rate-limited on the provider's own
// limiters" is a property this package's seam preserves rather than owns.
type ProviderRoute struct {
	Path       string
	Handler    http.HandlerFunc
	Wrap       ProviderRouteWrap
	CSRFExempt bool
}

// SignInProvider is what an edition registers to supply platform mode's
// sign-in flow. editions/platform.Provider is the only implementation today;
// the shape is general so a future edition's own provider can register a
// different flow behind the identical seam.
type SignInProvider interface {
	// Name identifies the provider for logging/diagnostics.
	Name() string
	// Routes returns the HTTP routes this provider serves, built against h.
	// Called at route-table composition time (auth_mode.go's platformAuthMode)
	// — which happens once at boot, but ALSO on every request that computes
	// CORS headers (restAPI.corsAllowHeaders), so an implementation MUST be
	// idempotent and must not rebuild per-call state (rate limiters chief
	// among it) on every invocation. See editions/platform.Provider.Routes'
	// own doc comment for how it satisfies that.
	Routes(h Host) []ProviderRoute
}

// Host is everything a registered SignInProvider needs from the engine, and
// nothing else. Every method here is implemented by restAPIHost, below,
// against restAPI's already-private fields and already-private helpers —
// the provider never sees restAPI itself.
//
//nolint:interfacebloat // single-registration-point seam for SignInProvider (ADR-0010); splitting it would force every provider to receive N interfaces and defeat the registration contract. Add a method, don't split.
type Host interface {
	// Config returns the live config snapshot (config can hot-reload; never
	// cache the returned pointer across requests).
	Config() *config.Config
	// MutateConfig applies mutate to config.json's raw JSON map and persists
	// it atomically — the same safe read-modify-write path
	// HandleCompleteOnboarding and every settings-mutation handler uses.
	MutateConfig(mutate func(m map[string]any) error) error

	// Auditor returns the shared audit logger, or nil when auditing is
	// disabled (sandbox.audit_log) — callers must check for nil themselves,
	// exactly as the engine's own handlers do.
	Auditor() *audit.Logger
	// CredentialStore returns the shared encrypted credential store, or nil
	// in a boot state that has none (e.g. a locked store, or a test fixture
	// that wired no store at all).
	CredentialStore() *credentials.Store

	// IssueSession mints an ordinary session — the identical call
	// HandleCompleteOnboarding makes — and returns the raw session token so
	// the caller can hand the SAME session to a second response later (the
	// platform claim handoff). Minting happens ONLY through this method:
	// nothing else in Host exposes the primitive that opens a session at all.
	IssueSession(w http.ResponseWriter, r *http.Request, username string) (token string, err error)
	// WriteSessionToken re-issues the cookie for an ALREADY-minted session
	// token (IssueSession's return value) on a second response — the claim
	// handoff, never a way to mint a new session from an arbitrary string.
	WriteSessionToken(w http.ResponseWriter, r *http.Request, token string)
	// ResolveSessionUser resolves the caller's session cookie, or nil.
	ResolveSessionUser(r *http.Request) *config.UserConfig
	// IssueCSRFCookie seeds the CSRF cookie — the bootstrap contract every
	// CSRF-exempt route that has no cookie yet must carry out on its way out.
	IssueCSRFCookie(w http.ResponseWriter, r *http.Request) error

	// ClientIP resolves the caller's address honouring gateway.trust_xff
	// exactly as the engine's own handlers see it (for audit source_ip and
	// log lines — NOT the rate limiter's own key, which NewRateLimiter's
	// returned RateLimiter resolves the identical way internally).
	ClientIP(r *http.Request) string

	// NewRateLimiter and NewReadSizedRateLimiter build a per-IP sliding-window
	// limiter with the engine's own semantics (429 + Retry-After on trip,
	// XFF-spoofing resistant via ClientIP's same logic) for the provider to
	// wrap its own routes with. NewReadSizedRateLimiter is for a route that
	// is exclusively polled GET/HEAD, where the declared limit IS the ceiling
	// (see apiRateLimiter.readSized's doc comment) — every other route wants
	// NewRateLimiter. Build once and reuse; see ProviderRoute's doc comment.
	NewRateLimiter(limit int, window time.Duration) RateLimiter
	NewReadSizedRateLimiter(limit int, window time.Duration) RateLimiter

	// DecodeAndValidate decodes r's JSON body into dst and, when this
	// instance has gateway.validate_inbound enabled, validates it first
	// against the ENGINE's own contracts/components/schemas/<schemaName>.yaml
	// (WP2a: the platform endpoints' wire schemas stay in engine/omnipus).
	// Returns false — having already written the HTTP response — on any
	// decode or validation failure.
	DecodeAndValidate(w http.ResponseWriter, r *http.Request, schemaName string, dst any) bool
	// WriteError and WriteJSON write the engine's own JSON envelope
	// (gen.ErrorResponse for an error, the raw body for a 200) so a
	// provider's responses are byte-for-byte what a handler still inside the
	// engine would have written.
	WriteError(w http.ResponseWriter, status int, msg string)
	WriteJSON(w http.ResponseWriter, body any)
}

// RateLimiter is what Host.NewRateLimiter/NewReadSizedRateLimiter hands a
// provider: enough to rate-limit its own routes with the engine's own
// sliding-window limiter, without depending on the engine's private
// apiRateLimiter type.
type RateLimiter interface {
	// Wrap applies rate limiting to next, in front of it — the same
	// withRateLimit(limiter, handler) composition local mode's routes get
	// from auth_mode.go, just invoked by the provider on itself instead.
	Wrap(next http.HandlerFunc) http.HandlerFunc
	// Limit and Window report the ceiling this limiter was built with, so a
	// provider's own tests can pin the documented numbers without reaching
	// into a private struct field.
	Limit() int
	Window() time.Duration
}

// signInProvider is the process-global registration slot. Set once, by
// RegisterSignInProvider, before the gateway starts serving; read on every
// call to platformAuthMode (auth_mode.go). Never written to after boot in
// production; setSignInProviderForTest (signin_provider_test.go) is the only
// other writer, and only this package's own tests may reach it.
var signInProvider SignInProvider

// RegisterSignInProvider installs the platform sign-in provider. Call once,
// from an edition's main package (editions/cmd/omnipus/main.go), before
// app.Main() runs — never at runtime, and never a second time: ADR-0010
// decision 3 is that nothing at runtime moves a binary between auth modes,
// and a provider silently swapped out from under a running process is the
// same class of hazard wearing a different hat.
//
// The open-source engine's own cmd/omnipus never calls this. Platform mode
// with no provider registered (a hosted/desktop build whose registration was
// somehow skipped, or literally any non-edition build stamped hosted/desktop)
// falls back to platformAuthMode's "no provider" branch: fail closed, not
// fail open onto some other behaviour.
func RegisterSignInProvider(p SignInProvider) {
	if p == nil {
		panic("gateway: RegisterSignInProvider called with a nil provider")
	}
	if signInProvider != nil {
		panic("gateway: RegisterSignInProvider called twice — a provider replaced mid-process " +
			"is the runtime auth-mode switch ADR-0010 decision 3 forbids")
	}
	signInProvider = p
}

// platformSignInNotConfigured answers every request to
// PlatformAuthStartPath when platform mode is active but no SignInProvider is
// registered (WP2 deliverable 2). It is the ONLY platform-mode route that
// exists in that state — see platformAuthMode (auth_mode.go).
func platformSignInNotConfigured(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	jsonErr(w, http.StatusServiceUnavailable,
		"Signing in is not configured on this instance: no sign-in provider is registered.")
}

// ── restAPIHost: Host's production implementation ───────────────────────────

// restAPIHost is Host's only production implementation, over *restAPI. It
// exists so a registered SignInProvider never sees restAPI's private fields —
// only this file, already inside package gateway, does.
type restAPIHost struct{ a *restAPI }

// signInHost builds the Host a's registered SignInProvider is called with.
// Cheap and stateless (a value wrapping a's pointer) — safe to build fresh on
// every call.
func (a *restAPI) signInHost() Host { return restAPIHost{a: a} }

func (h restAPIHost) Config() *config.Config { return h.a.agentLoop.GetConfig() }

func (h restAPIHost) MutateConfig(mutate func(m map[string]any) error) error {
	return h.a.safeUpdateConfigJSON(mutate)
}

func (h restAPIHost) Auditor() *audit.Logger { return h.a.auditor }

func (h restAPIHost) CredentialStore() *credentials.Store { return h.a.credStore }

func (h restAPIHost) IssueSession(w http.ResponseWriter, r *http.Request, username string) (string, error) {
	return middleware.IssueSessionCookie(w, r, username, h.a.safeUpdateConfigJSON)
}

func (h restAPIHost) WriteSessionToken(w http.ResponseWriter, r *http.Request, token string) {
	middleware.WriteSessionCookie(w, r, token)
}

func (h restAPIHost) ResolveSessionUser(r *http.Request) *config.UserConfig {
	cfg := h.a.agentLoop.GetConfig()
	user, err := middleware.ResolveUserFromCookie(r, cfg.Gateway.Users)
	if err != nil {
		return nil
	}
	return user
}

func (h restAPIHost) IssueCSRFCookie(w http.ResponseWriter, r *http.Request) error {
	return middleware.IssueCSRFCookie(w, r)
}

func (h restAPIHost) ClientIP(r *http.Request) string { return h.a.clientIPWithLiveFallback(r) }

func (h restAPIHost) NewRateLimiter(limit int, window time.Duration) RateLimiter {
	return apiRateLimiterHandle{newAPIRateLimiter(limit, window)}
}

func (h restAPIHost) NewReadSizedRateLimiter(limit int, window time.Duration) RateLimiter {
	return apiRateLimiterHandle{newReadSizedAPIRateLimiter(limit, window)}
}

func (h restAPIHost) DecodeAndValidate(w http.ResponseWriter, r *http.Request, schemaName string, dst any) bool {
	cfg := h.a.agentLoop.GetConfig()
	return decodeAndValidate(w, r, schemaName, dst, cfg.Gateway.ValidateInbound)
}

func (h restAPIHost) WriteError(w http.ResponseWriter, status int, msg string) {
	jsonErr(w, status, msg)
}
func (h restAPIHost) WriteJSON(w http.ResponseWriter, body any) { jsonOK(w, body) }

// apiRateLimiterHandle adapts the engine's private *apiRateLimiter to the
// public RateLimiter interface a provider consumes. Wrap delegates to the
// SAME withRateLimit every other rate-limited route in this package uses, so
// a provider route's 429 body, Retry-After header and XFF-spoofing
// resistance are pixel-for-pixel what a route wrapped by auth_mode.go's own
// authRoute.limiter field gets.
type apiRateLimiterHandle struct{ l *apiRateLimiter }

func (h apiRateLimiterHandle) Wrap(next http.HandlerFunc) http.HandlerFunc {
	return withRateLimit(h.l, next)
}
func (h apiRateLimiterHandle) Limit() int            { return h.l.limit }
func (h apiRateLimiterHandle) Window() time.Duration { return h.l.window }
