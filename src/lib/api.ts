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

import { getConfigCoercionCount, resetConfigCoercionCount } from './api/config'
import { getApiSchemaErrorCount, resetApiSchemaErrorCount } from './api/http'

export { ApiError, isApiError, getErrorMessage } from './api-error'

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
  SandboxStatus,
  AuditEntry,
  AuditLogResponse,
  AuditLogToggle,
  RateLimitConfig,
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
  // O4 gateway self-restart (contract-first #8):
  GatewayRestartResponse,
  // O14 god-mode switch (contract-first #8):
  GodModeStatus,
  GodModeUpdateRequest,
  // Slash-command harmonization (contract-first #8):
  SlashCommand,
  // Memory/recap settings (workspace-heartbeat-memory-config-spec.md FR-019):
  MemorySettings,
  // M11 per-(agent, workspace) email mailbox account (contract-first #8):
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
  // ADR-083 EMB-007/EMB-007b Step 0 — typed 409 body for a refused Library
  // whole-file save:
  LibraryConflictError,
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
  // ADR-083 EMB-007/EMB-007b Step 0:
  LibraryConflictError,
}

// Performance — max-parallel agent concurrency settings.
// PerformanceSettings and PerformanceSettingsUpdate are re-exported from
// generated openapi-types (contract-first #8).
// See contracts/components/schemas/PerformanceSettings.yaml.

export type { PerformanceSettings, PerformanceSettingsUpdate }

// ── Split modules (2026-09-15) ──────────────────────────────────────────────────
//
// api.ts is a barrel: every name below was declared in this file before the
// split and is re-exported here, so no caller had to change. The modules own
// the code; this file owns the public surface.
export * from './api/agents'
export * from './api/auth'
export * from './api/channels'
export * from './api/config'
export { ApiSchemaError, CSRF_HEADER_NAME, getApiSchemaErrorCount, getCsrfCookie, resetApiSchemaErrorCount } from './api/http'
export * from './api/library'
export * from './api/plans'
export * from './api/providers'
export * from './api/security'
export * from './api/sessions'
export * from './api/skills'
export * from './api/system'
export * from './api/tasks'
export * from './api/tools'
export * from './api/uploads'
export * from './api/workspaces'
