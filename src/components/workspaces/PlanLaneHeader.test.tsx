/**
 * PlanLaneHeader.test.tsx
 *
 * Plan Swimlane board redesign — the plan-lane header rendered at the left
 * of each swimlane band on the Board. Covers:
 *   1. The state badge always pairs an icon WITH visible text (a11y — never
 *      color-alone).
 *   2. Progress (done/total member tasks) renders as visible text and an
 *      accessible label.
 *   3. Collapse/expand: aria-expanded reflects `collapsed`, clicking calls
 *      `onToggleCollapse`.
 *   4. The ⑂ "view graph" button is disabled below 2 member tasks (a DAG
 *      needs at least 2 tasks) and otherwise scopes the Graph tab
 *      (`setActivePlanId`) then navigates there.
 *   5. The ⋯ menu's Approve (draft-only) / Stop (running/approved) / Edit /
 *      Clear actions, including the Stop/Clear confirm dialogs.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PlanLaneHeader } from './PlanLaneHeader'
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
// internals (mirrors -WorkspaceTabBar.test.tsx's DropdownMenu stub).
vi.mock('@/components/ui/dropdown-menu', () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) =>
    asChild ? <>{children}</> : <div>{children}</div>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="plan-lane-menu">{children}</div>
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

function renderHeader(overrides: Partial<React.ComponentProps<typeof PlanLaneHeader>> = {}) {
  const onToggleCollapse = vi.fn()
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
    collapsed: false,
    onToggleCollapse,
    onEdit,
    onApprove,
    onStop,
    onClear,
    ...overrides,
  }
  const utils = render(<PlanLaneHeader {...props} />)
  return { onToggleCollapse, onEdit, onApprove, onStop, onClear, ...utils }
}

beforeEach(() => {
  mockNavigate.mockReset()
  mockSetActivePlanId.mockReset()
})

// ── State badge (icon + text) ───────────────────────────────────────────────

describe('PlanLaneHeader — state badge (icon + text, a11y)', () => {
  it.each([
    ['draft', 'Draft'],
    ['approved', 'Approved'],
    ['running', 'Running'],
    ['done', 'Done'],
    ['failed', 'Failed'],
  ] as const)('renders the %s state with a visible text label (not color-alone)', (state, label) => {
    renderHeader({ plan: makePlan({ state }) })
    expect(screen.getByText(label)).toBeInTheDocument()
  })
})

// ── Progress ─────────────────────────────────────────────────────────────────

describe('PlanLaneHeader — progress', () => {
  it('renders the done/total member-task count as text', () => {
    renderHeader({ memberTotal: 5, memberDone: 3 })
    expect(screen.getByText('3/5')).toBeInTheDocument()
  })

  it('exposes progress via an accessible label', () => {
    renderHeader({ memberTotal: 5, memberDone: 3 })
    expect(screen.getByLabelText('Progress: 3 of 5 tasks done')).toBeInTheDocument()
  })

  it('renders 0/0 without dividing by zero when the plan has no member tasks', () => {
    renderHeader({ memberTotal: 0, memberDone: 0 })
    expect(screen.getByText('0/0')).toBeInTheDocument()
  })
})

// ── Owner ────────────────────────────────────────────────────────────────────

describe('PlanLaneHeader — owner', () => {
  it("renders the owning agent's name when it resolves", () => {
    renderHeader({ agents: [{ id: 'jim', name: 'Jim', type: 'core', locked: true, status: 'active', soul: '', timeout_seconds: 300, max_tool_iterations: 50 }] })
    expect(screen.getByText('Jim')).toBeInTheDocument()
  })

  it('omits the owner chip when no agent matches owner_agent_id', () => {
    renderHeader({ agents: [] })
    expect(screen.queryByText('jim')).not.toBeInTheDocument()
  })
})

// ── Collapse toggle ──────────────────────────────────────────────────────────

describe('PlanLaneHeader — collapse toggle', () => {
  it('carries aria-expanded=true and a Collapse label when expanded', () => {
    renderHeader({ collapsed: false })
    expect(screen.getByRole('button', { name: /collapse launch lane/i })).toHaveAttribute('aria-expanded', 'true')
  })

  it('carries aria-expanded=false and an Expand label when collapsed', () => {
    renderHeader({ collapsed: true })
    expect(screen.getByRole('button', { name: /expand launch lane/i })).toHaveAttribute('aria-expanded', 'false')
  })

  it('clicking the lane toggle calls onToggleCollapse', async () => {
    const user = userEvent.setup()
    const { onToggleCollapse } = renderHeader()
    await user.click(screen.getByRole('button', { name: /collapse launch lane/i }))
    expect(onToggleCollapse).toHaveBeenCalledTimes(1)
  })
})

// ── View graph (⑂) ───────────────────────────────────────────────────────────

describe('PlanLaneHeader — view plan graph (⑂)', () => {
  it('is enabled with ≥2 member tasks, and clicking scopes the Graph tab then navigates', async () => {
    const user = userEvent.setup()
    renderHeader({ memberTotal: 2, plan: makePlan({ id: 'plan-9' }), workspaceId: 'ws-9' })
    const btn = screen.getByRole('button', { name: 'View plan graph' })
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
    renderHeader({ memberTotal: 1 })
    const btn = screen.getByRole('button', { name: 'View plan graph' })
    expect(btn).toBeDisabled()

    await user.click(btn)
    expect(mockSetActivePlanId).not.toHaveBeenCalled()
    expect(mockNavigate).not.toHaveBeenCalled()
  })

  it('is disabled with zero member tasks', () => {
    renderHeader({ memberTotal: 0 })
    expect(screen.getByRole('button', { name: 'View plan graph' })).toBeDisabled()
  })
})

// ── ⋯ menu actions ───────────────────────────────────────────────────────────

describe('PlanLaneHeader — ⋯ menu: Approve (draft-only)', () => {
  it('shows Approve for a draft plan and calls onApprove when clicked', async () => {
    const user = userEvent.setup()
    const { onApprove } = renderHeader({ plan: makePlan({ state: 'draft' }) })
    await user.click(screen.getByRole('button', { name: 'Approve' }))
    expect(onApprove).toHaveBeenCalledTimes(1)
  })

  it.each(['approved', 'running', 'done', 'failed'] as const)(
    'does not show Approve for a %s plan',
    (state) => {
      renderHeader({ plan: makePlan({ state }) })
      expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument()
    },
  )
})

describe('PlanLaneHeader — ⋯ menu: Stop (running/approved)', () => {
  it('shows Stop for a running plan; confirming calls onStop', async () => {
    const user = userEvent.setup()
    const { onStop } = renderHeader({ plan: makePlan({ state: 'running' }) })
    await user.click(screen.getByRole('button', { name: 'Stop' }))

    const dialog = screen.getByRole('alertdialog')
    expect(onStop).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button', { name: 'Stop' }))
    expect(onStop).toHaveBeenCalledTimes(1)
  })

  it('shows Stop for an approved (cap-waiting) plan too', () => {
    renderHeader({ plan: makePlan({ state: 'approved' }) })
    expect(screen.getByRole('button', { name: 'Stop' })).toBeInTheDocument()
  })

  it('does not show Stop for a draft plan', () => {
    renderHeader({ plan: makePlan({ state: 'draft' }) })
    expect(screen.queryByRole('button', { name: 'Stop' })).not.toBeInTheDocument()
  })

  it('Cancel dismisses the Stop confirm dialog without calling onStop', async () => {
    const user = userEvent.setup()
    const { onStop } = renderHeader({ plan: makePlan({ state: 'running' }) })
    await user.click(screen.getByRole('button', { name: 'Stop' }))
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(onStop).not.toHaveBeenCalled()
  })
})

describe('PlanLaneHeader — ⋯ menu: Edit (always available, no confirm)', () => {
  it.each(['draft', 'approved', 'running', 'done', 'failed'] as const)(
    'calls onEdit immediately for a %s plan',
    async (state) => {
      const user = userEvent.setup()
      const { onEdit } = renderHeader({ plan: makePlan({ state }) })
      await user.click(screen.getByRole('button', { name: 'Edit' }))
      expect(onEdit).toHaveBeenCalledTimes(1)
    },
  )
})

describe('PlanLaneHeader — ⋯ menu: Clear', () => {
  it('opens a confirm dialog naming the plan; confirming calls onClear', async () => {
    const user = userEvent.setup()
    const { onClear } = renderHeader({ plan: makePlan({ state: 'draft', title: 'Old plan' }) })
    await user.click(screen.getByRole('button', { name: 'Clear' }))

    const dialog = screen.getByRole('alertdialog')
    expect(within(dialog).getByText(/Old plan/)).toBeInTheDocument()
    expect(onClear).not.toHaveBeenCalled()

    await user.click(within(dialog).getByRole('button', { name: 'Clear' }))
    expect(onClear).toHaveBeenCalledTimes(1)
  })

  it('is disabled while the plan is running (backend rejects delete-while-running)', () => {
    renderHeader({ plan: makePlan({ state: 'running' }) })
    expect(screen.getByRole('button', { name: 'Clear' })).toBeDisabled()
  })

  it('is enabled for a non-running plan', () => {
    renderHeader({ plan: makePlan({ state: 'draft' }) })
    expect(screen.getByRole('button', { name: 'Clear' })).not.toBeDisabled()
  })
})

describe('PlanLaneHeader — ⋯ trigger a11y', () => {
  it('names the trigger button after the plan', () => {
    renderHeader({ plan: makePlan({ title: 'Nightly sync' }) })
    expect(screen.getByRole('button', { name: 'Plan actions for Nightly sync' })).toBeInTheDocument()
  })
})
