/**
 * connection.agentsRefetch.test.ts — founder decision 2026-10-06: a tab always
 * has a fresh agent list after any websocket connect or reconnect, whether or
 * not the agent picker is mounted. The real connection store and the real
 * production QueryClient are used; no React component is mounted, so the
 * picker (and useAgentsCrossTabRefresh) is provably not involved. Marking the
 * query invalidated is observed on the cache entry itself.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { queryClient } from '@/lib/queryClient'
import { useConnectionStore } from './connection'

function seedFreshAgents() {
  queryClient.setQueryData(['agents'], [])
  expect(queryClient.getQueryState(['agents'])?.isInvalidated).toBe(false)
}

describe('connection store -> agents query (connect / reconnect)', () => {
  beforeEach(() => {
    queryClient.clear()
    useConnectionStore.setState({ isConnected: false, disconnectedAt: null })
  })
  afterEach(() => {
    vi.restoreAllMocks()
    queryClient.clear()
    useConnectionStore.setState({ isConnected: false, disconnectedAt: null })
  })

  it('invalidates the agents query when the socket first connects, with no picker mounted', () => {
    seedFreshAgents()
    useConnectionStore.getState().setConnected(true)
    expect(queryClient.getQueryState(['agents'])?.isInvalidated).toBe(true)
  })

  it('invalidates again on a reconnect after a drop, not on the drop itself', () => {
    useConnectionStore.getState().setConnected(true)
    seedFreshAgents()

    useConnectionStore.getState().recordDisconnect(null)
    expect(queryClient.getQueryState(['agents'])?.isInvalidated).toBe(false)

    useConnectionStore.getState().setConnected(true)
    expect(queryClient.getQueryState(['agents'])?.isInvalidated).toBe(true)
  })

  it('invalidates exactly once per false -> true transition', () => {
    const spy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined)
    useConnectionStore.getState().setConnected(true)
    useConnectionStore.getState().setConnected(true)
    expect(spy).toHaveBeenCalledTimes(1)
    expect(spy).toHaveBeenCalledWith({ queryKey: ['agents'] })
  })
})
