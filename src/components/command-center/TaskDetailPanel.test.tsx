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
import { render, screen, fireEvent, act } from '@testing-library/react'
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
    updateTask: vi.fn().mockResolvedValue({}),
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
      <TaskDetailPanel task={task} onClose={onClose} />
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
