import { fetchSessionMessages } from '@/lib/api/sessions'
import type { HistorySlice, HistorySliceContext, HistoryActionKind, HistoryPageSizing } from './types'
import type { SessionHistoryPage, SessionHistoryQuery } from '@/lib/api/history-types'
import type { ChatStore, ChatMessage } from './types'
import { queryClient } from '@/lib/queryClient'
import { useConnectionStore } from '@/store/connection'
import { mergeRecordedSessionTokens } from './session-usage'
import { isPendingTailMessage } from './cursor'
import { emptyHistoryState, historyRowsPatch, mergeHistoryEntry, retainedPending } from './history-state'
import { applyHistoryPageToBucket, reachedClosedClear, releaseHistoryProtection } from './history-merge'
export { emptyHistoryState } from './history-state'
export type { HistorySlice, HistorySliceContext, HistoryActionKind, HistoryPageSizing, SessionHistoryState, SessionHistoryRange, HistoryRequestState } from './types'

const missingHistory = emptyHistoryState()
export function selectSessionHistory(state: ChatStore, sessionId: string) { return state.sessionsById[sessionId]?.history ?? missingHistory }

type ReadOwner = { generation: number; controller: AbortController; promise: Promise<void>; query: SessionHistoryQuery; baseline: Record<string, ChatMessage>; staged: SessionHistoryPage['entries'] } // not-wire-format
const owners = new Map<string, Map<HistoryActionKind, ReadOwner>>()
const failed = new Map<string, Map<HistoryActionKind, SessionHistoryQuery>>()
const releaseHooks = new Set<(sessionId: string | null) => void>()
export function registerHistoryRelease(hook: (sessionId: string | null) => void): void { releaseHooks.add(hook) }
export function discardHistoryStaging(): void { for (const hook of releaseHooks) hook(null) }
export function abortHistoryReads(sessionId: string): void {
  for (const hook of releaseHooks) hook(sessionId)
  for (const owner of owners.get(sessionId)?.values() ?? []) { owner.baseline = {}; owner.staged.length = 0; owner.controller.abort() }
  owners.delete(sessionId)
  failed.delete(sessionId)
}
const copy: Record<HistoryActionKind, string> = {
  older: 'Could not load earlier messages.', newer: 'Could not load newer messages.', latest: 'Could not load latest messages.', refresh: 'Could not refresh loaded messages.',
}

export function createHistorySlice(context: HistorySliceContext): HistorySlice {
  const { get, withBucket } = context
  const state = (sid: string) => get().sessionsById[sid]
  function request(sid: string, kind: HistoryActionKind, query: SessionHistoryQuery): Promise<void> {
    const bucket = state(sid)
    if (bucket?.history.requests[kind].status === 'loading') return owners.get(sid)?.get(kind)?.promise ?? Promise.resolve()
    const controller = new AbortController()
    const generation = bucket?.history.viewGeneration ?? 0
    const previous = bucket?.history.requests[kind]
    withBucket(sid, b => ({ history: { ...b.history, requests: { ...b.history.requests, [kind]: { status: 'loading', error: previous?.error ?? null, attempt: (previous?.attempt ?? 0) + 1 } } } }))
    const owner: ReadOwner = { generation, controller, query: { ...query }, promise: Promise.resolve(), baseline: { ...bucket?.messagesById }, staged: [] }
    if (!owners.has(sid)) owners.set(sid, new Map())
    owners.get(sid)!.set(kind, owner)
    const valid = () => !controller.signal.aborted && state(sid)?.history.viewGeneration === generation && owners.get(sid)?.get(kind) === owner
    owner.promise = (async () => {
      try {
        if (kind === 'refresh') await refresh(sid, query, owner, valid)
        else {
          const page = await fetchSessionMessages(sid, { ...query, signal: controller.signal })
          if (!valid()) return
          validateReadDomain(page, query, kind)
          withBucket(sid, b => applyHistoryPageToBucket(b, page, kind, owner.baseline))
        }
        if (!valid()) return
        failed.get(sid)?.delete(kind)
        withBucket(sid, b => ({ history: { ...b.history, requests: { ...b.history.requests, [kind]: { ...b.history.requests[kind], status: 'idle', error: null } } } }))
      } catch (error) {
        if (!valid()) return
        // Protocol/transport errors have an action-owned, persistent recovery
        // surface. Do not print content, positions or unredacted causes.
        if (error instanceof TypeError && !(error.name === 'ApiError')) throw error
        if (!failed.has(sid)) failed.set(sid, new Map())
        failed.get(sid)!.set(kind, { ...query })
        withBucket(sid, b => ({ history: { ...b.history, requests: { ...b.history.requests, [kind]: { ...b.history.requests[kind], status: 'error', error: copy[kind] } } } }))
      } finally {
        owner.baseline = {}; owner.staged.length = 0
        if (owners.get(sid)?.get(kind) === owner) {
          owners.get(sid)!.delete(kind)
          if (!owners.get(sid)!.size) owners.delete(sid)
        }
      }
    })()
    return owner.promise
  }

  async function refresh(sid: string, query: SessionHistoryQuery, owner: ReadOwner, valid: () => boolean): Promise<void> {
    const old = state(sid)!.history.readerPositions.slice()
    const staged = owner.staged
    const seen = new Set<string>()
    let before: string | undefined
    let retention: string | undefined
    let recordedTokens: number | undefined
    for (;;) {
      const page = await fetchSessionMessages(sid, { ...query, ...(before ? { before } : {}), signal: owner.controller.signal })
      if (!valid()) return
      validateReadDomain(page, query, 'refresh')
      if (retention !== undefined && retention !== page.page_info.retention_generation) throw new Error('History retention changed during refresh.')
      retention = page.page_info.retention_generation
      recordedTokens = mergeRecordedSessionTokens(recordedTokens, page.session_tokens_total)
      for (const entry of page.entries) {
        if (seen.has(entry.historyPosition) || !old.includes(entry.historyPosition)) throw new Error('Contradictory refresh interval.')
        seen.add(entry.historyPosition)
      }
      staged.unshift(...page.entries)
      if (staged.length > old.length || staged.length > 500) throw new Error('History refresh exceeds its saved interval.')
      if (!page.has_earlier) break
      if (!page.next_before || page.next_before === before || page.entries.length === 0) throw new Error('History refresh boundary did not advance.')
      before = page.next_before
    }
    if (!valid()) return
    const ordered = old.filter(position => seen.has(position))
    if (ordered.join() !== staged.map(entry => entry.historyPosition).join()) throw new Error('Contradictory refresh order.')
    withBucket(sid, b => {
      const rows = staged.map(entry => {
        const current = b.messagesById[entry.id]
        return mergeHistoryEntry(entry, current, owner.baseline[entry.id])
      })
      const rowIds = new Set(rows.map(row => row.id))
      const confirmed = new Set(rows.flatMap(row => row.clientMessageId ? [row.clientMessageId] : []))
      const needed = retainedPending(b).filter(row => !rowIds.has(row.id) && !confirmed.has(row.clientMessageId ?? row.id))
      const pending = needed.filter(isPendingTailMessage)
      const liveOrder = [...new Set([...(b.historyLiveOrder ?? []), ...needed.map(row => row.id)])].filter(id => !rowIds.has(id) && !confirmed.has(b.messagesById[id]?.clientMessageId ?? id)).slice(-100)
      const live = liveOrder.flatMap(id => b.messagesById[id] ? [b.messagesById[id]] : [])
      const patch = historyRowsPatch(b, [...rows, ...pending, ...live], ordered)
      return { ...patch, messageOrder: [...new Set([...rows, ...pending].map(row => row.id))], historyLiveOrder: liveOrder,
        sessionTokens: mergeRecordedSessionTokens(b.sessionTokens, recordedTokens),
        historyNotice: ordered.length < old.length ? (ordered.length ? 'Some loaded messages have expired.' : 'These loaded messages have expired.') : undefined,
        history: { ...patch.history!, serverState: { status: 'available' }, historyLoaded: true,
          olderBoundary: ordered[0] ?? null, newerBoundary: ordered.at(-1) ?? null,
          ...(ordered.length ? {} : { hasEarlier: null, hasLater: null }) } }
    })
  }
  const sizing = (value?: HistoryPageSizing) => value ?? { limit: 20, min_rows: 20 }
  function older(sid: string, size?: HistoryPageSizing, cross = false): Promise<void> {
    const bucket = state(sid)
    if (!bucket || !bucket.history.hasEarlier || !bucket.history.olderBoundary || bucket.history.requests.refresh.status !== 'idle' || (!cross && reachedClosedClear(bucket))) return Promise.resolve()
    return request(sid, 'older', { before: bucket.history.olderBoundary, ...sizing(size) })
  }
  return {
    loadLatestHistory: (sid, size) => request(sid, 'latest', size ?? { limit: 40, min_rows: 20 }),
    loadOlderHistory: older,
    loadNewerHistory: (sid, size) => {
      const bucket = state(sid)
      return bucket?.history.hasLater && bucket.history.newerBoundary && bucket.history.requests.refresh.status === 'idle'
        ? request(sid, 'newer', { after: bucket.history.newerBoundary, ...sizing(size) }) : Promise.resolve()
    },
    showEarlierHistory: (sid, position, size) => {
      const bucket = state(sid)
      const clear = bucket && reachedClosedClear(bucket)
      if (!clear || clear.historyPosition !== position) return Promise.resolve()
      withBucket(sid, b => ({ history: { ...b.history, viewMode: 'historical', openedClearPositions: [...new Set([...b.history.openedClearPositions, position])] } }))
      return older(sid, size, true)
    },
    returnToLatestHistory: (sid, size) => request(sid, 'latest', size ?? { limit: 40, min_rows: 20 }),
    refreshLoadedHistory: sid => {
      const range = state(sid)?.history.readerRange
      return range ? request(sid, 'refresh', { ...range, limit: 100, min_rows: 20 }) : Promise.resolve()
    },
    retryHistory: (sid, kind) => {
      if (kind === 'latest' && state(sid)?.history.serverState?.status === 'unavailable') {
        const active = state(sid)!
        if (active.history.requests.latest.status === 'loading') return Promise.resolve()
        const sent = useConnectionStore.getState().connection?.send({ type: 'attach_session', session_id: sid })
        withBucket(sid, b => ({ history: { ...b.history, requests: { ...b.history.requests, latest: { status: sent ? 'loading' : 'error', error: sent ? b.history.requests.latest.error : copy.latest, attempt: b.history.requests.latest.attempt + 1 } } } }))
        return Promise.resolve()
      }
      const query = failed.get(sid)?.get(kind)
      return query ? request(sid, kind, query) : kind === 'latest' ? request(sid, 'latest', { limit: 40, min_rows: 20 }) : Promise.resolve()
    },
    enterHistoryReading: sid => withBucket(sid, b => b.history.viewMode === 'historical' || !b.messageOrder.length ? {} : { history: { ...b.history, viewMode: 'historical' } }),
    releaseSessionHistory: sid => {
      abortHistoryReads(sid)
      releaseHistoryProtection(sid)
      queryClient.removeQueries({ queryKey: ['messages', sid] })
      queryClient.removeQueries({ queryKey: ['session-detail', sid] })
      withBucket(sid, b => {
        const pending = retainedPending(b)
        const clean = historyRowsPatch(b, pending, [])
        return { ...clean, historyNotice: undefined, historyNeedsSnapshot: true, history: { ...emptyHistoryState(), viewGeneration: b.history.viewGeneration + 1 } }
      })
    },
  }
}

function validateReadDomain(page: SessionHistoryPage, query: SessionHistoryQuery, action: HistoryActionKind): void {
  const expected = action === 'refresh' ? 'window_refresh' : action
  if (page.page_info.mode !== expected || (query.before && page.entries.some(entry => entry.historyPosition === query.before)) || (query.after && page.entries.some(entry => entry.historyPosition === query.after))) throw new Error('Inconsistent history read domain.')
}
