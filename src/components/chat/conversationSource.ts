// conversationSource — what the shared message list, its history boundary and
// its viewport read (#1449 ADR-20261028 A1): one interface, two
// implementations. The session source (below) passes today's chat-store
// state and actions straight through, so a normal chat behaves exactly as
// before (FE-1 neutrality gate). The room source
// (src/components/rooms/roomConversationSource.ts) projects a group chat's
// own store; the two stores stay separate (FR-199).
//
// The list and history components read `useConversationSource()`: the
// provided source on a group page, otherwise the session source. Message
// ROWS read only the stable `useConversationKind()` so a room frame never
// re-renders a memoised row (ADR A1.3 row identity).

import { createContext, useContext, useMemo } from 'react'
import { emptyHistoryState, useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import type { HistoryActionKind, HistoryPageSizing, SessionHistoryState } from '@/store/chat/types'
import { protectHistoryPositions, reachedClosedClear, releaseHistoryProtection } from '@/store/chat/history-merge'
import { forgetHistoryGeometry, recordHistoryGeometry } from '@/lib/chat-history-sizing'

/** A fresh read of the source's history for asynchronous work (fills, retries). */
export interface ConversationSnapshot { // not-wire-format: in-browser read model
  history: SessionHistoryState
  /** The live view is still catching up (session replay / room snapshot pending). */
  catchingUp: boolean
}

export interface ConversationSource { // not-wire-format: React context contract of the shared chat stack
  kind: 'session' | 'room'
  /** The session id, or `room:<roomId>`; null when nothing is open. Identity of one opened conversation. */
  key: string | null
  /** The chat session; always null for a group (FR-199). */
  sessionId: string | null
  history: SessionHistoryState
  historyNotice?: string
  catchingUp: boolean
  capabilities: {
    /** The window can sit away from the live tail and page newer (session only). */
    newerWindow: boolean
    /** /clear markers close earlier history (session only). */
    clearMarkers: boolean
    /** Live tool calls, streaming and session send state reach the rows (session only). */
    liveToolCalls: boolean
  }
  /** Fresh state for async loops; null when the conversation is gone. */
  read(): ConversationSnapshot | null
  enterReading(): void
  /** The older edge: enter history reading, then load the previous page (or show earlier past a /clear). */
  beginOlder(size: HistoryPageSizing): Promise<void>
  loadOlder(size: HistoryPageSizing): Promise<void>
  loadNewer(size: HistoryPageSizing): Promise<void>
  returnToLatest(size: HistoryPageSizing): Promise<void>
  refresh(): Promise<void>
  retry(kind: HistoryActionKind): Promise<void>
  protectPositions(positions: ReadonlySet<string>): void
  releaseProtection(): void
  recordGeometry(height: number, average: number): void
  forgetGeometry(): void
}

/** Provided on a group page; absent (null) everywhere a normal chat renders. */
export const ConversationSourceContext = createContext<ConversationSource | null>(null)

/** Stable per opened conversation: what message rows may read. */
export const ConversationKindContext = createContext<'session' | 'room'>('session')

/** The conversation kind the current rows render for (never changes while a row is mounted). */
export function useConversationKind(): 'session' | 'room' {
  return useContext(ConversationKindContext)
}

const DISABLED_HISTORY = emptyHistoryState()
const NO_SNAPSHOT = () => null

/**
 * The normal chat's source: today's foreground history and the chat store's
 * own history actions, called exactly as the components called them before.
 * `enabled` false (a group page) subscribes to nothing that changes.
 */
export function useSessionConversationSource(enabled: boolean): ConversationSource {
  const sid = useSessionStore((s) => (enabled ? s.activeSessionId : null))
  const history = useChatStore((s) => (enabled ? s.history : DISABLED_HISTORY))
  const historyNotice = useChatStore((s) => (enabled ? s.historyNotice : undefined))
  const catchingUp = useChatStore((s) => enabled && (s.isReplaying || (sid ? s.sessionsById[sid]?.awaitingCatchUp === true : false)))
  return useMemo<ConversationSource>(() => ({
    kind: 'session',
    key: sid,
    sessionId: sid,
    history,
    historyNotice,
    catchingUp,
    capabilities: { newerWindow: true, clearMarkers: true, liveToolCalls: true },
    read: sid
      ? () => {
          const kept = useChatStore.getState().sessionsById[sid]
          return kept ? { history: kept.history, catchingUp: !!(kept.isReplaying || kept.awaitingCatchUp) } : null
        }
      : NO_SNAPSHOT,
    enterReading: () => { if (sid) useChatStore.getState().enterHistoryReading(sid) },
    beginOlder: async (size) => {
      if (!sid) return
      const state = useChatStore.getState()
      state.enterHistoryReading(sid)
      const bucket = state.sessionsById[sid]
      const clear = bucket && reachedClosedClear(bucket)
      return clear ? state.showEarlierHistory(sid, clear.historyPosition, size) : state.loadOlderHistory(sid, size)
    },
    loadOlder: async (size) => { if (sid) return useChatStore.getState().loadOlderHistory(sid, size) },
    loadNewer: async (size) => { if (sid) return useChatStore.getState().loadNewerHistory(sid, size) },
    returnToLatest: async (size) => { if (sid) return useChatStore.getState().returnToLatestHistory(sid, size) },
    refresh: async () => { if (sid) return useChatStore.getState().refreshLoadedHistory(sid) },
    retry: async (kind) => { if (sid) return useChatStore.getState().retryHistory(sid, kind) },
    protectPositions: (positions) => { if (sid) protectHistoryPositions(sid, positions) },
    releaseProtection: () => { if (sid) releaseHistoryProtection(sid) },
    recordGeometry: (height, average) => { if (sid) recordHistoryGeometry(sid, height, average) },
    forgetGeometry: () => { if (sid) forgetHistoryGeometry(sid) },
  }), [sid, history, historyNotice, catchingUp])
}

/** The conversation the shared list renders: the group's when provided, else the normal chat's. */
export function useConversationSource(): ConversationSource {
  const provided = useContext(ConversationSourceContext)
  const session = useSessionConversationSource(provided === null)
  return provided ?? session
}
