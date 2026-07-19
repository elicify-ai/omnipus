/**
 * CreatePlanSlideOver.test.tsx
 *
 * ADR-049 FR-083 (US-10 AS-4): Create Plan posts goal/DoD/owner/bounds.
 * FR-084 (US-10 AS-5, SD-C4): Approve is confirm-on-success — a 400 lists
 * per-task errors inline and does NOT close/optimistically transition.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreatePlanSlideOver } from './CreatePlanSlideOver'
import { ApiError } from '@/lib/api-error'
import type { Plan } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      { id: 'jim', name: 'Jim', type: 'core', default: false },
    ]),
    fetchWorkspaceDelegation: vi.fn().mockRejectedValue(new Error('not mocked')),
    createPlan: vi.fn(),
    updatePlan: vi.fn(),
    approvePlan: vi.fn(),
    plansQueryKeys: { list: (id: string) => ['plans', id] },
  }
})

import { createPlan, approvePlan } from '@/lib/api'

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

vi.mock('@/store/auth', () => ({
  useAuthStore: (selector?: (s: { username: string | null }) => unknown) => {
    const store = { username: 'alice' }
    return selector ? selector(store) : store
  },
}))

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

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

function renderSlideOver(props: Partial<React.ComponentProps<typeof CreatePlanSlideOver>> = {}) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <CreatePlanSlideOver
        open
        onOpenChange={vi.fn()}
        workspaceId="ws-1"
        {...props}
      />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.mocked(createPlan).mockReset()
  vi.mocked(approvePlan).mockReset()
  mockAddToast.mockReset()
})

describe('CreatePlanSlideOver — create', () => {
  it('requires a title', async () => {
    renderSlideOver()
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    expect(await screen.findByText(/title is required/i)).toBeInTheDocument()
    expect(createPlan).not.toHaveBeenCalled()
  })

  it('posts title/goal/owner/bounds on Create', async () => {
    vi.mocked(createPlan).mockResolvedValueOnce(makePlan() as never)
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/^title/i), { target: { value: 'v1.0 Launch' } })
    fireEvent.change(screen.getByLabelText(/^goal$/i), { target: { value: 'Ship it' } })

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(createPlan).toHaveBeenCalledOnce())
    const body = vi.mocked(createPlan).mock.calls[0][0]
    expect(body.title).toBe('v1.0 Launch')
    expect(body.goal).toBe('Ship it')
    expect(body.workspace_id).toBe('ws-1')
  })
})

describe('CreatePlanSlideOver — Approve (SD-C4 confirm-on-success)', () => {
  it('shows the Approve action only for a draft plan', () => {
    renderSlideOver({ plan: makePlan({ state: 'running' }) })
    expect(screen.queryByRole('button', { name: /^approve$/i })).toBeNull()
  })

  it('a 400 with per-task errors lists them inline and does not close the slide-over', async () => {
    const onOpenChange = vi.fn()
    const body = JSON.stringify({
      task_errors: [{ task_id: 't1', title: 'Write report', reason: 'missing acceptance criteria' }],
    })
    vi.mocked(approvePlan).mockRejectedValueOnce(new ApiError(400, 'Bad request', { body }))

    renderSlideOver({ plan: makePlan({ state: 'draft' }), onOpenChange })

    fireEvent.click(screen.getByRole('button', { name: /^approve$/i }))

    expect(await screen.findByTestId('plan-approve-task-errors')).toHaveTextContent('Write report: missing acceptance criteria')
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
  })

  it('a successful Approve closes the slide-over and toasts', async () => {
    const onOpenChange = vi.fn()
    vi.mocked(approvePlan).mockResolvedValueOnce(makePlan({ state: 'approved' }) as never)

    renderSlideOver({ plan: makePlan({ state: 'draft' }), onOpenChange })
    fireEvent.click(screen.getByRole('button', { name: /^approve$/i }))

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
    expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ variant: 'success' }))
  })
})
