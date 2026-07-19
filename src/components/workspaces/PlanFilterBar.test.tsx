/**
 * PlanFilterBar.test.tsx
 *
 * ADR-049 FR-080/US-10 AS-1: renders plan pills (All + one per plan) + tag
 * chips (All tags/Untagged + distinct tags) in place of the removed
 * `MilestoneFilterPills`. No milestone UI anywhere (SC-040).
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PlanFilterBar } from './PlanFilterBar'
import type { Plan, Task } from '@/lib/api'

function makePlan(overrides: Partial<Plan> = {}): Plan {
  return {
    id: 'plan-1',
    workspace_id: 'ws-1',
    title: 'Launch',
    state: 'running',
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

describe('PlanFilterBar — renders plan pills in place of milestone pills (US-10 AS-1)', () => {
  it('renders an "All" pill and one pill per plan; no milestone UI anywhere', () => {
    const plans = [makePlan({ id: 'plan-1', title: 'Launch' }), makePlan({ id: 'plan-2', title: 'Hardening' })]
    render(
      <PlanFilterBar
        plans={plans}
        tasks={[]}
        activePlanId={null}
        activeTagFilter={null}
        onSelectPlan={vi.fn()}
        onSelectTag={vi.fn()}
        onNewPlan={vi.fn()}
      />,
    )

    expect(screen.getByRole('button', { name: 'All' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Launch' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Hardening' })).toBeInTheDocument()
    expect(screen.queryByText(/milestone/i)).toBeNull()
  })

  it('marks the active plan pill aria-pressed=true and others false', () => {
    const plans = [makePlan({ id: 'plan-1', title: 'Launch' })]
    render(
      <PlanFilterBar
        plans={plans}
        tasks={[]}
        activePlanId="plan-1"
        activeTagFilter={null}
        onSelectPlan={vi.fn()}
        onSelectTag={vi.fn()}
        onNewPlan={vi.fn()}
      />,
    )
    expect(screen.getByRole('button', { name: 'Launch' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: 'All' })).toHaveAttribute('aria-pressed', 'false')
  })

  it('calls onSelectPlan with the plan id when a pill is clicked', () => {
    const onSelectPlan = vi.fn()
    const plans = [makePlan({ id: 'plan-1', title: 'Launch' })]
    render(
      <PlanFilterBar
        plans={plans}
        tasks={[]}
        activePlanId={null}
        activeTagFilter={null}
        onSelectPlan={onSelectPlan}
        onSelectTag={vi.fn()}
        onNewPlan={vi.fn()}
      />,
    )
    screen.getByRole('button', { name: 'Launch' }).click()
    expect(onSelectPlan).toHaveBeenCalledWith('plan-1')
  })

  it('calls onNewPlan when "New plan" is clicked', () => {
    const onNewPlan = vi.fn()
    render(
      <PlanFilterBar
        plans={[]}
        tasks={[]}
        activePlanId={null}
        activeTagFilter={null}
        onSelectPlan={vi.fn()}
        onSelectTag={vi.fn()}
        onNewPlan={onNewPlan}
      />,
    )
    screen.getByRole('button', { name: /new plan/i }).click()
    expect(onNewPlan).toHaveBeenCalledOnce()
  })

  it('renders the "All" pill even with zero plans (boundary — empty state)', () => {
    render(
      <PlanFilterBar
        plans={[]}
        tasks={[]}
        activePlanId={null}
        activeTagFilter={null}
        onSelectPlan={vi.fn()}
        onSelectTag={vi.fn()}
        onNewPlan={vi.fn()}
      />,
    )
    expect(screen.getByRole('button', { name: 'All' })).toBeInTheDocument()
  })
})

describe('PlanFilterBar — tag chip row (US-11 AS-8)', () => {
  it('does not render the tag row when no task carries a tag and no tag filter is active', () => {
    render(
      <PlanFilterBar
        plans={[]}
        tasks={[makeTask()]}
        activePlanId={null}
        activeTagFilter={null}
        onSelectPlan={vi.fn()}
        onSelectTag={vi.fn()}
        onNewPlan={vi.fn()}
      />,
    )
    expect(screen.queryByRole('group', { name: 'Tag filter' })).toBeNull()
  })

  it('renders distinct tags across the given tasks, plus Untagged', () => {
    const tasks = [
      makeTask({ id: 't1', tags: ['release'] }),
      makeTask({ id: 't2', tags: ['urgent', 'release'] }),
    ]
    render(
      <PlanFilterBar
        plans={[]}
        tasks={tasks}
        activePlanId={null}
        activeTagFilter={null}
        onSelectPlan={vi.fn()}
        onSelectTag={vi.fn()}
        onNewPlan={vi.fn()}
      />,
    )
    const tagGroup = screen.getByRole('group', { name: 'Tag filter' })
    expect(tagGroup).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'release' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'urgent' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Untagged' })).toBeInTheDocument()
  })

  it('clicking a tag chip calls onSelectTag with that tag', () => {
    const onSelectTag = vi.fn()
    const tasks = [makeTask({ tags: ['release'] })]
    render(
      <PlanFilterBar
        plans={[]}
        tasks={tasks}
        activePlanId={null}
        activeTagFilter={null}
        onSelectPlan={vi.fn()}
        onSelectTag={onSelectTag}
        onNewPlan={vi.fn()}
      />,
    )
    screen.getByRole('button', { name: 'release' }).click()
    expect(onSelectTag).toHaveBeenCalledWith('release')
  })

  it('clicking the already-active tag chip toggles it off (calls onSelectTag(null))', () => {
    const onSelectTag = vi.fn()
    const tasks = [makeTask({ tags: ['release'] })]
    render(
      <PlanFilterBar
        plans={[]}
        tasks={tasks}
        activePlanId={null}
        activeTagFilter="release"
        onSelectPlan={vi.fn()}
        onSelectTag={onSelectTag}
        onNewPlan={vi.fn()}
      />,
    )
    screen.getByRole('button', { name: 'release' }).click()
    expect(onSelectTag).toHaveBeenCalledWith(null)
  })
})
