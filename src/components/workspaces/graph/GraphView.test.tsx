/**
 * Render tests for GraphView — the Task DAG canvas.
 *
 * React Flow needs a real ResizeObserver + element sizing, which jsdom lacks,
 * so we stub ResizeObserver and the bounding-box geometry before mounting.
 * These tests assert the two user-visible outcomes: the empty state when there
 * are no graphable tasks, and a node-per-task render with the task titles when
 * there are.
 */

import { describe, it, expect, beforeAll, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { GraphView } from './GraphView'
import type { Task } from '@/lib/api'

// ── React Flow jsdom shims ──────────────────────────────────────────────────
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
  // React Flow measures node bounding boxes; give them a size.
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

describe('GraphView — empty state', () => {
  it('shows the empty state when there are no tasks', () => {
    render(<GraphView tasks={[]} agents={[]} onTaskClick={() => {}} />)
    expect(screen.getByTestId('graph-empty-state')).toBeInTheDocument()
    expect(screen.getByText('No tasks yet')).toBeInTheDocument()
  })

  it('shows the empty state when every task is non-user-surface', () => {
    const tasks = [makeTask({ id: 'hb', surface: 'heartbeat' })]
    render(<GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} />)
    expect(screen.getByTestId('graph-empty-state')).toBeInTheDocument()
  })
})

describe('GraphView — renders nodes + edges', () => {
  it('renders a node for each graphable task with its title', () => {
    const tasks = [
      makeTask({ id: 'a', title: 'Design the schema' }),
      makeTask({ id: 'b', title: 'Build the API', blocked_by: ['a'] }),
    ]
    const { container } = render(
      <GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} />,
    )

    // One custom node element per task (data-testid set by TaskNode).
    expect(container.querySelector('[data-testid="task-node-a"]')).not.toBeNull()
    expect(container.querySelector('[data-testid="task-node-b"]')).not.toBeNull()

    // Titles are visible.
    expect(screen.getByText('Design the schema')).toBeInTheDocument()
    expect(screen.getByText('Build the API')).toBeInTheDocument()
  })

  it('mounts the React Flow edge layer for a graph with dependencies', () => {
    // Note: React Flow only paints individual edge *paths* after nodes are
    // measured, which jsdom does not do — so we assert the edges SVG layer is
    // present (proving edges were handed to React Flow). The exact edge model
    // (source/target/animation) is asserted deterministically against
    // buildTaskGraph in taskGraph.test.ts.
    const tasks = [
      makeTask({ id: 'a', title: 'Alpha' }),
      makeTask({ id: 'b', title: 'Bravo', blocked_by: ['a'] }),
    ]
    const { container } = render(
      <GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} />,
    )
    expect(container.querySelector('.react-flow__edges')).not.toBeNull()
    expect(container.querySelector('.react-flow__viewport')).not.toBeNull()
  })
})
