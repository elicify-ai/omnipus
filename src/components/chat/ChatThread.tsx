// Standalone bounded thread for embedding; the full screen uses ChatMessageList.
import { useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getMessages, useChatStore } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { fetchSessionMessages } from '@/lib/api'
import { ErrorState } from '@/components/ui/error-state'
import { Skeleton } from '@/components/ui/skeleton'
import { MessageItem } from './MessageItem'
import { RoomTrafficMessage } from './RoomTrafficEntry'
import { ROOMS_ENABLED } from '@/lib/featureSwitches'

interface ChatThreadProps {
  /** Load and display the newest bounded page for this session. */
  sessionId?: string
}

export function ChatThread({ sessionId }: ChatThreadProps) {
  const bucket = useChatStore(state => sessionId ? state.sessionsById[sessionId] : undefined)
  const foreground = useChatStore(state => state.messages)
  const messages = bucket ? getMessages(bucket) : sessionId ? [] : foreground
  const reading = useRef<{ sid: string; generation: number; baseline: Record<string, ChatMessage> } | null>(null)
  const [failure, setFailure] = useState<{ sid: string; attempt: number } | null>(null)
  const query = useQuery({
    queryKey: ['messages', sessionId],
    queryFn: async ({ signal }) => {
      const current = useChatStore.getState().sessionsById[sessionId!]
      const owner = { sid: sessionId!, generation: current?.history.viewGeneration ?? 0, baseline: { ...current?.messagesById } }
      reading.current = owner
      try { return await fetchSessionMessages(sessionId!, { signal }) }
      catch (error) { owner.baseline = {}; if (reading.current === owner) reading.current = null; throw error }
    },
    enabled: !!sessionId && !bucket?.isReplaying && !bucket?.history.historyLoaded && bucket?.history.serverState?.status !== 'unavailable',
    gcTime: 0, retry: false, refetchOnWindowFocus: false, refetchOnMount: 'always',
  })
  useEffect(() => {
    const owner = reading.current
    if (!owner || !query.data || query.isFetching || query.isError || owner.sid !== sessionId || query.data.session_id !== sessionId) return
    useChatStore.getState().acceptHistoryFallback(sessionId, query.data, owner.generation, owner.baseline)
    reading.current = null
    setFailure(null)
  }, [sessionId, query.data, query.dataUpdatedAt, query.isFetching, query.isError])
  useEffect(() => {
    if (sessionId && query.isError) setFailure(previous => ({ sid: sessionId, attempt: previous?.sid === sessionId ? previous.attempt + 1 : 1 }))
  }, [sessionId, query.error, query.isError])
  useEffect(() => () => {
    if (reading.current && reading.current.sid === sessionId) { reading.current.baseline = {}; reading.current = null }
    if (sessionId && useSessionStore.getState().activeSessionId !== sessionId) useChatStore.getState().releaseSessionHistory(sessionId)
  }, [sessionId])
  const unavailable = bucket?.history.serverState?.status === 'unavailable'
  const error = unavailable ? bucket.history.serverState!.message : failure?.sid === sessionId ? 'Could not load messages.' : undefined
  const loading = query.isFetching || bucket?.history.requests.latest.status === 'loading'
  const retry = () => {
    if (!sessionId || loading) return
    if (unavailable && useSessionStore.getState().activeSessionId === sessionId) void useChatStore.getState().retryHistory(sessionId, 'latest')
    else void query.refetch()
  }

  return (
    <div role="log" aria-label="Chat messages" aria-live="off" aria-busy={loading || undefined}>
      {error && <ErrorState message={`${error}${failure && failure.sid === sessionId && failure.attempt > 1 ? ` Retry ${failure.attempt} failed.` : ''}`} onRetry={retry} retryDisabled={loading} />}
      {loading && <>
        <Skeleton className="h-4 w-32" />
        <p role="status">Loading messages…</p>
      </>}
      {unavailable && messages.length > 0 && <p>Cached messages may be out of date. Live output — earlier history unavailable</p>}
      {messages.map(msg => (
        // FR-202: group-chat traffic stays collapsed until WU-15 (#1314).
        ROOMS_ENABLED && msg.roomRef ? <RoomTrafficMessage key={msg.historyPosition ?? msg.id} message={msg} /> : <MessageItem key={msg.historyPosition ?? msg.id} message={msg} />
      ))}
    </div>
  )
}
