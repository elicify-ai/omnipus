/**
 * PlanActionButton.test.tsx — ADR-052 §6.8 button matrix for plans.
 *
 * Covers:
 *   1. Per-state button matrix: draft→Execute, running→Stop,
 *      cap-queued approved→Stop, cancelled→Play, genuinely-failed→none,
 *      done→none.
 *   2. Confirm-modal gating on every ▶/■ (mutation only fires after Confirm).
 *   3. Cancel dismisses without mutating.
 *   4. Execute calls `executePlan`, Stop calls `stopPlan`, Play calls
 *      `restartPlan` — never the deprecated PUT-based `approvePlan`.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { PlanActionButton, planActionFor } from './PlanActionButton'
import type { Plan } from '@/lib/api'
import { ApiError } from '@/lib/api-error'

const executePlanMock = vi.fn()
const stopPlanMock = vi.fn()
const restartPlanMock = vi.fn()

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    executePlan: (id: string) => executePlanMock(id),
    stopPlan: (id: string) => stopPlanMock(id),
    restartPlan: (id: string) => restartPlanMock(id),
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: typeof mockAddToast }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

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
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderButton(plan: Plan) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <PlanActionButton plan={plan} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  executePlanMock.mockReset().mockResolvedValue(makePlan({ state: 'approved' }))
  stopPlanMock.mockReset().mockResolvedValue(makePlan({ state: 'failed', failed_reason: 'stopped_by_user' }))
  restartPlanMock.mockReset().mockResolvedValue(makePlan({ state: 'approved' }))
  mockAddToast.mockReset()
})

describe('planActionFor — ADR-052 §6.8 button matrix', () => {
  it('draft → execute', () => {
    expect(planActionFor(makePlan({ state: 'draft' }))).toBe('execute')
  })
  it('running → stop', () => {
    expect(planActionFor(makePlan({ state: 'running' }))).toBe('stop')
  })
  it('approved (cap-queued) → stop', () => {
    expect(planActionFor(makePlan({ state: 'approved' }))).toBe('stop')
  })
  it('failed + stopped_by_user (cancelled) → play', () => {
    expect(planActionFor(makePlan({ state: 'failed', failed_reason: 'stopped_by_user' }))).toBe('play')
  })
  it('failed + judge_rounds_exhausted (genuine failure) → no play', () => {
    expect(planActionFor(makePlan({ state: 'failed', failed_reason: 'judge_rounds_exhausted' }))).toBeNull()
  })
  it('failed + idle_expired (genuine failure) → no play', () => {
    expect(planActionFor(makePlan({ state: 'failed', failed_reason: 'idle_expired' }))).toBeNull()
  })
  it('failed with no reason recorded → no play', () => {
    expect(planActionFor(makePlan({ state: 'failed' }))).toBeNull()
  })
  it('done → none', () => {
    expect(planActionFor(makePlan({ state: 'done' }))).toBeNull()
  })
})

describe('PlanActionButton — rendering per state', () => {
  it('renders Execute for a draft plan', () => {
    renderButton(makePlan({ state: 'draft' }))
    expect(screen.getByRole('button', { name: 'Execute plan Launch' })).toBeInTheDocument()
  })

  it('renders Stop for a running plan', () => {
    renderButton(makePlan({ state: 'running' }))
    expect(screen.getByRole('button', { name: 'Stop plan Launch' })).toBeInTheDocument()
  })

  it('renders Stop for a cap-queued approved plan', () => {
    renderButton(makePlan({ state: 'approved' }))
    expect(screen.getByRole('button', { name: 'Stop plan Launch' })).toBeInTheDocument()
  })

  it('renders Restart for a cancelled plan (S4 UAT — Play/Restart vocabulary must agree; the spec calls it Restart)', () => {
    renderButton(makePlan({ state: 'failed', failed_reason: 'stopped_by_user' }))
    expect(screen.getByRole('button', { name: 'Restart plan Launch' })).toBeInTheDocument()
  })

  it('renders nothing for a genuinely-failed plan', () => {
    const { container } = renderButton(makePlan({ state: 'failed', failed_reason: 'judge_rounds_exhausted' }))
    expect(container.querySelector('button')).toBeNull()
  })

  it('renders nothing for a done plan', () => {
    const { container } = renderButton(makePlan({ state: 'done' }))
    expect(container.querySelector('button')).toBeNull()
  })
})

describe('PlanActionButton — confirm-modal gating (ADR-052 FR-020)', () => {
  it('Execute opens a confirm modal naming the plan; the mutation does not fire until Confirm', async () => {
    const user = userEvent.setup()
    renderButton(makePlan({ state: 'draft', title: 'Payments revamp' }))
    await user.click(screen.getByRole('button', { name: 'Execute plan Payments revamp' }))

    const dialog = screen.getByRole('alertdialog')
    expect(within(dialog).getByText(/Payments revamp/)).toBeInTheDocument()
    expect(executePlanMock).not.toHaveBeenCalled()

    await user.click(within(dialog).getByRole('button', { name: 'Execute' }))
    expect(executePlanMock).toHaveBeenCalledWith('plan-1')
    expect(executePlanMock).toHaveBeenCalledTimes(1)
  })

  it('Cancel dismisses the Execute confirm without calling executePlan', async () => {
    const user = userEvent.setup()
    renderButton(makePlan({ state: 'draft' }))
    await user.click(screen.getByRole('button', { name: 'Execute plan Launch' }))
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(executePlanMock).not.toHaveBeenCalled()
  })

  it('Stop opens a confirm modal; confirming calls stopPlan (never the deprecated approvePlan)', async () => {
    const user = userEvent.setup()
    renderButton(makePlan({ state: 'running' }))
    await user.click(screen.getByRole('button', { name: 'Stop plan Launch' }))
    const dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Stop' }))
    expect(stopPlanMock).toHaveBeenCalledWith('plan-1')
  })

  it('Restart opens a confirm modal; confirming calls restartPlan (S4 UAT — aria-label/button/title/toast all say Restart)', async () => {
    const user = userEvent.setup()
    renderButton(makePlan({ state: 'failed', failed_reason: 'stopped_by_user' }))
    await user.click(screen.getByRole('button', { name: 'Restart plan Launch' }))
    const dialog = screen.getByRole('alertdialog')
    expect(within(dialog).getByText('Restart this plan?')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Restart' }))
    expect(restartPlanMock).toHaveBeenCalledWith('plan-1')
  })
})

describe('PlanActionButton — click isolation (Gate-2 finding #2, parity with TaskActionButton)', () => {
  it('a click on the action button never bubbles to an ancestor onClick', async () => {
    const user = userEvent.setup()
    const ancestorClick = vi.fn()
    render(
      <QueryClientProvider client={makeClient()}>
        { }
        <div onClick={ancestorClick}>
          <PlanActionButton plan={makePlan({ state: 'draft' })} />
        </div>
      </QueryClientProvider>,
    )
    await user.click(screen.getByRole('button', { name: 'Execute plan Launch' }))
    expect(ancestorClick).not.toHaveBeenCalled()
  })
})

describe('PlanActionButton — 400 task_errors surfacing (ADR-052 FR-005/FR-084, parity with the retired WorkspaceTasksTab approve wiring)', () => {
  it('a 400 with a per-task payload surfaces the per-task reasons via toast on Execute', async () => {
    const user = userEvent.setup()
    const body = JSON.stringify({
      task_errors: [{ task_id: 't1', title: 'Write report', reason: 'missing acceptance criteria' }],
    })
    executePlanMock.mockRejectedValueOnce(new ApiError(400, 'Bad request', { body }))

    renderButton(makePlan({ state: 'draft', title: 'Launch' }))
    await user.click(screen.getByRole('button', { name: 'Execute plan Launch' }))
    const dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Execute' }))

    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({
          variant: 'error',
          message: expect.stringContaining('Write report: missing acceptance criteria'),
        }),
      ),
    )
    expect(mockAddToast).not.toHaveBeenCalledWith(
      expect.objectContaining({ message: 'Failed to execute plan' }),
    )
  })
})
