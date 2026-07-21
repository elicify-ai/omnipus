import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Play, Stop, type Icon } from '@phosphor-icons/react'
import { ConfirmActionModal } from './ConfirmActionModal'
import { executePlan, stopPlan, restartPlan, isApiError, parsePlanApproveTaskErrors } from '@/lib/api'
import type { Plan } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { cn } from '@/lib/utils'

export type PlanAction = 'execute' | 'stop' | 'play'

/**
 * ADR-052 §6.8 button matrix — which single action (if any) a plan offers.
 * `approved` folds into `stop` alongside `running`: a plan can legitimately
 * sit `approved` while cap-queued (US-3 Acceptance 4 — "queued behind cap"),
 * and that queued plan must stay Stoppable, not just a dispatching one
 * (Edge Cases — "Cap frees while a plan is approved-queued AND the operator
 * Stops it → Stop wins").
 */
export function planActionFor(plan: Pick<Plan, 'state' | 'failed_reason'>): PlanAction | null {
  if (plan.state === 'draft') return 'execute'
  if (plan.state === 'running' || plan.state === 'approved') return 'stop'
  if (plan.state === 'failed' && plan.failed_reason === 'stopped_by_user') return 'play'
  return null // done, or a genuinely-failed plan — no restart offered (US-9 Acceptance 2)
}

/** Exhaustiveness guard for the `PlanAction` switch below — a compile error
 * here (not a silent fallthrough) is exactly what should happen if a 4th
 * `PlanAction` variant is ever added without updating the mutation dispatch. */
function assertNever(x: never): never {
  throw new Error(`Unhandled PlanAction: ${JSON.stringify(x)}`)
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

const ACTION_COPY: Record<PlanAction, ActionCopy> = {
  execute: {
    icon: Play,
    label: 'Execute',
    pendingLabel: 'Executing…',
    confirmTitle: 'Execute this plan?',
    confirmDescription: (title) =>
      `This starts autonomous execution of "${title}". Its member tasks will run — and be judged and retried — without further approval.`,
    destructive: false,
    successMessage: 'Plan execution started',
    errorFallback: 'Failed to execute plan',
  },
  stop: {
    icon: Stop,
    label: 'Stop',
    pendingLabel: 'Stopping…',
    confirmTitle: 'Stop this plan?',
    confirmDescription: (title) =>
      `This cancels "${title}"'s in-flight member tasks and verification, then marks it Cancelled. It will not resume automatically.`,
    destructive: true,
    successMessage: 'Plan stopped',
    errorFallback: 'Failed to stop plan',
  },
  play: {
    icon: Play,
    label: 'Play',
    pendingLabel: 'Restarting…',
    confirmTitle: 'Restart this plan?',
    confirmDescription: (title) =>
      `This re-runs "${title}"'s incomplete tasks from scratch. Completed work is preserved.`,
    destructive: false,
    successMessage: 'Plan restarted',
    errorFallback: 'Failed to restart plan',
  },
}

interface PlanActionButtonProps {
  plan: Plan
  className?: string
}

/**
 * Self-contained ▶/■ plan action button (ADR-052 §6.8, FR-020): draft →
 * Execute; running/cap-queued-approved → Stop; cancelled (`failed` +
 * `stopped_by_user`) → Play (restart); `done` and a genuinely-failed plan
 * (`judge_rounds_exhausted`/`idle_expired`) render nothing (US-9 Acceptance 2
 * — no restart for a real failure).
 *
 * Owns its mutation directly — calls `executePlan`/`stopPlan`/`restartPlan`
 * (the gated POST /approve, POST /stop, POST /restart routes; NEVER the old
 * PUT-based approve, which ADR-052 US-5 forbids) and invalidates the plans +
 * tasks caches on success, so any surface can render this button with just a
 * `plan` prop — no callback wiring, no parent-owned mutation required.
 */
export function PlanActionButton({ plan, className }: PlanActionButtonProps) {
  const [confirmOpen, setConfirmOpen] = useState(false)
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)

  const action = planActionFor(plan)

  const mutation = useMutation({
    mutationFn: (a: PlanAction) => {
      switch (a) {
        case 'execute':
          return executePlan(plan.id)
        case 'stop':
          return stopPlan(plan.id)
        case 'play':
          return restartPlan(plan.id)
        default:
          return assertNever(a)
      }
    },
    onSuccess: (_data, a) => {
      // Broad prefix invalidation (no workspaceId needed) — matches the
      // existing WorkspaceTasksTab.tsx convention of calling
      // `tasksQueryKeys.list()` with no params for the same reason.
      void queryClient.invalidateQueries({ queryKey: ['plans'] })
      void queryClient.invalidateQueries({ queryKey: ['tasks'] })
      addToast({ message: ACTION_COPY[a].successMessage, variant: 'success' })
    },
    onError: (err, a) => {
      // Execute reuses the same 400 task_errors shape Approve's gated
      // criteria check produced (FR-005/FR-084) — surface those reasons
      // instead of the generic fallback.
      if (a === 'execute' && isApiError(err) && err.status === 400) {
        const taskErrors = parsePlanApproveTaskErrors(err.body)
        if (taskErrors) {
          const lines = taskErrors.map((e) => `${e.title ?? e.task_id}: ${e.reason}`)
          addToast({
            message: `Cannot execute — the following tasks are missing criteria:\n${lines.join('\n')}`,
            variant: 'error',
          })
          return
        }
      }
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
      // Self-contained click isolation (parity with TaskActionButton's own
      // wrapper) — defense-in-depth. PlansFilterBand already wraps this
      // control's slot in an `onClick={(e) => e.stopPropagation()}` div
      // alongside the pencil/⋯ menu, so this is a second, redundant layer:
      // it keeps the button safe from bubbling into an ancestor's onSelect
      // even if a future caller renders it outside that wrapper.
      onClick={(e) => e.stopPropagation()}
      onPointerDown={(e) => e.stopPropagation()}
      onKeyDown={(e) => e.stopPropagation()}
      className="inline-flex"
    >
      <button tabIndex={0}
        type="button"
        aria-label={`${copy.label} plan ${plan.title}`}
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
        description={copy.confirmDescription(plan.title)}
        confirmLabel={copy.label}
        pendingLabel={copy.pendingLabel}
        destructive={copy.destructive}
        isPending={pending}
        onConfirm={() => mutation.mutate(action)}
      />
    </span>
  )
}
