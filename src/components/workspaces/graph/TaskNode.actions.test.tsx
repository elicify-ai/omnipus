/**
 * TaskNode.actions.test.tsx — ADR-052 §6.8 (Graph node ▶/■ action button,
 * hover/selected-revealed) + FR-015/US-8 (orange "Cancelled" vs red "Failed"
 * node chip).
 *
 * Rendered through the full GraphView pipeline (not TaskNode in isolation) —
 * `Handle`/`Position` from `@xyflow/react` require real React Flow node
 * context, so this mirrors GraphView.test.tsx's harness (xyflow module mock +
 * jsdom ResizeObserver/geometry shims + QueryClientProvider for the embedded
 * TaskActionButton's useMutation).
 *
 * The full per-state action button matrix lives in TaskActionButton.test.tsx
 * — this file proves TaskNode wires it with the real task and the cancelled-
 * aware visual, and that the button doesn't hijack node open/select.
 */

import { describe, it, expect, beforeAll, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Task } from '@/lib/api'

vi.mock('@xyflow/react', async () => {
  const actual = await vi.importActual<typeof import('@xyflow/react')>('@xyflow/react')
  return { ...actual }
})

import { GraphView } from './GraphView'

const runTaskMock = vi.fn()
const stopTaskMock = vi.fn()
const restartTaskMock = vi.fn()

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    runTask: (id: string) => runTaskMock(id),
    stopTask: (id: string) => stopTaskMock(id),
    restartTask: (id: string) => restartTaskMock(id),
  }
})

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

function renderGraph(tasks: Task[], onTaskClick: (t: Task) => void = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <GraphView tasks={tasks} agents={[]} onTaskClick={onTaskClick} />
    </QueryClientProvider>,
  )
}

describe('TaskNode — orange "Cancelled" vs red "Failed" node chip (ADR-052 FR-015/US-8)', () => {
  it('a task Stopped by the user renders "Cancelled" on the node, not "Failed"', () => {
    renderGraph([makeTask({ status: 'failed', cancel_reason: 'stopped_by_user', title: 'Cancelled task' })])
    expect(screen.getByText('Cancelled')).toBeInTheDocument()
    expect(screen.queryByText('Failed')).not.toBeInTheDocument()
  })

  it('a genuinely-failed task renders "Failed" on the node, not "Cancelled"', () => {
    renderGraph([makeTask({ status: 'failed', title: 'Genuinely failed task' })])
    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.queryByText('Cancelled')).not.toBeInTheDocument()
  })

  it('the node aria-label reflects "Cancelled", not "Failed"', () => {
    renderGraph([makeTask({ status: 'failed', cancel_reason: 'stopped_by_user', title: 'Draft the memo' })])
    expect(screen.getByLabelText('Draft the memo, Cancelled')).toBeInTheDocument()
  })
})

// React Flow never gets a real ResizeObserver measurement in jsdom, so every
// node (and everything nested inside it, including TaskActionButton) renders
// under a `.react-flow__node` wrapper with `visibility: hidden` — this
// excludes it from Testing Library's accessible-role tree entirely (even
// with `{ hidden: true }`, which does not reliably restore an aria-label-only
// accessible NAME for a visibility-hidden subtree here — see
// GraphView.test.tsx's file-level comment on the same constraint, and its own
// tests locating nodes via the stable `data-testid` for the identical
// reason). We do the same: locate the action button via its own `aria-label`
// ATTRIBUTE with `container.querySelector`, and drive it with `fireEvent`
// (raw DOM events, no visibility/pointer-events preflight) rather than
// `userEvent` (which factors in visibility before allowing interaction).
describe('TaskNode — ▶/■ action button (ADR-052 §6.8)', () => {
  it('renders a Run action for an idle task on the node', () => {
    const { container } = renderGraph([makeTask({ status: 'inbox', title: 'Draft the memo' })])
    expect(container.querySelector('button[aria-label="Run task Draft the memo"]')).not.toBeNull()
  })

  it('renders a Stop action for an in_progress task on the node', () => {
    const { container } = renderGraph([makeTask({ status: 'in_progress', title: 'Ship it' })])
    expect(container.querySelector('button[aria-label="Stop task Ship it"]')).not.toBeNull()
  })

  it('renders no action button for a done task', () => {
    const { container } = renderGraph([makeTask({ status: 'done', title: 'Wrapped up' })])
    expect(container.querySelector('button[aria-label*="task Wrapped up"]')).toBeNull()
  })

  it('clicking the action button never opens the task (does not hijack node click/select)', () => {
    const onTaskClick = vi.fn()
    const { container } = renderGraph([makeTask({ status: 'inbox', title: 'Draft the memo' })], onTaskClick)
    const btn = container.querySelector('button[aria-label="Run task Draft the memo"]') as HTMLElement
    fireEvent.click(btn)
    expect(onTaskClick).not.toHaveBeenCalled()
  })

  it('the node itself still opens the task on Enter (keyboard operability unaffected)', () => {
    const onTaskClick = vi.fn()
    const { container } = renderGraph([makeTask({ id: 'a', status: 'inbox', title: 'Draft the memo' })], onTaskClick)
    const node = container.querySelector('[data-testid="task-node-a"]') as HTMLElement
    node.focus()
    node.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }))
    expect(onTaskClick).toHaveBeenCalledTimes(1)
  })
})
