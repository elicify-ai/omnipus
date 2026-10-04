// RunningIndicator.surfaces.test.tsx — RED pack for side-panel-shell-spec.md
// Wave 3, FR-022 (SP-41) — the PLACEMENT half:
//
//   "Every running task surfaced in the Tasks panel's Board, List and Graph
//    views, and in the Plans band, MUST show ONE standard 'running'
//    indicator."  (FR-022)
//
// Oracle sources: spec §13 FR-022 / §10 Wave 3 last row; "running task" =
// a task whose status is `in_progress` — the canonical live-work state
// (src/lib/statusColors.ts: "in_progress — Forge Gold — live work", and the
// Graph's STATUS_VISUALS mark it animated); the Plans band's row of FR-022
// is read here as: a plan whose state is `running` (the engine actively
// dispatching member tasks) carries the indicator on its tile — the band
// surfaces plans, not individual tasks, so this is the only reading that
// can satisfy FR-022 there. FLAGGED for CHECK/architect to confirm; see the
// report's open items.
//
// The component itself is pinned by RunningIndicator.test.tsx; here the
// contract is WHERE it appears:
//   - exactly ONE `data-testid="running-indicator"` per running task/plan
//     in the rendered surface,
//   - ZERO when nothing runs,
//   - the one indicator sits INSIDE the running item's own subtree (proved
//     by an ancestor walk matching the running item's title — deliberately
//     no container-testid prescription).
//
// RED evidence (2026-10-04, read BoardView.tsx / ListView.tsx /
// graph/GraphView.test.tsx harness / PlansFilterBand.tsx): none of the four
// surfaces renders any running-indicator element today. Every assertion
// below fails against this code.

import { describe, it, expect, vi, beforeAll } from 'vitest'
import { render, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Task, Plan, Agent } from '@/lib/api'

// ── React Flow jsdom shims (mirrors graph/GraphView.test.tsx) ───────────────
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

function makePlan(overrides: Partial<Plan> = {}): Plan {
  return {
    id: `plan-${Math.random().toString(36).slice(2)}`,
    workspace_id: 'ws-1',
    title: 'A plan',
    state: 'draft',
    plan_phase: 'idle',
    owner_agent_id: 'jim',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  } as Plan
}

const agents: Agent[] = [
  { revision: '0'.repeat(64), id: 'jim', name: 'Jim', type: 'core', locked: true, status: 'active', soul: '', timeout_seconds: 300, max_tool_iterations: 50, memory_enabled: true, needs_model: false },
]

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

/** Walk up from an element to the shallowest ancestor whose text contains
 * BOTH the item's title and a running indicator — proving the indicator is
 * INSIDE the running item's own subtree without prescribing container
 * test ids. Returns null when they never co-occur in one subtree. */
function ancestorOfIndicatorAndTitle(
  container: HTMLElement,
  title: string,
): HTMLElement | null {
  const indicators = Array.from(container.querySelectorAll('[data-testid="running-indicator"]'))
  for (const indicator of indicators) {
    let el: HTMLElement | null = indicator as HTMLElement
    while (el && el !== container) {
      if (el.textContent?.includes(title)) return el
      el = el.parentElement
    }
  }
  return null
}

function countIndicators(container: HTMLElement): number {
  return container.querySelectorAll('[data-testid="running-indicator"]').length
}

import { BoardView } from './BoardView'
import { ListView } from './ListView'
import { PlansFilterBand } from './PlansFilterBand'
import { GraphView } from './graph/GraphView'

describe('SP-41 — Board surfaces the running indicator on running tasks', () => {
  it('the one in_progress task carries exactly one indicator inside its card', () => {
    const tasks = [
      makeTask({ id: 't-live', title: 'Live refactor', status: 'in_progress' }),
      makeTask({ id: 't-done', title: 'Shipped fix', status: 'done' }),
      makeTask({ id: 't-next', title: 'Queued fix', status: 'next' }),
    ]
    const mounted = render(
      <QueryClientProvider client={makeClient()}>
        <BoardView tasks={tasks} plans={[]} agents={[]} altitude="top-level" onTaskClick={vi.fn()} onTaskMove={vi.fn()} onMoveRejected={vi.fn()} />
      </QueryClientProvider>,
    )
    expect(countIndicators(mounted.container)).toBe(1)
    expect(ancestorOfIndicatorAndTitle(mounted.container, 'Live refactor')).not.toBeNull()
    expect(ancestorOfIndicatorAndTitle(mounted.container, 'Shipped fix')).toBeNull()
    expect(ancestorOfIndicatorAndTitle(mounted.container, 'Queued fix')).toBeNull()
  })

  it('GUARD (green by construction today): no running task → no indicator anywhere on the board', () => {
    // Labelled guard — the false-positive half of the placement contract;
    // it can only bite once the indicator exists.
    const tasks = [
      makeTask({ id: 't-done', title: 'Shipped fix', status: 'done' }),
      makeTask({ id: 't-next', title: 'Queued fix', status: 'next' }),
    ]
    const mounted = render(
      <QueryClientProvider client={makeClient()}>
        <BoardView tasks={tasks} plans={[]} agents={[]} altitude="top-level" onTaskClick={vi.fn()} onTaskMove={vi.fn()} onMoveRejected={vi.fn()} />
      </QueryClientProvider>,
    )
    expect(countIndicators(mounted.container)).toBe(0)
  })
})

describe('SP-41 — List surfaces the running indicator on running tasks', () => {
  it('the one in_progress task carries exactly one indicator inside its row', () => {
    const tasks = [
      makeTask({ id: 't-live', title: 'Live refactor', status: 'in_progress' }),
      makeTask({ id: 't-done', title: 'Shipped fix', status: 'done' }),
    ]
    const mounted = render(
      <QueryClientProvider client={makeClient()}>
        <ListView tasks={tasks} agents={[{ id: 'jim', name: 'Jim' }]} onTaskClick={vi.fn()} />
      </QueryClientProvider>,
    )
    expect(countIndicators(mounted.container)).toBe(1)
    expect(ancestorOfIndicatorAndTitle(mounted.container, 'Live refactor')).not.toBeNull()
    expect(ancestorOfIndicatorAndTitle(mounted.container, 'Shipped fix')).toBeNull()
  })

  it('GUARD (green by construction today): no running task → no indicator in the list', () => {
    // Labelled guard — see the board guard note.
    const tasks = [makeTask({ id: 't-done', title: 'Shipped fix', status: 'done' })]
    const mounted = render(
      <QueryClientProvider client={makeClient()}>
        <ListView tasks={tasks} agents={[{ id: 'jim', name: 'Jim' }]} onTaskClick={vi.fn()} />
      </QueryClientProvider>,
    )
    expect(countIndicators(mounted.container)).toBe(0)
  })
})

describe('SP-41 — Graph surfaces the running indicator on running nodes', () => {
  it('the one in_progress node carries exactly one indicator', () => {
    const tasks = [
      makeTask({ id: 't-live', title: 'Live refactor', status: 'in_progress' }),
      makeTask({ id: 't-done', title: 'Shipped fix', status: 'done' }),
    ]
    const mounted = render(
      <QueryClientProvider client={makeClient()}>
        <GraphView tasks={tasks} agents={[]} onTaskClick={vi.fn()} />
      </QueryClientProvider>,
    )
    expect(countIndicators(mounted.container)).toBe(1)
    expect(ancestorOfIndicatorAndTitle(mounted.container, 'Live refactor')).not.toBeNull()
    expect(ancestorOfIndicatorAndTitle(mounted.container, 'Shipped fix')).toBeNull()
  })
})

describe('SP-41 — Plans band surfaces the running indicator (running-plan reading, FLAGGED)', () => {
  it("a running plan's tile carries exactly one indicator inside it", () => {
    const running = makePlan({ id: 'plan-live', title: 'Release train', state: 'running' })
    const draft = makePlan({ id: 'plan-draft', title: 'Someday maybe', state: 'draft' })
    const mounted = render(
      <QueryClientProvider client={makeClient()}>
        <PlansFilterBand
          plans={[running, draft]}
          tasks={[]}
          agents={agents}
          selectedPlanId={null}
          onSelectPlan={vi.fn()}
          onNewPlan={vi.fn()}
          onEditPlan={vi.fn()}
          onClearPlan={vi.fn()}
          showNewPlanTile={false}
        />
      </QueryClientProvider>,
    )
    expect(countIndicators(mounted.container)).toBe(1)
    expect(ancestorOfIndicatorAndTitle(mounted.container, 'Release train')).not.toBeNull()
    expect(ancestorOfIndicatorAndTitle(mounted.container, 'Someday maybe')).toBeNull()
  })

  it('GUARD (green by construction today): no running plan → no indicator in the band', () => {
    // Labelled guard — see the board guard note.
    const draft = makePlan({ id: 'plan-draft', title: 'Someday maybe', state: 'draft' })
    const mounted = render(
      <QueryClientProvider client={makeClient()}>
        <PlansFilterBand
          plans={[draft]}
          tasks={[]}
          agents={agents}
          selectedPlanId={null}
          onSelectPlan={vi.fn()}
          onNewPlan={vi.fn()}
          onEditPlan={vi.fn()}
          onClearPlan={vi.fn()}
          showNewPlanTile={false}
        />
      </QueryClientProvider>,
    )
    expect(countIndicators(mounted.container)).toBe(0)
  })
})

describe('SP-41 — ONE standard indicator: every surface uses the same element contract', () => {
  it('board, list and band indicators share the exact same test id (no divergent copies)', () => {
    // FR-022's "ONE standard" invariant: the surfaces must not each invent
    // their own running badge. Same selector everywhere is the observable.
    const surfaces: HTMLElement[] = []
    const board = render(
      <QueryClientProvider client={makeClient()}>
        <BoardView tasks={[makeTask({ title: 'Live', status: 'in_progress' })]} plans={[]} agents={[]} altitude="top-level" onTaskClick={vi.fn()} onTaskMove={vi.fn()} onMoveRejected={vi.fn()} />
      </QueryClientProvider>,
    )
    surfaces.push(board.container)
    const list = render(
      <QueryClientProvider client={makeClient()}>
        <ListView tasks={[makeTask({ title: 'Live', status: 'in_progress' })]} agents={[{ id: 'jim', name: 'Jim' }]} onTaskClick={vi.fn()} />
      </QueryClientProvider>,
    )
    surfaces.push(list.container)
    const band = render(
      <QueryClientProvider client={makeClient()}>
        <PlansFilterBand plans={[makePlan({ title: 'Live', state: 'running' })]} tasks={[]} agents={agents} selectedPlanId={null} onSelectPlan={vi.fn()} onNewPlan={vi.fn()} onEditPlan={vi.fn()} onClearPlan={vi.fn()} showNewPlanTile={false} />
      </QueryClientProvider>,
    )
    surfaces.push(band.container)

    for (const surface of surfaces) {
      const found = surface.querySelectorAll('[data-testid="running-indicator"]')
      expect(found.length, 'each surface shows its running item via THE shared indicator').toBe(1)
    }
    void within
  })
})
