import { describe, it, expect, vi, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useSettledFlag } from './useSettledFlag'

afterEach(() => {
  vi.useRealTimers()
})

// Operator report 2026-07-31: a red "Disconnected" banner flashed for a split
// second on a connection that immediately recovered. These pin the behaviour
// that makes such a blip invisible without hiding a real outage.
describe('useSettledFlag', () => {
  it('stays false for a blip shorter than the delay (the flash this exists to prevent)', () => {
    vi.useFakeTimers()
    const { result, rerender } = renderHook(({ v }) => useSettledFlag(v, 2000), {
      initialProps: { v: false },
    })

    rerender({ v: true })
    act(() => {
      vi.advanceTimersByTime(400) // a sub-second reconnect
    })
    expect(result.current).toBe(false)

    rerender({ v: false }) // recovered
    act(() => {
      vi.advanceTimersByTime(5000)
    })
    expect(result.current).toBe(false)
  })

  it('becomes true once the condition persists past the delay (a real outage still surfaces)', () => {
    vi.useFakeTimers()
    const { result, rerender } = renderHook(({ v }) => useSettledFlag(v, 2000), {
      initialProps: { v: false },
    })

    rerender({ v: true })
    act(() => {
      vi.advanceTimersByTime(2001)
    })
    expect(result.current).toBe(true)
  })

  it('clears immediately on recovery rather than lingering for another delay', () => {
    vi.useFakeTimers()
    const { result, rerender } = renderHook(({ v }) => useSettledFlag(v, 2000), {
      initialProps: { v: true },
    })
    act(() => {
      vi.advanceTimersByTime(2001)
    })
    expect(result.current).toBe(true)

    rerender({ v: false })
    expect(result.current).toBe(false)
  })
})
