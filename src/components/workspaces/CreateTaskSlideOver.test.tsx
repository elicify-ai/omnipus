/**
 * CreateTaskSlideOver.test.tsx
 *
 * Tests for the CreateTaskSlideOver component against the unified Sprint 2
 * Task model. Key changes from the old BoardTask model:
 *   - Field: `name` → `title`
 *   - API:   `createBoardTask` → `createTask`
 *   - Action: always `llm`, surface: always `user`
 *   - No `status` in create body — server always seeds `inbox`
 *   - "Create & Start" → "Create & Run" (create + PATCH to in_progress)
 *   - Validation label: "Name" → "Title"
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateTaskSlideOver } from './CreateTaskSlideOver'

// ── API mock ─────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchMilestones: vi.fn().mockResolvedValue([]),
    createTask: vi.fn(),
    updateTask: vi.fn(),
    tasksQueryKeys: { list: () => ['tasks'] },
    boardTasksQueryKeys: { list: () => ['tasks'] },
    workspacesQueryKeys: { list: () => ['workspaces'] },
    milestonesQueryKeys: { list: (id: string) => ['milestones', id] },
    isApiError: vi.fn().mockReturnValue(false),
  }
})

import { createTask, updateTask, fetchAgents, fetchMilestones } from '@/lib/api'

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

// Minimal Task shape returned by mock createTask
function makeCreatedTask(overrides: Record<string, unknown> = {}) {
  return {
    id: 'new-task',
    title: 'New task',
    action: 'llm',
    status: 'inbox',
    priority: 3,
    workspace_id: 'proj-test',
    surface: 'user',
    owner: 'alice',
    created_by: 'alice',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

function renderSlideOver(props: Partial<{
  open: boolean
  onOpenChange: (open: boolean) => void
  workspaceId: string
  milestoneId: string | null
}> = {}) {
  const defaults = {
    open: true,
    onOpenChange: vi.fn(),
    workspaceId: 'proj-test',
    milestoneId: null,
  }
  const merged = { ...defaults, ...props }
  return render(
    <QueryClientProvider client={makeClient()}>
      <CreateTaskSlideOver
        open={merged.open}
        onOpenChange={merged.onOpenChange}
        workspaceId={merged.workspaceId}
        milestoneId={merged.milestoneId}
      />
    </QueryClientProvider>,
  )
}

// ── Test setup ────────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.mocked(fetchAgents).mockResolvedValue([])
  vi.mocked(fetchMilestones).mockResolvedValue([])
  vi.mocked(createTask).mockReset()
  vi.mocked(updateTask).mockReset()
  mockAddToast.mockReset()
})

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('CreateTaskSlideOver — renders all fields', () => {
  it('renders with all expected form fields and action buttons', async () => {
    // BDD: Given the CreateTaskSlideOver is open,
    // When it renders,
    // Then Title, Prompt, Priority, Milestone placeholder, Agent, Create, and Create & Run are visible.
    renderSlideOver()

    // Title field — by label
    expect(screen.getByLabelText(/title/i)).toBeInTheDocument()

    // Prompt / Instructions field
    expect(screen.getByLabelText(/prompt/i)).toBeInTheDocument()

    // Priority label is present
    expect(screen.getByText(/priority/i)).toBeInTheDocument()

    // "No milestones — create one first" message since fetchMilestones returns []
    expect(await screen.findByText(/no milestones/i)).toBeInTheDocument()

    // Create button
    expect(screen.getByRole('button', { name: /^create$/i })).toBeInTheDocument()

    // Create & Run button
    expect(screen.getByRole('button', { name: /create & run/i })).toBeInTheDocument()

    // Cancel button
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
  })
})

describe('CreateTaskSlideOver — Create button calls createTask and lands in inbox', () => {
  it('Create calls createTask with correct body (no status field — server seeds inbox)', async () => {
    // BDD: Given a valid task title is entered,
    // When the user clicks Create,
    // Then createTask is called with title, action:'llm', workspace_id, surface:'user'.
    // Status is NOT sent — server always seeds inbox.
    vi.mocked(createTask).mockResolvedValueOnce(makeCreatedTask({ title: 'My task' }) as never)

    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'My task' } })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())

    const callArg = vi.mocked(createTask).mock.calls[0][0]
    expect(callArg.title).toBe('My task')
    expect(callArg.action).toBe('llm')
    expect(callArg.surface).toBe('user')
    expect(callArg.workspace_id).toBe('proj-test')
    // No status field in the create body
    expect((callArg as Record<string, unknown>).status).toBeUndefined()
    // No updateTask call for plain "Create"
    expect(vi.mocked(updateTask)).not.toHaveBeenCalled()
  })

  it('differentiation test: Create vs Create & Run produce different API call sequences', async () => {
    // Anti-hardcode: Create calls createTask only; Create & Run calls createTask + updateTask(in_progress).
    const inboxTask = makeCreatedTask({ id: 'inbox-task', title: 'Task A' })
    const runTask = makeCreatedTask({ id: 'run-task', title: 'Task B', status: 'in_progress' })
    const runTaskInProgress = makeCreatedTask({ id: 'run-task', status: 'in_progress' })

    const onOpenChange = vi.fn()

    // First render — Create → createTask only
    const { unmount } = render(
      <QueryClientProvider client={makeClient()}>
        <CreateTaskSlideOver open onOpenChange={onOpenChange} workspaceId="proj-1" />
      </QueryClientProvider>,
    )
    vi.mocked(createTask).mockResolvedValueOnce(inboxTask as never)
    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Task A' } })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledTimes(1))
    expect(vi.mocked(updateTask)).not.toHaveBeenCalled()
    unmount()

    vi.mocked(createTask).mockReset()
    vi.mocked(updateTask).mockReset()

    // Second render — Create & Run → createTask + updateTask(in_progress)
    render(
      <QueryClientProvider client={makeClient()}>
        <CreateTaskSlideOver open onOpenChange={vi.fn()} workspaceId="proj-1" />
      </QueryClientProvider>,
    )
    vi.mocked(createTask).mockResolvedValueOnce(runTask as never)
    vi.mocked(updateTask).mockResolvedValueOnce(runTaskInProgress as never)
    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Task B' } })
    fireEvent.click(screen.getByRole('button', { name: /create & run/i }))
    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledTimes(1))
    expect(vi.mocked(updateTask).mock.calls[0][1]).toMatchObject({ status: 'in_progress' })
  })
})

describe('CreateTaskSlideOver — Create & Run calls createTask then PATCH in_progress', () => {
  it('Create & Run calls createTask then updateTask with status in_progress', async () => {
    // BDD: Given a valid task title is entered,
    // When the user clicks Create & Run,
    // Then createTask is called first, then updateTask(id, { status: 'in_progress' }).
    const created = makeCreatedTask({ id: 'run-task', title: 'Start immediately' })
    const running = makeCreatedTask({ id: 'run-task', status: 'in_progress' })

    vi.mocked(createTask).mockResolvedValueOnce(created as never)
    vi.mocked(updateTask).mockResolvedValueOnce(running as never)

    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Start immediately' } })
    fireEvent.click(screen.getByRole('button', { name: /create & run/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())
    await waitFor(() => expect(vi.mocked(updateTask)).toHaveBeenCalledOnce())

    const updateArg = vi.mocked(updateTask).mock.calls[0][1]
    expect(updateArg).toMatchObject({ status: 'in_progress' })
  })
})

describe('CreateTaskSlideOver — title is required validation', () => {
  it('clicking Create with empty title shows validation error and makes no API call', async () => {
    // BDD: Given the Title field is empty,
    // When the user clicks Create,
    // Then "Title is required" error is displayed,
    // And createTask is NOT called.
    renderSlideOver()

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    expect(await screen.findByText(/title is required/i)).toBeInTheDocument()
    expect(vi.mocked(createTask)).not.toHaveBeenCalled()
  })

  it('clicking Create & Run with empty title shows validation error and makes no API call', async () => {
    renderSlideOver()

    fireEvent.click(screen.getByRole('button', { name: /create & run/i }))

    expect(await screen.findByText(/title is required/i)).toBeInTheDocument()
    expect(vi.mocked(createTask)).not.toHaveBeenCalled()
  })
})

describe('CreateTaskSlideOver — priority defaults', () => {
  it('priority defaults to P3 when slide-over opens', async () => {
    renderSlideOver()

    await act(async () => {})

    const p3Elements = screen.getAllByText(/p3/i)
    expect(p3Elements.length).toBeGreaterThanOrEqual(1)

    expect(screen.queryByText(/^p1/i)).toBeNull()
    expect(screen.queryByText(/^p2/i)).toBeNull()
    expect(screen.queryByText(/^p4/i)).toBeNull()
    expect(screen.queryByText(/^p5/i)).toBeNull()
  })
})

describe('CreateTaskSlideOver — workers excluded from the agent assignee picker', () => {
  const agentsWithWorker = [
    { id: 'mia', name: 'Mia', type: 'core', default: false },
    { id: 'jim', name: 'Jim', type: 'core', default: false },
    { id: 'builder', name: 'Builder Worker', type: 'worker', default: false },
  ]

  it('does not offer a worker as an assignee option; lists base agents', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(agentsWithWorker as never)
    Element.prototype.scrollIntoView = vi.fn()

    renderSlideOver()

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

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })
})

describe('CreateTaskSlideOver — Cancel closes the slide-over', () => {
  it('clicking Cancel calls onOpenChange(false) and makes no API call', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()

    render(
      <QueryClientProvider client={makeClient()}>
        <CreateTaskSlideOver open onOpenChange={onOpenChange} workspaceId="proj-test" />
      </QueryClientProvider>,
    )

    await user.click(screen.getByRole('button', { name: /cancel/i }))

    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(vi.mocked(createTask)).not.toHaveBeenCalled()
  })
})
