import { useQuery } from '@tanstack/react-query'
import { fetchSessionDetail, fetchSessions, type Session } from '@/lib/api'
import { interruptionReasonText } from '@/lib/subagentStatus'
import { queryClient } from '@/lib/queryClient'

export interface TaskSessionInterruption {
  reason?: Session['interruption_reason']
}

/**
 * The task's linked session, when that session's lifecycle is interrupted.
 * Null while unknown or when the session is any other state — the task's own
 * status stays In Progress, Done, and so on.
 *
 * CR I2 (DESIGN 1): the roots-only sidebar query cannot see a task worker's
 * child session, so a roots miss resolves through the child's own detail
 * endpoint. The detail result is cached under the task-child key — never
 * written into the shared ['sessions'] roots cache — so the sidebar stays
 * roots-faithful while Board/List/Graph still find the exact child.
 */
export function useTaskSessionInterruption(sessionId: string | undefined): TaskSessionInterruption | null {
  const roots = useQuery({
    queryKey: ['sessions'],
    queryFn: () => fetchSessions(),
    staleTime: 15_000,
    refetchInterval: sessionId ? 15_000 : false,
    enabled: !!sessionId,
    select: (list: Session[]) => list.find((session) => session.id === sessionId),
  }, queryClient)
  const detail = useQuery({
    queryKey: ['task-session-interruption', sessionId],
    queryFn: () => fetchSessionDetail(sessionId as string),
    staleTime: 15_000,
    refetchInterval: sessionId ? 15_000 : false,
    enabled: !!sessionId && !roots.data,
  }, queryClient)
  const resolved = roots.data ?? detail.data?.session
  if (!sessionId || resolved?.lifecycle_state !== 'interrupted') return null
  return { reason: resolved.interruption_reason }
}

/** Static Interrupted label. The reason uses the same title tooltip as a helper row. */
export function TaskSessionInterrupted({ reason }: { reason?: Session['interruption_reason'] }) {
  const detail = interruptionReasonText(reason)
  return (
    <span
      data-testid="task-session-interrupted"
      title={detail ? `Interrupted: ${detail}` : 'Interrupted'}
      className="shrink-0 text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
    >
      Interrupted
    </span>
  )
}
