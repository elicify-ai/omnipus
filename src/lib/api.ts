// REST API client — all calls go through the backend gateway.
// Auth: Authorization: Bearer <token> header. Token read from sessionStorage (preferred) or localStorage ('omnipus_auth_token'). Backend validates against per-user RBAC token hashes or legacy OMNIPUS_BEARER_TOKEN env var.
// CSRF: X-CSRF-Token header echoes the __Host-csrf cookie value on every
// state-changing request (double-submit cookie, issue #97). The cookie is
// issued by the backend on /auth/login, /auth/register-admin, and
// /onboarding/complete. State-changing calls made before the cookie exists
// fail fast client-side so the UI surfaces an actionable error instead of
// waiting for the server's 403.
//
// Errors: request() throws ApiError on non-2xx responses and on transport
// failures (network down, fetch threw). Callers should branch on err.status
// (or err.isAuthError() / err.isNotFound() / err.isRateLimited() / etc)
// rather than regex-matching err.message — see src/lib/api-error.ts.

import { ApiError } from './api-error'
export { ApiError, isApiError } from './api-error'
import { maybeDevToast } from './dev-toast'
import { logError } from './telemetry'

import type { ZodType } from 'zod'
import { z } from 'zod'

// ── Generated Zod schemas (REST edge validation) ────────────────────────────
//
// These are the generated schemas from contracts/openapi.yaml. They are used
// to validate API responses at the SPA edge (hard-constraint #8). Callers
// that need a SPA-internal transform type before passing to consumers are
// validated against the wire schema first, then transformed.
//
// GAP REPORT — endpoints that genuinely cannot use a generated schema:
//   GET /config / PUT /config — Config is a deep SPA transform (rawToFrontendConfig /
//     frontendToRawConfig); no named schema component matches the wire shape exactly.
//     Wire shape is an untyped JSON object; the transform is the contract.
//   GET /channels/:id       — Record<string,unknown> passthrough; schema varies per channel.
//   PUT /channels/:id/configure — void; channel-specific body; no generated schema.
//   GET /credentials        — wire returns string[]; SPA shape is CredentialKey[]; the
//     SPA-internal shape is a SPA-only concern, not a generated schema component.
//   GET /about              — AboutInfo is a looser SPA-compatibility subset of AboutResponse
//     (different field names: uptime_seconds vs uptime); cannot validate without false negatives.

import {
  ChannelRouting as ChannelRoutingSchema,
  LoginResponse as LoginResponseSchema,
  ProbeProviderResponse as ProbeProviderResponseSchema,
  Agent as AgentSchema,
  AgentSession as AgentSessionSchema,
  AuditLogResponse as AuditLogResponseSchema,
  AuditLogToggle as AuditLogToggleSchema,
  ExecAllowlist as ExecAllowlistSchema,
  ExecProxyStatus as ExecProxyStatusSchema,
  GlobalToolPolicies as GlobalToolPoliciesSchema,
  PendingRestartEntry as PendingRestartEntrySchema,
  PromptGuardResponse as PromptGuardResponseSchema,
  RetentionConfig as RetentionConfigSchema,
  RetentionSweepResult as RetentionSweepResultSchema,
  SandboxConfig as SandboxConfigSchema,
  SandboxStatus as SandboxStatusSchema,
  SessionScopeResponse as SessionScopeResponseSchema,
  SkillTrustResponse as SkillTrustResponseSchema,
  // New generated Zod schemas (contract-first #8):
  AppState as AppStateSchema,
  ValidateTokenResponse as ValidateTokenResponseSchema,
  DoctorResult as DoctorResultSchema,
  DevicesResponse as DevicesResponseSchema,
  BackupEntry as BackupEntrySchema,
  StorageStats as StorageStatsSchema,
  MeInfo as MeInfoSchema,
  // Newly wired schemas:
  Provider as ProviderSchema,
  CliDetect as CliDetectSchema,
  // external-executor-cli-path-detection spec (ADR-030): create-time validate.
  CliValidateResponse as CliValidateResponseSchema,
  GatewayStatus as GatewayStatusSchema,
  ToolRegistryEntry as ToolRegistryEntrySchema,
  ChannelEntry as ChannelEntrySchema,
  ChannelEnabledResponse as ChannelEnabledResponseSchema,
  ChannelCreateResponse as ChannelCreateResponseSchema,
  Skill as SkillSchema,
  SkillSearchResult as SkillSearchResultSchema,
  SkillMarketplaceStatus as SkillMarketplaceStatusSchema,
  McpServer as McpServerSchema,
  McpServerTestResponse as McpServerTestResponseSchema,
  ActivityEvent as ActivityEventSchema,
  // Wire-shape schemas used for raw-to-SPA transform validation:
  Message as WireMessageSchema,
  Session as WireSessionSchema,
  SessionDetail as WireSessionDetailSchema,
  // Newly promoted from inline openapi.yaml schemas:
  AuditLogUpdateResponse as AuditLogUpdateResponseSchema,
  SkillTrustUpdateResponse as SkillTrustUpdateResponseSchema,
  PromptGuardUpdateResponse as PromptGuardUpdateResponseSchema,
  RateLimitsResponse as RateLimitsResponseSchema,
  RateLimitsUpdateResponse as RateLimitsUpdateResponseSchema,
  SessionScopeUpdateResponse as SessionScopeUpdateResponseSchema,
  RetentionUpdateResponse as RetentionUpdateResponseSchema,
  AgentToolsResponse as AgentToolsResponseSchema,
  ChannelTestResponse as ChannelTestResponseSchema,
  OperationResult as OperationResultSchema,
  UploadFilesResponse as UploadFilesResponseSchema,
  BackupCreateResponse as BackupCreateResponseSchema,
  RotateTokenResponse as RotateTokenResponseSchema,
  // fix-AC: promoted from hand-written inline schemas:
  UserContextResponse as UserContextResponseSchema,
  McpServerToolsResponse as McpServerToolsResponseSchema,
  // #264 Schedules (contract-first #8):
  Schedule as ScheduleSchema,
  ScheduleList as ScheduleListSchema,
  ScheduleRunResult as ScheduleRunResultSchema,
  // #264 Notifications (contract-first #8):
  NotificationList as NotificationListSchema,
  // Level-1 workspaces + unified tasks + token stats (contract-first #8):
  Workspace as WorkspaceSchema,
  // M5 per-workspace delegation graph (contract-first #8):
  WorkspaceDelegation as WorkspaceDelegationSchema,
  // Workspace / Project Instructions (contract-first #8):
  WorkspaceInstructionsResponse as WorkspaceInstructionsResponseSchema,
  Task as TaskSchema,
  TokenUsageSummary as TokenUsageSummarySchema,
  // Milestones (contract-first #8):
  Milestone as MilestoneSchema,
  // Spec-3 max-parallel + orchestrator (contract-first #8):
  PerformanceSettings as PerformanceSettingsSchema,
  // Spec-6 U5 — re-auth + Integrations + transcribe (contract-first #8):
  ReAuthResponse as ReAuthResponseSchema,
  IntegrationProvidersResponse as IntegrationProvidersResponseSchema,
  TranscribeResponse as TranscribeResponseSchema,
  // Spec-4 — external-CLI runner connection test (contract-first #8):
  RunnerTestResponse as RunnerTestResponseSchema,
  // Version drift detection (used by fetchVersion → useVersionCheck):
  VersionResponse as VersionResponseSchema,
  // Voice provider capability detection (used by fetchVoiceProvider):
  VoiceProvider as VoiceProviderSchema,
  // O4 gateway self-restart (contract-first #8):
  GatewayRestartResponse as GatewayRestartResponseSchema,
  // O14 god-mode switch (contract-first #8):
  GodModeStatus as GodModeStatusSchema,
  // Slash-command harmonization (contract-first #8):
  SlashCommand as SlashCommandSchema,
  // Memory/recap settings (workspace-heartbeat-memory-config-spec.md FR-019):
  MemorySettings as MemorySettingsSchema,
} from '@/lib/api/generated/schemas'

// ── Schema validation error ────────────────────────────────────────────────────
//
// Thrown when an API response does not conform to the expected Zod schema.
// Only thrown when a schema was explicitly passed to request() — unvalidated
// calls fall back to the old untyped behaviour.
//
// In dev mode, a toast is emitted as well (see request() below).

export class ApiSchemaError extends Error {
  readonly endpoint: string
  readonly zodIssues: Array<{ path: (string | number)[]; message: string }>
  readonly rawBody: unknown

  constructor(endpoint: string, zodIssues: Array<{ path: (string | number)[]; message: string }>, rawBody: unknown) {
    super(`API response schema mismatch for ${endpoint}: ${zodIssues[0]?.message ?? 'unknown'}`)
    this.name = 'ApiSchemaError'
    this.endpoint = endpoint
    this.zodIssues = zodIssues
    this.rawBody = rawBody
  }
}

let _apiSchemaErrorCount = 0

export function getApiSchemaErrorCount(): number {
  return _apiSchemaErrorCount
}

export function resetApiSchemaErrorCount(): void {
  _apiSchemaErrorCount = 0
}

// Increment the API-schema-error counter AND emit a production telemetry
// event. Rate-limited inside logError so a contract-drift flood doesn't
// spam the log collector. Dev builds skip telemetry (they already get the
// dev toast).
function _recordApiSchemaError(endpoint: string, issueCount: number): void {
  _apiSchemaErrorCount++
  if (!import.meta.env.DEV && import.meta.env.MODE !== 'test') {
    logError({
      event: 'apiSchemaError',
      endpoint,
      issueCount,
      totalErrors: _apiSchemaErrorCount,
    })
  }
}

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

// Expose counters on window.__omnipus_test_hooks in DEV/test builds and in
// Playwright automation (navigator.webdriver=true) so E2E tests against
// production builds can assert on validation health without reaching into module
// internals.
if ((import.meta.env.DEV || import.meta.env.MODE === 'test' || (typeof navigator !== 'undefined' && navigator.webdriver)) && typeof window !== 'undefined') {
  const w = window as unknown as { __omnipus_test_hooks?: Record<string, unknown> }
  w.__omnipus_test_hooks ??= {}
  w.__omnipus_test_hooks.getApiSchemaErrorCount = getApiSchemaErrorCount
  w.__omnipus_test_hooks.resetApiSchemaErrorCount = resetApiSchemaErrorCount
  w.__omnipus_test_hooks.getConfigCoercionCount = getConfigCoercionCount
  w.__omnipus_test_hooks.resetConfigCoercionCount = resetConfigCoercionCount
}

// ── Re-exports from generated types ───────────────────────────────────────────
//
// Wire-format types come from the generated openapi-types.ts. The hand-written
// interfaces below replace the generated types only when the SPA-internal shape
// differs from the wire shape (e.g. Session flattens nested stats, ToolCall uses
// `params` while the wire uses `parameters`).
//
// RULE: types with hand-written bodies below are imported aliased and NOT
// re-exported from generated — the local export interface is canonical.
// Types without local bodies are imported and immediately re-exported.
// See CLAUDE.md hard-constraint #8.

// Types whose generated shape is canonical (no local body) — import into scope
// so function return-type annotations compile, then re-export for consumers.
import type {
  LoginResponse,
  ProbeProviderResponse,
  AgentSession,
  AgentToolEntry,
  SessionStats,
  Attachment,
  SessionScopeRequest,
  SessionScopeResponse,
  ToolRegistryEntry,
  GlobalToolPolicies,
  ToolPolicy,
  ChannelEntry,
  RetentionConfig,
  RetentionSweepResult,
  SandboxConfig,
  SandboxConfigUpdate,
  SandboxStatus,
  AuditEntry,
  AuditLogResponse,
  AuditLogToggle,
  RateLimitConfig,
  ExecAllowlist,
  ExecProxyStatus,
  SkillTrustResponse,
  PromptGuardResponse,
  PendingRestartEntry,
  AboutResponse,
  // Wire types migrated from hand-written interfaces to generated types:
  Agent,
  Provider,
  ProviderUpdateRequest,
  CliDetect,
  CliDetectEntry,
  CliValidateRequest,
  CliValidateResponse,
  GatewayStatus,
  Skill,
  SkillSearchResult,
  SkillMarketplaceStatus,
  SkillInstallRequest,
  ActivityEvent,
  UploadedFile,
  AgentToolsCfg,
  OnboardingCompleteRequest,
  // New wire types (contract-first #8):
  Task,
  McpServer,
  McpServerCreate,
  McpServerUpdate,
  McpServerTestResponse,
  AppState,
  ValidateTokenResponse,
  DoctorIssue,
  DoctorResult,
  DevicePending,
  DevicePaired,
  DevicesResponse,
  BackupEntry,
  StorageStats,
  MeInfo,
  // Newly promoted from inline openapi.yaml schemas:
  AuditLogUpdateResponse,
  SkillTrustUpdateRequest,
  SkillTrustUpdateResponse,
  PromptGuardUpdateRequest,
  PromptGuardUpdateResponse,
  RateLimitsResponse,
  RateLimitsUpdateRequest,
  RateLimitsUpdateResponse,
  SessionScopeUpdateResponse,
  RetentionUpdateResponse,
  ChannelEnabledResponse,
  AgentToolsResponse,
  UploadFilesResponse,
  BackupCreateResponse,
  ChannelTestResponse,
  OperationResult,
  // fix-AC: promoted from hand-written inline schemas:
  UserContextResponse,
  McpServerToolsResponse,
  AgentUpdateRequest,
  AgentCreateRequest,
  FallbackModel,
  ChannelRouting,
  // ADR-029 channel-instance CRUD (US-6/US-10/US-11):
  ChannelCreateRequest,
  ChannelCreateResponse,
  // Level-1 workspaces + unified tasks + token stats (contract-first #8):
  Workspace,
  WorkspaceCreateRequest,
  WorkspaceUpdateRequest,
  WorkspaceMemberConfig,
  // M5 per-workspace delegation graph (contract-first #8):
  WorkspaceDelegation,
  WorkspaceDelegationEdge,
  WorkspaceDelegationUpdateRequest,
  // Workspace / Project Instructions (contract-first #8):
  WorkspaceInstructionsResponse,
  WorkspaceInstructionsRequest,
  TokenUsageSummary,
  // Unified task types (Sprint 2) — imported once here (Task was already imported above):
  TaskCreateRequest,
  TaskUpdateRequest,
  Todo,
  TaskTrigger,
  // #264 Schedules (contract-first #8):
  Schedule,
  ScheduleCreate,
  ScheduleUpdate,
  ScheduleList,
  ScheduleRunResult,
  // #264 Notifications (contract-first #8):
  NotificationList,
  // Milestones:
  Milestone,
  MilestoneCreateRequest,
  MilestoneUpdateRequest,
  MilestoneListResponse,
  // Spec-3 max-parallel + orchestrator (contract-first #8):
  PerformanceSettings,
  PerformanceSettingsUpdate,
  // Spec-6 U5 — re-auth + Integrations + transcribe (contract-first #8):
  ReAuthResponse,
  IntegrationProvider,
  IntegrationProvidersResponse,
  IntegrationProviderUpdateRequest,
  TranscribeResponse,
  // Spec-4 — sub-agent executor + external-CLI runner test (contract-first #8):
  ExecutorConfig,
  RunnerTestResponse,
  // Version drift detection (used by useVersionCheck):
  VersionResponse,
  // Voice provider capability detection (used by voice-provider-detect):
  VoiceProvider,
  // O4 gateway self-restart (contract-first #8):
  GatewayRestartResponse,
  // O14 god-mode switch (contract-first #8):
  GodModeStatus,
  GodModeUpdateRequest,
  // Slash-command harmonization (contract-first #8):
  SlashCommand,
  // Memory/recap settings (workspace-heartbeat-memory-config-spec.md FR-019):
  MemorySettings,
} from '@/lib/api/generated/openapi-types'

export type {
  LoginResponse,
  ProbeProviderResponse,
  AgentSession,
  AgentToolEntry,
  SessionStats,
  Attachment,
  SessionScopeRequest,
  SessionScopeResponse,
  ToolRegistryEntry,
  GlobalToolPolicies,
  ToolPolicy,
  ChannelEntry,
  RetentionConfig,
  RetentionSweepResult,
  SandboxConfig,
  SandboxStatus,
  AuditEntry,
  AuditLogResponse,
  AuditLogToggle,
  RateLimitConfig,
  ExecAllowlist,
  ExecProxyStatus,
  SkillTrustResponse,
  PromptGuardResponse,
  PendingRestartEntry,
  AboutResponse,
  // Wire types migrated from hand-written interfaces:
  Agent,
  Provider,
  CliDetect,
  CliDetectEntry,
  CliValidateRequest,
  CliValidateResponse,
  GatewayStatus,
  Skill,
  SkillSearchResult,
  SkillMarketplaceStatus,
  SkillInstallRequest,
  ActivityEvent,
  UploadedFile,
  AgentToolsCfg,
  OnboardingCompleteRequest,
  // New wire types:
  Task,
  McpServer,
  McpServerCreate,
  McpServerUpdate,
  McpServerTestResponse,
  AppState,
  ValidateTokenResponse,
  DoctorIssue,
  DoctorResult,
  DevicePending,
  DevicePaired,
  DevicesResponse,
  BackupEntry,
  StorageStats,
  MeInfo,
  // Promoted from inline openapi.yaml schemas:
  AuditLogUpdateResponse,
  SkillTrustUpdateRequest,
  SkillTrustUpdateResponse,
  PromptGuardUpdateRequest,
  PromptGuardUpdateResponse,
  RateLimitsResponse,
  RateLimitsUpdateRequest,
  RateLimitsUpdateResponse,
  SessionScopeUpdateResponse,
  RetentionUpdateResponse,
  ChannelEnabledResponse,
  AgentToolsResponse,
  UploadFilesResponse,
  BackupCreateResponse,
  ChannelTestResponse,
  OperationResult,
  // fix-AC: promoted from hand-written inline schemas:
  UserContextResponse,
  McpServerToolsResponse,
  AgentUpdateRequest,
  AgentCreateRequest,
  FallbackModel,
  ChannelRouting,
  // ADR-029 channel-instance CRUD (US-6/US-10/US-11):
  ChannelCreateRequest,
  ChannelCreateResponse,
  // Level-1 workspaces + unified tasks + token stats:
  Workspace,
  WorkspaceCreateRequest,
  WorkspaceUpdateRequest,
  WorkspaceMemberConfig,
  // M5 per-workspace delegation graph:
  WorkspaceDelegation,
  WorkspaceDelegationEdge,
  WorkspaceDelegationUpdateRequest,
  // Workspace / Project Instructions:
  WorkspaceInstructionsResponse,
  WorkspaceInstructionsRequest,
  TokenUsageSummary,
  // Unified task types (Sprint 2) — Task already exported above, add new ones:
  TaskCreateRequest,
  TaskUpdateRequest,
  Todo,
  TaskTrigger,
  Milestone,
  MilestoneCreateRequest,
  MilestoneUpdateRequest,
  MilestoneListResponse,
  // Spec-6 U5:
  ReAuthResponse,
  IntegrationProvider,
  IntegrationProvidersResponse,
  IntegrationProviderUpdateRequest,
  TranscribeResponse,
  // Spec-4 — sub-agent executor + external-CLI runner test:
  ExecutorConfig,
  RunnerTestResponse,
  // O4 gateway self-restart:
  GatewayRestartResponse,
  // O14 god-mode switch:
  GodModeStatus,
  GodModeUpdateRequest,
  // Slash-command harmonization (contract-first #8):
  SlashCommand,
  // Memory/recap settings (workspace-heartbeat-memory-config-spec.md FR-019):
  MemorySettings,
}

const BASE_URL = import.meta.env.VITE_API_URL ?? ''

// The server issues one of two cookie names depending on the request's TLS
// state. TLS: __Host-csrf (browser enforces Secure + Path=/ + no Domain).
// Plain HTTP: csrf (no __Host- prefix, Secure=false) — __Host- cookies are
// silently dropped by browsers on non-localhost plain-HTTP origins.
// Keep both constants in sync with pkg/gateway/middleware/csrf.go.
const CSRF_COOKIE_NAME = '__Host-csrf'
const CSRF_COOKIE_NAME_HTTP = 'csrf'
const CSRF_HEADER_NAME = 'X-CSRF-Token'
const STATE_CHANGING_METHODS = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])

// CSRF_EXEMPT_PATHS lists state-changing endpoints whose handler's job is to
// ISSUE the __Host-csrf cookie. They can't require the cookie to be present
// — that's the chicken-and-egg bootstrap problem. Keep this list in sync
// with pkg/gateway/middleware/csrf.go `exemptPaths` for /api/v1/* entries.
// Paths here are compared against the /api/v1/-prefixed URL.
//
// Why each entry is here:
//   - /api/v1/onboarding/complete — called on fresh install (no cookie exists).
//   - /api/v1/auth/login — called on first load of an existing install
//     (refresh, new tab); cookie may be absent until the login succeeds.
//   - /api/v1/auth/register-admin — first-boot admin account creation.
const CSRF_EXEMPT_PATHS = new Set<string>([
  '/api/v1/onboarding/complete',
  '/api/v1/onboarding/probe-provider',
  '/api/v1/auth/login',
  '/api/v1/auth/register-admin',
])

// readCSRFCookie parses document.cookie and returns the __Host-csrf value,
// or null if the cookie is absent. We intentionally do not cache — cookies
// can change after login/logout/onboarding and caching would cause stale
// tokens on the next state-changing call.
function readCSRFCookie(): string | null {
  if (typeof document === 'undefined') return null
  // Try __Host-csrf (TLS) first, then the plain-HTTP fallback.
  for (const name of [CSRF_COOKIE_NAME, CSRF_COOKIE_NAME_HTTP]) {
    const prefix = `${name}=`
    for (const part of document.cookie.split(';')) {
      const trimmed = part.trim()
      if (trimmed.startsWith(prefix)) {
        const raw = trimmed.slice(prefix.length)
        // Apply decodeURIComponent defensively: if the browser percent-encoded
        // the cookie value (e.g. standard base64 "=", "+", "/"), we decode it
        // so the header value matches what the server originally set. If
        // decoding fails (malformed sequence such as a lone "%"), fall back to
        // the raw string and let the server compare verbatim.
        try {
          return decodeURIComponent(raw)
        } catch {
          return raw
        }
      }
    }
  }
  return null
}

function getAuthHeaders(): HeadersInit {
  const token = sessionStorage.getItem('omnipus_auth_token') ?? localStorage.getItem('omnipus_auth_token')
  return token ? { Authorization: `Bearer ${token}` } : {}
}

// buildHeaders composes the standard request headers, layering (in order):
// content-type → bearer auth → CSRF header → caller overrides. The CSRF
// header is added unconditionally when the cookie exists; safe GETs pass
// it through too because doing so is harmless and keeps the code simple.
function buildHeaders(extra?: HeadersInit): HeadersInit {
  const csrf = readCSRFCookie()
  return {
    'Content-Type': 'application/json',
    ...getAuthHeaders(),
    ...(csrf ? { [CSRF_HEADER_NAME]: csrf } : {}),
    ...extra,
  }
}

// isPathCSRFExempt checks whether a state-changing call can skip the
// client-side cookie-presence check. Only onboarding-complete is exempt —
// see CSRF_EXEMPT_PATHS.
function isPathCSRFExempt(apiPath: string): boolean {
  return CSRF_EXEMPT_PATHS.has(`/api/v1${apiPath}`)
}

async function request<T>(path: string, init?: RequestInit, schema?: ZodType<T>): Promise<T> {
  // Client-side CSRF gate: reject state-changing calls that would be
  // guaranteed to 403 at the server. This gives a clear error immediately
  // instead of a cryptic "403 csrf cookie missing" from the network tab
  // and also prevents a cascade of dependent requests firing during a
  // broken auth state.
  //
  // We synthesize an ApiError with status 403 + a CSRF-specific code so
  // callers can branch on `err.code === 'csrf_missing'` without having to
  // string-match the message. The message text is preserved verbatim from
  // the previous implementation for any caller that hasn't migrated yet.
  const method = (init?.method ?? 'GET').toUpperCase()
  if (
    STATE_CHANGING_METHODS.has(method) &&
    !isPathCSRFExempt(path) &&
    readCSRFCookie() === null
  ) {
    throw new ApiError(
      403,
      `CSRF cookie missing — cannot ${method} ${path}. ` +
        `Log in or complete onboarding first so the server can issue the CSRF cookie.`,
      { code: 'csrf_missing' },
    )
  }

  let res: Response
  try {
    res = await fetch(`${BASE_URL}/api/v1${path}`, {
      ...init,
      headers: buildHeaders(init?.headers),
    })
  } catch (cause) {
    // Transport-level failure — DNS, TCP, TLS, AbortController, or fetch threw
    // for any other reason. Surface as a status-0 ApiError so callers can
    // distinguish "browser couldn't reach the server" from "server said no".
    throw new ApiError(0, 'Network unavailable. Check your connection.', { cause })
  }
  if (!res.ok) {
    throw await ApiError.fromResponse(res)
  }

  // Empty-body responses (HTTP 204 No Content, 205 Reset Content, or any 2xx
  // that explicitly advertises a zero-length body) carry no JSON to parse.
  // DELETE/PUT/POST handlers that return 204 would otherwise throw on
  // res.json() ("Unexpected end of JSON input"), making a successful mutation
  // appear to fail. Detect the DEFINITIVE empty-body signals and resolve with
  // `undefined` — the correct value for the Promise<void> callers (deleteTask,
  // deleteSkill, deleteMcpServer, deleteCredential, deleteSchedule,
  // clearAllSessions, …). We deliberately do NOT key off Content-Type here: a
  // non-JSON body with actual content (e.g. an HTML error page served with a
  // misconfigured 200) is a different failure that must still flow to the
  // schema/JSON-parse path below so it surfaces as an ApiSchemaError, not a
  // silent success.
  const contentLength = res.headers.get('Content-Length')
  if (res.status === 204 || res.status === 205 || contentLength === '0') {
    return undefined as T
  }

  // Parse the response body, handling non-JSON (e.g. unexpected HTML 200)
  // gracefully — surface as ApiSchemaError with the raw text as rawBody so
  // callers can see what the server actually sent.
  let body: unknown
  try {
    body = await res.json() as unknown
  } catch (cause) {
    // A JSON-parse failure on a body with no Content-Length header is the
    // common shape of an empty 2xx (some gateways omit Content-Length on a
    // bodyless 200/202 instead of using 204). For a caller that passed NO
    // schema — i.e. a Promise<void> mutation — that is a legitimate success,
    // so resolve with undefined rather than throwing. When a schema WAS
    // provided the body genuinely should have been JSON, so surface the
    // ApiSchemaError as before.
    if (schema === undefined && (contentLength === null || contentLength === '')) {
      return undefined as T
    }
    const rawText = String(cause instanceof Error ? cause.message : cause)
    if (schema !== undefined) {
      _recordApiSchemaError(`${method} /api/v1${path}`, 1)
      const schemaErr = new ApiSchemaError(
        `${method} /api/v1${path}`,
        [{ path: [], message: 'Response is not valid JSON' }],
        rawText,
      )
      void maybeDevToast(`[api] Non-JSON response: ${path}`, `${method}:${path}:non-json`)
      throw schemaErr
    }
    // No schema — throw a generic ApiError for non-JSON bodies on non-2xx
    // (we already checked res.ok above, so a JSON parse error here on a 200
    // is itself unexpected; surface as a 0-status transport error).
    throw new ApiError(0, `Response from ${path} is not valid JSON`, { cause })
  }

  if (import.meta.env.DEV && schema === undefined) {
    console.warn(`[api] ${method} /api/v1${path}: no Zod schema — response validation skipped. Add schema from src/lib/api/generated/schemas.ts.`)
  }

  // When a Zod schema is provided, validate the response body against it.
  // On failure: throw ApiSchemaError (+ dev toast). Never silently return
  // schema-invalid data — callers that need the old unchecked behaviour
  // should not pass a schema.
  if (schema !== undefined) {
    const result = schema.safeParse(body)
    if (!result.success) {
      _recordApiSchemaError(`${method} /api/v1${path}`, result.error.issues.length)
      const schemaErr = new ApiSchemaError(
        `${method} /api/v1${path}`,
        result.error.issues.map((i) => ({ path: i.path as (string | number)[], message: i.message })),
        body,
      )
      const first = schemaErr.zodIssues[0]
      void maybeDevToast(`[api] Schema mismatch: ${path} — ${first?.message ?? 'unknown'}`, `${method}:${path}:schema`)
      throw schemaErr
    }
    return result.data
  }

  return body as T
}

// ── Agents ────────────────────────────────────────────────────────────────────

// 'none' is a UI-only sentinel meaning "inherit global default" — it is NOT a
// valid contract value and must be stripped before submission (see AgentProfile.tsx).
// The wire format only accepts: workspace | workspace+net | host | off.
export type SandboxProfile = 'none' | 'workspace' | 'workspace+net' | 'host' | 'off' // not-wire-format: 'none' is UI-only; strip to undefined before send

export interface AgentShellPolicy { // not-wire-format: SPA-internal helper type — the shell_policy field on the generated Agent type is an inline anonymous object; this interface is never sent to or received from the gateway as a standalone value
  enable_deny_patterns?: boolean
  custom_deny_patterns?: string[]
}

// Agent — re-exported from generated openapi-types (contract-first #8).
// The generated type is the source of truth; see contracts/components/schemas/Agent.yaml.

// AgentKind — the agent's "kind" axis. Derived directly from the generated
// Agent['type'] so it is NOT a parallel wire type (Constraint #8): the generated
// Agent remains the single source of truth. Re-used everywhere the literal union
// 'core' | 'custom' | 'system' | 'worker' was previously repeated inline (session
// store, ToolsAndPermissions) so the literal isn't scattered.
export type AgentKind = NonNullable<Agent['type']>

// isWorker — a worker is a delegation-only labour agent: never a chat target,
// never a channel routing default, never a schedule owner. Used by the chat
// switcher, channel-routing picker, and schedule-owner picker to filter workers
// out of those selection sites. Accepts a loose shape so it works on partial
// agent objects too.
//
// W2 (agent-form-requirements): recognise both Subagent and subagent_3p (the new
// wire enum values for the user-creatable worker types). The legacy "worker"
// value is the build-time/seed config constant and is NOT emitted by the
// gateway; it is left here as a defensive fallback so callers don't break on
// stale payloads.
export function isWorker(a: { type?: string | null }): boolean {
  return a.type === 'Subagent' || a.type === 'subagent_3p' || a.type === 'worker'
}

export function fetchAgents(): Promise<Agent[]> {
  return request<Agent[]>('/agents', undefined, z.array(AgentSchema) as ZodType<Agent[]>)
}

export function fetchAgent(id: string): Promise<Agent> {
  return request<Agent>(`/agents/${encodeURIComponent(id)}`, undefined, AgentSchema as ZodType<Agent>)
}

export function createAgent(data: AgentCreateRequest): Promise<Agent> {
  return request<Agent>('/agents', { method: 'POST', body: JSON.stringify(data) }, AgentSchema as ZodType<Agent>)
}

export function updateAgent(id: string, data: AgentUpdateRequest): Promise<Agent> {
  return request<Agent>(`/agents/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(data) }, AgentSchema as ZodType<Agent>)
}

// Wave 5 / spec §6.1 BDD #15: Edit slide-over footer Delete button.
// DELETE /api/v1/agents/{id} — handler rejects locked (core/system) agents
// with 403 + code `agent_locked`. Custom (non-locked) agents return 204 No
// Content; 404 for unknown ids. The wrapper uses `request<void>` (no
// response body) so the mutation consumer only sees success/failure.
export function deleteAgent(id: string): Promise<void> {
  return request<void>(`/agents/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

// Spec-4 FR-4.2 — external-CLI runner connection test.
// POST /api/v1/agents/{id}/runner/test validates the agent's configured external
// CLI (claude-code / codex / opencode) without running any real agent work:
// binary present + version handshake + authenticated. The response carries a
// distinct `reason` (missing-binary | unauthenticated | handshake-failed |
// unknown-cli | not-external-cli) so the UI can show a precise remedy. The
// generated RunnerTestResponse type + Zod schema are the source of truth.
export function testAgentRunner(id: string): Promise<RunnerTestResponse> {
  return request<RunnerTestResponse>(
    `/agents/${encodeURIComponent(id)}/runner/test`,
    { method: 'POST' },
    RunnerTestResponseSchema as ZodType<RunnerTestResponse>,
  )
}

// AgentSession — re-exported from generated openapi-types (no local body needed).

export function fetchAgentSessions(agentId: string): Promise<AgentSession[]> {
  return request<AgentSession[]>(`/agents/${encodeURIComponent(agentId)}/sessions`, undefined, z.array(AgentSessionSchema))
}

// ── Sessions ──────────────────────────────────────────────────────────────────

export interface Session { // not-wire-format: SPA transformation type produced by rawToSession(). Flattens the nested stats sub-object from the wire RawSession into top-level fields (message_count, total_tokens, total_cost). The wire shape is the generated Session schema; this SPA shape is intentionally different.
  id: string
  agent_id: string
  title: string
  type: 'chat' | 'task' | 'channel' | 'scheduled' | 'heartbeat'
  status?: 'active' | 'archived' | 'interrupted'
  task_id?: string
  workspace_id?: string
  created_at: string
  updated_at: string
  message_count: number
  total_tokens?: number
  total_cost?: number
  // Channel identifier that initiated this session (e.g. "webchat", "telegram").
  // Legacy sessions may omit this field; callers should treat undefined as "webchat".
  channel?: string
  // Multi-agent session fields — present on sessions created with the joined
  // session model. For legacy single-agent sessions these are absent; callers
  // should fall back to [agent_id] when agent_ids is undefined.
  agent_ids?: string[]      // all agents that participated in this session
  active_agent_id?: string  // the agent currently handling this session
  // Computed server-side (FR-028, A2/G-01): true while the heartbeat member
  // whose session_id matches this session's id has heartbeat.enabled = true.
  // When true the SPA pins the session at the top of the panel and hides its
  // delete (trash) button; DELETE /sessions/{id} returns 409 server-side.
  protected?: boolean
}

interface _RawSessionInternal { // not-wire-format: SPA-internal adapter that renames nested stats fields before public Session type; the wire shape is validated via WireSessionSchema, this type only models the pre-transform intermediate
  id: string
  agent_id: string
  title: string
  type?: 'chat' | 'task' | 'channel' | 'scheduled' | 'heartbeat'
  status?: 'active' | 'archived' | 'interrupted'
  task_id?: string
  workspace_id?: string
  created_at: string
  updated_at: string
  channel?: string
  agent_ids?: string[]
  active_agent_id?: string
  protected?: boolean
  stats?: {
    tokens_in: number
    tokens_out: number
    tokens_total: number
    cost: number
    tool_calls: number
    message_count: number
  }
}

// Alias for backward-compat within this file (rawToSession signature).
type RawSession = _RawSessionInternal

function rawToSession(raw: RawSession): Session {
  return {
    id: raw.id,
    agent_id: raw.agent_id,
    title: raw.title,
    // Legacy sessions without a type field default to 'chat'
    type: raw.type ?? 'chat',
    status: raw.status,
    task_id: raw.task_id,
    workspace_id: raw.workspace_id,
    created_at: raw.created_at,
    updated_at: raw.updated_at,
    message_count: raw.stats?.message_count ?? 0,
    total_tokens: raw.stats?.tokens_total,
    total_cost: raw.stats?.cost,
    channel: raw.channel,
    agent_ids: raw.agent_ids,
    active_agent_id: raw.active_agent_id,
    // Computed server-side: true while the heartbeat member is enabled (FR-028).
    protected: raw.protected,
  }
}

// ── Role-discriminated Message union (#3) ─────────────────────────────────────
//
// Each role declares exactly the statuses that are legal for it:
//   user      — 'done' (normal) | 'error' (failed send, retriable via UserMessageRetryButton)
//   assistant — 'streaming' | 'done' | 'error' | 'interrupted'
//   system    — 'done' (informational banners; no error/streaming states)
//
// Using a discriminated union means that `(role:'user', status:'error')` is
// representable AND handled: TypeScript exhausts the union in switch/if blocks
// so adding a new role or status without updating renderers becomes a type error.
//
// Consumers that accept any message use the union alias `Message` (unchanged
// surface area). Narrowing by role gives the role-specific status set.

interface MessageBase { // not-wire-format
  id: string
  session_id?: string
  content: string
  timestamp: string
  tokens?: number
  cost?: number
  /**
   * Authoring agent id (assistant messages). Carried through from the wire
   * `agent_id` so cold-load (REST) transcripts render each message under its
   * true author after a handover — matching the WS-replay path which already
   * populates ChatMessage.agentId from each frame's agent_id.
   */
  agentId?: string
  /**
   * Per-turn model record (Phase 1, FR-013). Only populated for assistant
   * messages that have a recorded model on the wire. Legacy turns and
   * non-assistant messages leave this undefined. Empty string is treated
   * the same as undefined at render time (per spec §18 Q6: no placeholder
   * text — just don't show anything when the field is empty).
   */
  model?: string
}

export interface UserMessage extends MessageBase { // not-wire-format: SPA-internal user message. Status 'error' means the WS send failed; Retry button re-sends the content.
  role: 'user'
  /** 'done' — delivered to gateway. 'error' — WS send failed; show Retry. */
  status?: 'done' | 'error'
  tool_calls?: never
}

export interface AssistantMessage extends MessageBase { // not-wire-format: SPA-internal assistant message. Status diverges from wire ('streaming'/'done' vs wire 'ok'/'error'). tool_calls uses params (not wire parameters). NOT the same as the generated Message schema.
  role: 'assistant'
  /** 'streaming' — turn in progress. 'done' — complete. 'error' — agent error. 'interrupted' — user cancelled. */
  status?: 'streaming' | 'done' | 'error' | 'interrupted'
  tool_calls?: ToolCall[]
}

export interface SystemMessage extends MessageBase { // not-wire-format: SPA-internal system/banner message.
  role: 'system'
  status?: 'done'
  tool_calls?: never
}

/** Union of all SPA-internal message shapes. Discriminate on `role`. */ // not-wire-format
export type Message = UserMessage | AssistantMessage | SystemMessage

export interface ToolCall { // not-wire-format: SPA-internal tool call shape. Uses 'params' for the input parameters while the wire ToolCall schema uses 'parameters'. The status enum also differs (SPA adds 'running'; wire uses 'pending'/'denied'). This type is intentionally different from the generated ToolCall schema.
  id: string
  tool: string
  params: Record<string, unknown>
  result?: unknown
  status: 'running' | 'success' | 'error' | 'cancelled'
  duration_ms?: number
  error?: string
}

// ── Tool Results ──────────────────────────────────────────────────────────────
//
// Typed shapes for the JSON result payloads emitted by specific tools.
// These are parsed from ToolCall.result (which is `unknown` on the wire) by
// the tool-UI components in src/components/chat/tools/.
//
// Pre-unification result shapes: ServeWorkspaceResult, RunInWorkspaceResult.
// New tool-result code on the agent side emits `WebServeResult` (defined in
// WebServeUI.tsx); the two shapes here remain the iframe prop carriers that
// `WebServeBlock` casts into and that `IframePreview` consumes — so they
// are NOT dead. Replay paths (chat transcripts saved before unification)
// also rely on these shapes when rendering historical sessions.

/**
 * Result shape originally emitted by the legacy `serve_workspace` tool.
 *
 * Used as the static-mode iframe prop carrier on the canonical `web_serve`
 * code path: `WebServeBlock` casts the static-mode result of `web_serve`
 * into this shape and feeds it to `IframePreview` (kind=`'web_serve'`).
 * Also produced by the legacy `serve_workspace` tool in pre-unification
 * chat transcripts so the SPA can render historical sessions.
 *
 * Tool-result authors should produce `WebServeResult`; only this shape is
 * exposed to the iframe layer.
 *
 * `path` is the relative preview path (e.g. `"/preview/<agent>/<token>/"`
 * or the legacy `"/serve/<agent>/<token>/"`).
 * `url` is the absolute URL preserved for transcript replay safety — old
 * transcripts may contain legacy `0.0.0.0` URLs that the SPA rewrites via
 * `rewriteLegacyURL` in `src/lib/preview-url.ts`.
 */
export interface ServeWorkspaceResult { // not-wire-format: parsed from ToolCall.result (typed as unknown on the wire). Not a direct REST or WebSocket response schema — this is a tool-result payload shape that the SPA casts from the opaque result field. See WebServeBlock in chat/tools/.
  /** Relative preview path, e.g. `"/preview/<agent>/<token>/"`. */
  path: string
  /** Absolute URL — preserved for replay safety; may contain legacy hosts. */
  url: string
  /** ISO-8601 token expiry timestamp. */
  expires_at: string
}

/**
 * Result shape originally emitted by the legacy `run_in_workspace` tool.
 *
 * Used as the dev-mode iframe prop carrier on the canonical `web_serve`
 * code path: `WebServeBlock` casts the dev-mode result of `web_serve`
 * into this shape and feeds it to `IframePreview` (kind=`'run_in_workspace'`,
 * which is a mode discriminator, not a current tool name). Also produced
 * by the legacy `run_in_workspace` tool in pre-unification chat transcripts.
 *
 * Tool-result authors should produce `WebServeResult`; only this shape is
 * exposed to the iframe layer.
 *
 * `path` is the relative dev preview path (e.g. `"/preview/<agent>/<token>/"`
 * or the legacy `"/dev/<agent>/<token>/"`).
 * `url` is the absolute URL preserved for transcript replay safety.
 * `command` is the command string that was executed.
 * `port` is the local port the dev server is listening on (inside the workspace).
 */
export interface RunInWorkspaceResult { // not-wire-format: parsed from ToolCall.result (typed as unknown on the wire). Not a direct REST or WebSocket response schema — this is a tool-result payload shape that the SPA casts from the opaque result field. See WebServeBlock in chat/tools/.
  /** Relative dev path, e.g. `"/preview/<agent>/<token>/"`. */
  path: string
  /** Absolute URL — preserved for replay safety; may contain legacy hosts. */
  url: string
  /** ISO-8601 token expiry timestamp. */
  expires_at: string
  /** The command string that was executed (e.g. `"npm run dev"`). */
  command: string
  /** Local port the dev server is listening on inside the workspace. */
  port: number
}

// ── Wire ToolCall / Message adapters ─────────────────────────────────────────
//
// The wire ToolCall schema uses `parameters` (matching the Go struct tag
// `json:"parameters,omitempty"`). The SPA-internal ToolCall uses `params`.
// These adapter types are NOT new wire-format types — they are aliases over
// the generated Message/ToolCall wire shape used only in the transform layer.
// They carry a `// not-wire-format` marker as adapter aliases per CLAUDE.md #8.

interface RawToolCall { // not-wire-format: adapter alias over the generated ToolCall wire schema. Used only in rawToToolCall() to rename `parameters`→`params` before the SPA consumer sees it. The wire name is `parameters` (Go json tag); the SPA-internal name is `params` (ToolCall interface above). Never sent to or received as a standalone type from the gateway.
  id: string
  tool: string
  status: 'success' | 'error' | 'pending' | 'denied' | 'running' | 'cancelled'
  duration_ms?: number
  parameters?: Record<string, unknown>
  result?: unknown
  parent_tool_call_id?: string
}

interface RawMessage { // not-wire-format: adapter alias over the generated Message wire schema. Used only in rawToMessage() to delegate ToolCall transformation. The wire `status` enum values differ from the SPA's ('ok'|'error'|'interrupted' vs 'streaming'|'done'|'error'|'interrupted'). Never sent to or received as a standalone type from the gateway.
  id: string
  type?: 'message' | 'compaction' | 'system'
  role?: 'user' | 'assistant' | 'system'
  content?: string
  summary?: string
  timestamp: string
  tokens?: number
  cost?: number
  status?: 'ok' | 'error' | 'interrupted'
  attachments?: Attachment[]
  tool_calls?: RawToolCall[]
  agent_id: string
  messages_compacted?: number
  /**
   * Per-turn model record (Phase 1, FR-013). Forwarded to AssistantMessage
   * so the UI can render which model produced each assistant turn. Absent
   * on legacy turns; legacy turns must NOT show any model info (no
   * placeholder text per spec §18 Q6).
   */
  model?: string
}

function rawToToolCall(raw: RawToolCall): ToolCall {
  // Map wire status → SPA status.
  // Wire values: 'success' | 'error' | 'pending' | 'denied' | 'running' | 'cancelled'
  // SPA values:  'running' | 'success' | 'error' | 'cancelled'
  //
  // 'pending' → 'running': the SPA uses 'running' for in-progress calls; in a
  //   completed transcript 'pending' should never occur, but we map it safely.
  // 'denied'  → 'cancelled': the tool call was rejected by policy — the SPA
  //   has no 'denied' state, so 'cancelled' is the closest accurate status.
  let status: ToolCall['status']
  switch (raw.status) {
    case 'success':
    case 'error':
    case 'running':
    case 'cancelled':
      status = raw.status
      break
    case 'denied':
      status = 'cancelled'
      break
    case 'pending':
    default:
      status = 'running'
      break
  }
  return {
    id: raw.id,
    tool: raw.tool,
    status,
    params: raw.parameters ?? {},
    result: raw.result,
    duration_ms: raw.duration_ms,
    error: undefined,
  }
}

function rawToMessage(raw: RawMessage): Message {
  const role = raw.role ?? 'assistant'
  const baseStatus = raw.status === 'ok' ? ('done' as const) : raw.status
  // #3: construct the correct discriminated variant based on role.
  // Tool calls only appear on assistant messages per the wire schema, so the
  // cast here is correct and the compiler accepts it once role is narrowed.
  if (role === 'user') {
    return {
      id: raw.id,
      session_id: undefined,
      role: 'user',
      content: raw.content ?? raw.summary ?? '',
      timestamp: raw.timestamp,
      tokens: raw.tokens,
      cost: raw.cost,
      agentId: raw.agent_id || undefined,
      status: (baseStatus === 'done' || baseStatus === 'error') ? baseStatus : 'done',
    } satisfies UserMessage
  }
  if (role === 'system') {
    return {
      id: raw.id,
      session_id: undefined,
      role: 'system',
      content: raw.content ?? raw.summary ?? '',
      timestamp: raw.timestamp,
      tokens: raw.tokens,
      cost: raw.cost,
      agentId: raw.agent_id || undefined,
      status: 'done',
    } satisfies SystemMessage
  }
  // role === 'assistant' (default)
  // Per-turn model record (FR-013). Forwarded to AssistantMessage so the
  // UI can render which model produced each assistant turn. The wire
  // field is optional — legacy turns lack it; those must NOT show a
  // placeholder (spec §18 Q6). Empty string is normalized to undefined
  // here so the renderer's `if (model) ` check covers both cases.
  const rawModel = raw.model?.trim()
  const modelField = rawModel && rawModel.length > 0 ? rawModel : undefined
  return {
    id: raw.id,
    session_id: undefined,
    role: 'assistant',
    content: raw.content ?? raw.summary ?? '',
    timestamp: raw.timestamp,
    tokens: raw.tokens,
    cost: raw.cost,
    // Carry the per-message authoring agent so a reloaded handover transcript
    // renders each assistant turn under its true author (not the active agent).
    agentId: raw.agent_id || undefined,
    // Wire status is 'ok'→'done' | 'error' | 'interrupted'. 'streaming' is SPA-only
    // (never on persisted wire messages) so this branch guards for undefined only.
    status: (baseStatus === 'done' || baseStatus === 'error' || baseStatus === 'interrupted') ? baseStatus : 'done',
    tool_calls: raw.tool_calls?.map(rawToToolCall),
    ...(modelField ? { model: modelField } : {}),
  } satisfies AssistantMessage
}

export async function fetchSessions(agentId?: string, type?: Session['type']): Promise<Session[]> {
  const params: Record<string, string> = {}
  if (agentId) params.agent_id = agentId
  if (type) params.type = type
  const qs = Object.keys(params).length > 0 ? '?' + new URLSearchParams(params).toString() : ''
  // The OpenAPI contract for GET /sessions describes a oneOf response: a
  // plain JSON array when there are no partial errors, OR
  // {sessions: [...], partial_errors: [...]} when one or more agents failed
  // to list. The previous code validated only the array variant — when
  // partial_errors fired (e.g. a session with a missing .context entry),
  // Zod rejected the whole response and the session panel showed empty.
  // Accept both shapes so legitimate sessions still render alongside any
  // partial-error info.
  const unionSchema = z.union([
    z.array(WireSessionSchema),
    z.object({
      sessions: z.array(WireSessionSchema),
      partial_errors: z.array(z.string()).optional(),
    }),
  ])
  const resp = await request<unknown>(`/sessions${qs}`, undefined, unionSchema as ZodType<unknown>)
  const raw: RawSession[] = Array.isArray(resp)
    ? (resp as RawSession[])
    : ((resp as { sessions: RawSession[] }).sessions ?? [])
  return raw.map(rawToSession)
}

export async function fetchSessionMessages(sessionId: string): Promise<Message[]> {
  // Validate with the wire Message schema first, then transform each message
  // so that tool_calls[].parameters is renamed to tool_calls[].params.
  const raw = await request<RawMessage[]>(
    `/sessions/${encodeURIComponent(sessionId)}/messages`,
    undefined,
    z.array(WireMessageSchema) as ZodType<RawMessage[]>,
  )
  return raw.map(rawToMessage)
}

export async function installSkillFromFile(content: string, filename: string): Promise<void> {
  await request<void>('/skills/install', {
    method: 'POST',
    body: JSON.stringify({ content, filename }),
  })
}

/**
 * searchSkills queries the ClawHub marketplace registry via
 * GET /api/v1/skills/search?q=<query>&limit=<n>. Returns an array of
 * SkillSearchResult (marketplace hits, NOT installed skills). The backend
 * returns 400 for an empty/blank query and 502 when the registry is
 * unreachable — both surface as a typed ApiError to the caller.
 */
export async function searchSkills(q: string, limit = 20): Promise<SkillSearchResult[]> {
  const params = new URLSearchParams({ q, limit: String(limit) })
  return request<SkillSearchResult[]>(
    `/skills/search?${params.toString()}`,
    undefined,
    z.array(SkillSearchResultSchema) as ZodType<SkillSearchResult[]>,
  )
}

/**
 * installSkillBySlug installs a marketplace skill by its slug via
 * POST /api/v1/skills/install with a SkillInstallRequest body
 * ({ slug, version? }). Returns the freshly installed Skill on success.
 * The backend returns 409 when the skill is already installed and 502 when
 * the registry is unreachable.
 */
export async function installSkillBySlug(slug: string, version?: string): Promise<Skill> {
  const body: SkillInstallRequest = version ? { slug, version } : { slug }
  return request<Skill>(
    '/skills/install',
    {
      method: 'POST',
      body: JSON.stringify(body),
    },
    SkillSchema as ZodType<Skill>,
  )
}

/**
 * fetchSkillMarketplaceStatus reports whether a skill marketplace is enabled
 * via GET /api/v1/skills/marketplace. When `enabled` is false the SPA hides
 * the search/browse UI and offers only file-based install — the backend
 * returns 409 for /skills/search and /skills/install in that state.
 */
export async function fetchSkillMarketplaceStatus(): Promise<SkillMarketplaceStatus> {
  return request<SkillMarketplaceStatus>(
    '/skills/marketplace',
    undefined,
    SkillMarketplaceStatusSchema as ZodType<SkillMarketplaceStatus>,
  )
}

export interface SessionDetail { // not-wire-format: SPA-internal detail type. Uses the SPA-internal Session (stats-flattened) and SPA-internal Message (params field), not the wire-format generated SessionDetail. See fetchSessionDetail() which transforms the raw response.
  session: Session
  messages: Message[]
  agent_removed?: boolean
}

export async function fetchSessionDetail(sessionId: string): Promise<SessionDetail> {
  // Validate with the wire SessionDetail schema (session + messages array),
  // then transform both the nested session stats and each message's tool_calls.
  type RawSessionDetail = { session: RawSession; messages: RawMessage[]; agent_removed?: boolean }
  const raw = await request<RawSessionDetail>(
    `/sessions/${encodeURIComponent(sessionId)}`,
    undefined,
    WireSessionDetailSchema as ZodType<RawSessionDetail>,
  )
  return {
    session: rawToSession(raw.session),
    messages: raw.messages.map(rawToMessage),
    agent_removed: raw.agent_removed,
  }
}

export async function createSession(agentId: string): Promise<Session> {
  // Wire returns the wire Session shape (nested stats); transform to SPA Session.
  const raw = await request<RawSession>('/sessions', {
    method: 'POST',
    body: JSON.stringify({ agent_id: agentId }),
  }, WireSessionSchema as ZodType<RawSession>)
  return rawToSession(raw)
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
  }
  security: {
    policy_mode: 'allow' | 'deny'
    exec_approval: 'auto' | 'ask' | 'deny'
    // Prompt guard strictness is owned by the dedicated /security/prompt-guard
    // endpoint since Wave 3. This field is still populated on read for
    // backward compatibility but must NOT be sent on updateConfig calls.
    prompt_injection_level?: 'off' | 'low' | 'medium' | 'high'
    daily_cost_cap?: number
    exec_timeout_seconds?: number
    max_background_seconds?: number
    enable_deny_patterns?: boolean
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
      // Previously missing — silently stripped by rawToFrontendConfig/frontendToRawConfig before this fix
      model_name?: string
      provider?: string
    }
  }
}

const VALID_POLICY_MODES = ['allow', 'deny'] as const
const VALID_EXEC_APPROVALS = ['auto', 'ask', 'deny'] as const
const VALID_INJECTION_LEVELS = ['off', 'low', 'medium', 'high'] as const

function validEnum<T extends string>(value: unknown, valid: readonly T[], fallback: T): T {
  if (typeof value === 'string' && (valid as readonly string[]).includes(value)) return value as T
  if (value !== undefined && value !== null) {
    _configCoercionCount++
    if (import.meta.env.DEV) {
      console.warn(`[api] validEnum coercion: ${JSON.stringify(value)} → ${fallback}`)
    }
  }
  return fallback
}

// cast provides a type-safe wrapper around the repetitive (raw.foo ?? fallback) as T pattern.
function cast<T>(obj: unknown, fallback: T): T {
  return (obj ?? fallback) as T
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
      bind_address: cast<string>(gateway.host, '127.0.0.1'),
      port: cast<number>(gateway.port, 8080),
      token: gateway.token as string | undefined,
      hot_reload: gateway.hot_reload as boolean | undefined,
      log_level: gateway.log_level as string | undefined,
      dev_mode_bypass: gateway.dev_mode_bypass as boolean | undefined,
    },
    security: {
      policy_mode: validEnum(security.policy_mode, VALID_POLICY_MODES, 'deny'),
      exec_approval: validEnum(security.exec_approval, VALID_EXEC_APPROVALS, 'ask'),
      prompt_injection_level: validEnum(security.prompt_injection_level, VALID_INJECTION_LEVELS, 'medium'),
      daily_cost_cap: security.daily_cost_cap as number | undefined,
      exec_timeout_seconds: security.exec_timeout_seconds as number | undefined,
      max_background_seconds: security.max_background_seconds as number | undefined,
      enable_deny_patterns: security.enable_deny_patterns as boolean | undefined,
      rate_limits: {
        max_tokens_per_day: rateLimits.max_tokens_per_day as number | undefined,
        max_cost_per_day: rateLimits.max_cost_per_day as number | undefined,
        max_agent_llm_calls_per_hour: rateLimits.max_agent_llm_calls_per_hour as number | undefined,
        max_agent_tool_calls_per_minute: rateLimits.max_agent_tool_calls_per_minute as number | undefined,
      },
    },
    data: {
      session_retention_days: cast<number>(retention.session_days, 90),
    },
    agents: {
      defaults: {
        default_agent_id: agentDefaults.default_agent_id as string | undefined,
        model_name: agentDefaults.model_name as string | undefined,
        provider: agentDefaults.provider as string | undefined,
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
    // dev_mode_bypass is intentionally omitted — PUT /config blocks that field.
    raw.gateway = gw
  }
  if (data.security) {
    const sec: Record<string, unknown> = {}
    if (data.security.policy_mode !== undefined) sec.policy_mode = data.security.policy_mode
    if (data.security.exec_approval !== undefined) sec.exec_approval = data.security.exec_approval
    // prompt_injection_level intentionally omitted — owned by PUT /security/prompt-guard.
    if (data.security.daily_cost_cap !== undefined) sec.daily_cost_cap = data.security.daily_cost_cap
    if (data.security.exec_timeout_seconds !== undefined) sec.exec_timeout_seconds = data.security.exec_timeout_seconds
    if (data.security.max_background_seconds !== undefined) sec.max_background_seconds = data.security.max_background_seconds
    if (data.security.enable_deny_patterns !== undefined) sec.enable_deny_patterns = data.security.enable_deny_patterns
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

// ── Providers ─────────────────────────────────────────────────────────────────

// Provider — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/Provider.yaml.

export function fetchProviders(): Promise<Provider[]> {
  return request<Provider[]>('/providers', undefined, z.array(ProviderSchema) as ZodType<Provider[]>)
}

// configureProvider sets a model/provider's API key, endpoint, and/or model.
// Post-onboarding this PUT is re-auth gated (Spec-6 FR-12.2 / FR-6.6): the server
// rejects it with 403 unless a single-use consent token (from reAuth) is replayed
// in the X-Reauth-Token header. The token is OPTIONAL here because the same route
// is used during onboarding, where no authenticated user exists yet and the gate
// is skipped (see pkg/gateway/rest.go provider PUT handler).
export function configureProvider(
  id: string,
  apiKey?: string,
  endpoint?: string,
  model?: string,
  reAuthToken?: string,
  models?: string[],
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
  return request<Provider>(`/providers/${id}`, {
    method: 'PUT',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
    body: JSON.stringify(body),
  }, ProviderSchema as ZodType<Provider>)
}

export function testProvider(id: string): Promise<OperationResult> {
  return request<OperationResult>(`/providers/${id}/test`, { method: 'POST' }, OperationResultSchema as ZodType<OperationResult>)
}

// refreshProviderModels re-fetches a provider's model catalogue. For a provider
// WITH a live /models endpoint (has_models_endpoint=true) the backend re-queries
// upstream and returns the refreshed list; for an endpoint-less provider it
// returns the stored operator-supplied slug catalogue (nothing to refresh).
// POST /api/v1/providers/{id}/refresh-models → Provider (contract type).
export function refreshProviderModels(id: string): Promise<Provider> {
  return request<Provider>(`/providers/${id}/refresh-models`, { method: 'POST' }, ProviderSchema as ZodType<Provider>)
}

// fetchCliDetect probes the host for installed external CLIs (claude-code /
// codex / opencode), used by the Agents screen to gate the "+ External
// subagent" runtime choices. UAT fix: this MUST go through the authed
// `request()` wrapper so the bearer token rides along — the previous raw
// `fetch('/api/v1/system/cli-detect')` sent no auth header and got a 401,
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

export function rotateGatewayToken(): Promise<{ token: string }> {
  return request('/config/gateway/rotate-token', { method: 'POST' }, RotateTokenResponseSchema as ZodType<{ token: string }>)
}

// ── Tasks (unified Sprint 2 model) ───────────────────────────────────────────
//
// One entity replaces both the legacy workflow Task and GTD BoardTask outright.
// All types are from generated openapi-types (contract-first #8).
// See contracts/components/schemas/Task.yaml / TaskCreateRequest.yaml /
// TaskUpdateRequest.yaml.
//
// Endpoints:
//   GET    /tasks              → Task[]   (list, workspace-scoped)
//   POST   /tasks              → Task     (create, lands in inbox)
//   GET    /tasks/{id}         → Task
//   PATCH  /tasks/{id}         → Task     (partial update — method is PATCH)
//   DELETE /tasks/{id}         → void
//   GET    /tasks/{id}/subtasks → Task[]
//   PUT    /tasks/{id}/todos    → Task     (replace checklist atomically)
//   PUT    /tasks/{id}/dependencies → Task (replace blocked_by atomically)
//
// "Start" semantics: there is no /start endpoint. Set status=in_progress via
// PATCH to start a task (drag or Run button).

export const tasksQueryKeys = {
  list: (params?: { workspace_id?: string; status?: string; agent_id?: string; milestone_id?: string; surface?: string }) => {
    const cleaned = params
      ? Object.fromEntries(Object.entries(params).filter(([, v]) => v !== undefined))
      : {}
    return ['tasks', cleaned] as const
  },
  detail: (id: string) => ['tasks', id] as const,
  subtasks: (id: string) => ['tasks', id, 'subtasks'] as const,
}

// Keep boardTasksQueryKeys as an alias so tests and existing queries still compile
// during the transition — it redirects to the same unified key space.
export const boardTasksQueryKeys = tasksQueryKeys

export function fetchTasks(params?: { workspace_id?: string; status?: string; agent_id?: string; milestone_id?: string; surface?: string }): Promise<Task[]> {
  const search = new URLSearchParams()
  if (params?.workspace_id) search.set('workspace_id', params.workspace_id)
  if (params?.status) search.set('status', params.status)
  if (params?.agent_id) search.set('agent_id', params.agent_id)
  if (params?.milestone_id) search.set('milestone_id', params.milestone_id)
  if (params?.surface) search.set('surface', params.surface)
  const qs = search.toString() ? '?' + search.toString() : ''
  return request<Task[]>(`/tasks${qs}`, undefined, z.array(TaskSchema) as ZodType<Task[]>)
}

// Keep fetchBoardTasks as an alias so existing call-sites compile during transition.
export function fetchBoardTasks(params?: { workspace_id?: string; status?: string; agent_id?: string; milestone_id?: string }): Promise<Task[]> {
  return fetchTasks(params)
}

export function fetchTask(id: string): Promise<Task> {
  return request<Task>(`/tasks/${encodeURIComponent(id)}`, undefined, TaskSchema as ZodType<Task>)
}

export function fetchSubtasks(taskId: string): Promise<Task[]> {
  return request<Task[]>(`/tasks/${encodeURIComponent(taskId)}/subtasks`, undefined, z.array(TaskSchema) as ZodType<Task[]>)
}

export function createTask(body: TaskCreateRequest): Promise<Task> {
  return request<Task>('/tasks', { method: 'POST', body: JSON.stringify(body) }, TaskSchema as ZodType<Task>)
}

export function updateTask(id: string, data: TaskUpdateRequest): Promise<Task> {
  return request<Task>(`/tasks/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(data) }, TaskSchema as ZodType<Task>)
}

export function setTaskTodos(taskId: string, todos: Todo[]): Promise<Task> {
  return request<Task>(`/tasks/${encodeURIComponent(taskId)}/todos`, { method: 'PUT', body: JSON.stringify(todos) }, TaskSchema as ZodType<Task>)
}

export function setTaskDependencies(taskId: string, blockedBy: string[]): Promise<Task> {
  return request<Task>(`/tasks/${encodeURIComponent(taskId)}/dependencies`, { method: 'PUT', body: JSON.stringify(blockedBy) }, TaskSchema as ZodType<Task>)
}

export function deleteTask(id: string): Promise<void> {
  return request<void>(`/tasks/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

// ── #264 Schedules ──────────────────────────────────────────────────────────────

// Schedule wire types are re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/Schedule*.yaml. The /schedules surface is the
// contract projection over the underlying cron job.

export function fetchSchedules(): Promise<Schedule[]> {
  // GET /schedules returns { schedules: Schedule[] }; flatten to the array the UI consumes.
  return request<ScheduleList>('/schedules', undefined, ScheduleListSchema as ZodType<ScheduleList>)
    .then((list) => list.schedules)
}

export function createSchedule(body: ScheduleCreate): Promise<Schedule> {
  return request<Schedule>('/schedules', { method: 'POST', body: JSON.stringify(body) }, ScheduleSchema as ZodType<Schedule>)
}

export function updateSchedule(id: string, body: ScheduleUpdate): Promise<Schedule> {
  return request<Schedule>(`/schedules/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(body) }, ScheduleSchema as ZodType<Schedule>)
}

export function deleteSchedule(id: string): Promise<void> {
  return request<void>(`/schedules/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function runSchedule(id: string): Promise<ScheduleRunResult> {
  return request<ScheduleRunResult>(`/schedules/${encodeURIComponent(id)}/run`, { method: 'POST' }, ScheduleRunResultSchema as ZodType<ScheduleRunResult>)
}

export function pauseSchedule(id: string): Promise<Schedule> {
  return request<Schedule>(`/schedules/${encodeURIComponent(id)}/pause`, { method: 'POST' }, ScheduleSchema as ZodType<Schedule>)
}

// ── #264 Notifications ────────────────────────────────────────────────────────
//
// Header notification center. The REST surface seeds the store on mount and the
// `notification` WS frame keeps it live. NotificationList wire type is re-exported
// from generated openapi-types (contract-first #8); see
// contracts/components/schemas/Notification*.yaml.

export function fetchNotifications(): Promise<NotificationList> {
  // GET /notifications → { notifications: Notification[], unread_count }.
  return request<NotificationList>('/notifications', undefined, NotificationListSchema as ZodType<NotificationList>)
}

export function markNotificationRead(id: string): Promise<void> {
  // POST /notifications/{id}/read — void response.
  return request<void>(`/notifications/${encodeURIComponent(id)}/read`, { method: 'POST' })
}

export function markAllNotificationsRead(): Promise<void> {
  // POST /notifications/read-all — void response.
  return request<void>('/notifications/read-all', { method: 'POST' })
}

// ── Gateway Status ────────────────────────────────────────────────────────────

// GatewayStatus — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/GatewayStatus.yaml.

export function fetchGatewayStatus(): Promise<GatewayStatus> {
  return request<GatewayStatus>('/status', undefined, GatewayStatusSchema as ZodType<GatewayStatus>)
}

// ── Tools & Channels ──────────────────────────────────────────────────────────

// Tool — type alias for ToolRegistryEntry (contract-first #8).
// GET /tools returns ToolRegistryEntry[]; this alias preserves backward compat.
// See contracts/components/schemas/ToolRegistryEntry.yaml.
export type Tool = ToolRegistryEntry

// fetchTools is a backward-compat alias for fetchRegistryTools.
// New callers should use fetchRegistryTools (or fetchBuiltinTools) directly.
export function fetchTools(): Promise<ToolRegistryEntry[]> { return fetchRegistryTools() }

// Channel — type alias for ChannelEntry (contract-first #8).
// GET /channels returns ChannelEntry[]; this alias preserves backward compat.
// See contracts/components/schemas/ChannelEntry.yaml.
export type Channel = ChannelEntry

export function fetchChannels(): Promise<ChannelEntry[]> {
  return request<ChannelEntry[]>('/channels', undefined, z.array(ChannelEntrySchema) as ZodType<ChannelEntry[]>)
}

export function enableChannel(id: string): Promise<ChannelEnabledResponse> {
  // Backend returns ChannelEnabledResponse {id, enabled} — not a full ChannelEntry.
  return request<ChannelEnabledResponse>(
    `/channels/${encodeURIComponent(id)}/enable`,
    { method: 'PUT' },
    ChannelEnabledResponseSchema as ZodType<ChannelEnabledResponse>,
  )
}

export function disableChannel(id: string): Promise<ChannelEnabledResponse> {
  // Backend returns ChannelEnabledResponse {id, enabled} — not a full ChannelEntry.
  return request<ChannelEnabledResponse>(
    `/channels/${encodeURIComponent(id)}/disable`,
    { method: 'PUT' },
    ChannelEnabledResponseSchema as ZodType<ChannelEnabledResponse>,
  )
}

export function fetchChannelConfig(id: string): Promise<Record<string, unknown>> {
  // no-schema: channel config structure varies per channel type; no generated schema component.
  return request<Record<string, unknown>>(`/channels/${encodeURIComponent(id)}`)
}

export function configureChannel(id: string, config: Record<string, unknown>): Promise<void> {
  // no-schema: void response; channel-specific body.
  return request<void>(`/channels/${encodeURIComponent(id)}/configure`, {
    method: 'PUT',
    body: JSON.stringify(config),
  })
}

export function testChannel(id: string): Promise<ChannelTestResponse> {
  return request<ChannelTestResponse>(`/channels/${encodeURIComponent(id)}/test`, {
    method: 'POST',
  }, ChannelTestResponseSchema as ZodType<ChannelTestResponse>)
}

// ChannelRouting — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/ChannelRouting.yaml.

export function getChannelRouting(id: string): Promise<ChannelRouting> {
  return request<ChannelRouting>(
    `/channels/${encodeURIComponent(id)}/routing`,
    undefined,
    ChannelRoutingSchema as ZodType<ChannelRouting>,
  )
}

export function setChannelRouting(id: string, body: ChannelRouting): Promise<ChannelRouting> {
  return request<ChannelRouting>(
    `/channels/${encodeURIComponent(id)}/routing`,
    { method: 'PUT', body: JSON.stringify(body) },
    ChannelRoutingSchema as ZodType<ChannelRouting>,
  )
}

// ── Channel-instance CRUD (ADR-029 US-6 / US-10 / US-11) ─────────────────────
//
// createChannelInstance  — POST /channels with {type, slug}; backend derives the
//   instance key as "<type>.<slug>" (FR-017). Returns 201 ChannelCreateResponse
//   on success; 400 for unknown type or malformed slug; 409 if the key already
//   exists. Slug validation against [a-z0-9-]{1,32} is enforced client-side too
//   (the dialog blocks submit) but the backend is the authoritative validator.
//
// deleteChannelInstance — DELETE /channels/{id}; returns 204 on success; 404 for
//   unknown instance; 400 for malformed id. Removes config + credential refs +
//   per-instance state directory (e.g. WhatsApp store.db). "webchat" is a
//   built-in and cannot be deleted (backend returns 400).

export function createChannelInstance(body: ChannelCreateRequest): Promise<ChannelCreateResponse> {
  return request<ChannelCreateResponse>(
    '/channels',
    { method: 'POST', body: JSON.stringify(body) },
    ChannelCreateResponseSchema as ZodType<ChannelCreateResponse>,
  )
}

export function deleteChannelInstance(id: string): Promise<void> {
  return request<void>(`/channels/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

// ── Email Mailbox Account ─────────────────────────────────────────────────────
//
// Email is a TOOL (not a channel) with a per-(agent, workspace) mailbox account,
// cap-1 in 0.1.0. The config is stored via the existing email channel config
// endpoint — the backend agent (parallel) will add dedicated mailbox endpoints
// when the contract is extended. Until then this seam routes through the channel
// config path; swap the implementation here when the new endpoints land.
//
// Wire fields sent / received:
//   imap_host, imap_port, smtp_host, smtp_port, username — plain text
//   password — write-only; the backend credential-routes it and never returns it
//   agent_id  — binds the mailbox to a specific agent (0.1.0: cap-1)
//   workspace_id — the workspace the mailbox surfaces in (0.1.0: "my-workspace")

// not-wire-format: SPA-internal shape for the email mailbox account config form
export interface MailboxAccountConfig { // not-wire-format: SPA-internal form-data type for the email mailbox account config panel. Never sent as a standalone wire type; fields are spread into a Record<string,unknown> payload passed to saveMailboxConfig.
  imap_host: string
  imap_port: number | ''
  smtp_host: string
  smtp_port: number | ''
  username: string
  /** Write-only: present only when the user provides a new password; never returned by GET. */
  password: string
  /** Agent this mailbox belongs to (cap-1 in 0.1.0). */
  agent_id: string
  /** Workspace the mailbox surfaces in (cap-1 in 0.1.0). */
  workspace_id: string
  /** Free-form display fields returned by GET (may not be present). */
  [key: string]: unknown
}

export const EMAIL_CHANNEL_ID = 'email'

/**
 * Fetch the current email mailbox account config.
 * Routes through GET /api/v1/channels/email — the backend never returns the
 * password field (it is credential-store-routed, stored as password_ref).
 *
 * INTEGRATOR NOTE: when the backend agent adds dedicated mailbox endpoints
 * (GET /api/v1/mailboxes/:agentId), replace the body of this function only.
 */
export function fetchMailboxConfig(): Promise<Record<string, unknown>> {
  return fetchChannelConfig(EMAIL_CHANNEL_ID)
}

/**
 * Save the email mailbox account config.
 * Routes through PUT /api/v1/channels/email/configure — the backend
 * credential-routes the password field so it is never persisted in plaintext.
 *
 * INTEGRATOR NOTE: when the backend agent adds dedicated mailbox endpoints
 * (PUT /api/v1/mailboxes/:agentId), replace the body of this function only.
 */
export function saveMailboxConfig(config: Record<string, unknown>): Promise<void> {
  return configureChannel(EMAIL_CHANNEL_ID, config)
}

// ── Skills ────────────────────────────────────────────────────────────────────

// Skill — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/Skill.yaml.

// McpServer — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/McpServer.yaml.
// McpServerCreate — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/McpServerCreate.yaml.

export async function fetchSkills(): Promise<Skill[]> {
  // Tolerant per-item validation: a single skill whose payload fails the Skill
  // schema must NOT hide the entire installed-skills list. (A community/ClawHub
  // skill with an unexpected field value previously made the whole list silently
  // vanish.) Validate each item, keep the valid ones, drop + warn on the rest.
  const raw = await request<unknown[]>('/skills')
  if (!Array.isArray(raw)) return []
  const out: Skill[] = []
  let dropped = 0
  for (const item of raw) {
    const parsed = SkillSchema.safeParse(item)
    if (parsed.success) out.push(parsed.data as Skill)
    else dropped++
  }
  if (dropped > 0 && import.meta.env?.DEV) {
    // eslint-disable-next-line no-console
    console.warn(`fetchSkills: dropped ${dropped} skill(s) that failed schema validation`)
  }
  return out
}

export function deleteSkill(name: string): Promise<void> {
  // no-schema: void response; DELETE has no body.
  return request<void>(`/skills/${encodeURIComponent(name)}`, { method: 'DELETE' })
}

// ── Slash commands ─────────────────────────────────────────────────────────────

// SlashCommand is re-exported from the `export type {}` block above (contract-first #8).
// See contracts/components/schemas/SlashCommand.yaml.

// fetchCommands — mirrors fetchSkills; fetches the surface-applicable slash commands
// from GET /api/v1/commands?surface=<surface>.  Per US-4 / FR-008 / SC-005, the
// web palette must render from this endpoint and never from a hardcoded list.
// Tolerant per-item validation: a single malformed item must NOT hide the entire list.
export async function fetchCommands(surface: 'web' | 'cli' | 'channel' = 'web'): Promise<SlashCommand[]> {
  const raw = await request<unknown[]>(`/commands?surface=${encodeURIComponent(surface)}`)
  if (!Array.isArray(raw)) return []
  const out: SlashCommand[] = []
  let dropped = 0
  for (const item of raw) {
    const parsed = SlashCommandSchema.safeParse(item)
    if (parsed.success) out.push(parsed.data)
    else dropped++
  }
  if (dropped > 0 && import.meta.env?.DEV) {
    // eslint-disable-next-line no-console
    console.warn(`fetchCommands: dropped ${dropped} command(s) that failed schema validation`)
  }
  return out
}

// fetchMcpServers is a backward-compat alias for fetchMcpServersForAgent.
// New callers should use fetchMcpServersForAgent directly.
export function fetchMcpServers(): Promise<McpServer[]> { return fetchMcpServersForAgent() }

export function addMcpServer(data: McpServerCreate): Promise<McpServer> {
  return request<McpServer>('/mcp-servers', { method: 'POST', body: JSON.stringify(data) }, McpServerSchema as ZodType<McpServer>)
}

export function deleteMcpServer(id: string): Promise<void> {
  // no-schema: void response; DELETE has no body.
  return request<void>(`/mcp-servers/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export async function fetchMcpServerTools(id: string): Promise<string[]> {
  const resp = await request<McpServerToolsResponse>(`/mcp-servers/${encodeURIComponent(id)}/tools`, undefined, McpServerToolsResponseSchema as ZodType<McpServerToolsResponse>)
  return resp.tools
}

export function patchMcpServer(id: string, body: McpServerUpdate): Promise<McpServer> {
  return request<McpServer>(`/mcp-servers/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(body) }, McpServerSchema as ZodType<McpServer>)
}

export function testMcpServer(id: string): Promise<McpServerTestResponse> {
  return request<McpServerTestResponse>(`/mcp-servers/${encodeURIComponent(id)}/test`, { method: 'POST' }, McpServerTestResponseSchema as ZodType<McpServerTestResponse>)
}

// ── Storage Stats ─────────────────────────────────────────────────────────────

// StorageStats — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/StorageStats.yaml.

export function fetchStorageStats(): Promise<StorageStats> {
  return request<StorageStats>('/storage/stats', undefined, StorageStatsSchema)
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

export async function registerAdmin(username: string, password: string): Promise<LoginResponse> {
  return request<LoginResponse>('/auth/register-admin', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  }, LoginResponseSchema)
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
export async function probeProvider(
  id: string,
  apiKey: string,
  endpoint?: string,
): Promise<ProbeProviderResponse> {
  return request<ProbeProviderResponse>('/onboarding/probe-provider', {
    method: 'POST',
    body: JSON.stringify({ id, api_key: apiKey, endpoint: endpoint ?? '' }),
  }, ProbeProviderResponseSchema)
}

// ValidateTokenResponse — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/ValidateTokenResponse.yaml.

export async function validateToken(): Promise<ValidateTokenResponse> {
  return request<ValidateTokenResponse>('/auth/validate', undefined, ValidateTokenResponseSchema)
}

// ── Doctor ────────────────────────────────────────────────────────────────────

// DoctorIssue — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/DoctorIssue.yaml.
// DoctorResult — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/DoctorResult.yaml.

export function fetchDoctorResults(): Promise<DoctorResult | null> {
  return request<DoctorResult | null>('/doctor', undefined, DoctorResultSchema.nullable())
}

export function runDoctor(): Promise<DoctorResult> {
  return request<DoctorResult>('/doctor', { method: 'POST' }, DoctorResultSchema)
}

// ── Activity Feed ─────────────────────────────────────────────────────────────

// ActivityEvent — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/ActivityEvent.yaml.

export function fetchActivity(): Promise<ActivityEvent[]> {
  return request<ActivityEvent[]>('/activity', undefined, z.array(ActivityEventSchema) as ZodType<ActivityEvent[]>)
}

// ── Credentials ───────────────────────────────────────────────────────────────

export interface CredentialKey { // not-wire-format: SPA-internal credential display shape. The wire GET /credentials endpoint returns string[] (key names only); the component accesses .key on each entry. This interface reflects how the SPA displays credential entries but does NOT match the wire format. The wire format is defined inline in the openapi.yaml /credentials GET response schema as string[].
  key: string
  created_at?: string
  updated_at?: string
}

export async function fetchCredentials(): Promise<CredentialKey[]> {
  // Wire format: GET /credentials returns string[] (key names only).
  // The SPA uses CredentialKey[] (objects with .key) so SecuritySection.tsx can
  // render cred.key and use it as a React key. We validate the wire shape, then
  // transform string[] → {key:string}[].
  const wire = await request<string[]>('/credentials', undefined, z.array(z.string()) as ZodType<string[]>)
  return wire.map((key) => ({ key }))
}

// Credential add/delete are re-auth gated server-side (ADR-022 / Spec-6 FR-12.2):
// the server rejects with 403 unless a single-use consent token (from reAuth) is
// replayed in the X-Reauth-Token header. Pass reAuthToken via runGated().
export function addCredential(key: string, value: string, reAuthToken?: string): Promise<void> {
  // no-schema: void response; POST body is a write-only operation.
  return request<void>('/credentials', {
    method: 'POST',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
    body: JSON.stringify({ key, value }),
  })
}

export function deleteCredential(key: string, reAuthToken?: string): Promise<void> {
  // no-schema: void response; DELETE has no body.
  return request<void>(`/credentials/${encodeURIComponent(key)}`, {
    method: 'DELETE',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
  })
}

// rotateCredentials re-encrypts the whole vault under a new passphrase (G5). Like
// add/delete it is re-auth gated server-side (ADR-022): pass reAuthToken via runGated().
export function rotateCredentials(newPassphrase: string, reAuthToken?: string): Promise<void> {
  // no-schema: void/{status} response; POST body triggers re-encryption.
  return request<void>('/credentials/rotate', {
    method: 'POST',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
    body: JSON.stringify({ new_passphrase: newPassphrase }),
  })
}

// ── Devices ───────────────────────────────────────────────────────────────────

// DevicePending — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/DevicePending.yaml.
// DevicePaired — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/DevicePaired.yaml.
// DevicesResponse — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/DevicesResponse.yaml.

export function fetchDevices(): Promise<DevicesResponse> {
  return request<DevicesResponse>('/devices', undefined, DevicesResponseSchema)
}

// ── Backup / Restore ──────────────────────────────────────────────────────────

// BackupEntry — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/BackupEntry.yaml.

export function createBackup(): Promise<BackupCreateResponse> {
  return request<BackupCreateResponse>('/backup', { method: 'POST' }, BackupCreateResponseSchema as ZodType<BackupCreateResponse>)
}

export function fetchBackups(): Promise<BackupEntry[]> {
  return request<BackupEntry[]>('/backups', undefined, z.array(BackupEntrySchema))
}

export function restoreBackup(filename: string): Promise<void> {
  // no-schema: void response; 204 No Content on success.
  return request<void>('/restore', { method: 'POST', body: JSON.stringify({ filename }) })
}

export function clearAllSessions(): Promise<void> {
  // no-schema: void response; DELETE returns 204 No Content.
  return request<void>('/sessions/all', { method: 'DELETE' })
}

export async function renameSession(id: string, title: string): Promise<Session> {
  // Wire returns the wire Session shape (nested stats); transform to SPA Session.
  const raw = await request<RawSession>(`/sessions/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify({ title }),
  }, WireSessionSchema as ZodType<RawSession>)
  return rawToSession(raw)
}

export function deleteSession(id: string): Promise<OperationResult> {
  return request<OperationResult>(`/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }, OperationResultSchema as ZodType<OperationResult>)
}

// ── About ─────────────────────────────────────────────────────────────────────

export interface AboutInfo { // not-wire-format: SPA-internal backward-compatible subset of AboutResponse. The generated AboutResponse has required fields (uptime, pid, frame_ancestors_fallback) and different optionality for preview_port/preview_listener_enabled. The SPA uses this looser interface to maintain backward compatibility with older gateway versions that may not send all AboutResponse fields.
  version: string
  go_version: string
  os: string
  arch: string
  uptime_seconds: number
  // Preview listener fields — added by FR-009 / iframe-preview feature.
  // preview_port is the port the preview listener is bound on (default:
  // gateway.port + 1). Absent when preview_listener_enabled is false.
  preview_port?: number
  // preview_origin is the fully-qualified origin operators set via
  // gateway.preview_origin (e.g. "https://preview.acme.com"). Absent when
  // not configured — the SPA constructs the iframe URL from hostname +
  // preview_port in that case.
  preview_origin?: string
  // preview_listener_enabled reflects whether the preview listener is bound.
  // Absent on old gateway versions that predate this feature (treat as true).
  preview_listener_enabled?: boolean
  // warmup_timeout_seconds is sourced from
  // cfg.Tools.RunInWorkspace.WarmupTimeoutSeconds (default 60). Used by
  // RunInWorkspaceUI to cap the warmup polling loop.
  warmup_timeout_seconds?: number
}

export function fetchAboutInfo(): Promise<AboutInfo> {
  // no-schema: AboutInfo is a looser SPA-compatibility subset of the wire AboutResponse
  // (different field names: uptime_seconds vs uptime). Validating against a strict schema
  // would produce false negatives on older gateway versions.
  return request<AboutInfo>('/about')
}

/**
 * Returns the gateway's version string and build SHA. Used by the
 * `useVersionCheck` hook to detect version drift. The `/version` endpoint
 * is unauthenticated and lives outside the regular auth/CSRF envelope,
 * so we go through `request` (not raw `fetch`) to add the
 * `Authorization` header when present and keep the request shape uniform
 * with every other call. The response is validated by the generated
 * `VersionResponse` Zod schema (per `contracts/openapi.yaml`).
 */
export function fetchVersion(): Promise<VersionResponse> {
  return request<VersionResponse>(
    '/version',
    undefined,
    VersionResponseSchema as ZodType<VersionResponse>,
  )
}

/**
 * Returns the active voice provider descriptor. Used by
 * `voice-provider-detect` to decide whether the SPA should render a
 * dropdown, a free-text input, or hide the voice field. The response is
 * validated by the generated `VoiceProvider` Zod schema.
 */
export function fetchVoiceProvider(): Promise<VoiceProvider> {
  return request<VoiceProvider>(
    '/voice/provider',
    undefined,
    VoiceProviderSchema as ZodType<VoiceProvider>,
  )
}

/**
 * Returns whether the preview listener is enabled.
 *
 * `preview_listener_enabled` is an optional bool where `undefined` means "true"
 * — old gateway versions that predate the field did not include it, and those
 * versions always ran the preview listener. Reading the field directly risks
 * treating `undefined` as falsy; use this accessor instead.
 */
export function isPreviewListenerEnabled(info: AboutInfo | undefined): boolean {
  return info?.preview_listener_enabled !== false
}

// ── Audit Log ─────────────────────────────────────────────────────────────────

export type AuditEventType = 'tool_call' | 'exec' | 'file_op' | 'llm_call' | 'policy_eval' | 'rate_limit' | 'ssrf' | 'startup' | 'shutdown'
export type AuditDecision = 'allow' | 'deny' | 'error'

// AuditEntry — re-exported from generated openapi-types (no local body needed).
// AuditEventType and AuditDecision remain as local type aliases for UI use.

export function fetchAuditLog(): Promise<AuditLogResponse> {
  return request<AuditLogResponse>('/audit-log', undefined, AuditLogResponseSchema as ZodType<AuditLogResponse>)
}

// ── User Context (USER.md) ────────────────────────────────────────────────────

export function fetchUserContext(): Promise<UserContextResponse> {
  return request<UserContextResponse>('/user-context', undefined, UserContextResponseSchema as ZodType<UserContextResponse>)
}

export function updateUserContext(content: string): Promise<void> {
  // no-schema: void response; PUT returns 204 No Content.
  return request<void>('/user-context', {
    method: 'PUT',
    body: JSON.stringify({ content }),
  })
}

// ── RBAC / Me ─────────────────────────────────────────────────────────────────

export type UserRole = 'admin' | 'user'

// MeInfo — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/MeInfo.yaml.

export async function fetchMe(): Promise<MeInfo> {
  return request<MeInfo>('/me', undefined, MeInfoSchema)
}

// ── File Upload ───────────────────────────────────────────────────────────────

// UploadedFile — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/UploadedFile.yaml.

export async function uploadFiles(sessionId: string, files: File[]): Promise<UploadFilesResponse> {
  const formData = new FormData()
  formData.append('session_id', sessionId)
  for (const file of files) {
    formData.append('files', file)
  }
  const token = sessionStorage.getItem('omnipus_auth_token') ?? localStorage.getItem('omnipus_auth_token')
  const csrf = readCSRFCookie()
  // Upload is a state-changing POST — fail fast if we have no CSRF cookie
  // (see request() for the same pattern). We still send the header on the
  // off chance the user explicitly set the cookie externally.
  if (csrf === null) {
    throw new ApiError(
      403,
      'CSRF cookie missing — cannot upload files. Log in first so the server can issue the CSRF cookie.',
      { code: 'csrf_missing' },
    )
  }
  // Build headers by hand because FormData must NOT have a Content-Type
  // set — the browser needs to fill in the multipart boundary itself.
  const headers: Record<string, string> = {
    [CSRF_HEADER_NAME]: csrf,
  }
  if (token) {
    headers.Authorization = `Bearer ${token}`
  }
  let res: Response
  try {
    res = await fetch(`${BASE_URL}/api/v1/upload`, {
      method: 'POST',
      headers,
      body: formData,
    })
  } catch (cause) {
    throw new ApiError(0, 'Network unavailable. Check your connection.', { cause })
  }
  if (!res.ok) {
    throw await ApiError.fromResponse(res)
  }
  const raw: unknown = await res.json()
  const parsed = (UploadFilesResponseSchema as ZodType<UploadFilesResponse>).safeParse(raw)
  if (!parsed.success) {
    _recordApiSchemaError('/upload', parsed.error.issues.length)
    const issues = parsed.error.issues.map((i) => ({ path: i.path as (string | number)[], message: i.message }))
    void maybeDevToast(`[api] uploadFiles response schema mismatch: ${issues[0]?.message ?? 'unknown'}`, 'POST:/upload:schema')
    throw new ApiSchemaError('/upload', issues, raw)
  }
  return parsed.data
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

// ── Integrations (Spec-6 FR-12.1) ──────────────────────────────────────────────

export function fetchIntegrationProviders(): Promise<IntegrationProvidersResponse> {
  return request<IntegrationProvidersResponse>(
    '/integrations/providers',
    undefined,
    IntegrationProvidersResponseSchema as ZodType<IntegrationProvidersResponse>,
  )
}

// configureIntegrationProvider sets a provider's API key and/or selects it as
// active. It REQUIRES a re-auth consent token (from reAuth) — the server rejects
// the PUT with 403 without a valid token. The token is replayed in the
// X-Reauth-Token header.
export function configureIntegrationProvider(
  id: string,
  body: IntegrationProviderUpdateRequest,
  reAuthToken: string,
): Promise<IntegrationProvidersResponse> {
  return request<IntegrationProvidersResponse>(
    `/integrations/providers/${encodeURIComponent(id)}`,
    {
      method: 'PUT',
      headers: { [REAUTH_HEADER]: reAuthToken },
      body: JSON.stringify(body),
    },
    IntegrationProvidersResponseSchema as ZodType<IntegrationProvidersResponse>,
  )
}

// ── Voice transcription (composer mic, Spec-6 FR-12.1) ─────────────────────────
//
// transcribeAudio uploads a recorded audio Blob to the active transcriber and
// returns the recognised text. Multipart form-data; the request() helper is
// JSON-only, so this uses fetch directly with the auth + CSRF headers.
export async function transcribeAudio(audio: Blob): Promise<TranscribeResponse> {
  const form = new FormData()
  // Preserve the recorded mime type's extension hint where possible.
  const ext = audio.type.includes('webm') ? 'webm'
    : audio.type.includes('ogg') ? 'ogg'
    : audio.type.includes('wav') ? 'wav'
    : audio.type.includes('mp4') || audio.type.includes('mpeg') ? 'm4a'
    : 'webm'
  form.append('audio', audio, `recording.${ext}`)

  const csrf = readCSRFCookie()
  let res: Response
  try {
    res = await fetch(`${BASE_URL}/api/v1/voice/transcribe`, {
      method: 'POST',
      // NOTE: do NOT set Content-Type — the browser sets the multipart boundary.
      headers: {
        ...getAuthHeaders(),
        ...(csrf ? { [CSRF_HEADER_NAME]: csrf } : {}),
      },
      body: form,
    })
  } catch (cause) {
    throw new ApiError(0, 'Network unavailable. Check your connection.', { cause })
  }
  if (!res.ok) {
    throw await ApiError.fromResponse(res)
  }
  const raw = (await res.json()) as unknown
  const parsed = TranscribeResponseSchema.safeParse(raw)
  if (!parsed.success) {
    const issues = parsed.error.issues
    _recordApiSchemaError('/voice/transcribe', issues.length)
    void maybeDevToast(
      `[api] transcribe response schema mismatch: ${issues[0]?.message ?? 'unknown'}`,
      'POST:/voice/transcribe:schema',
    )
    throw new ApiSchemaError('/voice/transcribe', issues, raw)
  }
  return parsed.data as TranscribeResponse
}

// ── Exec Allowlist ────────────────────────────────────────────────────────────

// ExecAllowlist — re-exported from generated openapi-types (no local body needed).

export function fetchExecAllowlist(): Promise<ExecAllowlist> {
  return request<ExecAllowlist>('/security/exec-allowlist', undefined, ExecAllowlistSchema)
}

export function updateExecAllowlist(patterns: string[]): Promise<ExecAllowlist> {
  return request<ExecAllowlist>('/security/exec-allowlist', {
    method: 'PUT',
    body: JSON.stringify({ allowed_binaries: patterns }),
  }, ExecAllowlistSchema)
}

// ── Security Admin Endpoints ──────────────────────────────────────────────────
//
// Enums and typed helpers for the security and admin endpoints.
// These are separate from the pre-existing /security/* helpers above — they use
// the canonical request/response shapes and are wired to the admin UI panels.

export type SkillTrustLevel = 'block_unverified' | 'warn_unverified' | 'allow_all'
export type PromptInjectionLevel = 'low' | 'medium' | 'high'
export type DMScope = 'main' | 'per-peer' | 'per-channel-peer' | 'per-account-channel-peer'

// PendingRestartEntry — re-exported from generated openapi-types (no local body needed).

export function fetchPendingRestart(): Promise<PendingRestartEntry[]> {
  return request<PendingRestartEntry[]>('/config/pending-restart', undefined, z.array(PendingRestartEntrySchema) as ZodType<PendingRestartEntry[]>)
}

// GatewayRestartResponse — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/GatewayRestartResponse.yaml.
//
// POST /api/v1/gateway/restart triggers a graceful self-restart. The server replies
// with 202 Accepted (status:"restarting") immediately, before re-execing. Admin-only;
// secured by RequireAdmin + RequireNotBypass (returns 503 when dev_mode_bypass is active).
export function gatewayRestart(): Promise<GatewayRestartResponse> {
  return request<GatewayRestartResponse>('/gateway/restart', {
    method: 'POST',
  }, GatewayRestartResponseSchema)
}

// ── God-mode (O14) ─────────────────────────────────────────────────────────────
//
// God-mode is the single global "bypass-permissions" switch: flipping it ON
// floors every agent's tool policy at "allow" (no prompts), turns the kernel
// sandbox off, opens network egress, and disables the shell guard — regardless
// of per-agent profiles. Audit logging, the prompt-injection guard, and rate
// limiting STAY ON. The per-agent overrides are non-destructive: switching god
// mode off restores prior behaviour exactly.
//
// GodModeStatus / GodModeUpdateRequest are the generated contract types (#8);
// see contracts/components/schemas/GodMode*.yaml. fetchGodMode reads the live
// runtime state ({ enabled, available }); setGodMode flips it.
//
// Step-up auth: the POST is re-auth-gated. Callers obtain a single-use consent
// token via reAuth() and pass it here; it is replayed in the X-Reauth-Token
// header (a missing/invalid token yields a 403). `available` is false on a
// nogodmode build or when --allow-god-mode was not passed at boot; enabling
// then returns 403.

export function fetchGodMode(): Promise<GodModeStatus> {
  return request<GodModeStatus>('/gateway/god-mode', undefined, GodModeStatusSchema)
}

export function setGodMode(enabled: boolean, reAuthToken?: string): Promise<GodModeStatus> {
  const body: GodModeUpdateRequest = { enabled }
  return request<GodModeStatus>('/gateway/god-mode', {
    method: 'POST',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
    body: JSON.stringify(body),
  }, GodModeStatusSchema)
}

// Audit log toggle — distinct from GET /audit-log (which returns AuditEntry[]).
// This endpoint controls whether audit logging is enabled at all.
// AuditLogToggle is re-exported from generated openapi-types above.

// AuditLogUpdateResponse — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/AuditLogUpdateResponse.yaml.

export function fetchAuditLogToggle(): Promise<AuditLogToggle> {
  return request<AuditLogToggle>('/security/audit-log', undefined, AuditLogToggleSchema)
}

export function updateAuditLog(enabled: boolean): Promise<AuditLogUpdateResponse> {
  return request<AuditLogUpdateResponse>('/security/audit-log', {
    method: 'PUT',
    body: JSON.stringify({ enabled }),
  }, AuditLogUpdateResponseSchema)
}

// Skill trust — controls how unverified community skills are handled.
// SkillTrustResponse is re-exported from generated openapi-types above.
// SkillTrustUpdateRequest — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/SkillTrustUpdateRequest.yaml.
// SkillTrustUpdateResponse — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/SkillTrustUpdateResponse.yaml.

export function fetchSkillTrust(): Promise<SkillTrustResponse> {
  return request<SkillTrustResponse>('/security/skill-trust', undefined, SkillTrustResponseSchema)
}

export function updateSkillTrust(level: SkillTrustLevel): Promise<SkillTrustUpdateResponse> {
  return request<SkillTrustUpdateResponse>('/security/skill-trust', {
    method: 'PUT',
    body: JSON.stringify({ level } satisfies SkillTrustUpdateRequest),
  }, SkillTrustUpdateResponseSchema)
}

// Prompt guard — uses `level` field, aligns with PromptInjectionLevel.
// PromptGuardResponse is re-exported from generated openapi-types above.
// PromptGuardUpdateRequest — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/PromptGuardUpdateRequest.yaml.
// PromptGuardUpdateResponse — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/PromptGuardUpdateResponse.yaml.

export function fetchPromptGuardLevel(): Promise<PromptGuardResponse> {
  return request<PromptGuardResponse>('/security/prompt-guard', undefined, PromptGuardResponseSchema)
}

export function updatePromptGuardLevel(level: PromptInjectionLevel): Promise<PromptGuardUpdateResponse> {
  return request<PromptGuardUpdateResponse>('/security/prompt-guard', {
    method: 'PUT',
    body: JSON.stringify({ level } satisfies PromptGuardUpdateRequest),
  }, PromptGuardUpdateResponseSchema)
}

// Rate limits — adds write support and configures spending/throughput caps.
// RateLimitsResponse — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/RateLimitsResponse.yaml.
// RateLimitsUpdateRequest — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/RateLimitsUpdateRequest.yaml.
// RateLimitsUpdateResponse — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/RateLimitsUpdateResponse.yaml.

export function fetchRateLimits(): Promise<RateLimitsResponse> {
  return request<RateLimitsResponse>('/security/rate-limits', undefined, RateLimitsResponseSchema)
}

export function updateRateLimits(body: RateLimitsUpdateRequest): Promise<RateLimitsUpdateResponse> {
  return request<RateLimitsUpdateResponse>('/security/rate-limits', {
    method: 'PUT',
    body: JSON.stringify(body),
  }, RateLimitsUpdateResponseSchema)
}

// Sandbox config — mode, allowed paths, SSRF controls, and the global
// agent defaults (default_profile, shell_deny_patterns).
// SandboxConfig — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/SandboxConfig.yaml.
// SandboxConfigUpdate — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/SandboxConfigUpdate.yaml.

// SandboxConfigResponse is a backward-compat alias for the generated SandboxConfig.
// SandboxConfig already contains requires_restart, applied_mode, saved, and all
// sandbox fields — no extra SPA-specific shape is needed.
export type SandboxConfigResponse = SandboxConfig

export function fetchSandboxConfig(): Promise<SandboxConfigResponse> {
  return request<SandboxConfigResponse>('/security/sandbox-config', undefined, SandboxConfigSchema)
}

// updateSandboxConfig persists a sandbox-config mutation. It is re-auth gated
// (Spec-6 FR-12.2): the server rejects the PUT with 403 unless a single-use
// consent token (from reAuth) is replayed in the X-Reauth-Token header.
export function updateSandboxConfig(
  body: SandboxConfigUpdate,
  reAuthToken?: string,
): Promise<SandboxConfigResponse> {
  return request<SandboxConfigResponse>('/security/sandbox-config', {
    method: 'PUT',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
    body: JSON.stringify(body),
  }, SandboxConfigSchema)
}

// Session scope — controls DM conversation isolation granularity.
// SessionScopeResponse is re-exported from generated openapi-types above.
// SessionScopeRequest (request body) — re-exported from generated openapi-types.
// See contracts/components/schemas/SessionScopeRequest.yaml.
// SessionScopeUpdateResponse — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/SessionScopeUpdateResponse.yaml.

export function fetchSessionScope(): Promise<SessionScopeResponse> {
  return request<SessionScopeResponse>('/security/session-scope', undefined, SessionScopeResponseSchema as ZodType<SessionScopeResponse>)
}

export function updateSessionScope(dm_scope: DMScope): Promise<SessionScopeUpdateResponse> {
  return request<SessionScopeUpdateResponse>('/security/session-scope', {
    method: 'PUT',
    body: JSON.stringify({ dm_scope } satisfies SessionScopeRequest),
  }, SessionScopeUpdateResponseSchema)
}

// Retention — session log retention policy.

// RetentionMode mirrors the Go pkg/config.RetentionMode enum. Derive with
// retentionMode(resp) from the flat wire shape; the backend does not send
// this as a field.
export type RetentionMode = 'default' | 'custom' | 'forever'

export function retentionMode(resp: {
  session_days?: number
  disabled?: boolean
}): RetentionMode {
  if (resp.disabled) return 'forever'
  if ((resp.session_days ?? 0) > 0) return 'custom'
  return 'default'
}

// GET /security/retention returns RetentionConfig (session_days?, disabled?).
// RetentionConfig — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/RetentionConfig.yaml.

// RetentionUpdateBody is a backward-compat alias for the PUT request body.
export type RetentionUpdateBody = RetentionConfig

// RetentionUpdateResponse — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/RetentionUpdateResponse.yaml.

export function fetchRetention(): Promise<RetentionConfig> {
  return request<RetentionConfig>('/security/retention', undefined, RetentionConfigSchema)
}

export function updateRetention(body: RetentionUpdateBody): Promise<RetentionUpdateResponse> {
  return request<RetentionUpdateResponse>('/security/retention', {
    method: 'PUT',
    body: JSON.stringify(body),
  }, RetentionUpdateResponseSchema)
}

// Retention sweep — immediately purge sessions beyond the retention window.
// RetentionSweepResult is re-exported from generated openapi-types above.

export function triggerRetentionSweep(): Promise<RetentionSweepResult> {
  return request<RetentionSweepResult>('/security/retention/sweep', { method: 'POST' }, RetentionSweepResultSchema)
}

// Performance — max-parallel agent concurrency settings.
// PerformanceSettings and PerformanceSettingsUpdate are re-exported from
// generated openapi-types (contract-first #8).
// See contracts/components/schemas/PerformanceSettings.yaml.

export type { PerformanceSettings, PerformanceSettingsUpdate }

export function fetchPerformanceSettings(): Promise<PerformanceSettings> {
  return request<PerformanceSettings>('/performance', undefined, PerformanceSettingsSchema)
}

// updatePerformanceSettings persists the max-parallel-agents concurrency setting.
// It is re-auth gated (Spec-6 FR-12.2 / Spec-3 FR-6.6): the server rejects the PUT
// with 403 unless a single-use consent token (from reAuth) is replayed in the
// X-Reauth-Token header.
export function updatePerformanceSettings(
  body: PerformanceSettingsUpdate,
  reAuthToken?: string,
): Promise<PerformanceSettings> {
  return request<PerformanceSettings>('/performance', {
    method: 'PUT',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
    body: JSON.stringify(body),
  }, PerformanceSettingsSchema)
}

// ── Exec Proxy ────────────────────────────────────────────────────────────────
// ExecProxyStatus — re-exported from generated openapi-types (no local body needed).

export function fetchExecProxyStatus(): Promise<ExecProxyStatus> {
  return request<ExecProxyStatus>('/security/exec-proxy-status', undefined, ExecProxyStatusSchema)
}


// ── Agent Tools ───────────────────────────────────────────────────────────────

// RegistryTool — type alias for ToolRegistryEntry (contract-first #8).
// Central registry tool entry (FR-027, FR-029). Includes a source discriminator
// so the UI can badge MCP tools differently from builtin ones.
// See contracts/components/schemas/ToolRegistryEntry.yaml.
export type RegistryTool = ToolRegistryEntry

/** Backward-compat alias — existing callers that reference BuiltinTool still work. */
export type BuiltinTool = RegistryTool

// AgentToolsCfg — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/AgentToolsCfg.yaml.

// AgentToolEntry — re-exported from generated openapi-types (no local body needed).

/** Fetch all tools from the central registry (FR-027). Includes both builtin and MCP tools. */
export function fetchRegistryTools(): Promise<RegistryTool[]> {
  return request<RegistryTool[]>('/tools', undefined, z.array(ToolRegistryEntrySchema) as ZodType<RegistryTool[]>)
}

/** Backward-compat alias — callers that used fetchBuiltinTools() still work. */
export const fetchBuiltinTools = fetchRegistryTools

export function fetchMcpServersForAgent(): Promise<McpServer[]> {
  return request<McpServer[]>('/mcp-servers', undefined, z.array(McpServerSchema) as ZodType<McpServer[]>)
}

// AgentToolsResponse — imported from generated openapi-types (contract-first #8).
// AgentToolsResponseSchema — imported from generated schemas (contract-first #8).
export function fetchAgentTools(agentId: string): Promise<AgentToolsResponse> {
  return request<AgentToolsResponse>(`/agents/${encodeURIComponent(agentId)}/tools`, undefined, AgentToolsResponseSchema as ZodType<AgentToolsResponse>)
}

// updateAgentTools persists per-agent tool policies. It is re-auth gated
// server-side (requireReAuth): pass a consent token from useReAuthGate/runGated
// via reAuthToken to replay it in the X-Reauth-Token header. The first call
// may pass '' (no token); if the server demands re-auth, runGated opens the
// dialog and retries with the minted token. // not-wire-format
export function updateAgentTools(
  agentId: string,
  cfg: AgentToolsCfg,
  reAuthToken?: string,
): Promise<AgentToolsResponse> {
  return request<AgentToolsResponse>(`/agents/${encodeURIComponent(agentId)}/tools`, {
    method: 'PUT',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
    body: JSON.stringify(cfg),
  }, AgentToolsResponseSchema as ZodType<AgentToolsResponse>)
}

/**
 * POST /api/v1/tool-approvals/{approvalId} — resolve a pending tool approval.
 * FR-011, FR-082. Throws with status code prefix on non-2xx (e.g. "403: ...").
 */
export function postToolApproval(approvalId: string, action: 'approve' | 'deny' | 'cancel'): Promise<void> {
  // no-schema: void response; POST returns 204 No Content.
  return request<void>(`/tool-approvals/${encodeURIComponent(approvalId)}`, {
    method: 'POST',
    body: JSON.stringify({ action }),
  })
}

// ── Global Tool Policies ──────────────────────────────────────────────────────
// GlobalToolPolicies — re-exported from generated openapi-types (no local body needed).

export function fetchGlobalToolPolicies(): Promise<GlobalToolPolicies> {
  return request<GlobalToolPolicies>('/security/tool-policies', undefined, GlobalToolPoliciesSchema)
}

// updateGlobalToolPolicies persists the global tool-policy grant. It is re-auth
// gated (Spec-3 FR-3.3 / Spec-6 FR-12.2): the server rejects the PUT with 403
// unless a single-use consent token (from reAuth) is replayed in the
// X-Reauth-Token header.
export function updateGlobalToolPolicies(
  cfg: GlobalToolPolicies,
  reAuthToken?: string,
): Promise<GlobalToolPolicies> {
  return request<GlobalToolPolicies>('/security/tool-policies', {
    method: 'PUT',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
    body: JSON.stringify(cfg),
  }, GlobalToolPoliciesSchema)
}

// ── Sandbox Status ────────────────────────────────────────────────────────────
// SandboxStatus — re-exported from generated openapi-types (no local body needed).
// The generated schema is a superset of the previous hand-written shape.

export function fetchSandboxStatus(): Promise<SandboxStatus> {
  return request<SandboxStatus>('/security/sandbox-status', undefined, SandboxStatusSchema)
}

// ── Tool Results (lazy fetch for ToolResultRef sentinels) ─────────────────────
// Endpoint: GET /api/v1/sessions/{session_id}/tool-results/{ref}
// Session-scoped: a ref is only readable in the session that produced it.
export function fetchToolResult(sessionId: string, ref: string): Promise<unknown> {
  return request<unknown>(
    `/sessions/${encodeURIComponent(sessionId)}/tool-results/${encodeURIComponent(ref)}`,
  )
}

// ── Workspaces ────────────────────────────────────────────────────────────────
//
// Workspaces are lightweight metadata records (no filesystem dirs). All types are
// re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/Workspace*.yaml.

export const workspacesQueryKeys = {
  list: (params?: { status?: string }) => ['workspaces', params] as const,
  detail: (id: string) => ['workspaces', id] as const,
  delegation: (id: string) => ['workspaces', id, 'delegation'] as const,
  instructions: (id: string) => ['workspaces', id, 'instructions'] as const,
}

export function fetchWorkspaces(params?: { status?: string }): Promise<Workspace[]> {
  const qs = params?.status ? '?' + new URLSearchParams({ status: params.status }).toString() : ''
  return request<Workspace[]>(`/workspaces${qs}`, undefined, z.array(WorkspaceSchema) as ZodType<Workspace[]>)
}

/**
 * Fetch a single workspace by id.
 * Used by the AgentProfile Heartbeat tab (FR-016 / US-5) to read the current
 * member_configs for the (workspace, agent) heartbeat pair.
 */
export function fetchWorkspace(id: string): Promise<Workspace> {
  return request<Workspace>(
    `/workspaces/${encodeURIComponent(id)}`,
    undefined,
    WorkspaceSchema as ZodType<Workspace>,
  )
}

export function createWorkspace(body: WorkspaceCreateRequest): Promise<Workspace> {
  return request<Workspace>(
    '/workspaces',
    { method: 'POST', body: JSON.stringify(body) },
    WorkspaceSchema as ZodType<Workspace>,
  )
}

export function updateWorkspace(id: string, body: WorkspaceUpdateRequest): Promise<Workspace> {
  return request<Workspace>(
    `/workspaces/${encodeURIComponent(id)}`,
    { method: 'PUT', body: JSON.stringify(body) },
    WorkspaceSchema as ZodType<Workspace>,
  )
}

export function deleteWorkspace(id: string): Promise<void> {
  return request<void>(`/workspaces/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

// ── Per-workspace delegation graph (M5) ─────────────────────────────────────────
//
// The delegation graph is the workspace's source of truth for who-delegates-to-
// whom. The Team tab edits it as a node-and-edge graph and persists the WHOLE
// edge set on each change (full replace, not merge — see WorkspaceDelegation
// UpdateRequest). `team[]` on the read response is computed server-side (union of
// core_team + every agent named by an edge) so the editor can render isolated
// member nodes that have no edges yet.

export function fetchWorkspaceDelegation(id: string): Promise<WorkspaceDelegation> {
  return request<WorkspaceDelegation>(
    `/workspaces/${encodeURIComponent(id)}/delegation`,
    undefined,
    WorkspaceDelegationSchema as ZodType<WorkspaceDelegation>,
  )
}

export function updateWorkspaceDelegation(
  id: string,
  edges: WorkspaceDelegationEdge[],
): Promise<WorkspaceDelegation> {
  const body: WorkspaceDelegationUpdateRequest = { edges }
  return request<WorkspaceDelegation>(
    `/workspaces/${encodeURIComponent(id)}/delegation`,
    { method: 'PUT', body: JSON.stringify(body) },
    WorkspaceDelegationSchema as ZodType<WorkspaceDelegation>,
  )
}

// ── Workspace / Project Instructions ─────────────────────────────────────────
//
// Per-workspace AGENT.md content — applied to every agent working in the
// workspace, on top of their persona. Contract-first per Constraint #8.
// See contracts/components/schemas/WorkspaceInstructions*.yaml.

export function fetchWorkspaceInstructions(workspaceId: string): Promise<WorkspaceInstructionsResponse> {
  return request<WorkspaceInstructionsResponse>(
    `/workspaces/${encodeURIComponent(workspaceId)}/instructions`,
    undefined,
    WorkspaceInstructionsResponseSchema as ZodType<WorkspaceInstructionsResponse>,
  )
}

export function updateWorkspaceInstructions(
  workspaceId: string,
  content: string,
): Promise<WorkspaceInstructionsResponse> {
  const body: WorkspaceInstructionsRequest = { content }
  return request<WorkspaceInstructionsResponse>(
    `/workspaces/${encodeURIComponent(workspaceId)}/instructions`,
    { method: 'PUT', body: JSON.stringify(body) },
    WorkspaceInstructionsResponseSchema as ZodType<WorkspaceInstructionsResponse>,
  )
}

// ── Milestones ────────────────────────────────────────────────────────────────
//
// Milestones are scoped to a workspace. All types are re-exported from generated
// openapi-types (contract-first #8). See contracts/components/schemas/Milestone*.yaml.
//
// `progress` (0–1 completion fraction) is a generated, read-only field on Milestone:
// computed server-side at read time (done/total over the milestone's GTD board tasks).
// It is optional in the schema and absent on create/update echoes when no tasks exist.

export const milestonesQueryKeys = {
  list: (workspaceId: string) => ['milestones', workspaceId] as const,
  detail: (workspaceId: string, milestoneId: string) => ['milestones', workspaceId, milestoneId] as const,
}

const MilestoneListResponseSchema = z.object({
  milestones: z.array(MilestoneSchema),
  total: z.number().int(),
})

export function fetchMilestones(workspaceId: string): Promise<Milestone[]> {
  return request<{ milestones: Milestone[]; total: number }>(
    `/workspaces/${encodeURIComponent(workspaceId)}/milestones`,
    undefined,
    MilestoneListResponseSchema,
  ).then((res) => res.milestones)
}

export function getMilestone(workspaceId: string, milestoneId: string): Promise<Milestone> {
  return request<Milestone>(
    `/workspaces/${encodeURIComponent(workspaceId)}/milestones/${encodeURIComponent(milestoneId)}`,
    undefined,
    MilestoneSchema,
  )
}

export function createMilestone(workspaceId: string, body: MilestoneCreateRequest): Promise<Milestone> {
  return request<Milestone>(
    `/workspaces/${encodeURIComponent(workspaceId)}/milestones`,
    { method: 'POST', body: JSON.stringify(body) },
    MilestoneSchema,
  )
}

export function updateMilestone(workspaceId: string, milestoneId: string, body: MilestoneUpdateRequest): Promise<Milestone> {
  return request<Milestone>(
    `/workspaces/${encodeURIComponent(workspaceId)}/milestones/${encodeURIComponent(milestoneId)}`,
    { method: 'PUT', body: JSON.stringify(body) },
    MilestoneSchema,
  )
}

export function deleteMilestone(workspaceId: string, milestoneId: string): Promise<void> {
  return request<void>(
    `/workspaces/${encodeURIComponent(workspaceId)}/milestones/${encodeURIComponent(milestoneId)}`,
    { method: 'DELETE' },
  )
}

// ── Token Usage Stats ─────────────────────────────────────────────────────────
//
// Token usage summary by agent for the current month.
// See contracts/components/schemas/TokenUsageSummary.yaml.

export type TokenStatsPeriod = 'day' | 'week' | 'month' | 'all'

export const tokenStatsQueryKeys = {
  monthly: () => ['token-stats', 'month'] as const,
  byPeriod: (period: TokenStatsPeriod) => ['token-stats', period] as const,
}

export const auditLogQueryKeys = {
  list: () => ['audit-log'] as const,
}

export function fetchTokenStats(period: TokenStatsPeriod = 'month'): Promise<TokenUsageSummary> {
  return request<TokenUsageSummary>(
    `/stats/tokens?period=${period}`,
    undefined,
    TokenUsageSummarySchema as ZodType<TokenUsageSummary>,
  )
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
