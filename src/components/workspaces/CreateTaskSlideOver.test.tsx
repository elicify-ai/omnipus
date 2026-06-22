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
    fetchTasks: vi.fn().mockResolvedValue([]),
    createTask: vi.fn(),
    updateTask: vi.fn(),
    tasksQueryKeys: { list: () => ['tasks'] },
    boardTasksQueryKeys: { list: () => ['tasks'] },
    workspacesQueryKeys: { list: () => ['workspaces'] },
    milestonesQueryKeys: { list: (id: string) => ['milestones', id] },
    isApiError: vi.fn().mockReturnValue(false),
  }
})

import { createTask, updateTask, fetchAgents, fetchMilestones, fetchTasks } from '@/lib/api'

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
  vi.mocked(fetchTasks).mockResolvedValue([])
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

describe('CreateTaskSlideOver — full task UX fields (trigger / depends-on / due / todos)', () => {
  function makeWsTask(over: Record<string, unknown> = {}) {
    return makeCreatedTask({ id: 'dep-1', title: 'Existing dependency', ...over })
  }

  it('posts a once trigger with at_ms when "Once" is selected with a datetime', async () => {
    vi.mocked(createTask).mockResolvedValueOnce(makeCreatedTask({ title: 'Trig' }) as never)
    Element.prototype.scrollIntoView = vi.fn()
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Trig' } })

    // Open the Trigger select and pick "Once"
    const trigCombo = document.getElementById('ct-trigger') as HTMLElement
    fireEvent.click(trigCombo)
    fireEvent.click(await screen.findByText(/once \(at a time\)/i))

    // Fill the datetime-local input
    const dt = await screen.findByLabelText(/trigger date and time/i)
    fireEvent.change(dt, { target: { value: '2026-07-31T17:00' } })

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())

    const body = vi.mocked(createTask).mock.calls[0][0]
    expect(body.trigger?.type).toBe('once')
    expect(typeof body.trigger?.config.at_ms).toBe('number')
    expect(body.trigger?.config.at_ms).toBe(new Date('2026-07-31T17:00').getTime())
    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('posts an every trigger with every_ms derived from minutes', async () => {
    vi.mocked(createTask).mockResolvedValueOnce(makeCreatedTask() as never)
    Element.prototype.scrollIntoView = vi.fn()
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Every task' } })
    fireEvent.click(document.getElementById('ct-trigger') as HTMLElement)
    fireEvent.click(await screen.findByText(/every \(interval\)/i))

    const minutes = await screen.findByLabelText(/interval in minutes/i)
    fireEvent.change(minutes, { target: { value: '30' } })

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())

    const body = vi.mocked(createTask).mock.calls[0][0]
    expect(body.trigger?.type).toBe('every')
    expect(body.trigger?.config.every_ms).toBe(30 * 60_000)
    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('posts a recurring trigger with the cron expression', async () => {
    vi.mocked(createTask).mockResolvedValueOnce(makeCreatedTask() as never)
    Element.prototype.scrollIntoView = vi.fn()
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Cron task' } })
    fireEvent.click(document.getElementById('ct-trigger') as HTMLElement)
    fireEvent.click(await screen.findByText(/recurring \(cron\)/i))

    const cron = await screen.findByLabelText(/cron expression/i)
    fireEvent.change(cron, { target: { value: '0 8 * * *' } })

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())

    const body = vi.mocked(createTask).mock.calls[0][0]
    expect(body.trigger?.type).toBe('recurring')
    expect(body.trigger?.config.cron_expr).toBe('0 8 * * *')
    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('blocks Create when "Once" is selected but no datetime is set', async () => {
    Element.prototype.scrollIntoView = vi.fn()
    renderSlideOver()
    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'No time' } })
    fireEvent.click(document.getElementById('ct-trigger') as HTMLElement)
    fireEvent.click(await screen.findByText(/once \(at a time\)/i))

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    expect(await screen.findByText(/pick a date and time/i)).toBeInTheDocument()
    expect(vi.mocked(createTask)).not.toHaveBeenCalled()
    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('posts blocked_by with the selected dependency task IDs', async () => {
    vi.mocked(fetchTasks).mockResolvedValue([
      makeWsTask({ id: 'dep-a', title: 'Dependency A' }),
      makeWsTask({ id: 'dep-b', title: 'Dependency B' }),
    ] as never)
    vi.mocked(createTask).mockResolvedValueOnce(makeCreatedTask() as never)
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Dependent' } })

    // Open the depends-on popover and check one dependency
    fireEvent.click(await screen.findByText(/no dependencies/i))
    fireEvent.click(await screen.findByText('Dependency A'))

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())

    const body = vi.mocked(createTask).mock.calls[0][0]
    expect(body.blocked_by).toEqual(['dep-a'])
  })

  it('posts due as an RFC3339 string when a due date is set', async () => {
    vi.mocked(createTask).mockResolvedValueOnce(makeCreatedTask() as never)
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'With due' } })
    fireEvent.change(screen.getByLabelText(/^due date$/i), { target: { value: '2026-08-01T09:00' } })

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())

    const body = vi.mocked(createTask).mock.calls[0][0]
    expect(body.due).toBe(new Date('2026-08-01T09:00').toISOString())
  })

  it('posts todos from the checklist as {text, done:false}', async () => {
    vi.mocked(createTask).mockResolvedValueOnce(makeCreatedTask() as never)
    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'With todos' } })

    const todoInput = screen.getByLabelText(/new checklist item/i)
    fireEvent.change(todoInput, { target: { value: 'First item' } })
    fireEvent.click(screen.getByRole('button', { name: /add checklist item/i }))
    fireEvent.change(todoInput, { target: { value: 'Second item' } })
    fireEvent.click(screen.getByRole('button', { name: /add checklist item/i }))

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())

    const body = vi.mocked(createTask).mock.calls[0][0]
    expect(body.todos).toEqual([
      { text: 'First item', done: false },
      { text: 'Second item', done: false },
    ])
  })

  it('omits trigger/blocked_by/due/todos from the body when none are set', async () => {
    vi.mocked(createTask).mockResolvedValueOnce(makeCreatedTask() as never)
    renderSlideOver()
    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Bare' } })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())

    const body = vi.mocked(createTask).mock.calls[0][0]
    expect(body.trigger).toBeUndefined()
    expect(body.blocked_by).toBeUndefined()
    expect(body.due).toBeUndefined()
    expect(body.todos).toBeUndefined()
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
