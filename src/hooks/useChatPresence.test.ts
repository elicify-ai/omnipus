/**
 * FR-026 / BS-6.16 sending side. Authorization of the claim is the gateway's
 * job and is not asserted here (founder: S-01 is not in this round).
 */
import { act, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { CHAT_PRESENCE_REFRESH_MS, useChatPresence } from './useChatPresence'

const SESSION = 'sess-watch'

describe('useChatPresence sends watching (BS-6.16)', () => {
  const sent: { session_id?: string; watching?: boolean }[] = []

  afterEach(() => {
    vi.useRealTimers()
    sent.length = 0
    useConnectionStore.setState({ connection: null, isConnected: false })
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'visible' })
  })

  function mount() {
    useSessionStore.getState().setActiveSession(SESSION, 'mia')
    useConnectionStore.setState({
      connection: { send: (frame: { session_id?: string; watching?: boolean }) => { sent.push(frame); return true } } as never,
      isConnected: true,
    })
    return renderHook(() => useChatPresence(SESSION, 'W'))
  }

  it('sends watching true on mount, refreshes at 15s, and sends false when hidden or unmounted', () => {
    vi.useFakeTimers()
    const hook = mount()
    expect(sent).toEqual([{ type: 'chat_presence', session_id: SESSION, watching: true }])

    act(() => { vi.advanceTimersByTime(CHAT_PRESENCE_REFRESH_MS) })
    expect(sent.at(-1)).toMatchObject({ session_id: SESSION, watching: true })
    expect(sent).toHaveLength(2)

    act(() => {
      Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => 'hidden' })
      document.dispatchEvent(new Event('visibilitychange'))
    })
    expect(sent.at(-1)).toMatchObject({ session_id: SESSION, watching: false })

    const beforeUnmount = sent.length
    hook.unmount()
    expect(sent.length).toBe(beforeUnmount)
  })

  // D-presence: refresh is 15s, and a claim expires after 45s (three missed
  // refreshes). The numbers are the spec's, not CHAT_PRESENCE_REFRESH_MS.
  it('sends nothing more at 14999ms and watching true at 15000ms', () => {
    vi.useFakeTimers()
    mount()
    expect(sent).toEqual([{ type: 'chat_presence', session_id: SESSION, watching: true }])
    act(() => { vi.advanceTimersByTime(14_999) })
    expect(sent).toHaveLength(1)
    act(() => { vi.advanceTimersByTime(1) })
    expect(sent).toEqual([
      { type: 'chat_presence', session_id: SESSION, watching: true },
      { type: 'chat_presence', session_id: SESSION, watching: true },
    ])
  })

  it('sends watching false when a still-visible chat unmounts', () => {
    vi.useFakeTimers()
    const hook = mount()
    expect(sent).toHaveLength(1)
    hook.unmount()
    expect(sent).toEqual([
      { type: 'chat_presence', session_id: SESSION, watching: true },
      { type: 'chat_presence', session_id: SESSION, watching: false },
    ])
  })

  it('sends false when the active session changes, and true again after reconnect', () => {
    vi.useFakeTimers()
    mount()
    act(() => { useSessionStore.getState().setActiveSession('sess-other', 'mia') })
    expect(sent.at(-1)).toEqual({ type: 'chat_presence', session_id: SESSION, watching: false })
    act(() => { useSessionStore.getState().setActiveSession(SESSION, 'mia') })
    expect(sent.at(-1)).toEqual({ type: 'chat_presence', session_id: SESSION, watching: true })
    const beforeDrop = sent.length
    act(() => { useConnectionStore.setState({ isConnected: false }) })
    expect(sent).toHaveLength(beforeDrop)
    act(() => { useConnectionStore.setState({ isConnected: true }) })
    expect(sent.at(-1)).toEqual({ type: 'chat_presence', session_id: SESSION, watching: true })
    expect(sent).toHaveLength(beforeDrop + 1)
  })
})
