// useLibraryAutoRefresh.test.tsx — the R1 gate of the Library's background
// self-refresh (founder decision 2026-10-30, "Q8b A"): true while the tab is
// visible AND the window is focused, false the moment the tab hides or the
// window blurs, back on focus/visibility. This is the condition under which
// LibraryExplorer's re-check interval is allowed to run at all — no requests
// while closed (the explorer unmounts), hidden, or unfocused.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, fireEvent } from '@testing-library/react'

import { LIBRARY_AUTO_REFRESH_INTERVAL_MS, useLibraryAutoRefresh } from './useLibraryAutoRefresh'

describe('useLibraryAutoRefresh (R1 gate)', () => {
  let hasFocusSpy: ReturnType<typeof vi.spyOn>
  let visibilitySpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    vi.restoreAllMocks()
    hasFocusSpy = vi.spyOn(document, 'hasFocus').mockReturnValue(true)
    visibilitySpy = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('is true while the tab is visible and the window is focused', () => {
    const { result } = renderHook(() => useLibraryAutoRefresh())
    expect(result.current).toBe(true)
  })

  it('goes false when the tab is hidden and true again when it returns', () => {
    const { result } = renderHook(() => useLibraryAutoRefresh())

    visibilitySpy.mockReturnValue('hidden')
    act(() => {
      fireEvent(document, new Event('visibilitychange'))
    })
    expect(result.current).toBe(false)

    visibilitySpy.mockReturnValue('visible')
    act(() => {
      fireEvent(document, new Event('visibilitychange'))
    })
    expect(result.current).toBe(true)
  })

  it('goes false when the window loses focus and true again on focus', () => {
    const { result } = renderHook(() => useLibraryAutoRefresh())

    hasFocusSpy.mockReturnValue(false)
    act(() => {
      fireEvent(window, new Event('blur'))
    })
    expect(result.current).toBe(false)

    hasFocusSpy.mockReturnValue(true)
    act(() => {
      fireEvent(window, new Event('focus'))
    })
    expect(result.current).toBe(true)
  })

  it('starts false when mounted on a hidden tab (no requests until visible)', () => {
    visibilitySpy.mockReturnValue('hidden')
    const { result } = renderHook(() => useLibraryAutoRefresh())
    expect(result.current).toBe(false)
  })

  it('keeps the whole Library on one 10-second cadence', () => {
    // The listing re-check and any future consumer must not drift apart:
    // one constant, one cadence.
    expect(LIBRARY_AUTO_REFRESH_INTERVAL_MS).toBe(10_000)
  })
})
