import { StrictMode } from 'react'
import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useLoadingVisibility } from './use-loading-visibility'

// Execution contract D6: 400ms delay and 300ms continuous minimum visibility.
// Real hook and React; only elapsed time is controlled. These expected numbers
// come from the approved contract, independently of generated token values.
// Mutations: delay 399ms, dwell 299ms, and removed timeout cleanup.
const advance = (milliseconds: number) => act(() => vi.advanceTimersByTime(milliseconds))

describe('shared loading visibility', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'performance'] })
  })
  afterEach(() => { vi.useRealTimers() })

  it('stays hidden until 400ms and remains visible while pending', () => {
    const { result } = renderHook(() => useLoadingVisibility(true))
    expect(result.current).toBe(false)
    advance(399)
    expect(result.current).toBe(false)
    advance(1)
    expect(result.current).toBe(true)
    advance(1)
    expect(result.current).toBe(true)
    advance(10_000)
    expect(result.current).toBe(true)
  })

  it('never flashes when pending ends before the delay', () => {
    const { result, rerender } = renderHook(({ pending }) => useLoadingVisibility(pending), { initialProps: { pending: true } })
    advance(399)
    rerender({ pending: false })
    advance(1)
    expect(result.current).toBe(false)
    advance(10_000)
    expect(result.current).toBe(false)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('retains an indicator for exactly its 300ms minimum dwell', () => {
    const { result, rerender } = renderHook(({ pending }) => useLoadingVisibility(pending), { initialProps: { pending: true } })
    advance(400)
    expect(result.current).toBe(true)
    rerender({ pending: false })
    advance(299)
    expect(result.current).toBe(true)
    advance(1)
    expect(result.current).toBe(false)
    advance(1)
    expect(result.current).toBe(false)
  })

  it('hides immediately when pending ends after the minimum dwell', () => {
    const { result, rerender } = renderHook(({ pending }) => useLoadingVisibility(pending), { initialProps: { pending: true } })
    advance(701)
    rerender({ pending: false })
    expect(result.current).toBe(false)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('cancels a scheduled hide when work restarts during minimum dwell', () => {
    const { result, rerender } = renderHook(({ pending }) => useLoadingVisibility(pending), { initialProps: { pending: true } })
    advance(400)
    rerender({ pending: false })
    advance(100)
    rerender({ pending: true })
    advance(200)
    expect(result.current).toBe(true)
    advance(400)
    expect(result.current).toBe(true)
    rerender({ pending: false })
    expect(result.current).toBe(false)
  })

  it('starts a fresh delay after a cancelled operation', () => {
    const { result, rerender } = renderHook(({ pending }) => useLoadingVisibility(pending), { initialProps: { pending: true } })
    advance(200)
    rerender({ pending: false })
    rerender({ pending: true })
    advance(399)
    expect(result.current).toBe(false)
    advance(1)
    expect(result.current).toBe(true)
  })

  it('leaves no pending timer after unmount under StrictMode', () => {
    const { unmount } = renderHook(() => useLoadingVisibility(true), { wrapper: StrictMode })
    expect(vi.getTimerCount()).toBe(1)
    unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})
