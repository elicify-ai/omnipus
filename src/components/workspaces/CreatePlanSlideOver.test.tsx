/**
 * CreatePlanSlideOver.test.tsx
 *
 * ADR-049 FR-083 (US-10 AS-4): Create Plan posts goal/DoD/owner/bounds.
 * FR-084 (US-10 AS-5, SD-C4): Approve is confirm-on-success — a 400 lists
 * per-task errors inline and does NOT close/optimistically transition.
 */

import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreatePlanSlideOver } from './CreatePlanSlideOver'
import { ApiError } from '@/lib/api-error'
import type { Plan } from '@/lib/api'

// jsdom doesn't implement scrollIntoView — Radix Select's Viewport calls it
// (unconditionally, via an effect) to scroll the selected item into view on
// open. Needed only because these tests actually open+select in the Owner
// agent SmartSelect (same gap noted in CreateTaskSlideOver.test.tsx /
// date-time-picker.test.tsx for the same underlying Radix Select).
beforeAll(() => {
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {}
  }
})

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
    // ADR-052 G2: the component calls executePlan (POST /approve), not the
    // deprecated approvePlan alias — mock the name it actually imports.
    executePlan: vi.fn(),
    plansQueryKeys: { list: (id: string) => ['plans', id] },
  }
})

import { createPlan, executePlan } from '@/lib/api'

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

// Owner agent picker is a plain (<=5 item) SmartSelect → Radix Select, whose
// trigger carries `aria-label="Owner agent"` directly (SmartSelect's required
// `ariaLabel` prop) — no DOM-traversal hack needed, unlike pickers elsewhere
// in the app that rely on a sibling Label's text. The trigger is `disabled`
// while `useWorkspaceTeamIds`'s delegation-graph query is still in flight
// (mocked here to reject via `fetchWorkspaceDelegation`), so a click fired
// before it settles would silently no-op — wait for it to become enabled
// first (mirrors CreateTaskSlideOver.test.tsx's `openAgentPicker`).
async function openOwnerPicker(): Promise<HTMLElement> {
  const trigger = screen.getByRole('combobox', { name: /^owner agent$/i })
  await waitFor(() => expect(trigger).not.toBeDisabled())
  fireEvent.click(trigger)
  return trigger
}

async function selectOwner(name: RegExp) {
  await openOwnerPicker()
  const option = await screen.findByRole('option', { name })
  fireEvent.pointerDown(option, { pointerId: 1, button: 0 })
  fireEvent.click(option)
}

beforeEach(() => {
  vi.mocked(createPlan).mockReset()
  vi.mocked(executePlan).mockReset()
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
    await selectOwner(/^jim$/i)

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(createPlan).toHaveBeenCalledOnce())
    const body = vi.mocked(createPlan).mock.calls[0][0]
    expect(body.title).toBe('v1.0 Launch')
    expect(body.goal).toBe('Ship it')
    expect(body.workspace_id).toBe('ws-1')
    expect(body.owner_agent_id).toBe('jim')
  })

  // S2 UAT finding: owner_agent_id is server-required (400 invalid
  // owner_agent_id) but the field previously had no client-side validation at
  // all and defaulted to the unselected '__none__' sentinel — a first-time
  // Create click fired a request doomed to 400, then (S3 finding) retried it
  // up to 4×, and the resulting error toast rendered underneath the
  // slide-over footer (invisible on screen). This proves the fix: the
  // request never fires, and the error is visible inline next to the field.
  it('requires an owner agent — shows a visible, field-specific error and fires no request', async () => {
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/^title/i), { target: { value: 'v1.0 Launch' } })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    expect(await screen.findByText(/owner agent is required/i)).toBeInTheDocument()
    expect(createPlan).not.toHaveBeenCalled()
  })

  it('clears the owner-agent error once an owner is selected', async () => {
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/^title/i), { target: { value: 'v1.0 Launch' } })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    expect(await screen.findByText(/owner agent is required/i)).toBeInTheDocument()

    await selectOwner(/^jim$/i)

    await waitFor(() => expect(screen.queryByText(/owner agent is required/i)).toBeNull())
  })

  it('shows both Title and Owner errors together when both are left empty (batched validation)', async () => {
    renderSlideOver()
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    expect(await screen.findByText(/title is required/i)).toBeInTheDocument()
    expect(await screen.findByText(/owner agent is required/i)).toBeInTheDocument()
    expect(createPlan).not.toHaveBeenCalled()
  })

  it('shows an error toast (and does not close) when Create fails — e.g. the workspace-route 405', async () => {
    const onOpenChange = vi.fn()
    vi.mocked(createPlan).mockRejectedValueOnce(
      new ApiError(405, 'method not allowed', { body: JSON.stringify({ error: 'method not allowed' }) }),
    )
    renderSlideOver({ onOpenChange })

    fireEvent.change(screen.getByLabelText(/^title/i), { target: { value: 'v1.0 Launch' } })
    await selectOwner(/^jim$/i)
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'error', message: expect.stringMatching(/method not allowed/i) }),
      ),
    )
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
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
    vi.mocked(executePlan).mockRejectedValueOnce(new ApiError(400, 'Bad request', { body }))

    renderSlideOver({ plan: makePlan({ state: 'draft' }), onOpenChange })

    fireEvent.click(screen.getByRole('button', { name: /^approve$/i }))

    expect(await screen.findByTestId('plan-approve-task-errors')).toHaveTextContent('Write report: missing acceptance criteria')
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
  })

  it('a successful Approve closes the slide-over and toasts', async () => {
    const onOpenChange = vi.fn()
    vi.mocked(executePlan).mockResolvedValueOnce(makePlan({ state: 'approved' }) as never)

    renderSlideOver({ plan: makePlan({ state: 'draft' }), onOpenChange })
    fireEvent.click(screen.getByRole('button', { name: /^approve$/i }))

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
    expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ variant: 'success' }))
  })
})

// S2 UAT finding: the Goal textarea allowed 4000 chars client-side while the
// SERVER caps goals at 2000 (pkg/plan/plan.go maxPlanGoalRunes) — a
// 2500-char goal was silently accepted by the UI and only rejected on submit
// with "plan validation: goal must be 2000 characters or fewer". These tests
// pin the textarea's real cap and prove truncation is never silent (S3).
describe('CreatePlanSlideOver — character caps are visible, not silent (S3/S4 UAT)', () => {
  it('caps the Goal textarea at the real server limit (2000), not the old 4000', () => {
    renderSlideOver()
    const goal = screen.getByLabelText(/^goal$/i) as HTMLTextAreaElement
    expect(goal.maxLength).toBe(2000)
  })

  it('caps the Title input at the real server limit (200)', () => {
    renderSlideOver()
    const title = screen.getByLabelText(/^title/i) as HTMLInputElement
    expect(title.maxLength).toBe(200)
  })

  it('shows a live character counter for Title that flips to a "max length reached" notice at the cap', () => {
    renderSlideOver()
    const title = screen.getByLabelText(/^title/i)

    fireEvent.change(title, { target: { value: 'short' } })
    expect(screen.getByText('5/200')).toBeInTheDocument()
    expect(screen.queryByText(/max length reached/i)).toBeNull()

    fireEvent.change(title, { target: { value: 'x'.repeat(200) } })
    expect(screen.getByText(/200\/200/)).toBeInTheDocument()
    expect(screen.getByText(/max length reached/i)).toBeInTheDocument()
  })

  it('shows a live character counter for Goal that flips to a "max length reached" notice at the cap', () => {
    renderSlideOver()
    const goal = screen.getByLabelText(/^goal$/i)

    fireEvent.change(goal, { target: { value: 'short goal' } })
    expect(screen.getByText('10/2000')).toBeInTheDocument()
    expect(screen.queryByText(/max length reached/i)).toBeNull()

    fireEvent.change(goal, { target: { value: 'y'.repeat(2000) } })
    expect(screen.getByText(/2000\/2000/)).toBeInTheDocument()
    expect(screen.getByText(/max length reached/i)).toBeInTheDocument()
  })
})
