// #1697 — the 2 s timer that returns `reconnectedAt` to rest must not stay
// pending when the state it belongs to is gone (a store reset, or a later
// reconnect cycle). It used to be fire-and-forget, so it could fire after the
// test file ended and its jsdom window was gone.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/lib/queryClient', () => ({ queryClient: { invalidateQueries: vi.fn() } }))

import { useConnectionStore } from './connection'

function reconnect(): void {
  useConnectionStore.getState().setConnected(false)
  useConnectionStore.getState().setConnected(true)
}

describe('reconnectedAt clear timer', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    useConnectionStore.setState({ isConnected: false, disconnectedAt: null, reconnectedAt: null })
  })
  afterEach(() => {
    useConnectionStore.setState({ isConnected: false, disconnectedAt: null, reconnectedAt: null })
    vi.useRealTimers()
  })

  it('still returns reconnectedAt to null after 2 s', () => {
    reconnect()
    expect(useConnectionStore.getState().reconnectedAt).not.toBeNull()
    vi.advanceTimersByTime(1_999)
    expect(useConnectionStore.getState().reconnectedAt).not.toBeNull()
    vi.advanceTimersByTime(1)
    expect(useConnectionStore.getState().reconnectedAt).toBeNull()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('a reset of reconnectedAt clears the pending timer', () => {
    reconnect()
    expect(vi.getTimerCount()).toBe(1)
    useConnectionStore.setState({ reconnectedAt: null })
    expect(vi.getTimerCount()).toBe(0)
  })

  it('a later reconnect cycle replaces the earlier timer and keeps its own timestamp for the full 2 s', () => {
    reconnect()
    vi.advanceTimersByTime(1_500)
    vi.setSystemTime(Date.now() + 1)
    reconnect()
    expect(vi.getTimerCount()).toBe(1)
    vi.advanceTimersByTime(1_500)
    expect(useConnectionStore.getState().reconnectedAt, 'the first cycle\'s timer must not stomp the second').not.toBeNull()
    vi.advanceTimersByTime(500)
    expect(useConnectionStore.getState().reconnectedAt).toBeNull()
  })

  it('two reconnects in the same millisecond leave one pending timer, not two', () => {
    reconnect()
    reconnect()
    expect(vi.getTimerCount()).toBe(1)
  })
})
