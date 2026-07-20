/**
 * PlansFilterBand.test.tsx — ADR-051 D2/D3 (Plans-as-Filter band).
 *
 * Covers:
 *   1. The leading "All tasks" tile selects (onSelectPlan(null)) and reflects
 *      `selectedPlanId === null` as its selected/aria-pressed state.
 *   2. A plan tile's body click selects that plan (onSelectPlan(id)).
 *   3. Re-clicking the ALREADY-active plan tile clears the filter
 *      (onSelectPlan(null)) — toggle-off.
 *   4. The pencil (edit) button fires onEditPlan and NEVER onSelectPlan.
 *   5. The ⋯ menu's Approve/Stop/Clear fire their own callbacks and NEVER
 *      onSelectPlan, including through the portalled confirm dialogs.
 *   6. Selected state (ring + aria-pressed) reflects `selectedPlanId`.
 *   7. ＋ New Plan fires onNewPlan.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PlansFilterBand } from './PlansFilterBand'
import type { Agent, Plan, Task } from '@/lib/api'

// Minimal stub so the ⋯ trigger renders its content without Radix portal
// internals (mirrors PlanCard.test.tsx's dropdown stub).
vi.mock('@/components/ui/dropdown-menu', () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) =>
    asChild ? <>{children}</> : <div>{children}</div>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="plan-tile-menu">{children}</div>
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

function makePlan(overrides: Partial<Plan> = {}): Plan {
  return {
    id: 'plan-1',
    workspace_id: 'ws-1',
    title: 'Launch',
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

const agents: Agent[] = [
  { id: 'jim', name: 'Jim', type: 'core', locked: true, status: 'active', soul: '', timeout_seconds: 300, max_tool_iterations: 50 },
]

function renderBand(overrides: Partial<React.ComponentProps<typeof PlansFilterBand>> = {}) {
  const onSelectPlan = vi.fn()
  const onNewPlan = vi.fn()
  const onEditPlan = vi.fn()
  const onApprovePlan = vi.fn()
  const onStopPlan = vi.fn()
  const onClearPlan = vi.fn()
  const props = {
    plans: [makePlan()],
    tasks: [] as Task[],
    agents,
    selectedPlanId: null,
    onSelectPlan,
    onNewPlan,
    onEditPlan,
    onApprovePlan,
    onStopPlan,
    onClearPlan,
    ...overrides,
  }
  const utils = render(<PlansFilterBand {...props} />)
  return { onSelectPlan, onNewPlan, onEditPlan, onApprovePlan, onStopPlan, onClearPlan, ...utils }
}

// ── All tasks tile ───────────────────────────────────────────────────────────

describe('PlansFilterBand — All tasks tile', () => {
  it('renders first and selects (onSelectPlan(null)) when clicked', async () => {
    const user = userEvent.setup()
    const { onSelectPlan } = renderBand({ selectedPlanId: 'plan-1' })
    await user.click(screen.getByRole('button', { name: /All tasks/ }))
    expect(onSelectPlan).toHaveBeenCalledWith(null)
  })

  it('is aria-pressed when selectedPlanId is null, and not when a plan is selected', () => {
    const { rerender } = renderBand({ selectedPlanId: null })
    expect(screen.getByRole('button', { name: /All tasks/ })).toHaveAttribute('aria-pressed', 'true')

    rerender(
      <PlansFilterBand
        plans={[makePlan()]}
        tasks={[]}
        agents={agents}
        selectedPlanId="plan-1"
        onSelectPlan={vi.fn()}
        onNewPlan={vi.fn()}
        onEditPlan={vi.fn()}
        onApprovePlan={vi.fn()}
        onStopPlan={vi.fn()}
        onClearPlan={vi.fn()}
      />,
    )
    expect(screen.getByRole('button', { name: /All tasks/ })).toHaveAttribute('aria-pressed', 'false')
  })

  it('shows the total task count', () => {
    renderBand({
      tasks: [makeTask({ id: 't1' }), makeTask({ id: 't2' }), makeTask({ id: 't3' })],
    })
    expect(screen.getByText('3 tasks')).toBeInTheDocument()
  })

  it('renders only real plans — no ghost placeholder tiles when there are zero plans', () => {
    renderBand({ plans: [] })
    expect(screen.getByRole('button', { name: /All tasks/ })).toBeInTheDocument()
    expect(screen.queryByTestId(/^plan-filter-tile-/)).not.toBeInTheDocument()
  })
})

// ── Plan tile select / toggle ────────────────────────────────────────────────

describe('PlansFilterBand — plan tile select / toggle', () => {
  it('clicking an unselected plan tile selects it (onSelectPlan(id))', async () => {
    const user = userEvent.setup()
    const { onSelectPlan } = renderBand({ plans: [makePlan({ id: 'plan-9', title: 'Payments revamp' })] })
    await user.click(screen.getByRole('button', { name: 'Payments revamp' }))
    expect(onSelectPlan).toHaveBeenCalledWith('plan-9')
  })

  it('re-clicking the ALREADY-active plan tile clears the filter (onSelectPlan(null))', async () => {
    const user = userEvent.setup()
    const { onSelectPlan } = renderBand({
      plans: [makePlan({ id: 'plan-9', title: 'Payments revamp' })],
      selectedPlanId: 'plan-9',
    })
    await user.click(screen.getByRole('button', { name: 'Payments revamp' }))
    expect(onSelectPlan).toHaveBeenCalledWith(null)
  })

  it('the selected plan tile exposes aria-pressed=true; others false', () => {
    renderBand({
      plans: [makePlan({ id: 'plan-a', title: 'A' }), makePlan({ id: 'plan-b', title: 'B' })],
      selectedPlanId: 'plan-a',
    })
    expect(screen.getByRole('button', { name: 'A' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: 'B' })).toHaveAttribute('aria-pressed', 'false')
  })
})

// ── Progress / owner display ─────────────────────────────────────────────────

describe('PlansFilterBand — plan tile progress + owner', () => {
  it('computes done/total from top-level member tasks (excludes subtasks)', () => {
    renderBand({
      plans: [makePlan({ id: 'plan-1' })],
      tasks: [
        makeTask({ id: 't1', plan_id: 'plan-1', status: 'done' }),
        makeTask({ id: 't2', plan_id: 'plan-1', status: 'in_progress' }),
        makeTask({ id: 't3', plan_id: 'plan-1', status: 'done', parent_task_id: 't1' }), // subtask — excluded
        makeTask({ id: 't4', plan_id: 'plan-2', status: 'done' }), // different plan — excluded
      ],
    })
    expect(screen.getByText('1/2')).toBeInTheDocument()
    expect(screen.getByLabelText('Progress: 1 of 2 tasks done')).toBeInTheDocument()
  })

  it("renders the owning agent's short name", () => {
    renderBand({ plans: [makePlan({ owner_agent_id: 'jim' })] })
    expect(screen.getByText('Jim')).toBeInTheDocument()
  })

  it('shows 0/0 (never NaN) when a plan has zero member tasks', () => {
    renderBand({
      plans: [makePlan({ id: 'plan-empty' })],
      tasks: [],
    })
    expect(screen.getByText('0/0')).toBeInTheDocument()
    expect(screen.getByLabelText('Progress: 0 of 0 tasks done')).toBeInTheDocument()
    // Guard against a regression to `Math.round(NaN)` — no "NaN" anywhere.
    expect(screen.queryByText(/NaN/)).not.toBeInTheDocument()
  })
})

// ── Edit isolation (pencil never filters) ────────────────────────────────────

describe('PlansFilterBand — edit pencil isolation', () => {
  it('the pencil fires onEditPlan and NOT onSelectPlan', async () => {
    const user = userEvent.setup()
    const { onEditPlan, onSelectPlan } = renderBand({ plans: [makePlan({ id: 'plan-x', title: 'Nightly sync' })] })
    await user.click(screen.getByRole('button', { name: 'Edit plan Nightly sync' }))
    expect(onEditPlan).toHaveBeenCalledTimes(1)
    expect(onSelectPlan).not.toHaveBeenCalled()
  })
})

// ── ⋯ menu isolation (never filters) ─────────────────────────────────────────

describe('PlansFilterBand — ⋯ menu isolation', () => {
  it('Approve fires onApprovePlan and NOT onSelectPlan', async () => {
    const user = userEvent.setup()
    const { onApprovePlan, onSelectPlan } = renderBand({
      plans: [makePlan({ id: 'plan-x', state: 'draft' })],
    })
    await user.click(screen.getByRole('button', { name: 'Approve' }))
    expect(onApprovePlan).toHaveBeenCalledTimes(1)
    expect(onSelectPlan).not.toHaveBeenCalled()
  })

  it('does not show Approve for a non-draft plan', () => {
    renderBand({ plans: [makePlan({ state: 'running' })] })
    expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument()
  })

  it('shows Stop for a running/approved plan; confirming calls onStop but NEVER onSelectPlan', async () => {
    const user = userEvent.setup()
    const { onStopPlan, onSelectPlan } = renderBand({
      plans: [makePlan({ id: 'plan-x', state: 'running' })],
    })
    await user.click(screen.getByRole('button', { name: 'Stop' }))
    const dialog = screen.getByRole('alertdialog')
    expect(onStopPlan).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button', { name: 'Stop' }))
    expect(onStopPlan).toHaveBeenCalledTimes(1)
    expect(onSelectPlan).not.toHaveBeenCalled()
  })

  it('Cancel dismisses the Stop confirm dialog without calling onStopPlan', async () => {
    const user = userEvent.setup()
    const { onStopPlan, onSelectPlan } = renderBand({ plans: [makePlan({ state: 'running' })] })
    await user.click(screen.getByRole('button', { name: 'Stop' }))
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(onStopPlan).not.toHaveBeenCalled()
    // Parity with the confirm-path isolation tests above — cancelling out of
    // the ⋯ menu's confirm dialog must never bubble into the tile's own
    // select/filter control either.
    expect(onSelectPlan).not.toHaveBeenCalled()
  })

  it('Clear opens a confirm dialog naming the plan; confirming calls onClearPlan but NEVER onSelectPlan', async () => {
    const user = userEvent.setup()
    const { onClearPlan, onSelectPlan } = renderBand({
      plans: [makePlan({ id: 'plan-x', state: 'draft', title: 'Old plan' })],
    })
    await user.click(screen.getByRole('button', { name: 'Clear' }))
    const dialog = screen.getByRole('alertdialog')
    expect(within(dialog).getByText(/Old plan/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Clear' }))
    expect(onClearPlan).toHaveBeenCalledTimes(1)
    expect(onSelectPlan).not.toHaveBeenCalled()
  })

  it('Clear is disabled while the plan is running', () => {
    renderBand({ plans: [makePlan({ state: 'running' })] })
    expect(screen.getByRole('button', { name: 'Clear' })).toBeDisabled()
  })

  it('names the ⋯ trigger after the plan', () => {
    renderBand({ plans: [makePlan({ title: 'Nightly sync' })] })
    expect(screen.getByRole('button', { name: 'Plan actions for Nightly sync' })).toBeInTheDocument()
  })
})

// ── New Plan affordance ───────────────────────────────────────────────────────

describe('PlansFilterBand — New Plan affordance', () => {
  it('fires onNewPlan when clicked', async () => {
    const user = userEvent.setup()
    const { onNewPlan } = renderBand()
    await user.click(screen.getByRole('button', { name: 'New plan' }))
    expect(onNewPlan).toHaveBeenCalledTimes(1)
  })
})

// ── pendingAction (discriminated prop) ──────────────────────────────────────
//
// PlansFilterBand collapses the three independently-pending mutation states
// (approve/stop/clear) into ONE `pendingAction: { planId, action } | null`
// prop — the tile derives its own isApproving/isStopping/isClearing by
// matching `pendingAction.planId` against its own plan id AND checking
// `action`. These tests exercise that derivation directly via the real prop
// shape (not the old approvingPlanId/stoppingPlanId/clearingPlanId props).

describe('PlansFilterBand — pendingAction (discriminated prop)', () => {
  it('disables Approve and shows "Approving…" when pendingAction matches this plan\'s approve', () => {
    renderBand({
      plans: [makePlan({ id: 'plan-x', state: 'draft' })],
      pendingAction: { planId: 'plan-x', action: 'approve' },
    })
    const approveBtn = screen.getByRole('button', { name: 'Approving…' })
    expect(approveBtn).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument()
  })

  it('disables Stop and shows "Stopping…" when pendingAction matches this plan\'s stop', () => {
    renderBand({
      plans: [makePlan({ id: 'plan-x', state: 'running' })],
      pendingAction: { planId: 'plan-x', action: 'stop' },
    })
    const stopBtn = screen.getByRole('button', { name: 'Stopping…' })
    expect(stopBtn).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Stop' })).not.toBeInTheDocument()
  })

  it('disables Clear and shows "Clearing…" when pendingAction matches this plan\'s clear', () => {
    renderBand({
      plans: [makePlan({ id: 'plan-x', state: 'draft' })],
      pendingAction: { planId: 'plan-x', action: 'clear' },
    })
    const clearBtn = screen.getByRole('button', { name: 'Clearing…' })
    expect(clearBtn).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Clear' })).not.toBeInTheDocument()
  })

  it('a pendingAction for a DIFFERENT plan does not disable this plan\'s actions (proves the planId match, not a global flag)', () => {
    renderBand({
      plans: [makePlan({ id: 'plan-x', state: 'draft' })],
      pendingAction: { planId: 'plan-other', action: 'approve' },
    })
    const approveBtn = screen.getByRole('button', { name: 'Approve' })
    expect(approveBtn).not.toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Approving…' })).not.toBeInTheDocument()
  })

  it('a pendingAction for a DIFFERENT action on this same plan does not disable Approve (proves the action match)', () => {
    renderBand({
      plans: [makePlan({ id: 'plan-x', state: 'draft' })],
      pendingAction: { planId: 'plan-x', action: 'clear' },
    })
    const approveBtn = screen.getByRole('button', { name: 'Approve' })
    expect(approveBtn).not.toBeDisabled()
  })
})
