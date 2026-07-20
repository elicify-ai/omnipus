/**
 * TaskRunStatusField.test.tsx
 *
 * Unit coverage for the read-only status badge + "Run now" action extracted
 * for the calendar's recurring-task EDIT mode (CalendarEventSlideOver.test.tsx
 * covers the integration). "Run now" calls runTaskNow (POST /tasks/{id}/runs,
 * ADR-050 RD7) — see task-run-history-spec.md §3.4/§4.3. Also covers the
 * run-aware `occurrence` prop (ADR-050 RD8, folded `{ms, run}` — M7 fix): a
 * resolved run re-points the badge at THAT run's status; an occurrence with
 * no resolved run renders a text state instead of the badge — "Scheduled" for
 * a FUTURE occurrence (D1, operator decision 2026-07-20: Run-now is never
 * offered for a future occurrence) or "Not yet run" for a past/current one
 * (Run-now stays available there).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskRunStatusField } from './TaskRunStatusField'
import type { Task, TaskRun } from '@/lib/api'
import type { TaskRunOccurrenceContext } from '@/lib/taskRuns'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    runTaskNow: vi.fn().mockResolvedValue(undefined),
    isApiError: vi.fn().mockReturnValue(false),
    tasksQueryKeys: actual.tasksQueryKeys,
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

// Fixed reference "now" (D1 future/past gate) — tests pin `now` explicitly
// rather than relying on the wall clock, so they stay deterministic
// regardless of when the suite runs.
const NOW = new Date(2026, 6, 20, 12, 0, 0).getTime() // 2026-07-20 noon
const PAST_MS = NOW - 3 * 60 * 60 * 1000 // 09:00 same day — PAST relative to NOW
const FUTURE_MS = NOW + 24 * 60 * 60 * 1000 // next day — FUTURE relative to NOW

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Task',
    action: 'llm',
    priority: 3,
    status: 'next',
    workspace_id: 'ws-1',
    surface: 'user',
    owner: 'alice',
    created_by: 'alice',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-21T11:30:00Z',
    ...overrides,
  }
}

function makeRun(overrides: Partial<TaskRun> = {}): TaskRun {
  return {
    run_id: 'run-1',
    task_id: 'task-1',
    occurrence_ms: PAST_MS,
    status: 'done',
    result: 'Done.',
    session_id: 'sess-run-1',
    kind: 'scheduled',
    started_at: '2026-07-20T09:00:00Z',
    ended_at: '2026-07-20T09:05:00Z',
    ...overrides,
  }
}

function renderField(
  task: Task,
  extra: { occurrence?: TaskRunOccurrenceContext; now?: number } = {},
) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <TaskRunStatusField task={task} occurrence={extra.occurrence} now={extra.now} />
    </QueryClientProvider>,
  )
}

beforeEach(async () => {
  const api = await import('@/lib/api')
  vi.mocked(api.runTaskNow).mockReset().mockResolvedValue(undefined)
  mockAddToast.mockReset()
})

describe('TaskRunStatusField — status badge', () => {
  it('renders the correct label per status', async () => {
    const cases: [Task['status'], string][] = [
      ['next', 'Next'],
      ['in_progress', 'In Progress'],
      ['blocked', 'Blocked'],
      ['done', 'Done'],
      ['failed', 'Failed'],
    ]
    for (const [status, label] of cases) {
      const { unmount } = renderField(makeTask({ id: `task-${status}`, status }))
      expect(await screen.findByTestId('task-run-status-badge')).toHaveTextContent(label)
      unmount()
    }
  })

  it('shows "Last updated" derived from task.updated_at', async () => {
    renderField(makeTask())
    expect(await screen.findByText(/last updated/i)).toBeInTheDocument()
  })
})

describe('TaskRunStatusField — "Run now"', () => {
  it('renders "Run now" for a startable status (next)', async () => {
    renderField(makeTask({ status: 'next' }))
    expect(await screen.findByRole('button', { name: /run now/i })).toBeInTheDocument()
  })

  it('renders "Run now" for a failed task (manual re-run)', async () => {
    renderField(makeTask({ status: 'failed' }))
    expect(await screen.findByRole('button', { name: /run now/i })).toBeInTheDocument()
  })

  it('hides "Run now" while already in_progress', async () => {
    renderField(makeTask({ status: 'in_progress' }))
    await screen.findByTestId('task-run-status-badge')
    expect(screen.queryByRole('button', { name: /run now/i })).toBeNull()
  })

  it('hides "Run now" for a done one-shot task (no repeating trigger)', async () => {
    renderField(makeTask({ status: 'done' }))
    await screen.findByTestId('task-run-status-badge')
    expect(screen.queryByRole('button', { name: /run now/i })).toBeNull()
  })

  it('hides "Run now" for a done once-trigger task', async () => {
    renderField(makeTask({ status: 'done', trigger: { type: 'once', config: { at_ms: 1_800_000_000_000 } } }))
    await screen.findByTestId('task-run-status-badge')
    expect(screen.queryByRole('button', { name: /run now/i })).toBeNull()
  })

  // Regression: a done RECURRING series re-arms and must stay re-runnable — the
  // client guard mirrors the server validateTransition carve-out (done→in_progress
  // allowed for repeating tasks). Before the fix canDropTransition('done',…) hid
  // Run now on exactly the recurring tasks the calendar edit slide-over manages.
  it('shows "Run now" for a done RECURRING (rrule) task', async () => {
    renderField(
      makeTask({
        status: 'done',
        trigger: { type: 'recurring', config: { rrule: 'FREQ=WEEKLY;BYDAY=MO', dtstart_ms: 1_800_000_000_000 } },
      }),
    )
    expect(await screen.findByRole('button', { name: /run now/i })).toBeInTheDocument()
  })

  it('shows "Run now" for a done EVERY task', async () => {
    renderField(makeTask({ status: 'done', trigger: { type: 'every', config: { every_ms: 3_600_000 } } }))
    expect(await screen.findByRole('button', { name: /run now/i })).toBeInTheDocument()
  })

  it('hides "Run now" for a blocked task', async () => {
    renderField(makeTask({ status: 'blocked' }))
    await screen.findByTestId('task-run-status-badge')
    expect(screen.queryByRole('button', { name: /run now/i })).toBeNull()
  })

  it('clicking "Run now" calls runTaskNow(task.id, undefined) — task-level, no occurrence', async () => {
    const { runTaskNow } = await import('@/lib/api')
    renderField(makeTask({ id: 'task-run', status: 'next' }))

    fireEvent.click(await screen.findByRole('button', { name: /run now/i }))

    await waitFor(() => expect(vi.mocked(runTaskNow)).toHaveBeenCalledWith('task-run', undefined))
    await waitFor(() => expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'success' }),
    ))
  })

  it('surfaces a mutation failure via a toast', async () => {
    const { runTaskNow } = await import('@/lib/api')
    vi.mocked(runTaskNow).mockRejectedValueOnce(new Error('server rejected'))
    renderField(makeTask({ status: 'next' }))

    fireEvent.click(await screen.findByRole('button', { name: /run now/i }))

    await waitFor(() => expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'error' }),
    ))
  })
})

describe('TaskRunStatusField — run-aware (`occurrence` prop, ADR-050 RD8 / M7)', () => {
  it('with a resolved run, the badge reflects the RUN status, not task.status', async () => {
    const task = makeTask({ id: 'task-occ', status: 'in_progress' })
    const run = makeRun({ status: 'failed' })
    renderField(task, { occurrence: { ms: run.occurrence_ms as number, run }, now: NOW })

    expect(await screen.findByTestId('task-run-status-badge')).toHaveTextContent('Failed')
  })

  it('with a resolved run on a PAST occurrence, "Run now" calls runTaskNow(task.id, occurrenceMs)', async () => {
    const { runTaskNow } = await import('@/lib/api')
    const task = makeTask({ id: 'task-occ-run', status: 'done' })
    const run = makeRun({ status: 'done', occurrence_ms: PAST_MS })
    renderField(task, { occurrence: { ms: PAST_MS, run }, now: NOW })

    fireEvent.click(await screen.findByRole('button', { name: /run now/i }))

    await waitFor(() => expect(vi.mocked(runTaskNow)).toHaveBeenCalledWith('task-occ-run', PAST_MS))
  })

  it('hides "Run now" when the resolved run is already in_progress', async () => {
    const task = makeTask({ id: 'task-occ-inflight' })
    const run = makeRun({ status: 'in_progress', occurrence_ms: PAST_MS })
    renderField(task, { occurrence: { ms: PAST_MS, run }, now: NOW })

    await screen.findByTestId('task-run-status-badge')
    expect(screen.queryByRole('button', { name: /run now/i })).toBeNull()
  })

  it('a PAST occurrence with NO resolved run shows "Not yet run" — no badge, and "Run now" stays available', async () => {
    const task = makeTask({ id: 'task-occ-past-no-run', status: 'next' })
    renderField(task, { occurrence: { ms: PAST_MS }, now: NOW })

    expect(await screen.findByTestId('occurrence-run-now-only')).toHaveTextContent(/not yet run/i)
    expect(screen.queryByTestId('task-run-status-badge')).not.toBeInTheDocument()
    expect(screen.queryByTestId('occurrence-scheduled')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /run now/i })).toBeInTheDocument()
  })

  it('a PAST occurrence with no run — clicking "Run now" calls runTaskNow(task.id, occurrenceMs)', async () => {
    const { runTaskNow } = await import('@/lib/api')
    const task = makeTask({ id: 'task-occ-past-runnow' })
    renderField(task, { occurrence: { ms: PAST_MS }, now: NOW })

    fireEvent.click(await screen.findByRole('button', { name: /run now/i }))

    await waitFor(() => expect(vi.mocked(runTaskNow)).toHaveBeenCalledWith('task-occ-past-runnow', PAST_MS))
  })

  it('an occurrence at exactly `now` (boundary) is treated as past/current — "Run now" stays available (occurrenceMs <= now)', async () => {
    const task = makeTask({ id: 'task-occ-boundary' })
    renderField(task, { occurrence: { ms: NOW }, now: NOW })

    expect(screen.getByRole('button', { name: /run now/i })).toBeInTheDocument()
    expect(screen.queryByTestId('occurrence-scheduled')).not.toBeInTheDocument()
  })

  it('D1 — a FUTURE occurrence with NO resolved run shows a "Scheduled" state and NO "Run now"', async () => {
    const task = makeTask({ id: 'task-occ-future', status: 'next' })
    renderField(task, { occurrence: { ms: FUTURE_MS }, now: NOW })

    expect(await screen.findByTestId('occurrence-scheduled')).toBeInTheDocument()
    expect(screen.queryByTestId('task-run-status-badge')).not.toBeInTheDocument()
    expect(screen.queryByTestId('occurrence-run-now-only')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /run now/i })).not.toBeInTheDocument()
  })

  it('D1 — hides "Run now" for a FUTURE occurrence even when a run has already resolved for it', async () => {
    // Defensive edge case (should not occur in practice — a resolved run
    // implies the fire already happened): the future gate is unconditional
    // on occurrence.ms, not contingent on whether a run happens to be
    // attached, so a stale/inconsistent future occurrence_ms still never
    // offers to double-run it.
    const task = makeTask({ id: 'task-occ-future-with-run' })
    const run = makeRun({ status: 'done', occurrence_ms: FUTURE_MS })
    renderField(task, { occurrence: { ms: FUTURE_MS, run }, now: NOW })

    expect(await screen.findByTestId('task-run-status-badge')).toHaveTextContent('Done')
    expect(screen.queryByRole('button', { name: /run now/i })).not.toBeInTheDocument()
  })

  it('with neither `occurrence` prop, behaviour is unchanged (task-level badge)', async () => {
    const task = makeTask({ id: 'task-plain', status: 'failed' })
    renderField(task)

    expect(await screen.findByTestId('task-run-status-badge')).toHaveTextContent('Failed')
    expect(screen.queryByTestId('occurrence-run-now-only')).not.toBeInTheDocument()
    expect(screen.queryByTestId('occurrence-scheduled')).not.toBeInTheDocument()
  })
})
