import { useState } from 'react'
import { Play, Stop } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import type { Agent, Plan } from '@/lib/api'
import { planStateColor, planStateLabel, planSecondaryChipLabel } from '@/lib/planStateColors'
import { cn } from '@/lib/utils'

interface PlanCardProps {
  plan: Plan
  agents: Agent[]
  onEdit: () => void
  onApprove: () => void
  onStop: () => void
  isApproving?: boolean
  isStopping?: boolean
}

/**
 * A single Plan summary card (ADR-049 FR-082, US-10 AS-3) — name, truncated
 * goal, `PlanState` badge (+ secondary phase/pause/failed-reason chip, R1),
 * owner-agent chip, progress bar, and bounds. Approve renders only in
 * `draft`; Stop/Clear (D8) renders while `running`/`approved` (the
 * cap-waiting state, R1 O2).
 */
export function PlanCard({ plan, agents, onEdit, onApprove, onStop, isApproving, isStopping }: PlanCardProps) {
  const [confirmStop, setConfirmStop] = useState(false)
  const owner = agents.find((a) => a.id === plan.owner_agent_id)
  const pct = Math.round((plan.progress ?? 0) * 100)
  const secondary = planSecondaryChipLabel(plan)
  const canApprove = plan.state === 'draft'
  const canStop = plan.state === 'running' || plan.state === 'approved'
  const rounds = plan.bounds?.plan_judge_max_rounds ?? 20
  const idleDays = plan.bounds?.idle_expiry_days ?? 7

  return (
    <div
      className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-3"
      data-testid={`plan-card-${plan.id}`}
    >
      <button tabIndex={0}
        type="button"
        onClick={onEdit}
        className="w-full text-left"
      >
        <div className="flex items-start justify-between gap-2">
          <p className="flex-1 text-sm font-medium text-[var(--color-secondary)] truncate">{plan.title}</p>
          <span
            className="flex-shrink-0 rounded px-1.5 py-0.5 text-[10px] font-bold"
            style={{ color: planStateColor(plan.state), backgroundColor: `${planStateColor(plan.state)}1a` }}
          >
            {planStateLabel(plan.state)}
          </span>
        </div>

        {plan.goal && (
          <p className="mt-1 text-xs text-[var(--color-muted)] line-clamp-2" title={plan.goal}>
            {plan.goal}
          </p>
        )}

        {secondary && (
          <span className="mt-1.5 inline-block rounded-full bg-[var(--color-warning)]/10 px-2 py-0.5 text-[10px] text-[color:var(--color-warning)]">
            {secondary}
          </span>
        )}

        <div className="mt-2 flex items-center gap-2">
          {owner && (
            <span className="rounded-full bg-[var(--color-surface-2)] border border-[var(--color-border)] px-2 py-0.5 text-[10px] text-[var(--color-muted)] flex-shrink-0">
              {owner.name}
            </span>
          )}
          <div className="flex-1 h-1.5 rounded-full bg-[var(--color-surface-2)] overflow-hidden min-w-[40px]">
            <div
              className={cn('h-full rounded-full transition-all', pct >= 100 ? 'bg-green-500' : 'bg-[var(--color-accent)]')}
              style={{ width: `${pct}%` }}
            />
          </div>
          <span className="w-8 text-right text-[10px] text-[var(--color-muted)] flex-shrink-0">{pct}%</span>
        </div>

        <p className="mt-1.5 text-[10px] text-[var(--color-muted)]">
          {rounds} rounds max · {idleDays}d idle-expiry
          {typeof plan.judge_rounds === 'number' && <> · round {plan.judge_rounds}</>}
        </p>
      </button>

      {(canApprove || canStop) && (
        <div className="flex gap-2 mt-2">
          {canApprove && (
            <Button
              type="button"
              size="sm"
              className="h-7 gap-1 text-xs flex-1 bg-[var(--color-accent)] text-[var(--color-primary)] hover:bg-[var(--color-accent)]/90"
              onClick={onApprove}
              disabled={isApproving}
            >
              <Play size={11} weight="fill" />
              {isApproving ? 'Approving…' : 'Approve'}
            </Button>
          )}
          {canStop && (
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-7 gap-1 text-xs flex-1 border-[var(--color-error)]/30 text-[color:var(--color-error)] hover:bg-[var(--color-error)]/10"
              onClick={() => setConfirmStop(true)}
              disabled={isStopping}
            >
              <Stop size={11} weight="fill" />
              {isStopping ? 'Stopping…' : 'Stop/Clear'}
            </Button>
          )}
        </div>
      )}

      <AlertDialog open={confirmStop} onOpenChange={setConfirmStop}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Stop this plan?</AlertDialogTitle>
            <AlertDialogDescription>
              This winds down “{plan.title}”'s active loop. In-flight work finishes gracefully; the plan will not resume automatically.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => { setConfirmStop(false); onStop() }}
              className="bg-[var(--color-error)] text-white hover:bg-[var(--color-error)]/90"
            >
              Stop/Clear
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
