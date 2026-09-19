// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// ---------------------------------------------------------------------------
// Auth mode: registration-time composition (ADR-0010 WP2)
//
// omnipus.ai's product sign-in and upstream's own local password login used
// to be an either/or DELETION: ADR-0008 removed HandleLogin, HandleChangePassword,
// the re-auth consent primitive, their contracts and their CSRF exemptions
// outright, and rest.go hard-registered the platform routes in their place.
// That makes every future upstream merge re-delete the same files by hand.
//
// This file is the seam instead: a mode is a small, static description of
// what registerSettingsAndAccountRoutes should wire up — never a runtime `if`
// inside a shared handler (workplan review finding 3: the divergence is a
// route table, a set of per-route wrappers, a CSRF-exempt set and a CORS
// header list, not one branch). rest.go asks activeAuthMode() which routes to
// register and how to wrap them; middleware/csrf.go asks
// config.EditionAuthMode() (independently — importing pkg/gateway from
// pkg/gateway/middleware would cycle) for its own default exempt set, kept in
// lockstep with the lists below by auth_mode_test.go.
//
// The two modes:
//   - localAuthMode: upstream's own password login, restored byte-for-byte
//     from the engine merge base (engine/PIN's ENGINE_COMMIT) — HandleLogin,
//     HandleChangePassword and the re-auth consent primitive (rest_auth.go,
//     rest_integrations_auth.go) are upstream's code, unmodified, compiled
//     into this mode's route table only.
//   - platformAuthMode: the omnipus.ai sign-in (desktop, hosted). Its
//     handlers used to live here too (rest_platform_auth.go,
//     platform_discovery.go); WP2 phase 2 moved them out to
//     editions/platform behind the SignInProvider/Host seam
//     (signin_provider.go) — this function now asks the registered provider
//     for its routes instead of hard-registering them.
//
// activeAuthMode derives the mode from config.EditionAuthMode(), never from a
// config key (ADR-0010 decision 3): a hosted or desktop binary cannot be
// talked into local mode at runtime, because there is no switch to flip. An
// unknown edition yields the empty authMode — zero auth routes and zero
// onboarding routes get registered, so sign-in and onboarding answer 404
// rather than guessing which mode a misbuilt binary meant to be. (WP1's
// EditionMisbuild already refuses such a binary at boot; this is
// defense-in-depth at the route layer, not the primary guard.)
// ---------------------------------------------------------------------------

// authRouteWrap names how a mode's registered route is wrapped at
// registration time — decided here, never inside a shared handler.
type authRouteWrap int

const (
	// authWrapBare registers the handler with no auth wrapper at all. Used
	// for the platform sign-in callback: a plain browser navigation with no
	// session yet to check, exactly like /metrics and /browser-start.
	authWrapBare authRouteWrap = iota
	// authWrapOptionalAuth is withOptionalAuth: an unauthenticated caller
	// passes through with no user in the request context.
	authWrapOptionalAuth
	// authWrapAuth is withAuth: an unauthenticated caller is refused 401.
	authWrapAuth
)

// authRoute is one route a mode contributes to registration: the exact path,
// the handler, which wrapper kind applies, and an optional rate limiter (nil
// = unlimited). wrapRoute turns this into the http.Handler rest.go registers.
type authRoute struct {
	path    string
	handler http.HandlerFunc
	wrap    authRouteWrap
	limiter *apiRateLimiter
}

// authMode is what a mode contributes at registration time: the sign-in
// routes to register, whether the FR-050 onboarding window runs before or
// after authentication, the CSRF-exempt paths, and the extra CORS
// allow-headers its routes need beyond the shared baseline (Authorization,
// Content-Type, X-Csrf-Token).
//
// The zero value is the "unknown edition" mode: no name, no routes, no
// exemptions, no headers — activeAuthMode returns it when
// config.EditionAuthMode() cannot derive a mode, and every caller below
// treats an empty authRoutes/name as "register nothing".
type authMode struct {
	// name identifies the mode for logging/diagnostics only; nothing
	// compares it to decide behavior.
	name string
	// authRoutes are this mode's sign-in-specific routes (login/
	// change-password/reauth for local; start/session/claim/callback for
	// platform). /api/v1/auth/validate and /api/v1/auth/logout are common to
	// both modes and are registered outside this table.
	authRoutes []authRoute
	// onboardingWrap is the FR-050 property: local mode runs onboarding
	// BEFORE any session exists (authWrapOptionalAuth, upstream's own
	// posture); platform mode runs it AFTER sign-in (authWrapAuth). The zero
	// value (authWrapBare) on the empty mode means registerSettingsAndAccountRoutes
	// must not register the onboarding routes at all — see known below.
	onboardingWrap authRouteWrap
	// csrfExemptPaths is this mode's contribution to the CSRF middleware's
	// default exempt set. middleware/csrf.go's defaultExemptPaths mirrors
	// this list from config.EditionAuthMode() directly (it cannot import this
	// package); auth_mode_test.go pins the two in lockstep.
	csrfExemptPaths []string
	// corsExtraHeaders are additional Access-Control-Allow-Headers values
	// this mode's routes need beyond the baseline. Local mode adds
	// X-Reauth-Token for the re-auth consent primitive; platform mode adds
	// nothing.
	corsExtraHeaders []string
}

// known reports whether this is a real mode (local or platform) rather than
// the empty "unknown edition" value. Callers use this to decide whether the
// onboarding routes may be registered at all — the zero-value onboardingWrap
// (authWrapBare) would otherwise register onboarding with NO auth check,
// which is the one shape known's guard exists to prevent.
func (m authMode) known() bool {
	return m.name != ""
}

// localAuthMode is upstream's own sign-in, restored exactly as the engine
// merge base (engine/PIN's ENGINE_COMMIT) had it: username/password login,
// self-service change-password, and the re-auth consent primitive gating
// sensitive settings changes. This is upstream's behavior, unchanged — only
// its registration moved behind the seam.
func localAuthMode(a *restAPI) authMode {
	return authMode{
		name: "local",
		authRoutes: []authRoute{
			{path: "/api/v1/auth/login", handler: a.HandleLogin, wrap: authWrapOptionalAuth},
			{path: "/api/v1/auth/change-password", handler: a.HandleChangePassword, wrap: authWrapAuth},
			// Password re-auth consent primitive (Spec-6 FR-12.2). Distinct
			// from RequireNotBypass (a 503 dev-mode guard) — this re-verifies
			// the user's one password before a sensitive settings change.
			{path: "/api/v1/auth/reauth", handler: a.HandleReAuth, wrap: authWrapAuth, limiter: reauthLimiter},
		},
		onboardingWrap: authWrapOptionalAuth,
		csrfExemptPaths: []string{
			"/api/v1/onboarding/complete",
			"/api/v1/onboarding/probe-provider",
			"/api/v1/auth/login",
		},
		corsExtraHeaders: []string{"X-Reauth-Token"},
	}
}

// platformAuthMode is the omnipus.ai sign-in (desktop, hosted): delegated to
// a registered SignInProvider (signin_provider.go, ADR-0010 WP2 phase 2) — no
// local credential exists. rest_platform_auth.go and platform_discovery.go
// used to be hard-registered here directly; they now live in
// editions/platform, and this function only asks the registered provider for
// its routes.
//
// No provider registered (the open-source engine's own cmd/omnipus, or a
// hosted/desktop build whose registration was somehow skipped) is NOT the
// same as "no routes at all": PlatformAuthStartPath is always registered, so
// a signed-out SPA gets a stable 503 rather than a 404 it cannot tell apart
// from "this build has no sign-in yet" (WP2 deliverable 2) — everything else
// (session, claim, callback) genuinely does not exist, fail closed rather
// than guessing.
func platformAuthMode(a *restAPI) authMode {
	p := signInProvider
	if p == nil {
		return authMode{
			name: "platform",
			authRoutes: []authRoute{
				{path: PlatformAuthStartPath, handler: platformSignInNotConfigured, wrap: authWrapOptionalAuth},
			},
			onboardingWrap:  authWrapAuth,
			csrfExemptPaths: []string{PlatformAuthStartPath},
		}
	}

	h := a.signInHost()
	provRoutes := p.Routes(h)
	routes := make([]authRoute, 0, len(provRoutes))
	csrfExempt := make([]string, 0, 1)
	for _, rt := range provRoutes {
		wrap := authWrapOptionalAuth
		if rt.Wrap == ProviderRouteBare {
			wrap = authWrapBare
		}
		// No `limiter` here: a provider route arrives already wrapped with
		// its OWN rate limiter (Host.NewRateLimiter, applied inside
		// ProviderRoute.Handler) — "rate-limited on the provider's own
		// limiters" (WP2 deliverable 1), not on a limiter this seam owns.
		routes = append(routes, authRoute{path: rt.Path, handler: rt.Handler, wrap: wrap})
		if rt.CSRFExempt {
			csrfExempt = append(csrfExempt, rt.Path)
		}
	}
	return authMode{
		name:            "platform",
		authRoutes:      routes,
		onboardingWrap:  authWrapAuth,
		csrfExemptPaths: csrfExempt,
	}
}

// activeAuthMode picks the mode config.EditionAuthMode() derives from the
// stamped edition — never a config key (ADR-0010 decision 3), so nothing at
// runtime can move a hosted or desktop binary back to local mode. An unknown
// edition (EditionAuthMode returns "") yields the empty authMode: no auth
// routes and no onboarding routes get registered, so a misbuilt binary's
// sign-in and onboarding answer 404 instead of guessing which mode it meant
// to run as.
func activeAuthMode(a *restAPI) authMode {
	switch config.EditionAuthMode() {
	case config.AuthModeLocal:
		return localAuthMode(a)
	case config.AuthModePlatform:
		return platformAuthMode(a)
	default:
		return authMode{}
	}
}

// corsBaselineHeaders are the Access-Control-Allow-Headers values every
// route needs regardless of mode.
var corsBaselineHeaders = []string{"Authorization", "Content-Type", "X-Csrf-Token"}

// corsAllowHeaders returns rest.go's Access-Control-Allow-Headers value: the
// shared baseline plus the active mode's extra headers. Local mode adds
// X-Reauth-Token (the re-auth consent primitive); platform mode adds
// nothing — this is the seam's answer to "the X-Reauth-Token CORS header is
// contributed by local mode only" (WP2 deliverable 2).
func (a *restAPI) corsAllowHeaders() string {
	// The mode is fixed at build time (ADR-0010), so the composition — which
	// asks the registered sign-in provider for its routes — happens once, not
	// on every request from every setCORSHeaders call site.
	a.corsAllowHeadersOnce.Do(func() {
		headers := append([]string{}, corsBaselineHeaders...)
		headers = append(headers, activeAuthMode(a).corsExtraHeaders...)
		a.corsAllowHeadersValue = strings.Join(headers, ", ")
	})
	return a.corsAllowHeadersValue
}

// wrapRoute turns an authRoute into the http.Handler rest.go registers,
// matching exactly how every route below was already composed by hand: a
// rate limiter, when present, sits BETWEEN the auth wrapper and the handler
// (withAuth(withRateLimit(limiter, handler))) — never the other order, or an
// unauthenticated caller could burn the shared limiter's budget before the
// auth check even runs. A bare route gets no wrapper of any kind.
func (a *restAPI) wrapRoute(rt authRoute) http.Handler {
	h := rt.handler
	if rt.limiter != nil {
		h = withRateLimit(rt.limiter, h)
	}
	switch rt.wrap {
	case authWrapAuth:
		return a.withAuth(h)
	case authWrapOptionalAuth:
		return a.withOptionalAuth(h)
	default:
		return h
	}
}
