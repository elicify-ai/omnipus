import { useState } from 'react'
import {
  ListChecks,
  Plus,
  PencilSimple,
  DotsThreeVertical,
  Stop,
  Broom,
  CheckCircle,
  XCircle,
  CircleNotch,
} from '@phosphor-icons/react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
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
import { Progress } from '@/components/ui/progress'
import { PlanActionButton } from './PlanActionButton'
import type { Agent, Plan, Task } from '@/lib/api'
import { isPlanCancelled, planDisplayColor, planDisplayLabel } from '@/lib/planStateColors'
import { cn } from '@/lib/utils'

interface PlansFilterBandProps {
  plans: Plan[]
  tasks: Task[]
  agents: Agent[]
  selectedPlanId: string | null
  onSelectPlan: (planId: string | null) => void
  onNewPlan: () => void
  onEditPlan: (plan: Plan) => void
  /**
   * @deprecated ADR-052 replaces the old PUT-based Approve affordance with a
   * self-contained ▶ Execute button (`PlanActionButton`, calling the gated
   * `executePlan`/POST-approve flow directly) — this callback is no longer
   * invoked. Kept optional, unused, for compatibility with existing callers
   * during the ADR-052 rollout.
   */
  onApprovePlan?: (plan: Plan) => void
  /**
   * @deprecated Stop is now handled by the self-contained `PlanActionButton`
   * (calling `stopPlan` directly). Kept optional, unused, for compatibility.
   */
  onStopPlan?: (plan: Plan) => void
  onClearPlan: (plan: Plan) => void
  /**
   * The plan (if any) with a Clear mutation currently in flight. (Execute/
   * Stop/Play pending state is now owned internally by `PlanActionButton` —
   * this prop only ever needs to carry `action: 'clear'`; the wider union is
   * kept for compatibility with existing callers during the ADR-052 rollout.)
   */
  pendingAction?: { planId: string; action: 'approve' | 'stop' | 'clear' } | null
  /** Render the trailing dashed "New plan" tile. Default true (standalone use);
   * the Tasks screen sets false because its "Plans" section header owns the
   * minimalist "+ New Plan" link, so the band isn't duplicated. */
  showNewPlanTile?: boolean
}

/** Shared footprint for the "All tasks" tile and every plan tile — Requirement: "Keep tiles the SAME size as each other." */
const TILE_SIZE = 'w-56 flex-shrink-0'

/**
 * Per-state glyph — always paired with `planDisplayLabel`'s visible text,
 * never color-alone (a11y). `cancelled` (ADR-052 FR-015/US-8) overrides the
 * `failed`-state glyph with a distinct shape (Stop, not XCircle) so the
 * orange "Cancelled" marker doesn't just repaint the red "Failed" icon.
 */
function PlanStateGlyph({ state, cancelled = false, size = 9 }: { state: Plan['state']; cancelled?: boolean; size?: number }) {
  if (cancelled) return <Stop size={size} weight="fill" />
  switch (state) {
    case 'draft':
      return <PencilSimple size={size} />
    case 'approved':
      return <CheckCircle size={size} />
    case 'running':
      return <CircleNotch size={size} className="animate-spin" />
    case 'done':
      return <CheckCircle size={size} weight="fill" />
    case 'failed':
      return <XCircle size={size} weight="fill" />
    default:
      return <CheckCircle size={size} />
  }
}

/**
 * ADR-051 D2/D3 — Plans-as-Filter band. A horizontal row of plan tiles above
 * the (combined, workspace-wide) task board. Clicking a tile's body FILTERS
 * the board to that plan — it does NOT navigate/drill (that's the
 * superseded Hierarchical Drill-Down board model). A leading "All tasks"
 * tile is the default selected state and clears the filter; re-clicking the
 * already-active plan tile also clears it (toggle-off).
 *
 * Because the tile body is a filter control (not an edit affordance), each
 * plan tile exposes an explicit pencil (edit) button, the ADR-052 §6.8
 * ▶/■ action button (Execute/Stop/Play — `PlanActionButton`), and a ⋯ menu
 * (Clear) as SIBLINGS of the select button — never nested inside it — so a
 * click on any of them can never bubble into the tile's onSelectPlan. The
 * tile itself is `role="group"`, not a `role="button"` wrapping other
 * buttons, so this isolation holds structurally rather than by convention.
 * The ⋯ menu content, `PlanActionButton`'s own confirm modal, and the ⋯
 * menu's Clear AlertDialog all render through a Radix portal (outside the
 * tile's DOM subtree), so their clicks structurally cannot reach the tile's
 * select button either.
 */
export function PlansFilterBand({
  plans,
  tasks,
  agents,
  selectedPlanId,
  onSelectPlan,
  onNewPlan,
  onEditPlan,
  onClearPlan,
  pendingAction = null,
  showNewPlanTile = true,
}: PlansFilterBandProps) {
  return (
    <div
      role="group"
      aria-label="Plans filter"
      className="flex items-stretch gap-3 overflow-x-auto px-6 py-4 bg-[var(--color-surface-0)] flex-shrink-0"
    >
      <AllTasksTile
        selected={selectedPlanId === null}
        onSelect={() => onSelectPlan(null)}
        totalTasks={tasks.length}
      />

      {plans.map((plan) => {
        // "member tasks" = this plan's own top-level tasks — subtasks nest
        // under their parent and would otherwise double-count toward the
        // tile's done/total progress.
        const memberTasks = tasks.filter((t) => t.plan_id === plan.id && !t.parent_task_id)
        const memberTotal = memberTasks.length
        const memberDone = memberTasks.filter((t) => t.status === 'done').length
        const isSelected = selectedPlanId === plan.id

        return (
          <PlanFilterTile
            key={plan.id}
            plan={plan}
            agents={agents}
            memberTotal={memberTotal}
            memberDone={memberDone}
            selected={isSelected}
            onSelect={() => onSelectPlan(isSelected ? null : plan.id)}
            onEdit={() => onEditPlan(plan)}
            onClear={() => onClearPlan(plan)}
            isClearing={pendingAction?.planId === plan.id && pendingAction.action === 'clear'}
          />
        )
      })}

      {showNewPlanTile && (
        <button tabIndex={0}
          type="button"
          onClick={onNewPlan}
          aria-label="New plan"
          className={cn(
            TILE_SIZE,
            'flex flex-col items-center justify-center gap-1 rounded-lg border border-dashed border-[var(--color-border)] p-3 text-[var(--color-muted)] transition-colors hover:border-[var(--color-accent)] hover:text-[var(--color-accent)] pointer-coarse:min-h-[44px]',
          )}
        >
          <Plus size={16} />
          <span className="text-xs font-medium">New plan</span>
        </button>
      )}
    </div>
  )
}

function AllTasksTile({
  selected,
  onSelect,
  totalTasks,
}: {
  selected: boolean
  onSelect: () => void
  totalTasks: number
}) {
  return (
    <div
      role="group"
      aria-label="All tasks"
      data-testid="all-tasks-tile"
      className={cn(
        TILE_SIZE,
        'rounded-lg border bg-[var(--color-surface-1)] p-3 transition-colors',
        selected
          ? 'border-[var(--color-accent)] ring-1 ring-[var(--color-accent)]'
          : 'border-[var(--color-border)] hover:border-[var(--color-border)]/60 hover:bg-[var(--color-surface-2)]/40',
      )}
    >
      <button tabIndex={0}
        type="button"
        aria-pressed={selected}
        aria-label="All tasks"
        onClick={onSelect}
        className="flex h-full w-full flex-col items-start gap-2 text-left"
      >
        <span
          className={cn(
            'inline-flex items-center gap-1.5 text-sm font-medium',
            selected ? 'text-[var(--color-accent)]' : 'text-[var(--color-secondary)]',
          )}
        >
          <ListChecks size={14} weight={selected ? 'fill' : 'regular'} />
          All tasks
        </span>
        <span className="text-[10px] text-[var(--color-muted)]">
          {totalTasks} task{totalTasks === 1 ? '' : 's'}
        </span>
      </button>
    </div>
  )
}

interface PlanFilterTileProps {
  plan: Plan
  agents: Agent[]
  memberTotal: number
  memberDone: number
  selected: boolean
  onSelect: () => void
  onEdit: () => void
  onClear: () => void
  isClearing: boolean
}

function PlanFilterTile({
  plan,
  agents,
  memberTotal,
  memberDone,
  selected,
  onSelect,
  onEdit,
  onClear,
  isClearing,
}: PlanFilterTileProps) {
  const [confirmClear, setConfirmClear] = useState(false)

  const owner = agents.find((a) => a.id === plan.owner_agent_id)
  const pct = memberTotal > 0 ? Math.round((memberDone / memberTotal) * 100) : 0
  const cancelled = isPlanCancelled(plan)
  const displayColor = planDisplayColor(plan)
  const displayLabelText = planDisplayLabel(plan)

  return (
    <div
      role="group"
      aria-label={plan.title}
      data-testid={`plan-filter-tile-${plan.id}`}
      title={plan.title}
      className={cn(
        TILE_SIZE,
        'group relative rounded-lg border border-l-2 border-l-[var(--color-accent)]/40 bg-[var(--color-surface-1)] p-3 transition-colors',
        selected
          ? 'border-[var(--color-accent)] ring-1 ring-[var(--color-accent)]'
          : 'border-[var(--color-border)] hover:border-[var(--color-border)]/60 hover:bg-[var(--color-surface-2)]/40',
      )}
    >
      {/* Edit + ▶/■/Play + ⋯ actions — SIBLINGS of the select button below,
          never nested inside it, so they can never trigger onSelect. Hover-
          revealed on pointer-fine devices, always visible on touch. */}
      <div
        className="absolute right-1.5 top-1.5 z-10 flex items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100"
        onClick={(e) => e.stopPropagation()}
      >
        <button tabIndex={0}
          type="button"
          aria-label={`Edit plan ${plan.title}`}
          onClick={onEdit}
          className="inline-flex items-center justify-center rounded p-1 text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
        >
          <PencilSimple size={13} />
        </button>

        {/* ADR-052 §6.8 button matrix — draft → Execute, running/cap-queued
            approved → Stop, cancelled → Play. Renders nothing for done/a
            genuinely-failed plan. Self-contained: owns its own confirm modal
            + mutation (executePlan/stopPlan/restartPlan). */}
        <PlanActionButton plan={plan} />

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button tabIndex={0}
              type="button"
              aria-label={`Plan actions for ${plan.title}`}
              className="inline-flex items-center justify-center rounded p-1 text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
            >
              <DotsThreeVertical size={14} weight="bold" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-44">
            <DropdownMenuItem
              onClick={() => setConfirmClear(true)}
              disabled={isClearing || plan.state === 'running'}
              className={cn('flex items-center gap-2', 'text-[color:var(--color-error)]')}
            >
              <Broom size={13} />
              {isClearing ? 'Clearing…' : 'Clear'}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      {/* Select control — the tile's TITLE/body is the filter toggle (Von
          Restorff: `aria-pressed` + gold ring communicate the active tile). */}
      <button tabIndex={0}
        type="button"
        aria-pressed={selected}
        aria-label={plan.title}
        onClick={onSelect}
        className="flex h-full w-full flex-col items-start gap-2 pr-10 text-left"
      >
        <span
          className="inline-flex flex-shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-bold leading-tight"
          style={{ color: displayColor, backgroundColor: `${displayColor}1a` }}
        >
          <PlanStateGlyph state={plan.state} cancelled={cancelled} />
          {displayLabelText}
        </span>

        <span className="line-clamp-2 text-sm font-medium leading-snug text-[var(--color-secondary)]">
          {plan.title}
        </span>

        <span className="flex flex-wrap items-center gap-1.5">
          <span
            className="inline-flex items-center gap-1 rounded-full border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-0.5 text-[10px] text-[var(--color-muted)]"
            role="img"
            aria-label={`Progress: ${memberDone} of ${memberTotal} tasks done`}
          >
            <Progress value={pct} className="h-1 w-8" />
            <span className="tabular-nums">
              {memberDone}/{memberTotal}
            </span>
          </span>
          {owner && (
            <span
              title={owner.name}
              className="max-w-[100px] truncate rounded-full border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-0.5 text-[10px] text-[var(--color-muted)]"
            >
              {owner.name.split('—')[0].trim()}
            </span>
          )}
        </span>
      </button>

      <AlertDialog open={confirmClear} onOpenChange={setConfirmClear}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Clear this plan?</AlertDialogTitle>
            <AlertDialogDescription>
              This permanently deletes “{plan.title}”. Member tasks are not deleted, but lose their plan grouping. This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => { setConfirmClear(false); onClear() }}
              className="bg-[var(--color-error)] text-white hover:bg-[var(--color-error)]/90"
            >
              Clear
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
