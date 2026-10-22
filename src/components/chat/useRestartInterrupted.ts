import { useEffect, useSyncExternalStore } from 'react'
import { hashKey, useQuery } from '@tanstack/react-query'
import { fetchSessions } from '@/lib/api'
import { interruptionNoticeDetail } from '@/lib/subagentStatus'
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
  // A re-save of the byte-identical list (structural sharing keeps the same
  // data reference) notifies no useQuery observer — its update count is not a
  // tracked result prop — yet that re-save IS a new saved observation, the one
  // signal a second same-boot ceiling needs (DESIGN Decision 1: re-arm for
  // each new interrupted outcome/turn). Subscribing to the cache makes the
  // update count a real reactive input instead of a render-by-accident read
  // (same rationale as useQueryRefreshFailed: cache listeners run
  // synchronously, the observer defers).
  const version = useSyncExternalStore(
    (onChange) => queryClient.getQueryCache().subscribe((event) => {
      if (event.query.queryHash === hashKey(['sessions'])) onChange()
    }),
    () => queryClient.getQueryState(['sessions'])?.dataUpdateCount ?? 0,
    () => 0,
  )
  return {
    state: data?.lifecycle_state,
    reason: data?.interruption_reason,
    updatedAt: data?.updated_at,
    stopCause: data?.stop_note?.cause,
    version,
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
  const ended = useChatStore((s) => (sessionId != null ? s.sessionsById[sessionId]?.endedInterruption : undefined))
  const listStillPrevious = ended != null && (!saved.settled || saved.version <= ended.listVersion)
  const liveEnded = ended != null && (listStillPrevious || saved.state === 'interrupted' || saved.state === undefined)
  return ((observed && !progressed) || saved.state === 'interrupted' || liveEnded) && !running && !dismissed
}

/** Cause sentence for the single notice. Restart copy when the cause is absent or restart. */
export function useInterruptionNoticeDetail(): string {
  const sessionId = useSessionStore((s) => s.activeSessionId)
  const saved = useSavedLifecycle(sessionId)
  const endedReason = useChatStore((s) => (sessionId != null ? s.sessionsById[sessionId]?.endedInterruption?.reason : undefined))
  const reason = endedReason ?? (saved.state === 'interrupted' ? saved.reason : undefined)
  return interruptionNoticeDetail(reason)
}

/** Ends the notice at the user's own action before the new turn's first frame
 * arrives. An explicit dismissal also settles the saved interrupted report at
 * the current list version (restartNoticeResolvedAt): later refetches of the
 * same stale row must not re-arm the notice the user just dismissed. */
export function dismissRestartNotice(sessionId: string) {
  const currentVersion = queryClient.getQueryState(['sessions'])?.dataUpdateCount ?? 0
  setDismissed(sessionId, true, false, undefined, false, currentVersion, currentVersion)
}

function setDismissed(
  sessionId: string,
  value: boolean,
  clearObserved = false,
  seenAt?: string,
  clearEnded = false,
  seenVersion?: number,
  resolvedAt?: number,
  reopenReport = false,
) {
  useChatStore.setState((state) => {
    const bucket = state.sessionsById[sessionId]
    if (!bucket) return state
    const nextAt = value ? (seenAt ?? bucket.restartNoticeDismissedAt) : undefined
    const nextEnded = clearObserved || clearEnded ? undefined : bucket.endedInterruption
    const nextBoot = clearObserved ? undefined : bucket.restartInterruptedBootId
    const nextObservedVersion = clearObserved ? undefined : bucket.restartInterruptedListVersion
    // Binding the dismissal to the list read that produced it (seenVersion)
    // and settling or reopening the report (resolvedAt / reopenReport) is what
    // lets a second same-boot ceiling re-arm while a stale refetch after a
    // witnessed completion never does — see useRestartNoticeDismissal.
    const nextDismissedVersion = value ? (seenVersion ?? bucket.restartNoticeDismissedListVersion) : undefined
    const nextResolvedAt = value
      ? (reopenReport ? undefined : (resolvedAt ?? bucket.restartNoticeResolvedAt))
      : undefined
    if (
      !!bucket.restartNoticeDismissed === value
      && bucket.restartNoticeDismissedAt === nextAt
      && bucket.endedInterruption === nextEnded
      && bucket.restartInterruptedBootId === nextBoot
      && bucket.restartInterruptedListVersion === nextObservedVersion
      && bucket.restartNoticeDismissedListVersion === nextDismissedVersion
      && bucket.restartNoticeResolvedAt === nextResolvedAt
    ) return state
    return { sessionsById: { ...state.sessionsById, [sessionId]: {
      ...bucket,
      restartNoticeDismissed: value,
      restartNoticeDismissedAt: nextAt,
      endedInterruption: nextEnded,
      restartInterruptedBootId: nextBoot,
      restartInterruptedListVersion: nextObservedVersion,
      restartNoticeDismissedListVersion: nextDismissedVersion,
      restartNoticeResolvedAt: nextResolvedAt,
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
  const ended = useChatStore((s) => (sessionId != null ? s.sessionsById[sessionId]?.endedInterruption : undefined))
  const dismissed = useChatStore((s) => (sessionId != null && !!s.sessionsById[sessionId]?.restartNoticeDismissed))
  const dismissedAt = useChatStore((s) => (sessionId != null ? s.sessionsById[sessionId]?.restartNoticeDismissedAt : undefined))
  const dismissedVersion = useChatStore((s) => (sessionId != null ? s.sessionsById[sessionId]?.restartNoticeDismissedListVersion : undefined))
  const resolvedAt = useChatStore((s) => (sessionId != null ? s.sessionsById[sessionId]?.restartNoticeResolvedAt : undefined))
  useEffect(() => {
    if (!sessionId) return
    const listStillPrevious = ended != null && (!saved.settled || saved.version <= ended.listVersion)
    if (oldBoot || progressed) setDismissed(sessionId, false, true)
    // The continuation dismissal binds itself to the current list read and
    // reopens the report: the new turn's outcome is unknown until the UI
    // witnesses how it ends.
    else if (running && (observed || saved.state === 'interrupted' || ended != null)) setDismissed(sessionId, true, false, saved.updatedAt, true, saved.version, undefined, true)
    else if (!observed && saved.state !== undefined && saved.state !== 'interrupted' && !listStillPrevious) setDismissed(sessionId, false, false, undefined, true)
    else if (!running && saved.state === 'interrupted' && dismissed && (
      // A newer interrupted updated_at re-arms, as before. A second same-boot
      // ceiling whose list read re-saves the byte-identical row — same
      // updated_at, newer observation count — re-arms too, but only while the
      // continuation's outcome is unwitnessed (restartNoticeResolvedAt unset):
      // a witnessed clean completion or an explicit dismissal settles the
      // report, so a stale refetch of the old interrupted row after a real
      // done never brings the notice back.
      (saved.updatedAt && dismissedAt && saved.updatedAt > dismissedAt) ||
      (dismissedVersion != null && resolvedAt === undefined && saved.version > dismissedVersion)
    )) setDismissed(sessionId, false)
  }, [sessionId, saved.state, saved.settled, saved.updatedAt, saved.version, running, observed, progressed, oldBoot, ended, dismissed, dismissedAt, dismissedVersion, resolvedAt])
}
