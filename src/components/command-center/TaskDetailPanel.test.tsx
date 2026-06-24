/**
 * TaskDetailPanel.test.tsx
 *
 * Renders the REAL TaskDetailPanel component and asserts that "Open in Chat"
 * triggers navigation to /sessions/$sessionId.
 *
 * Sprint 2 migration: TaskDetailPanel now uses the unified Task type — the
 * old two-mode (workflow/gtd) API is gone. BoardTask is gone. All tasks use
 * the 7-state lifecycle: inbox/next/planning/in_progress/blocked/done/failed.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskDetailPanel } from './TaskDetailPanel'
import type { Task } from '@/lib/api'

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
    fetchMilestones: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    fetchTasks: vi.fn().mockResolvedValue([]),
    updateTask: vi.fn().mockResolvedValue({}),
    deleteTask: vi.fn().mockResolvedValue(undefined),
    setTaskTodos: vi.fn().mockResolvedValue({}),
    setTaskDependencies: vi.fn().mockResolvedValue({}),
    isApiError: vi.fn().mockReturnValue(false),
    tasksQueryKeys: actual.tasksQueryKeys,
    milestonesQueryKeys: actual.milestonesQueryKeys,
    workspacesQueryKeys: actual.workspacesQueryKeys,
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
  mockAddToast.mockReset()
})

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

// ── 7-state lifecycle tests ───────────────────────────────────────────────────
// Verify the unified status vocabulary renders correctly.

describe('TaskDetailPanel — 7-state status rendering', () => {
  it('renders Start Task button for inbox/next/planning tasks', async () => {
    for (const status of ['inbox', 'next', 'planning'] as const) {
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

// ── Worker exclusion tests ────────────────────────────────────────────────────

async function assertAgentPickerExcludesWorker(): Promise<void> {
  const agentLabel = await screen.findByText('Agent')
  const fieldRoot = agentLabel.parentElement as HTMLElement
  const agentTrigger = fieldRoot.querySelector('[role="combobox"]') as HTMLElement
  expect(agentTrigger).toBeTruthy()
  fireEvent.click(agentTrigger)

  await waitFor(() => {
    const options = Array.from(document.querySelectorAll('[role="option"]')).map(
      (el) => el.textContent ?? '',
    )
    expect(options.some((t) => t.includes('Mia'))).toBe(true)
    expect(options.some((t) => t.includes('Jim'))).toBe(true)
    expect(options.some((t) => t.includes('Builder Worker'))).toBe(false)
  })
}

const agentsWithWorker = [
  { id: 'mia', name: 'Mia', type: 'core', default: false },
  { id: 'jim', name: 'Jim', type: 'core', default: false },
  { id: 'builder', name: 'Builder Worker', type: 'worker', default: false },
]

describe('TaskDetailPanel — workers excluded from the assignee picker', () => {
  beforeEach(() => {
    Element.prototype.scrollIntoView = vi.fn()
  })

  it('does not offer a worker as an assignee; lists base agents', async () => {
    const { fetchAgents } = await import('@/lib/api')
    vi.mocked(fetchAgents).mockResolvedValue(agentsWithWorker as never)

    renderPanel(taskWithPrompt)
    await assertAgentPickerExcludesWorker()

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
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
  it('selecting "Recurring" PATCHes a recurring trigger with a default cron', async () => {
    const { updateTask } = await import('@/lib/api')
    Element.prototype.scrollIntoView = vi.fn()
    renderPanel(makeTask({ id: 'task-trig', status: 'next' }))

    // The Trigger field SmartSelect — open it and choose Recurring
    const recurringOption = await openSmartSelectAndFind(/trigger/i, /recurring \(cron\)/i)
    fireEvent.click(recurringOption)

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalled())
    const arg = lastUpdateArg(updateTask)
    expect(arg.trigger?.type).toBe('recurring')
    expect(arg.trigger?.config.cron_expr).toBeTruthy()
    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('editing the cron expression PATCHes the new cron on blur', async () => {
    const { updateTask } = await import('@/lib/api')
    renderPanel(makeTask({ id: 'task-cron', status: 'next', trigger: { type: 'recurring', config: { cron_expr: '0 9 * * MON' } } }))

    const cron = await screen.findByLabelText(/cron expression/i)
    fireEvent.change(cron, { target: { value: '15 6 * * *' } })
    fireEvent.blur(cron)

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalled())
    const arg = lastUpdateArg(updateTask)
    expect(arg.trigger?.config.cron_expr).toBe('15 6 * * *')
  })
})

describe('TaskDetailPanel — editable due date', () => {
  it('setting a due date PATCHes due as RFC3339 on blur', async () => {
    const { updateTask } = await import('@/lib/api')
    renderPanel(makeTask({ id: 'task-due', status: 'next' }))

    const due = await screen.findByLabelText(/due date/i)
    fireEvent.change(due, { target: { value: '2026-09-10T12:30' } })
    fireEvent.blur(due)

    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalled())
    const arg = lastUpdateArg(updateTask)
    expect(arg.due).toBe(new Date('2026-09-10T12:30').toISOString())
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
    renderPanel(makeTask({ id: 'task-clear-due', status: 'next', due: '2026-09-10T12:30:00Z' }))

    const due = await screen.findByLabelText(/due date/i)
    fireEvent.change(due, { target: { value: '' } })
    fireEvent.blur(due)

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
    // 6 options → SmartSelect renders a SearchableSelect (button, listbox popup).
    const trigger = fieldRoot.querySelector('[aria-haspopup="listbox"]') as HTMLElement
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

    const due = await screen.findByLabelText(/due date/i)
    fireEvent.change(due, { target: { value: '2026-09-10T12:30' } })
    fireEvent.blur(due)

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
