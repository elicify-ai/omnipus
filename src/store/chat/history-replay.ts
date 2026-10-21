// Snapshot projection staging, not a second transport/replay-frame buffer.
// The socket's render-once buffer still owns delivery through catch_up_complete.
import type { ServerFrame, SessionSnapshotFrame } from '@/lib/api/generated/asyncapi-types'
import type { HistorySliceContext } from './types'
import type { SessionHistoryEntry, SessionHistoryPage } from '@/lib/api/history-types'
import type { ChatMessage, SessionChatState } from './types'
import { emptySessionState } from './session'
import { getMessages } from './messages'
import { abortHistoryReads } from './history'
import { applyHistoryPageToBucket } from './history-merge'
import { validateHistoryMetadata, historyPosition } from '@/lib/api/history-validation'
import { SessionHistoryResponse as PageSchema } from '@/lib/api/generated/schemas'

export function createHistoryFrameOwner(context: HistorySliceContext) {
  type Stage = { frame: SessionSnapshotFrame; groups: Map<string, SessionChatState>; generation: number; baseline: Record<string, ChatMessage>; invalid: boolean }
  const stages = new Map<string, Stage>()
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
    context.withBucket(frame.session_id, b => ({ history: { ...b.history, viewGeneration: b.history.viewGeneration + 1,
      requests: { ...b.history.requests, older: { status: 'idle', error: null, attempt: 0 }, newer: { status: 'idle', error: null, attempt: 0 }, refresh: { status: 'idle', error: null, attempt: 0 } } } }))
    const bucket = context.get().sessionsById[frame.session_id]
    stages.set(frame.session_id, { frame, groups: new Map(), generation: bucket.history.viewGeneration, baseline: { ...bucket.messagesById }, invalid: false })
  }
  function collect(frame: ServerFrame, sid: string, stage: Stage, reduce: (frame: ServerFrame) => void): void {
    const origin = 'history_origin' in frame ? frame.history_origin : undefined
    if (!origin) return
    let position: string
    try { position = historyPosition(origin.position, 'WS history', ['history_origin', 'position']) } catch { stage.invalid = true; return }
    const previous = stage.groups.get(position) ?? emptySessionState()
    active = { sid, bucket: previous }
    // A tool-only owning source is a real non-prose group, not the preceding
    // assistant bubble. The ordinary tool adapters then retain exact offsets.
    if ((frame.type === 'tool_call_start' || frame.type === 'tool_call_result') && !active.bucket.messageOrder.length) {
      const id = origin.entry_id ?? `history-${position}`
      const message: ChatMessage = { id, role: 'assistant', content: '', timestamp: '1970-01-01T00:00:00Z', status: 'done', historyPosition: position }
      active.bucket = { ...active.bucket, messagesById: { [id]: message }, messageOrder: [id] }
    }
    try { reduce(frame); stage.groups.set(position, active.bucket) } finally { active = null }
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
          const tools = new Map(row.tool_calls?.map(call => [call.id, call]) ?? [])
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
        if (b.history.viewMode === 'historical') return { history: { ...b.history, tailEntries: entries.slice(-100), historyLoaded: true } }
        return applyHistoryPageToBucket(b, page, 'latest', stage.baseline)
      })
      return true
    } catch {
      reject(sid)
      return false
    }
  }
  function handle(frame: ServerFrame, reduce: (frame: ServerFrame) => void): void {
    const sid = 'session_id' in frame ? frame.session_id : frame.type === 'ask_user_question' ? frame.card.session_id : undefined
    for (const [id, stage] of stages) if (context.get().sessionsById[id]?.history.viewGeneration !== stage.generation) stages.delete(id)
    if (frame.type === 'session_snapshot') begin(frame)
    const stage = sid ? stages.get(sid) : undefined
    if (sid && stage && 'history_origin' in frame && frame.history_origin) { collect(frame, sid, stage, reduce); return }
    // Replay done bakes historical tool state only. It cannot close a current
    // turn or commit the source page; active projection may follow it.
    if (sid && stage && frame.type === 'done' && !frame.seq && !frame.turn_id) {
      if (frame.stats?.replay_error) { stages.delete(sid); reject(sid); return }
      for (const [position, bucket] of stage.groups) {
        active = { sid, bucket }
        try { reduce(frame); stage.groups.set(position, active.bucket) } finally { active = null }
      }
      return
    }
    if (sid && stage && frame.type === 'catch_up_complete') {
      stages.delete(sid)
      if (!finish(sid, stage)) {
        context.withBucket(sid, b => ({ awaitingCatchUp: false, isReplaying: false, history: { ...b.history, liveControlsReady: true } }))
        return
      }
    }
    reduce(frame)
    if (sid && frame.type === 'catch_up_complete' && frame.mode === 'snapshot' && context.get().sessionsById[sid]?.history.viewMode === 'historical') void context.get().refreshLoadedHistory(sid)
  }
  return { handle, withBucket: wrappedBucket, get: wrappedGet }
}
