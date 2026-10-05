// RunningIndicator.surfaces.test.tsx — wave-3 join pack for
// side-panel-shell-spec.md Wave 3, FR-022 (SP-41) — the PLACEMENT half:
//
//   "Every running task surfaced in the Tasks panel's Board, List and Graph
//    views, and in the Plans band, MUST show ONE standard 'running'
//    indicator."  (FR-022)
//
// Oracle sources: spec §13 FR-022 / §10 Wave 3 last row; "running task" =
// a task whose status is `in_progress` — the canonical live-work state
// (src/lib/statusColors.ts: "in_progress — Forge Gold — live work", and the
// Graph's STATUS_VISUALS mark it animated); the Plans band's row of FR-022
// was RESOLVED by founder Q1 (2026-10-05, recorded verbatim in
// coordination/PANEL-INDICATOR-DECISION-20261005.md): the running PLAN's
// TILE carries the indicator, spinner ONLY — "on the plan tile no token
// count, only the spinning arrow animation" (PI1) — and running task nodes
// in the plan graph view MUST animate, the founder's explicit emphasis
// (PI2). Board/List/Graph keep FR-022's full treatment unchanged.
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
// JOIN STATUS (2026-10-05, work/side-panel-wave3-join-20261005) — STILL RED,
// and red for the RIGHT reason: the slice delivered the catalogued component
// (src/components/ui/RunningIndicator.tsx, entered in design-system/
// catalog.json) but wired it into NONE of the four surfaces. Production gap,
// frontend-lead owns the fix (verified by grep: no file outside
// src/components/ui/ references RunningIndicator):
//   - src/components/workspaces/TaskCard.tsx        (Board cards)
//   - src/components/workspaces/ListView.tsx        (List rows)
//   - src/components/workspaces/graph/TaskNode.tsx  (Graph nodes — still the
//     old `animate-pulse` treatment)
//   - src/components/workspaces/PlansFilterBand.tsx (Plans band tiles)
// FR-022 requires all four. The Board/List/Graph tests below fail with
// "0 !== 1" — the honest red naming the missing wiring; they pass exactly
// when the real surfaces render the real indicator. No assertion was
// weakened to reach that state.

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
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  } as Plan
}

const agents: Agent[] = [
  // max_tool_iterations_source/'override_ignored' are required Agent wire
  // fields (generated openapi contract) — the fixture carries an agent on
  // the global default cap, not an override.
  { revision: '0'.repeat(64), id: 'jim', name: 'Jim', type: 'core', locked: true, status: 'active', soul: '', timeout_seconds: 300, max_tool_iterations: 50, max_tool_iterations_source: 'global', max_tool_iterations_override_ignored: false, memory_enabled: true, needs_model: false },
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

describe('SP-41 — Graph surfaces the running indicator on running nodes (founder PI2: these MUST animate)', () => {
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

describe('SP-41 — Plans band (founder decision PI1, 2026-10-05): running plan tile shows the spinner ONLY — no token count', () => {
  // RESOLVED by founder Q1 (coordination/PANEL-INDICATOR-DECISION-20261005.md):
  // "on the plan tile no token count, only the spinning arrow animation".
  // The band's placement oracle is the running PLAN's tile; its TREATMENT is
  // spinner-only — FR-022's token-count half explicitly does NOT apply here
  // (while Board/List/Graph keep the full FR-022 treatment unchanged).
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

  it('the band indicator carries NO token count — the spinner is the whole treatment (PI1)', () => {
    // The founder's exact carve-out: "no token count". The shared component's
    // "{n} tok" half must not appear on the plan tile — not even as a
    // placeholder or aggregate. The INDICATOR element's own text is the
    // oracle (the tile's other numbers — task progress — are not the
    // indicator and are not governed by PI1).
    const running = makePlan({ id: 'plan-live', title: 'Release train', state: 'running' })
    const mounted = render(
      <QueryClientProvider client={makeClient()}>
        <PlansFilterBand
          plans={[running]}
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
    const indicator = mounted.container.querySelector('[data-testid="running-indicator"]')
    expect(indicator, 'the running plan tile carries the indicator').not.toBeNull()
    expect(indicator?.querySelector('svg.animate-spin')).not.toBeNull()
    expect(indicator?.textContent ?? '').not.toMatch(/tok/i)
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

describe('SP-41 — ONE standard indicator: board/list share the full treatment; the band is the PI1 spinner-only carve-out', () => {
  it('board and list indicators share the exact same element contract (no divergent copies)', () => {
    // FR-022's "ONE standard" invariant for the task surfaces: no surface
    // invents its own running badge. Same selector everywhere is the
    // observable.
    const board = render(
      <QueryClientProvider client={makeClient()}>
        <BoardView tasks={[makeTask({ title: 'Live', status: 'in_progress' })]} plans={[]} agents={[]} altitude="top-level" onTaskClick={vi.fn()} onTaskMove={vi.fn()} onMoveRejected={vi.fn()} />
      </QueryClientProvider>,
    )
    const list = render(
      <QueryClientProvider client={makeClient()}>
        <ListView tasks={[makeTask({ title: 'Live', status: 'in_progress' })]} agents={[{ id: 'jim', name: 'Jim' }]} onTaskClick={vi.fn()} />
      </QueryClientProvider>,
    )
    for (const surface of [board.container, list.container]) {
      const found = surface.querySelectorAll('[data-testid="running-indicator"]')
      expect(found.length, 'each task surface shows its running item via THE shared indicator').toBe(1)
    }
    void within
  })

  it('the band stays inside the ONE-indicator element contract while omitting only the count (PI1 carve-out)', () => {
    // The carve-out narrows the TREATMENT (no token count), not the element:
    // the band's indicator is still the same catalogued element (same test
    // id, same spinner), so a second divergent badge cannot sneak in.
    const band = render(
      <QueryClientProvider client={makeClient()}>
        <PlansFilterBand plans={[makePlan({ title: 'Live', state: 'running' })]} tasks={[]} agents={agents} selectedPlanId={null} onSelectPlan={vi.fn()} onNewPlan={vi.fn()} onEditPlan={vi.fn()} onClearPlan={vi.fn()} showNewPlanTile={false} />
      </QueryClientProvider>,
    )
    const indicators = band.container.querySelectorAll('[data-testid="running-indicator"]')
    expect(indicators.length).toBe(1)
    expect((indicators[0] as HTMLElement).textContent ?? '').not.toMatch(/tok/i)
  })
})
