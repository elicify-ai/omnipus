import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import {
  ArrowsOut,
  TreeStructure,
  DotsThreeVertical,
  PencilSimple,
  Play,
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
  DropdownMenuSeparator,
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
import type { Agent, Plan } from '@/lib/api'
import { planStateColor, planStateLabel } from '@/lib/planStateColors'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { cn } from '@/lib/utils'

/**
 * Per-state glyph — ALWAYS paired with `planStateLabel`'s text (never
 * colour-alone, a11y).
 */
function PlanStateGlyph({ state, size = 9 }: { state: Plan['state']; size?: number }) {
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

interface PlanCardProps {
  plan: Plan
  /** Owning workspace — needed to build the Graph tab's route params. */
  workspaceId: string
  agents: Agent[]
  /** Total member tasks (ALL of this plan's top-level tasks, independent of any tag filter). */
  memberTotal: number
  /** Member tasks currently `done`. */
  memberDone: number
  /** Open the plan's edit slide-over (from the ⋯ menu's Edit item). */
  onEdit: () => void
  onApprove: () => void
  onStop: () => void
  onClear: () => void
  isApproving?: boolean
  isStopping?: boolean
  isClearing?: boolean
}

/**
 * Top-level Board card for a Plan (Hierarchical Drill-Down board). Deliberately
 * shares the TASK CARD's shell — same size, radius, padding, and `chip + title
 * + meta` layout — so plans and tasks read as peers of one system (Law of
 * Similarity). A plan is marked only by a subtle gold left accent + a STATE
 * chip in the task card's priority slot (Von Restorff — one differentiator, not
 * a different card). Its actions collapse into a single ⋯ menu revealed on
 * hover (progressive disclosure), so the resting card is visually identical to
 * a task card. The plan TITLE is the drill-in control (opens this plan's own
 * task board via the shared `activePlanId` scope).
 */
export function PlanCard({
  plan,
  workspaceId,
  agents,
  memberTotal,
  memberDone,
  onEdit,
  onApprove,
  onStop,
  onClear,
  isApproving = false,
  isStopping = false,
  isClearing = false,
}: PlanCardProps) {
  const navigate = useNavigate()
  const setActivePlanId = useWorkspacesStore((s) => s.setActivePlanId)
  const [confirmStop, setConfirmStop] = useState(false)
  const [confirmClear, setConfirmClear] = useState(false)

  const owner = agents.find((a) => a.id === plan.owner_agent_id)
  const pct = memberTotal > 0 ? Math.round((memberDone / memberTotal) * 100) : 0
  const canApprove = plan.state === 'draft'
  const canStop = plan.state === 'running' || plan.state === 'approved'
  // A DAG needs ≥2 tasks to have any edges — below that "View graph" is a no-op.
  const hasGraph = memberTotal >= 2
  const stateColor = planStateColor(plan.state)

  function handleDrillIn() {
    setActivePlanId(plan.id)
  }

  function handleViewGraph() {
    if (!hasGraph) return
    setActivePlanId(plan.id)
    void navigate({ to: '/workspaces/$workspaceId/graph', params: { workspaceId } })
  }

  return (
    <div
      role="group"
      aria-label={plan.title}
      data-testid={`plan-card-${plan.id}`}
      title={plan.title}
      className="group relative rounded-lg border border-[var(--color-border)] border-l-2 border-l-[var(--color-accent)]/40 bg-[var(--color-surface-1)] p-3 transition-colors hover:border-[var(--color-border)]/60 hover:bg-[var(--color-surface-2)]/40"
    >
      {/* Actions — ONE ⋯ menu, hover-revealed (touch: always visible). Keeps the
          resting plan card the same footprint + look as a task card. */}
      <div
        className="absolute right-1.5 top-1.5 z-10 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100"
        onClick={(e) => e.stopPropagation()}
      >
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
            <DropdownMenuItem onClick={handleDrillIn} className="flex items-center gap-2">
              <ArrowsOut size={13} />
              Open board
            </DropdownMenuItem>
            <DropdownMenuItem onClick={handleViewGraph} disabled={!hasGraph} className="flex items-center gap-2">
              <TreeStructure size={13} />
              View graph
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            {canApprove && (
              <DropdownMenuItem onClick={onApprove} disabled={isApproving} className="flex items-center gap-2">
                <Play size={13} weight="fill" />
                {isApproving ? 'Approving…' : 'Approve'}
              </DropdownMenuItem>
            )}
            {canStop && (
              <DropdownMenuItem onClick={() => setConfirmStop(true)} disabled={isStopping} className="flex items-center gap-2">
                <Stop size={13} weight="fill" />
                {isStopping ? 'Stopping…' : 'Stop'}
              </DropdownMenuItem>
            )}
            <DropdownMenuItem onClick={onEdit} className="flex items-center gap-2">
              <PencilSimple size={13} />
              Edit
            </DropdownMenuItem>
            <DropdownMenuSeparator />
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

      {/* Row 1 — state chip (the task card's priority slot) + title. The title
          is the primary drill-in control (open this plan's task board). */}
      <div className="flex items-start gap-2 pr-6">
        <span
          className="mt-0.5 inline-flex flex-shrink-0 items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-bold leading-tight"
          style={{ color: stateColor, backgroundColor: `${stateColor}1a`, borderColor: `${stateColor}4d` }}
        >
          <PlanStateGlyph state={plan.state} />
          {planStateLabel(plan.state)}
        </span>
        <button tabIndex={0}
          type="button"
          onClick={handleDrillIn}
          title="Open plan board"
          className="flex-1 line-clamp-2 text-left text-sm font-medium leading-snug text-[var(--color-secondary)] transition-colors hover:text-[var(--color-accent)]"
        >
          {plan.title}
        </button>
      </div>

      {/* Row 2 — meta: progress + owner (mirrors the task card's assignee row). */}
      <div className="mt-2 flex flex-wrap items-center gap-1.5">
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
          // Recognition-over-Recall: show the short lead token; full name on hover.
          <span
            title={owner.name}
            className="max-w-[120px] truncate rounded-full border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-0.5 text-[10px] text-[var(--color-muted)]"
          >
            {owner.name.split('—')[0].trim()}
          </span>
        )}
      </div>

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
              Stop
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

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
