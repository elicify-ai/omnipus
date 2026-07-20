import { useMemo, useState } from 'react'
import {
  DndContext,
  PointerSensor,
  KeyboardSensor,
  KeyboardCode,
  useSensor,
  useSensors,
  useDraggable,
  useDroppable,
  DragOverlay,
  type Announcements,
  type ScreenReaderInstructions,
  type DragStartEvent,
  type DragEndEvent,
} from '@dnd-kit/core'
import { useNavigate } from '@tanstack/react-router'
import { CaretRight, TreeStructure } from '@phosphor-icons/react'
import { TaskCard } from './TaskCard'
import { PlanCard } from './PlanCard'
import { STATUS_COLORS, STATUS_LABELS, STATUS_ORDER } from '@/lib/statusColors'
import { planStateColor, planStateLabel } from '@/lib/planStateColors'
import type { Task, Agent, Plan } from '@/lib/api'
import type { BoardAltitude } from '@/store/workspacesStore'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { filterByTag } from '@/lib/planFilter'
import { cn } from '@/lib/utils'

type TaskStatus = Task['status']

interface ColumnConfig {
  status: TaskStatus
  label: string
  /** Header tint — driven by the shared 7-state status palette (Forge Gold for
   * in_progress) so the column reads identically to the Graph and roll-ups. */
  headerColor: string
}

// 7-state lifecycle: inbox → next → planning → in_progress → blocked → done → failed
const COLUMNS: ColumnConfig[] = STATUS_ORDER.map((status) => ({
  status,
  label: STATUS_LABELS[status],
  headerColor: STATUS_COLORS[status],
}))

// Screen-reader-only instructions dnd-kit surfaces via aria-describedby on
// every draggable card, announced once when a card first receives focus.
// Space (not Enter) lifts a card — Enter opens the task instead (see the
// KeyboardSensor `keyboardCodes` override below and TaskCard's onKeyDown).
const BOARD_SCREEN_READER_INSTRUCTIONS: ScreenReaderInstructions = {
  draggable:
    'To pick up a task card, press space. While dragging, use the arrow keys to move the card between columns. Press space again to drop the task in its new column, or press escape to cancel.',
}

/**
 * Whether a card may be dropped into the target column, mirroring the backend
 * transition guard (pkg/task/store.go::validateTransition):
 *   - `blocked` is a backend-managed side-state — you may never drop INTO it.
 *   - `done` is terminal — you may never drag a card OUT of done.
 *   - `blocked` clears automatically — you may never drag a card OUT of blocked.
 * Returns { ok, reason } so the caller can show a graceful message.
 */
export function canDropTransition(from: TaskStatus, to: TaskStatus): { ok: boolean; reason?: string } {
  if (from === to) return { ok: true }
  if (to === 'blocked') {
    return { ok: false, reason: 'Blocked is set automatically when a dependency is unmet — you can’t move a task here.' }
  }
  if (from === 'done') {
    return { ok: false, reason: 'Done is final — completed tasks can’t be moved.' }
  }
  if (from === 'blocked') {
    return { ok: false, reason: 'Blocked clears automatically when its dependencies complete.' }
  }
  return { ok: true }
}

/**
 * Builds the dnd-kit `Announcements` (screen-reader live-region text) for a
 * drag lifecycle over the given root tasks. Pure and exported so the message
 * text is unit-testable without mounting `DndContext` (dnd-kit's pointer DnD
 * cannot be driven faithfully in jsdom — see BoardViewDnd.test.tsx). Scoped
 * to whichever set of tasks is actually draggable in the current board level
 * (loose tasks at the top level, or a plan's member tasks when drilled in).
 */
export function buildBoardAnnouncements(rootTasks: Task[]): Announcements {
  const taskTitle = (id: string | number) => rootTasks.find((t) => t.id === id)?.title ?? 'the task'
  const columnLabel = (id: string | number | undefined) => COLUMNS.find((c) => c.status === id)?.label

  return {
    onDragStart({ active }) {
      return `Picked up task "${taskTitle(active.id)}".`
    },
    onDragOver({ active, over }) {
      if (!over) return undefined
      const label = columnLabel(over.id)
      return label ? `Task "${taskTitle(active.id)}" is over the ${label} column.` : undefined
    },
    onDragEnd({ active, over }) {
      if (!over) return `Task "${taskTitle(active.id)}" was dropped.`
      const label = columnLabel(over.id)
      return label
        ? `Task "${taskTitle(active.id)}" was moved to the ${label} column.`
        : `Task "${taskTitle(active.id)}" was dropped.`
    },
    onDragCancel({ active }) {
      return `Moving task "${taskTitle(active.id)}" was cancelled.`
    },
  }
}

/**
 * Derives which status COLUMN a plan card renders in at the Board's top
 * level (Hierarchical Drill-Down board redesign) — purely a function of the
 * plan's member top-level tasks. A plan card's column is COMPUTED, never
 * user-set (it is not draggable):
 *
 *   1. Zero member tasks              → 'inbox'
 *   2. Every member task is 'done'    → 'done'
 *   3. Every member task is terminal
 *      ('done'|'failed') AND ≥1 'failed' → 'failed'
 *   4. Otherwise, the first match in this precedence order:
 *      any 'in_progress' → 'in_progress'
 *      else any 'blocked' → 'blocked'
 *      else any 'planning' → 'planning'
 *      else any 'next' → 'next'
 *      else → 'inbox'
 */
export function derivePlanColumn(memberTasks: Task[]): TaskStatus {
  if (memberTasks.length === 0) return 'inbox'
  if (memberTasks.every((t) => t.status === 'done')) return 'done'
  const allTerminal = memberTasks.every((t) => t.status === 'done' || t.status === 'failed')
  if (allTerminal && memberTasks.some((t) => t.status === 'failed')) return 'failed'
  if (memberTasks.some((t) => t.status === 'in_progress')) return 'in_progress'
  if (memberTasks.some((t) => t.status === 'blocked')) return 'blocked'
  if (memberTasks.some((t) => t.status === 'planning')) return 'planning'
  if (memberTasks.some((t) => t.status === 'next')) return 'next'
  return 'inbox'
}

/** Zero-filled per-status task count, used by the sticky header row at both board levels. */
function countByStatus(tasks: Task[]): Record<TaskStatus, number> {
  const counts = {} as Record<TaskStatus, number>
  for (const status of STATUS_ORDER) counts[status] = 0
  for (const t of tasks) counts[t.status] = (counts[t.status] ?? 0) + 1
  return counts
}

interface BoardViewProps {
  tasks: Task[]
  /** Plans in this workspace (ADR-049). */
  plans: Plan[]
  agents?: Agent[]
  /** Owning workspace — the ⑂ "view graph" control routes here. */
  workspaceId: string
  /**
   * Shared Board⇄Graph plan scope (workspacesStore.activePlanId).
   * `null` → TOP LEVEL (plan cards + loose tasks share the 7 status
   * columns). A plan id → DRILLED into that plan's own task board. Only
   * takes effect once the id actually resolves against `plans` — an
   * unresolved scope (plansError, a just-cleared plan, a refetch race)
   * falls back to the top level instead of rendering an empty board,
   * mirroring WorkspaceGraphTab's own `activePlan` guard.
   */
  activePlanId: string | null
  /** The active tag filter (`null` = none). */
  activeTagFilter: string | null
  altitude: BoardAltitude
  onTaskClick: (task: Task) => void
  /** Persist a drag-to-column status change. Required for kanban DnD. */
  onTaskMove?: (task: Task, newStatus: TaskStatus) => void
  /** Surface a rejected drop (e.g. dropping into `blocked`). */
  onMoveRejected?: (reason: string) => void
  /** Plan-card ⋯ menu actions (Approve draft-only / Stop running-or-approved / Edit / Clear). */
  onApprovePlan?: (plan: Plan) => void
  onStopPlan?: (plan: Plan) => void
  onEditPlan?: (plan: Plan) => void
  onClearPlan?: (plan: Plan) => void
  approvingPlanId?: string | null
  stoppingPlanId?: string | null
  clearingPlanId?: string | null
}

/**
 * Hierarchical Drill-Down board (replaces the Plan Swimlane redesign — the
 * per-plan swimlane bands were found cognitively overloaded). Unifies Board
 * scope with the Graph tab via the shared `activePlanId` store field:
 *
 *   - TOP LEVEL (`activePlanId === null`): a single flat kanban — the 7
 *     `STATUS_ORDER` columns hold PLAN CARDS (column derived from member
 *     tasks, see `derivePlanColumn`) together with LOOSE task cards (tasks
 *     with no resolving `plan_id`, in their own real status column).
 *   - DRILLED (`activePlanId === <planId>`): that plan's own member tasks
 *     render as a normal draggable kanban, behind a compact breadcrumb strip
 *     ("Board › {plan.title}").
 *
 * Horizontal status DnD (dnd-kit) is preserved for TASK cards at both
 * levels; plan cards are never draggable — their column is computed, not
 * user-set. Moving a task between plans stays a separate, explicit action
 * ("Move to plan…" on the task detail panel), not drag-and-drop.
 */
export function BoardView({
  tasks,
  plans,
  agents = [],
  workspaceId,
  activePlanId,
  activeTagFilter,
  altitude,
  onTaskClick,
  onTaskMove,
  onMoveRejected,
  onApprovePlan,
  onStopPlan,
  onEditPlan,
  onClearPlan,
  approvingPlanId = null,
  stoppingPlanId = null,
  clearingPlanId = null,
}: BoardViewProps) {
  // Filter out non-user-surface tasks (e.g. heartbeat tasks are hidden from general views)
  const userTasks = tasks.filter((t) => t.surface === 'user' || t.surface === undefined)
  // Board shows only top-level tasks — subtasks (parent_task_id present) are
  // NEVER rendered as standalone cards. They nest under their parent.
  // Membership/progress figures are computed off this UNFILTERED set (a
  // plan's real state/progress shouldn't wobble with the tag filter); only
  // which CARDS render is affected by the tag filter (rootTasksVisible).
  const rootTasksAll = userTasks.filter((t) => !t.parent_task_id)
  const rootTasksVisible = filterByTag(rootTasksAll, activeTagFilter)

  const activePlan = useMemo(
    () => (activePlanId != null ? (plans.find((p) => p.id === activePlanId) ?? null) : null),
    [activePlanId, plans],
  )

  if (activePlan) {
    return (
      <DrilledPlanBoard
        plan={activePlan}
        workspaceId={workspaceId}
        plans={plans}
        agents={agents}
        rootTasksAll={rootTasksAll}
        rootTasksVisible={rootTasksVisible}
        altitude={altitude}
        onTaskClick={onTaskClick}
        onTaskMove={onTaskMove}
        onMoveRejected={onMoveRejected}
      />
    )
  }

  return (
    <TopLevelBoard
      plans={plans}
      agents={agents}
      workspaceId={workspaceId}
      rootTasksAll={rootTasksAll}
      rootTasksVisible={rootTasksVisible}
      altitude={altitude}
      onTaskClick={onTaskClick}
      onTaskMove={onTaskMove}
      onMoveRejected={onMoveRejected}
      onApprovePlan={onApprovePlan}
      onStopPlan={onStopPlan}
      onEditPlan={onEditPlan}
      onClearPlan={onClearPlan}
      approvingPlanId={approvingPlanId}
      stoppingPlanId={stoppingPlanId}
      clearingPlanId={clearingPlanId}
    />
  )
}

// ── Top level ────────────────────────────────────────────────────────────────

interface TopLevelBoardProps {
  plans: Plan[]
  agents: Agent[]
  workspaceId: string
  rootTasksAll: Task[]
  rootTasksVisible: Task[]
  altitude: BoardAltitude
  onTaskClick: (task: Task) => void
  onTaskMove?: (task: Task, newStatus: TaskStatus) => void
  onMoveRejected?: (reason: string) => void
  onApprovePlan?: (plan: Plan) => void
  onStopPlan?: (plan: Plan) => void
  onEditPlan?: (plan: Plan) => void
  onClearPlan?: (plan: Plan) => void
  approvingPlanId: string | null
  stoppingPlanId: string | null
  clearingPlanId: string | null
}

function TopLevelBoard({
  plans,
  agents,
  workspaceId,
  rootTasksAll,
  rootTasksVisible,
  altitude,
  onTaskClick,
  onTaskMove,
  onMoveRejected,
  onApprovePlan,
  onStopPlan,
  onEditPlan,
  onClearPlan,
  approvingPlanId,
  stoppingPlanId,
  clearingPlanId,
}: TopLevelBoardProps) {
  const planIds = useMemo(() => new Set(plans.map((p) => p.id)), [plans])

  // Loose tasks — no plan_id, or a plan_id that doesn't resolve to a loaded
  // plan (plansError, a create-plan/tasks refetch race, a just-cleared plan)
  // — placed in their own real status column, tag-filter applied.
  const looseTasksVisible = useMemo(
    () => rootTasksVisible.filter((t) => !t.plan_id || !planIds.has(t.plan_id)),
    [rootTasksVisible, planIds],
  )

  // Plan cards are never tag-filtered (they aren't taggable themselves) and
  // their derived column is computed off ALL member tasks, not the visible
  // subset — a plan's true lifecycle column shouldn't wobble with the
  // active tag filter (mirrors the old swimlane header's plan-wide ⑂/progress).
  const plansByColumn = useMemo(() => {
    const map = new Map<TaskStatus, Plan[]>()
    for (const status of STATUS_ORDER) map.set(status, [])
    for (const plan of plans) {
      const members = rootTasksAll.filter((t) => t.plan_id === plan.id)
      map.get(derivePlanColumn(members))!.push(plan)
    }
    return map
  }, [plans, rootTasksAll])

  const memberFigures = useMemo(() => {
    const map = new Map<string, { memberTotal: number; memberDone: number }>()
    for (const plan of plans) {
      const members = rootTasksAll.filter((t) => t.plan_id === plan.id)
      map.set(plan.id, { memberTotal: members.length, memberDone: members.filter((t) => t.status === 'done').length })
    }
    return map
  }, [plans, rootTasksAll])

  const counts = useMemo(() => {
    const base = countByStatus(looseTasksVisible)
    for (const [status, colPlans] of plansByColumn) base[status] += colPlans.length
    return base
  }, [looseTasksVisible, plansByColumn])

  return (
    <div className="flex flex-col flex-1 min-h-0 overflow-hidden">
      <div className="flex-1 min-h-0 overflow-auto">
        <div className="min-w-max flex flex-col min-h-full">
          <StatusHeaderRow counts={counts} />
          <LaneStatusRow
            tasks={looseTasksVisible}
            plans={plans}
            agents={agents}
            altitude={altitude}
            onTaskClick={onTaskClick}
            onTaskMove={onTaskMove}
            onMoveRejected={onMoveRejected}
            renderColumnExtra={(status) => {
              const colPlans = plansByColumn.get(status) ?? []
              if (colPlans.length === 0) return null
              return colPlans.map((plan) => {
                const figures = memberFigures.get(plan.id) ?? { memberTotal: 0, memberDone: 0 }
                return (
                  <PlanCard
                    key={plan.id}
                    plan={plan}
                    workspaceId={workspaceId}
                    agents={agents}
                    memberTotal={figures.memberTotal}
                    memberDone={figures.memberDone}
                    onEdit={() => onEditPlan?.(plan)}
                    onApprove={() => onApprovePlan?.(plan)}
                    onStop={() => onStopPlan?.(plan)}
                    onClear={() => onClearPlan?.(plan)}
                    isApproving={approvingPlanId === plan.id}
                    isStopping={stoppingPlanId === plan.id}
                    isClearing={clearingPlanId === plan.id}
                  />
                )
              })
            }}
          />
        </div>
      </div>
    </div>
  )
}

// ── Drilled into a plan ──────────────────────────────────────────────────────

interface DrilledPlanBoardProps {
  plan: Plan
  workspaceId: string
  plans: Plan[]
  agents: Agent[]
  rootTasksAll: Task[]
  rootTasksVisible: Task[]
  altitude: BoardAltitude
  onTaskClick: (task: Task) => void
  onTaskMove?: (task: Task, newStatus: TaskStatus) => void
  onMoveRejected?: (reason: string) => void
}

function DrilledPlanBoard({
  plan,
  workspaceId,
  plans,
  agents,
  rootTasksAll,
  rootTasksVisible,
  altitude,
  onTaskClick,
  onTaskMove,
  onMoveRejected,
}: DrilledPlanBoardProps) {
  const planTasksVisible = useMemo(
    () => rootTasksVisible.filter((t) => t.plan_id === plan.id),
    [rootTasksVisible, plan.id],
  )
  const memberTotal = useMemo(() => rootTasksAll.filter((t) => t.plan_id === plan.id).length, [rootTasksAll, plan.id])
  const memberDone = useMemo(
    () => rootTasksAll.filter((t) => t.plan_id === plan.id && t.status === 'done').length,
    [rootTasksAll, plan.id],
  )
  const counts = useMemo(() => countByStatus(planTasksVisible), [planTasksVisible])

  return (
    <div className="flex flex-col flex-1 min-h-0 overflow-hidden">
      <PlanBreadcrumb plan={plan} workspaceId={workspaceId} memberTotal={memberTotal} memberDone={memberDone} />
      <div className="flex-1 min-h-0 overflow-auto">
        <div className="min-w-max flex flex-col min-h-full">
          <StatusHeaderRow counts={counts} />
          <LaneStatusRow
            tasks={planTasksVisible}
            plans={plans}
            agents={agents}
            altitude={altitude}
            onTaskClick={onTaskClick}
            onTaskMove={onTaskMove}
            onMoveRejected={onMoveRejected}
          />
        </div>
      </div>
    </div>
  )
}

interface PlanBreadcrumbProps {
  plan: Plan
  workspaceId: string
  memberTotal: number
  memberDone: number
}

/** Compact "Board › {plan.title}" strip shown only while drilled into a plan. */
function PlanBreadcrumb({ plan, workspaceId, memberTotal, memberDone }: PlanBreadcrumbProps) {
  const navigate = useNavigate()
  const setActivePlanId = useWorkspacesStore((s) => s.setActivePlanId)
  const hasGraph = memberTotal >= 2
  const pct = memberTotal > 0 ? Math.round((memberDone / memberTotal) * 100) : 0

  function handleViewGraph() {
    if (!hasGraph) return
    setActivePlanId(plan.id)
    void navigate({ to: '/workspaces/$workspaceId/graph', params: { workspaceId } })
  }

  return (
    <div
      className="flex items-center gap-2 px-3 h-[25px] flex-shrink-0 bg-[var(--color-surface-0)] border-b border-[var(--color-border)]/15"
      data-testid="plan-breadcrumb"
    >
      <button tabIndex={0}
        type="button"
        onClick={() => setActivePlanId(null)}
        className="text-xs font-medium text-[var(--color-muted)] hover:text-[var(--color-accent)] transition-colors"
      >
        Board
      </button>
      <CaretRight size={10} className="shrink-0 text-[var(--color-muted)]" aria-hidden="true" />
      <span className="text-xs font-semibold text-[var(--color-secondary)] truncate max-w-[240px]" title={plan.title}>
        {plan.title}
      </span>
      <span
        className="flex-shrink-0 inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-bold"
        style={{ color: planStateColor(plan.state), backgroundColor: `${planStateColor(plan.state)}1a` }}
      >
        {planStateLabel(plan.state)}
      </span>
      <div
        className="flex items-center gap-1 flex-shrink-0"
        role="img"
        aria-label={`Progress: ${memberDone} of ${memberTotal} tasks done`}
      >
        <div className="h-1.5 w-12 rounded-full bg-[var(--color-surface-2)] overflow-hidden">
          <div className="h-full rounded-full bg-[var(--color-accent)]" style={{ width: `${pct}%` }} />
        </div>
        <span className="text-[9px] text-[var(--color-muted)] tabular-nums">
          {memberDone}/{memberTotal}
        </span>
      </div>
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
    </div>
  )
}

// ── Shared: sticky status header row ────────────────────────────────────────

function StatusHeaderRow({ counts }: { counts: Record<TaskStatus, number> }) {
  return (
    <div className="flex sticky top-0 z-10 bg-[var(--color-surface-0)] border-b border-[var(--color-border)]/15">
      {COLUMNS.map((col) => (
        // Compact status header: a thin label + count strip.
        <div key={col.status} className="flex-1 min-w-[162px] flex items-center gap-2 px-3 h-[25px]">
          <span className="text-xs font-semibold leading-none" style={{ color: col.headerColor }}>
            {col.label}
          </span>
          <span className="rounded-full bg-[var(--color-surface-2)] px-1.5 text-[10px] font-semibold leading-none text-[var(--color-muted)]">
            {counts[col.status] ?? 0}
          </span>
        </div>
      ))}
    </div>
  )
}

// ── Shared: one row of 7 status cells (single DndContext) ──────────────────

interface LaneStatusRowProps {
  /** The draggable task cards to render, bucketed by status — LOOSE tasks at the top level, or a plan's member tasks when drilled in. */
  tasks: Task[]
  plans: Plan[]
  agents: Agent[]
  altitude: BoardAltitude
  onTaskClick: (task: Task) => void
  onTaskMove?: (task: Task, newStatus: TaskStatus) => void
  onMoveRejected?: (reason: string) => void
  /** Top-level only — renders each column's plan cards ABOVE its task cards. */
  renderColumnExtra?: (status: TaskStatus) => React.ReactNode
}

/**
 * One row of 7 status cells, each independently droppable — horizontal
 * status DnD, scoped to the tasks passed in via its own `DndContext` (top
 * level = loose tasks only; drilled = one plan's member tasks).
 */
function LaneStatusRow({
  tasks,
  plans,
  agents,
  altitude,
  onTaskClick,
  onTaskMove,
  onMoveRejected,
  renderColumnExtra,
}: LaneStatusRowProps) {
  const [activeTask, setActiveTask] = useState<Task | null>(null)

  const sensors = useSensors(
    // A small activation distance lets a plain click still open the detail panel
    // (the card's onClick) without being swallowed by the drag.
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    // Restrict the lift/drop key to Space only (dnd-kit's default is Space
    // AND Enter) so Enter stays free for TaskCard's "open task" action.
    useSensor(KeyboardSensor, {
      keyboardCodes: {
        start: [KeyboardCode.Space],
        cancel: [KeyboardCode.Esc],
        end: [KeyboardCode.Space],
      },
    }),
  )

  const announcements = useMemo<Announcements>(() => buildBoardAnnouncements(tasks), [tasks])

  function handleDragStart(event: DragStartEvent) {
    const id = String(event.active.id)
    setActiveTask(tasks.find((t) => t.id === id) ?? null)
  }

  function handleDragEnd(event: DragEndEvent) {
    const dragged = activeTask
    setActiveTask(null)
    if (!event.over || !dragged) return
    const targetStatus = String(event.over.id) as TaskStatus
    if (targetStatus === dragged.status) return
    const verdict = canDropTransition(dragged.status, targetStatus)
    if (!verdict.ok) {
      if (verdict.reason) onMoveRejected?.(verdict.reason)
      return
    }
    onTaskMove?.(dragged, targetStatus)
  }

  return (
    <DndContext
      sensors={sensors}
      accessibility={{
        screenReaderInstructions: BOARD_SCREEN_READER_INSTRUCTIONS,
        announcements,
      }}
      onDragStart={handleDragStart}
      onDragEnd={handleDragEnd}
      onDragCancel={() => setActiveTask(null)}
    >
      <div className="flex flex-1">
        {COLUMNS.map((col) => (
          <LaneStatusCell
            key={col.status}
            config={col}
            tasks={tasks.filter((t) => t.status === col.status)}
            plans={plans}
            agents={agents}
            altitude={altitude}
            activeTask={activeTask}
            onTaskClick={onTaskClick}
            extra={renderColumnExtra?.(col.status)}
          />
        ))}
      </div>
      <DragOverlay dropAnimation={null}>
        {activeTask ? (
          // A purely VISUAL clone that follows the cursor while dragging —
          // the "real" draggable card stays in place underneath (dimmed via
          // opacity-40 in DraggableTaskCard).
          <div aria-hidden="true" className="opacity-90 rotate-2 cursor-grabbing">
            <TaskCard
              task={activeTask}
              plans={plans}
              agents={agents}
              altitude="top-level"
              onClick={() => {}}
            />
          </div>
        ) : null}
      </DragOverlay>
    </DndContext>
  )
}

interface LaneStatusCellProps {
  config: ColumnConfig
  tasks: Task[]
  plans: Plan[]
  agents: Agent[]
  altitude: BoardAltitude
  activeTask: Task | null
  onTaskClick: (task: Task) => void
  extra?: React.ReactNode
}

function LaneStatusCell({
  config,
  tasks,
  plans,
  agents,
  altitude,
  activeTask,
  onTaskClick,
  extra,
}: LaneStatusCellProps) {
  const { setNodeRef, isOver } = useDroppable({ id: config.status })

  // Visual feedback: highlight a cell the dragged card can legally land in.
  const canAccept = activeTask ? canDropTransition(activeTask.status, config.status).ok : true

  return (
    <div
      ref={setNodeRef}
      role="group"
      aria-label={`${config.label} column`}
      className={cn(
        'flex flex-col flex-1 min-w-[162px] min-h-[56px] gap-2 p-2 transition-colors',
        isOver && canAccept && 'bg-[var(--color-accent)]/5 ring-1 ring-inset ring-[var(--color-accent)]/40',
        isOver && !canAccept && 'bg-[var(--color-error)]/5 ring-1 ring-inset ring-[var(--color-error)]/40',
      )}
    >
      {extra}
      {tasks.map((task) => (
        <DraggableTaskCard
          key={task.id}
          task={task}
          plans={plans}
          agents={agents}
          altitude={altitude}
          onClick={() => onTaskClick(task)}
          onChildClick={onTaskClick}
        />
      ))}
    </div>
  )
}

interface DraggableTaskCardProps {
  task: Task
  plans: Plan[]
  agents: Agent[]
  altitude: BoardAltitude
  onClick: () => void
  onChildClick: (child: Task) => void
}

function DraggableTaskCard({
  task,
  plans,
  agents,
  altitude,
  onClick,
  onChildClick,
}: DraggableTaskCardProps) {
  // `roleDescription: 'draggable task'` overrides dnd-kit's generic default
  // ("draggable") so screen readers announce something meaningful.
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, isDragging } = useDraggable({
    id: task.id,
    attributes: { roleDescription: 'draggable task' },
  })

  return (
    // Plain measurement/positioning node only — NOT a tab stop. dnd-kit's
    // attributes/listeners are spread onto TaskCard's own root button below
    // instead, so each card has exactly one focusable element (WCAG 4.1.2 —
    // previously this wrapper AND TaskCard's inner div were both
    // role="button" tabIndex=0, two tab stops for one card).
    <div
      ref={setNodeRef}
      // The card while being dragged is shown in the DragOverlay; hide the source.
      className={cn(isDragging && 'opacity-40')}
    >
      <TaskCard
        task={task}
        plans={plans}
        agents={agents}
        altitude={altitude}
        onClick={onClick}
        onChildClick={onChildClick}
        // `useDraggable`'s `listeners` is `SyntheticListenerMap | undefined`
        // (undefined only while the draggable is `disabled`, which this call
        // never sets — but the type doesn't know that). Collapse it into the
        // `drag` prop's own optionality here so `TaskCardDrag.listeners` can
        // stay non-undefined: a card is either fully draggable (attributes +
        // real listeners + activatorRef) or not draggable at all, never
        // "draggable but with dead keyboard/pointer activators".
        drag={listeners ? { attributes, listeners, activatorRef: setActivatorNodeRef } : undefined}
      />
    </div>
  )
}
