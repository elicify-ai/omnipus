// frames.ts: inbound websocket frame routing and reduction


import type { StoreApi } from 'zustand'
import { produce } from 'immer'
import { generateId } from '@/lib/constants'
import { useUiStore } from '@/store/ui'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { queryClient } from '@/lib/queryClient'
import { tasksQueryKeys } from '@/lib/api'
import type { Agent } from '@/lib/api'
import type {
  WsSubagentStartFrame,
  WsSubagentEndFrame,
} from '@/lib/ws'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { logDiagnostic } from '@/lib/telemetry'
import { normalizeTruncationReason } from '@/lib/truncation'
import {
  getLLMErrorDisplay,
  readEntryIdFromFrame,
  readLLMErrorFromFrame,
  sanitizeLegacyErrorMessage,
} from '@/lib/llm-error'
import { advanceEventTime, clampToolResult, findLastAssistantMessageId, findOpenAssistantMessageId, getMessages } from '../messages'
import { markTurnFinished, scheduleLibraryChangedInvalidate } from '../routing'
import { CANCEL_ACK_FRAME_TYPES, EMPTY_BUCKET, REPLAY_ERROR_BASE_DELAY_MS, REPLAY_ERROR_MAX_DELAY_MS, SESSION_SCOPED_FRAME_TYPES, UNKNOWN_FRAME_TOAST_THRESHOLD, inFlightReattachSids, pendingCancelAckSids, replayErrorRetryAttempts, replayErrorRetryTimers, replayingClearTimers, replayingStartedAt, sawReplayMessageThisTurn } from '../runtime-state'
import { applyMessageArray, bakeToolCallsByOwner, emptySessionState, isToolCallBakedInBucket } from '../session'
import { gateFrameBySeq, cursorFromTerminalFrame, insertHistoryMessageId, CURSOR_MINTING_FRAME_TYPES, type SeqFrameLike } from '../cursor'
import type { ChatMessage, ChatStore, RateLimitEventData, SessionChatState, SubagentSpan, SubagentSpanRunning, SubagentSpanTerminal } from '../types'
import { handleReplayAndStatusFrame } from './replay-and-status-frames'
import { handleCatchUpFrame } from './catchup-frames'
import { handleProviderFrame } from './provider-frames'



type FrameSlice = Pick<ChatStore, 'handleFrame'>
type ToolCallResultFrame = Extract<Parameters<ChatStore['handleFrame']>[0], { type: 'tool_call_result' }>
type MessageStatusFrame = Extract<Parameters<ChatStore['handleFrame']>[0], { type: 'message_status' }>
type TokenFrameType = Extract<Parameters<ChatStore['handleFrame']>[0], { type: 'token' }>

// Both helpers below are kept at module scope (not inlined in handleFrame)
// so their bodies don't count against handleFrame's/createFrameSlice's
// grandfathered line budgets (scripts/budgets/functions.txt) — that function
// was already at its recorded ceiling before #823, so any net-new line
// inside it fails `make lint-budgets`; extracting is the prescribed fix
// ("Grandfathered entries may only shrink — do not add to one, extract
// first.", CLAUDE.md "Size budgets").

// #823 state A: received/working/failed map onto the user message's own
// deliveryStatus.
function applyMessageStatusFrame(bucket: SessionChatState, frame: MessageStatusFrame): Partial<SessionChatState> {
  return produce(bucket, (draft) => {
    const message = draft.messagesById[frame.client_message_id]
    if (!message || message.role !== 'user') return
    message.deliveryStatus = frame.state === 'failed' ? 'failed' : frame.state
    message.status = frame.state === 'failed' ? 'error' : 'done'
  }) as Partial<SessionChatState>
}

// I1: advance the reconnect `since` cursor from whatever frame carries a
// `timestamp` field. The cursor is sent as `since` on attach_session so the
// gateway skips transcript entries the SPA already saw.
//
// What this actually covers today — the generic `frame.timestamp` read
// below reads broadly, but the set of frames that can satisfy it is small
// and worth stating plainly rather than leaving as "any frame":
//   - `replay_error` is the ONLY frame the gateway currently sends with a
//     populated timestamp (pkg/gateway/replay.go, `buildReplayErrorFrame`
//     is the single `Timestamp:` assignment in the replay path).
//   - `replay_message` declares an OPTIONAL `timestamp` in the contract
//     (contracts/components/schemas/ReplayMessageFrame.yaml) but neither
//     gateway construction site populates it, so in production it never
//     advances the cursor. The reducer still honours it if that changes.
//   - `token` / `done` / `session_state` have no `timestamp` field at all
//     and never advance the cursor.
//
// So the cursor moves rarely and lags the true high-water mark. That is
// conservative in the SAFE direction: too-old a `since` costs a duplicate
// replay the dedup paths absorb, whereas too-new would silently skip
// messages. Do not "fix" the lag by advancing on a frame whose timestamp
// is not a transcript-entry time — the server compares `since` against
// TranscriptEntry.Timestamp, so only those values are meaningful here.
//
// advanceEventTime is monotonic (only moves forward) and compares
// chronologically, not lexicographically — see its doc comment for why the
// difference matters on RFC3339Nano's variable-width fractional seconds.
// Being monotonic, it is safe to run before the per-frame reducer
// regardless of dedup/early-return paths.
function advanceReceivedEventTime(
  frame: unknown,
  targetSid: string | null,
  withBucket: FrameContext['withBucket'],
): void {
  const frameTimestamp = (frame as { timestamp?: string }).timestamp
  if (frameTimestamp && targetSid) {
    withBucket(targetSid, (b) => ({
      lastReceivedEventTime: advanceEventTime(b.lastReceivedEventTime, frameTimestamp),
    }))
  }
}

// #823 catch-up redesign (BE-DESIGN.md §6.2): the SPA-side apply-rule gate,
// run once per frame ahead of the whole per-type switch below (kept out of
// handleFrame's own body — see the comment on advanceReceivedEventTime just
// above for why: handleFrame is already at its grandfathered line-budget
// ceiling, scripts/budgets/functions.txt).
//
// Returns 'apply' when the frame should proceed to the normal reducers
// (either it carries no `seq` at all — the overwhelming majority of frames
// today, since Lane A's gateway hub had not shipped `seq` on any real
// connection as of this write — or it is exactly the next expected number).
// Returns 'drop-or-gap' when the frame must NOT reach the switch: either it
// is a duplicate/already-applied (`kind: 'drop'`) or it is a genuine gap
// (`kind: 'gap'`), in which case this function ALSO fires the re-attach the
// design requires (§6.2's gap row: "send attach_session{S, cursor}") using
// whatever cursor the bucket already has — the server's own §3.3 rule
// decides whether that cursor is still servable.
function applySeqGate(
  frame: unknown,
  targetSid: string | null,
  get: FrameContext['get'],
  withBucket: FrameContext['withBucket'],
): 'apply' | 'drop-or-gap' {
  const f = frame as SeqFrameLike & { type?: string }
  if (f.seq === undefined || f.seq === null || !targetSid) return 'apply'
  if (f.type && CURSOR_MINTING_FRAME_TYPES.has(f.type)) {
    // The cursor is about to be re-minted from this frame — any re-attach
    // that was in flight for this session is now resolved (Opus review
    // round 2 item 7).
    inFlightReattachSids.delete(targetSid)
    return 'apply'
  }
  const bucket = get().sessionsById[targetSid]
  const decision = gateFrameBySeq(bucket?.cursor ?? null, f)
  if (decision.kind === 'apply') {
    withBucket(targetSid, () => ({ cursor: decision.cursor }))
    inFlightReattachSids.delete(targetSid)
    return 'apply'
  }
  if (decision.kind === 'gap') {
    // #823 catch-up redesign, Opus review round 2 item 7 (LOW): without this
    // guard, every gapped frame that arrives before the server responds to
    // the FIRST attach_session — a burst of tokens, for instance — sent
    // another one, once per frame. Only the first frame of a gap actually
    // triggers a re-attach; the rest are silently dropped as before
    // (still correct — they're still gapped — just without re-sending).
    if (!inFlightReattachSids.has(targetSid)) {
      inFlightReattachSids.add(targetSid)
      console.warn('[chat] sequence gap — re-attaching', { sessionId: targetSid, have: decision.cursor.seq, got: f.seq })
      logDiagnostic('chatSeqGapReattach', { sessionId: targetSid, have: decision.cursor.seq, got: f.seq })
      useConnectionStore.getState().connection?.send({
        type: 'attach_session',
        session_id: targetSid,
        since_seq: decision.cursor.seq,
        boot_id: decision.cursor.bootId,
      })
    }
  }
  return 'drop-or-gap'
}

// ADR-091 D7/D10 + #823 Opus review round 2 item 1 ("update moved tools by
// call_id"): applies a tool_call_result to this session's own flat tool
// call. The live entry can be gone not because this result is genuinely
// unmatched, but because §6.3's disconnect handling (clearStreamingState)
// already BAKED it into its owning message's tool_calls array — a
// disconnect no longer cancels a running tool call, so the real result can
// still legitimately arrive afterward (catch-up, or a late live frame).
// Before falling back to "unmatched" (which renders a scary standalone
// error notice for a tool that's actually fine), check every message's own
// baked tool_calls for this call_id and update it in place. Extracted from
// handleFrame to keep it under its grandfathered line budget
// (scripts/budgets/functions.txt) — no behaviour change.
function applyToolCallResultFrame(
  b: SessionChatState,
  frame: ToolCallResultFrame,
  clampedResult: ReturnType<typeof clampToolResult>,
): Partial<SessionChatState> {
  if (!b.toolCalls[frame.call_id]) {
    for (const id of b.messageOrder) {
      const msg = b.messagesById[id]
      const idx = msg?.tool_calls?.findIndex((tc) => tc.id === frame.call_id) ?? -1
      if (idx === -1) continue
      return produce(b, (draft) => {
        const tc = draft.messagesById[id].tool_calls![idx]
        tc.result = clampedResult
        tc.status = frame.status ?? 'success'
        tc.duration_ms = frame.duration_ms
        tc.error = frame.error
      }) as Partial<SessionChatState>
    }
    return appendUnmatchedToolError(b, frame, clampedResult)
  }
  return produce(b, (draft) => {
    const tc = draft.toolCalls[frame.call_id]
    tc.result = clampedResult
    tc.status = frame.status ?? 'success'
    tc.duration_ms = frame.duration_ms
    tc.error = frame.error
  }) as Partial<SessionChatState>
}

// #823/ADR-091 merge review (Opus F4, low, plausible race): the Go side's
// deliverSubagentStart persists the span to the session transcript BEFORE
// emitting the live frame — not one atomic step. If a second tab's
// attach_session binds in that exact window, its snapshot replay emits this
// SAME span (reconstructed from the transcript, unsequenced) and the live
// copy (sequenced, seq above whatever the bucket's cursor already had) also
// arrives — one delegation, two subagent_start frames for the same span_id.
// The subagent_start case ignores a frame whose span_id this reports as
// already present: it never creates a second span and never touches the
// existing one's state (a subagent_message/subagent_state may have updated
// it since the first copy was applied).
function bucketHasSpan(b: SessionChatState, spanId: string): boolean {
  const entry = b.spanBySpanId?.[spanId]
  const span = entry ? b.messagesById[entry.messageId]?.spans?.[entry.spanIdx] : undefined
  return !!span && span.spanId === spanId
}

function appendUnmatchedToolError(
  bucket: SessionChatState,
  frame: ToolCallResultFrame,
  result: unknown,
): Partial<SessionChatState> {
  if (frame.status !== 'error') {
    console.debug('[chat] resolveToolCall for unknown call_id', frame.call_id)
    return {}
  }
  console.warn('[chat] unmatched tool error rendered as standalone notice', { callId: frame.call_id, tool: frame.tool })
  logDiagnostic('chatUnmatchedToolError', { callId: frame.call_id, tool: frame.tool })
  return produce(bucket, (draft) => {
    const notice: ChatMessage = {
      id: generateId(),
      role: 'assistant',
      content: '',
      timestamp: new Date().toISOString(),
      status: 'done',
      isStreaming: false,
      agentId: frame.agent_id,
      tool_calls: [{
        id: frame.call_id,
        tool: frame.tool,
        params: {},
        result,
        status: 'error',
        duration_ms: frame.duration_ms,
        error: frame.error,
      }],
    }
    draft.messagesById[notice.id] = notice
    draft.messageOrder.push(notice.id)
  }) as Partial<SessionChatState>
}

// ADR-091 D7/I-4 (cross-family review finding 19): bounds
// SessionChatState.pendingSpanUpdatesBySpanId growth for a span_id whose
// subagent_start never arrives (e.g. an old transcript missing it — edge
// case table). Small, matching GOAL_PILLS_CAP's role for the same shape of
// "bounded pending map" problem (goals.ts::evictGoalPillsOverCap) — there is
// no realistic scenario with dozens of concurrently in-flight replay gaps.
const PENDING_SPAN_UPDATE_CAP = 50

type PendingSpanUpdate = NonNullable<SessionChatState['pendingSpanUpdatesBySpanId']>[string]

/**
 * Merge a subagent_message/subagent_state update into span_id's pending
 * slot (created on first miss) when the span itself can't be found — the
 * frame arrived before its span's own subagent_start. Each field in `patch`
 * is applied independently: `patch` must omit a key entirely rather than
 * set it to `undefined`, or a later-arriving update of the OTHER frame type
 * would clobber an earlier one's field (e.g. a pending statusLine from
 * subagent_message wiped by a subsequent subagent_state's patch that never
 * meant to touch it). Evicts the oldest entry (insertion order) once over
 * PENDING_SPAN_UPDATE_CAP.
 */
function recordPendingSpanUpdate(
  existing: SessionChatState['pendingSpanUpdatesBySpanId'],
  spanId: string,
  patch: PendingSpanUpdate,
): NonNullable<SessionChatState['pendingSpanUpdatesBySpanId']> {
  const next = { ...(existing ?? {}) }
  next[spanId] = { ...next[spanId], ...patch }
  const keys = Object.keys(next)
  if (keys.length > PENDING_SPAN_UPDATE_CAP) {
    delete next[keys[0]]
  }
  return next
}

/**
 * Lifecycle values that mean the child left the queue. `queued` does not.
 * Names are SubagentStateFrame.state (running / needs_input / paused / completed).
 * Kept at module scope so handleFrame's grandfathered line budget does not grow.
 */
const RAN_LIFECYCLE_STATES: ReadonlySet<string> = new Set(['running', 'needs_input', 'paused', 'completed'])

/** Once true, stays true. A later failed / cancelled / timed_out must not clear it. */
function stickyHasRun(previous: boolean | undefined, state: string): true | undefined {
  if (previous === true || RAN_LIFECYCLE_STATES.has(state)) return true
  return undefined
}

function spanWithLifecycle(
  span: SubagentSpan,
  state: NonNullable<SubagentSpan['lifecycleState']>,
  createdAt: string,
): SubagentSpan {
  const hasRun = stickyHasRun(span.hasRun, state)
  if (hasRun) return { ...span, lifecycleState: state, lastUpdateAt: createdAt, hasRun }
  return { ...span, lifecycleState: state, lastUpdateAt: createdAt }
}

/** Omit hasRun when unset so a merge cannot clobber a sticky true with undefined. */
function pendingLifecyclePatch(
  previous: PendingSpanUpdate | undefined,
  state: NonNullable<SubagentSpan['lifecycleState']>,
  createdAt: string,
): PendingSpanUpdate {
  const hasRun = stickyHasRun(previous?.hasRun, state)
  if (hasRun) return { lifecycleState: state, lastUpdateAt: createdAt, hasRun }
  return { lifecycleState: state, lastUpdateAt: createdAt }
}

// #823 catch-up redesign, Opus review round 2 (BE-DESIGN.md §6.3, "Turn-keyed
// bubbles — replaces 'append to the last assistant bubble'"): the #822
// mechanism this block used to implement
// (isTerminalCatchUpToken/applyTokenStreamingState/needsTerminalCatchUp,
// keyed on terminalCatchUpPending/activeTurnBubbleOpened) is deleted.
// catch_up_complete (slices/catchup-frames.ts) is now the sole "catch-up is
// over" signal — see that file's own doc comment — so a token/done frame
// never needs to guess whether it is closing a replay window.
//
// CORRECTION: an earlier pass of this function keyed bubbles by message_id
// alone, producing a SEPARATE bubble per message_id — which made F1's own
// two-round example (msg-1 "Let me check." + tool call, then msg-2
// "All done.") render as two bubbles. That is wrong: §6.3 and §8.3 both
// require EXACTLY ONE bubble per turn, and F1's fixture "assistant_messages"
// are the turn's successive text SEGMENTS within that one bubble, not
// separate bubbles ("one_bubble_per_message" in the fixture means "the whole
// reply is one bubble," not "one bubble per message_id" — confirmed against
// the design doc's literal §6.3 rule below). §6.3's real rule:
//   1. If a bubble already holds this message_id (its own id, OR previously
//      registered via the turn-merge in step 2 below) — append/replace/
//      ignore-if-complete, exactly as before.
//   2. Otherwise, if there is an OPEN (not closed by this turn_id's own
//      `done`) bubble for the SAME turn_id + agent_id, register this
//      message_id onto THAT bubble (mirrors the identical mechanism
//      replay_message already uses for its own same-turn merge — see
//      `ChatMessage.mergedReplayIds` / `SessionChatState.
//      mergedReplayMessageIds`'s doc comments) and append/replace there.
//   3. Otherwise create a new bubble, anchored on turn_id.
function findBubbleIdForMessageId(draft: SessionChatState, messageId: string): string | null {
  if (draft.messagesById[messageId]) return messageId
  for (const id of draft.messageOrder) {
    const m = draft.messagesById[id]
    if (m?.role === 'assistant' && m.mergedReplayIds?.includes(messageId)) return id
  }
  return null
}

// Opus review round 3 item N4 (LOW-MEDIUM): a failed session rebuild — the
// gateway sends `error` + `done{stats.replay_error:true}` and unbinds,
// instead of ever reaching catch_up_complete. Re-attach WITHOUT
// since_seq/boot_id (see replayErrorRetryAttempts' own doc comment in
// runtime-state.ts for why a cursor is untrustworthy here), with
// exponential backoff so a gateway that keeps failing this same rebuild
// isn't hammered in a tight loop. Extracted purely to keep handleFrame
// under its line budget (scripts/budgets/functions.txt) — no behavior
// change from the inline version it replaces.
function scheduleReplayErrorRetry(sid: string): void {
  const attempt = (replayErrorRetryAttempts[sid] ?? 0) + 1
  replayErrorRetryAttempts[sid] = attempt
  if (replayErrorRetryTimers[sid]) clearTimeout(replayErrorRetryTimers[sid])
  const delay = Math.min(REPLAY_ERROR_BASE_DELAY_MS * 2 ** (attempt - 1), REPLAY_ERROR_MAX_DELAY_MS)
  replayErrorRetryTimers[sid] = setTimeout(() => {
    delete replayErrorRetryTimers[sid]
    useConnectionStore.getState().connection?.send({ type: 'attach_session', session_id: sid })
  }, delay)
}

function applyTokenContentTo(draft: SessionChatState, bubbleId: string, frame: TokenFrameType): void {
  const m = draft.messagesById[bubbleId]
  if (frame.replace) {
    m.content = frame.content
  } else {
    // Opus review round 3 item N3 (MEDIUM-LOW): the #823 rewrite of the
    // token case dropped the paragraph-break insertion `pendingTextBoundary`
    // exists for (see ChatMessage.pendingTextBoundary's own doc comment,
    // which still describes it: "the next token append ... gets a paragraph
    // break instead of gluing on with no separator"). Without it, the text
    // AFTER a tool call glues directly onto the text before it with no
    // separator — "Let me check.All done." live, vs a real break after a
    // reload replays the same content through applyMessageArray's own
    // (unaffected) formatting. Insert the break, then consume the flag —
    // mirrors the pre-#823 behavior exactly.
    const boundary = m.pendingTextBoundary && m.content ? '\n\n' : ''
    m.content = (m.content ?? '') + boundary + frame.content
    m.pendingTextBoundary = false
  }
  if (frame.agent_id) m.agentId = frame.agent_id
  if (frame.turn_id && !m.turnId) m.turnId = frame.turn_id
  m.isStreaming = true
  m.status = 'streaming'
  draft.isStreaming = true
}

function resolveTokenBubbleByMessageId(draft: SessionChatState, frame: TokenFrameType): void {
  const messageId = frame.message_id!
  const turnId = frame.turn_id

  // Step 1: this message_id is already accounted for on some bubble
  // (live-registered as that bubble's own id, or merged onto an earlier
  // bubble of the same turn by step 2 on a previous token).
  //
  // Opus review round 6, R-J (HIGH, DO-NOT-SHIP, real-browser regression):
  // a bare message_id match here is not enough — if the bubble it names
  // belongs to a DIFFERENT, genuinely finished turn (message_id is not
  // guaranteed turn-unique; confirmed by direct reproduction: turn B
  // reusing turn A's message_id merged 120 tokens of two separate answers
  // into ONE bubble), treating it as "the same answer continuing" is
  // exactly wrong — a whole second turn's answer got appended into the
  // first turn's already-finished bubble instead of opening its own. A
  // message_id match only counts when BOTH frames carry no turn_id, or
  // their turn_ids agree; otherwise this is a cross-turn id collision and
  // falls through to steps 2/3 as if the message_id were unregistered.
  const rawResolvedId = findBubbleIdForMessageId(draft, messageId)
  const resolvedId =
    rawResolvedId && turnId && draft.messagesById[rawResolvedId].turnId && draft.messagesById[rawResolvedId].turnId !== turnId
      ? null
      : rawResolvedId
  if (resolvedId) {
    const existing = draft.messagesById[resolvedId]
    if (existing.status === 'interrupted' || existing.status === 'error') return
    if (existing.status === 'done' && !existing.isStreaming) {
      // Opus review round 3 item N2 (MEDIUM-HIGH, DO-NOT-SHIP,
      // browser-confirmed: sending a message mid-answer dropped the rest of
      // the CURRENT step — the agent doesn't reach a step boundary the
      // instant a steer message is sent, so the SAME turn/message_id keeps
      // streaming tokens for it afterward): a bubble closed only by
      // `closedBySteer` (outbound-lifecycle.ts's sendMessage mid-turn
      // branch, ADR-070 §2.1) for the SAME turn_id is not actually
      // finished — it was closed early so the user's new message could sit
      // after it in the thread, not because the step itself ended. Reopen
      // it and keep appending instead of discarding these tokens.
      if (existing.closedBySteer && existing.turnId === frame.turn_id) {
        existing.closedBySteer = false
      } else if (existing.turnId && existing.turnId === draft.activeTurnId) {
        // Opus review round 3 item N5 (LOW-MEDIUM): a replay_message-
        // reconstructed bubble is always created with status:'done' (it's
        // reconstructing HISTORY — replay-and-status-frames.ts's own
        // newMsg literal), even when it belongs to a turn session_state
        // (which always precedes replay frames in a real attach, §4.1 A6)
        // has just confirmed is STILL RUNNING. Design §6.3: "For the active
        // turn from session_state.active_turn, the replayed bubble stays
        // eligible to receive tokens." Without this, a full rebuild mid-way
        // through a multi-step turn split the continuation into a second
        // bubble. Distinct from F4's persisted-between-bind-and-read case
        // (BE-DESIGN.md §4.2/§6.3, kept below): there session_state carries
        // NO active_turn for the already-finished turn, so activeTurnId is
        // null and this branch correctly does not apply.
      } else {
        // §4.2/§6.3 overlap rule: a bubble already finalized (status 'done',
        // not streaming) ignores any further token for its message_id
        // outright — this is what makes a snapshot's persisted-between-
        // bind-and-read race safe (F4's own scenario): the replayed history
        // already shows the full text, and live tokens with seq > W for
        // the SAME message_id that arrive afterward must never re-open or
        // duplicate it.
        return
      }
    }
    applyTokenContentTo(draft, resolvedId, frame)
    return
  }

  // Step 2: no bubble holds this message_id yet. Look for an OPEN bubble
  // (not finalized by this turn's own `done`) for the SAME turn_id and
  // agent_id — this is F1's shape (msg-1's tool-call round, then msg-2's
  // continuation): register the new message_id onto that SAME bubble
  // instead of starting a second one.
  if (turnId) {
    // A user message (the sender's own steer, or its server echo) ends the
    // current turn SEGMENT (ADR-070 round 2 fix): once one has been placed,
    // an EARLIER assistant bubble of this same turn_id is round 1's, not a
    // candidate for round 2's new message_id — round 2 gets its own bubble,
    // placed after that user message. Only a candidate at or after the last
    // user message may still be merged into.
    let lastUserIdx = -1
    for (let i = draft.messageOrder.length - 1; i >= 0; i--) {
      if (draft.messagesById[draft.messageOrder[i]]?.role === 'user') {
        lastUserIdx = i
        break
      }
    }
    let turnBubbleId: string | null = null
    for (let i = draft.messageOrder.length - 1; i >= 0; i--) {
      if (i < lastUserIdx) break // crossed the segment boundary — stop looking
      const id = draft.messageOrder[i]
      const m = draft.messagesById[id]
      if (m?.role !== 'assistant') continue
      // Opus review round 7 follow-up (real wire capture): a turn that
      // opens with a tool call and NO preamble text. tool_call_start /
      // tool_call_result carry neither turn_id nor message_id (the model
      // hasn't named the turn yet), so the bubble they open has turnId
      // undefined. The first token then arrives WITH turn_id/message_id —
      // it must ADOPT that trailing, not-yet-turn-stamped bubble of the
      // current segment (stamping it now that the turn is known) rather
      // than opening a second one. An unstamped bubble can only belong to
      // the CURRENT segment (Step 2's own lastUserIdx scan above already
      // stopped looking past the last user message), so matching ANY
      // unstamped bubble here is safe — there is at most one.
      const unstamped = m.turnId === undefined
      if (!unstamped && m.turnId !== turnId) continue
      if (frame.agent_id && m.agentId && m.agentId !== frame.agent_id) continue
      if (m.status === 'interrupted' || m.status === 'error') continue
      // N2/N5 (see Step 1's identical fixes above for the full "why"): a
      // bubble closed only by a mid-turn steer, OR a replay_message-
      // reconstructed bubble belonging to the server-confirmed active
      // turn, is not actually finished for its own turn — eligible to
      // receive this turn's NEXT message_id too.
      const stillEligible = unstamped || m.closedBySteer || (m.turnId === draft.activeTurnId && draft.activeTurnId != null)
      if (m.status === 'done' && !m.isStreaming && !stillEligible) continue // closed by done(turn_id) — do not reopen
      turnBubbleId = id
      break
    }
    if (turnBubbleId) {
      const m = draft.messagesById[turnBubbleId]
      if (m.turnId === undefined) m.turnId = turnId // now that a token has named the turn
      m.closedBySteer = false
      m.mergedReplayIds = [...(m.mergedReplayIds ?? []), messageId]
      draft.mergedReplayMessageIds = { ...(draft.mergedReplayMessageIds ?? {}), [messageId]: true }
      applyTokenContentTo(draft, turnBubbleId, frame)
      return
    }
  }

  // Step 3: no bubble for this turn_id either — a genuinely new turn's first
  // message_id. There may already be an OPEN assistant bubble under a
  // different, local id, from a DIFFERENT (now-superseded) turn:
  //
  //   (a) sendMessage's own send-time optimistic placeholder
  //       (outbound-lifecycle.ts, a generateId()'d empty bubble minted the
  //       instant the user hits send, purely so "the agent is about to
  //       reply" renders with zero latency, before the server has assigned
  //       — or this client has even learned — the real message_id/turn_id).
  //       Real-browser regression (orchestrator report, 2026-09-24, BUG 2):
  //       under the pre-#823 "last assistant message" heuristic the first
  //       live token transparently reused this placeholder; message_id-keyed
  //       resolution instead always minted a SECOND, brand-new bubble here,
  //       leaving the placeholder orphaned — an empty "just the agent name"
  //       bubble in front of every real answer. Fix: RE-KEY the placeholder
  //       to the real message_id/turn_id instead of creating a second bubble.
  //
  //   (b) a genuinely different, already-content-bearing bubble — e.g. a
  //       bubble a disconnect left open (BUG 1's fix, clearStreamingState no
  //       longer closes it) that this new turn is superseding. That one is
  //       FINALIZED, not reused — bake any tool calls it still owns first
  //       (mirrors the pre-#823 abandoned-bubble bake: a tool call started
  //       before this boundary must not be silently dropped from the
  //       closing bubble's rendered tool_calls).
  //
  // Told apart by content: (a) has none yet (and owns no tool calls) — it is
  // by construction the placeholder, since a real bubble that has already
  // received a token would already have matched step 1 or 2 above for its
  // OWN turn. (b) has already streamed something.
  const prevOpenId = findOpenAssistantMessageId(draft.messageOrder, draft.messagesById)
  if (prevOpenId && prevOpenId !== messageId) {
    const prevMsg = draft.messagesById[prevOpenId]
    const ownedIds = draft.toolCallOrder.filter(
      (id) => draft.toolCalls[id] && draft.toolCallOwnerMessageId?.[id] === prevOpenId,
    )
    const isEmptyPlaceholder =
      (prevMsg.isStreaming || prevMsg.status === 'streaming') &&
      !prevMsg.content &&
      ownedIds.length === 0 &&
      !prevMsg.media?.length &&
      !prevMsg.spans?.length
    if (isEmptyPlaceholder) {
      delete draft.messagesById[prevOpenId]
      const idx = draft.messageOrder.indexOf(prevOpenId)
      const bubble: ChatMessage = {
        ...prevMsg,
        id: messageId,
        content: frame.content,
        agentId: frame.agent_id ?? prevMsg.agentId,
        turnId: turnId ?? prevMsg.turnId,
      }
      draft.messagesById[messageId] = bubble
      if (idx !== -1) draft.messageOrder[idx] = messageId
      else draft.messageOrder.push(messageId)
      draft.isStreaming = true
      return
    }
    if (prevMsg.isStreaming || prevMsg.status === 'streaming') {
      if (ownedIds.length > 0) {
        bakeToolCallsByOwner(draft.messagesById, ownedIds, draft.toolCalls, draft.toolCallOwnerMessageId ?? {}, prevOpenId, draft.textAtToolCallStart)
      }
      prevMsg.isStreaming = false
      prevMsg.status = 'done'
      prevMsg.pendingTextBoundary = false
    }
  }
  // R-J's collision case (see the "cross-turn id collision" comment on
  // resolvedId above): messageId can already belong to a DIFFERENT,
  // unrelated bubble (Step 1 deliberately refused to reuse it). Minting the
  // new bubble under that same id would silently overwrite/destroy the
  // other turn's bubble in messagesById (same key). Mint a fresh local id
  // instead in that case, and still register the server's message_id onto
  // it (mergedReplayMessageIds) so a later token that legitimately repeats
  // this message_id for THIS turn still resolves consistently.
  const idCollision = !!draft.messagesById[messageId]
  const bubbleId = idCollision ? generateId() : messageId
  const bubble: ChatMessage = {
    id: bubbleId,
    role: 'assistant',
    content: frame.content,
    timestamp: new Date().toISOString(),
    status: 'streaming',
    isStreaming: true,
    agentId: frame.agent_id ?? useSessionStore.getState().activeAgentId ?? undefined,
    turnId,
    ...(idCollision ? { mergedReplayIds: [messageId] } : {}),
  }
  if (idCollision) {
    draft.mergedReplayMessageIds = { ...(draft.mergedReplayMessageIds ?? {}), [messageId]: true }
  }
  draft.messagesById[bubble.id] = bubble
  // Opus review round 3 item N5 (LOW-MEDIUM): a brand new assistant bubble
  // must land BEFORE the pending tail (unresolved queued/sending/failed
  // sends), not blindly at the true end of messageOrder — same rule as
  // replay_message's own history insertion (insertHistoryMessageId, item 2
  // above), applied here too since a live token can mint a new bubble
  // while a pending message is sitting at the tail (a full rebuild during
  // a multi-step turn is the scenario that surfaced it: the turn's
  // continuation must not render BELOW a pending message it was never
  // actually sent after).
  insertHistoryMessageId(draft.messageOrder, draft.messagesById, bubble.id)
  draft.isStreaming = true
}

interface FrameContext {
  set: StoreApi<ChatStore>['setState']
  get: StoreApi<ChatStore>['getState']
  getActiveSid: () => string | null
  bucketToForeground: (bucket: SessionChatState) => Omit<SessionChatState, 'messageOrder' | 'trimmedCount' | 'spanBySpanId' | 'pendingSpanUpdatesBySpanId' | 'toolCallOwnerMessageId'> & { messages: ChatMessage[]; lastAssistantMessageId: string | null }
  withBucket: (sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void
  deleteBucket: (sid: string) => void
  resolveKickoffAttempt: (workspaceId: string, outcome: 'done' | 'failed') => void
  abandonPendingKickoffInternal: () => void
  syncForeground: () => void
  armRateLimitClear: (sid: string, event: RateLimitEventData) => void
  maybeDrainNext: () => void
  runtime: { unknownFrameCount: number; agentIdAtLastMintSend: string | null }
}
export function createFrameSlice({ set, get, getActiveSid, bucketToForeground, withBucket, deleteBucket, resolveKickoffAttempt, abandonPendingKickoffInternal, syncForeground, armRateLimitClear, maybeDrainNext, runtime }: FrameContext): FrameSlice {
  return {
    handleFrame: (frame) => {
      // Resolve which session this frame belongs to.
      // session_started is special: it carries the new id for the pending message.
      // ask_user_question nests its session id on card.session_id (required
      // by schema) rather than the top level — fall back to it so the frame
      // routes session-scoped like every other member of
      // SESSION_SCOPED_FRAME_TYPES.
      const frameSessionId =
        (frame as { session_id?: string }).session_id ??
        (frame as { card?: { session_id?: string } }).card?.session_id

      // ADR-091 D7/FR-E-002 (cross-family review finding 18): the required-
      // session check runs FIRST, before anything else — timestamp advance,
      // handleReplayAndStatusFrame, the cancel-ack disambiguation below, and
      // every switch-case reducer — and returns from `handleFrame` itself.
      // A session-scoped frame identifies its own session; when it doesn't,
      // that is a routing error, never a "guess the session" situation, so
      // it is dropped outright — never filed under whatever happens to be
      // foreground, and never reassigned to some OTHER session either (see
      // the CANCEL_ACK_FRAME_TYPES note below, which is why that mechanism
      // is scoped to non-session-scoped frame types only). Doing this here,
      // before the switch, is what makes every session-scoped case arm
      // downstream able to assume `targetSid` is non-null once reached —
      // upstream fallbacks (`rate_limit`'s `targetSid ?? getActiveSid()`,
      // `tool_approval_required`'s unconditional `enqueue(frame)`) were the
      // two production paths that used to file such a frame anyway.
      if (SESSION_SCOPED_FRAME_TYPES.has(frame.type) && !frameSessionId) {
        console.error('[chat] server frame missing session_id — dropping', { type: frame.type })
        logDiagnostic('chatFrameMissingSessionId', { frameType: frame.type })
        useConnectionStore.getState().setConnectionError(
          'internal: server frame missing session_id — please reload'
        )
        return
      }

      const activeSid = getActiveSid()

      // F-S1: Route to the correct bucket. By this point a session-scoped
      // frame is guaranteed to carry frameSessionId (the branch above
      // returned otherwise), so only a GLOBAL frame (error, ping, pong,
      // device_pairing_*, session_state) can still be missing one.
      const targetSid: string | null = (() => {
        if (frame.type === 'session_started') return activeSid // handled below, value unused
        if (frameSessionId) return frameSessionId
        // F-S3 (UAT, browser-panel "Take over"): an untagged (no session_id)
        // `error` frame that is really the server's cancellation
        // acknowledgment for a BACKGROUND session (cancelStream(sessionId),
        // e.g. the browser panel's "Take over" pausing its own pinned
        // session while a different chat is foreground) must not be
        // misattributed to whatever session happens to be active — that
        // corrupts the active session's own, unrelated in-flight turn with a
        // stray (interrupted)/error status. When there is exactly one
        // session with an outstanding, unacknowledged explicit cancel and it
        // is NOT the active session, it is unambiguously the more likely
        // origin than "whatever's on screen" — route there instead. Any
        // other case (no pending cancel, more than one pending, or the
        // pending one IS the active session — the ordinary single-session
        // Stop-button flow) falls through unchanged to the existing
        // behaviour below.
        //
        // CANCEL_ACK_FRAME_TYPES no longer includes `token`/`done`: both are
        // session-scoped (SESSION_SCOPED_FRAME_TYPES), so a missing-id
        // instance of either already returned above — this disambiguation
        // can only ever apply to `error`, which is global. Reassigning a
        // session-scoped frame to a guessed session (rather than dropping
        // it) is exactly the bug cross-family review finding 18 reported.
        if (CANCEL_ACK_FRAME_TYPES.has(frame.type) && pendingCancelAckSids.size === 1) {
          const [onlyPendingSid] = pendingCancelAckSids
          if (onlyPendingSid !== activeSid) return onlyPendingSid
        }
        // Global frame (error, ping, pong, device_pairing_*, session_state) — use active.
        return activeSid
      })()

      // HIGH-2: reset unknown-frame counter on every known-good frame.
      runtime.unknownFrameCount = 0

      // I1: advance the reconnect `since` cursor — see advanceReceivedEventTime's
      // own doc comment above (moved there so this addition doesn't grow
      // handleFrame past its grandfathered line budget).
      advanceReceivedEventTime(frame, targetSid, withBucket)

      // #823 catch-up redesign (BE-DESIGN.md §6.2): the apply rule, gating
      // every SEQUENCED session-scoped frame ahead of the switch below — see
      // applySeqGate's own doc comment for why this belongs here rather than
      // per-case.
      if (applySeqGate(frame, targetSid, get, withBucket) === 'drop-or-gap') {
        syncForeground()
        return
      }

      if (handleReplayAndStatusFrame({ frame, targetSid, get, withBucket, armRateLimitClear })) {
        syncForeground()
        return
      }

      if (handleCatchUpFrame({ frame, targetSid, get, withBucket })) {
        syncForeground()
        return
      }

      // provider-messages spec §8 — the failover-chain frames
      // (provider_retry / provider_fallback / replay_provider_fallback).
      // Own-switch handler (catchup-frames.ts pattern): the main switch
      // below is a grandfathered budget entry. Returns false for every
      // other frame type.
      if (handleProviderFrame({ frame, targetSid, get, withBucket })) {
        syncForeground()
        return
      }

      switch (frame.type) {
        case 'message_status':
          withBucket(targetSid, (bucket) => applyMessageStatusFrame(bucket, frame))
          break
        case 'session_started': {
          // Server minted a new session_id in response to a message sent without one.
          const newSid = frame.session_id

          // This ack may be resolving a pending workspace-setup
          // kickoff rather than an ordinary sendMessage no-session turn.
          // Capture + clear `pendingKickoff` up front — this frame always
          // resolves whichever kickoff was outstanding, regardless of which
          // branch below runs. Resolve the per-workspace attempt tracker
          // ('done') too — this ack is always a SUCCESS for the kickoff
          // (a failure arrives as an 'error' frame instead, never as
          // session_started), regardless of which branch below handles it.
          const resolvedKickoff = get().pendingKickoff
          if (resolvedKickoff) {
            set({ pendingKickoff: null })
            resolveKickoffAttempt(resolvedKickoff.workspaceId, 'done')
          }
          const stillOnOriginatingWorkspace =
            resolvedKickoff === null ||
            useWorkspacesStore.getState().activeWorkspaceId === resolvedKickoff.workspaceId
          // Plausibility gate: even when still on the originating
          // workspace, only treat this ack as safe to FOREGROUND when the
          // '__pending' placeholder is STILL the local foreground turn
          // (activeSessionId === '__pending'). If the user has since
          // attached to a different, real session WITHOUT leaving the
          // workspace — e.g. picked an existing conversation from the
          // sidebar/search while the kickoff was still resolving —
          // `activeSessionId` is that real session's id, not '__pending';
          // forefronting the kickoff's session here would silently evict
          // the session the user actually selected (and, with `sendMessage`'s
          // no-longer-possible collision — the collision guard prevents
          // a stray plain message from ever sharing the '__pending' bucket
          // with the kickoff — this is the one remaining way a late kickoff
          // ack could still misattribute). Treat it exactly like "wrong
          // workspace": migrate silently, do not disturb whatever is
          // actually foreground.
          const kickoffStillForeground =
            resolvedKickoff === null || useSessionStore.getState().activeSessionId === '__pending'

          if (resolvedKickoff && (!stillOnOriginatingWorkspace || !kickoffStillForeground)) {
            // Late ack, wrong workspace, or superseded foreground:
            // do NOT foreground it — leave whatever the user is currently
            // looking at completely alone. Record the real session id under
            // the ORIGINATING workspace's descriptor instead, so a later
            // enterWorkspaceChat for that workspace attaches to (and
            // replays) this session normally.
            useSessionStore.getState().setWorkspaceSessionDescriptor(resolvedKickoff.workspaceId, {
              id: newSid,
              type: 'chat',
              title: null,
              agentId: frame.agent_id ?? null,
            })
            // Free the '__pending' bucket key. Its content so far (Ava's
            // greeting, still streaming) isn't needed locally — re-entering
            // the originating workspace triggers a fresh server replay via
            // attachToSession regardless, and leaving it here would risk
            // colliding with the next unrelated no-session turn that reuses
            // the same '__pending' key.
            deleteBucket('__pending')
            queryClient.invalidateQueries({ queryKey: ['sessions'] })
            // A message that collided with this kickoff's slot (via
            // the `sendMessage` collision guard) sits in `outboundQueue` —
            // nothing else will release it now that this kickoff has
            // resolved via this (non-foregrounding) branch.
            get().drainOutboundQueue()
            break
          }

          // Plain sendMessage ack, OR a kickoff ack while the user is still
          // on the workspace that triggered it — foreground exactly as
          // before (byte-for-byte unchanged from the pre-fix logic).
          //
          // Register in session store and create the bucket.
          //
          // Only adopt the server's agent while the selection is still the one
          // this mint was sent under. If the user switched the picker while the
          // ack was in flight, their newer explicit choice wins — a stale echo
          // must not silently reassign the agent out from under them.
          const currentAgentId = useSessionStore.getState().activeAgentId
          const userReselected =
            runtime.agentIdAtLastMintSend !== null && currentAgentId !== runtime.agentIdAtLastMintSend
          runtime.agentIdAtLastMintSend = null
          useSessionStore
            .getState()
            .setActiveSession(newSid, userReselected ? currentAgentId : (frame.agent_id ?? currentAgentId))
          // ADR-092 founder ruling (2026-09-24): a per-chat Auto-approve
          // choice made in the composer while this was still a brand-new,
          // session-less chat is now carried AS PART OF the very message
          // that minted this session (`sendMessage`'s no-active-session
          // branch sends it as `auto_approve` on the MessageFrame) — the
          // server records it in SessionModeStore before the turn is even
          // dispatched to the agent loop (websocket_chat.go's
          // recordSessionAndTranscript), so by the time this ack arrives the
          // choice has ALREADY taken effect server-side. A follow-up
          // `session_mode_update` round trip here (the previous approach)
          // is no longer needed and — being sent only after this ack —
          // could race the agent loop's own first LLM round trip, letting
          // the new chat's first tool call be decided under the wrong mode.
          // Captured and cleared here, unconditionally, so it is never
          // reused for a later, unrelated new chat's picker default.
          const pendingChoice = get().pendingAutoApproveChoice
          if (pendingChoice !== null) {
            set({ pendingAutoApproveChoice: null })
          }
          // Bucket is lazily created by first withBucket call; ensure it exists now
          // so the foreground syncs immediately.
          // FR-21 / T21–T25: session_started fires when the server begins a new turn
          // for a message sent without a session_id. The agent is about to stream —
          // pre-set isStreaming:true so the Stop button appears immediately without
          // waiting for the first token or tool_call_start frame.
          //
          // #253(a): If a '__pending' bucket exists (from the no-session optimistic
          // render path), migrate its messages into the real session bucket so the
          // user sees a continuous conversation rather than a blank slate + re-render.
          set((state) => {
            const pendingBucket = state.sessionsById['__pending']
            if (state.sessionsById[newSid]) {
              // Real bucket already exists — drop the pending bucket if present.
              if (!pendingBucket) return {}
              const sessionsById = { ...state.sessionsById }
              delete sessionsById['__pending']
              return { sessionsById }
            }
            // #823 catch-up redesign pass 2 (BE-DESIGN.md §3.4/§6.1): mint
            // the cursor from session_started's own seq/boot_id when
            // present — this is the NEW session's very first cursor
            // position, exempted from the ordinary seq gate
            // (CURSOR_MINTING_FRAME_TYPES, cursor.ts) because
            // session_started is special-routed (targetSid resolves to
            // activeSid, never the newly-minted newSid — see this case's
            // own targetSid comment above), so nothing else could ever
            // write it.
            const cursorPatch =
              frame.seq !== undefined
                ? { cursor: cursorFromTerminalFrame({ seq: frame.seq, boot_id: frame.boot_id }) }
                : {}
            // Migrate pending bucket messages into the new bucket, or start fresh.
            const baseBucket: SessionChatState = pendingBucket
              ? {
                  ...pendingBucket,
                  isStreaming: true,
                  lastUserMessageAt: Date.now(),
                  ...cursorPatch,
                }
              : { ...emptySessionState(), isStreaming: true, ...cursorPatch }
            const sessionsById = { ...state.sessionsById, [newSid]: baseBucket }
            // Remove the temporary pending bucket.
            delete sessionsById['__pending']
            return { sessionsById, ...bucketToForeground(baseBucket) }
          })
          // Reflect the captured choice into this session's OWN bucket
          // (never a global field — see the pendingChoice capture above) so
          // `useResolvedAutoApprove` shows it via `autoApproveEffective`
          // exactly as a `session_mode_updated` ack would have, without
          // waiting on one: `SessionModeStore.Get` returns the modifier
          // verbatim once set (`ResolveAutoApprove`,
          // pkg/agent/sessionmode.go), so this mirrors the value the server
          // already recorded for newSid — not a guess.
          if (pendingChoice !== null) {
            withBucket(newSid, () => ({ autoApproveEffective: pendingChoice }))
          }
          // Invalidate sessions list so the session lists (SearchModal, sidebar
          // accordion) re-fetch and show the new session.
          queryClient.invalidateQueries({ queryKey: ['sessions'] })
          // A steer sent while this turn was still under the '__pending'
          // placeholder sid (see sendMessage's isStreaming branch) was
          // buffered into outboundQueue instead of sent — drainOutboundQueue
          // was previously only invoked on WS reconnect (OmnipusRuntimeProvider),
          // so without this call that buffered steer would sit inert until
          // the NEXT disconnect/reconnect cycle, which may never happen in a
          // healthy session. Draining here moves it into pendingDrainQueue;
          // maybeDrainNext() (called inside drainOutboundQueue) reads
          // isStreaming fresh via get() — which bucketToForeground above just
          // set to true for this brand-new turn — so it correctly no-ops now
          // and the buffered message goes out automatically as an ordinary
          // next-turn message once THIS turn's own done/error frame calls
          // maybeDrainNext() again. A no-op (queue empty) the vast majority
          // of the time, so unconditional here is cheap and safe.
          get().drainOutboundQueue()
          break
        }

        case 'token':
          if (targetSid) {
            withBucket(targetSid, (b) => {
              return produce(b, (draft) => {
                // #823 catch-up redesign pass 2 (§6.3, Q3): message_id-keyed
                // resolution REPLACES the heuristic below for every frame
                // that carries one — see resolveTokenBubbleByMessageId's own
                // doc comment. The heuristic survives only as a fallback for
                // a message_id-less frame (a hand-built test frame, or the
                // webchatChannel.Send no-stream fallback, which still has
                // none per Lane A's report).
                if (frame.message_id) {
                  resolveTokenBubbleByMessageId(draft, frame)
                  return
                }
                let lastMsgId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
                // FR-21 / T21–T26: if the last assistant message was already
                // interrupted (user clicked Stop / pressed Escape / used /cancel),
                // discard any trailing tokens the server sends before it processes
                // the cancel. markLastMessageInterrupted() sets isStreaming:false on
                // the message so that AssistantUI renders the correct "incomplete/cancelled"
                // status. We must NOT append to the interrupted message or create a new
                // placeholder — either would erase the (interrupted) label or create a
                // ghost streaming bubble without the label.
                if (lastMsgId && draft.messagesById[lastMsgId].status === 'interrupted') return
                // Same rule, same reason, for a terminally errored bubble.
                //
                // A failed turn delivers its explanation TWICE by design, on two
                // independent paths: the typed `error` frame (rich — code,
                // retryable, detail, Retry button), and then a plain `token` +
                // `done` from the outbound-publish fallback that exists so a
                // turn which produced no tokens still terminates the stream
                // instead of leaving a stuck "thinking" spinner
                // (pkg/gateway/websocket.go, wsStreamer.Finalize's markStreamed
                // guard). Both carry the same user-facing sentence.
                //
                // Without this guard the trailing token crosses the closed-bubble
                // segment boundary below and mints a SECOND bubble holding a
                // duplicate of the text already shown — and the duplicate carries
                // none of the error treatment, so the copy the user is most
                // likely to read is the one with no Retry button. Worse, the two
                // paths classify independently (the frame from the provider's
                // HTTP status, the fallback from the error STRING via
                // TranslateTurnError with a nil ProviderError), so they can
                // legitimately disagree and show the user two different
                // explanations for one failure.
                //
                // Discarding here rather than suppressing server-side is
                // deliberate: the client is the only party that KNOWS the error
                // frame arrived. A backend suppression would have to assume
                // delivery, and a wrong assumption there turns a duplicate
                // message into no message at all — trading a cosmetic defect for
                // the silence this whole fix exists to remove.
                if (lastMsgId && draft.messagesById[lastMsgId].status === 'error') return
                // Track the bubble being left behind by either boundary check
                // below so any tool calls it already holds can be baked onto
                // it (see the baking block right after both checks) instead
                // of silently accumulating in the shared bucket-level
                // toolCallOrder to be baked at `done` time onto whichever
                // bubble happens to be LAST at that point — which, once a
                // turn produces more than one bubble (Fix 5a; the Bug 2
                // sync/await-mode attribution fix), may be a completely
                // different producer's bubble than the one the tool call
                // actually started on.
                let abandonedMsgId: string | null = null
                // Empty-response variant of the "delivered twice" defect
                // documented on the status==='error' guard above: when the
                // LLM call itself produced no tokens at all (not a
                // classified error — the engine's success-path empty-content
                // fallback, pkg/agent/loop.go's `defaultResponse` sentinel),
                // NO `error` frame is ever sent, so the guard above never
                // fires. The turn still finalizes the optimistic placeholder
                // via `done` with content:'' (nothing was ever streamed to
                // abandon it against), and THEN webchatChannel.Send()'s
                // markStreamed fallback (pkg/gateway/webchat_channel.go —
                // markStreamed is only called when `accumulated.Len() > 0`)
                // delivers the actual fallback text as a second token+done
                // pair so the turn doesn't strand the user on a stuck
                // "thinking" spinner. Without this check, the boundary rule
                // right below (closed bubble = new segment) abandons the
                // now-closed EMPTY placeholder and mints a brand-new bubble
                // for that fallback text — leaving the original placeholder
                // stranded on screen as a permanent empty bubble with a Copy
                // button that copies nothing (D-fix's terminal-empty
                // variant). A closed bubble that finalized holding
                // absolutely nothing — no text, no tool call, no media, no
                // subagent span — was never actually shown as content, so
                // reusing it here (rather than abandoning it) collapses the
                // two deliveries back into the single bubble the user
                // actually needs to see.
                const lastMsg = lastMsgId ? draft.messagesById[lastMsgId] : null
                const lastMsgIsEmptyTerminal =
                  !!lastMsg &&
                  !lastMsg.isStreaming &&
                  !lastMsg.content?.trim().length &&
                  !lastMsg.tool_calls?.length &&
                  !lastMsg.media?.length &&
                  !lastMsg.spans?.length
                // Only reuse the last assistant bubble if it is still
                // streaming (or, per the empty-terminal case just above, if
                // it finalized holding nothing at all). A closed bubble
                // that DID hold something (status=done, real content/tool
                // calls/media/spans already shown) means the prior LLM call
                // has finalized and any new tokens are part of a *new*
                // turn-segment — typically a follow-up call after a tool
                // returned. Stuffing them back into that closed bubble is
                // what produced the "text-then-image-at-bottom" ordering.
                if (lastMsgId && !draft.messagesById[lastMsgId].isStreaming && !lastMsgIsEmptyTerminal) {
                  abandonedMsgId = lastMsgId
                  lastMsgId = null
                }
                // Delegate-attribution fix: a still-streaming bubble is ALSO a
                // "new segment" boundary when the incoming frame's producer
                // differs from the bubble's already-known producer. Without
                // this, a background delegate's own token stream — which per
                // Fix 5a correctly carries the DELEGATE's agent_id on the wire
                // — lands on the delegator's still-open bubble (the delegator's
                // turn is not "done" while it waits on the delegate) and the
                // unconditional `msg.agentId = frame.agent_id` write below
                // silently relabels the ENTIRE bubble — including the
                // delegator's own already-rendered lead-in reasoning text — as
                // the delegate's. That produced both the persisted
                // misattribution (the delegate's tokens are the last writer
                // before the bubble finalizes) and the transient live flicker
                // (the delegator's own follow-up tokens later re-claim it).
                // Only split when BOTH ids are known and they actually
                // disagree — an unset bubble agentId (e.g. the optimistic
                // placeholder from sendMessage()) or a frame that omits
                // agent_id (legacy) must keep the existing permissive
                // behavior.
                if (
                  lastMsgId &&
                  frame.agent_id &&
                  draft.messagesById[lastMsgId].agentId &&
                  draft.messagesById[lastMsgId].agentId !== frame.agent_id
                ) {
                  abandonedMsgId = lastMsgId
                  lastMsgId = null
                }
                // Copy (not move) any tool calls OWNED by the bubble we are
                // abandoning onto it, BEFORE it stops being "the last
                // message". ChatScreen.tsx's VirtualizedMessageListInner
                // renders every message except the CURRENT last one via the
                // historical VirtualAssistantMessageRow — which only shows
                // tool calls already present in message.tool_calls, not the
                // live bucket — so the instant a NEW bubble opens (this
                // frame), the abandoned bubble is no longer last and stops
                // being live-rendered. Without this, an unbaked tool call
                // would stay invisible until a turn-end bake (`done`,
                // `clearStreamingState`) finally routes it.
                //
                // Deliberately a COPY, not a move: the call may still be
                // 'running' (e.g. the delegator's own "Delegate task" call —
                // its tool_call_result only arrives once the delegate's
                // whole sub-turn, including its reply tokens that just
                // triggered this very abandonment, has finished). The live
                // bucket entry (toolCalls/toolCallOrder/textAtToolCallStart)
                // is deliberately left untouched here so tool_call_result
                // can still update it, and the eventual turn-end bake
                // (bakeToolCallsByOwner, routed via toolCallOwnerMessageId)
                // overwrites this copy with the final resolved status —
                // baking a 'running' snapshot here and then clearing the
                // live bucket would freeze the pill at "Running..." forever,
                // since nothing would be left to apply the later result to.
                if (abandonedMsgId && draft.toolCallOrder.length > 0) {
                  const ownedIds = draft.toolCallOrder.filter(
                    (id) => draft.toolCalls[id] && draft.toolCallOwnerMessageId?.[id] === abandonedMsgId,
                  )
                  if (ownedIds.length > 0) {
                    bakeToolCallsByOwner(draft.messagesById, ownedIds, draft.toolCalls, draft.toolCallOwnerMessageId ?? {}, abandonedMsgId, draft.textAtToolCallStart)
                  }
                }
                if (!lastMsgId) {
                  const placeholder: ChatMessage = {
                    id: generateId(),
                    role: 'assistant',
                    content: '',
                    timestamp: new Date().toISOString(),
                    status: 'streaming',
                    isStreaming: true,
                    // Fix 5a: prefer the real producer's agent id (populated by the
                    // backend at token-emission time) over the client-side
                    // activeAgentId guess — the guess is wrong for background/
                    // delegated sub-turns where the true producer isn't "whoever
                    // the user happens to be chatting with". Falls back to the
                    // guess only for legacy/older frames that omit agent_id.
                    agentId: frame.agent_id ?? useSessionStore.getState().activeAgentId ?? undefined,
                  }
                  draft.messagesById[placeholder.id] = placeholder
                  draft.messageOrder.push(placeholder.id)
                  lastMsgId = placeholder.id
                }
                const msg = draft.messagesById[lastMsgId]
                // Fix 5a (cont.): the bubble being appended to here may be the
                // OPTIMISTIC placeholder created synchronously in sendMessage()
                // (which has no agentId yet — the true producer isn't known at
                // send time) rather than the one created above. Stamp/refresh the
                // attribution from the frame as soon as the backend tells us,
                // rather than only at placeholder-creation time — this is the
                // path that actually fires for the common "user message → agent
                // reply" case. Strict improvement: only overrides when the frame
                // carries agent_id; otherwise the existing value (possibly
                // undefined, falling back to activeAgentId at render time) is
                // left untouched — no behavior change for legacy frames.
                if (frame.agent_id) {
                  msg.agentId = frame.agent_id
                }
                // #823 catch-up redesign (BE-DESIGN.md §6.3): TokenFrame now
                // carries turn_id (Step 0's contract change) — stamp it the
                // same way agentId is stamped above, additive-only (never
                // overwrites an already-known value with a different one,
                // same permissive rule as the agentId boundary check).
                if (frame.turn_id && !msg.turnId) {
                  msg.turnId = frame.turn_id
                }
                // Consume the seam marker: a tool call started on this bubble
                // since the last token was appended, so this frame begins a
                // new logical unit — insert a paragraph break instead of
                // gluing it directly onto the trailing narration (live-UAT
                // regression — e.g. "...now.Now delegating…" / "...inline:ping"
                // with zero separator).
                if (msg.pendingTextBoundary) {
                  if (msg.content) msg.content += '\n\n'
                  msg.pendingTextBoundary = false
                }
                msg.content = msg.content + frame.content
                msg.isStreaming = true
                msg.status = 'streaming'
                draft.isStreaming = true
              }) as Partial<SessionChatState>
            })
          }
          break

        case 'done':
          if (targetSid) {
            // B1.3d: when done arrives for a targetSid that isn't in sessionsById yet,
            // the session was probably switched away mid-stream. The active bucket's
            // isStreaming flag would otherwise stay true forever (infinite spinner).
            // Log a diagnostic warning and conditionally force-clear isStreaming on
            // the active bucket.
            //
            // H1-FE: Guard against corrupting an active mid-stream session.
            // Two cases where we must NOT force-clear the active bucket:
            //   1. targetSid === activeSid — the active session itself produced an
            //      unknown-sid done, which should never happen; the normal path below
            //      will handle it correctly, so do not fall through to the break.
            //   2. The active bucket sent a user message recently (< 10 s ago) and is
            //      still streaming — the done belongs to a different (wiped/replayed)
            //      session and the active spinner is legitimate.
            const knownSid = !!get().sessionsById[targetSid]
            if (!knownSid) {
              console.warn('chat.done_unknown_sid', { targetSid, activeSid: activeSid })
              logDiagnostic('chatDoneUnknownSid', { targetSid, activeSid })
              const STREAM_GRACE_MS = 10_000
              if (activeSid && activeSid !== targetSid && get().sessionsById[activeSid]) {
                const activeBucket = get().sessionsById[activeSid]!
                const isActiveMidStream =
                  activeBucket.isStreaming &&
                  activeBucket.lastUserMessageAt !== null &&
                  Date.now() - activeBucket.lastUserMessageAt < STREAM_GRACE_MS
                if (!isActiveMidStream) {
                  // Active bucket spinner is likely a stale remnant from the wiped
                  // session — safe to clear.
                  withBucket(activeSid, () => ({ isStreaming: false }))
                  maybeDrainNext()
                } else {
                  console.warn('chat.done_unknown_sid_skipped_active_mid_stream', {
                    targetSid,
                    activeSid,
                    lastUserMessageAt: activeBucket.lastUserMessageAt,
                  })
                  logDiagnostic('chatDoneUnknownSidSkippedActiveMidStream', {
                    targetSid,
                    activeSid,
                    lastUserMessageAt: activeBucket.lastUserMessageAt,
                  })
                }
              } else {
                // Defensive (boundary case, not an observed failure): no active
                // bucket to force-clear here — e.g. no active session at all, or
                // its bucket doesn't exist yet. isStreaming may already be false
                // via other means, but drain anyway so a message queued behind
                // this unknown-sid done can't get permanently stranded.
                maybeDrainNext()
              }
              break
            }

            // Decide whether isReplaying must clear now vs. defer to a setTimeout.
            // The clear happens INSIDE the same withBucket return below — never via
            // a nested withBucket call, because the outer set() commits the bucket
            // last and clobbers any nested writes that ran during the updater.
            const sid = targetSid
            // F-S3: this session's cancellation (if any) has now been
            // terminally acknowledged — stop treating it as "pending" so a
            // later, unrelated untagged frame doesn't get misattributed here.
            pendingCancelAckSids.delete(sid)
            const priorBucket = get().sessionsById[sid] ?? EMPTY_BUCKET
            const wasReplaying = priorBucket.isReplaying
            const elapsed = wasReplaying ? Date.now() - (replayingStartedAt[sid] ?? 0) : 0
            // FR-I-014: mirror the same MIN_REPLAY_DISPLAY_MS used in setReplaying above.
            // Both code paths that clear isReplaying must use the same threshold.
            const MIN_REPLAY_DISPLAY_MS = 750
            const clearReplayingNow = wasReplaying && elapsed >= MIN_REPLAY_DISPLAY_MS
            if (wasReplaying) {
              sawReplayMessageThisTurn[sid] = false
              if (!clearReplayingNow) {
                if (replayingClearTimers[sid]) {
                  clearTimeout(replayingClearTimers[sid])
                }
                replayingClearTimers[sid] = setTimeout(() => {
                  delete replayingClearTimers[sid]
                  withBucket(sid, () => ({ isReplaying: false }))
                }, MIN_REPLAY_DISPLAY_MS - elapsed)
              }
            }
            // ADR-082 D3/D4 (FR-007/FR-009), review S1/CR1, updated for
            // #823 pass 2: a `done` frame can still arrive TWICE for a
            // mid-turn attach on Lane A's current gateway — once marking the
            // end of transcript replay (carries `stats.frames_emitted`,
            // never `stats.tokens`/`stats.cost` — pkg/gateway/replay.go's
            // terminator emit, kept vestigially, see the honest-gap note
            // just below), and again later when the announced turn itself
            // actually finishes (`stats.tokens`/`stats.cost` always stamped,
            // even a zero-token turn). Tell them apart PURELY by this stats
            // shape.
            const doneStats = frame.stats
            // N4 — see scheduleReplayErrorRetry's own doc comment.
            if (doneStats?.replay_error === true) { scheduleReplayErrorRetry(sid); break }
            const isReplayTerminatorDone =
              doneStats?.frames_emitted !== undefined &&
              doneStats?.tokens === undefined &&
              doneStats?.cost === undefined
            if (isReplayTerminatorDone) {
              // #823 catch-up redesign pass 2 (Lane A's SQUAD-REPORT-BEA.md
              // "Opus pass", honest gap #5): this frame is VESTIGIAL for
              // catch-up purposes — Lane A's gateway still emits it
              // (removing it broke ~40 gateway-side replay tests) but it
              // carries no turn_id and no seq, so nothing here can attribute
              // a placeholder to it. catch_up_complete
              // (slices/catchup-frames.ts) is now the SOLE "catch-up is
              // over" signal — it clears isReplaying/awaitingCatchUp on its
              // own. Not a full no-op, though: FX-E (ADR-082 D9) still
              // applies — bake any tool calls stranded by replay
              // reconstruction (a tool call that is the LITERAL LAST
              // transcript entry, with no further message of any role
              // replayed after it) onto their owning message. Nothing else
              // in the new design re-flushes toolCallOrder for a session
              // with no live turn to continue.
              if (priorBucket.toolCallOrder.length > 0) {
                withBucket(sid, (b) => {
                  if (b.toolCallOrder.length === 0) return {}
                  return produce(b, (draft) => {
                    const fallbackMsgId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
                    bakeToolCallsByOwner(draft.messagesById, draft.toolCallOrder, draft.toolCalls, draft.toolCallOwnerMessageId ?? {}, fallbackMsgId, draft.textAtToolCallStart)
                    draft.toolCalls = {}
                    draft.toolCallOrder = []
                    draft.textAtToolCallStart = {}
                    draft.toolCallOwnerMessageId = {}
                  }) as Partial<SessionChatState>
                })
              }
              // Mirrors the normal finalization path's own drain call below —
              // harmless here too (maybeDrainNext no-ops while isStreaming).
              maybeDrainNext()
              break
            }
            // ADR-087 D2 (finding #10) — live-path counterpart of the
            // `replayTruncated`/`replayTruncationReason` pair computed for
            // 'replay_message' above. Same `normalizeTruncationReason`
            // legacy-default rule, same wire shape, different carrier
            // (DoneStats instead of ReplayMessageFrame) so a turn truncated
            // while the user is watching renders the suffix immediately.
            const doneTruncated = doneStats?.truncated === true
            const doneTruncationReason = normalizeTruncationReason(
              doneStats?.truncated,
              doneStats?.truncation_reason,
            )
            withBucket(sid, (b) => {
              return produce(b, (draft) => {
                // #823 catch-up redesign, Opus review round 2 (§6.3:
                // "done(turn_id): close every bubble of that turn"): find the
                // bubble by turn_id FIRST — this is the turn-keyed
                // counterpart of the 'token' case's own
                // resolveTokenBubbleByMessageId/findBubbleIdForMessageId. A
                // turn can register more than one message_id onto the SAME
                // bubble (F1's msg-1 + tool call + msg-2, merged via
                // mergedReplayIds), so `frame.message_id` naming that bubble
                // directly can no longer be assumed — turn_id is the
                // reliable key now. Falls back to the pre-#823
                // message_id/lastMsgId heuristic when the frame carries no
                // turn_id, or names one with no matching bubble (the
                // webchatChannel.Send no-stream fallback, which has neither).
                const doneTurnId = frame.turn_id
                const turnBubbleId = doneTurnId
                  ? (() => {
                      for (let i = draft.messageOrder.length - 1; i >= 0; i--) {
                        const id = draft.messageOrder[i]
                        const m = draft.messagesById[id]
                        if (m?.role === 'assistant' && m.turnId === doneTurnId) return id
                      }
                      return null
                    })()
                  : null
                const lastMsgId =
                  turnBubbleId ??
                  (frame.message_id && draft.messagesById[frame.message_id]
                    ? frame.message_id
                    : findLastAssistantMessageId(draft.messageOrder, draft.messagesById))
                // Sweep the UNION of {the last assistant message} (unchanged
                // — always normalized exactly as before, even when it isn't
                // flagged "streaming" at all, e.g. a replay-reconstructed
                // bubble whose `isStreaming` is `undefined` rather than
                // `true`/`false` — see the 'replay_message' case's own doc
                // comment on why replay bubbles are finalized the instant
                // they're created) ∪ {every OTHER still-streaming assistant
                // message in the bucket} (defense-in-depth addition —
                // mirrors clearStreamingState's own sweep, used on a hard
                // WS-drop, see the identical backward-scan loop there). A
                // mid-turn steer (sendMessage's `isStreaming` branch) appends
                // the steering text as a new USER message AFTER the
                // still-open assistant bubble, so that bubble is no longer
                // "the last message" by the time `done` arrives — finalizing
                // only the last assistant message would leave the ORIGINAL
                // bubble permanently stuck at isStreaming:true (permanent
                // shimmer, Copy bar suppressed) even though the turn is over.
                for (let i = draft.messageOrder.length - 1; i >= 0; i--) {
                  const id = draft.messageOrder[i]
                  const m = draft.messagesById[id]
                  if (m?.role !== 'assistant') continue
                  if (id !== lastMsgId && !m.isStreaming && m.status !== 'streaming') continue
                  // FR-21 / T21–T25: do NOT overwrite 'interrupted' status with 'done'.
                  //
                  // 'error' is preserved for the same reason, and it is not a
                  // theoretical case: a failed turn emits its typed `error`
                  // frame and then, microseconds later, the streamer's
                  // deferred finalize emits `done` for the SAME session. The
                  // error bubble IS lastMsgId, so the `continue` above does not
                  // protect it, and demoting it to 'done' silently erased every
                  // bit of the error treatment the frame had just earned:
                  // the "Error" label, the Retry button (AssistantUI maps
                  // 'error' → incomplete), and the verbose "Technical details"
                  // disclosure. errorCode was left set, so the bubble ended in
                  // a state no renderer can act on — status 'done' carrying an
                  // errorCode, while every renderer gates on status === 'error'.
                  //
                  // A terminal status is terminal. `done` means "the turn is
                  // over", which is already true of an errored turn; it must
                  // never be read as "the turn succeeded". The rendering layer
                  // owns this property rather than inheriting it from whatever
                  // order the backend happens to emit its frames in.
                  m.isStreaming = false
                  m.status =
                    m.status === 'interrupted' || m.status === 'error' ? m.status : 'done'
                  // Clear the tool-call text-boundary marker on finalize. If the
                  // turn's last event was a tool call with no trailing narration
                  // token before `done`, pendingTextBoundary would otherwise be
                  // left `true` on a message with no next token coming — a
                  // representable-but-meaningless state for a finalized bubble.
                  m.pendingTextBoundary = false
                  // ADR-087 D2 (finding #10): the live `done` frame carries the
                  // same truncation signal ReplayMessageFrame carries on
                  // reattach (DoneStats.truncated/truncation_reason mirror
                  // Message.truncated/truncation_reason). Stamp it on the
                  // bubble the turn actually finished on (lastMsgId) so a
                  // turn cut off at the output limit — or cancelled — renders
                  // its suffix immediately, without waiting for a reload or a
                  // reconnect to replay it in. Only lastMsgId: the other
                  // still-streaming bubbles this sweep also finalizes (the
                  // mid-turn-steer defense-in-depth case above) are not the
                  // entry the backend actually marked truncated.
                  if (id === lastMsgId && doneTruncated) {
                    m.truncated = true
                    m.truncationReason = doneTruncationReason
                  }
                }
                // Bake any pending tool calls into the last assistant message so
                // VirtualAssistantMessageRow can render them from message.tool_calls.
                // This covers two cases:
                //   1. Replay: replay_message coalesces into the empty placeholder and
                //      returns before baking; done is the only signal that all frames
                //      for the final entry are complete.
                //   2. Live turns: tool calls stay in toolCallOrder until the next
                //      sendMessage bakes them, causing them to disappear the moment
                //      isStreaming goes false and the message moves to the historical
                //      renderer (VirtualAssistantMessageRow reads message.tool_calls, not
                //      the bucket live map). Confirmed: ChatScreen.tsx switches to
                //      VirtualAssistantMessageRow at isStreaming=false, so baking at
                //      done is required for live turns too (hotfix/v0.1.1 aff2caa).
                // Routed via toolCallOwnerMessageId (falling back to
                // lastMsgId for unmapped/legacy calls) rather than blindly
                // "the last message" — a turn can now legitimately produce
                // more than one assistant bubble (Fix 5a; the sync/await-
                // mode delegate attribution fix), and each tool call must
                // land on the bubble that actually issued it, not whichever
                // bubble happens to be last when the turn ends.
                if (draft.toolCallOrder.length > 0) {
                  bakeToolCallsByOwner(draft.messagesById, draft.toolCallOrder, draft.toolCalls, draft.toolCallOwnerMessageId ?? {}, lastMsgId, draft.textAtToolCallStart)
                  draft.toolCalls = {}
                  draft.toolCallOrder = []
                  draft.textAtToolCallStart = {}
                  draft.toolCallOwnerMessageId = {}
                }
                const tokenDelta = frame.stats?.tokens ?? 0
                const costDelta = frame.stats?.cost ?? 0
                draft.isStreaming = false
                draft.sessionTokens = draft.sessionTokens + tokenDelta
                draft.sessionCost = draft.sessionCost + costDelta
                draft.replayCompletedForSession = draft.isReplaying ? sid : draft.replayCompletedForSession
                draft.cancelStage = null
                // ADR-082 D4 (FR-009 "finalize once"), review S1/S2: this is
                // the turn's OWN done — the isReplayTerminatorDone branch
                // above always `break`s before reaching here, so every path
                // that gets here carries real turn stats (or is the
                // stats-less outbound-publish fallback done, which is also
                // always a real turn's own completion — see
                // pkg/gateway/webchat_channel.go). The announced turn, if
                // any, is now finalized: record its id as finished BEFORE
                // clearing so a stale/racing session_state.active_turn that
                // re-announces this same turn_id later (S2) is recognized
                // and ignored rather than re-opening streaming state with no
                // second done ever coming to close it. Then clear
                // activeTurnId/activeTurnAgentId so a later, unrelated
                // replay-terminating done for this session never mistakes a
                // stale id for a still-open turn.
                markTurnFinished(sid, draft.activeTurnId)
                draft.activeTurnId = null
                draft.activeTurnAgentId = null
                if (clearReplayingNow) {
                  draft.isReplaying = false
                }
              }) as Partial<SessionChatState>
            })
            // The turn that just completed may be one we sent from the
            // offline-queue drain — send the next queued message, if any.
            maybeDrainNext()
          } else {
            // Defensive (boundary case, not an observed failure): a 'done'
            // frame with no session_id at all (a protocol-violating/malformed
            // frame) skips the whole block above, including the
            // maybeDrainNext() call inside it. isStreaming may already be
            // false via other means, but drain anyway so a message queued
            // behind this malformed done can't get permanently stranded.
            maybeDrainNext()
          }
          break

        case 'error':
          {
            // ADR-051 — typed error payload. Live ErrorFrame optionally carries
            // `payload.llm_error` (a stable, enumerated code + message + an
            // optional verbose `detail`) and (on the replay sibling) an
            // `entry_id`. Legacy frames (pre-ADR-051, gateway-synthesized
            // cancel-acks, etc.) lack the typed payload; for those, fall back
            // to `frame.message` verbatim — exactly the pre-ADR-051 behavior.
            // `errorEntryId` powers the live→replay dedup (a reloaded session
            // must not re-render the same error twice). Read once here at the
            // top so all three error sub-paths below (kickoff reject, no-bucket
            // sweep, per-bucket render) see the same values.
            const llmError = readLLMErrorFromFrame(frame)
            const errorEntryId = readEntryIdFromFrame(frame)
            const verboseChatEnabled = useChatPreferencesStore.getState().verboseChatEnabled
            // D5 fix (UAT Site 3): a legacy/synthesized ErrorFrame (no typed
            // llm_error payload) falls back to the raw wire `message` for
            // display in several branches below (translatedMessage here, the
            // kickoff-reject toast, setConnectionError x2, and the
            // coalesced/fresh bubble content). Sanitize ONCE up front so
            // every one of those reads the same safe value — see
            // sanitizeLegacyErrorMessage's doc comment (lib/llm-error.ts) for
            // what it catches. Pattern-matching checks below (isCancelAck,
            // isDuplicate, console.warn/logDiagnostic) intentionally keep
            // reading `frame.message` directly — the original wire string,
            // not the sanitized display copy — since those are content
            // classifiers/logs, not user-facing text.
            const safeMessage = sanitizeLegacyErrorMessage(frame.message ?? '')
            // Resolve the visible message: translated code→display copy when
            // the typed payload is present, else the sanitized `frame.message`
            // (legacy). `errorDetail` is non-undefined only when verbose is on
            // AND detail was actually carried — the renderers key off its
            // presence to decide whether to mount the "Technical details"
            // disclosure.
            const { message: translatedMessage, detail: errorDetail } = llmError
              ? getLLMErrorDisplay(llmError, verboseChatEnabled)
              : { message: safeMessage, detail: undefined }
            // ADR-051 — live→replay dedup. If an error bubble carrying the
            // same `errorEntryId` is already in the foreground bucket (or any
            // bucket routed below), do not push a second one. Pre-checks the
            // FOREGROUND bucket here; the per-bucket branch below re-checks
            // against the routed bucket's own messages so a background-session
            // duplicate is also caught. (Live error then reload → the replay
            // path's case 'replay_error' performs the symmetric check.)
            const errorDedupId = errorEntryId

            // A kickoff rejection (duplicate kickoff, unknown
            // workspace, or a server-side kickoff error) is a hard REJECT of
            // a turn that never really started — there is no real session
            // behind it, only the local '__pending' sentinel. A kickoff
            // rejection therefore NEVER carries a session_id on the wire
            // (there is no real session to tag it with) — a
            // session-TAGGED error frame is by definition never a kickoff
            // reject, so `!frameSessionId` gates the ENTIRE kickoff-error
            // branch (both sub-cases below), never just the still-foreground
            // one. A genuinely tagged error always falls through unchanged
            // to the generic routing further down.
            const rejectedKickoff = get().pendingKickoff
            if (rejectedKickoff && !frameSessionId) {
              const sessionStillPending = useSessionStore.getState().activeSessionId === '__pending'
              // "already ran" style messages are a benign DUPLICATE — an
              // EARLIER attempt already succeeded server-side (setup_pending
              // is already correctly false), not a real failure. Everything
              // else is a genuine failure, worded differently so the user
              // knows re-opening the workspace will retry it.
              const isDuplicate = /already/i.test(frame.message ?? '')
              // `abandonPendingKickoffInternal` handles the shared cleanup
              // trio for BOTH sub-cases below: clear `pendingKickoff`, drop
              // the orphaned '__pending' bucket, and mark this workspace's
              // `kickoffAttemptStatus` 'failed' so
              // useWorkspaceSetupKickoff's invalidate-on-failure effect can
              // react and roll back its own premature optimistic
              // setup_pending:false cache write.
              abandonPendingKickoffInternal()
              console.warn('chat.workspace_setup_kickoff_rejected', { message: frame.message, duplicate: isDuplicate, stillForeground: sessionStillPending })
              logDiagnostic('chatWorkspaceSetupKickoffRejected', { message: frame.message, duplicate: isDuplicate, stillForeground: sessionStillPending })
              if (sessionStillPending) {
                // The kickoff's placeholder is still what the user is
                // looking at — reset the composer back to a normal, empty
                // state (not to be left as a stuck 'error'-status bubble
                // under '__pending', which would otherwise poison every
                // subsequent sendMessage call with a protocol-violating
                // session_id:'__pending' frame — see sendMessage's composer
                // guard) and surface a toast.
                useSessionStore.getState().setActiveSession(null)
                useUiStore.getState().addToast(
                  isDuplicate
                    ? {
                        message: 'Workspace setup already ran — find the interview in your sessions list.',
                        variant: 'default',
                      }
                    : {
                        // D5 fix (Site 3): safeMessage (computed once at the
                        // top of this case block) instead of the raw
                        // frame.message.
                        message: frame.message
                          ? `Could not start the workspace setup interview: ${safeMessage}`
                          : 'Could not start the workspace setup interview — reopen the workspace to retry.',
                        variant: 'warning',
                      },
                )
              }
              // Reject-after-navigation fall-through: when the
              // kickoff's placeholder is NO LONGER the local foreground
              // (the user already navigated away, or attached to a
              // different real session — either resets `activeSessionId`
              // away from '__pending'), this untagged reject belongs to a
              // turn nothing is currently showing. No toast — there is
              // nothing on screen to retry FROM; the workspace-open hook is
              // what will retry, once it re-checks server truth on the next
              // open. Swallow quietly and do NOT let this frame fall through
              // to the generic routing below — it would otherwise
              // misattribute to whatever IS foreground (erroring an
              // unrelated conversation's last bubble, or — if nothing is
              // foreground — raising a bogus global connection-error banner
              // via the `!targetSid` branch just below).
              //
              // Only `drainOutboundQueue()` (not `maybeDrainNext()`,
              // which the ORIGINAL version of this branch used — a bug: it
              // only ever moves `pendingDrainQueue`, never `outboundQueue`,
              // so it never actually freed a message buffered by the
              // collision guard or mid-turn '__pending' steering) releases
              // whatever is actually queued.
              get().drainOutboundQueue()
              break
            }

            // C8: a terminal error frame must always resolve the in-flight turn.
            // When the frame can't be routed to a bucket (no active session /
            // missing session_id in production), fall back to a global sweep so
            // no bucket is left wedged in a streaming state.
            if (!targetSid) {
              const isCancelAck = /turn.cancel/i.test(frame.message ?? '')
              if (!isCancelAck) {
                // D5 fix (Site 3): safeMessage, not the raw frame.message —
                // this is the global connection-error banner (AppShell),
                // visible on every screen.
                useConnectionStore.getState().setConnectionError(safeMessage)
              }
              get().clearStreamingState()
              break
            }
            // An error frame arriving during replay must also clear isReplaying —
            // otherwise the session is permanently wedged behind the "Loading
            // session history…" overlay with a disabled composer, and (unlike the
            // done path) nothing else ever clears it. Mirror the done-frame logic:
            // clear immediately once MIN_REPLAY_DISPLAY_MS has elapsed, otherwise
            // defer to a timer (cancelling any stale one first).
            const sid = targetSid
            // F-S3: see the matching comment in the 'done' case above.
            pendingCancelAckSids.delete(sid)
            const wasReplaying = (get().sessionsById[sid] ?? EMPTY_BUCKET).isReplaying
            const replayElapsed = wasReplaying ? Date.now() - (replayingStartedAt[sid] ?? 0) : 0
            const MIN_REPLAY_DISPLAY_MS = 750
            const clearReplayingNow = wasReplaying && replayElapsed >= MIN_REPLAY_DISPLAY_MS
            if (wasReplaying) {
              sawReplayMessageThisTurn[sid] = false
              if (!clearReplayingNow) {
                if (replayingClearTimers[sid]) {
                  clearTimeout(replayingClearTimers[sid])
                }
                replayingClearTimers[sid] = setTimeout(() => {
                  delete replayingClearTimers[sid]
                  withBucket(sid, () => ({ isReplaying: false }))
                }, MIN_REPLAY_DISPLAY_MS - replayElapsed)
              }
            }
            withBucket(targetSid, (b) => {
              const isCancelAck = /turn.cancel/i.test(frame.message ?? '')
              // ADR-051 — live→replay dedup (per-bucket half; the foreground
              // pre-check ran above). If a bubble carrying the same
              // `errorEntryId` is already in this bucket, the live error has
              // already been rendered — skip the toast + bubble push but
              // STILL close out streaming state (an idempotent re-close is a
              // no-op on already-closed bubbles), so a replay-late-arriving
              // live frame doesn't leave a bubble stuck streaming.
              if (errorDedupId) {
                const alreadyRendered = getMessages(b).some(
                  (m) => m.errorEntryId === errorDedupId,
                )
                if (alreadyRendered) {
                  return produce(b, (draft) => {
                    draft.isStreaming = false
                    if (clearReplayingNow) draft.isReplaying = false
                    // S7/S1: an error frame terminates a turn exactly like a
                    // done would — see the matching comment on the C8 sweep
                    // branch below for why every branch here clears this.
                    markTurnFinished(targetSid, draft.activeTurnId)
                    draft.activeTurnId = null
                    draft.activeTurnAgentId = null
                  }) as Partial<SessionChatState>
                }
              }
              const lastMsgId = findLastAssistantMessageId(b.messageOrder, b.messagesById)
              const lastMsg = lastMsgId ? b.messagesById[lastMsgId] : undefined
              // Mirror replay_error: only coalesce into lastMsgId when that
              // bubble is THIS turn (streaming, or an empty placeholder).
              // A second tab / reload has history and no placeholder —
              // lastMsgId is the last successful reply and must not be
              // restamped as the failure. SessionID delivery made that
              // live frame reach the second tab; this guard is the pair.
              const lastContentEmpty = !!lastMsg && (!lastMsg.content || !lastMsg.content.trim())
              // ADR-070 §2.6: `lastContentEmpty` alone would otherwise pull
              // an already-closed, closedBySteer bubble (content still ''
              // because the steer landed before any text streamed) into
              // this sweep and re-stamp it with the error — mirrors how
              // `lastIsTerminalError` already excludes 'error' below.
              const lastIsThisTurn = !!lastMsg && !lastMsg.closedBySteer && (
                lastMsg.isStreaming
                || lastMsg.status === 'streaming'
                || lastContentEmpty
              )
              const lastIsTerminalError = !!lastMsg
                && lastMsg.status === 'error'
                && !lastMsg.isStreaming
              const lastIsSameError = lastIsTerminalError
                && !!llmError
                && (
                  errorEntryId
                    ? lastMsg?.errorEntryId === errorEntryId
                    : (lastMsg?.errorCode === llmError.code
                      || lastMsg?.content === translatedMessage)
                )
              // C8: close every still-streaming assistant (a mid-turn steer
              // appends a USER message after the open bubble, so it is no
              // longer last). Include last only when it belongs to this turn.
              const streamingIds: string[] = []
              for (let i = b.messageOrder.length - 1; i >= 0; i--) {
                const id = b.messageOrder[i]
                const m = b.messagesById[id]
                if (m?.role !== 'assistant') continue
                const isLastThisTurn = id === lastMsgId && lastIsThisTurn && !lastIsTerminalError
                if (isLastThisTurn || m.isStreaming || m.status === 'streaming') {
                  // ADR-051 — when the incoming frame carries a typed
                  // payload, do NOT include an already-terminal 'error'
                  // bubble in the C8 close sweep. The sweep's purpose is to
                  // finalize in-flight streaming bubbles on a hard error; a
                  // PRIOR turn's already-error bubble (which lands here only
                  // because `id === lastMsgId` pulls in the last assistant
                  // message unconditionally) has nothing to close. Without
                  // this guard, a second typed ErrorFrame would re-close
                  // the first's bubble and (via the typed-field stamping
                  // below) overwrite its errorCode/errorEntryId, breaking
                  // the per-entry-id dedup contract (different entry_ids
                  // must yield different bubbles). Legacy frames (no typed
                  // payload) keep the pre-ADR-051 behavior — re-closing is
                  // an unobservable no-op for them, so the regression risk
                  // is zero.
                  if (
                    llmError
                    && m.status === 'error'
                    && !m.isStreaming
                  ) continue
                  streamingIds.push(id)
                }
              }
              if (streamingIds.length > 0) {
                return produce(b, (draft) => {
                  for (const id of streamingIds) {
                    const msg = draft.messagesById[id]
                    const prevStatus = msg.status
                    // FR-21 / T21–T23: do NOT overwrite 'interrupted' status with 'error'.
                    const resolvedStatus = (prevStatus === 'interrupted' || isCancelAck)
                      ? 'interrupted'
                      : 'error'
                    // ADR-051: when the typed payload is present, prefer the
                    // translated code→display copy as the bubble content —
                    // but only when the bubble has NO partial content yet
                    // (msg.content is empty). A bubble that already streamed
                    // narration text keeps that text as its content and just
                    // gets the typed error fields stamped on for the
                    // "Technical details" disclosure. Pre-ADR-051 fallback
                    // (`frame.message`, sanitized — D5 fix Site 3) is
                    // preserved when no typed payload.
                    const fallbackContent = llmError
                      ? translatedMessage
                      : safeMessage
                    msg.content = (resolvedStatus === 'interrupted')
                      ? msg.content
                      : (msg.content || (fallbackContent ?? ''))
                    msg.isStreaming = false
                    msg.status = resolvedStatus
                    msg.pendingTextBoundary = false
                    // ADR-051 — stamp typed fields on the closed bubble so
                    // the "Technical details" disclosure renders even when
                    // we coalesced into a streaming bubble instead of
                    // pushing a fresh error bubble below. ONLY stamp on the
                    // transition into 'error' (prevStatus !== 'error'): the
                    // C8 union sweep unconditionally includes the last
                    // assistant message even when it's already a finalized
                    // error bubble, so without this guard a second live
                    // error frame would overwrite the first's typed fields
                    // (same id, different entry_id → lost dedup signal).
                    // Cancel-acks and interrupted resolutions are also
                    // excluded by the resolvedStatus guard.
                    if (
                      resolvedStatus === 'error'
                      && prevStatus !== 'error'
                      && llmError
                    ) {
                      msg.errorCode = llmError.code
                      if (errorDetail !== undefined) msg.errorDetail = errorDetail
                      if (errorEntryId) msg.errorEntryId = errorEntryId
                      if (llmError.facts) msg.errorFacts = llmError.facts
                    }
                  }
                  draft.isStreaming = false
                  if (clearReplayingNow) {
                    draft.isReplaying = false
                  }
                  // S7/S1: a terminal error frame ends the turn exactly like
                  // a `done` would — clear the ADR-082 active-turn
                  // announcement (and record it finished, S2) here too, or a
                  // later, unrelated done for this session could misread a
                  // stale activeTurnId as "replay still awaiting catch-up"
                  // and open a stray empty placeholder.
                  markTurnFinished(targetSid, draft.activeTurnId)
                  draft.activeTurnId = null
                  draft.activeTurnAgentId = null
                }) as Partial<SessionChatState>
              }
              // Same catalogue already on the last bubble (replay drew it,
              // then the live frame arrived): do not mint a duplicate.
              if (lastIsSameError) {
                return produce(b, (draft) => {
                  draft.isStreaming = false
                  if (clearReplayingNow) draft.isReplaying = false
                  markTurnFinished(targetSid, draft.activeTurnId)
                  draft.activeTurnId = null
                  draft.activeTurnAgentId = null
                }) as Partial<SessionChatState>
              }
              // No this-turn assistant to coalesce into — last is a prior
              // healthy reply, or the bucket is empty. Push one new bubble
              // so the error is not silently dropped or written onto history.
              if (!isCancelAck) {
                // D5 fix (Site 3): safeMessage, not the raw frame.message.
                useConnectionStore.getState().setConnectionError(safeMessage)
              }
              // ADR-051 — fresh error bubble: use the translated copy when
              // the typed payload is present (matches the streaming-coalesce
              // branch above), else the sanitized legacy `frame.message`
              // (D5 fix Site 3). Stamp the typed fields so the "Technical
              // details" disclosure can render.
              const errMsg: ChatMessage = {
                id: generateId(),
                role: 'assistant',
                content: isCancelAck ? '' : (llmError ? translatedMessage : safeMessage),
                timestamp: new Date().toISOString(),
                status: isCancelAck ? 'interrupted' : 'error',
                isStreaming: false,
                ...(!isCancelAck && llmError
                  ? {
                      errorCode: llmError.code,
                      ...(errorDetail !== undefined ? { errorDetail } : {}),
                      ...(errorEntryId ? { errorEntryId } : {}),
                      ...(llmError.facts ? { errorFacts: llmError.facts } : {}),
                    }
                  : {}),
              }
              const msgs = [...getMessages(b), errMsg]
              markTurnFinished(targetSid, b.activeTurnId)
              return {
                ...applyMessageArray(msgs, b),
                isStreaming: false,
                ...(clearReplayingNow ? { isReplaying: false } : {}),
                activeTurnId: null,
                activeTurnAgentId: null,
              }
            })
            // The failed turn may have been one we sent from the offline-queue
            // drain — send the next queued message, if any.
            maybeDrainNext()
          }
          break

        case 'tool_call_start': {
          if (!targetSid) break
          // ADR-091 D7/D10: a tool call this session's own turn starts is
          // always flat, never nested under a subagent span — a genuine
          // child's own tool calls carry the CHILD's own session_id (I-4)
          // and never arrive here at all. The out-of-order step buffer
          // (`pendingByParentCallId`) and the O(1) span-attach path
          // (`spanByParentCallId`/`hasOpenSpanFast`) that used to nest a
          // `parent_call_id`-tagged frame into an open span are deleted.
          withBucket(targetSid, (b) => {
              // Anchor resolution. Default: the raw message-order tail, same
              // as before — this is REPLAY-SAFE and must stay unconditional
              // on role alone (NOT isStreaming): replay-reconstructed
              // assistant bubbles (see the 'replay_message' case) are
              // finalized (isStreaming:false) the INSTANT they're created —
              // that's by design, not a live "closed segment" — and
              // tool_call_start frames replayed right after them must still
              // reuse that same bubble (mirrors the 'replay_message' case's
              // own doc comment on why isStreaming can't serve as the
              // open/closed signal during replay).
              //
              // Fallback ONLY when the raw tail is NOT an assistant message:
              // this is the mid-turn-steer case (sendMessage's `isStreaming`
              // branch appends the steer text as a new USER message AFTER
              // the still-open assistant bubble, so the raw tail is no
              // longer assistant by the time this frame arrives) — anchor
              // instead on the last assistant message further back, but
              // ONLY if it is still genuinely STREAMING (never a
              // replay-finalized or turn-closed one — reusing a closed
              // bubble here would be the "text-then-image-at-bottom"
              // reconnect-bug class of mistake the 'token' handler's own
              // still-streaming check already guards against). Without this
              // fallback, tool_call_start would wrongly mint a brand-new
              // assistant placeholder while the ORIGINAL bubble is left
              // behind still marked isStreaming:true — nothing else ever
              // finalizes an abandoned bubble like that (the done/error
              // handlers below now sweep every still-streaming assistant
              // message, but only once a terminal frame actually arrives),
              // so it would render a permanent shimmer/spinner (Copy bar
              // suppressed) until a manual reload replayed history.
              const rawTailId = b.messageOrder[b.messageOrder.length - 1]
              const rawTail = rawTailId ? b.messagesById[rawTailId] : undefined
              let lastMsgId: string | undefined
              let lastMsg: ChatMessage | undefined
              if (rawTail?.role === 'assistant') {
                lastMsgId = rawTailId
                lastMsg = rawTail
              } else {
                const lastAssistantId = findLastAssistantMessageId(b.messageOrder, b.messagesById)
                const lastAssistantMsg = lastAssistantId ? b.messagesById[lastAssistantId] : undefined
                if (lastAssistantMsg?.isStreaming) {
                  lastMsgId = lastAssistantId ?? undefined
                  lastMsg = lastAssistantMsg
                }
              }
              const textSnapshot = (lastMsg?.role === 'assistant' ? lastMsg.content : '') ?? ''
              // Reconnect/replay safety: if this call_id is already recorded
              // (we have a textAtToolCallStart snapshot for it), keep the
              // ORIGINAL snapshot. A reattach replays from the start of the
              // transcript while the bucket already holds the completed
              // assistant text — without this guard every snapshot gets
              // overwritten with "end of full text", which makes the
              // runtime adapter render every tool call AFTER the text
              // (the "tool calls grouped at the bottom" reconnect bug).
              // Likewise, don't downgrade a tool call's status from
              // success/error back to running.
              const orderHasCall = b.toolCallOrder.includes(frame.call_id)
              const existingSnapshot = b.textAtToolCallStart[frame.call_id]
              const existingOwner = b.toolCallOwnerMessageId?.[frame.call_id]
              const existingTC = b.toolCalls[frame.call_id]
              // Defense-in-depth alongside stampToolCallOffset's prevOffset-
              // first invariant (see that function's doc comment): a
              // reconnect can replay `tool_call_start` for a call that has
              // ALREADY been baked into a historical message's tool_calls
              // (the done-bake / replay-merge bakes wipe toolCallOrder/
              // textAtToolCallStart, so orderHasCall/existingSnapshot alone
              // can't detect this). When that's the case, the frame must be
              // treated as a no-op start — see isToolCallBakedInBucket's doc
              // comment for why re-recording a snapshot or re-queuing the id
              // into toolCallOrder here would risk corrupting the already-
              // correct stamped offset. A still-streaming turn's calls
              // reattaching mid-turn are NOT yet baked into any message, so
              // this is false for them and they take the unchanged path below.
              const alreadyBaked = isToolCallBakedInBucket(b.messagesById, frame.call_id)
              // FR-21 / T21–T25: a tool_call_start for a top-level (non-subagent)
              // tool means the agent is actively working — set isStreaming:true so
              // the Stop button appears even when the LLM emits a tool call without
              // streaming any text first (e.g. glm-5v-turbo immediately calling
              // write_file).  Do not set it during replay (b.isReplaying) because
              // replay frames reconstruct history and should not trigger the spinner.
              const shouldMarkStreaming = !b.isReplaying
              return produce(b, (draft) => {
                // Owning message for THIS call — recorded below into
                // toolCallOwnerMessageId (reconnect/replay-safe: only set
                // when not already recorded, mirroring existingSnapshot).
                let ownerMsgId: string | null = null
                if (!lastMsg || lastMsg.role !== 'assistant') {
                  const ph: ChatMessage = { id: generateId(), role: 'assistant', content: '', timestamp: new Date().toISOString(), status: 'streaming', isStreaming: true, agentId: frame.agent_id ?? useSessionStore.getState().activeAgentId ?? undefined }
                  draft.messagesById[ph.id] = ph
                  draft.messageOrder.push(ph.id)
                  ownerMsgId = ph.id
                } else if (lastMsgId) {
                  ownerMsgId = lastMsgId
                  // A tool call is starting on the bubble that's still holding
                  // the delegator's own narration — flag the seam so the next
                  // token append (the post-tool-call continuation, or a
                  // synchronous delegate's own reply riding the same bubble)
                  // gets a paragraph break instead of gluing on with no
                  // separator. See `pendingTextBoundary` on ChatMessage.
                  draft.messagesById[lastMsgId].pendingTextBoundary = true
                  // Sync/await-mode delegation attribution fix: frame.agent_id
                  // on a tool_call_start is always the CALLER of the tool
                  // (pkg/agent/turn.go's appendToolCallTranscript stamps the
                  // invoking turnState's own resolveActiveAgentID; replay.go's
                  // buildStart sources it from the owning TranscriptEntry's
                  // AgentID) — for a top-level "delegate" call, that's the
                  // DELEGATOR, never the delegate. Stamp it here the same way
                  // the 'token' case already stamps from its own frame
                  // (Fix 5a) — without this, a bubble whose agentId is still
                  // unset (e.g. the optimistic placeholder sendMessage()
                  // creates, which carries no agentId at creation) stays
                  // unattributed through the entire tool call. In synchronous
                  // (await) delegation the delegator's OWN turn is blocked on
                  // the delegate and gets no chance to stamp the bubble with
                  // its own id first (unlike background delegation, where the
                  // delegator's turn continues and typically stamps the
                  // bubble before the delegate's async tokens ever arrive).
                  // The delegate's reply tokens then land first and, per the
                  // 'token' case's agent_id-boundary rule (only a KNOWN
                  // mismatch opens a new bubble), silently claim the whole
                  // bubble — including the delegator's own "Delegate task"
                  // tool-call card. Stamping here locks the bubble's identity
                  // to its rightful owner (the tool's caller) as soon as the
                  // call starts, so the boundary check correctly reroutes the
                  // delegate's later same-turn tokens to a new bubble instead
                  // (mirrors the already-correct async/background behavior).
                  if (frame.agent_id) {
                    draft.messagesById[lastMsgId].agentId = frame.agent_id
                  }
                }
                if (shouldMarkStreaming) draft.isStreaming = true
                if (!existingTC || existingTC.status === 'running') {
                  draft.toolCalls[frame.call_id] = { id: frame.call_id, call_id: frame.call_id, tool: frame.tool, params: frame.params, status: 'running' }
                }
                if (!orderHasCall && !alreadyBaked) draft.toolCallOrder.push(frame.call_id)
                if (existingSnapshot === undefined && !alreadyBaked) {
                  draft.textAtToolCallStart[frame.call_id] = textSnapshot
                }
                if (existingOwner === undefined && ownerMsgId) {
                  if (!draft.toolCallOwnerMessageId) draft.toolCallOwnerMessageId = {}
                  draft.toolCallOwnerMessageId[frame.call_id] = ownerMsgId
                }
              }) as Partial<SessionChatState>
            })
          break
        }

        case 'tool_call_result': {
          if (!targetSid) break
          const clampedResult = clampToolResult(frame.result)
          // ADR-091 D7/D10: see case 'tool_call_start' — every result here
          // is for this session's own flat tool call; no span-nesting path.
          withBucket(targetSid, (b) => applyToolCallResultFrame(b, frame, clampedResult))
          break
        }

        case 'subagent_start': {
          if (!targetSid) break
          const sf = frame as WsSubagentStartFrame
          withBucket(targetSid, (b) => {
            // Opus F4 — see bucketHasSpan's own doc comment.
            if (bucketHasSpan(b, sf.span_id)) return {}
            return produce(b, (draft) => {
              // ADR-070 §2.1/F2: a bare findLastAssistantMessageId scan here
              // would reattach this span to a closed, closedBySteer bubble
              // (positioned before a mid-turn steer's user message) if the
              // span is the first live frame to arrive after the steer.
              // findOpenAssistantMessageId refuses that; when it resolves to
              // null, open a fresh placeholder — mirroring
              // case 'tool_call_start' — rather than silently dropping a
              // real delegation span (worse than the mis-ordering itself).
              let lastMsgId = findOpenAssistantMessageId(draft.messageOrder, draft.messagesById)
              if (!lastMsgId) {
                const placeholder: ChatMessage = {
                  id: generateId(),
                  role: 'assistant',
                  content: '',
                  timestamp: new Date().toISOString(),
                  status: 'streaming',
                  isStreaming: true,
                  agentId: sf.agent_id ?? useSessionStore.getState().activeAgentId ?? undefined,
                }
                draft.messagesById[placeholder.id] = placeholder
                draft.messageOrder.push(placeholder.id)
                lastMsgId = placeholder.id
              }
              // ADR-091 D7/I-4 (cross-family review finding 19): a
              // subagent_message/subagent_state that arrived BEFORE this
              // subagent_start (replay-gap ordering) left its update parked
              // here, keyed by span_id — apply and clear it now rather than
              // seeding the span with nothing and waiting for a NEXT update
              // that may never come.
              const pendingUpdate = draft.pendingSpanUpdatesBySpanId?.[sf.span_id]

              // ADR-091 D7/I-4: `child_session_id` (additive, CP-0) is now
              // the open control's target — populated from whichever front
              // door launched it (`delegate` or `create_task`, both stamp
              // it — I-4). `producing_session_id`, the ADR-057 relabelling
              // workaround this delivery supersedes, is still present on the
              // wire until the coordinated deletion (not yet — the lead
              // sends it once the Go readers are gone) but is no longer
              // read here.
              const span: SubagentSpanRunning = {
                spanId: sf.span_id,
                parentCallId: sf.parent_call_id,
                taskLabel: sf.task_label,
                status: 'running',
                agentId: sf.agent_id,
                childSessionId: sf.child_session_id,
                statusLine: pendingUpdate?.statusLine,
                lifecycleState: pendingUpdate?.lifecycleState, hasRun: pendingUpdate?.hasRun,
                // Seeds the status line's "last update N s ago" fallback
                // (D7 table) from the moment the span itself appears — a
                // real subagent_message/subagent_state, each carrying its
                // own created_at, supersedes this the instant one arrives.
                // subagent_start carries no created_at of its own (wire
                // gap), so Date.now() is the only source — UNLESS a pending
                // update (above) already carries the true event time of the
                // update that arrived first.
                lastUpdateAt: pendingUpdate?.lastUpdateAt ?? new Date().toISOString(),
              }
              const lastMsg = draft.messagesById[lastMsgId]
              const spanIdx = (lastMsg.spans ?? []).length
              if (!lastMsg.spans) lastMsg.spans = []
              lastMsg.spans.push(span)
              // O(1) index keyed by span_id, consumed by the subagent_end/
              // _message/_state handlers below for O(1) lookup instead of a
              // backward linear scan.
              if (!draft.spanBySpanId) draft.spanBySpanId = {}
              draft.spanBySpanId[sf.span_id] = { messageId: lastMsgId, spanIdx }
              if (pendingUpdate && draft.pendingSpanUpdatesBySpanId) {
                delete draft.pendingSpanUpdatesBySpanId[sf.span_id]
              }
            }) as Partial<SessionChatState>
          })
          break
        }

        case 'subagent_end': {
          if (!targetSid) break
          const ef = frame as WsSubagentEndFrame
          withBucket(targetSid, (b) => {
            return produce(b, (draft) => {
              // Builds the terminal span record from whichever running span
              // (indexed or scanned) matched ef.span_id — shared by both
              // resolution paths below so they can never diverge.
              function buildTerminalSpan(existingSpan: SubagentSpan): SubagentSpanTerminal {
                return {
                  spanId: existingSpan.spanId,
                  parentCallId: existingSpan.parentCallId,
                  taskLabel: existingSpan.taskLabel,
                  // Defensive fallback: SubagentEndFrame carries its own optional
                  // agent_id; prefer it if the server ever populates it, else
                  // keep the value already stamped by subagent_start.
                  agentId: ef.agent_id ?? existingSpan.agentId,
                  // SubagentEndFrame carries no child_session_id of its own
                  // (only subagent_start does, I-4) — keep whatever
                  // subagent_start already stamped.
                  childSessionId: existingSpan.childSessionId,
                  statusLine: existingSpan.statusLine,
                  lifecycleState: existingSpan.lifecycleState, hasRun: existingSpan.hasRun,
                  lastUpdateAt: new Date().toISOString(),
                  status: ef.status,
                  durationMs: ef.duration_ms ?? 0,
                  finalResult: ef.final_result,
                  reason: ef.reason,
                }
              }

              // O(1) lookup first. Re-verify the indexed span's own id still
              // matches before trusting it — cheap, and guards against any
              // staleness (e.g. an index entry surviving a code path that
              // doesn't maintain it) rather than silently mutating the wrong
              // span.
              const indexEntry = draft.spanBySpanId?.[ef.span_id]
              const indexedMsg = indexEntry ? draft.messagesById[indexEntry.messageId] : undefined
              const indexedSpan = indexEntry ? indexedMsg?.spans?.[indexEntry.spanIdx] : undefined
              if (indexEntry && indexedMsg?.spans && indexedSpan && indexedSpan.spanId === ef.span_id) {
                indexedMsg.spans[indexEntry.spanIdx] = buildTerminalSpan(indexedSpan)
                delete draft.spanBySpanId![ef.span_id]
                return
              }

              // Fallback: O(N) scan (legacy path, index miss — e.g. a bucket
              // built before this index existed, or an out-of-band mutation
              // that didn't maintain it).
              console.warn('[chat] subagent_end: span index miss, falling back to O(N) scan', { spanId: ef.span_id })
              logDiagnostic('chatSubagentEndSpanIndexMiss', { spanId: ef.span_id, sessionId: targetSid })
              for (let i = draft.messageOrder.length - 1; i >= 0; i--) {
                const msgId = draft.messageOrder[i]
                const msg = draft.messagesById[msgId]
                if (msg.role !== 'assistant' || !msg.spans) continue
                const spanIdx = msg.spans.findIndex((s) => s.spanId === ef.span_id)
                if (spanIdx === -1) continue
                const existingSpan = msg.spans[spanIdx]
                msg.spans[spanIdx] = buildTerminalSpan(existingSpan)
                if (draft.spanBySpanId) delete draft.spanBySpanId[existingSpan.spanId]
                return
              }
              console.warn('[chat] subagent_end received for unknown span_id', { spanId: ef.span_id })
              logDiagnostic('chatSubagentEndUnknownSpanId', { spanId: ef.span_id, sessionId: targetSid })
            }) as Partial<SessionChatState>
          })
          break
        }

        // ADR-091 D7/I-4/FR-E-004, FR-E-010: wires what ADR-053 designed and
        // nothing ever emitted or consumed — the row's status line and
        // lifecycle state, reduced onto the span record (never nested as a
        // "step"; there is no child-step nesting left to feed, D10). Kept
        // beside subagent_start/_end (not in replay-and-status-frames.ts)
        // since all four subagent_* frames share one span-index lookup.
        case 'subagent_message': {
          if (!targetSid) break
          const mf = frame
          withBucket(targetSid, (b) => {
            return produce(b, (draft) => {
              // Ambiguity resolution (WP-E spec, accepted default): 'steer'
              // and 'respond' carry no `text` and render as the literal
              // string 'steered'; every other kind shows its `text` when
              // present, truncated to 120 characters with an ellipsis. A
              // kind with no text (e.g. a bare 'artifact' ping) leaves the
              // existing statusLine untouched rather than blanking it.
              const nextStatusLine =
                mf.kind === 'steer' || mf.kind === 'respond'
                  ? 'steered'
                  : mf.text
                    ? mf.text.length > 120 ? `${mf.text.slice(0, 120)}…` : mf.text
                    : undefined

              function applyToSpan(span: SubagentSpan): SubagentSpan {
                return {
                  ...span,
                  statusLine: nextStatusLine ?? span.statusLine,
                  lastUpdateAt: mf.created_at,
                }
              }

              const indexEntry = draft.spanBySpanId?.[mf.span_id]
              const indexedMsg = indexEntry ? draft.messagesById[indexEntry.messageId] : undefined
              const indexedSpan = indexEntry ? indexedMsg?.spans?.[indexEntry.spanIdx] : undefined
              if (indexEntry && indexedMsg?.spans && indexedSpan && indexedSpan.spanId === mf.span_id) {
                indexedMsg.spans[indexEntry.spanIdx] = applyToSpan(indexedSpan)
                return
              }
              console.warn('[chat] subagent_message: span index miss, falling back to O(N) scan', { spanId: mf.span_id })
              logDiagnostic('chatSubagentMessageSpanIndexMiss', { spanId: mf.span_id, sessionId: targetSid })
              for (let i = draft.messageOrder.length - 1; i >= 0; i--) {
                const msgId = draft.messageOrder[i]
                const msg = draft.messagesById[msgId]
                if (msg.role !== 'assistant' || !msg.spans) continue
                const spanIdx = msg.spans.findIndex((s) => s.spanId === mf.span_id)
                if (spanIdx === -1) continue
                msg.spans[spanIdx] = applyToSpan(msg.spans[spanIdx])
                return
              }
              // ADR-091 D7/I-4 (cross-family review finding 19): no span
              // exists yet anywhere in this session — a genuine replay-gap
              // ordering (this frame arrived before its span's own
              // subagent_start), not necessarily a real "unknown" frame.
              // Park the update; subagent_start applies and clears it.
              // `patch` omits `statusLine` entirely when there is nothing to
              // apply (mirrors applyToSpan's own `?? span.statusLine`
              // no-op above) so a PRIOR pending statusLine from an earlier
              // subagent_message for the same span_id is never overwritten
              // with `undefined`.
              console.warn('[chat] subagent_message received before its span\'s subagent_start — parked as a pending update', { spanId: mf.span_id })
              logDiagnostic('chatSubagentMessageReplayGap', { spanId: mf.span_id, sessionId: targetSid })
              draft.pendingSpanUpdatesBySpanId = recordPendingSpanUpdate(
                draft.pendingSpanUpdatesBySpanId,
                mf.span_id,
                nextStatusLine !== undefined
                  ? { statusLine: nextStatusLine, lastUpdateAt: mf.created_at }
                  : { lastUpdateAt: mf.created_at },
              )
            }) as Partial<SessionChatState>
          })
          break
        }

        case 'subagent_state': {
          if (!targetSid) break
          const sf2 = frame
          withBucket(targetSid, (b) => {
            return produce(b, (draft) => {
              function applyToSpan(span: SubagentSpan): SubagentSpan {
                return spanWithLifecycle(span, sf2.state, sf2.created_at)
              }

              const indexEntry = draft.spanBySpanId?.[sf2.span_id]
              const indexedMsg = indexEntry ? draft.messagesById[indexEntry.messageId] : undefined
              const indexedSpan = indexEntry ? indexedMsg?.spans?.[indexEntry.spanIdx] : undefined
              if (indexEntry && indexedMsg?.spans && indexedSpan && indexedSpan.spanId === sf2.span_id) {
                indexedMsg.spans[indexEntry.spanIdx] = applyToSpan(indexedSpan)
                return
              }
              console.warn('[chat] subagent_state: span index miss, falling back to O(N) scan', { spanId: sf2.span_id })
              logDiagnostic('chatSubagentStateSpanIndexMiss', { spanId: sf2.span_id, sessionId: targetSid })
              for (let i = draft.messageOrder.length - 1; i >= 0; i--) {
                const msgId = draft.messageOrder[i]
                const msg = draft.messagesById[msgId]
                if (msg.role !== 'assistant' || !msg.spans) continue
                const spanIdx = msg.spans.findIndex((s) => s.spanId === sf2.span_id)
                if (spanIdx === -1) continue
                msg.spans[spanIdx] = applyToSpan(msg.spans[spanIdx])
                return
              }
              // ADR-091 D7/I-4 (cross-family review finding 19): see the
              // matching comment in the 'subagent_message' case above —
              // same replay-gap parking, keyed by the same pending map.
              console.warn('[chat] subagent_state received before its span\'s subagent_start — parked as a pending update', { spanId: sf2.span_id })
              logDiagnostic('chatSubagentStateReplayGap', { spanId: sf2.span_id, sessionId: targetSid })
              draft.pendingSpanUpdatesBySpanId = recordPendingSpanUpdate(
                draft.pendingSpanUpdatesBySpanId,
                sf2.span_id,
                pendingLifecyclePatch(draft.pendingSpanUpdatesBySpanId?.[sf2.span_id], sf2.state, sf2.created_at),
              )
            }) as Partial<SessionChatState>
          })
          break
        }

        case 'task_status_changed':
          queryClient.invalidateQueries({ queryKey: ['tasks'] })
          break

        // D-107 (2026-09-14): a Library write landed on the REST surface —
        // usually in ANOTHER tab, which is the whole point. GLOBAL frame (no
        // session_id — it describes a workspace's file tree, not a chat).
        // Invalidate EVERY cached library query for the named workspace
        // (partial-key ['library', workspace_id] covers entries in every
        // folder, both include_hidden variants, and content) rather than
        // trying to compute which folders were affected — a wrong folder set
        // would reintroduce exactly the stale-listing defect this exists to
        // remove. The workspaces list carries entry_count, so it goes too.
        // The originating tab's redundant invalidate is a no-op (its own
        // mutation already invalidated), and the focus-path pull half lives
        // in useLibraryCrossTabRefresh for tabs that never see a WS event.
        //
        // F3 (2026-09-14, SILENT-FAILURES-rate-limits-dd25339bf.md): routed
        // through scheduleLibraryChangedInvalidate rather than invalidating
        // synchronously here — a burst of these frames (bulk trash, several
        // tabs writing at once) used to cost one full reload pass PER FRAME.
        // See that function's own doc comment for the debounce/coalesce
        // rationale; a real change still refreshes every open view, just
        // once per short window instead of once per frame.
        case 'library_changed':
          scheduleLibraryChangedInvalidate(frame.workspace_id)
          break

        // Per-task run history (ADR-050 / task-run-history-spec §3.8): fires
        // at run open AND close (not just terminal), so the calendar chip
        // flips to "In progress" immediately, not only on completion. Unlike
        // task_status_changed this frame carries no session_id (it is not
        // session-scoped — see SESSION_SCOPED_FRAME_TYPES above, deliberately
        // NOT listed there) since a run can be materialized for a future
        // occurrence with no session yet attached to Task itself. Invalidate
        // BOTH the occurrence overlay (every workspace/range/tz variant —
        // partial-key match) and this task's own run-history list so the
        // calendar chips and the Runs list (TaskRunsList) update live.
        case 'task_run_status':
          queryClient.invalidateQueries({ queryKey: ['tasks', 'occurrences'] })
          queryClient.invalidateQueries({ queryKey: tasksQueryKeys.runs(frame.task_id) })
          break

        case 'agent_switched': {
          const newAgentId = frame.agent_id
          const sessionStore = useSessionStore.getState()
          // Use the frame's session_id if present; fall back to active.
          const switchSid = frameSessionId ?? sessionStore.activeSessionId
          if (newAgentId) {
            // Precedence rule 1 (src/store/session.ts's AGENT PRECEDENCE
            // RULE): this frame reports a handover the BACKEND already
            // performed, so it outranks an explicit user pick rather than
            // being filtered out as a session-derived hint — the picker has
            // to name whoever is actually answering.
            sessionStore.applyServerAgentSwitch(switchSid, newAgentId)
          } else {
            // agent_id ABSENT/nil is the backend's deliberate wire shape for
            // a return-to-default switch (pkg/gateway/websocket.go's
            // agent_switched frame builder omits AgentId specifically for
            // that case). Without this branch the picker silently keeps
            // showing whoever the conversation was handed off to, since
            // `if (newAgentId)` above never fires.
            //
            // There is no dedicated "clear the server-side override" action,
            // so resolve the configured default agent the same way the rest
            // of the app already does — the cached `['agents']` list
            // (useChatAgents/AgentCard/WorkspaceTeamTab all key off
            // `Agent.default === true`, itself derived server-side from
            // `cfg.Agents.Defaults.DefaultAgentID`) — and apply it via the
            // same server-authoritative path as a named switch.
            const agents = queryClient.getQueryData<Agent[]>(['agents'])
            const defaultAgent = agents?.find((a) => a.default)
            if (defaultAgent) {
              sessionStore.applyServerAgentSwitch(switchSid, defaultAgent.id, defaultAgent.type)
            } else {
              // No cached agent list yet (e.g. first frame before the
              // AgentPicker/mention menu has mounted) — refetch it so a
              // subsequent read finds the default agent. Never silently do
              // nothing here: that is exactly the bug this branch fixes.
              queryClient.invalidateQueries({ queryKey: ['agents'] })
            }
          }
          queryClient.invalidateQueries({ queryKey: ['sessions'] })
          break
        }

        default:
          runtime.unknownFrameCount++
          console.warn('[chat] Unknown frame type', { type: (frame as { type?: string }).type, count: runtime.unknownFrameCount })
          logDiagnostic('chatUnknownFrameType', { frameType: (frame as { type?: string }).type, count: runtime.unknownFrameCount })
          if (runtime.unknownFrameCount >= UNKNOWN_FRAME_TOAST_THRESHOLD) {
            useUiStore.getState().addToast({
              message: "Server is sending events this UI doesn't understand — refresh to update.",
              variant: 'warning',
            })
            // Reset so we don't spam the toast on every subsequent unknown frame.
            runtime.unknownFrameCount = 0
          }
          break
      }

      // After processing a frame for the foreground session, re-sync foreground fields
      // in case withBucket targeted a non-foreground session (background sessions).
      // When the target was foreground, withBucket already synced; this call is idempotent.
      syncForeground()
    },
  }
}
