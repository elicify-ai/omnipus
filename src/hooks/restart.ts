import { useQuery } from '@tanstack/react-query'
import { fetchPendingRestart, type PendingRestartEntry } from '@/lib/api'

export const PENDING_RESTART_QUERY_KEY = ['pending-restart'] as const

export const PENDING_RESTART_POLL_INTERVAL = 10_000

// Re-export for backward compat with callers that imported ApiError from here
export type { ApiError } from '@/lib/api'

export function usePendingRestart(): {
  entries: PendingRestartEntry[]
  isLoading: boolean
  isError: boolean
  error: unknown
  refetch: () => Promise<unknown>
} {
  const { data, isLoading, isError, error, refetch } = useQuery({
    queryKey: PENDING_RESTART_QUERY_KEY,
    queryFn: fetchPendingRestart,
    refetchInterval: PENDING_RESTART_POLL_INTERVAL,
  })

  return {
    entries: data ?? [],
    isLoading,
    isError,
    error: error ?? null,
    refetch,
  }
}
