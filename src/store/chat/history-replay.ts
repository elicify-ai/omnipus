// Snapshot projection staging, not a second transport/replay-frame buffer.
// The socket's render-once buffer still owns delivery through catch_up_complete.
import type { ServerFrame, SessionSnapshotFrame } from '@/lib/api/generated/asyncapi-types'
import type { HistorySliceContext } from './types'
import type { SessionHistoryEntry, SessionHistoryPage } from '@/lib/api/history-types'
import type { ChatMessage, PositionedToolCall, SessionChatState } from './types'
import { isProgressiveAttachFrame, isSupersededAttachFrame } from '@/lib/attach-replay'
import { detachedLivePatch, liveProjectionBucket, withLiveHistoryBucket } from './history-live'
import { bakeToolCallsByOwner, emptySessionState } from './session'
import { clampToolResult, getMessages } from './messages'
import { applyToolCallResultFrame } from './slices/frames'
import { refreshRosterAfterAgentMutation } from './agentMutationRefresh'
import { historyMetadata, historyRowsPatch, mergeHistoryEntry, retainedPending } from './history-state'
import { useSessionStore } from '@/store/session'
import { cursorFromTerminalFrame, fenceCursorAcrossBoots, isPendingTailMessage } from './cursor'
import { findFirstSendMessage } from './first-send'
import { abortHistoryReads, registerHistoryRelease } from './history'
import { applyHistoryPageToBucket, HistoryPageProtocolError, releaseHistorySpanFacts } from './history-merge'
import { ApiSchemaError } from '@/lib/api/http'
import { logDiagnostic } from '@/lib/telemetry'
import { validateHistoryMetadata, historyPosition } from '@/lib/api/history-validation'
import { SessionHistoryResponse as PageSchema } from '@/lib/api/generated/schemas'

type Stage = { frame: SessionSnapshotFrame; groups: Map<string, SessionChatState>; generation: number; baseline: Record<string, ChatMessage>; invalid: boolean; progressive: boolean; published: boolean; priorOrder: string[]; priorHistory: SessionChatState['history']; publishedRows: Record<string, ChatMessage>; cancellations: Set<string>; priorBootId: string | undefined } // not-wire-format: transient projection ownership

/** Local classification only; never carries source data or exception details. */
class SnapshotProtocolError extends Error {
  constructor(readonly kind: 'schema' | 'source_origin' | 'row_count') {
    super('Invalid history snapshot.')
  }
}

/** Row weight per counted source, exactly as the server weighs the record
 * (pkg/session/chat_history_reduce.go): one row per tool call plus one
 * narration row when it has text or no tools at all -- a content-less turn
 * start still counts -- and none for a cancellation marker. */
function sourceProjection(groups: Stage['groups'], cancellations: ReadonlySet<string> = new Set()) {
  const positions = [...groups.keys()]
  const entries: SessionHistoryEntry[] = []
  let rowCount = 0
  for (const [position, group] of groups) {
    for (const row of getMessages(group)) {
      const tools = new Map<string, PositionedToolCall>(row.tool_calls?.map(call => [call.id, call]) ?? [])
      for (const call of Object.values(group.toolCalls)) tools.set(call.id, { ...call, textOffset: group.textAtToolCallStart[call.id]?.length })
      entries.push({ ...row, historyPosition: position, ...(tools.size ? { tool_calls: [...tools.values()] } : {}) } as SessionHistoryEntry)
      if (!cancellations.has(position)) rowCount += Number(!!row.content || tools.size === 0) + tools.size
    }
  }
  return { positions, entries, rowCount }
}

/** Restore the prior reader. With `failed`, the SAME store update also
 * publishes the failed load ("Could not load messages." + Retry), so a
 * subscriber exception during restoration cannot strand a restored reader
 * that still claims a successful load. */
function restorePublishedProjection(context: HistorySliceContext, sid: string, stage: Stage, beforeCommit?: SessionChatState, failed = false): void {
  if (!stage.published && !beforeCommit) return
  context.withBucket(sid, b => {
    if (b.history.viewGeneration !== stage.generation || (!beforeCommit && b.history.historyLoaded)) return {}
    // Progressive rows are still provisional: on failure restore the pre-stage
    // reader, not the partially published page captured just before commit.
    const prior = stage.published ? undefined : beforeCommit
    const baseline = prior?.messagesById ?? stage.baseline
    const history = prior?.history ?? stage.priorHistory
    const hidden = new Set(prior?.historyLiveOrder ?? [])
    const priorOrder = prior ? [...new Set([...prior.messageOrder, ...hidden])] : stage.priorOrder
    const rows = priorOrder.flatMap(id => {
      const old = baseline[id]
      const current = b.messagesById[id]
      return old ? [current && !current.historyPosition ? current : old] : []
    })
    for (const pending of retainedPending(b)) if (!pending.historyPosition && !rows.some(row => row.id === pending.id)) rows.push(pending)
    const patch = historyRowsPatch(prior ?? b, rows, history.readerPositions)
    return { ...patch,
      ...(prior ? { messageOrder: patch.messageOrder!.filter(id => !hidden.has(id)), historyLiveOrder: [...hidden], recoveredFirstSend: prior.recoveredFirstSend } : {}),
      history: { ...history, viewGeneration: b.history.viewGeneration, serverState: b.history.serverState, liveControlsReady: b.history.liveControlsReady,
        ...(failed ? { historyLoaded: false, requests: failedLatest(b.history.requests) } : { requests: b.history.requests }) } }
  })
}
function failedLatest(requests: SessionChatState['history']['requests']): SessionChatState['history']['requests'] {
  return { ...requests, latest: { status: 'error', error: 'Could not load messages.', attempt: requests.latest.attempt + 1 } }
}

function publishProgressiveProjection(context: HistorySliceContext, sid: string, stage: Stage): void {
  if (!stage.progressive || stage.invalid) return
  const hadReader = stage.priorOrder.some(id => { const row = stage.baseline[id]; return row && !isPendingTailMessage(row) && !row.isStreaming })
  if (hadReader) return // A real cached/detached reader never becomes a partial latest page.
  context.withBucket(sid, b => {
    if (b.history.viewGeneration !== stage.generation || b.history.viewMode !== 'latest' || (stage.published && b.history.historyLoaded)) return {}
    const { entries, positions } = sourceProjection(stage.groups)
    const ids = new Set(entries.map(row => row.id))
    const pending = retainedPending(b).filter(row => !row.historyPosition && !ids.has(row.id))
    const rows = [...entries, ...pending]
    stage.published = true
    stage.publishedRows = Object.fromEntries(rows.map(row => [row.id, row]))
    const patch = historyRowsPatch(b, rows, positions)
    return { ...patch, history: { ...patch.history!, historyLoaded: false, pageMetadata: null, hasEarlier: null, hasLater: null, olderBoundary: null, newerBoundary: null } }
  })
}

/** Reconcile an explicitly recovered input only after the selected page validates. */
function settleRecoveredSnapshotInput(bucket: SessionChatState, page: SessionHistoryPage): SessionChatState {
  const recovery = bucket.recoveredFirstSend
  if (!recovery) return bucket
  const recorded = page.entries.find(entry => entry.role === 'user' && entry.clientMessageId === recovery.clientMessageId)
  const original = findFirstSendMessage(bucket, recovery.clientMessageId)
  if (!recorded || recorded.role !== 'user' || !original || original.role !== 'user') return bucket
  const messagesById = { ...bucket.messagesById }
  delete messagesById[original.id]
  // The recorded identity/content wins; local attachment details and first-send
  // state survive when the replay carrier does not repeat them.
  messagesById[recorded.id] = { ...original, ...recorded, deliveryStatus: recorded.deliveryStatus ?? 'received' }
  const rekey = (id: string) => id === original.id ? recorded.id : id
  return {
    ...bucket, messagesById,
    messageOrder: [...new Set(bucket.messageOrder.map(rekey))],
    historyLiveOrder: bucket.historyLiveOrder?.map(rekey),
    recoveredFirstSend: { ...recovery, reconciled: true },
  }
}

/** A reply this tab watched streaming whose turn the server's current state
 * no longer runs, and that the committed page does not hold, did not survive
 * on the server (a restart cut it before it was saved). Dropping it at the
 * snapshot commit restores the pre-paging rebuild: the question it answered is
 * the last row again and the witnessed-cut sweep reads the real transcript.
 * A turn the server still reports active, any row the page holds (also via a
 * merged replay id), and every user row stay untouched. */
function dropDeadWitnessedStreams(bucket: SessionChatState, page: SessionHistoryPage): SessionChatState {
  const witnessed = new Set(bucket.wipedOpenTurnIds ?? [])
  if (!witnessed.size) return bucket
  const onPage = new Set(page.entries.map(entry => entry.id))
  const dead = new Set([...new Set([...bucket.messageOrder, ...(bucket.historyLiveOrder ?? [])])].filter(id => {
    const row = bucket.messagesById[id]
    return row?.role === 'assistant' && (row.isStreaming || row.status === 'streaming') && !row.historyPosition &&
      !!row.turnId && witnessed.has(row.turnId) && row.turnId !== bucket.activeTurnId &&
      !onPage.has(row.id) && !row.mergedReplayIds?.some(id => onPage.has(id))
  }))
  if (!dead.size) return bucket
  const messagesById = { ...bucket.messagesById }
  for (const id of dead) delete messagesById[id]
  // With the dead reply gone, the chat streams only if a row still does or the
  // server reports a turn; a stale flag would read as "still running".
  const stillStreaming = !!bucket.activeTurnId || Object.values(messagesById).some(row => row.isStreaming || row.status === 'streaming')
  return { ...bucket, messagesById, isStreaming: bucket.isStreaming && stillStreaming,
    messageOrder: bucket.messageOrder.filter(id => !dead.has(id)), historyLiveOrder: bucket.historyLiveOrder?.filter(id => !dead.has(id)) }
}

function settleCompletedSnapshot(bucket: SessionChatState, page: SessionHistoryPage, baseline: Record<string, ChatMessage>, bootChanged: boolean): { bucket: SessionChatState; page: SessionHistoryPage } {
  if (bucket.history.serverState?.status !== 'available' || bucket.activeTurnId) return { bucket, page }
  const messagesById = { ...bucket.messagesById }
  const settledTurns = new Set<string>()
  let refreshed = false
  const entries = page.entries.map(entry => {
    // Live first-send narration can retain its generated display ID while the
    // token resolver registers the real message_id in mergedReplayIds. Match
    // only that actual alias, with the same turn/producer, never text or turn alone.
    const aliases = !bucket.messagesById[entry.id] && entry.turnId
      ? Object.values(bucket.messagesById).filter(row =>
        row.role === 'assistant' && row.turnId === entry.turnId && row.mergedReplayIds?.includes(entry.id) &&
        (!entry.agentId || !row.agentId || entry.agentId === row.agentId) &&
        // A live bubble can aggregate several IDs. Never collapse distinct
        // selected source occurrences onto that single display identity.
        !page.entries.some(other => other !== entry && (other.id === row.id || row.mergedReplayIds?.includes(other.id))))
      : []
    const current = bucket.messagesById[entry.id] ?? (aliases.length === 1 ? aliases[0] : undefined)
    const captured = current && baseline[current.id]
    if (entry.role !== 'assistant' || entry.status !== 'done' || current?.role !== 'assistant' || !captured) return entry
    if (current.content !== captured.content || (!current.isStreaming && current.status !== 'streaming')) return entry
    if (current.status === 'error' || current.status === 'interrupted') return entry
    // Keep the established display/tool-owner identity. The successful saved
    // occurrence replaces this unchanged prefix, not a second rendered row.
    const aligned = current.id === entry.id ? entry : { ...entry, id: current.id }
    messagesById[current.id] = { ...mergeHistoryEntry(aligned, current, captured), content: entry.content, status: entry.status, isStreaming: false, confirmedUnfinished: undefined }
    if (current.turnId) settledTurns.add(current.turnId)
    refreshed = true
    return aligned
  })
  if (!refreshed) return { bucket, page }
  const stillOpen = Object.values(messagesById).some(row => row.role === 'assistant' && (row.isStreaming || row.status === 'streaming'))
  return {
    bucket: {
      ...bucket, messagesById, isStreaming: stillOpen ? bucket.isStreaming : false,
      // A changed boot retains the personally witnessed-cut check. A normal
      // completed snapshot must not relabel its completed answer as unfinished.
      wipedOpenTurnIds: bootChanged ? bucket.wipedOpenTurnIds : bucket.wipedOpenTurnIds?.filter(id => !settledTurns.has(id)),
    },
    page: { ...page, entries },
  }
}

/** A persisted span fact is its own silent source row. Reducing it alone in
 * its isolated group could never meet the span's start (another position,
 * possibly another page); the page commit settles it onto the start row
 * (history-merge.ts::settleHistorySpans), exactly as REST pages do. */
function stageSpanFact(stage: Stage, frame: ServerFrame, entryId: string | undefined, position: string, previous: SessionChatState): boolean {
  if (frame.type !== 'subagent_start' && frame.type !== 'subagent_state' && frame.type !== 'subagent_message' && frame.type !== 'subagent_end') return false
  const id = entryId ?? `history-${position}`
  const row: ChatMessage = { id, role: 'assistant', content: '', timestamp: '1970-01-01T00:00:00Z', status: 'done', historyPosition: position, historySpanFrame: frame }
  stage.groups.set(position, { ...previous, messagesById: { [id]: row }, messageOrder: [id] })
  return true
}

/** D7: only a complete, applied snapshot may claim its cut. A rejected page
 * keeps the prior cursor, so the next live frame re-attaches from the position
 * this reader actually holds; a skipped page (a newer view owns the bucket)
 * still takes the frame's cursor. Current controls complete either way.
 * F2: when the rejected page came from ANOTHER boot, the kept cursor is fenced
 * so the new boot's first sequenced frame re-attaches instead of comparing its
 * new-boot seq with the old boot's number and being dropped. */
function completeUncommittedSnapshot(context: HistorySliceContext, sid: string, frame: Extract<ServerFrame, { type: 'catch_up_complete' }>, outcome: 'rejected' | 'skipped'): void {
  context.withBucket(sid, b => {
    // Cross-boot only when BOTH boots are known: an empty bootId (a cursor
    // minted from boot-less live frames) is an unknown boot, not a different one.
    const crossBoot = outcome === 'rejected' && b.cursor != null && !!b.cursor.bootId && !!frame.boot_id && frame.boot_id !== b.cursor.bootId
    return { ...(outcome === 'skipped' ? { cursor: cursorFromTerminalFrame(frame, b.cursor?.bootId) } : crossBoot && b.cursor ? { cursor: fenceCursorAcrossBoots(b.cursor) } : {}),
      awaitingCatchUp: false, isReplaying: false, history: { ...b.history, liveControlsReady: true } }
  })
}

export function createHistoryFrameOwner(context: HistorySliceContext) {
  const stages = new Map<string, Stage>()
  const discardedSnapshots = new Set<string>()
  registerHistoryRelease(sid => {
    releaseHistorySpanFacts(sid ?? undefined)
    if (!sid) { for (const [id, stage] of stages) restorePublishedProjection(context, id, stage); stages.clear(); discardedSnapshots.clear(); return }
    const stage = stages.get(sid)
    if (stage) { restorePublishedProjection(context, sid, stage); discardedSnapshots.add(sid) }
    stages.delete(sid)
  })
  let active: { sid: string; bucket: SessionChatState; staging?: boolean } | null = null
  const wrappedBucket: HistorySliceContext['withBucket'] = (sid, update) => {
    if (active && sid === active.sid) active.bucket = { ...active.bucket, ...update(active.bucket) }
    else context.withBucket(sid, update)
  }
  const wrappedGet: HistorySliceContext['get'] = () => {
    const state = context.get()
    if (!active) return state
    // Isolated source reconstruction must not settle the root first-send owner,
    // including when the same recorded row is replayed twice before COMPLETE.
    return { ...state, ...active.bucket, ...(active.staging ? { pendingFirstSend: null } : {}), messages: getMessages(active.bucket), sessionsById: { ...state.sessionsById, [active.sid]: active.bucket } }
  }
  function reject(sid: string): void {
    context.withBucket(sid, b => ({ history: { ...b.history, historyLoaded: false, requests: failedLatest(b.history.requests) } }))
  }
  /** Restore and reject as one update; a store subscriber that throws again
   * during that update is diagnosed by its own fixed class, never re-thrown
   * past the already-published failure state. */
  function failSnapshot(sid: string, stage: Stage, beforeCommit?: SessionChatState): void {
    try {
      restorePublishedProjection(context, sid, stage, beforeCommit, true)
    } catch {
      logDiagnostic('chatHistorySnapshotRestoreFailed', { phase: 'snapshot_restore', kind: 'unexpected_exception' })
    }
    // Decide from the STORED state, not from whether the restoration started:
    // an exception before Zustand assigned it leaves the failed load
    // unpublished; a subscriber exception after assignment does not. A view
    // that moved on is not this snapshot's to fail. If this minimal visible
    // failure update itself throws, it propagates.
    const b = context.get().sessionsById[sid]
    if (b && b.history.viewGeneration === stage.generation && (b.history.historyLoaded || b.history.requests.latest.status !== 'error')) reject(sid)
  }
  function begin(frame: SessionSnapshotFrame): void {
    abortHistoryReads(frame.session_id)
    discardedSnapshots.delete(frame.session_id)
    context.withBucket(frame.session_id, b => ({ history: { ...b.history, viewGeneration: b.history.viewGeneration + 1,
      requests: { ...b.history.requests,
        latest: b.history.serverState?.status === 'unavailable' || b.history.requests.latest.error
          ? { ...b.history.requests.latest, status: 'loading', error: b.history.serverState?.status === 'unavailable' ? b.history.serverState.message ?? 'Could not load messages.' : b.history.requests.latest.error }
          : b.history.requests.latest,
        older: { status: 'idle', error: null, attempt: 0 }, newer: { status: 'idle', error: null, attempt: 0 }, refresh: { status: 'idle', error: null, attempt: 0 } } } }))
    const bucket = context.get().sessionsById[frame.session_id]
    stages.set(frame.session_id, { frame, groups: new Map(), generation: bucket.history.viewGeneration, baseline: { ...bucket.messagesById }, invalid: false,
      progressive: isProgressiveAttachFrame(frame), published: false, cancellations: new Set(), priorOrder: bucket.messageOrder.slice(), priorHistory: bucket.history, publishedRows: {},
      // The live cursor's boot before this snapshot (undefined: no cursor yet,
      // so no boot to compare). A different snapshot boot means the gateway
      // restarted, whatever the snapshot reason says.
      priorBootId: bucket.cursor?.bootId })
  }
  function collect(frame: ServerFrame, sid: string, stage: Stage, reduce: (frame: ServerFrame) => void): void {
    const origin = 'history_origin' in frame ? frame.history_origin : undefined
    if (!origin) return
    let position: string
    try { position = historyPosition(origin.position, 'WS history', ['history_origin', 'position']) } catch { stage.invalid = true; return }
    if (frame.type === 'replay_message' && frame.role === 'turn_canceled') stage.cancellations.add(position)
    const previous = stage.groups.get(position) ?? { ...emptySessionState(), isReplaying: true, awaitingCatchUp: true }
    if (stageSpanFact(stage, frame, origin.entry_id, position, previous)) { publishProgressiveProjection(context, sid, stage); return }
    active = { sid, bucket: previous, staging: true }
    // A tool-only owning source is a real non-prose group, not the preceding
    // assistant bubble. The ordinary tool adapters then retain exact offsets.
    if ((frame.type === 'tool_call_start' || frame.type === 'tool_call_result') && !active.bucket.messageOrder.length) {
      const id = origin.entry_id ?? `history-${position}`
      const message: ChatMessage = { id, role: 'assistant', content: '', timestamp: '1970-01-01T00:00:00Z', status: 'done', historyPosition: position }
      active.bucket = { ...active.bucket, messagesById: { [id]: message }, messageOrder: [id], historyPendingNarrationId: id }
    }
    try {
      reduce(frame)
      if (frame.type === 'replay_message' && (frame.role ?? 'assistant') === 'assistant') active.bucket = { ...active.bucket, historyPendingNarrationId: undefined }
      if (!active.bucket.messageOrder.length) {
        const id = origin.entry_id ?? `history-${position}`
        const message: ChatMessage = { id, role: 'assistant', content: '', timestamp: '1970-01-01T00:00:00Z', status: 'done', historyPosition: position }
        active.bucket = { ...active.bucket, messagesById: { [id]: message }, messageOrder: [id] }
      }
      stage.groups.set(position, active.bucket)
      publishProgressiveProjection(context, sid, stage)
    } finally { active = null }
  }
  function mergeProjectionResult(frame: Extract<ServerFrame, { type: 'tool_call_result' }>, sid: string, stage: Stage): boolean {
    const owns = (bucket: SessionChatState) => !!bucket.toolCalls[frame.call_id] || Object.values(bucket.messagesById).some(row => row.tool_calls?.some(call => call.id === frame.call_id))
    const owner = [...stage.groups].reverse().find(([, group]) => owns(group))
    if (!owner) return false
    const [position, group] = owner
    const result = clampToolResult(frame.result)
    stage.groups.set(position, { ...group, ...applyToolCallResultFrame(group, frame, result) })
    // A selected start is not result coverage. Keep the real terminal value in
    // its staged source even when no start was repeated in active projection.
    withLiveHistoryBucket(context.withBucket, sid, bucket => owns(bucket) ? applyToolCallResultFrame(bucket, frame, result) : {})
    refreshRosterAfterAgentMutation(frame)
    return true
  }
  /** committed: the page is published. rejected: the staged page failed
   * validation or commit (failSnapshot restored the prior reader). skipped:
   * a newer view owns the bucket, or this page is already published. */
  function finish(sid: string, stage: Stage): 'committed' | 'rejected' | 'skipped' {
    const bucket = context.get().sessionsById[sid]
    if (!bucket || bucket.history.viewGeneration !== stage.generation || (stage.published && bucket.history.historyLoaded)) return 'skipped'
    let beforeCommit: SessionChatState | undefined
    try {
      const { positions, entries, rowCount } = sourceProjection(stage.groups, stage.cancellations)
      const baseline = { ...stage.baseline, ...stage.publishedRows }
      const { frame } = stage
      const parsed = PageSchema.safeParse({ ...frame, entries: [] })
      if (!parsed.success) throw new SnapshotProtocolError('schema')
      if (stage.invalid) throw new SnapshotProtocolError('source_origin')
      validateHistoryMetadata(parsed.data, positions, 'WS history')
      if (rowCount !== frame.page_info.row_count) throw new SnapshotProtocolError('row_count')
      const page: SessionHistoryPage = { ...parsed.data, entries }
      context.withBucket(sid, original => {
        // Zustand may publish this projection before a subscriber throws. Keep
        // the immediate prior reader so that failure is still transactional.
        beforeCommit = original
        const recovered = settleRecoveredSnapshotInput(original, page)
        // A restarted gateway keeps the witnessed-cut check, also when the
        // snapshot reason is unknown_position: the boot the live stream ran
        // on is gone, so a witnessed open turn did not finish there. Same
        // definition as the catch_up_complete reducer's bootChanged.
        const bootChanged = frame.reason === 'boot_mismatch' || (stage.priorBootId !== undefined && frame.boot_id !== undefined && frame.boot_id !== stage.priorBootId)
        const { bucket: b, page: completedPage } = settleCompletedSnapshot(recovered, page, baseline, bootChanged)
        const completion = b === original ? {} : { isStreaming: b.isStreaming, wipedOpenTurnIds: b.wipedOpenTurnIds, recoveredFirstSend: b.recoveredFirstSend }
        if (b.history.viewMode === 'historical') {
          const needed = new Set(retainedPending(b).map(row => row.id))
          const liveOrder = (b.historyLiveOrder ?? []).filter(id => !stage.baseline[id] || needed.has(id))
          const liveIds = new Set(liveOrder)
          const incoming = new Map(completedPage.entries.map(row => [row.id, row]))
          const rows = [...new Set([...b.messageOrder, ...liveOrder])].flatMap(id => {
            const current = b.messagesById[id]
            const fresh = incoming.get(id)
            return current ? [fresh && (liveIds.has(id) || (stage.publishedRows[id] && current.historyPosition === fresh.historyPosition)) ? mergeHistoryEntry(fresh, current, baseline[id]) : current] : []
          })
          const patch = historyRowsPatch(b, rows, b.history.readerPositions)
          const capacity = Math.max(0, 100 - liveOrder.length)
          const tail = completedPage.entries.filter(row => !liveIds.has(row.id))
          return { ...patch, ...completion, messageOrder: b.messageOrder, historyLiveOrder: liveOrder, historyNeedsSnapshot: false,
            history: { ...patch.history!, tailEntries: capacity ? tail.slice(-capacity) : [], pageMetadata: historyMetadata(completedPage), historyLoaded: true } }
        }
        const live = dropDeadWitnessedStreams(b, completedPage)
        return { ...applyHistoryPageToBucket(live, completedPage, 'latest', baseline), ...completion, ...(live !== b ? { isStreaming: live.isStreaming } : {}) }
      })
      return 'committed'
    } catch (error) {
      const kind = error instanceof SnapshotProtocolError ? error.kind
        : error instanceof ApiSchemaError || error instanceof HistoryPageProtocolError ? 'protocol_metadata'
          : 'unexpected_exception'
      // Only fixed classifications leave this boundary. Never log error text,
      // stack, source bodies/identities or opaque source positions.
      logDiagnostic('chatHistorySnapshotRejected', { phase: 'snapshot_commit', kind })
      failSnapshot(sid, stage, beforeCommit)
      return 'rejected'
    }
  }
  function handle(frame: ServerFrame, reduce: (frame: ServerFrame) => void): void {
    if (isSupersededAttachFrame(frame)) {
      if (frame.type === 'session_snapshot') { stages.delete(frame.session_id); discardedSnapshots.add(frame.session_id) }
      if (frame.type === 'catch_up_complete') {
        stages.delete(frame.session_id); discardedSnapshots.delete(frame.session_id)
        // This serialized response cannot certify a newer view, but its real
        // live boundary is still needed to apply the held seq>W frames.
        context.withBucket(frame.session_id, b => {
          const cursor = cursorFromTerminalFrame(frame, b.cursor?.bootId)
          return b.cursor?.bootId === cursor.bootId && b.cursor.seq > cursor.seq ? {} : { cursor }
        })
      }
      return
    }
    const sid = 'session_id' in frame ? frame.session_id : frame.type === 'ask_user_question' ? frame.card.session_id : undefined
    for (const [id, stage] of stages) if (context.get().sessionsById[id]?.history.viewGeneration !== stage.generation) stages.delete(id)
    if (frame.type === 'session_snapshot') {
      if (useSessionStore.getState().activeSessionId !== frame.session_id) {
        abortHistoryReads(frame.session_id)
        discardedSnapshots.add(frame.session_id)
        context.withBucket(frame.session_id, () => ({ historyNeedsSnapshot: true }))
        return
      }
      begin(frame)
    }
    const stage = sid ? stages.get(sid) : undefined
    if (sid && 'history_origin' in frame && frame.history_origin && !('seq' in frame && frame.seq !== undefined)) {
      if (stage) { collect(frame, sid, stage, reduce); return }
      if (discardedSnapshots.has(sid)) return
      // A valid standalone carrier may also carry origin facts; absence of a
      // snapshot stage alone does not make it an obsolete attach response.
    }
    if (sid && discardedSnapshots.has(sid)) {
      if (frame.type === 'done' && !frame.seq && !frame.turn_id) {
        if (frame.stats?.replay_error) discardedSnapshots.delete(sid)
        return
      }
      if (frame.type === 'catch_up_complete') {
        discardedSnapshots.delete(sid)
        context.withBucket(sid, b => ({ cursor: cursorFromTerminalFrame(frame, b.cursor?.bootId), awaitingCatchUp: false, isReplaying: false,
          history: { ...b.history, liveControlsReady: true } }))
        return // Current controls completed; this discarded page did not load.
      }
    }
    if (sid && stage && frame.type === 'tool_call_result' && frame.seq === undefined && mergeProjectionResult(frame, sid, stage)) return
    // Replay done bakes historical tool state only. It cannot close a current
    // turn or commit the source page; active projection may follow it.
    if (sid && stage && frame.type === 'done' && !frame.seq && !frame.turn_id) {
      if (frame.stats?.replay_error) { stages.delete(sid); failSnapshot(sid, stage); return }
      for (const [position, bucket] of stage.groups) {
        const messagesById = { ...bucket.messagesById }
        const fallback = [...bucket.messageOrder].reverse().find(id => messagesById[id]?.role === 'assistant') ?? null
        bakeToolCallsByOwner(messagesById, bucket.toolCallOrder, bucket.toolCalls, bucket.toolCallOwnerMessageId ?? {}, fallback, bucket.textAtToolCallStart)
        stage.groups.set(position, { ...bucket, messagesById, isStreaming: false, toolCalls: {}, toolCallOrder: [], toolCallOwnerMessageId: {}, textAtToolCallStart: {} })
      }
      publishProgressiveProjection(context, sid, stage)
      return
    }
    if (sid && stage && frame.type === 'catch_up_complete') {
      stages.delete(sid)
      const outcome = finish(sid, stage)
      if (outcome !== 'committed') { completeUncommittedSnapshot(context, sid, frame, outcome); return }
    }
    const before = sid ? context.get().sessionsById[sid] : undefined
    if (sid && before?.history.viewMode === 'historical' && !['session_snapshot', 'session_state'].includes(frame.type)) {
      active = { sid, bucket: liveProjectionBucket(before) }
      try {
        reduce(frame)
        const projected = active.bucket
        context.withBucket(sid, () => detachedLivePatch(before, projected))
      } finally { active = null }
    } else reduce(frame)
    if (sid && frame.type === 'catch_up_complete' && frame.mode === 'snapshot' && context.get().sessionsById[sid]?.history.viewMode === 'historical') void context.get().refreshLoadedHistory(sid)
  }
  return { handle, withBucket: wrappedBucket, get: wrappedGet }
}
