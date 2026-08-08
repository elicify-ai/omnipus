/**
 * WorkspaceTasksTab.moveMutation.test.tsx
 *
 * 14-reviewer sign-off Finding #3 (MEDIUM, test gap): the Board `onTaskMove`
 * seam WorkspaceTasksTab wires to `moveMutation.mutateAsync`
 * (WorkspaceTasksTab.tsx ~:80-90, :379) had no test exercising the REAL
 * closure — every existing BoardView/BoardViewDnd test injects a plain
 * `vi.fn()` for `onTaskMove`, so a silently no-op wiring (e.g. a dropped
 * mutation call, or a typo'd PATCH body field) would still pass every
 * existing test while BoardView's own ARIA live region announces "moved".
 *
 * This file mocks `BoardView` itself (mirroring this test suite's existing
 * `PlansFilterBand`/`WorkspaceGraphTab` stub convention in
 * WorkspaceTasksTab.test.tsx — a plain button per callback prop) so it can
 * invoke WorkspaceTasksTab's REAL `onTaskMove` closure directly, without
 * needing to re-simulate dnd-kit's pointer/keyboard drag mechanics — those
 * are already fully covered by BoardViewDnd.test.tsx, which is a DIFFERENT
 * seam (BoardView's own drag lifecycle) from the one this file targets
 * (WorkspaceTasksTab's mutation wiring).
 *
 * Per the finding, this file does NOT modify WorkspaceTasksTab.tsx
 * production code — mock-only.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Task } from '@/lib/api'
import { ApiError } from '@/lib/api-error'
import { WorkspaceTasksTab } from './WorkspaceTasksTab'

if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

// ── Mocks ────────────────────────────────────────────────────────────────────

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchTasks: vi.fn(),
    fetchPlans: vi.fn(),
    fetchAgents: vi.fn(),
    fetchWorkspaceDelegation: vi.fn().mockRejectedValue(new Error('not mocked')),
    updateTask: vi.fn(),
    // taskMoveErrorMessage is intentionally left as the REAL implementation
    // (not overridden) — the rejection test below asserts against its
    // actual output, not a re-implemented/duplicated expectation.
  }
})

import { fetchTasks, fetchPlans, fetchAgents, updateTask, taskMoveErrorMessage } from '@/lib/api'

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

vi.mock('./PlansFilterBand', () => ({
  PlansFilterBand: () => <div data-testid="plans-filter-band-stub" />,
}))

vi.mock('./WorkspaceGraphTab', () => ({
  WorkspaceGraphTab: () => <div data-testid="graph-tab-sentinel" />,
}))

// The seam under test: a plain button per task that invokes the REAL
// `onTaskMove` prop WorkspaceTasksTab passes to BoardView
// (`(task, status) => moveMutation.mutateAsync({ task, status })`), without
// needing dnd-kit's real drag mechanics (irrelevant to this file's target —
// the wiring FROM the callback TO the mutation, not how the callback gets
// invoked in a real browser).
vi.mock('./BoardView', () => ({
  BoardView: (props: {
    tasks: Task[]
    onTaskMove?: (task: Task, status: Task['status']) => Promise<unknown> | void
  }) => (
    <div data-testid="board-view-stub">
      {props.tasks.map((task) => (
        <button
          key={task.id}
          type="button"
          onClick={() => {
            // The real onTaskMove (moveMutation.mutateAsync) already routes
            // a rejection to moveMutation.onError -> addToast; swallow the
            // promise here purely so an expected rejection doesn't surface
            // as an unhandled rejection in the test run.
            void Promise.resolve(props.onTaskMove?.(task, 'in_progress')).catch(() => {})
          }}
        >
          move-{task.id}
        </button>
      ))}
    </div>
  ),
}))

// ── Fixtures ─────────────────────────────────────────────────────────────────

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 't1',
    title: 'Write report',
    status: 'inbox',
    action: 'llm',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderTab(client: QueryClient, workspaceId = 'ws-1') {
  return render(
    <QueryClientProvider client={client}>
      <WorkspaceTasksTab workspaceId={workspaceId} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockAddToast.mockReset()
  vi.mocked(fetchTasks).mockReset().mockResolvedValue([])
  vi.mocked(fetchPlans).mockReset().mockResolvedValue([])
  vi.mocked(fetchAgents).mockReset().mockResolvedValue([])
  vi.mocked(updateTask).mockReset()
})

// ── Tests ────────────────────────────────────────────────────────────────────

describe('WorkspaceTasksTab — Board moveMutation wiring (Finding #3: the untested seam)', () => {
  it('a legal move calls updateTask(task.id, {status}) and invalidates the tasks + workspaces query caches on success', async () => {
    const user = userEvent.setup()
    const task = makeTask({ id: 't1', title: 'Write report', status: 'inbox' })
    vi.mocked(fetchTasks).mockResolvedValue([task])
    vi.mocked(updateTask).mockResolvedValue({ ...task, status: 'in_progress' })

    const client = makeClient()
    const invalidateSpy = vi.spyOn(client, 'invalidateQueries')
    renderTab(client)

    const button = await screen.findByRole('button', { name: 'move-t1' })
    await user.click(button)

    // The REAL mutationFn: ({task, status}) => updateTask(task.id, {status}).
    await waitFor(() => expect(updateTask).toHaveBeenCalledWith('t1', { status: 'in_progress' }))

    // The REAL onSuccess: invalidates tasksQueryKeys.list() and
    // workspacesQueryKeys.list() on the query client from context.
    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith(
        expect.objectContaining({ queryKey: expect.arrayContaining(['tasks']) }),
      )
      expect(invalidateSpy).toHaveBeenCalledWith(
        expect.objectContaining({ queryKey: expect.arrayContaining(['workspaces']) }),
      )
    })

    // No error toast on the success path.
    expect(mockAddToast).not.toHaveBeenCalled()
  })

  it('a rejected move surfaces the REAL taskMoveErrorMessage-mapped toast via the real onError handler', async () => {
    const user = userEvent.setup()
    const task = makeTask({ id: 't1', title: 'Write report', status: 'inbox' })
    vi.mocked(fetchTasks).mockResolvedValue([task])
    const conflict = new ApiError(409, 'This conflicts with the current state. Please refresh and try again.', {
      body: '{}',
    })
    vi.mocked(updateTask).mockRejectedValue(conflict)

    const client = makeClient()
    renderTab(client)

    const button = await screen.findByRole('button', { name: 'move-t1' })
    await user.click(button)

    await waitFor(() => expect(updateTask).toHaveBeenCalledWith('t1', { status: 'in_progress' }))

    // `plans` resolves to [] here, so taskMoveErrorMessage's plan-aware 409
    // branch can't apply — computing it independently (against the real,
    // unmocked implementation) keeps this assertion honest rather than
    // duplicating/guessing its output.
    const expectedMessage = taskMoveErrorMessage(conflict, [])
    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith({ message: expectedMessage, variant: 'error' }),
    )
  })

  it('does NOT call updateTask when no move is triggered (sanity: the stub itself does no implicit work)', async () => {
    const task = makeTask({ id: 't1' })
    vi.mocked(fetchTasks).mockResolvedValue([task])
    const client = makeClient()
    renderTab(client)

    await screen.findByRole('button', { name: 'move-t1' })
    expect(updateTask).not.toHaveBeenCalled()
    expect(mockAddToast).not.toHaveBeenCalled()
  })
})
