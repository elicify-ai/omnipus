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
import { TaskCard } from './TaskCard'
import { STATUS_COLORS, STATUS_LABELS, STATUS_ORDER } from '@/lib/statusColors'
import type { Task, Agent, Plan } from '@/lib/api'
import type { BoardAltitude } from '@/store/workspacesStore'
import { cn } from '@/lib/utils'

type TaskStatus = Task['status']

interface ColumnConfig {
  status: TaskStatus
  label: string
  /** Header tint — driven by the shared status palette (Forge Gold for
   * in_progress) so the column reads identically to the Graph and roll-ups. */
  headerColor: string
}

// Columns are read off STATUS_ORDER — NEVER hardcoded here (ADR-051 D5 drops
// `planning` from the canonical status set; this board just follows suit and
// renders however many columns STATUS_ORDER holds, with no board-side edit).
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
 * cannot be driven faithfully in jsdom — see BoardViewDnd.test.tsx).
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

/** Zero-filled per-status task count, used by the sticky header row. */
function countByStatus(tasks: Task[]): Record<TaskStatus, number> {
  const counts = {} as Record<TaskStatus, number>
  for (const status of STATUS_ORDER) counts[status] = 0
  for (const t of tasks) counts[t.status] = (counts[t.status] ?? 0) + 1
  return counts
}

interface BoardViewProps {
  /**
   * The task set to render — already filtered by the screen (WorkspaceTasksTab)
   * to whatever plan/owner/tag filters are active (ADR-051 D2/D6). BoardView
   * itself no longer knows about plans-as-filter or the active tag filter; it
   * just lays out whatever tasks it's handed.
   */
  tasks: Task[]
  /** Plans in this workspace — used only to look up a task's owning plan for
   * TaskCard's goal-loop "paused" suffix (`Task.plan_id` → `Plan.paused_reason`). */
  plans: Plan[]
  agents?: Agent[]
  altitude: BoardAltitude
  onTaskClick: (task: Task) => void
  /** Persist a drag-to-column status change. Required for kanban DnD. */
  onTaskMove?: (task: Task, newStatus: TaskStatus) => void
  /** Surface a rejected drop (e.g. dropping into `blocked`). */
  onMoveRejected?: (reason: string) => void
}

/**
 * Plain, tasks-only kanban (ADR-051 D4 — replaces the Hierarchical
 * Drill-Down board). Plans no longer render as cards on the board and there
 * is no drill-down level: plans live only in the PlansFilterBand above this
 * component, which filters the task set BoardView receives. This is one flat
 * board — a sticky status header + the STATUS_ORDER columns as full-height
 * vertical lists of TaskCards, with restored vertical column dividers.
 * Horizontal status DnD (dnd-kit) moves a task between columns.
 */
export function BoardView({
  tasks,
  plans,
  agents = [],
  altitude,
  onTaskClick,
  onTaskMove,
  onMoveRejected,
}: BoardViewProps) {
  // Filter out non-user-surface tasks (e.g. heartbeat tasks are hidden from general views).
  const userTasks = tasks.filter((t) => t.surface === 'user' || t.surface === undefined)
  // Board shows only top-level tasks — subtasks (parent_task_id present) are
  // NEVER rendered as standalone cards. They nest under their parent (altitude 'show-all').
  const rootTasks = useMemo(() => userTasks.filter((t) => !t.parent_task_id), [userTasks])

  const counts = useMemo(() => countByStatus(rootTasks), [rootTasks])

  return (
    <div className="flex flex-col flex-1 min-h-0 overflow-hidden">
      <div className="flex-1 min-h-0 overflow-auto">
        <div className="min-w-max flex flex-col min-h-full">
          <StatusHeaderRow counts={counts} />
          <LaneStatusRow
            tasks={rootTasks}
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

// ── Sticky status header row ────────────────────────────────────────────────

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

// ── One row of N status cells (single DndContext) ──────────────────────────

interface LaneStatusRowProps {
  tasks: Task[]
  plans: Plan[]
  agents: Agent[]
  altitude: BoardAltitude
  onTaskClick: (task: Task) => void
  onTaskMove?: (task: Task, newStatus: TaskStatus) => void
  onMoveRejected?: (reason: string) => void
}

/**
 * One full-height row of status cells (one per STATUS_ORDER entry), each
 * independently droppable — horizontal status DnD scoped to the given tasks
 * via its own `DndContext`.
 */
function LaneStatusRow({
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
  config: ColumnConfig
  tasks: Task[]
  plans: Plan[]
  agents: Agent[]
  altitude: BoardAltitude
  activeTask: Task | null
  onTaskClick: (task: Task) => void
}

function LaneStatusCell({
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
      aria-label={`${config.label} column`}
      className={cn(
        // Vertical column dividers, restored (ADR-051 D4) — a subtle hairline
        // between columns so the board reads as a real grid, not a loose row
        // of stacks. Full-height via flex-1 stretch against the row's height.
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
