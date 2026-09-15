/**
 * TaskDetailPanel.lastActivity.test.tsx
 *
 * Founder decision 2026-09-14 (UAT E-15c): the task detail panel shows how long
 * ago a running task last did anything, beside its In Progress status badge.
 *
 * Only `Date` is faked (not timers), so react-query's own scheduling keeps
 * working while the chip's age is computed against a fixed clock.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
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

const NOW = Date.parse('2026-09-14T12:00:00Z')

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Reconcile invoices',
    action: 'llm',
    priority: 3,
    status: 'in_progress',
    workspace_id: 'ws-test',
    surface: 'user',
    owner: 'alice',
    created_by: 'alice',
    created_at: '2026-09-14T11:00:00Z',
    updated_at: '2026-09-14T11:00:00Z',
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

describe('TaskDetailPanel last activity', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(NOW)
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('shows the last activity beside the In Progress badge for a running task', () => {
    renderPanel(makeTask({ last_activity_at: new Date(NOW - 42_000).toISOString() }))

    const chip = screen.getByTestId('task-last-activity')
    expect(chip).toHaveTextContent(/^Last activity 42 s ago$/)
    expect(chip.parentElement).toHaveTextContent('In Progress')
  })

  it('shows no last activity for a task that is not running', () => {
    renderPanel(makeTask({ status: 'next', last_activity_at: new Date(NOW - 42_000).toISOString() }))
    expect(screen.queryByTestId('task-last-activity')).toBeNull()
  })
})
