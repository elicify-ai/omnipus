// Wave-3 RED placement pack: baseline side-panel-shell-spec.md §10/FR-022,
// amended only by PANEL-INDICATOR-DECISION-20261005.md PI1/PI2/PI3.
// PI1: the running PLAN TILE has a spinning arrow, no count.
// PI2: a running TASK NODE in the PLAN-SCOPED graph must animate.
// PI3: Board/List/Graph running TASKS also have spinner-only treatment;
// chat's existing counter stays unchanged. No token source is invented.
//
// The unit boundaries are REAL BoardView/ListView/GraphView/PlansFilterBand.
// Layout-only browser shims are necessary for React Flow in jsdom. Neither
// RunningIndicator nor the surfaces are mocked. No test id is required:
// the indicator is an accessible Running status with an animated SVG.
// Item boundaries are the actual card/node role-button, table row, or
// labelled plan tile. A column or canvas ancestor cannot satisfy placement.
// The instrument cases exercise the SAME assertion on minimal fixtures,
// moving/removing the status and injecting '0 tok'. They verify this test
// instrument, not production mutation coverage (deferred to fresh CHECK).
//
// Known RED production gap at 105e2c072: TaskCard, ListView, graph/TaskNode,
// PlansFilterBand do not consume the catalogued RunningIndicator yet.

import { afterAll, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Agent, Plan, Task } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { BoardView } from './BoardView'
import { ListView } from './ListView'
import { PlansFilterBand } from './PlansFilterBand'
import { GraphView } from './graph/GraphView'
import { TaskChildren } from './TaskChildren'

// Network edge only, needed by the REAL Board → TaskCard → TaskChildren path.
const apiMocks = vi.hoisted(() => ({ fetchSubtasks: vi.fn() }))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchSubtasks: apiMocks.fetchSubtasks,
}))
beforeEach(() => { apiMocks.fetchSubtasks.mockReset().mockResolvedValue([]) })

// jsdom has no layout engine. Supply only browser geometry, not graph state.
// 800×600 is this test's frame; 248×96 is the TaskNode/dagre layout fixture
// (taskGraph.ts NODE_WIDTH/NODE_HEIGHT), and handles are the rendered h-2/w-2.
// These sizes are harness inputs, never running-indicator expected values.
function layoutSize(target: Element) {
  if (target.matches('.react-flow__handle')) return { width: 8, height: 8 }
  if (target.matches('.react-flow__node')) return { width: 248, height: 96 }
  return { width: 800, height: 600 }
}

const layoutProperties = ['offsetWidth', 'offsetHeight', 'clientWidth', 'clientHeight'] as const
const originalLayoutProperties = new Map(layoutProperties.map((property) =>
  [property, Object.getOwnPropertyDescriptor(HTMLElement.prototype, property)] as const))
const nativeBoundingRect = HTMLElement.prototype.getBoundingClientRect
let restoreBoundingRect: () => void

beforeAll(() => {
  // React Flow's browser boundary follows its documented testing shim:
  // https://reactflow.dev/learn/advanced-use/testing . An empty observe()
  // leaves node.measured unset and the real node wrapper visibility:hidden.
  class LayoutResizeObserver implements ResizeObserver {
    private pending = new Map<Element, ReturnType<typeof setTimeout>>()
    constructor(private callback: ResizeObserverCallback) {}

    observe(target: Element) {
      this.unobserve(target)
      this.pending.set(target, setTimeout(() => {
        this.pending.delete(target)
        const { width, height } = layoutSize(target)
        const size = { inlineSize: width, blockSize: height }
        const entry: ResizeObserverEntry = {
          target, contentRect: new DOMRectReadOnly(0, 0, width, height),
          borderBoxSize: [size], contentBoxSize: [size], devicePixelContentBoxSize: [size],
        }
        this.callback([entry], this)
      }, 0))
    }

    unobserve(target: Element) {
      clearTimeout(this.pending.get(target))
      this.pending.delete(target)
    }

    disconnect() {
      for (const target of this.pending.keys()) this.unobserve(target)
    }
  }
  vi.stubGlobal('ResizeObserver', LayoutResizeObserver)
  vi.stubGlobal('DOMMatrixReadOnly', class {
    m22: number
    constructor(transform: string) {
      const matrix = transform.match(/^matrix\(([^)]+)\)$/)?.[1].split(',').map(Number)
      this.m22 = matrix?.[3] ?? Number(transform.match(/scale\(([\d.]+)\)/)?.[1] ?? 1)
    }
  })
  for (const property of layoutProperties) {
    Object.defineProperty(HTMLElement.prototype, property, {
      configurable: true,
      get(this: HTMLElement) {
        // New client metrics are confined to the graph/frame. Keep unrelated
        // Board/List/Plans browser inputs at their original jsdom values.
        if (property.startsWith('client') && !this.closest('.react-flow') && !this.querySelector(':scope > .react-flow')) {
          const descriptor = originalLayoutProperties.get(property)
          return descriptor?.get?.call(this) ?? descriptor?.value ?? 0
        }
        const size = layoutSize(this)
        return property.endsWith('Width') ? size.width : size.height
      },
    })
  }
  const boundingRect = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    if (!this.matches('.react-flow, .react-flow__renderer, .react-flow__viewport, .react-flow__node, .react-flow__handle')) {
      return nativeBoundingRect.call(this)
    }
    const { width, height } = layoutSize(this)
    const node = this.closest<HTMLElement>('.react-flow__node')
    const position = node?.style.transform.match(/translate\(([-\d.]+)px,\s*([-\d.]+)px\)/)
    let x = Number(position?.[1] ?? 0)
    let y = Number(position?.[2] ?? 0)
    if (!node) return new DOMRect(x, y, width, height)
    if (this.matches('.react-flow__handle')) {
      const nodeSize = layoutSize(node)
      x += this.classList.contains('react-flow__handle-right') ? nodeSize.width - width / 2 : -width / 2
      y += (nodeSize.height - height) / 2
    }
    const transform = this.closest('.react-flow')?.querySelector<HTMLElement>('.react-flow__viewport')?.style.transform ?? ''
    const pan = transform.match(/translate\(([-\d.]+)px,\s*([-\d.]+)px\)/)
    const zoom = Number(transform.match(/scale\(([\d.]+)\)/)?.[1] ?? 1)
    return new DOMRect(Number(pan?.[1] ?? 0) + x * zoom, Number(pan?.[2] ?? 0) + y * zoom, width * zoom, height * zoom)
  })
  restoreBoundingRect = () => boundingRect.mockRestore()
})

afterAll(() => {
  restoreBoundingRect()
  for (const [property, descriptor] of originalLayoutProperties) {
    if (descriptor) Object.defineProperty(HTMLElement.prototype, property, descriptor)
    else Reflect.deleteProperty(HTMLElement.prototype, property)
  }
  vi.unstubAllGlobals()
})

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-default', title: 'A task', status: 'inbox', action: 'llm', priority: 3,
    workspace_id: 'ws-1', surface: 'user', owner: 'admin', created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z', updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

function makePlan(overrides: Partial<Plan> = {}): Plan {
  return {
    id: 'plan-default', workspace_id: 'ws-1', title: 'A plan', state: 'draft',
    plan_phase: 'idle', owner_agent_id: 'jim', owner: 'admin', created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z', updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

const agents: Agent[] = [{
  revision: '0'.repeat(64), id: 'jim', name: 'Jim', type: 'core', locked: true,
  status: 'active', soul: '', timeout_seconds: 300, max_tool_iterations: 50,
  max_tool_iterations_source: 'global', max_tool_iterations_override_ignored: false,
  memory_enabled: true, needs_model: false,
}]

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

type ItemKind = 'board' | 'list' | 'graph' | 'plan' | 'child'

function itemBoundaryOf(container: HTMLElement, title: string, kind: ItemKind): HTMLElement {
  if (kind === 'plan') return within(container).getByRole('group', { name: title })
  const titleElement = within(container).getByText(title, { exact: true })
  const selector = kind === 'list' ? 'tr' : kind === 'child' ? 'button' : '[role="button"]'
  const boundary = titleElement.closest(selector)
  expect(boundary, `${kind}: ${title} is mounted in its real item boundary`).not.toBeNull()
  return boundary as HTMLElement
}

function runningIndicatorsIn(scope: HTMLElement): HTMLElement[] {
  return within(scope).queryAllByRole('status', { name: 'Running' })
}

/** Exact visible treatment: spinner ONLY. Empty text rejects bare numbers,
 * '0 tok', unavailable labels, aggregates and placeholders, not just /tok/.
 * Accessible naming remains 'Running', not a numeric count. */
function assertSpinnerOnly(status: HTMLElement): void {
  expect(status).toHaveAccessibleName('Running')
  expect(status.querySelectorAll('svg')).toHaveLength(1)
  expect(status.querySelector('svg')).toHaveClass('animate-spin')
  expect(status.textContent?.trim(), 'PI1/PI3: the indicator has no count or placeholder').toBe('')
}

function assertItemIndicator(boundary: HTMLElement): HTMLElement {
  const indicators = runningIndicatorsIn(boundary)
  expect(indicators, 'exactly one Running status must be INSIDE the running item').toHaveLength(1)
  assertSpinnerOnly(indicators[0])
  return indicators[0]
}

function renderTaskSurface(kind: 'board' | 'list' | 'graph', tasks: Task[], planId?: string) {
  return render(
    <QueryClientProvider client={makeClient()}>
      {kind === 'board' ? (
        <BoardView tasks={tasks} plans={[]} agents={[]} altitude="top-level"
          onTaskClick={vi.fn()} onTaskMove={vi.fn()} onMoveRejected={vi.fn()} />
      ) : kind === 'list' ? (
        <ListView tasks={tasks} agents={agents} onTaskClick={vi.fn()} />
      ) : (
        <GraphView tasks={tasks} agents={[]} onTaskClick={vi.fn()} planId={planId} />
      )}
    </QueryClientProvider>,
  )
}

function renderBand(plans: Plan[]) {
  const mounted = render(
    <QueryClientProvider client={makeClient()}>
      <PlansFilterBand plans={plans} tasks={[]} agents={agents} selectedPlanId={null}
        onSelectPlan={vi.fn()} onNewPlan={vi.fn()} onEditPlan={vi.fn()}
        onClearPlan={vi.fn()} showNewPlanTile={false} />
    </QueryClientProvider>,
  )
  // T12 collapses Plans on open; placement assertions exercise the real expanded tiles.
  fireEvent.click(within(mounted.container).getByRole('button', { name: 'Plans' }))
  return mounted
}

// Minimal test-instrument fixtures use the same semantic boundary types,
// not any test-id seam. This is not a mock of a production surface.
function InstrumentItem({ title, kind, withIndicator = false }: {
  title: string; kind: ItemKind; withIndicator?: boolean
}) {
  const body = <>
    <span>{title}</span>
    {withIndicator && <span role="status" aria-label="Running"><svg className="animate-spin" /></span>}
  </>
  if (kind === 'list') return <tr><td>{body}</td></tr>
  if (kind === 'plan') return <Card role="group" aria-label={title}>{body}</Card>
  return <Button role="button">{body}</Button>
}

describe.each(['board', 'list', 'graph', 'plan', 'child'] as const)(
  'instrument proof — %s item boundary', (kind) => {
    function mountInstrument() {
      const items = <>
        <InstrumentItem title="Alpha item" kind={kind} withIndicator />
        <InstrumentItem title="Beta item" kind={kind} />
      </>
      return render(kind === 'list' ? <table><tbody>{items}</tbody></table> : <div>{items}</div>)
    }

    it('accepts the indicator inside the running item and finds none in its sibling', () => {
      const mounted = mountInstrument()
      const running = itemBoundaryOf(mounted.container, 'Alpha item', kind)
      const sibling = itemBoundaryOf(mounted.container, 'Beta item', kind)
      expect(() => assertItemIndicator(running)).not.toThrow()
      expect(runningIndicatorsIn(sibling)).toHaveLength(0)
    })

    it('rejects the SAME assertion after moving the indicator into the sibling', () => {
      const mounted = mountInstrument()
      const running = itemBoundaryOf(mounted.container, 'Alpha item', kind)
      const sibling = itemBoundaryOf(mounted.container, 'Beta item', kind)
      const status = assertItemIndicator(running)
      sibling.appendChild(status)
      expect(() => assertItemIndicator(running)).toThrow(/exactly one Running status/)
      expect(runningIndicatorsIn(sibling)).toHaveLength(1)
    })

    it('rejects an indicator outside the item, even inside its shared parent', () => {
      const mounted = mountInstrument()
      const running = itemBoundaryOf(mounted.container, 'Alpha item', kind)
      const status = assertItemIndicator(running)
      running.parentElement?.appendChild(status)
      expect(() => assertItemIndicator(running)).toThrow(/exactly one Running status/)
    })

    it('rejects removal of the indicator instead of passing by absence', () => {
      const mounted = mountInstrument()
      const running = itemBoundaryOf(mounted.container, 'Alpha item', kind)
      assertItemIndicator(running).remove()
      expect(() => assertItemIndicator(running)).toThrow(/exactly one Running status/)
    })

    it('rejects a token placeholder injected into the animated status', () => {
      const mounted = mountInstrument()
      const running = itemBoundaryOf(mounted.container, 'Alpha item', kind)
      assertItemIndicator(running).appendChild(document.createTextNode('0 tok'))
      expect(() => assertItemIndicator(running)).toThrow(/no count or placeholder/)
    })
  },
)

const runningTask = makeTask({ id: 't-live', title: 'Live refactor', status: 'in_progress' })
const doneTask = makeTask({ id: 't-done', title: 'Shipped fix', status: 'done' })
const nextTask = makeTask({ id: 't-next', title: 'Queued fix', status: 'next' })

describe('SP-41/PI3 — Board running cards', () => {
  it('puts exactly one animated, count-free Running status inside the running card, not other cards', () => {
    const mounted = renderTaskSurface('board', [runningTask, doneTask, nextTask])
    assertItemIndicator(itemBoundaryOf(mounted.container, runningTask.title, 'board'))
    expect(runningIndicatorsIn(itemBoundaryOf(mounted.container, doneTask.title, 'board'))).toHaveLength(0)
    expect(runningIndicatorsIn(itemBoundaryOf(mounted.container, nextTask.title, 'board'))).toHaveLength(0)
    expect(runningIndicatorsIn(mounted.container)).toHaveLength(1)
  })

  it('GUARD: no running task means no indicator on the mounted board', () => {
    const mounted = renderTaskSurface('board', [doneTask, nextTask])
    itemBoundaryOf(mounted.container, doneTask.title, 'board')
    itemBoundaryOf(mounted.container, nextTask.title, 'board')
    expect(runningIndicatorsIn(mounted.container)).toHaveLength(0)
  })
})

describe('SP-41/PI3 — List running rows', () => {
  it('puts exactly one animated, count-free Running status inside the running row, not the done row', () => {
    const mounted = renderTaskSurface('list', [runningTask, doneTask])
    const row = itemBoundaryOf(mounted.container, runningTask.title, 'list')
    const indicator = assertItemIndicator(row)
    // Status is the column governed by the running treatment, not Title,
    // Agent or Updated. Compute its index from the real header row.
    const headers = within(mounted.container).getAllByRole('columnheader')
    const statusIndex = headers.findIndex((header) => within(header).queryByRole('button', { name: /^Status column/ }) !== null)
    expect(statusIndex, 'the actual List has a Status column').toBeGreaterThanOrEqual(0)
    expect(within(row).getAllByRole('cell')[statusIndex]).toContainElement(indicator)
    expect(runningIndicatorsIn(itemBoundaryOf(mounted.container, doneTask.title, 'list'))).toHaveLength(0)
    expect(runningIndicatorsIn(mounted.container)).toHaveLength(1)
  })

  it('GUARD: no running task means no indicator in the mounted list', () => {
    const mounted = renderTaskSurface('list', [doneTask])
    itemBoundaryOf(mounted.container, doneTask.title, 'list')
    expect(runningIndicatorsIn(mounted.container)).toHaveLength(0)
  })
})

describe('SP-41/PI3 — Graph running nodes', () => {
  it('puts exactly one animated, count-free Running status inside the running node, not the done node', async () => {
    const mounted = renderTaskSurface('graph', [runningTask, doneTask])
    await waitFor(() => expect(itemBoundaryOf(mounted.container, runningTask.title, 'graph')).toBeVisible())
    assertItemIndicator(itemBoundaryOf(mounted.container, runningTask.title, 'graph'))
    expect(runningIndicatorsIn(itemBoundaryOf(mounted.container, doneTask.title, 'graph'))).toHaveLength(0)
    expect(runningIndicatorsIn(mounted.container)).toHaveLength(1)
  })
})

describe('PI2 — running TASK NODE in a plan-scoped graph', () => {
  it('animates the running plan member node without a count and excludes other-plan nodes', async () => {
    const running = makeTask({ ...runningTask, plan_id: 'plan-1' })
    const done = makeTask({ ...doneTask, plan_id: 'plan-1' })
    const foreign = makeTask({ id: 't-foreign', title: 'Other plan work', status: 'in_progress', plan_id: 'plan-2' })
    const mounted = renderTaskSurface('graph', [running, done, foreign], 'plan-1')
    // First prove the requested plan's nodes mounted and the other plan did
    // not; only then assert the running member's item-local treatment.
    const member = itemBoundaryOf(mounted.container, running.title, 'graph')
    await waitFor(() => expect(member).toBeVisible())
    itemBoundaryOf(mounted.container, done.title, 'graph')
    expect(within(mounted.container).queryByText(foreign.title)).not.toBeInTheDocument()
    assertItemIndicator(member)
    expect(runningIndicatorsIn(itemBoundaryOf(mounted.container, done.title, 'graph'))).toHaveLength(0)
  })
})

const runningPlan = makePlan({ id: 'plan-live', title: 'Release train', state: 'running' })
const draftPlan = makePlan({ id: 'plan-draft', title: 'Someday maybe', state: 'draft' })

describe('SP-41/PI3 — nested Board TaskChildren compact rows', () => {
  const childRunning = makeTask({ id: 'child-live', title: 'Running child', status: 'in_progress', parent_task_id: 'parent-1' })
  const childDone = makeTask({ id: 'child-done', title: 'Finished child', status: 'done', parent_task_id: 'parent-1' })

  function renderChildren(children: Task[]) {
    return render(
      <QueryClientProvider client={makeClient()}>
        <TaskChildren parentTaskId="parent-1" preloaded={children} onChildClick={vi.fn()} />
      </QueryClientProvider>,
    )
  }

  it('puts an animated count-free status INSIDE the running child row, not its completed sibling', () => {
    const mounted = renderChildren([childRunning, childDone])
    const list = within(mounted.container).getByRole('list', { name: 'Subtasks' })
    expect(within(list).getAllByRole('listitem')).toHaveLength(2)
    const running = itemBoundaryOf(mounted.container, childRunning.title, 'child')
    const done = itemBoundaryOf(mounted.container, childDone.title, 'child')
    assertItemIndicator(running)
    expect(runningIndicatorsIn(done)).toHaveLength(0)
    expect(runningIndicatorsIn(list)).toHaveLength(1)
    expect(apiMocks.fetchSubtasks).not.toHaveBeenCalled()
  })

  it('GUARD: mounted nonrunning child rows carry no animated indicator', () => {
    const mounted = renderChildren([childDone])
    itemBoundaryOf(mounted.container, childDone.title, 'child')
    expect(runningIndicatorsIn(mounted.container)).toHaveLength(0)
  })

  it('reaches the same nested row through REAL BoardView → TaskCard → TaskChildren when children are expanded', async () => {
    apiMocks.fetchSubtasks.mockResolvedValue([childRunning, childDone])
    const parent = makeTask({ id: 'parent-1', title: 'Parent card', status: 'next' })
    const mounted = render(
      <QueryClientProvider client={makeClient()}>
        <BoardView tasks={[parent]} plans={[]} agents={[]} altitude="show-all"
          onTaskClick={vi.fn()} onTaskMove={vi.fn()} onMoveRejected={vi.fn()} />
      </QueryClientProvider>,
    )
    await within(mounted.container).findByText(childRunning.title)
    await waitFor(() => expect(apiMocks.fetchSubtasks).toHaveBeenCalledWith('parent-1'))
    const parentCard = itemBoundaryOf(mounted.container, parent.title, 'board')
    const childRow = itemBoundaryOf(mounted.container, childRunning.title, 'child')
    expect(parentCard).toContainElement(childRow)
    assertItemIndicator(childRow)
    expect(runningIndicatorsIn(itemBoundaryOf(mounted.container, childDone.title, 'child'))).toHaveLength(0)
  })
})

describe('PI1 — running PLAN TILE in the Plans band', () => {
  it('puts exactly one animated status inside the running tile, not the draft tile', () => {
    const mounted = renderBand([runningPlan, draftPlan])
    assertItemIndicator(itemBoundaryOf(mounted.container, runningPlan.title, 'plan'))
    expect(runningIndicatorsIn(itemBoundaryOf(mounted.container, draftPlan.title, 'plan'))).toHaveLength(0)
  })

  it('the mounted running tile has spinner-only treatment: no token text or placeholder (PI1)', () => {
    // Retains the separate no-count case from 105e2c072; it never passes just
    // because nothing rendered. assertItemIndicator first requires the status.
    const mounted = renderBand([runningPlan])
    const indicator = assertItemIndicator(itemBoundaryOf(mounted.container, runningPlan.title, 'plan'))
    expect(indicator.textContent?.trim()).toBe('')
    expect(indicator).toHaveAccessibleName('Running')
  })

  it('GUARD: no running plan means no indicator in the mounted band', () => {
    const mounted = renderBand([draftPlan])
    itemBoundaryOf(mounted.container, draftPlan.title, 'plan')
    expect(runningIndicatorsIn(mounted.container)).toHaveLength(0)
  })
})

describe('SP-41 — one treatment on all four surfaces', () => {
  it('Board/List/Graph/Plans use the same animated Running status with no counts', async () => {
    for (const kind of ['board', 'list', 'graph'] as const) {
      const mounted = renderTaskSurface(kind, [runningTask])
      if (kind === 'graph') {
        await waitFor(() => expect(itemBoundaryOf(mounted.container, runningTask.title, kind)).toBeVisible())
      }
      assertItemIndicator(itemBoundaryOf(mounted.container, runningTask.title, kind))
      expect(runningIndicatorsIn(mounted.container)).toHaveLength(1)
      mounted.unmount()
    }
    const band = renderBand([runningPlan])
    assertItemIndicator(itemBoundaryOf(band.container, runningPlan.title, 'plan'))
    expect(runningIndicatorsIn(band.container)).toHaveLength(1)
  })

  it('the band uses the same accessible indicator contract while keeping PI1 count-free treatment', () => {
    // Retains the separate band's shared-element-contract case from 105e2c072;
    // accessibility replaces the unapproved data-testid-only seam.
    const band = renderBand([runningPlan])
    const indicator = assertItemIndicator(itemBoundaryOf(band.container, runningPlan.title, 'plan'))
    expect(indicator).toHaveAttribute('role', 'status')
    expect(indicator.textContent?.trim()).toBe('')
  })
})
