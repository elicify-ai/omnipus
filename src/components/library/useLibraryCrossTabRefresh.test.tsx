/**
 * useLibraryCrossTabRefresh.test.tsx — D-107, focus half (2026-09-14 fix
 * round).
 *
 * The WS library_changed frame covers the tab that never sees a focus event
 * (two windows side by side). This hook is the OTHER half: the tab the user
 * returns to gets an explicit invalidation on window focus and on
 * visibilitychange→visible, independent of staleTime — TanStack's built-in
 * refetchOnWindowFocus only refetches queries that are already stale, so a
 * listing inside its 10 s staleTime window could still serve deleted rows
 * for that window's duration after focus. Asserts the invalidation fires for
 * BOTH events, does NOT fire while hidden, and cleans its listeners on
 * unmount.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { fireEvent, act } from '@testing-library/react'

import { useLibraryCrossTabRefresh } from './useLibraryCrossTabRefresh'
import { queryClient } from '@/lib/queryClient'
import { libraryQueryKeys } from '@/lib/api'

function setVisibility(state: DocumentVisibilityState) {
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue(state)
}

describe('useLibraryCrossTabRefresh (D-107 focus path)', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    setVisibility('visible')
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('window focus invalidates the workspace listings and the workspaces list', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined)

    renderHook(() => useLibraryCrossTabRefresh('ws-1'))

    act(() => {
      fireEvent(window, new Event('focus'))
    })

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['library', 'ws-1'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: libraryQueryKeys.workspaces() })
  })

  it('returning to the tab (visibilitychange → visible) invalidates too', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined)

    renderHook(() => useLibraryCrossTabRefresh('ws-2'))

    setVisibility('visible')
    act(() => {
      fireEvent(document, new Event('visibilitychange'))
    })

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['library', 'ws-2'] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: libraryQueryKeys.workspaces() })
  })

  it('going hidden fires nothing, and unmount removes the listeners', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined)

    const { unmount } = renderHook(() => useLibraryCrossTabRefresh('ws-3'))
    invalidateSpy.mockClear()

    setVisibility('hidden')
    act(() => {
      fireEvent(document, new Event('visibilitychange'))
    })
    expect(invalidateSpy).not.toHaveBeenCalled()

    unmount()

    setVisibility('visible')
    act(() => {
      fireEvent(document, new Event('visibilitychange'))
      fireEvent(window, new Event('focus'))
    })
    expect(invalidateSpy).not.toHaveBeenCalled()
  })

  it('no workspace selected (virtual root) still refreshes the workspaces list', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined)

    renderHook(() => useLibraryCrossTabRefresh(null))

    act(() => {
      fireEvent(window, new Event('focus'))
    })

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: libraryQueryKeys.workspaces() })
    expect(invalidateSpy).not.toHaveBeenCalledWith({ queryKey: ['library'] })
  })
})
