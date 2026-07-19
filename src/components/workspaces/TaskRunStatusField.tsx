// TaskRunStatusField — a read-only status badge + "Run now" action.
//
// Built for the calendar's recurring-task EDIT slide-over (CalendarEventSlideOver):
// recurring tasks are excluded from Board/List (US-3), so the calendar
// slide-over is their ONLY surface — this gives that surface a way to see a
// recurring task's current run status and manually trigger a run without
// reinventing TaskDetailPanel's "Start" mutation.
//
// "Run now" reuses the exact same mutation TaskDetailPanel's "Start Task"
// button performs — PATCH status → in_progress. There is no dedicated
// /start endpoint; the server auto-dispatches the agent once status flips to
// in_progress (see pkg/gateway/rest_tasks.go's StartTaskNow, idempotent on
// session_id).
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
import { updateTask, isApiError, tasksQueryKeys } from '@/lib/api'
import type { Task } from '@/lib/api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useUiStore } from '@/store/ui'
import { canDropTransition } from '@/components/workspaces/BoardView'
import { STATUS_BADGE, statusLabel } from '@/components/workspaces/taskStatusConfig'
import { Play } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

export interface TaskRunStatusFieldProps {
  task: Task
}

function formatDate(iso?: string): string {
  if (!iso) return '—'
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(iso))
}

export function TaskRunStatusField({ task }: TaskRunStatusFieldProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  // "Run now" = PATCH status to in_progress (no /start endpoint in the
  // unified model) — identical mutation to TaskDetailPanel's "Start Task".
  const { mutate: doRunNow, isPending: isStarting } = useMutation({
    mutationFn: () => updateTask(task.id, { status: 'in_progress' }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
      queryClient.invalidateQueries({ queryKey: ['workspaces'] })
      addToast({ message: 'Task started.', variant: 'success' })
    },
    onError: (err: unknown) =>
      addToast({
        message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to start task',
        variant: 'error',
      }),
  })

  const badgeClass = STATUS_BADGE[task.status] ?? STATUS_BADGE.inbox
  // Already running, or a transition into in_progress is disallowed (done is
  // terminal; blocked clears automatically) — mirrors canDropTransition, the
  // same guard TaskDetailPanel's Board DnD and status picker use.
  const canRunNow = task.status !== 'in_progress' && canDropTransition(task.status, 'in_progress').ok

  return (
    <div className="space-y-1.5">
      <p className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">Run status</p>
      <div className="flex items-center gap-2">
        <Badge
          data-testid="task-run-status-badge"
          className={cn('h-7 text-xs border-transparent rounded-md px-2 inline-flex items-center', badgeClass)}
        >
          {statusLabel(task.status)}
        </Badge>
        <span className="text-xs text-[var(--color-muted)]">Last updated {formatDate(task.updated_at)}</span>
      </div>
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
