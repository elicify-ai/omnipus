/**
 * TaskActivityChip.test.tsx
 *
 * Founder decision 2026-09-14 (UAT E-15c): a running task shows how long ago
 * its run last did anything — "In progress · last activity 5 s ago" — so long
 * work is visible and a real hang stands out, without any fixed time limit.
 * Expected strings below are written from that decision, not read back from
 * the implementation.
 *
 * TaskCard renders use `showActions={false}`: the ▶/■ button needs a
 * QueryClientProvider and is unrelated to this chip.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import { TaskCard } from './TaskCard'
import { TaskActivityChip, TASK_ACTIVITY_STALE_MS, formatActivityAge, taskActivity } from './TaskActivityChip'
import type { Task } from '@/lib/api'

const NOW = Date.parse('2026-09-14T12:00:00Z')

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Reconcile invoices',
    status: 'in_progress',
    action: 'llm',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-09-14T11:00:00Z',
    updated_at: '2026-09-14T11:00:00Z',
    ...overrides,
  }
}

function secondsAgo(s: number): string {
  return new Date(NOW - s * 1000).toISOString()
}

describe('formatActivityAge', () => {
  it.each([
    [-3_000, 'just now'],
    [0, 'just now'],
    [999, 'just now'],
    [5_000, '5 s ago'],
    [59_999, '59 s ago'],
    [60_000, '1 min ago'],
    [12 * 60_000, '12 min ago'],
    [3 * 3_600_000, '3 h ago'],
    [49 * 3_600_000, '2 d ago'],
  ])('%i ms renders as %s', (ms, want) => {
    expect(formatActivityAge(ms)).toBe(want)
  })
})

describe('taskActivity', () => {
  it('is fresh for a run active 5 seconds ago', () => {
    expect(taskActivity(makeTask({ last_activity_at: secondsAgo(5) }), NOW)).toEqual({
      age: '5 s ago',
      stale: false,
    })
  })

  it('turns stale exactly at the 5-minute silence limit, not before', () => {
    expect(TASK_ACTIVITY_STALE_MS).toBe(5 * 60_000)
    expect(taskActivity(makeTask({ last_activity_at: secondsAgo(299) }), NOW)?.stale).toBe(false)
    expect(taskActivity(makeTask({ last_activity_at: secondsAgo(300) }), NOW)?.stale).toBe(true)
  })

  it('is stale for a run last active 12 minutes ago', () => {
    expect(taskActivity(makeTask({ last_activity_at: secondsAgo(12 * 60) }), NOW)).toEqual({
      age: '12 min ago',
      stale: true,
    })
  })

  it.each(['inbox', 'next', 'blocked', 'done', 'failed'] as const)(
    'shows nothing for a %s task even if a timestamp is present',
    (status) => {
      expect(taskActivity(makeTask({ status, last_activity_at: secondsAgo(5) }), NOW)).toBeNull()
    },
  )

  it('shows nothing when the server reported no activity evidence', () => {
    expect(taskActivity(makeTask({ last_activity_at: undefined }), NOW)).toBeNull()
    expect(taskActivity(makeTask({ last_activity_at: 'not-a-date' }), NOW)).toBeNull()
  })
})

describe('TaskCard last-activity chip', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('renders the fresh label and keeps its age current while the card stays open', () => {
    render(<TaskCard task={makeTask({ last_activity_at: secondsAgo(5) })} onClick={() => {}} showActions={false} />)

    const chip = screen.getByTestId('task-last-activity')
    expect(chip).toHaveTextContent('In progress · last activity 5 s ago')
    expect(chip).toHaveAttribute('data-stale', 'false')

    act(() => {
      vi.advanceTimersByTime(10_000)
    })
    expect(screen.getByTestId('task-last-activity')).toHaveTextContent('In progress · last activity 15 s ago')
  })

  it('marks a run with no activity for 12 minutes as stale', () => {
    render(
      <TaskCard task={makeTask({ last_activity_at: secondsAgo(12 * 60) })} onClick={() => {}} showActions={false} />,
    )
    const chip = screen.getByTestId('task-last-activity')
    expect(chip).toHaveTextContent('In progress · last activity 12 min ago')
    expect(chip).toHaveAttribute('data-stale', 'true')
  })

  it('renders no chip once the task is no longer in progress', () => {
    render(
      <TaskCard
        task={makeTask({ status: 'done', last_activity_at: secondsAgo(5) })}
        onClick={() => {}}
        showActions={false}
      />,
    )
    expect(screen.queryByTestId('task-last-activity')).toBeNull()
  })
})

describe('TaskActivityChip panel variant', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('omits the status words, since it sits beside the In Progress badge', () => {
    render(<TaskActivityChip task={makeTask({ last_activity_at: secondsAgo(42) })} variant="panel" />)
    expect(screen.getByTestId('task-last-activity')).toHaveTextContent(/^Last activity 42 s ago$/)
  })
})
