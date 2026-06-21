/**
 * TaskChildren.test.tsx — the nested subtask list shown under a parent card
 * when the Board altitude is 'show-all'.
 *
 * Covers:
 *   - renders nothing when there are no children (empty → null),
 *   - fetches via fetchSubtasks when no preloaded data is provided, and renders
 *     a row per child with its status label (the altitude-expand fetch path),
 *   - uses preloaded children WITHOUT fetching when they are supplied,
 *   - clicking a child row invokes onChildClick with that child.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskChildren } from './TaskChildren'
import type { Task } from '@/lib/api'

const fetchSubtasksMock = vi.fn()

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSubtasks: (id: string) => fetchSubtasksMock(id),
    tasksQueryKeys: {
      ...actual.tasksQueryKeys,
      subtasks: (id: string) => ['tasks', id, 'subtasks'],
    },
  }
})

function child(over: Partial<Task> = {}): Task {
  return {
    id: 'c1',
    title: 'Child task',
    status: 'in_progress',
    action: 'llm',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    parent_task_id: 'p1',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...over,
  } as Task
}

function renderChildren(props: Partial<React.ComponentProps<typeof TaskChildren>> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <TaskChildren parentTaskId="p1" onChildClick={vi.fn()} {...props} />
    </QueryClientProvider>,
  )
}

describe('TaskChildren', () => {
  beforeEach(() => {
    fetchSubtasksMock.mockReset()
  })

  it('renders nothing when the fetch returns no children (empty → null)', async () => {
    fetchSubtasksMock.mockResolvedValue([])
    const { container } = renderChildren()
    await waitFor(() => expect(fetchSubtasksMock).toHaveBeenCalledWith('p1'))
    expect(container.querySelector('[aria-label="Subtasks"]')).toBeNull()
  })

  it('fetches subtasks and renders a row per child with its status label', async () => {
    fetchSubtasksMock.mockResolvedValue([
      child({ id: 'c1', title: 'First child', status: 'in_progress' }),
      child({ id: 'c2', title: 'Second child', status: 'done' }),
    ])
    renderChildren()
    await screen.findByText('First child')
    expect(screen.getByText('Second child')).toBeInTheDocument()
    expect(screen.getByText('In Progress')).toBeInTheDocument()
    expect(screen.getByText('Done')).toBeInTheDocument()
  })

  it('uses preloaded children without fetching', async () => {
    renderChildren({ preloaded: [child({ id: 'c9', title: 'Preloaded child' })] })
    await screen.findByText('Preloaded child')
    expect(fetchSubtasksMock).not.toHaveBeenCalled()
  })

  it('invokes onChildClick with the clicked child', async () => {
    const onChildClick = vi.fn()
    renderChildren({
      preloaded: [child({ id: 'c5', title: 'Clickable child' })],
      onChildClick,
    })
    fireEvent.click(await screen.findByText('Clickable child'))
    expect(onChildClick).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'c5', title: 'Clickable child' }),
    )
  })
})
