import { create } from 'zustand'
import type { WsConnection } from '@/lib/ws'

// Review finding 13: reconnectedAt used to be cleared by NOTHING — once set,
// it stayed non-null forever, so every consumer's `active = !isConnected ||
// reconnectedAt !== null` computation stayed true permanently, keeping a
// 1-second polling timer running on every assistant-message row for the
// rest of the session. Mirrors ConnectionStatus.tsx's own RECOVERY_NOTICE_MS
// (the "Up to date" note's visible duration) — after that window the note is
// gone from the UI anyway, so there is nothing left that needs `now` to keep
// ticking.
const RECONNECTED_AT_CLEAR_MS = 2_000

interface ConnectionStore {
  connection: WsConnection | null
  isConnected: boolean
  connectionError: string | null
  /**
   * Current reconnect phase.
   * null        — not reconnecting (connected or gave up / intentionally disconnected).
   * 'reconnecting' — fast backoff phase, actively retrying.
   * 'slow'      — slow retry phase (60s interval), backgrounded reconnect.
   * 'gave_up'   — all attempts exhausted; waiting for manual user action.
   */
  reconnectPhase: 'reconnecting' | 'slow' | 'gave_up' | null
  /** Current attempt number within the active reconnect phase (1-based). */
  reconnectAttempt: number
  /** Wall-clock markers used by the quiet 15 s / 2 min reconnect UI. */
  disconnectedAt: number | null
  reconnectedAt: number | null
  lastDisconnectDurationMs: number | null
  lastDisconnectWasTerminal: boolean
  /** Assistant bubble that was running when this socket dropped. */
  disconnectedAssistantMessageId: string | null
  /**
   * W2: lite mode is activated when heap pressure exceeds 250 MiB (iOS threshold).
   * When true the UI skips auto-expanding tool calls and the ring-buffer cap is
   * lowered to 200 messages via getEffectiveMessageLimit() in chat.ts.
   */
  liteMode: boolean
  setConnection: (conn: WsConnection | null) => void
  setConnected: (connected: boolean) => void
  recordDisconnect: (assistantMessageId: string | null) => void
  setConnectionError: (error: string | null) => void
  setReconnectState: (phase: 'reconnecting' | 'slow' | 'gave_up' | null, attempt: number) => void
  setLiteMode: (liteMode: boolean) => void
  reconnect: () => void
}

export const useConnectionStore = create<ConnectionStore>((set, get) => ({
  connection: null,
  isConnected: false,
  connectionError: null,
  reconnectPhase: null,
  reconnectAttempt: 0,
  disconnectedAt: null,
  reconnectedAt: null,
  lastDisconnectDurationMs: null,
  lastDisconnectWasTerminal: false,
  disconnectedAssistantMessageId: null,
  liteMode: false,
  setConnection: (conn) => set({ connection: conn }),
  setConnected: (connected) => {
    const current = get()
    const now = Date.now()
    const justReconnected = connected && current.disconnectedAt !== null
    set({
      isConnected: connected,
      connectionError: connected ? null : current.connectionError,
      reconnectPhase: connected ? null : current.reconnectPhase,
      reconnectAttempt: connected ? 0 : current.reconnectAttempt,
      disconnectedAt: connected ? null : (current.disconnectedAt ?? now),
      reconnectedAt: justReconnected ? now : current.reconnectedAt,
      lastDisconnectDurationMs: justReconnected
        ? now - current.disconnectedAt!
        : current.lastDisconnectDurationMs,
      lastDisconnectWasTerminal: justReconnected
        ? current.reconnectPhase === 'gave_up'
        : current.lastDisconnectWasTerminal,
      disconnectedAssistantMessageId: connected ? null : current.disconnectedAssistantMessageId,
    })
    if (justReconnected) {
      // Review finding 13: without this, reconnectedAt (and therefore the
      // `active` flag every "Up to date"/status consumer derives from it)
      // never returns to a resting state, so the 1-second polling timer in
      // useConnectionNow runs forever. Guard on `reconnectedAt === now`
      // rather than clearing unconditionally, so a LATER disconnect/
      // reconnect cycle's own fresh timestamp is never stomped by this
      // stale timer firing after the fact.
      window.setTimeout(() => {
        if (get().reconnectedAt === now) {
          set({ reconnectedAt: null })
        }
      }, RECONNECTED_AT_CLEAR_MS)
    }
  },
  recordDisconnect: (assistantMessageId) => {
    const current = get()
    set({
      isConnected: false,
      disconnectedAt: current.disconnectedAt ?? Date.now(),
      reconnectedAt: null,
      disconnectedAssistantMessageId: assistantMessageId ?? current.disconnectedAssistantMessageId,
    })
  },
  setConnectionError: (error) => set({ connectionError: error }),
  setReconnectState: (phase, attempt) => set({ reconnectPhase: phase, reconnectAttempt: attempt }),
  setLiteMode: (liteMode) => set({ liteMode }),

  reconnect: () => {
    const { connection } = get()
    if (!connection) {
      set({ connectionError: 'Cannot reconnect — please refresh the page.' })
      return
    }
    set({ connectionError: null, reconnectPhase: null, reconnectAttempt: 0 })
    connection.connect()
  },
}))
