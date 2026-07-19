// PlanCard.test.tsx — Plan summary card (ADR-049 FR-082, US-10 AS-3, R1).
//
// Covers: composition (title/goal/owner chip/progress/bounds), the
// `planSecondaryChipLabel` phase/pause/failed-reason chip, the
// draft-only Approve action, the running/approved-only Stop/Clear action,
// and the Stop-confirm AlertDialog (including its behavior when the
// caller's stop mutation ultimately fails).
//
// Fixture pattern mirrors PlanFilterBar.test.tsx's makePlan (this same
// directory) plus the shared makeAgent factory (src/test/factories.ts).

import { describe, it, expect, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PlanCard } from './PlanCard'
import type { Agent, Plan } from '@/lib/api'
import { makeAgent } from '@/test/factories'

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

function renderCard(overrides: Partial<Parameters<typeof PlanCard>[0]> = {}) {
  const onEdit = vi.fn()
  const onApprove = vi.fn()
  const onStop = vi.fn()
  const props = {
    plan: makePlan(),
    agents: [] as Agent[],
    onEdit,
    onApprove,
    onStop,
    ...overrides,
  }
  const utils = render(<PlanCard {...props} />)
  return { onEdit, onApprove, onStop, ...utils }
}

describe('PlanCard — composition', () => {
  it('renders title, goal, owner chip, progress percentage, and bounds text', () => {
    const agents = [makeAgent({ id: 'jim', name: 'Jim', type: 'core' })]
    renderCard({
      plan: makePlan({
        title: 'Ship v0.3',
        goal: 'Get the Workspaces redesign out the door',
        owner_agent_id: 'jim',
        progress: 0.42,
        bounds: { plan_judge_max_rounds: 12, idle_expiry_days: 5 },
      }),
      agents,
    })

    expect(screen.getByText('Ship v0.3')).toBeInTheDocument()
    expect(screen.getByText('Get the Workspaces redesign out the door')).toBeInTheDocument()
    expect(screen.getByText('Jim')).toBeInTheDocument()
    expect(screen.getByText('42%')).toBeInTheDocument()
    expect(screen.getByText(/12 rounds max/)).toBeInTheDocument()
    expect(screen.getByText(/5d idle-expiry/)).toBeInTheDocument()
  })

  it('falls back to default bounds (20 rounds / 7d) when the plan carries none', () => {
    renderCard({ plan: makePlan({ bounds: undefined }) })
    expect(screen.getByText(/20 rounds max/)).toBeInTheDocument()
    expect(screen.getByText(/7d idle-expiry/)).toBeInTheDocument()
  })

  it('shows the current judge round when present', () => {
    renderCard({ plan: makePlan({ judge_rounds: 3 }) })
    expect(screen.getByText(/round 3/)).toBeInTheDocument()
  })

  it('omits the goal paragraph when the plan has none', () => {
    const { container } = renderCard({ plan: makePlan({ goal: undefined }) })
    // Only the goal paragraph carries a `title` attribute (a tooltip of the
    // full text) — with no goal, nothing in the card should have one.
    expect(container.querySelector('[title]')).toBeNull()
  })

  it('omits the owner chip when no agent matches owner_agent_id', () => {
    renderCard({ plan: makePlan({ owner_agent_id: 'ghost-agent' }), agents: [] })
    // No owner chip text rendered — the state badge is the only chip-like element.
    expect(screen.queryByText('ghost-agent')).not.toBeInTheDocument()
  })

  it('clicking the card body calls onEdit', async () => {
    const user = userEvent.setup()
    const { onEdit } = renderCard({ plan: makePlan({ title: 'Clickable plan' }) })
    await user.click(screen.getByText('Clickable plan'))
    expect(onEdit).toHaveBeenCalledTimes(1)
  })
})

describe('PlanCard — planSecondaryChipLabel secondary chip', () => {
  it('shows the plan_phase chip for a running, non-idle plan', () => {
    renderCard({ plan: makePlan({ state: 'running', plan_phase: 'dispatching' }) })
    expect(screen.getByText('dispatching')).toBeInTheDocument()
  })

  it('shows the paused_reason chip in preference to plan_phase', () => {
    renderCard({
      plan: makePlan({ state: 'running', plan_phase: 'judging', paused_reason: 'owner agent disabled' }),
    })
    expect(screen.getByText('paused — owner agent disabled')).toBeInTheDocument()
  })

  it('shows the humanised failed_reason chip for a failed plan', () => {
    renderCard({ plan: makePlan({ state: 'failed', failed_reason: 'judge_rounds_exhausted' }) })
    expect(screen.getByText('judge rounds exhausted')).toBeInTheDocument()
  })

  it('renders no secondary chip for a draft plan', () => {
    renderCard({ plan: makePlan({ state: 'draft' }) })
    expect(screen.queryByText('dispatching')).not.toBeInTheDocument()
    expect(screen.queryByText(/paused —/)).not.toBeInTheDocument()
  })

  it('renders no secondary chip for an idle running plan with no pause', () => {
    renderCard({ plan: makePlan({ state: 'running', plan_phase: 'idle' }) })
    expect(screen.queryByText('idle')).not.toBeInTheDocument()
  })
})

describe('PlanCard — Approve action (draft-only)', () => {
  it('renders Approve in draft state and calls onApprove when clicked', async () => {
    const user = userEvent.setup()
    const { onApprove } = renderCard({ plan: makePlan({ state: 'draft' }) })
    const approveBtn = screen.getByRole('button', { name: 'Approve' })
    expect(approveBtn).toBeInTheDocument()
    await user.click(approveBtn)
    expect(onApprove).toHaveBeenCalledTimes(1)
  })

  it('does not render Approve in approved/running/done/failed states', () => {
    for (const state of ['approved', 'running', 'done', 'failed'] as const) {
      const { unmount } = renderCard({ plan: makePlan({ state }) })
      expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument()
      unmount()
    }
  })

  it('shows "Approving…" and disables the button while isApproving', () => {
    renderCard({ plan: makePlan({ state: 'draft' }), isApproving: true })
    const approveBtn = screen.getByRole('button', { name: 'Approving…' })
    expect(approveBtn).toBeDisabled()
  })
})

describe('PlanCard — Stop/Clear action (running/approved-only)', () => {
  it('renders Stop/Clear for a running plan', () => {
    renderCard({ plan: makePlan({ state: 'running' }) })
    expect(screen.getByRole('button', { name: /Stop\/Clear/ })).toBeInTheDocument()
  })

  it('renders Stop/Clear for an approved (cap-waiting) plan too (R1 O2)', () => {
    renderCard({ plan: makePlan({ state: 'approved' }) })
    expect(screen.getByRole('button', { name: /Stop\/Clear/ })).toBeInTheDocument()
  })

  it('does not render Stop/Clear in draft/done/failed states', () => {
    for (const state of ['draft', 'done', 'failed'] as const) {
      const { unmount } = renderCard({ plan: makePlan({ state }) })
      expect(screen.queryByRole('button', { name: /Stop\/Clear/ })).not.toBeInTheDocument()
      unmount()
    }
  })

  it('renders neither action for a done plan', () => {
    renderCard({ plan: makePlan({ state: 'done' }) })
    expect(screen.queryByRole('button', { name: 'Approve' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Stop\/Clear/ })).not.toBeInTheDocument()
  })
})

describe('PlanCard — Stop-confirm AlertDialog', () => {
  it('clicking Stop/Clear opens a confirmation dialog naming the plan, without calling onStop yet', async () => {
    const user = userEvent.setup()
    const { onStop } = renderCard({ plan: makePlan({ state: 'running', title: 'Nightly sync' }) })
    await user.click(screen.getByRole('button', { name: /Stop\/Clear/ }))

    const dialog = screen.getByRole('alertdialog')
    expect(screen.getByText('Stop this plan?')).toBeInTheDocument()
    // "Nightly sync" also appears in the (still-mounted) card title behind
    // the dialog — scope to the dialog to assert its description names the plan.
    expect(within(dialog).getByText(/Nightly sync/)).toBeInTheDocument()
    expect(onStop).not.toHaveBeenCalled()
  })

  it('Cancel dismisses the dialog without calling onStop', async () => {
    const user = userEvent.setup()
    const { onStop } = renderCard({ plan: makePlan({ state: 'running' }) })
    await user.click(screen.getByRole('button', { name: /Stop\/Clear/ }))
    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(onStop).not.toHaveBeenCalled()
  })

  it('confirming calls onStop exactly once and closes the dialog', async () => {
    const user = userEvent.setup()
    const { onStop } = renderCard({ plan: makePlan({ state: 'running' }) })
    await user.click(screen.getByRole('button', { name: /Stop\/Clear/ }))

    // Two "Stop/Clear"-labelled buttons exist once the dialog is open (the
    // card's own trigger, now behind the dialog, and the dialog's confirm
    // action) — scope to the dialog to click the confirm action specifically.
    const dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Stop/Clear' }))

    expect(onStop).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('error path: after a failed stop (isStopping returns to false without success), Stop/Clear re-enables rather than staying stuck on "Stopping…"', async () => {
    const user = userEvent.setup()
    const onStop = vi.fn()
    const { rerender } = render(
      <PlanCard plan={makePlan({ state: 'running' })} agents={[]} onEdit={vi.fn()} onApprove={vi.fn()} onStop={onStop} isStopping={false} />,
    )
    await user.click(screen.getByRole('button', { name: /Stop\/Clear/ }))
    const dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Stop/Clear' }))
    expect(onStop).toHaveBeenCalledTimes(1)

    // Caller's mutation goes pending — the parent flips isStopping true while in flight.
    rerender(
      <PlanCard plan={makePlan({ state: 'running' })} agents={[]} onEdit={vi.fn()} onApprove={vi.fn()} onStop={onStop} isStopping />,
    )
    expect(screen.getByRole('button', { name: 'Stopping…' })).toBeDisabled()

    // Mutation's onError fires (caller surfaces a toast elsewhere) and flips
    // isStopping back to false — PlanCard itself must not get stuck disabled;
    // the plan is still 'running' so Stop/Clear must be clickable again.
    rerender(
      <PlanCard plan={makePlan({ state: 'running' })} agents={[]} onEdit={vi.fn()} onApprove={vi.fn()} onStop={onStop} isStopping={false} />,
    )
    const retryBtn = screen.getByRole('button', { name: 'Stop/Clear' })
    expect(retryBtn).not.toBeDisabled()

    // A second confirm attempt still works cleanly (not double-firing from the first).
    await user.click(retryBtn)
    const dialog2 = screen.getByRole('alertdialog')
    await user.click(within(dialog2).getByRole('button', { name: 'Stop/Clear' }))
    expect(onStop).toHaveBeenCalledTimes(2)
  })
})
