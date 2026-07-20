import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import {
  CaretDown,
  CaretRight,
  Strategy,
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
 * color-alone, a11y) for the lane header's state badge. Deliberately a
 * switch (not a lookup map typed against `Icon`) to keep this file's Phosphor
 * import list the single source of truth for which glyphs are in play.
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

interface PlanLaneHeaderProps {
  plan: Plan
  /** Owning workspace — needed to build the Graph tab's route params. */
  workspaceId: string
  agents: Agent[]
  /** Total member tasks (ALL of this plan's tasks, independent of any tag filter). */
  memberTotal: number
  /** Member tasks currently `done`. */
  memberDone: number
  collapsed: boolean
  onToggleCollapse: () => void
  onEdit: () => void
  onApprove: () => void
  onStop: () => void
  onClear: () => void
  isApproving?: boolean
  isStopping?: boolean
  isClearing?: boolean
}

/**
 * The plan-lane header — left-hand label of one swimlane band on the Board
 * (Plan Swimlane redesign). Uses `planStateColors.ts`'s state-color and
 * secondary-chip logic, but is its own compact, single-row band header
 * (distinct from a standalone plan summary card) that also owns lane
 * collapse and the cross-tab "view this plan's graph" hop.
 */
export function PlanLaneHeader({
  plan,
  workspaceId,
  agents,
  memberTotal,
  memberDone,
  collapsed,
  onToggleCollapse,
  onEdit,
  onApprove,
  onStop,
  onClear,
  isApproving = false,
  isStopping = false,
  isClearing = false,
}: PlanLaneHeaderProps) {
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

  function handleViewGraph() {
    if (!hasGraph) return
    setActivePlanId(plan.id)
    void navigate({ to: '/workspaces/$workspaceId/graph', params: { workspaceId } })
  }

  return (
    <div
      className="flex flex-col gap-1 px-2.5 py-2 w-full min-w-0"
      data-testid={`plan-lane-header-${plan.id}`}
      title={secondary ? `${plan.title} — ${secondary}` : plan.title}
    >
      {/* Row 1 — collapse toggle + plan title + state badge. The title gets
          its OWN row so the narrow 224px gutter can never squeeze it to zero
          width: the previous single-row layout packed the state badge,
          progress, owner chip and two action buttons alongside a `flex-1`
          title, and their fixed widths summed past 224px — collapsing the
          title to nothing (it rendered as an empty `truncate` span). */}
      <div className="flex items-center gap-1.5 min-w-0">
        <button tabIndex={0}
          type="button"
          onClick={onToggleCollapse}
          aria-expanded={!collapsed}
          aria-label={collapsed ? `Expand ${plan.title} lane` : `Collapse ${plan.title} lane`}
          className="flex items-center gap-1 min-w-0 flex-1 text-left text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors"
        >
          {collapsed ? <CaretRight size={12} className="shrink-0" /> : <CaretDown size={12} className="shrink-0" />}
          <Strategy size={13} weight="fill" className="shrink-0 text-[color:var(--color-accent)]" aria-hidden="true" />
          <span className="text-xs font-semibold text-[var(--color-secondary)] truncate">
            {plan.title}
          </span>
        </button>

        <span
          className="flex-shrink-0 inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-bold"
          style={{ color: planStateColor(plan.state), backgroundColor: `${planStateColor(plan.state)}1a` }}
        >
          <PlanStateGlyph state={plan.state} />
          {planStateLabel(plan.state)}
        </span>
      </div>

      {/* Row 2 — progress, owner, and lane actions. Left-padded so it hangs
          under the title (clearing the caret+icon on row 1). The `flex-1`
          spacer pushes the actions to the right edge of the gutter. HIDDEN when
          the lane is collapsed, so a collapsed band is a single title line —
          rendering the full row 2 made the collapsed state too tall. */}
      {!collapsed && (
      <div className="flex items-center gap-1.5 min-w-0 pl-[18px]">
        <div className="flex items-center gap-1 flex-shrink-0" role="img" aria-label={`Progress: ${memberDone} of ${memberTotal} tasks done`}>
          <Progress value={pct} className="h-1.5 w-8" />
          <span className="text-[9px] text-[var(--color-muted)] flex-shrink-0 tabular-nums">
            {memberDone}/{memberTotal}
          </span>
        </div>

        {owner && (
          // Recognition-over-Recall (ux-psychology): the gutter is narrow, so a full
          // display name ("Jim — Planner & Orchestrator") truncates to unreadable
          // ("Jim — Pla…"). Show the short lead token; full name stays on hover.
          <span
            title={owner.name}
            className="min-w-0 truncate rounded-full bg-[var(--color-surface-2)] border border-[var(--color-border)] px-1.5 py-0.5 text-[10px] text-[var(--color-muted)] max-w-[100px]"
          >
            {owner.name.split('—')[0].trim()}
          </span>
        )}

        <div className="flex-1" aria-hidden="true" />

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
          <DropdownMenuContent align="start" className="w-40">
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
