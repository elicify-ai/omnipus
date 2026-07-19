/**
 * Render tests for GraphView's Plan Swimlane board redesign additions: the
 * "N unlinked tasks" tray (`collapseOrphans`) and the plan-scoped canvas
 * (`planId`). These are opt-in props (default off) so the pre-existing
 * GraphView.test.tsx behavior is untouched — every test here passes them
 * explicitly.
 *
 * Same jsdom shims as GraphView.test.tsx (React Flow needs a real
 * ResizeObserver + element sizing, which jsdom lacks).
 */

import { describe, it, expect, beforeAll, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import type { Task } from '@/lib/api'
import { GraphView } from './GraphView'

beforeAll(() => {
  class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  const g = globalThis as unknown as Record<string, unknown>
  g.ResizeObserver = ResizeObserverStub
  g.DOMMatrixReadOnly = class {
    m22 = 1
  }
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, value: 800 })
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, value: 600 })
  if (!Element.prototype.getBoundingClientRect) return
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockReturnValue({
    x: 0, y: 0, width: 248, height: 96, top: 0, left: 0, right: 248, bottom: 96, toJSON: () => ({}),
  } as DOMRect)
})

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 't1',
    title: 'A task',
    action: 'llm',
    status: 'inbox',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    ...overrides,
  } as Task
}

describe('GraphView — unlinked tasks tray (collapseOrphans)', () => {
  it('does not render a tray when collapseOrphans is off (default) — legacy behavior', () => {
    const tasks = [makeTask({ id: 'lone', title: 'Lone task' })]
    const { container } = render(<GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} />)
    expect(container.querySelector('[data-testid="unlinked-tasks-tray"]')).toBeNull()
    // Legacy behavior: the lone task still renders as its own node.
    expect(container.querySelector('[data-testid="task-node-lone"]')).not.toBeNull()
  })

  it('collapses standalone tasks into the tray instead of loose nodes when collapseOrphans is true', () => {
    const tasks = [
      makeTask({ id: 'lone1', title: 'Lone task 1' }),
      makeTask({ id: 'lone2', title: 'Lone task 2' }),
    ]
    const { container, getByTestId } = render(
      <GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} collapseOrphans />,
    )

    // No loose dots for either unlinked task.
    expect(container.querySelector('[data-testid="task-node-lone1"]')).toBeNull()
    expect(container.querySelector('[data-testid="task-node-lone2"]')).toBeNull()

    // The tray is present and reports the correct count.
    const tray = getByTestId('unlinked-tasks-tray')
    expect(tray).toHaveTextContent('2 unlinked tasks')
  })

  it('keeps a connected DAG on the canvas while collapsing only the true orphan into the tray', () => {
    const tasks = [
      makeTask({ id: 'a', title: 'Design the schema' }),
      makeTask({ id: 'b', title: 'Build the API', blocked_by: ['a'] }),
      makeTask({ id: 'lone', title: 'One-off cleanup' }),
    ]
    const { container, getByTestId } = render(
      <GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} collapseOrphans />,
    )

    expect(container.querySelector('[data-testid="task-node-a"]')).not.toBeNull()
    expect(container.querySelector('[data-testid="task-node-b"]')).not.toBeNull()
    expect(container.querySelector('[data-testid="task-node-lone"]')).toBeNull()

    const tray = getByTestId('unlinked-tasks-tray')
    expect(tray).toHaveTextContent('1 unlinked task')
  })

  it('expands the tray on click to list the collapsed tasks, and opens one via onTaskClick', () => {
    const tasks = [
      makeTask({ id: 'lone1', title: 'Write release notes' }),
      makeTask({ id: 'lone2', title: 'Rotate API keys' }),
    ]
    const onTaskClick = vi.fn()
    render(<GraphView tasks={tasks} agents={[]} onTaskClick={onTaskClick} collapseOrphans />)

    // Not expanded initially — the list is not in the document.
    expect(screen.queryByRole('list', { name: 'Unlinked tasks' })).toBeNull()

    const trigger = screen.getByRole('button', { name: /unlinked tasks?/i })
    expect(trigger).toHaveAttribute('aria-expanded', 'false')

    fireEvent.click(trigger)
    expect(trigger).toHaveAttribute('aria-expanded', 'true')

    const list = screen.getByRole('list', { name: 'Unlinked tasks' })
    expect(list).toBeInTheDocument()
    expect(screen.getByText('Write release notes')).toBeInTheDocument()
    expect(screen.getByText('Rotate API keys')).toBeInTheDocument()

    fireEvent.click(screen.getByText('Rotate API keys'))
    expect(onTaskClick).toHaveBeenCalledTimes(1)
    expect(onTaskClick.mock.calls[0][0]).toMatchObject({ id: 'lone2', title: 'Rotate API keys' })
  })

  it('renders no tray and the normal empty state when there are zero tasks, even with collapseOrphans on', () => {
    render(<GraphView tasks={[]} agents={[]} onTaskClick={() => {}} collapseOrphans />)
    expect(screen.getByTestId('graph-empty-state')).toBeInTheDocument()
    expect(screen.queryByTestId('unlinked-tasks-tray')).toBeNull()
  })

  it('renders ONLY the tray (no empty state) when every task is an unlinked orphan', () => {
    const tasks = [makeTask({ id: 'lone', title: 'Solo task' })]
    render(<GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} collapseOrphans />)
    expect(screen.queryByTestId('graph-empty-state')).toBeNull()
    expect(screen.getByTestId('unlinked-tasks-tray')).toBeInTheDocument()
  })
})

describe('GraphView — plan-scoped canvas (planId)', () => {
  it('shows only the scoped plan\'s member tasks as nodes', () => {
    const tasks = [
      makeTask({ id: 'a', title: 'Alpha', plan_id: 'plan-1' }),
      makeTask({ id: 'b', title: 'Bravo', plan_id: 'plan-1', blocked_by: ['a'] }),
      makeTask({ id: 'c', title: 'Charlie', plan_id: 'plan-2' }),
      makeTask({ id: 'd', title: 'Delta' }), // no plan
    ]
    const { container } = render(
      <GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} planId="plan-1" />,
    )
    expect(container.querySelector('[data-testid="task-node-a"]')).not.toBeNull()
    expect(container.querySelector('[data-testid="task-node-b"]')).not.toBeNull()
    expect(container.querySelector('[data-testid="task-node-c"]')).toBeNull()
    expect(container.querySelector('[data-testid="task-node-d"]')).toBeNull()
  })

  it('never renders the unlinked tray in plan scope, even though a lone member has no edges', () => {
    const tasks = [
      makeTask({ id: 'a', plan_id: 'plan-1' }),
      makeTask({ id: 'b', plan_id: 'plan-1' }),
    ]
    render(<GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} planId="plan-1" collapseOrphans />)
    expect(screen.queryByTestId('unlinked-tasks-tray')).toBeNull()
  })

  it('shows the friendly "no dependencies yet" state for a plan with fewer than 2 member tasks', () => {
    const tasks = [makeTask({ id: 'a', plan_id: 'plan-1' })]
    render(<GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} planId="plan-1" />)
    expect(screen.getByTestId('graph-plan-empty-state')).toBeInTheDocument()
    expect(screen.getByText('This plan has no dependencies yet')).toBeInTheDocument()
  })

  it('shows the friendly "no dependencies yet" state for a plan with zero member tasks', () => {
    const tasks = [makeTask({ id: 'a', plan_id: 'other-plan' })]
    render(<GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} planId="plan-1" />)
    expect(screen.getByTestId('graph-plan-empty-state')).toBeInTheDocument()
  })
})
