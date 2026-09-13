/**
 * CreateTaskSlideOver.plan.test.tsx
 *
 * GOAL-FR-059 — the create form offers the same Plan picker the detail
 * panel has, defaulting to the inherited `planId` prop (the board's active
 * plan filter) when one is passed, and to "No plan" otherwise, rather than
 * silently sending whatever the board happened to be filtered by with no
 * way to see or change it. Joint delivery plan U4 row, test-matrix item 59.
 */

import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateTaskSlideOver, reconcileBlockedByForPlan } from './CreateTaskSlideOver'

// Radix Select needs these jsdom polyfills to open/select an item (same gap
// noted in CreateTaskSlideOver.test.tsx and date-time-picker.test.tsx).
beforeAll(() => {
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false
  }
  Element.prototype.scrollIntoView = vi.fn()
})

// Pick an option from an open Radix <Select>.
function selectOption(optionName: string | RegExp) {
  const option = screen.getByRole('option', { name: optionName })
  fireEvent.pointerDown(option, { pointerId: 1, button: 0 })
  fireEvent.click(option)
}

// ── API mock ─────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchTasks: vi.fn(),
    fetchPlans: vi.fn(),
    fetchWorkspaceDelegation: vi.fn().mockRejectedValue(new Error('not mocked')),
    createTask: vi.fn(),
    updateTask: vi.fn(),
    tasksQueryKeys: { list: () => ['tasks'] },
    workspacesQueryKeys: {
      list: () => ['workspaces'],
      delegation: (id: string) => ['workspaces', id, 'delegation'],
    },
    isApiError: vi.fn().mockReturnValue(false),
  }
})

import { createTask, fetchPlans, fetchTasks } from '@/lib/api'

const mockAddToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function makeCreatedTask(overrides: Record<string, unknown> = {}) {
  return {
    id: 'new-task',
    title: 'New task',
    action: 'llm',
    status: 'inbox',
    priority: 3,
    workspace_id: 'proj-test',
    surface: 'user',
    owner: 'alice',
    created_by: 'alice',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

function makeTask(overrides: Record<string, unknown> = {}) {
  return {
    id: 'dep-1',
    title: 'Draft the brief',
    action: 'llm',
    status: 'inbox',
    priority: 3,
    workspace_id: 'proj-test',
    surface: 'user',
    owner: 'alice',
    created_by: 'alice',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

function makePlan(overrides: Record<string, unknown> = {}) {
  return {
    id: 'plan-1',
    title: 'Q3 comms',
    workspace_id: 'proj-test',
    status: 'draft',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

function renderSlideOver(planId?: string | null) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <CreateTaskSlideOver open onOpenChange={vi.fn()} workspaceId="proj-test" planId={planId} />
    </QueryClientProvider>,
  )
}

function addItem(inputLabel: RegExp, text: string) {
  const input = screen.getByLabelText(inputLabel)
  fireEvent.change(input, { target: { value: text } })
  const draftPanel = input.parentElement as HTMLElement
  fireEvent.click(
    Array.from(draftPanel.querySelectorAll('button')).find((b) =>
      /add criterion/i.test(b.textContent ?? ''),
    ) as HTMLButtonElement,
  )
}

beforeEach(() => {
  vi.mocked(createTask).mockReset()
  vi.mocked(fetchPlans).mockReset().mockResolvedValue([])
  vi.mocked(fetchTasks).mockReset().mockResolvedValue([])
  mockAddToast.mockReset()
})

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('reconcileBlockedByForPlan — the rule the picker must apply', () => {
  const tasks = [
    makeTask({ id: 'a', plan_id: 'plan-1' }),
    makeTask({ id: 'b', plan_id: 'plan-2' }),
    makeTask({ id: 'c', plan_id: null }),
    makeTask({ id: 'sub', plan_id: 'plan-2', parent_task_id: 'b' }),
  ]

  it('drops a dependency that belongs to a different plan', () => {
    expect(reconcileBlockedByForPlan(['a'], 'plan-2', tasks)).toEqual([])
  })

  it('keeps a dependency that belongs to the new plan', () => {
    expect(reconcileBlockedByForPlan(['b'], 'plan-2', tasks)).toEqual(['b'])
  })

  it('treats the plan-less "Loose" group as its own DAG', () => {
    expect(reconcileBlockedByForPlan(['c'], null, tasks)).toEqual(['c'])
    expect(reconcileBlockedByForPlan(['c'], 'plan-1', tasks)).toEqual([])
    expect(reconcileBlockedByForPlan(['a'], null, tasks)).toEqual([])
  })

  it('drops a subtask — only top-level tasks are eligible dependencies', () => {
    expect(reconcileBlockedByForPlan(['sub'], 'plan-2', tasks)).toEqual([])
  })

  it('drops an id that no longer exists in the workspace at all', () => {
    expect(reconcileBlockedByForPlan(['ghost'], 'plan-1', tasks)).toEqual([])
  })
})

describe('CreateTaskSlideOver — Plan picker (GOAL-FR-059)', () => {
  it('renders a Plan picker defaulting to "No plan" when no planId prop is passed', async () => {
    renderSlideOver()

    expect(await screen.findByText('Plan')).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Plan' })).toHaveTextContent('No plan')
  })

  it('fetches this workspace\'s plans and offers them by title in the picker', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([
      makePlan({ id: 'plan-1', title: 'Q3 comms' }),
      makePlan({ id: 'plan-2', title: 'Launch prep' }),
    ] as never)

    renderSlideOver()

    await waitFor(() => expect(vi.mocked(fetchPlans)).toHaveBeenCalledWith('proj-test'))

    fireEvent.click(screen.getByRole('combobox', { name: 'Plan' }))
    expect(await screen.findByRole('option', { name: 'Q3 comms' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'Launch prep' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'No plan' })).toBeInTheDocument()
  })

  it('defaults the picker to the inherited planId prop (the board\'s active plan filter)', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([
      makePlan({ id: 'plan-1', title: 'Q3 comms' }),
    ] as never)

    renderSlideOver('plan-1')

    await waitFor(() =>
      expect(screen.getByRole('combobox', { name: 'Plan' })).toHaveTextContent('Q3 comms'),
    )
  })

  it('lets the operator change the picker away from the inherited plan before submitting', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([
      makePlan({ id: 'plan-1', title: 'Q3 comms' }),
      makePlan({ id: 'plan-2', title: 'Launch prep' }),
    ] as never)
    vi.mocked(createTask).mockResolvedValueOnce(makeCreatedTask() as never)

    renderSlideOver('plan-1')

    await waitFor(() =>
      expect(screen.getByRole('combobox', { name: 'Plan' })).toHaveTextContent('Q3 comms'),
    )

    // Switch to the OTHER plan rather than the inherited one.
    fireEvent.click(screen.getByRole('combobox', { name: 'Plan' }))
    await screen.findByRole('option', { name: 'Launch prep' })
    selectOption('Launch prep')

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Reassigned' } })
    fireEvent.change(screen.getByLabelText(/^goal/i), { target: { value: 'Do it.' } })
    addItem(/what must be true when this is done\?/i, 'A criterion.')
    addItem(/definition of done item/i, 'A DoD item.')

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())
    const body = vi.mocked(createTask).mock.calls[0][0]
    expect(body.plan_id).toBe('plan-2')
  })

  it('sends no plan_id when the picker is cleared back to "No plan"', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([
      makePlan({ id: 'plan-1', title: 'Q3 comms' }),
    ] as never)
    vi.mocked(createTask).mockResolvedValueOnce(makeCreatedTask() as never)

    renderSlideOver('plan-1')

    await waitFor(() =>
      expect(screen.getByRole('combobox', { name: 'Plan' })).toHaveTextContent('Q3 comms'),
    )

    fireEvent.click(screen.getByRole('combobox', { name: 'Plan' }))
    await screen.findByRole('option', { name: 'No plan' })
    selectOption('No plan')

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Unassigned from plan' } })
    fireEvent.change(screen.getByLabelText(/^goal/i), { target: { value: 'Do it.' } })
    addItem(/what must be true when this is done\?/i, 'A criterion.')
    addItem(/definition of done item/i, 'A DoD item.')

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())
    const body = vi.mocked(createTask).mock.calls[0][0]
    expect(body.plan_id).toBeUndefined()
  })

  // Review finding: the Plan picker changed `form.planId` without touching
  // `form.blockedBy`. `depCandidates` is filtered to the SELECTED plan, so a
  // dependency chosen under the old plan vanished from the picker AND from
  // the chip row while still riding along in the POST body — an invisible,
  // unremovable cross-plan `blocked_by` edge. This drives the real picker,
  // through the real component: nothing here reconciles on the test's behalf.
  it('switching plans after choosing a dependency does NOT submit a cross-plan blocked_by edge', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([
      makePlan({ id: 'plan-1', title: 'Q3 comms' }),
      makePlan({ id: 'plan-2', title: 'Launch prep' }),
    ] as never)
    vi.mocked(fetchTasks).mockResolvedValue([
      makeTask({ id: 'dep-1', title: 'Draft the brief', plan_id: 'plan-1' }),
      makeTask({ id: 'dep-2', title: 'Book the venue', plan_id: 'plan-2' }),
    ] as never)
    vi.mocked(createTask).mockResolvedValueOnce(makeCreatedTask() as never)

    renderSlideOver('plan-1')

    await waitFor(() =>
      expect(screen.getByRole('combobox', { name: 'Plan' })).toHaveTextContent('Q3 comms'),
    )

    // Pick plan-1's task as a dependency.
    const depTrigger = await screen.findByRole('button', { name: /no dependencies/i })
    fireEvent.click(depTrigger)
    fireEvent.click(await screen.findByRole('button', { name: /draft the brief/i }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /1 task selected/i })).toBeInTheDocument(),
    )

    // Now move the task to the OTHER plan.
    fireEvent.click(screen.getByRole('combobox', { name: 'Plan' }))
    await screen.findByRole('option', { name: 'Launch prep' })
    selectOption('Launch prep')

    // The selection must be gone from the UI, not merely hidden.
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: /1 task selected/i })).toBeNull(),
    )

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Moved task' } })
    fireEvent.change(screen.getByLabelText(/^goal/i), { target: { value: 'Do it.' } })
    addItem(/what must be true when this is done\?/i, 'A criterion.')
    addItem(/definition of done item/i, 'A DoD item.')

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())
    const body = vi.mocked(createTask).mock.calls[0][0]
    expect(body.plan_id).toBe('plan-2')
    // Load-bearing: `dep-1` lives in plan-1. Before the fix this was
    // `blocked_by: ['dep-1']` — a cross-plan edge the operator never saw.
    expect(body.blocked_by).toBeUndefined()
  })

  it('submits with the default-inherited planId untouched when the operator never opens the picker', async () => {
    vi.mocked(fetchPlans).mockResolvedValue([
      makePlan({ id: 'plan-1', title: 'Q3 comms' }),
    ] as never)
    vi.mocked(createTask).mockResolvedValueOnce(makeCreatedTask() as never)

    renderSlideOver('plan-1')

    await waitFor(() =>
      expect(screen.getByRole('combobox', { name: 'Plan' })).toHaveTextContent('Q3 comms'),
    )

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Inherits plan' } })
    fireEvent.change(screen.getByLabelText(/^goal/i), { target: { value: 'Do it.' } })
    addItem(/what must be true when this is done\?/i, 'A criterion.')
    addItem(/definition of done item/i, 'A DoD item.')

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())
    const body = vi.mocked(createTask).mock.calls[0][0]
    expect(body.plan_id).toBe('plan-1')
  })
})
