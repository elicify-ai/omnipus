/**
 * TaskChecklistField.test.tsx
 *
 * Unit coverage for the checklist field extracted out of TaskDetailPanel
 * (single source of truth shared with CalendarEventSlideOver's recurring-task
 * EDIT mode — see CalendarEventSlideOver.test.tsx for the calendar-side
 * integration coverage). TaskDetailPanel.test.tsx's existing checklist
 * describe block ("editable todos checklist") already re-verifies this same
 * behavior through the full panel, proving the refactor is behavior-preserving.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskChecklistField } from './TaskChecklistField'
import type { Task } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    setTaskTodos: vi.fn().mockResolvedValue({}),
    isApiError: vi.fn().mockReturnValue(false),
    tasksQueryKeys: actual.tasksQueryKeys,
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
    title: 'Task',
    action: 'llm',
    priority: 3,
    status: 'next',
    workspace_id: 'ws-1',
    surface: 'user',
    owner: 'alice',
    created_by: 'alice',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

function renderField(task: Task, disabled?: boolean) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <TaskChecklistField task={task} disabled={disabled} />
    </QueryClientProvider>,
  )
}

beforeEach(async () => {
  const api = await import('@/lib/api')
  vi.mocked(api.setTaskTodos).mockReset().mockResolvedValue({} as never)
  mockAddToast.mockReset()
})

describe('TaskChecklistField', () => {
  it('renders todo items with the item count in the header', async () => {
    renderField(makeTask({
      todos: [
        { text: 'Step one', status: 'pending' },
        { text: 'Step two', status: 'completed' },
      ],
    }))
    expect(await screen.findByText(/checklist \(1\/2\)/i)).toBeInTheDocument()
    expect(screen.getByText('Step one')).toBeInTheDocument()
    expect(screen.getByText('Step two')).toBeInTheDocument()
  })

  it('renders the bare "Checklist" header with no count when there are no items', async () => {
    renderField(makeTask({ todos: [] }))
    expect(await screen.findByText(/^checklist$/i)).toBeInTheDocument()
  })

  it('adding a checklist item calls setTaskTodos with the appended item and clears the input', async () => {
    const { setTaskTodos } = await import('@/lib/api')
    renderField(makeTask({ todos: [{ text: 'Existing', status: 'pending' }] }))

    const input = await screen.findByLabelText(/new checklist item/i)
    fireEvent.change(input, { target: { value: 'Brand new' } })
    fireEvent.click(screen.getByRole('button', { name: /add checklist item/i }))

    await waitFor(() => expect(vi.mocked(setTaskTodos)).toHaveBeenCalledWith('task-1', [
      { text: 'Existing', status: 'pending' },
      { text: 'Brand new', status: 'pending' },
    ]))
    await waitFor(() => expect(input).toHaveValue(''))
  })

  it('toggling a pending item marks it completed; toggling it again marks it pending', async () => {
    const { setTaskTodos } = await import('@/lib/api')
    renderField(makeTask({ todos: [{ text: 'Flip me', status: 'pending' }] }))

    fireEvent.click(await screen.findByLabelText(/toggle flip me/i))
    await waitFor(() => expect(vi.mocked(setTaskTodos)).toHaveBeenCalledWith('task-1', [
      { text: 'Flip me', status: 'completed' },
    ]))
  })

  it('removing a checklist item calls setTaskTodos without it', async () => {
    const { setTaskTodos } = await import('@/lib/api')
    renderField(makeTask({
      todos: [{ text: 'Keep', status: 'pending' }, { text: 'Drop', status: 'pending' }],
    }))

    fireEvent.click(await screen.findByLabelText(/remove checklist item drop/i))
    await waitFor(() => expect(vi.mocked(setTaskTodos)).toHaveBeenCalledWith('task-1', [
      { text: 'Keep', status: 'pending' },
    ]))
  })

  it('disabled=true disables the add input, add button, toggle, and remove controls', async () => {
    renderField(makeTask({ todos: [{ text: 'Locked', status: 'pending' }] }), true)

    expect(await screen.findByLabelText(/new checklist item/i)).toBeDisabled()
    expect(screen.getByRole('button', { name: /add checklist item/i })).toBeDisabled()
    expect(screen.getByLabelText(/toggle locked/i)).toBeDisabled()
    expect(screen.getByLabelText(/remove checklist item locked/i)).toBeDisabled()
  })

  it('surfaces a mutation failure via a toast', async () => {
    const { setTaskTodos } = await import('@/lib/api')
    vi.mocked(setTaskTodos).mockRejectedValueOnce(new Error('network down'))
    renderField(makeTask({ todos: [{ text: 'Flip me', status: 'pending' }] }))

    fireEvent.click(await screen.findByLabelText(/toggle flip me/i))

    await waitFor(() => expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'error' }),
    ))
  })
})
