// PlansBand.wave3.test.tsx — wave-3 join pack for side-panel-shell-spec.md
// Wave 3, SP-36:
//
//   "Plans band: tile strip + side scroll kept; done plans hidden by default
//    + toggle; 'scroll for more →' hint (incl. phone)."  (§13 SP-36; §10
//    Wave 3 Tasks — Plans band row)
//
//   Spec §10: "Keeps its tile strip with side (horizontal) scroll; completed
//   plans hidden by default behind a 'Show done' toggle; a small 'scroll for
//   more →' hint shows when the strip overflows, on phone too."
//
//   Wireframe §2A (461px default): the toggle and the hint "both still work
//   at this width"; the 320px floor frame repeats it. §7 phone frames keep
//   the band with the same controls.
//
// Oracle sources: spec §10 Wave 3 / §13 SP-36, wireframe §2A + §7.
// "Done plans" = plans in the wire `done` state (canonical 5-value plan
// state machine — openapi-types.ts Plan.state; a plan is "completed" exactly
// when state === 'done').
//
// JOIN STATUS (2026-10-05, work/side-panel-wave3-join-20261005) — delivered
// seam renegotiations (authorised by the 8773803cf pack's header), spec
// expectations unchanged:
//
//   1. The "Show done" control is delivered as a real SWITCH
//      (`role="switch"`, aria-checked, accessible name "Show done plans",
//      labelled "Show done (N)") — the spec's word is "toggle", and a switch
//      IS the toggle primitive; the count makes the hidden state visible.
//      Its a11y contract (name + checked state + reveals on check) is what
//      this pack pins; the old pack's button/aria-pressed shape is retired
//      with the renegotiated seam.
//   2. The overflow hint keeps the exact wireframe string "scroll for more
//      →" and appears only while the strip measures over its box — the
//      component measures the strip element itself (scrollWidth >
//      clientWidth + 2, the wireframe's own tolerance) via ResizeObserver
//      plus a re-check whenever the tile set changes. jsdom has no layout,
//      so the pack stubs the strip's two layout metrics (a process-edge
//      mock of browser measurement, same boundary the RED pack declared)
//      and drives the re-check through the component's real tile-set input
//      (a rerender with a new plans array) — never through a fabricated
//      window-resize event the component does not listen for.
//   3. The tile test ids (`plan-filter-tile-<id>`) are unchanged from the
//      RED pack.
//
// jsdom note (documented gap, unchanged from RED): actual pixel overflow at
// the 320px floor / 390px phone widths is browser-truth → UAT click-test
// rows. Here the overflow MECHANISM is what a unit test can prove.

import { describe, it, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Plan, Agent } from '@/lib/api'

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

// ADR-052 FR-039: memory_enabled is required on the wire Agent type (fixture
// mirrors PlansFilterBand.test.tsx). max_tool_iterations_source/'override_ignored'
// are required Agent wire fields (generated openapi contract) — the fixture
// carries an agent on the global default cap, not an override.
const agents: Agent[] = [
  { revision: '0'.repeat(64), id: 'jim', name: 'Jim', type: 'core', locked: true, status: 'active', soul: '', timeout_seconds: 300, max_tool_iterations: 50, max_tool_iterations_source: 'global', max_tool_iterations_override_ignored: false, memory_enabled: true, needs_model: false, figure: 'Omnipus', role: 'general' },
]

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

import { PlansFilterBand } from './PlansFilterBand'

type BandProps = Parameters<typeof PlansFilterBand>[0]

/** Render the band; returns a rerender that feeds a NEW plans array (the
 * component re-measures overflow whenever the tile set changes). */
function renderBand(plans: Plan[], tasks: BandProps['tasks'] = []) {
  const onSelectPlan = vi.fn()
  const mounted = render(
    <QueryClientProvider client={makeClient()}>
      <PlansFilterBand
        plans={plans}
        tasks={tasks}
        agents={agents}
        selectedPlanId={null}
        onSelectPlan={onSelectPlan}
        onNewPlan={vi.fn()}
        onEditPlan={vi.fn()}
        onClearPlan={vi.fn()}
        showNewPlanTile={false}
      />
    </QueryClientProvider>,
  )
  const rerenderWith = (nextPlans: Plan[]) =>
    mounted.rerender(
      <QueryClientProvider client={makeClient()}>
        <PlansFilterBand
          plans={nextPlans}
          tasks={tasks}
          agents={agents}
          selectedPlanId={null}
          onSelectPlan={onSelectPlan}
          onNewPlan={vi.fn()}
          onEditPlan={vi.fn()}
          onClearPlan={vi.fn()}
          showNewPlanTile={false}
        />
      </QueryClientProvider>,
    )
  return { mounted, rerenderWith, onSelectPlan }
}

/** The band's horizontal strip: the overflow-x-auto scroller inside the
 * "Plans filter" group — the KEPT side-scroll mechanism (spec §10). */
function scrollerOf(mounted: ReturnType<typeof renderBand>['mounted']): HTMLElement {
  const group = mounted.getByRole('group', { name: 'Plans filter' })
  const strip = group.querySelector('.overflow-x-auto')
  expect(strip, 'the band keeps a horizontally scrollable tile strip').not.toBeNull()
  return strip as HTMLElement
}

function stubMeasurements(el: HTMLElement, scrollWidth: number, clientWidth: number) {
  Object.defineProperty(el, 'scrollWidth', { configurable: true, value: scrollWidth })
  Object.defineProperty(el, 'clientWidth', { configurable: true, value: clientWidth })
}

describe('Plans band — SP-36: done plans hidden by default behind a "Show done" toggle', () => {
  it('a done plan does NOT render a tile by default; draft plans still do', () => {
    const done = makePlan({ id: 'plan-done', title: 'Finished plan', state: 'done' })
    const draft = makePlan({ id: 'plan-draft', title: 'Active plan', state: 'draft' })
    const { mounted } = renderBand([draft, done])

    expect(mounted.getByTestId('plan-filter-tile-plan-draft')).toBeInTheDocument()
    expect(mounted.queryByTestId('plan-filter-tile-plan-done')).not.toBeInTheDocument()
  })

  it('the "Show done" toggle exists as a switch, starts unchecked, and carries its count', () => {
    const done = makePlan({ id: 'plan-done', title: 'Finished plan', state: 'done' })
    const { mounted } = renderBand([done])
    const toggle = mounted.getByRole('switch', { name: 'Show done plans' })
    expect(toggle.getAttribute('aria-checked')).toBe('false')
    // The hidden state is visible: the label names how many are hidden.
    expect(mounted.getByText('Show done (1)')).toBeInTheDocument()
  })

  it('turning the toggle on reveals done plans alongside the live ones', () => {
    const done = makePlan({ id: 'plan-done', title: 'Finished plan', state: 'done' })
    const draft = makePlan({ id: 'plan-draft', title: 'Active plan', state: 'draft' })
    const { mounted } = renderBand([draft, done])
    const toggle = mounted.getByRole('switch', { name: 'Show done plans' })

    fireEvent.click(toggle)
    expect(toggle.getAttribute('aria-checked')).toBe('true')
    expect(mounted.getByTestId('plan-filter-tile-plan-draft')).toBeInTheDocument()
    expect(mounted.getByTestId('plan-filter-tile-plan-done')).toBeInTheDocument()
  })

  it('turning the toggle back off hides done plans again (state round-trip)', () => {
    const done = makePlan({ id: 'plan-done', title: 'Finished plan', state: 'done' })
    const { mounted } = renderBand([done])
    const toggle = mounted.getByRole('switch', { name: 'Show done plans' })

    // Intermediate assertion pins the hidden start state — the click below
    // must prove a transition, not a static condition.
    expect(mounted.queryByTestId('plan-filter-tile-plan-done')).not.toBeInTheDocument()

    fireEvent.click(toggle)
    expect(mounted.getByTestId('plan-filter-tile-plan-done')).toBeInTheDocument()

    fireEvent.click(toggle)
    expect(toggle.getAttribute('aria-checked')).toBe('false')
    expect(mounted.queryByTestId('plan-filter-tile-plan-done')).not.toBeInTheDocument()
  })

  it('ONLY the done state hides: with a mixed set, exactly the non-done tiles render by default', () => {
    // Boundary of the hiding rule: the spec hides "completed" plans — the
    // canonical state machine's terminal-success value. Every non-done state
    // is live work or live failure and must stay surfaced. One render with
    // all five states makes this a counting oracle: exactly four tiles, and
    // never the done one.
    const draft = makePlan({ id: 'plan-draft', title: 'Draft plan', state: 'draft' })
    const approved = makePlan({ id: 'plan-approved', title: 'Approved plan', state: 'approved' })
    const running = makePlan({ id: 'plan-running', title: 'Running plan', state: 'running' })
    const failed = makePlan({ id: 'plan-failed', title: 'Failed plan', state: 'failed' })
    const done = makePlan({ id: 'plan-done', title: 'Finished plan', state: 'done' })
    const { mounted } = renderBand([draft, approved, running, failed, done])

    expect(mounted.getByTestId('plan-filter-tile-plan-draft')).toBeInTheDocument()
    expect(mounted.getByTestId('plan-filter-tile-plan-approved')).toBeInTheDocument()
    expect(mounted.getByTestId('plan-filter-tile-plan-running')).toBeInTheDocument()
    expect(mounted.getByTestId('plan-filter-tile-plan-failed')).toBeInTheDocument()
    expect(mounted.queryByTestId('plan-filter-tile-plan-done')).not.toBeInTheDocument()
  })

  it('the switch does not render at all when nothing is done (a bare "Show done (0)" would be noise)', () => {
    const { mounted } = renderBand([makePlan({ title: 'Active plan', state: 'draft' })])
    expect(mounted.queryByRole('switch', { name: 'Show done plans' })).not.toBeInTheDocument()
  })
})

describe('Plans band — SP-36: "scroll for more →" hint on horizontal overflow', () => {
  it('GUARD (kept mechanism): the band keeps its horizontal-scroll strip (side scroll retained — spec §10)', () => {
    // Labelled guard, not a RED test: the scroller is already horizontally
    // scrollable. It pins the "kept" half of SP-36 so GREEN cannot trade the
    // strip for wrapping while adding the new controls.
    const { mounted } = renderBand([makePlan({ title: 'Only plan' })])
    expect(scrollerOf(mounted).className).toMatch(/overflow-x-auto/)
  })

  it('the hint stays absent while the strip fits its box (no false positive)', () => {
    const { mounted, rerenderWith } = renderBand([makePlan({ title: 'Only plan' })])
    const scroller = scrollerOf(mounted)
    stubMeasurements(scroller, 400, 461)
    // Re-measure via the component's real tile-set input (a new plans array
    // re-runs its overflow check) — the strip fits, so no hint.
    rerenderWith([makePlan({ title: 'Only plan 2' })])
    expect(mounted.queryByText('scroll for more →')).not.toBeInTheDocument()
  })

  it('the hint renders with the exact wireframe string when the strip overflows', () => {
    const plans = [
      makePlan({ id: 'plan-1', title: 'Plan one' }),
      makePlan({ id: 'plan-2', title: 'Plan two' }),
      makePlan({ id: 'plan-3', title: 'Plan three' }),
    ]
    const { mounted, rerenderWith } = renderBand([plans[0]])
    const scroller = scrollerOf(mounted)
    // A 461px panel whose content needs 1200px — the default docked case the
    // wireframe §2A frame shows (~2 tiles visible before the fade).
    stubMeasurements(scroller, 1200, 461)
    rerenderWith(plans)
    const hint = mounted.getByText('scroll for more →')
    expect(hint.textContent).toBe('scroll for more →')
  })

  it('the hint clears when the overflow goes away (e.g. the strip fits after a plan is removed)', () => {
    const two = [
      makePlan({ id: 'plan-1', title: 'Plan one' }),
      makePlan({ id: 'plan-2', title: 'Plan two' }),
    ]
    const { mounted, rerenderWith } = renderBand([two[0]])
    const scroller = scrollerOf(mounted)
    stubMeasurements(scroller, 1200, 461)
    rerenderWith(two)
    expect(mounted.getByText('scroll for more →')).toBeInTheDocument()

    stubMeasurements(scroller, 400, 461)
    rerenderWith([two[0]])
    expect(mounted.queryByText('scroll for more →')).not.toBeInTheDocument()
  })
})

// Deliberate gap, recorded for CHECK (not a test): the phone (390px
// full-bleed takeover) shows the same controls; at unit level the band is
// width-agnostic, so the phone-specific proof (the hint visible at device
// width) is a UAT click-test row, not a jsdom test.
