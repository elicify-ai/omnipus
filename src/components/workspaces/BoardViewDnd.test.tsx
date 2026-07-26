/**
 * BoardViewDnd.test.tsx
 *
 * Tests for the kanban drag-and-drop status change on the Board view (UAT
 * "full task UX" — task #3). Covers:
 *   1. The transition guard `canDropTransition` mirrors the backend
 *      (pkg/task/store.go::validateTransition): dropping INTO `blocked` is
 *      prevented; dragging OUT of `done`/`blocked` is prevented; other moves
 *      are allowed.
 *   2. Cards render as draggable elements (dnd-kit attributes) and columns are
 *      registered droppables for every status in STATUS_ORDER.
 *   3. A simulated drag-end into a legal column calls onTaskMove with the new
 *      status; a drag-end into `blocked` calls onMoveRejected instead.
 *   4. UAT Finding 1 regression coverage: `boardKeyboardCoordinateGetter`'s
 *      column-teleport math (pure), AND a full keyboard-driven Space → Arrow
 *      → Space interaction through a REALLY-mounted `BoardView` (real
 *      `DndContext`/sensors, not a mocked-out handler) that ends in a real
 *      `onTaskMove` call for the TARGET column — proving arrow keys actually
 *      move the drag target instead of landing back on the origin column.
 *
 * dnd-kit's POINTER DnD cannot be driven faithfully in jsdom (continuous
 * mouse-position math over un-rendered layout). KEYBOARD DnD is different —
 * it's a handful of discrete, real `keydown` events over the SAME rect-based
 * collision math the pointer path uses, so it's testable end-to-end once the
 * only piece jsdom can't produce on its own (real `getBoundingClientRect`
 * layout) is stubbed with fixed per-node rects (see `withMockedBoardRects`
 * below). The move-decision guard test below still exercises the drag-END
 * guard logic directly (unchanged from before) since THAT part never needed
 * real layout to verify.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BoardView, canDropTransition, buildBoardAnnouncements, boardKeyboardCoordinateGetter } from './BoardView'
import { ApiError } from '@/lib/api-error'
import type { Task, Plan } from '@/lib/api'
import { STATUS_ORDER, STATUS_LABELS } from '@/lib/statusColors'
import type {
  Active,
  Over,
  DragStartEvent,
  DragOverEvent,
  DragEndEvent,
  DragCancelEvent,
  SensorContext,
} from '@dnd-kit/core'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSubtasks: vi.fn().mockResolvedValue([]),
    tasksQueryKeys: {
      list: (p: unknown) => ['tasks', p],
      subtasks: (id: string) => ['tasks', id, 'subtasks'],
    },
  }
})

const baseTask = (overrides: Partial<Task> = {}): Task => ({
  id: 'task-1',
  title: 'Draggable task',
  status: 'inbox',
  action: 'llm',
  priority: 3,
  workspace_id: 'ws-1',
  surface: 'user',
  owner: 'admin',
  created_by: 'admin',
  created_at: '2026-06-20T10:00:00Z',
  updated_at: '2026-06-20T10:00:00Z',
  ...overrides,
})

const plans: Plan[] = []

// ADR-052 §6.8: every rendered TaskCard now embeds a TaskActionButton, which
// calls useMutation/useQueryClient — those hooks throw without a QueryClient
// in the tree.
function renderBoard(props: Partial<React.ComponentProps<typeof BoardView>> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <BoardView
        tasks={[baseTask()]}
        plans={plans}
        agents={[]}
        altitude="top-level"
        onTaskClick={vi.fn()}
        onTaskMove={vi.fn()}
        onMoveRejected={vi.fn()}
        {...props}
      />
    </QueryClientProvider>,
  )
}

// ── Guard logic (exhaustive) ───────────────────────────────────────────────────

describe('canDropTransition — backend-mirrored guard', () => {
  it('prevents dropping INTO blocked from every other status', () => {
    for (const from of STATUS_ORDER) {
      if (from === 'blocked') continue
      const v = canDropTransition(from, 'blocked')
      expect(v.ok).toBe(false)
      expect(v.reason).toMatch(/automatically/i)
    }
  })

  it('prevents dragging OUT of done (terminal)', () => {
    for (const to of STATUS_ORDER) {
      if (to === 'done') continue
      const v = canDropTransition('done', to)
      expect(v.ok).toBe(false)
      // `done → blocked` is rejected by the blocked guard (checked first); every
      // other `done → X` is rejected with the terminal-done message.
      if (to !== 'blocked') expect(v.reason).toMatch(/final|done/i)
    }
  })

  it('prevents dragging OUT of blocked (auto-managed)', () => {
    for (const to of STATUS_ORDER) {
      if (to === 'blocked') continue
      const v = canDropTransition('blocked', to)
      expect(v.ok).toBe(false)
    }
  })

  it('allows ordinary forward/backward moves between non-terminal statuses', () => {
    expect(canDropTransition('inbox', 'next').ok).toBe(true)
    expect(canDropTransition('next', 'in_progress').ok).toBe(true)
    expect(canDropTransition('in_progress', 'done').ok).toBe(true)
    expect(canDropTransition('inbox', 'failed').ok).toBe(true)
    expect(canDropTransition('failed', 'next').ok).toBe(true)
  })

  it('is a no-op (allowed) when from === to', () => {
    for (const s of STATUS_ORDER) {
      expect(canDropTransition(s, s).ok).toBe(true)
    }
  })
})

// ── Render: draggable cards + droppable columns ────────────────────────────────

describe('BoardView — DnD wiring', () => {
  it('renders the dragged card with a draggable role attribute', () => {
    renderBoard()
    // TaskCard's own root carries dnd-kit's attributes (overridden roleDescription
    // "draggable task") — the DraggableTaskCard wrapper in BoardView is a plain,
    // non-interactive measurement div (see the single-tab-stop test below).
    const card = screen.getByText('Draggable task').closest('[aria-roledescription="draggable task"]')
    expect(card).not.toBeNull()
  })

  it('renders exactly one focusable tab stop per card (WCAG 4.1.2 — no nested role="button")', () => {
    renderBoard()
    const card = screen.getByText('Draggable task').closest('[role="button"]')
    expect(card).not.toBeNull()
    // The `useDraggable` wrapper (this card's parent) must NOT also be a
    // role="button" — only TaskCard's own root should carry it, so there is
    // exactly one role="button" descendant under the wrapper.
    const wrapper = card?.parentElement
    expect(wrapper?.getAttribute('role')).not.toBe('button')
    expect(wrapper?.querySelectorAll('[role="button"]').length).toBe(1)
  })

  it('renders one droppable column per STATUS_ORDER entry', () => {
    renderBoard({ tasks: [] })
    // Columns carry aria-label "<Label> column" — one per status. NOT a
    // hardcoded count (ADR-051 D5 drops `planning` from STATUS_ORDER,
    // 7 → 6) — this just mirrors whatever STATUS_ORDER currently holds.
    const cols = screen.getAllByLabelText(/column$/i)
    expect(cols.length).toBe(STATUS_ORDER.length)
  })
})

// ── boardKeyboardCoordinateGetter — column-teleport math (UAT Finding 1) ────────
//
// Pure unit coverage of the actual keyboard coordinate getter dnd-kit invokes
// on every arrow-key press during a keyboard drag. Each status column is
// modelled as a 180px-wide rect laid out left-to-right in STATUS_ORDER (index
// * 200px), matching the board's real single-row layout — this is the exact
// shape `context.droppableRects` has at runtime, just with fixed numbers
// instead of real measured layout.

function columnRect(index: number) {
  const left = index * 200
  return { top: 0, left, right: left + 180, bottom: 300, width: 180, height: 300 }
}

function makeSensorContext(collisionLeft: number, collisionWidth = 150): SensorContext {
  const droppableRects = new Map(STATUS_ORDER.map((status, i) => [status, columnRect(i)]))
  return {
    collisionRect: { top: 10, left: collisionLeft, right: collisionLeft + collisionWidth, bottom: 90, width: collisionWidth, height: 80 },
    droppableRects,
  } as unknown as SensorContext
}

function pressArrow(code: string, collisionLeft: number, collisionWidth = 150) {
  const context = makeSensorContext(collisionLeft, collisionWidth)
  const preventDefault = vi.fn()
  const result = boardKeyboardCoordinateGetter(
    { code, preventDefault } as unknown as KeyboardEvent,
    { active: 'task-1', currentCoordinates: { x: collisionLeft, y: 10 }, context },
  )
  return { result, preventDefault }
}

describe('boardKeyboardCoordinateGetter — column-teleport coordinate math', () => {
  it('ArrowRight teleports the drag rect onto the center of the NEXT column (Inbox -> Next)', () => {
    // Rect centered at x=85, inside column 0 (Inbox: 0..180).
    const { result, preventDefault } = pressArrow('ArrowRight', 10)
    expect(preventDefault).toHaveBeenCalled()
    expect(result).toBeDefined()
    // Column 1 (Next) spans 200..380, center 290; rect width 150 -> left = 290 - 75 = 215.
    expect(result!.x).toBeCloseTo(215)
    expect(result!.y).toBe(10)
  })

  it('ArrowDown moves the same direction as ArrowRight (next column) — the board is a single row', () => {
    const right = pressArrow('ArrowRight', 10)
    const down = pressArrow('ArrowDown', 10)
    expect(down.result).toEqual(right.result)
  })

  it('ArrowLeft teleports onto the PREVIOUS column (Next -> Inbox)', () => {
    // Rect centered at x=290, inside column 1 (Next: 200..380).
    const { result } = pressArrow('ArrowLeft', 215)
    // Column 0 (Inbox) spans 0..180, center 90; left = 90 - 75 = 15.
    expect(result!.x).toBeCloseTo(15)
  })

  it('ArrowUp moves the same direction as ArrowLeft (previous column)', () => {
    const left = pressArrow('ArrowLeft', 215)
    const up = pressArrow('ArrowUp', 215)
    expect(up.result).toEqual(left.result)
  })

  it('stops at the LAST column — ArrowRight past Failed (index 5) returns undefined, not a wrap-around', () => {
    const lastColumnCenter = 5 * 200 + 90 // Failed: 1000..1180, center 1090
    const { result } = pressArrow('ArrowRight', lastColumnCenter - 75)
    expect(result).toBeUndefined()
  })

  it('stops at the FIRST column — ArrowLeft before Inbox (index 0) returns undefined, not a wrap-around', () => {
    const { result } = pressArrow('ArrowLeft', 10)
    expect(result).toBeUndefined()
  })

  it('ignores non-arrow keys (e.g. Tab) — returns undefined, no preventDefault', () => {
    const { result, preventDefault } = pressArrow('Tab', 10)
    expect(result).toBeUndefined()
    expect(preventDefault).not.toHaveBeenCalled()
  })

  it('returns undefined (no throw) when collisionRect is null — no active measured drag yet', () => {
    const context = { collisionRect: null, droppableRects: new Map() } as unknown as SensorContext
    const result = boardKeyboardCoordinateGetter(
      { code: 'ArrowRight', preventDefault: vi.fn() } as unknown as KeyboardEvent,
      { active: 'task-1', currentCoordinates: { x: 0, y: 0 }, context },
    )
    expect(result).toBeUndefined()
  })

  it('falls back to the NEAREST column center when the rect sits outside every column (still resolves a sane direction)', () => {
    // Center way past every column (5000) — nearest is the last column
    // (Failed, index 5), so ArrowRight (already "past" it) stops, while
    // ArrowLeft moves to the second-to-last column (Done, index 4).
    const right = pressArrow('ArrowRight', 5000)
    expect(right.result).toBeUndefined()
    const left = pressArrow('ArrowLeft', 5000)
    expect(left.result).toBeDefined()
    // Column 4 (Done) spans 800..980, center 890; rect width 150 -> left = 890-75 = 815.
    expect(left.result!.x).toBeCloseTo(815)
  })
})

// ── Move decision behaviour (the guard drives onTaskMove vs onMoveRejected) ─────

// ── Drag announcement message text (screen-reader live region) ─────────────────
//
// dnd-kit's DragStartEvent/DragOverEvent/DragEndEvent/DragCancelEvent carry a
// few fields buildBoardAnnouncements never reads (activatorEvent, collisions,
// delta) — these helpers fill them with inert placeholders purely to satisfy
// the type, so each test can focus on the one field that actually drives the
// message (`active.id` / `over?.id`).

const activatorEvent = new Event('pointerdown')

function makeActive(id: string): Active {
  return { id, data: { current: undefined }, rect: { current: { initial: null, translated: null } } } as unknown as Active
}

function makeOver(id: string): Over {
  return { id, rect: {}, disabled: false, data: { current: undefined } } as unknown as Over
}

function makeStartEvent(id: string): DragStartEvent {
  return { active: makeActive(id), activatorEvent }
}

function makeOverEvent(activeId: string, overId: string | null): DragOverEvent {
  return {
    active: makeActive(activeId),
    activatorEvent,
    collisions: null,
    delta: { x: 0, y: 0 },
    over: overId ? makeOver(overId) : null,
  }
}

function makeEndEvent(activeId: string, overId: string | null): DragEndEvent {
  return makeOverEvent(activeId, overId)
}

function makeCancelEvent(activeId: string): DragCancelEvent {
  return makeOverEvent(activeId, null)
}

describe('buildBoardAnnouncements — screen-reader message text', () => {
  const rootTasks: Task[] = [baseTask({ id: 'task-1', title: 'Draggable task' })]

  it('onDragStart announces the task title being picked up', () => {
    const announcements = buildBoardAnnouncements(rootTasks)
    const msg = announcements.onDragStart?.(makeStartEvent('task-1'))
    expect(msg).toBe('Picked up task "Draggable task".')
  })

  it('onDragStart falls back to "the task" for an unknown id', () => {
    const announcements = buildBoardAnnouncements(rootTasks)
    const msg = announcements.onDragStart?.(makeStartEvent('missing'))
    expect(msg).toBe('Picked up task "the task".')
  })

  it('onDragOver announces the column the card is currently over', () => {
    const announcements = buildBoardAnnouncements(rootTasks)
    const msg = announcements.onDragOver?.(makeOverEvent('task-1', 'in_progress'))
    expect(msg).toBe('Task "Draggable task" is over the In Progress column.')
  })

  it('onDragOver returns undefined when not over any droppable', () => {
    const announcements = buildBoardAnnouncements(rootTasks)
    const msg = announcements.onDragOver?.(makeOverEvent('task-1', null))
    expect(msg).toBeUndefined()
  })

  it('onDragEnd announces only the mechanical drop, never a completed "moved" claim (UAT round-2 N2 — the real outcome isn\'t known synchronously)', () => {
    const announcements = buildBoardAnnouncements(rootTasks)
    const msg = announcements.onDragEnd?.(makeEndEvent('task-1', 'done'))
    expect(msg).toBe('Task "Draggable task" was dropped on the Done column. Confirming…')
    expect(msg).not.toMatch(/was moved/i)
  })

  it('onDragEnd announces a plain "was dropped" when not released over a column', () => {
    const announcements = buildBoardAnnouncements(rootTasks)
    const msg = announcements.onDragEnd?.(makeEndEvent('task-1', null))
    expect(msg).toBe('Task "Draggable task" was dropped.')
  })

  it('onDragCancel announces the cancellation', () => {
    const announcements = buildBoardAnnouncements(rootTasks)
    const msg = announcements.onDragCancel?.(makeCancelEvent('task-1'))
    expect(msg).toBe('Moving task "Draggable task" was cancelled.')
  })
})

describe('BoardView — move decision', () => {
  it('a legal target status maps to onTaskMove, an illegal one to onMoveRejected', () => {
    // This mirrors the exact decision the drag-end handler makes.
    const onTaskMove = vi.fn()
    const onMoveRejected = vi.fn()

    function decide(from: Task['status'], to: Task['status']) {
      if (from === to) return
      const v = canDropTransition(from, to)
      if (!v.ok) {
        if (v.reason) onMoveRejected(v.reason)
        return
      }
      onTaskMove(to)
    }

    decide('inbox', 'in_progress')
    expect(onTaskMove).toHaveBeenCalledWith('in_progress')

    decide('next', 'blocked')
    expect(onMoveRejected).toHaveBeenCalledTimes(1)
    expect(onTaskMove).toHaveBeenCalledTimes(1) // unchanged — blocked rejected
  })
})

// ── Real keyboard drag-and-drop, end-to-end (UAT Finding 1) ─────────────────────
//
// Everything above proves the coordinate MATH is right in isolation. This
// drives the ACTUAL rendered BoardView through real `keydown` events, over
// the real `DndContext`/sensors/collision-detection wiring, and asserts the
// real `onTaskMove` callback — the thing WorkspaceTasksTab wires straight to
// its `updateTask` mutation (the `PATCH /tasks/{id}` call) — fires for the
// arrow-key TARGET column. Before the fix this test fails exactly the way
// the UAT report describes: the drop lands back on 'inbox' (the origin).
//
// jsdon has no real layout engine (every element's `getBoundingClientRect`
// reports all-zero without help), so `withMockedBoardRects` stubs it for one
// test so columns measure as a real left-to-right row (COLUMNS/STATUS_ORDER
// order) and the dragged card measures as sitting inside its origin column —
// exactly what a real browser's layout would produce.
async function withMockedBoardRects<T>(run: () => Promise<T> | T): Promise<T> {
  const original = HTMLElement.prototype.getBoundingClientRect
  HTMLElement.prototype.getBoundingClientRect = function (this: HTMLElement) {
    const label = this.getAttribute('aria-label')
    if (label) {
      const idx = STATUS_ORDER.findIndex((s) => `${STATUS_LABELS[s]} column` === label)
      if (idx !== -1) return columnRect(idx) as DOMRect
    }
    // dnd-kit measures TWO different nodes as "the dragged card" depending
    // on drag phase, and `collisionRect` follows whichever is active:
    //  1. Before/without a mounted overlay: the DRAGGABLE's own node
    //     (`useDraggable`'s `setNodeRef`, attached to BoardView's plain
    //     wrapper <div> around TaskCard — see DraggableTaskCard). That
    //     wrapper carries no attribute of its own; its immediate first
    //     child IS TaskCard's root (`aria-roledescription="draggable
    //     task"`) exactly once.
    //  2. Once `<DragOverlay>` mounts its ghost clone (which it does as
    //     soon as a drag starts here — BoardView always renders one), dnd-kit
    //     switches to measuring THAT node instead (`draggingNodeRect =
    //     dragOverlay.rect ?? activeNodeRect`) — the ghost's own `<div
    //     aria-hidden className="opacity-90 rotate-2 cursor-grabbing">`
    //     wrapper (see StatusColumnsRow's `<DragOverlay>` children).
    // Verified empirically: without case 2, this fell through to the
    // generic fallback below (whose 1024x768 center landed the "current
    // column" resolution on `in_progress` instead of `inbox`, moving the
    // ArrowRight target two columns further than intended).
    if (
      this.firstElementChild?.getAttribute('aria-roledescription') === 'draggable task' ||
      this.classList.contains('cursor-grabbing')
    ) {
      // Sits inside column 0 (Inbox) — matches the fixture task's status.
      return { top: 10, left: 10, right: 160, bottom: 90, width: 150, height: 80 } as DOMRect
    }
    // Generic fallback for every other measured node (wrappers, the scroll
    // container, etc.) — kept within jsdom's default 1024x768 viewport so
    // nothing dnd-kit measures reads as "off-screen".
    return { top: 0, left: 0, right: 1024, bottom: 768, width: 1024, height: 768 } as DOMRect
  }
  try {
    return await run()
  } finally {
    HTMLElement.prototype.getBoundingClientRect = original
  }
}

describe('BoardView — real keyboard drag-and-drop, end-to-end (UAT Finding 1)', () => {
  it('Space lifts, ArrowRight moves the target to Next, Space drops there — onTaskMove(task, "next"), never back on Inbox', async () => {
    await withMockedBoardRects(async () => {
      const onTaskMove = vi.fn()
      const onMoveRejected = vi.fn()
      const task = baseTask({ id: 'task-1', title: 'Draggable task', status: 'inbox' })
      renderBoard({ tasks: [task], onTaskMove, onMoveRejected })

      const card = screen.getByText('Draggable task').closest('[role="button"]') as HTMLElement
      expect(card).not.toBeNull()
      card.focus()
      expect(document.activeElement).toBe(card)

      // Lift (Space). dnd-kit's KeyboardSensor registers its persistent
      // document-level keydown listener via a `setTimeout(fn)` (0ms) inside
      // its `attach()` — flush that macrotask before firing the next key, or
      // the ArrowRight below arrives before the listener exists.
      fireEvent.keyDown(card, { code: 'Space', key: ' ' })
      await new Promise((resolve) => setTimeout(resolve, 0))

      // THE regression under test: move one column right, Inbox -> Next.
      fireEvent.keyDown(card, { code: 'ArrowRight', key: 'ArrowRight' })

      // Drop.
      fireEvent.keyDown(card, { code: 'Space', key: ' ' })

      expect(onMoveRejected).not.toHaveBeenCalled()
      expect(onTaskMove).toHaveBeenCalledTimes(1)
      const [movedTask, newStatus] = onTaskMove.mock.calls[0] as [Task, Task['status']]
      expect(movedTask.id).toBe('task-1')
      // Before the fix, this resolved back to 'inbox' — the origin column —
      // instead of following the arrow key. 'next' is the regression guard.
      expect(newStatus).toBe('next')
    })
  })

  it('ArrowDown behaves the same as ArrowRight (single-row board) — also lands on Next', async () => {
    await withMockedBoardRects(async () => {
      const onTaskMove = vi.fn()
      const task = baseTask({ id: 'task-1', title: 'Draggable task', status: 'inbox' })
      renderBoard({ tasks: [task], onTaskMove })

      const card = screen.getByText('Draggable task').closest('[role="button"]') as HTMLElement
      card.focus()

      fireEvent.keyDown(card, { code: 'Space', key: ' ' })
      await new Promise((resolve) => setTimeout(resolve, 0))
      fireEvent.keyDown(card, { code: 'ArrowDown', key: 'ArrowDown' })
      fireEvent.keyDown(card, { code: 'Space', key: ' ' })

      expect(onTaskMove).toHaveBeenCalledTimes(1)
      const [, newStatus] = onTaskMove.mock.calls[0] as [Task, Task['status']]
      expect(newStatus).toBe('next')
    })
  })

  it('Escape cancels the lift — no onTaskMove, task stays put', async () => {
    await withMockedBoardRects(async () => {
      const onTaskMove = vi.fn()
      const task = baseTask({ id: 'task-1', title: 'Draggable task', status: 'inbox' })
      renderBoard({ tasks: [task], onTaskMove })

      const card = screen.getByText('Draggable task').closest('[role="button"]') as HTMLElement
      card.focus()

      fireEvent.keyDown(card, { code: 'Space', key: ' ' })
      await new Promise((resolve) => setTimeout(resolve, 0))
      fireEvent.keyDown(card, { code: 'ArrowRight', key: 'ArrowRight' })
      fireEvent.keyDown(card, { code: 'Escape', key: 'Escape' })

      expect(onTaskMove).not.toHaveBeenCalled()
    })
  })
})

// ── Outcome live region — UAT round-2 N2 ────────────────────────────────────
//
// The `role="status"` region dnd-kit's own `Announcements` populates
// (`buildBoardAnnouncements`) narrates drag MECHANICS ONLY (covered above —
// it never claims "moved"). These tests cover the SEPARATE, board-owned
// `data-testid="board-move-outcome"` region that announces the REAL result —
// exactly once, only once it's actually known — driving the fix for: "a
// screen-reader user is told the move succeeded when it was rejected and the
// card snapped back."
async function dragOneColumnRight(card: HTMLElement) {
  fireEvent.keyDown(card, { code: 'Space', key: ' ' })
  await new Promise((resolve) => setTimeout(resolve, 0))
  fireEvent.keyDown(card, { code: 'ArrowRight', key: 'ArrowRight' })
  fireEvent.keyDown(card, { code: 'Space', key: ' ' })
}

async function dragColumnsRight(card: HTMLElement, times: number) {
  fireEvent.keyDown(card, { code: 'Space', key: ' ' })
  await new Promise((resolve) => setTimeout(resolve, 0))
  for (let i = 0; i < times; i++) {
    fireEvent.keyDown(card, { code: 'ArrowRight', key: 'ArrowRight' })
  }
  fireEvent.keyDown(card, { code: 'Space', key: ' ' })
}

describe('BoardView — outcome live region (UAT round-2 N2: never announce a moved that did not happen)', () => {
  it('does NOT announce "moved" until the async onTaskMove promise actually resolves', async () => {
    await withMockedBoardRects(async () => {
      let resolveMove!: () => void
      const pending = new Promise<void>((resolve) => {
        resolveMove = resolve
      })
      const onTaskMove = vi.fn().mockReturnValue(pending)
      const task = baseTask({ id: 'task-1', title: 'Draggable task', status: 'inbox' })
      renderBoard({ tasks: [task], onTaskMove })

      const card = screen.getByText('Draggable task').closest('[role="button"]') as HTMLElement
      card.focus()
      await dragOneColumnRight(card)

      expect(onTaskMove).toHaveBeenCalledTimes(1)
      // The mutation hasn't settled yet — the outcome region must NOT
      // already claim success (this is the exact bug: announcing "moved"
      // before the result is known).
      const region = screen.getByTestId('board-move-outcome')
      expect(region).not.toHaveTextContent(/moved/i)

      resolveMove()
      await waitFor(() => expect(region).toHaveTextContent('Task "Draggable task" was moved to the Next column.'))
    })
  })

  it('announces the REAL rejection reason (never "moved") when the async onTaskMove promise rejects — same wording a sighted user would see toasted', async () => {
    await withMockedBoardRects(async () => {
      const conflict = new ApiError(409, 'This conflicts with the current state. Please refresh and try again.', {
        body: JSON.stringify({
          error:
            'task_executor: parent plan is not in a dispatchable state (approved/running, unpaused): plan "plan-9" is draft (paused_reason="")',
        }),
      })
      const onTaskMove = vi.fn().mockRejectedValue(conflict)
      const task = baseTask({ id: 'task-1', title: 'Draggable task', status: 'inbox', plan_id: 'plan-9' })
      renderBoard({ tasks: [task], onTaskMove })

      const card = screen.getByText('Draggable task').closest('[role="button"]') as HTMLElement
      card.focus()
      await dragOneColumnRight(card)

      const region = screen.getByTestId('board-move-outcome')
      // Never claims success at any point on the way to the real (failure) outcome.
      expect(region).not.toHaveTextContent(/was moved/i)

      await waitFor(() =>
        expect(region).toHaveTextContent(
          'Task "Draggable task" could not be moved to the Next column: This plan is still a draft — Execute it (from the Plans band above) before this task can run. It remains in the Inbox column.',
        ),
      )
      // Still never flipped to a false "moved" claim after settling.
      expect(region).not.toHaveTextContent(/was moved/i)
    })
  })

  it('announces a SYNCHRONOUS transition-guard rejection (e.g. dropping into Blocked) immediately, with no onTaskMove call at all', async () => {
    await withMockedBoardRects(async () => {
      const onTaskMove = vi.fn()
      const onMoveRejected = vi.fn()
      const task = baseTask({ id: 'task-1', title: 'Draggable task', status: 'inbox' })
      renderBoard({ tasks: [task], onTaskMove, onMoveRejected })

      const card = screen.getByText('Draggable task').closest('[role="button"]') as HTMLElement
      card.focus()
      // inbox(0) -> next(1) -> in_progress(2) -> blocked(3): 3 ArrowRights.
      await dragColumnsRight(card, 3)

      expect(onTaskMove).not.toHaveBeenCalled()
      expect(onMoveRejected).toHaveBeenCalledTimes(1)
      // Known synchronously — no waitFor/network round trip needed.
      const region = screen.getByTestId('board-move-outcome')
      expect(region).toHaveTextContent(
        'Task "Draggable task" could not be moved to the Blocked column: Blocked is set automatically when a dependency is unmet — you can’t move a task here. It remains in the Inbox column.',
      )
      expect(region).not.toHaveTextContent(/was moved/i)
    })
  })
})
