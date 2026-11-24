import { describe, it, expect, vi, afterEach } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import type { PendingRestartEntry } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchPendingRestart: vi.fn() }
})

import { fetchPendingRestart } from '@/lib/api'
import { usePendingRestart, PENDING_RESTART_POLL_INTERVAL } from './restart'

function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return React.createElement(QueryClientProvider, { client }, children)
  }
}

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
}

// C-2 (round 1, review MAJ-006) makes `reason` a required member of every
// PendingRestartEntry: "restart_required" (a restart-gated key whose saved
// value differs from the boot value) or "apply_failed" (a hot-reloaded key
// whose last save did not apply). The fixtures below carry both spellings.
// The narrow cast exists because the landing branch's generated types do not
// carry `reason` yet (it arrives with the frontend/backend merge); the cast
// never removes a required field — it only lets this fixture carry a member
// the older generated type does not know.
const entry = (e: {
  key: string
  applied_value: unknown
  persisted_value: unknown
  reason: 'restart_required' | 'apply_failed'
}): PendingRestartEntry => e as PendingRestartEntry

describe('usePendingRestart', () => {
  afterEach(() => {
    vi.resetAllMocks()
  })

  it('initial fetch returns empty array — store shows no entries', async () => {
    const client = makeClient()
    vi.mocked(fetchPendingRestart).mockResolvedValue([])

    const { result } = renderHook(() => usePendingRestart(), {
      wrapper: makeWrapper(client),
    })

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false)
    })

    expect(result.current.entries).toEqual([])
    expect(result.current.isError).toBe(false)
    client.clear()
  })

  it('fetch returns entries — store exposes them', async () => {
    const client = makeClient()
    const entries: PendingRestartEntry[] = [
      entry({ key: 'security.prompt_guard', applied_value: 'low', persisted_value: 'high', reason: 'restart_required' }),
      entry({ key: 'security.audit_log', applied_value: false, persisted_value: true, reason: 'restart_required' }),
    ]
    vi.mocked(fetchPendingRestart).mockResolvedValue(entries)

    const { result } = renderHook(() => usePendingRestart(), {
      wrapper: makeWrapper(client),
    })

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false)
    })

    expect(result.current.entries).toHaveLength(2)
    expect(result.current.entries[0].key).toBe('security.prompt_guard')
    expect(result.current.entries[1].key).toBe('security.audit_log')
    client.clear()
  })

  it('isError is true when fetch throws', async () => {
    const client = makeClient()
    vi.mocked(fetchPendingRestart).mockRejectedValue(new Error('network error'))

    const { result } = renderHook(() => usePendingRestart(), {
      wrapper: makeWrapper(client),
    })

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false)
    })

    expect(result.current.isError).toBe(true)
    expect(result.current.entries).toEqual([])
    client.clear()
  })

  it('polling: poll interval constant is 10000ms', () => {
    expect(PENDING_RESTART_POLL_INTERVAL).toBe(10_000)
  })

  it('refetch() re-calls fetchPendingRestart and updates entries', async () => {
    const client = makeClient()
    vi.mocked(fetchPendingRestart)
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([entry({ key: 'x', applied_value: 1, persisted_value: 2, reason: 'apply_failed' })])

    const { result } = renderHook(() => usePendingRestart(), {
      wrapper: makeWrapper(client),
    })

    await waitFor(() => {
      expect(result.current.isLoading).toBe(false)
    })

    expect(result.current.entries).toHaveLength(0)

    await act(async () => {
      await result.current.refetch()
    })

    await waitFor(() => {
      expect(result.current.entries).toHaveLength(1)
    })
    expect(result.current.entries[0].key).toBe('x')
    client.clear()
  })
})
