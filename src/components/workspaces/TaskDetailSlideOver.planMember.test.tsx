/**
 * TaskDetailSlideOver.planMember.test.tsx
 *
 * Draft isolation between tasks.
 *
 * The sheet does NOT close when the operator clicks a different task on the
 * board: `open={task != null}` stays true, so the panel is re-rendered with a
 * new `task` prop rather than remounted. The panel's own resync effect covers
 * the state it owns (prompt, title, due, errors) — but not the state its
 * CHILDREN own. `WriteSetInput` holds the in-progress path and its inline
 * validation error in local `useState`, which no effect on the panel can
 * reach.
 *
 * So the draft and the error follow the operator from task A into task B's
 * panel, and pressing Add there commits A's path to B — a write to the wrong
 * task, from a control that looks like it is showing B.
 */

import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskDetailSlideOver } from './TaskDetailSlideOver'
import type { Task } from '@/lib/api'

beforeAll(() => {
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false
  }
  Element.prototype.scrollIntoView = vi.fn()
})

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
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-a',
    title: 'A — writes the report',
    action: 'llm',
    priority: 3,
    status: 'next',
    workspace_id: 'ws-test',
    plan_id: 'plan-1',
    surface: 'user',
    owner: 'alice',
    created_by: 'alice',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

const TASK_A = makeTask({ id: 'task-a', write_set: ['out/a.md'] })
const TASK_B = makeTask({ id: 'task-b', title: 'B — writes the schema', write_set: ['out/b.md'] })

beforeEach(() => {
  vi.mocked(updateTask).mockReset().mockResolvedValue({} as never)
  mockAddToast.mockReset()
})

/** Type a path into the write-set editor and press Add. */
function addPath(path: string) {
  fireEvent.change(screen.getByLabelText(/add a path this task writes/i), {
    target: { value: path },
  })
  fireEvent.click(screen.getByRole('button', { name: /add path/i }))
}

describe('TaskDetailSlideOver — switching tasks with the sheet open', () => {
  it('does not carry an in-progress path draft or its error into the next task', async () => {
    const client = makeClient()
    const { rerender } = render(
      <QueryClientProvider client={client}>
        <TaskDetailSlideOver task={TASK_A} onClose={vi.fn()} />
      </QueryClientProvider>,
    )

    await screen.findByLabelText(/add a path this task writes/i)
    addPath('/src/a.go')
    expect(await screen.findByText('Use a path relative to the workspace root')).toBeInTheDocument()
    expect(screen.getByLabelText(/add a path this task writes/i)).toHaveValue('/src/a.go')

    // The board's selection changes. The sheet never closes — both tasks are
    // non-null, so `open` stays true throughout.
    rerender(
      <QueryClientProvider client={client}>
        <TaskDetailSlideOver task={TASK_B} onClose={vi.fn()} />
      </QueryClientProvider>,
    )

    const input = await screen.findByLabelText(/add a path this task writes/i)
    expect(input).toHaveValue('')
    expect(screen.queryByText('Use a path relative to the workspace root')).not.toBeInTheDocument()
    // …and it really is task B's editor.
    expect(screen.getAllByTestId('write-set-chip').map((c) => c.textContent)).toEqual(['out/b.md'])
  })

  it('cannot commit task A\'s abandoned draft to task B', async () => {
    const client = makeClient()
    const { rerender } = render(
      <QueryClientProvider client={client}>
        <TaskDetailSlideOver task={TASK_A} onClose={vi.fn()} />
      </QueryClientProvider>,
    )

    await screen.findByLabelText(/add a path this task writes/i)
    fireEvent.change(screen.getByLabelText(/add a path this task writes/i), {
      target: { value: 'src/only-for-task-a.go' },
    })

    rerender(
      <QueryClientProvider client={client}>
        <TaskDetailSlideOver task={TASK_B} onClose={vi.fn()} />
      </QueryClientProvider>,
    )

    // Nothing is pending in B's box, so Add has nothing to commit.
    await screen.findByLabelText(/add a path this task writes/i)
    expect(screen.getByRole('button', { name: /add path/i })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: /add path/i }))
    expect(vi.mocked(updateTask)).not.toHaveBeenCalled()
  })
})
