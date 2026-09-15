/**
 * TaskDetailPanel.criteria.test.tsx
 *
 * GOAL-FR-053 (panel half) + D-C's uniform edit gate: the panel marks
 * Acceptance criteria and Definition of Done as required, the D5 soft-tier
 * empty hint is retired with no replacement text, and — new to this wave —
 * an edit that would leave the task with zero criteria or zero DoD items is
 * refused: the autosave is blocked, a reason is shown, and the list reverts
 * to its prior (non-empty) contents because the blocked doUpdate never
 * changes what `task.criteria`/`task.dod` the editor is fed.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskDetailPanel } from './TaskDetailPanel'
import type { Task, AcceptanceCriterion } from '@/lib/api'

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSubtasks: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    fetchTasks: vi.fn().mockResolvedValue([]),
    fetchPlans: vi.fn().mockResolvedValue([]),
    fetchTaskEvidence: vi.fn().mockResolvedValue([]),
    fetchTaskVerdicts: vi.fn().mockResolvedValue([]),
    fetchWorkspaceDelegation: vi.fn().mockRejectedValue(new Error('not mocked')),
    updateTask: vi.fn().mockResolvedValue({}),
    deleteTask: vi.fn().mockResolvedValue(undefined),
    setTaskTodos: vi.fn().mockResolvedValue({}),
    setTaskDependencies: vi.fn().mockResolvedValue({}),
    stopTaskGoalLoop: vi.fn().mockResolvedValue({}),
    isApiError: vi.fn().mockReturnValue(false),
    tasksQueryKeys: actual.tasksQueryKeys,
    taskEvidenceQueryKeys: actual.taskEvidenceQueryKeys,
    taskVerdictsQueryKeys: actual.taskVerdictsQueryKeys,
    workspacesQueryKeys: actual.workspacesQueryKeys,
    plansQueryKeys: actual.plansQueryKeys,
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function makeCriterion(overrides: Partial<AcceptanceCriterion> = {}): AcceptanceCriterion {
  return {
    id: 'crit-1',
    kind: 'prose',
    judgment: 'boolean',
    text: 'Default criterion',
    author: { kind: 'user', id: 'alice' },
    status: 'pending',
    ...overrides,
  }
}

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'A task',
    action: 'llm',
    priority: 3,
    status: 'next',
    workspace_id: 'ws-test',
    surface: 'user',
    owner: 'alice',
    created_by: 'alice',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

function renderPanel(task: Task | null) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <TaskDetailPanel task={task} onClose={vi.fn()} />
    </QueryClientProvider>,
  )
}

beforeEach(async () => {
  const api = await import('@/lib/api')
  vi.mocked(api.updateTask).mockReset().mockResolvedValue({} as never)
  mockAddToast.mockReset()
})

describe('TaskDetailPanel — Acceptance criteria required, hint removed (GOAL-FR-053)', () => {
  it('marks the Acceptance criteria field required with the same asterisk style the Title/Goal fields use', async () => {
    renderPanel(makeTask())
    const label = await screen.findByText(/acceptance criteria/i)
    const fieldRoot = label.closest('div') as HTMLElement
    expect(fieldRoot.querySelector('.text-\\[var\\(--color-error\\)\\]')).not.toBeNull()
  })

  it('the D5 soft-tier hint sentence never appears, and gets no replacement text on this surface', async () => {
    renderPanel(makeTask({ criteria: [] }))
    await screen.findByText(/acceptance criteria/i)
    // The exact two sentences GOAL-FR-053 requires reduced to zero hits.
    expect(screen.queryByText(/no criteria — this task will be judged/i)).toBeNull()
    expect(screen.queryByText(/no criteria added — this task will be judged/i)).toBeNull()
  })
})

describe('TaskDetailPanel — D-C uniform edit gate: removing the last item is refused', () => {
  // Every seeded criterion/DoD text below legitimately renders TWICE once
  // the panel mounts: once editable (AcceptanceCriteriaEditor/
  // DefinitionOfDoneEditor) and once as a CriteriaVerdictList verdict row
  // fed the same array (GOAL-FR-054/055). Readiness waits below therefore
  // target the unambiguous "Remove criterion …" button instead of the text
  // itself, and any presence/absence check on the text uses
  // getAllByText/queryAllByText rather than the singular form.

  it('removing the last acceptance criterion is blocked, states why, and never calls updateTask', async () => {
    const { updateTask } = await import('@/lib/api')
    renderPanel(makeTask({ criteria: [makeCriterion({ id: 'crit-1', text: 'Only criterion' })] }))

    const removeBtn = await screen.findByRole('button', { name: /remove criterion only criterion/i })
    fireEvent.click(removeBtn)

    expect(await screen.findByText(/must keep at least one acceptance criterion/i)).toBeInTheDocument()
    expect(vi.mocked(updateTask)).not.toHaveBeenCalled()
    // The item reappears — the blocked autosave never changed `task.criteria`,
    // so the controlled editor reverts the attempted removal (still present
    // at least once, in the editable list).
    expect(screen.getAllByText('Only criterion').length).toBeGreaterThanOrEqual(1)
  })

  it('removing the last DoD item is blocked the same way, independent of the criteria gate', async () => {
    const { updateTask } = await import('@/lib/api')
    renderPanel(makeTask({
      criteria: [makeCriterion({ id: 'crit-1', text: 'Keeps this one' })],
      dod: [makeCriterion({ id: 'dod-1', text: 'Only DoD item' })],
    }))

    const removeBtn = await screen.findByRole('button', { name: /remove criterion only dod item/i })
    fireEvent.click(removeBtn)

    expect(await screen.findByText(/must keep at least one definition-of-done item/i)).toBeInTheDocument()
    expect(vi.mocked(updateTask)).not.toHaveBeenCalled()
    expect(screen.getAllByText('Only DoD item').length).toBeGreaterThanOrEqual(1)
  })

  it('removing one of TWO criteria succeeds normally (the gate only fires at zero)', async () => {
    const { updateTask } = await import('@/lib/api')
    renderPanel(makeTask({
      criteria: [
        makeCriterion({ id: 'crit-1', text: 'First criterion' }),
        makeCriterion({ id: 'crit-2', text: 'Second criterion' }),
      ],
    }))

    const removeBtn = await screen.findByRole('button', { name: /remove criterion second criterion/i })
    fireEvent.click(removeBtn)

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledWith(
      'task-1',
      { criteria: [expect.objectContaining({ id: 'crit-1', text: 'First criterion' })] },
    ))
    expect(screen.queryByText(/must keep at least one/i)).toBeNull()
  })

  it('adding a criterion clears a previously-shown refusal reason', async () => {
    renderPanel(makeTask({ criteria: [makeCriterion({ id: 'crit-1', text: 'Solo criterion' })] }))

    const removeBtn = await screen.findByRole('button', { name: /remove criterion solo criterion/i })
    fireEvent.click(removeBtn)
    expect(await screen.findByText(/must keep at least one acceptance criterion/i)).toBeInTheDocument()

    // Add a new one back through the draft input, scoped to its own draft
    // panel — the DoD editor renders its own, differently-labelled draft
    // input with an "Add criterion" button of its own, so an unscoped query
    // would be ambiguous whenever both editors are on the page.
    const input = screen.getByLabelText(/what must be true when this is done\?/i)
    fireEvent.change(input, { target: { value: 'Replacement criterion' } })
    const draftPanel = input.parentElement as HTMLElement
    fireEvent.click(within(draftPanel).getByRole('button', { name: /add criterion/i }))

    await waitFor(() => expect(screen.queryByText(/must keep at least one acceptance criterion/i)).toBeNull())
  })
})
