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

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { TaskCard, type TaskCardDrag } from './TaskCard'
import type { Task } from '@/lib/api'
import type { DraggableAttributes } from '@dnd-kit/core'

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

describe('TaskCard — keyboard activation, draggable (drag prop present)', () => {
  it('Enter opens the task via onClick', () => {
    const onClick = vi.fn()
    render(<TaskCard task={baseTask()} onClick={onClick} drag={makeDrag()} />)

    const card = screen.getByRole('button')
    fireEvent.keyDown(card, { key: 'Enter' })

    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('Space does NOT open the task — it is reserved for dnd-kit keyboard lift', () => {
    const onClick = vi.fn()
    render(<TaskCard task={baseTask()} onClick={onClick} drag={makeDrag()} />)

    const card = screen.getByRole('button')
    fireEvent.keyDown(card, { key: ' ' })

    expect(onClick).not.toHaveBeenCalled()
  })

  it('still forwards Space to dnd-kit\'s own onKeyDown listener (the lift itself keeps working)', () => {
    const onClick = vi.fn()
    const drag = makeDrag()
    render(<TaskCard task={baseTask()} onClick={onClick} drag={drag} />)

    const card = screen.getByRole('button')
    fireEvent.keyDown(card, { key: ' ' })

    expect(drag.listeners?.onKeyDown).toHaveBeenCalledTimes(1)
  })
})

describe('TaskCard — keyboard activation, non-draggable (no drag prop, e.g. ExecutionView)', () => {
  it('Enter opens the task via onClick', () => {
    const onClick = vi.fn()
    render(<TaskCard task={baseTask()} onClick={onClick} />)

    const card = screen.getByRole('button')
    fireEvent.keyDown(card, { key: 'Enter' })

    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('Space ALSO opens the task — there is no drag context reserving it (WCAG 4.1.2)', () => {
    const onClick = vi.fn()
    render(<TaskCard task={baseTask()} onClick={onClick} />)

    const card = screen.getByRole('button')
    fireEvent.keyDown(card, { key: ' ' })

    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('a plain mouse click also opens the task', () => {
    const onClick = vi.fn()
    render(<TaskCard task={baseTask()} onClick={onClick} />)

    fireEvent.click(screen.getByRole('button'))

    expect(onClick).toHaveBeenCalledTimes(1)
  })
})
