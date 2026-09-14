/**
 * TaskDetailPanel.depError.test.tsx
 *
 * Live-UAT defect (lane W2-L4, scenario E-10): creating a dependency cycle
 * in the "Depends on" editor (A depends on B, then B depends on A) sends
 * `PUT /api/v1/tasks/{A}/dependencies` → HTTP 400 with a plain-language
 * rejection (`{"error":"...\"Research task\" can't depend on ...","field":
 * "blocked_by"}` — see pkg/task/blocked_by.go's cycle check) and the UI
 * showed NOTHING — the checkbox just failed to apply, with only the
 * console logging the 400. This is the same defect class as A-11
 * (CreateTaskSlideOver.serverError.test.tsx): the server is the sole
 * authority for the cycle rule (pkg/task's cycle check) — the SPA never
 * reimplements it, it only has to DISPLAY the server's own message. Both
 * call sites share one recognizer (`getErrorMessage`, from
 * taskValidationError.ts's header) rather than a copy per component.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskDetailPanel } from './TaskDetailPanel'
import { ApiError } from '@/lib/api'
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
    setTaskDependencies: vi.fn(),
    stopTaskGoalLoop: vi.fn().mockResolvedValue({}),
    fetchTaskRuns: vi.fn().mockResolvedValue([]),
    runTaskNow: vi.fn().mockResolvedValue(undefined),
    // isApiError/getErrorMessage/ApiError all fall through to the real
    // implementation (via `...actual`) — `fakeApiError` below constructs a
    // real `ApiError` instance, so `err instanceof ApiError` (what both
    // helpers actually check) is genuinely true, same as production.
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

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

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

function renderPanel(task: Task | null, onClose = vi.fn()) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <TaskDetailPanel task={task} onClose={onClose} />
    </QueryClientProvider>,
  )
}

/** A real `ApiError` instance, exactly as `request()` would throw it. */
function fakeApiError(userMessage: string) {
  return new ApiError(400, userMessage)
}

// The plain-language rejection the server now sends for a dependency cycle
// (task-1's title is "Research task", other-1's is "Other Task One") — see
// pkg/task/blocked_by.go's detectCycleDFS.
const CYCLE_MESSAGE =
  '"Research task" can\'t depend on "Other Task One": "Other Task One" already depends on it, ' +
  'which would create a loop'

beforeEach(async () => {
  const api = await import('@/lib/api')
  vi.mocked(api.setTaskDependencies).mockReset()
  vi.mocked(api.fetchTasks).mockReset().mockResolvedValue([])
  mockAddToast.mockReset()
})

describe('TaskDetailPanel — surfaces a rejected dependency PUT (E-10)', () => {
  it('renders the server blocked_by-cycle message inline under "Depends on"', async () => {
    const { fetchTasks, setTaskDependencies } = await import('@/lib/api')
    vi.mocked(fetchTasks).mockResolvedValueOnce([
      makeTask({ id: 'other-1', title: 'Other Task One' }),
    ])
    vi.mocked(setTaskDependencies).mockRejectedValueOnce(fakeApiError(CYCLE_MESSAGE))

    renderPanel(makeTask({ id: 'task-1', status: 'next', workspace_id: 'ws-test' }))

    fireEvent.click(await screen.findByText(/no dependencies/i))
    fireEvent.click(await screen.findByText('Other Task One'))

    await waitFor(() => expect(vi.mocked(setTaskDependencies)).toHaveBeenCalledOnce())

    // The server's own message renders verbatim, inline — not just a toast.
    expect(await screen.findByText(CYCLE_MESSAGE)).toBeInTheDocument()
    expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({ message: CYCLE_MESSAGE, variant: 'error' }),
    )

    // The checkbox never applied — `blockedBy` still renders from the
    // server's actual (unchanged) value, matching the live-UAT report
    // ("the checkbox just fails to apply").
    expect(screen.getByText(/no dependencies/i)).toBeInTheDocument()
  })

  it('renders a visible message for a non-cycle (generic) dependency-PUT failure', async () => {
    const { fetchTasks, setTaskDependencies } = await import('@/lib/api')
    vi.mocked(fetchTasks).mockResolvedValueOnce([
      makeTask({ id: 'other-1', title: 'Other Task One' }),
    ])
    vi.mocked(setTaskDependencies).mockRejectedValueOnce(
      fakeApiError('The server is unavailable. Please try again in a moment.'),
    )

    renderPanel(makeTask({ id: 'task-1', status: 'next', workspace_id: 'ws-test' }))

    fireEvent.click(await screen.findByText(/no dependencies/i))
    fireEvent.click(await screen.findByText('Other Task One'))

    expect(
      await screen.findByText(/the server is unavailable\. please try again in a moment\./i),
    ).toBeInTheDocument()
  })

  it('clears the dependency error once a retry succeeds', async () => {
    const { fetchTasks, setTaskDependencies } = await import('@/lib/api')
    vi.mocked(fetchTasks).mockResolvedValueOnce([
      makeTask({ id: 'other-1', title: 'Other Task One' }),
    ])
    vi.mocked(setTaskDependencies)
      .mockRejectedValueOnce(fakeApiError(CYCLE_MESSAGE))
      .mockResolvedValueOnce({} as never)

    renderPanel(makeTask({ id: 'task-1', status: 'next', workspace_id: 'ws-test' }))

    fireEvent.click(await screen.findByText(/no dependencies/i))
    fireEvent.click(await screen.findByText('Other Task One'))
    expect(await screen.findByText(CYCLE_MESSAGE)).toBeInTheDocument()

    // Retry the same toggle — this attempt succeeds. The popover stays open
    // across the first (failed) selection (it's a multi-select checklist,
    // not a single-select-and-close menu), so the retry clicks the same
    // item again rather than re-opening the trigger.
    fireEvent.click(await screen.findByText('Other Task One'))

    await waitFor(() => expect(vi.mocked(setTaskDependencies)).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.queryByText(CYCLE_MESSAGE)).toBeNull())
  })
})
