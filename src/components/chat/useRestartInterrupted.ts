import { useQuery } from '@tanstack/react-query'
import { fetchSessions } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'

/**
 * True when the active chat's SAVED lifecycle (REST `Session.lifecycle_state`,
 * the same ['sessions'] list the sidebar renders) says a gateway restart cut
 * its turn off (`interrupted`) and nothing is running for it now.
 *
 * Why the saved state and not the WebSocket attach: no WS frame carries the
 * lifecycle, and `session_snapshot.reason === 'boot_mismatch'` is only seen by
 * a tab that watched the old boot. A tab opened after the restart (fresh tab,
 * reload) never sees it, so the saved REST state is the one signal every tab
 * shares. Reading the same query key as the sidebar keeps body and sidebar in
 * agreement by construction (no extra request when the sidebar is mounted).
 *
 * Binds to the app's singleton query client (the one main.tsx provides) rather
 * than the nearest provider, so the status rows that call it need no provider.
 *
 * Hidden the moment a turn runs again — the user's next message continues the
 * same chat and the saved state settles to its new value on the next refresh.
 */
export function useRestartInterrupted(): boolean {
  const sessionId = useSessionStore((s) => s.activeSessionId)
  const running = useChatStore((s) =>
    s.isStreaming || (sessionId != null && s.sessionsById[sessionId]?.activeTurnId != null),
  )
  const { data: saved } = useQuery({
    queryKey: ['sessions'],
    queryFn: () => fetchSessions(),
    staleTime: 15_000,
    enabled: !!sessionId && sessionId !== '__pending',
    select: (list) => list.find((session) => session.id === sessionId)?.lifecycle_state,
  }, queryClient)
  return saved === 'interrupted' && !running
}
