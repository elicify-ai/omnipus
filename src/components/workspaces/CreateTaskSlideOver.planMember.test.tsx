/**
 * CreateTaskSlideOver.planMember.test.tsx
 *
 * UAT defect A: `pkg/plan/lint.go` refuses a plan whose PARALLEL members
 * declare overlapping `write_set` paths, and refuses one whose convergence
 * point is not an authored join member (`is_join`). Both refusals are
 * correct, and both name a fix — but neither field could be entered anywhere
 * in the interface, so a UI-only operator could neither trigger the safety
 * check nor act on its advice.
 *
 * `handleTaskCreate` (pkg/gateway/rest_tasks.go) has always honoured both
 * fields on POST — `req.WriteSet`/`req.IsJoin` are copied onto the task —
 * and both are declared on `TaskCreateRequest.yaml`. The gap was purely the
 * form: it never collected them and `buildBody` never sent them.
 *
 * These tests drive the REAL form and assert on the body handed to
 * `createTask`, so a form that renders the controls but forgets to submit
 * them still fails.
 */

import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateTaskSlideOver } from './CreateTaskSlideOver'

beforeAll(() => {
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false
  }
  Element.prototype.scrollIntoView = vi.fn()
})

function selectOption(optionName: string | RegExp) {
  const option = screen.getByRole('option', { name: optionName })
  fireEvent.pointerDown(option, { pointerId: 1, button: 0 })
  fireEvent.click(option)
}

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

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
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

function renderSlideOver(planId?: string | null) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <CreateTaskSlideOver open onOpenChange={vi.fn()} workspaceId="proj-test" planId={planId} />
    </QueryClientProvider>,
  )
}

/** Add a criterion / DoD item through the real editor's draft panel. */
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

/** Add one path through the real write-set editor. */
function addPath(path: string) {
  fireEvent.change(screen.getByLabelText(/add a path this task writes/i), {
    target: { value: path },
  })
  fireEvent.click(screen.getByRole('button', { name: /add path/i }))
}

/** Fill the four mandatory fields so the form will actually submit. */
function fillRequiredFields(title = 'A member task') {
  fireEvent.change(screen.getByLabelText(/title/i), { target: { value: title } })
  fireEvent.change(screen.getByLabelText(/^goal/i), { target: { value: 'Do the thing.' } })
  addItem(/what must be true when this is done\?/i, 'A criterion.')
  addItem(/definition of done item/i, 'A DoD item.')
}

beforeEach(() => {
  vi.mocked(createTask).mockReset().mockResolvedValue(makeCreatedTask() as never)
  vi.mocked(fetchPlans).mockReset().mockResolvedValue([makePlan()] as never)
  vi.mocked(fetchTasks).mockReset().mockResolvedValue([])
  mockAddToast.mockReset()
})

describe('CreateTaskSlideOver — plan-member authoring (write_set + is_join)', () => {
  it('offers no plan-member fields on a standalone task — both are ignored without a plan_id', async () => {
    renderSlideOver()

    expect(await screen.findByText('Plan')).toBeInTheDocument()
    expect(screen.queryByTestId('ct-plan-member-fields')).not.toBeInTheDocument()
    expect(screen.queryByLabelText(/add a path this task writes/i)).not.toBeInTheDocument()
    expect(screen.queryByTestId('plan-member-join-checkbox')).not.toBeInTheDocument()
  })

  it('reveals both plan-member fields as soon as a plan is selected', async () => {
    renderSlideOver()

    await waitFor(() =>
      expect(screen.getByRole('combobox', { name: 'Plan' })).toHaveTextContent('No plan'),
    )
    fireEvent.click(screen.getByRole('combobox', { name: 'Plan' }))
    await screen.findByRole('option', { name: 'Q3 comms' })
    selectOption('Q3 comms')

    expect(await screen.findByTestId('ct-plan-member-fields')).toBeInTheDocument()
    expect(screen.getByLabelText(/add a path this task writes/i)).toBeInTheDocument()
    expect(screen.getByTestId('plan-member-join-checkbox')).toBeInTheDocument()
  })

  it('submits the authored write_set paths on create', async () => {
    renderSlideOver('plan-1')

    await screen.findByTestId('ct-plan-member-fields')
    addPath('out/report.md')
    addPath('pkg/plan/lint.go')
    fillRequiredFields()

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())
    expect(vi.mocked(createTask).mock.calls[0][0].write_set).toEqual([
      'out/report.md',
      'pkg/plan/lint.go',
    ])
  })

  it('submits is_join: true once the merge-point box is ticked', async () => {
    renderSlideOver('plan-1')

    await screen.findByTestId('ct-plan-member-fields')
    fireEvent.click(screen.getByTestId('plan-member-join-checkbox'))
    fillRequiredFields('J3 merge point')

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())
    expect(vi.mocked(createTask).mock.calls[0][0].is_join).toBe(true)
  })

  it('omits both fields when the operator authored neither', async () => {
    renderSlideOver('plan-1')

    await screen.findByTestId('ct-plan-member-fields')
    fillRequiredFields()

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())
    const body = vi.mocked(createTask).mock.calls[0][0]
    expect(body.write_set).toBeUndefined()
    expect(body.is_join).toBeUndefined()
  })

  it('does not submit a stranded write_set after the plan is cleared back to "No plan"', async () => {
    renderSlideOver('plan-1')

    await screen.findByTestId('ct-plan-member-fields')
    addPath('out/report.md')

    fireEvent.click(screen.getByRole('combobox', { name: 'Plan' }))
    await screen.findByRole('option', { name: 'No plan' })
    selectOption('No plan')

    fillRequiredFields()
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())
    const body = vi.mocked(createTask).mock.calls[0][0]
    expect(body.plan_id).toBeUndefined()
    expect(body.write_set).toBeUndefined()
  })

  it('refuses an absolute path inline instead of sending one the plan lint cannot compare', async () => {
    renderSlideOver('plan-1')

    await screen.findByTestId('ct-plan-member-fields')
    addPath('/out/report.md')

    expect(
      await screen.findByText('Use a path relative to the workspace root'),
    ).toBeInTheDocument()
    expect(screen.queryByTestId('write-set-chip')).not.toBeInTheDocument()
  })

  it('removes an authored path again through its own chip', async () => {
    renderSlideOver('plan-1')

    await screen.findByTestId('ct-plan-member-fields')
    addPath('out/report.md')
    addPath('out/summary.md')
    expect(screen.getAllByTestId('write-set-chip')).toHaveLength(2)

    fireEvent.click(screen.getByRole('button', { name: 'Remove path out/report.md' }))

    fillRequiredFields()
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())
    expect(vi.mocked(createTask).mock.calls[0][0].write_set).toEqual(['out/summary.md'])
  })

  it('labels the join control by what ticking it does, not by the field name', async () => {
    renderSlideOver('plan-1')

    await screen.findByTestId('ct-plan-member-fields')
    const panel = screen.getByTestId('ct-plan-member-fields')
    expect(panel.textContent).toContain('This task merges parallel work into one result')
    // The wire field name is an implementation detail, never user-facing copy.
    expect(panel.textContent).not.toMatch(/is_join|write_set/)
  })
})
