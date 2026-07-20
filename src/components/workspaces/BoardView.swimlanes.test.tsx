/**
 * BoardView.swimlanes.test.tsx
 *
 * Plan Swimlane board redesign — the flat 7-column board becomes a sticky
 * status-column header row + horizontal swimlane bands grouped by plan.
 * Covers:
 *   1. Grouping: one band per plan, containing only that plan's tasks; a
 *      final "Loose tasks (no plan)" band for tasks with no `plan_id`,
 *      rendered even when empty.
 *   2. Lane order: running → approved → draft → done/failed, Loose always
 *      last, regardless of input order.
 *   3. Collapse/expand: terminal (done/failed) plans default-collapsed,
 *      everything else default-expanded; clicking the chevron toggles and
 *      hides/shows that band's status-cell row; a collapsed band still shows
 *      its one-line progress summary in the header.
 *   4. The ⑂ "view graph" button is disabled below 2 member tasks (plan-wide,
 *      NOT scoped to the active tag filter) and, when enabled, sets
 *      `activePlanId` and navigates to this workspace's Graph tab.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BoardView } from './BoardView'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { Task, Plan } from '@/lib/api'

// ── Mocks ────────────────────────────────────────────────────────────────────

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => mockNavigate,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSubtasks: vi.fn().mockResolvedValue([]),
    tasksQueryKeys: {
      list: (p: unknown) => ['tasks', p],
      subtasks: (id: string) => ['tasks', id, 'subtasks'],
    },
  }
})

// ── Fixtures ─────────────────────────────────────────────────────────────────

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Task',
    status: 'inbox',
    action: 'llm',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

function makePlan(overrides: Partial<Plan> = {}): Plan {
  return {
    id: 'plan-1',
    workspace_id: 'ws-1',
    title: 'Plan',
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

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderBoard(props: Partial<React.ComponentProps<typeof BoardView>> = {}) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <BoardView
        tasks={[]}
        plans={[]}
        agents={[]}
        workspaceId="ws-1"
        activeTagFilter={null}
        altitude="top-level"
        onAltitudeChange={vi.fn()}
        onTaskClick={vi.fn()}
        onNewTask={vi.fn()}
        onTaskMove={vi.fn()}
        onMoveRejected={vi.fn()}
        {...props}
      />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockNavigate.mockReset()
  // The lane-collapse map and the Graph-tab scope are real Zustand store
  // state (not mocked here) — reset between tests so one test's toggle/click
  // can't leak into the next (the store is a module-level singleton shared
  // across every `it()` in this file).
  useWorkspacesStore.setState({ collapsedLanes: {}, activePlanId: null })
})

// ── Grouping ─────────────────────────────────────────────────────────────────

describe('BoardView swimlanes — grouping by plan', () => {
  it('renders one band per plan, each containing only that plan\'s tasks', () => {
    const planA = makePlan({ id: 'plan-a', title: 'Plan A', state: 'running' })
    const planB = makePlan({ id: 'plan-b', title: 'Plan B', state: 'draft' })
    const taskA = makeTask({ id: 't-a', title: 'Task A', plan_id: 'plan-a' })
    const taskB = makeTask({ id: 't-b', title: 'Task B', plan_id: 'plan-b' })
    const taskLoose = makeTask({ id: 't-loose', title: 'Task Loose' })

    renderBoard({ plans: [planA, planB], tasks: [taskA, taskB, taskLoose] })

    const bandA = screen.getByTestId('swimlane-band-plan-a')
    expect(within(bandA).getByText('Task A')).toBeInTheDocument()
    expect(within(bandA).queryByText('Task B')).not.toBeInTheDocument()
    expect(within(bandA).queryByText('Task Loose')).not.toBeInTheDocument()

    const bandB = screen.getByTestId('swimlane-band-plan-b')
    expect(within(bandB).getByText('Task B')).toBeInTheDocument()
    expect(within(bandB).queryByText('Task A')).not.toBeInTheDocument()

    const loose = screen.getByTestId('swimlane-band-loose')
    expect(within(loose).getByText('Task Loose')).toBeInTheDocument()
    expect(within(loose).queryByText('Task A')).not.toBeInTheDocument()
  })

  it('renders the Loose tasks (no plan) band even with zero plans and zero loose tasks', () => {
    renderBoard({ plans: [], tasks: [] })
    expect(screen.getByTestId('swimlane-band-loose')).toBeInTheDocument()
    expect(screen.getByText('Loose tasks (no plan)')).toBeInTheDocument()
  })

  it('behaves like the pre-swimlane flat board when there are zero plans (every task is Loose)', () => {
    const task = makeTask({ id: 't1', title: 'Solo task', status: 'in_progress' })
    renderBoard({ plans: [], tasks: [task] })
    expect(screen.getByTestId('swimlane-band-loose')).toBeInTheDocument()
    expect(within(screen.getByTestId('swimlane-band-loose')).getByText('Solo task')).toBeInTheDocument()
    // No plan bands rendered at all.
    expect(screen.queryAllByTestId(/^swimlane-band-plan-/)).toHaveLength(0)
  })
})

// ── Orphaned plan_id (review-gate fix #1) ───────────────────────────────────
//
// A root task whose `plan_id` is truthy but matches NO plan in the loaded
// `plans` list (plansError → `plans` defaults to `[]`, a create-plan/tasks
// refetch race, or the post-Clear window before the plans query re-settles)
// must still render — routed into the Loose band — instead of landing in
// neither a plan band nor Loose and silently vanishing while the sticky
// per-status header count still includes it.

describe('BoardView swimlanes — orphaned plan_id tasks never vanish', () => {
  it('a task whose plan_id matches no loaded plan renders in the Loose band', () => {
    const orphan = makeTask({ id: 't-orphan', title: 'Orphan task', plan_id: 'plan-gone' })
    renderBoard({ plans: [], tasks: [orphan] })

    const loose = screen.getByTestId('swimlane-band-loose')
    expect(within(loose).getByText('Orphan task')).toBeInTheDocument()
    // No plan band exists for the dangling plan_id — there's nowhere else it could hide.
    expect(screen.queryAllByTestId(/^swimlane-band-plan-/)).toHaveLength(0)
  })

  it('an orphaned task lands in Loose even alongside a real plan band (not a phantom third home)', () => {
    const plan = makePlan({ id: 'plan-a', title: 'Plan A', state: 'running' })
    const member = makeTask({ id: 't-member', title: 'Member task', plan_id: 'plan-a' })
    const orphan = makeTask({ id: 't-orphan', title: 'Orphan task', plan_id: 'plan-gone' })
    renderBoard({ plans: [plan], tasks: [member, orphan] })

    const bandA = screen.getByTestId('swimlane-band-plan-a')
    expect(within(bandA).getByText('Member task')).toBeInTheDocument()
    expect(within(bandA).queryByText('Orphan task')).not.toBeInTheDocument()

    const loose = screen.getByTestId('swimlane-band-loose')
    expect(within(loose).getByText('Orphan task')).toBeInTheDocument()
  })

  it('the sticky per-status header count reflects the orphaned task, never exceeding the cards rendered', () => {
    const orphan = makeTask({ id: 't-orphan', title: 'Orphan task', plan_id: 'plan-gone', status: 'inbox' })
    renderBoard({ plans: [], tasks: [orphan] })

    // The task actually renders (in Loose)...
    expect(screen.getByText('Orphan task')).toBeInTheDocument()
    // ...and the Inbox column's sticky header count is 1 (the pre-fix bug
    // counted it from the raw task list while the card itself had vanished,
    // so the count could exceed what was actually on screen).
    const inboxLabel = screen.getByText('Inbox')
    const countBadge = inboxLabel.parentElement!.querySelector('span:nth-child(2)')
    expect(countBadge).toHaveTextContent('1')
  })

  it('simulates plansError (plans defaults to []) — every planned task still renders, grouped into Loose', () => {
    const t1 = makeTask({ id: 't1', title: 'Task One', plan_id: 'plan-a' })
    const t2 = makeTask({ id: 't2', title: 'Task Two', plan_id: 'plan-b' })
    // plansError → the caller's `plans` query defaults to `[]`, same as here.
    renderBoard({ plans: [], tasks: [t1, t2] })

    const loose = screen.getByTestId('swimlane-band-loose')
    expect(within(loose).getByText('Task One')).toBeInTheDocument()
    expect(within(loose).getByText('Task Two')).toBeInTheDocument()
  })
})

// ── Lane order ───────────────────────────────────────────────────────────────

describe('BoardView swimlanes — lane order', () => {
  it('orders bands running → approved → draft → done/failed, Loose last, regardless of input order', () => {
    const planDone = makePlan({ id: 'plan-done', title: 'Done plan', state: 'done' })
    const planDraft = makePlan({ id: 'plan-draft', title: 'Draft plan', state: 'draft' })
    const planRunning = makePlan({ id: 'plan-running', title: 'Running plan', state: 'running' })
    const planApproved = makePlan({ id: 'plan-approved', title: 'Approved plan', state: 'approved' })
    const planFailed = makePlan({ id: 'plan-failed', title: 'Failed plan', state: 'failed' })

    // Deliberately scrambled input order.
    renderBoard({
      plans: [planDone, planDraft, planFailed, planRunning, planApproved],
      tasks: [],
    })

    const testIds = screen.getAllByTestId(/^swimlane-band-/).map((el) => el.getAttribute('data-testid'))
    expect(testIds).toEqual([
      'swimlane-band-plan-running',
      'swimlane-band-plan-approved',
      'swimlane-band-plan-draft',
      // done/failed share a tier — stable sort keeps their relative INPUT order.
      'swimlane-band-plan-done',
      'swimlane-band-plan-failed',
      'swimlane-band-loose',
    ])
  })
})

// ── Collapse / expand ────────────────────────────────────────────────────────

describe('BoardView swimlanes — default collapse state', () => {
  it('default-collapses terminal (done/failed) plan lanes', () => {
    const planDone = makePlan({ id: 'plan-done', state: 'done' })
    renderBoard({ plans: [planDone], tasks: [] })
    const header = within(screen.getByTestId('plan-lane-header-plan-done'))
    expect(header.getByRole('button', { name: /expand/i })).toHaveAttribute('aria-expanded', 'false')
  })

  it.each(['draft', 'approved', 'running'] as const)('default-expands a %s plan lane', (state) => {
    const plan = makePlan({ id: 'plan-x', state })
    renderBoard({ plans: [plan], tasks: [] })
    const header = within(screen.getByTestId('plan-lane-header-plan-x'))
    expect(header.getByRole('button', { name: /collapse/i })).toHaveAttribute('aria-expanded', 'true')
  })

  it('the Loose lane defaults to expanded', () => {
    renderBoard({ plans: [], tasks: [makeTask({ plan_id: undefined })] })
    expect(screen.getByRole('button', { name: 'Collapse Loose tasks (no plan) lane' })).toHaveAttribute(
      'aria-expanded',
      'true',
    )
  })

  it('clicking the chevron collapses an expanded plan lane and hides its task cards', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ id: 'plan-x', title: 'Plan X', state: 'draft' })
    const task = makeTask({ id: 't-x', title: 'Task X', plan_id: 'plan-x' })
    renderBoard({ plans: [plan], tasks: [task] })

    expect(screen.getByText('Task X')).toBeInTheDocument()
    const toggle = within(screen.getByTestId('plan-lane-header-plan-x')).getByRole('button', { name: /collapse/i })
    await user.click(toggle)
    expect(screen.queryByText('Task X')).not.toBeInTheDocument()
  })

  it('clicking the chevron again re-expands the lane', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ id: 'plan-x', title: 'Plan X', state: 'draft' })
    const task = makeTask({ id: 't-x', title: 'Task X', plan_id: 'plan-x' })
    renderBoard({ plans: [plan], tasks: [task] })

    const header = () => within(screen.getByTestId('plan-lane-header-plan-x'))
    await user.click(header().getByRole('button', { name: /collapse/i }))
    expect(screen.queryByText('Task X')).not.toBeInTheDocument()
    await user.click(header().getByRole('button', { name: /expand/i }))
    expect(screen.getByText('Task X')).toBeInTheDocument()
  })

  it('clicking the Loose lane chevron collapses it and hides its task cards', async () => {
    const user = userEvent.setup()
    const task = makeTask({ id: 't-loose', title: 'Loose task' })
    renderBoard({ plans: [], tasks: [task] })
    expect(screen.getByText('Loose task')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Collapse Loose tasks (no plan) lane' }))
    expect(screen.queryByText('Loose task')).not.toBeInTheDocument()
  })

  it('a collapsed band still shows its one-line progress summary in the header', () => {
    const plan = makePlan({ id: 'plan-done', state: 'done' })
    const t1 = makeTask({ id: 't1', plan_id: 'plan-done', status: 'done' })
    const t2 = makeTask({ id: 't2', plan_id: 'plan-done', status: 'inbox' })
    renderBoard({ plans: [plan], tasks: [t1, t2] })

    const header = within(screen.getByTestId('plan-lane-header-plan-done'))
    expect(header.getByText('1/2')).toBeInTheDocument()
    // And its status-cell row is genuinely not rendered (collapsed) — scope
    // to this band specifically (the Loose lane below it is expanded by
    // default and has its own 7 "column" cells, which would otherwise make
    // a page-wide query find more than zero).
    const band = within(screen.getByTestId('swimlane-band-plan-done'))
    expect(band.queryByLabelText(/column$/)).not.toBeInTheDocument()
  })
})

// ── ⑂ view plan graph wiring ─────────────────────────────────────────────────

describe('BoardView swimlanes — ⑂ view plan graph', () => {
  it('disables ⑂ for a plan with fewer than 2 member tasks', () => {
    const plan = makePlan({ id: 'plan-x', state: 'draft' })
    const task = makeTask({ id: 't1', plan_id: 'plan-x' })
    renderBoard({ plans: [plan], tasks: [task] })
    const header = within(screen.getByTestId('plan-lane-header-plan-x'))
    expect(header.getByRole('button', { name: 'View plan graph' })).toBeDisabled()
  })

  it('enables ⑂ with ≥2 member tasks; clicking sets activePlanId and navigates to the Graph tab', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ id: 'plan-x', state: 'draft' })
    const t1 = makeTask({ id: 't1', plan_id: 'plan-x' })
    const t2 = makeTask({ id: 't2', plan_id: 'plan-x' })
    renderBoard({ plans: [plan], tasks: [t1, t2], workspaceId: 'ws-7' })

    const header = within(screen.getByTestId('plan-lane-header-plan-x'))
    const btn = header.getByRole('button', { name: 'View plan graph' })
    expect(btn).not.toBeDisabled()

    await user.click(btn)

    expect(useWorkspacesStore.getState().activePlanId).toBe('plan-x')
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/workspaces/$workspaceId/graph',
      params: { workspaceId: 'ws-7' },
    })
  })

  it('member-task eligibility is plan-wide, unaffected by the active tag filter', () => {
    const plan = makePlan({ id: 'plan-x', state: 'draft' })
    const t1 = makeTask({ id: 't1', plan_id: 'plan-x', tags: ['keep'] })
    const t2 = makeTask({ id: 't2', plan_id: 'plan-x', tags: ['drop'] })
    renderBoard({ plans: [plan], tasks: [t1, t2], activeTagFilter: 'keep' })

    // The tag filter drops t2's CARD from the band (only t1 has "keep")...
    const band = within(screen.getByTestId('swimlane-band-plan-x'))
    expect(band.getAllByText('Task')).toHaveLength(1)

    // ...but ⑂ and the progress counter still reflect BOTH member tasks —
    // plan-wide membership, not scoped to the current view filter.
    const header = within(screen.getByTestId('plan-lane-header-plan-x'))
    expect(header.getByRole('button', { name: 'View plan graph' })).not.toBeDisabled()
    expect(header.getByText('0/2')).toBeInTheDocument()
  })
})
