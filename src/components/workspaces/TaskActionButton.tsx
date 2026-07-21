import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Play, Stop, type Icon } from '@phosphor-icons/react'
import { ConfirmActionModal } from './ConfirmActionModal'
import { runTask, stopTask, restartTask, isApiError } from '@/lib/api'
import type { Task } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { cn } from '@/lib/utils'

export type TaskAction = 'run' | 'restart' | 'stop'

/**
 * ADR-052 §6.8 button matrix — which single action (if any) a task offers.
 *
 * Standalone task (no `plan_id`) — idle (inbox/next/failed/cancelled) → Play;
 * in_progress → Stop (chat-send toggle: the SAME slot morphs). `blocked` is
 * backend-managed (an unmet dependency) and `done` is terminal — neither
 * offers a manual action.
 *
 * In-plan task (`plan_id` set) — Stop ONLY while `in_progress`; nothing
 * otherwise (the plan drives its start and its restart — G4/FR-025).
 *
 * Within the idle bucket, WHICH tool to call depends on why it's idle: a
 * cancelled task (`failed` + `cancel_reason: stopped_by_user`) restarts via
 * `restartTask` (clears cancel_reason, resets attempt_count — FR-028); every
 * other idle state (inbox/next/a genuinely-failed standalone task) kicks off
 * fresh via `runTask` (the full attempt loop — FR-019).
 */
export function taskActionFor(task: Pick<Task, 'status' | 'plan_id' | 'cancel_reason'>): TaskAction | null {
  if (task.plan_id) {
    return task.status === 'in_progress' ? 'stop' : null
  }
  if (task.status === 'in_progress') return 'stop'
  if (task.status === 'inbox' || task.status === 'next') return 'run'
  if (task.status === 'failed') {
    return task.cancel_reason === 'stopped_by_user' ? 'restart' : 'run'
  }
  return null // blocked, done
}

/** Exhaustiveness guard for the `TaskAction` switch below — a compile error
 * here (not a silent fallthrough) is exactly what should happen if a 4th
 * `TaskAction` variant is ever added without updating the mutation dispatch. */
function assertNever(x: never): never {
  throw new Error(`Unhandled TaskAction: ${JSON.stringify(x)}`)
}

interface ActionCopy {
  icon: Icon
  label: string
  pendingLabel: string
  confirmTitle: string
  confirmDescription: (title: string) => string
  destructive: boolean
  successMessage: string
  errorFallback: string
}

const ACTION_COPY: Record<TaskAction, ActionCopy> = {
  run: {
    icon: Play,
    label: 'Run',
    pendingLabel: 'Starting…',
    confirmTitle: 'Run this task?',
    confirmDescription: (title) => `This starts "${title}" now — it runs to completion or the retry limit.`,
    destructive: false,
    successMessage: 'Task started',
    errorFallback: 'Failed to run task',
  },
  restart: {
    icon: Play,
    label: 'Play',
    pendingLabel: 'Restarting…',
    confirmTitle: 'Restart this task?',
    confirmDescription: (title) => `This re-runs "${title}" from scratch.`,
    destructive: false,
    successMessage: 'Task restarted',
    errorFallback: 'Failed to restart task',
  },
  stop: {
    icon: Stop,
    label: 'Stop',
    pendingLabel: 'Stopping…',
    confirmTitle: 'Stop this task?',
    confirmDescription: (title) => `This cancels "${title}"'s in-flight work.`,
    destructive: true,
    successMessage: 'Task stopped',
    errorFallback: 'Failed to stop task',
  },
}

interface TaskActionButtonProps {
  task: Task
  className?: string
}

/**
 * Self-contained ▶/■ task action button (ADR-052 §6.8, FR-020) — shared by
 * the Board card (TaskCard), the List row, and the Graph node so the button
 * matrix, confirm copy, and mutation wiring live in exactly one place.
 *
 * Owns its mutation directly — calls `runTask`/`restartTask`/`stopTask` and
 * invalidates the tasks (+ plans, harmless when the task has none) caches on
 * success, so any surface can render this with just a `task` prop.
 */
export function TaskActionButton({ task, className }: TaskActionButtonProps) {
  const [confirmOpen, setConfirmOpen] = useState(false)
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)

  const action = taskActionFor(task)

  const mutation = useMutation({
    mutationFn: (a: TaskAction) => {
      switch (a) {
        case 'run':
          return runTask(task.id)
        case 'restart':
          return restartTask(task.id)
        case 'stop':
          return stopTask(task.id)
        default:
          return assertNever(a)
      }
    },
    onSuccess: (_data, a) => {
      void queryClient.invalidateQueries({ queryKey: ['tasks'] })
      void queryClient.invalidateQueries({ queryKey: ['plans'] })
      addToast({ message: ACTION_COPY[a].successMessage, variant: 'success' })
    },
    onError: (err, a) => {
      const msg = isApiError(err)
        ? err.userMessage
        : err instanceof Error
          ? err.message
          : ACTION_COPY[a].errorFallback
      addToast({ message: msg, variant: 'error' })
    },
    onSettled: () => setConfirmOpen(false),
  })

  if (!action) return null

  const copy = ACTION_COPY[action]
  const Icon = copy.icon
  const pending = mutation.isPending && mutation.variables === action

  return (
    <span
      // Isolate from whatever interactive surface this is embedded in (the
      // Board card's draggable + clickable root, the List row's clickable
      // <tr>, the Graph node's clickable/keyboard-openable root) — a click,
      // drag-lift, or bubbled Enter/Space on this control must never also
      // open the task or start a card drag.
      onClick={(e) => e.stopPropagation()}
      onPointerDown={(e) => e.stopPropagation()}
      onKeyDown={(e) => e.stopPropagation()}
      className="inline-flex"
    >
      <button tabIndex={0}
        type="button"
        aria-label={`${copy.label} task ${task.title}`}
        onClick={() => setConfirmOpen(true)}
        disabled={pending}
        className={cn(
          'inline-flex items-center justify-center rounded p-1 transition-colors pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px] disabled:opacity-50',
          action === 'stop'
            ? 'text-[var(--color-muted)] hover:bg-[var(--color-error)]/10 hover:text-[color:var(--color-error)]'
            : 'text-[var(--color-muted)] hover:bg-[var(--color-accent)]/10 hover:text-[var(--color-accent)]',
          className,
        )}
      >
        <Icon size={13} weight="fill" />
      </button>

      <ConfirmActionModal
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={copy.confirmTitle}
        description={copy.confirmDescription(task.title)}
        confirmLabel={copy.label}
        pendingLabel={copy.pendingLabel}
        destructive={copy.destructive}
        isPending={pending}
        onConfirm={() => mutation.mutate(action)}
      />
    </span>
  )
}
