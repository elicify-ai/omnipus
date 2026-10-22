import { useQuery } from '@tanstack/react-query'
import { fetchSessions, type Session } from '@/lib/api'
import { interruptionReasonText } from '@/lib/subagentStatus'
import { queryClient } from '@/lib/queryClient'

export interface TaskSessionInterruption {
  reason?: Session['interruption_reason']
}

/**
 * The task's linked session, when that session's lifecycle is interrupted.
 * Null while unknown or when the session is any other state — the task's own
 * status stays In Progress, Done, and so on. Shares the sidebar sessions query.
 */
export function useTaskSessionInterruption(sessionId: string | undefined): TaskSessionInterruption | null {
  const { data } = useQuery({
    queryKey: ['sessions'],
    queryFn: () => fetchSessions(),
    staleTime: 15_000,
    refetchInterval: sessionId ? 15_000 : false,
    enabled: !!sessionId,
    select: (list: Session[]) => list.find((session) => session.id === sessionId),
  }, queryClient)
  if (!sessionId || data?.lifecycle_state !== 'interrupted') return null
  return { reason: data.interruption_reason }
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
