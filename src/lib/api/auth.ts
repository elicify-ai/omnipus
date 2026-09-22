// auth.ts: local-mode login, omnipus.ai sign-in, logout, onboarding, token
// validation, password change and re-auth.
//
// ADR-0010 WP2 — auth mode by registration-time composition: this file
// carries the client functions for BOTH auth modes. Exactly one is ever
// reachable on a given binary — the engine registers only the active mode's
// routes (auth_mode.go) — but the SPA is one build that ships both editions'
// screens (ADR-0010 decision 3: "the open-source build has no platform
// package"; conversely a hosted/desktop build ships local mode's login
// screen too, dormant). login/changePassword/reAuth/REAUTH_HEADER are local
// mode's (core edition) own client functions, restored from the engine merge
// base unchanged; startPlatformAuth/fetchAuthSession/claimPlatformAuth are
// platform mode's (desktop, hosted).

import type { ZodType } from 'zod'
import {
  LoginResponse as LoginResponseSchema,
  OnboardingCompleteResponse as OnboardingCompleteResponseSchema,
  PlatformAuthStartResponse as PlatformAuthStartResponseSchema,
  AuthSessionResponse as AuthSessionResponseSchema,
  ProbeProviderResponse as ProbeProviderResponseSchema,
  // New generated Zod schemas (contract-first #8):
  AppState as AppStateSchema,
  ValidateTokenResponse as ValidateTokenResponseSchema,
  OperationResult as OperationResultSchema,
  RotateTokenResponse as RotateTokenResponseSchema,
  // Spec-6 U5 — re-auth + Integrations + transcribe (contract-first #8):
  ReAuthResponse as ReAuthResponseSchema,
} from '@/lib/api/generated/schemas'
import type {
  LoginResponse,
  PlatformAuthStartRequest,
  PlatformAuthStartResponse,
  AuthSessionResponse,
  ProbeProviderRequest,
  ProbeProviderResponse,
  OnboardingCompleteRequest,
  OnboardingCompleteResponse,
  AppState,
  ValidateTokenResponse,
  OperationResult,
  // Spec-6 U5 — re-auth + Integrations + transcribe (contract-first #8):
  ReAuthResponse,
} from '@/lib/api/generated/openapi-types'
import { rememberIdentityMode } from './identityMode'
import { request } from './http'

export function rotateGatewayToken(): Promise<{ token: string }> {
  return request('/config/gateway/rotate-token', { method: 'POST' }, RotateTokenResponseSchema as ZodType<{ token: string }>)
}

// ── App State ─────────────────────────────────────────────────────────────────

// AppState — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/AppState.yaml.

export async function fetchAppState(): Promise<AppState> {
  const state = await request<AppState>('/state', undefined, AppStateSchema)
  // The client-side CSRF exemptions for the local-mode onboarding routes
  // (http.ts MODE_GATED) key off this; every boot path calls fetchAppState.
  rememberIdentityMode(state.identity?.mode)
  return state
}

export function completeOnboarding(): Promise<void> {
  return request('/state', {
    method: 'PATCH',
    body: JSON.stringify({ onboarding_complete: true }),
  })
}

// ── Auth / local-mode login (core edition) ───────────────────────────────────
//
// LoginResponse is re-exported from @/lib/api/generated/openapi-types. Only
// reachable when the engine is built and registered in local auth mode
// (config.EditionAuthMode() == AuthModeLocal, i.e. the core edition) —
// /api/v1/auth/login is not registered at all in platform mode, so calling
// this against a desktop/hosted instance 404s.

export async function login(username: string, password: string): Promise<LoginResponse> {
  return request<LoginResponse>('/auth/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  }, LoginResponseSchema)
}

// ── Auth / omnipus.ai sign-in (desktop, hosted) ──────────────────────────────
//
// Platform mode has no local credential — this application never sees an
// account password, because the password is typed on the platform's own page
// in the user's real browser. Only reachable when the engine is built and
// registered in platform auth mode; /api/v1/auth/platform/start is not
// registered at all in local mode, so calling this against a core-edition
// instance 404s.
//
// startPlatformAuth asks the gateway to begin a sign-in and returns where to
// send the browser. The PKCE verifier behind that URL stays in the gateway;
// the caller gets only the URL and an opaque state.

export async function startPlatformAuth(
  method: PlatformAuthStartRequest['method'],
): Promise<PlatformAuthStartResponse> {
  return request<PlatformAuthStartResponse>('/auth/platform/start', {
    method: 'POST',
    body: JSON.stringify({ method }),
  }, PlatformAuthStartResponseSchema)
}

/**
 * fetchAuthSession reports who this browser is signed in as, or throws a 401
 * ApiError when it is signed in as nobody. It has no side effects, which is
 * what makes it safe to poll once a second while the user finishes signing in
 * in their browser.
 */
export async function fetchAuthSession(): Promise<AuthSessionResponse> {
  return request<AuthSessionResponse>('/auth/session', {}, AuthSessionResponseSchema)
}

/**
 * claimPlatformAuth collects the session that the browser half of a sign-in
 * just opened, and is the ONLY way the desktop app can end up signed in.
 *
 * On desktop the sign-in page opens in the user's real browser, so the cookie
 * /auth/callback sets lands in THAT browser's jar — this application's jar
 * never receives it. Presenting the state returned by startPlatformAuth issues
 * the same session as cookies on this response, which makes this client the
 * signed-in one.
 *
 * In an ordinary browser the cookie is already here, fetchAuthSession answers
 * first, and this is never called. Callers should therefore try the session
 * poll first and treat a 404 from here as the ordinary "nothing waiting yet".
 *
 * The state is a credential for the sixty seconds after the callback succeeds:
 * keep it in memory, never in storage, and never in a log.
 */
export async function claimPlatformAuth(state: string): Promise<AuthSessionResponse> {
  return request<AuthSessionResponse>(
    '/auth/platform/claim',
    { method: 'POST', body: JSON.stringify({ state }) },
    AuthSessionResponseSchema,
  )
}

/**
 * logout revokes the current session server-side (FR-020): clears
 * token_hash/session_token_hash in config.json and expires both the
 * omnipus-session and __Host-csrf cookies via Set-Cookie. Returns 204 (no
 * body) on success. Callers (Sidebar "Sign out") MUST call this BEFORE
 * clearing local UI state and navigating to /login — client-side clearing
 * alone is not a complete logout (a replayed cookie would still authenticate).
 * Contrast with forceLogout() (src/lib/authLogout.ts), which is the
 * server-already-rejected path (401/WS 1008) and has no cookie left worth
 * revoking.
 */
export async function logout(): Promise<void> {
  return request<void>('/auth/logout', { method: 'POST' })
}

// completeOnboardingTransaction finalises first-run setup. WP5 (ADR-0010)
// composes this by auth mode: in platform mode (hosted/desktop) the account
// is ALREADY SIGNED IN (ADR-0008 ruling 2) — the request carries no `admin`
// block and the response carries no bearer token, because the session that
// authorised this call is the session that continues afterwards. In local
// mode (the open-source edition) nothing has signed in yet — `req.admin`
// (upstream's `{username, password}`) is required, and the response carries
// `token`, the one-shot bearer token this call mints alongside the session
// cookie. `req.preferences` is optional in both modes. Which shape a given
// build sends is decided by the caller (src/routes/onboarding.tsx), reading
// the app state's identity/edition — never by this function.
export async function completeOnboardingTransaction(
  req: OnboardingCompleteRequest,
): Promise<OnboardingCompleteResponse> {
  return request<OnboardingCompleteResponse>('/onboarding/complete', {
    method: 'POST',
    body: JSON.stringify(req),
  }, OnboardingCompleteResponseSchema)
}

// probeProvider is a non-persistent "test + fetch model list" call used during
// onboarding, before the __Host-csrf cookie can be issued. It accepts the
// api_key in the request body, asks the server to hit the provider's /models
// endpoint with that key, and returns both a success flag and the model list.
// Nothing is written to disk or in-memory config.
//
// After onboarding completes, the server returns HTTP 409 from this endpoint.
// Admins who want to add providers post-onboarding use configureProvider
// (PUT /providers/{id}) + fetchProviders (GET /providers) — both work
// because the browser has the __Host-csrf cookie at that point.
//
// ProbeProviderResponse is re-exported from @/lib/api/generated/openapi-types.
export async function probeProvider(req: ProbeProviderRequest): Promise<ProbeProviderResponse> {
  // ADR-067 FR-023 / ADR-068 FR-036: ONE ProbeProviderRequest shape
  // {id, auth, api_key?, model?, api_base?, protocol?} — the generated type is
  // the only shape this wrapper accepts, so the sign-in path (auth: 'sign_in',
  // no api_key) and the chosen-model path (model, echoed back as probed_model)
  // are expressible without a second function or a parallel struct.
  return request<ProbeProviderResponse>('/onboarding/probe-provider', {
    method: 'POST',
    body: JSON.stringify(req),
  }, ProbeProviderResponseSchema)
}

// ValidateTokenResponse — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/ValidateTokenResponse.yaml.

export async function validateToken(): Promise<ValidateTokenResponse> {
  return request<ValidateTokenResponse>('/auth/validate', undefined, ValidateTokenResponseSchema)
}

// ── Auth / local-mode password change (core edition) ─────────────────────────
//
// Only reachable in local auth mode; /api/v1/auth/change-password is not
// registered at all in platform mode.

export function changePassword(currentPassword: string, newPassword: string): Promise<OperationResult> {
  return request<OperationResult>('/auth/change-password', {
    method: 'POST',
    body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
  }, OperationResultSchema as ZodType<OperationResult>)
}

// ── Re-auth consent primitive (Spec-6 FR-12.2, local mode / core edition) ────
//
// reAuth re-verifies the single user's one password before a sensitive settings
// change. On success it returns a short-lived, single-use consent token the
// caller replays in the X-Reauth-Token header on the very next sensitive request
// (e.g. configureIntegrationProvider). This is the NEW consent primitive — it is
// NOT RequireNotBypass (a 503 dev-mode guard, unrelated). A wrong password
// rejects with a 401 ApiError.
//
// Only reachable in local auth mode; /api/v1/auth/reauth is not registered at
// all in platform mode, where there is no local password to re-type and the
// signed-in session itself is the guard (requireReAuth is a no-op there —
// pkg/gateway/rest_integrations_auth.go).
export const REAUTH_HEADER = 'X-Reauth-Token'

export function reAuth(password: string): Promise<ReAuthResponse> {
  return request<ReAuthResponse>('/auth/reauth', {
    method: 'POST',
    body: JSON.stringify({ password }),
  }, ReAuthResponseSchema as ZodType<ReAuthResponse>)
}

