// TaskRunStatusField — a read-only status badge + "Run now" action.
//
// Built for the calendar's recurring-task EDIT slide-over (CalendarEventSlideOver):
// recurring tasks are excluded from Board/List (US-3), so the calendar
// slide-over is their ONLY surface — this gives that surface a way to see a
// recurring task's current run status and manually trigger a run without
// reinventing TaskDetailPanel's "Start" mutation.
//
// Run-aware (ADR-050 RD8 / task-run-history-spec §4.3): an optional `run`
// prop lets the calendar's occurrence slide-over re-point this at a SPECIFIC
// occurrence's run (status + timestamp) instead of the task's aggregate
// status. An optional `occurrenceMs` threads the clicked occurrence through
// to "Run now" so it re-runs/materializes THAT occurrence
// (POST /tasks/{id}/runs with occurrence_ms, ADR-050 RD7) instead of a
// generic fresh run. When `occurrenceMs` is set but no `run` has resolved
// yet (e.g. a future occurrence with no run, or the resolving query still in
// flight), the badge is hidden entirely and only "Run now" renders (spec
// §4.3 — "future occurrence, no run → only Run now, no status badge, no
// result"). Both props absent → EXACT existing behaviour (task.status-driven
// badge, runTaskNow(task.id) with no occurrence).
//
// "Run now" calls runTaskNow (POST /tasks/{id}/runs, ADR-050 RD7) — this
// opens/re-opens a TaskRun record. There is no dedicated /start endpoint;
// the server dispatches the agent once the run is opened (see
// pkg/gateway/rest_tasks.go, task-run-history-spec.md §3.4).
//
// This is intentionally NOT the same UI as TaskDetailPanel's editable status
// SmartSelect — TaskDetailPanel keeps its own richer editable dropdown (any
// status transition, Retry, done-terminal guard) inline; this component is
// the simpler read-only view appropriate for the calendar's recurring-task
// context. Both share the same STATUS_OPTIONS/STATUS_BADGE source of truth
// (taskStatusConfig.ts) and the same status-transition guard
// (canDropTransition) so the two surfaces never disagree about labels,
// colors, or which transitions are legal.

import { useMutation, useQueryClient } from '@tanstack/react-query'
import { runTaskNow, isApiError, tasksQueryKeys } from '@/lib/api'
import type { Task, TaskRun } from '@/lib/api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useUiStore } from '@/store/ui'
import { canDropTransition } from '@/components/workspaces/BoardView'
import { STATUS_BADGE, statusLabel } from '@/components/workspaces/taskStatusConfig'
import { Play } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

export interface TaskRunStatusFieldProps {
  task: Task
  /**
   * A specific occurrence's resolved run (ADR-050 RD8). When provided, the
   * badge reflects THIS run's status/timestamp instead of task.status —
   * used by the calendar's occurrence slide-over. Absent → falls back to
   * task.status (unchanged existing behaviour).
   */
  run?: TaskRun
  /**
   * The occurrence instant this field is scoped to, when opened from a
   * calendar occurrence chip. When set, "Run now" re-runs/materializes THAT
   * occurrence via runTaskNow(task.id, occurrenceMs) instead of a generic
   * fresh run. Omit for the existing task-level behaviour.
   */
  occurrenceMs?: number | null
}

function formatDate(iso?: string): string {
  if (!iso) return '—'
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(iso))
}

export function TaskRunStatusField({ task, run, occurrenceMs }: TaskRunStatusFieldProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const hasOccurrenceContext = occurrenceMs !== undefined && occurrenceMs !== null
  // Only-Run-now state (spec §4.3): an occurrence is selected but no run has
  // resolved for it yet (future fire, or the resolving query still in
  // flight/erred) — there is nothing occurrence-specific to badge.
  const runNowOnly = hasOccurrenceContext && !run

  // "Run now" = POST /tasks/{id}/runs (ADR-050 RD7). With occurrenceMs set,
  // this re-runs/materializes that SPECIFIC occurrence; omitted, it re-runs
  // the task as a fresh run (existing task-level behaviour) — mirrors
  // runTaskNow's own occurrenceMs-omitted-vs-set contract.
  const { mutate: doRunNow, isPending: isStarting } = useMutation({
    mutationFn: () => runTaskNow(task.id, occurrenceMs),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.runs(task.id) })
      queryClient.invalidateQueries({ queryKey: ['tasks', 'occurrences'] })
      queryClient.invalidateQueries({ queryKey: ['workspaces'] })
      addToast({ message: 'Task started.', variant: 'success' })
    },
    onError: (err: unknown) =>
      addToast({
        message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to start task',
        variant: 'error',
      }),
  })

  const badgeStatus = run ? run.status : task.status
  const badgeClass = STATUS_BADGE[badgeStatus] ?? STATUS_BADGE.inbox
  const lastUpdatedIso = run ? (run.ended_at ?? run.started_at) : task.updated_at

  // A repeating series (every/recurring) is never truly "done" — a per-run
  // terminal status just marks the last occurrence; the series re-arms and can
  // be re-run on demand. This mirrors the server's validateTransition carve-out
  // (pkg/task/store.go), which allows done→in_progress for repeating tasks so
  // "Run now" starts a fresh run. Without this, canDropTransition('done',…)
  // ("Done is final") would hide Run now on exactly the recurring tasks the
  // calendar edit slide-over exists to manage. This TASK-LEVEL gate only
  // applies to the fallback (no run resolved) path below.
  const isRepeating = task.trigger?.type === 'every' || task.trigger?.type === 'recurring'
  // Already running, or a transition into in_progress is disallowed (done is
  // terminal for a one-shot task; blocked clears automatically) — otherwise
  // mirrors canDropTransition, the same guard TaskDetailPanel's Board DnD and
  // status picker use.
  const taskLevelCanRunNow =
    task.status !== 'in_progress' &&
    (canDropTransition(task.status, 'in_progress').ok || (task.status === 'done' && isRepeating))

  // Run-level gate: any resolved run can be re-run unless it's currently in
  // flight. Occurrence-selected-but-unresolved (runNowOnly): always
  // actionable — there is no run yet to gate against (spec §4.3).
  const canRunNow = run ? run.status !== 'in_progress' : runNowOnly ? true : taskLevelCanRunNow

  return (
    <div className="space-y-1.5">
      <p className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">Run status</p>
      {runNowOnly ? (
        <p className="text-xs text-[var(--color-muted)]" data-testid="occurrence-run-now-only">
          Not yet run.
        </p>
      ) : (
        <div className="flex items-center gap-2">
          <Badge
            data-testid="task-run-status-badge"
            className={cn('h-7 text-xs border-transparent rounded-md px-2 inline-flex items-center', badgeClass)}
          >
            {statusLabel(badgeStatus)}
          </Badge>
          <span className="text-xs text-[var(--color-muted)]">Last updated {formatDate(lastUpdatedIso)}</span>
        </div>
      )}
      {canRunNow && (
        <Button
          type="button"
          size="sm"
          className="gap-2 text-xs h-8"
          onClick={() => doRunNow()}
          disabled={isStarting}
        >
          <Play size={13} weight="fill" />
          {isStarting ? 'Starting…' : 'Run now'}
        </Button>
      )}
    </div>
  )
}
