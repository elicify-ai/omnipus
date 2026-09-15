/**
 * CreateTaskSlideOver.assigneeWarning.test.tsx
 *
 * Founder decision 2026-09-15: the server saves a task assigned to an agent
 * that cannot finish it (an operator may fix the agent's permissions after
 * assigning) and returns Task.assignee_warning. The Create dialog closes on
 * success, so the warning must reach the user as a warning toast — for Create
 * and for Create & Run. A task with no warning gets only the success toast.
 * Expected strings are the founder's wording.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateTaskSlideOver } from './CreateTaskSlideOver'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchTasks: vi.fn().mockResolvedValue([]),
    fetchPlans: vi.fn().mockResolvedValue([]),
    fetchWorkspaceDelegation: vi.fn().mockRejectedValue(new Error('not mocked')),
    createTask: vi.fn(),
    updateTask: vi.fn(),
    tasksQueryKeys: { list: () => ['tasks'] },
    workspacesQueryKeys: {
      list: () => ['workspaces'],
      delegation: (id: string) => ['workspaces', id, 'delegation'],
    },
  }
})

import { createTask, updateTask } from '@/lib/api'

const mockAddToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

const WARNING =
  "Worker isn't allowed to report tasks as done. Allow 'goal_claim' for it in Agents → Tools, or assign another agent."

function savedTask(overrides: Record<string, unknown> = {}) {
  return {
    id: 'new-task',
    title: 'Greet script',
    action: 'llm',
    status: 'inbox',
    priority: 3,
    workspace_id: 'proj-test',
    surface: 'user',
    owner: 'alice',
    created_by: 'alice',
    created_at: '2026-09-15T10:00:00Z',
    updated_at: '2026-09-15T10:00:00Z',
    ...overrides,
  }
}

function renderSlideOver() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <CreateTaskSlideOver open onOpenChange={vi.fn()} workspaceId="proj-test" />
    </QueryClientProvider>,
  )
}

function addItem(inputLabel: RegExp, text: string) {
  const input = screen.getByLabelText(inputLabel)
  fireEvent.change(input, { target: { value: text } })
  const draftPanel = input.parentElement as HTMLElement
  fireEvent.click(within(draftPanel).getByRole('button', { name: /add criterion/i }))
}

function fillValidForm() {
  fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Greet script' } })
  fireEvent.change(screen.getByLabelText(/^goal/i), { target: { value: 'Write greet.py.' } })
  addItem(/what must be true when this is done\?/i, 'greet.py prints Hello World')
  addItem(/definition of done item/i, 'The script exits with status 0.')
}

beforeEach(() => {
  vi.mocked(createTask).mockReset()
  vi.mocked(updateTask).mockReset()
  mockAddToast.mockReset()
})

describe('CreateTaskSlideOver — assignee warning', () => {
  it('Create shows the warning as a warning toast', async () => {
    vi.mocked(createTask).mockResolvedValueOnce(
      savedTask({ assignee_warning: { message: WARNING, field: 'agent_id' } }) as never,
    )
    renderSlideOver()
    fillValidForm()
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith({ message: WARNING, variant: 'warning' }),
    )
    expect(mockAddToast).toHaveBeenCalledWith({ message: 'Task created', variant: 'success' })
  })

  it('Create & Run shows the warning carried by the started task', async () => {
    vi.mocked(createTask).mockResolvedValueOnce(savedTask() as never)
    vi.mocked(updateTask).mockResolvedValueOnce(
      savedTask({ status: 'in_progress', assignee_warning: { message: WARNING, field: 'agent_id' } }) as never,
    )
    renderSlideOver()
    fillValidForm()
    fireEvent.click(screen.getByRole('button', { name: /create & run/i }))

    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith({ message: WARNING, variant: 'warning' }),
    )
  })

  it('a task with no warning gets only the success toast', async () => {
    vi.mocked(createTask).mockResolvedValueOnce(savedTask() as never)
    renderSlideOver()
    fillValidForm()
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith({ message: 'Task created', variant: 'success' }),
    )
    expect(mockAddToast).not.toHaveBeenCalledWith(expect.objectContaining({ variant: 'warning' }))
  })
})
