/**
 * TaskCard.test.tsx
 *
 * Keyboard-activation coverage for TaskCard (WCAG 2.1.1 / 4.1.2), split by
 * whether the card is wired for dnd-kit dragging (`drag` prop present) or not:
 *
 *   - Draggable (BoardView usage): Space is reserved for dnd-kit's keyboard
 *     lift — only Enter opens the task.
 *   - Non-draggable (e.g. ExecutionView's read-only columns, which pass no
 *     `drag` prop at all): there is no keyboard-drag to reserve Space for, so
 *     a role="button" ignoring Space would break WCAG 4.1.2 — both Enter and
 *     Space must open the task.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskCard, type TaskCardDrag } from './TaskCard'
import type { Task } from '@/lib/api'
import type { DraggableAttributes } from '@dnd-kit/core'

// Only used by the "nested subtask bubbling" describe block below (altitude
// 'show-all' mounts TaskChildren, which fetches via fetchSubtasks). Other
// tests in this file never render children (default altitude 'top-level'),
// so this mock is inert for them.
const fetchSubtasksMock = vi.fn()

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSubtasks: (id: string) => fetchSubtasksMock(id),
    tasksQueryKeys: {
      ...actual.tasksQueryKeys,
      subtasks: (id: string) => ['tasks', id, 'subtasks'],
    },
  }
})

const baseTask = (overrides: Partial<Task> = {}): Task => ({
  id: 'task-1',
  title: 'Sample task',
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

/** A minimal but realistic `drag` prop, as BoardView's DraggableTaskCard supplies it. */
function makeDrag(): TaskCardDrag {
  const attributes: DraggableAttributes = {
    role: 'button',
    tabIndex: 0,
    'aria-disabled': false,
    'aria-pressed': undefined,
    'aria-roledescription': 'draggable task',
    'aria-describedby': 'dnd-kit-instructions',
  }
  return {
    attributes,
    listeners: {
      onKeyDown: vi.fn(),
      onPointerDown: vi.fn(),
    },
    activatorRef: vi.fn(),
  }
}

// ADR-052 §6.8: TaskCard now embeds a `TaskActionButton` (also role="button")
// by default. These two describe blocks test the CARD ROOT's own keyboard
// semantics (open vs. drag-lift) in isolation from that new, orthogonal
// affordance — `showActions={false}` keeps `screen.getByRole('button')`
// (no name filter) unambiguous, exactly as before this feature landed. The
// action button's own isolation (a click on it never opens the card) is
// covered separately in TaskCard.cancelled.test.tsx, and it needs no
// QueryClientProvider here since `showActions={false}` means it never mounts.
describe('TaskCard — keyboard activation, draggable (drag prop present)', () => {
  it('Enter opens the task via onClick', () => {
    const onClick = vi.fn()
    render(<TaskCard task={baseTask()} onClick={onClick} drag={makeDrag()} showActions={false} />)

    const card = screen.getByRole('button')
    fireEvent.keyDown(card, { key: 'Enter' })

    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('Space does NOT open the task — it is reserved for dnd-kit keyboard lift', () => {
    const onClick = vi.fn()
    render(<TaskCard task={baseTask()} onClick={onClick} drag={makeDrag()} showActions={false} />)

    const card = screen.getByRole('button')
    fireEvent.keyDown(card, { key: ' ' })

    expect(onClick).not.toHaveBeenCalled()
  })

  it('still forwards Space to dnd-kit\'s own onKeyDown listener (the lift itself keeps working)', () => {
    const onClick = vi.fn()
    const drag = makeDrag()
    render(<TaskCard task={baseTask()} onClick={onClick} drag={drag} showActions={false} />)

    const card = screen.getByRole('button')
    fireEvent.keyDown(card, { key: ' ' })

    expect(drag.listeners.onKeyDown).toHaveBeenCalledTimes(1)
  })
})

describe('TaskCard — keyboard activation, non-draggable (no drag prop, e.g. ExecutionView)', () => {
  it('Enter opens the task via onClick', () => {
    const onClick = vi.fn()
    render(<TaskCard task={baseTask()} onClick={onClick} showActions={false} />)

    const card = screen.getByRole('button')
    fireEvent.keyDown(card, { key: 'Enter' })

    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('Space ALSO opens the task — there is no drag context reserving it (WCAG 4.1.2)', () => {
    const onClick = vi.fn()
    render(<TaskCard task={baseTask()} onClick={onClick} showActions={false} />)

    const card = screen.getByRole('button')
    fireEvent.keyDown(card, { key: ' ' })

    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('a plain mouse click also opens the task', () => {
    const onClick = vi.fn()
    render(<TaskCard task={baseTask()} onClick={onClick} showActions={false} />)

    fireEvent.click(screen.getByRole('button'))

    expect(onClick).toHaveBeenCalledTimes(1)
  })
})

// ── Bubbling hijack regression (altitude 'show-all' — nested TaskChildren) ──
//
// A subtask row (TaskChildren.tsx) is a native <button> with NO onKeyDown of
// its own — it relies on the browser's native Enter-activation (which
// user-event's `keyboard()` API faithfully simulates, including respecting
// preventDefault). Before the fix, TaskCard's own onKeyDown had no
// `e.target !== e.currentTarget` guard, so an Enter keydown that bubbled up
// from the subtask button ALSO ran the card's handler, which
// preventDefault()'d the event (cancelling the subtask's native activation)
// and called the PARENT card's onClick instead.
describe('TaskCard — keyboard event bubbling from nested subtask rows (altitude "show-all")', () => {
  beforeEach(() => {
    fetchSubtasksMock.mockReset()
  })

  function renderWithQuery(ui: React.ReactElement) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
  }

  it('Enter on a focused subtask opens the SUBTASK, not the parent card', async () => {
    fetchSubtasksMock.mockResolvedValue([
      baseTask({ id: 'child-1', title: 'Child task', parent_task_id: 'task-1' }),
    ])
    const onClick = vi.fn()
    const onChildClick = vi.fn()
    const user = userEvent.setup()
    renderWithQuery(
      <TaskCard task={baseTask()} altitude="show-all" onClick={onClick} onChildClick={onChildClick} />,
    )

    const subtaskButton = await screen.findByRole('button', { name: /Subtask: Child task/i })
    subtaskButton.focus()
    await user.keyboard('{Enter}')

    expect(onChildClick).toHaveBeenCalledTimes(1)
    expect(onChildClick).toHaveBeenCalledWith(expect.objectContaining({ id: 'child-1' }))
    expect(onClick).not.toHaveBeenCalled()
  })

  it('Enter on the card root itself still opens the card, even with subtasks rendered', async () => {
    fetchSubtasksMock.mockResolvedValue([
      baseTask({ id: 'child-1', title: 'Child task', parent_task_id: 'task-1' }),
    ])
    const onClick = vi.fn()
    const { container } = renderWithQuery(
      <TaskCard task={baseTask()} altitude="show-all" onClick={onClick} />,
    )

    await screen.findByRole('button', { name: /Subtask: Child task/i })
    // The card root is the outermost role="button" in document order — the
    // subtask row is nested inside it.
    const card = container.querySelectorAll('[role="button"]')[0]
    fireEvent.keyDown(card, { key: 'Enter' })

    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('Space still lifts the card (draggable) when pressed on the card root, with subtasks rendered', async () => {
    fetchSubtasksMock.mockResolvedValue([
      baseTask({ id: 'child-1', title: 'Child task', parent_task_id: 'task-1' }),
    ])
    const drag = makeDrag()
    const { container } = renderWithQuery(
      <TaskCard task={baseTask()} altitude="show-all" onClick={vi.fn()} drag={drag} />,
    )

    await screen.findByRole('button', { name: /Subtask: Child task/i })
    const card = container.querySelectorAll('[role="button"]')[0]
    fireEvent.keyDown(card, { key: ' ' })

    expect(drag.listeners.onKeyDown).toHaveBeenCalledTimes(1)
  })

  it('Space on a focused subtask does NOT reach the card drag-lift listener (guard blocks bubbled Space too)', async () => {
    fetchSubtasksMock.mockResolvedValue([
      baseTask({ id: 'child-1', title: 'Child task', parent_task_id: 'task-1' }),
    ])
    const drag = makeDrag()
    renderWithQuery(
      <TaskCard task={baseTask()} altitude="show-all" onClick={vi.fn()} drag={drag} />,
    )

    const subtaskButton = await screen.findByRole('button', { name: /Subtask: Child task/i })
    fireEvent.keyDown(subtaskButton, { key: ' ' })

    expect(drag.listeners.onKeyDown).not.toHaveBeenCalled()
  })
})
