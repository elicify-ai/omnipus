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

function renderTab(workspaceId = 'ws-1') {
  return render(
    <QueryClientProvider client={makeClient()}>
      <WorkspaceGraphTab workspaceId={workspaceId} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  graphViewCalls.length = 0
  useWorkspacesStore.setState({ activePlanId: null })
  vi.mocked(fetchPlans).mockReset().mockResolvedValue([])
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
    expect(screen.getByText('50%')).toBeInTheDocument()
  })

  it('does not render the active-plan header when activePlanId does not resolve to a loaded plan', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([makePlan({ id: 'plan-other' })])
    useWorkspacesStore.setState({ activePlanId: 'plan-dead' })
    renderTab()

    await screen.findByTestId('graph-view-stub')
    expect(screen.queryByText('Running')).not.toBeInTheDocument()
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
