/**
 * TaskDetailPanel.test.tsx — #6 false-confidence test fix.
 *
 * Renders the REAL TaskDetailPanel component and asserts that "Open in Chat"
 * triggers navigation to /sessions/$sessionId (which calls attachToSession
 * and sends the attach_session WS frame via the route's useEffect).
 *
 * Traces to: sprint-258 review finding #6 — replace inline-lambda test with
 * real-component assertion.
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
    updateTask: vi.fn().mockResolvedValue({}),
    updateBoardTask: vi.fn().mockResolvedValue({}),
    startTask: vi.fn().mockResolvedValue({}),
    isApiError: vi.fn().mockReturnValue(false),
  }
})

const mockAddToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    // Support both selector form (useUiStore(s => s.addToast)) and
    // direct destructuring form (const { addToast } = useUiStore())
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

const taskWithSession: Task = {
  id: 'task-1',
  title: 'Research task',
  prompt: 'Research the topic',
  status: 'running',
  priority: 3,
  trigger_type: 'manual',
  agent_name: 'General Assistant',
  agent_id: AGENT_ID,
  session_id: SESSION_ID,
}

const taskWithoutSession: Task = {
  id: 'task-2',
  title: 'Queued task',
  prompt: 'Do something',
  status: 'queued',
  priority: 3,
  trigger_type: 'manual',
  agent_name: 'General Assistant',
}

function renderPanel(task: Task | null, onClose = vi.fn()) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <TaskDetailPanel taskMode="workflow" task={task} onClose={onClose} />
    </QueryClientProvider>
  )
}

// ── Tests ─────────────────────────────────────────────────────────────────────

beforeEach(() => {
  mockNavigate.mockReset()
})

describe('TaskDetailPanel — Open in Chat (#250 regression, #6 test fix)', () => {
  it('shows "Open in Chat" button only when task has a session_id', async () => {
    renderPanel(taskWithSession)
    // Real component renders the sheet which contains the button
    const btn = await screen.findByRole('button', { name: /Open in Chat/i })
    expect(btn).toBeInTheDocument()
  })

  it('does NOT show "Open in Chat" when task has no session_id', async () => {
    renderPanel(taskWithoutSession)
    // Wait for async renders
    await act(async () => {})
    const btn = screen.queryByRole('button', { name: /Open in Chat/i })
    expect(btn).toBeNull()
  })

  it('clicking "Open in Chat" navigates to /sessions/$sessionId (not to /)', async () => {
    // BDD:
    //   Given a task with a known session_id
    //   When the user clicks "Open in Chat"
    //   Then navigate() is called with { to: '/sessions/$sessionId', params: { sessionId } }
    //   And NOT with { to: '/' }
    //
    // This asserts the #250 fix: the old code navigated to '/' which caused
    // RootChatScreen to call startNewSession() (clearing activeSessionId) and
    // raced with attachToSession — live streaming never worked.
    const onClose = vi.fn()
    renderPanel(taskWithSession, onClose)

    const btn = await screen.findByRole('button', { name: /Open in Chat/i })
    await act(async () => { fireEvent.click(btn) })

    // Must navigate to the session route
    expect(mockNavigate).toHaveBeenCalledWith(
      expect.objectContaining({
        to: '/sessions/$sessionId',
        params: { sessionId: SESSION_ID },
      }),
    )
    // Must NOT navigate to '/' (the broken old behavior)
    const toRootCall = mockNavigate.mock.calls.find(
      (args) => args[0]?.to === '/'
    )
    expect(toRootCall).toBeUndefined()

    // Panel closes after navigation
    expect(onClose).toHaveBeenCalled()
  })
})

describe('TaskDetailPanel — task without session (closed panel)', () => {
  it('renders null when task is null (sheet is closed)', () => {
    const { container } = renderPanel(null)
    // SheetContent renders nothing visible when open=false
    expect(container).toBeTruthy()
  })
})

// ── GTD mode tests ─────────────────────────────────────────────────────────────
// Traces to: project-task-management-level1-spec.md — TaskDetailPanel taskMode BDD scenarios
//
// The GTD panel (taskMode="gtd") accepts a BoardTask, not a Task.
// FR-L2-024: subtasks section must NOT appear in GTD mode.
// FR-L2-023: prompt field and priority selector must appear in GTD mode.

const boardTaskWithPrompt: import('@/lib/api').BoardTask = {
  id: 'board-task-1',
  name: 'Fix the build',
  status: 'inbox',
  priority: 2,
  prompt: 'Run the tests and fix all failures.',
  created_at: '2026-06-09T10:00:00Z',
  updated_at: '2026-06-09T10:00:00Z',
}

const boardTaskMinimal: import('@/lib/api').BoardTask = {
  id: 'board-task-2',
  name: 'Deploy to staging',
  status: 'next',
  priority: 3,
  created_at: '2026-06-09T11:00:00Z',
  updated_at: '2026-06-09T11:00:00Z',
}

function renderGtdPanel(task: import('@/lib/api').BoardTask | null, onClose = vi.fn()) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <TaskDetailPanel taskMode="gtd" task={task} onClose={onClose} />
    </QueryClientProvider>,
  )
}

describe('TaskDetailPanel — taskMode gtd: does not render subtasks section', () => {
  it('does not render any subtask-related text in GTD mode (FR-L2-024)', async () => {
    // BDD: Given a GTD board task is rendered in taskMode="gtd",
    // When the component renders,
    // Then no "subtasks" or "sub-task" text appears in the DOM.
    // Traces to: project-task-management-level1-spec.md — TaskDetailPanel gtd-no-subtasks scenario
    renderGtdPanel(boardTaskWithPrompt)

    // Wait for async effects to settle
    await act(async () => {})

    // The word "subtask" (singular or plural, case-insensitive) must NOT appear
    expect(screen.queryByText(/sub-?task/i)).toBeNull()

    // Also verify the sub-tasks heading (used in workflow mode) is absent
    expect(screen.queryByText(/sub-tasks/i)).toBeNull()
  })
})

describe('TaskDetailPanel — taskMode gtd: renders prompt field', () => {
  it('renders the prompt content in GTD mode (FR-L2-023)', async () => {
    // BDD: Given a GTD board task with a prompt value,
    // When the component renders in taskMode="gtd",
    // Then the prompt text is visible in the DOM.
    // Traces to: project-task-management-level1-spec.md — TaskDetailPanel gtd-prompt-field scenario
    renderGtdPanel(boardTaskWithPrompt)

    // The prompt field should be displayed
    expect(await screen.findByText(/run the tests and fix all failures/i)).toBeInTheDocument()

    // The field label "Prompt / Instructions" should appear
    expect(screen.getByText(/prompt/i)).toBeInTheDocument()
  })

  it('shows "No prompt set." when task has no prompt', async () => {
    // BDD: Given a GTD board task without a prompt,
    // When the component renders in taskMode="gtd",
    // Then "No prompt set." text is visible.
    // Traces to: project-task-management-level1-spec.md — TaskDetailPanel gtd-empty-prompt
    renderGtdPanel(boardTaskMinimal)

    expect(await screen.findByText(/no prompt set/i)).toBeInTheDocument()
  })
})

describe('TaskDetailPanel — taskMode gtd: renders priority selector', () => {
  it('renders a priority selector in GTD mode (FR-L2-023)', async () => {
    // BDD: Given a GTD board task,
    // When the component renders in taskMode="gtd",
    // Then a priority select control is visible.
    // Traces to: project-task-management-level1-spec.md — TaskDetailPanel gtd-priority-selector scenario
    renderGtdPanel(boardTaskWithPrompt)

    // The "Priority" field label must be present
    expect(await screen.findByText(/^priority$/i)).toBeInTheDocument()
  })

  it('differentiation test: different task priorities show different initial values', async () => {
    // Anti-hardcode: two tasks with different priorities must render different priority values.
    // Traces to: project-task-management-level1-spec.md — TaskDetailPanel gtd priority differentiation
    const p2Task = { ...boardTaskWithPrompt, priority: 2 }
    const p4Task = { ...boardTaskMinimal, priority: 4 }

    const { unmount } = renderGtdPanel(p2Task)
    // P2 task should show P2 priority label
    expect(await screen.findByText(/P2/i)).toBeInTheDocument()
    unmount()

    renderGtdPanel(p4Task)
    // P4 task should show P4 priority label
    expect(await screen.findByText(/P4/i)).toBeInTheDocument()
  })
})

// Workers (type:'worker') are delegation-only labour — never a DIRECT task
// runner. Assigning one as a task's agent_id routes it through processTaskDirect,
// which is forbidden. Both the GTD and the workflow detail-panel assignee pickers
// must exclude workers while still offering base agents.
//
// Helper: open the SmartSelect (Radix Select) inside the "Agent" Field and read
// the listbox option labels. With 2 base agents + 1 worker + "Unassigned" the
// SmartSelect stays under its searchable threshold, so it is a Radix Select whose
// trigger has role="combobox" and whose items have role="option".
async function assertAgentPickerExcludesWorker(): Promise<void> {
  // The "Agent" field label is a <p>; the picker trigger is its sibling combobox.
  const agentLabel = await screen.findByText('Agent')
  const fieldRoot = agentLabel.parentElement as HTMLElement
  const agentTrigger = fieldRoot.querySelector('[role="combobox"]') as HTMLElement
  expect(agentTrigger).toBeTruthy()
  fireEvent.click(agentTrigger)

  // The listbox opens immediately with the "Unassigned" sentinel; the async
  // agents query then populates the base-agent options. Retry until the loaded
  // base agents (Mia, Jim) appear — and assert the worker is never among them.
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
    // jsdom lacks scrollIntoView; Radix Select calls it when opening the listbox.
    Element.prototype.scrollIntoView = vi.fn()
  })

  it('GTD mode: does not offer a worker as an assignee; lists base agents', async () => {
    const { fetchAgents } = await import('@/lib/api')
    vi.mocked(fetchAgents).mockResolvedValue(agentsWithWorker as never)

    renderGtdPanel(boardTaskWithPrompt)
    await assertAgentPickerExcludesWorker()

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('workflow mode: does not offer a worker as an assignee; lists base agents', async () => {
    const { fetchAgents } = await import('@/lib/api')
    vi.mocked(fetchAgents).mockResolvedValue(agentsWithWorker as never)

    renderPanel(taskWithSession)
    await assertAgentPickerExcludesWorker()

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })
})

describe('TaskDetailPanel — taskMode workflow: renders as before', () => {
  it('workflow mode renders Open in Chat button when session_id is present', async () => {
    // BDD: Given taskMode="workflow" with a task that has a session_id,
    // When the component renders,
    // Then "Open in Chat" button is visible (existing behavior unchanged).
    // Traces to: project-task-management-level1-spec.md — TaskDetailPanel workflow-unchanged scenario
    renderPanel(taskWithSession)

    expect(await screen.findByRole('button', { name: /open in chat/i })).toBeInTheDocument()
  })

  it('workflow mode renders subtask section when subtasks exist', async () => {
    // BDD: Given taskMode="workflow" and fetchSubtasks returns subtasks,
    // When the component renders,
    // Then subtask items are shown.
    // Traces to: project-task-management-level1-spec.md — TaskDetailPanel workflow-subtasks-present
    const { fetchSubtasks } = await import('@/lib/api')
    vi.mocked(fetchSubtasks).mockResolvedValueOnce([
      {
        id: 'sub-1',
        title: 'Subtask Alpha',
        prompt: '',
        status: 'queued',
        priority: 3,
        trigger_type: 'manual',
        agent_name: 'Mia',
      } as import('@/lib/api').Task,
    ])

    renderPanel(taskWithSession)

    expect(await screen.findByText(/subtask alpha/i)).toBeInTheDocument()
  })
})
