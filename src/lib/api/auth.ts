// auth.ts: Login, logout, onboarding, token validation, password and re-auth

import type { ZodType } from 'zod'
import {
  LoginResponse as LoginResponseSchema,
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
  ProbeProviderRequest,
  ProbeProviderResponse,
  OnboardingCompleteRequest,
  AppState,
  ValidateTokenResponse,
  OperationResult,
  // Spec-6 U5 — re-auth + Integrations + transcribe (contract-first #8):
  ReAuthResponse,
} from '@/lib/api/generated/openapi-types'
import { request } from './http'

export function rotateGatewayToken(): Promise<{ token: string }> {
  return request('/config/gateway/rotate-token', { method: 'POST' }, RotateTokenResponseSchema as ZodType<{ token: string }>)
}

// ── App State ─────────────────────────────────────────────────────────────────

// AppState — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/AppState.yaml.

export function fetchAppState(): Promise<AppState> {
  return request<AppState>('/state', undefined, AppStateSchema)
}

export function completeOnboarding(): Promise<void> {
  return request('/state', {
    method: 'PATCH',
    body: JSON.stringify({ onboarding_complete: true }),
  })
}

// ── Auth / Login ─────────────────────────────────────────────────────────────────
//
// LoginResponse is re-exported from @/lib/api/generated/openapi-types at the
// top of this file. The hand-written interface has been removed.

export async function login(username: string, password: string): Promise<LoginResponse> {
  return request<LoginResponse>('/auth/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  }, LoginResponseSchema)
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

export async function completeOnboardingTransaction(req: OnboardingCompleteRequest): Promise<LoginResponse> {
  return request<LoginResponse>('/onboarding/complete', {
    method: 'POST',
    body: JSON.stringify(req),
  }, LoginResponseSchema)
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

// ── Auth ──────────────────────────────────────────────────────────────────────

export function changePassword(currentPassword: string, newPassword: string): Promise<OperationResult> {
  return request<OperationResult>('/auth/change-password', {
    method: 'POST',
    body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
  }, OperationResultSchema as ZodType<OperationResult>)
}

// ── Re-auth consent primitive (Spec-6 FR-12.2) ─────────────────────────────────
//
// reAuth re-verifies the single user's one password before a sensitive settings
// change. On success it returns a short-lived, single-use consent token the
// caller replays in the X-Reauth-Token header on the very next sensitive request
// (e.g. configureIntegrationProvider). This is the NEW consent primitive — it is
// NOT RequireNotBypass (a 503 dev-mode guard, unrelated). A wrong password
// rejects with a 401 ApiError.
export const REAUTH_HEADER = 'X-Reauth-Token'

export function reAuth(password: string): Promise<ReAuthResponse> {
  return request<ReAuthResponse>('/auth/reauth', {
    method: 'POST',
    body: JSON.stringify({ password }),
  }, ReAuthResponseSchema as ZodType<ReAuthResponse>)
}
