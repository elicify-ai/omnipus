/**
 * WorkspaceGraphTab.test.tsx
 *
 * The Graph tab's plan switcher, active-plan header, and the plan-scope
 * guard against an unresolvable `activePlanId` (review-gate fix #3): when
 * `activePlanId` is set but doesn't resolve to a loaded plan (plansError,
 * still loading, or a just-cleared plan), the canvas must not silently
 * plan-scope to a dead id and show the misleading "This plan has no
 * dependencies yet" empty state.
 *
 * GraphView itself (the React Flow DAG canvas) is stubbed — its rendering is
 * covered by GraphView.test.tsx / GraphView.unlinkedTray.test.tsx; here we
 * only assert the props WorkspaceGraphTab feeds it and the tab's own chrome
 * (plan switcher, header, error banners).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClientProvider, QueryClient } from '@tanstack/react-query'
import { WorkspaceGraphTab } from './WorkspaceGraphTab'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { Plan, Task } from '@/lib/api'
import { PLAN_CANCELLED_COLOR, planDisplayColor } from '@/lib/planStateColors'

// ── Mocks ────────────────────────────────────────────────────────────────────

const graphViewCalls: Array<{ planId: string | null | undefined; tasksLength: number }> = []
vi.mock('./graph/GraphView', () => ({
  GraphView: (props: { planId?: string | null; tasks: Task[] }) => {
    graphViewCalls.push({ planId: props.planId, tasksLength: props.tasks.length })
    return <div data-testid="graph-view-stub" data-plan-id={props.planId ?? 'null'} />
  },
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchTasks: vi.fn().mockResolvedValue([]),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchPlans: vi.fn(),
  }
})

import { fetchPlans } from '@/lib/api'

// Radix Select needs these jsdom polyfills to open (same gap noted across
// the other Select-driven test files in this directory).
beforeEach(() => {
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {}
  }
})

// ── Fixtures ─────────────────────────────────────────────────────────────────

function makePlan(overrides: Partial<Plan> = {}): Plan {
  return {
    id: 'plan-1',
    workspace_id: 'ws-1',
    title: 'Launch',
    state: 'draft',
    plan_phase: 'idle',
    owner_agent_id: 'jim',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderTab(workspaceId = 'ws-1', opts: { hidePlanSelector?: boolean } = {}) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <WorkspaceGraphTab workspaceId={workspaceId} hidePlanSelector={opts.hidePlanSelector} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  graphViewCalls.length = 0
  useWorkspacesStore.setState({ activePlanId: null })
  vi.mocked(fetchPlans).mockReset().mockResolvedValue([])
})

// ── Embedded toolbar (hidePlanSelector) ──────────────────────────────────────

describe('WorkspaceGraphTab — embedded toolbar (hidePlanSelector)', () => {
  it('skips the toolbar row entirely when embedded with no active plan (no stray line above the canvas)', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([makePlan({ id: 'plan-1', title: 'Launch' })])
    renderTab('ws-1', { hidePlanSelector: true })
    await screen.findByTestId('graph-view-stub')
    // Plan selector is hidden and there's no active plan → the whole toolbar row
    // is skipped, so neither the "By plan" label nor the Select renders.
    expect(screen.queryByText('By plan')).toBeNull()
    expect(screen.queryByLabelText(/filter the graph by plan/i)).toBeNull()
  })

  it('still shows the active-plan header (goal/progress) when embedded WITH a resolvable active plan', async () => {
    useWorkspacesStore.setState({ activePlanId: 'plan-1' })
    vi.mocked(fetchPlans).mockResolvedValue([makePlan({ id: 'plan-1', title: 'Launch', goal: 'Ship it' })])
    renderTab('ws-1', { hidePlanSelector: true })
    await screen.findByTestId('graph-view-stub')
    // The plan selector stays hidden, but the toolbar renders because a plan is
    // active — its goal/progress must still show.
    expect(screen.queryByText('By plan')).toBeNull()
    await waitFor(() => expect(screen.getByText('Ship it')).toBeInTheDocument())
  })
})

// ── Plan switcher ────────────────────────────────────────────────────────────

describe('WorkspaceGraphTab — plan switcher', () => {
  it('selecting a plan sets activePlanId; selecting "All tasks" maps back to null', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([makePlan({ id: 'plan-1', title: 'Launch' })])
    renderTab()
    await screen.findByTestId('graph-view-stub')

    fireEvent.click(screen.getByRole('combobox', { name: 'Filter the graph by plan' }))
    const planOption = await screen.findByRole('option', { name: 'Launch' })
    fireEvent.pointerDown(planOption, { pointerId: 1, button: 0 })
    fireEvent.click(planOption)

    await waitFor(() => expect(useWorkspacesStore.getState().activePlanId).toBe('plan-1'))

    fireEvent.click(screen.getByRole('combobox', { name: 'Filter the graph by plan' }))
    const allOption = await screen.findByRole('option', { name: 'All tasks' })
    fireEvent.pointerDown(allOption, { pointerId: 1, button: 0 })
    fireEvent.click(allOption)

    await waitFor(() => expect(useWorkspacesStore.getState().activePlanId).toBeNull())
  })
})

// ── Active-plan header ───────────────────────────────────────────────────────

describe('WorkspaceGraphTab — active-plan header', () => {
  it('renders the state chip, goal, and progress when activePlanId resolves to a loaded plan', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([
      makePlan({ id: 'plan-9', title: 'Launch', state: 'running', goal: 'Ship v1', progress: 0.5 }),
    ])
    useWorkspacesStore.setState({ activePlanId: 'plan-9' })
    renderTab()

    expect(await screen.findByText('Running')).toBeInTheDocument()
    expect(screen.getByText('Ship v1')).toBeInTheDocument()
    expect(screen.getByText(/50% done/)).toBeInTheDocument()
  })

  it('does not render the active-plan header when activePlanId does not resolve to a loaded plan', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([makePlan({ id: 'plan-other' })])
    useWorkspacesStore.setState({ activePlanId: 'plan-dead' })
    renderTab()

    await screen.findByTestId('graph-view-stub')
    expect(screen.queryByText('Running')).not.toBeInTheDocument()
  })

  // Gate-2 finding #1 regression: WorkspaceGraphTab.tsx:248 builds the plan
  // info strip's state-chip tint fill as `` `${planDisplayColor(activePlan)}1a` ``
  // — a bare string concat, not a CSS function call. If PLAN_CANCELLED_COLOR
  // were ever a `var(--x)` reference again, the result
  // (`var(--color-warning)1a`) is not valid CSS (`var()` doesn't accept a
  // trailing token), so the whole `backgroundColor` declaration would be
  // silently dropped and the "Cancelled" chip would render with orange text
  // but NO tint — inconsistent with the red "Failed" chip elsewhere. jsdom
  // won't reliably validate/reject that invalid CSS for us, so this asserts
  // directly on the STRING the component builds, not on DOM style-parsing
  // behavior.
  it('PLAN_CANCELLED_COLOR is a literal hex, not a var(...) reference, so the "Cancelled" chip tint stays valid CSS', () => {
    expect(PLAN_CANCELLED_COLOR).not.toMatch(/^var\(/)
    expect(PLAN_CANCELLED_COLOR).toMatch(/^#[0-9a-fA-F]{6}$/)
    const cancelledTint = `${planDisplayColor(makePlan({ state: 'failed', failed_reason: 'stopped_by_user' }))}1a`
    expect(cancelledTint).toMatch(/^#[0-9a-fA-F]{8}$/)
  })

  it('renders the "Cancelled" state chip (not "Failed") for a user-stopped active plan', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([
      makePlan({ id: 'plan-9', title: 'Launch', state: 'failed', failed_reason: 'stopped_by_user' }),
    ])
    useWorkspacesStore.setState({ activePlanId: 'plan-9' })
    renderTab()

    expect(await screen.findByText('Cancelled')).toBeInTheDocument()
    expect(screen.queryByText('Failed')).not.toBeInTheDocument()
  })
})

// ADR-053 FE-2 §7 (D7) — the Graph view surfaces the active plan's runtime
// sub-phase as a chip beside the state badge, so an operator landing on the
// Graph (after a plan tile auto-switches to it) sees the durable re-planning
// hold without leaving the canvas.
describe('WorkspaceGraphTab — plan-phase chip (ADR-053 FE-2 §7)', () => {
  it('renders the re-planning warning chip when the active plan is awaiting owner correction', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([
      makePlan({ id: 'plan-rp', title: 'Launch', state: 'running', plan_phase: 'awaiting_owner_correction' }),
    ])
    useWorkspacesStore.setState({ activePlanId: 'plan-rp' })
    renderTab()

    const chip = await screen.findByTestId('plan-phase-chip-plan-rp')
    expect(chip).toHaveTextContent('Re-planning — awaiting owner correction')
  })

  it('renders a quieter capitalized sub-phase chip while judging', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([
      makePlan({ id: 'plan-j', title: 'Launch', state: 'running', plan_phase: 'judging' }),
    ])
    useWorkspacesStore.setState({ activePlanId: 'plan-j' })
    renderTab()

    const chip = await screen.findByTestId('plan-phase-chip-plan-j')
    expect(chip).toHaveTextContent('Judging')
  })

  it('renders no phase chip for a running plan on the idle sub-phase', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([
      makePlan({ id: 'plan-i', title: 'Launch', state: 'running', plan_phase: 'idle' }),
    ])
    useWorkspacesStore.setState({ activePlanId: 'plan-i' })
    renderTab()

    await screen.findByTestId('graph-view-stub')
    expect(screen.queryByTestId('plan-phase-chip-plan-i')).not.toBeInTheDocument()
  })
})

// S2 UAT finding — the ONLY explanation of `awaiting_owner_correction` used
// to be a hover-only `title` tooltip on the chip above (dead on touch, easy
// to miss). This is now always-visible plain-language text.
describe('WorkspaceGraphTab — plan-phase explanation (S2 UAT — replaces the hover-only tooltip)', () => {
  it('renders an always-visible plain-language explanation for the warning chip, not a title tooltip', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([
      makePlan({ id: 'plan-rp2', title: 'Launch', state: 'running', plan_phase: 'awaiting_owner_correction' }),
    ])
    useWorkspacesStore.setState({ activePlanId: 'plan-rp2' })
    renderTab()

    const explanation = await screen.findByTestId('plan-phase-explanation-plan-rp2')
    expect(explanation).not.toHaveAttribute('title')
    expect(explanation.textContent).toMatch(/no in-app action/i)
    expect(explanation.textContent).toMatch(/stop this plan/i)
    // Does not name the three corrective verbs as if they were reachable —
    // they have no exposed route/control (see planStateColors.ts).
    expect(explanation.textContent).not.toMatch(/append a tail|supersede|targeted.retry/i)
  })

  it('renders no explanation for a quieter info sub-phase (e.g. judging)', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([
      makePlan({ id: 'plan-j2', title: 'Launch', state: 'running', plan_phase: 'judging' }),
    ])
    useWorkspacesStore.setState({ activePlanId: 'plan-j2' })
    renderTab()

    await screen.findByTestId('plan-phase-chip-plan-j2')
    expect(screen.queryByTestId('plan-phase-explanation-plan-j2')).not.toBeInTheDocument()
  })
})

// S3 UAT finding — `failed_reason` was on the wire but never rendered; the
// strip showed only the word "Failed" with no explanation.
describe('WorkspaceGraphTab — failed_reason rendering (S3 UAT)', () => {
  it('renders the human-readable failed_reason for a genuinely-failed active plan', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([
      makePlan({ id: 'plan-fail', title: 'Launch', state: 'failed', failed_reason: 'judge_rounds_exhausted' }),
    ])
    useWorkspacesStore.setState({ activePlanId: 'plan-fail' })
    renderTab()

    expect(await screen.findByText('Failed')).toBeInTheDocument()
    expect(screen.getByTestId('plan-failed-reason-plan-fail')).toHaveTextContent('judge rounds exhausted')
  })

  it('does not render a redundant reason line for a cancelled (user-stopped) plan — "Cancelled" already says why', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([
      makePlan({ id: 'plan-cancel', title: 'Launch', state: 'failed', failed_reason: 'stopped_by_user' }),
    ])
    useWorkspacesStore.setState({ activePlanId: 'plan-cancel' })
    renderTab()

    expect(await screen.findByText('Cancelled')).toBeInTheDocument()
    expect(screen.queryByTestId('plan-failed-reason-plan-cancel')).not.toBeInTheDocument()
  })
})

// ── Plan-scope guard against an unresolvable plan (review-gate fix #3) ──────

describe('WorkspaceGraphTab — plan-scope guard', () => {
  it('passes planId=null to GraphView when activePlanId is stale (not in the loaded plans list)', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([makePlan({ id: 'plan-real' })])
    // Simulates plansError / the create-plan refetch race / a just-cleared
    // plan: the store still points at a plan id the loaded list no longer has.
    useWorkspacesStore.setState({ activePlanId: 'plan-cleared' })
    renderTab()

    const stub = await screen.findByTestId('graph-view-stub')
    expect(stub).toHaveAttribute('data-plan-id', 'null')
  })

  it('passes the real planId through once it resolves against the loaded plans list', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([makePlan({ id: 'plan-real' })])
    useWorkspacesStore.setState({ activePlanId: 'plan-real' })
    renderTab()

    const stub = await screen.findByTestId('graph-view-stub')
    expect(stub).toHaveAttribute('data-plan-id', 'plan-real')
  })

  it('passes planId=null while plans are still loading, even with an activePlanId set', async () => {
    vi.mocked(fetchPlans).mockImplementation(() => new Promise(() => {})) // never resolves
    useWorkspacesStore.setState({ activePlanId: 'plan-real' })
    renderTab()

    const stub = await screen.findByTestId('graph-view-stub')
    expect(stub).toHaveAttribute('data-plan-id', 'null')
  })
})

// ── Error banners ────────────────────────────────────────────────────────────

describe('WorkspaceGraphTab — error banners', () => {
  it('shows a plans-failed banner on plansError', async () => {
    vi.mocked(fetchPlans).mockRejectedValue(new Error('boom'))
    renderTab()
    expect(await screen.findByText(/plans failed to load/i)).toBeInTheDocument()
  })

  it('shows an agents-failed banner on agentsError', async () => {
    const api = await import('@/lib/api')
    vi.mocked(api.fetchAgents).mockRejectedValueOnce(new Error('boom'))
    renderTab()
    expect(await screen.findByText(/agent details failed to load/i)).toBeInTheDocument()
  })
})
