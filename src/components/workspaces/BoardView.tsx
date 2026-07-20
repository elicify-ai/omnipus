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
import { CaretDown, CaretRight, LinkBreak } from '@phosphor-icons/react'
import { TaskCard } from './TaskCard'
import { PlanLaneHeader } from './PlanLaneHeader'
import { STATUS_COLORS, STATUS_LABELS, STATUS_ORDER } from '@/lib/statusColors'
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

/** Sentinel lane id for the "no plan" band (never a real Plan.id — those are ULIDs). */
export const LOOSE_LANE_ID = '__loose__'

const LOOSE_LANE_LABEL = 'Loose tasks (no plan)'

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
 * cannot be driven faithfully in jsdom — see BoardViewDnd.test.tsx). Called
 * once per swimlane band, scoped to that band's own visible tasks — a drag
 * only ever involves tasks rendered in the SAME band (DnD is scoped per lane,
 * see LaneStatusRow).
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

/** Swimlane sort tier — running(0) → approved(1) → draft(2) → done/failed(3); stable within a tier (Array.sort is spec-stable). */
function planTier(state: Plan['state']): number {
  switch (state) {
    case 'running':
      return 0
    case 'approved':
      return 1
    case 'draft':
      return 2
    case 'done':
    case 'failed':
    default:
      return 3
  }
}

function isTerminalPlanState(state: Plan['state']): boolean {
  return state === 'done' || state === 'failed'
}

interface BoardViewProps {
  tasks: Task[]
  /** Plans in this workspace (ADR-049) — one swimlane band per plan. */
  plans: Plan[]
  agents?: Agent[]
  /** Owning workspace — a lane header's ⑂ "view graph" button routes here. */
  workspaceId: string
  /** The active tag filter (`null` = none). */
  activeTagFilter: string | null
  altitude: BoardAltitude
  onTaskClick: (task: Task) => void
  /** Persist a drag-to-column status change. Required for kanban DnD. */
  onTaskMove?: (task: Task, newStatus: TaskStatus) => void
  /** Surface a rejected drop (e.g. dropping into `blocked`). */
  onMoveRejected?: (reason: string) => void
  /** Plan-lane header actions (⋯ menu / Approve draft-only / Stop running-or-approved / Edit / Clear). */
  onApprovePlan?: (plan: Plan) => void
  onStopPlan?: (plan: Plan) => void
  onEditPlan?: (plan: Plan) => void
  onClearPlan?: (plan: Plan) => void
  approvingPlanId?: string | null
  stoppingPlanId?: string | null
  clearingPlanId?: string | null
}

/**
 * Plan Swimlane board (v0.3 UX rework, psychology-grounded redesign): a
 * single sticky `STATUS_ORDER` header row + horizontal swimlane bands
 * grouped by plan, each with a collapsible `PlanLaneHeader` on its left.
 * Tasks with no `plan_id` — or whose `plan_id` doesn't resolve to a loaded
 * plan (plansError, a create-plan/tasks refetch race, a just-cleared plan)
 * — render in a final "Loose tasks (no plan)" band; they never vanish.
 * Horizontal status DnD (dnd-kit) is preserved WITHIN each band — moving a
 * task between plans is a separate, explicit action ("Move to plan…" on the
 * task detail panel), not drag-and-drop, so a card can never silently change
 * `plan_id` from a mis-aimed drop.
 */
export function BoardView({
  tasks,
  plans,
  agents = [],
  workspaceId,
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
  // plan's real progress shouldn't wobble with the tag filter); only which
  // CARDS render is affected by the tag filter (rootTasksVisible below).
  const rootTasksAll = userTasks.filter((t) => !t.parent_task_id)
  const rootTasksVisible = filterByTag(rootTasksAll, activeTagFilter)

  const collapsedLanes = useWorkspacesStore((s) => s.collapsedLanes)
  const setLaneCollapsed = useWorkspacesStore((s) => s.setLaneCollapsed)

  const orderedPlans = useMemo(() => [...plans].sort((a, b) => planTier(a.state) - planTier(b.state)), [plans])

  function effectiveCollapsed(laneId: string, defaultCollapsed: boolean): boolean {
    return collapsedLanes[laneId] ?? defaultCollapsed
  }

  // Ids of plans actually present in the loaded `plans` list. A task's
  // `plan_id` can point at a plan that ISN'T in that list — plansError
  // (`plans` defaults to `[]`), the create-plan/tasks refetch race, or the
  // post-Clear window before the plans query re-settles. Route those
  // orphaned-plan_id tasks into the Loose band below instead of the old
  // `!t.plan_id` check, which rendered them in NEITHER a plan band nor
  // Loose — silently vanishing them from the board (review-gate fix #1).
  const planIds = useMemo(() => new Set(plans.map((p) => p.id)), [plans])

  // Single pass over the (tag-filtered) visible root tasks: bucket each into
  // its plan's lane, or Loose when `plan_id` is absent or unresolved. Every
  // task lands in exactly one bucket, so the union of every plan bucket +
  // Loose is exactly `rootTasksVisible` — the sticky per-status header
  // counts below are derived from this SAME bucketing (`renderedTasksVisible`),
  // so a count can never exceed the cards that actually render.
  const { laneTasksByPlanId, looseTasksVisible } = useMemo(() => {
    const byPlan = new Map<string, Task[]>()
    const loose: Task[] = []
    for (const t of rootTasksVisible) {
      if (t.plan_id && planIds.has(t.plan_id)) {
        const existing = byPlan.get(t.plan_id)
        if (existing) existing.push(t)
        else byPlan.set(t.plan_id, [t])
      } else {
        loose.push(t)
      }
    }
    return { laneTasksByPlanId: byPlan, looseTasksVisible: loose }
  }, [rootTasksVisible, planIds])

  const renderedTasksVisible = useMemo(
    () => [...laneTasksByPlanId.values()].flat().concat(looseTasksVisible),
    [laneTasksByPlanId, looseTasksVisible],
  )

  const looseTasksAllCount = rootTasksAll.filter((t) => !t.plan_id || !planIds.has(t.plan_id)).length
  const looseCollapsed = effectiveCollapsed(LOOSE_LANE_ID, false)

  return (
    <div className="flex flex-col flex-1 min-h-0 overflow-hidden">
      <div className="flex-1 min-h-0 overflow-auto">
        {/* min-w-max wrapper — floors ALL rows (header + every band) at ONE
            shared content width so their backgrounds and border lines span the
            FULL horizontal scroll width. Each row is `width:auto`, i.e. 100% of
            this wrapper, so they're always equal; the wrapper itself is floored
            at its content's max-content (the widest row) and still stretches to
            fill the viewport on wide screens. Without this, each row was only as
            wide as the visible viewport while its columns overflowed past it —
            so the sticky header bar and the horizontal band separators "broke
            up" (stopped dead) the moment you scrolled right. A per-row min-w-max
            does NOT work: it sizes each row to its OWN content, so a band with a
            wider task card grows past the header and they misalign again. */}
        <div className="min-w-max">
        {/* Sticky status-column header row — spans every swimlane band below it. */}
        <div className="flex sticky top-0 z-10 bg-[var(--color-surface-1)] border-b border-[var(--color-border)]/50">
          <div className="w-[224px] shrink-0 sticky left-0 z-20 bg-[var(--color-surface-1)]" aria-hidden="true" />
          {COLUMNS.map((col) => {
            const count = renderedTasksVisible.filter((t) => t.status === col.status).length
            return (
              // Compact status header (~40% of the old height): a thin label +
              // count strip. The per-column "+" quick-add buttons were removed
              // — new tasks are created from the toolbar's "New task" button.
              <div
                key={col.status}
                className="flex-1 min-w-[162px] flex items-center gap-2 px-3 h-[15px]"
              >
                <span className="text-[11px] font-semibold leading-none" style={{ color: col.headerColor }}>
                  {col.label}
                </span>
                <span className="rounded-full bg-[var(--color-surface-2)] px-1 text-[9px] font-semibold leading-none text-[var(--color-muted)]">
                  {count}
                </span>
              </div>
            )
          })}
        </div>

        {/* Plan swimlane bands — running → approved → draft → done/failed (planTier order). */}
        {orderedPlans.map((plan) => {
          const memberTotal = rootTasksAll.filter((t) => t.plan_id === plan.id).length
          const memberDone = rootTasksAll.filter((t) => t.plan_id === plan.id && t.status === 'done').length
          const laneTasks = laneTasksByPlanId.get(plan.id) ?? []
          const collapsed = effectiveCollapsed(plan.id, isTerminalPlanState(plan.state))
          return (
            <div key={plan.id} className="flex border-b border-[var(--color-border)]/50" data-testid={`swimlane-band-${plan.id}`}>
              <div className="w-[224px] shrink-0 sticky left-0 z-10 border-r border-[var(--color-border)]/25 bg-[var(--color-surface-1)]">
                <PlanLaneHeader
                  plan={plan}
                  workspaceId={workspaceId}
                  agents={agents}
                  memberTotal={memberTotal}
                  memberDone={memberDone}
                  collapsed={collapsed}
                  onToggleCollapse={() => setLaneCollapsed(plan.id, !collapsed)}
                  onEdit={() => onEditPlan?.(plan)}
                  onApprove={() => onApprovePlan?.(plan)}
                  onStop={() => onStopPlan?.(plan)}
                  onClear={() => onClearPlan?.(plan)}
                  isApproving={approvingPlanId === plan.id}
                  isStopping={stoppingPlanId === plan.id}
                  isClearing={clearingPlanId === plan.id}
                />
              </div>
              {collapsed ? (
                <CollapsedLaneColumns />
              ) : (
                <LaneStatusRow
                  laneLabel={plan.title}
                  tasks={laneTasks}
                  plans={plans}
                  agents={agents}
                  altitude={altitude}
                  onTaskClick={onTaskClick}
                  onTaskMove={onTaskMove}
                  onMoveRejected={onMoveRejected}
                />
              )}
            </div>
          )
        })}

        {/* Loose tasks (no plan) — always last, always rendered (never hidden even
            when empty), so a plan-less workspace behaves exactly like the
            pre-swimlane flat board. */}
        <div className="flex" data-testid="swimlane-band-loose">
          <div className="w-[224px] shrink-0 sticky left-0 z-10 border-r border-[var(--color-border)]/25 bg-[var(--color-surface-1)]">
            <div className="flex items-center gap-1.5 px-2.5 py-2 w-full min-w-0" data-testid="plan-lane-header-loose">
              <button tabIndex={0}
                type="button"
                onClick={() => setLaneCollapsed(LOOSE_LANE_ID, !looseCollapsed)}
                aria-expanded={!looseCollapsed}
                aria-label={looseCollapsed ? `Expand ${LOOSE_LANE_LABEL} lane` : `Collapse ${LOOSE_LANE_LABEL} lane`}
                className="flex items-center gap-1 min-w-0 flex-1 text-left text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors"
              >
                {looseCollapsed ? <CaretRight size={12} className="shrink-0" /> : <CaretDown size={12} className="shrink-0" />}
                <LinkBreak size={13} className="shrink-0 text-[var(--color-muted)]" aria-hidden="true" />
                <span className="text-xs font-semibold text-[var(--color-secondary)] truncate">{LOOSE_LANE_LABEL}</span>
              </button>
              <span className="flex-shrink-0 rounded-full bg-[var(--color-surface-2)] px-1.5 py-0.5 text-[10px] font-semibold text-[var(--color-muted)]">
                {looseTasksAllCount}
              </span>
            </div>
          </div>
          {looseCollapsed ? (
            <CollapsedLaneColumns />
          ) : (
            <LaneStatusRow
              laneLabel={LOOSE_LANE_LABEL}
              tasks={looseTasksVisible}
              plans={plans}
              agents={agents}
              altitude={altitude}
              onTaskClick={onTaskClick}
              onTaskMove={onTaskMove}
              onMoveRejected={onMoveRejected}
            />
          )}
        </div>
        </div>
      </div>
    </div>
  )
}

/**
 * Faint column dividers drawn across a COLLAPSED band. A collapsed lane renders
 * no status cells (just its title-line header), which would otherwise leave the
 * board's vertical grid broken over that row — this keeps the column lines
 * continuous through collapsed lanes. Purely decorative: no tasks, no
 * droppables, aria-hidden. Column count/width + border weight mirror the real
 * `LaneStatusCell` so the lines align exactly with the expanded bands above and
 * below it. */
function CollapsedLaneColumns() {
  return (
    <div className="flex flex-1" aria-hidden="true">
      {COLUMNS.map((col) => (
        <div
          key={col.status}
          className="flex-1 min-w-[162px] border-r border-[var(--color-border)]/25 last:border-r-0"
        />
      ))}
    </div>
  )
}

interface LaneStatusRowProps {
  /** Human label used to disambiguate this lane's per-status droppable aria-labels from every other lane's. */
  laneLabel: string
  /** This lane's visible (tag-filtered) top-level tasks — the ONLY tasks a drag in this lane's DndContext can ever reference. */
  tasks: Task[]
  plans: Plan[]
  agents: Agent[]
  altitude: BoardAltitude
  onTaskClick: (task: Task) => void
  onTaskMove?: (task: Task, newStatus: TaskStatus) => void
  onMoveRejected?: (reason: string) => void
}

/**
 * One swimlane band's row of 7 status cells, each independently droppable —
 * horizontal status DnD, scoped to THIS band only via its own `DndContext`
 * (a card can never be dropped into another band's cell because each band's
 * drag lifecycle is a wholly separate dnd-kit instance).
 */
function LaneStatusRow({
  laneLabel,
  tasks,
  plans,
  agents,
  altitude,
  onTaskClick,
  onTaskMove,
  onMoveRejected,
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
            laneLabel={laneLabel}
            config={col}
            tasks={tasks.filter((t) => t.status === col.status)}
            plans={plans}
            agents={agents}
            altitude={altitude}
            activeTask={activeTask}
            onTaskClick={onTaskClick}
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
  laneLabel: string
  config: ColumnConfig
  tasks: Task[]
  plans: Plan[]
  agents: Agent[]
  altitude: BoardAltitude
  activeTask: Task | null
  onTaskClick: (task: Task) => void
}

function LaneStatusCell({
  laneLabel,
  config,
  tasks,
  plans,
  agents,
  altitude,
  activeTask,
  onTaskClick,
}: LaneStatusCellProps) {
  const { setNodeRef, isOver } = useDroppable({ id: config.status })

  // Visual feedback: highlight a cell the dragged card can legally land in.
  const canAccept = activeTask ? canDropTransition(activeTask.status, config.status).ok : true

  return (
    <div
      ref={setNodeRef}
      role="group"
      aria-label={`${laneLabel} ${config.label} column`}
      className={cn(
        'flex flex-col flex-1 min-w-[162px] min-h-[56px] gap-2 p-2 border-r border-[var(--color-border)]/25 last:border-r-0 transition-colors',
        isOver && canAccept && 'bg-[var(--color-accent)]/5 ring-1 ring-inset ring-[var(--color-accent)]/40',
        isOver && !canAccept && 'bg-[var(--color-error)]/5 ring-1 ring-inset ring-[var(--color-error)]/40',
      )}
    >
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
