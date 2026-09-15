/**
 * TaskDetailPanel.assigneeWarning.test.tsx
 *
 * Founder decision 2026-09-15: the task form shows Task.assignee_warning inline,
 * next to the Agent picker it is about (field "agent_id") — the server saved the
 * assignment (an operator may fix the agent's permissions afterwards) and its
 * text names the fix. Expected strings are the founder's wording.
 */

import { describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
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

vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: vi.fn() }
    return selector ? selector(store) : store
  },
}))

const WARNING =
  "Worker isn't allowed to report tasks as done. Allow 'goal_claim' for it in Agents → Tools, or assign another agent."

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Reconcile invoices',
    action: 'llm',
    priority: 3,
    status: 'next',
    workspace_id: 'ws-test',
    surface: 'user',
    owner: 'alice',
    created_by: 'alice',
    created_at: '2026-09-15T11:00:00Z',
    updated_at: '2026-09-15T11:00:00Z',
    agent_id: 'worker',
    agent_name: 'Worker',
    ...overrides,
  }
}

function renderPanel(task: Task) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <TaskDetailPanel task={task} onClose={vi.fn()} />
    </QueryClientProvider>,
  )
}

describe('TaskDetailPanel assignee warning', () => {
  it('shows the warning inside the Agent field', () => {
    renderPanel(makeTask({ assignee_warning: { message: WARNING, field: 'agent_id' } }))
    const warning = screen.getByTestId('task-assignee-warning')
    expect(warning).toHaveTextContent(WARNING)
    // The warning sits in the same field wrapper as the "Agent" label — the
    // control its `field: "agent_id"` names — not in some other section.
    const field = warning.parentElement as HTMLElement
    expect(within(field).getByText('Agent', { selector: 'p' })).toBeInTheDocument()
  })

  it('shows nothing when the agent can finish the task', () => {
    renderPanel(makeTask())
    expect(screen.queryByTestId('task-assignee-warning')).not.toBeInTheDocument()
  })
})
