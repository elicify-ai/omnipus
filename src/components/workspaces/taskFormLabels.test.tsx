/**
 * taskFormLabels.test.tsx
 *
 * GOAL-FR-056 ("Prompt is renamed Goal") and GOAL-FR-057 ("Checklist is
 * relabelled Todos"), asserted across BOTH task surfaces in one file — the
 * create form (`CreateTaskSlideOver`, U4's half) and the detail panel
 * (`TaskDetailPanel`, this wave's half) — so a future change to either
 * surface that leaves the two disagreeing goes red here rather than passing
 * two separate suites that each only look at their own half.
 *
 * This file RENDERS `CreateTaskSlideOver.tsx`; it does not edit it (joint
 * delivery plan U5 row).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateTaskSlideOver } from './CreateTaskSlideOver'
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
    fetchTasks: vi.fn().mockResolvedValue([]),
    fetchPlans: vi.fn().mockResolvedValue([]),
    fetchSubtasks: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    fetchTaskEvidence: vi.fn().mockResolvedValue([]),
    fetchTaskVerdicts: vi.fn().mockResolvedValue([]),
    fetchWorkspaceDelegation: vi.fn().mockRejectedValue(new Error('not mocked')),
    createTask: vi.fn(),
    updateTask: vi.fn().mockResolvedValue({}),
    deleteTask: vi.fn().mockResolvedValue(undefined),
    setTaskTodos: vi.fn().mockResolvedValue({}),
    setTaskDependencies: vi.fn().mockResolvedValue({}),
    stopTaskGoalLoop: vi.fn().mockResolvedValue({}),
    isApiError: vi.fn().mockReturnValue(false),
    tasksQueryKeys: actual.tasksQueryKeys,
    boardTasksQueryKeys: actual.tasksQueryKeys,
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

function renderCreateForm() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <CreateTaskSlideOver open onOpenChange={vi.fn()} workspaceId="ws-1" />
    </QueryClientProvider>,
  )
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

function renderPanel(task: Task) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <TaskDetailPanel task={task} onClose={vi.fn()} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockAddToast.mockReset()
})

describe('Both task forms label the Goal field "Goal" (GOAL-FR-056)', () => {
  it('CreateTaskSlideOver: the field reads "Goal", not "Prompt", with the new placeholder', async () => {
    renderCreateForm()
    expect(await screen.findByText(/^goal/i)).toBeInTheDocument()
    expect(screen.queryByText(/^prompt$/i)).toBeNull()
    expect(screen.getByPlaceholderText('What should this task achieve?')).toBeInTheDocument()
    expect(screen.queryByPlaceholderText(/describe what the agent should do/i)).toBeNull()
  })

  it('TaskDetailPanel: the field reads "Goal", not "Prompt / Instructions"', async () => {
    renderPanel(makeTask({ prompt: 'Existing instructions.' }))
    expect(await screen.findByText(/^goal$/i)).toBeInTheDocument()
    expect(screen.queryByText(/prompt \/ instructions/i)).toBeNull()
    expect(screen.queryByText(/^prompt$/i)).toBeNull()
  })

  it('the wire field itself stays `prompt` — this is a label-only rename (no contract change implied)', async () => {
    const { updateTask } = await import('@/lib/api')
    renderPanel(makeTask({ prompt: 'Keep me.' }))
    // Sanity: the panel still reads task.prompt into the field's content,
    // proving the rename touched only the label, not the underlying field.
    expect(await screen.findByText('Keep me.')).toBeInTheDocument()
    expect(vi.mocked(updateTask)).not.toHaveBeenCalled()
  })
})

describe('Both task forms label the todos field "Todos" (GOAL-FR-057)', () => {
  it('CreateTaskSlideOver: its own inline field reads "Todos" with the new placeholder', async () => {
    renderCreateForm()
    expect(await screen.findByText(/^todos$/i)).toBeInTheDocument()
    expect(screen.queryByText(/^checklist$/i)).toBeNull()
    expect(screen.getByPlaceholderText('Add a todo…')).toBeInTheDocument()
    expect(screen.queryByPlaceholderText(/add a checklist item/i)).toBeNull()
    // Its aria-label is intentionally its own and was never "checklist"-worded.
    expect(screen.getByLabelText(/new checklist item/i)).toBeInTheDocument()
  })

  it('TaskDetailPanel: the shared TaskChecklistField header reads "Todos" with the new placeholder', async () => {
    renderPanel(makeTask({ todos: [] }))
    expect(await screen.findByText(/^todos$/i)).toBeInTheDocument()
    expect(screen.queryByText(/^checklist$/i)).toBeNull()
    expect(screen.getByPlaceholderText('Add a todo…')).toBeInTheDocument()
    // C-62/C-79: the visible text changes; every aria-label on the shared
    // component keeps saying "checklist", byte-identical.
    expect(screen.getByLabelText(/new checklist item/i)).toBeInTheDocument()
  })

  it('the exported component/file name stays TaskChecklistField — the rename is label-only, not a symbol rename', async () => {
    // A compile-time assertion, effectively: if this import ever breaks
    // because the symbol was renamed, this file fails to even load.
    const mod = await import('./TaskChecklistField')
    expect(typeof mod.TaskChecklistField).toBe('function')
  })
})
