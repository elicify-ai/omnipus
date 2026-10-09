import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { fetchSessions } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'

/**
 * Saved lifecycle and successful-read version from the sidebar's shared
 * ['sessions'] list. The optional lifecycle can be unknown; a failed refetch
 * can retain older data, which must not clear a newly confirmed restart.
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
  const { data, isFetching } = useQuery({
    queryKey: ['sessions'],
    queryFn: () => fetchSessions(),
    staleTime: 15_000,
    enabled: !!sessionId && sessionId !== '__pending',
    select: (list) => list.find((session) => session.id === sessionId),
  }, queryClient)
  return {
    state: data?.lifecycle_state, stopCause: data?.stop_note?.cause,
    version: queryClient.getQueryState(['sessions'])?.dataUpdateCount ?? 0,
    settled: !isFetching,
  }
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
 * A held-open tab also knows a restart cut its answer off once its boot-mismatch
 * catch-up completes. That per-chat signal chooses the same single notice even
 * while REST is pending, missing the optional lifecycle, or failed. A later
 * successful list read can clear it when another tab continued the chat. Known
 * boundary: fresh/reloaded tabs still need the optional saved lifecycle;
 * unknown_position alone proves no interruption.
 */
export function useRestartInterrupted(): boolean {
  const sessionId = useSessionStore((s) => s.activeSessionId)
  const saved = useSavedLifecycle(sessionId)
  const running = useChatStore((s) =>
    s.isStreaming || (sessionId != null && s.sessionsById[sessionId]?.activeTurnId != null),
  )
  const { observed, progressed } = useObservedRestart(sessionId, saved)
  const dismissed = useChatStore((s) => (sessionId != null && !!s.sessionsById[sessionId]?.restartNoticeDismissed))
  return ((observed && !progressed) || saved.state === 'interrupted') && !running && !dismissed
}

/** Ends the notice at the user's own action before the new turn's first frame arrives. */
export function dismissRestartNotice(sessionId: string) {
  setDismissed(sessionId, true)
}

function setDismissed(sessionId: string, value: boolean, clearObserved = false) {
  useChatStore.setState((state) => {
    const bucket = state.sessionsById[sessionId]
    if (!bucket || (!!bucket.restartNoticeDismissed === value && (!clearObserved || !bucket.restartInterruptedBootId))) return state
    return { sessionsById: { ...state.sessionsById, [sessionId]: {
      ...bucket, restartNoticeDismissed: value,
      ...(clearObserved ? { restartInterruptedBootId: undefined, restartInterruptedListVersion: undefined } : {}),
    } } }
  })
}

function useObservedRestart(sessionId: string | null, saved: ReturnType<typeof useSavedLifecycle>) {
  const bootId = useChatStore((s) => sessionId != null ? s.sessionsById[sessionId]?.restartInterruptedBootId : undefined)
  const currentBootId = useChatStore((s) => sessionId != null ? s.sessionsById[sessionId]?.cursor?.bootId : undefined)
  const listVersion = useChatStore((s) => sessionId != null ? s.sessionsById[sessionId]?.restartInterruptedListVersion : undefined)
  const observed = bootId !== undefined && bootId === currentBootId
  const progressed = observed && saved.settled && saved.version > (listVersion ?? saved.version) &&
    (saved.state === 'working' || saved.state === 'done' || saved.state === 'failed' ||
      (saved.state === 'stopped' && saved.stopCause != null && saved.stopCause !== 'restart'))
  return { observed, progressed, oldBoot: bootId !== undefined && currentBootId !== undefined && bootId !== currentBootId }
}

/** Mounted once by the notice; local clicks and a new interrupted boot also write dismissal. */
export function useRestartNoticeDismissal(): void {
  const sessionId = useSessionStore((s) => s.activeSessionId)
  const saved = useSavedLifecycle(sessionId)
  const running = useChatStore((s) =>
    s.isStreaming || (sessionId != null && s.sessionsById[sessionId]?.activeTurnId != null),
  )
  const { observed, progressed, oldBoot } = useObservedRestart(sessionId, saved)
  useEffect(() => {
    if (!sessionId) return
    if (oldBoot || progressed) setDismissed(sessionId, false, true)
    else if (running && (observed || saved.state === 'interrupted')) setDismissed(sessionId, true)
    else if (!observed && saved.state !== undefined && saved.state !== 'interrupted') setDismissed(sessionId, false)
  }, [sessionId, saved.state, running, observed, progressed, oldBoot])
}
