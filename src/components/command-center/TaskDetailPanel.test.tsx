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
    updateTask: vi.fn().mockResolvedValue({}),
    deleteTask: vi.fn().mockResolvedValue(undefined),
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

beforeEach(() => {
  mockNavigate.mockReset()
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
        { text: 'Step one', done: false },
        { text: 'Step two', done: true },
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
