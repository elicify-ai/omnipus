// PlansBand.wave3.test.tsx — RED pack for side-panel-shell-spec.md Wave 3,
// SP-36:
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
// state machine: draft/approved/running/done/failed — openapi-types.ts
// Plan.state; a plan is "completed" exactly when state === 'done').
//
// RED↔GREEN DOM contracts declared by this pack:
//   - The "Show done" control: `data-testid="plans-show-done-toggle"`,
//     a real button carrying aria-pressed (a toggle per the spec's word).
//   - The overflow hint: `data-testid="plans-scroll-hint"` with the exact
//     wireframe string "scroll for more →".
//   - Overflow detection is a layout measurement (scrollWidth vs
//     clientWidth) re-evaluated when the box may have changed — the pack
//     drives it by stubbing those measurements on the band's scroller
//     (role="group" aria-label="Plans filter", the overflow-x-auto element
//     today) and firing a window resize. That stub is a process-edge mock
//     (browser layout metrics are the edge), not a mock of the unit.
//
// RED evidence (2026-10-04, read src/components/workspaces/PlansFilterBand.tsx
// in full): the band renders every plan tile it is handed with no state
// filtering, no "Show done" control, and no overflow hint. Every assertion
// below fails against this code.
//
// jsdom note (documented gap): actual pixel overflow at the 320px floor /
// 390px phone widths is browser-truth → UAT click-test rows. Here the
// overflow MECHANISM is what a unit test can prove.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, fireEvent, act } from '@testing-library/react'
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
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  } as Plan
}

// ADR-052 FR-039: memory_enabled is required on the wire Agent type (fixture
// mirrors PlansFilterBand.test.tsx).
const agents: Agent[] = [
  { revision: '0'.repeat(64), id: 'jim', name: 'Jim', type: 'core', locked: true, status: 'active', soul: '', timeout_seconds: 300, max_tool_iterations: 50, memory_enabled: true, needs_model: false },
]

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

import { PlansFilterBand } from './PlansFilterBand'

function renderBand(plans: Plan[], tasks: Parameters<typeof PlansFilterBand>[0]['tasks'] = []) {
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
  return { mounted, onSelectPlan }
}

beforeEach(() => {
  // jsdom has no window resize listener side effects to reset; each test
  // renders fresh anyway.
})

describe('Plans band — SP-36: done plans hidden by default behind a "Show done" toggle', () => {
  it('a done plan does NOT render a tile by default; draft plans still do', () => {
    const done = makePlan({ id: 'plan-done', title: 'Finished plan', state: 'done' })
    const draft = makePlan({ id: 'plan-draft', title: 'Active plan', state: 'draft' })
    const { mounted } = renderBand([draft, done])

    expect(mounted.getByTestId('plan-filter-tile-plan-draft')).toBeInTheDocument()
    expect(mounted.queryByTestId('plan-filter-tile-plan-done')).not.toBeInTheDocument()
  })

  it('the "Show done" toggle exists and starts off', () => {
    const { mounted } = renderBand([makePlan({ title: 'Active plan', state: 'draft' })])
    const toggle = mounted.getByTestId('plans-show-done-toggle')
    expect(toggle.tagName).toBe('BUTTON')
    expect(toggle.getAttribute('aria-pressed')).toBe('false')
  })

  it('turning the toggle on reveals done plans alongside the live ones', () => {
    const done = makePlan({ id: 'plan-done', title: 'Finished plan', state: 'done' })
    const draft = makePlan({ id: 'plan-draft', title: 'Active plan', state: 'draft' })
    const { mounted } = renderBand([draft, done])
    const toggle = mounted.getByTestId('plans-show-done-toggle')

    fireEvent.click(toggle)
    expect(toggle.getAttribute('aria-pressed')).toBe('true')
    expect(mounted.getByTestId('plan-filter-tile-plan-draft')).toBeInTheDocument()
    expect(mounted.getByTestId('plan-filter-tile-plan-done')).toBeInTheDocument()
  })

  it('turning the toggle back off hides done plans again (state round-trip)', () => {
    const done = makePlan({ id: 'plan-done', title: 'Finished plan', state: 'done' })
    const { mounted } = renderBand([done])
    const toggle = mounted.getByTestId('plans-show-done-toggle')

    // Intermediate assertion pins the hidden start state — the click below
    // must prove a transition, not a static condition.
    expect(mounted.queryByTestId('plan-filter-tile-plan-done')).not.toBeInTheDocument()

    fireEvent.click(toggle)
    expect(mounted.getByTestId('plan-filter-tile-plan-done')).toBeInTheDocument()

    fireEvent.click(toggle)
    expect(toggle.getAttribute('aria-pressed')).toBe('false')
    expect(mounted.queryByTestId('plan-filter-tile-plan-done')).not.toBeInTheDocument()
  })

  it('ONLY the done state hides: with a mixed set, exactly the non-done tiles render by default', () => {
    // Boundary of the hiding rule: the spec hides "completed" plans — the
    // canonical state machine's terminal-success value. Every non-done state
    // is live work or live failure and must stay surfaced. One render with
    // all five states makes this a counting oracle: exactly four tiles, and
    // never the done one. (Fails today: all five render.)
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
})

describe('Plans band — SP-36: "scroll for more →" hint on horizontal overflow', () => {
  /** The band's scroller is the role="group"[aria-label="Plans filter"]
   * element (its overflow-x-auto class is today's horizontal-scroll
   * mechanism the spec says is KEPT). */
  function scrollerOf(mounted: ReturnType<typeof renderBand>['mounted']): HTMLElement {
    return mounted.getByRole('group', { name: 'Plans filter' })
  }

  function stubMeasurements(el: HTMLElement, scrollWidth: number, clientWidth: number) {
    Object.defineProperty(el, 'scrollWidth', { configurable: true, value: scrollWidth })
    Object.defineProperty(el, 'clientWidth', { configurable: true, value: clientWidth })
  }

  it('GUARD (green by construction today — no hint exists yet): the hint stays absent while the strip fits its box', () => {
    // Labelled guard, not a RED test: it pins the no-false-positive half of
    // the hint contract once the feature lands.
    const { mounted } = renderBand([makePlan({ title: 'Only plan' })])
    const scroller = scrollerOf(mounted)
    stubMeasurements(scroller, 400, 461)
    act(() => {
      window.dispatchEvent(new Event('resize'))
    })
    expect(mounted.queryByTestId('plans-scroll-hint')).not.toBeInTheDocument()
  })

  it('the hint renders with the exact wireframe string when the strip overflows', () => {
    const { mounted } = renderBand([
      makePlan({ title: 'Plan one' }),
      makePlan({ title: 'Plan two' }),
      makePlan({ title: 'Plan three' }),
    ])
    const scroller = scrollerOf(mounted)
    // A 461px panel whose content needs 1200px — the default docked case the
    // wireframe §2A frame shows (~2 tiles visible before the fade).
    stubMeasurements(scroller, 1200, 461)
    act(() => {
      window.dispatchEvent(new Event('resize'))
    })
    const hint = mounted.getByTestId('plans-scroll-hint')
    expect(hint.textContent).toBe('scroll for more →')
  })

  it('the hint clears when the overflow goes away (e.g. the strip fits after a plan is removed)', () => {
    const { mounted } = renderBand([
      makePlan({ title: 'Plan one' }),
      makePlan({ title: 'Plan two' }),
    ])
    const scroller = scrollerOf(mounted)
    stubMeasurements(scroller, 1200, 461)
    act(() => {
      window.dispatchEvent(new Event('resize'))
    })
    expect(mounted.getByTestId('plans-scroll-hint')).toBeInTheDocument()

    stubMeasurements(scroller, 400, 461)
    act(() => {
      window.dispatchEvent(new Event('resize'))
    })
    expect(mounted.queryByTestId('plans-scroll-hint')).not.toBeInTheDocument()
  })

  it('GUARD (green by construction today): the band keeps its horizontal-scroll strip (side scroll retained — spec §10)', () => {
    // Labelled guard, not a RED test: the scroller is already horizontally
    // scrollable in today's code. It pins the "kept" half of SP-36 so GREEN
    // cannot trade the strip for wrapping while adding the new controls.
    const { mounted } = renderBand([makePlan({ title: 'Only plan' })])
    const scroller = scrollerOf(mounted)
    expect(scroller.className).toMatch(/overflow-x-auto/)
  })
})

// Deliberate gap, recorded for CHECK (not a test): the phone (390px
// full-bleed takeover) shows the same controls; at unit level the band is
// width-agnostic, so the phone-specific proof (the hint visible at device
// width) is a UAT click-test row, not a jsdom test.
