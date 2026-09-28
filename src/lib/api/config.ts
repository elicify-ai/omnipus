// config.ts: Config read/write transform and the settings endpoints

import { logError } from '../telemetry'
import type { ZodType } from 'zod'
import {
  // Spec-3 max-parallel + orchestrator (contract-first #8):
  PerformanceSettings as PerformanceSettingsSchema,
  // Memory/recap settings (workspace-heartbeat-memory-config-spec.md FR-019):
  MemorySettings as MemorySettingsSchema,
  // ADR-066 D9 — global context-budget settings (Settings → Models):
  ContextSettings as ContextSettingsSchema,
  // #904 tool-iteration limit — D11 lowering preview + D16 drift 409:
  MaxToolIterationsLoweringPreview as MaxToolIterationsLoweringPreviewSchema,
  MaxToolIterationsLoweringConflict as MaxToolIterationsLoweringConflictSchema,
  ErrorResponse as ErrorResponseSchema,
  MaxToolIterationAgentChange as MaxToolIterationAgentChangeSchema,
  PerformanceReloadFailedError as PerformanceReloadFailedErrorSchema,
} from '@/lib/api/generated/schemas'
import type {
  RetentionConfig,
  // ADR-066 D9 — global context-budget settings (Settings → Models):
  ContextSettings,
  ContextSettingsUpdate,
  // Spec-3 max-parallel + orchestrator (contract-first #8):
  PerformanceSettings,
  PerformanceSettingsUpdate,
  // Memory/recap settings (workspace-heartbeat-memory-config-spec.md FR-019):
  MemorySettings,
  // #904 tool-iteration limit (tool-iteration-limit-spec.md D11/D16):
  MaxToolIterationsLoweringPreview,
  MaxToolIterationsLoweringConflict,
  MaxToolIterationAgentChange,
  ErrorResponse,
  PerformanceReloadFailedDetails,
  PerformancePendingApply,
  PerformanceReloadFailedError as PerformanceReloadFailedBody,
} from '@/lib/api/generated/openapi-types'
import { ApiError, isApiError } from '../api-error'
import { REAUTH_HEADER } from './auth'
import { request } from './http'

// Config validEnum coercion counter — incremented each time validEnum replaces
// an unexpected backend enum value with the fallback. Exposed on
// window.__omnipus_test_hooks so Playwright tests can assert coercion health.
let _configCoercionCount = 0

export function getConfigCoercionCount(): number {
  return _configCoercionCount
}

export function resetConfigCoercionCount(): void {
  _configCoercionCount = 0
}

// ── Config ────────────────────────────────────────────────────────────────────

// Frontend-shaped config. Mapped from raw backend response via rawToFrontendConfig().
export interface Config { // not-wire-format: SPA-internal configuration shape produced by rawToFrontendConfig(). The backend returns a raw nested JSON object with different field names (e.g. gateway.host instead of gateway.bind_address, nested storage.retention.session_days instead of data.session_retention_days). This type is the SPA's normalised view, not the wire format.
  gateway: {
    bind_address: string
    port: number
    token?: string
    hot_reload?: boolean
    log_level?: string
    // dev_mode_bypass is read-only in the UI — it cannot be toggled via the
    // config PUT endpoint (which blocks it via blockedPaths). The UI uses this
    // to hide admin-only controls that are inoperative when bypass is on.
    dev_mode_bypass?: boolean
    // preview_enabled: live toggle for /preview/ (ADR-044/FR-006). Default enabled.
    preview_enabled?: boolean
  }
  security: {
    // Prompt guard strictness is owned by the dedicated /security/prompt-guard
    // endpoint since Wave 3. This field is still populated on read for
    // backward compatibility but must NOT be sent on updateConfig calls.
    prompt_injection_level?: 'off' | 'low' | 'medium' | 'high'
    // ADR-053 D12 retired the SEC-26 USD cap. The daily_cost_cap field is
    // gone from both the wire types and this Config.
    exec_timeout_seconds?: number
    max_background_seconds?: number
    rate_limits: {
      max_tokens_per_day?: number
      max_cost_per_day?: number
      max_agent_llm_calls_per_hour?: number
      max_agent_tool_calls_per_minute?: number
    }
  }
  data: {
    session_retention_days: number
  }
  tools?: {
    exec?: {
      enable_proxy?: boolean
    }
  }
  agents?: {
    defaults?: {
      default_agent_id?: string
      // ADR-068 D14.1: the default model is the exact (provider, model) pair
      // persisted at agents.defaults.default_model (agents.defaults.model_name
      // no longer exists). Threaded through rawToFrontendConfig so it survives
      // a settings round-trip.
      default_model?: { provider?: string; model?: string }
    }
  }
}

const VALID_INJECTION_LEVELS = ['off', 'low', 'medium', 'high'] as const

// describeCoercedValue renders an arbitrary wire value into a short, loggable
// string for recordCoercion's console.warn/logError payloads. TelemetryEvent
// only accepts string | number | boolean | null | undefined properties, so an
// object/array value must be stringified rather than passed through raw —
// and JSON.stringify itself can throw (circular refs) or return undefined
// (a bare `undefined`/function/symbol value), so this stays defensive.
function describeCoercedValue(value: unknown): string {
  if (value === undefined) return 'undefined'
  try {
    const json = JSON.stringify(value)
    return json !== undefined ? json : String(value)
  } catch {
    return String(value)
  }
}

// recordCoercion centralizes the telemetry/dev-warn side effect shared by
// validEnum/castString/castNumber/castOptionalNumber below: bump the
// module-level coercion counter, and make the event visible somewhere a
// human can see it. DEV builds get a console.warn (unchanged from before);
// non-DEV (production) builds now get a rate-limited logError() telemetry
// record instead — mirroring _recordApiSchemaError's pattern above — so a
// wrong-shaped value silently substituted for a security-relevant field
// (e.g. security.rate_limits.*) is never a fully
// silent event in a production build. Previously this counter+console.warn
// pair was the ONLY signal, and the console.warn was DEV-only, so a
// production coercion of a guardrail field produced no observable trace
// anywhere (issue #146).
function recordCoercion(fieldLabel: string, value: unknown, fallback: unknown): void {
  _configCoercionCount++
  if (import.meta.env.DEV) {
    console.warn(`[api] config coercion (${fieldLabel}): ${describeCoercedValue(value)} → ${describeCoercedValue(fallback)}`)
    return
  }
  if (import.meta.env.MODE !== 'test') {
    logError({
      event: 'configCoercion',
      field: fieldLabel,
      coercedValue: describeCoercedValue(value),
      fallbackValue: describeCoercedValue(fallback),
      totalCoercions: _configCoercionCount,
    })
  }
}

function validEnum<T extends string>(value: unknown, valid: readonly T[], fallback: T, fieldLabel: string): T {
  if (typeof value === 'string' && (valid as readonly string[]).includes(value)) return value as T
  if (value !== undefined && value !== null) {
    recordCoercion(fieldLabel, value, fallback)
  }
  return fallback
}

// cast provides a type-safe wrapper around the repetitive (raw.foo ?? fallback) as T pattern.
// NOTE: cast<T>() only fills in null/undefined — it does NOT check the value's
// runtime type, so a wrong-shaped-but-present wire value (e.g. a string where
// a number was expected) passes straight through mistyped. That is fine for
// low-risk/cosmetic fields (a display-only string, a passthrough token) but
// NOT for fields whose wrong shape would cause visible breakage (rendering a
// non-primitive as a React child) or a bad security/financial decision
// downstream (a corrupted cost cap or rate limit silently turning into NaN →
// null on the next PUT, effectively disabling the guardrail). For those,
// use castString/castNumber/castOptionalNumber below, which mirror
// validEnum's runtime-checked-with-safe-fallback pattern instead.
function cast<T>(obj: unknown, fallback: T): T {
  return (obj ?? fallback) as T
}

// castString/castNumber: like validEnum, but for a scalar type rather than a
// closed enum. Verifies typeof before accepting the wire value; a present
// but wrong-typed value is coerced to the fallback and counted via
// recordCoercion (same telemetry/dev-warn behaviour as validEnum) rather
// than silently passed through mistyped.
function castString(value: unknown, fallback: string, fieldLabel: string): string {
  if (typeof value === 'string') return value
  if (value !== undefined && value !== null) {
    recordCoercion(fieldLabel, value, fallback)
  }
  return fallback
}

function castNumber(value: unknown, fallback: number, fieldLabel: string): number {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (value !== undefined && value !== null) {
    recordCoercion(fieldLabel, value, fallback)
  }
  return fallback
}

// castOptionalNumber: for scalar fields that are legitimately optional on the
// wire (no safe non-undefined fallback exists — e.g. an unset cost cap means
// "no cap", not "cap of 0"). A present-but-wrong-typed value is dropped to
// undefined (same as "not configured") rather than passed through mistyped,
// since a bad number silently reaching a spend/rate-limit/timeout guardrail
// is worse than that guardrail reading as "unset".
function castOptionalNumber(value: unknown, fieldLabel: string): number | undefined {
  if (value === undefined || value === null) return undefined
  if (typeof value === 'number' && Number.isFinite(value)) return value
  recordCoercion(fieldLabel, value, undefined)
  return undefined
}

function rawToFrontendConfig(raw: Record<string, unknown>): Config {
  const gateway = cast<Record<string, unknown>>(raw.gateway, {})
  const storage = cast<Record<string, unknown>>(raw.storage, {})
  const retention = cast<Record<string, unknown>>(storage.retention, {})
  const security = cast<Record<string, unknown>>(raw.security, {})
  const rateLimits = cast<Record<string, unknown>>(security.rate_limits, {})
  const agents = cast<Record<string, unknown>>(raw.agents, {})
  const agentDefaults = cast<Record<string, unknown>>(agents.defaults, {})
  return {
    gateway: {
      // Connectivity basics: rendered directly into form inputs (GatewaySection)
      // and .toString()'d for display — a wrong-shaped value here is visible
      // breakage (a non-primitive rendered as a React child, or "[object
      // Object]" from a stray .toString()), so these are runtime-type-checked.
      bind_address: castString(gateway.host, '127.0.0.1', 'gateway.host'),
      port: castNumber(gateway.port, 8080, 'gateway.port'),
      token: gateway.token as string | undefined,
      hot_reload: gateway.hot_reload as boolean | undefined,
      log_level: gateway.log_level as string | undefined,
      dev_mode_bypass: gateway.dev_mode_bypass as boolean | undefined,
      // ADR-044/FR-006 semantic default: absent/null → enabled; only explicit false disables.
      preview_enabled: gateway.preview_enabled !== false,
    },
    security: {
      prompt_injection_level: validEnum(security.prompt_injection_level, VALID_INJECTION_LEVELS, 'medium', 'security.prompt_injection_level'),
      // Spend/execution guardrails: a wrong-shaped value here is a bad
      // decision downstream, not just a display glitch — SecuritySection reads
      // these with `?.toString()` (which happily stringifies ANY type without
      // throwing) then feeds the result to parseFloat/parseInt on save, so a
      // corrupted-but-truthy value would silently NaN out and strip the
      // guardrail on the next PUT rather than failing loudly. Runtime-checked
      // and dropped to undefined (== "not configured") instead.
      // ADR-053 D12: daily_cost_cap is gone.
      exec_timeout_seconds: castOptionalNumber(security.exec_timeout_seconds, 'security.exec_timeout_seconds'),
      max_background_seconds: castOptionalNumber(security.max_background_seconds, 'security.max_background_seconds'),
      rate_limits: {
        max_tokens_per_day: castOptionalNumber(rateLimits.max_tokens_per_day, 'security.rate_limits.max_tokens_per_day'),
        max_cost_per_day: castOptionalNumber(rateLimits.max_cost_per_day, 'security.rate_limits.max_cost_per_day'),
        max_agent_llm_calls_per_hour: castOptionalNumber(rateLimits.max_agent_llm_calls_per_hour, 'security.rate_limits.max_agent_llm_calls_per_hour'),
        max_agent_tool_calls_per_minute: castOptionalNumber(rateLimits.max_agent_tool_calls_per_minute, 'security.rate_limits.max_agent_tool_calls_per_minute'),
      },
    },
    data: {
      // Same rationale as gateway.port above — DataSection reads this via
      // .toString() directly (no optional chaining, so a null/undefined
      // value WOULD throw a TypeError at render time). castNumber guarantees
      // session_retention_days is never null/undefined, but without the
      // runtime type check a wrong-shaped-but-present value (e.g. an object)
      // would NOT throw — .toString() happily renders it as garbage text
      // ("[object Object]") instead of a number. Runtime-checked here for
      // the same reason as gateway.port: a corrupted display, not a crash,
      // is still the failure being guarded against.
      session_retention_days: castNumber(retention.session_days, 90, 'storage.retention.session_days'),
    },
    agents: {
      defaults: {
        default_agent_id: agentDefaults.default_agent_id as string | undefined,
        default_model: agentDefaults.default_model as { provider?: string; model?: string } | undefined,
      },
    },
  }
}

export async function fetchConfig(): Promise<Config> {
  const raw = await request<Record<string, unknown>>('/config')
  return rawToFrontendConfig(raw)
}

// frontendToRawConfig is the inverse of rawToFrontendConfig. It serialises the
// SPA-shaped Config back to the wire shape the backend expects on PUT /config.
// The gateway's config.json uses `gateway.host` (not `bind_address`), and the
// session-retention field lives at `storage.retention.session_days` (not
// `data.session_retention_days`). Sending the SPA shape directly causes silent
// data loss — the backend ignores unknown keys.
//
// This function only serialises fields the PUT /config handler accepts. It
// intentionally omits `gateway.dev_mode_bypass` (blocked server-side) and
// `security.prompt_injection_level` (owned by PUT /security/prompt-guard).
function frontendToRawConfig(data: Partial<Config>): Record<string, unknown> {
  const raw: Record<string, unknown> = {}
  if (data.gateway) {
    const gw: Record<string, unknown> = {}
    if (data.gateway.bind_address !== undefined) gw.host = data.gateway.bind_address
    if (data.gateway.port !== undefined) gw.port = data.gateway.port
    if (data.gateway.token !== undefined) gw.token = data.gateway.token
    if (data.gateway.hot_reload !== undefined) gw.hot_reload = data.gateway.hot_reload
    if (data.gateway.log_level !== undefined) gw.log_level = data.gateway.log_level
    if (data.gateway.preview_enabled !== undefined) gw.preview_enabled = data.gateway.preview_enabled
    // dev_mode_bypass is intentionally omitted — PUT /config blocks that field.
    raw.gateway = gw
  }
  if (data.security) {
    const sec: Record<string, unknown> = {}
    // prompt_injection_level intentionally omitted — owned by PUT /security/prompt-guard.
    // daily_cost_cap intentionally omitted — ADR-053 D12 retired the SEC-26
    // USD cap.
    if (data.security.exec_timeout_seconds !== undefined) sec.exec_timeout_seconds = data.security.exec_timeout_seconds
    if (data.security.max_background_seconds !== undefined) sec.max_background_seconds = data.security.max_background_seconds
    if (data.security.rate_limits) {
      sec.rate_limits = { ...data.security.rate_limits }
    }
    raw.security = sec
  }
  if (data.data) {
    raw.storage = {
      retention: {
        session_days: data.data.session_retention_days,
      },
    }
  }
  if (data.agents?.defaults) {
    raw.agents = { defaults: { ...data.agents.defaults } }
  }
  return raw
}

export async function updateConfig(data: Partial<Config>): Promise<Config> {
  // Translate SPA shape → wire shape before sending, then transform the
  // raw wire response back to SPA shape on success.
  const wireBody = frontendToRawConfig(data)
  const raw = await request<Record<string, unknown>>('/config', {
    method: 'PUT',
    body: JSON.stringify(wireBody),
  })
  return rawToFrontendConfig(raw)
}

// Session scope — controls DM conversation isolation granularity.
// SessionScopeResponse is re-exported from generated openapi-types above.
// SessionScopeRequest (request body) — re-exported from generated openapi-types.
// See contracts/components/schemas/SessionScopeRequest.yaml.
// SessionScopeUpdateResponse — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/SessionScopeUpdateResponse.yaml.
//
// No current caller reads or writes DM scope (fetchSessionScope/
// updateSessionScope were removed as zero-consumer exports) — DMScope and
// the re-exported types above remain available for a future Settings surface.

// Retention — session log retention policy.
// RetentionConfig — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/RetentionConfig.yaml.
//
// No current caller reads, writes, or sweeps retention (fetchRetention/
// updateRetention/triggerRetentionSweep/retentionMode were all removed as
// zero-consumer exports). The two type aliases below remain available for a
// future Settings surface — RetentionMode is the SPA-internal classification
// of a RetentionConfig response; RetentionUpdateBody is the PUT request body
// shape.
export type RetentionMode = 'default' | 'custom' | 'forever'

export type RetentionUpdateBody = RetentionConfig

export function fetchPerformanceSettings(): Promise<PerformanceSettings> {
  return request<PerformanceSettings>('/performance', undefined, PerformanceSettingsSchema)
}

// updatePerformanceSettings persists the max-parallel-agents concurrency setting.
// It is re-auth gated (Spec-6 FR-12.2 / Spec-3 FR-6.6): the server rejects the PUT
// with 403 unless a single-use consent token (from reAuth) is replayed in the
// X-Reauth-Token header.
//
// #904: a PUT that changes max_tool_iterations can answer 409
// MaxToolIterationsLoweringConflict (D16 drift — the set of agents that would
// be lowered differs from the body's confirmed_lowering). That refusal is
// re-thrown as the typed MaxToolIterationsLoweringConflictError so the screen
// can re-open its confirm dialog with the fresh list. The two lowering-failure
// 500s (max_tool_iterations_lowering_failed / _rollback_incomplete) carry a
// server message the admin must read verbatim — it names the agents and says
// nothing was changed — so their userMessage is the server's `error` text
// rather than the generic 5xx default.
export async function updatePerformanceSettings(
  body: PerformanceSettingsUpdate,
  reAuthToken?: string,
): Promise<PerformanceSettings> {
  try {
    return await request<PerformanceSettings>('/performance', {
      method: 'PUT',
      headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
      body: JSON.stringify(body),
    }, PerformanceSettingsSchema)
  } catch (err) {
    throw performanceWriteError(err, body)
  }
}

// fetchMaxToolIterationsLoweringPreview asks, read-only, which agents' own
// limits would be lowered if the global were set to `value` (spec D11). No
// step-up token: the PUT consumes it, not the preview.
export function fetchMaxToolIterationsLoweringPreview(
  value: number,
): Promise<MaxToolIterationsLoweringPreview> {
  return request<MaxToolIterationsLoweringPreview>(
    `/performance/max-tool-iterations/preview?value=${encodeURIComponent(String(value))}`,
    undefined,
    MaxToolIterationsLoweringPreviewSchema as ZodType<MaxToolIterationsLoweringPreview>,
  )
}

/**
 * MaxToolIterationsLoweringConflictError is PUT /performance's 409 (spec D16):
 * nothing was written because the agents that would be lowered changed since
 * the admin's preview. `preview` is the fresh list computed at refusal time —
 * the dialog reloads from it without a second preview call. Extends ApiError
 * so every existing `isApiError`/`getErrorMessage` call site still works.
 * Mirrors src/lib/api/library.ts::LibraryVersionConflictError.
 */
export class MaxToolIterationsLoweringConflictError extends ApiError {
  readonly preview: MaxToolIterationsLoweringPreview

  constructor(conflict: MaxToolIterationsLoweringConflict, bodyText: string) {
    super(409, conflict.error, { code: conflict.code, body: bodyText })
    this.name = 'MaxToolIterationsLoweringConflictError'
    this.preview = conflict.preview
    Object.setPrototypeOf(this, MaxToolIterationsLoweringConflictError.prototype)
  }
}

export function isMaxToolIterationsLoweringConflict(
  err: unknown,
): err is MaxToolIterationsLoweringConflictError {
  return err instanceof MaxToolIterationsLoweringConflictError
}

/**
 * PerformanceReloadFailedError is PUT /performance's 500
 * `performance_reload_failed` (body: the generated PerformanceReloadFailedError
 * schema): every write of the request IS committed to config.json, but the new
 * values are not in force yet. It is never a failed save, so its userMessage
 * always starts "Saved, but not applied yet".
 *
 *   - `stage` is details.stage. `refresh`: the in-memory configuration was not
 *     swapped, so GET /performance still shows the OLD values — the screen must
 *     keep the saved values on show instead of re-syncing to it. `reload`: GET
 *     shows the new values; only the agents' reload failed. `null`: the body did
 *     not say (malformed or missing details) — treated like `refresh`, the one
 *     reading that never shows a stale value as current.
 *   - `changedFields` is details.changed_fields (when unreadable: the settings
 *     the request carried).
 *   - `loweredAgents` is details.lowered_agents — GET /performance does not
 *     carry them. `loweredUnknown` is true when agents may have been lowered
 *     but the list could not be read (malformed, or missing although the
 *     request confirmed a lowering): the screen must say so, not stay silent.
 */
export const PERFORMANCE_RELOAD_FAILED_CODE = 'performance_reload_failed'

export type PerformanceReloadStage = PerformanceReloadFailedDetails['stage']
export type PerformanceChangedField = PerformanceReloadFailedDetails['changed_fields'][number]

export interface PerformanceReloadFailure { // not-wire-format: constructor options for PerformanceReloadFailedError, parsed from the generated PerformanceReloadFailedError body
  stage: PerformanceReloadStage | null
  changedFields: PerformanceChangedField[]
  loweredAgents: MaxToolIterationAgentChange[]
  loweredUnknown: boolean
}

const CHANGED_FIELD_LABELS: Record<PerformanceChangedField, string> = {
  max_parallel_agents: 'agents running at once',
  tools_on_demand: 'on-demand tool loading',
  goal_max_rounds: 'goal tries',
  max_tool_iterations: 'tool-call limit',
}

function reloadFailedMessage(serverMessage: string, f: PerformanceReloadFailure): string {
  const detail = serverMessage.trim()
  const named = f.changedFields.map((k) => CHANGED_FIELD_LABELS[k]).join(', ')
  const fallback = named ? `the new ${named} will be used after a restart or reload` : ''
  const text = detail || fallback
  if (f.stage === 'reload') {
    return `Saved, but not applied yet${text ? `: ${text}` : ' — the running agents keep the old values until a restart or reload.'}`
  }
  return `Saved, but not applied yet — saved to the settings file; takes effect after a restart or reload${text ? `: ${text}` : '.'}`
}

/**
 * The "saved, but not applied yet" text for the server's pending-apply state
 * (GET /performance pending_apply, #904) when this page holds no failure
 * message of its own — e.g. after a page reload. Same wording as a
 * PerformanceReloadFailedError with an empty server message.
 */
export function performancePendingApplyMessage(p: PerformancePendingApply): string {
  return reloadFailedMessage('', { stage: p.stage, changedFields: p.changed_fields, loweredAgents: [], loweredUnknown: false })
}

export class PerformanceReloadFailedError extends ApiError {
  readonly stage: PerformanceReloadStage | null
  readonly changedFields: PerformanceChangedField[]
  readonly loweredAgents: MaxToolIterationAgentChange[]
  readonly loweredUnknown: boolean

  constructor(serverMessage: string, bodyText: string, cause: unknown, failure: Partial<PerformanceReloadFailure> = {}) {
    const f: PerformanceReloadFailure = {
      stage: failure.stage ?? null,
      changedFields: failure.changedFields ?? [],
      loweredAgents: failure.loweredAgents ?? [],
      loweredUnknown: failure.loweredUnknown ?? false,
    }
    super(500, reloadFailedMessage(serverMessage, f), {
      code: PERFORMANCE_RELOAD_FAILED_CODE,
      body: bodyText,
      cause,
    })
    this.name = 'PerformanceReloadFailedError'
    this.stage = f.stage
    this.changedFields = f.changedFields
    this.loweredAgents = f.loweredAgents
    this.loweredUnknown = f.loweredUnknown
    Object.setPrototypeOf(this, PerformanceReloadFailedError.prototype)
  }

  /** True only when GET /performance is known to show the saved values. */
  get inMemoryUpdated(): boolean {
    return this.stage === 'reload'
  }
}

export function isPerformanceReloadFailed(err: unknown): err is PerformanceReloadFailedError {
  return err instanceof PerformanceReloadFailedError
}

// performanceWriteError re-parses a PUT /performance failure body against the
// generated Zod schemas. A 409 that does not match the conflict envelope stays
// a plain 409 ApiError (never misreported as a different status), exactly as
// library.ts::libraryConflictErrorFromResponse does. A 500
// `performance_reload_failed` is ALWAYS a PerformanceReloadFailedError — a
// malformed or missing details object or an empty message degrades what it can
// say, never turns a committed save into a generic failure. Any other 500
// carrying one of #904's own codes (every `max_tool_iterations_*` failure)
// keeps the server's message, which says what was and was not written; any
// other 500 keeps the generic 5xx default so internal detail is never shown.
function performanceWriteError(err: unknown, body: PerformanceSettingsUpdate): unknown {
  if (!isApiError(err)) return err
  let raw: unknown
  try {
    raw = err.body === undefined ? undefined : (JSON.parse(err.body) as unknown)
  } catch {
    raw = undefined
  }
  if (err.status === 409) {
    const parsed = (MaxToolIterationsLoweringConflictSchema as ZodType<MaxToolIterationsLoweringConflict>).safeParse(raw)
    return parsed.success ? new MaxToolIterationsLoweringConflictError(parsed.data, err.body ?? '') : err
  }
  if (err.status !== 500) return err
  if (err.code === PERFORMANCE_RELOAD_FAILED_CODE) return reloadFailedError(err, raw, body)
  if (!(err.code ?? '').startsWith('max_tool_iterations_')) return err
  const parsed = (ErrorResponseSchema as ZodType<ErrorResponse>).safeParse(raw)
  if (!parsed.success || parsed.data.error.trim() === '') return err
  return new ApiError(err.status, parsed.data.error, { code: err.code, body: err.body, cause: err })
}

// reloadFailedError reads the body with the generated
// PerformanceReloadFailedError schema. When it does not match, it salvages
// what it can (the message, a well-formed lowered list), names the changed
// settings from the request the client sent (a malformed body cannot say which
// it saved, but every setting the PUT carried was written), and reports the
// rest as unknown rather than guessing.
function reloadFailedError(err: ApiError, raw: unknown, body: PerformanceSettingsUpdate): PerformanceReloadFailedError {
  const bodyText = err.body ?? ''
  const typed = (PerformanceReloadFailedErrorSchema as ZodType<PerformanceReloadFailedBody>).safeParse(raw)
  if (typed.success) {
    const d = typed.data.details
    return new PerformanceReloadFailedError(typed.data.error, bodyText, err, {
      stage: d.stage,
      changedFields: d.changed_fields,
      loweredAgents: d.lowered_agents,
    })
  }
  console.warn('[api] performance_reload_failed: body did not match PerformanceReloadFailedError', typed.error)
  const obj = raw !== null && typeof raw === 'object' ? (raw as Record<string, unknown>) : {}
  const message = typeof obj.error === 'string' ? obj.error : ''
  const details = obj.details !== null && typeof obj.details === 'object' ? (obj.details as Record<string, unknown>) : {}
  const lowered = (MaxToolIterationAgentChangeSchema as ZodType<MaxToolIterationAgentChange>).array().safeParse(details.lowered_agents)
  const confirmedLowering = (body.confirmed_lowering?.length ?? 0) > 0
  const loweredUnknown = lowered.success ? false : details.lowered_agents !== undefined || confirmedLowering
  const sentFields = (Object.keys(CHANGED_FIELD_LABELS) as PerformanceChangedField[]).filter((k) => body[k] !== undefined)
  return new PerformanceReloadFailedError(message, bodyText, err, {
    stage: null,
    changedFields: sentFields,
    loweredAgents: lowered.success ? lowered.data : [],
    loweredUnknown,
  })
}

// ── Memory Settings ───────────────────────────────────────────────────────────
//
// Global memory/recap and retention settings. Readable/writable by any
// authenticated user (no admin gate — operator decision A2/G-02, FR-019).
// The dedicated endpoint reads/writes ONLY the MemorySettings fields — no
// merge of sibling config sections or secrets.
// See contracts/components/schemas/MemorySettings.yaml.

export function fetchMemorySettings(): Promise<MemorySettings> {
  return request<MemorySettings>(
    '/settings/memory',
    undefined,
    MemorySettingsSchema as ZodType<MemorySettings>,
  )
}

export function updateMemorySettings(body: MemorySettings): Promise<MemorySettings> {
  return request<MemorySettings>(
    '/settings/memory',
    { method: 'PUT', body: JSON.stringify(body) },
    MemorySettingsSchema as ZodType<MemorySettings>,
  )
}

// ADR-066 D9 (FR-036) — global context-budget settings: per-surface tool-result
// caps, the absolute mid-turn trigger, the ingest bound, the global default
// context window and the per-(provider, model) window overrides. User-facing
// location: Settings → Models (FR-037). PUT is a PARTIAL update
// (ContextSettingsUpdate): an omitted field is unchanged, `model_overrides`
// replaces the whole list, `default_context_window: null` clears it. Every
// 200 write triggers a registry reload on the gateway. withAuth (the
// /settings/memory precedent). See contracts/components/schemas/ContextSettings.yaml.

export function getContextSettings(): Promise<ContextSettings> {
  return request<ContextSettings>(
    '/settings/context',
    undefined,
    ContextSettingsSchema as ZodType<ContextSettings>,
  )
}

export function putContextSettings(body: ContextSettingsUpdate): Promise<ContextSettings> {
  return request<ContextSettings>(
    '/settings/context',
    { method: 'PUT', body: JSON.stringify(body) },
    ContextSettingsSchema as ZodType<ContextSettings>,
  )
}
