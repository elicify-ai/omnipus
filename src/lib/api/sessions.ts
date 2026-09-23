// sessions.ts: Sessions, the message union, wire adapters and the session tree

import { maybeDevToast } from '../dev-toast'
import { normalizeTruncationReason, type TruncationReason } from '../truncation'
import type { ZodType } from 'zod'
import { z } from 'zod'
import {
  ClearAllSessionsResponse as ClearAllSessionsResponseSchema,
  // Wire-shape schemas used for raw-to-SPA transform validation:
  Message as WireMessageSchema,
  Session as WireSessionSchema,
  // ADR-057 FR-091/FR-098 (U10, W16e): GET /sessions now returns one named
  // SessionPage envelope ({sessions, next_cursor?, partial_errors?}) instead
  // of the retired two-variant oneOf (bare array | {sessions, partial_errors}).
  SessionPage as WireSessionPageSchema,
  OperationResult as OperationResultSchema,
} from '@/lib/api/generated/schemas'
import type {
  GoalOutcome as WireGoalOutcome,
  Attachment,
  ClearAllSessionsResponse,
  OperationResult,
  JudgeVerdict,
} from '@/lib/api/generated/openapi-types'
import { _recordApiSchemaError, request } from './http'

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
  /**
   * Goal outcome line (founder decision 2026-09-14): set on a `role: 'system'`
   * message that records how a goal ENDED — from the persisted
   * `system_subtype: goal_outcome` transcript entry on a cold REST load
   * (`rawToMessage`), or from the live/replayed `goal_outcome` WS frame
   * (store/chat.ts → `buildGoalOutcomeInsertion`, src/lib/goalOutcome.ts).
   * Renderers show `GoalOutcomeRow` for it, regardless of Verbose chat.
   */
  goalOutcome?: WireGoalOutcome
  /**
   * ADR-087 D2 — set on the last assistant entry of an incomplete turn.
   * Only populated for assistant messages (only role the backend ever
   * stamps this on — `MarkLastEntryTruncated` writes the last assistant
   * transcript entry). Placed on the shared base (rather than only
   * `AssistantMessage`) matching the existing `model`/`verdict` pattern, so
   * the discriminated `Message` union stays trivially narrowable without a
   * role guard at every read site.
   */
  truncated?: boolean
  /**
   * ADR-087 D2/D1 — narrows why `truncated` is true. Drives the muted
   * footer suffix (`getMessageStatusSuffix`, src/lib/truncation.ts):
   * `'cancelled'` renders `(interrupted)`, `'max_output_tokens'` renders
   * `(cut off at the output limit)`. Already legacy-defaulted to
   * `'cancelled'` (ADR-087 D2) by the callers that set this field —
   * `rawToMessage` (cold-load) and the WS replay reducer
   * (`store/chat.ts`'s `case 'replay_message'`) — via
   * `normalizeTruncationReason`.
   */
  truncationReason?: TruncationReason
  /**
   * Turn-correlation id (wire `Message.turn_id`, stamped by the backend on
   * every real assistant entry). Forwarded by rawToMessage so
   * chat.ts's `mergeJudgeVerdictHistory` can anchor a cold-loaded
   * `judge_verdict` entry's thread position to the judged turn's assistant
   * message — the one stable per-turn correlator shared by the REST
   * transcript and the WS-replay path (replay frames carry `turn_id` but
   * no timestamp, so timestamps cannot order a replay-populated bucket;
   * see that action's doc comment).
   */
  turnId?: string
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
  /**
   * Finding 4 (SQUAD-BRIEF-AY) — true when this call's `status` was flipped
   * from 'running' to 'cancelled' by clearStreamingState() (a hard WS
   * disconnect) specifically, never by an explicit user cancel or a real
   * server-reported cancellation. Debugging/bookkeeping only, SPA-internal.
   * The one thing it drives: when the assistant bubble owning this call is
   * reopened by a reconnect catch-up token (frames.ts's 'token' case), a
   * call still carrying this flag is restored to 'running' and moved back
   * into the live tool-call tracking instead of staying stuck 'cancelled' —
   * the disconnect, not the tool, is what stopped it, and the turn (and
   * therefore the tool call) may still be genuinely in flight server-side.
   */
  cancelledByDisconnect?: boolean
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
  type?: 'message' | 'compaction' | 'system' | 'judge_verdict'
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
  /**
   * ADR-087 D2 — set on the last assistant entry when it is incomplete. See
   * `truncation_reason` for why. Forwarded to AssistantMessage/ChatMessage
   * so a cold-loaded transcript renders the same cut-off notice a live or
   * replayed turn would (rawToMessage below applies the legacy-default rule
   * via `normalizeTruncationReason`).
   */
  truncated?: boolean
  /**
   * ADR-087 D2 — narrows why `truncated` is true. Absent on a
   * `truncated: true` entry means `'cancelled'` (every entry written before
   * this field existed predates it and was always a cancel).
   */
  truncation_reason?: TruncationReason
  /**
   * Goal outcome line — present on a `type: system, system_subtype:
   * goal_outcome` entry (contracts/components/schemas/GoalOutcome.yaml).
   * Forwarded by rawToMessage so a reloaded thread shows how a goal ended.
   */
  goal_outcome?: WireGoalOutcome
  /**
   * Judge verdict payload — present on a `type: judge_verdict, role: system`
   * entry (contracts/components/schemas/JudgeVerdict.yaml, wire
   * `Message.verdict`). Forwarded by rawToMessage so a cold-loaded (REST)
   * transcript can render `JudgeVerdictThreadCard`, gated by
   * `shouldRenderJudgeVerdictInThread` (toolVisibility.ts) — ADR-049
   * D2/D4/SD-C10. See rawToMessage's own comment on this field for why REST
   * is currently the only carrier that can populate the card.
   */
  verdict?: JudgeVerdict
  /**
   * Turn-correlation id (wire `Message.turn_id`) — see MessageBase.turnId.
   */
  turn_id?: string
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
      ...(raw.goal_outcome ? { goalOutcome: raw.goal_outcome } : {}),
      // ADR-049 D2/D4/SD-C10: a persisted `type: judge_verdict` entry cold-
      // loaded via REST must carry `type`/`verdict` through so ChatScreen's
      // `msg.type === 'judge_verdict'` branch can render
      // JudgeVerdictThreadCard. Before this fix `raw.type`/`raw.verdict`
      // were silently dropped here, so the card never appeared even when
      // Verbose chat was on. Live-thread-card fix (2026-09-14): the
      // live/replayed `judge_verdict` WS frame (store/chat.ts's `case
      // 'judge_verdict'`) now ALSO carries `session_id` for scope=task/
      // scope=goal (JudgeVerdictFrame.yaml) and inserts the same card
      // directly (src/lib/judgeVerdictThread.ts), keyed by this entry's own
      // id — so this REST path is no longer the only carrier; it remains
      // the fallback for scope=plan (no session_id) and for a cold-only
      // load with no live WS connection.
      ...(raw.type === 'judge_verdict' && raw.verdict ? { type: 'judge_verdict' as const, verdict: raw.verdict } : {}),
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
  // ADR-087 D2 — cold-load (REST) truncation plumbing, layer 3 of the SPA's
  // six-layer path (§7.2). `normalizeTruncationReason` applies the legacy
  // default (absent reason on a truncated entry means 'cancelled') so the
  // cold-load and WS-replay paths (store/chat.ts's `case 'replay_message'`)
  // derive the same value from the same rule.
  const truncationReason = normalizeTruncationReason(raw.truncated, raw.truncation_reason)
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
    ...(raw.turn_id ? { turnId: raw.turn_id } : {}),
    ...(modelField ? { model: modelField } : {}),
    ...(raw.truncated ? { truncated: true as const, truncationReason } : {}),
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

/**
 * Create a session for an agent, optionally inside a workspace.
 *
 * `workspaceId` is the workspace the chat this session backs belongs to. Pass
 * it whenever the caller knows one — omit it only for the global/inbox chat,
 * which genuinely belongs to no workspace.
 *
 * It matters beyond bookkeeping: the live browser panel decides which
 * workspace's browser, and whose live logins, it shows by reading the
 * workspace off the attaching chat session's own meta on the server (ADR-075
 * FR-016/FR-017 — no workspace travels on the attach frame itself). A session
 * created without one leaves that read empty, and an agent on more than one
 * workspace's team is then refused as ambiguous. Sending it here means a chat
 * carries its workspace from birth instead of from its first message.
 */
export async function createSession(agentId: string, workspaceId?: string): Promise<Session> {
  const trimmedWorkspaceId = typeof workspaceId === 'string' ? workspaceId.trim() : ''
  // Wire returns the wire Session shape (nested stats); transform to SPA Session.
  const raw = await request<RawSession>('/sessions', {
    method: 'POST',
    body: JSON.stringify({
      agent_id: agentId,
      // Omitted entirely when absent: the contract caps it at 128 chars and
      // rejects a workspace that does not exist, and "" is not a workspace.
      ...(trimmedWorkspaceId.length > 0 && trimmedWorkspaceId.length <= 128
        ? { workspace_id: trimmedWorkspaceId }
        : {}),
    }),
  }, WireSessionSchema as ZodType<RawSession>)
  return rawToSession(raw)
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
