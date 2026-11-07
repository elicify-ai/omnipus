// TaskRunStatusField — a read-only status badge + "Run now" action.
//
// Built for the calendar's recurring-task EDIT slide-over (CalendarEventSlideOver):
// recurring tasks are excluded from Board/List (US-3), so the calendar
// slide-over is their ONLY surface — this gives that surface a way to see a
// recurring task's current run status and manually trigger a run without
// reinventing TaskDetailPanel's "Start" mutation.
//
// Run-aware (ADR-050 RD8 / task-run-history-spec §4.3): an optional
// `occurrence` prop (folded `{ms, run}` — M7 fix) lets the calendar's
// occurrence slide-over re-point this at a SPECIFIC occurrence's run (status
// + timestamp) instead of the task's aggregate status, and thread that
// occurrence through to "Run now" (POST /tasks/{id}/runs with occurrence_ms,
// ADR-050 RD7) so it re-runs/materializes THAT occurrence instead of a
// generic fresh run. Omitted → EXACT existing behaviour (task.status-driven
// badge, runTaskNow(task.id) with no occurrence).
//
// D1 (operator decision 2026-07-20): "Run now" is NEVER offered for a FUTURE
// occurrence (`occurrence.ms > now`) — running it early would double-execute
// the naturally-scheduled fire. A future occurrence with no run shows its
// scheduled status instead, with no Run-now button at all. A past/current
// occurrence (`occurrence.ms <= now`) keeps Run-now, whether or not a run has
// resolved for it yet. `now` is an injected prop (never a bare `Date.now()`
// read inline) so this gate is deterministically testable.
//
// T10 amendment to D1 (schedules-t10-spec-r6.md, US-9 eligibility table):
// the ONE future occurrence that does offer Run now is the task's currently
// armed occurrence (`armedOccurrenceMs`, read by the caller from the
// occurrences response's `armed_occurrence_ms` — the live scheduler
// registration, never client RRULE math). Running it is row E1: the server
// consumes that occurrence, so nothing fires again at the original instant.
// Any other future instant (E3) stays hidden; unknown/absent armed state
// (not yet loaded, de-armed, no registration) keeps D1's hide. On a refusal
// the occurrences and runs queries are re-read before another action is
// rendered (stale clicks are resolved by the server, never guessed here).
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
// context. Both share `StatusBadge` for colors, `taskStatusConfig.ts`'s
// `STATUS_OPTIONS`/`statusLabel` for labels, and the same status-transition
// guard (canDropTransition) so the two surfaces never disagree about labels,
// colors, or which transitions are legal.

import { useMutation, useQueryClient } from '@tanstack/react-query'
import { runTaskNow, runNowRefusalMessage, tasksQueryKeys } from '@/lib/api'
import type { Task } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { useUiStore } from '@/store/ui'
import { canDropTransition } from '@/components/workspaces/BoardView'
import { StatusBadge } from '@/components/workspaces/StatusBadge'
import { statusLabel } from '@/components/workspaces/taskStatusConfig'
import { formatDateTime } from '@/lib/dateFormat'
import type { TaskRunOccurrenceContext } from '@/lib/taskRuns'
import { Play } from '@phosphor-icons/react'

export interface TaskRunStatusFieldProps {
  task: Task
  /**
   * The calendar occurrence this field is scoped to, folded with its
   * resolved run (M7 fix — see file header). Omit for the existing
   * task-level behaviour.
   */
  occurrence?: TaskRunOccurrenceContext
  /**
   * Injected "now" (epoch ms) for the D1 future-occurrence Run-now gate.
   * Defaults to real "now"; tests pin it for determinism.
   */
  now?: number
  /**
   * The task's currently armed occurrence instant (`armed_occurrence_ms`
   * from the occurrences response), or null/undefined when none is armed
   * or it is not known yet. Only consulted with an occurrence context.
   */
  armedOccurrenceMs?: number | null
}

export function TaskRunStatusField({ task, occurrence, now = Date.now(), armedOccurrenceMs }: TaskRunStatusFieldProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const run = occurrence?.run
  const occurrenceMs = occurrence?.ms
  const hasOccurrenceContext = occurrence !== undefined
  // D1 (operator decision 2026-07-20): a future occurrence never offers
  // Run-now, whether or not a run has already resolved for it.
  const isFutureOccurrence = hasOccurrenceContext && occurrenceMs! > now
  // T10 E1: the selected instant IS the live armed occurrence.
  const isArmedOccurrence = hasOccurrenceContext && armedOccurrenceMs != null && occurrenceMs === armedOccurrenceMs
  // A resolved run always renders its own badge. Absent a run, the
  // task-level fallback badge renders ONLY when there's no occurrence
  // context at all — an occurrence with no run yet shows a text state
  // instead (scheduled, or "not yet run"), never the unrelated task-level
  // badge (spec §4.3).
  const showBadge = !!run || !hasOccurrenceContext

  // "Run now" = POST /tasks/{id}/runs (ADR-050 RD7). With occurrenceMs set,
  // this re-runs/materializes that SPECIFIC occurrence; omitted, it re-runs
  // the task as a fresh run (existing task-level behaviour).
  const { mutate: doRunNow, isPending: isStarting } = useMutation({
    mutationFn: () => runTaskNow(task.id, occurrenceMs),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.runs(task.id) })
      queryClient.invalidateQueries({ queryKey: ['tasks', 'occurrences'] })
      queryClient.invalidateQueries({ queryKey: ['workspaces'] })
      addToast({ message: 'Task started.', variant: 'success' })
    },
    onError: (err: unknown) => {
      // T10: a refusal (e.g. the occurrence is no longer armed, or an
      // automatic delivery is already queued) means this view is stale —
      // re-read the server's occurrence and run truth before another action.
      queryClient.invalidateQueries({ queryKey: ['tasks', 'occurrences'] })
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.runs(task.id) })
      addToast({ message: runNowRefusalMessage(err, 'Failed to start task'), variant: 'error' })
    },
  })

  const badgeStatus = run ? run.status : task.status
  const lastUpdatedIso = run ? (run.ended_at ?? run.started_at) : task.updated_at

  // A repeating series (recurring) is never truly "done" — a per-run
  // terminal status just marks the last occurrence; the series re-arms and can
  // be re-run on demand. This mirrors the server's validateTransition carve-out
  // (pkg/task/store.go), which allows done→in_progress for repeating tasks so
  // "Run now" starts a fresh run. Without this, canDropTransition('done',…)
  // ("Done is final") would hide Run now on exactly the recurring tasks the
  // calendar edit slide-over exists to manage. This TASK-LEVEL gate only
  // applies to the fallback (no occurrence context) path below.
  const isRepeating = task.trigger?.type === 'recurring'
  // Already running, or a transition into in_progress is disallowed (done is
  // terminal for a one-shot task; blocked clears automatically) — otherwise
  // mirrors canDropTransition, the same guard TaskDetailPanel's Board DnD and
  // status picker use.
  const taskLevelCanRunNow =
    task.status !== 'in_progress' &&
    (canDropTransition(task.status, 'in_progress').ok || (task.status === 'done' && isRepeating))

  // Run-now gate: task-level (no occurrence context) uses the existing
  // status-transition gate, unaffected by D1. Occurrence-scoped: never for a
  // future occurrence (D1) unless it is the armed one (T10 E1); otherwise any resolved run can be re-run unless
  // it's currently in flight, and an unresolved (no-run) past/current
  // occurrence is always actionable — there's nothing to gate against yet.
  const canRunNow = hasOccurrenceContext
    ? (!isFutureOccurrence || isArmedOccurrence) && (run ? run.status !== 'in_progress' : true)
    : taskLevelCanRunNow

  return (
    <div className="space-y-[var(--space-1)]">
      <p className="text-[length:var(--type-caption-size)] font-semibold uppercase tracking-wider text-[var(--color-muted)]">Run status</p>
      {showBadge ? (
        <div className="flex items-center gap-[var(--space-2)]">
          <StatusBadge
            status={badgeStatus}
            data-testid="task-run-status-badge"
            className="h-7 text-[length:var(--type-utility-xs-size)] border-transparent rounded-md px-[var(--space-2)] inline-flex items-center"
          >
            {statusLabel(badgeStatus)}
          </StatusBadge>
          <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Last updated {formatDateTime(lastUpdatedIso)}</span>
        </div>
      ) : isFutureOccurrence ? (
        // D1: a future occurrence with no run shows its scheduled status —
        // never "Not yet run" (which implied Run-now was on offer).
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]" data-testid="occurrence-scheduled">
          Scheduled.
        </p>
      ) : (
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]" data-testid="occurrence-run-now-only">
          Not yet run.
        </p>
      )}
      {canRunNow && (
        <Button
          type="button"
          size="sm"
          className="gap-[var(--space-2)] text-[length:var(--type-utility-xs-size)] h-8"
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
