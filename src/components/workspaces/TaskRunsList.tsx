// TaskRunsList — per-task execution-record history (ADR-050 / spec §4.3/4.4).
//
// TaskRun is a purely additive record layer alongside Task.status/result/
// session_id (which keep their exact existing behaviour) — see
// docs/internal/specs/task-run-history-spec.md §2.1. This is the SINGLE
// run-history list used by BOTH the calendar occurrence slide-over (§4.3 —
// "Always show the run-history list … so history survives a schedule edit"
// for the individual-occurrence/series-edit case, and the bucket-drill-in
// day-scoped list for the aggregated-bucket case — H2 fix, see `dayRange`
// below) and TaskDetailPanel's Runs section (§4.4, Board/List normal-task
// re-run flow). Do not fork/duplicate this component — extend it in place.
//
// Each run renders: a status badge sharing taskStatusConfig's STATUS_BADGE/
// statusLabel vocabulary (TaskRun.status is a strict subset of Task['status'] —
// in_progress | done | failed — so the two surfaces never disagree on colors/
// labels), how it started (scheduled fire vs manual Run-now), when it ended,
// its terminal result (mirrors TaskResultField's compact display idiom via
// the shared `hasVisibleResult` gate — Q3 dedup), and an Open-in-Chat action
// when it produced a session (mirrors OpenInChatButton's navigation — a
// TaskRun always carries session_id from open time onward, unlike
// Task.session_id, but the render is still defensively gated on a truthy
// value).

import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { ChatCircle, ClockCounterClockwise, Warning } from '@phosphor-icons/react'
import { fetchTaskRuns, tasksQueryKeys, isApiError } from '@/lib/api'
import type { TaskRun } from '@/lib/api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { STATUS_BADGE, statusLabel } from '@/components/workspaces/taskStatusConfig'
import { formatDateTime } from '@/lib/dateFormat'
import { hasVisibleResult } from '@/lib/taskRuns'
import { cn } from '@/lib/utils'

export interface TaskRunsListProps {
  taskId: string
  /** Called after an Open-in-Chat navigation fires — e.g. close the host slide-over/panel. */
  onNavigate?: () => void
  className?: string
  /**
   * Client-side filter to a single calendar day's runs (H2 fix, ADR-050 /
   * task-run-history-spec §4.3 "a bucket click opens a mini-list of that
   * day's runs") — when set, only runs whose `started_at` falls within
   * `[startMs, endMs)` are rendered. The underlying `GET /tasks/{id}/runs`
   * fetch is unchanged (task-scoped, retention-bounded); this filters the
   * already-fetched list, so it composes with the loading/error/empty states
   * below without a second network round-trip. Omitted → unfiltered
   * (existing behaviour, both the always-embedded history list and
   * TaskDetailPanel's Runs section).
   */
  dayRange?: { startMs: number; endMs: number }
}

export function TaskRunsList({ taskId, onNavigate, className, dayRange }: TaskRunsListProps) {
  const navigate = useNavigate()

  const {
    data: runs = [],
    isLoading,
    isError,
    error,
    refetch,
    isFetching,
  } = useQuery({
    queryKey: tasksQueryKeys.runs(taskId),
    queryFn: () => fetchTaskRuns(taskId),
    enabled: Boolean(taskId),
  })

  const visibleRuns = useMemo(() => {
    if (!dayRange) return runs
    return runs.filter((r) => {
      const startedMs = new Date(r.started_at).getTime()
      return !isNaN(startedMs) && startedMs >= dayRange.startMs && startedMs < dayRange.endMs
    })
  }, [runs, dayRange])

  function openRunInChat(run: TaskRun) {
    void navigate({ to: '/sessions/$sessionId', params: { sessionId: run.session_id } })
    onNavigate?.()
  }

  return (
    <div className={cn('space-y-1.5', className)}>
      <p className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">
        Run history
      </p>

      {isLoading ? (
        <TaskRunsSkeleton />
      ) : isError ? (
        <div
          data-testid="task-runs-error"
          className="flex items-center justify-between gap-2 rounded-md border border-[color:var(--color-error)]/30 bg-[color:var(--color-error)]/10 px-3 py-2 text-xs text-[color:var(--color-error)]"
        >
          <span className="flex items-center gap-1.5">
            <Warning size={13} weight="fill" />
            {isApiError(error) ? error.userMessage : 'Failed to load run history.'}
          </span>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-6 shrink-0 px-2 text-[11px]"
            onClick={() => void refetch()}
            disabled={isFetching}
          >
            {isFetching ? 'Retrying…' : 'Retry'}
          </Button>
        </div>
      ) : visibleRuns.length === 0 ? (
        <div
          data-testid="task-runs-empty"
          className="flex items-center gap-1.5 rounded-md border border-dashed border-[var(--color-border)] px-3 py-3 text-xs text-[var(--color-muted)]"
        >
          <ClockCounterClockwise size={14} />
          No runs yet.
        </div>
      ) : (
        <ul className="space-y-2">
          {visibleRuns.map((run) => (
            <TaskRunRow key={run.run_id} run={run} onOpenInChat={() => openRunInChat(run)} />
          ))}
        </ul>
      )}
    </div>
  )
}

function TaskRunRow({ run, onOpenInChat }: { run: TaskRun; onOpenInChat: () => void }) {
  const badgeClass = STATUS_BADGE[run.status] ?? STATUS_BADGE.inbox
  const isFailed = run.status === 'failed'
  const showResult = hasVisibleResult(run.status, run.result)

  return (
    <li
      data-testid="task-run-row"
      className={cn(
        'space-y-1.5 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] p-2.5',
        isFailed && 'border-[color:var(--color-error)]/30',
      )}
    >
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Badge className={cn('h-6 rounded-md border-transparent px-2 text-[11px]', badgeClass)}>
            {statusLabel(run.status)}
          </Badge>
          <span className="text-[11px] text-[var(--color-muted)]">
            {run.kind === 'manual' ? 'Run now' : 'Scheduled'}
          </span>
        </div>
        <span className="text-[11px] text-[var(--color-muted)]">{formatDateTime(run.ended_at)}</span>
      </div>

      {showResult ? (
        <pre
          data-testid="task-run-result"
          className="max-h-[120px] overflow-y-auto whitespace-pre-wrap break-words rounded-md bg-[var(--color-surface-2)] p-2 font-mono text-xs leading-relaxed text-[var(--color-secondary)]"
        >
          {run.result}
        </pre>
      ) : null}

      {run.session_id ? (
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-7 w-full gap-2 text-xs"
          onClick={onOpenInChat}
        >
          <ChatCircle size={12} />
          Open in Chat
        </Button>
      ) : null}
    </li>
  )
}

function TaskRunsSkeleton() {
  return (
    <div className="animate-pulse space-y-2" data-testid="task-runs-skeleton">
      {[1, 2, 3].map((i) => (
        <div key={i} className="h-16 rounded-md bg-[var(--color-surface-2)]" />
      ))}
    </div>
  )
}
