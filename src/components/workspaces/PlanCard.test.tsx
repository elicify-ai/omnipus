/**
 * PlanCard.test.tsx
 *
 * Hierarchical Drill-Down board redesign — the plan card rendered at the
 * Board's top level (replaces PlanLaneHeader.test.tsx's per-lane band
 * header coverage). Covers:
 *   1. The state badge always pairs an icon WITH visible text (a11y — never
 *      color-alone).
 *   2. Progress (done/total member tasks) renders as visible text and an
 *      accessible label.
 *   3. Owner chip.
 *   4. The plan TITLE is a drill-in control (sets the shared Board⇄Graph
 *      scope); edit is reachable ONLY via the ⋯ menu. No control double-fires
 *      onEdit, and the portalled Stop/Clear confirm dialogs never re-fire it.
 *   5. Actions collapse into ONE ⋯ menu: Open board (drills in) / View graph
 *      (scopes + opens the Graph tab, disabled below 2 tasks) / Approve / Stop /
 *      Edit / Clear.
 *   6. The ⑂ "view graph" control is disabled below 2 member tasks and
 *      otherwise scopes + opens the Graph tab.
 *   7. The ⋯ menu's Approve (draft-only) / Stop (running/approved) / Edit /
 *      Clear actions, including the Stop/Clear confirm dialogs.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PlanCard } from './PlanCard'
import type { Agent, Plan } from '@/lib/api'

// ── Mocks ────────────────────────────────────────────────────────────────────

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => mockNavigate,
}))

const mockSetActivePlanId = vi.fn()
vi.mock('@/store/workspacesStore', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/store/workspacesStore')>()
  return {
    ...actual,
    useWorkspacesStore: (selector?: (s: { setActivePlanId: typeof mockSetActivePlanId }) => unknown) => {
      const store = { setActivePlanId: mockSetActivePlanId }
      return selector ? selector(store) : store
    },
  }
})

// Minimal stub so the ⋯ trigger renders its content without Radix portal
// internals (mirrors the retired PlanLaneHeader.test.tsx's dropdown stub).
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

function renderCard(overrides: Partial<React.ComponentProps<typeof PlanCard>> = {}) {
  const onEdit = vi.fn()
  const onApprove = vi.fn()
  const onStop = vi.fn()
  const onClear = vi.fn()
  const props = {
    plan: makePlan(),
    workspaceId: 'ws-1',
    agents: [] as Agent[],
    memberTotal: 4,
    memberDone: 2,
    onEdit,
    onApprove,
    onStop,
    onClear,
    ...overrides,
  }
  const utils = render(<PlanCard {...props} />)
  return { onEdit, onApprove, onStop, onClear, ...utils }
}

beforeEach(() => {
  mockNavigate.mockReset()
  mockSetActivePlanId.mockReset()
})

// ── State badge (icon + text) ───────────────────────────────────────────────

describe('PlanCard — state badge (icon + text, a11y)', () => {
  it.each([
    ['draft', 'Draft'],
    ['approved', 'Approved'],
    ['running', 'Running'],
    ['done', 'Done'],
    ['failed', 'Failed'],
  ] as const)('renders the %s state with a visible text label (not color-alone)', (state, label) => {
    renderCard({ plan: makePlan({ state }) })
    expect(screen.getByText(label)).toBeInTheDocument()
  })
})

// ── Progress ─────────────────────────────────────────────────────────────────

describe('PlanCard — progress', () => {
  it('renders the done/total member-task count as text', () => {
    renderCard({ memberTotal: 5, memberDone: 3 })
    expect(screen.getByText('3/5')).toBeInTheDocument()
  })

  it('exposes progress via an accessible label', () => {
    renderCard({ memberTotal: 5, memberDone: 3 })
    expect(screen.getByLabelText('Progress: 3 of 5 tasks done')).toBeInTheDocument()
  })

  it('renders 0/0 without dividing by zero when the plan has no member tasks', () => {
    renderCard({ memberTotal: 0, memberDone: 0 })
    expect(screen.getByText('0/0')).toBeInTheDocument()
  })
})

// ── Owner ────────────────────────────────────────────────────────────────────

describe('PlanCard — owner', () => {
  it("renders the owning agent's name when it resolves", () => {
    renderCard({
      agents: [
        { id: 'jim', name: 'Jim', type: 'core', locked: true, status: 'active', soul: '', timeout_seconds: 300, max_tool_iterations: 50 },
      ],
    })
    expect(screen.getByText('Jim')).toBeInTheDocument()
  })

  it('omits the owner chip when no agent matches owner_agent_id', () => {
    renderCard({ agents: [] })
    expect(screen.queryByText('jim')).not.toBeInTheDocument()
  })
})

// ── Title = drill-in; edit is menu-only ──────────────────────────────────────

describe('PlanCard — title drills in; edit is menu-only', () => {
  it('clicking the plan title drills into the plan (activePlanId), not edit', async () => {
    const user = userEvent.setup()
    const { onEdit } = renderCard({ plan: makePlan({ id: 'plan-x', title: 'Launch' }) })
    await user.click(screen.getByRole('button', { name: 'Launch' }))
    expect(mockSetActivePlanId).toHaveBeenCalledWith('plan-x')
    expect(onEdit).not.toHaveBeenCalled()
  })

  it('pressing Enter on the focused title drills in', async () => {
    const user = userEvent.setup()
    renderCard({ plan: makePlan({ id: 'plan-x', title: 'Launch' }) })
    screen.getByRole('button', { name: 'Launch' }).focus()
    await user.keyboard('{Enter}')
    expect(mockSetActivePlanId).toHaveBeenCalledWith('plan-x')
  })

  it('clicking the ⋯ Open board item drills in but does not fire onEdit', async () => {
    const user = userEvent.setup()
    const { onEdit } = renderCard({ plan: makePlan({ id: 'plan-x' }) })
    await user.click(screen.getByRole('button', { name: 'Open board' }))
    expect(onEdit).not.toHaveBeenCalled()
  })

  it('edit fires only via the ⋯ menu Edit item (once, no bubbling)', async () => {
    const user = userEvent.setup()
    const { onEdit } = renderCard({ plan: makePlan({ id: 'plan-x' }) })
    await user.click(screen.getByRole('button', { name: 'Edit' }))
    expect(onEdit).toHaveBeenCalledTimes(1)
  })

  it('confirming Stop calls onStop but NEVER onEdit (portalled dialog must not re-fire the card)', async () => {
    const user = userEvent.setup()
    const { onEdit, onStop } = renderCard({ plan: makePlan({ id: 'plan-x', state: 'running' }) })
    await user.click(screen.getByRole('button', { name: 'Stop' }))
    await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Stop' }))
    expect(onStop).toHaveBeenCalledTimes(1)
    expect(onEdit).not.toHaveBeenCalled()
  })
})

// ── Drill-in (title + ⋯ Open board) ──────────────────────────────────────────

describe('PlanCard — drill-in', () => {
  it('clicking the ⋯ Open board item sets activePlanId to this plan and does not navigate', async () => {
    const user = userEvent.setup()
    renderCard({ plan: makePlan({ id: 'plan-9' }) })
    await user.click(screen.getByRole('button', { name: 'Open board' }))
    expect(mockSetActivePlanId).toHaveBeenCalledWith('plan-9')
    expect(mockNavigate).not.toHaveBeenCalled()
  })
})

// ── View graph (⑂) ───────────────────────────────────────────────────────────

describe('PlanCard — view plan graph (⑂)', () => {
  it('is enabled with ≥2 member tasks, and clicking scopes the Graph tab then navigates', async () => {
    const user = userEvent.setup()
    renderCard({ memberTotal: 2, plan: makePlan({ id: 'plan-9' }), workspaceId: 'ws-9' })
    const btn = screen.getByRole('button', { name: 'View graph' })
    expect(btn).not.toBeDisabled()

    await user.click(btn)

    expect(mockSetActivePlanId).toHaveBeenCalledWith('plan-9')
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/workspaces/$workspaceId/graph',
      params: { workspaceId: 'ws-9' },
    })
  })

  it('is disabled (greyed) with exactly 1 member task — no DAG possible', async () => {
    const user = userEvent.setup()
    renderCard({ memberTotal: 1 })
    const btn = screen.getByRole('button', { name: 'View graph' })
    expect(btn).toBeDisabled()

    await user.click(btn)
    expect(mockSetActivePlanId).not.toHaveBeenCalled()
    expect(mockNavigate).not.toHaveBeenCalled()
  })

  it('is disabled with zero member tasks', () => {
    renderCard({ memberTotal: 0 })
    expect(screen.getByRole('button', { name: 'View graph' })).toBeDisabled()
  })
})

// ── ⋯ menu actions ───────────────────────────────────────────────────────────

describe('PlanCard — ⋯ menu: Approve (draft-only)', () => {
  it('shows Approve for a draft plan and calls onApprove when clicked', async () => {
    const user = userEvent.setup()
    const { onApprove } = renderCard({ plan: makePlan({ state: 'draft' }) })
    await user.click(screen.getByRole('button', { name: 'Approve' }))
    expect(onApprove).toHaveBeenCalledTimes(1)
  })

  it.each(['approved', 'running', 'done', 'failed'] as const)(
    'does not show Approve for a %s plan',
    (state) => {
      renderCard({ plan: makePlan({ state }) })
      expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument()
    },
  )
})

describe('PlanCard — ⋯ menu: Stop (running/approved)', () => {
  it('shows Stop for a running plan; confirming calls onStop', async () => {
    const user = userEvent.setup()
    const { onStop } = renderCard({ plan: makePlan({ state: 'running' }) })
    await user.click(screen.getByRole('button', { name: 'Stop' }))

    const dialog = screen.getByRole('alertdialog')
    expect(onStop).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button', { name: 'Stop' }))
    expect(onStop).toHaveBeenCalledTimes(1)
  })

  it('shows Stop for an approved (cap-waiting) plan too', () => {
    renderCard({ plan: makePlan({ state: 'approved' }) })
    expect(screen.getByRole('button', { name: 'Stop' })).toBeInTheDocument()
  })

  it('does not show Stop for a draft plan', () => {
    renderCard({ plan: makePlan({ state: 'draft' }) })
    expect(screen.queryByRole('button', { name: 'Stop' })).not.toBeInTheDocument()
  })

  it('Cancel dismisses the Stop confirm dialog without calling onStop', async () => {
    const user = userEvent.setup()
    const { onStop } = renderCard({ plan: makePlan({ state: 'running' }) })
    await user.click(screen.getByRole('button', { name: 'Stop' }))
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(onStop).not.toHaveBeenCalled()
  })
})

describe('PlanCard — ⋯ menu: Clear', () => {
  it('opens a confirm dialog naming the plan; confirming calls onClear', async () => {
    const user = userEvent.setup()
    const { onClear } = renderCard({ plan: makePlan({ state: 'draft', title: 'Old plan' }) })
    await user.click(screen.getByRole('button', { name: 'Clear' }))

    const dialog = screen.getByRole('alertdialog')
    expect(within(dialog).getByText(/Old plan/)).toBeInTheDocument()
    expect(onClear).not.toHaveBeenCalled()

    await user.click(within(dialog).getByRole('button', { name: 'Clear' }))
    expect(onClear).toHaveBeenCalledTimes(1)
  })

  it('is disabled while the plan is running (backend rejects delete-while-running)', () => {
    renderCard({ plan: makePlan({ state: 'running' }) })
    expect(screen.getByRole('button', { name: 'Clear' })).toBeDisabled()
  })

  it('is enabled for a non-running plan', () => {
    renderCard({ plan: makePlan({ state: 'draft' }) })
    expect(screen.getByRole('button', { name: 'Clear' })).not.toBeDisabled()
  })
})

describe('PlanCard — ⋯ trigger a11y', () => {
  it('names the trigger button after the plan', () => {
    renderCard({ plan: makePlan({ title: 'Nightly sync' }) })
    expect(screen.getByRole('button', { name: 'Plan actions for Nightly sync' })).toBeInTheDocument()
  })
})
