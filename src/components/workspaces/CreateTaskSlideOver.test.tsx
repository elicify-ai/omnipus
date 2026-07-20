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

import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateTaskSlideOver } from './CreateTaskSlideOver'

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

// Click a react-day-picker day cell (data-day="YYYY-MM-DD") inside an open DateTimePicker/DatePicker popover.
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

// Open a DateTimePicker (by its aria-label) and pick a full date + time.
async function pickDateTime(
  triggerLabel: RegExp,
  { isoDate, hour, minute }: { isoDate: string; hour: string; minute: string },
) {
  fireEvent.click(await screen.findByRole('button', { name: triggerLabel }))
  navigateToMonth(isoDate)
  clickDay(isoDate)
  selectOption('Hour', hour)
  selectOption('Minute', minute)
}

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

// ── API mock ─────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchMilestones: vi.fn().mockResolvedValue([]),
    fetchTasks: vi.fn().mockResolvedValue([]),
    // Fix B: the assignee picker's workspace-team scoping — see the
    // "assignee picker is workspace-team-scoped" describe block below.
    fetchWorkspaceDelegation: vi.fn(),
    createTask: vi.fn(),
    updateTask: vi.fn(),
    tasksQueryKeys: { list: () => ['tasks'] },
    boardTasksQueryKeys: { list: () => ['tasks'] },
    workspacesQueryKeys: {
      list: () => ['workspaces'],
      delegation: (id: string) => ['workspaces', id, 'delegation'],
    },
    milestonesQueryKeys: { list: (id: string) => ['milestones', id] },
    isApiError: vi.fn().mockReturnValue(false),
  }
})

import {
  createTask,
  updateTask,
  fetchAgents,
  fetchMilestones,
  fetchTasks,
  fetchWorkspaceDelegation,
} from '@/lib/api'

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
  // Default: the workspace-team query fails (unmocked in most tests, which
  // don't care about team-scoping) — this is the DEGRADED fallback path
  // (buildTaskAssigneeItems / useWorkspaceTeamIds), which offers the full
  // unscoped agent list, i.e. today's pre-scoping behaviour. Tests that
  // exercise team-scoping itself override this per-test.
  vi.mocked(fetchWorkspaceDelegation).mockReset().mockRejectedValue(new Error('not mocked'))
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

describe('CreateTaskSlideOver — worker-type agents are offered as assignees', () => {
  // Bug fix: tasks CAN be assigned to Subagent/subagent_3p worker-type agents
  // (workspace-team membership is what the backend now enforces, not agent
  // kind — see pkg/gateway/rest_tasks.go::validateTaskAgentID). The picker
  // must list workers alongside base agents, distinguished by a " · Worker"
  // suffix (mirrors AddAgentPicker's " · leaf" convention).
  const agentsWithWorker = [
    { id: 'mia', name: 'Mia', type: 'core', default: false },
    { id: 'jim', name: 'Jim', type: 'core', default: false },
    { id: 'builder', name: 'Builder Worker', type: 'Subagent', default: false },
  ]

  it('offers a Subagent worker as an assignee option, distinguished from base agents', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(agentsWithWorker as never)
    Element.prototype.scrollIntoView = vi.fn()

    renderSlideOver()

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

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('selecting a Subagent worker as assignee and submitting sends its id as agent_id', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(agentsWithWorker as never)
    vi.mocked(createTask).mockResolvedValueOnce(makeCreatedTask({ agent_id: 'builder' }) as never)
    Element.prototype.scrollIntoView = vi.fn()

    renderSlideOver()

    await openAgentPicker()

    const workerOption = await screen.findByRole('option', { name: /Builder Worker/i })
    fireEvent.pointerDown(workerOption, { pointerId: 1, button: 0 })
    fireEvent.click(workerOption)

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Delegate to worker' } })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())
    const callArg = vi.mocked(createTask).mock.calls[0][0]
    expect(callArg.agent_id).toBe('builder')

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('surfaces a backend rejection (e.g. non-team-member agent) via toast instead of failing silently', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(agentsWithWorker as never)
    vi.mocked(createTask).mockRejectedValueOnce(new Error('agent "builder" is not a member of workspace "proj-test"'))
    Element.prototype.scrollIntoView = vi.fn()

    renderSlideOver()

    await openAgentPicker()

    const workerOption = await screen.findByRole('option', { name: /Builder Worker/i })
    fireEvent.pointerDown(workerOption, { pointerId: 1, button: 0 })
    fireEvent.click(workerOption)

    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'Delegate to worker' } })
    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))

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
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', default: false },
      { id: 'ext', name: 'External Runner', type: 'subagent_3p', default: false },
    ] as never)
    vi.mocked(fetchWorkspaceDelegation).mockResolvedValue({
      workspace_id: 'proj-test',
      edges: [],
      team: ['mia', 'ext'],
      default_depth: 3,
    } as never)
    Element.prototype.scrollIntoView = vi.fn()

    renderSlideOver()

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

describe('CreateTaskSlideOver — assignee picker is workspace-team-scoped (Fix B)', () => {
  it('excludes an agent that is NOT a member of the workspace team, regardless of kind', async () => {
    // Team-scoping is the real gate now, not worker-vs-main kind: an
    // off-team CORE agent must be excluded exactly like an off-team worker
    // would be — mirrors the backend's workspaceTeamSet.
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', default: false },
      { id: 'offteam', name: 'Off Team Agent', type: 'core', default: false },
    ] as never)
    vi.mocked(fetchWorkspaceDelegation).mockResolvedValue({
      workspace_id: 'proj-test',
      edges: [],
      team: ['mia'],
      default_depth: 3,
    } as never)
    Element.prototype.scrollIntoView = vi.fn()

    renderSlideOver()

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
    // Degraded behavior (item 3 of the fix): a failed team-set fetch must not
    // leave the picker empty — it falls back to the pre-scoping unscoped
    // list, same as today's behaviour. The backend still enforces team
    // membership server-side, so this is a graceful degrade, not a bypass.
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', default: false },
      { id: 'offteam', name: 'Off Team Agent', type: 'core', default: false },
    ] as never)
    vi.mocked(fetchWorkspaceDelegation).mockRejectedValue(new Error('network down'))
    Element.prototype.scrollIntoView = vi.fn()

    renderSlideOver()

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

  it('F1: shows an inline "team unavailable" hint next to the picker when the workspace-team query errors', async () => {
    // The degraded unscoped fallback above must not be silent — a muted hint
    // renders next to the Agent picker so the degrade is visible, not
    // indistinguishable from a healthy, unrestricted workspace.
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', default: false },
    ] as never)
    vi.mocked(fetchWorkspaceDelegation).mockRejectedValue(new Error('network down'))

    renderSlideOver()

    expect(await screen.findByText(/team list unavailable — showing all agents/i)).toBeInTheDocument()
  })

  it('does NOT show the "team unavailable" hint when the workspace-team query succeeds', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', default: false },
    ] as never)
    vi.mocked(fetchWorkspaceDelegation).mockResolvedValue({
      workspace_id: 'proj-test',
      edges: [],
      team: ['mia'],
      default_depth: 3,
    } as never)

    renderSlideOver()

    await waitFor(() => expect(screen.getByText('Agent')).toBeInTheDocument())
    expect(screen.queryByText(/team list unavailable/i)).toBeNull()
  })

  it('disables the picker with a loading placeholder while the team query is in flight', async () => {
    let resolveDelegation: (v: unknown) => void = () => {}
    vi.mocked(fetchWorkspaceDelegation).mockReturnValue(
      new Promise((resolve) => { resolveDelegation = resolve }) as never,
    )
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'mia', name: 'Mia', type: 'core', default: false },
    ] as never)

    renderSlideOver()

    const agentLabel = await screen.findByText('Agent')
    const fieldRoot = agentLabel.parentElement as HTMLElement
    const agentTrigger = fieldRoot.querySelector('[role="combobox"]') as HTMLElement
    // The real signal (item 3 of the fix): the picker is disabled while the
    // team-set query is in flight, rather than offering a stale/unscoped
    // list or an interactive-but-wrong picker.
    expect(agentTrigger).toBeDisabled()

    // Resolve so the pending promise doesn't leak into the next test.
    resolveDelegation({ workspace_id: 'proj-test', edges: [], team: ['mia'], default_depth: 3 })
    await waitFor(() => expect(agentTrigger).not.toBeDisabled())
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

    // Pick the date + time via the DateTimePicker (calendar day + Hour/Minute selects)
    await pickDateTime(/trigger date and time/i, { isoDate: '2026-07-31', hour: '17', minute: '00' })

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())

    const body = vi.mocked(createTask).mock.calls[0][0]
    expect(body.trigger?.type).toBe('once')
    expect(typeof body.trigger?.config.at_ms).toBe('number')
    expect(body.trigger?.config.at_ms).toBe(new Date('2026-07-31T17:00').getTime())
    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  // FR-011/US-3.3 (test 23): recurring trigger options are removed from the
  // generic create form entirely — recurring tasks are calendar-only (D3),
  // created/edited exclusively via the calendar's event slide-over. The two
  // tests that used to post `every`/`recurring` triggers from this form are
  // replaced by the trim-verification tests below; `every`/`recurring`
  // remain wire-legal (no enum change) but no form produces them anymore.
  it('the Trigger dropdown offers only "None (manual)" and "Once (at a time)" — no recurring options', async () => {
    Element.prototype.scrollIntoView = vi.fn()
    renderSlideOver()

    fireEvent.click(document.getElementById('ct-trigger') as HTMLElement)

    // Query by role="option" — the closed trigger's own selected-value
    // display ALSO reads "None (manual)" (the default), so a plain
    // getByText would ambiguously match both it and the open option.
    expect(await screen.findByRole('option', { name: /none \(manual\)/i })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: /once \(at a time\)/i })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /every \(interval\)/i })).toBeNull()
    expect(screen.queryByRole('option', { name: /recurring \(cron\)/i })).toBeNull()
    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('renders no cron input or interval input anywhere in the form, regardless of trigger selection', async () => {
    Element.prototype.scrollIntoView = vi.fn()
    renderSlideOver()

    // Default (manual) — no trigger-related date/cron/interval controls.
    expect(screen.queryByLabelText(/cron expression/i)).toBeNull()
    expect(screen.queryByLabelText(/interval in minutes/i)).toBeNull()

    // Switch to the only other offered kind, "Once" — still no cron/interval
    // input; only the date/time picker appears.
    fireEvent.click(document.getElementById('ct-trigger') as HTMLElement)
    fireEvent.click(await screen.findByText(/once \(at a time\)/i))

    expect(await screen.findByRole('button', { name: /trigger date and time/i })).toBeInTheDocument()
    expect(screen.queryByLabelText(/cron expression/i)).toBeNull()
    expect(screen.queryByLabelText(/interval in minutes/i)).toBeNull()
    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('blocks Create when "Once" is selected but no datetime is set', async () => {
    Element.prototype.scrollIntoView = vi.fn()
    renderSlideOver()
    fireEvent.change(screen.getByLabelText(/title/i), { target: { value: 'No time' } })
    fireEvent.click(document.getElementById('ct-trigger') as HTMLElement)
    fireEvent.click(await screen.findByText(/once \(at a time\)/i))

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    // Two DateTimePicker triggers (Trigger + Due) also show "Pick a date and time" as
    // their empty placeholder, so match the error paragraph text uniquely.
    expect(await screen.findByText(/pick a date and time for the one-time trigger/i)).toBeInTheDocument()
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
    await pickDateTime(/^due date$/i, { isoDate: '2026-08-01', hour: '09', minute: '00' })

    fireEvent.click(screen.getByRole('button', { name: /^create$/i }))
    await waitFor(() => expect(vi.mocked(createTask)).toHaveBeenCalledOnce())

    const body = vi.mocked(createTask).mock.calls[0][0]
    expect(body.due).toBe(new Date('2026-08-01T09:00').toISOString())
  })

  it('posts todos from the checklist as {text, status:"pending"}', async () => {
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
      { text: 'First item', status: 'pending' },
      { text: 'Second item', status: 'pending' },
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
