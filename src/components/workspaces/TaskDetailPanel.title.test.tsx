/**
 * TaskDetailPanel.title.test.tsx
 *
 * GOAL-FR-058: the panel's Title becomes editable, following the same
 * click-to-edit/autosave pattern the Prompt (now Goal) field already uses.
 * Test 58 of the goal-entity-spec TDD plan ("panel renames a task and
 * autosaves it").
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
    title: 'Original title',
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

describe('TaskDetailPanel — Title is editable (GOAL-FR-058)', () => {
  it('renders the title read-only with a hidden Edit control', async () => {
    renderPanel(makeTask())
    expect(await screen.findByText('Original title')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /edit title/i })).toBeInTheDocument()
    // No editable input while not in edit mode.
    expect(screen.queryByLabelText(/^title$/i)).toBeNull()
  })

  it('clicking Edit reveals an input pre-filled with the current title', async () => {
    renderPanel(makeTask({ title: 'Fix the deploy script' }))
    fireEvent.click(await screen.findByRole('button', { name: /edit title/i }))

    const input = await screen.findByLabelText(/^title$/i)
    expect(input).toHaveValue('Fix the deploy script')
  })

  it('saving a new title autosaves via doUpdate({ title }) — no Save button anywhere in the panel', async () => {
    const { updateTask } = await import('@/lib/api')
    renderPanel(makeTask({ title: 'Old name' }))

    fireEvent.click(await screen.findByRole('button', { name: /edit title/i }))
    const input = await screen.findByLabelText(/^title$/i)
    fireEvent.change(input, { target: { value: 'New name' } })
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledWith('task-1', { title: 'New name' }))
  })

  it('pressing Enter in the title input saves it too', async () => {
    const { updateTask } = await import('@/lib/api')
    renderPanel(makeTask({ title: 'Old name' }))

    fireEvent.click(await screen.findByRole('button', { name: /edit title/i }))
    const input = await screen.findByLabelText(/^title$/i)
    fireEvent.change(input, { target: { value: 'Renamed via Enter' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledWith('task-1', { title: 'Renamed via Enter' }))
  })

  it('refuses to save a blank title and never calls updateTask', async () => {
    const { updateTask } = await import('@/lib/api')
    renderPanel(makeTask({ title: 'Keep me' }))

    fireEvent.click(await screen.findByRole('button', { name: /edit title/i }))
    const input = await screen.findByLabelText(/^title$/i)
    fireEvent.change(input, { target: { value: '   ' } })
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    expect(await screen.findByText(/title is required/i)).toBeInTheDocument()
    expect(vi.mocked(updateTask)).not.toHaveBeenCalled()
  })

  it('Cancel discards the draft and reverts to the original title with no autosave', async () => {
    const { updateTask } = await import('@/lib/api')
    renderPanel(makeTask({ title: 'Do not change me' }))

    fireEvent.click(await screen.findByRole('button', { name: /edit title/i }))
    const input = await screen.findByLabelText(/^title$/i)
    fireEvent.change(input, { target: { value: 'Discarded draft' } })
    fireEvent.click(screen.getByRole('button', { name: /^cancel$/i }))

    expect(await screen.findByText('Do not change me')).toBeInTheDocument()
    expect(screen.queryByText('Discarded draft')).toBeNull()
    expect(vi.mocked(updateTask)).not.toHaveBeenCalled()
  })

  it('does not PATCH when the trimmed title is unchanged', async () => {
    const { updateTask } = await import('@/lib/api')
    renderPanel(makeTask({ title: 'Stays the same' }))

    fireEvent.click(await screen.findByRole('button', { name: /edit title/i }))
    const input = await screen.findByLabelText(/^title$/i)
    fireEvent.change(input, { target: { value: '  Stays the same  ' } })
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(screen.queryByRole('button', { name: /^save$/i })).toBeNull())
    expect(vi.mocked(updateTask)).not.toHaveBeenCalled()
  })
})
