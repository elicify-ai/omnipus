// Snapshot projection staging, not a second transport/replay-frame buffer.
// The socket's render-once buffer still owns delivery through catch_up_complete.
import type { ServerFrame, SessionSnapshotFrame } from '@/lib/api/generated/asyncapi-types'
import type { HistorySliceContext } from './types'
import type { SessionHistoryEntry, SessionHistoryPage } from '@/lib/api/history-types'
import type { ChatMessage, PositionedToolCall, SessionChatState } from './types'
import { isSupersededAttachFrame } from '@/lib/attach-replay'
import { detachedLivePatch, liveProjectionBucket, withLiveHistoryBucket } from './history-live'
import { bakeToolCallsByOwner, emptySessionState } from './session'
import { clampToolResult, getMessages } from './messages'
import { applyToolCallResultFrame } from './slices/frames'
import { refreshRosterAfterAgentMutation } from './agentMutationRefresh'
import { historyMetadata, historyRowsPatch, mergeHistoryEntry, retainedPending } from './history-state'
import { useSessionStore } from '@/store/session'
import { cursorFromTerminalFrame } from './cursor'
import { abortHistoryReads, registerHistoryRelease } from './history'
import { applyHistoryPageToBucket } from './history-merge'
import { validateHistoryMetadata, historyPosition } from '@/lib/api/history-validation'
import { SessionHistoryResponse as PageSchema } from '@/lib/api/generated/schemas'

export function createHistoryFrameOwner(context: HistorySliceContext) {
  type Stage = { frame: SessionSnapshotFrame; groups: Map<string, SessionChatState>; generation: number; baseline: Record<string, ChatMessage>; invalid: boolean }
  const stages = new Map<string, Stage>()
  const discardedSnapshots = new Set<string>()
  registerHistoryRelease(sid => {
    if (!sid) { stages.clear(); discardedSnapshots.clear(); return }
    if (stages.has(sid)) discardedSnapshots.add(sid)
    stages.delete(sid)
  })
  let active: { sid: string; bucket: SessionChatState } | null = null
  const wrappedBucket: HistorySliceContext['withBucket'] = (sid, update) => {
    if (active && sid === active.sid) active.bucket = { ...active.bucket, ...update(active.bucket) }
    else context.withBucket(sid, update)
  }
  const wrappedGet: HistorySliceContext['get'] = () => {
    const state = context.get()
    if (!active) return state
    return { ...state, ...active.bucket, messages: getMessages(active.bucket), sessionsById: { ...state.sessionsById, [active.sid]: active.bucket } }
  }
  function reject(sid: string): void {
    context.withBucket(sid, b => ({ history: { ...b.history, historyLoaded: false, requests: { ...b.history.requests, latest: { status: 'error', error: 'Could not load messages.', attempt: b.history.requests.latest.attempt + 1 } } } }))
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
    stages.set(frame.session_id, { frame, groups: new Map(), generation: bucket.history.viewGeneration, baseline: { ...bucket.messagesById }, invalid: false })
  }
  function collect(frame: ServerFrame, sid: string, stage: Stage, reduce: (frame: ServerFrame) => void): void {
    const origin = 'history_origin' in frame ? frame.history_origin : undefined
    if (!origin) return
    let position: string
    try { position = historyPosition(origin.position, 'WS history', ['history_origin', 'position']) } catch { stage.invalid = true; return }
    const previous = stage.groups.get(position) ?? { ...emptySessionState(), isReplaying: true, awaitingCatchUp: true }
    active = { sid, bucket: previous }
    // A tool-only owning source is a real non-prose group, not the preceding
    // assistant bubble. The ordinary tool adapters then retain exact offsets.
    if ((frame.type === 'tool_call_start' || frame.type === 'tool_call_result') && !active.bucket.messageOrder.length) {
      const id = origin.entry_id ?? `history-${position}`
      const message: ChatMessage = { id, role: 'assistant', content: '', timestamp: '1970-01-01T00:00:00Z', status: 'done', historyPosition: position }
      active.bucket = { ...active.bucket, messagesById: { [id]: message }, messageOrder: [id] }
    }
    try {
      reduce(frame)
      if (!active.bucket.messageOrder.length) {
        const id = origin.entry_id ?? `history-${position}`
        const message: ChatMessage = { id, role: 'assistant', content: '', timestamp: '1970-01-01T00:00:00Z', status: 'done', historyPosition: position }
        active.bucket = { ...active.bucket, messagesById: { [id]: message }, messageOrder: [id] }
      }
      stage.groups.set(position, active.bucket)
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
  function finish(sid: string, stage: Stage): boolean {
    const bucket = context.get().sessionsById[sid]
    if (!bucket || bucket.history.viewGeneration !== stage.generation) return false
    try {
      const positions = [...stage.groups.keys()]
      const entries: SessionHistoryEntry[] = []
      let rowCount = 0
      for (const [position, group] of stage.groups) {
        const rows = getMessages(group)
        for (const row of rows) {
          const tools = new Map<string, PositionedToolCall>(row.tool_calls?.map(call => [call.id, call]) ?? [])
          for (const call of Object.values(group.toolCalls)) tools.set(call.id, { ...call, textOffset: group.textAtToolCallStart[call.id]?.length })
          const value = { ...row, historyPosition: position, ...(tools.size ? { tool_calls: [...tools.values()] } : {}) } as SessionHistoryEntry
          entries.push(value)
          rowCount += Number(!!row.content || (tools.size === 0 && row.role === 'system')) + tools.size
        }
      }
      const { frame } = stage
      const parsed = PageSchema.safeParse({ ...frame, entries: [] })
      if (!parsed.success || stage.invalid) throw new Error('Invalid history snapshot.')
      validateHistoryMetadata(parsed.data, positions, 'WS history')
      if (rowCount !== frame.page_info.row_count) throw new Error('Invalid history row count.')
      const page: SessionHistoryPage = { ...parsed.data, entries }
      context.withBucket(sid, b => {
        if (b.history.viewMode === 'historical') {
          const needed = new Set(retainedPending(b).map(row => row.id))
          const liveOrder = (b.historyLiveOrder ?? []).filter(id => !stage.baseline[id] || needed.has(id))
          const liveIds = new Set(liveOrder)
          const incoming = new Map(entries.map(row => [row.id, row]))
          const rows = [...new Set([...b.messageOrder, ...liveOrder])].flatMap(id => {
            const current = b.messagesById[id]
            const fresh = incoming.get(id)
            return current ? [liveIds.has(id) && fresh ? mergeHistoryEntry(fresh, current, stage.baseline[id]) : current] : []
          })
          const patch = historyRowsPatch(b, rows, b.history.readerPositions)
          const capacity = Math.max(0, 100 - liveOrder.length)
          const tail = entries.filter(row => !liveIds.has(row.id))
          return { ...patch, messageOrder: b.messageOrder, historyLiveOrder: liveOrder, historyNeedsSnapshot: false,
            history: { ...patch.history!, tailEntries: capacity ? tail.slice(-capacity) : [], pageMetadata: historyMetadata(page), historyLoaded: true } }
        }
        return applyHistoryPageToBucket(b, page, 'latest', stage.baseline)
      })
      return true
    } catch {
      reject(sid)
      return false
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
      if (frame.stats?.replay_error) { stages.delete(sid); reject(sid); return }
      for (const [position, bucket] of stage.groups) {
        const messagesById = { ...bucket.messagesById }
        const fallback = [...bucket.messageOrder].reverse().find(id => messagesById[id]?.role === 'assistant') ?? null
        bakeToolCallsByOwner(messagesById, bucket.toolCallOrder, bucket.toolCalls, bucket.toolCallOwnerMessageId ?? {}, fallback, bucket.textAtToolCallStart)
        stage.groups.set(position, { ...bucket, messagesById, isStreaming: false, toolCalls: {}, toolCallOrder: [], toolCallOwnerMessageId: {}, textAtToolCallStart: {} })
      }
      return
    }
    if (sid && stage && frame.type === 'catch_up_complete') {
      stages.delete(sid)
      if (!finish(sid, stage)) {
        context.withBucket(sid, b => ({ cursor: cursorFromTerminalFrame(frame, b.cursor?.bootId), awaitingCatchUp: false, isReplaying: false, history: { ...b.history, liveControlsReady: true } }))
        return
      }
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
