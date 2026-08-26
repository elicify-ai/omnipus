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

import { ApiError, isApiError as isApiErrorFn, getErrorMessage } from './api-error'
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
  ToolApprovalResponse as ToolApprovalResponseSchema,
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
  // ADR-057 FR-091/FR-098 (U10, W16e): GET /sessions now returns one named
  // SessionPage envelope ({sessions, next_cursor?, partial_errors?}) instead
  // of the retired two-variant oneOf (bare array | {sessions, partial_errors}).
  SessionPage as WireSessionPageSchema,
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
  // Planning & Goals (ADR-049, contract-first #8):
  Plan as PlanSchema,
  // ADR-052 Wave 2 — plan execute/restart 400 error body (contract-first #8):
  PlanApproveError as PlanApproveErrorSchema,
  EvidenceRecord as EvidenceRecordSchema,
  JudgeVerdict as JudgeVerdictSchema,
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
  // ADR-053 D12/R§8.3 (FE-6) — app-level OVERALL token budget status:
  TokenBudgetStatus as TokenBudgetStatusSchema,
  // ADR-066 D9 — global context-budget settings (Settings → Models):
  ContextSettings as ContextSettingsSchema,
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
  HostFolderListing as HostFolderListingSchema,
  WorkspaceMountCreateResponse as WorkspaceMountCreateResponseSchema,
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
  ProbeProviderRequest,
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
  ProvidersCatalog,
  CatalogProvider,
  ProviderUpdateRequest,
  // ADR-068 FR-010/FR-012/FR-018 — provider removal + the default-model pair:
  ProviderDeleteRequest,
  ProviderDeleteResponse,
  ProviderDependent,
  DefaultModel,
  DefaultModelUpdateRequest,
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
  // ADR-068 section 8b sign-in wire shapes (T068-33/T068-34):
  SignInStartResponse,
  SignInStatus,
  SignInPollRequest,
  SignInPollResponse,
  // ADR-068 FR-031/T068-27: "Check with my account".
  EntitlementResponse,
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
  // ADR-053 D12/R§8.3 (FE-6) — app-level OVERALL token budget status:
  TokenBudgetStatus,
  // ADR-066 D9 — global context-budget settings (Settings → Models):
  ContextSettings,
  ContextSettingsUpdate,
  ContextModelOverride,
  ContextWindowSource,
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
  // Planning & Goals (ADR-049, contract-first #8) — Plan container, task
  // acceptance criteria, evidence, and judge verdicts (replaces Milestones):
  Plan,
  PlanCreateRequest,
  PlanUpdateRequest,
  PlanListResponse,
  // ADR-052 Wave 2 — plan execute/restart 400 error body (contract-first #8):
  PlanApproveError,
  AcceptanceCriterion,
  EvidenceRecord,
  JudgeVerdict,
  CriterionVerdict,
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
  ToolApprovalResponse,
  // ADR-039 — user-initiated browsing + annotate-a-region-and-discuss:
  BrowserInspectRequest,
  BrowserInspectResponse,
  // ADR-051 Rev 4 — workspace media library (contract-first #8):
  MediaLibraryEntry,
  MediaAttachmentRequest,
  // library-spec.md — Library file explorer over workspace work/ trees (contract-first #8):
  LibraryWorkspaceNode,
  LibraryEntry,
  HostFolderListing,
  HostFolderEntry,
  LibraryEntryMount,
  WorkspaceMountCreateRequest,
  WorkspaceMountCreateResponse,
  LibraryContentResponse,
  LibraryContentRequest,
  LibraryMkdirRequest,
  LibraryRenameRequest,
  LibraryUploadResponse,
  LibraryTransferRequest,
} from '@/lib/api/generated/openapi-types'

export type {
  LoginResponse,
  ProbeProviderRequest,
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
  ProvidersCatalog,
  CatalogProvider,
  ProviderDeleteRequest,
  ProviderDeleteResponse,
  ProviderDependent,
  DefaultModel,
  DefaultModelUpdateRequest,
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
  // ADR-053 D12/R§8.3 (FE-6) — app-level OVERALL token budget status:
  TokenBudgetStatus,
  // ADR-066 D9 — global context-budget settings (Settings → Models):
  ContextSettings,
  ContextSettingsUpdate,
  ContextModelOverride,
  ContextWindowSource,
  // Unified task types (Sprint 2) — Task already exported above, add new ones:
  TaskCreateRequest,
  TaskUpdateRequest,
  Todo,
  TaskTrigger,
  // Planning & Goals (ADR-049) — Plan container, task acceptance criteria,
  // evidence, and judge verdicts (replaces Milestones — the Milestone schema
  // family was deleted from contracts/ on this branch; do not reintroduce):
  Plan,
  PlanCreateRequest,
  PlanUpdateRequest,
  PlanListResponse,
  // ADR-052 Wave 2 — plan execute/restart 400 error body:
  PlanApproveError,
  AcceptanceCriterion,
  EvidenceRecord,
  JudgeVerdict,
  CriterionVerdict,
  // Per-task run history (ADR-050 / task-run-history-spec §4.1) — additive,
  // unrelated to the Plan/Milestone replacement above:
  TaskRun,
  RunNowRequest,
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
  HostFolderListing,
  HostFolderEntry,
  LibraryEntryMount,
  WorkspaceMountCreateRequest,
  WorkspaceMountCreateResponse,
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
  // 'verifier' (ADR-052 FR-036) tags a verifier-role adjudication session
  // (the Judge). Hidden from GET /sessions by default; fetchSessions()'s
  // includeVerifier opt-in surfaces it (UsageScreen's "By session" tab only).
  // 'delegate' (ADR-057 FR-008/W2c) tags a subordinate session minted by a
  // delegation — it always carries a non-empty parent_session_id below.
  // Like 'scheduled'/'heartbeat'/'verifier' it is server-minted only.
  type: 'chat' | 'task' | 'channel' | 'scheduled' | 'heartbeat' | 'verifier' | 'delegate'
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
  // ADR-057 FR-008/FR-091. The direct parent's session id — present only on
  // a subordinate ('delegate') session. Absent (never empty-string) on a
  // root session. A session whose parent no longer resolves is surfaced by
  // GET /sessions as a ROOT rather than being silently dropped (BDD-106) —
  // so a session with parent_session_id set but not findable in a locally
  // held tree should be treated as "not yet expanded into", never as an
  // error.
  parent_session_id?: string
  // ADR-057 FR-091/FR-097/FR-104. Count of this session's DIRECT children,
  // resolved server-side from the in-memory parent index in O(1) per row.
  // Populated on GET /sessions (default roots-only listing, flat=true
  // listing, and parent_session_id-filtered listing); zero for a session
  // with no children. Not necessarily present on GET /sessions/{id} detail.
  child_count?: number
}

interface _RawSessionInternal { // not-wire-format: SPA-internal adapter that renames nested stats fields before public Session type; the wire shape is validated via WireSessionSchema, this type only models the pre-transform intermediate
  id: string
  agent_id: string
  title: string
  type?: 'chat' | 'task' | 'channel' | 'scheduled' | 'heartbeat' | 'verifier' | 'delegate'
  status?: 'active' | 'archived' | 'interrupted'
  task_id?: string
  workspace_id?: string
  created_at: string
  updated_at: string
  channel?: string
  agent_ids?: string[]
  active_agent_id?: string
  protected?: boolean
  parent_session_id?: string
  child_count?: number
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
    // ADR-057 FR-008/FR-091/FR-097: passed through verbatim — a root session
    // simply never has parent_session_id set (never empty-string per the
    // contract, so no coercion needed), and child_count defaults via the
    // consuming code's own `?? 0`, not here, so "absent" (detail endpoint)
    // and "explicitly zero" (list endpoint) stay distinguishable.
    parent_session_id: raw.parent_session_id,
    child_count: raw.child_count,
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
  /**
   * ADR-049 D2/SD-A14/SD-C10: classification carried through from the wire
   * `Message.type` (generated `openapi-types.ts`) for the one variant SPA
   * rendering cares about — `'judge_verdict'` — so the chat store/renderers
   * can recognise a persisted judge-verdict transcript entry (cold-load or
   * WS replay) and route it through `shouldRenderJudgeVerdictInThread`
   * (toolVisibility.ts) instead of the normal role-based rows. Other wire
   * `type` values ('message'/'compaction'/'system'/'tool_call'/
   * 'turn_canceled') are not modeled here — this SPA-internal `Message`
   * union already has its own, richer per-role shape for those.
   */
  type?: 'judge_verdict'
  /** The verdict payload when `type === 'judge_verdict'` (wire `Message.verdict`, same shape as the live `JudgeVerdictFrame` push minus the `type`/`session_id` discriminator fields). */
  verdict?: JudgeVerdict
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

// The 3rd-arg opts object is opt-in and additive only — every existing
// zero/one/two-arg call site (Sidebar, SearchModal) is byte-for-byte
// unaffected and keeps excluding verifier sessions (and getting the default
// roots-only page) by construction, since `opts` is simply undefined.
//
// ADR-057 US-19/FR-091/FR-092/FR-104 (W16d/W16h): grew four more fields —
//   - parentSessionId: GET /sessions?parent_session_id=<id> — that node's
//     DIRECT children only, a page at a time, instead of roots. Mutually
//     exclusive with `flat` (server 400s if both are supplied).
//   - flat: GET /sessions?flat=true — every session (roots AND
//     subordinates) as one flat paged list, child_count still populated.
//     UsageScreen's "By session" tab passes this so delegated children's
//     spend stays auditable (FR-104) instead of silently disappearing
//     under the default roots-only listing.
//   - limit / offset: ADR-057 FR-092/FR-098 paging. Omitted uses the
//     server's default page size / first page.
// includeVerifier maps 1:1 to the generated `include_verifier` query param
// (contracts/openapi.yaml `listSessions`, ADR-052 FR-036): omitted/false
// excludes type:"verifier" sessions from the response; true opts them in.
// UsageScreen's "By session" tab is the one caller that passes true, so
// verifier LLM spend is auditable per-session there (SC-014), not just in
// the unfiltered token-stats aggregate.
export interface FetchSessionsOptions { // not-wire-format: client-side call-options bag for fetchSessionPage/fetchSessions; each field becomes an individual query-string param on the request, never a serialized JSON object sent or received over the wire
  includeVerifier?: boolean
  /** ADR-057 FR-091/US-19: direct children of this session id, paged. */
  parentSessionId?: string
  /** ADR-057 FR-104: every session (roots + subordinates), flat, paged. */
  flat?: boolean
  /** ADR-057 FR-092: page size. Server default applies when omitted. */
  limit?: number
  /** ADR-057 FR-098: offset into the recency-ordered sequence, or a prior next_cursor's value. */
  offset?: number
}

// ADR-057 FR-091/FR-098 (W16d): the paged envelope GET /sessions now always
// returns — SPA-shaped (Session[], not RawSession[]) so callers never see
// the wire's nested `stats`. This is the seam U24 consumes to drive the
// sidebar/search session tree's "load next page" and "expand this node"
// requests (cross-unit request, spec line ~1033) — fetchSessions() below
// remains the simple array-returning convenience wrapper for callers that
// don't need cursoring (Sidebar, SearchModal, UsageScreen all use that).
export interface SessionListPage { // not-wire-format: SPA-internal paged envelope produced by fetchSessionPage() from the wire SessionPage; sessions is post-mapped Session[] (not RawSession[]) and fields are camelCase (nextCursor/partialErrors) vs the wire's next_cursor/partial_errors
  sessions: Session[]
  /** Present unless this is the last page. Opaque — pass back as `offset`. */
  nextCursor?: string
  /** Sanitized per-store failure tokens from a partial legacy-store merge (FR-098). */
  partialErrors?: string[]
}

export async function fetchSessionPage(
  agentId?: string,
  type?: Session['type'],
  opts?: FetchSessionsOptions,
): Promise<SessionListPage> {
  const params: Record<string, string> = {}
  if (agentId) params.agent_id = agentId
  if (type) params.type = type
  if (opts?.includeVerifier) params.include_verifier = 'true'
  if (opts?.parentSessionId) params.parent_session_id = opts.parentSessionId
  if (opts?.flat) params.flat = 'true'
  if (opts?.limit !== undefined) params.limit = String(opts.limit)
  if (opts?.offset !== undefined) params.offset = String(opts.offset)
  const qs = Object.keys(params).length > 0 ? '?' + new URLSearchParams(params).toString() : ''
  // ADR-057 FR-091/grill2 M2-10: the historic two-variant oneOf (a bare
  // Session array, or {sessions, partial_errors}) is retired — the wire now
  // always returns one named SessionPage envelope
  // ({sessions, next_cursor?, partial_errors?}), validated against U10's
  // generated schema rather than a hand-rolled union.
  const resp = await request<{ sessions: RawSession[]; next_cursor?: string; partial_errors?: string[] }>(
    `/sessions${qs}`,
    undefined,
    WireSessionPageSchema as ZodType<{ sessions: RawSession[]; next_cursor?: string; partial_errors?: string[] }>,
  )
  // A non-empty `partial_errors` means one or more agents failed to list
  // their sessions — `resp.sessions` is a real but INCOMPLETE enumeration,
  // not a full one. Silently returning it made a partial listing read as
  // complete everywhere fetchSessions is consulted (worst on UsageScreen's
  // spend-audit tab, which sums sessions to report cost/usage). Surface it
  // the same way a schema mismatch does (console.warn + dev toast) rather
  // than dropping it on the floor.
  if (resp.partial_errors && resp.partial_errors.length > 0) {
    console.warn('[api] GET /sessions returned partial_errors — the list is incomplete:', resp.partial_errors)
    void maybeDevToast(
      `[api] Session list incomplete: ${resp.partial_errors.length} agent(s) failed to enumerate`,
      'GET:/sessions:partial_errors',
    )
  }
  return {
    sessions: resp.sessions.map(rawToSession),
    nextCursor: resp.next_cursor,
    partialErrors: resp.partial_errors,
  }
}

// ADR-057 FR-091/FR-098 (post-review fix): GET /sessions is paginated
// server-side (default limit 50, pkg/gateway/rest.go's
// u18DefaultSessionPageLimit) — a single fetchSessionPage() call is no longer
// "every session". fetchSessions() is the "give me the COMPLETE set"
// convenience wrapper its three production callers (Sidebar's workspace
// accordion, SearchModal's cross-workspace search, UsageScreen's spend
// audit) have always relied on since before pagination existed — none of
// them implement their own paging loop, and two of them (SearchModal's find
// results, UsageScreen's cost totals) actively regress into silent data loss
// if handed only page 1. So this wrapper exhausts every page via
// `next_cursor` (a numeric offset, re-sent as the next `offset`) before
// returning, rather than reproducing the truncation one layer up.
// fetchSessionPage() remains the single-page primitive for callers that DO
// want to control paging themselves — SessionTree.tsx's useSessionForest
// fetches one node's children a page at a time by design (BDD-103).
export async function fetchSessions(
  agentId?: string,
  type?: Session['type'],
  opts?: FetchSessionsOptions,
): Promise<Session[]> {
  const sessions: Session[] = []
  let offset = opts?.offset
  // Safety valve, not a normal exit: the server's own default page size is
  // 50, so 1000 pages is 50,000 sessions — far past any real install. If a
  // buggy/malicious server never stops returning next_cursor, this stops the
  // tab from fetch-looping forever instead of quietly capping the result
  // (callers must not mistake "we gave up" for "here is the complete set").
  const MAX_PAGES = 1000
  for (let i = 0; i < MAX_PAGES; i++) {
    const page = await fetchSessionPage(agentId, type, { ...opts, offset })
    sessions.push(...page.sessions)
    if (!page.nextCursor) return sessions
    offset = Number(page.nextCursor)
  }
  console.warn(`[api] fetchSessions: aborted after ${MAX_PAGES} pages — server kept returning next_cursor; result is INCOMPLETE`)
  void maybeDevToast(
    `[api] Session list exceeded ${MAX_PAGES} pages — showing a partial set`,
    'GET:/sessions:max-pages',
  )
  return sessions
}

// ── Session tree assembly (ADR-057 US-19/FR-091/FR-097, W16d) ─────────────────
//
// GET /sessions never returns a whole forest — it pages over roots (or one
// node's direct children via parentSessionId, or a flat list under
// flat=true). The client assembles the tree incrementally as the user
// expands nodes; these are the pure, exported primitives U24 (sidebar tree,
// search tree — cross-unit request, spec line ~1033) builds that UI on top
// of. All three are immutable (return a new tree) so they drop directly
// into React/Zustand state without extra cloning at the call site.

export interface SessionTreeNode { // not-wire-format: SPA-internal tree node assembled client-side from paged GET /sessions responses; childrenLoaded is a UI-only fetch-state flag with no wire counterpart, never sent to or received from the gateway
  session: Session
  children: SessionTreeNode[]
  /**
   * True once this node's children have actually been fetched (vs merely
   * known about via child_count). A leaf (child_count === 0) starts
   * "loaded" with an empty array — there is nothing to fetch.
   */
  childrenLoaded: boolean
}

/** Wraps a flat page of sessions (roots, or any page) as top-level tree nodes with no children fetched yet. */
export function buildSessionTree(sessions: Session[]): SessionTreeNode[] {
  return sessions.map((session) => ({
    session,
    children: [],
    childrenLoaded: (session.child_count ?? 0) === 0,
  }))
}

/**
 * Finds the node for `sessionId` anywhere in the tree (any depth), or
 * undefined if it is not present — e.g. a session known only by id from a
 * search hit before its ancestor chain has been fetched.
 */
export function findSessionNode(tree: SessionTreeNode[], sessionId: string): SessionTreeNode | undefined {
  for (const node of tree) {
    if (node.session.id === sessionId) return node
    const found = findSessionNode(node.children, sessionId)
    if (found) return found
  }
  return undefined
}

/**
 * Returns a NEW tree with `children` attached under the node whose session
 * id is `parentId`, at whatever depth it is found (US-19 AS-3: a depth-3
 * tree expands one level at a time, each expansion touching only that
 * node). If `parentId` is not present anywhere in the tree, the tree is
 * returned unchanged — callers should have inserted that node (e.g. via
 * buildSessionTree for a freshly-expanded root, or insertOrphanSessionAsRoot
 * for BDD-106's orphan case) before attaching its children.
 */
export function attachSessionChildren(
  tree: SessionTreeNode[],
  parentId: string,
  children: Session[],
): SessionTreeNode[] {
  return tree.map((node) => {
    if (node.session.id === parentId) {
      return { ...node, children: buildSessionTree(children), childrenLoaded: true }
    }
    if (node.children.length > 0) {
      const updatedChildren = attachSessionChildren(node.children, parentId, children)
      if (updatedChildren !== node.children) {
        return { ...node, children: updatedChildren }
      }
    }
    return node
  })
}

/**
 * BDD-106: a session whose parent_session_id names a session that no longer
 * resolves is shown as a root-level row rather than silently dropped — "a
 * session that exists and is not reachable in the tree is the R-7 shape
 * again". The default roots-only listing already satisfies this for the
 * common case server-side (FR-091 returns such a session AS a root, so it
 * simply appears in the normal root page). This helper covers the narrower
 * client-side case: a session encountered as somebody's declared child (or
 * a search hit) whose own id is not yet present anywhere in the local tree
 * — it is appended as a new root rather than discarded. A no-op if the
 * session is already present anywhere in the tree.
 */
export function insertOrphanSessionAsRoot(tree: SessionTreeNode[], orphan: Session): SessionTreeNode[] {
  if (findSessionNode(tree, orphan.id)) return tree
  return [...tree, ...buildSessionTree([orphan])]
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
    // ADR-053 D12 retired the SEC-26 USD cap; the app-level spend brake is
    // now the token budget (set via /api/v1/settings/token-budget). The
    // daily_cost_cap field is gone from both the wire types and this Config.
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
      // ADR-068 D14.1: the default model is the exact (provider, model) pair
      // persisted at agents.defaults.default_model (agents.defaults.model_name
      // no longer exists). Threaded through rawToFrontendConfig so it survives
      // a settings round-trip.
      default_model?: { provider?: string; model?: string }
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
      // ADR-053 D12: daily_cost_cap is gone — token budget is the sole brake.
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
    if (data.security.policy_mode !== undefined) sec.policy_mode = data.security.policy_mode
    if (data.security.exec_approval !== undefined) sec.exec_approval = data.security.exec_approval
    // prompt_injection_level intentionally omitted — owned by PUT /security/prompt-guard.
    // daily_cost_cap intentionally omitted — ADR-053 D12 retired the SEC-26
    // USD cap; the app-level spend brake is the token budget, set via
    // PUT /api/v1/settings/token-budget.
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
// is skipped (see pkg/gateway/rest.go provider PUT handler).
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
//   POST   /tasks/{id}/stop     → Task     (Stop/Clear — `stopTask`, ADR-052)
//   POST   /tasks/{id}/restart  → Task | 409 (▶ Play a Stopped task — `restartTask`, ADR-052 FR-026)
//
// "Start"/"Run" semantics: there is no /start or /run endpoint for a
// standalone task (`run_task` on the wire is an AGENT tool, not a REST
// route — ADR-052 G4). Set status=in_progress via PATCH to start/run one
// (drag, the board's Run button, or `runTask` below).

export const tasksQueryKeys = {
  list: (params?: { workspace_id?: string; status?: string; agent_id?: string; plan_id?: string; surface?: string }) => {
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

export function fetchTasks(params?: { workspace_id?: string; status?: string; agent_id?: string; plan_id?: string; surface?: string }): Promise<Task[]> {
  const search = new URLSearchParams()
  if (params?.workspace_id) search.set('workspace_id', params.workspace_id)
  if (params?.status) search.set('status', params.status)
  if (params?.agent_id) search.set('agent_id', params.agent_id)
  if (params?.plan_id) search.set('plan_id', params.plan_id)
  if (params?.surface) search.set('surface', params.surface)
  const qs = search.toString() ? '?' + search.toString() : ''
  return request<Task[]>(`/tasks${qs}`, undefined, z.array(TaskSchema) as ZodType<Task[]>)
}

// Keep fetchBoardTasks as an alias so existing call-sites compile during transition.
export function fetchBoardTasks(params?: { workspace_id?: string; status?: string; agent_id?: string; plan_id?: string }): Promise<Task[]> {
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

/**
 * Stop/Clear (D8) a task's own goal loop — POST /tasks/{id}/stop (ADR-049 —
 * "clear affordances at every level", distinct from `stopPlan`, which stops
 * a Plan's loop; ADR-052 FR-010/FR-022/US-7 — `RequestCancelForSession`
 * reaches the worker turn + its subagents + its shells, the same chat
 * cancel cascade `handleTaskStop` already uses).
 */
export function stopTask(id: string): Promise<Task> {
  return request<Task>(`/tasks/${encodeURIComponent(id)}/stop`, { method: 'POST' }, TaskSchema as ZodType<Task>)
}

/** @deprecated Use `stopTask` — kept as an alias for callers not yet swept (e.g. `TaskDetailPanel.tsx`). Same signature/behavior. */
export const stopTaskGoalLoop = stopTask

/**
 * Restart (▶ Play) a standalone task previously Stopped by the user — POST
 * /tasks/{id}/restart (ADR-052 FR-026/US-9/US-10). Resets `attempt_count` to
 * 0, clears `cancel_reason`, and transitions the task to `next` so the goal
 * loop picks it up again and drives the FULL attempt loop (run -> judge ->
 * retry to the limit — same as `run_task` / a plan member, A3).
 *
 * A `409` means "not restartable": the task belongs to a plan (an in-plan
 * member restarts only via its plan — restart the plan instead, `restartPlan`),
 * or the task isn't `failed`, or its `cancel_reason` isn't `stopped_by_user`.
 * Rewritten into a specific, actionable message here rather than the generic
 * "conflicts with current state" default.
 */
export async function restartTask(id: string): Promise<Task> {
  try {
    return await request<Task>(`/tasks/${encodeURIComponent(id)}/restart`, { method: 'POST' }, TaskSchema as ZodType<Task>)
  } catch (err) {
    throw friendlyConflictError(
      err,
      "This task is not restartable — it belongs to a plan (restart the plan instead), or wasn't Stopped by a user.",
    )
  }
}

/**
 * Run a standalone task now (▶ Play on an idle task — ADR-052 FR-019/G4).
 *
 * There is NO dedicated REST route for this: `run_task` on the wire is an
 * AGENT tool only (verified against the generated contract — no
 * `/tasks/{id}/run` operation exists in `src/lib/api/generated/openapi-types.ts`).
 * The UI's "run now" path is the SAME one the board's drag-to-`in_progress`
 * move and `CreateTaskSlideOver`'s "Create & Run" already use ("Start"
 * semantics, see the Tasks section header above): PATCH the task to
 * `status: 'in_progress'`. The engine's goal loop then drives the task
 * through the FULL attempt loop (run -> judge -> retry to the limit),
 * identical to `run_task` / a plan member (A3) — this is not a lesser,
 * single-shot run.
 *
 * The engine — not this call — rejects an in-plan member (G4: in-plan tasks
 * start only via their plan); a `409` from that case is rewritten into a
 * friendlier "not runnable" message rather than the generic conflict default.
 */
export async function runTask(id: string): Promise<Task> {
  try {
    return await updateTask(id, { status: 'in_progress' })
  } catch (err) {
    throw friendlyConflictError(
      err,
      "This task can't be run right now — it may already be running, or be an in-plan member (its plan drives its start, not a standalone run).",
    )
  }
}

// ── Board drag-to-column move: 409 message mapping (UAT round-2 N1/N2) ─────
//
// The board's drag-to-column move (BoardView.tsx -> WorkspaceTasksTab.tsx's
// moveMutation) PATCHes `status` straight through `updateTask` above — it
// does NOT go through `runTask`'s `friendlyConflictError` wrapper, so a plan
// member dragged into `in_progress` hit the generic
// `ApiError.fromResponse` 409 default ("This conflicts with the current
// state. Please refresh and try again.") verbatim. That default is actively
// WRONG for this endpoint's most common 409 cause (see below) — refreshing
// can never fix it — so this is a dedicated mapper, not a reuse of
// `friendlyConflictError` (whose single hardcoded string-per-endpoint
// shape can't express "different message per plan state").
//
// pkg/gateway/rest_tasks.go's handleTaskPatch returns 409 from exactly two
// call sites (verified by reading the handler, not assumed) when the PATCH
// sets `status: in_progress` and StartTaskNow then fails:
//   1. `agent.ErrDispatchCapReached` — the global dispatch semaphore is
//      full. Genuinely transient congestion; retrying shortly can help.
//   2. `agent.ErrPlanNotExecuting` / `agent.ErrPlanStateUnresolvable` — the
//      S1 plan-state gate (pkg/agent/task_executor.go's
//      `requirePlanExecuting`, commit 5d77f26a) refusing dispatch because
//      the task's parent plan isn't `approved`/`running`-and-unpaused.
//      Refreshing NEVER helps here — the plan itself has to change state
//      (Execute a draft, restart an eligible stopped plan, or re-enable a
//      disabled owner agent to clear a pause).
// `pkg/task/store.go`'s plain `Update` (what this PATCH ultimately calls)
// has no optimistic-concurrency/version check at all — illegal lifecycle
// transitions there resolve to `task.ErrValidation` (400 Bad Request via
// `isTaskValidationErr`), never 409 — so there is currently no THIRD,
// generic-concurrency 409 cause on this endpoint to confuse with the above
// two. (`src/lib/queryClient.ts`'s retry-exclusion comment already
// documents this same overload for the RETRY question — that finding
// stands; nothing here changes retry behavior, only display text, and both
// causes are read from the same plain-text `{"error": string}` body that
// comment says has "no machine-readable field distinguishing the two
// cases" — true for a structured/typed field, but the two sentinel error
// strings ARE textually distinguishable, which is all a display mapper
// needs.)
//
// Exported (not `friendlyConflictError`-private) because BOTH the toast
// (WorkspaceTasksTab's moveMutation.onError) and the screen-reader live
// region (BoardView's own post-drop announcement) need the IDENTICAL text —
// a screen-reader user must never hear a different reason than the sighted
// toast shows for the same rejected drop.
export function describeTaskMoveConflict(err: unknown, plans: Plan[]): string | undefined {
  if (!isApiErrorFn(err) || err.status !== 409 || !err.body) return undefined

  let raw = err.body
  try {
    const parsed = JSON.parse(err.body) as { error?: unknown; message?: unknown }
    if (typeof parsed.error === 'string') raw = parsed.error
    else if (typeof parsed.message === 'string') raw = parsed.message
  } catch {
    // Not JSON (H3-FE already guards the oversized/binary cases upstream in
    // ApiError.fromResponse) — fall back to the raw text as-is.
  }

  if (raw.includes('global dispatch cap reached')) {
    return 'Too many tasks are starting at once — the server is at its dispatch limit. Try moving this task again in a moment.'
  }

  // Mirrors task_executor.go's ErrPlanNotExecuting wrap exactly:
  //   "...parent plan is not in a dispatchable state (approved/running, unpaused): plan "<id>" is <state> (paused_reason="<reason>")"
  const gateMatch = raw.match(
    /parent plan is not in a dispatchable state.*?: plan "([^"]*)" is (\w+) \(paused_reason="([^"]*)"\)/,
  )
  if (gateMatch) {
    const [, planId, state, pausedReason] = gateMatch
    // PermitsMemberDispatch (pkg/plan/plan.go) checks PausedReason FIRST,
    // before State — a paused plan is refused regardless of state, so this
    // takes precedence here too.
    if (pausedReason) {
      if (pausedReason === 'owner_disabled') {
        return "This plan is paused because its owner agent is disabled — re-enable the agent to resume this plan's tasks."
      }
      return `This plan is paused (${pausedReason}) — resolve that before this task can run.`
    }
    switch (state) {
      case 'draft':
        return 'This plan is still a draft — Execute it (from the Plans band above) before this task can run.'
      case 'done':
        return "This plan has already finished — its tasks can't be started this way."
      case 'failed': {
        // Cross-reference the already-loaded plans list (BoardView already
        // receives it) for `failed_reason` — the gate's own message doesn't
        // carry it, and it's the difference between "Restart the plan" (a
        // real, offered action for `stopped_by_user`) and "not restartable"
        // (every other failure reason — PlanActionButton offers no restart
        // for those either, US-9 Acceptance 2).
        const plan = plans.find((p) => p.id === planId)
        return plan?.failed_reason === 'stopped_by_user'
          ? 'This plan was stopped — Restart it (from the Plans band above) before this task can run.'
          : "This plan has failed and can't be restarted — its tasks can no longer run."
      }
      default:
        // A future 6th plan state the client doesn't know about yet — fall
        // through to the generic-but-honest 409 fallback below rather than
        // guessing at a state-specific message we can't stand behind.
        return undefined
    }
  }

  // Mirrors ErrPlanStateUnresolvable's wrap: "...parent plan's state could not be verified: plan "<id>": <err>"
  if (raw.includes("parent plan's state could not be verified")) {
    return "This plan's current state couldn't be verified — try moving this task again in a moment."
  }

  return undefined
}

/**
 * The single message both the move-conflict toast (WorkspaceTasksTab) and
 * the drag-and-drop live-region announcement (BoardView) render for a failed
 * board move — `describeTaskMoveConflict`'s specific mapping when it
 * recognizes the 409 body, else an honest, non-committal fallback that never
 * repeats `ApiError`'s generic 409 default ("refresh and try again") since
 * that claim is exactly what's false for the plan-gate case above. Non-409
 * errors (500s, network failures, etc.) fall through to the ordinary
 * `getErrorMessage` priority (ApiError.userMessage > Error.message >
 * fallback) unchanged.
 */
export function taskMoveErrorMessage(err: unknown, plans: Plan[]): string {
  const specific = describeTaskMoveConflict(err, plans)
  if (specific) return specific
  if (isApiErrorFn(err) && err.status === 409) {
    return 'This move was rejected by the server — the task or its plan may be in a state that does not allow it right now.'
  }
  return getErrorMessage(err, 'Failed to move task')
}

// ── Task evidence & judge verdicts (ADR-049 D2, Planning & Goals) ───────────
//
// Read-only surfaces backing the acceptance-criteria editor's evidence viewer
// and per-attempt verdict list. See contracts/components/schemas/EvidenceRecord.yaml
// / JudgeVerdict.yaml (contract rows C10/C11).

export const taskEvidenceQueryKeys = {
  list: (taskId: string) => ['tasks', taskId, 'evidence'] as const,
}

export const taskVerdictsQueryKeys = {
  list: (taskId: string) => ['tasks', taskId, 'verdicts'] as const,
}

export function fetchTaskEvidence(taskId: string): Promise<EvidenceRecord[]> {
  return request<EvidenceRecord[]>(
    `/tasks/${encodeURIComponent(taskId)}/evidence`,
    undefined,
    z.array(EvidenceRecordSchema) as ZodType<EvidenceRecord[]>,
  )
}

export function fetchTaskVerdicts(taskId: string): Promise<JudgeVerdict[]> {
  return request<JudgeVerdict[]>(
    `/tasks/${encodeURIComponent(taskId)}/verdicts`,
    undefined,
    z.array(JudgeVerdictSchema) as ZodType<JudgeVerdict[]>,
  )
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
  if (dropped > 0) {
    if (import.meta.env?.DEV) {
       
      console.warn(`fetchCommands: dropped ${dropped} command(s) that failed schema validation`)
    } else if (import.meta.env?.MODE !== 'test') {
      // Bugfix (slash-palette silent-empty): this warning used to be DEV-only,
      // so a production build that dropped a command for failing
      // SlashCommandSchema had ZERO observable trace — the palette just
      // looked short with no signal anywhere. Mirrors recordCoercion /
      // _recordApiSchemaError's established DEV-console.warn-vs-production-
      // logError split (this file, above).
      logError({
        event: 'commandSchemaDrop',
        surface,
        droppedCount: dropped,
        totalCount: raw.length,
      })
    }
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
 * On "always", grant_recorded is present: true if the standing grant stuck,
 * false if this call was approved once but the next identical call will ask
 * again.
 */
export function submitToolApproval(
  approvalId: string,
  action: ToolApprovalActionRequest['action'],
): Promise<ToolApprovalResponse> {
  return request<ToolApprovalResponse>(`/tool-approvals/${encodeURIComponent(approvalId)}`, {
    method: 'POST',
    body: JSON.stringify({ action }),
  }, ToolApprovalResponseSchema as ZodType<ToolApprovalResponse>)
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
 * List the directories inside `path` on the operator's own machine, for the
 * mount folder picker.
 *
 * A web page cannot open the native folder picker and learn a real filesystem
 * path, so the gateway lists folders instead. Omit `path` to start in the
 * operator's home directory. Each entry carries its own `mountable`/`broad`
 * verdict so the picker can disable a choice at the point of selection rather
 * than accepting it and refusing afterwards.
 */
export function fetchHostFolders(path?: string): Promise<HostFolderListing> {
  const qs = path ? `?path=${encodeURIComponent(path)}` : ''
  return request<HostFolderListing>(
    `/system/folders${qs}`,
    undefined,
    HostFolderListingSchema as ZodType<HostFolderListing>,
  )
}

/**
 * Mount a real local folder into a workspace, making it writable there.
 *
 * Resolves with a `warning` when the target was broad but allowed (the home
 * directory, the filesystem root, a top-level system directory) — the caller
 * MUST surface it. Rejects (400) when the target is or lies inside the Omnipus
 * data directory, the one hard boundary.
 */
export function createWorkspaceMount(
  workspaceId: string,
  body: WorkspaceMountCreateRequest,
): Promise<WorkspaceMountCreateResponse> {
  return request<WorkspaceMountCreateResponse>(
    `/workspaces/${encodeURIComponent(workspaceId)}/mounts`,
    { method: 'POST', body: JSON.stringify(body) },
    WorkspaceMountCreateResponseSchema as ZodType<WorkspaceMountCreateResponse>,
  )
}

/**
 * Revoke a mount. This removes the workspace's ACCESS to the folder and
 * deletes nothing on the operator's disk — the distinction the UI must state
 * outright, since the control sits where "delete" normally lives.
 */
export function deleteWorkspaceMount(workspaceId: string, name: string): Promise<void> {
  return request<void>(
    `/workspaces/${encodeURIComponent(workspaceId)}/mounts/${encodeURIComponent(name)}`,
    { method: 'DELETE' },
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

/**
 * Rewrite a `409 Conflict` into an action-specific, human-actionable message
 * — the generic `ApiError` default for 409 ("This conflicts with the
 * current state. Please refresh and try again.") doesn't say WHY a
 * restart/run isn't possible right now. Only 409 is rewritten; every other
 * status (400/401/404/network) and every non-`ApiError` passes through
 * completely unchanged. Shared by `restartPlan`/`restartTask`/`runTask`
 * (ADR-052 FR-016/FR-019/FR-026 — restart/run reject with 409 for "not
 * restartable"/"not runnable" states).
 */
function friendlyConflictError(err: unknown, message: string): unknown {
  if (isApiErrorFn(err) && err.status === 409) {
    return new ApiError(409, message, { code: err.code, body: err.body, cause: err.cause })
  }
  return err
}

// ── Plans (ADR-049 D1/FR-1 — replaces Milestones; ADR-052 Wave 2 — agent plan
// authoring & execution) ──────────────────────────────────────────────────
//
// A Plan is a first-class entity that groups an executable task DAG under a
// goal, Definition of Done, owner agent, and 5-value state machine
// (draft/approved/running/done/failed). Tasks join a plan via `Task.plan_id`
// (same-workspace FK). Membership + `progress` are computed read-time by the
// backend — never stored on the Plan record (mirrors the removed Milestone's
// computeMilestoneCounts). See contracts/components/schemas/Plan*.yaml.
//
// Endpoints:
//   GET    /workspaces/{id}/plans → PlanListResponse
//   POST   /workspaces/{id}/plans → Plan   (createWorkspacePlan — the ONLY
//                                           create route; bare POST /plans
//                                           deliberately 405s [rest_plans.go]
//                                           since creation/listing are
//                                           workspace-nested. `workspace_id`
//                                           is ALSO required in the body and
//                                           validated to match the path.)
//   PUT    /plans/{id}           → Plan   (partial update — title/goal/
//                                           description/owner/dod/bounds
//                                           ONLY. ADR-052 G2/FR-007: the SPA
//                                           MUST NEVER send `state` here —
//                                           PUT is not a gated transition
//                                           entry point [it skips both the
//                                           FR-084 criteria gate and the
//                                           cap-16 admission check]. The
//                                           single gated entry point into
//                                           `approved` is POST .../approve.)
//   POST   /plans/{id}/approve   → Plan | 400 PlanApproveError
//                                           (executePlan — ADR-052 FR-003;
//                                           the ONLY path draft->approved
//                                           takes; the engine then promotes
//                                           approved->running under the cap
//                                           on its own tick)
//   POST   /plans/{id}/stop      → Plan   (Stop/Clear a running plan, D8)
//   POST   /plans/{id}/restart   → Plan | 409
//                                           (restartPlan — ADR-052 FR-026,
//                                           the ▶ Play route for a plan
//                                           `failed`+`stopped_by_user`)
//   DELETE /plans/{id}           → void   (rejected 400/409 while running)

export const plansQueryKeys = {
  list: (workspaceId: string) => ['plans', workspaceId] as const,
  detail: (workspaceId: string, planId: string) => ['plans', workspaceId, planId] as const,
}

const PlanListResponseSchema = z.object({
  plans: z.array(PlanSchema),
  total: z.number().int(),
})

export function fetchPlans(workspaceId: string): Promise<Plan[]> {
  return request<PlanListResponse>(
    `/workspaces/${encodeURIComponent(workspaceId)}/plans`,
    undefined,
    PlanListResponseSchema as ZodType<PlanListResponse>,
  ).then((res) => res.plans)
}

export function fetchPlan(id: string): Promise<Plan> {
  return request<Plan>(`/plans/${encodeURIComponent(id)}`, undefined, PlanSchema as ZodType<Plan>)
}

/**
 * Create a plan — POST /workspaces/{id}/plans (createWorkspacePlan). Bare
 * POST /plans 405s (`rest_plans.go` HandlePlans: "Bare /plans has no
 * GET/POST"); creation is workspace-nested, mirroring `fetchPlans`.
 * `body.workspace_id` drives the path (also required/validated in the body).
 */
export function createPlan(body: PlanCreateRequest): Promise<Plan> {
  return request<Plan>(
    `/workspaces/${encodeURIComponent(body.workspace_id)}/plans`,
    { method: 'POST', body: JSON.stringify(body) },
    PlanSchema as ZodType<Plan>,
  )
}

/**
 * Partial plan update — title/goal/description/owner_agent_id/dod/bounds
 * ONLY. ADR-052 §6.3/FR-007 (G2 fix): the SPA must NEVER send `state` in this
 * body — PUT is not, and must never become, a state-transition entry point
 * (the backend endpoint that previously accepted `state` here bypassed both
 * the FR-084 per-task criteria gate and the cap-16 admission check). Use
 * `executePlan` / `stopPlan` / `restartPlan` for every state transition.
 */
export function updatePlan(id: string, body: Omit<PlanUpdateRequest, 'state'>): Promise<Plan> {
  return request<Plan>(`/plans/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(body) }, PlanSchema as ZodType<Plan>)
}

/**
 * Execute (Approve) a draft plan — POST /plans/{id}/approve (ADR-052 FR-003/
 * FR-007/FR-008/US-3/US-5). This is the SOLE gated entry point into
 * `approved`: it runs the tiered Definition-of-Done check plus the
 * unconditional per-member-task criteria gate (FR-084), then the single
 * plan-engine instance promotes `approved` -> `running` under the global cap
 * (16) on its own tick — this call returns once the plan reaches `approved`,
 * it does not wait for a cap slot ("queued behind cap" is a normal outcome,
 * not an error). SD-C4: confirm-on-success, no optimistic flip — a `400`
 * carries a `PlanApproveError` body (`error` and/or `task_errors`); parse it
 * with `parsePlanApproveTaskErrors`.
 *
 * Named `executePlan` (the ▶ Execute button's semantics — G4/FR-003) rather
 * than the historical `approvePlan`, which used to PUT `{state:'approved'}`
 * and — per the G2 bug this repoints — silently bypassed BOTH the criteria
 * gate and the cap. `approvePlan` survives below as a deprecated alias for
 * any not-yet-swept caller; new call sites should use `executePlan`.
 */
export function executePlan(id: string): Promise<Plan> {
  return request<Plan>(`/plans/${encodeURIComponent(id)}/approve`, { method: 'POST' }, PlanSchema as ZodType<Plan>)
}

/** @deprecated Use `executePlan` — this name predates the ADR-052 G2 fix (PUT-based approve bypassed the criteria gate + cap). Same signature/behavior, POST /approve underneath. */
export const approvePlan = executePlan

/** Stop/Clear (D8) — stops a running plan's loop. May be optimistic (SD-C5): it cannot validation-fail like Approve. */
export function stopPlan(id: string): Promise<Plan> {
  return request<Plan>(`/plans/${encodeURIComponent(id)}/stop`, { method: 'POST' }, PlanSchema as ZodType<Plan>)
}

/**
 * Restart (▶ Play) a plan previously Stopped by the user — POST
 * /plans/{id}/restart (ADR-052 FR-016/FR-017/FR-026, US-9). Resets every
 * non-`done` member to `next`/`blocked` with `attempt_count` reset to 0,
 * resets the plan's `judge_rounds` to 0, preserves `done` members + their
 * evidence, clears `failed_reason`, and returns the plan in `approved` state
 * (NOT `running` — the engine promotes it under the cap on its own tick,
 * exactly like a first execute, so a restart can never skip cap admission).
 *
 * A `409` means "not restartable": the plan isn't `failed`, or its
 * `failed_reason` isn't `stopped_by_user` (a GENUINE failure —
 * `judge_rounds_exhausted` / `idle_expired` — is a terminal state with no
 * Play offered, FR-018). Rewritten into a specific, actionable message here
 * rather than the generic "conflicts with current state" default.
 */
export async function restartPlan(id: string): Promise<Plan> {
  try {
    return await request<Plan>(`/plans/${encodeURIComponent(id)}/restart`, { method: 'POST' }, PlanSchema as ZodType<Plan>)
  } catch (err) {
    throw friendlyConflictError(
      err,
      'This plan is not restartable — it must have been Stopped by a user (not a genuine failure) to Play it again.',
    )
  }
}

export function deletePlan(id: string): Promise<void> {
  return request<void>(`/plans/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

/**
 * PlanApproveTaskError — one entry of the generated `PlanApproveError.task_errors`
 * array (contracts/components/schemas/PlanApproveError.yaml — now a real,
 * generated Constraint #8 wire type; this alias exists only so callers don't
 * need to reach into `NonNullable<PlanApproveError['task_errors']>[number]`
 * themselves).
 */
export type PlanApproveTaskError = NonNullable<PlanApproveError['task_errors']>[number]

/**
 * Parse a `POST /plans/{id}/approve` `400` body (`ApiError.body`) into the
 * generated `PlanApproveError` shape, edge-validated with the generated Zod
 * schema (Constraint #8 — no hand-rolled parsing of the response shape).
 * Returns `null` when the body is empty, not JSON, doesn't validate against
 * the schema, or validates but carries no `task_errors` — callers fall back
 * to `err.userMessage` (which already carries the plan-level `error` string
 * for the non-task-errors rejection case, e.g. an empty DoD).
 */
export function parsePlanApproveTaskErrors(body: string | undefined): PlanApproveTaskError[] | null {
  if (!body) return null
  let parsed: unknown
  try {
    parsed = JSON.parse(body)
  } catch {
    return null
  }
  const result = PlanApproveErrorSchema.safeParse(parsed)
  if (!result.success) return null
  const taskErrors = result.data.task_errors
  return taskErrors && taskErrors.length > 0 ? taskErrors : null
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

// ADR-053 D12/R§8.3 (FE-6) — app-level OVERALL token budget for the Usage
// screen. ONE shared pool across all workloads (owner/member/verifier/Judge);
// no per-plan cap, no money/USD cap, no IsPrivilegedAgent exemption (D12).
// GET returns the live spend accounting (TokenBudgetStatus). PUT persists the
// operator-set ceiling; the ceiling is restart-gated (R§8.3e — a live ceiling
// change would straddle two budgets, the N-15 hazard; the live lever for
// runaway spend is the existing Stop/cancel cascade, NOT a live token cut).
// The PUT body is the single operator-set field (`budget`; 0 = unbounded
// sentinel, R§8.3a) — no hand-written request wire type; the response is
// zod-validated against the landed TokenBudgetStatus schema (contract-first #8).
// See contracts/components/schemas/TokenBudgetStatus.yaml.
export const tokenBudgetQueryKeys = {
  status: ['token-budget', 'status'] as const,
}

export function fetchTokenBudgetStatus(): Promise<TokenBudgetStatus> {
  return request<TokenBudgetStatus>(
    '/settings/token-budget',
    undefined,
    TokenBudgetStatusSchema as ZodType<TokenBudgetStatus>,
  )
}

export function updateTokenBudget(budget: number): Promise<TokenBudgetStatus> {
  return request<TokenBudgetStatus>(
    '/settings/token-budget',
    { method: 'PUT', body: JSON.stringify({ budget }) },
    TokenBudgetStatusSchema as ZodType<TokenBudgetStatus>,
  )
}
