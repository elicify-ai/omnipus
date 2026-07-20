/**
 * WorkspaceTasksTab.test.tsx
 *
 * Mutation wiring for the plan-lane ⋯ menu actions surfaced through the
 * REAL component tree (WorkspaceTasksTab → BoardView → PlanLaneHeader), not
 * just PlanLaneHeader's own callback props (see PlanLaneHeader.test.tsx for
 * that layer). Covers:
 *   - Approve calls `approvePlan(planId)` and toasts on success.
 *   - A 400 from Approve with a per-task payload surfaces the per-task
 *     "needs acceptance criteria" reasons via toast (review-gate fix #2) —
 *     not the generic "Failed to approve plan" fallback.
 *   - Stop (after confirm) calls `stopPlan(planId)` and toasts on
 *     success/error.
 *   - Clear (after confirm) calls `deletePlan(planId)` and toasts on
 *     success/error.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { WorkspaceTasksTab } from './WorkspaceTasksTab'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { ApiError } from '@/lib/api-error'
import type { Plan } from '@/lib/api'

// ── Mocks ────────────────────────────────────────────────────────────────────

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => mockNavigate,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchTasks: vi.fn(),
    fetchPlans: vi.fn(),
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchWorkspaceDelegation: vi.fn().mockRejectedValue(new Error('not mocked')),
    approvePlan: vi.fn(),
    stopPlan: vi.fn(),
    deletePlan: vi.fn(),
  }
})

import { fetchTasks, fetchPlans, approvePlan, stopPlan, deletePlan } from '@/lib/api'

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

// Minimal stub so the ⋯ trigger's content renders without Radix portal
// internals (mirrors PlanLaneHeader.test.tsx's DropdownMenu stub).
vi.mock('@/components/ui/dropdown-menu', () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) =>
    asChild ? <>{children}</> : <div>{children}</div>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="plan-lane-menu">{children}</div>
  ),
  DropdownMenuItem: ({
    children,
    onClick,
    disabled,
    className,
  }: {
    children: React.ReactNode
    onClick?: () => void
    disabled?: boolean
    className?: string
  }) => (
    <button type="button" onClick={onClick} disabled={disabled} className={className}>
      {children}
    </button>
  ),
  DropdownMenuSeparator: () => <hr />,
}))

// ── Fixtures ─────────────────────────────────────────────────────────────────

function makePlan(overrides: Partial<Plan> = {}): Plan {
  return {
    id: 'plan-a',
    workspace_id: 'ws-1',
    title: 'Plan A',
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
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderTab(workspaceId = 'ws-1') {
  return render(
    <QueryClientProvider client={makeClient()}>
      <WorkspaceTasksTab workspaceId={workspaceId} mode="board" />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockNavigate.mockReset()
  mockAddToast.mockReset()
  // Real Zustand store — reset between tests (module-level singleton).
  useWorkspacesStore.setState({ collapsedLanes: {}, activeTagFilter: null, boardAltitude: 'top-level', activePlanId: null })
  vi.mocked(fetchTasks).mockReset().mockResolvedValue([])
  vi.mocked(fetchPlans).mockReset().mockResolvedValue([])
  vi.mocked(approvePlan).mockReset()
  vi.mocked(stopPlan).mockReset()
  vi.mocked(deletePlan).mockReset()
})

// ── Approve ──────────────────────────────────────────────────────────────────

describe('WorkspaceTasksTab — plan lane Approve', () => {
  it('calls approvePlan with the plan id and toasts on success', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ state: 'draft' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    vi.mocked(approvePlan).mockResolvedValue({ ...plan, state: 'approved' })

    renderTab()
    await screen.findByTestId('plan-lane-header-plan-a')

    await user.click(screen.getByRole('button', { name: 'Approve' }))

    await waitFor(() => expect(approvePlan).toHaveBeenCalledWith('plan-a'))
    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success', message: 'Plan approved' }),
      ),
    )
  })

  it('a 400 with a per-task payload surfaces the per-task reasons via toast (review-gate fix #2)', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ state: 'draft' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    const body = JSON.stringify({
      task_errors: [{ task_id: 't1', title: 'Write report', reason: 'missing acceptance criteria' }],
    })
    vi.mocked(approvePlan).mockRejectedValue(new ApiError(400, 'Bad request', { body }))

    renderTab()
    await screen.findByTestId('plan-lane-header-plan-a')

    await user.click(screen.getByRole('button', { name: 'Approve' }))

    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({
          variant: 'error',
          message: expect.stringContaining('Write report: missing acceptance criteria'),
        }),
      ),
    )
    // Not the generic fallback — the per-task reason is the whole point.
    expect(mockAddToast).not.toHaveBeenCalledWith(
      expect.objectContaining({ message: 'Failed to approve plan' }),
    )
  })

  it('a plain (non-per-task) error still falls back to the generic toast', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ state: 'draft' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    vi.mocked(approvePlan).mockRejectedValue(new Error('network down'))

    renderTab()
    await screen.findByTestId('plan-lane-header-plan-a')
    await user.click(screen.getByRole('button', { name: 'Approve' }))

    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error', message: 'network down' }),
      ),
    )
  })
})

// ── Stop ─────────────────────────────────────────────────────────────────────

describe('WorkspaceTasksTab — plan lane Stop', () => {
  it('confirming Stop calls stopPlan with the plan id and toasts on success', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ state: 'running' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    vi.mocked(stopPlan).mockResolvedValue({ ...plan, state: 'done' })

    renderTab()
    await screen.findByTestId('plan-lane-header-plan-a')
    await user.click(screen.getByRole('button', { name: 'Stop' }))

    const dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Stop' }))

    await waitFor(() => expect(stopPlan).toHaveBeenCalledWith('plan-a'))
    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success', message: 'Plan stopped' }),
      ),
    )
  })

  it('surfaces a Stop failure via an error toast', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ state: 'running' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    vi.mocked(stopPlan).mockRejectedValue(new Error('boom'))

    renderTab()
    await screen.findByTestId('plan-lane-header-plan-a')
    await user.click(screen.getByRole('button', { name: 'Stop' }))

    const dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Stop' }))

    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error', message: 'boom' }),
      ),
    )
  })
})

// ── Clear ────────────────────────────────────────────────────────────────────

describe('WorkspaceTasksTab — plan lane Clear', () => {
  it('confirming Clear calls deletePlan with the plan id and toasts on success', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ state: 'draft' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    vi.mocked(deletePlan).mockResolvedValue(undefined)

    renderTab()
    await screen.findByTestId('plan-lane-header-plan-a')
    await user.click(screen.getByRole('button', { name: 'Clear' }))

    const dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Clear' }))

    await waitFor(() => expect(deletePlan).toHaveBeenCalledWith('plan-a'))
    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success', message: 'Plan cleared' }),
      ),
    )
  })

  it('surfaces a Clear failure via an error toast (e.g. backend rejects delete-while-running)', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ state: 'draft' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    vi.mocked(deletePlan).mockRejectedValue(new Error('plan is running'))

    renderTab()
    await screen.findByTestId('plan-lane-header-plan-a')
    await user.click(screen.getByRole('button', { name: 'Clear' }))

    const dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Clear' }))

    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error', message: 'plan is running' }),
      ),
    )
  })
})
