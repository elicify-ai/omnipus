// subagent_* span reduction, one rule set for every carrier (ADR-091 D7/I-4).
// Moved verbatim out of slices/frames.ts::handleFrame so the live frame path
// and the paged history paths (snapshot replay and REST pages) apply the same
// start/state/message/end rules. Pure over the bucket it is given: it returns
// the next bucket fields and never reads or writes the store's bucket itself.
import { produce } from 'immer'
import { generateId } from '@/lib/constants'
import { useSessionStore } from '@/store/session'
import { logDiagnostic } from '@/lib/telemetry'
import type {
  SubagentStartFrame,
  SubagentEndFrame,
  SubagentMessageFrame,
  SubagentStateFrame,
} from '@/lib/api/generated/asyncapi-types'
import { findOpenAssistantMessageId } from '../messages'
import type { ChatMessage, SessionChatState, SubagentSpan, SubagentSpanRunning, SubagentSpanTerminal } from '../types'

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

type SubagentMessageFields = Pick<SubagentMessageFrame, 'kind' | 'text' | 'created_at'>

/**
 * What one subagent_message reduces onto its span. 'steer'/'respond' carry no
 * text and read as 'steered'; every other kind shows its text truncated to 120
 * characters. A `not_delivered` line (FR-013) is NOT a status line: it is
 * recorded separately so Activity can show its own row.
 */
function reduceSubagentMessage(mf: SubagentMessageFields): {
  nextStatusLine: string | undefined
  notDelivered: { text?: string; at: string } | undefined
} {
  if (mf.kind === 'not_delivered') return { nextStatusLine: undefined, notDelivered: { text: mf.text, at: mf.created_at } }
  if (mf.kind === 'steer' || mf.kind === 'respond') return { nextStatusLine: 'steered', notDelivered: undefined }
  const nextStatusLine = mf.text ? (mf.text.length > 120 ? `${mf.text.slice(0, 120)}…` : mf.text) : undefined
  return { nextStatusLine, notDelivered: undefined }
}

/** Pending-slot patch for a message that beat its span's subagent_start; omits absent keys. */
function pendingMessagePatch(
  statusLine: string | undefined,
  notDelivered: { text?: string; at: string } | undefined,
  createdAt: string,
): PendingSpanUpdate {
  return {
    ...(statusLine !== undefined ? { statusLine } : {}),
    lastUpdateAt: createdAt,
    ...(notDelivered ? { notDelivered } : {}),
  }
}

/**
 * Lifecycle values that mean the child left the queue. `queued` does not.
 * Names are SubagentStateFrame.state (running / needs_input / paused / completed).
 * Kept at module scope so handleFrame's grandfathered line budget does not grow.
 */
const RAN_LIFECYCLE_STATES: ReadonlySet<string> = new Set(['running', 'needs_input', 'paused', 'completed'])

/** Once true, stays true. A later failed / cancelled / timed_out must not clear it. */
/** In-progress child states a recorded end makes stale. */
const IN_PROGRESS_LIFECYCLE: ReadonlySet<string> = new Set(['queued', 'running', 'needs_input'])
/** The child lifecycle a terminal end implies; parked keeps the child's own state (it waits for an answer). */
const LIFECYCLE_FOR_END: Partial<Record<SubagentEndFrame['status'], NonNullable<SubagentSpan['lifecycleState']>>> = {
  success: 'completed', error: 'failed', timeout: 'failed', cancelled: 'stopped', interrupted: 'stopped',
}

/**
 * The end is authoritative (#1266 r23): a recorded end settles a stale
 * in-progress lifecycle, so every lifecycle reader (Activity status, the
 * running-children count, the panel dot) agrees the helper finished. A
 * terminal lifecycle the child already reported, and a parked end, are kept.
 */
function settledLifecycle(span: SubagentSpan, endStatus: SubagentEndFrame['status']): Pick<SubagentSpan, 'lifecycleState' | 'hasRun'> {
  const settled = span.lifecycleState && IN_PROGRESS_LIFECYCLE.has(span.lifecycleState) ? LIFECYCLE_FOR_END[endStatus] : undefined
  if (!settled) return { lifecycleState: span.lifecycleState, hasRun: span.hasRun }
  return { lifecycleState: settled, hasRun: stickyHasRun(span.hasRun, settled) }
}

function stickyHasRun(previous: boolean | undefined, state: string): true | undefined {
  if (previous === true || RAN_LIFECYCLE_STATES.has(state)) return true
  return undefined
}

function spanWithLifecycle(
  span: SubagentSpan,
  state: NonNullable<SubagentSpan['lifecycleState']>,
  createdAt: string,
  interruptionReason?: SubagentSpan['interruptionReason'],
): SubagentSpan {
  const hasRun = stickyHasRun(span.hasRun, state)
  // The typed cause belongs to an 'interrupted' lifecycle only; any other
  // state ends the interruption and drops the cause with it.
  const reason = state === 'interrupted' ? (interruptionReason ?? span.interruptionReason) : undefined
  const next: SubagentSpan = { ...span, lifecycleState: state, lastUpdateAt: createdAt, interruptionReason: reason }
  if (hasRun) return { ...next, hasRun }
  return next
}

/** Omit hasRun when unset so a merge cannot clobber a sticky true with undefined. */
function pendingLifecyclePatch(
  previous: PendingSpanUpdate | undefined,
  state: NonNullable<SubagentSpan['lifecycleState']>,
  createdAt: string,
  interruptionReason?: PendingSpanUpdate['interruptionReason'],
): PendingSpanUpdate {
  const hasRun = stickyHasRun(previous?.hasRun, state)
  const reason = state === 'interrupted' ? interruptionReason : undefined
  if (hasRun) return { lifecycleState: state, lastUpdateAt: createdAt, hasRun, interruptionReason: reason }
  return { lifecycleState: state, lastUpdateAt: createdAt, interruptionReason: reason }
}

function applySubagentStart(b: SessionChatState, frame: SubagentStartFrame): Partial<SessionChatState> {
  const sf = frame as SubagentStartFrame
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
      interruptionReason: pendingUpdate?.lifecycleState === 'interrupted' ? pendingUpdate.interruptionReason : undefined,
      notDelivered: pendingUpdate?.notDelivered,
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
}

function applySubagentEnd(b: SessionChatState, frame: SubagentEndFrame, targetSid: string): Partial<SessionChatState> {
  const ef = frame as SubagentEndFrame
  return produce(b, (draft) => {
    // Builds the terminal span record from whichever running span
    // (indexed or scanned) matched ef.span_id — shared by both
    // resolution paths below so they can never diverge.
    function buildTerminalSpan(existingSpan: SubagentSpan): SubagentSpanTerminal {
      // The engine-owned typed cause (tool_limit / reconciliation_required) is
      // the crash-recovery feature's incomplete outcome. It is more specific
      // than the end status, so it names the lifecycle ('interrupted') and
      // carries the cause itself. Any other end keeps the release's
      // authoritative end mapping (#1266 r23) and the cause the span already
      // carried while its lifecycle is still 'interrupted'.
      const interruptedCause = ef.reason === 'tool_limit' || ef.reason === 'reconciliation_required' ? ef.reason : undefined
      const settled = settledLifecycle(existingSpan, ef.status)
      const lifecycleState = interruptedCause ? 'interrupted' : settled.lifecycleState
      const interruptionReason = interruptedCause
        ? interruptedCause
        : lifecycleState === 'interrupted' ? existingSpan.interruptionReason : undefined
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
        lifecycleState,
        hasRun: settled.hasRun,
        interruptionReason,
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
}

// ADR-091 D7/I-4/FR-E-004, FR-E-010: wires what ADR-053 designed and
// nothing ever emitted or consumed — the row's status line and
// lifecycle state, reduced onto the span record (never nested as a
// "step"; there is no child-step nesting left to feed, D10). Kept
// beside subagent_start/_end (not in replay-and-status-frames.ts)
// since all four subagent_* frames share one span-index lookup.
function applySubagentMessage(b: SessionChatState, frame: SubagentMessageFrame, targetSid: string): Partial<SessionChatState> {
  const mf = frame
  return produce(b, (draft) => {
    // Ambiguity resolution (WP-E spec, accepted default): 'steer'
    // and 'respond' carry no `text` and render as the literal
    // string 'steered'; every other kind shows its `text` when
    // present, truncated to 120 characters with an ellipsis. A
    // kind with no text (e.g. a bare 'artifact' ping) leaves the
    // existing statusLine untouched rather than blanking it.
    const { nextStatusLine, notDelivered } = reduceSubagentMessage(mf)

    function applyToSpan(span: SubagentSpan): SubagentSpan {
      return {
        ...span,
        statusLine: nextStatusLine ?? span.statusLine,
        lastUpdateAt: mf.created_at,
        ...(notDelivered ? { notDelivered } : {}),
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
      pendingMessagePatch(nextStatusLine, notDelivered, mf.created_at),
    )
  }) as Partial<SessionChatState>
}

function applySubagentState(b: SessionChatState, frame: SubagentStateFrame, targetSid: string): Partial<SessionChatState> {
  const sf2 = frame
  return produce(b, (draft) => {
    function applyToSpan(span: SubagentSpan): SubagentSpan {
      return spanWithLifecycle(span, sf2.state, sf2.created_at, sf2.interruption_reason)
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
      pendingLifecyclePatch(draft.pendingSpanUpdatesBySpanId?.[sf2.span_id], sf2.state, sf2.created_at, sf2.interruption_reason),
    )
  }) as Partial<SessionChatState>
}

export type SpanFrame = SubagentStartFrame | SubagentEndFrame | SubagentMessageFrame | SubagentStateFrame

/** Apply one subagent_* frame to `bucket`; `targetSid` names its session in diagnostics. */
export function applySpanFrame(bucket: SessionChatState, frame: SpanFrame, targetSid: string): Partial<SessionChatState> {
  switch (frame.type) {
    case 'subagent_start': return applySubagentStart(bucket, frame)
    case 'subagent_end': return applySubagentEnd(bucket, frame, targetSid)
    case 'subagent_message': return applySubagentMessage(bucket, frame, targetSid)
    case 'subagent_state': return applySubagentState(bucket, frame, targetSid)
  }
}
