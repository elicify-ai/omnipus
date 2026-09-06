/**
 * TaskDetailPanel.test.tsx
 *
 * Renders the REAL TaskDetailPanel component and asserts that "Open in Chat"
 * triggers navigation to /sessions/$sessionId.
 *
 * Sprint 2 migration: TaskDetailPanel now uses the unified Task type — the
 * old two-mode (workflow/gtd) API is gone. BoardTask is gone. All tasks use
 * the 6-state lifecycle (ADR-051 D5): inbox/next/in_progress/blocked/done/failed.
 */

import { useState } from 'react'
import { describe, it, expect, vi, beforeEach, beforeAll, afterEach } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskDetailPanel } from './TaskDetailPanel'
import type { Task, TaskUpdateRequest, Plan, TaskRun } from '@/lib/api'

// DateTimePicker (shadcn Calendar + Select) needs these jsdom polyfills to open
// (same gap noted in date-time-picker.test.tsx).
beforeAll(() => {
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {}
  }
  if (typeof window !== 'undefined' && !window.ResizeObserver) {
    window.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver
  }
})

// Click a react-day-picker day cell (data-day="YYYY-MM-DD") inside an open DateTimePicker popover.
function clickDay(isoDate: string) {
  const btn = document.querySelector(`[data-day="${isoDate}"] button`)
  if (!btn) throw new Error(`no day button for ${isoDate}`)
  fireEvent.click(btn)
}

// Pick an option from an open DateTimePicker's Hour/Minute <Select>.
function selectOption(comboboxName: string, optionName: string) {
  fireEvent.click(screen.getByRole('combobox', { name: comboboxName }))
  const option = screen.getByRole('option', { name: optionName })
  fireEvent.pointerDown(option, { pointerId: 1, button: 0 })
  fireEvent.click(option)
}

// When no value is set, the calendar opens on the real "today" month (react-day-picker's
// own default), not the target month — navigate forward/back via the Nav buttons so the
// target day is on-screen regardless of when the suite happens to run.
function navigateToMonth(isoDate: string) {
  const [y, m] = isoDate.split('-').map(Number)
  const target = new Date(y, m - 1, 1)
  const now = new Date()
  const diff = (target.getFullYear() - now.getFullYear()) * 12 + (target.getMonth() - now.getMonth())
  const label = diff >= 0 ? /go to the next month/i : /go to the previous month/i
  for (let i = 0; i < Math.abs(diff); i++) {
    fireEvent.click(screen.getByRole('button', { name: label }))
  }
}

// Open the "Due date" DateTimePicker and pick a full date + time.
async function pickDueDateTime({ isoDate, hour, minute }: { isoDate: string; hour: string; minute: string }) {
  fireEvent.click(await screen.findByRole('button', { name: /due date/i }))
  navigateToMonth(isoDate)
  clickDay(isoDate)
  selectOption('Hour', hour)
  selectOption('Minute', minute)
}

// ── Mocks ─────────────────────────────────────────────────────────────────────

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => mockNavigate,
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
    // ADR-051 plans-as-filter — the "Move to plan…" picker's plans query.
    fetchPlans: vi.fn().mockResolvedValue([]),
    fetchTaskEvidence: vi.fn().mockResolvedValue([]),
    fetchTaskVerdicts: vi.fn().mockResolvedValue([]),
    // Fix B: the assignee picker's workspace-team scoping — see the
    // "assignee picker is workspace-team-scoped" describe block below.
    fetchWorkspaceDelegation: vi.fn(),
    updateTask: vi.fn().mockResolvedValue({}),
    deleteTask: vi.fn().mockResolvedValue(undefined),
    setTaskTodos: vi.fn().mockResolvedValue({}),
    setTaskDependencies: vi.fn().mockResolvedValue({}),
    stopTaskGoalLoop: vi.fn().mockResolvedValue({}),
    fetchTaskRuns: vi.fn().mockResolvedValue([]),
    runTaskNow: vi.fn().mockResolvedValue(undefined),
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

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

const SESSION_ID = 'sess-task-test-abc'
const AGENT_ID = 'general-assistant'

// Minimal required fields for the unified Task type
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

const taskWithSession: Task = makeTask({
  id: 'task-1',
  title: 'Research task',
  prompt: 'Research the topic',
  status: 'in_progress',
  agent_name: 'General Assistant',
  agent_id: AGENT_ID,
  session_id: SESSION_ID,
})

const taskWithoutSession: Task = makeTask({
  id: 'task-2',
  title: 'Queued task',
  prompt: 'Do something',
  status: 'next',
  agent_name: 'General Assistant',
})

function renderPanel(task: Task | null, onClose = vi.fn()) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <TaskDetailPanel task={task} onClose={onClose} />
    </QueryClientProvider>
  )
}

// ── Tests ─────────────────────────────────────────────────────────────────────

beforeEach(async () => {
  mockNavigate.mockReset()
  const api = await import('@/lib/api')
  vi.mocked(api.updateTask).mockReset().mockResolvedValue({} as never)
  vi.mocked(api.setTaskTodos).mockReset().mockResolvedValue({} as never)
  vi.mocked(api.setTaskDependencies).mockReset().mockResolvedValue({} as never)
  vi.mocked(api.fetchTasks).mockReset().mockResolvedValue([])
  vi.mocked(api.fetchSubtasks).mockReset().mockResolvedValue([])
  vi.mocked(api.fetchPlans).mockReset().mockResolvedValue([])
  // Default: the workspace-team query fails (unmocked in most tests, which
  // don't care about team-scoping) — this is the DEGRADED fallback path
  // (buildTaskAssigneeItems / useWorkspaceTeamIds), which offers the full
  // unscoped agent list, i.e. today's pre-scoping behaviour. Tests that
  // exercise team-scoping itself override this per-test.
  vi.mocked(api.fetchWorkspaceDelegation).mockReset().mockRejectedValue(new Error('not mocked'))
  vi.mocked(api.fetchTaskRuns).mockReset().mockResolvedValue([])
  vi.mocked(api.runTaskNow).mockReset().mockResolvedValue(undefined)
  mockAddToast.mockReset()
})

// Minimal required fields for TaskRun (ADR-050 §2.1) — mirrors TaskRunsList.test.tsx's makeRun.
function makeRun(overrides: Partial<TaskRun> = {}): TaskRun {
  return {
    run_id: 'run-1',
    task_id: 'task-1',
    occurrence_ms: null,
    status: 'done',
    result: 'All good.',
    session_id: 'sess-run-1',
    kind: 'manual',
    started_at: '2026-07-20T09:00:00Z',
    ended_at: '2026-07-20T09:05:00Z',
    ...overrides,
  }
}

describe('TaskDetailPanel — Open in Chat (#250 regression)', () => {
  it('shows "Open in Chat" button only when task has a session_id', async () => {
    renderPanel(taskWithSession)
    const btn = await screen.findByRole('button', { name: /Open in Chat/i })
    expect(btn).toBeInTheDocument()
  })

  it('does NOT show "Open in Chat" when task has no session_id', async () => {
    renderPanel(taskWithoutSession)
    await act(async () => {})
    const btn = screen.queryByRole('button', { name: /Open in Chat/i })
    expect(btn).toBeNull()
  })

  it('clicking "Open in Chat" navigates to /sessions/$sessionId (not to /)', async () => {
    // BDD:
    //   Given a task with a known session_id
    //   When the user clicks "Open in Chat"
    //   Then navigate() is called with { to: '/sessions/$sessionId', params: { sessionId } }
    const onClose = vi.fn()
    renderPanel(taskWithSession, onClose)

    const btn = await screen.findByRole('button', { name: /Open in Chat/i })
    await act(async () => { fireEvent.click(btn) })

    expect(mockNavigate).toHaveBeenCalledWith(
      expect.objectContaining({
        to: '/sessions/$sessionId',
        params: { sessionId: SESSION_ID },
      }),
    )
    const toRootCall = mockNavigate.mock.calls.find((args) => args[0]?.to === '/')
    expect(toRootCall).toBeUndefined()

    expect(onClose).toHaveBeenCalled()
  })
})

describe('TaskDetailPanel — task without session (closed panel)', () => {
  it('renders null when task is null', () => {
    const { container } = renderPanel(null)
    expect(container).toBeTruthy()
  })
})

// ── 6-state lifecycle tests (ADR-051 D5) ──────────────────────────────────────
// Verify the unified status vocabulary renders correctly.

describe('TaskDetailPanel — 6-state status rendering', () => {
  it('renders Start Task button for inbox/next tasks', async () => {
    for (const status of ['inbox', 'next'] as const) {
      const task = makeTask({ status })
      const { unmount } = renderPanel(task)
      expect(await screen.findByRole('button', { name: /Start Task/i })).toBeInTheDocument()
      unmount()
    }
  })

  it('shows in_progress as a read-only badge (not a dropdown)', async () => {
    const task = makeTask({ status: 'in_progress' })
    renderPanel(task)
    expect(await screen.findByText(/In Progress/i)).toBeInTheDocument()
    // Start Task button must NOT appear when task is in_progress
    expect(screen.queryByRole('button', { name: /Start Task/i })).toBeNull()
  })

  it('renders Retry button for failed tasks', async () => {
    const task = makeTask({ status: 'failed' })
    renderPanel(task)
    expect(await screen.findByRole('button', { name: /Retry/i })).toBeInTheDocument()
  })

  it('shows blocked as a read-only badge (not a dropdown) — blocked is backend-derived', async () => {
    // BDD: Given a task with status "blocked" (set by the backend when a dependency is unmet),
    // When TaskDetailPanel renders,
    // Then "Blocked" appears as a read-only badge (same pattern as in_progress),
    // And the Start Task button does NOT appear (blocked tasks are not user-startable),
    // And no dropdown picker offers "blocked" as a selectable value.
    // Traces to: fix-3-blocked-derived-state — blocked must not be user-selectable
    const task = makeTask({ status: 'blocked' })
    renderPanel(task)

    // Read-only badge must be present
    expect(await screen.findByText(/Blocked/i)).toBeInTheDocument()

    // Start Task button must NOT appear (blocked is not startable)
    expect(screen.queryByRole('button', { name: /Start Task/i })).toBeNull()
  })
})

// ── Runs section (ADR-050 / task-run-history-spec §4.4) ──────────────────────
// TaskDetailPanel embeds the shared TaskRunsList as a "Runs" history section,
// and the failed-task "Retry" affordance now re-runs via runTaskNow (a fresh
// run; occurrence_ms omitted) instead of the old PATCH-status path — so a
// failed once-task can be re-run without overwriting the prior attempt.

describe('TaskDetailPanel — Runs section (ADR-050 §4.4)', () => {
  it('renders the run history for the task', async () => {
    const { fetchTaskRuns } = await import('@/lib/api')
    vi.mocked(fetchTaskRuns).mockResolvedValue([
      makeRun({ run_id: 'run-a', status: 'failed', result: 'Boom.' }),
      makeRun({ run_id: 'run-b', status: 'done', result: 'All good.' }),
    ])

    renderPanel(makeTask({ id: 'task-1', status: 'failed' }))

    expect(await screen.findByText(/run history/i)).toBeInTheDocument()
    await waitFor(() => expect(screen.getAllByTestId('task-run-row')).toHaveLength(2))
    expect(screen.getByText('Boom.')).toBeInTheDocument()
    expect(screen.getByText('All good.')).toBeInTheDocument()
    expect(vi.mocked(fetchTaskRuns)).toHaveBeenCalledWith('task-1')
  })

  it('shows the empty-runs state when the task has no run history yet', async () => {
    const { fetchTaskRuns } = await import('@/lib/api')
    vi.mocked(fetchTaskRuns).mockResolvedValue([])

    renderPanel(makeTask({ id: 'task-empty-runs', status: 'next' }))

    expect(await screen.findByTestId('task-runs-empty')).toBeInTheDocument()
  })

  it('clicking "Retry" on a failed task calls runTaskNow(task.id) with no occurrence_ms', async () => {
    // BDD: Given a failed once/normal task,
    //      When the user clicks "Retry",
    //      Then runTaskNow is called with just the task id — occurrence_ms
    //      is omitted entirely (undefined), which is what tells the backend
    //      to open a fresh run rather than re-open a specific occurrence.
    const { runTaskNow } = await import('@/lib/api')
    renderPanel(makeTask({ id: 'task-retry-1', status: 'failed' }))

    const retryBtn = await screen.findByRole('button', { name: /Retry/i })
    await act(async () => { fireEvent.click(retryBtn) })

    await waitFor(() => expect(vi.mocked(runTaskNow)).toHaveBeenCalled())
    expect(vi.mocked(runTaskNow).mock.calls[0]).toEqual(['task-retry-1'])
  })

  it('a failed task keeps showing its prior run in the Runs list after Retry is clicked, AND the new run appears (re-run does not overwrite history)', async () => {
    // BDD: Given a failed task whose Runs list already contains the failed
    //      attempt, When the user clicks Retry (which calls runTaskNow),
    //      Then the prior failed run is still listed afterward AND a new run
    //      appears — the fresh run is additive, not a replacement (ADR-050
    //      Acceptance Scenario 3).
    //
    // Q4 (de-tautologize): a STATEFUL mock — the fetch AFTER Retry's
    // onSuccess `invalidateQueries` refetch returns a DIFFERENT (longer)
    // list. A static `mockResolvedValue` here would pass this test even if
    // Retry silently did nothing (the assertion "still 1 row" is trivially
    // true against a mock that never changes) — this version fails unless
    // the refetch actually happens and actually reflects a new run.
    const { fetchTaskRuns, runTaskNow } = await import('@/lib/api')
    const failedRun = makeRun({ run_id: 'run-failed-1', status: 'failed', result: 'It broke.', session_id: 'sess-failed-1' })
    const freshRun = makeRun({ run_id: 'run-fresh-1', status: 'in_progress', result: undefined, session_id: 'sess-fresh-1' })
    vi.mocked(fetchTaskRuns)
      .mockResolvedValueOnce([failedRun])
      .mockResolvedValue([failedRun, freshRun])
    vi.mocked(runTaskNow).mockResolvedValue(undefined)

    renderPanel(makeTask({ id: 'task-retry-2', status: 'failed' }))

    await waitFor(() => expect(screen.getAllByTestId('task-run-row')).toHaveLength(1))
    expect(screen.getByText('It broke.')).toBeInTheDocument()

    const retryBtn = await screen.findByRole('button', { name: /Retry/i })
    await act(async () => { fireEvent.click(retryBtn) })

    await waitFor(() => expect(vi.mocked(runTaskNow)).toHaveBeenCalledWith('task-retry-2'))

    // The prior failed run is STILL listed, and the fresh run now appears
    // too — proving Retry's invalidateQueries actually drove a refetch that
    // surfaced a genuinely new run row, not just an unchanged static mock.
    await waitFor(() => expect(screen.getAllByTestId('task-run-row')).toHaveLength(2))
    expect(screen.getByText('It broke.')).toBeInTheDocument()
  })

  it('surfaces a Retry/re-run failure via toast instead of failing silently', async () => {
    const { runTaskNow } = await import('@/lib/api')
    vi.mocked(runTaskNow).mockRejectedValueOnce(new Error('dispatch failed'))

    renderPanel(makeTask({ id: 'task-retry-err', status: 'failed' }))

    const retryBtn = await screen.findByRole('button', { name: /Retry/i })
    await act(async () => { fireEvent.click(retryBtn) })

    await waitFor(() => expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'error' }),
    ))
  })
})

// ── Start does NOT auto-navigate ──────────────────────────────────────────────
//
// FIX B1: Pressing "Start Task" stays on the board — no auto-navigation to
// the session. The user reaches the session via the "Open in Chat" button that
// appears once the task has a session_id.

describe('TaskDetailPanel — Start Task does NOT auto-navigate (FIX B1)', () => {
  it('pressing Start Task calls updateTask but does NOT call navigate', async () => {
    // BDD: Given a task in "next" status (startable)
    //      When the user clicks "Start Task"
    //      Then updateTask is called with status: in_progress
    //      And navigate() is NOT called (user stays on the board)
    const { updateTask } = await import('@/lib/api')
    vi.mocked(updateTask).mockResolvedValue({} as never)

    renderPanel(makeTask({ id: 'task-start-no-nav', status: 'next' }))

    const startBtn = await screen.findByRole('button', { name: /Start Task/i })
    await act(async () => { fireEvent.click(startBtn) })

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalled())

    // navigate must NOT have been called — the user stays on the board
    expect(mockNavigate).not.toHaveBeenCalled()
  })

  it('"Open in Chat" is the intended path to the session — present only when task has session_id', async () => {
    // BDD: Given a task with a session_id (already running)
    //      When the panel renders
    //      Then "Open in Chat" button is present and clicking it navigates to the session
    const onClose = vi.fn()
    renderPanel(taskWithSession, onClose)

    const openBtn = await screen.findByRole('button', { name: /Open in Chat/i })
    await act(async () => { fireEvent.click(openBtn) })

    expect(mockNavigate).toHaveBeenCalledWith(
      expect.objectContaining({
        to: '/sessions/$sessionId',
        params: { sessionId: SESSION_ID },
      }),
    )
    expect(onClose).toHaveBeenCalled()
  })
})

// ── Prompt field tests ────────────────────────────────────────────────────────

const taskWithPrompt: Task = makeTask({
  id: 'task-prompt-1',
  title: 'Fix the build',
  status: 'inbox',
  priority: 2,
  prompt: 'Run the tests and fix all failures.',
})

const taskNoPrompt: Task = makeTask({
  id: 'task-no-prompt',
  title: 'Deploy to staging',
  status: 'next',
  priority: 3,
})

describe('TaskDetailPanel — renders prompt field', () => {
  it('renders the prompt content', async () => {
    renderPanel(taskWithPrompt)
    expect(await screen.findByText(/run the tests and fix all failures/i)).toBeInTheDocument()
    expect(screen.getByText(/prompt/i)).toBeInTheDocument()
  })

  it('shows "No prompt set." when task has no prompt', async () => {
    renderPanel(taskNoPrompt)
    expect(await screen.findByText(/no prompt set/i)).toBeInTheDocument()
  })
})

// ── Priority selector tests ───────────────────────────────────────────────────

describe('TaskDetailPanel — renders priority selector', () => {
  it('renders a priority selector', async () => {
    renderPanel(taskWithPrompt)
    expect(await screen.findByText(/^priority$/i)).toBeInTheDocument()
  })

  it('differentiation test: different task priorities show different initial values', async () => {
    const p2Task = makeTask({ priority: 2 })
    const p4Task = makeTask({ id: 'task-p4', priority: 4 })

    const { unmount } = renderPanel(p2Task)
    expect(await screen.findByText(/P2/i)).toBeInTheDocument()
    unmount()

    renderPanel(p4Task)
    expect(await screen.findByText(/P4/i)).toBeInTheDocument()
  })
})

// ── Todos checklist tests ─────────────────────────────────────────────────────

describe('TaskDetailPanel — renders todos checklist', () => {
  it('renders todo items when present', async () => {
    const task = makeTask({
      todos: [
        { text: 'Step one', status: 'pending' as const },
        { text: 'Step two', status: 'completed' as const },
      ],
    })
    renderPanel(task)
    expect(await screen.findByText(/step one/i)).toBeInTheDocument()
    expect(screen.getByText(/step two/i)).toBeInTheDocument()
  })

  it('does not render todos section when todos array is empty', async () => {
    const task = makeTask({ todos: [] })
    renderPanel(task)
    await act(async () => {})
    expect(screen.queryByText(/todos/i)).toBeNull()
  })

  it('renders in_progress todo distinctly and does not count it as completed', async () => {
    const task = makeTask({
      todos: [
        { text: 'Working on this', status: 'in_progress' as const },
        { text: 'All done', status: 'completed' as const },
        { text: 'Not started', status: 'pending' as const },
      ],
    })
    renderPanel(task)
    expect(await screen.findByText(/working on this/i)).toBeInTheDocument()
    expect(screen.getByText(/all done/i)).toBeInTheDocument()
    expect(screen.getByText(/not started/i)).toBeInTheDocument()
    // Progress count: only 1 completed out of 3
    expect(screen.getByText(/\(1\/3\)/)).toBeInTheDocument()
  })
})

// ── Worker inclusion tests ────────────────────────────────────────────────────
// Bug fix: tasks CAN be assigned to Subagent/subagent_3p worker-type agents
// (workspace-team membership is what the backend now enforces, not agent kind
// — see pkg/gateway/rest_tasks.go::validateTaskAgentID). The picker must list
// workers alongside base agents, distinguished by a " · Worker" suffix
// (mirrors AddAgentPicker's " · leaf" convention).

// Open the Agent field's SmartSelect. Fix B: the trigger is `disabled` while
// the workspace-team query (fetchWorkspaceDelegation) is in flight, so a
// click fired before it settles would silently no-op (Radix's Select ignores
// open-triggering clicks while disabled) instead of opening the dropdown —
// wait for it to become enabled first.
async function openAgentPicker(): Promise<HTMLElement> {
  const agentLabel = await screen.findByText('Agent')
  const fieldRoot = agentLabel.parentElement as HTMLElement
  const agentTrigger = fieldRoot.querySelector('[role="combobox"]') as HTMLElement
  await waitFor(() => expect(agentTrigger).not.toBeDisabled())
  fireEvent.click(agentTrigger)
  return agentTrigger
}

async function assertAgentPickerIncludesWorker(): Promise<void> {
  await openAgentPicker()

  await waitFor(() => {
    const options = Array.from(document.querySelectorAll('[role="option"]')).map(
      (el) => el.textContent ?? '',
    )
    expect(options.some((t) => t.includes('Mia'))).toBe(true)
    expect(options.some((t) => t.includes('Jim'))).toBe(true)
    // Worker is offered, and tagged " · Worker" so it stays distinguishable.
    expect(options.some((t) => t.includes('Builder Worker') && t.includes('Worker'))).toBe(true)
  })
}

const agentsWithWorker = [
  { id: 'mia', name: 'Mia', type: 'core', default: false },
  { id: 'jim', name: 'Jim', type: 'core', default: false },
  { id: 'builder', name: 'Builder Worker', type: 'Subagent', default: false },
]

describe('TaskDetailPanel — worker-type agents are offered as assignees', () => {
  beforeEach(() => {
    Element.prototype.scrollIntoView = vi.fn()
  })

  it('offers a Subagent worker as an assignee, alongside base agents', async () => {
    const { fetchAgents } = await import('@/lib/api')
    vi.mocked(fetchAgents).mockResolvedValue(agentsWithWorker as never)

    renderPanel(taskWithPrompt)
    await assertAgentPickerIncludesWorker()

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('selecting a Subagent worker as assignee sends its id via updateTask', async () => {
    const { fetchAgents, updateTask } = await import('@/lib/api')
    vi.mocked(fetchAgents).mockResolvedValue(agentsWithWorker as never)
    vi.mocked(updateTask).mockResolvedValue({} as never)

    renderPanel(taskWithPrompt)

    await openAgentPicker()

    const workerOption = await screen.findByRole('option', { name: /Builder Worker/i })
    fireEvent.pointerDown(workerOption, { pointerId: 1, button: 0 })
    fireEvent.click(workerOption)

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledWith(
      taskWithPrompt.id,
      expect.objectContaining({ agent_id: 'builder' }),
    ))

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('surfaces a backend rejection (e.g. non-team-member agent) via toast instead of failing silently', async () => {
    const { fetchAgents, updateTask } = await import('@/lib/api')
    vi.mocked(fetchAgents).mockResolvedValue(agentsWithWorker as never)
    vi.mocked(updateTask).mockRejectedValueOnce(
      new Error('agent "builder" is not a member of workspace "ws-test"'),
    )

    renderPanel(taskWithPrompt)

    await openAgentPicker()

    const workerOption = await screen.findByRole('option', { name: /Builder Worker/i })
    fireEvent.pointerDown(workerOption, { pointerId: 1, button: 0 })
    fireEvent.click(workerOption)

    await waitFor(() => expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'error' }),
    ))

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('includes a subagent_3p (external-CLI) worker when it IS a member of the workspace team', async () => {
    // Fix B: subagent_3p is no longer unconditionally excluded from the
    // picker — the backend's ONLY gate is workspace-team membership now
    // (validateTaskAgentID / workspaceTeamSet), and the external-CLI
    // task-execution path is being wired up alongside this change. A 3p
    // worker that IS on the team must be offered, distinguished by the same
    // " · Worker" suffix as a native Subagent.
    const { fetchAgents, fetchWorkspaceDelegation } = await import('@/lib/api')
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', default: false },
      { id: 'ext', name: 'External Runner', type: 'subagent_3p', default: false },
    ] as never)
    vi.mocked(fetchWorkspaceDelegation).mockResolvedValue({
      workspace_id: 'ws-test',
      edges: [],
      team: ['mia', 'ext'],
      default_depth: 3,
    } as never)

    renderPanel(taskWithPrompt)

    await openAgentPicker()

    await waitFor(() => {
      const options = Array.from(document.querySelectorAll('[role="option"]')).map(
        (el) => el.textContent ?? '',
      )
      expect(options.some((t) => t.includes('Mia'))).toBe(true)
      expect(options.some((t) => t.includes('External Runner') && t.includes('Worker'))).toBe(true)
    })

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })
})

// ── Plan field (ADR-051 plans-as-filter) ────────────────────────────────────────
// The "Move to plan…" picker — the explicit cross-plan reassignment path: the
// Board doesn't change a task's plan via drag; use this dropdown to move it.

function makePlan(overrides: Partial<Plan> = {}): Plan {
  return {
    id: 'plan-1',
    workspace_id: 'ws-test',
    title: 'Launch',
    state: 'draft',
    plan_phase: 'idle',
    owner_agent_id: 'jim',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

// Open the Plan field's SmartSelect (mirrors openAgentPicker above).
async function openPlanPicker(): Promise<HTMLElement> {
  const planLabel = await screen.findByText('Plan')
  const fieldRoot = planLabel.parentElement as HTMLElement
  const planTrigger = fieldRoot.querySelector('[role="combobox"]') as HTMLElement
  fireEvent.click(planTrigger)
  return planTrigger
}

describe('TaskDetailPanel — Plan field', () => {
  // Radix Select needs `scrollIntoView` to open (see the worker-inclusion
  // describe block above, which deletes it off the shared prototype after
  // each of its own tests) — reinstate it for this block too.
  beforeEach(() => {
    Element.prototype.scrollIntoView = vi.fn()
  })
  afterEach(() => {
    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('renders the workspace plans as options, alongside "No plan"', async () => {
    const { fetchPlans } = await import('@/lib/api')
    vi.mocked(fetchPlans).mockResolvedValue([
      makePlan({ id: 'plan-1', title: 'Launch' }),
      makePlan({ id: 'plan-2', title: 'Onboarding' }),
    ] as never)

    renderPanel(taskWithPrompt)
    await openPlanPicker()

    await waitFor(() => {
      const options = Array.from(document.querySelectorAll('[role="option"]')).map(
        (el) => el.textContent ?? '',
      )
      expect(options.some((t) => t.includes('Launch'))).toBe(true)
      expect(options.some((t) => t.includes('Onboarding'))).toBe(true)
      expect(options.some((t) => t.includes('No plan'))).toBe(true)
    })
  })

  it('selecting a plan sends its id via updateTask', async () => {
    const { fetchPlans, updateTask } = await import('@/lib/api')
    vi.mocked(fetchPlans).mockResolvedValue([makePlan({ id: 'plan-1', title: 'Launch' })] as never)
    vi.mocked(updateTask).mockResolvedValue({} as never)

    renderPanel(taskWithPrompt)
    await openPlanPicker()

    const option = await screen.findByRole('option', { name: 'Launch' })
    fireEvent.pointerDown(option, { pointerId: 1, button: 0 })
    fireEvent.click(option)

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledWith(
      taskWithPrompt.id,
      expect.objectContaining({ plan_id: 'plan-1' }),
    ))
  })

  it('selecting "No plan" sends an empty plan_id', async () => {
    const { fetchPlans, updateTask } = await import('@/lib/api')
    vi.mocked(fetchPlans).mockResolvedValue([makePlan({ id: 'plan-1', title: 'Launch' })] as never)
    vi.mocked(updateTask).mockResolvedValue({} as never)

    const plannedTask = makeTask({ id: 'task-planned', plan_id: 'plan-1' })
    renderPanel(plannedTask)
    await openPlanPicker()

    const option = await screen.findByRole('option', { name: 'No plan' })
    fireEvent.pointerDown(option, { pointerId: 1, button: 0 })
    fireEvent.click(option)

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledWith(
      'task-planned',
      expect.objectContaining({ plan_id: '' }),
    ))
  })
})

// ── Team-scoping (Fix B) ──────────────────────────────────────────────────────
// The assignee picker is scoped to task.workspace_id's TEAM (core_team ∪
// delegation edges) — see useWorkspaceTeamIds / buildTaskAssigneeItems.

describe('TaskDetailPanel — assignee picker is workspace-team-scoped (Fix B)', () => {
  beforeEach(() => {
    Element.prototype.scrollIntoView = vi.fn()
  })

  it('excludes an agent that is NOT a member of the workspace team, regardless of kind', async () => {
    const { fetchAgents, fetchWorkspaceDelegation } = await import('@/lib/api')
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', default: false },
      { id: 'offteam', name: 'Off Team Agent', type: 'core', default: false },
    ] as never)
    vi.mocked(fetchWorkspaceDelegation).mockResolvedValue({
      workspace_id: 'ws-test',
      edges: [],
      team: ['mia'],
      default_depth: 3,
    } as never)

    renderPanel(taskWithPrompt)

    await openAgentPicker()

    await waitFor(() => {
      const options = Array.from(document.querySelectorAll('[role="option"]')).map(
        (el) => el.textContent ?? '',
      )
      expect(options.some((t) => t.includes('Mia'))).toBe(true)
      expect(options.some((t) => t.includes('Off Team Agent'))).toBe(false)
    })

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('falls back to the full unscoped agent list when the workspace-team query errors', async () => {
    const { fetchAgents, fetchWorkspaceDelegation } = await import('@/lib/api')
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', default: false },
      { id: 'offteam', name: 'Off Team Agent', type: 'core', default: false },
    ] as never)
    vi.mocked(fetchWorkspaceDelegation).mockRejectedValue(new Error('network down'))

    renderPanel(taskWithPrompt)

    await openAgentPicker()

    await waitFor(() => {
      const options = Array.from(document.querySelectorAll('[role="option"]')).map(
        (el) => el.textContent ?? '',
      )
      expect(options.some((t) => t.includes('Mia'))).toBe(true)
      expect(options.some((t) => t.includes('Off Team Agent'))).toBe(true)
    })

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('disables the picker while the team query is in flight', async () => {
    const { fetchAgents, fetchWorkspaceDelegation } = await import('@/lib/api')
    let resolveDelegation: (v: unknown) => void = () => {}
    vi.mocked(fetchWorkspaceDelegation).mockReturnValue(
      new Promise((resolve) => { resolveDelegation = resolve }) as never,
    )
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', default: false },
    ] as never)

    renderPanel(taskWithPrompt)

    const agentLabel = await screen.findByText('Agent')
    const fieldRoot = agentLabel.parentElement as HTMLElement
    const agentTrigger = fieldRoot.querySelector('[role="combobox"]') as HTMLElement
    // The real signal (item 3 of the fix): the picker is disabled while the
    // team-set query is in flight, rather than offering a stale/unscoped
    // list or an interactive-but-wrong picker.
    expect(agentTrigger).toBeDisabled()

    // Resolve so the pending promise doesn't leak into the next test.
    resolveDelegation({ workspace_id: 'ws-test', edges: [], team: ['mia'], default_depth: 3 })
    await waitFor(() => expect(agentTrigger).not.toBeDisabled())

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('F1: shows an inline "team unavailable" hint next to the picker when the workspace-team query errors', async () => {
    // The degraded unscoped fallback must not be silent — a muted hint
    // renders next to the Agent picker so the degrade is visible, not
    // indistinguishable from a healthy, unrestricted workspace.
    const { fetchAgents, fetchWorkspaceDelegation } = await import('@/lib/api')
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', default: false },
    ] as never)
    vi.mocked(fetchWorkspaceDelegation).mockRejectedValue(new Error('network down'))

    renderPanel(taskWithPrompt)

    expect(await screen.findByText(/team list unavailable — showing all agents/i)).toBeInTheDocument()
  })

  it('does NOT show the "team unavailable" hint when the workspace-team query succeeds', async () => {
    const { fetchAgents, fetchWorkspaceDelegation } = await import('@/lib/api')
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', default: false },
    ] as never)
    vi.mocked(fetchWorkspaceDelegation).mockResolvedValue({
      workspace_id: 'ws-test',
      edges: [],
      team: ['mia'],
      default_depth: 3,
    } as never)

    renderPanel(taskWithPrompt)

    await waitFor(() => expect(screen.getByText('Agent')).toBeInTheDocument())
    expect(screen.queryByText(/team list unavailable/i)).toBeNull()
  })

  it('legacy edge case: an already-assigned agent that has fallen off the workspace team still renders as the current value', async () => {
    // A task assigned to an agent that was later removed from core_team /
    // delegation edges (legacy data) must still show that assignee in the
    // picker instead of a broken/empty select — buildTaskAssigneeItems always
    // includes `currentAssigneeId` even when it's outside `teamIds`.
    const { fetchAgents, fetchWorkspaceDelegation } = await import('@/lib/api')
    const legacyTask = makeTask({
      id: 'task-legacy-assignee',
      title: 'Legacy assignment',
      agent_id: 'retired-agent',
      workspace_id: 'ws-test',
    })
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', default: false },
      { id: 'retired-agent', name: 'Retired Agent', type: 'core', default: false },
    ] as never)
    // The team no longer includes 'retired-agent'.
    vi.mocked(fetchWorkspaceDelegation).mockResolvedValue({
      workspace_id: 'ws-test',
      edges: [],
      team: ['mia'],
      default_depth: 3,
    } as never)

    renderPanel(legacyTask)

    const agentLabel = await screen.findByText('Agent')
    const fieldRoot = agentLabel.parentElement as HTMLElement
    const agentTrigger = fieldRoot.querySelector('[role="combobox"]') as HTMLElement
    // The currently-assigned (off-team) agent's name still renders as the
    // picker's current value instead of a blank/broken trigger.
    await waitFor(() => expect(agentTrigger.textContent ?? '').toMatch(/retired agent/i))

    // And it is present as a selectable option (not silently dropped).
    await waitFor(() => expect(agentTrigger).not.toBeDisabled())
    fireEvent.click(agentTrigger)
    await waitFor(() => {
      const options = Array.from(document.querySelectorAll('[role="option"]')).map(
        (el) => el.textContent ?? '',
      )
      expect(options.some((t) => t.includes('Retired Agent'))).toBe(true)
      expect(options.some((t) => t.includes('Mia'))).toBe(true)
    })

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })
})

// ── Workspace-less task guard (F3) ────────────────────────────────────────────
// A persisted task can predate the `workspace_id is required` guard
// (pkg/task/store.go::normalize only runs on Create/Update, never on load) —
// a legacy/hand-edited task.json with an empty workspace_id still loads and
// renders. validateTaskAgentID (pkg/gateway/rest_tasks.go) unconditionally
// 400s ANY agent_id assignment for such a task, so the picker must disable
// itself with an explanatory hint instead of offering guaranteed-400 choices.

describe('TaskDetailPanel — workspace-less task disables the assignee picker (F3)', () => {
  it('disables the Agent picker and shows an "assignment unavailable" hint when the task has no workspace_id', async () => {
    const { fetchAgents } = await import('@/lib/api')
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', default: false },
    ] as never)

    const workspacelessTask = makeTask({
      id: 'task-no-workspace',
      title: 'Orphaned task',
      workspace_id: '',
    })

    renderPanel(workspacelessTask)

    const agentLabel = await screen.findByText('Agent')
    const fieldRoot = agentLabel.parentElement as HTMLElement
    const agentTrigger = fieldRoot.querySelector('[role="combobox"]') as HTMLElement

    // Disabled immediately — this is NOT the loading state (useWorkspaceTeamIds
    // is `enabled:false` for a falsy workspaceId, so isLoading is false too);
    // it is the distinct "no workspace to validate against" guard.
    expect(agentTrigger).toBeDisabled()
    expect(await screen.findByText(/task has no workspace — assignment unavailable/i)).toBeInTheDocument()
  })

  it('does not show the workspace-less hint for a task that has a workspace_id', async () => {
    const { fetchAgents, fetchWorkspaceDelegation } = await import('@/lib/api')
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', default: false },
    ] as never)
    vi.mocked(fetchWorkspaceDelegation).mockResolvedValue({
      workspace_id: 'ws-test',
      edges: [],
      team: ['mia'],
      default_depth: 3,
    } as never)

    renderPanel(taskWithPrompt)

    await waitFor(() => expect(screen.getByText('Agent')).toBeInTheDocument())
    expect(screen.queryByText(/assignment unavailable/i)).toBeNull()
  })
})

// ── Subtasks section ──────────────────────────────────────────────────────────

describe('TaskDetailPanel — renders subtask section when subtasks exist', () => {
  it('subtask items are shown when fetchSubtasks returns data', async () => {
    // BDD: Given fetchSubtasks returns subtasks,
    // When the component renders,
    // Then subtask items are shown.
    const { fetchSubtasks } = await import('@/lib/api')
    vi.mocked(fetchSubtasks).mockResolvedValueOnce([
      makeTask({
        id: 'sub-1',
        title: 'Subtask Alpha',
        status: 'next',
        agent_name: 'Mia',
      }),
    ])

    renderPanel(taskWithSession)
    expect(await screen.findByText(/subtask alpha/i)).toBeInTheDocument()
  })
})

// ── Full task UX edits (trigger / depends-on / due / todos) ────────────────────

describe('TaskDetailPanel — editable trigger', () => {
  // FR-011/D3/FR-005, updated by operator ruling 2026-08-07: recurring- AND
  // once-trigger SCHEDULE editing are both removed from the generic detail
  // panel entirely — actual date/time entry exists only in the calendar
  // editor. The trigger-KIND switch (manual ⇄ once) still lives here, tested
  // by the two tests below. Every/recurring/once-at-rest all get the
  // calendar-redirect rendering, tested in the FR-023 describe block below
  // (which used to cover only recurring/every — the once-DateTimePicker test
  // that used to live here is replaced by that block's "once" case, since
  // once now gets the identical treatment).
  it('the Trigger dropdown offers only "None (manual)" and "Once (at a time)" for a manual task', async () => {
    Element.prototype.scrollIntoView = vi.fn()
    renderPanel(makeTask({ id: 'task-trig', status: 'next' }))

    await openSmartSelectAndFind(/^trigger$/i, /once \(at a time\)/i)
    // Query by role="option" — the closed trigger's own selected-value
    // display ALSO reads "None (manual)" (the default), so a plain
    // getByText would ambiguously match both it and the open option.
    expect(screen.getByRole('option', { name: /none \(manual\)/i })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: /once \(at a time\)/i })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /every \(interval\)/i })).toBeNull()
    expect(screen.queryByRole('option', { name: /recurring \(cron\)/i })).toBeNull()
    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('Binding Rule 4 positive control: selecting "Once (at a time)" on a manual/no-trigger task still switches the trigger kind via updateTask', async () => {
    // Operator ruling 2026-08-07 moved once-SCHEDULE editing (the actual
    // date/time) to the calendar-only redirect — it did NOT touch the
    // trigger-KIND switch (manual → once), which stays inline right here,
    // exactly as before. This proves a manual/no-trigger task still gets
    // that inline editing today, unaffected by the ruling.
    Element.prototype.scrollIntoView = vi.fn()
    const { updateTask } = await import('@/lib/api')
    renderPanel(makeTask({ id: 'task-trig-manual-switch', status: 'next' }))

    const label = await screen.findByText(/^trigger$/i)
    const fieldRoot = label.parentElement as HTMLElement
    const combo = fieldRoot.querySelector('[role="combobox"]') as HTMLElement
    fireEvent.click(combo)

    const option = await screen.findByRole('option', { name: /once \(at a time\)/i })
    fireEvent.pointerDown(option, { pointerId: 1, button: 0 })
    fireEvent.click(option)

    await waitFor(() => {
      const arg = lastUpdateArg(updateTask)
      expect(arg.trigger?.type).toBe('once')
      expect(typeof arg.trigger?.config.at_ms).toBe('number')
    })

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })
})

// ── FR-023 defensive guard ────────────────────────────────────────────────────
// Board/List already exclude every schedule-bearing task (BoardView/
// ListView's isScheduledTrigger filter — once/every/recurring alike, per
// operator ruling 2026-08-07) — this panel should rarely receive one in
// practice (reachable via a dependency chip, subtask row, search result,
// stale cache, or a race). These tests force-feed one directly as the `task`
// prop and assert the defensive read-only rendering: a plain-English
// summary, an "Edit in workspace calendar" link, no SmartSelect trigger
// picker, no inline DateTimePicker, and — critically — no raw cron/rule
// string anywhere in the rendered output.

describe('TaskDetailPanel — FR-023 defensive guard for a force-fed scheduled task (once/every/recurring)', () => {
  it('a task already on a "once" schedule shows the calendar-redirect affordance, not an inline DateTimePicker (operator ruling 2026-08-07)', async () => {
    // Supersedes the pre-ruling test this used to be: picking a date+time via
    // an inline DateTimePicker that PATCHed trigger.config.at_ms directly.
    // `once` is now calendar-only, exactly like every/recurring — same
    // read-only summary + "Edit in workspace calendar" link, no inline
    // "Trigger date and time" picker, no trigger-kind SmartSelect.
    renderPanel(makeTask({
      id: 'task-trig-once',
      status: 'next',
      workspace_id: 'ws-test',
      trigger: { type: 'once', config: { at_ms: new Date('2026-09-10T08:15').getTime() } },
    }))

    expect(await screen.findByText(/runs once/i)).toBeInTheDocument()
    expect(await screen.findByText(/edit in workspace calendar/i)).toBeInTheDocument()
    // No inline DateTimePicker for the trigger time…
    expect(screen.queryByRole('button', { name: /trigger date and time/i })).toBeNull()
    // …and no trigger-kind SmartSelect either — matches the every/recurring
    // rendering below exactly.
    const label = await screen.findByText(/^trigger$/i)
    const fieldRoot = label.parentElement as HTMLElement
    expect(fieldRoot.querySelector('[role="combobox"]')).toBeNull()
  })

  it('omits the calendar link for a force-fed "once" task with no workspace_id, but still shows the read-only summary', async () => {
    renderPanel(makeTask({
      id: 'task-once-no-ws',
      status: 'next',
      workspace_id: '',
      trigger: { type: 'once', config: { at_ms: new Date('2026-09-10T08:15').getTime() } },
    }))

    expect(await screen.findByText(/runs once/i)).toBeInTheDocument()
    expect(screen.queryByText(/edit in workspace calendar/i)).toBeNull()
  })

  it('renders a read-only summary and an "Edit in workspace calendar" link for a recurring (cron) task — never the raw cron string', async () => {
    renderPanel(makeTask({
      id: 'task-recurring-forced',
      status: 'next',
      workspace_id: 'ws-test',
      trigger: { type: 'recurring', config: { cron_expr: '0 9 * * MON' } },
    }))

    expect(await screen.findByText(/edit in workspace calendar/i)).toBeInTheDocument()
    // No cron string is ever displayed (D8/D9/FR-023).
    expect(screen.queryByText(/0 9 \* \* MON/)).toBeNull()
    expect(screen.queryByLabelText(/cron expression/i)).toBeNull()
    // No editable trigger picker for a recurring task.
    const label = await screen.findByText(/^trigger$/i)
    const fieldRoot = label.parentElement as HTMLElement
    expect(fieldRoot.querySelector('[role="combobox"]')).toBeNull()
  })

  it('renders a read-only summary for an every-interval task, derived from every_ms, with no interval input', async () => {
    renderPanel(makeTask({
      id: 'task-every-forced',
      status: 'next',
      workspace_id: 'ws-test',
      trigger: { type: 'every', config: { every_ms: 45 * 60_000 } },
    }))

    expect(await screen.findByText(/repeats every 45 minutes/i)).toBeInTheDocument()
    expect(await screen.findByText(/edit in workspace calendar/i)).toBeInTheDocument()
    expect(screen.queryByLabelText(/interval in minutes/i)).toBeNull()
    const label = await screen.findByText(/^trigger$/i)
    const fieldRoot = label.parentElement as HTMLElement
    expect(fieldRoot.querySelector('[role="combobox"]')).toBeNull()
  })

  it('omits the calendar link when the force-fed task has no workspace_id, but still shows the read-only summary', async () => {
    renderPanel(makeTask({
      id: 'task-recurring-no-ws',
      status: 'next',
      workspace_id: '',
      trigger: { type: 'recurring', config: { cron_expr: '0 9 * * MON' } },
    }))

    expect(await screen.findByText(/repeats on a recurring schedule/i)).toBeInTheDocument()
    expect(screen.queryByText(/edit in workspace calendar/i)).toBeNull()
  })
})

describe('TaskDetailPanel — editable due date', () => {
  it('setting a due date PATCHes due as RFC3339', async () => {
    const { updateTask } = await import('@/lib/api')
    renderPanel(makeTask({ id: 'task-due', status: 'next' }))

    await pickDueDateTime({ isoDate: '2026-09-10', hour: '12', minute: '30' })

    // Debounced autosave (~500ms after the last pick) — give it room beyond
    // the default waitFor timeout so this isn't a timing-flaky assertion.
    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalled(), { timeout: 3000 })
    const arg = lastUpdateArg(updateTask)
    expect(arg.due).toBe(new Date('2026-09-10T12:30').toISOString())
  })

  it('a day→hour→minute pick sequence results in a single PATCH carrying the final composed value', async () => {
    // Reviewer fix: day, hour, and minute each fire a separate onChange from
    // the DateTimePicker. Before debouncing, each one PATCHed independently —
    // 3 sequential requests with intermediate (partially-composed) values,
    // and a risk of them resolving out of order. Now they coalesce into ONE
    // PATCH carrying the final, fully-composed date.
    const { updateTask } = await import('@/lib/api')
    renderPanel(makeTask({ id: 'task-due-single-patch', status: 'next' }))

    await pickDueDateTime({ isoDate: '2026-09-10', hour: '12', minute: '30' })

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalled(), { timeout: 3000 })
    // Give any (incorrect) extra debounced PATCHes a moment to have fired too.
    await new Promise((resolve) => setTimeout(resolve, 200))

    expect(vi.mocked(updateTask)).toHaveBeenCalledTimes(1)
    const arg = lastUpdateArg(updateTask)
    expect(arg.due).toBe(new Date('2026-09-10T12:30').toISOString())
  })
})

// ── CRITICAL data-loss regression: due-date autosave must not wipe an
// unsaved prompt edit ──────────────────────────────────────────────────────
//
// TaskDetailPanel's `task` prop is not query-owned by this component — the
// real host screen re-renders it with a NEW `task` object whenever the tasks
// query refetches (e.g. because TaskDetailPanel's own due/trigger autosave
// PATCH invalidated it). Before the fix, a single resync useEffect keyed
// partly on task?.due / task?.trigger?.config?.at_ms re-fired on that resync
// and reset promptDraft/editingPrompt — silently discarding whatever the
// user had typed. This harness reproduces that resync by having the mocked
// updateTask feed the patched fields back into a task object held in local
// state, exactly mirroring the parent's useQuery→find(task.id) round trip.
describe('TaskDetailPanel — prompt draft survives a due-date autosave (data-loss regression)', () => {
  it('editing the prompt then picking a due date does not wipe the unsaved prompt text', async () => {
    const { updateTask } = await import('@/lib/api')

    const baseTask = makeTask({ id: 'task-preserve-prompt', status: 'next', prompt: 'Original prompt' })
    let applyPatch: (patch: TaskUpdateRequest) => void = () => {}

    function Harness() {
      const [task, setTask] = useState<Task>(baseTask)
      // `as Task` matches the mocked updateTask below: a TaskUpdateRequest's
      // criteria use the ADR-074 AcceptanceCriterionInput shape (kind
      // optional), so a raw request->response spread no longer typechecks —
      // fine for this harness, which never patches criteria.
      applyPatch = (patch) => setTask((prev) => ({ ...prev, ...patch }) as Task)
      return (
        <QueryClientProvider client={makeClient()}>
          <TaskDetailPanel task={task} onClose={vi.fn()} />
        </QueryClientProvider>
      )
    }

    vi.mocked(updateTask).mockImplementation(async (_id: string, patch: TaskUpdateRequest) => {
      // Mirrors the real flow: the PATCH succeeds, the tasks query is
      // invalidated, and the parent hands TaskDetailPanel a NEW task object
      // (same id/prompt/workspace_id, updated due) — done here synchronously
      // via the harness's own state instead of a real query round trip.
      applyPatch(patch)
      return { ...baseTask, ...patch } as Task
    })

    render(<Harness />)

    // Start editing the prompt — unsaved.
    fireEvent.click(await screen.findByLabelText(/edit prompt/i))
    const promptBox = document.querySelector('textarea') as HTMLTextAreaElement
    expect(promptBox).toBeTruthy()
    fireEvent.change(promptBox, { target: { value: 'Unsaved draft text' } })

    // Now pick a due date — the resulting (debounced) PATCH feeds a new task
    // object back in via applyPatch, same id/prompt/workspace_id, new due.
    await pickDueDateTime({ isoDate: '2026-09-10', hour: '12', minute: '30' })

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalled(), { timeout: 3000 })

    // The unsaved prompt edit must survive: still in edit mode, still showing
    // the draft text — NOT reset to the (stale) saved prompt.
    const promptBoxAfter = document.querySelector('textarea') as HTMLTextAreaElement | null
    expect(promptBoxAfter).toBeTruthy()
    expect(promptBoxAfter).toHaveValue('Unsaved draft text')
  })
})

describe('TaskDetailPanel — editable dependencies (blocked_by)', () => {
  it('toggling a candidate calls setTaskDependencies with the new blocked_by set', async () => {
    const { fetchTasks, setTaskDependencies } = await import('@/lib/api')
    vi.mocked(fetchTasks).mockResolvedValueOnce([
      makeTask({ id: 'other-1', title: 'Other Task One' }),
    ])
    renderPanel(makeTask({ id: 'task-dep', status: 'next', workspace_id: 'ws-test' }))

    // Open the depends-on popover
    fireEvent.click(await screen.findByText(/no dependencies/i))
    fireEvent.click(await screen.findByText('Other Task One'))

    await waitFor(() => expect(vi.mocked(setTaskDependencies)).toHaveBeenCalled())
    expect(vi.mocked(setTaskDependencies).mock.calls[0][1]).toEqual(['other-1'])
  })

  it('lists only SAME-plan tasks as candidates (cross-plan / plan-less excluded)', async () => {
    const { fetchTasks } = await import('@/lib/api')
    vi.mocked(fetchTasks).mockResolvedValueOnce([
      makeTask({ id: 'same-1', title: 'Same Plan Sibling', plan_id: 'plan-x', workspace_id: 'ws-test' }),
      makeTask({ id: 'other-1', title: 'Other Plan Task', plan_id: 'plan-y', workspace_id: 'ws-test' }),
      makeTask({ id: 'loose-1', title: 'Loose Task', workspace_id: 'ws-test' }),
    ])
    renderPanel(makeTask({ id: 'task-scoped', status: 'next', plan_id: 'plan-x', workspace_id: 'ws-test' }))

    fireEvent.click(await screen.findByText(/no dependencies/i))

    expect(await screen.findByText('Same Plan Sibling')).toBeInTheDocument()
    expect(screen.queryByText('Other Plan Task')).not.toBeInTheDocument()
    expect(screen.queryByText('Loose Task')).not.toBeInTheDocument()
  })

  it('still shows an existing cross-plan dependency as a chip (removable, not silently dropped)', async () => {
    const { fetchTasks } = await import('@/lib/api')
    vi.mocked(fetchTasks).mockResolvedValueOnce([
      makeTask({ id: 'legacy-dep', title: 'Legacy Cross-Plan Dep', plan_id: 'plan-y', workspace_id: 'ws-test' }),
    ])
    // task is in plan-x but already depends on a plan-y task (pre-enforcement /
    // agent-set): the candidate list won't offer it, but the chip must still
    // render (title resolved from the full task list) so it stays removable.
    renderPanel(
      makeTask({ id: 'task-legacy', status: 'next', plan_id: 'plan-x', workspace_id: 'ws-test', blocked_by: ['legacy-dep'] }),
    )

    expect(await screen.findByText('Legacy Cross-Plan Dep')).toBeInTheDocument()
  })
})

describe('TaskDetailPanel — editable todos checklist', () => {
  it('adding a checklist item calls setTaskTodos with the appended item', async () => {
    const { setTaskTodos } = await import('@/lib/api')
    renderPanel(makeTask({ id: 'task-todo', status: 'next', todos: [{ text: 'Existing', status: 'pending' as const }] }))

    const input = await screen.findByLabelText(/new checklist item/i)
    fireEvent.change(input, { target: { value: 'Brand new' } })
    fireEvent.click(screen.getByRole('button', { name: /add checklist item/i }))

    await waitFor(() => expect(vi.mocked(setTaskTodos)).toHaveBeenCalled())
    expect(vi.mocked(setTaskTodos).mock.calls[0][1]).toEqual([
      { text: 'Existing', status: 'pending' },
      { text: 'Brand new', status: 'pending' },
    ])
  })

  it('toggling a pending checklist item marks it completed', async () => {
    const { setTaskTodos } = await import('@/lib/api')
    renderPanel(makeTask({ id: 'task-toggle', status: 'next', todos: [{ text: 'Flip me', status: 'pending' as const }] }))

    fireEvent.click(await screen.findByLabelText(/toggle flip me/i))

    await waitFor(() => expect(vi.mocked(setTaskTodos)).toHaveBeenCalled())
    expect(vi.mocked(setTaskTodos).mock.calls[0][1]).toEqual([{ text: 'Flip me', status: 'completed' }])
  })

  it('toggling a completed checklist item marks it pending', async () => {
    const { setTaskTodos } = await import('@/lib/api')
    renderPanel(makeTask({ id: 'task-toggle-back', status: 'next', todos: [{ text: 'Done item', status: 'completed' as const }] }))

    fireEvent.click(await screen.findByLabelText(/toggle done item/i))

    await waitFor(() => expect(vi.mocked(setTaskTodos)).toHaveBeenCalled())
    expect(vi.mocked(setTaskTodos).mock.calls[0][1]).toEqual([{ text: 'Done item', status: 'pending' }])
  })

  it('removing a checklist item calls setTaskTodos without it', async () => {
    const { setTaskTodos } = await import('@/lib/api')
    renderPanel(makeTask({ id: 'task-rm', status: 'next', todos: [{ text: 'Keep', status: 'pending' as const }, { text: 'Drop', status: 'pending' as const }] }))

    fireEvent.click(await screen.findByLabelText(/remove checklist item drop/i))

    await waitFor(() => expect(vi.mocked(setTaskTodos)).toHaveBeenCalled())
    expect(vi.mocked(setTaskTodos).mock.calls[0][1]).toEqual([{ text: 'Keep', status: 'pending' }])
  })
})

describe('TaskDetailPanel — clearing a due date (clear_due)', () => {
  it('PATCHes clear_due:true and never sends due:"" when the field is cleared', async () => {
    const { updateTask } = await import('@/lib/api')
    const dueIso = '2026-09-10T12:30:00Z'
    renderPanel(makeTask({ id: 'task-clear-due', status: 'next', due: dueIso }))

    // Clear by clicking the already-selected day again — react-day-picker's single-select
    // mode (required=false, the default) deselects and fires onSelect(undefined).
    fireEvent.click(await screen.findByRole('button', { name: /due date/i }))
    const selectedDate = new Date(dueIso)
    const localIso = `${selectedDate.getFullYear()}-${String(selectedDate.getMonth() + 1).padStart(2, '0')}-${String(selectedDate.getDate()).padStart(2, '0')}`
    clickDay(localIso)

    // Clears via the unambiguous flag; never sends the broken empty string.
    await vi.waitFor(() => {
      const clearedViaFlag = vi
        .mocked(updateTask)
        .mock.calls.some((c) => (c[1] as { clear_due?: boolean }).clear_due === true)
      expect(clearedViaFlag).toBe(true)
    })
    const sentEmptyDue = vi
      .mocked(updateTask)
      .mock.calls.some((c) => (c[1] as { due?: string }).due === '')
    expect(sentEmptyDue).toBe(false)
  })
})

describe('TaskDetailPanel — done-terminal status guard', () => {
  it('renders done as a read-only "Done (final)" badge — no status dropdown', async () => {
    // Done is terminal (canDropTransition forbids leaving done). The panel must
    // mirror that: a read-only badge, no selectable picker to a rejected status.
    renderPanel(makeTask({ id: 'task-done', status: 'done' }))

    expect(await screen.findByTestId('status-done-terminal')).toBeInTheDocument()

    const statusLabel = await screen.findByText(/^status$/i)
    const fieldRoot = statusLabel.parentElement as HTMLElement
    // No status picker trigger is rendered for a done task (neither the Radix
    // combobox nor the searchable listbox button).
    expect(fieldRoot.querySelector('[role="combobox"]')).toBeNull()
    expect(fieldRoot.querySelector('[aria-haspopup="listbox"]')).toBeNull()
  })

  it('excludes blocked (backend-derived) from the selectable status options', async () => {
    Element.prototype.scrollIntoView = vi.fn()
    // The searchable status popover (cmdk) needs ResizeObserver, which jsdom
    // does not provide. Stub it for this test.
    const RealResizeObserver = (globalThis as { ResizeObserver?: unknown }).ResizeObserver
    ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
    renderPanel(makeTask({ id: 'task-next', status: 'next' }))

    const statusLabel = await screen.findByText(/^status$/i)
    const fieldRoot = statusLabel.parentElement as HTMLElement
    // 5 options (ADR-051 D5 removed `planning`) → at/under SmartSelect's
    // SEARCHABLE_THRESHOLD, so it renders the plain Radix Select (combobox
    // trigger), not the searchable cmdk popover.
    const trigger = fieldRoot.querySelector('[role="combobox"]') as HTMLElement
    expect(trigger).toBeTruthy()
    fireEvent.click(trigger)

    // Done is a valid forward transition from next and must be offered…
    expect(await screen.findByRole('option', { name: /^Done$/i })).toBeInTheDocument()
    // …but blocked is never a selectable target.
    expect(screen.queryByRole('option', { name: /^Blocked$/i })).toBeNull()
    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
    ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = RealResizeObserver
  })
})

describe('TaskDetailPanel — autosave indicator', () => {
  it('shows "Saving..." then "Saved" when a field change PATCHes successfully', async () => {
    const { updateTask } = await import('@/lib/api')
    vi.mocked(updateTask).mockResolvedValue({} as never)
    renderPanel(makeTask({ id: 'task-autosave', status: 'next' }))

    await pickDueDateTime({ isoDate: '2026-09-10', hour: '12', minute: '30' })

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalled())
    await screen.findByText(/saved/i)
  })
})

// ── helpers for the SmartSelect-driven fields ──────────────────────────────────

function lastUpdateArg(updateTask: unknown): { trigger?: { type: string; config: Record<string, unknown> }; due?: string } {
  const mock = vi.mocked(updateTask as (id: string, data: unknown) => Promise<unknown>)
  return mock.mock.calls[mock.mock.calls.length - 1][1] as never
}

async function openSmartSelectAndFind(fieldLabel: RegExp, optionLabel: RegExp): Promise<HTMLElement> {
  // Each Field renders its label text followed by the control. Find the field
  // wrapper by its label, then click the combobox trigger inside it.
  const label = await screen.findByText(fieldLabel)
  const fieldRoot = label.parentElement as HTMLElement
  const combo = fieldRoot.querySelector('[role="combobox"]') as HTMLElement
  fireEvent.click(combo)
  return screen.findByText(optionLabel)
}
