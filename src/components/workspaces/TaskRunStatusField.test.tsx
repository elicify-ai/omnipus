/**
 * TaskRunStatusField.test.tsx
 *
 * Unit coverage for the read-only status badge + "Run now" action extracted
 * for the calendar's recurring-task EDIT mode (CalendarEventSlideOver.test.tsx
 * covers the integration). "Run now" calls runTaskNow (POST /tasks/{id}/runs,
 * ADR-050 RD7) — see task-run-history-spec.md §3.4/§4.3. Also covers the
 * run-aware `run`/`occurrenceMs` props (ADR-050 RD8): a resolved run
 * re-points the badge at THAT run's status; an occurrence with no resolved
 * run yet renders only "Run now" (no badge).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskRunStatusField } from './TaskRunStatusField'
import type { Task, TaskRun } from '@/lib/api'

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
    occurrence_ms: 1_800_000_000_000,
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
  extra: { run?: TaskRun; occurrenceMs?: number | null } = {},
) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <TaskRunStatusField task={task} run={extra.run} occurrenceMs={extra.occurrenceMs} />
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

describe('TaskRunStatusField — run-aware (`run` / `occurrenceMs`, ADR-050 RD8)', () => {
  it('with a resolved `run`, the badge reflects the RUN status, not task.status', async () => {
    const task = makeTask({ id: 'task-occ', status: 'in_progress' })
    const run = makeRun({ status: 'failed' })
    renderField(task, { run, occurrenceMs: run.occurrence_ms })

    expect(await screen.findByTestId('task-run-status-badge')).toHaveTextContent('Failed')
  })

  it('with a resolved `run`, "Run now" calls runTaskNow(task.id, occurrenceMs)', async () => {
    const { runTaskNow } = await import('@/lib/api')
    const task = makeTask({ id: 'task-occ-run', status: 'done' })
    const run = makeRun({ status: 'done', occurrence_ms: 1_900_000_000_000 })
    renderField(task, { run, occurrenceMs: 1_900_000_000_000 })

    fireEvent.click(await screen.findByRole('button', { name: /run now/i }))

    await waitFor(() => expect(vi.mocked(runTaskNow)).toHaveBeenCalledWith('task-occ-run', 1_900_000_000_000))
  })

  it('hides "Run now" when the resolved run is already in_progress', async () => {
    const task = makeTask({ id: 'task-occ-inflight' })
    const run = makeRun({ status: 'in_progress' })
    renderField(task, { run, occurrenceMs: run.occurrence_ms })

    await screen.findByTestId('task-run-status-badge')
    expect(screen.queryByRole('button', { name: /run now/i })).toBeNull()
  })

  it('an occurrence with NO resolved run shows ONLY "Run now" — no badge', async () => {
    const task = makeTask({ id: 'task-occ-future', status: 'next' })
    renderField(task, { occurrenceMs: 2_000_000_000_000 }) // no `run` — future/unresolved

    expect(await screen.findByRole('button', { name: /run now/i })).toBeInTheDocument()
    expect(screen.queryByTestId('task-run-status-badge')).not.toBeInTheDocument()
  })

  it('a future/unresolved occurrence "Run now" still calls runTaskNow(task.id, occurrenceMs)', async () => {
    const { runTaskNow } = await import('@/lib/api')
    const task = makeTask({ id: 'task-occ-future-run' })
    renderField(task, { occurrenceMs: 2_100_000_000_000 })

    fireEvent.click(await screen.findByRole('button', { name: /run now/i }))

    await waitFor(() => expect(vi.mocked(runTaskNow)).toHaveBeenCalledWith('task-occ-future-run', 2_100_000_000_000))
  })

  it('with neither `run` nor `occurrenceMs`, behaviour is unchanged (task-level badge)', async () => {
    const task = makeTask({ id: 'task-plain', status: 'failed' })
    renderField(task)

    expect(await screen.findByTestId('task-run-status-badge')).toHaveTextContent('Failed')
    expect(screen.queryByTestId('occurrence-run-now-only')).not.toBeInTheDocument()
  })
})
