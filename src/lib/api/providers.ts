// providers.ts: LLM providers, the catalog, default model, sign-in and integrations

import { ApiError } from '../api-error'
import { maybeDevToast } from '../dev-toast'
import type { ZodType } from 'zod'
import { z } from 'zod'
import {
  // Newly wired schemas:
  Provider as ProviderSchema,
  // ADR-068 FR-010/FR-018 (contract-first #8):
  ProviderDeleteResponse as ProviderDeleteResponseSchema,
  DefaultModel as DefaultModelSchema,
  ProvidersCatalog as ProvidersCatalogSchema,
  CliDetect as CliDetectSchema,
  // ADR-068 §8b sign-in wire shapes (T068-33/T068-34):
  SignInStartResponse as SignInStartResponseSchema,
  SignInStatus as SignInStatusSchema,
  SignInPollResponse as SignInPollResponseSchema,
  // ADR-068 FR-031/T068-27: "Check with my account".
  EntitlementResponse as EntitlementResponseSchema,
  // external-executor-cli-path-detection spec (ADR-030): create-time validate.
  CliValidateResponse as CliValidateResponseSchema,
  // Real, live command-line preview for a subagent_3p executor's current
  // settings (replaces the static AutoAppliedFlags description with the
  // ACTUAL computed argv/command_line for what the operator has typed).
  // Only the RESPONSE schema is needed — fetchExecutorPreview's request body
  // is a plain outbound POST, validated server-side, not SPA-edge-validated
  // (same as fetchCliValidate's CliValidateRequest above).
  ExecutorCommandPreviewResponse as ExecutorCommandPreviewResponseSchema,
  // Real, imperative "send a test message" run for a subagent_3p executor
  // (POST /agents/executor-smoke-test) — same "response schema only" rule as
  // ExecutorCommandPreviewResponse above; the request body is validated
  // server-side.
  ExecutorSmokeTestResponse as ExecutorSmokeTestResponseSchema,
  OperationResult as OperationResultSchema,
  IntegrationProvidersResponse as IntegrationProvidersResponseSchema,
} from '@/lib/api/generated/schemas'
import type {
  Provider,
  ProvidersCatalog,
  ProviderUpdateRequest,
  // ADR-068 FR-010/FR-012/FR-018 — provider removal + the default-model pair:
  ProviderDeleteRequest,
  ProviderDeleteResponse,
  DefaultModel,
  DefaultModelUpdateRequest,
  CliDetect,
  CliValidateRequest,
  CliValidateResponse,
  // ADR-068 section 8b sign-in wire shapes (T068-33/T068-34):
  SignInStartResponse,
  SignInStatus,
  SignInPollRequest,
  SignInPollResponse,
  // ADR-068 FR-031/T068-27: "Check with my account".
  EntitlementResponse,
  OperationResult,
  IntegrationProvidersResponse,
  IntegrationProviderUpdateRequest,
  // Real, live command-line preview for a subagent_3p executor (contract-first #8):
  ExecutorCommandPreviewRequest,
  ExecutorCommandPreviewResponse,
  // Real, imperative "send a test message" run for a subagent_3p executor
  // (contract-first #8):
  ExecutorSmokeTestRequest,
  ExecutorSmokeTestResponse,
} from '@/lib/api/generated/openapi-types'
import { REAUTH_HEADER } from './auth'
import { ApiSchemaError, BASE_URL, _recordApiSchemaError, buildHeaders, request } from './http'

// ── Providers ─────────────────────────────────────────────────────────────────

// Provider — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/Provider.yaml.

export function fetchProviders(): Promise<Provider[]> {
  return request<Provider[]>('/providers', undefined, z.array(ProviderSchema) as ZodType<Provider[]>)
}

// ── Providers catalog (ETag re-validated) ────────────────────────────────────
//
// The registry-fed catalog the gateway itself uses (ADR-067 FR-017, ADR-068
// FR-037) — the schema-2.0.0 document with nested models plus the serving
// envelope (served_from / stale). This is the SPA's ONLY catalog source: the
// bundled TS catalog emission under src/lib/generated/ was deleted (T068-05,
// SC-010) and must never return.
//
// Cadence is ADR-067 A-1: re-validate on Settings open and every 15 minutes
// (the schedule lives in providersCatalogQuery.ts). The assertion FR-037 makes
// is "at most one 200 per ETag value" — 304s are expected requests, a second
// 200 for an unchanged document is not. That is what the module-level cache
// below buys: the strong ETag the gateway sends is replayed as If-None-Match,
// and a 304 resolves with the SAME document object we already parsed, so the
// body is downloaded and zod-validated exactly once per catalog version.
//
// The cache is module-level rather than TanStack-Query-level on purpose: the
// query cache is evicted on gcTime and cleared on logout, and each of those
// would otherwise cost a fresh 200 for a document the client already holds.
let providersCatalogETag: string | null = null

let providersCatalogDocument: ProvidersCatalog | null = null

// resetProvidersCatalogCache drops the memoised document + ETag. Used by tests
// and by any caller that must force a cold 200 (e.g. after a sign-out clears
// the session the catalog was fetched under).
export function resetProvidersCatalogCache(): void {
  providersCatalogETag = null
  providersCatalogDocument = null
}

// GET /api/v1/providers/catalog → ProvidersCatalog (contract type).
//
// Rejects with ApiError on any non-2xx (the picker renders "Catalog
// unavailable" with a Retry from this rejection — ADR-068 BDD "Catalog
// unavailable in the picker") and with ApiSchemaError when the body does not
// match the generated schema. A failed re-validation never poisons the cached
// document: the previously served catalog stays available for the next call.
export async function fetchProvidersCatalog(): Promise<ProvidersCatalog> {
  return fetchProvidersCatalogOnce(true)
}

async function fetchProvidersCatalogOnce(mayRetryWithoutETag: boolean): Promise<ProvidersCatalog> {
  const path = '/providers/catalog'
  const conditional = providersCatalogETag !== null && providersCatalogDocument !== null
  let res: Response
  try {
    res = await fetch(`${BASE_URL}/api/v1${path}`, {
      credentials: 'include',
      headers: buildHeaders(conditional ? { 'If-None-Match': providersCatalogETag as string } : undefined),
    })
  } catch (cause) {
    throw new ApiError(0, 'Network unavailable. Check your connection.', { cause })
  }

  if (res.status === 304) {
    if (providersCatalogDocument !== null) return providersCatalogDocument
    // A 304 with nothing cached means our ETag outlived the document (only
    // reachable if the cache was reset mid-flight). Retry unconditionally
    // once so the caller still gets a catalog rather than an error. The
    // `mayRetryWithoutETag` flag makes the recursion provably single-shot.
    resetProvidersCatalogCache()
    if (mayRetryWithoutETag) return fetchProvidersCatalogOnce(false)
    throw new ApiError(304, 'Providers catalog returned 304 with no cached document.')
  }

  if (!res.ok) throw await ApiError.fromResponse(res)

  let body: unknown
  try {
    body = (await res.json()) as unknown
  } catch {
    _recordApiSchemaError(`GET /api/v1${path}`, 1)
    const schemaErr = new ApiSchemaError(
      `GET /api/v1${path}`,
      [{ path: [], message: 'Response is not valid JSON' }],
      undefined,
    )
    void maybeDevToast(`[api] Non-JSON response: ${path}`, `GET:${path}:non-json`)
    throw schemaErr
  }

  const parsed = (ProvidersCatalogSchema as ZodType<ProvidersCatalog>).safeParse(body)
  if (!parsed.success) {
    _recordApiSchemaError(`GET /api/v1${path}`, parsed.error.issues.length)
    const schemaErr = new ApiSchemaError(
      `GET /api/v1${path}`,
      parsed.error.issues.map((i) => ({ path: i.path as (string | number)[], message: i.message })),
      body,
    )
    void maybeDevToast(`[api] Schema mismatch: ${path} — ${schemaErr.zodIssues[0]?.message ?? 'unknown'}`, `GET:${path}:schema`)
    throw schemaErr
  }

  // Only a validated document may claim an ETag — otherwise a malformed 200
  // would install an ETag whose 304s resolve with the previous catalog.
  providersCatalogDocument = parsed.data
  providersCatalogETag = res.headers.get('ETag')
  return parsed.data
}

// configureProvider sets a model/provider's API key, endpoint, and/or model.
// Post-onboarding this PUT is re-auth gated (Spec-6 FR-12.2 / FR-6.6): the server
// rejects it with 403 unless a single-use consent token (from reAuth) is replayed
// in the X-Reauth-Token header. The token is OPTIONAL here because the same route
// is used during onboarding, where no authenticated user exists yet and the gate
// is skipped (see pkg/gateway/rest_providers.go's providerPutAdmit).
export function configureProvider(
  id: string,
  apiKey?: string,
  endpoint?: string,
  model?: string,
  reAuthToken?: string,
  models?: string[],
  // ADR-068 FR-037: an operator-named custom endpoint carries its own base URL
  // and wire protocol — both are contract fields on ProviderUpdateRequest, and
  // the server requires the pair to admit an id that is not in the catalog.
  custom?: Pick<ProviderUpdateRequest, 'api_base' | 'protocol'>,
): Promise<Provider> {
  // ProviderUpdateRequest (contract): api_key/model are strings, models is the
  // operator-supplied slug catalogue for endpoint-less providers. `endpoint` is
  // not a contract field — it is merged loosely only when a caller supplies one
  // (back-compat; no current caller does).
  const body: ProviderUpdateRequest & { endpoint?: string } = {}
  if (apiKey !== undefined) body.api_key = apiKey
  if (endpoint !== undefined) body.endpoint = endpoint
  if (model !== undefined) body.model = model
  if (models !== undefined) body.models = models
  if (custom?.api_base !== undefined) body.api_base = custom.api_base
  if (custom?.protocol !== undefined) body.protocol = custom.protocol
  return request<Provider>(`/providers/${id}`, {
    method: 'PUT',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
    body: JSON.stringify(body),
  }, ProviderSchema as ZodType<Provider>)
}

// ── Provider removal + the global default model (ADR-068 US-3 / US-4) ────────
//
// deleteProvider removes the configured row AND its stored key. There is no
// Undo and no dry run (FR-017): the secret is gone the moment the server
// answers 200, so nothing here retains it and no caller is offered a restore.
//
// `newDefault` is required by the server (409 otherwise) when the provider
// backs the default model — the dialog collects it inline. The RESPONSE is
// authoritative for the post-removal state: the server recomputes dependents
// and backs_default under the config lock, so a dependent that appeared while
// the dialog was open still comes back here (FR-012).
export function deleteProvider(
  id: string,
  newDefault?: DefaultModelUpdateRequest,
): Promise<ProviderDeleteResponse> {
  const body: ProviderDeleteRequest | undefined = newDefault ? { new_default: newDefault } : undefined
  return request<ProviderDeleteResponse>(
    `/providers/${id}`,
    { method: 'DELETE', ...(body ? { body: JSON.stringify(body) } : {}) },
    ProviderDeleteResponseSchema as ZodType<ProviderDeleteResponse>,
  )
}

// getDefaultModel reads agents.defaults.default_model as a (provider, model)
// pair with ADR-066's resolved window and its source. A fresh install has no
// default: the GET answers 404 and this rejects with an ApiError the caller
// renders as "not set" — never as a failure toast.
export function getDefaultModel(): Promise<DefaultModel> {
  return request<DefaultModel>('/providers/default-model', undefined, DefaultModelSchema as ZodType<DefaultModel>)
}

// putDefaultModel writes the pair. Takes effect on the next turn, with no
// gateway restart (FR-018).
export function putDefaultModel(pair: DefaultModelUpdateRequest): Promise<DefaultModel> {
  return request<DefaultModel>(
    '/providers/default-model',
    { method: 'PUT', body: JSON.stringify(pair) },
    DefaultModelSchema as ZodType<DefaultModel>,
  )
}

export function testProvider(id: string): Promise<OperationResult> {
  return request<OperationResult>(`/providers/${id}/test`, { method: 'POST' }, OperationResultSchema as ZodType<OperationResult>)
}

// checkEntitlement — "Check with my account" (ADR-068 FR-031, T068-27): one
// live listing call made with this provider's own stored key, intersected
// with the served catalog. 409 for protocol "cli" and custom rows (nothing to
// list with); 422 when no key resolves; 502 `{"error":"could not fetch
// upstream model list: status <n>"}` on an upstream non-2xx with nothing
// cached — surfaced by the caller as an inline warning, never a client retry.
export function checkEntitlement(id: string): Promise<EntitlementResponse> {
  return request<EntitlementResponse>(
    `/providers/${id}/entitlement`,
    { method: 'POST' },
    EntitlementResponseSchema as ZodType<EntitlementResponse>,
  )
}

// ── Provider sign-in (device code / CLI login, ADR-068 §8b, T068-33) ────────
//
// SignInDialog (src/components/providers/SignInDialog.tsx) is the sole
// caller. All five endpoints are `adminWrap` (401 when unauthenticated) with
// ONE documented exception (FR-050): while onboarding is incomplete they are
// reachable without a session, which is what lets onboarding step 3 run a
// real sign-in before any admin account exists to authenticate as. Once
// onboarding completes they revert to the normal 401/503 posture.

// startSignIn begins a vendor sign-in for a provider whose catalog row
// declares `sign_in` (ADR-068 FR-008). Returns the `cli_login` instruction
// (codex-cli / github-copilot — run the vendor CLI's own login command) or a
// `device_code` session (openai-chatgpt, and xai once configured —
// verification link + user code to poll, FR-044).
export function startSignIn(id: string): Promise<SignInStartResponse> {
  return request<SignInStartResponse>(
    `/providers/${id}/sign-in`,
    { method: 'POST' },
    SignInStartResponseSchema as ZodType<SignInStartResponse>,
  )
}

// fetchSignInStatus reads a provider's current vendor sign-in state without
// side effects — no vendor poll, no file write (FR-007/FR-009). Used for the
// cli_login "Check sign-in" button.
export function fetchSignInStatus(id: string): Promise<SignInStatus> {
  return request<SignInStatus>(
    `/providers/${id}/sign-in/status`,
    undefined,
    SignInStatusSchema as ZodType<SignInStatus>,
  )
}

// pollSignIn performs at most one vendor poll for an open device-code
// session. The caller MUST respect the LATEST `interval_seconds` it has seen
// (from startSignIn or a prior poll response) and never poll faster — and
// must back off when a poll response raises it via vendor `slow_down`
// (FR-045).
export function pollSignIn(id: string, deviceAuthId: string): Promise<SignInPollResponse> {
  const body: SignInPollRequest = { device_auth_id: deviceAuthId }
  return request<SignInPollResponse>(
    `/providers/${id}/sign-in/poll`,
    { method: 'POST', body: JSON.stringify(body) },
    SignInPollResponseSchema as ZodType<SignInPollResponse>,
  )
}

// importCodexLogin copies an existing Codex CLI login (~/.codex/auth.json)
// into openai-chatgpt's own encrypted OAuth entry (FR-047) — read-only, no
// refresh token imported (that session ends at the copied token's `exp`).
// 404 when no Codex login exists.
export function importCodexLogin(): Promise<SignInStatus> {
  return request<SignInStatus>(
    '/providers/openai-chatgpt/sign-in/import',
    { method: 'POST' },
    SignInStatusSchema as ZodType<SignInStatus>,
  )
}

// signOutProvider deletes the provider's stored OAuth credential entry
// (device_code providers) and returns the row to not_signed_in (FR-048). A
// missing entry is still success; a no-op success for cli_login providers,
// which hold no Omnipus-side credential to delete.
export function signOutProvider(id: string): Promise<OperationResult> {
  return request<OperationResult>(
    `/providers/${id}/sign-in`,
    { method: 'DELETE' },
    OperationResultSchema as ZodType<OperationResult>,
  )
}

// fetchCliDetect probes the host for installed external CLIs (claude-code /
// codex / opencode), used by the Agents screen to gate the "+ External
// subagent" runtime choices. UAT fix: this MUST go through the authed
// `request()` wrapper so the omnipus-session cookie rides along
// (credentials:'include') — the previous raw
// `fetch('/api/v1/system/cli-detect')` sent no credentials and got a 401,
// surfacing a false "Could not detect installed external CLIs" banner.
export function fetchCliDetect(): Promise<CliDetect> {
  return request<CliDetect>('/system/cli-detect', undefined, CliDetectSchema as ZodType<CliDetect>)
}

// fetchCliValidate performs a stateless, create-time check that a CLI binary
// actually runs at the given path (external-executor-cli-path-detection spec
// FR-006/FR-013/FR-014/FR-015/FR-017/FR-018). It spawns only `<cli> --version`
// server-side (15s timeout, no shell) and returns exactly one classified
// `reason`. Callers MUST gate blocking on `reason` (missing-binary /
// handshake-failed), never on the raw `ok` boolean (FR-018) — `unauthenticated`
// also reports ok=true but is a non-blocking warning. The endpoint is
// `withAuth` (create-parity with `createAgent`), rate-limited, and audited
// server-side — pass an AbortSignal so a debounced validate-on-blur caller can
// cancel a stale in-flight request when the path changes again.
export function fetchCliValidate(
  cli: CliValidateRequest['cli'],
  cliPath: string,
  opts?: { signal?: AbortSignal },
): Promise<CliValidateResponse> {
  const body: CliValidateRequest = { cli, cli_path: cliPath }
  return request<CliValidateResponse>(
    '/system/cli-validate',
    { method: 'POST', body: JSON.stringify(body), signal: opts?.signal },
    CliValidateResponseSchema as ZodType<CliValidateResponse>,
  )
}

// fetchExecutorPreview computes the REAL command line Omnipus would spawn for
// a subagent_3p external-CLI worker with the given settings — argv sourced
// from the same buildArgs() logic each driver (claude/codex/opencode) uses at
// real dispatch time, not a hand-maintained static description. Stateless and
// body-driven (mirrors
// fetchCliValidate) so it works both from the create wizard, where no agent
// id exists yet, and from an existing agent's edit form. Any cli_args token
// the safety filter would strip at real dispatch time is excluded from the
// previewed argv and reported instead in the response's dropped_args, so the
// operator sees before saving that something they typed will be silently
// ignored. Pass an AbortSignal so a debounced live-preview caller can cancel
// a stale in-flight request when a field changes again.
export function fetchExecutorPreview(
  req: ExecutorCommandPreviewRequest,
  opts?: { signal?: AbortSignal },
): Promise<ExecutorCommandPreviewResponse> {
  return request<ExecutorCommandPreviewResponse>(
    '/agents/executor-preview',
    { method: 'POST', body: JSON.stringify(req), signal: opts?.signal },
    ExecutorCommandPreviewResponseSchema as ZodType<ExecutorCommandPreviewResponse>,
  )
}

// fetchExecutorSmokeTest actually RUNS a trivial, real prompt through a
// subagent_3p external-CLI worker's real dispatch path (the same
// driver.Run() a genuine delegation uses) and returns the real response.
// Unlike fetchExecutorPreview above (config-only, argv computation, never
// spawns anything), this spends real model usage and holds a real
// subprocess open for up to ~30s (rest_executor_smoketest.go's bounded
// timeout/turn cap) — an explicit operator action only, never called
// automatically. Stateless and body-driven (mirrors fetchExecutorPreview and
// fetchCliValidate) so it works both from the create wizard, where no agent
// id exists yet, and from an existing agent's edit form. Always resolves
// (never rejects) for a domain-level failure — a failed run comes back as a
// 200 with `ok: false` and `error` set, matching fetchCliValidate's
// convention of using the body for domain-level failure rather than 4xx/5xx;
// this only rejects (throws ApiError) for a genuine transport/auth/rate-limit
// failure. Pass an AbortSignal so the caller can cancel a stale in-flight
// run when a rapid re-click fires a new one, or on unmount.
export function fetchExecutorSmokeTest(
  req: ExecutorSmokeTestRequest,
  opts?: { signal?: AbortSignal },
): Promise<ExecutorSmokeTestResponse> {
  return request<ExecutorSmokeTestResponse>(
    '/agents/executor-smoke-test',
    { method: 'POST', body: JSON.stringify(req), signal: opts?.signal },
    ExecutorSmokeTestResponseSchema as ZodType<ExecutorSmokeTestResponse>,
  )
}

// ── Integrations (Spec-6 FR-12.1) ──────────────────────────────────────────────

export function fetchIntegrationProviders(): Promise<IntegrationProvidersResponse> {
  return request<IntegrationProvidersResponse>(
    '/integrations/providers',
    undefined,
    IntegrationProvidersResponseSchema as ZodType<IntegrationProvidersResponse>,
  )
}

// configureIntegrationProvider sets a provider's API key and/or selects it as
// active. requireReAuth (pkg/gateway/rest_integrations_auth.go) gates this PUT
// unconditionally, but is itself a no-op in platform mode — so local mode
// REQUIRES a valid re-auth consent token (from reAuth) or the server rejects
// with 403, while platform mode's ConfirmDialog flow calls this with no token
// at all. The token, when present, is replayed in the X-Reauth-Token header.
export function configureIntegrationProvider(
  id: string,
  body: IntegrationProviderUpdateRequest,
  reAuthToken?: string,
): Promise<IntegrationProvidersResponse> {
  return request<IntegrationProvidersResponse>(
    `/integrations/providers/${encodeURIComponent(id)}`,
    {
      method: 'PUT',
      headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
      body: JSON.stringify(body),
    },
    IntegrationProvidersResponseSchema as ZodType<IntegrationProvidersResponse>,
  )
}
