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
import { Plus } from '@phosphor-icons/react'
import { TaskCard } from './TaskCard'
import { AltitudeToggle } from './AltitudeToggle'
import { STATUS_COLORS, STATUS_LABELS, STATUS_ORDER } from '@/lib/statusColors'
import type { Task, Agent, Milestone } from '@/lib/api'
import type { BoardAltitude } from '@/store/workspacesStore'
import { MILESTONE_FILTER_UNSCHEDULED } from './MilestoneFilterPills'
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

interface BoardViewProps {
  tasks: Task[]
  milestones: Milestone[]
  agents?: Agent[]
  activeMilestoneId: string | null
  altitude: BoardAltitude
  onAltitudeChange: (next: BoardAltitude) => void
  onTaskClick: (task: Task) => void
  onNewTask: (status?: TaskStatus) => void
  /** Persist a drag-to-column status change. Required for kanban DnD. */
  onTaskMove?: (task: Task, newStatus: TaskStatus) => void
  /** Surface a rejected drop (e.g. dropping into `blocked`). */
  onMoveRejected?: (reason: string) => void
}

export function BoardView({
  tasks,
  milestones,
  agents = [],
  activeMilestoneId,
  altitude,
  onAltitudeChange,
  onTaskClick,
  onNewTask,
  onTaskMove,
  onMoveRejected,
}: BoardViewProps) {
  // Filter out non-user-surface tasks (e.g. heartbeat tasks are hidden from general views)
  const userTasks = tasks.filter((t) => t.surface === 'user' || t.surface === undefined)
  // Apply milestone filter
  const filteredTasks = filterByMilestone(userTasks, activeMilestoneId)
  // Board shows only top-level tasks — subtasks (parent_task_id present) are
  // NEVER rendered as standalone cards. They nest under their parent.
  const rootTasks = filteredTasks.filter((t) => !t.parent_task_id)

  const [activeTask, setActiveTask] = useState<Task | null>(null)

  const sensors = useSensors(
    // A small activation distance lets a plain click still open the detail panel
    // (the card's onClick) without being swallowed by the drag.
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    // Restrict the lift/drop key to Space only (dnd-kit's default is Space
    // AND Enter) so Enter stays free for TaskCard's "open task" action —
    // otherwise a focused card's Enter key would be ambiguous between
    // opening the task and starting a keyboard drag.
    useSensor(KeyboardSensor, {
      keyboardCodes: {
        start: [KeyboardCode.Space],
        cancel: [KeyboardCode.Esc],
        end: [KeyboardCode.Space],
      },
    }),
  )

  // Live-region announcements for the drag lifecycle, read by screen readers
  // as a card is picked up, moved over a column, dropped, or cancelled.
  const announcements = useMemo<Announcements>(() => buildBoardAnnouncements(rootTasks), [rootTasks])

  function handleDragStart(event: DragStartEvent) {
    const id = String(event.active.id)
    setActiveTask(rootTasks.find((t) => t.id === id) ?? null)
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
    <div className="flex flex-col flex-1 min-h-0 overflow-hidden">
      {/* Board toolbar: altitude toggle */}
      <div className="flex items-center justify-end px-4 py-1.5 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] flex-shrink-0">
        <AltitudeToggle value={altitude} onChange={onAltitudeChange} />
      </div>

      {/* Columns */}
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
        <div className="flex gap-2 p-4 overflow-x-auto min-h-0 flex-1">
          {COLUMNS.map((col) => (
            <BoardColumn
              key={col.status}
              config={col}
              tasks={rootTasks.filter((t) => t.status === col.status)}
              milestones={milestones}
              agents={agents}
              altitude={altitude}
              activeTask={activeTask}
              onTaskClick={onTaskClick}
              onNewTask={() => onNewTask(col.status)}
            />
          ))}
        </div>
        <DragOverlay dropAnimation={null}>
          {activeTask ? (
            <div className="opacity-90 rotate-2 cursor-grabbing">
              <TaskCard
                task={activeTask}
                milestones={milestones}
                agents={agents}
                altitude="top-level"
                onClick={() => {}}
              />
            </div>
          ) : null}
        </DragOverlay>
      </DndContext>
    </div>
  )
}

function filterByMilestone(tasks: Task[], activeMilestoneId: string | null): Task[] {
  if (activeMilestoneId === null) return tasks
  if (activeMilestoneId === MILESTONE_FILTER_UNSCHEDULED) {
    return tasks.filter((t) => !t.milestone_id)
  }
  return tasks.filter((t) => t.milestone_id === activeMilestoneId)
}

interface BoardColumnProps {
  config: ColumnConfig
  tasks: Task[]
  milestones: Milestone[]
  agents: Agent[]
  altitude: BoardAltitude
  activeTask: Task | null
  onTaskClick: (task: Task) => void
  onNewTask: () => void
}

function BoardColumn({
  config,
  tasks,
  milestones,
  agents,
  altitude,
  activeTask,
  onTaskClick,
  onNewTask,
}: BoardColumnProps) {
  const { setNodeRef, isOver } = useDroppable({ id: config.status })

  // Visual feedback: highlight a column the dragged card can legally land in.
  const canAccept = activeTask
    ? canDropTransition(activeTask.status, config.status).ok
    : true

  return (
    <div
      ref={setNodeRef}
      className={cn(
        'flex flex-col min-w-[180px] flex-1 bg-[var(--color-surface-1)] rounded-xl border border-[var(--color-border)] max-h-full transition-colors',
        isOver && canAccept && 'border-[var(--color-accent)] ring-1 ring-[var(--color-accent)]/40',
        isOver && !canAccept && 'border-[var(--color-error)]/50',
      )}
      aria-label={`${config.label} column`}
    >
      {/* Column header */}
      <div className="flex items-center justify-between gap-2 px-3 py-2.5 border-b border-[var(--color-border)] flex-shrink-0">
        <div className="flex items-center gap-2">
          <span className="text-sm font-semibold" style={{ color: config.headerColor }}>
            {config.label}
          </span>
          <span className="rounded-full bg-[var(--color-surface-2)] px-1.5 py-0.5 text-[10px] font-semibold text-[var(--color-muted)]">
            {tasks.length}
          </span>
        </div>
        <button
          type="button"
          onClick={onNewTask}
          aria-label={`New ${config.label} task`}
          className="p-1 rounded text-[var(--color-muted)] hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-2)] transition-colors"
        >
          <Plus size={12} />
        </button>
      </div>

      {/* Task cards */}
      <div className="flex flex-col gap-2 p-2 flex-1 overflow-y-auto">
        {tasks.length === 0 ? (
          <button
            type="button"
            onClick={onNewTask}
            className="mx-auto mt-3 flex items-center gap-1 text-xs text-[var(--color-muted)] hover:text-[var(--color-accent)] transition-colors"
          >
            <Plus size={11} />
            Add your first task
          </button>
        ) : (
          tasks.map((task) => (
            <DraggableTaskCard
              key={task.id}
              task={task}
              milestones={milestones}
              agents={agents}
              altitude={altitude}
              onClick={() => onTaskClick(task)}
              onChildClick={onTaskClick}
            />
          ))
        )}
      </div>
    </div>
  )
}

interface DraggableTaskCardProps {
  task: Task
  milestones: Milestone[]
  agents: Agent[]
  altitude: BoardAltitude
  onClick: () => void
  onChildClick: (child: Task) => void
}

function DraggableTaskCard({
  task,
  milestones,
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
        milestones={milestones}
        agents={agents}
        altitude={altitude}
        onClick={onClick}
        onChildClick={onChildClick}
        drag={{ attributes, listeners, activatorRef: setActivatorNodeRef }}
      />
    </div>
  )
}
