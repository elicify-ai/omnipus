// types.ts: Chat, attachment, subagent-span, rate-limit, and per-session state contracts

import type { Message, ToolCall, AgentKind } from '@/lib/api'
import type { WsReceiveFrame } from '@/lib/ws'
import type {
  GoalStatusFrame,
  LoopStatusFrame,
  AskUserQuestionCard,
  AskUserAnswerFrame,
  SubagentStateFrame,
  LLMError as GeneratedLLMError,
} from '@/lib/api/generated/asyncapi-types'
import { type LLMErrorCode } from '@/lib/llm-error'

/**
 * #823 catch-up redesign (BE-DESIGN.md §6.1) — the SPA's per-session
 * position in the gateway's per-session event log (the "hub", Lane A's
 * `pkg/gateway/ws_session_hub.go`). Paired boot id + sequence number, sent
 * back as `attach_session{since_seq, boot_id}` on reconnect instead of a
 * full-bucket wipe. See `cursor.ts::gateFrameBySeq` for the apply rule this
 * type feeds.
 */
export interface SessionCursor {
  bootId: string
  seq: number
}

export interface MediaAttachment {
  type: 'image' | 'audio' | 'video' | 'file'
  url: string
  filename: string
  contentType: string
  caption?: string
}

// FR-H-008/FR-H-009: a subagent span brackets one sub-turn.
// Discriminated union: 'running' vs terminal so TypeScript enforces that
// durationMs / finalResult / reason are only accessible on terminal spans.
//
// ADR-091 D7/D10: a child's own tool calls are never nested into this span
// any more — every frame a child session produces now carries the CHILD's
// own `session_id` (I-4), so it lands in the child's own bucket, not the
// parent's. The step-buffering mechanism that used to attach them here
// (`pendingByParentCallId`, `SpanStep`, `spanByParentCallId`) is deleted;
// the child's steps are visible only in the child's own session, opened via
// `childSessionId`.
interface SubagentSpanBase {
  spanId: string
  parentCallId: string
  taskLabel: string
  /**
   * Id of the agent running this sub-turn (the delegate), when the frame
   * carried one — used to resolve name/icon/type for display. Note:
   * pre-ADR-091, for native (non-external-CLI) delegation to a named target
   * agent, this reflected the PARENT's id due to a backend limitation in
   * the deleted pkg/agent/subturn.go — see the agent-resolution fallback in
   * useRunningActivity.ts (and that function's own ADR-091 fix lane
   * RX-SUBTURN note) which worked around this using the originating
   * delegate call's own agent_id param. Today's backend
   * (pkg/agent/steer_frames.go) sets this from the real target agent for
   * every delegation kind — the fallback's continued necessity is flagged
   * there, not re-verified here.
   */
  agentId?: string
  /**
   * ADR-091 D7/I-4: the real, store-backed child session this span's
   * sub-turn ran as (`SubagentStartFrame.child_session_id`, carried onto a
   * terminal span from `SubagentEndFrame.child_session_id` too — present
   * iff the gateway sent one). Absent on a transcript written before this
   * delivery (history-only), in which case the row renders without the open
   * control. Populated so the side panel's open control can navigate to the
   * child's own full session (`/sessions/{childSessionId}`, FR-046).
   */
  childSessionId?: string
  /**
   * ADR-091 D7/FR-E-004: the last `subagent_message.text` reduced onto this
   * span (progress/checkpoint/blocker/question/error/handback carry text;
   * `steer`/`respond` have none and are rendered as the literal string
   * 'steered' instead — see the ambiguity note on `reduceSubagentMessage` in
   * frames.ts). Truncated to 120 characters with an ellipsis (accepted
   * default, WP-E spec ambiguity table). Undefined until the child's first
   * `subagent_message` arrives.
   */
  statusLine?: string
  /**
   * ADR-091 D7/FR-E-004: the last `subagent_state.state` reduced onto this
   * span — the eight-value lifecycle domain (`queued`, `running`,
   * `needs_input`, `paused`, `completed`, `failed`, `cancelled`,
   * `timed_out`), distinct from this span's own running/terminal `status`
   * (which is driven by `subagent_start`/`subagent_end`, the PARENT's own
   * lifecycle events about the child — D7). The side panel row reads this
   * to show "queued" while the span itself is still `status: 'running'`
   * (a queued launch emits `subagent_start` immediately, before the child
   * actually starts executing — I-4).
   */
  lifecycleState?: SubagentStateFrame['state']
  /**
   * Sticky. True once a runnable `subagent_state` (`running`, `needs_input`,
   * `paused`, or `completed`) has been reduced onto this span. Never cleared
   * by a later terminal state. The last `lifecycleState` cannot say this: a
   * child that ran and then failed ends on `failed`, the same final shape as
   * one dropped from the queue before it ran (delegation chat surface D10).
   * Replay rebuilds it because every persisted state frame is reduced again.
   */
  hasRun?: boolean
  /**
   * ADR-091 D7 table's own status-line example, "last update N s ago":
   * the ISO timestamp (`created_at`) of the last `subagent_message` OR
   * `subagent_state` frame reduced onto this span (also seeded at
   * `subagent_start`/`subagent_end`). Used by the side panel as the status
   * line's fallback text — ticking relative time — while no
   * `subagent_message.text` has arrived yet (accepted default for an
   * ambiguity the WP-E spec did not fully resolve; see this delivery's
   * final report).
   */
  lastUpdateAt?: string
}

export interface SubagentSpanRunning extends SubagentSpanBase {
  status: 'running'
}

export interface SubagentSpanTerminal extends SubagentSpanBase {
  /**
   * 'parked' (ADR-057 UAT defect C2 fix): the child stopped because a
   * message_parent(kind="question", wait=true) call parked it awaiting the
   * parent's answer — not a success, error, cancellation, or timeout. See
   * SubagentEndFrame.yaml for the full contract-level description.
   */
  status: 'success' | 'error' | 'cancelled' | 'interrupted' | 'timeout' | 'parked'
  durationMs: number
  finalResult?: string
  /** Reason populated when status is 'interrupted'. */
  reason?: 'parent_timeout' | 'parent_cancelled' | 'parent_done_early' | 'unknown'
}

export type SubagentSpan = SubagentSpanRunning | SubagentSpanTerminal

// #3: ChatMessage is the SPA-internal display type. It intersects Message (the
// discriminated union) with extra display-only fields so each role variant
// still carries its role-specific status constraints. Using a type alias (not
// interface extends) because TypeScript does not allow extending a union type.
//
// ADR-087 (Truncation is an outcome, not a silence) — `truncated`/
// `truncationReason` are inherited here from `Message`'s shared
// `MessageBase` (src/lib/api.ts), matching the existing `model`/`verdict`
// pattern rather than being redeclared on this intersection. They are
// populated by `rawToMessage` (cold-load/REST — layer 3, api.ts) and by the
// `case 'replay_message'` reducer below (WS replay — layer 6) via the same
// `normalizeTruncationReason` (src/lib/truncation.ts) legacy-default rule.
// `getMessageStatusSuffix` (same module) is the single render-layer
// consumer of both fields — see its D1 precedence doc comment.
export type ChatMessage = Message & {
  isStreaming?: boolean
  /** SPA-only acknowledgement state for a user-authored message. */
  deliveryStatus?: 'queued' | 'sending' | 'received' | 'working' | 'failed'
  media?: MediaAttachment[]
  /**
   * Review finding 17 — the `media://` refs (opts.mediaRefs) this user
   * message was originally SENT with. `media` above is display-only
   * (filename/contentType/a resolved preview `url` — see sendMessage's own
   * doc comment); it does not carry the wire ref an attachment needs to be
   * re-attached to a resend. SPA-only, never serialized to the wire — kept
   * alongside `media` purely so `resendMessage` (outbound-lifecycle.ts) can
   * resend a failed message WITH its original attachments instead of
   * silently dropping them.
   */
  mediaRefs?: string[]
  spans?: SubagentSpan[]
  /** Agent that produced this message (assistant messages only). */
  agentId?: string
  /**
   * Turn-correlation id (Fix 5c; wire field ReplayMessageFrame.turn_id, sourced
   * from TranscriptEntry.TurnID), stamped on assistant messages hydrated via WS
   * replay. Lets a later `turn_canceled` replay entry find and re-mark this
   * exact message as interrupted, without relying on stream adjacency — async
   * delegation can interleave other agents'/turns' frames in between, so "last
   * assistant message" is not a safe proxy for replay correlation. Not
   * populated for messages created via live token streaming (TokenFrame
   * carries no turn_id on the wire — live cancellation instead uses
   * markLastMessageInterrupted()'s last-assistant scan) or via the REST
   * cold-load path (fetchSessionMessages), whose persisted entries already carry the
   * true status directly and need no correlation.
   */
  turnId?: string
  /**
   * Text-join seam marker (live-UAT regression fix): set on a
   * still-streaming assistant bubble whenever a top-level tool call starts
   * while it holds text (see the non-spanned branch of `case 'tool_call_start'`).
   * A tool call — including a synchronous ("await") `delegate` call whose own
   * reply rides back on the SAME bubble because it shares the delegator's
   * agent_id / omits one — is a new logical unit starting. Without this, the
   * next `token` frame's content is glued directly onto the trailing
   * narration with no space or break (e.g. "...now.Now delegating..." or
   * "...inline:ping"). Consumed (cleared) by the `case 'token'` handler,
   * which inserts a paragraph break before appending when the flag is set.
   */
  pendingTextBoundary?: boolean
  /**
   * Ids of additional replay_message transcript entries that have been
   * MERGED (appended, with a paragraph break) into this bubble's content,
   * beyond the entry that created it — see the `replay_message` reducer's
   * same-turn/same-agent coalesce branch. Needed because a merge APPENDS
   * (not idempotently overwrites) content: without recording which entry
   * ids were already folded in, a WS reconnect re-replaying the same
   * transcript window (attach_session's `since` cursor) would append the
   * same segment a second time. The primary entry's own id is already
   * covered by messageOrder membership (the existing top-level dedup
   * check); this field covers the entries folded in AFTER that one.
   */
  mergedReplayIds?: string[]
  /**
   * #823 catch-up redesign, Opus review round 3 item N1 (BE-DESIGN.md §6.5):
   * set ONCE, directly, by the `catch_up_complete` reducer's own sweep — NOT
   * recomputed reactively from `turnId !== activeTurnId` on every render.
   * The reactive comparison was the actual N1 bug: `activeTurnId` is ONLY
   * ever populated by a `session_state.active_turn` frame, which for an
   * ORDINARY live turn that never disconnected never arrives mid-turn (only
   * the very first, turn-less `session_state{}` at connection bind does) —
   * so `activeTurnId` stays `null` for the ENTIRE duration of every normal
   * live turn, making `msg.turnId !== activeTurnId` true from the first
   * token onward and showing "couldn't be finished · Generate again" under
   * every streaming answer, disconnect or not (browser-confirmed at t006 of
   * a live turn). §6.5's real rule only makes sense evaluated ONCE, right
   * after a genuine `catch_up_complete` — the one moment `session_state`'s
   * `active_turn` is authoritative for messages that predate it. This flag
   * records that one-time judgment so the render layer never has to
   * re-derive (and re-break) it.
   */
  confirmedUnfinished?: boolean
  /**
   * ADR-051 — the typed LLM error code carried on a live `ErrorFrame` /
   * `ReplayErrorFrame` payload (`payload.llm_error.code`). SPA-only display
   * field (never serialized to the wire — the bubble is rendered from the
   * translated copy in `llm-error.ts::codeToDisplay`). Used by the
   * renderers to gate the "Technical details" disclosure; the visible
   * message text comes from `message.content`, which the reducer sets to
   * the code's generic display copy.
   */
  errorCode?: LLMErrorCode
  /**
   * ADR-051 — the verbose-only `payload.llm_error.detail` string. Surfaced
   * in the "Technical details" disclosure ONLY when `verboseChatEnabled` is
   * on; otherwise the renderer must omit the disclosure entirely. Replay
   * frames carry no detail, so this is set only on live error bubbles.
   */
  errorDetail?: string
  /**
   * ADR-051 — the transcript `entry_id` the live `ErrorFrame` / the
   * `ReplayErrorFrame` was stamped with, used for live→replay dedup: when a
   * session is reloaded, the replay of the same error must NOT push a
   * second bubble if the live bubble carrying the same `errorEntryId` is
   * still in the ring buffer. `undefined` for legacy frames (no typed
   * payload) — dedup then falls back to the existing content+role dedup.
   */
  errorEntryId?: string
  /**
   * provider-messages spec (MAJ-103/DG-4) — the optional `facts` object from
   * a live `ErrorFrame`'s `payload.llm_error.facts` (provider, model,
   * request_id — nothing else). Display-only (never serialized): rendered as
   * the verbose-only facts lines inside the "Technical details" disclosure
   * (`MessageItem.tsx`); request_id is surfaced ONLY under Verbose chat.
   * Replay frames carry no facts (the `LLMErrorReplay` wire type omits the
   * field), so this is set only on live error bubbles.
   */
  errorFacts?: GeneratedLLMError['facts']
  /**
   * ADR-070 §2.4 — true when this assistant bubble was closed because a
   * mid-turn steer (a follow-up sent while it was still streaming) landed
   * after it, rather than because the reply/turn fully finished. Debugging/
   * bookkeeping only — deliberately NOT a new `AssistantMessage.status`
   * value (a union-member design was proposed and rejected; see the ADR for
   * why) and never serialized to the wire. Status stays `'done'`, identical
   * to an ordinarily-finished reply, so every existing UI/ARIA/Copy site
   * keeps its current, correct-for-`'done'` behavior unmodified. Consulted
   * only by: the C8 error-sweep (must not re-stamp it), and
   * `markLastMessageInterrupted` (must not re-stamp it as interrupted) —
   * both via `findOpenAssistantMessageId`'s eligibility check or an
   * equivalent explicit exclusion, never by a UI render branch.
   */
  closedBySteer?: boolean
  /**
   * Operator-reported UX fix (2026-09-08 — a `/goal` activation left the
   * user staring at a generic thinking indicator for 17 minutes with no
   * sign the goal had registered). Set ONLY on the synthetic `role:
   * 'system'` marker message the `case 'goal_status'` reducer inserts the
   * first time it sees an `active` frame for this goal_id — never on an
   * ordinary system banner (help text, `/new`, etc.), which carries no
   * goal_id at all. Purely a render-time discriminator (MessageItem.tsx /
   * ChatScreen.tsx's `SystemMessage`/`VirtualSystemMessageRow`) so the
   * `data-testid="goal-ack-line"` e2e hook lands on exactly this message
   * and not on every system banner. Never serialized to the wire — the
   * marker message itself is entirely SPA-synthesized, not a persisted
   * transcript entry (see the `case 'goal_status'` doc comment for the
   * durability tradeoff this implies).
   */
  goalAckGoalId?: string
  /**
   * ADR-085 BROWSER-FR-042/FR-044 (wave B8, `src/store/chat.ts` region 2 of
   * 2 — C-73). Set ONLY on the synthetic `role: 'system'` marker message
   * `case 'browser_handover_notice'` inserts via `buildBrowserHandoverInsertion`
   * — never on an ordinary system banner. Purely a render-time discriminator
   * (`ChatScreen.tsx`'s `SystemMessage`/`VirtualSystemMessageRow`, mirroring
   * `goalAckGoalId`'s identical role) so the `data-testid=
   * "browser-handover-notice"` e2e hook (C-90) lands on exactly this
   * message and nothing else. Never serialized to the wire. Unlike
   * `goalAckGoalId` (which stores the goal_id, a value the ack line's own
   * id is DERIVED from via `goalAckMessageId`), this field stores the SAME
   * string as `id` — the server-computed `message_id` (BROWSER-FR-044) IS
   * the de-dup key, there is no separate domain id to carry.
   */
  browserHandoverNoticeId?: string
}

export interface QueuedOutboundMessage {
  id: string
  content: string
  timestamp: string
}

/** Strings remain readable for persisted/test state created before #823. */
export type OutboundQueueItem = string | QueuedOutboundMessage

// Client-side truncation sentinel — parallel to server TruncatedResult/ToolResultRef shapes.
export interface ClientTruncatedResult {
  _truncated_client: true
  original_size_bytes: number
  preview: string
}

export interface RateLimitEventData {
  scope: 'agent' | 'channel' | 'global'
  resource: string
  policyRule: string
  retryAfterSeconds: number
  agentId?: string
  tool?: string
}

/** All per-session chat state for one concurrent session. */
export interface SessionChatState {
  // Map-indexed ring buffer. Use getMessages(bucket) to get the ordered array.
  messagesById: Record<string, ChatMessage>
  messageOrder: string[]
  /** Number of messages trimmed from the front of messageOrder (evicted from the ring buffer). */
  trimmedCount: number

  toolCalls: Record<string, ToolCall & { call_id: string }>
  toolCallOrder: string[]
  textAtToolCallStart: Record<string, string>
  /**
   * Per-call-id message ownership, set at tool_call_start (mirrors
   * textAtToolCallStart's per-call bookkeeping). Once a turn produces more
   * than one assistant bubble (Fix 5a; the sync/await-mode delegate
   * attribution fix), "bake every pending tool call onto whichever message
   * is LAST" is no longer correct — a call must bake onto the SPECIFIC
   * bubble it actually started on. Consulted by every bake site (the
   * 'token' case's producer-boundary bake, `done`, `clearStreamingState`)
   * via bakeToolCallsByOwner(); falls back to "the last message" for
   * unmapped calls (legacy/pre-tracking, or an owner message evicted from
   * the ring buffer) so single-bubble turns are unaffected.
   *
   * Optional (not just "possibly empty"): several existing test fixtures
   * across the codebase construct a SessionChatState-shaped bucket by hand,
   * pre-dating this field. Every read site defensively falls back to `{}`
   * and every write site initializes it first, so those fixtures keep
   * working — a required field would force touching every such fixture for
   * a purely-internal bookkeeping addition with no test-visible behavior of
   * its own (it only changes WHICH bubble a bake targets).
   */
  toolCallOwnerMessageId?: Record<string, string>
  isStreaming: boolean
  /** True from attach_session until first done frame — disables send input during replay. */
  isReplaying: boolean
  /** Set when a done frame arrives while isReplaying was true. */
  replayCompletedForSession: string | null
  /**
   * Turn ids that had a still-open (isStreaming/status 'streaming')
   * assistant bubble at the moment a session_snapshot wipe erased it. A
   * full history rebuild always reconstructs a bubble as `status: 'done'`
   * via replay_message, even when the underlying turn was genuinely cut
   * short (e.g. a gateway crash mid-stream) — losing the one signal that
   * would otherwise mark it unfinished. Carried forward across the wipe
   * so catch_up_complete's confirmedUnfinished sweep can still flag a
   * turn matching one of these ids, and cleared once that sweep runs.
   */
  wipedOpenTurnIds?: string[]
  /**
   * #823 review round 9 — after a boot_mismatch (or equivalent
   * gateway-restarted) snapshot rebuild, the transcript's LAST entry
   * was a user message with no assistant reply and no active turn: the
   * turn was killed before a single token streamed, so no assistant
   * bubble was ever created for confirmedUnfinished to attach to. Set
   * by catch_up_complete's sweep (catchup-frames.ts) to the LAST user
   * message's id; the render layer uses it to show "couldn't be
   * finished · Generate again" for that exchange specifically.
   * Deliberately narrower than "any snapshot with no active turn":
   * retention_exceeded means old history was trimmed, not that a turn
   * was interrupted, so a trimmed reply must NOT show this.
   */
  unansweredLastUserMessageId?: string | null
  /**
   * #823 review round 9 — true for exactly one catch_up_complete cycle:
   * set by session_snapshot when frame.reason === 'boot_mismatch',
   * carried through the history wipe (like wipedOpenTurnIds), and read
   * (then cleared) by catch_up_complete's sweep. Never persisted beyond
   * that one cycle — a LATER, unrelated snapshot for this session must
   * not accidentally inherit a stale true from an earlier restart.
   */
  snapshotWasBootMismatch?: boolean
  sessionTokens: number
  sessionCost: number
  rateLimitEvent: RateLimitEventData | null
  /**
   * H1-FE: Unix timestamp (ms) when the most recent user message was sent for
   * this session. Used to guard against force-clearing isStreaming on the active
   * bucket when an unknown-sid done arrives — if the active session just sent a
   * user message recently it is very likely mid-stream, not a stale spinner.
   */
  lastUserMessageAt: number | null
  /**
   * B3: current cancel stage for this session, or null when no cancel is in
   * progress. Set by cancel_stage frames from the gateway:
   *   graceful  — cancel acknowledged; agent finishing current tool call.
   *   hard      — graceful deadline expired; force-killing the agent turn.
   *   detached  — session detached from the turn; treat as idle.
   * Cleared to null when a done frame arrives.
   */
  cancelStage: 'graceful' | 'hard' | 'detached' | null
  /**
   * ADR-092: this session's resolved per-chat Auto-approve state, or null
   * when no `session_mode_updated` ack has arrived yet for this session
   * (the header badge falls back to the global x per-agent resolution in
   * that case). Set by `session_mode_updated` frames; there is no
   * "rejected" case to handle — the server always acknowledges a
   * `session_mode_update` send. Distinct from the wire's own
   * `auto_approve_effective: boolean` (never null) — the extra null state
   * here is purely "we haven't heard back yet", not a value the server
   * ever sends.
   */
  autoApproveEffective?: boolean | null
  /**
   * ISO timestamp of the most recent server frame the SPA has applied.
   * Used as the `since` cursor in attach_session to avoid replaying already-seen frames.
   */
  lastReceivedEventTime: string | null
  /**
   * O(1) index from span_id → { messageId, spanIdx } for the currently-running
   * subagent span. Added to address chat UI freeze under heavy
   * subagent/delegation activity: the subagent_end handler used to locate
   * its target span with a backward linear scan over `messageOrder × spans`,
   * which re-ran on every subagent_end frame for the whole duration of a
   * long turn. Written by subagent_start; consulted by subagent_message and
   * subagent_state (ADR-091 D7/FR-E-004, reducing the frame onto the span
   * record) while the span is still running; deleted the moment
   * subagent_end consumes it (once a span is terminal it is never looked up
   * by span_id again). ADR-091 D10 deleted its sibling index,
   * `spanByParentCallId` — the O(1) lookup by the ORIGINATING tool-call id
   * that fed the now-deleted child-step-nesting mechanism
   * (`pendingByParentCallId`); this index survives because subagent_end/
   * _message/_state all key off `span_id`, never `parent_call_id`.
   *
   * Optional for the same fixture-compat reason as `toolCallOwnerMessageId`
   * below: several existing test fixtures construct a SessionChatState-
   * shaped bucket by hand, pre-dating this field. Every read site
   * defensively falls back to `{}`/misses-and-falls-back-to-scan, and every
   * write site (`emptySessionState`, the subagent_start handler) initializes
   * or populates it.
   */
  spanBySpanId?: Record<string, { messageId: string; spanIdx: number }>
  /**
   * ADR-091 D7/I-4 (cross-family review finding 19): bounded per-session map
   * from span_id → the fields a `subagent_message`/`subagent_state` frame
   * would have reduced onto that span, captured when the frame arrives
   * BEFORE its span's own `subagent_start` — a genuine replay-gap ordering
   * (I-4 frames from concurrently running children can interleave on
   * reconnect), not a bug. Without this, that update used to be logged and
   * discarded outright, and the child's actual first status/state never
   * reached the row until (if ever) a later update arrived for the same
   * span_id.
   *
   * Consulted and cleared by the `subagent_start` handler in frames.ts the
   * moment it creates the span (`PENDING_SPAN_UPDATE_CAP` bounds growth for
   * a span_id whose `subagent_start` never arrives — an old transcript
   * missing it, edge case table). Each field is written independently
   * (never overwriting an already-pending field with `undefined`) so a
   * message-only update and a later state-only update for the SAME span_id
   * both survive to be applied together.
   */
  pendingSpanUpdatesBySpanId?: Record<string, { statusLine?: string; lifecycleState?: SubagentStateFrame['state']; lastUpdateAt?: string; hasRun?: boolean }>
  /**
   * Session-scoped record of every replay_message id that has EVER been
   * merged (via the `replay_message` same-turn/same-agent coalesce branch)
   * into ANY assistant bubble in this session — independent of which bubble
   * is currently the tail.
   *
   * Fixes a real duplicate-bubble bug (2 independent reviewers, hotfix/v0.1.1
   * 7-reviewer gate): the dedup check used to consult ONLY the current tail
   * bubble's `ChatMessage.mergedReplayIds`. That is correct at MERGE time (a
   * merge always targets the then-current tail) but wrong at DEDUP-CHECK
   * time: a WS reconnect can re-replay an already-merged entry id (call it
   * B) after a LATER turn has produced a new tail bubble (call it D) — at
   * that point `findLastAssistantMessageId` resolves to D, D's
   * `mergedReplayIds` never contained B (it was recorded on the earlier
   * bubble A), and the `sameTurn` check on the merge branch also fails (B
   * belongs to an earlier turn than D) — so B fell through and was pushed as
   * a brand-new standalone duplicate bubble. This session-level set is
   * checked instead, so "already merged" survives regardless of what has
   * become the tail since. Populated alongside (not instead of) the
   * per-bubble `ChatMessage.mergedReplayIds` field, which is kept for local
   * introspection on the bubble itself.
   *
   * Optional for the same reason as `toolCallOwnerMessageId` above: several
   * existing test fixtures construct a SessionChatState-shaped bucket by
   * hand, pre-dating this field. Every read site defensively falls back to
   * `{}` / `false` and every write site initializes it first.
   */
  mergedReplayMessageIds?: Record<string, true>
  /**
   * ADR-049 D6/US-12: latest `goal_status` frame for this session, or
   * null/undefined when no goal is active. Session-scoped — the frame
   * always carries `session_id` (`goal_status` is in
   * `SESSION_SCOPED_FRAME_TYPES`). Drives `GoalIndicator` (SD-C9): the
   * reducer here just stores whatever frame arrives verbatim (mirrors
   * `rateLimitEvent`'s "store the raw event, let the renderer decide"
   * pattern) — it never nulls this out on a `'cleared'` (or any other
   * terminal) state. CORRECTION (regression review, post-bc66345f):
   * GoalIndicator does NOT special-case `'cleared'` by hiding — it renders
   * a dedicated `cleared` branch via `describeNonActiveState` (a stale
   * comment here previously claimed otherwise). This field is a single
   * scalar (not a map), so it cannot leak the way `goalPills` below could;
   * it simply reflects the latest frame across all of the session's goals
   * until the next one arrives, with no cap/eviction needed.
   *
   * Optional for the same reason as `toolCallOwnerMessageId`/
   * `mergedReplayMessageIds` above: several existing test fixtures construct
   * a SessionChatState-shaped bucket by hand, pre-dating this field. Every
   * read site defensively falls back to `null` and every write site
   * (`emptySessionState`, the store's initial state) sets it explicitly.
   */
  goalStatus?: GoalStatusFrame | null
  /**
   * ADR-053 FE-1 / US-14: per-goal-id pill-state map. Each active goal in the
   * session gets its own entry (keyed by `GoalStatusFrame.goal_id`, falling
   * back to `'_default'` when the frame omits one), so a session carrying 2
   * goals renders 2 pills + 2 timers. The bottom-right `GoalPillTray` reads
   * this map; the legacy single `goalStatus` (above) is still maintained as a
   * derived "latest frame" for back-compat with any reader that hasn't
   * migrated yet. Optional for the same fixture-compat reason as
   * `toolCallOwnerMessageId` above — pre-existing test fixtures construct a
   * `SessionChatState`-shaped bucket by hand; every read site falls back to
   * `{}` and every write site initializes it.
   *
   * BOUNDED (regression fix, bc66345f follow-up): before bc66345f the
   * backend never emitted `goal_id`, so every frame landed on the shared
   * `'_default'` key and simply overwrote it — cardinality was permanently
   * 1. bc66345f correctly minted a stable, unique-per-generation `goal_id`
   * (required for the multi-goal tray to work at all), which incidentally
   * changed this map's cardinality to unbounded — nothing ever deleted an
   * entry, so a long session accumulated one permanent, undismissable
   * tombstone per completed/failed/cleared goal. `evictGoalPillsOverCap`
   * (below) now caps cardinality at `GOAL_PILLS_CAP`, preferring to evict
   * the oldest TERMINAL (`done`/`failed`/`cleared`) entries first — a
   * terminal `goal_id` is retired for good (a new goal, `follow_up`, or
   * Play always mints a fresh id rather than reusing one) so it can never
   * receive another frame, making it safe to drop with no risk of
   * clobbering a still-live goal. This is a memory-lifecycle bound, not a
   * rendering decision: it does not touch whether/how a pill is displayed
   * (that's `GoalPillTray`'s own short-lived display timer) — see the
   * `case 'goal_status'` handler below for the full reasoning.
   */
  goalPills?: Record<string, GoalStatusFrame>
  /** ADR-049 D6/US-12: latest `loop_status` frame for this session. Same session-scoped/store-verbatim/optional-for-fixture-compat pattern as `goalStatus`. */
  loopStatus?: LoopStatusFrame | null
  /**
   * askuserquestion-tool-spec v3 (ADR-074 D4b): the session's latest
   * AskUserQuestion card — pending (renders the tabbed question zone +
   * locks the composer) or terminal (renders the collapsed record). Fed by
   * the session-scoped `ask_user_question` frame and the `session_state`
   * reconnect snapshot (`pending_asks`). One card per routing session by
   * server contract, so a single slot suffices. Optional for the same
   * fixture-compat reason as `goalStatus` above.
   */
  pendingAsk?: AskUserQuestionCard | null
  /**
   * ADR-082 D4/D5 (FR-008/FR-009): turn id of the in-flight turn most
   * recently announced via `session_state.active_turn` for this session,
   * or null/undefined when no turn is known to be in flight. Paired with
   * `activeTurnAgentId`. Set by the `session_state` handler when the frame
   * carries `active_turn` (a reconnecting/attaching connection learning a
   * turn is already running, or a bare re-broadcast confirming one is still
   * running — see the 'session_state' case's own S2 finished-turn guard);
   * cleared the moment the turn's OWN (non-replay) terminal frame —
   * `done` OR `error`, review S1/S7 — resolves it, or by
   * `clearStreamingState`/`cancelStream`/`markLastMessageInterrupted` on a
   * hard disconnect or explicit user cancel so a dead/abandoned turn can
   * never leave this wedged. Also cleared by a session_state snapshot that
   * carries NO `active_turn` for this session (review CR3/S2). Review
   * finding S1/CR1: the `done`/`error` cases classify their OWN frame
   * shape to decide whether they are the thing that finalizes a turn — they
   * never read `activeTurnId` to make that call, only to know WHICH
   * bubble/turn to finalize once they've already decided to. Optional for
   * the same fixture-compat reason as `toolCallOwnerMessageId` above.
   */
  activeTurnId?: string | null
  /** Agent id paired with `activeTurnId` — see its doc comment. */
  activeTurnAgentId?: string | null
  /**
   * #823 catch-up redesign (BE-DESIGN.md §6.1) — this bucket's position in
   * the gateway hub's per-session event log, or `null`/unset when no
   * sequenced frame has been applied yet (every frame before Lane A's hub
   * lands, and every frame the design deliberately keeps unsequenced —
   * §1.2's "Not sequenced" list). Advanced ONLY by `cursor.ts::gateFrameBySeq`
   * (called from `handleFrame` before the per-frame switch) and by the
   * terminal `session_started`/`catch_up_complete`/`session_snapshot` frames,
   * which mint it directly from their own `seq`/`boot_id` fields. Read by the
   * WS attach path (`src/store/session.ts`, `OmnipusRuntimeProvider.tsx`) to
   * send `{since_seq, boot_id}` on reconnect instead of a full-bucket wipe.
   */
  cursor?: SessionCursor | null
  /**
   * #823 catch-up redesign (BE-DESIGN.md §4.6/§6.2) — true from the moment a
   * `session_snapshot` frame wipes this bucket's history until the matching
   * `catch_up_complete` lands. Gates `ConnectionStatus.tsx`'s "couldn't be
   * finished" derivation (§6.5): an `unfinished` verdict must never render
   * mid-catch-up, only once the server's own post-catch-up
   * `session_state.active_turn` has actually been read for this attach.
   */
  awaitingCatchUp?: boolean
}

/**
 * Client-only extension of {@link ToolCall} carrying its interleaving
 * position within the owning message. `textOffset` is the character offset
 * into the owning message's FINAL `content` string where this call started
 * — SPA-internal (camelCase deliberately, NEVER crosses the wire;
 * ChatMessage/store shapes are not-wire-format), used by renderers to
 * interleave text segments and tool calls after finalize/replay.
 *
 * Undefined when no position could be determined (e.g. a reconnect edge
 * where the tool_call_start snapshot was never recorded — see
 * `stampToolCallOffset`). Renderers must fall back to the legacy
 * bottom-grouped rendering for those calls only; an absent offset must
 * never be defaulted to 0, which would misrepresent "position unknown" as
 * "started at the very beginning of the message".
 */
export type PositionedToolCall = ToolCall & { textOffset?: number }

/**
 * Filter the span-index map (typed shape `{ messageId: string }`), returning
 * a NEW map with all entries whose `messageId` is in `evictedMessageIds`
 * removed.
 *
 * Returns the SAME reference (no allocation) when `index` is undefined so
 * callers whose `spanBySpanId` was never populated (older test fixtures)
 * stay undefined rather than being implicitly upgraded to `{}`.
 *
 * ADR-091 D10 deleted this function's sibling map, `spanByParentCallId` (the
 * child-step-nesting index) — `spanBySpanId` is now the only span index, so
 * the lockstep two-map filtering `evictSpanIndexEntries` used to do
 * collapses to filtering this one map alone; kept as its own named export
 * (rather than inlined at each call site) since three call sites
 * (`session.ts::applyMessageArray`, `messages.ts::evictMessageFromBucket`)
 * shared it before, and the unit test `removes spanBySpanId entries
 * pointing at the evicted message` locks the invariant.
 */
export function evictSpanIndexEntries<T extends { messageId: string }>(
  index: Record<string, T> | undefined,
  evictedMessageIds: Set<string>,
): Record<string, T> | undefined {
  if (index === undefined) return undefined
  const next: Record<string, T> = {}
  for (const [k, v] of Object.entries(index)) {
    if (!evictedMessageIds.has(v.messageId)) next[k] = v
  }
  return next
}

export interface ChatStore {
  /** Per-session state buckets keyed by session_id. */
  sessionsById: Record<string, SessionChatState>

  // ── Foreground selectors (derived from sessionsById[activeSessionId]) ────────
  // These are convenience getters for the UI to read the active session's state.
  // They return stable empty values when no session is active.
  //
  // NOTE: `messages` is the COMPUTED ordered array derived from the active
  // bucket's messagesById + messageOrder. It is synced after every bucket
  // mutation. Consumers should NOT access sessionsById[id].messagesById or
  // sessionsById[id].messageOrder directly — use getMessages(bucket) instead
  // (or `messagesById` immediately below, for a single-id lookup).
  messages: ChatMessage[]
  /**
   * O(1) per-id lookup companion to `messages` above — the active bucket's
   * raw messagesById map, projected straight through (not rebuilt) by
   * bucketToForeground/syncChatForeground. A component that only cares
   * about ONE message should select `s.messagesById[id]` rather than
   * `s.messages.find((m) => m.id === id)`: `messages` gets a brand-new
   * array identity on every bucket mutation (bucketToForeground rebuilds it
   * via getMessages() every time), so subscribing to it re-renders on every
   * WS frame regardless of which message changed, and `.find()` re-scans
   * the whole array every render. `messagesById[id]`, by contrast, keeps
   * the SAME object reference across mutations that don't touch that
   * specific message — see VirtualAssistantMessageRow's memo comment for
   * the Immer structural-sharing guarantee this relies on. Added to address
   * chat UI freeze under heavy subagent/delegation activity:
   * AssistantMessage and SubagentSpansRenderer both used to subscribe to
   * the whole `messages` array just to `.find()`/`.filter()` their own
   * message out of it on every frame.
   */
  messagesById: Record<string, ChatMessage>
  /**
   * The id of the most recent, still-eligible-to-be-"the reply" assistant
   * message in the active session's bucket (null when none exists), derived
   * by bucketToForeground via findOpenAssistantMessageId (ADR-070 §2.7,
   * grill-spec round 2 NEW-002 — was findLastAssistantMessageId; routed
   * through the eligibility helper so a bubble closed by a mid-turn steer
   * cannot be mistaken for the turn's genuinely-final reply between the
   * steer and the next frame reopening one). Its only consumer today is
   * ChatScreen's ARIA "New response from {agent}" live-region announcement,
   * which must fire once per completed turn, not once per steered segment —
   * see the field's original doc reasoning below for why it exists as a
   * dedicated derived field at all. Companion to `messagesById` for any
   * consumer that previously did `[...messages].reverse().find((m) => m.role === 'assistant')`
   * — the ARIA live-region hook in ChatScreen used to do exactly that on
   * every WS frame: it subscribed to the whole `messages` array (which
   * gets a brand-new array identity on every bucket mutation) and then
   * allocated a reversed copy + linear scan per render. Selecting this
   * single id instead skips both the per-frame re-render (the id only
   * changes when the LAST assistant message itself changes — e.g. a new
   * turn starts — not on every frame of the turn) and the array work.
   * The full message object (for status reads) is then looked up via
   * `messagesById[id]` — the same O(1)-lookup idiom AssistantMessage and
   * SubagentSpansRenderer use.
   */
  lastAssistantMessageId: string | null
  isStreaming: boolean
  isReplaying: boolean
  replayCompletedForSession: string | null
  wipedOpenTurnIds?: string[]
  toolCalls: Record<string, ToolCall & { call_id: string }>
  toolCallOrder: string[]
  textAtToolCallStart: Record<string, string>
  sessionTokens: number
  sessionCost: number
  rateLimitEvent: RateLimitEventData | null
  /** ADR-049 D6/US-12: active session's latest `goal_status` frame, or null/undefined. Drives `GoalIndicator`. Optional — see SessionChatState.goalStatus's doc comment (fixture-compat). */
  goalStatus?: GoalStatusFrame | null
  /**
   * ADR-053 FE-1 / US-14: active session's per-goal-id pill-state map.
   * Drives `GoalPillTray` (bottom-right, one pill per goal-id). Each entry
   * is the latest `goal_status` frame for that goal-id. Empty object when
   * no goals are active. Optional — see SessionChatState.goalPills (fixture-compat).
   */
  goalPills?: Record<string, GoalStatusFrame>
  /** ADR-049 D6/US-12: active session's latest `loop_status` frame, or null/undefined. */
  loopStatus?: LoopStatusFrame | null
  /** askuserquestion-tool-spec v3: the active session's AskUserQuestion card (pending → question zone + composer lock; terminal → collapsed record). See SessionChatState.pendingAsk. */
  pendingAsk?: AskUserQuestionCard | null
  lastUserMessageAt: number | null
  /** B3: cancel progress stage for the active session, or null when idle. */
  cancelStage: 'graceful' | 'hard' | 'detached' | null
  /** ADR-092: active session's resolved per-chat Auto-approve state. See SessionChatState.autoApproveEffective. */
  autoApproveEffective?: boolean | null
  /** ISO timestamp of the most recent server frame for the active session. */
  lastReceivedEventTime: string | null
  /**
   * Phase 1 / FR-008/009/010: per-thread model override for the next
   * outgoing message. The composer model selector writes the picker's
   * value here; the AssistantUI runtime reads it in `onNew` and threads
   * it into `sendMessage` as `model_name` (forwarded to the server as
   * `metadata.model_name` in the WS frame). Cleared after each send
   * so the next session reopen re-derives the default from transcript
   * history or the agent's `model` config (per spec §18 Q3).
   */
  nextModel: string | null
  /** Set the next-turn model override (called by the composer). */
  setNextModel: (model: string | null) => void

  // ── Actions that operate on the foreground session ───────────────────────────
  setReplaying: (value: boolean) => void
  setMessages: (messages: Message[]) => void
  /**
   * Backfills any `type: judge_verdict` entries from a REST-fetched
   * transcript (src/lib/api.ts's `rawToMessage`) into `sessionId`'s bucket,
   * WITHOUT touching anything else the WS live/replay path already
   * populated. ADR-049 D2/D4/SD-C10 — see the doc comment on this action's
   * implementation for the full rationale.
   *
   * Live-thread-card fix (2026-09-14): the live/replayed `judge_verdict` WS
   * frame now ALSO inserts a thread message directly, for scope=task/
   * scope=goal (which now carry `session_id` — `case 'judge_verdict'`,
   * `src/lib/judgeVerdictThread.ts`), keyed by the SAME id this REST path
   * produces (the persisted entry's own id) — so this backfill is now only
   * reached for: a scope this module doesn't cover (scope=plan has no
   * session_id and stays panel-only), or a session whose WS replay never
   * ran (e.g. a cold REST-only load with no live connection). Idempotent
   * (skips any id already present) so calling it on every `historyData`
   * resolution, and after the live/replay path already inserted the same
   * card, is always safe.
   */
  mergeJudgeVerdictHistory: (sessionId: string, historyMessages: Message[]) => void
  appendMessage: (message: ChatMessage) => void
  updateLastAssistantMessage: (content: string, done?: boolean) => void
  /**
   * Marks the target session's last assistant message as interrupted.
   * `sessionId` optionally scopes this to a specific (possibly background)
   * session — e.g. the browser panel's "Take over" pausing its own pinned
   * session. Omitted (the default) targets the active session, matching
   * every pre-existing call site (Stop button, Escape, `/cancel`).
   */
  markLastMessageInterrupted: (sessionId?: string) => void

  startToolCall: (callId: string, tool: string, params: Record<string, unknown>) => void
  resolveToolCall: (callId: string, result: unknown, status: 'success' | 'error', durationMs?: number, error?: string) => void
  cancelToolCall: (callId: string) => void

  updateSessionStats: (tokens: number, cost: number) => void
  /**
   * Seed sessionTokens from the persisted total_tokens on a historic session
   * attach. Only sets the value when the bucket is fresh (sessionTokens === 0)
   * so live deltas from the `done` frame are never double-counted.
   */
  seedSessionTokens: (total: number) => void
  setRateLimitEvent: (event: RateLimitEventData) => void
  clearRateLimitEvent: () => void

  // Resets only the foreground session bucket. Does NOT affect other sessions.
  resetSession: () => void

  // Wipes a specific session bucket and marks it as replaying so the next
  // replay frames rebuild from scratch. Used on WS reconnect to prevent
  // duplicate bubbles when the gateway re-replays the transcript.
  resetSessionForReplay: (sessionId: string) => void

  // ── Outbound queue (Fix 3) ────────────────────────────────────────────────────
  // Messages typed while the WS is disconnected are buffered here (max 5).
  // drainOutboundQueue() is called by OmnipusRuntimeProvider on reconnect.
  /** Pending outbound messages queued while WS was disconnected. Max 5. */
  outboundQueue: OutboundQueueItem[]
  /** Queue a message for when the WS reconnects. Returns false if queue is full. */
  enqueueOutboundMessage: (content: string, queuedMessage?: QueuedOutboundMessage) => boolean
  /** Send all queued messages now that the WS is connected. */
  drainOutboundQueue: () => void
  /**
   * BUG FIX (2026-07): messages handed off from `outboundQueue` by
   * `drainOutboundQueue()`, sent ONE AT A TIME. `sendMessage` only allows one
   * in-flight turn (`isStreaming`) — the previous implementation looped over
   * the whole queue synchronously, so every message after the first hit the
   * `isStreaming` guard and was silently dropped (no chat bubble, no error).
   * `drainOutboundQueue` now moves the queue here and sends only the head;
   * `maybeDrainNext()` (module-private, called from every place a turn ends —
   * the `done`/`error` frame handlers, `cancelStream`, `clearStreamingState`,
   * `markLastMessageInterrupted`, and the failed-send rollback in
   * `sendMessage` itself) pops and sends the next item once `isStreaming`
   * clears. Exposed on the store (not a closured helper) so tests can invoke
   * it directly if needed.
   */
  pendingDrainQueue: OutboundQueueItem[]

  // ── Actions ───────────────────────────────────────────────────────────────────
  // opts.mediaRefs: optional media:// refs (e.g. uploaded images) threaded
  //   into the outbound message frame so the agent sees the attachment (#254).
  // opts.attachments: optional MediaAttachment[] rendered inline on the
  //   optimistic user bubble (display only — not sent on the wire).
  // opts.model_name: optional model slug for THIS turn only (Phase 1, FR-010).
  //   The composer model selector writes the picker's value here; sendMessage
  //   forwards it as `metadata.model_name` in the WS message frame. The
  //   server honors it when present and falls back to the agent's `model`
  //   config when absent.
  sendMessage: (content: string, opts?: { mediaRefs?: string[]; attachments?: MediaAttachment[]; model_name?: string; clientMessageId?: string; queuedAt?: string }) => void
  /**
   * Review finding 17 — "Try again" on a failed user message used to call
   * `sendMessage(content)` with a brand-new id, which left the FAILED
   * bubble in place and appended a SECOND, duplicate bubble — and, since
   * only `content` (a plain string) was threaded through, silently dropped
   * any attachments the original message carried. `resendMessage` instead
   * looks the existing message up BY ID, resends its exact original content
   * AND `mediaRefs` under the SAME client_message_id, and flips its
   * deliveryStatus back to 'sending' IN PLACE — no second bubble, no
   * dropped attachments. A no-op if messageId does not resolve to an
   * existing, resend-eligible (role:'user') message.
   */
  resendMessage: (messageId: string) => void
  /** Validate an outbound MessageFrame against the generated Zod schema. Logs and dev-toasts on failure but never blocks the send. `sessionId` (the sending session, or the pending-bucket key when no session exists yet) is threaded through into the production telemetry record for operator correlation. */
  _validateOutboundFrame: (payload: unknown, sessionId?: string | null) => void
  /**
   * Auto-triggers Ava's (or whichever agent leads the workspace's
   * core_team) workspace-setup interview the first time a freshly-created
   * workspace (server-seeded `setup_pending: true`) is opened with no
   * conversation yet. Reuses `sendMessage`'s no-active-session ("new turn")
   * wire mechanics — pending `'__pending'` placeholder bucket,
   * `_validateOutboundFrame`, `isStreaming`, rollback-on-send-failure — MINUS
   * any user-visible bubble: the backend records this turn's kickoff frame
   * as a SYSTEM-role transcript entry (centered pill on replay), not a user
   * message, so rendering an optimistic user bubble here would show text the
   * user never typed. No `session_id` is sent — the server mints one and
   * acks with `session_started`, same as any other first message.
   *
   * Sets the active agent (mirrors `AgentPicker`'s auto-select:
   * `setActiveSession` with the resolved agent's id+type) so the
   * composer/thread header show the agent immediately, without depending on
   * `AgentPicker`'s own effect having already run.
   *
   * Bails silently (returns `false`, no toast) when offline, mid-stream, or
   * a conversation already exists (`activeSessionId !== null`) — this must
   * never fire over an existing chat. Returns `true` once the frame is
   * handed to `connection.send` successfully. The caller (
   * `useWorkspaceSetupKickoff`) uses the return value to decide whether to
   * optimistically clear `setup_pending` in the workspaces query cache.
   */
  sendWorkspaceSetupKickoff: (opts: {
    workspaceId: string
    workspaceName: string
    agentId: string
    agentType: AgentKind | null
  }) => boolean
  /**
   * Kickoff pending-session hardening: the workspace id of the
   * ONE in-flight `sendWorkspaceSetupKickoff` call, or `null` when no
   * kickoff is outstanding. Set the moment a kickoff frame is handed to
   * `connection.send` successfully; cleared on whichever terminal outcome
   * resolves it first:
   *   - `session_started` — the kickoff succeeded (see that frame handler,
   *     which also uses the recorded workspaceId to decide whether the ack
   *     is still for the workspace the user is currently on, AND whether
   *     the '__pending' placeholder is still the local foreground turn at
   *     all — the plausibility gate).
   *   - a rejecting, UNTAGGED `error` frame (a kickoff turn never
   *     gets a real session_id, so any tagged error can never be a kickoff
   *     reject) — regardless of whether `activeSessionId` is still
   *     `'__pending'` at the time (the reject-after-navigation
   *     fall-through covers the case where the user already moved on
   *     locally).
   *   - a hard WS disconnect — `clearStreamingState` tears this down
   *     via `abandonPendingKickoff` the instant the connection drops, since
   *     no ack can ever arrive on a dead connection.
   *   - a failed `connection.send()` inside `sendWorkspaceSetupKickoff`
   *     itself (the full-rollback branch).
   *
   * Also doubles as a single-slot in-flight guard: `sendWorkspaceSetupKickoff`
   * bails while this is non-null, since a second kickoff would collide with
   * the first on the shared `'__pending'` bucket key before either resolves.
   * `sendMessage`'s own no-active-session branch respects the same guard
   * — it enqueues instead of building a second
   * `'__pending'` bucket while this is non-null.
   */
  pendingKickoff: { workspaceId: string } | null
  /**
   * Honest retry: per-workspace kickoff-attempt guard, keyed by
   * workspace id. This is `useWorkspaceSetupKickoff`'s OWN send-time "have I
   * already fired for this workspace" bookkeeping — moved out of a
   * component-local `useRef` (which, once a kickoff terminally failed, had
   * NO path back to "retry": failure just left the ref permanently claimed)
   * and into the store so it can also be corrected by chat.ts's own async
   * WS-frame handlers (`session_started` / `error` / a hard disconnect),
   * which are the only code that ever learns of an ASYNC kickoff outcome —
   * the hook itself only ever observes the synchronous accept/reject of the
   * initial `connection.send()` call.
   *   'in-flight' — a kickoff was sent for this workspace and hasn't
   *                 resolved yet; blocks a second concurrent fire for the
   *                 SAME workspace id.
   *   'failed'    — the kickoff terminally failed (server reject, a hard
   *                 disconnect, or a synchronous send failure). Does NOT
   *                 block a future fire — the hook's own effect reacts to
   *                 this transition by invalidating the workspaces query so
   *                 server truth (which restores `setup_pending:true` on a
   *                 genuine failure, per the kickoff contract — a DUPLICATE
   *                 rejection is the exception: the flag is already
   *                 correctly `false` from the earlier, actually-successful
   *                 attempt) replaces the hook's own premature optimistic
   *                 `false` write. The next natural re-render (once the
   *                 invalidated query refetches a fresh `Workspace` object)
   *                 decides honestly whether to retry via the ordinary
   *                 `setup_pending` check — the DUPLICATE case naturally
   *                 stops there since the refetch reports `false`.
   * No entry means "never attempted, or already resolved 'done'" — free to
   * fire.
   */
  kickoffAttemptStatus: Record<string, 'in-flight' | 'failed'>
  /**
   * Claim the per-workspace kickoff-attempt slot. Called by
   * `useWorkspaceSetupKickoff` immediately BEFORE invoking
   * `sendWorkspaceSetupKickoff`, mirroring the old ref's "claim before send"
   * ordering — a synchronous re-render triggered by the send itself must
   * not re-enter the hook's effect body and fire a second frame.
   */
  markKickoffInFlight: (workspaceId: string) => void
  /**
   * Resolve a workspace's kickoff-attempt slot. `'done'` deletes the
   * entry (success, or any other case that no longer needs tracking);
   * `'failed'` marks it so `useWorkspaceSetupKickoff`'s invalidate-on-failure
   * effect fires.
   */
  resolveKickoffAttempt: (workspaceId: string, outcome: 'done' | 'failed') => void
  /**
   * Kickoff pending-session hardening: full, quiet teardown of an
   * outstanding workspace-setup kickoff — clears `pendingKickoff`, drops the
   * orphaned `'__pending'` bucket (if present), and marks the workspace's
   * `kickoffAttemptStatus` as `'failed'`. No-op when no kickoff is
   * outstanding. Used by `clearStreamingState` (a hard disconnect means no
   * ack for any in-flight kickoff can ever arrive on the dead connection)
   * and by `OmnipusRuntimeProvider.reattachActiveSession` (a reconnect
   * mid-kickoff must not send a protocol-violating `attach_session
   * {session_id: '__pending'}`). Deliberately does NOT touch
   * `activeSessionId` or show a toast — those are the caller's own,
   * context-specific responsibility (a disconnect vs. a reconnect want
   * different visible behavior).
   */
  abandonPendingKickoff: () => void
  /**
   * Cancels the target session's in-flight turn (sends the `cancel` WS frame
   * and marks its last assistant message interrupted). `sessionId` optionally
   * scopes this to a specific session — e.g. the browser panel's "Take over"
   * pausing the RIGHT (pinned) session instead of whatever chat is
   * foreground. Omitted (the default) targets the active session, matching
   * every pre-existing call site (Stop button, Escape, `/cancel`).
   */
  cancelStream: (sessionId?: string) => void
  respondToPairing: (deviceId: string, decision: 'approve' | 'reject') => void
  /**
   * askuserquestion-tool-spec v3 §3: submit (or cancel) the pending
   * AskUserQuestion card over the WS (`ask_user_answer`). Validation is the
   * server's (first-valid-wins); a dropped connection surfaces a connection
   * error and leaves the card pending for the reconnect snapshot to
   * re-hydrate.
   */
  sendAskUserAnswer: (answer: Omit<AskUserAnswerFrame, 'type'>) => void
  /**
   * ADR-092: set or clear this session's per-chat Auto-approve modifier
   * (`session_mode_update`). A human-only action — nothing agent-facing
   * calls this. `true` turns Auto ON for this chat even when the resolved
   * agent x global default has it off (the one deliberate loosening
   * exception in this contract); `false` turns it off; `null` clears the
   * modifier and reverts to the inherited agent/global resolution. Always
   * acknowledged by a `session_mode_updated` frame — no rejection case.
   */
  sendSessionModeUpdate: (sessionId: string, autoApprove: boolean | null) => void
  /**
   * ADR-092 UX fix: a per-chat Auto-approve choice made in the composer
   * BEFORE this chat has a real, server-known session (a brand-new chat —
   * `session_mode_update` needs a session id the server already knows
   * about, which does not exist until the first message is sent). Storing
   * the choice here lets `AutoApprovePicker` stay usable and show the
   * choice immediately instead of greying out.
   *
   * Consumed by `sendMessage`'s no-active-session branch
   * (`src/store/chat/slices/outbound-lifecycle.ts`): sent as
   * `MessageFrame.auto_approve` on the very message that mints the session,
   * so the server records it (`SessionModeStore`) before the turn is
   * dispatched to the agent loop — the very first turn already runs under
   * the chosen mode, with no follow-up round trip needed. `session_started`
   * (the frame slice's own case) then reflects the same value into the new
   * session's bucket and clears this field. Also cleared by
   * `startNewSession`/`attachToSession` (`src/store/session.ts`, via
   * `registerChatClearPendingAutoApprove`) so a choice made and then
   * abandoned (no message ever sent) does not leak onto a LATER, unrelated
   * new chat.
   */
  pendingAutoApproveChoice: boolean | null
  /** Sets or clears `pendingAutoApproveChoice`. See its doc comment. */
  setPendingAutoApproveChoice: (choice: boolean | null) => void

  // C8: defensively clear in-flight/streaming state for every session bucket.
  // Called when the stream is terminated by something OTHER than a clean done
  // frame (WS close/disconnect, a terminal error frame, an error event). Without
  // this, a turn whose terminal frame is missed would leave isStreaming=true
  // forever — the composer stays disabled and the "thinking" spinner never
  // resolves (the "stuck chat stream" wedge). Marks any still-streaming
  // assistant message as 'done' so AssistantUI stops rendering it as running.
  clearStreamingState: () => void

  handleFrame: (frame: WsReceiveFrame) => void
}

export const finishedTurnIdsBySession: Record<string, string[]> = {}
