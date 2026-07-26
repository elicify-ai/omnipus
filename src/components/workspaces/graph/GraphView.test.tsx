/**
 * Render tests for GraphView — the Task DAG canvas.
 *
 * React Flow needs a real ResizeObserver + element sizing, which jsdom lacks,
 * so we stub ResizeObserver and the bounding-box geometry before mounting.
 * These tests assert the two user-visible outcomes: the empty state when there
 * are no graphable tasks, and a node-per-task render with the task titles when
 * there are.
 *
 * Wrapped in a QueryClientProvider (ADR-052 §6.8): every task node now embeds
 * a TaskActionButton (▶/■), which calls `useMutation`/`useQueryClient` — those
 * hooks throw without a QueryClient in the tree, even when no test here ever
 * clicks the button.
 */

import { describe, it, expect, beforeAll, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, type RenderResult } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Edge, Node } from '@xyflow/react'
import type { Task } from '@/lib/api'

// Capture every `nodes` array React Flow is rendered with, by wrapping the
// real `ReactFlow` component and recording its props before delegating to
// the actual implementation. This is how the position-stability regression
// test below (GraphView — position stability across an unrelated re-render)
// observes node-object identity without needing to simulate a real drag
// gesture (React Flow's pointer-based DnD cannot be driven faithfully in
// jsdom — see the file-level comment above).
const capturedNodesCalls: Node[][] = []
// Every `edges` array React Flow is rendered with — captured alongside
// `capturedNodesCalls` so tests can assert the ACTUAL edge set (source/
// target pairs) GraphView hands to React Flow, not just that some edges
// SVG layer element exists in the DOM (which React Flow renders regardless
// of whether any edges were actually passed — see the "renders nodes +
// edges" describe block below).
const capturedEdgesCalls: Edge[][] = []
// Every `fitView(options)` call made through `useReactFlow()` — including
// GraphView's own S2-fix re-fit effect AND the <Controls> "fit view" button
// (both go through this same hook) — recorded here so the re-fit tests below
// can assert WHEN a re-fit happened without needing real React Flow pixel
// measurement (jsdom has none — see the file-level comment above). Wraps the
// REAL `useReactFlow`/`fitView` rather than replacing it outright, so
// `<Controls>`'s own internal `useReactFlow()` call keeps working.
const fitViewCalls: unknown[] = []
vi.mock('@xyflow/react', async () => {
  const actual = await vi.importActual<typeof import('@xyflow/react')>('@xyflow/react')
  return {
    ...actual,
    ReactFlow: (props: React.ComponentProps<typeof actual.ReactFlow>) => {
      capturedNodesCalls.push(props.nodes as Node[])
      capturedEdgesCalls.push((props.edges ?? []) as Edge[])
      return <actual.ReactFlow {...props} />
    },
    useReactFlow: (...args: Parameters<typeof actual.useReactFlow>) => {
      const real = actual.useReactFlow(...args)
      return {
        ...real,
        fitView: (options?: Parameters<typeof real.fitView>[0]) => {
          fitViewCalls.push(options)
          return real.fitView(options)
        },
      }
    },
  }
})

// `vi.mock` calls are hoisted above every import in this file (including the
// one below), so GraphView's own `import { ReactFlow } from '@xyflow/react'`
// resolves to the wrapped version above.
import { GraphView } from './GraphView'

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

beforeEach(() => {
  capturedNodesCalls.length = 0
  capturedEdgesCalls.length = 0
  fitViewCalls.length = 0
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

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

/** Render GraphView under a QueryClientProvider — exposes the client too so
 * a rerender in the same test can reuse the SAME provider instance. */
function renderGraph(ui: React.ReactElement): RenderResult & { client: QueryClient } {
  const client = makeClient()
  const utils = render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
  return { ...utils, client }
}

describe('GraphView — empty state', () => {
  it('shows the empty state when there are no tasks', () => {
    renderGraph(<GraphView tasks={[]} agents={[]} onTaskClick={() => {}} />)
    expect(screen.getByTestId('graph-empty-state')).toBeInTheDocument()
    expect(screen.getByText('No tasks yet')).toBeInTheDocument()
  })

  it('shows the empty state when every task is non-user-surface', () => {
    const tasks = [makeTask({ id: 'hb', surface: 'heartbeat' })]
    renderGraph(<GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} />)
    expect(screen.getByTestId('graph-empty-state')).toBeInTheDocument()
  })
})

describe('GraphView — renders nodes + edges', () => {
  it('renders a node for each graphable task with its title', () => {
    const tasks = [
      makeTask({ id: 'a', title: 'Design the schema' }),
      makeTask({ id: 'b', title: 'Build the API', blocked_by: ['a'] }),
    ]
    const { container } = renderGraph(
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
    // measured, which jsdom does not do — so the `.react-flow__edges` SVG
    // layer element renders regardless of whether any edges were actually
    // passed in (mutation-testing regression: `edges={[]}` still leaves this
    // layer present, so asserting only its existence proves nothing). The
    // load-bearing assertion is against the CAPTURED `edges` prop GraphView
    // actually hands to React Flow — the real blocker->blocked edge GraphView
    // computed from `blocked_by: ['a']`. The exact edge model (styling/
    // animation) is asserted deterministically against buildTaskGraph in
    // taskGraph.test.ts; this test's job is just proving GraphView wires that
    // model THROUGH to React Flow's `edges` prop.
    const tasks = [
      makeTask({ id: 'a', title: 'Alpha' }),
      makeTask({ id: 'b', title: 'Bravo', blocked_by: ['a'] }),
    ]
    const { container } = renderGraph(
      <GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} />,
    )
    expect(container.querySelector('.react-flow__edges')).not.toBeNull()
    expect(container.querySelector('.react-flow__viewport')).not.toBeNull()

    const lastEdges = capturedEdgesCalls[capturedEdgesCalls.length - 1]
    expect(lastEdges).toHaveLength(1)
    expect(lastEdges[0]).toMatchObject({ id: 'a->b', source: 'a', target: 'b' })
  })

  it('passes an EMPTY edges array to React Flow when no task blocks another (proves the edge count reflects real dependencies, not a fixed/hardcoded set)', () => {
    const tasks = [
      makeTask({ id: 'a', title: 'Alpha' }),
      makeTask({ id: 'b', title: 'Bravo' }), // no blocked_by — no dependency
    ]
    renderGraph(<GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} />)

    const lastEdges = capturedEdgesCalls[capturedEdgesCalls.length - 1]
    expect(lastEdges).toHaveLength(0)
  })
})

describe('GraphView — keyboard operability (WCAG 2.1.1 / 4.1.2)', () => {
  // Nodes render with `visibility: hidden` in jsdom (React Flow never gets a
  // real ResizeObserver measurement here), which excludes them from
  // Testing Library's default accessible-role tree — so, like the existing
  // tests above, we locate nodes via the stable `data-testid` rather than
  // `getByRole`, and assert the ARIA wiring directly.

  it('exposes each node as a single focusable role="button" with a title+status label', () => {
    const tasks = [makeTask({ id: 'a', title: 'Design the schema', status: 'in_progress' })]
    const { container } = renderGraph(<GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} />)

    const node = container.querySelector('[data-testid="task-node-a"]')
    expect(node).not.toBeNull()
    expect(node).toHaveAttribute('role', 'button')
    expect(node).toHaveAttribute('tabindex', '0')
    expect(node).toHaveAttribute('aria-label', 'Design the schema, In Progress')

    // React Flow's own node wrapper (`.react-flow__node`, the parent) must
    // NOT also be a tab stop — GraphView marks nodes `focusable: false` so
    // there is exactly one keyboard stop per card, not two.
    const rfWrapper = node?.closest('.react-flow__node')
    expect(rfWrapper).not.toBeNull()
    expect(rfWrapper).not.toHaveAttribute('tabindex')
    expect(rfWrapper).not.toHaveAttribute('role')
  })

  it('Enter on a focused node opens the task via onTaskClick', () => {
    const tasks = [makeTask({ id: 'a', title: 'Design the schema' })]
    const onTaskClick = vi.fn()
    const { container } = renderGraph(<GraphView tasks={tasks} agents={[]} onTaskClick={onTaskClick} />)

    const node = container.querySelector('[data-testid="task-node-a"]') as HTMLElement
    fireEvent.keyDown(node, { key: 'Enter' })
    expect(onTaskClick).toHaveBeenCalledTimes(1)
    expect(onTaskClick.mock.calls[0][0]).toMatchObject({ id: 'a', title: 'Design the schema' })
  })

  it('Space on a focused node also opens the task via onTaskClick', () => {
    const tasks = [makeTask({ id: 'a', title: 'Build the API' })]
    const onTaskClick = vi.fn()
    const { container } = renderGraph(<GraphView tasks={tasks} agents={[]} onTaskClick={onTaskClick} />)

    const node = container.querySelector('[data-testid="task-node-a"]') as HTMLElement
    fireEvent.keyDown(node, { key: ' ' })
    expect(onTaskClick).toHaveBeenCalledTimes(1)
  })
})

describe('GraphView — position stability across an unrelated re-render', () => {
  // Regression test for the re-seed effect described in GraphView.tsx
  // (`nodesWithOpen` / `stableOnOpen`): the parent commonly passes
  // `onTaskClick` as a fresh inline closure every render (e.g.
  // WorkspaceGraphTab's `onTaskClick={(task) => setSelectedTaskId(task.id)}`).
  // If the re-seed effect's memo ever depended on that raw prop again instead
  // of the ref-backed `stableOnOpen`, a parent re-render with no actual graph
  // change would recompute `nodesWithOpen` into brand-new node objects, the
  // effect would re-fire `setNodes(nodesWithOpen)`, and every user-dragged
  // node position would snap back to the dagre layout. We assert the ACTUAL
  // node objects React Flow renders with stay referentially identical across
  // such a re-render — dagre would recompute the SAME numeric positions for
  // unchanged input, so asserting on position VALUES wouldn't catch a re-seed;
  // object identity is the only observable that distinguishes "recomputed"
  // from "untouched".
  it('keeps the nodes array passed to React Flow referentially stable when the parent re-renders with a fresh onTaskClick closure', () => {
    const tasks = [
      makeTask({ id: 'a', title: 'Design the schema' }),
      makeTask({ id: 'b', title: 'Build the API', blocked_by: ['a'] }),
    ]
    // Stable `agents` reference across every render below — an inline `[]`
    // literal in the JSX would itself be a fresh array identity on every
    // call, which would recompute GraphView's `layout` (and thus
    // `nodesWithOpen`) regardless of onTaskClick, confounding exactly the
    // thing this test isolates. Mirrors production, where `agents` comes
    // from a TanStack Query cache and is only a new reference when the data
    // actually changes.
    const agents: never[] = []
    const onTaskClickA = () => {}
    const { rerender, client } = renderGraph(
      <GraphView tasks={tasks} agents={agents} onTaskClick={onTaskClickA} />,
    )

    // Prime: re-render once with the SAME onTaskClick reference before taking
    // the baseline snapshot. This settles any one-time mount-effect churn
    // unrelated to what we're testing (e.g. the "reflect external selection"
    // effect normalising an initial `selected: undefined` to `selected: false`
    // on every node the first time it runs) — since the reference is
    // unchanged here, `nodesWithOpen` cannot recompute either way (fixed or
    // reverted), so this step is neutral with respect to the regression this
    // test targets.
    rerender(
      <QueryClientProvider client={client}>
        <GraphView tasks={tasks} agents={agents} onTaskClick={onTaskClickA} />
      </QueryClientProvider>,
    )
    expect(capturedNodesCalls.length).toBeGreaterThan(0)
    const firstNodes = capturedNodesCalls[capturedNodesCalls.length - 1]
    expect(firstNodes).toHaveLength(2)

    // Re-render with a BRAND NEW onTaskClick closure — same tasks/agents, so
    // nothing about the actual graph changed.
    const onTaskClickB = () => {}
    rerender(
      <QueryClientProvider client={client}>
        <GraphView tasks={tasks} agents={agents} onTaskClick={onTaskClickB} />
      </QueryClientProvider>,
    )

    const lastNodes = capturedNodesCalls[capturedNodesCalls.length - 1]
    // Same array reference — `nodesWithOpen` did not recompute, so the re-seed
    // effect never re-fired `setNodes`.
    expect(lastNodes).toBe(firstNodes)
    // And every individual node object is untouched too (not just the array).
    expect(lastNodes[0]).toBe(firstNodes[0])
    expect(lastNodes[1]).toBe(firstNodes[1])
  })
})

describe('GraphView — re-fits the viewport when the node SET changes (S2 UAT fix #25)', () => {
  // Regression coverage for the finding: "the graph NEVER re-fits when the
  // plan-scope filter changes the node set" — `fitView` on <ReactFlow> only
  // ever applies once, at mount, so a scope change (or any other change to
  // WHICH tasks are visible) left the camera at a stale scale/pan. These
  // tests assert the fix via the `fitViewCalls` spy installed on the mocked
  // `useReactFlow` above — jsdom has no real pixel measurement to assert
  // against directly (see the file-level comment), so "did GraphView ask
  // React Flow to re-fit" is the observable that stands in for "did the
  // camera reframe".

  it('does not call fitView on mount — the fitView prop already frames the initial layout', () => {
    const tasks = [makeTask({ id: 'a' }), makeTask({ id: 'b', blocked_by: ['a'] })]
    renderGraph(<GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} />)
    expect(fitViewCalls).toHaveLength(0)
  })

  it('re-fits when the plan-scope filter narrows "All" down to one plan', () => {
    const tasks = [
      makeTask({ id: 'a', plan_id: 'plan-1' }),
      makeTask({ id: 'b', plan_id: 'plan-1' }),
      makeTask({ id: 'c' }), // outside the plan — visible only in "All"
    ]
    const { rerender, client } = renderGraph(
      <GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} planId={null} />,
    )
    expect(fitViewCalls).toHaveLength(0)

    rerender(
      <QueryClientProvider client={client}>
        <GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} planId="plan-1" />
      </QueryClientProvider>,
    )

    expect(fitViewCalls.length).toBeGreaterThanOrEqual(1)
  })

  it('re-fits again when the scope widens back to "All" (the exact regression the tester measured)', () => {
    // Mirrors the tester's measured sequence: All (3) -> scope to a plan (2)
    // -> back to All (3). Both scope changes must re-fit — the second one is
    // the specific case that left 48/50 nodes clipped off-screen in the UAT
    // report because the camera was still framed for the narrower scope.
    const tasks = [
      makeTask({ id: 'a', plan_id: 'plan-1' }),
      makeTask({ id: 'b', plan_id: 'plan-1' }),
      makeTask({ id: 'c' }),
    ]
    const { rerender, client } = renderGraph(
      <GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} planId={null} />,
    )

    rerender(
      <QueryClientProvider client={client}>
        <GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} planId="plan-1" />
      </QueryClientProvider>,
    )
    const callsAfterNarrowing = fitViewCalls.length
    expect(callsAfterNarrowing).toBeGreaterThanOrEqual(1)

    rerender(
      <QueryClientProvider client={client}>
        <GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} planId={null} />
      </QueryClientProvider>,
    )

    expect(fitViewCalls.length).toBeGreaterThan(callsAfterNarrowing)
  })

  it('never re-fits when only node DATA changes and the visible task SET stays the same (preserves manual pan/zoom)', () => {
    // The other half of the requirement: re-fitting on every data-only change
    // (a status edit, a re-resolved agent) would yank a deliberately
    // panned/zoomed viewport out from under the user for no reason.
    const tasks = [
      makeTask({ id: 'a', status: 'inbox' }),
      makeTask({ id: 'b', blocked_by: ['a'], status: 'inbox' }),
    ]
    const { rerender, client } = renderGraph(
      <GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} />,
    )
    expect(fitViewCalls).toHaveLength(0)

    const sameIdsDifferentStatus = [
      makeTask({ id: 'a', status: 'in_progress' }),
      makeTask({ id: 'b', blocked_by: ['a'], status: 'in_progress' }),
    ]
    rerender(
      <QueryClientProvider client={client}>
        <GraphView tasks={sameIdsDifferentStatus} agents={[]} onTaskClick={() => {}} />
      </QueryClientProvider>,
    )

    expect(fitViewCalls).toHaveLength(0)
  })

  it('clamps fitView to a minimum readable scale (S3 UAT fix #26) on every imperative re-fit', () => {
    const tasks = [
      makeTask({ id: 'a', plan_id: 'plan-1' }),
      makeTask({ id: 'b', plan_id: 'plan-1' }),
      makeTask({ id: 'c' }),
    ]
    const { rerender, client } = renderGraph(
      <GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} planId={null} />,
    )

    rerender(
      <QueryClientProvider client={client}>
        <GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} planId="plan-1" />
      </QueryClientProvider>,
    )

    expect(fitViewCalls.length).toBeGreaterThanOrEqual(1)
    for (const call of fitViewCalls) {
      expect(call).toMatchObject({ minZoom: 0.8 })
    }
  })
})

describe('GraphView — unlinked-count notice (S3 UAT fix #9)', () => {
  // "The graph shows a SUBSET with no indication" — when collapseOrphans
  // excludes some (but not all) visible tasks from the canvas, the previous
  // behaviour dropped them silently. `buildTaskGraph` already returns the
  // exact count via `unlinked`; these tests assert it now reaches the screen.

  it('shows a count + hint when some (but not all) visible tasks are unlinked', () => {
    const tasks = [
      makeTask({ id: 'a', plan_id: 'plan-x' }), // DAG-relevant via plan membership
      makeTask({ id: 'lone1' }),
      makeTask({ id: 'lone2' }),
    ]
    renderGraph(<GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} collapseOrphans />)

    const notice = screen.getByTestId('graph-unlinked-notice')
    expect(notice).toHaveTextContent('2 tasks not shown here')
    expect(notice).toHaveTextContent(/Board.*List/)
  })

  it('uses singular wording for exactly one unlinked task', () => {
    const tasks = [makeTask({ id: 'a', plan_id: 'plan-x' }), makeTask({ id: 'lone' })]
    renderGraph(<GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} collapseOrphans />)

    expect(screen.getByTestId('graph-unlinked-notice')).toHaveTextContent('1 task not shown here')
  })

  it('renders no notice when every visible task is DAG-relevant', () => {
    const tasks = [
      makeTask({ id: 'a', plan_id: 'plan-x' }),
      makeTask({ id: 'b', plan_id: 'plan-x' }),
    ]
    renderGraph(<GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} collapseOrphans />)

    expect(screen.queryByTestId('graph-unlinked-notice')).toBeNull()
  })

  it('renders no notice when collapseOrphans is off, even though standalone tasks are present', () => {
    const tasks = [makeTask({ id: 'a' }), makeTask({ id: 'b' })]
    renderGraph(<GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} />)

    expect(screen.queryByTestId('graph-unlinked-notice')).toBeNull()
  })

  it('still shows the full empty state (not the notice) when every visible task is unlinked', () => {
    const tasks = [makeTask({ id: 'a' }), makeTask({ id: 'b' })]
    renderGraph(<GraphView tasks={tasks} agents={[]} onTaskClick={() => {}} collapseOrphans />)

    expect(screen.getByTestId('graph-empty-state')).toBeInTheDocument()
    expect(screen.queryByTestId('graph-unlinked-notice')).toBeNull()
  })
})
