import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import {
  Strategy,
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
import { planStateColor, planStateLabel, planSecondaryChipLabel } from '@/lib/planStateColors'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { cn } from '@/lib/utils'

/**
 * Per-state icon (paired ALWAYS with `planStateLabel`'s text — never
 * color-alone, a11y). Ported verbatim from the retired PlanLaneHeader.
 */
function PlanStateGlyph({ state, size = 10 }: { state: Plan['state']; size?: number }) {
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
  /** Body click (not on a control) → open the plan's edit slide-over. Also used by the ⋯ menu's Edit item. */
  onEdit: () => void
  onApprove: () => void
  onStop: () => void
  onClear: () => void
  isApproving?: boolean
  isStopping?: boolean
  isClearing?: boolean
}

/**
 * Top-level Board card for a Plan (Hierarchical Drill-Down board redesign,
 * replaces PlanLaneHeader's per-lane band header). Rendered into the status
 * column `derivePlanColumn` computes from the plan's member tasks
 * (BoardView.tsx) — NOT draggable, its column is derived, never user-set.
 *
 * Body click opens the plan's edit slide-over (`onEdit`); the controls row
 * (drill-in / graph / ⋯ actions) `stopPropagation`s so clicking any control
 * never also fires the body's edit handler.
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
  const secondary = planSecondaryChipLabel(plan)
  const pct = memberTotal > 0 ? Math.round((memberDone / memberTotal) * 100) : 0
  const canApprove = plan.state === 'draft'
  const canStop = plan.state === 'running' || plan.state === 'approved'
  // A DAG needs at least 2 tasks to draw any edges — below that the Graph
  // tab has nothing meaningful to show for this plan.
  const hasGraph = memberTotal >= 2

  function handleDrillIn() {
    setActivePlanId(plan.id)
  }

  function handleViewGraph() {
    if (!hasGraph) return
    setActivePlanId(plan.id)
    void navigate({ to: '/workspaces/$workspaceId/graph', params: { workspaceId } })
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLDivElement>) {
    // Ignore keydowns bubbled up from a nested interactive control (the
    // drill-in/graph/⋯ buttons below) — only the card itself opening Edit.
    if (e.target !== e.currentTarget) return
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      onEdit()
    }
  }

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onEdit}
      onKeyDown={handleKeyDown}
      data-testid={`plan-card-${plan.id}`}
      title={secondary ? `${plan.title} — ${secondary}` : plan.title}
      className="rounded-lg border border-[var(--color-border)] border-l-2 border-l-[var(--color-accent)]/50 bg-[var(--color-surface-1)] p-3 cursor-pointer transition-colors hover:border-[var(--color-border)]/60 hover:bg-[var(--color-surface-2)]/40"
    >
      {/* Row 1 — plan icon + title */}
      <div className="flex items-start gap-2">
        <Strategy size={14} weight="fill" className="shrink-0 mt-0.5 text-[color:var(--color-accent)]" aria-hidden="true" />
        <p className="flex-1 text-sm font-semibold text-[var(--color-secondary)] leading-snug line-clamp-2">
          {plan.title}
        </p>
      </div>

      {/* Row 2 — state badge (icon + text, never color-alone) */}
      <div className="mt-1.5 flex items-center gap-1.5">
        <span
          className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-bold"
          style={{ color: planStateColor(plan.state), backgroundColor: `${planStateColor(plan.state)}1a` }}
        >
          <PlanStateGlyph state={plan.state} />
          {planStateLabel(plan.state)}
        </span>
      </div>

      {/* Row 3 — progress + owner */}
      <div className="mt-2 flex items-center gap-1.5 flex-wrap">
        <div
          className="flex items-center gap-1 flex-shrink-0"
          role="img"
          aria-label={`Progress: ${memberDone} of ${memberTotal} tasks done`}
        >
          <Progress value={pct} className="h-1.5 w-10" />
          <span className="text-[9px] text-[var(--color-muted)] flex-shrink-0 tabular-nums">
            {memberDone}/{memberTotal}
          </span>
        </div>

        {owner && (
          // Recognition-over-Recall (ux-psychology): show the short lead
          // token; full name stays on hover via `title`.
          <span
            title={owner.name}
            className="min-w-0 truncate rounded-full bg-[var(--color-surface-2)] border border-[var(--color-border)] px-1.5 py-0.5 text-[10px] text-[var(--color-muted)] max-w-[100px]"
          >
            {owner.name.split('—')[0].trim()}
          </span>
        )}
      </div>

      {/* Row 4 — controls (drill-in / graph / actions). stopPropagation so
          clicking any control here never also fires the card body's Edit
          handler above. */}
      <div className="mt-2 flex items-center justify-end gap-1" onClick={(e) => e.stopPropagation()}>
        <button tabIndex={0}
          type="button"
          onClick={handleDrillIn}
          aria-label="Open plan board"
          title="Open plan board"
          className="flex-shrink-0 inline-flex items-center justify-center p-1 rounded text-[var(--color-muted)] hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-2)] transition-colors pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
        >
          <ArrowsOut size={13} />
        </button>

        <button tabIndex={0}
          type="button"
          onClick={handleViewGraph}
          disabled={!hasGraph}
          aria-label="View plan graph"
          title={hasGraph ? 'View plan graph' : 'Needs at least 2 tasks in this plan'}
          className="flex-shrink-0 inline-flex items-center justify-center p-1 rounded text-[var(--color-muted)] hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-2)] transition-colors disabled:opacity-30 disabled:pointer-events-none disabled:hover:text-[var(--color-muted)] pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
        >
          <TreeStructure size={13} />
        </button>

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button tabIndex={0}
              type="button"
              aria-label={`Plan actions for ${plan.title}`}
              className="flex-shrink-0 inline-flex items-center justify-center p-1 rounded text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
            >
              <DotsThreeVertical size={14} weight="bold" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-40">
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
