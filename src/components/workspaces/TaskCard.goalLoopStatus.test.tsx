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
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render, screen } from '@testing-library/react'
import { TaskCard, goalLoopStatusLabel, DEFAULT_TASK_MAX_ATTEMPTS } from './TaskCard'
import type { Task, Plan } from '@/lib/api'

const __dirname = dirname(fileURLToPath(import.meta.url))
const PLANNING_GO = join(__dirname, '..', '..', '..', 'pkg', 'config', 'planning.go')

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
    expect(goalLoopStatusLabel({ attempt_count: undefined, max_attempts: null, status: 'failed' }, false)).toBeNull()
  })

  it('returns null when attempt_count is 0', () => {
    expect(goalLoopStatusLabel({ attempt_count: 0, max_attempts: null, status: 'failed' }, false)).toBeNull()
  })

  it('renders "attempt N/M" against max_attempts when present (terminal task — the used-up count)', () => {
    expect(goalLoopStatusLabel({ attempt_count: 2, max_attempts: 5, status: 'failed' }, false)).toBe('attempt 2/5')
  })

  it('falls back to the default max attempts when max_attempts is absent', () => {
    expect(goalLoopStatusLabel({ attempt_count: 1, max_attempts: null, status: 'failed' }, false)).toBe(`attempt 1/${DEFAULT_TASK_MAX_ATTEMPTS}`)
  })

  // Anti-drift oracle. Every other assertion in this file uses
  // DEFAULT_TASK_MAX_ATTEMPTS as its own expected value, so all of them pass
  // at ANY value of the constant — including a stale one. This constant is
  // the DENOMINATOR whenever `Task.max_attempts` is absent, which is the
  // normal "inherit the global" case, so when it drifts below the backend's
  // real ceiling a perfectly healthy task renders as "attempt 7/3" — already
  // past a limit it has not reached. The only check that can fail on drift
  // reads the Go constant itself.
  // The single goal try limit (founder decision 2026-09-14) retired the
  // separate DefaultTaskMaxAttempts; the fallback tracks DefaultGoalMaxRounds.
  it('DEFAULT_TASK_MAX_ATTEMPTS equals pkg/config/planning.go\'s DefaultGoalMaxRounds', () => {
    const go = readFileSync(PLANNING_GO, 'utf-8')
    const m = go.match(/DefaultGoalMaxRounds\s*=\s*(\d+)/)
    expect(m, 'DefaultGoalMaxRounds not found in pkg/config/planning.go').not.toBeNull()
    expect(DEFAULT_TASK_MAX_ATTEMPTS).toBe(Number(m![1]))
  })

  // Founder decision 2026-09-14: "Tries per goal" bounds tasks too, so the
  // denominator is the server-resolved `effective_max_attempts` (the enforced
  // ceiling), not `max_attempts` and not the hardcoded fallback. With the
  // setting at 5 and no per-task override the server sends 5 and no
  // max_attempts; the card must say 5, not 20.
  it('renders the server-resolved effective_max_attempts, not the fallback', () => {
    expect(
      goalLoopStatusLabel({ attempt_count: 2, effective_max_attempts: 5, max_attempts: null, status: 'failed' }, false),
    ).toBe('attempt 2/5')
  })

  it('appends "· paused" when paused is true, without incrementing the attempt (task not in_progress)', () => {
    expect(goalLoopStatusLabel({ attempt_count: 2, max_attempts: 3, status: 'blocked' }, true)).toBe('attempt 2/3 · paused')
  })

  // Live UAT: AttemptCount counts attempts already CONSUMED — the backend's
  // sole writer (consumeAttemptOrExhaust) persists it only once an attempt's
  // outcome is known, AFTER that attempt finished, but hands the judge
  // `AttemptCount + 1` as the LIVE attempt's own number. Showing the raw,
  // not-yet-caught-up count for the whole duration of attempt 2 read as a
  // stuck counter ("attempt 1/20"). This is the fix: `status: 'in_progress'`
  // shows the attempt actually in flight.
  it('shows the IN-FLIGHT attempt (AttemptCount + 1) while status is in_progress', () => {
    expect(goalLoopStatusLabel({ attempt_count: 1, max_attempts: 20, status: 'in_progress' }, false)).toBe('attempt 2/20')
  })

  it('shows the plain used-up count once the task is terminal (done)', () => {
    expect(goalLoopStatusLabel({ attempt_count: 1, max_attempts: 20, status: 'done' }, false)).toBe('attempt 1/20')
  })

  it('shows the plain used-up count once the task is terminal (failed)', () => {
    expect(goalLoopStatusLabel({ attempt_count: 1, max_attempts: 20, status: 'failed' }, false)).toBe('attempt 1/20')
  })

  it('the in_progress increment composes with the paused suffix', () => {
    expect(goalLoopStatusLabel({ attempt_count: 1, max_attempts: 20, status: 'in_progress' }, true)).toBe(
      'attempt 2/20 · paused',
    )
  })
})

describe('TaskCard — goal-loop status affordance (FR-090)', () => {
  it('shows "attempt N/M" — the used-up count — for a terminal task with a real attempt_count', () => {
    render(
      <TaskCard
        task={makeTask({ attempt_count: 2, max_attempts: 3, status: 'failed' })}
        onClick={() => {}}
        showActions={false}
      />,
    )
    expect(screen.getByText('attempt 2/3')).toBeInTheDocument()
  })

  // Live UAT regression: `makeTask()` defaults to `status: 'in_progress'` —
  // an ACTIVELY RUNNING goal loop, the affordance's actual use case. AttemptCount
  // (2) counts attempts already consumed; the live attempt in flight is 3.
  it('shows the IN-FLIGHT attempt (AttemptCount + 1) while the task is actively running', () => {
    render(<TaskCard task={makeTask({ attempt_count: 2, max_attempts: 3 })} onClick={() => {}} showActions={false} />)
    expect(screen.getByText('attempt 3/3')).toBeInTheDocument()
  })

  it('shows the server-resolved effective_max_attempts as the denominator', () => {
    render(
      <TaskCard
        task={makeTask({ attempt_count: 2, effective_max_attempts: 5, status: 'failed' })}
        onClick={() => {}}
        showActions={false}
      />,
    )
    expect(screen.getByText('attempt 2/5')).toBeInTheDocument()
  })

  it('shows nothing when attempt_count is absent (task not running a goal loop)', () => {
    render(<TaskCard task={makeTask()} onClick={() => {}} showActions={false} />)
    expect(screen.queryByText(/attempt \d/)).toBeNull()
  })

  // The four paused-suffix tests below pin `status: 'blocked'` (not the
  // in_progress default) deliberately — they exercise the PAUSE-suffix
  // composition, independent of the in-flight-attempt increment (covered by
  // its own tests above and in the pure-fn describe block), so the expected
  // count stays the raw, un-incremented `attempt_count`.
  it('shows the paused suffix when the owning plan is running and paused', () => {
    const task = makeTask({ attempt_count: 1, max_attempts: 3, plan_id: 'plan-1', status: 'blocked' })
    const plans = [makePlan({ id: 'plan-1', state: 'running', paused_reason: 'owner agent disabled' })]
    render(<TaskCard task={task} plans={plans} onClick={() => {}} showActions={false} />)
    expect(screen.getByText('attempt 1/3 · paused')).toBeInTheDocument()
  })

  it('does NOT show paused when the owning plan is running but not paused', () => {
    const task = makeTask({ attempt_count: 1, max_attempts: 3, plan_id: 'plan-1', status: 'blocked' })
    const plans = [makePlan({ id: 'plan-1', state: 'running' })]
    render(<TaskCard task={task} plans={plans} onClick={() => {}} showActions={false} />)
    expect(screen.getByText('attempt 1/3')).toBeInTheDocument()
    expect(screen.queryByText(/paused/)).toBeNull()
  })

  it('does NOT show paused when the plans prop is absent entirely (no fabricated state)', () => {
    const task = makeTask({ attempt_count: 1, max_attempts: 3, plan_id: 'plan-1', status: 'blocked' })
    render(<TaskCard task={task} onClick={() => {}} showActions={false} />)
    expect(screen.getByText('attempt 1/3')).toBeInTheDocument()
    expect(screen.queryByText(/paused/)).toBeNull()
  })

  it('does NOT show paused when the owning plan is done/failed even with a stale paused_reason', () => {
    const task = makeTask({ attempt_count: 1, max_attempts: 3, plan_id: 'plan-1', status: 'blocked' })
    const plans = [makePlan({ id: 'plan-1', state: 'done' })]
    render(<TaskCard task={task} plans={plans} onClick={() => {}} showActions={false} />)
    expect(screen.queryByText(/paused/)).toBeNull()
  })
})
