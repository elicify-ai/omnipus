// goals.ts: Goal-pill bounds and synthetic goal/browser insertion builders

import type {
  GoalStatusFrame,
  BrowserHandoverNoticeFrame,
} from '@/lib/api/generated/asyncapi-types'
import { GOAL_TERMINAL_STATES } from './frames'
import type { ChatMessage, SessionChatState } from './types'

/**
 * Hard cap on `goalPills`' cardinality. Before bc66345f every `goal_status`
 * frame landed on the shared `'_default'` key (the backend never emitted
 * `goal_id`), so this map's size was permanently 1. bc66345f correctly gave
 * every goal a stable, unique-per-generation `goal_id` — required for the
 * multi-goal pill tray to disambiguate goals at all — which incidentally
 * changed the map's cardinality from 1 to UNBOUNDED: nothing ever deleted
 * an entry, so a long-lived session accumulated one permanent,
 * undismissable tombstone per completed/failed/cleared goal.
 *
 * 20 is comfortably above the backend's own global active-loop cap
 * (`GoalStatusFrame.cap`, default 16 — the ceiling on simultaneously
 * non-terminal goals+plans+loops), so under normal operation this never
 * evicts a still-live (non-terminal) entry; it only ever trims accumulated
 * terminal history once a session has run through more than 20 goals.
 */
const GOAL_PILLS_CAP = 20

/**
 * Bounds `goalPills` at `GOAL_PILLS_CAP`, called on every `goal_status`
 * write (see `case 'goal_status'` below). This is a memory-lifecycle
 * concern, NOT a rendering decision — it does not decide whether/how a
 * pill is displayed (that stays `GoalPillTray`'s job, consistent with this
 * file's "components decide whether/how to render each state" design). It
 * decides only whether an entry is safe to garbage-collect, which is a
 * question about the DATA's lifecycle (can this goal_id ever change again?)
 * rather than the UI's (should this currently be shown?) — the two are
 * orthogonal, and this function answers only the first.
 *
 * A terminal `goal_id` is retired for good (ADR-053: goal_id is unique per
 * generation; a new goal, `follow_up`, or Play always mints a fresh id
 * rather than reusing a terminated one), so it can never receive another
 * frame — evicting the OLDEST terminal entries first (object key order —
 * plain-string keys preserve insertion order) is therefore safe and can
 * never clobber a still-live goal. Only if the map is still over cap after
 * every terminal entry is gone (i.e. more non-terminal goals are
 * simultaneously live than the cap allows, far beyond the backend's own
 * default active-loop cap of 16) does this fall back to age-based eviction
 * of the oldest entries regardless of state, so the bound holds
 * unconditionally per requirement (b) — "regardless of" what the tray does.
 */
export function evictGoalPillsOverCap(pills: Record<string, GoalStatusFrame>): Record<string, GoalStatusFrame> {
  const keys = Object.keys(pills)
  let overBy = keys.length - GOAL_PILLS_CAP
  if (overBy <= 0) return pills
  const next = { ...pills }
  for (const key of keys) {
    if (overBy <= 0) break
    if (GOAL_TERMINAL_STATES.has(next[key].state)) {
      delete next[key]
      overBy--
    }
  }
  if (overBy > 0) {
    for (const key of Object.keys(next)) {
      if (overBy <= 0) break
      delete next[key]
      overBy--
    }
  }
  return next
}

/**
 * Field-preserving merge for `goalPills[pillKey]` — ADR-088 code-review
 * round 1, Finding 1 (HIGH): the goal record card was only transiently
 * visible. Root cause: the engine's ROUTINE `goal_status` emissions
 * (end-of-turn pushes from the goal loop) carry NO `criteria`/`dod`/
 * `definition` — only the `set_goal` post-write emission populates the
 * record. Wholesale-replacing the stored pill on every frame (the previous
 * behavior) meant the very next routine frame after registration clobbered
 * the record-carrying pill — the pre-ADR-082 thread-tail card component
 * (retired; read `goalPills` as its ONLY source) lost its
 * `criteria.length>0` filter and unmounted seconds after appearing, until
 * the next `set_goal` write re-populated it. ADR-082 D9 moved the record
 * card's primary source to each `set_goal` call's OWN result (SetGoalToolUI/
 * SetGoalCardBlock) — `goalPills` is now the progress OVERLAY only (state/
 * round/per-criterion status by goal_id) — but this merge rule still
 * matters: the overlay reads the SAME map, and a criteria-less routine push
 * clobbering the last known criteria/dod here would still corrupt what
 * per-criterion status gets matched onto the card by text.
 *
 * Rule (see the case 'goal_status' comment for why): a frame that DOES
 * carry `criteria` always wins wholesale (it IS a fresh record — either the
 * initial author or a `set_goal(mode: update)` steering revision). A
 * terminal/cleared frame (`done`/`failed`/`cleared`) also always wins
 * wholesale — record display ends with the goal regardless of what was
 * stored. Otherwise (incoming carries no criteria AND is non-terminal —
 * i.e. a routine `active`/`judging`/`waiting_on_user`/... progress push)
 * carry the stored `criteria`/`dod`/`definition` forward while taking every
 * other field (state/round/reason/accounting) from the incoming frame — the
 * incoming frame is still the source of truth for everything EXCEPT the
 * record fields it didn't populate.
 */
export function mergeGoalPillFrame(
  stored: GoalStatusFrame | undefined,
  incoming: GoalStatusFrame,
): GoalStatusFrame {
  const incomingHasCriteria = (incoming.criteria?.length ?? 0) > 0
  if (incomingHasCriteria || GOAL_TERMINAL_STATES.has(incoming.state)) {
    return incoming
  }
  const storedHasCriteria = (stored?.criteria?.length ?? 0) > 0
  if (!stored || !storedHasCriteria) {
    return incoming
  }
  return {
    ...incoming,
    criteria: stored.criteria,
    dod: stored.dod,
    definition: stored.definition,
  }
}

// ── Goal acknowledgement line (operator-reported UX fix, 2026-09-08) ──────
//
// Repro: `/goal <text>` activates INSTANTLY (ADR-088 D1, zero LLM calls
// before the working agent's first request), but the user saw nothing
// confirming that — just the generic rotating thinking indicator — for as
// long as 17 minutes in the reported case, while a tool call had actually
// failed off-screen. Fix: render one quiet line, "Goal set. Working out
// what done looks like.", at the goal's chronological position the FIRST
// time the store observes an `active` goal_status frame for a given
// goal_id — driven entirely by that real server-pushed frame, never an
// optimistic client-side guess (see the case 'goal_status' handler below).
//
// Durability tradeoff (flagged per the wave brief rather than faked): this
// line is a client-synthesized `ChatMessage`, NOT a persisted transcript
// entry — pkg/agent/goal_loop.go (which owns the instant-activation call
// site) is out of this wave's scope, so there is no backend anchor writing
// it into the session's JSONL the way the goal record card itself is
// anchored (pkg/agent/goal_record_wiring.go's anchorGoalRecordInTranscript).
// It still survives an ordinary page reload: EmitGoalStatusRehydrate
// (pkg/agent/goal_record_wiring.go, extended by this same wave) now
// re-emits the SAME `active` frame on every WS re-attach — including a
// full page reload's fresh attach — for as long as the goal stays active,
// whether or not its record has been written yet (previously it only
// covered the record-populated case). Since insertion below is idempotent
// (keyed by a deterministic `goal-ack-<goal_id>` message id), that rehydrate
// re-arrival reconstructs this exact line after a reload rather than a
// second one appearing. What it does NOT survive: a session whose local
// message history is cleared/never-loaded before any reattach happens for
// this goal (there is no transcript entry to replay it from at all in that
// case) — the cheapest durable alternative, if that gap ever matters in
// practice, is a small addition to goal_loop.go's activateInstantGoal (out
// of scope here) that anchors a plain (non-tool-call) transcript entry the
// same way the record card is anchored, so a cold REST/replay load also
// reconstructs it with no live frame required.
const GOAL_ACK_LINE_TEXT = 'Goal set. Working out what done looks like.'

/** Deterministic id for one goal's ack-line marker message — doubles as the
 * de-dup key (a message already present at this id means the line has
 * already been shown for this goal_id, live or rehydrated) so the handler
 * below never inserts a second one. */
function goalAckMessageId(goalId: string): string {
  return `goal-ack-${goalId}`
}

/**
 * Finds the message id the goal-ack line should render immediately after —
 * the most recent user message that actually issued this goal (so the line
 * lands at "the goal's chronological position in the thread", not just
 * tacked onto whatever is currently last). Two matching strategies, tried
 * in order: (1) a user message containing the frame's own `condition` text
 * verbatim — this is what the persisted transcript carries, since the
 * gateway records the RAW inbound `/goal <condition>` message before any
 * command-rewrite touches it (mirrors pkg/agent/loop.go's scheduled-run
 * comment "mirroring the interactive websocket path"); (2) defensively, the
 * most recent user message that starts with the `/goal` command literal,
 * in case the condition text was normalized/trimmed differently than the
 * frame's copy. Returns null when neither matches (falls back to appending
 * at the current tail — correct for the live case, where nothing has
 * happened after the command yet).
 */
function findGoalCommandAnchorId(
  order: readonly string[],
  byId: Record<string, ChatMessage>,
  condition: string,
): string | null {
  const trimmedCondition = condition.trim()
  for (let i = order.length - 1; i >= 0; i--) {
    const m = byId[order[i]]
    if (!m || m.role !== 'user') continue
    const content = m.content ?? ''
    if (trimmedCondition && content.includes(trimmedCondition)) return m.id
    if (/^\s*\/goal\b/i.test(content)) return m.id
  }
  return null
}

/**
 * Builds the {messagesById, messageOrder} patch that inserts the goal-ack
 * marker for `goalFrame` into bucket `b`, or returns null when no insertion
 * is needed (no goal_id on the frame — a legacy/compat emission — the
 * frame's state is not `active`, or the marker for this goal_id already
 * exists). Split out of the `case 'goal_status'` handler so the insertion
 * logic has a single, independently-reasoned-about home.
 */
export function buildGoalAckInsertion(
  b: SessionChatState,
  goalFrame: GoalStatusFrame,
): Pick<SessionChatState, 'messagesById' | 'messageOrder'> | null {
  if (goalFrame.state !== 'active' || !goalFrame.goal_id) return null
  const ackId = goalAckMessageId(goalFrame.goal_id)
  if (b.messagesById[ackId]) return null // already shown for this goal — idempotent, never duplicate.

  const ackMessage: ChatMessage = {
    id: ackId,
    role: 'system',
    status: 'done',
    content: GOAL_ACK_LINE_TEXT,
    timestamp: new Date().toISOString(),
    goalAckGoalId: goalFrame.goal_id,
  }
  const messagesById = { ...b.messagesById, [ackId]: ackMessage }
  const anchorId = findGoalCommandAnchorId(b.messageOrder, b.messagesById, goalFrame.condition)
  const messageOrder = anchorId
    ? (() => {
        const idx = b.messageOrder.indexOf(anchorId)
        return [...b.messageOrder.slice(0, idx + 1), ackId, ...b.messageOrder.slice(idx + 1)]
      })()
    : [...b.messageOrder, ackId]
  return { messagesById, messageOrder }
}

/**
 * Builds the {messagesById, messageOrder} patch that inserts the ADR-085
 * browser-handover waiting notice for `frame` into bucket `b`, or returns
 * null when a message with this id already exists in the bucket — the
 * idempotency BROWSER-FR-044 requires. The server derives `message_id`
 * deterministically from `(session_id, holdStartedAtUnixNano)` — a
 * wall-clock nanosecond timestamp minted once at the transition INTO a
 * stood-down state and reused verbatim for every emission within that one
 * unbroken hold (never a per-process counter, which would collide across a
 * gateway restart — C-84) — so a duplicate arrival (a WS re-attach
 * re-delivering the same live frame, or the FR-043a replay of the same
 * persisted transcript entry landing on top of an already-live line) must
 * never insert a second line, while a genuinely NEW hold (a fresh
 * `holdStartedAtUnixNano`) always gets its own.
 *
 * Deliberately simpler than `buildGoalAckInsertion` in one respect: this
 * notice always appends at the current tail rather than being anchored to
 * an earlier message in the thread — a handover has no "condition" text
 * (goal's `/goal <condition>` command) to search the history for, and
 * BROWSER-FR-041/042 only ever describe it as delivered live into the open
 * thread, never backdated to some earlier point in it. Split out of `case
 * 'browser_handover_notice'` for the same reason `buildGoalAckInsertion`
 * is split out of `case 'goal_status'`: the insertion logic gets a single,
 * independently-reasoned-about home. Per C-73, this function and its call
 * site are B8's entire region of this file — it reads no goal symbol.
 */
export function buildBrowserHandoverInsertion(
  b: SessionChatState,
  frame: BrowserHandoverNoticeFrame,
): Pick<SessionChatState, 'messagesById' | 'messageOrder'> | null {
  if (b.messagesById[frame.message_id]) return null // already shown for this hold — idempotent, never duplicate.

  const noticeMessage: ChatMessage = {
    id: frame.message_id,
    role: 'system',
    status: 'done',
    content: frame.text,
    timestamp: new Date().toISOString(),
    browserHandoverNoticeId: frame.message_id,
  }
  return {
    messagesById: { ...b.messagesById, [frame.message_id]: noticeMessage },
    messageOrder: [...b.messageOrder, frame.message_id],
  }
}
