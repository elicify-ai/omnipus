/**
 * BoardView.drilldown.test.tsx
 *
 * Hierarchical Drill-Down board redesign — replaces the per-plan swimlane
 * bands (BoardView.swimlanes.test.tsx) with a single flat kanban.
 * `activePlanId` (shared with the Graph tab via workspacesStore) drives the
 * level:
 *   - null       → TOP LEVEL: plan cards + loose tasks share the 7 status
 *                   columns; a plan card's column is DERIVED from its
 *                   member tasks (`derivePlanColumn`, see
 *                   derivePlanColumn.test.ts for the exhaustive branch
 *                   coverage).
 *   - <plan id>  → DRILLED: that plan's own member tasks render as a normal
 *                   draggable kanban, behind a compact breadcrumb strip.
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

// Minimal stub so the ⋯ trigger's content renders without Radix portal
// internals (mirrors WorkspaceTasksTab.test.tsx's DropdownMenu stub).
vi.mock('@/components/ui/dropdown-menu', () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) =>
    asChild ? <>{children}</> : <div>{children}</div>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="plan-card-menu">{children}</div>
  ),
  DropdownMenuItem: ({
    children,
    onClick,
    disabled,
    className,
  }: {
    children: React.ReactNode
    onClick?: () => void
    disabled?: boolean
    className?: string
  }) => (
    <button type="button" onClick={onClick} disabled={disabled} className={className}>
      {children}
    </button>
  ),
  DropdownMenuSeparator: () => <hr />,
}))

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
        activePlanId={null}
        activeTagFilter={null}
        altitude="top-level"
        onTaskClick={vi.fn()}
        onTaskMove={vi.fn()}
        onMoveRejected={vi.fn()}
        {...props}
      />
    </QueryClientProvider>,
  )
}

function statusColumn(label: string) {
  return within(screen.getByRole('group', { name: `${label} column` }))
}

beforeEach(() => {
  mockNavigate.mockReset()
  // Real Zustand store — reset between tests (module-level singleton).
  useWorkspacesStore.setState({ activePlanId: null })
})

// ── Top level: plans + loose tasks share the 7 columns ──────────────────────

describe('BoardView top level — plans + loose tasks share the 7 columns', () => {
  it('places a loose task (no plan_id) in its own real status column', () => {
    const loose = makeTask({ id: 't-loose', title: 'Loose task', status: 'next' })
    renderBoard({ tasks: [loose] })
    expect(statusColumn('Next').getByText('Loose task')).toBeInTheDocument()
  })

  it('places an orphaned-plan_id task (no loaded plan matches) in its own status column too', () => {
    const orphan = makeTask({ id: 't-orphan', title: 'Orphan task', plan_id: 'plan-gone', status: 'inbox' })
    renderBoard({ plans: [], tasks: [orphan] })
    expect(statusColumn('Inbox').getByText('Orphan task')).toBeInTheDocument()
  })

  it('places a plan card in the column derived from its member tasks, not as individual task cards', () => {
    const plan = makePlan({ id: 'plan-a', title: 'Plan A' })
    const t1 = makeTask({ id: 't1', title: 'Member 1', plan_id: 'plan-a', status: 'in_progress' })
    const t2 = makeTask({ id: 't2', title: 'Member 2', plan_id: 'plan-a', status: 'next' })
    renderBoard({ plans: [plan], tasks: [t1, t2] })

    expect(statusColumn('In Progress').getByTestId('plan-card-plan-a')).toBeInTheDocument()
    // Member tasks are NOT rendered as standalone top-level cards.
    expect(screen.queryByText('Member 1')).not.toBeInTheDocument()
    expect(screen.queryByText('Member 2')).not.toBeInTheDocument()
  })

  it('a plan with all-done children lands in Done', () => {
    const plan = makePlan({ id: 'plan-done', title: 'Done plan' })
    const t1 = makeTask({ id: 't1', plan_id: 'plan-done', status: 'done' })
    const t2 = makeTask({ id: 't2', plan_id: 'plan-done', status: 'done' })
    renderBoard({ plans: [plan], tasks: [t1, t2] })
    expect(statusColumn('Done').getByTestId('plan-card-plan-done')).toBeInTheDocument()
  })

  it('a plan with zero member tasks lands in Inbox', () => {
    const plan = makePlan({ id: 'plan-empty', title: 'Empty plan' })
    renderBoard({ plans: [plan], tasks: [] })
    expect(statusColumn('Inbox').getByTestId('plan-card-plan-empty')).toBeInTheDocument()
  })

  it("a plan card's derived column is unaffected by the active tag filter", () => {
    const plan = makePlan({ id: 'plan-a', title: 'Plan A' })
    const t1 = makeTask({ id: 't1', plan_id: 'plan-a', status: 'in_progress', tags: ['keep'] })
    const t2 = makeTask({ id: 't2', plan_id: 'plan-a', status: 'in_progress', tags: ['drop'] })
    renderBoard({ plans: [plan], tasks: [t1, t2], activeTagFilter: 'keep' })
    // Both members are in_progress plan-wide — the card still lands there
    // even though the tag filter would only keep t1's own card (member
    // tasks never render as standalone cards anyway).
    expect(statusColumn('In Progress').getByTestId('plan-card-plan-a')).toBeInTheDocument()
  })

  it('plan cards are not draggable (no dnd-kit draggable role)', () => {
    const plan = makePlan({ id: 'plan-a', title: 'Plan A' })
    renderBoard({ plans: [plan], tasks: [] })
    const card = screen.getByTestId('plan-card-plan-a')
    expect(card).not.toHaveAttribute('aria-roledescription', 'draggable task')
  })

  it('the sticky per-status header counts plan cards and loose tasks together', () => {
    const plan = makePlan({ id: 'plan-a', title: 'Plan A' })
    const member = makeTask({ id: 't1', plan_id: 'plan-a', status: 'next' })
    const loose = makeTask({ id: 't-loose', title: 'Loose', status: 'next' })
    renderBoard({ plans: [plan], tasks: [member, loose] })
    const nextLabel = screen.getByText('Next')
    const countBadge = nextLabel.parentElement!.querySelector('span:nth-child(2)')
    // 1 plan card (member `t1` is 'next' → derived column 'next') + 1 loose task.
    expect(countBadge).toHaveTextContent('2')
  })
})

// ── Drilling into a plan ─────────────────────────────────────────────────────

describe('BoardView — drilling into a plan', () => {
  it('activePlanId set to a resolving plan shows only that plan\'s tasks, behind a breadcrumb', () => {
    const planA = makePlan({ id: 'plan-a', title: 'Plan A' })
    const planB = makePlan({ id: 'plan-b', title: 'Plan B' })
    const member = makeTask({ id: 't-member', title: 'Member task', plan_id: 'plan-a', status: 'next' })
    const other = makeTask({ id: 't-other', title: 'Other plan task', plan_id: 'plan-b', status: 'next' })
    const loose = makeTask({ id: 't-loose', title: 'Loose task', status: 'next' })

    renderBoard({
      plans: [planA, planB],
      tasks: [member, other, loose],
      activePlanId: 'plan-a',
    })

    expect(screen.getByTestId('plan-breadcrumb')).toHaveTextContent('Plan A')
    expect(screen.getByText('Member task')).toBeInTheDocument()
    expect(screen.queryByText('Other plan task')).not.toBeInTheDocument()
    expect(screen.queryByText('Loose task')).not.toBeInTheDocument()
    // No plan cards render at the drilled level.
    expect(screen.queryByTestId('plan-card-plan-a')).not.toBeInTheDocument()
  })

  it('an unresolved activePlanId (plansError / just-cleared plan) falls back to the top level', () => {
    const loose = makeTask({ id: 't-loose', title: 'Loose task', status: 'next' })
    renderBoard({ plans: [], tasks: [loose], activePlanId: 'plan-gone' })
    expect(screen.queryByTestId('plan-breadcrumb')).not.toBeInTheDocument()
    expect(screen.getByText('Loose task')).toBeInTheDocument()
  })

  it('the breadcrumb "Board" button clears activePlanId back to the top level', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ id: 'plan-a', title: 'Plan A' })
    renderBoard({ plans: [plan], tasks: [], activePlanId: 'plan-a' })
    await user.click(screen.getByRole('button', { name: 'Board' }))
    expect(useWorkspacesStore.getState().activePlanId).toBeNull()
  })

  it("clicking a plan card's drill-in control sets activePlanId without navigating", async () => {
    const user = userEvent.setup()
    const plan = makePlan({ id: 'plan-a', title: 'Plan A' })
    renderBoard({ plans: [plan], tasks: [] })
    await user.click(screen.getByRole('button', { name: 'Open plan board' }))
    expect(useWorkspacesStore.getState().activePlanId).toBe('plan-a')
    expect(mockNavigate).not.toHaveBeenCalled()
  })

  it("clicking a plan card's graph control sets activePlanId and navigates to the Graph tab", async () => {
    const user = userEvent.setup()
    const plan = makePlan({ id: 'plan-a', title: 'Plan A' })
    const t1 = makeTask({ id: 't1', plan_id: 'plan-a' })
    const t2 = makeTask({ id: 't2', plan_id: 'plan-a' })
    renderBoard({ plans: [plan], tasks: [t1, t2], workspaceId: 'ws-7' })

    const btn = screen.getByRole('button', { name: 'View plan graph' })
    expect(btn).not.toBeDisabled()
    await user.click(btn)

    expect(useWorkspacesStore.getState().activePlanId).toBe('plan-a')
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/workspaces/$workspaceId/graph',
      params: { workspaceId: 'ws-7' },
    })
  })

  it('disables the graph control below 2 member tasks', () => {
    const plan = makePlan({ id: 'plan-a', title: 'Plan A' })
    const t1 = makeTask({ id: 't1', plan_id: 'plan-a' })
    renderBoard({ plans: [plan], tasks: [t1] })
    expect(screen.getByRole('button', { name: 'View plan graph' })).toBeDisabled()
  })

  it('also disables the breadcrumb graph control below 2 member tasks while drilled in', () => {
    const plan = makePlan({ id: 'plan-a', title: 'Plan A' })
    const t1 = makeTask({ id: 't1', plan_id: 'plan-a' })
    renderBoard({ plans: [plan], tasks: [t1], activePlanId: 'plan-a' })
    expect(within(screen.getByTestId('plan-breadcrumb')).getByRole('button', { name: 'View plan graph' })).toBeDisabled()
  })

  it('clicking a plan card body opens the edit slide-over via onEditPlan', async () => {
    const user = userEvent.setup()
    const onEditPlan = vi.fn()
    const plan = makePlan({ id: 'plan-a', title: 'Plan A' })
    renderBoard({ plans: [plan], tasks: [], onEditPlan })
    await user.click(screen.getByTestId('plan-card-plan-a'))
    expect(onEditPlan).toHaveBeenCalledWith(plan)
  })

  it("clicking a plan card's ⋯ menu action does not also trigger onEditPlan (stopPropagation)", async () => {
    const user = userEvent.setup()
    const onEditPlan = vi.fn()
    const onApprovePlan = vi.fn()
    const plan = makePlan({ id: 'plan-a', title: 'Plan A', state: 'draft' })
    renderBoard({ plans: [plan], tasks: [], onEditPlan, onApprovePlan })
    await user.click(screen.getByRole('button', { name: 'Approve' }))
    expect(onApprovePlan).toHaveBeenCalledWith(plan)
    expect(onEditPlan).not.toHaveBeenCalled()
  })

  it("clicking a plan card's drill-in control does not also trigger onEditPlan (stopPropagation)", async () => {
    const user = userEvent.setup()
    const onEditPlan = vi.fn()
    const plan = makePlan({ id: 'plan-a', title: 'Plan A' })
    renderBoard({ plans: [plan], tasks: [], onEditPlan })
    await user.click(screen.getByRole('button', { name: 'Open plan board' }))
    expect(onEditPlan).not.toHaveBeenCalled()
  })
})
