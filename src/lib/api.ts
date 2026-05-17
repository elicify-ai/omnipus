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

import type { ZodType } from 'zod'
import { z } from 'zod'

// ── Generated Zod schemas (REST edge validation) ────────────────────────────
//
// These are the generated schemas from contracts/openapi.yaml. They are used
// to validate API responses at the SPA edge (hard-constraint #8). Only named
// schemas that match the SPA-internal types are imported here — call sites
// that use local SPA-specific transformation types (Session, Config, Provider,
// AppState, etc.) are listed in the GAP REPORT below.
//
// GAP REPORT — endpoints without matching generated schema (no schema passed):
//   GET /sessions           — RawSession→Session transform; Session schema differs (stats nesting)
//   GET /sessions/:id/messages — local Message type differs (params vs parameters, status enums)
//   GET /sessions/:id       — local SessionDetail transform; same as above
//   POST /sessions          — local Session type
//   PUT /sessions/:id       — local Session type
//   GET /config             — local Config type with custom transform (rawToFrontendConfig)
//   PUT /config             — local Config type
//   POST /config/gateway/rotate-token — local {token:string} inline type
//   GET /providers          — local Provider type (not in generated schema)
//   PUT /providers/:id      — local Provider type
//   POST /providers/:id/test — local {success,error?} inline type
//   GET /tasks              — local Task type (not in generated schema)
//   GET /tasks/:id/subtasks — local Task type
//   POST /tasks             — local Task type
//   PUT /tasks/:id          — local Task type
//   POST /tasks/:id/start   — void
//   DELETE /tasks/:id       — void
//   GET /status             — local GatewayStatus type (not in generated schema)
//   GET /tools              — RegistryTool (not in generated schema; uses ToolRegistryEntry)
//   GET /channels           — local Channel type; schema ChannelEntry has extra `description` field
//   PUT /channels/:id/enable — {id,enabled} inline (schema returns ChannelEntry)
//   PUT /channels/:id/disable — {id,enabled} inline
//   GET /channels/:id       — Record<string,unknown> passthrough
//   PUT /channels/:id/configure — void/Record passthrough
//   POST /channels/:id/test — {success,message} inline (matches schema exactly → wired)
//   GET /skills             — local Skill type (not in generated schema)
//   DELETE /skills/:name    — void
//   GET /mcp-servers        — local McpServer type (not in generated schema)
//   POST /mcp-servers       — local McpServer type
//   DELETE /mcp-servers/:id — void
//   GET /mcp-servers/:id/tools — string[] inline
//   GET /storage/stats      — local StorageStats type (not in generated schema)
//   GET /state              — local AppState type (not in generated schema)
//   PATCH /state            — void
//   GET /auth/validate      — local ValidateTokenResponse (matches inline schema → wired)
//   POST /doctor            — local DoctorResult type (not in generated schema)
//   GET /doctor             — local DoctorResult|null (not in generated schema)
//   GET /activity           — local ActivityEvent type (not in generated schema)
//   GET /credentials        — string[] inline
//   POST /credentials       — {key:string} inline
//   DELETE /credentials/:key — {status,key} inline
//   GET /devices            — local DevicesResponse type (not in generated schema)
//   POST /backup            — {filename:string} inline (schema returns {path,size_bytes,created_at})
//   GET /backups            — local BackupEntry type; schema has path not filename → different
//   POST /restore           — void
//   DELETE /sessions/all    — void
//   POST /auth/change-password — {success:boolean} inline (matches schema → wired)
//   GET /me                 — local MeInfo type (not in generated schema)
//   PUT /user-context       — void
//   GET /user-context       — {content:string} inline
//   PUT /security/audit-log — local AuditLogUpdateResponse (inline schema → wired)
//   PUT /security/skill-trust — local SkillTrustUpdateResponse (inline schema → wired)
//   PUT /security/prompt-guard — local PromptGuardUpdateResponse (inline schema → wired)
//   GET /security/rate-limits — local RateLimitsResponse (inline schema → wired)
//   PUT /security/rate-limits — local RateLimitsResponse (inline schema → wired)
//   PUT /security/session-scope — local SessionScopeUpdateResponse (inline schema → wired)
//   GET /security/retention — RetentionConfig (partial schema → wired with .partial())
//   PUT /security/retention — local RetentionUpdateResponse (inline schema → wired)
//   POST /security/retention/sweep — RetentionSweepResult (wired)
//   GET /agents/:id/tools   — inline schema → wired
//   PUT /agents/:id/tools   — inline schema → wired
//   POST /tool-approvals/:id — void
//   GET /about              — AboutResponse schema has fields that differ (uptime vs uptime_seconds) → partial match, wired with passthrough

import {
  LoginResponse as LoginResponseSchema,
  ProbeProviderResponse as ProbeProviderResponseSchema,
  Agent as AgentSchema,
  AgentSession as AgentSessionSchema,
  AgentToolEntry as AgentToolEntrySchema,
  AgentToolsCfg as AgentToolsCfgSchema,
  AuditEntry as AuditEntrySchema,
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
  UserCreateResponse as UserCreateResponseSchema,
  UserDeleteResponse as UserDeleteResponseSchema,
  UserResetPasswordResponse as UserResetPasswordResponseSchema,
  UserRoleChangeResponse as UserRoleChangeResponseSchema,
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

// Types where local hand-written body diverges from generated — imported aliased
// for any internal-file use only. NOT in the re-export block below.
import type {
  Agent as _GAgent,
  AgentToolsCfg as _GAgentToolsCfg,
  Session as _GSession,
  SessionDetail as _GSessionDetail,
  Message as _GMessage,
  ToolCall as _GToolCall,
} from '@/lib/api/generated/openapi-types'

// Types whose generated shape is canonical (no local body) — import into scope
// so function return-type annotations compile, then re-export for consumers.
import type {
  LoginResponse,
  ProbeProviderResponse,
  AgentSession,
  AgentToolEntry,
  SessionStats,
  Attachment,
  User,
  UserCreateRequest,
  UserCreateResponse,
  UserDeleteResponse,
  UserRoleChangeRequest,
  UserRoleChangeResponse,
  UserResetPasswordRequest,
  UserResetPasswordResponse,
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
  AuditLogToggle,
  RateLimitConfig,
  ExecAllowlist,
  ExecProxyStatus,
  SkillTrustResponse,
  PromptGuardResponse,
  PendingRestartEntry,
  AboutResponse,
} from '@/lib/api/generated/openapi-types'

export type {
  LoginResponse,
  ProbeProviderResponse,
  AgentSession,
  AgentToolEntry,
  SessionStats,
  Attachment,
  User,
  UserCreateRequest,
  UserCreateResponse,
  UserDeleteResponse,
  UserRoleChangeRequest,
  UserRoleChangeResponse,
  UserResetPasswordRequest,
  UserResetPasswordResponse,
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
  AuditLogToggle,
  RateLimitConfig,
  ExecAllowlist,
  ExecProxyStatus,
  SkillTrustResponse,
  PromptGuardResponse,
  PendingRestartEntry,
  AboutResponse,
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

  // Parse the response body, handling non-JSON (e.g. unexpected HTML 200)
  // gracefully — surface as ApiSchemaError with the raw text as rawBody so
  // callers can see what the server actually sent.
  let body: unknown
  try {
    body = await res.json() as unknown
  } catch (cause) {
    const rawText = String(cause instanceof Error ? cause.message : cause)
    if (schema !== undefined) {
      _apiSchemaErrorCount++
      const schemaErr = new ApiSchemaError(
        `${method} /api/v1${path}`,
        [{ path: [], message: 'Response is not valid JSON' }],
        rawText,
      )
      if (import.meta.env.DEV) {
        try {
          // eslint-disable-next-line @typescript-eslint/no-require-imports
          const { useUiStore } = require('@/store/ui') as {
            useUiStore: { getState: () => { addToast: (t: { message: string; variant: 'warning' }) => void } }
          }
          useUiStore.getState().addToast({
            message: `[api] Non-JSON response: ${path}`,
            variant: 'warning',
          })
        } catch {
          console.warn('[api] Non-JSON response:', path)
        }
      }
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
      _apiSchemaErrorCount++
      const schemaErr = new ApiSchemaError(
        `${method} /api/v1${path}`,
        result.error.issues.map((i) => ({ path: i.path as (string | number)[], message: i.message })),
        body,
      )
      if (import.meta.env.DEV) {
        try {
          // eslint-disable-next-line @typescript-eslint/no-require-imports
          const { useUiStore } = require('@/store/ui') as {
            useUiStore: { getState: () => { addToast: (t: { message: string; variant: 'warning' }) => void } }
          }
          const first = schemaErr.zodIssues[0]
          useUiStore.getState().addToast({
            message: `[api] Schema mismatch: ${path} — ${first?.message ?? 'unknown'}`,
            variant: 'warning',
          })
        } catch {
          console.warn('[api] Schema mismatch:', schemaErr.message)
        }
      }
      throw schemaErr
    }
    return result.data
  }

  return body as T
}

// ── Agents ────────────────────────────────────────────────────────────────────

export type SandboxProfile = 'none' | 'workspace' | 'workspace+net' | 'host' | 'off'

export interface AgentShellPolicy {
  enable_deny_patterns?: boolean
  custom_deny_patterns?: string[]
}

export interface Agent {
  id: string
  name: string
  description: string
  type: 'core' | 'custom'
  model: string
  status: 'active' | 'idle' | 'error' | 'draft'
  locked?: boolean
  icon?: string
  color?: string
  tools?: string[]
  tools_cfg?: AgentToolsCfg
  soul?: string
  heartbeat?: string
  instructions?: string
  fallback_models?: string[]
  model_params?: {
    temperature?: number
    max_tokens?: number
    top_p?: number
  }
  timeout_seconds?: number
  max_tool_iterations?: number
  steering_mode?: string
  tool_feedback?: boolean
  heartbeat_enabled?: boolean
  heartbeat_interval?: number
  rate_limits?: {
    use_global_defaults: boolean
    max_llm_calls_per_hour?: number
    max_tool_calls_per_minute?: number
    max_cost_per_day?: number
  }
  sandbox_profile?: SandboxProfile
  shell_policy?: AgentShellPolicy
  stats?: {
    total_sessions: number
    total_tokens: number
    total_cost: number
    last_active?: string
  }
}

export function fetchAgents(): Promise<Agent[]> {
  return request<Agent[]>('/agents', undefined, z.array(AgentSchema) as ZodType<Agent[]>)
}

export function fetchAgent(id: string): Promise<Agent> {
  return request<Agent>(`/agents/${encodeURIComponent(id)}`, undefined, AgentSchema as ZodType<Agent>)
}

export function createAgent(data: Partial<Agent>): Promise<Agent> {
  return request<Agent>('/agents', { method: 'POST', body: JSON.stringify(data) }, AgentSchema as ZodType<Agent>)
}

export function updateAgent(id: string, data: Partial<Agent>): Promise<Agent> {
  return request<Agent>(`/agents/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(data) }, AgentSchema as ZodType<Agent>)
}

// AgentSession — re-exported from generated openapi-types (no local body needed).

export function fetchAgentSessions(agentId: string): Promise<AgentSession[]> {
  return request<AgentSession[]>(`/agents/${encodeURIComponent(agentId)}/sessions`, undefined, z.array(AgentSessionSchema))
}

// ── Sessions ──────────────────────────────────────────────────────────────────

export interface Session {
  id: string
  agent_id: string
  title: string
  type: 'chat' | 'task' | 'channel'
  status?: 'active' | 'archived' | 'interrupted'
  task_id?: string
  created_at: string
  updated_at: string
  message_count: number
  total_tokens?: number
  total_cost?: number
  // Multi-agent session fields — present on sessions created with the joined
  // session model. For legacy single-agent sessions these are absent; callers
  // should fall back to [agent_id] when agent_ids is undefined.
  agent_ids?: string[]      // all agents that participated in this session
  active_agent_id?: string  // the agent currently handling this session
}

interface RawSession {
  id: string
  agent_id: string
  title: string
  type?: 'chat' | 'task' | 'channel'
  status?: 'active' | 'archived' | 'interrupted'
  task_id?: string
  created_at: string
  updated_at: string
  agent_ids?: string[]
  active_agent_id?: string
  stats?: {
    tokens_in: number
    tokens_out: number
    tokens_total: number
    cost: number
    tool_calls: number
    message_count: number
  }
}

function rawToSession(raw: RawSession): Session {
  return {
    id: raw.id,
    agent_id: raw.agent_id,
    title: raw.title,
    // Legacy sessions without a type field default to 'chat'
    type: raw.type ?? 'chat',
    status: raw.status,
    task_id: raw.task_id,
    created_at: raw.created_at,
    updated_at: raw.updated_at,
    message_count: raw.stats?.message_count ?? 0,
    total_tokens: raw.stats?.tokens_total,
    total_cost: raw.stats?.cost,
    agent_ids: raw.agent_ids,
    active_agent_id: raw.active_agent_id,
  }
}

export interface Message {
  id: string
  session_id?: string
  role: 'user' | 'assistant' | 'system'
  content: string
  timestamp: string
  tokens?: number
  cost?: number
  status?: 'streaming' | 'done' | 'error' | 'interrupted'
  tool_calls?: ToolCall[]
}

export interface ToolCall {
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
export interface ServeWorkspaceResult {
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
export interface RunInWorkspaceResult {
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

export async function fetchSessions(agentId?: string, type?: Session['type']): Promise<Session[]> {
  const params: Record<string, string> = {}
  if (agentId) params.agent_id = agentId
  if (type) params.type = type
  const qs = Object.keys(params).length > 0 ? '?' + new URLSearchParams(params).toString() : ''
  const raw = await request<RawSession[]>(`/sessions${qs}`)
  return raw.map(rawToSession)
}

export function fetchSessionMessages(sessionId: string): Promise<Message[]> {
  return request<Message[]>(`/sessions/${encodeURIComponent(sessionId)}/messages`)
}

export async function installSkillFromFile(content: string, filename: string): Promise<void> {
  await request<void>('/skills/install', {
    method: 'POST',
    body: JSON.stringify({ content, filename }),
  })
}

export interface SessionDetail {
  session: Session
  messages: Message[]
  agent_removed?: boolean
}

export async function fetchSessionDetail(sessionId: string): Promise<SessionDetail> {
  const raw = await request<{ session: RawSession; messages: Message[]; agent_removed?: boolean }>(
    `/sessions/${encodeURIComponent(sessionId)}`,
  )
  return {
    session: rawToSession(raw.session),
    messages: raw.messages,
    agent_removed: raw.agent_removed,
  }
}

export function createSession(agentId: string): Promise<Session> {
  return request<Session>('/sessions', {
    method: 'POST',
    body: JSON.stringify({ agent_id: agentId }),
  })
}

// ── Config ────────────────────────────────────────────────────────────────────

// Frontend-shaped config. Mapped from raw backend response via rawToFrontendConfig().
export interface Config {
  gateway: {
    bind_address: string
    port: number
    auth_mode: 'none' | 'token'
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
    }
  }
}

const VALID_AUTH_MODES = ['none', 'token'] as const
const VALID_POLICY_MODES = ['allow', 'deny'] as const
const VALID_EXEC_APPROVALS = ['auto', 'ask', 'deny'] as const
const VALID_INJECTION_LEVELS = ['off', 'low', 'medium', 'high'] as const

function validEnum<T extends string>(value: unknown, valid: readonly T[], fallback: T): T {
  if ((valid as readonly string[]).includes(value as string)) return value as T
  console.warn('[api] validEnum: unexpected value', value, '— falling back to', fallback)
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
      auth_mode: validEnum(gateway.auth_mode, VALID_AUTH_MODES, 'none'),
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
      },
    },
  }
}

export async function fetchConfig(): Promise<Config> {
  const raw = await request<Record<string, unknown>>('/config')
  return rawToFrontendConfig(raw)
}

export function updateConfig(data: Partial<Config>): Promise<Config> {
  return request<Config>('/config', { method: 'PUT', body: JSON.stringify(data) })
}

// ── Providers ─────────────────────────────────────────────────────────────────

export interface Provider {
  id: string
  name?: string
  display_name?: string
  status: 'connected' | 'disconnected' | 'error'
  models?: string[]
  error?: string
}

export function fetchProviders(): Promise<Provider[]> {
  return request<Provider[]>('/providers')
}

export function configureProvider(id: string, apiKey?: string, endpoint?: string, model?: string): Promise<Provider> {
  const body: Record<string, string> = {}
  if (apiKey !== undefined) body.api_key = apiKey
  if (endpoint !== undefined) body.endpoint = endpoint
  if (model !== undefined) body.model = model
  return request<Provider>(`/providers/${id}`, {
    method: 'PUT',
    body: JSON.stringify(body),
  })
}

export function testProvider(id: string): Promise<{ success: boolean; error?: string }> {
  return request(`/providers/${id}/test`, { method: 'POST' })
}

export function rotateGatewayToken(): Promise<{ token: string }> {
  return request('/config/gateway/rotate-token', { method: 'POST' })
}

// ── Tasks ─────────────────────────────────────────────────────────────────────

export interface Task {
  id: string
  title: string
  prompt: string
  agent_id?: string
  agent_name?: string
  created_by?: string
  parent_task_id?: string
  priority: number
  status: 'queued' | 'assigned' | 'running' | 'completed' | 'failed'
  result?: string
  artifacts?: string[]
  session_id?: string
  trigger_type: 'manual' | 'time' | 'event'
  created_at?: string
  started_at?: string
  completed_at?: string
}

export function fetchTasks(status?: Task['status']): Promise<Task[]> {
  const qs = status ? '?' + new URLSearchParams({ status }).toString() : ''
  return request<Task[]>(`/tasks${qs}`)
}

export function fetchSubtasks(taskId: string): Promise<Task[]> {
  return request<Task[]>(`/tasks/${encodeURIComponent(taskId)}/subtasks`)
}

export function createTask(data: {
  title: string
  prompt: string
  agent_id?: string
  priority?: number
  parent_task_id?: string
}): Promise<Task> {
  return request<Task>('/tasks', { method: 'POST', body: JSON.stringify(data) })
}

export function updateTask(id: string, data: Partial<Task>): Promise<Task> {
  return request<Task>(`/tasks/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(data) })
}

export function startTask(id: string): Promise<void> {
  return request(`/tasks/${encodeURIComponent(id)}/start`, { method: 'POST' })
}

export function deleteTask(id: string): Promise<void> {
  return request(`/tasks/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

// ── Gateway Status ────────────────────────────────────────────────────────────

export interface GatewayStatus {
  online: boolean
  agent_count: number
  channel_count: number
  daily_cost: number
  version?: string
}

export function fetchGatewayStatus(): Promise<GatewayStatus> {
  return request<GatewayStatus>('/status')
}

// ── Tools & Channels ──────────────────────────────────────────────────────────

export interface Tool {
  name: string
  category: string
  description: string
}

export function fetchTools(): Promise<Tool[]> {
  return request<Tool[]>('/tools')
}

export interface Channel {
  id: string
  name: string
  transport: string
  enabled: boolean
  configured?: boolean
}

export function fetchChannels(): Promise<Channel[]> {
  return request<Channel[]>('/channels')
}

export function enableChannel(id: string): Promise<Channel> {
  return request<Channel>(`/channels/${encodeURIComponent(id)}/enable`, { method: 'PUT' })
}

export function disableChannel(id: string): Promise<Channel> {
  return request<Channel>(`/channels/${encodeURIComponent(id)}/disable`, { method: 'PUT' })
}

export function fetchChannelConfig(id: string): Promise<Record<string, unknown>> {
  return request<Record<string, unknown>>(`/channels/${encodeURIComponent(id)}`)
}

export function configureChannel(id: string, config: Record<string, unknown>): Promise<void> {
  return request<void>(`/channels/${encodeURIComponent(id)}/configure`, {
    method: 'PUT',
    body: JSON.stringify(config),
  })
}

const _testChannelSchema = z.object({ success: z.boolean(), message: z.string() }).passthrough()

export function testChannel(id: string): Promise<{ success: boolean; message: string }> {
  return request<{ success: boolean; message: string }>(`/channels/${encodeURIComponent(id)}/test`, {
    method: 'POST',
  }, _testChannelSchema)
}

// ── Skills ────────────────────────────────────────────────────────────────────

export interface Skill {
  id: string
  name: string
  version: string
  description: string
  author: string
  verified: boolean
  status: 'active' | 'inactive' | 'error'
  agent_assignment?: string
}

export interface McpServer {
  id: string
  name: string
  transport: 'stdio' | 'sse' | 'websocket'
  status: 'connected' | 'disconnected' | 'error'
  tool_count: number
  tools?: string[]
}

export interface McpServerCreate {
  name: string
  command: string
  args?: string[]
  transport: 'stdio' | 'sse' | 'websocket'
}

export function fetchSkills(): Promise<Skill[]> {
  return request<Skill[]>('/skills')
}

export function deleteSkill(name: string): Promise<void> {
  return request<void>(`/skills/${encodeURIComponent(name)}`, { method: 'DELETE' })
}

export function fetchMcpServers(): Promise<McpServer[]> {
  return request<McpServer[]>('/mcp-servers')
}

export function addMcpServer(data: McpServerCreate): Promise<McpServer> {
  return request<McpServer>('/mcp-servers', { method: 'POST', body: JSON.stringify(data) })
}

export function deleteMcpServer(id: string): Promise<void> {
  return request<void>(`/mcp-servers/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function fetchMcpServerTools(id: string): Promise<string[]> {
  return request<string[]>(`/mcp-servers/${encodeURIComponent(id)}/tools`)
}

// ── Storage Stats ─────────────────────────────────────────────────────────────

export interface StorageStats {
  workspace_size_bytes: number
  session_count: number
  memory_entry_count: number
  oldest_session_date?: string
}

export function fetchStorageStats(): Promise<StorageStats> {
  return request<StorageStats>('/storage/stats')
}

// ── App State ─────────────────────────────────────────────────────────────────

export interface AppState {
  onboarding_complete: boolean
  last_doctor_run?: string
  last_doctor_score?: number
  god_mode_available?: boolean
  god_mode_opted_in?: boolean
  dev_mode_bypass?: boolean
}

export function fetchAppState(): Promise<AppState> {
  return request<AppState>('/state')
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

export interface CompleteOnboardingRequest {
  provider: {
    id: string
    api_key: string
    model: string
  }
  admin: {
    username: string
    password: string
  }
}

export async function completeOnboardingTransaction(req: CompleteOnboardingRequest): Promise<LoginResponse> {
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

// ValidateTokenResponse: type alias for the auth validate endpoint response.
// Not in the generated openapi-types; kept as a local type.
export type ValidateTokenResponse = {
  username: string
  role: UserRole
}

const _validateTokenSchema = z.object({ username: z.string(), role: z.enum(['admin', 'user']) }).passthrough()

export async function validateToken(): Promise<ValidateTokenResponse> {
  return request<ValidateTokenResponse>('/auth/validate', undefined, _validateTokenSchema)
}

// ── Doctor ────────────────────────────────────────────────────────────────────

export interface DoctorIssue {
  id: string
  severity: 'high' | 'medium' | 'low'
  title: string
  description: string
  recommendation: string
  action_link?: string
  action_label?: string
}

export interface DoctorResult {
  score: number
  issues: DoctorIssue[]
  checked_at: string
}

export function fetchDoctorResults(): Promise<DoctorResult | null> {
  return request<DoctorResult | null>('/doctor')
}

export function runDoctor(): Promise<DoctorResult> {
  return request<DoctorResult>('/doctor', { method: 'POST' })
}

// ── Activity Feed ─────────────────────────────────────────────────────────────

export interface ActivityEvent {
  id: string
  type: 'task_created' | 'task_updated' | 'session_started' | 'session_ended' | 'agent_error' | 'tool_called' | 'approval_requested' | (string & {})
  summary: string
  timestamp: string
  agent_id?: string
  agent_name?: string
}

export function fetchActivity(): Promise<ActivityEvent[]> {
  return request<ActivityEvent[]>('/activity')
}

// ── Credentials ───────────────────────────────────────────────────────────────

export interface CredentialKey {
  key: string
  created_at?: string
  updated_at?: string
}

export function fetchCredentials(): Promise<CredentialKey[]> {
  return request<CredentialKey[]>('/credentials')
}

export function addCredential(key: string, value: string): Promise<void> {
  return request<void>('/credentials', { method: 'POST', body: JSON.stringify({ key, value }) })
}

export function deleteCredential(key: string): Promise<void> {
  return request<void>(`/credentials/${encodeURIComponent(key)}`, { method: 'DELETE' })
}

// ── Devices ───────────────────────────────────────────────────────────────────

export interface DevicePending {
  device_id: string
  fingerprint: string
  pairing_code: string
  device_name: string
  created_at: string
  expires_at: string
}

export interface DevicePaired {
  device_id: string
  fingerprint: string
  device_name: string
  paired_at: string
  last_seen_at: string
  status: 'active' | 'revoked'
}

// DevicesResponse: SPA-internal response shape not described in the contract.
export type DevicesResponse = {
  pending: DevicePending[]
  paired: DevicePaired[]
}

export function fetchDevices(): Promise<DevicesResponse> {
  return request<DevicesResponse>('/devices')
}

// ── Backup / Restore ──────────────────────────────────────────────────────────

export interface BackupEntry {
  filename: string
  size_bytes: number
  created_at: string
}

export function createBackup(): Promise<{ filename: string }> {
  return request('/backup', { method: 'POST' })
}

export function fetchBackups(): Promise<BackupEntry[]> {
  return request<BackupEntry[]>('/backups')
}

export function restoreBackup(filename: string): Promise<void> {
  return request<void>('/restore', { method: 'POST', body: JSON.stringify({ filename }) })
}

export function clearAllSessions(): Promise<void> {
  return request<void>('/sessions/all', { method: 'DELETE' })
}

export function renameSession(id: string, title: string): Promise<Session> {
  return request<Session>(`/sessions/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify({ title }),
  })
}

const _deleteSessionSchema = z.object({ success: z.boolean() }).passthrough()

export function deleteSession(id: string): Promise<{ success: boolean }> {
  return request<{ success: boolean }>(`/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }, _deleteSessionSchema)
}

// ── About ─────────────────────────────────────────────────────────────────────

export interface AboutInfo {
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
  return request<AboutInfo>('/about')
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

export function fetchAuditLog(): Promise<AuditEntry[]> {
  return request<AuditEntry[]>('/audit-log', undefined, z.array(AuditEntrySchema))
}

// ── User Context (USER.md) ────────────────────────────────────────────────────

export function fetchUserContext(): Promise<{ content: string }> {
  return request<{ content: string }>('/user-context')
}

export function updateUserContext(content: string): Promise<void> {
  return request<void>('/user-context', {
    method: 'PUT',
    body: JSON.stringify({ content }),
  })
}

// ── RBAC / Me ─────────────────────────────────────────────────────────────────

export type UserRole = 'admin' | 'user'

export interface MeInfo {
  role: UserRole
}

export async function fetchMe(): Promise<MeInfo> {
  return request<MeInfo>('/me')
}

// ── File Upload ───────────────────────────────────────────────────────────────

export interface UploadedFile {
  name: string
  path: string
  size: number
  content_type: string
}

export async function uploadFiles(sessionId: string, files: File[]): Promise<{ files: UploadedFile[] }> {
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
  return res.json()
}

// ── Auth ──────────────────────────────────────────────────────────────────────

const _changePasswordSchema = z.object({ success: z.boolean() }).passthrough()

export function changePassword(currentPassword: string, newPassword: string): Promise<{ success: boolean }> {
  return request<{ success: boolean }>('/auth/change-password', {
    method: 'POST',
    body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
  }, _changePasswordSchema)
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

// Audit log toggle — distinct from GET /audit-log (which returns AuditEntry[]).
// This endpoint controls whether audit logging is enabled at all.
// AuditLogToggle is re-exported from generated openapi-types above.

// SPA-internal update response — not a wire type, no generated counterpart.
export type AuditLogUpdateResponse = {
  saved: boolean
  requires_restart: boolean
  applied_enabled: boolean
}

const _auditLogUpdateSchema = z.object({ saved: z.boolean(), requires_restart: z.boolean(), applied_enabled: z.boolean() }).passthrough()

export function fetchAuditLogToggle(): Promise<AuditLogToggle> {
  return request<AuditLogToggle>('/security/audit-log', undefined, AuditLogToggleSchema)
}

export function updateAuditLog(enabled: boolean): Promise<AuditLogUpdateResponse> {
  return request<AuditLogUpdateResponse>('/security/audit-log', {
    method: 'PUT',
    body: JSON.stringify({ enabled }),
  }, _auditLogUpdateSchema)
}

// Skill trust — controls how unverified community skills are handled.
// SkillTrustResponse is re-exported from generated openapi-types above.

// SPA-internal update response — not a wire type, no generated counterpart.
export type SkillTrustUpdateResponse = {
  saved: boolean
  requires_restart: boolean
  applied_level: SkillTrustLevel
}

export interface SkillTrustUpdateBody {
  level: SkillTrustLevel
}

const _skillTrustUpdateSchema = z.object({
  saved: z.boolean(),
  requires_restart: z.boolean(),
  applied_level: z.enum(['block_unverified', 'warn_unverified', 'allow_all']),
}).passthrough()

export function fetchSkillTrust(): Promise<SkillTrustResponse> {
  return request<SkillTrustResponse>('/security/skill-trust', undefined, SkillTrustResponseSchema)
}

export function updateSkillTrust(level: SkillTrustLevel): Promise<SkillTrustUpdateResponse> {
  return request<SkillTrustUpdateResponse>('/security/skill-trust', {
    method: 'PUT',
    body: JSON.stringify({ level } satisfies SkillTrustUpdateBody),
  }, _skillTrustUpdateSchema)
}

// Prompt guard — uses `level` field, aligns with PromptInjectionLevel.
// PromptGuardResponse is re-exported from generated openapi-types above.

// SPA-internal update response — not a wire type, no generated counterpart.
export type PromptGuardUpdateResponse = {
  saved: boolean
  requires_restart: boolean
  applied_level: PromptInjectionLevel
}

export interface PromptGuardUpdateBody {
  level: PromptInjectionLevel
}

const _promptGuardUpdateSchema = z.object({
  saved: z.boolean(),
  requires_restart: z.boolean(),
  applied_level: z.enum(['low', 'medium', 'high']),
}).passthrough()

export function fetchPromptGuardLevel(): Promise<PromptGuardResponse> {
  return request<PromptGuardResponse>('/security/prompt-guard', undefined, PromptGuardResponseSchema)
}

export function updatePromptGuardLevel(level: PromptInjectionLevel): Promise<PromptGuardUpdateResponse> {
  return request<PromptGuardUpdateResponse>('/security/prompt-guard', {
    method: 'PUT',
    body: JSON.stringify({ level } satisfies PromptGuardUpdateBody),
  }, _promptGuardUpdateSchema)
}

// Rate limits — adds write support and configures spending/throughput caps.
// SPA-internal read response — not a generated wire type. The generated
// RateLimitConfig schema is used for the full config shape.
export type RateLimitsResponse = {
  daily_cost_cap_usd?: number
  max_agent_llm_calls_per_hour?: number
  max_agent_tool_calls_per_minute?: number
}

export interface RateLimitsUpdateBody {
  daily_cost_cap_usd?: number
  max_agent_llm_calls_per_hour?: number
  max_agent_tool_calls_per_minute?: number
}

const _rateLimitsSchema = z.object({
  daily_cost_cap_usd: z.number().optional(),
  max_agent_llm_calls_per_hour: z.number().optional(),
  max_agent_tool_calls_per_minute: z.number().optional(),
}).passthrough()

export function fetchRateLimits(): Promise<RateLimitsResponse> {
  return request<RateLimitsResponse>('/security/rate-limits', undefined, _rateLimitsSchema)
}

export function updateRateLimits(body: RateLimitsUpdateBody): Promise<RateLimitsResponse> {
  return request<RateLimitsResponse>('/security/rate-limits', {
    method: 'PUT',
    body: JSON.stringify(body),
  }, _rateLimitsSchema)
}

// Sandbox config — mode, allowed paths, SSRF controls, and the global
// agent defaults (default_profile, shell_deny_patterns).
// allow_internal is []string matching OmnipusSSRFConfig.AllowInternal in pkg/config/sandbox.go.
// Entries may be hostname, exact IP, or CIDR range. Empty slice means "block all".
// SandboxConfigResponse is a richer SPA-internal read shape. The generated SandboxConfig
// type is the wire schema; this adds SPA-specific fields.
export type SandboxConfigResponse = {
  mode?: string
  // applied_mode is the value the gateway is currently enforcing. It differs
  // from `mode` when the operator saved a change but hasn't restarted.
  applied_mode?: string
  allow_network_outbound?: boolean
  allowed_paths?: string[]
  ssrf_enabled?: boolean
  ssrf_allow_internal?: string[]
  ssrf?: {
    enabled?: boolean
    allow_internal?: string[]
  }
  // default_profile is the global fallback applied to NEW custom agents
  // that do not pick their own SandboxProfile. Empty = inherit hardcoded.
  default_profile?: SandboxProfile | ''
  // shell_deny_patterns is the global fallback shell-deny regex list
  // applied on top of any per-agent custom patterns.
  shell_deny_patterns?: string[]
  requires_restart?: boolean
}

export interface SandboxConfigUpdateBody {
  mode?: string
  allow_network_outbound?: boolean
  allowed_paths?: string[]
  ssrf_enabled?: boolean
  ssrf_allow_internal?: string[]
  ssrf?: {
    enabled?: boolean
    allow_internal?: string[]
  }
  default_profile?: SandboxProfile | ''
  shell_deny_patterns?: string[]
}

export function fetchSandboxConfig(): Promise<SandboxConfigResponse> {
  return request<SandboxConfigResponse>('/security/sandbox-config', undefined, SandboxConfigSchema)
}

export function updateSandboxConfig(body: SandboxConfigUpdateBody): Promise<SandboxConfigResponse> {
  return request<SandboxConfigResponse>('/security/sandbox-config', {
    method: 'PUT',
    body: JSON.stringify(body),
  }, SandboxConfigSchema)
}

// Session scope — controls DM conversation isolation granularity.
// SessionScopeResponse is re-exported from generated openapi-types above.

export interface SessionScopeUpdateBody {
  dm_scope: DMScope
}

// SPA-internal update response — not a wire type, no generated counterpart.
export type SessionScopeUpdateResponse = {
  saved: boolean
  requires_restart: boolean
  // applied_dm_scope reflects the value currently in effect. Since DM scope is
  // restart-gated, this is the previous value until the gateway is restarted.
  applied_dm_scope: DMScope
}

const _sessionScopeUpdateSchema = z.object({
  saved: z.boolean(),
  requires_restart: z.boolean(),
  // applied_dm_scope is typed as string here to match the generated spec;
  // the SPA casts it to DMScope after validation.
  applied_dm_scope: z.string(),
}).passthrough()

export function fetchSessionScope(): Promise<SessionScopeResponse> {
  return request<SessionScopeResponse>('/security/session-scope', undefined, SessionScopeResponseSchema as ZodType<SessionScopeResponse>)
}

export function updateSessionScope(dm_scope: DMScope): Promise<SessionScopeUpdateResponse> {
  return request<SessionScopeUpdateResponse>('/security/session-scope', {
    method: 'PUT',
    body: JSON.stringify({ dm_scope } satisfies SessionScopeUpdateBody),
  }, _sessionScopeUpdateSchema as ZodType<SessionScopeUpdateResponse>)
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

// SPA-internal retention read response — not a generated wire type.
export type RetentionResponse = {
  session_days?: number
  disabled?: boolean
}

export interface RetentionUpdateBody {
  session_days?: number
  disabled?: boolean
}

// Matches the handler at pkg/gateway/rest_retention.go's putRetention response:
// flat {saved, requires_restart, session_days, disabled}. An earlier nested
// `applied: {...}` shape never shipped — the handler always wrote flat.
// SPA-internal update response — not a generated wire type.
export type RetentionUpdateResponse = {
  saved: boolean
  requires_restart: boolean
  session_days: number
  disabled: boolean
}

const _retentionUpdateSchema = z.object({
  saved: z.boolean(),
  requires_restart: z.boolean(),
  session_days: z.number().int().gte(0),
  disabled: z.boolean(),
}).passthrough()

export function fetchRetention(): Promise<RetentionResponse> {
  return request<RetentionResponse>('/security/retention', undefined, RetentionConfigSchema)
}

export function updateRetention(body: RetentionUpdateBody): Promise<RetentionUpdateResponse> {
  return request<RetentionUpdateResponse>('/security/retention', {
    method: 'PUT',
    body: JSON.stringify(body),
  }, _retentionUpdateSchema)
}

// Retention sweep — immediately purge sessions beyond the retention window.
// RetentionSweepResult is re-exported from generated openapi-types above.
// RetentionSweepResponse is a backward-compat alias.
export type RetentionSweepResponse = RetentionSweepResult

export function triggerRetentionSweep(): Promise<RetentionSweepResponse> {
  return request<RetentionSweepResponse>('/security/retention/sweep', { method: 'POST' }, RetentionSweepResultSchema)
}

// Users — list, create, delete, reset password, change role.
export interface UserEntry {
  username: string
  role: UserRole
  has_password: boolean
  has_active_token: boolean
}

export interface CreateUserBody {
  username: string
  role: UserRole
  password: string
}

// UserCreateResponse, UserDeleteResponse, UserResetPasswordResponse, UserRoleChangeResponse
// are re-exported from generated openapi-types above.
// These backward-compat aliases allow existing callers to use the old names.
export type CreateUserResponse = UserCreateResponse
export type DeleteUserResponse = UserDeleteResponse

export interface ResetUserPasswordBody {
  password: string
}

export type ResetUserPasswordResponse = UserResetPasswordResponse

export interface UpdateUserRoleBody {
  role: UserRole
}

export type UpdateUserRoleResponse = UserRoleChangeResponse

// UserEntry is the SPA-internal type; the generated User schema is compatible (passthrough).
const _userListSchema = z.array(z.object({
  username: z.string(),
  role: z.enum(['admin', 'user']),
  has_password: z.boolean(),
  has_active_token: z.boolean(),
}).passthrough())

export function fetchUsers(): Promise<UserEntry[]> {
  return request<UserEntry[]>('/users', undefined, _userListSchema)
}

export async function createUser(body: CreateUserBody): Promise<CreateUserResponse> {
  const response = await request<CreateUserResponse & { token?: string }>('/users', {
    method: 'POST',
    body: JSON.stringify(body),
  }, UserCreateResponseSchema)
  if ('token' in response) {
    throw new Error('unexpected token in create response')
  }
  return response
}

export function deleteUser(username: string): Promise<DeleteUserResponse> {
  return request<DeleteUserResponse>(`/users/${encodeURIComponent(username)}`, { method: 'DELETE' }, UserDeleteResponseSchema)
}

export function resetUserPassword(username: string, password: string): Promise<ResetUserPasswordResponse> {
  return request<ResetUserPasswordResponse>(`/users/${encodeURIComponent(username)}/password`, {
    method: 'PUT',
    body: JSON.stringify({ password } satisfies ResetUserPasswordBody),
  }, UserResetPasswordResponseSchema)
}

export function updateUserRole(username: string, role: UserRole): Promise<UpdateUserRoleResponse> {
  return request<UpdateUserRoleResponse>(`/users/${encodeURIComponent(username)}/role`, {
    method: 'PATCH',
    body: JSON.stringify({ role } satisfies UpdateUserRoleBody),
  }, UserRoleChangeResponseSchema)
}

// ── Exec Proxy ────────────────────────────────────────────────────────────────
// ExecProxyStatus — re-exported from generated openapi-types (no local body needed).

export function fetchExecProxyStatus(): Promise<ExecProxyStatus> {
  return request<ExecProxyStatus>('/security/exec-proxy-status', undefined, ExecProxyStatusSchema)
}


// ── Agent Tools ───────────────────────────────────────────────────────────────

/**
 * Central registry tool entry (FR-027, FR-029).
 * Replaces the narrower BuiltinTool shape — includes a source discriminator
 * so the UI can badge MCP tools differently from builtin ones.
 */
export interface RegistryTool {
  name: string
  scope: 'system' | 'core' | 'general'
  category: string
  description: string
  /** Origin of the tool. 'builtin' = compiled-in Go tool; 'mcp' = MCP server tool. */
  source: 'builtin' | 'mcp'
}

/** Backward-compat alias — existing callers that reference BuiltinTool still work. */
export type BuiltinTool = RegistryTool

export interface AgentToolsCfg {
  builtin: {
    default_policy?: 'allow' | 'ask' | 'deny'
    policies?: Record<string, 'allow' | 'ask' | 'deny'>
  }
  mcp?: { servers: { id: string; tools?: string[] }[] }
}

// AgentToolEntry — re-exported from generated openapi-types (no local body needed).

/** Fetch all tools from the central registry (FR-027). Includes both builtin and MCP tools. */
export function fetchRegistryTools(): Promise<RegistryTool[]> {
  return request<RegistryTool[]>('/tools')
}

/** Backward-compat alias — callers that used fetchBuiltinTools() still work. */
export const fetchBuiltinTools = fetchRegistryTools

export function fetchMcpServersForAgent(): Promise<McpServer[]> {
  return request<McpServer[]>('/mcp-servers')
}

type AgentToolsResponse = { config: AgentToolsCfg; tools: AgentToolEntry[] }
const _agentToolsSchema = z.object({
  config: AgentToolsCfgSchema,
  tools: z.array(AgentToolEntrySchema),
}).passthrough() as ZodType<AgentToolsResponse>

export function fetchAgentTools(agentId: string): Promise<AgentToolsResponse> {
  return request<AgentToolsResponse>(`/agents/${encodeURIComponent(agentId)}/tools`, undefined, _agentToolsSchema)
}

export function updateAgentTools(agentId: string, cfg: AgentToolsCfg): Promise<AgentToolsResponse> {
  return request<AgentToolsResponse>(`/agents/${encodeURIComponent(agentId)}/tools`, {
    method: 'PUT',
    body: JSON.stringify(cfg),
  }, _agentToolsSchema)
}

/**
 * POST /api/v1/tool-approvals/{approvalId} — resolve a pending tool approval.
 * FR-011, FR-082. Throws with status code prefix on non-2xx (e.g. "403: ...").
 */
export function postToolApproval(approvalId: string, action: 'approve' | 'deny' | 'cancel'): Promise<void> {
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

export function updateGlobalToolPolicies(cfg: GlobalToolPolicies): Promise<GlobalToolPolicies> {
  return request<GlobalToolPolicies>('/security/tool-policies', {
    method: 'PUT',
    body: JSON.stringify(cfg),
  }, GlobalToolPoliciesSchema)
}

// ── Sandbox Status ────────────────────────────────────────────────────────────
// SandboxStatus — re-exported from generated openapi-types (no local body needed).
// The generated schema is a superset of the previous hand-written shape.

export function fetchSandboxStatus(): Promise<SandboxStatus> {
  return request<SandboxStatus>('/security/sandbox-status', undefined, SandboxStatusSchema)
}
