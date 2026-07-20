/**
 * BoardView.test.tsx
 *
 * ADR-051 D4 — the plain, tasks-only kanban that replaces the Hierarchical
 * Drill-Down board (BoardView.drilldown.test.tsx, deleted). Covers:
 *   1. Tasks render in their correct STATUS_ORDER column.
 *   2. Vertical column dividers are restored (border-r hairline between
 *      columns, dropped on the last column).
 *   3. One droppable column per STATUS_ORDER entry, and the drop guard
 *      (canDropTransition) still gates onTaskMove vs onMoveRejected.
 *   4. NO plan cards render anywhere on the board — plans only ever live in
 *      the PlansFilterBand now.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { BoardView, canDropTransition } from './BoardView'
import type { Task, Plan } from '@/lib/api'
import { STATUS_ORDER, STATUS_LABELS } from '@/lib/statusColors'

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

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: `t-${Math.random().toString(36).slice(2)}`,
    title: 'A task',
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
  }
}

const plans: Plan[] = []

function renderBoard(props: Partial<React.ComponentProps<typeof BoardView>> = {}) {
  return render(
    <BoardView
      tasks={[]}
      plans={plans}
      agents={[]}
      altitude="top-level"
      onTaskClick={vi.fn()}
      onTaskMove={vi.fn()}
      onMoveRejected={vi.fn()}
      {...props}
    />,
  )
}

describe('BoardView — tasks render in their correct status column', () => {
  it('renders each task under its own status column', () => {
    const tasks = [
      makeTask({ id: 't-inbox', title: 'Inbox task', status: 'inbox' }),
      makeTask({ id: 't-progress', title: 'In-progress task', status: 'in_progress' }),
      makeTask({ id: 't-done', title: 'Done task', status: 'done' }),
    ]
    renderBoard({ tasks })

    for (const task of tasks) {
      const card = screen.getByText(task.title)
      const column = card.closest(`[aria-label="${STATUS_LABELS[task.status]} column"]`)
      expect(column, `expected "${task.title}" inside the ${task.status} column`).not.toBeNull()
    }
  })

  it('never renders subtasks (parent_task_id set) as standalone top-level cards', () => {
    const parent = makeTask({ id: 'parent', title: 'Parent task' })
    const child = makeTask({ id: 'child', title: 'Child subtask', parent_task_id: 'parent' })
    renderBoard({ tasks: [parent, child] })

    expect(screen.getByText('Parent task')).toBeInTheDocument()
    expect(screen.queryByText('Child subtask')).toBeNull()
  })

  it('filters out non-user-surface tasks (e.g. heartbeat)', () => {
    const userTask = makeTask({ id: 'u1', title: 'User task', surface: 'user' })
    const heartbeatTask = makeTask({ id: 'h1', title: 'Heartbeat task', surface: 'heartbeat' })
    renderBoard({ tasks: [userTask, heartbeatTask] })

    expect(screen.getByText('User task')).toBeInTheDocument()
    expect(screen.queryByText('Heartbeat task')).toBeNull()
  })
})

describe('BoardView — column dividers restored (ADR-051 D4)', () => {
  it('every column except the last carries a right border divider', () => {
    renderBoard({ tasks: [] })
    const columns = STATUS_ORDER.map((status) =>
      screen.getByLabelText(`${STATUS_LABELS[status]} column`),
    )
    expect(columns.length).toBe(STATUS_ORDER.length)

    columns.forEach((col, i) => {
      expect(col.className).toContain('border-r')
      if (i === columns.length - 1) {
        expect(col.className).toContain('last:border-r-0')
      }
    })
  })
})

describe('BoardView — one droppable column per status', () => {
  it('renders exactly STATUS_ORDER.length droppable columns', () => {
    renderBoard({ tasks: [] })
    const cols = screen.getAllByLabelText(/column$/i)
    expect(cols.length).toBe(STATUS_ORDER.length)
  })
})

describe('BoardView — drop guard drives onTaskMove vs onMoveRejected', () => {
  it('a legal target status maps to onTaskMove, an illegal one to onMoveRejected', () => {
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

describe('BoardView — no plan cards anywhere on the board (ADR-051 D4)', () => {
  it('renders no plan-card test ids or plan-only affordances, even when plans are passed in', () => {
    const tasks = [makeTask({ id: 't1', title: 'Loose task', plan_id: 'plan-a' })]
    const boardPlans: Plan[] = [
      {
        id: 'plan-a',
        workspace_id: 'ws-1',
        title: 'A Plan',
        state: 'draft',
        plan_phase: 'idle',
        owner_agent_id: 'jim',
        owner: 'admin',
        created_by: 'admin',
        created_at: '2026-06-20T10:00:00Z',
        updated_at: '2026-06-20T10:00:00Z',
      },
    ]
    renderBoard({ tasks, plans: boardPlans })

    // The task itself renders...
    expect(screen.getByText('Loose task')).toBeInTheDocument()
    // ...but nothing carries a plan-card test id, and the plan's own title
    // never renders standalone (it isn't a card on the board).
    expect(screen.queryByTestId(/^plan-card-/)).toBeNull()
    expect(screen.queryByText('A Plan')).toBeNull()
  })
})
