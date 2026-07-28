// D9 — WorkspaceTasksTab (Board/List) query-error state.
//
// Before this fix, a failed tasks query (with nothing cached) rendered a
// plain dead-end <div> with no way to recover short of a full page reload —
// see the UAT defect inventory ("Board/List | no retry — dead-end text").
// This is a focused test file covering ONLY that error path (not the whole
// Board/List feature surface, which has no pre-existing dedicated suite);
// heavy child views (BoardView/ListView/etc.) are stubbed out.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { WorkspaceTasksTab } from './WorkspaceTasksTab'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchTasks: vi.fn(),
    fetchMilestones: vi.fn(),
    fetchAgents: vi.fn(),
    updateTask: vi.fn(),
    tasksQueryKeys: { list: (params?: Record<string, unknown>) => ['tasks', params ?? {}] },
    milestonesQueryKeys: { list: (workspaceId: string) => ['milestones', workspaceId] },
    workspacesQueryKeys: { list: (params?: Record<string, unknown>) => ['workspaces', params ?? {}] },
  }
})

import { fetchTasks, fetchMilestones, fetchAgents } from '@/lib/api'

vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const state = { addToast: vi.fn() }
    return selector ? selector(state) : state
  },
}))

// Heavy child views are irrelevant to the error-path under test — stub them.
vi.mock('./BoardView', () => ({ BoardView: () => <div data-testid="board-view-stub" /> }))
vi.mock('./ListView', () => ({ ListView: () => <div data-testid="list-view-stub" /> }))
vi.mock('./MilestoneFilterPills', () => ({
  MilestoneFilterPills: () => <div data-testid="milestone-pills-stub" />,
  MILESTONE_FILTER_UNSCHEDULED: '__unscheduled__',
}))
vi.mock('./TaskDetailSlideOver', () => ({ TaskDetailSlideOver: () => null }))
vi.mock('./CreateTaskSlideOver', () => ({ CreateTaskSlideOver: () => null }))
vi.mock('./CreateMilestoneSlideOver', () => ({ CreateMilestoneSlideOver: () => null }))

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  })
}

function renderTab(mode: 'board' | 'list' = 'board') {
  return render(
    <QueryClientProvider client={makeClient()}>
      <WorkspaceTasksTab workspaceId="ws-1" mode={mode} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchMilestones).mockResolvedValue([])
  vi.mocked(fetchAgents).mockResolvedValue([])
})

describe('WorkspaceTasksTab — tasks query error state (D9)', () => {
  it('renders the shared QueryErrorState (not the old dead-end text) when the tasks query fails with no cached tasks', async () => {
    vi.mocked(fetchTasks).mockRejectedValue(new Error('500 Server Error'))

    renderTab()

    await waitFor(() => {
      expect(screen.getByTestId('workspace-tasks-error')).toBeInTheDocument()
    })
    expect(screen.getByText(/failed to load tasks/i)).toBeInTheDocument()
    // A working Retry action must be present — the D9 defect was specifically
    // "no retry — dead-end text".
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
  })

  it('clicking Retry re-invokes fetchTasks', async () => {
    vi.mocked(fetchTasks).mockRejectedValue(new Error('500 Server Error'))

    renderTab()

    await waitFor(() => {
      expect(screen.getByTestId('workspace-tasks-error')).toBeInTheDocument()
    })

    const callsBefore = vi.mocked(fetchTasks).mock.calls.length
    vi.mocked(fetchTasks).mockResolvedValueOnce([])

    fireEvent.click(screen.getByRole('button', { name: /retry/i }))

    await waitFor(() => {
      expect(vi.mocked(fetchTasks).mock.calls.length).toBeGreaterThan(callsBefore)
    })
    // A successful retry clears the error state and shows the real view.
    await waitFor(() => {
      expect(screen.queryByTestId('workspace-tasks-error')).not.toBeInTheDocument()
      expect(screen.getByTestId('board-view-stub')).toBeInTheDocument()
    })
  })

  it('does NOT render the error state on a successful load', async () => {
    vi.mocked(fetchTasks).mockResolvedValue([])

    renderTab()

    await waitFor(() => {
      expect(screen.getByTestId('board-view-stub')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('workspace-tasks-error')).not.toBeInTheDocument()
  })
})
