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
