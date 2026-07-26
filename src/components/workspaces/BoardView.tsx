import { useEffect, useMemo, useState } from 'react'
import {
  DndContext,
  PointerSensor,
  KeyboardSensor,
  KeyboardCode,
  closestCenter,
  useSensor,
  useSensors,
  useDraggable,
  useDroppable,
  DragOverlay,
  type Announcements,
  type ScreenReaderInstructions,
  type DragStartEvent,
  type DragEndEvent,
  type KeyboardCoordinateGetter,
} from '@dnd-kit/core'
import { Info } from '@phosphor-icons/react'
import { TaskCard } from './TaskCard'
import { STATUS_COLORS, STATUS_LABELS, STATUS_ORDER } from '@/lib/statusColors'
import { taskMoveErrorMessage } from '@/lib/api'
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

/** The canonical status vocabulary, for spotting tasks whose `status` isn't
 * one of the STATUS_ORDER columns (e.g. a `planning` task left over from a
 * partial migration) — those tasks match no column and must not be dropped
 * silently. See the orphan-task banner in `BoardView`. */
const STATUS_SET = new Set<TaskStatus>(STATUS_ORDER)

/** Column order as rendered left-to-right (DOM/visual order matches
 * STATUS_ORDER — COLUMNS is built straight off it with no reordering), used
 * by `boardKeyboardCoordinateGetter` to resolve "next"/"previous" column. */
const COLUMN_STATUSES: TaskStatus[] = COLUMNS.map((c) => c.status)

/**
 * UAT Finding 1 fix: dnd-kit's DEFAULT keyboard coordinate getter
 * (`defaultKeyboardCoordinateGetter`) nudges the virtual drag position by a
 * fixed 25px per arrow-key press. A status column here is >=162px wide, so
 * even two presses (50px) never left the origin column's measured rect —
 * combined with the DndContext default `rectIntersection` collision
 * detection, `over` kept resolving back to the column the drag started in,
 * so "arrow keys move the card between columns" (the announced
 * screen-reader instruction) was a no-op: the drop always landed back on the
 * origin column with zero network calls.
 *
 * This getter instead teleports the drag rect directly onto the CENTER of
 * the adjacent column (by `COLUMN_STATUSES` order, i.e. visual left-to-right
 * order — the board is a single row, so ArrowRight/ArrowDown both mean "next
 * column" and ArrowLeft/ArrowUp both mean "previous column"), so one key
 * press reliably moves exactly one column and stops at the first/last
 * column rather than drifting by a few pixels. Paired with `closestCenter`
 * (passed as the DndContext's `collisionDetection` below, replacing the
 * default `rectIntersection`) so the teleported position resolves
 * predictably to the target column.
 */
const ARROW_COLUMN_STEP: Partial<Record<string, 1 | -1>> = {
  [KeyboardCode.Right]: 1,
  [KeyboardCode.Down]: 1,
  [KeyboardCode.Left]: -1,
  [KeyboardCode.Up]: -1,
}

export const boardKeyboardCoordinateGetter: KeyboardCoordinateGetter = (event, { context, currentCoordinates }) => {
  const step = ARROW_COLUMN_STEP[event.code]
  if (step === undefined) return undefined

  const { collisionRect, droppableRects } = context
  if (!collisionRect) return undefined

  const rectCenterX = collisionRect.left + collisionRect.width / 2

  // Which column is the drag rect currently over? Prefer an exact match;
  // fall back to the nearest column center (covers the first keypress,
  // before any move, when the rect is still measured over its origin card
  // rather than sitting exactly within a column boundary).
  let currentIndex = COLUMN_STATUSES.findIndex((status) => {
    const rect = droppableRects.get(status)
    return !!rect && rectCenterX >= rect.left && rectCenterX <= rect.left + rect.width
  })
  if (currentIndex === -1) {
    let closestDistance = Infinity
    COLUMN_STATUSES.forEach((status, index) => {
      const rect = droppableRects.get(status)
      if (!rect) return
      const distance = Math.abs(rect.left + rect.width / 2 - rectCenterX)
      if (distance < closestDistance) {
        closestDistance = distance
        currentIndex = index
      }
    })
  }
  if (currentIndex === -1) return undefined

  const targetIndex = currentIndex + step
  if (targetIndex < 0 || targetIndex >= COLUMN_STATUSES.length) return undefined // stop at the edges, don't wrap

  const targetRect = droppableRects.get(COLUMN_STATUSES[targetIndex])
  if (!targetRect) return undefined

  event.preventDefault()
  return {
    x: targetRect.left + targetRect.width / 2 - collisionRect.width / 2,
    y: currentCoordinates.y,
  }
}

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
 *
 * UAT round-2 N2 fix: `onDragEnd` here fires SYNCHRONOUSLY the instant a drag
 * is released — before `StatusColumnsRow.handleDragEnd` has even checked
 * `canDropTransition`, let alone before the (async, network-round-trip)
 * `onTaskMove` mutation has resolved. It previously announced "was moved to
 * the X column" unconditionally whenever the drop landed over a column —
 * which is a claim of a completed, successful state change dnd-kit cannot
 * possibly know yet, and one that's straightforwardly FALSE for a rejected
 * drop (an illegal transition like dropping onto `blocked`, e.g., OR a 409
 * from the plan-state gate, e.g.) — the card visually snaps back but a
 * screen-reader user had already been told "moved". This function now only
 * ever describes the MECHANICAL drop event (neutral, always true at this
 * instant) — the actual outcome ("was moved" / "could not be moved: <reason>")
 * is announced separately, once it's actually known, via the dedicated
 * `role="status"` region `StatusColumnsRow` renders and updates from
 * `handleDragEnd` (covering BOTH the synchronous transition-guard rejection
 * and the asynchronous mutation result) — see that function's own comment.
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
      // Deliberately NOT "was moved" — the drop is only mechanical fact at
      // this instant; the real outcome is announced separately, once known
      // (see this function's doc comment).
      return label
        ? `Task "${taskTitle(active.id)}" was dropped on the ${label} column. Confirming…`
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
  /** Whether a plan/owner/tag filter is currently narrowing `tasks` — drives
   * the empty-state copy ("no tasks match the filter" vs "no tasks yet"). */
  hasActiveFilter?: boolean
  onTaskClick: (task: Task) => void
  /**
   * Persist a drag-to-column status change. Required for kanban DnD.
   *
   * UAT round-2 N2: MAY return a Promise (WorkspaceTasksTab wires this to
   * `moveMutation.mutateAsync`, not the old fire-and-forget `.mutate`) — a
   * rejected promise is what lets the board's own live region (see
   * `StatusColumnsRow.handleDragEnd`) announce the REAL outcome instead of
   * dnd-kit's drop-instant announcement claiming success it can't yet know.
   * A caller that still passes a fire-and-forget `void` function keeps
   * working exactly as before (awaiting a non-promise resolves immediately).
   */
  onTaskMove?: (task: Task, newStatus: TaskStatus) => Promise<unknown> | void
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
  hasActiveFilter = false,
  onTaskClick,
  onTaskMove,
  onMoveRejected,
}: BoardViewProps) {
  // Filter out non-user-surface tasks (e.g. heartbeat tasks are hidden from general views).
  const userTasks = tasks.filter((t) => t.surface === 'user' || t.surface === undefined)
  // Board shows only top-level tasks — subtasks (parent_task_id present) are
  // NEVER rendered as standalone cards. They nest under their parent (altitude 'show-all').
  const rootTasks = useMemo(() => userTasks.filter((t) => !t.parent_task_id), [userTasks])

  // Tasks whose status isn't one of the STATUS_ORDER columns — they match no
  // column in the grid below and would otherwise vanish with no signal (they
  // still count toward `tasks.length` everywhere else, e.g. List).
  const orphanTasks = useMemo(() => rootTasks.filter((t) => !STATUS_SET.has(t.status)), [rootTasks])

  useEffect(() => {
    if (import.meta.env.DEV && orphanTasks.length > 0) {
      console.warn(
        '[BoardView] tasks with a status outside STATUS_ORDER (hidden from every column):',
        orphanTasks.map((t) => [t.id, t.status]),
      )
    }
  }, [orphanTasks])

  const counts = useMemo(() => countByStatus(rootTasks), [rootTasks])

  return (
    <div className="flex flex-col flex-1 min-h-0 overflow-hidden">
      {orphanTasks.length > 0 && (
        <div
          role="status"
          className="flex items-center gap-1.5 border-b border-[var(--color-border)]/15 bg-[var(--color-surface-0)] px-3 py-1 text-[10px] text-[var(--color-warning)] flex-shrink-0"
        >
          <Info size={11} weight="fill" className="shrink-0" />
          {orphanTasks.length} task{orphanTasks.length === 1 ? '' : 's'} with an unrecognized status{' '}
          {orphanTasks.length === 1 ? "isn't" : "aren't"} shown in any column.
        </div>
      )}
      {/* UAT Finding 3 fix: this outer container used to be `overflow-auto`
          (scrolling BOTH axes) wrapping a `min-h-full` (floor-only) inner
          div — that made it ONE shared vertical scroller for the whole
          board, so the tallest lane (e.g. Inbox) set the scrollHeight for
          every column and scrolling past a short lane's content just showed
          blank space under its still-visible header/count. Now this level
          only ever scrolls HORIZONTALLY (for narrow viewports/many
          columns); vertical scrolling is delegated to each column
          individually (see StatusColumn's own `overflow-y-auto` below), so
          the header row and each lane's own count stay meaningful no matter
          how far any single lane is scrolled, and two lanes' cards can be
          visible at once. */}
      <div className="relative flex-1 min-h-0 overflow-x-auto overflow-y-hidden">
        <div className="min-w-max flex flex-col h-full">
          <StatusHeaderRow counts={counts} />
          <StatusColumnsRow
            tasks={rootTasks}
            plans={plans}
            agents={agents}
            altitude={altitude}
            onTaskClick={onTaskClick}
            onTaskMove={onTaskMove}
            onMoveRejected={onMoveRejected}
          />
        </div>
        {/* The column grid (drop targets) stays mounted even when empty; this
            centered message just makes a legitimately-empty board read as
            empty rather than as a load failure. */}
        {rootTasks.length === 0 && <BoardEmptyState filtered={hasActiveFilter} />}
      </div>
    </div>
  )
}

/** Centered, subtle empty-state — a legitimately-empty (filtered or
 * genuinely task-free) board must never read as a load failure. */
function BoardEmptyState({ filtered }: { filtered: boolean }) {
  return (
    <div className="pointer-events-none absolute inset-0 flex items-center justify-center p-8">
      <p className="text-sm text-[var(--color-muted)]">
        {filtered ? 'No tasks match the current filter.' : 'No tasks yet.'}
      </p>
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

interface StatusColumnsRowProps {
  tasks: Task[]
  plans: Plan[]
  agents: Agent[]
  altitude: BoardAltitude
  onTaskClick: (task: Task) => void
  onTaskMove?: (task: Task, newStatus: TaskStatus) => Promise<unknown> | void
  onMoveRejected?: (reason: string) => void
}

/**
 * One full-height row of status columns (one per STATUS_ORDER entry), each
 * independently droppable — horizontal status DnD scoped to the given tasks
 * via its own `DndContext`.
 */
function StatusColumnsRow({
  tasks,
  plans,
  agents,
  altitude,
  onTaskClick,
  onTaskMove,
  onMoveRejected,
}: StatusColumnsRowProps) {
  const [activeTask, setActiveTask] = useState<Task | null>(null)
  // UAT round-2 N2 fix: the outcome live region. This is the ONLY signal a
  // screen-reader user gets about whether a drop actually succeeded — the
  // card visually snaps back to its origin column on refusal, so sight gives
  // no other cue. Updated exactly once per drop, only once the outcome is
  // actually known: synchronously for a `canDropTransition` guard refusal,
  // or once the (possibly async) `onTaskMove` promise settles. Never set
  // optimistically at drag-end — that was the bug (see
  // `buildBoardAnnouncements`'s doc comment for the other half of this fix,
  // which stopped dnd-kit's OWN drop announcement from claiming "moved").
  const [moveOutcome, setMoveOutcome] = useState('')

  const sensors = useSensors(
    // A small activation distance lets a plain click still open the detail panel
    // (the card's onClick) without being swallowed by the drag.
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    // Restrict the lift/drop key to Space only (dnd-kit's default is Space
    // AND Enter) so Enter stays free for TaskCard's "open task" action.
    // `coordinateGetter` is `boardKeyboardCoordinateGetter` (Finding 1 fix,
    // see above) — WITHOUT it, dnd-kit's 25px-per-press default never
    // crosses a column boundary and arrow keys become a no-op.
    useSensor(KeyboardSensor, {
      keyboardCodes: {
        start: [KeyboardCode.Space],
        cancel: [KeyboardCode.Esc],
        end: [KeyboardCode.Space],
      },
      coordinateGetter: boardKeyboardCoordinateGetter,
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

    const targetLabel = COLUMNS.find((c) => c.status === targetStatus)?.label ?? targetStatus
    const originLabel = COLUMNS.find((c) => c.status === dragged.status)?.label ?? dragged.status

    const verdict = canDropTransition(dragged.status, targetStatus)
    if (!verdict.ok) {
      if (verdict.reason) onMoveRejected?.(verdict.reason)
      // Known synchronously — no network round trip needed to report this.
      setMoveOutcome(
        `Task "${dragged.title}" could not be moved to the ${targetLabel} column: ${verdict.reason ?? 'that move is not allowed'} It remains in the ${originLabel} column.`,
      )
      return
    }

    // `onTaskMove` may return a Promise (WorkspaceTasksTab wires this to
    // `moveMutation.mutateAsync`) — dnd-kit's own `onDragEnd` can't be async,
    // but this component can still track the eventual result to drive the
    // live region. `taskMoveErrorMessage` is the SAME mapper the toast uses
    // (WorkspaceTasksTab's moveMutation.onError), so a sighted user's toast
    // and a screen-reader user's announcement always say the identical thing
    // about the same rejected drop.
    void Promise.resolve(onTaskMove?.(dragged, targetStatus)).then(
      () => {
        setMoveOutcome(`Task "${dragged.title}" was moved to the ${targetLabel} column.`)
      },
      (err: unknown) => {
        const reason = taskMoveErrorMessage(err, plans)
        setMoveOutcome(
          `Task "${dragged.title}" could not be moved to the ${targetLabel} column: ${reason} It remains in the ${originLabel} column.`,
        )
      },
    )
  }

  return (
    <>
      <DndContext
        sensors={sensors}
        // closestCenter (rather than the DndContext default of
        // rectIntersection) is dnd-kit's own recommended pairing for the
        // keyboard sensor — it resolves the teleported drag rect from
        // `boardKeyboardCoordinateGetter` to the target column predictably,
        // without requiring pixel-perfect rect overlap.
        collisionDetection={closestCenter}
        accessibility={{
          screenReaderInstructions: BOARD_SCREEN_READER_INSTRUCTIONS,
          announcements,
        }}
        onDragStart={handleDragStart}
        onDragEnd={handleDragEnd}
        onDragCancel={() => setActiveTask(null)}
      >
        {/* `min-h-0` lets this row take exactly the wrapper's remaining
            height (after the header) rather than growing with content —
            required for each StatusColumn's own `overflow-y-auto` below to
            actually bound and scroll (Finding 3). */}
        <div className="flex flex-1 min-h-0">
          {COLUMNS.map((col) => (
            <StatusColumn
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
                // Never render the ADR-052 action button on the purely-visual
                // drag ghost — it's aria-hidden and unreachable anyway; an
                // interactive-looking control following the cursor mid-drag
                // would just be visual noise.
                showActions={false}
              />
            </div>
          ) : null}
        </DragOverlay>
      </DndContext>
      {/*
        UAT round-2 N2 — the confirmed drag OUTCOME live region. Deliberately
        a SEPARATE DOM node from dnd-kit's own internal accessibility live
        region (the one `buildBoardAnnouncements` feeds, which only narrates
        drag MECHANICS — pick up / hover / drop / cancel — and never claims
        an outcome it can't know yet). This one is updated exactly once per
        drop, only once `handleDragEnd` actually knows whether the move
        succeeded, failed the synchronous transition guard, or was rejected
        by the server — so a screen-reader user, who gets NO other signal
        when a refused drop's card visually snaps back, always hears the
        real result and never a false "moved" followed by a contradiction.
        `sr-only` (visually hidden, still in the accessibility tree).
      */}
      <div role="status" aria-live="polite" data-testid="board-move-outcome" className="sr-only">
        {moveOutcome}
      </div>
    </>
  )
}

interface StatusColumnProps {
  config: ColumnConfig
  tasks: Task[]
  plans: Plan[]
  agents: Agent[]
  altitude: BoardAltitude
  activeTask: Task | null
  onTaskClick: (task: Task) => void
}

function StatusColumn({
  config,
  tasks,
  plans,
  agents,
  altitude,
  activeTask,
  onTaskClick,
}: StatusColumnProps) {
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
        // `min-h-0 overflow-y-auto overscroll-contain` (Finding 3): each lane
        // gets its OWN vertical scroller bounded by the row's stretched
        // height, instead of all six columns sharing one scroller sized to
        // the tallest lane — a short lane's content (and its own header
        // count above) stays visible/meaningful regardless of how far a
        // neighboring lane has been scrolled, and scrolling one lane never
        // bleeds into the page/board underneath (`overscroll-contain`).
        'flex flex-col flex-1 min-w-[162px] min-h-0 gap-2 p-2 overflow-y-auto overscroll-contain border-r border-[var(--color-border)]/25 last:border-r-0 transition-colors',
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
