import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { WsConnection } from '@/lib/ws'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useUiStore } from './ui'

// Oracle: founder stop rules (2026-10-06) + reviewer finding W2 — a Stop that
// sent nothing must be reported to the caller AND shown to the user, so the
// Stop window is never armed for a click that did nothing.
// REAL: chat/session/connection/ui stores and cancelStream. FAKE: the socket.

const SID = 'sess-cancel-delivery'
const sender = { send: vi.fn<WsConnection['send']>() }

function reset() {
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useUiStore.setState({ toasts: [] })
  })
}

beforeEach(() => {
  reset()
  sender.send.mockReset().mockReturnValue(true)
  act(() => {
    useSessionStore.getState().setActiveSession(SID, 'jim')
    useChatStore.setState({ isStreaming: true })
  })
})
afterEach(reset)

describe('cancelStream delivery report (W2)', () => {
  it('returns true and sends one frame when the socket accepts the cancel', () => {
    act(() => { useConnectionStore.getState().setConnection(sender as unknown as WsConnection) })
    let delivered: boolean | undefined
    act(() => { delivered = useChatStore.getState().cancelStream() })
    expect(delivered).toBe(true)
    expect(sender.send.mock.calls).toEqual([[{ type: 'cancel', session_id: SID }]])
    expect(useUiStore.getState().toasts).toEqual([])
  })

  it('returns false and shows a visible error toast when there is no connection', () => {
    let delivered: boolean | undefined
    act(() => { delivered = useChatStore.getState().cancelStream() })
    expect(delivered).toBe(false)
    expect(sender.send).not.toHaveBeenCalled()
    const toasts = useUiStore.getState().toasts
    expect(toasts).toHaveLength(1)
    expect(toasts[0].variant).toBe('error')
    expect(toasts[0].message).toMatch(/could not send (the )?(stop|cancel)/i)
  })

  it('returns false when the socket rejects the frame (existing toast stays)', () => {
    sender.send.mockReturnValue(false)
    act(() => { useConnectionStore.getState().setConnection(sender as unknown as WsConnection) })
    let delivered: boolean | undefined
    act(() => { delivered = useChatStore.getState().cancelStream() })
    expect(delivered).toBe(false)
    const toasts = useUiStore.getState().toasts
    expect(toasts).toHaveLength(1)
    expect(toasts[0].variant).toBe('error')
  })
})

// Mechanism behind the CI failure of tests/e2e/stop-scope-tree.spec.ts "after
// the confirmation window expires ..." (run on 61983a7df, 4 of 4: Expected 2
// frames, Received 1). The first cancel locally ENDS the turn
// (markLastMessageInterrupted clears isStreaming at once; the server's `done`
// is not awaited), and cancelStream's send gate reads isStreaming BEFORE that
// clear. So a later session-scoped cancel on the same, still-unacknowledged
// turn is the documented completed-turn no-op; only a tree stop still sends.
describe('second cancel after the first one ended the turn locally', () => {
  beforeEach(() => {
    act(() => { useConnectionStore.getState().setConnection(sender as unknown as WsConnection) })
  })

  it('the first cancel clears isStreaming immediately, with no server done', () => {
    act(() => { useChatStore.getState().cancelStream() })
    expect(useChatStore.getState().isStreaming).toBe(false)
  })

  it('a second session-scoped cancel sends no frame (completed-turn no-op)', () => {
    act(() => { useChatStore.getState().cancelStream() })
    act(() => { useChatStore.getState().cancelStream() })
    expect(sender.send.mock.calls).toEqual([[{ type: 'cancel', session_id: SID }]])
  })

  it('a confirmed tree cancel still sends after the turn ended locally', () => {
    act(() => { useChatStore.getState().cancelStream() })
    act(() => { useChatStore.getState().cancelStream(undefined, 'tree') })
    expect(sender.send.mock.calls).toEqual([
      [{ type: 'cancel', session_id: SID }],
      [{ type: 'cancel', session_id: SID, scope: 'tree' }],
    ])
  })
})
