import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { fetchSessions } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'

/**
 * Saved lifecycle (REST `Session.lifecycle_state`) of the active chat, read from
 * the same ['sessions'] list the sidebar renders. Undefined while the list is
 * loading, failed, or has no row for this chat.
 *
 * Why the saved state and not the WebSocket attach: no WS frame carries the
 * lifecycle, and `session_snapshot.reason === 'boot_mismatch'` is only seen by
 * a tab that watched the old boot. A tab opened after the restart (fresh tab,
 * reload) never sees it, so the saved REST state is the one signal every tab
 * shares. ChatScreen loads only `/messages` (no per-session detail), so the list
 * is the single existing source; no second observer or request is added when the
 * sidebar is mounted. Binds to the app's singleton query client (the one
 * main.tsx provides) so the status rows that call this need no provider.
 */
function useSavedLifecycle(sessionId: string | null) {
  const { data } = useQuery({
    queryKey: ['sessions'],
    queryFn: () => fetchSessions(),
    staleTime: 15_000,
    enabled: !!sessionId && sessionId !== '__pending',
    select: (list) => list.find((session) => session.id === sessionId)?.lifecycle_state,
  }, queryClient)
  return data
}

/**
 * True when a gateway restart cut the active chat's turn off (saved lifecycle
 * `interrupted`), nothing is running for it, and the user has not continued it
 * since. The notice ends on the user's own action, not on the list's freshness:
 * once a turn runs for the chat (RestartInterruptedNotice records it), the list
 * saying `interrupted` again — a refetch that beat the server's lifecycle commit,
 * a failed refetch keeping old data — cannot bring it back until the saved value
 * is seen at something else.
 *
 * Degraded: while the list is loading, errored or lacks the row the saved value
 * is unknown, so this is false — no notice, and the older boot-mismatch
 * "couldn't be finished" statuses behave exactly as before. It never reports
 * Working for a chat it knows nothing about.
 */
export function useRestartInterrupted(): boolean {
  const sessionId = useSessionStore((s) => s.activeSessionId)
  const saved = useSavedLifecycle(sessionId)
  const running = useChatStore((s) =>
    s.isStreaming || (sessionId != null && s.sessionsById[sessionId]?.activeTurnId != null),
  )
  const dismissed = useChatStore((s) => (sessionId != null && !!s.sessionsById[sessionId]?.restartNoticeDismissed))
  return saved === 'interrupted' && !running && !dismissed
}

/** Ends the notice at the user's own action before the new turn's first frame arrives. */
export function dismissRestartNotice(sessionId: string) {
  setDismissed(sessionId, true)
}

function setDismissed(sessionId: string, value: boolean) {
  useChatStore.setState((state) => {
    const bucket = state.sessionsById[sessionId]
    if (!bucket || !!bucket.restartNoticeDismissed === value) return state
    return { sessionsById: { ...state.sessionsById, [sessionId]: { ...bucket, restartNoticeDismissed: value } } }
  })
}

/** Single writer of the dismissal (mounted once, by RestartInterruptedNotice). */
export function useRestartNoticeDismissal(): void {
  const sessionId = useSessionStore((s) => s.activeSessionId)
  const saved = useSavedLifecycle(sessionId)
  const running = useChatStore((s) =>
    s.isStreaming || (sessionId != null && s.sessionsById[sessionId]?.activeTurnId != null),
  )
  useEffect(() => {
    if (!sessionId) return
    if (running && saved === 'interrupted') setDismissed(sessionId, true)
    else if (saved !== undefined && saved !== 'interrupted') setDismissed(sessionId, false)
  }, [sessionId, saved, running])
}
