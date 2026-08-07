/**
 * TaskCard.goalLoopStatus.test.tsx
 *
 * ADR-049 FR-090 (US-11 AS-5, SD-C12): the card shows a goal-loop status
 * affordance ("attempt N/M") sourced from the real `Task.attempt_count` /
 * `Task.max_attempts` wire fields, with a "· paused" suffix when the task's
 * owning Plan (via `plan_id`) reports `state: running` + a non-empty
 * `paused_reason` — never a fabricated/always-false pause state.
 *
 * `showActions={false}` on every render here (ADR-052 §6.8): this file only
 * exercises the goal-loop status chip, unrelated to the ▶/■ action button —
 * turning it off avoids needing a QueryClientProvider (TaskActionButton's
 * useMutation/useQueryClient would otherwise throw without one).
 */

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TaskCard, goalLoopStatusLabel, DEFAULT_TASK_MAX_ATTEMPTS } from './TaskCard'
import type { Task, Plan } from '@/lib/api'

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Goal-loop task',
    status: 'in_progress',
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

describe('goalLoopStatusLabel — pure fn', () => {
  it('returns null when attempt_count is absent', () => {
    expect(goalLoopStatusLabel({ attempt_count: undefined, max_attempts: null }, false)).toBeNull()
  })

  it('returns null when attempt_count is 0', () => {
    expect(goalLoopStatusLabel({ attempt_count: 0, max_attempts: null }, false)).toBeNull()
  })

  it('renders "attempt N/M" against max_attempts when present', () => {
    expect(goalLoopStatusLabel({ attempt_count: 2, max_attempts: 5 }, false)).toBe('attempt 2/5')
  })

  it('falls back to the default (3) max attempts when max_attempts is absent', () => {
    expect(goalLoopStatusLabel({ attempt_count: 1, max_attempts: null }, false)).toBe(`attempt 1/${DEFAULT_TASK_MAX_ATTEMPTS}`)
  })

  it('appends "· paused" when paused is true, without incrementing the attempt', () => {
    expect(goalLoopStatusLabel({ attempt_count: 2, max_attempts: 3 }, true)).toBe('attempt 2/3 · paused')
  })
})

describe('TaskCard — goal-loop status affordance (FR-090)', () => {
  it('shows "attempt N/M" when the task has a real attempt_count', () => {
    render(<TaskCard task={makeTask({ attempt_count: 2, max_attempts: 3 })} onClick={() => {}} showActions={false} />)
    expect(screen.getByText('attempt 2/3')).toBeInTheDocument()
  })

  it('shows nothing when attempt_count is absent (task not running a goal loop)', () => {
    render(<TaskCard task={makeTask()} onClick={() => {}} showActions={false} />)
    expect(screen.queryByText(/attempt \d/)).toBeNull()
  })

  it('shows the paused suffix when the owning plan is running and paused', () => {
    const task = makeTask({ attempt_count: 1, max_attempts: 3, plan_id: 'plan-1' })
    const plans = [makePlan({ id: 'plan-1', state: 'running', paused_reason: 'owner agent disabled' })]
    render(<TaskCard task={task} plans={plans} onClick={() => {}} showActions={false} />)
    expect(screen.getByText('attempt 1/3 · paused')).toBeInTheDocument()
  })

  it('does NOT show paused when the owning plan is running but not paused', () => {
    const task = makeTask({ attempt_count: 1, max_attempts: 3, plan_id: 'plan-1' })
    const plans = [makePlan({ id: 'plan-1', state: 'running' })]
    render(<TaskCard task={task} plans={plans} onClick={() => {}} showActions={false} />)
    expect(screen.getByText('attempt 1/3')).toBeInTheDocument()
    expect(screen.queryByText(/paused/)).toBeNull()
  })

  it('does NOT show paused when the plans prop is absent entirely (no fabricated state)', () => {
    const task = makeTask({ attempt_count: 1, max_attempts: 3, plan_id: 'plan-1' })
    render(<TaskCard task={task} onClick={() => {}} showActions={false} />)
    expect(screen.getByText('attempt 1/3')).toBeInTheDocument()
    expect(screen.queryByText(/paused/)).toBeNull()
  })

  it('does NOT show paused when the owning plan is done/failed even with a stale paused_reason', () => {
    const task = makeTask({ attempt_count: 1, max_attempts: 3, plan_id: 'plan-1' })
    const plans = [makePlan({ id: 'plan-1', state: 'done' })]
    render(<TaskCard task={task} plans={plans} onClick={() => {}} showActions={false} />)
    expect(screen.queryByText(/paused/)).toBeNull()
  })
})
