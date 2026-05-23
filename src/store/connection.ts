import { create } from 'zustand'
import type { WsConnection } from '@/lib/ws'

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
  /**
   * W2: lite mode is activated when heap pressure exceeds 250 MiB (iOS threshold).
   * When true the UI skips auto-expanding tool calls and the ring-buffer cap is
   * lowered to 200 messages via getEffectiveMessageLimit() in chat.ts.
   */
  liteMode: boolean
  setConnection: (conn: WsConnection | null) => void
  setConnected: (connected: boolean) => void
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
  liteMode: false,
  setConnection: (conn) => set({ connection: conn }),
  setConnected: (connected) =>
    set({
      isConnected: connected,
      connectionError: connected ? null : get().connectionError,
      // Clear reconnect state on connect so banner disappears immediately.
      reconnectPhase: connected ? null : get().reconnectPhase,
      reconnectAttempt: connected ? 0 : get().reconnectAttempt,
    }),
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
