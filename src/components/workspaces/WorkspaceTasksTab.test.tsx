/**
 * WorkspaceTasksTab.test.tsx
 *
 * ADR-051 — the combined "Tasks" screen. Covers:
 *   - The [Board] [List] [Graph] view switcher renders and defaults to Board.
 *   - The dynamic heading ("Tasks" / "{plan.title} — tasks", with an
 *     "· Owner: {name}" suffix when an owner filter is active).
 *   - "New task" is present and opens the create slide-over.
 *   - This screen's own plan-mutation WIRING (Approve/Stop/Clear/Edit/New
 *     Plan callbacks passed into PlansFilterBand fire the right mutation).
 *   - The altitude toggle only shows in Board view.
 *
 * PlansFilterBand and taskFilters.ts are owned by a parallel agent and may
 * not exist yet. `PlansFilterBand` is mocked to a plain stub that exposes
 * one button per callback prop — this file tests WorkspaceTasksTab's own
 * wiring (which mutation each prop triggers), not PlansFilterBand's real
 * internal DOM/markup, which is out of this file's ownership and is covered
 * by the band's own test file.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Plan, Task, Agent } from '@/lib/api'
import { WorkspaceTasksTab } from './WorkspaceTasksTab'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { ApiError } from '@/lib/api-error'

// jsdom doesn't implement scrollIntoView; Radix Select calls it when opening
// the listbox (repo-wide convention — see ChannelConfigPanel.test.tsx,
// CreateTaskSlideOver.test.tsx, NewWorkspaceSlideOver.test.tsx, etc.).
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

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
    fetchAgents: vi.fn(),
    fetchWorkspaceDelegation: vi.fn().mockRejectedValue(new Error('not mocked')),
    approvePlan: vi.fn(),
    stopPlan: vi.fn(),
    deletePlan: vi.fn(),
  }
})

import { fetchTasks, fetchPlans, fetchAgents, approvePlan, stopPlan, deletePlan } from '@/lib/api'

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

// Stub PlansFilterBand — owned by a parallel agent (may not exist on disk
// yet). Exposes one plain button per callback prop so this file can verify
// WorkspaceTasksTab's OWN wiring without depending on the real component's
// internal markup.
vi.mock('./PlansFilterBand', () => ({
  PlansFilterBand: (props: {
    plans: Plan[]
    onSelectPlan: (id: string | null) => void
    onNewPlan: () => void
    onEditPlan: (plan: Plan) => void
    onApprovePlan: (plan: Plan) => void
    onStopPlan: (plan: Plan) => void
    onClearPlan: (plan: Plan) => void
  }) => (
    <div data-testid="plans-filter-band-stub">
      <button type="button" onClick={() => props.onSelectPlan(null)}>
        select-all
      </button>
      <button type="button" onClick={props.onNewPlan}>
        stub-new-plan
      </button>
      {props.plans.map((plan) => (
        <div key={plan.id}>
          <button type="button" onClick={() => props.onSelectPlan(plan.id)}>
            select-{plan.id}
          </button>
          <button type="button" onClick={() => props.onEditPlan(plan)}>
            edit-{plan.id}
          </button>
          <button type="button" onClick={() => props.onApprovePlan(plan)}>
            approve-{plan.id}
          </button>
          <button type="button" onClick={() => props.onStopPlan(plan)}>
            stop-{plan.id}
          </button>
          <button type="button" onClick={() => props.onClearPlan(plan)}>
            clear-{plan.id}
          </button>
        </div>
      ))}
    </div>
  ),
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

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 't1',
    title: 'Write report',
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

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: 'ray',
    name: 'Ray',
    type: 'core',
    locked: true,
    status: 'active',
    soul: '',
    color: '#3B82F6',
    icon: 'MagnifyingGlass',
    timeout_seconds: 300,
    max_tool_iterations: 50,
    ...overrides,
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderTab(workspaceId = 'ws-1') {
  return render(
    <QueryClientProvider client={makeClient()}>
      <WorkspaceTasksTab workspaceId={workspaceId} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockNavigate.mockReset()
  mockAddToast.mockReset()
  // Real Zustand store — reset between tests (module-level singleton).
  useWorkspacesStore.setState({ activeTagFilter: null, boardAltitude: 'top-level', activePlanId: null })
  vi.mocked(fetchTasks).mockReset().mockResolvedValue([])
  vi.mocked(fetchPlans).mockReset().mockResolvedValue([])
  vi.mocked(fetchAgents).mockReset().mockResolvedValue([])
  vi.mocked(approvePlan).mockReset()
  vi.mocked(stopPlan).mockReset()
  vi.mocked(deletePlan).mockReset()
})

// ── View switcher ────────────────────────────────────────────────────────────

describe('WorkspaceTasksTab — view switcher', () => {
  it('renders the Board/List/Graph segmented control, defaulting to Board', async () => {
    renderTab()
    await screen.findByRole('button', { name: /new task/i })

    const board = screen.getByTestId('tasks-view-board')
    const list = screen.getByTestId('tasks-view-list')
    const graph = screen.getByTestId('tasks-view-graph')
    expect(board).toBeInTheDocument()
    expect(list).toBeInTheDocument()
    expect(graph).toBeInTheDocument()
    expect(board).toHaveAttribute('aria-checked', 'true')
    expect(list).toHaveAttribute('aria-checked', 'false')
    expect(graph).toHaveAttribute('aria-checked', 'false')
  })

  it('switching to List renders the ListView table', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchTasks).mockResolvedValue([makeTask()])
    renderTab()
    await user.click(screen.getByTestId('tasks-view-list'))
    expect(await screen.findByText('Write report')).toBeInTheDocument()
    // The List table's own column headers only render in list mode.
    expect(screen.getByText('Updated ↓')).toBeInTheDocument()
  })

  it('the altitude toggle only shows in Board view', async () => {
    renderTab()
    expect(await screen.findByRole('radio', { name: /top-level/i })).toBeInTheDocument()

    const user = userEvent.setup()
    await user.click(screen.getByTestId('tasks-view-list'))
    expect(screen.queryByRole('radio', { name: /top-level/i })).toBeNull()
  })
})

// ── Dynamic heading ──────────────────────────────────────────────────────────

describe('WorkspaceTasksTab — dynamic heading', () => {
  it('reads "Tasks" when no plan and no owner filter is active', async () => {
    renderTab()
    const heading = await screen.findByTestId('tasks-heading')
    expect(heading.textContent).toContain('Tasks')
    expect(heading.textContent).not.toContain('Owner:')
  })

  it('reads "{plan.title} — tasks" when a plan is selected (via the band stub)', async () => {
    const plan = makePlan({ title: 'Payments revamp' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])

    const user = userEvent.setup()
    renderTab()
    await user.click(await screen.findByRole('button', { name: 'select-plan-a' }))

    const heading = await screen.findByTestId('tasks-heading')
    await waitFor(() => expect(heading.textContent).toContain('Payments revamp — tasks'))
  })

  it('appends "· Owner: {name}" once an owner filter is active', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'ray', name: 'Ray' })])
    renderTab()

    // Radix Select interaction — click the trigger, then the option by its
    // role=option (mirrors ListView.filters.test.tsx's selectOption helper).
    const trigger = await screen.findByRole('combobox', { name: /filter by owner/i })
    fireEvent.click(trigger)
    const option = await screen.findByRole('option', { name: 'Owner: Ray' })
    fireEvent.click(option)

    const heading = await screen.findByTestId('tasks-heading')
    await waitFor(() => expect(heading.textContent).toContain('Owner: Ray'))
  })
})

// ── New task ─────────────────────────────────────────────────────────────────

describe('WorkspaceTasksTab — New task', () => {
  it('renders a working New task button that opens the create slide-over', async () => {
    const user = userEvent.setup()
    renderTab()
    const button = await screen.findByRole('button', { name: /new task/i })
    await user.click(button)
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
  })
})

// ── Plan mutation wiring (through the callback props passed to the band) ────

describe('WorkspaceTasksTab — plan mutation wiring', () => {
  it('the band stub\'s approve button calls approvePlan with the plan id and toasts on success', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ state: 'draft' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    vi.mocked(approvePlan).mockResolvedValue({ ...plan, state: 'approved' })

    renderTab()
    await user.click(await screen.findByRole('button', { name: 'approve-plan-a' }))

    await waitFor(() => expect(approvePlan).toHaveBeenCalledWith('plan-a'))
    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success', message: 'Plan approved' }),
      ),
    )
  })

  it('a 400 with a per-task payload surfaces the per-task reasons via toast', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ state: 'draft' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    const body = JSON.stringify({
      task_errors: [{ task_id: 't1', title: 'Write report', reason: 'missing acceptance criteria' }],
    })
    vi.mocked(approvePlan).mockRejectedValue(new ApiError(400, 'Bad request', { body }))

    renderTab()
    await user.click(await screen.findByRole('button', { name: 'approve-plan-a' }))

    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({
          variant: 'error',
          message: expect.stringContaining('Write report: missing acceptance criteria'),
        }),
      ),
    )
    expect(mockAddToast).not.toHaveBeenCalledWith(
      expect.objectContaining({ message: 'Failed to approve plan' }),
    )
  })

  it('the band stub\'s stop button calls stopPlan with the plan id and toasts on success', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ state: 'running' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    vi.mocked(stopPlan).mockResolvedValue({ ...plan, state: 'done' })

    renderTab()
    await user.click(await screen.findByRole('button', { name: 'stop-plan-a' }))

    await waitFor(() => expect(stopPlan).toHaveBeenCalledWith('plan-a'))
    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success', message: 'Plan stopped' }),
      ),
    )
  })

  it('the band stub\'s clear button calls deletePlan with the plan id and toasts on success', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ state: 'draft' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    vi.mocked(deletePlan).mockResolvedValue(undefined)

    renderTab()
    await user.click(await screen.findByRole('button', { name: 'clear-plan-a' }))

    await waitFor(() => expect(deletePlan).toHaveBeenCalledWith('plan-a'))
    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success', message: 'Plan cleared' }),
      ),
    )
  })

  it('the band stub\'s edit button opens the CreatePlanSlideOver in edit mode', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ title: 'Plan A' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])

    renderTab()
    await user.click(await screen.findByRole('button', { name: 'edit-plan-a' }))

    // CreatePlanSlideOver renders as a Sheet (role=dialog) once opened.
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
  })
})
