/**
 * TaskDetailPanel.no-milestone.test.tsx
 *
 * ADR-049 (US-11 AS-7, SC-040): the Milestone `SmartSelect` (formerly
 * `TaskDetailPanel.tsx:641-655`) is gone entirely — replaced by a Tags input
 * (migrated `milestone:<name>` tags render as ordinary chips) plus the
 * acceptance-criteria editor and Clear/Stop goal-loop affordance.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
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
    // Plan Swimlane redesign — the "Move to plan…" picker's plans query.
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
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Research task',
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
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <TaskDetailPanel task={task} onClose={vi.fn()} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockAddToast.mockReset()
})

describe('TaskDetailPanel — no milestone UI anywhere (SC-040)', () => {
  it('renders no "Milestone" field, label, or dropdown', async () => {
    renderPanel(makeTask())
    // Let the initial queries settle before asserting absence.
    await screen.findByText('Research task')
    expect(screen.queryByText(/^milestone$/i)).toBeNull()
    expect(screen.queryByRole('combobox', { name: /milestone/i })).toBeNull()
  })

  it('renders the Tags field (TagInput) in its place', async () => {
    renderPanel(makeTask())
    expect(await screen.findByRole('textbox', { name: /add tag/i })).toBeInTheDocument()
  })

  it('a migrated milestone:<name> tag renders as an ordinary chip', async () => {
    renderPanel(makeTask({ tags: ['milestone:q3'] }))
    expect(await screen.findByText('milestone:q3')).toBeInTheDocument()
  })

  it('renders the acceptance-criteria editor', async () => {
    renderPanel(makeTask())
    expect(await screen.findByText(/acceptance criteria/i)).toBeInTheDocument()
    expect(screen.getByText(/no criteria/i)).toBeInTheDocument()
  })

  it('shows the Clear/Stop goal-loop button only for a running task with a live attempt', async () => {
    renderPanel(makeTask({ status: 'in_progress', attempt_count: 1, max_attempts: 3 }))
    expect(await screen.findByRole('button', { name: /stop\/clear goal loop/i })).toBeInTheDocument()
  })

  it('does NOT show the Clear/Stop goal-loop button when the task has no attempt yet', async () => {
    renderPanel(makeTask({ status: 'in_progress' }))
    await screen.findByText('Research task')
    expect(screen.queryByRole('button', { name: /stop\/clear goal loop/i })).toBeNull()
  })
})
