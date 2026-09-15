import { afterEach, describe, expect, it, vi } from 'vitest'
import { watchPopoutClosed } from './browserLiveHandoff'

afterEach(() => vi.useRealTimers())
describe('owned popout lifetime', () => {
  it('waits for actual window closure, not a reload or elapsed loading time', () => {
    vi.useFakeTimers()
    const popup = { closed: false }, closed = vi.fn()
    const stop = watchPopoutClosed(popup, closed)
    vi.advanceTimersByTime(10000)
    expect(closed).not.toHaveBeenCalled()
    popup.closed = true
    vi.advanceTimersByTime(249)
    expect(closed).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(closed).toHaveBeenCalledExactlyOnceWith()
    vi.advanceTimersByTime(10000)
    expect(closed).toHaveBeenCalledTimes(1)
    stop()
  })
  it('cancels an obsolete owner monitor before a later close', () => {
    vi.useFakeTimers()
    const popup = { closed: false }, closed = vi.fn()
    const stop = watchPopoutClosed(popup, closed)
    stop(); popup.closed = true; vi.advanceTimersByTime(1000)
    expect(closed).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
  })
})
