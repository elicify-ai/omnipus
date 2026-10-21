import { useEffect, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { fetchSessionMessages } from '@/lib/api'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { pendingClearRefetches } from '@/store/chat/runtime-state'
import { ErrorState } from '@/components/ui/error-state'
import { Skeleton } from '@/components/ui/skeleton'
import { HistoryBoundary } from './HistoryBoundary'

export function useChatHistory() {
  const sid = useSessionStore(s => s.activeSessionId)
  const connected = useConnectionStore(s => s.isConnected)
  const replaying = useChatStore(s => s.isReplaying)
  const history = useChatStore(s => s.history)
  const generation = useRef(history.viewGeneration)
  const query = useQuery({
    queryKey: ['messages', sid],
    queryFn: ({ signal }) => {
      generation.current = useChatStore.getState().sessionsById[sid!]?.history.viewGeneration ?? 0
      return fetchSessionMessages(sid!, { signal })
    },
    enabled: !!sid && sid !== '__pending' && !connected && !replaying && !history.historyLoaded && history.serverState?.status !== 'unavailable',
    gcTime: 0, retry: false, refetchOnWindowFocus: false, refetchOnMount: false,
  })
  useEffect(() => {
    if (!sid || !query.data || query.data.session_id !== sid || replaying) return
    useChatStore.getState().acceptHistoryFallback(sid, query.data, generation.current)
  }, [sid, query.data, replaying])
  const gone = query.isError && (query.error as { status?: number } | null)?.status === 404
  return { ...query, sessionGone: gone, showWelcome: !sid || (history.historyLoaded && history.viewMode === 'latest' && !gone && history.serverState?.status !== 'unavailable') }
}

/** History-only failure/loading slot. The thread shell and its current controls
 * remain mounted; a missing index cannot masquerade as an empty welcome. */
export function HistoryOpenState({ read }: { read: ReturnType<typeof useChatHistory> }) {
  const sid = useSessionStore(s => s.activeSessionId)
  const history = useChatStore(s => s.history)
  const count = useChatStore(s => s.messages.length)
  if (history.serverState?.status === 'unavailable') return count > 0
    ? <p className="px-[var(--space-3)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Cached messages may be out of date. Live output — earlier history unavailable</p>
    : null
  if (read.isError && !history.liveControlsReady) return <ErrorState message="Could not load messages." onRetry={read.sessionGone ? undefined : () => {
    if (sid && pendingClearRefetches[sid]) useChatStore.getState().retryClearTranscript(sid)
    else void read.refetch()
  }} />
  if (history.requests.latest.error && history.viewMode === 'latest') return <HistoryBoundary action="latest" />
  if (sid && !history.historyLoaded && count === 0 && !read.isError) return <div className="p-[var(--space-3)]" aria-busy="true">
    <Skeleton className="h-8 w-full" />
    <p role="status" className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Loading messages…</p>
  </div>
  return null
}
