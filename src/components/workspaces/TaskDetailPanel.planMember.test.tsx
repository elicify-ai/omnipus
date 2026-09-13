/**
 * TaskDetailPanel.planMember.test.tsx
 *
 * UAT defect A, detail-panel half. The panel's sections ran TITLE…ARTIFACTS
 * with no write-set field and no join control anywhere, so an operator who
 * hit `pkg/plan/lint.go`'s overlapping-write or join-less-convergence refusal
 * had no way to edit the two fields the refusal names — `is_join` was
 * reachable only by hand-crafting a PATCH.
 *
 * Both fields already exist on `TaskUpdateRequest.yaml` and are honoured by
 * `handleTaskPatch` (pkg/gateway/rest_tasks.go), so the fix is the control,
 * not the contract. These tests assert on the PATCH body handed to
 * `updateTask`, so a panel that renders the controls but never saves them
 * still fails.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskDetailPanel } from './TaskDetailPanel'
import type { Task } from '@/lib/api'

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

import { updateTask } from '@/lib/api'

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

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'W1 writes report',
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

beforeEach(() => {
  vi.mocked(updateTask).mockReset().mockResolvedValue({} as never)
  mockAddToast.mockReset()
})

describe('TaskDetailPanel — plan-member fields (write_set + is_join)', () => {
  it('hides both fields on a standalone task — neither is meaningful without a plan', async () => {
    renderPanel(makeTask({ plan_id: undefined }))

    await screen.findByText('Tags')
    expect(screen.queryByLabelText(/add a path this task writes/i)).not.toBeInTheDocument()
    expect(screen.queryByTestId('plan-member-join-checkbox')).not.toBeInTheDocument()
  })

  it('shows both fields on a plan member', async () => {
    renderPanel(makeTask({ plan_id: 'plan-1' }))

    expect(await screen.findByLabelText(/add a path this task writes/i)).toBeInTheDocument()
    expect(screen.getByTestId('plan-member-join-checkbox')).toBeInTheDocument()
    expect(screen.getByText('Files this task writes')).toBeInTheDocument()
  })

  it('renders the task\'s existing write_set as removable chips', async () => {
    renderPanel(makeTask({ plan_id: 'plan-1', write_set: ['out/report.md', 'src/schema.go'] }))

    const chips = await screen.findAllByTestId('write-set-chip')
    expect(chips.map((c) => c.textContent)).toEqual(['out/report.md', 'src/schema.go'])
  })

  it('PATCHes the full replacement write_set when a path is added', async () => {
    renderPanel(makeTask({ plan_id: 'plan-1', write_set: ['out/report.md'] }))

    const input = await screen.findByLabelText(/add a path this task writes/i)
    fireEvent.change(input, { target: { value: 'src/schema.go' } })
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())
    expect(vi.mocked(updateTask).mock.calls[0][1]).toEqual({
      write_set: ['out/report.md', 'src/schema.go'],
    })
  })

  it('PATCHes the remaining write_set when a path is removed', async () => {
    renderPanel(makeTask({ plan_id: 'plan-1', write_set: ['out/report.md', 'src/schema.go'] }))

    fireEvent.click(await screen.findByRole('button', { name: 'Remove path out/report.md' }))

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())
    expect(vi.mocked(updateTask).mock.calls[0][1]).toEqual({ write_set: ['src/schema.go'] })
  })

  it('PATCHes is_join: true when the merge-point box is ticked', async () => {
    renderPanel(makeTask({ plan_id: 'plan-1', title: 'J3 merge point' }))

    fireEvent.click(await screen.findByTestId('plan-member-join-checkbox'))

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())
    expect(vi.mocked(updateTask).mock.calls[0][1]).toEqual({ is_join: true })
  })

  it('PATCHes is_join: false when an already-marked join member is un-ticked', async () => {
    renderPanel(makeTask({ plan_id: 'plan-1', is_join: true }))

    const box = await screen.findByTestId('plan-member-join-checkbox')
    expect(box).toHaveAttribute('data-state', 'checked')
    fireEvent.click(box)

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())
    expect(vi.mocked(updateTask).mock.calls[0][1]).toEqual({ is_join: false })
  })

  it('reflects an existing is_join: true as a ticked box', async () => {
    renderPanel(makeTask({ plan_id: 'plan-1', is_join: true }))
    expect(await screen.findByTestId('plan-member-join-checkbox')).toHaveAttribute(
      'data-state',
      'checked',
    )
  })
})
