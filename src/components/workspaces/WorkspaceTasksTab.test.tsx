/**
 * WorkspaceTasksTab.test.tsx
 *
 * ADR-051 — the combined "Tasks" screen. Covers:
 *   - The [Board] [List] [Graph] view switcher renders and defaults to Board.
 *   - The dynamic heading ("Tasks" / "{plan.title} — tasks", with an
 *     "· Agent: {name}" suffix when an agent filter is active).
 *   - "New task" is present and opens the create slide-over.
 *   - This screen's own plan-mutation WIRING (Clear/Edit/New Plan callbacks
 *     passed into PlansFilterBand fire the right mutation). Execute/Stop/Play
 *     are owned entirely by the self-contained `PlanActionButton` now (ADR-052
 *     §6.8, Gate-2 finding #4) — this screen no longer wires onApprovePlan/
 *     onStopPlan into PlansFilterBand, so there is nothing to test here for
 *     those actions; see PlanActionButton.test.tsx.
 *   - The altitude toggle only shows in Board view.
 *
 * PlansFilterBand and taskFilters.ts are owned by a parallel agent and may
 * not exist yet. `PlansFilterBand` is mocked to a plain stub that exposes
 * one button per callback prop — this file tests WorkspaceTasksTab's own
 * wiring (which mutation each prop triggers), not PlansFilterBand's real
 * internal DOM/markup, which is out of this file's ownership and is covered
 * by the band's own test file.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Plan, Task, Agent } from '@/lib/api'
import { WorkspaceTasksTab } from './WorkspaceTasksTab'
import { useWorkspacesStore } from '@/store/workspacesStore'

// jsdom doesn't implement scrollIntoView; Radix Select calls it when opening
// the listbox (repo-wide convention — see ChannelConfigPanel.test.tsx,
// CreateTaskSlideOver.test.tsx, NewWorkspaceSlideOver.test.tsx, etc.).
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

// ── Mocks ────────────────────────────────────────────────────────────────────

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => mockNavigate,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchTasks: vi.fn(),
    fetchPlans: vi.fn(),
    fetchAgents: vi.fn(),
    fetchWorkspaceDelegation: vi.fn().mockRejectedValue(new Error('not mocked')),
    deletePlan: vi.fn(),
  }
})

import { fetchTasks, fetchPlans, fetchAgents, deletePlan } from '@/lib/api'

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

// Stub DropdownMenu (Radix pointer/portal internals don't drive in jsdom) so
// the Agent + Tag filter menu items render inline and are directly clickable —
// this file tests WorkspaceTasksTab's filter WIRING, not Radix's open/close.
vi.mock('@/components/ui/dropdown-menu', () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) =>
    asChild ? <>{children}</> : <div>{children}</div>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuItem: ({ children, onClick, className }: { children: React.ReactNode; onClick?: () => void; className?: string }) => (
    <button type="button" role="menuitem" onClick={onClick} className={className}>{children}</button>
  ),
  DropdownMenuCheckboxItem: ({ children, checked, onCheckedChange, className }: { children: React.ReactNode; checked?: boolean; onCheckedChange?: (v: boolean) => void; className?: string }) => (
    <button type="button" role="menuitemcheckbox" aria-checked={checked} onClick={() => onCheckedChange?.(!checked)} className={className}>{children}</button>
  ),
  DropdownMenuSeparator: () => <hr />,
}))

// Stub PlansFilterBand — owned by a parallel agent (may not exist on disk
// yet). Exposes one plain button per callback prop so this file can verify
// WorkspaceTasksTab's OWN wiring without depending on the real component's
// internal markup. No onApprovePlan/onStopPlan stub buttons (Gate-2 finding
// #4) — WorkspaceTasksTab no longer passes those props; Execute/Stop/Play
// wiring is owned by the real (unmocked in its own test file) PlanActionButton.
vi.mock('./PlansFilterBand', () => ({
  PlansFilterBand: (props: {
    plans: Plan[]
    onSelectPlan: (id: string | null) => void
    onNewPlan: () => void
    onEditPlan: (plan: Plan) => void
    onClearPlan: (plan: Plan) => void
  }) => (
    <div data-testid="plans-filter-band-stub">
      <button type="button" onClick={() => props.onSelectPlan(null)}>
        select-all
      </button>
      <button type="button" onClick={props.onNewPlan}>
        stub-new-plan
      </button>
      {props.plans.map((plan) => (
        <div key={plan.id}>
          <button type="button" onClick={() => props.onSelectPlan(plan.id)}>
            select-{plan.id}
          </button>
          <button type="button" onClick={() => props.onEditPlan(plan)}>
            edit-{plan.id}
          </button>
          <button type="button" onClick={() => props.onClearPlan(plan)}>
            clear-{plan.id}
          </button>
        </div>
      ))}
    </div>
  ),
}))

// Sentinel stub for the Graph tab — real WorkspaceGraphTab has its own data
// fetching/canvas concerns (covered by its own test file); this file only
// needs to prove WorkspaceTasksTab actually swaps it in on view=graph and
// gates the Board-only filter controls around it (the W1 fix).
vi.mock('./WorkspaceGraphTab', () => ({
  WorkspaceGraphTab: () => <div data-testid="graph-tab-sentinel">Graph Sentinel</div>,
}))

// ── Fixtures ─────────────────────────────────────────────────────────────────

function makePlan(overrides: Partial<Plan> = {}): Plan {
  return {
    id: 'plan-a',
    workspace_id: 'ws-1',
    title: 'Plan A',
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

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 't1',
    title: 'Write report',
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

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: 'ray',
    name: 'Ray',
    type: 'core',
    locked: true,
    needs_model: false,
    status: 'active',
    soul: '',
    color: '#3B82F6',
    icon: 'MagnifyingGlass',
    timeout_seconds: 300,
    max_tool_iterations: 50,
    // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
    memory_enabled: true,
    ...overrides,
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderTab(workspaceId = 'ws-1') {
  return render(
    <QueryClientProvider client={makeClient()}>
      <WorkspaceTasksTab workspaceId={workspaceId} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockNavigate.mockReset()
  mockAddToast.mockReset()
  // Real Zustand store — reset between tests (module-level singleton).
  useWorkspacesStore.setState({ activeTags: [], boardAltitude: 'top-level', activePlanId: null })
  vi.mocked(fetchTasks).mockReset().mockResolvedValue([])
  vi.mocked(fetchPlans).mockReset().mockResolvedValue([])
  vi.mocked(fetchAgents).mockReset().mockResolvedValue([])
  vi.mocked(deletePlan).mockReset()
})

// ── View switcher ────────────────────────────────────────────────────────────

describe('WorkspaceTasksTab — view switcher', () => {
  it('renders the Board/List/Graph segmented control, defaulting to Board', async () => {
    renderTab()
    await screen.findByRole('button', { name: /new task/i })

    const board = screen.getByTestId('tasks-view-board')
    const list = screen.getByTestId('tasks-view-list')
    const graph = screen.getByTestId('tasks-view-graph')
    expect(board).toBeInTheDocument()
    expect(list).toBeInTheDocument()
    expect(graph).toBeInTheDocument()
    expect(board).toHaveAttribute('aria-checked', 'true')
    expect(list).toHaveAttribute('aria-checked', 'false')
    expect(graph).toHaveAttribute('aria-checked', 'false')
  })

  it('switching to List renders the ListView table and hides the Board-only Agent/Tag filters', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchTasks).mockResolvedValue([makeTask()])
    renderTab()
    // Board-only toolbar filters are present in Board view.
    expect(await screen.findByTestId('tasks-agent-filter')).toBeInTheDocument()
    expect(screen.getByTestId('tasks-tag-filter')).toBeInTheDocument()

    await user.click(screen.getByTestId('tasks-view-list'))
    expect(await screen.findByText('Write report')).toBeInTheDocument()
    // The List table's own column headers only render in list mode.
    expect(screen.getByText('Updated ↓')).toBeInTheDocument()
    // The List owns per-column filtering, so the toolbar Agent/Tag filters are
    // Board-only and must be gone here (mirror of the Graph gating below).
    expect(screen.queryByTestId('tasks-agent-filter')).toBeNull()
    expect(screen.queryByTestId('tasks-tag-filter')).toBeNull()
  })

})

// ── Dynamic heading ──────────────────────────────────────────────────────────

describe('WorkspaceTasksTab — dynamic heading', () => {
  it('reads "Team Task Backlog" when no plan and no owner filter is active', async () => {
    renderTab()
    const heading = await screen.findByTestId('tasks-heading')
    expect(heading.textContent).toContain('Team Task Backlog')
    expect(heading.textContent).not.toContain('Agent:')
  })

  it('reads "{plan.title} — tasks" when a plan is selected (via the band stub)', async () => {
    const plan = makePlan({ title: 'Payments revamp' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])

    const user = userEvent.setup()
    renderTab()
    await user.click(await screen.findByRole('button', { name: 'select-plan-a' }))

    const heading = await screen.findByTestId('tasks-heading')
    await waitFor(() => expect(heading.textContent).toContain('Payments revamp — tasks'))
  })

  it('appends "· Agent: {name}" once an agent filter is active', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([makeAgent({ id: 'ray', name: 'Ray' })])
    renderTab()

    // Agent filter — the chat-composer-style icon DropdownMenu (mocked here so
    // its items render inline). Click the agent's menu item by name.
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Ray' }))

    const heading = await screen.findByTestId('tasks-heading')
    await waitFor(() => expect(heading.textContent).toContain('Agent: Ray'))
  })
})

// ── New task ─────────────────────────────────────────────────────────────────

describe('WorkspaceTasksTab — New task', () => {
  it('renders a working New task button that opens the create slide-over', async () => {
    const user = userEvent.setup()
    renderTab()
    const button = await screen.findByRole('button', { name: /new task/i })
    await user.click(button)
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
  })
})

// ── S3 UAT finding — quick-create inside a plan-scoped board silently lands
// the task UNPLANNED (`plan_id: null`, intended — CreateTaskSlideOver always
// receives `planId={null}` here, "no filter-scoped quick-create"). With the
// heading reading "{plan} — tasks", the board then immediately reads "No
// tasks match the current filter" and the new task appears to vanish. This
// is not a behavior change — just telling the user up front, at the point
// they're about to act, via an always-visible (non-tooltip) hint.
describe('WorkspaceTasksTab — unplanned quick-create hint (S3 UAT)', () => {
  it('shows no hint when no plan filter is active', async () => {
    renderTab()
    await screen.findByRole('button', { name: /new task/i })
    expect(screen.queryByTestId('new-task-unplanned-hint')).not.toBeInTheDocument()
  })

  it('shows a visible hint naming the recovery path once a plan filter is active', async () => {
    const plan = makePlan({ title: 'Payments revamp' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    const user = userEvent.setup()
    renderTab()
    await user.click(await screen.findByRole('button', { name: 'select-plan-a' }))

    const hint = await screen.findByTestId('new-task-unplanned-hint')
    // Must be genuinely visible — not merely present in the DOM with content
    // but hidden via `hidden`/`display:none` (which `findByTestId` +
    // `.textContent` alone would not catch). There is no companion "chip"
    // for this hint (unlike the plan-phase explanation elsewhere), so there
    // is no separate title-tooltip element to check here.
    expect(hint).toBeVisible()
    expect(hint.textContent).toMatch(/unplanned/i)
    expect(hint.textContent).toMatch(/move to plan/i)
  })

  it('the hint disappears again once the plan filter is cleared', async () => {
    const plan = makePlan({ title: 'Payments revamp' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    const user = userEvent.setup()
    renderTab()
    await user.click(await screen.findByRole('button', { name: 'select-plan-a' }))
    await screen.findByTestId('new-task-unplanned-hint')

    await user.click(screen.getByRole('button', { name: 'select-all' }))
    await waitFor(() => expect(screen.queryByTestId('new-task-unplanned-hint')).not.toBeInTheDocument())
  })
})

// ── Plan mutation wiring (through the callback props passed to the band) ────
//
// Only Clear and Edit remain screen-owned — Execute/Stop/Play moved entirely
// into the self-contained `PlanActionButton` (ADR-052 §6.8, Gate-2 finding
// #4). That per-state button matrix (including the 400 task_errors-surfacing
// behavior Execute used to get from this screen's now-deleted
// approvePlanMutation) is covered by PlanActionButton.test.tsx, not here.

describe('WorkspaceTasksTab — plan mutation wiring', () => {
  it('the band stub\'s clear button calls deletePlan with the plan id and toasts on success', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ state: 'draft' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    vi.mocked(deletePlan).mockResolvedValue(undefined)

    renderTab()
    await user.click(await screen.findByRole('button', { name: 'clear-plan-a' }))

    await waitFor(() => expect(deletePlan).toHaveBeenCalledWith('plan-a'))
    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success', message: 'Plan cleared' }),
      ),
    )
  })

  it('the band stub\'s edit button opens the CreatePlanSlideOver in edit mode', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ title: 'Plan A' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])

    renderTab()
    await user.click(await screen.findByRole('button', { name: 'edit-plan-a' }))

    // CreatePlanSlideOver renders as a Sheet (role=dialog) once opened.
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
  })
})

// ── End-to-end filter behavior (the board actually filters) ────────────────
//
// Everything above verifies WIRING through the PlansFilterBand stub. These
// tests verify the actual PAYLOAD: selecting a plan/owner narrows what
// renders on the real (unmocked) Board below, proving `filteredTasks`
// (WorkspaceTasksTab.tsx) is genuinely applied — not just computed and
// ignored.

describe('WorkspaceTasksTab — end-to-end filter behavior (Board actually filters)', () => {
  it('selecting a plan tile filters the board down to that plan\'s member tasks', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ id: 'plan-a', title: 'Plan A' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    vi.mocked(fetchTasks).mockResolvedValue([
      makeTask({ id: 't-member', title: 'Member task', plan_id: 'plan-a' }),
      makeTask({ id: 't-loose', title: 'Loose task' }), // no plan_id — plan-less
    ])

    renderTab()
    // Both tasks render on the unfiltered board first — proves the board
    // really renders real cards before we narrow it.
    expect(await screen.findByText('Member task')).toBeInTheDocument()
    expect(screen.getByText('Loose task')).toBeInTheDocument()

    await user.click(await screen.findByRole('button', { name: 'select-plan-a' }))

    // Selecting a plan tile also switches the view to Graph (operator UX
    // fix — see "plan-select switches to Graph view" below), which swaps
    // Board out entirely. Switch back to Board to assert the actual
    // `filteredTasks` payload this test verifies.
    await user.click(screen.getByTestId('tasks-view-board'))

    // The plan-less task must actually disappear — not just the heading
    // changing while the board silently keeps rendering everything.
    await waitFor(() => expect(screen.queryByText('Loose task')).toBeNull())
    expect(screen.getByText('Member task')).toBeInTheDocument()
  })

  it('selecting an owner filters the board down to that owner\'s tasks', async () => {
    vi.mocked(fetchAgents).mockResolvedValue([
      makeAgent({ id: 'ray', name: 'Ray' }),
      makeAgent({ id: 'jim', name: 'Jim' }),
    ])
    vi.mocked(fetchTasks).mockResolvedValue([
      makeTask({ id: 't-ray', title: "Ray's task", agent_id: 'ray' }),
      makeTask({ id: 't-jim', title: "Jim's task", agent_id: 'jim' }),
    ])

    renderTab()
    expect(await screen.findByText("Ray's task")).toBeInTheDocument()
    expect(screen.getByText("Jim's task")).toBeInTheDocument()

    fireEvent.click(await screen.findByRole('menuitem', { name: 'Ray' }))

    await waitFor(() => expect(screen.queryByText("Jim's task")).toBeNull())
    expect(screen.getByText("Ray's task")).toBeInTheDocument()
  })
})

// ── Stale-filter reset (SF2 / code-reviewer fix) ────────────────────────────

describe('WorkspaceTasksTab — stale-filter reset', () => {
  it('nulls a stale activePlanId once `plans` loads without it — board is not stuck empty', async () => {
    // Simulate the scenario the reset effect guards against: activePlanId
    // already points at a plan id (e.g. leftover from a previous session, or
    // a plan tile's ⋯ → Clear that just deleted its own plan) that does NOT
    // exist in the freshly-loaded `plans` list.
    useWorkspacesStore.setState({ activePlanId: 'stale-plan-id' })
    vi.mocked(fetchTasks).mockResolvedValue([makeTask({ id: 't1', title: 'Surviving task' })])
    // A non-empty, genuinely different plans list — required so the effect's
    // `.length` guard (never fires before the first fetch resolves) actually
    // clears, exercising the real reset branch rather than the "haven't
    // loaded yet" no-op branch.
    vi.mocked(fetchPlans).mockResolvedValue([makePlan({ id: 'other-plan', title: 'Other plan' })])

    renderTab()

    await waitFor(() => expect(useWorkspacesStore.getState().activePlanId).toBeNull())

    const heading = await screen.findByTestId('tasks-heading')
    expect(heading.textContent?.trim()).toBe('Team Task Backlog')
    // The board itself must not be left stuck empty by the stale id.
    expect(screen.getByText('Surviving task')).toBeInTheDocument()
  })
})

// ── Graph view switch (W1 gating fix) ───────────────────────────────────────

describe('WorkspaceTasksTab — Graph view switch', () => {
  it('switching to Graph renders the Graph tab and hides the owner filter + tag bar', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchTasks).mockResolvedValue([makeTask({ id: 't1', tags: ['urgent'] })])

    renderTab()
    // Board-only filter controls are present in Board view.
    expect(await screen.findByTestId('tasks-tag-filter')).toBeInTheDocument()
    expect(screen.getByTestId('tasks-agent-filter')).toBeInTheDocument()

    const boardButton = screen.getByTestId('tasks-view-board')
    const graphButton = screen.getByTestId('tasks-view-graph')
    await user.click(graphButton)

    expect(await screen.findByTestId('graph-tab-sentinel')).toBeInTheDocument()
    expect(graphButton).toHaveAttribute('aria-checked', 'true')
    expect(boardButton).toHaveAttribute('aria-checked', 'false')

    // Board-only filter controls are gone in Graph view.
    expect(screen.queryByTestId('tasks-agent-filter')).toBeNull()
    expect(screen.queryByTestId('tasks-tag-filter')).toBeNull()
  })
})

// ── Plan-select switches to Graph view (operator UX fix) ───────────────────
//
// Operator-reported: clicking a plan tile only scoped the filter; the
// section below should ALSO switch to the Graph view. Selecting a plan tile
// (a real, non-null id reaching onSelectPlan) switches `view` to 'graph'.
// DESELECTING (onSelectPlan(null) — re-clicking the active tile, or the "All
// tasks" tile) must NOT force a view change. Manual view switching keeps
// working afterwards — picking a different plan tile switches to Graph
// again, by design.

describe('WorkspaceTasksTab — plan-select switches to Graph view (operator UX fix)', () => {
  it('selecting a plan tile switches the view to Graph', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ id: 'plan-a', title: 'Plan A' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    renderTab()

    // Starts on Board (default).
    expect(await screen.findByTestId('tasks-view-board')).toHaveAttribute('aria-checked', 'true')

    await user.click(await screen.findByRole('button', { name: 'select-plan-a' }))

    await waitFor(() =>
      expect(screen.getByTestId('tasks-view-graph')).toHaveAttribute('aria-checked', 'true'),
    )
    expect(await screen.findByTestId('graph-tab-sentinel')).toBeInTheDocument()
  })

  it('deselecting a plan (the "All tasks" tile / re-clicking the active tile, onSelectPlan(null)) does NOT force a view change', async () => {
    const user = userEvent.setup()
    const plan = makePlan({ id: 'plan-a', title: 'Plan A' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    renderTab()

    await user.click(await screen.findByRole('button', { name: 'select-plan-a' }))
    await waitFor(() =>
      expect(screen.getByTestId('tasks-view-graph')).toHaveAttribute('aria-checked', 'true'),
    )

    // Manually switch to List first — proves the deselect step below isn't
    // just "happening to already be on Graph".
    await user.click(screen.getByTestId('tasks-view-list'))
    expect(screen.getByTestId('tasks-view-list')).toHaveAttribute('aria-checked', 'true')

    // Deselect via the stub's "select-all" button — mirrors both the real
    // "All tasks" tile and re-clicking the already-active plan tile, both of
    // which call onSelectPlan(null) (see PlansFilterBand.test.tsx).
    await user.click(screen.getByRole('button', { name: 'select-all' }))

    // View must stay on List — a deselect never forces a view change.
    expect(screen.getByTestId('tasks-view-list')).toHaveAttribute('aria-checked', 'true')
  })

  it('selecting a different plan tile switches to Graph again, even after a manual switch away', async () => {
    const user = userEvent.setup()
    const planA = makePlan({ id: 'plan-a', title: 'Plan A' })
    const planB = makePlan({ id: 'plan-b', title: 'Plan B' })
    vi.mocked(fetchPlans).mockResolvedValue([planA, planB])
    renderTab()

    await user.click(await screen.findByRole('button', { name: 'select-plan-a' }))
    await waitFor(() =>
      expect(screen.getByTestId('tasks-view-graph')).toHaveAttribute('aria-checked', 'true'),
    )

    // Manually switch back to Board.
    await user.click(screen.getByTestId('tasks-view-board'))
    expect(screen.getByTestId('tasks-view-board')).toHaveAttribute('aria-checked', 'true')

    // Selecting a DIFFERENT plan tile switches to Graph again — expected,
    // per the ticket ("manual view switching afterwards must keep working").
    await user.click(await screen.findByRole('button', { name: 'select-plan-b' }))
    await waitFor(() =>
      expect(screen.getByTestId('tasks-view-graph')).toHaveAttribute('aria-checked', 'true'),
    )
  })

  // PlansFilterBand.test.tsx already proves the REAL band's pencil/⋯/▶/■
  // controls never call onSelectPlan at all — they're structural siblings of
  // the select button, isolated via stopPropagation/Radix portals. These two
  // prove WorkspaceTasksTab's OWN wiring doesn't separately trip the view
  // switch from the onEditPlan/onClearPlan callbacks either (both are wired
  // independently of handleSelectPlan). Split into two tests — not
  // sequential clicks in one — because the Edit slide-over stays open
  // (Radix marks the rest of the page `aria-hidden` while it is), which
  // would make the Clear button inaccessible for a follow-up click.
  it("the plan tile's Edit control never triggers the view change", async () => {
    const user = userEvent.setup()
    const plan = makePlan({ id: 'plan-a', title: 'Plan A' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    renderTab()

    expect(await screen.findByTestId('tasks-view-board')).toHaveAttribute('aria-checked', 'true')

    await user.click(await screen.findByRole('button', { name: 'edit-plan-a' }))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    expect(screen.getByTestId('tasks-view-board')).toHaveAttribute('aria-checked', 'true')
  })

  it("the plan tile's Clear control never triggers the view change", async () => {
    const user = userEvent.setup()
    const plan = makePlan({ id: 'plan-a', title: 'Plan A' })
    vi.mocked(fetchPlans).mockResolvedValue([plan])
    vi.mocked(deletePlan).mockResolvedValue(undefined)
    renderTab()

    expect(await screen.findByTestId('tasks-view-board')).toHaveAttribute('aria-checked', 'true')

    await user.click(await screen.findByRole('button', { name: 'clear-plan-a' }))
    await waitFor(() => expect(deletePlan).toHaveBeenCalledWith('plan-a'))
    expect(screen.getByTestId('tasks-view-board')).toHaveAttribute('aria-checked', 'true')
  })
})
