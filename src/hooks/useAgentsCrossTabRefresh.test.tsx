/**
 * useAgentsCrossTabRefresh.test.tsx — agent-picker freshness fix, focus half
 * (2026-09-28 fix round; GitHub issue #1009).
 *
 * Mirrors src/components/library/useLibraryCrossTabRefresh.test.tsx (D-107)
 * almost verbatim. The WS agent_created frame covers the tab that never
 * sees a focus event (two windows side by side); this hook is the OTHER
 * half: the tab the user returns to gets an explicit invalidation on window
 * focus and on visibilitychange→visible, independent of staleTime —
 * TanStack's built-in refetchOnWindowFocus only refetches queries that are
 * already stale, so a picker inside its staleTime window could still omit
 * a newly created agent for that window's duration after focus. Asserts the
 * invalidation fires for BOTH events, does NOT fire while hidden, and
 * cleans its listeners on unmount.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { fireEvent, act } from '@testing-library/react'

import { useAgentsCrossTabRefresh } from './useAgentsCrossTabRefresh'
import { queryClient } from '@/lib/queryClient'
import { useConnectionStore } from '@/store/connection'

function setVisibility(state: DocumentVisibilityState) {
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue(state)
}

describe('useAgentsCrossTabRefresh (agent-picker freshness fix, focus path)', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    setVisibility('visible')
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('window focus invalidates the agents query', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined)

    renderHook(() => useAgentsCrossTabRefresh())

    act(() => {
      fireEvent(window, new Event('focus'))
    })

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['agents'] })
  })

  it('returning to the tab (visibilitychange → visible) invalidates too', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined)

    renderHook(() => useAgentsCrossTabRefresh())

    setVisibility('visible')
    act(() => {
      fireEvent(document, new Event('visibilitychange'))
    })

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['agents'] })
  })

  it('going hidden fires nothing, and unmount removes the listeners', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined)

    const { unmount } = renderHook(() => useAgentsCrossTabRefresh())
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
})

// Founder decision 2026-10-06: a tab whose websocket connects or reconnects
// reloads the agent list, so an agent created while the socket was not yet
// open (or was down) shows up without a page reload. The signal is the app's
// own connection store (`isConnected`, driven by WsLifecycle's onConnected /
// onDisconnected via setConnected) — the same flag the rest of the SPA reads.
describe('useAgentsCrossTabRefresh (websocket connect / reconnect path)', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    useConnectionStore.setState({ isConnected: false, disconnectedAt: null })
  })
  afterEach(() => {
    vi.restoreAllMocks()
    useConnectionStore.setState({ isConnected: false, disconnectedAt: null })
  })

  it('invalidates the agents query exactly once when the socket first connects', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined)
    renderHook(() => useAgentsCrossTabRefresh())
    invalidateSpy.mockClear()

    act(() => {
      useConnectionStore.getState().setConnected(true)
    })

    expect(invalidateSpy).toHaveBeenCalledTimes(1)
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['agents'] })
  })

  it('invalidates again on every reconnect, but not on the drop itself', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined)
    renderHook(() => useAgentsCrossTabRefresh())
    act(() => {
      useConnectionStore.getState().setConnected(true)
    })
    invalidateSpy.mockClear()

    act(() => {
      useConnectionStore.getState().setConnected(false)
    })
    expect(invalidateSpy).not.toHaveBeenCalled()

    act(() => {
      useConnectionStore.getState().setConnected(true)
    })
    expect(invalidateSpy).toHaveBeenCalledTimes(1)
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['agents'] })
  })

  it('does not re-invalidate on unrelated connection-store updates while connected', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined)
    renderHook(() => useAgentsCrossTabRefresh())
    act(() => {
      useConnectionStore.getState().setConnected(true)
    })
    invalidateSpy.mockClear()

    act(() => {
      useConnectionStore.getState().setConnected(true)
      useConnectionStore.getState().setConnectionError(null)
    })
    expect(invalidateSpy).not.toHaveBeenCalled()
  })

  it('stops reacting to the connection after unmount', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined)
    const { unmount } = renderHook(() => useAgentsCrossTabRefresh())
    unmount()
    invalidateSpy.mockClear()

    act(() => {
      useConnectionStore.getState().setConnected(true)
    })
    expect(invalidateSpy).not.toHaveBeenCalled()
  })
})
