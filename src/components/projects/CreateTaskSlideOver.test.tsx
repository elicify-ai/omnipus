/**
 * CreateTaskSlideOver.test.tsx
 *
 * Tests for the CreateTaskSlideOver component covering all BDD scenarios
 * from the project-task-management-level1-spec.
 *
 * Traces to: project-task-management-level1-spec.md — CreateTaskSlideOver BDD scenarios
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
    createBoardTask: vi.fn(),
    boardTasksQueryKeys: { list: () => ['board-tasks'] },
    workspacesQueryKeys: { list: () => ['workspaces'] },
    milestonesQueryKeys: { list: (id: string) => ['milestones', id] },
    isApiError: vi.fn().mockReturnValue(false),
  }
})

import { createBoardTask, fetchAgents, fetchMilestones } from '@/lib/api'

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
  vi.mocked(createBoardTask).mockReset()
  mockAddToast.mockReset()
})

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('CreateTaskSlideOver — renders all fields', () => {
  it('renders with all expected form fields and action buttons', async () => {
    // BDD: Given the CreateTaskSlideOver is open,
    // When it renders,
    // Then Name, Prompt, Priority, Milestone placeholder, Agent, Create, and Create & Start are visible.
    // Traces to: project-task-management-level1-spec.md — CreateTaskSlideOver render test
    renderSlideOver()

    // Name field — by label
    expect(screen.getByLabelText(/name/i)).toBeInTheDocument()

    // Prompt / Instructions field
    expect(screen.getByLabelText(/prompt/i)).toBeInTheDocument()

    // Priority label is present
    expect(screen.getByText(/priority/i)).toBeInTheDocument()

    // "No milestones — create one first" message since fetchMilestones returns []
    expect(await screen.findByText(/no milestones/i)).toBeInTheDocument()

    // Create button
    expect(screen.getByRole('button', { name: /^create$/i })).toBeInTheDocument()

    // Create & Start button
    expect(screen.getByRole('button', { name: /create & start/i })).toBeInTheDocument()

    // Cancel button
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
  })
})

describe('CreateTaskSlideOver — Create button calls createBoardTask with status inbox', () => {
  it('Create calls createBoardTask with status: inbox when name is filled', async () => {
    // BDD: Given a valid task name is entered,
    // When the user clicks Create,
    // Then createBoardTask is called with status: 'inbox'.
    // Traces to: project-task-management-level1-spec.md — CreateTaskSlideOver create-inbox scenario
    const created = {
      id: 'new-task',
      name: 'My task',
      status: 'inbox',
      created_at: '2026-06-09T10:00:00Z',
      updated_at: '2026-06-09T10:00:00Z',
    }
    vi.mocked(createBoardTask).mockResolvedValueOnce(created as never)

    renderSlideOver()

    // Fill in the task name
    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'My task' } })

    // Click the Create button
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(vi.mocked(createBoardTask)).toHaveBeenCalledOnce())

    const callArg = vi.mocked(createBoardTask).mock.calls[0][0]
    expect(callArg.name).toBe('My task')
    expect(callArg.status).toBe('inbox')
    expect(callArg.workspace_id).toBe('proj-test')
  })

  it('differentiation test: Create sends inbox, Create & Start sends next — different statuses', async () => {
    // Anti-hardcode: two calls with different buttons must produce different status values.
    // Traces to: project-task-management-level1-spec.md — CreateTaskSlideOver differentiation
    const inboxTask = { id: 'inbox-task', name: 'Task A', status: 'inbox', created_at: '2026-06-09T10:00:00Z', updated_at: '2026-06-09T10:00:00Z' }
    const nextTask = { id: 'next-task', name: 'Task B', status: 'next', created_at: '2026-06-09T10:01:00Z', updated_at: '2026-06-09T10:01:00Z' }

    const onOpenChange = vi.fn()

    // First render — Create → inbox
    const { unmount } = render(
      <QueryClientProvider client={makeClient()}>
        <CreateTaskSlideOver open onOpenChange={onOpenChange} workspaceId="proj-1" />
      </QueryClientProvider>,
    )
    vi.mocked(createBoardTask).mockResolvedValueOnce(inboxTask as never)
    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'Task A' } })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    await waitFor(() => expect(vi.mocked(createBoardTask)).toHaveBeenCalledTimes(1))
    expect(vi.mocked(createBoardTask).mock.calls[0][0].status).toBe('inbox')
    unmount()

    vi.mocked(createBoardTask).mockReset()

    // Second render — Create & Start → next
    render(
      <QueryClientProvider client={makeClient()}>
        <CreateTaskSlideOver open onOpenChange={vi.fn()} workspaceId="proj-1" />
      </QueryClientProvider>,
    )
    vi.mocked(createBoardTask).mockResolvedValueOnce(nextTask as never)
    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'Task B' } })
    fireEvent.click(screen.getByRole('button', { name: /create & start/i }))
    await waitFor(() => expect(vi.mocked(createBoardTask)).toHaveBeenCalledTimes(1))
    expect(vi.mocked(createBoardTask).mock.calls[0][0].status).toBe('next')
  })
})

describe('CreateTaskSlideOver — Create & Start calls createBoardTask with status next', () => {
  it('Create & Start calls createBoardTask with status: next when name is filled', async () => {
    // BDD: Given a valid task name is entered,
    // When the user clicks Create & Start,
    // Then createBoardTask is called with status: 'next'.
    // Traces to: project-task-management-level1-spec.md — CreateTaskSlideOver create-next scenario
    const created = {
      id: 'next-task',
      name: 'Start immediately',
      status: 'next',
      created_at: '2026-06-09T10:00:00Z',
      updated_at: '2026-06-09T10:00:00Z',
    }
    vi.mocked(createBoardTask).mockResolvedValueOnce(created as never)

    renderSlideOver()

    fireEvent.change(screen.getByLabelText(/name/i), { target: { value: 'Start immediately' } })
    fireEvent.click(screen.getByRole('button', { name: /create & start/i }))

    await waitFor(() => expect(vi.mocked(createBoardTask)).toHaveBeenCalledOnce())

    const callArg = vi.mocked(createBoardTask).mock.calls[0][0]
    expect(callArg.name).toBe('Start immediately')
    expect(callArg.status).toBe('next')
  })
})

describe('CreateTaskSlideOver — name is required validation', () => {
  it('clicking Create with empty name shows validation error and makes no API call', async () => {
    // BDD: Given the Name field is empty,
    // When the user clicks Create,
    // Then "Name is required" error is displayed,
    // And createBoardTask is NOT called.
    // Traces to: project-task-management-level1-spec.md — CreateTaskSlideOver name-required validation
    renderSlideOver()

    // Name field is empty by default — click Create
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    expect(await screen.findByText(/name is required/i)).toBeInTheDocument()
    expect(vi.mocked(createBoardTask)).not.toHaveBeenCalled()
  })

  it('clicking Create & Start with empty name shows validation error and makes no API call', async () => {
    // BDD: Given the Name field is empty,
    // When the user clicks Create & Start,
    // Then validation error shown and API not called.
    // Traces to: project-task-management-level1-spec.md — CreateTaskSlideOver name-required validation (create-start)
    renderSlideOver()

    fireEvent.click(screen.getByRole('button', { name: /create & start/i }))

    expect(await screen.findByText(/name is required/i)).toBeInTheDocument()
    expect(vi.mocked(createBoardTask)).not.toHaveBeenCalled()
  })
})

describe('CreateTaskSlideOver — priority defaults', () => {
  it('priority defaults to P3 when slide-over opens', async () => {
    // BDD: Given the CreateTaskSlideOver is opened without pre-filled priority,
    // When it renders,
    // Then the priority select shows P3 as the selected value (the select trigger
    // renders "P3 — Medium" and the label badge shows "P3").
    // Traces to: project-task-management-level1-spec.md — CreateTaskSlideOver priority-default
    renderSlideOver()

    await act(async () => {})

    // The Select trigger renders the selected value as "P3 — Medium"
    // getAllByText ensures we handle multiple matches gracefully
    const p3Elements = screen.getAllByText(/p3/i)
    expect(p3Elements.length).toBeGreaterThanOrEqual(1)

    // Verify none of them show a different priority (e.g. P1, P2, P4, P5)
    expect(screen.queryByText(/^p1/i)).toBeNull()
    expect(screen.queryByText(/^p2/i)).toBeNull()
    expect(screen.queryByText(/^p4/i)).toBeNull()
    expect(screen.queryByText(/^p5/i)).toBeNull()
  })
})

describe('CreateTaskSlideOver — Cancel closes the slide-over', () => {
  it('clicking Cancel calls onOpenChange(false) and makes no API call', async () => {
    // BDD: Given the slide-over is open,
    // When the user clicks Cancel,
    // Then onOpenChange(false) is called,
    // And no API call is made.
    // Traces to: project-task-management-level1-spec.md — CreateTaskSlideOver cancel scenario
    const user = userEvent.setup()
    const onOpenChange = vi.fn()

    render(
      <QueryClientProvider client={makeClient()}>
        <CreateTaskSlideOver open onOpenChange={onOpenChange} workspaceId="proj-test" />
      </QueryClientProvider>,
    )

    await user.click(screen.getByRole('button', { name: /cancel/i }))

    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(vi.mocked(createBoardTask)).not.toHaveBeenCalled()
  })
})
