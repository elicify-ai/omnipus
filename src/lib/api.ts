// REST API client — all calls go through the backend gateway.
// Auth (US-5 / FR-010): the browser-managed `omnipus-session` HttpOnly cookie.
// Every request sends `credentials: 'include'` so the cookie rides along
// automatically; the SPA never reads, stores, or sends a JS-visible bearer
// token (that remains a *server*-side path for programmatic/CLI clients only
// — see ADR-044). Logout is server-authoritative: `logout()` below calls
// `POST /api/v1/auth/logout`, which clears the cookie.
// CSRF: X-CSRF-Token header echoes the __Host-csrf (or plain-HTTP `csrf`)
// cookie value, read FRESH from document.cookie on every state-changing
// request (never cached — see readCSRFCookie). The cookie is issued by the
// backend on /auth/login and /onboarding/complete, and re-minted on any
// authenticated safe (GET) request that lacks it (FR-019). State-changing
// calls made before the cookie exists fail fast client-side so the UI
// surfaces an actionable error instead of waiting for the server's 403; a
// 403 caused by an actually-expired/missing cookie (e.g. a returning user's
// first action is a POST) is recovered automatically by issuing a safe GET
// (which triggers the server re-mint) and retrying the original request
// once — see the retry logic in request().
//
// Errors: request() throws ApiError on non-2xx responses and on transport
// failures (network down, fetch threw). Callers should branch on err.status
// (or err.isAuthError() / err.isNotFound() / err.isRateLimited() / etc)
// rather than regex-matching err.message — see src/lib/api-error.ts.

import { ApiError, isApiError as isApiErrorFn } from './api-error'
export { ApiError, isApiError, getErrorMessage } from './api-error'
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
//
// GET /about is NOT in the gap list above — it has a hand-written local schema
// (AboutInfoSchema, defined next to the AboutInfo interface) rather than the
// generated AboutResponse schema, because its field names genuinely drift from
// the wire contract (uptime_seconds vs uptime) and several fields are
// legitimately absent on older gateway versions. The local schema validates
// the fields that must always be present and leaves the version-gated fields
// optional, so it still rejects a genuinely malformed response without
// false-negativing on backward compatibility.

import {
  ChannelRouting as ChannelRoutingSchema,
  LoginResponse as LoginResponseSchema,
  ProbeProviderResponse as ProbeProviderResponseSchema,
  Agent as AgentSchema,
  AgentSession as AgentSessionSchema,
  AuditLogResponse as AuditLogResponseSchema,
  ExecAllowlist as ExecAllowlistSchema,
  ExecProxyStatus as ExecProxyStatusSchema,
  GlobalToolPolicies as GlobalToolPoliciesSchema,
  PendingRestartEntry as PendingRestartEntrySchema,
  PromptGuardResponse as PromptGuardResponseSchema,
  SandboxConfig as SandboxConfigSchema,
  SandboxStatus as SandboxStatusSchema,
  SkillTrustResponse as SkillTrustResponseSchema,
  // New generated Zod schemas (contract-first #8):
  AppState as AppStateSchema,
  ValidateTokenResponse as ValidateTokenResponseSchema,
  DoctorResult as DoctorResultSchema,
  DevicesResponse as DevicesResponseSchema,
  BackupEntry as BackupEntrySchema,
  StorageStats as StorageStatsSchema,
  // Newly wired schemas:
  Provider as ProviderSchema,
  // D18 — model-capabilities warn-and-proceed (contract-first #8):
  ModelCapabilities as ModelCapabilitiesSchema,
  CliDetect as CliDetectSchema,
  // external-executor-cli-path-detection spec (ADR-030): create-time validate.
  CliValidateResponse as CliValidateResponseSchema,
  // Agent System P0 fix: real auto-applied CLI flags (replaces misleading
  // placeholder ghost-text in the executor cli_args field).
  ExecutorDefaults as ExecutorDefaultsSchema,
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
  ActivityEventsResponse as ActivityEventsResponseSchema,
  ClearAllSessionsResponse as ClearAllSessionsResponseSchema,
  // Wire-shape schemas used for raw-to-SPA transform validation:
  Message as WireMessageSchema,
  Session as WireSessionSchema,
  // Newly promoted from inline openapi.yaml schemas:
  SkillTrustUpdateResponse as SkillTrustUpdateResponseSchema,
  PromptGuardUpdateResponse as PromptGuardUpdateResponseSchema,
  AgentToolsResponse as AgentToolsResponseSchema,
  OperationResult as OperationResultSchema,
  UploadFilesResponse as UploadFilesResponseSchema,
  BackupCreateResponse as BackupCreateResponseSchema,
  RotateTokenResponse as RotateTokenResponseSchema,
  // fix-AC: promoted from hand-written inline schemas:
  UserContextResponse as UserContextResponseSchema,
  McpServerToolsResponse as McpServerToolsResponseSchema,
  // #264 Notifications (contract-first #8):
  NotificationList as NotificationListSchema,
  // Level-1 workspaces + unified tasks + token stats (contract-first #8):
  Workspace as WorkspaceSchema,
  // M5 per-workspace delegation graph (contract-first #8):
  WorkspaceDelegation as WorkspaceDelegationSchema,
  // Workspace / Project Instructions (contract-first #8):
  WorkspaceInstructionsResponse as WorkspaceInstructionsResponseSchema,
  Task as TaskSchema,
  // Per-task run history (ADR-050 / task-run-history-spec §4.1):
  TaskRun as TaskRunSchema,
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
  GodModeUpdateResponse as GodModeUpdateResponseSchema,
  // Slash-command harmonization (contract-first #8):
  SlashCommand as SlashCommandSchema,
  // Memory/recap settings (workspace-heartbeat-memory-config-spec.md FR-019):
  MemorySettings as MemorySettingsSchema,
  // M11 per-(agent, workspace) email mailbox account (contract-first #8):
  Mailbox as MailboxSchema,
  MailboxListResponse as MailboxListResponseSchema,
  // ADR-039 — user-initiated browsing + annotate-a-region-and-discuss:
  BrowserInspectResponse as BrowserInspectResponseSchema,
  // ADR-051 Rev 4 — workspace media library (contract-first #8):
  MediaLibraryEntry as MediaLibraryEntrySchema,
  // library-spec.md — Library file explorer over workspace work/ trees
  // (contract-first #8), supersedes the media library above. Only response
  // schemas are imported (request bodies are validated server-side, matching
  // the existing convention — see the comment above ExecutorCommandPreviewResponse).
  LibraryWorkspaceNode as LibraryWorkspaceNodeSchema,
  LibraryEntry as LibraryEntrySchema,
  LibraryContentResponse as LibraryContentResponseSchema,
  LibraryUploadResponse as LibraryUploadResponseSchema,
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
  // D18 — model-capabilities warn-and-proceed (contract-first #8):
  ModelCapabilities,
  CliDetect,
  CliDetectEntry,
  CliValidateRequest,
  CliValidateResponse,
  // Agent System P0 fix: real auto-applied CLI flags.
  ExecutorDefaults,
  GatewayStatus,
  Skill,
  SkillSearchResult,
  SkillMarketplaceStatus,
  SkillInstallRequest,
  ActivityEvent,
  ActivityEventsResponse,
  ClearAllSessionsResponse,
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
  // Newly promoted from inline openapi.yaml schemas:
  SkillTrustUpdateRequest,
  SkillTrustUpdateResponse,
  PromptGuardUpdateRequest,
  PromptGuardUpdateResponse,
  SessionScopeUpdateResponse,
  ChannelEnabledResponse,
  AgentToolsResponse,
  UploadFilesResponse,
  BackupCreateResponse,
  OperationResult,
  // fix-AC: promoted from hand-written inline schemas:
  UserContextResponse,
  McpServerToolsResponse,
  AgentUpdateRequest,
  AgentCreateRequest,
  AgentCreateRequestMain,
  AgentCreateRequestSubagent,
  AgentCreateRequestSubagent3p,
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
  // Per-task run history (ADR-050 / task-run-history-spec §4.1):
  TaskRun,
  RunNowRequest,
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
  // Real, live command-line preview for a subagent_3p executor (contract-first #8):
  ExecutorCommandPreviewRequest,
  ExecutorCommandPreviewResponse,
  // Real, imperative "send a test message" run for a subagent_3p executor
  // (contract-first #8):
  ExecutorSmokeTestRequest,
  ExecutorSmokeTestResponse,
  // Version drift detection (used by useVersionCheck):
  VersionResponse,
  // Voice provider capability detection (used by voice-provider-detect):
  VoiceProvider,
  // O4 gateway self-restart (contract-first #8):
  GatewayRestartResponse,
  // O14 god-mode switch (contract-first #8):
  GodModeStatus,
  GodModeUpdateRequest,
  GodModeUpdateResponse,
  // Slash-command harmonization (contract-first #8):
  SlashCommand,
  // Memory/recap settings (workspace-heartbeat-memory-config-spec.md FR-019):
  MemorySettings,
  // M11 per-(agent, workspace) email mailbox account (contract-first #8):
  Mailbox,
  MailboxConfigureRequest,
  // Tool-approval "always" grant action (commit 35447760, contract-first #8):
  ToolApprovalActionRequest,
  // ADR-039 — user-initiated browsing + annotate-a-region-and-discuss:
  BrowserInspectRequest,
  BrowserInspectResponse,
  // ADR-051 Rev 4 — workspace media library (contract-first #8):
  MediaLibraryEntry,
  MediaAttachmentRequest,
  // library-spec.md — Library file explorer over workspace work/ trees (contract-first #8):
  LibraryWorkspaceNode,
  LibraryEntry,
  LibraryContentResponse,
  LibraryContentRequest,
  LibraryMkdirRequest,
  LibraryRenameRequest,
  LibraryUploadResponse,
  LibraryTransferRequest,
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
  // D18 — model-capabilities warn-and-proceed (contract-first #8):
  ModelCapabilities,
  CliDetect,
  CliDetectEntry,
  CliValidateRequest,
  CliValidateResponse,
  // Agent System P0 fix: real auto-applied CLI flags.
  ExecutorDefaults,
  GatewayStatus,
  Skill,
  SkillSearchResult,
  SkillMarketplaceStatus,
  SkillInstallRequest,
  ActivityEvent,
  ActivityEventsResponse,
  ClearAllSessionsResponse,
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
  // Promoted from inline openapi.yaml schemas:
  SkillTrustUpdateRequest,
  SkillTrustUpdateResponse,
  PromptGuardUpdateRequest,
  PromptGuardUpdateResponse,
  SessionScopeUpdateResponse,
  ChannelEnabledResponse,
  AgentToolsResponse,
  UploadFilesResponse,
  BackupCreateResponse,
  OperationResult,
  // fix-AC: promoted from hand-written inline schemas:
  UserContextResponse,
  McpServerToolsResponse,
  AgentUpdateRequest,
  AgentCreateRequest,
  AgentCreateRequestMain,
  AgentCreateRequestSubagent,
  AgentCreateRequestSubagent3p,
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
  // Per-task run history (ADR-050 / task-run-history-spec §4.1):
  TaskRun,
  RunNowRequest,
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
  // Real, live command-line preview for a subagent_3p executor:
  ExecutorCommandPreviewRequest,
  ExecutorCommandPreviewResponse,
  // Real, imperative "send a test message" run for a subagent_3p executor:
  ExecutorSmokeTestRequest,
  ExecutorSmokeTestResponse,
  // O4 gateway self-restart:
  GatewayRestartResponse,
  // O14 god-mode switch:
  GodModeStatus,
  GodModeUpdateRequest,
  // Slash-command harmonization (contract-first #8):
  SlashCommand,
  // Memory/recap settings (workspace-heartbeat-memory-config-spec.md FR-019):
  MemorySettings,
  // M11 per-(agent, workspace) email mailbox account:
  Mailbox,
  MailboxConfigureRequest,
  // ADR-039 — user-initiated browsing + annotate-a-region-and-discuss:
  BrowserInspectRequest,
  BrowserInspectResponse,
  // ADR-051 Rev 4 — workspace media library (contract-first #8):
  MediaLibraryEntry,
  MediaAttachmentRequest,
  // library-spec.md — Library file explorer over workspace work/ trees (contract-first #8):
  LibraryWorkspaceNode,
  LibraryEntry,
  LibraryContentResponse,
  LibraryContentRequest,
  LibraryMkdirRequest,
  LibraryRenameRequest,
  LibraryUploadResponse,
  LibraryTransferRequest,
}

const BASE_URL = import.meta.env.VITE_API_URL ?? ''

// The server issues one of two cookie names depending on the request's TLS
// state. TLS: __Host-csrf (browser enforces Secure + Path=/ + no Domain).
// Plain HTTP: csrf (no __Host- prefix, Secure=false) — __Host- cookies are
// silently dropped by browsers on non-localhost plain-HTTP origins.
// Keep both constants in sync with pkg/gateway/middleware/csrf.go.
const CSRF_COOKIE_NAME = '__Host-csrf'
const CSRF_COOKIE_NAME_HTTP = 'csrf'
export const CSRF_HEADER_NAME = 'X-CSRF-Token'
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
const CSRF_EXEMPT_PATHS = new Set<string>([
  '/api/v1/onboarding/complete',
  '/api/v1/onboarding/probe-provider',
  '/api/v1/auth/login',
])

// readCSRFCookie parses document.cookie and returns the __Host-csrf (or
// plain-HTTP `csrf`) value, or null if neither cookie is present. We
// intentionally do not cache — cookies can change after login/logout/
// onboarding/re-mint, and caching would cause stale tokens on the next
// state-changing call. Called fresh on every request (FR-010, r4-MAJ-004).
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

/**
 * getCsrfCookie is the public form of readCSRFCookie, for the rare caller
 * that must build its own fetch() outside request() (e.g. the useAutoSave
 * pagehide/visibilitychange keepalive beacon, which fires a raw
 * `fetch(..., {keepalive:true})` that cannot go through the async request()
 * pipeline). Kept as a thin wrapper so there is exactly one cookie-parsing
 * implementation — see the "byte-for-byte identical" caution in
 * api.test.ts's own reimplementation of this logic.
 */
export function getCsrfCookie(): string | null {
  return readCSRFCookie()
}

// buildHeaders composes the standard request headers, layering (in order):
// content-type → CSRF header (read fresh) → caller overrides. There is no
// Authorization header here: the SPA authenticates via the omnipus-session
// cookie (sent automatically by the browser via credentials:'include' in
// performRequest below), never a JS-visible bearer token (US-5 / FR-010).
function buildHeaders(extra?: HeadersInit): HeadersInit {
  const csrf = readCSRFCookie()
  return {
    'Content-Type': 'application/json',
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

// isCsrfFailureBody distinguishes a CSRF-caused 403 from a generic
// authorization/permission 403 (e.g. RBAC denial, agent-locked). The
// gateway's CSRF middleware (pkg/gateway/middleware/csrf.go writeCSRFError)
// always responds with {"error":"csrf cookie missing"|"csrf header
// missing"|"csrf token mismatch"} on a CSRF rejection — every other 403
// uses unrelated wording. We match on this fixed vocabulary (via
// ApiError.body, the raw response text) rather than a machine code because
// the endpoint does not emit one.
function isCsrfFailureBody(body: string | undefined): boolean {
  if (!body) return false
  return /csrf/i.test(body)
}

/**
 * withCsrfRetry wraps a single state-changing attempt and recovers from a
 * CSRF-specific 403 exactly once (FR-010 / FR-019 / r4-MAJ-004): a returning
 * user whose CSRF cookie expired (browser reopened same-day) — or whose very
 * first action is a state-changing call before any GET has hit the server —
 * gets rejected by the server's double-submit check even though a client-
 * side presence check (where one exists) saw a cookie. Recovery: issue a
 * safe GET (the gateway re-mints the CSRF cookie on any authenticated safe
 * request that lacks one), then re-run `attempt` with a freshly read cookie.
 * If the retry also fails, that failure is surfaced — this can never loop
 * more than once because `attempt` is invoked directly, not through this
 * wrapper again.
 *
 * Shared by request() (JSON calls) and the two multipart raw-fetch call
 * sites (uploadFiles, transcribeAudio) so the recovery logic isn't
 * duplicated three times.
 */
async function withCsrfRetry<T>(attempt: () => Promise<T>): Promise<T> {
  try {
    return await attempt()
  } catch (err) {
    if (isApiErrorFn(err) && err.status === 403 && isCsrfFailureBody(err.body)) {
      try {
        await performRequest('/state', undefined, undefined)
      } catch {
        // Best-effort re-mint trigger — proceed to retry regardless of
        // whether the GET itself succeeded.
      }
      return await attempt()
    }
    throw err
  }
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
  const stateChanging = STATE_CHANGING_METHODS.has(method)
  if (
    stateChanging &&
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

  const attempt = () => performRequest<T>(path, init, schema)
  return stateChanging ? withCsrfRetry(attempt) : attempt()
}

async function performRequest<T>(path: string, init?: RequestInit, schema?: ZodType<T>): Promise<T> {
  const method = (init?.method ?? 'GET').toUpperCase()
  let res: Response
  try {
    res = await fetch(`${BASE_URL}/api/v1${path}`, {
      ...init,
      // Auth is the omnipus-session HttpOnly cookie — always send it (and
      // accept the server's Set-Cookie, e.g. a CSRF re-mint) even when
      // BASE_URL points at a different origin in dev (US-5 / FR-010).
      credentials: 'include',
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
  // deleteSkill, deleteMcpServer, deleteCredential, deleteSchedule, …).
  // clearAllSessions is NOT one of these — it returns HTTP 200 with a JSON
  // body, handled by the parse path below. We deliberately do NOT key off
  // Content-Type here: a non-JSON body with actual content (e.g. an HTML
  // error page served with a misconfigured 200) is a different failure
  // that must still flow to the schema/JSON-parse path below so it
  // surfaces as an ApiSchemaError, not a
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

// AssigneeTeamScope — F2: an explicit choice, not an absent optional. The
// prior signature took `teamIds?: Set<string>` as a bare positional, which
// meant a future `buildTaskAssigneeItems(agents)` call silently compiled and
// returned the unscoped roster (the omission is invisible at the call site).
// Forcing callers to name their intent (`scoped` vs `unscoped`) makes that
// mistake a type error instead of a silent behavior change. Mirrors the
// `{ kind: '...' }` discriminated-union idiom already used for hook state in
// this codebase (see `CliValidationState` in useCliPathValidation.ts).
// not-wire-format: UI-only helper type consumed by buildTaskAssigneeItems; never serialized across the gateway/SPA boundary
export type AssigneeTeamScope =
  | { kind: 'scoped'; ids: Set<string> }
  | { kind: 'unscoped' }

// buildTaskAssigneeItems — shared task-assignee `SmartSelect` item list,
// deduped out of `TaskDetailPanel` and `CreateTaskSlideOver` (Simplify
// finding, Agent System P0 fix-wave). Scoped to the task's WORKSPACE TEAM
// (core_team ∪ every delegation edge endpoint — see `useWorkspaceTeamIds`)
// so the picker mirrors what the backend actually allows instead of the
// global agent roster: `validateTaskAgentID` (`pkg/gateway/rest_tasks.go`)
// 400s any assignee outside that set. subagent_3p (external-CLI) workers are
// NO LONGER unconditionally excluded here (Fix B) — team membership is the
// only gate task assignment goes through now that external-CLI task
// execution is being wired up alongside this change, so a 3p worker that IS
// on the team is a legitimate, non-dead-end assignee. A " · Worker" suffix
// keeps every delegation-only kind (Subagent / subagent_3p / legacy worker)
// visually distinguishable (mirrors AddAgentPicker's " · leaf" convention).
// Callers prepend their own "Unassigned" (`__none__`) item.
//
// `options.teamScope` (F2 — see `AssigneeTeamScope` above):
//   - `{ kind: 'scoped', ids }` → scope the list to `ids`' members (the
//     normal, team-scoped case).
//   - `{ kind: 'unscoped' }` → NO scoping — every known agent is offered.
//     Callers pass this explicitly for the deliberate fallback when the
//     team-set query ERRORS (or has no data yet) — the backend still
//     enforces team membership server-side, so an unscoped list here is a
//     graceful degrade (matches this picker's pre-scoping behaviour) rather
//     than an empty, unusable picker. Pair an error-driven `unscoped` choice
//     with an inline "team unavailable" hint at the call site (see
//     `useWorkspaceTeamIds`) so the degrade is visible, not silent.
//
// `options.currentAssigneeId`, when given, is always included even if it
// falls outside a `scoped` set — an existing task's already-assigned agent
// (e.g. one later dropped from the workspace team — legacy data) must still
// render as the selected value instead of silently vanishing from its own
// picker.
export function buildTaskAssigneeItems(
  agents: Agent[],
  options: { teamScope: AssigneeTeamScope; currentAssigneeId?: string | null },
): { value: string; label: string; className: string }[] {
  const { teamScope, currentAssigneeId } = options
  return agents
    .filter((a) => teamScope.kind === 'unscoped' || teamScope.ids.has(a.id) || a.id === currentAssigneeId)
    .map((a) => ({
      value: a.id,
      label: isWorker(a) ? `${a.name} · Worker` : a.name,
      className: 'text-xs',
    }))
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

// ── Per-item message-list resilience (Issue 3 / library-uat HIGH) ────────────
//
// GET /sessions/{id}/messages, and the `messages` array nested inside
// GET /sessions/{id} (SessionDetail), are LIST responses. Before this fix
// both validated the ENTIRE array against z.array(WireMessageSchema) in one
// shot: a single malformed entry (e.g. a future/unknown EntryType, or — the
// case reproduced live by all three UAT testers — an attachment `type`
// value outside the Attachment enum) rejected the whole array, so one bad
// historical row made the ENTIRE session appear unrecoverably empty
// ("Could not load messages." + a Retry that can never succeed, since the
// same bad row comes back every time).
//
// Per CLAUDE.md hard-constraint #8, the SPA edge validates every incoming
// payload and on failure should drop + counter + dev-mode toast, with NO
// prod crash. That machinery already existed (_recordApiSchemaError below)
// but this call site still threw the whole batch. Fixed here by validating
// each element independently: keep the valid ones, and count + surface the
// invalid ones through the EXISTING _recordApiSchemaError path (no parallel
// counter — see fetchSkills/fetchCommands below for an older, simpler
// per-item pattern that predates _recordApiSchemaError and does NOT feed
// the shared counter; this one deliberately does).
//
// Scoping note: this degrade-per-item treatment applies ONLY to the
// `messages` LIST. The `session` object nested alongside it in
// SessionDetail is a single-object response and still fails loudly via the
// normal request()/ApiSchemaError path (see fetchSessionDetail below) —
// blanket-suppressing single-object validation failures would hide real
// contract drift instead of exposing it.
//
// Judgment call — placeholder vs. silent drop: a dropped item is replaced
// with a minimal placeholder SystemMessage ("This message could not be
// displayed") rather than vanishing without a trace. Silently omitting the
// row is itself a mild silent failure: message counts and scrollback shift
// with no visible signal, and a user/support conversation about "where did
// my upload go" becomes undebuggable. A visible placeholder costs a little
// transcript noise but tells the truth — something was here and couldn't
// be rendered — while the rest of the conversation still loads normally.

function placeholderMessage(raw: unknown, index: number): SystemMessage {
  const obj = (raw !== null && typeof raw === 'object') ? raw as Record<string, unknown> : {}
  const id = typeof obj.id === 'string' && obj.id.length > 0 ? obj.id : `unrenderable-${index}`
  const timestamp = typeof obj.timestamp === 'string' && obj.timestamp.length > 0
    ? obj.timestamp
    : new Date().toISOString()
  return {
    id,
    session_id: undefined,
    role: 'system',
    content: 'This message could not be displayed.',
    timestamp,
    status: 'done',
  }
}

// Validates each element of a raw message-list body against the wire
// Message schema. Valid entries are transformed via rawToMessage(); invalid
// entries are counted through _recordApiSchemaError (endpoint + that item's
// own issue list) and replaced with placeholderMessage() so the list length
// and ordering the user sees still matches what the server actually holds.
// A single rate-limited dev toast summarises the drop (maybeDevToast is
// throttled per key, so a burst of bad items in one response doesn't spam
// the UI); production gets the existing _recordApiSchemaError telemetry.
function parseWireMessageList(items: unknown[], endpoint: string): Message[] {
  const messages: Message[] = []
  let dropped = 0
  let firstIssue: string | undefined
  items.forEach((item, index) => {
    const result = WireMessageSchema.safeParse(item)
    if (result.success) {
      messages.push(rawToMessage(result.data as RawMessage))
      return
    }
    dropped++
    firstIssue ??= result.error.issues[0]?.message
    _recordApiSchemaError(endpoint, result.error.issues.length)
    messages.push(placeholderMessage(item, index))
  })
  if (dropped > 0) {
    void maybeDevToast(
      `[api] Dropped ${dropped} malformed message${dropped === 1 ? '' : 's'} from ${endpoint}: ${firstIssue ?? 'unknown'}`,
      `${endpoint}:message-item-schema`,
    )
  }
  return messages
}

export async function fetchSessionMessages(sessionId: string): Promise<Message[]> {
  // Top-level shape assertion only: the body must be an array. A non-array
  // body (an error page, a wholly different endpoint shape) is a genuine
  // contract break and still fails loudly here. Each element is validated
  // and degraded individually by parseWireMessageList — see the block
  // comment above for why.
  const path = `/sessions/${encodeURIComponent(sessionId)}/messages`
  const rawItems = await request<unknown[]>(
    path,
    undefined,
    z.array(z.unknown()) as ZodType<unknown[]>,
  )
  return parseWireMessageList(rawItems, `GET /api/v1${path}`)
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
  // `session` (a single object) is still validated strictly via
  // WireSessionSchema and fails loudly on mismatch — same policy as any
  // other single-object GET. `messages` (a list) is loosened to
  // z.array(z.unknown()) at this top-level shape check and validated /
  // degraded per-item below via parseWireMessageList, so one malformed
  // historical message can't take down the whole session-detail view
  // (Issue 3 / library-uat HIGH finding — see the block comment above
  // fetchSessionMessages for the full rationale and the placeholder
  // judgment call).
  type RawSessionDetailShape = { session: RawSession; messages: unknown[]; agent_removed?: boolean }
  const shapeSchema = z.object({
    session: WireSessionSchema,
    messages: z.array(z.unknown()),
    agent_removed: z.boolean().optional(),
  })
  const path = `/sessions/${encodeURIComponent(sessionId)}`
  const raw = await request<RawSessionDetailShape>(
    path,
    undefined,
    shapeSchema as ZodType<RawSessionDetailShape>,
  )
  return {
    session: rawToSession(raw.session),
    messages: parseWireMessageList(raw.messages, `GET /api/v1${path}`),
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
    // preview_enabled: live toggle for /preview/ (ADR-044/FR-006). Default enabled.
    preview_enabled?: boolean
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
// (e.g. security.daily_cost_cap, security.rate_limits.*) is never a fully
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
      policy_mode: validEnum(security.policy_mode, VALID_POLICY_MODES, 'deny', 'security.policy_mode'),
      exec_approval: validEnum(security.exec_approval, VALID_EXEC_APPROVALS, 'ask', 'security.exec_approval'),
      prompt_injection_level: validEnum(security.prompt_injection_level, VALID_INJECTION_LEVELS, 'medium', 'security.prompt_injection_level'),
      // Spend/execution guardrails: a wrong-shaped value here is a bad
      // decision downstream, not just a display glitch — SecuritySection reads
      // these with `?.toString()` (which happily stringifies ANY type without
      // throwing) then feeds the result to parseFloat/parseInt on save, so a
      // corrupted-but-truthy value would silently NaN out and strip the
      // guardrail on the next PUT rather than failing loudly. Runtime-checked
      // and dropped to undefined (== "not configured") instead.
      daily_cost_cap: castOptionalNumber(security.daily_cost_cap, 'security.daily_cost_cap'),
      exec_timeout_seconds: castOptionalNumber(security.exec_timeout_seconds, 'security.exec_timeout_seconds'),
      max_background_seconds: castOptionalNumber(security.max_background_seconds, 'security.max_background_seconds'),
      enable_deny_patterns: security.enable_deny_patterns as boolean | undefined,
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
    if (data.gateway.preview_enabled !== undefined) gw.preview_enabled = data.gateway.preview_enabled
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

// D18: flat list of {id, modalities} from the backend's in-repo capability
// catalog (pkg/providers/capabilities) — model vision capability is not
// knowable client-side at all otherwise. Used to warn (non-blocking) before
// sending a vision attachment to a model that cannot see images. Empty array
// when the catalog is unavailable server-side (never an error the caller
// needs to branch on beyond the normal request() failure path).
export function fetchModelCapabilities(): Promise<ModelCapabilities[]> {
  return request<ModelCapabilities[]>(
    '/providers/model-capabilities',
    undefined,
    z.array(ModelCapabilitiesSchema) as ZodType<ModelCapabilities[]>,
  )
}

// D18: pure decision helper shared by the two vision-attachment send paths
// (browserAnnotate.ts's live-browser annotation submit, attachment-adapter.ts's
// composer image attach) — kept here (not duplicated) so both warn on the
// identical rule. Unknown/unlisted models return false (optimistic — mirrors
// the server-side FR-026 default in pkg/providers/capabilities/catalog.go),
// so a stale or incomplete capabilities fetch never spuriously blocks/warns.
//
// Mirrors pkg/providers/capabilities/catalog.go's Catalog.Resolve fix
// (2026-07-28, live UAT): agents' models are provider-prefixed
// ("z-ai/glm-5.2"), but the /providers/model-capabilities catalog is keyed
// by the BARE model id ("glm-5.2") — the vendor is recorded separately.
// An exact-string-only lookup on a prefixed id always misses, silently
// falling through to the optimistic default even when the catalog carries
// an authoritative (and possibly negative) entry for that exact model. See
// findModelCapabilityEntry below for the stripped-prefix fallback, which
// applies the identical semantics as the Go side.
export function modelLacksImageCapability(modelId: string | undefined, entries: ModelCapabilities[]): boolean {
  if (!modelId) return false
  const entry = findModelCapabilityEntry(modelId, entries)
  if (!entry) return false
  return !entry.modalities.includes('image')
}

// findModelCapabilityEntry mirrors pkg/providers/capabilities/catalog.go's
// Catalog.Resolve + resolveStrippedPrefix exactly: try an exact id match
// first (so a genuine bare catalog id like "gpt-4o", which never carries a
// vendor prefix, always wins outright and never reaches the fallback);
// then strip leading "<segment>/" prefixes one at a time — walking from the
// longest remaining suffix down to the bare trailing segment — retrying the
// exact lookup after each strip, stopping at the first hit. This also
// handles the double-prefixed "openrouter/z-ai/glm-5.2" onboarding artifact
// (both segments must be stripped to reach the bare "glm-5.2" catalog id).
// Can never produce a WRONG match: catalog ids are unique, so a stripped
// suffix that hits is, by construction, the intended model.
function findModelCapabilityEntry(modelId: string, entries: ModelCapabilities[]): ModelCapabilities | undefined {
  const exact = entries.find((c) => c.id === modelId)
  if (exact) return exact

  let rest = modelId
  for (;;) {
    const idx = rest.indexOf('/')
    if (idx < 0 || idx === rest.length - 1) return undefined
    rest = rest.slice(idx + 1)
    const match = entries.find((c) => c.id === rest)
    if (match) return match
  }
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

// fetchExecutorDefaults returns the static reference list of CLI flags
// Omnipus automatically applies to a subagent_3p executor invocation, one
// entry per supported CLI (claude-code / codex / opencode) — e.g. the
// non-interactive-posture flags each driver's `buildArgs` always sets itself
// (`pkg/agent/runner/driver_*.go`) and that `argsafety.go`
// (`filterDangerousCLIArgs`) prevents an operator's `executor_cli_args`
// free-text field from silently overriding. Rendered read-only in both the
// create wizard (Step1Identity → ExecutorInputs) and the edit form
// (AgentProfile) so operators see the REAL applied config instead of
// misleading placeholder ghost-text (Agent System P0 fix). Not agent-scoped
// and not filterable server-side — the endpoint always returns all three
// entries; callers select the one matching the currently-chosen CLI
// (`useExecutorDefaults`).
export function fetchExecutorDefaults(): Promise<ExecutorDefaults[]> {
  return request<ExecutorDefaults[]>(
    '/agents/executor-defaults',
    undefined,
    z.array(ExecutorDefaultsSchema) as ZodType<ExecutorDefaults[]>,
  )
}

// fetchExecutorPreview computes the REAL command line Omnipus would spawn for
// a subagent_3p external-CLI worker with the given settings — argv sourced
// from the same buildArgs() logic each driver (claude/codex/opencode) uses at
// real dispatch time, not a hand-maintained description like
// fetchExecutorDefaults above. Stateless and body-driven (mirrors
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
  // Per-task run history (ADR-050 / task-run-history-spec §4.1) — invalidated
  // by the task_run_status WS frame handler (src/store/chat.ts) so the
  // calendar slide-over and TaskDetailPanel's Runs list update live.
  runs: (id: string) => ['tasks', id, 'runs'] as const,
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

// ── Per-task run history (ADR-050 / task-run-history-spec §4.1) ────────────────
//
// TaskRun is a purely additive execution-record layer — Task.status/result/
// session_id keep their existing behaviour unchanged. GET /tasks/{id}/runs is
// the authoritative history list (retention-bounded, newest first, full
// result strings); POST /tasks/{id}/runs ("Run now") opens + dispatches a new
// run and returns 202 (fire-and-forget — observe progress via the
// task_run_status WS frame, see src/store/chat.ts, or by refetching this
// list). Foundation for the calendar slide-over + TaskDetailPanel Runs
// section (both consume the shared TaskRunsList component).

export function fetchTaskRuns(taskId: string): Promise<TaskRun[]> {
  return request<TaskRun[]>(`/tasks/${encodeURIComponent(taskId)}/runs`, undefined, z.array(TaskRunSchema) as ZodType<TaskRun[]>)
}

/**
 * POST /tasks/{id}/runs ("Run now", ADR-050 RD7).
 *
 * - `occurrenceMs` provided (including explicit `null`) → body carries
 *   `{occurrence_ms}`: materializes/re-runs that specific recurring
 *   occurrence (idempotent against a concurrent scheduler fire for the same
 *   instant).
 * - `occurrenceMs` omitted (`undefined`) → empty body: re-runs a normal/once
 *   task as a fresh run: the prior run (if any) is preserved in the run
 *   history, not overwritten.
 *
 * Returns 202 with no body — the run executes asynchronously; no response
 * schema to validate (request() resolves `undefined` for a schema-less,
 * bodyless 2xx).
 */
export function runTaskNow(taskId: string, occurrenceMs?: number | null): Promise<void> {
  const init: RequestInit = { method: 'POST' }
  if (occurrenceMs !== undefined) {
    const body: RunNowRequest = { occurrence_ms: occurrenceMs }
    init.body = JSON.stringify(body)
  }
  return request<void>(`/tasks/${encodeURIComponent(taskId)}/runs`, init)
}

// ── #264 Schedules ──────────────────────────────────────────────────────────────
// The SPA schedules client was DELETED (2026-07-19, operator directive): the
// Schedules UI is retired — scheduled/recurring work lives in the workspace
// Calendar; heartbeats are the only agent-level exception. The backend
// /api/v1/schedules entity and the pkg/cron engine remain (they execute task
// triggers + heartbeats). Do NOT reintroduce a schedules UI or these wrappers
// when merging older branches — see CLAUDE.md "Retired surfaces".

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

// ChannelRouting — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/ChannelRouting.yaml.

export function fetchChannelRouting(id: string): Promise<ChannelRouting> {
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
// Email is a TOOL (not a channel) with a per-(agent, workspace) mailbox
// account: a mailbox belongs to exactly one (agent, workspace) pair — the
// same agent can hold a different mailbox in each workspace it belongs to
// (different roles, different inboxes). Configured via the pair endpoints
// (GET/PUT/DELETE /api/v1/agents/{id}/mailboxes/{workspaceId}); wire types
// Mailbox + MailboxConfigureRequest are generated from contracts/openapi.yaml
// (#8). Both ids ride in the path — MailboxConfigureRequest carries no
// workspace_id member.

export const EMAIL_CHANNEL_ID = 'email'

/**
 * Fetch the mailbox an agent holds in a given workspace (M11 — email is a
 * TOOL surface, not a conversational channel). Routes through
 * GET /api/v1/agents/{id}/mailboxes/{workspaceId}. Returns null when that
 * pair has no mailbox configured (404) — every other error is rethrown. The
 * password is never returned; `configured` reports whether a stored
 * credential resolves.
 *
 * (The legacy GET /channels/email path is dead: the ADR-029 instance-key
 * grammar gate rejects "email" because it is deliberately not a channel type.)
 */
export async function fetchAgentMailbox(agentId: string, workspaceId: string): Promise<Mailbox | null> {
  try {
    return await request<Mailbox>(
      `/agents/${encodeURIComponent(agentId)}/mailboxes/${encodeURIComponent(workspaceId)}`,
      undefined,
      MailboxSchema,
    )
  } catch (err) {
    if (isApiErrorFn(err) && err.status === 404) return null
    throw err
  }
}

/**
 * List every configured mailbox via GET /api/v1/mailboxes (one per configured
 * (agent, workspace) pair). Never 404s — an empty list means none configured.
 * Preferred over per-pair probing: each probe 404 lands in the browser
 * console as an error and trips the e2e zero-console-errors gate.
 */
export async function fetchMailboxes(): Promise<Mailbox[]> {
  const res = await request<{ mailboxes: Mailbox[] }>(
    '/mailboxes',
    undefined,
    MailboxListResponseSchema as ZodType<{ mailboxes: Mailbox[] }>,
  )
  return res.mailboxes
}

/**
 * The first configured mailbox, or null. Convenience for callers that only
 * need "is any mailbox configured" — with multiple mailboxes, use
 * fetchMailboxes and address them individually.
 */
export async function findConfiguredMailbox(): Promise<Mailbox | null> {
  const mailboxes = await fetchMailboxes()
  return mailboxes[0] ?? null
}

/**
 * Configure the mailbox an agent holds in a given workspace via
 * PUT /api/v1/agents/{id}/mailboxes/{workspaceId}. The backend
 * credential-routes the password (never persisted in plaintext); omit
 * `password` to keep the stored credential. `req` carries no workspace_id —
 * both ids ride in the path.
 */
export function saveAgentMailbox(
  agentId: string,
  workspaceId: string,
  req: MailboxConfigureRequest,
): Promise<Mailbox> {
  return request<Mailbox>(
    `/agents/${encodeURIComponent(agentId)}/mailboxes/${encodeURIComponent(workspaceId)}`,
    { method: 'PUT', body: JSON.stringify(req) },
    MailboxSchema,
  )
}

/**
 * Delete the mailbox an agent holds in a given workspace
 * (DELETE /api/v1/agents/{id}/mailboxes/{workspaceId}). Mailboxes the agent
 * holds in other workspaces are untouched.
 */
export function deleteAgentMailbox(agentId: string, workspaceId: string): Promise<OperationResult> {
  return request<OperationResult>(
    `/agents/${encodeURIComponent(agentId)}/mailboxes/${encodeURIComponent(workspaceId)}`,
    { method: 'DELETE' },
    OperationResultSchema,
  )
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

export function updateMcpServer(id: string, body: McpServerUpdate): Promise<McpServer> {
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
// GET /activity returns ActivityEventsResponse ({ events, warning? }), not a bare
// array — the backend surfaces a `warning` when a session store was unreadable
// (partial results). See contracts/components/schemas/ActivityEventsResponse.yaml.

export function fetchActivity(): Promise<ActivityEventsResponse> {
  return request<ActivityEventsResponse>('/activity', undefined, ActivityEventsResponseSchema)
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

// ClearAllSessionsResponse — re-exported from generated openapi-types
// (contract-first #8). See contracts/components/schemas/ClearAllSessionsResponse.yaml.
// DELETE returns HTTP 200 with a JSON body { status, count, warnings? } (not
// 204 No Content — see contracts/openapi.yaml clearAllSessions). `warnings`
// carries non-fatal per-agent removal failures (pkg/session/unified.go's
// ClearAll() aggregates them via errors.Join); callers must inspect it
// rather than assuming full success.
export function clearAllSessions(): Promise<ClearAllSessionsResponse> {
  return request<ClearAllSessionsResponse>('/sessions/all', { method: 'DELETE' }, ClearAllSessionsResponseSchema)
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

export interface AboutInfo { // not-wire-format: SPA-internal backward-compatible subset of AboutResponse. The generated AboutResponse has required fields (uptime, pid, frame_ancestors_fallback) and different optionality for preview_enabled. The SPA uses this looser interface to maintain backward compatibility with older gateway versions that may not send all AboutResponse fields.
  version: string
  go_version: string
  os: string
  arch: string
  uptime_seconds: number
  // preview_enabled reflects whether gateway.preview_enabled is currently on
  // (US-4 / FR-006/FR-015, ADR-044). When true, /preview/ is served on the
  // main gateway listener (no separate preview listener/port/origin exists
  // anymore — the preview_port/preview_origin/preview_listener_enabled
  // fields this replaces are retired, not deprecated). Read live: toggling
  // the Settings → Gateway "Preview" switch takes effect on the next fetch,
  // no restart. Absent on old gateway versions that predate the field
  // (treat as true — the same long-standing-default-on convention the
  // retired preview_listener_enabled used).
  preview_enabled?: boolean
  // warmup_timeout_seconds is sourced from
  // cfg.Tools.RunInWorkspace.WarmupTimeoutSeconds (default 60). Used by
  // RunInWorkspaceUI to cap the warmup polling loop.
  warmup_timeout_seconds?: number
  // device_pairing_enabled reflects Sandbox.Experimental.DevicePairingEnabled —
  // a dark-launched flag (default false). Absent on old gateway versions that
  // predate the field (treat as false — opposite default from
  // preview_enabled, since this is a new opt-in feature, not a long-standing
  // one being made optional).
  device_pairing_enabled?: boolean
}

// AboutInfoSchema is a hand-written local schema (not from generated schemas —
// see the GAP REPORT note above the generated-schema import block). It
// mirrors the AboutInfo interface field-for-field: the five fields every
// gateway version has always sent are required; every field added since are
// `.optional()` so a response from an older gateway that predates them still
// validates. A response failing this schema (e.g. version/os/arch missing or
// wrong-typed) is a genuine contract break worth surfacing as ApiSchemaError.
const AboutInfoSchema: ZodType<AboutInfo> = z.object({
  version: z.string(),
  go_version: z.string(),
  os: z.string(),
  arch: z.string(),
  uptime_seconds: z.number(),
  preview_enabled: z.boolean().optional(),
  warmup_timeout_seconds: z.number().optional(),
  device_pairing_enabled: z.boolean().optional(),
})

export function fetchAboutInfo(): Promise<AboutInfo> {
  return request<AboutInfo>('/about', undefined, AboutInfoSchema)
}

/**
 * Returns the gateway's version string and build SHA. Used by the
 * `useVersionCheck` hook to detect version drift. The `/version` endpoint
 * is unauthenticated and lives outside the regular auth/CSRF envelope, so we
 * go through `request` (not raw `fetch`) to keep the request shape uniform
 * with every other call (credentials, error handling). The response is
 * validated by the generated `VersionResponse` Zod schema (per
 * `contracts/openapi.yaml`).
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
 * Returns whether the preview feature (gateway.preview_enabled) is on.
 *
 * `preview_enabled` is an optional bool where `undefined` means "true" — old
 * gateway versions that predate the field did not include it, and those
 * versions always served previews. Reading the field directly risks
 * treating `undefined` as falsy; use this accessor instead. Live: re-fetch
 * `/about` after toggling Settings → Gateway to see the new value (no
 * restart required — US-4/FR-006).
 */
export function isPreviewEnabled(info: AboutInfo | undefined): boolean {
  return info?.preview_enabled !== false
}

/**
 * Returns whether the (dark-launched) device-pairing feature is enabled.
 * Unlike `isPreviewEnabled`, `undefined` means "false" here — this is
 * a new opt-in feature (default off), not a long-standing one being made
 * backward-compatibly optional.
 */
export function isDevicePairingEnabled(info: AboutInfo | undefined): boolean {
  return info?.device_pairing_enabled === true
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

// ── File Upload ───────────────────────────────────────────────────────────────

// UploadedFile — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/UploadedFile.yaml.

export async function uploadFiles(sessionId: string, files: File[], workspaceId?: string): Promise<UploadFilesResponse> {
  const formData = new FormData()
  formData.append('session_id', sessionId)
  if (workspaceId) {
    formData.append('workspace_id', workspaceId)
  }
  for (const file of files) {
    formData.append('files', file)
  }
  // Upload is a state-changing POST — fail fast if we have no CSRF cookie
  // (see request() for the same pattern).
  if (readCSRFCookie() === null) {
    throw new ApiError(
      403,
      'CSRF cookie missing — cannot upload files. Log in first so the server can issue the CSRF cookie.',
      { code: 'csrf_missing' },
    )
  }
  // FormData holds File/Blob references (not a consumed stream), so retrying
  // with the same formData on a CSRF-recovery pass (withCsrfRetry) re-sends
  // the same bytes safely.
  return withCsrfRetry(() => doUploadFiles(formData))
}

async function doUploadFiles(formData: FormData): Promise<UploadFilesResponse> {
  // Read fresh — never cache (see readCSRFCookie).
  const csrf = readCSRFCookie()
  // Build headers by hand because FormData must NOT have a Content-Type
  // set — the browser needs to fill in the multipart boundary itself. No
  // Authorization header: auth is the omnipus-session cookie (US-5 / FR-010).
  const headers: Record<string, string> = {}
  if (csrf) headers[CSRF_HEADER_NAME] = csrf
  let res: Response
  try {
    res = await fetch(`${BASE_URL}/api/v1/upload`, {
      method: 'POST',
      credentials: 'include',
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

// ── Live Browser (ADR-039) ──────────────────────────────────────────────────

/**
 * Best-effort DOM-element resolution at a point in an agent's live browser
 * tab (ADR-039 D-B3). Used by the annotate-and-discuss flow in
 * BrowserLiveView to enrich a cropped-image annotation with the underlying
 * element's tag/text when possible.
 *
 * The endpoint is best-effort SERVER-side (`ok:false` + `reason` on a
 * cross-origin frame / detached node / timeout) — that is a normal 200
 * response, not a request failure. Callers must not treat a transport-level
 * failure (network error, 4xx/5xx) any differently: either way, the caller's
 * job is to fall back to the image+comment alone, never to block on this.
 */
export function inspectBrowserElement(req: BrowserInspectRequest): Promise<BrowserInspectResponse> {
  return request<BrowserInspectResponse>(
    '/browser/inspect',
    { method: 'POST', body: JSON.stringify(req) },
    BrowserInspectResponseSchema as ZodType<BrowserInspectResponse>,
  )
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
// JSON-only, so this uses fetch directly with the CSRF header (auth is the
// omnipus-session cookie, sent via credentials:'include' below — US-5 / FR-010).
// MIME-type-fragment → file-extension lookup for transcribeAudio, in priority
// order. 'webm' is both the first check and the fallback, so no matching
// fragment falls through to the same value a match on 'webm' would produce.
const AUDIO_EXT_BY_MIME_FRAGMENT: readonly (readonly [fragment: string, ext: string])[] = [
  ['webm', 'webm'],
  ['ogg', 'ogg'],
  ['wav', 'wav'],
  ['mp4', 'm4a'],
  ['mpeg', 'm4a'],
]

function audioFileExtension(mimeType: string): string {
  return AUDIO_EXT_BY_MIME_FRAGMENT.find(([fragment]) => mimeType.includes(fragment))?.[1] ?? 'webm'
}

export async function transcribeAudio(audio: Blob): Promise<TranscribeResponse> {
  const form = new FormData()
  // Preserve the recorded mime type's extension hint where possible.
  const ext = audioFileExtension(audio.type)
  form.append('audio', audio, `recording.${ext}`)

  // Blob is not a consumed stream, so retrying with the same form on a
  // CSRF-recovery pass (withCsrfRetry) re-sends the same bytes safely.
  return withCsrfRetry(() => doTranscribeAudio(form))
}

async function doTranscribeAudio(form: FormData): Promise<TranscribeResponse> {
  // Read fresh — never cache (see readCSRFCookie).
  const csrf = readCSRFCookie()
  let res: Response
  try {
    res = await fetch(`${BASE_URL}/api/v1/voice/transcribe`, {
      method: 'POST',
      credentials: 'include',
      // NOTE: do NOT set Content-Type — the browser sets the multipart boundary.
      headers: {
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
// with 202 Accepted (status:"restarting") immediately, before re-execing. It is
// secured by RequireNotBypass (returns 503 when dev_mode_bypass is active).
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
// GodModeStatus / GodModeUpdateRequest / GodModeUpdateResponse are the
// generated contract types (#8); see contracts/components/schemas/GodMode*.yaml.
// fetchGodMode reads the live runtime state ({ enabled, available, supported });
// setGodMode flips it and returns { enabled, restart_required } — a distinct
// shape because enabling from an unauthorized boot persists config but does
// NOT take live effect until the gateway restarts (see restart_required).
//
// `supported` (build support, nogodmode tag) and `available` (this boot was
// authorized, either via --allow-god-mode or a prior UI enable + restart) are
// DIFFERENT gates. Enabling is permitted whenever `supported` is true, even
// if `available` is currently false — that is exactly the UI-driven
// enablement flow: flip switch -> persist authorization -> restart to
// activate. Enabling when `supported` is false always returns 403.
//
// Step-up auth: the POST is re-auth-gated. Callers obtain a single-use consent
// token via reAuth() and pass it here; it is replayed in the X-Reauth-Token
// header (a missing/invalid token yields a 403).

export function fetchGodMode(): Promise<GodModeStatus> {
  return request<GodModeStatus>('/gateway/god-mode', undefined, GodModeStatusSchema)
}

export function setGodMode(enabled: boolean, reAuthToken?: string): Promise<GodModeUpdateResponse> {
  const body: GodModeUpdateRequest = { enabled }
  return request<GodModeUpdateResponse>('/gateway/god-mode', {
    method: 'POST',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
    body: JSON.stringify(body),
  }, GodModeUpdateResponseSchema)
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

// Sandbox config — mode, allowed paths, SSRF controls, and the global
// shell_deny_patterns default.
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
 *
 * action is the generated ToolApprovalActionRequest['action'] union — includes
 * "always" (approve this call AND record a session-scoped Always-Allow grant
 * via ApprovalGrantStore.Record; see pkg/gateway/rest_tool_registry.go).
 */
export function submitToolApproval(
  approvalId: string,
  action: ToolApprovalActionRequest['action'],
): Promise<void> {
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
  // ADR-051 Rev 4 — workspace media library (Slice H):
  media: (workspaceId: string) => ['workspaces', workspaceId, 'media'] as const,
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

// ── ADR-051 Rev 4 — Workspace Media Library (Slice H) ─────────────────────────
//
// The workspace media library is the blob store behind chat uploads
// (`workspaces/<ws>/media/`, UUID-keyed with a manifest). NOTE: the standalone
// workspace "Media" tab that used to surface it was REMOVED when the Library
// replaced it — the Library is a file explorer over the workspace `work/` tree
// (see docs/internal/specs/library-spec.md), a different store, so do not
// reintroduce a UI that lists this manifest as if it were the Library. These
// endpoints remain live because the composer picker still attaches an existing
// library entry to a chat message by
// its `media://workspace/<workspace_id>/<media_id>` ref (FR-022) without
// re-uploading. Wire types are the generated MediaLibraryEntry /
// MediaAttachmentRequest (contract-first #8) — never hand-written.

/**
 * List a workspace's media-library entries (GET /workspaces/{id}/media).
 * Returns the full manifest; raw bytes are fetched on demand via /media/{ref}.
 */
export function fetchWorkspaceMedia(workspaceId: string): Promise<MediaLibraryEntry[]> {
  return request<MediaLibraryEntry[]>(
    `/workspaces/${encodeURIComponent(workspaceId)}/media`,
    undefined,
    z.array(MediaLibraryEntrySchema) as ZodType<MediaLibraryEntry[]>,
  )
}

/**
 * Explicitly delete one workspace media-library entry (FR-008). Removes the
 * raw bytes + manifest entry; the server emits a media.delete audit event
 * (FR-033). Returns 204 No Content.
 */
export function deleteWorkspaceMedia(workspaceId: string, mediaId: string): Promise<void> {
  return request<void>(
    `/workspaces/${encodeURIComponent(workspaceId)}/media/${encodeURIComponent(mediaId)}`,
    { method: 'DELETE' },
  )
}

/**
 * Register a workspace library entry as a chat attachment (FR-022,
 * POST /workspaces/{id}/media/attachments) without re-uploading the file.
 * Returns 204 No Content; the SPA threads the `media://workspace/<ws>/<id>`
 * ref into the outgoing message frame via the library-attachment store.
 */
export function attachWorkspaceMedia(workspaceId: string, mediaId: string): Promise<void> {
  const body: MediaAttachmentRequest = { media_id: mediaId }
  return request<void>(
    `/workspaces/${encodeURIComponent(workspaceId)}/media/attachments`,
    { method: 'POST', body: JSON.stringify(body) },
  )
}

// ── Library (library-spec.md) ────────────────────────────────────────────────
//
// A file explorer over workspace work/ trees — supersedes the Media Library
// above (D-2: entries are workspace-relative PATHS, not UUIDs; a workspace's
// Library root IS its work/ directory). Two entry points share one component
// (D-3): the sidebar's virtual root (every workspace, GET /library/workspaces)
// and a workspace-scoped view (GET /library/{workspace_id}/entries). Wire
// types are the generated Library* schemas (contract-first #8) — never
// hand-written. See contracts/components/schemas/Library*.yaml.

export const libraryQueryKeys = {
  workspaces: () => ['library', 'workspaces'] as const,
  entries: (workspaceId: string, path: string, includeHidden: boolean) =>
    ['library', workspaceId, 'entries', path, includeHidden] as const,
  content: (workspaceId: string, path: string) => ['library', workspaceId, 'content', path] as const,
}

/** Every workspace as a Library virtual-root node (D-3 sidebar entry point). */
export function fetchLibraryWorkspaces(): Promise<LibraryWorkspaceNode[]> {
  return request<LibraryWorkspaceNode[]>(
    '/library/workspaces',
    undefined,
    z.array(LibraryWorkspaceNodeSchema) as ZodType<LibraryWorkspaceNode[]>,
  )
}

/**
 * List the entries directly inside `path` in a workspace's work/ tree. Omit
 * or pass '' to list the work-tree root. `includeHidden` surfaces
 * dot-prefixed entries (e.g. work/.library/ — D-8's "Show Hidden" toggle;
 * LibraryEntry.is_hidden is the sole definition of "hidden").
 */
export function fetchLibraryEntries(
  workspaceId: string,
  path = '',
  includeHidden = false,
): Promise<LibraryEntry[]> {
  const params = new URLSearchParams()
  if (path) params.set('path', path)
  if (includeHidden) params.set('include_hidden', 'true')
  const qs = params.toString()
  return request<LibraryEntry[]>(
    `/library/${encodeURIComponent(workspaceId)}/entries${qs ? `?${qs}` : ''}`,
    undefined,
    z.array(LibraryEntrySchema) as ZodType<LibraryEntry[]>,
  )
}

/**
 * Create a directory inside a workspace's work tree (mkdir -p semantics —
 * intermediate directories along `path` are created too). Idempotent:
 * resolves normally (200) if a directory already exists at `path`; the
 * caller cannot distinguish "created" from "already there" from the
 * response alone, which the New Folder UI doesn't need to (library-spec.md
 * — UAT fix: without this endpoint being reachable from the UI at all,
 * `POST /library/{workspace_id}/mkdir` working at the API layer was a
 * capability nobody could use).
 */
export function mkdirLibraryEntry(workspaceId: string, body: LibraryMkdirRequest): Promise<LibraryEntry> {
  return request<LibraryEntry>(
    `/library/${encodeURIComponent(workspaceId)}/mkdir`,
    { method: 'POST', body: JSON.stringify(body) },
    LibraryEntrySchema as ZodType<LibraryEntry>,
  )
}

/** Delete a file or directory (and everything under it) from a workspace's work tree. Returns 204. */
export function deleteLibraryEntry(workspaceId: string, path: string): Promise<void> {
  const qs = new URLSearchParams({ path }).toString()
  return request<void>(`/library/${encodeURIComponent(workspaceId)}/entries?${qs}`, { method: 'DELETE' })
}

/** Read a file's text content for the Library viewer (D-5 preview/edit — read side; the editor/preview pane itself is a separate, later task). */
export function fetchLibraryContent(workspaceId: string, path: string): Promise<LibraryContentResponse> {
  const qs = new URLSearchParams({ path }).toString()
  return request<LibraryContentResponse>(
    `/library/${encodeURIComponent(workspaceId)}/content?${qs}`,
    undefined,
    LibraryContentResponseSchema as ZodType<LibraryContentResponse>,
  )
}

/** Write a file's text content from the Library editor (D-5 — write side; the editor itself is a separate, later task). */
export function putLibraryContent(workspaceId: string, body: LibraryContentRequest): Promise<LibraryEntry> {
  return request<LibraryEntry>(
    `/library/${encodeURIComponent(workspaceId)}/content`,
    { method: 'PUT', body: JSON.stringify(body) },
    LibraryEntrySchema as ZodType<LibraryEntry>,
  )
}

/** Rename or move an entry within a single workspace's work tree — same-workspace sugar over /library/move. Rejects (409) if "to" already exists. */
export function renameLibraryEntry(workspaceId: string, body: LibraryRenameRequest): Promise<LibraryEntry> {
  return request<LibraryEntry>(
    `/library/${encodeURIComponent(workspaceId)}/rename`,
    { method: 'POST', body: JSON.stringify(body) },
    LibraryEntrySchema as ZodType<LibraryEntry>,
  )
}

/**
 * Move a file or directory, optionally across two workspaces (D-9). Rejects
 * (409) if the destination already exists — the server never silently
 * overwrites, so there is no "overwrite" outcome to confirm beyond the
 * dialog step itself; a 409 here is surfaced to the caller as a normal
 * ApiError (never swallowed).
 */
export function moveLibraryEntry(body: LibraryTransferRequest): Promise<LibraryEntry> {
  return request<LibraryEntry>(
    '/library/move',
    { method: 'POST', body: JSON.stringify(body) },
    LibraryEntrySchema as ZodType<LibraryEntry>,
  )
}

/** Copy a file or directory, optionally across two workspaces (D-9), leaving the source in place. Same 409-on-conflict behavior as moveLibraryEntry. */
export function copyLibraryEntry(body: LibraryTransferRequest): Promise<LibraryEntry> {
  return request<LibraryEntry>(
    '/library/copy',
    { method: 'POST', body: JSON.stringify(body) },
    LibraryEntrySchema as ZodType<LibraryEntry>,
  )
}

/**
 * Upload one or more files into `path` inside a workspace's work tree (D-1 —
 * uploads land as real, named files, de-duplicated server-side on collision).
 * Multipart; mirrors uploadFiles's raw-fetch pattern above since request() is
 * JSON-only.
 */
export async function uploadLibraryFiles(workspaceId: string, files: File[], path = ''): Promise<LibraryUploadResponse> {
  const formData = new FormData()
  for (const file of files) {
    formData.append('files', file)
  }
  // Upload is a state-changing POST — fail fast if we have no CSRF cookie
  // (see request() for the same pattern).
  if (readCSRFCookie() === null) {
    throw new ApiError(
      403,
      'CSRF cookie missing — cannot upload files. Log in first so the server can issue the CSRF cookie.',
      { code: 'csrf_missing' },
    )
  }
  // FormData holds File references (not a consumed stream), so retrying with
  // the same formData on a CSRF-recovery pass (withCsrfRetry) re-sends the
  // same bytes safely.
  return withCsrfRetry(() => doUploadLibraryFiles(workspaceId, formData, path))
}

async function doUploadLibraryFiles(workspaceId: string, formData: FormData, path: string): Promise<LibraryUploadResponse> {
  // Read fresh — never cache (see readCSRFCookie).
  const csrf = readCSRFCookie()
  const headers: Record<string, string> = {}
  if (csrf) headers[CSRF_HEADER_NAME] = csrf
  const qs = path ? `?${new URLSearchParams({ path }).toString()}` : ''
  let res: Response
  try {
    res = await fetch(`${BASE_URL}/api/v1/library/${encodeURIComponent(workspaceId)}/upload${qs}`, {
      method: 'POST',
      credentials: 'include',
      // NOTE: do NOT set Content-Type — the browser sets the multipart boundary.
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
  const parsed = (LibraryUploadResponseSchema as ZodType<LibraryUploadResponse>).safeParse(raw)
  if (!parsed.success) {
    _recordApiSchemaError('/library/upload', parsed.error.issues.length)
    const issues = parsed.error.issues.map((i) => ({ path: i.path as (string | number)[], message: i.message }))
    void maybeDevToast(`[api] uploadLibraryFiles response schema mismatch: ${issues[0]?.message ?? 'unknown'}`, 'POST:/library/upload:schema')
    throw new ApiSchemaError('/library/upload', issues, raw)
  }
  return parsed.data
}

/**
 * URL for downloading a file's raw bytes (GET .../download). Deliberately
 * NOT routed through request() — the response is a binary stream, not JSON,
 * and GET is not state-changing so no CSRF token is required. Meant for an
 * <a href=… download> or window.open(): auth rides the same-origin
 * omnipus-session cookie automatically on a plain navigation/anchor click.
 */
export function libraryDownloadUrl(workspaceId: string, path: string): string {
  const qs = new URLSearchParams({ path }).toString()
  return `${BASE_URL}/api/v1/library/${encodeURIComponent(workspaceId)}/download?${qs}`
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
