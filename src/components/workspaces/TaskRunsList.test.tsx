/**
 * TaskRunsList.test.tsx
 *
 * Unit coverage for the shared per-task run-history list (ADR-050 /
 * docs/internal/specs/task-run-history-spec.md §4.3/4.4) — the single
 * component the calendar slide-over and TaskDetailPanel's Runs section both
 * embed. Covers: loading skeleton, populated list (newest-first render,
 * status badge, result, Open-in-Chat), empty state, and error state with retry.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskRunsList } from './TaskRunsList'
import type { TaskRun } from '@/lib/api'
import { ApiError } from '@/lib/api-error'

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => mockNavigate,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchTaskRuns: vi.fn(),
  }
})

import { fetchTaskRuns } from '@/lib/api'

function makeRun(overrides: Partial<TaskRun> = {}): TaskRun {
  return {
    run_id: 'run-1',
    task_id: 'task-1',
    occurrence_ms: null,
    status: 'done',
    result: 'All good.',
    session_id: 'sess-1',
    kind: 'manual',
    started_at: '2026-07-20T09:00:00Z',
    ended_at: '2026-07-20T09:05:00Z',
    ...overrides,
  }
}

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function renderList(
  taskId = 'task-1',
  onNavigate?: () => void,
  dayRange?: { startMs: number; endMs: number },
) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <TaskRunsList taskId={taskId} onNavigate={onNavigate} dayRange={dayRange} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockNavigate.mockReset()
  vi.mocked(fetchTaskRuns).mockReset()
})

describe('TaskRunsList — loading', () => {
  it('renders a skeleton while the query is pending', () => {
    vi.mocked(fetchTaskRuns).mockReturnValue(new Promise(() => {})) // never resolves
    renderList()
    expect(screen.getByTestId('task-runs-skeleton')).toBeInTheDocument()
  })
})

describe('TaskRunsList — empty state', () => {
  it('shows "No runs yet." when the task has no runs', async () => {
    vi.mocked(fetchTaskRuns).mockResolvedValue([])
    renderList()
    await waitFor(() => expect(screen.getByTestId('task-runs-empty')).toBeInTheDocument())
    expect(screen.getByText(/no runs yet/i)).toBeInTheDocument()
  })
})

describe('TaskRunsList — populated', () => {
  it('renders every run newest-first with status badge, ended time, and result', async () => {
    const runs = [
      makeRun({ run_id: 'run-old', status: 'failed', result: 'Boom.', ended_at: '2026-07-18T09:00:00Z' }),
      makeRun({ run_id: 'run-new', status: 'done', result: 'Done fine.', ended_at: '2026-07-20T09:05:00Z' }),
    ]
    vi.mocked(fetchTaskRuns).mockResolvedValue(runs)
    renderList()

    await waitFor(() => expect(screen.getAllByTestId('task-run-row')).toHaveLength(2))

    // Server order is preserved (newest-first is a server contract — GET
    // /tasks/{id}/runs returns "newest first" per the spec); assert the list
    // renders runs in the order the query returned them, not re-sorted.
    const rows = screen.getAllByTestId('task-run-row')
    expect(rows[0]).toHaveTextContent('Boom.')
    expect(rows[1]).toHaveTextContent('Done fine.')

    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.getByText('Done')).toBeInTheDocument()
  })

  it('does not render a result block for an in_progress run (no result yet)', async () => {
    vi.mocked(fetchTaskRuns).mockResolvedValue([
      makeRun({ status: 'in_progress', result: undefined, ended_at: null }),
    ])
    renderList()

    await waitFor(() => expect(screen.getAllByTestId('task-run-row')).toHaveLength(1))
    expect(screen.getByText('In Progress')).toBeInTheDocument()
    expect(screen.queryByTestId('task-run-result')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /open in chat/i })).toBeInTheDocument() // session_id still set
  })

  it('renders Open-in-Chat per run and navigates to its own session on click', async () => {
    const onNavigate = vi.fn()
    vi.mocked(fetchTaskRuns).mockResolvedValue([
      makeRun({ run_id: 'run-a', session_id: 'sess-a' }),
      makeRun({ run_id: 'run-b', session_id: 'sess-b' }),
    ])
    renderList('task-1', onNavigate)

    await waitFor(() => expect(screen.getAllByTestId('task-run-row')).toHaveLength(2))

    const buttons = screen.getAllByRole('button', { name: /open in chat/i })
    expect(buttons).toHaveLength(2)

    await act(async () => {
      fireEvent.click(buttons[1])
    })

    expect(mockNavigate).toHaveBeenCalledWith(
      expect.objectContaining({ to: '/sessions/$sessionId', params: { sessionId: 'sess-b' } }),
    )
    expect(onNavigate).toHaveBeenCalled()
  })

  it('omits Open-in-Chat for a run with no session_id', async () => {
    vi.mocked(fetchTaskRuns).mockResolvedValue([makeRun({ session_id: '' })])
    renderList()

    await waitFor(() => expect(screen.getAllByTestId('task-run-row')).toHaveLength(1))
    expect(screen.queryByRole('button', { name: /open in chat/i })).not.toBeInTheDocument()
  })

  // Backend overlap-guard run outcome (`skipped`) — a scheduled fire the
  // server never ran because the previous occurrence was still in_progress.
  // Must render its own "Skipped" badge with the orange (--color-cancelled)
  // classes, not silently fall back to the "Inbox" badge/label.
  it('renders a "Skipped" badge with the orange skipped classes for a skipped run', async () => {
    vi.mocked(fetchTaskRuns).mockResolvedValue([makeRun({ status: 'skipped', result: undefined, ended_at: null })])
    renderList()

    await waitFor(() => expect(screen.getAllByTestId('task-run-row')).toHaveLength(1))
    const badge = screen.getByText('Skipped')
    expect(badge).toBeInTheDocument()
    expect(badge.className).toContain('--color-cancelled')
    expect(screen.queryByText('Inbox')).not.toBeInTheDocument()
  })
})

// ── dayRange (H2 fix — bucket drill-in, task-run-history-spec §4.3) ─────────
// A bucket click passes `dayRange` so the calendar slide-over's day-scoped
// list shows only THAT day's runs, client-side filtered from the same
// task-scoped fetch (no second network round-trip).
describe('TaskRunsList — dayRange filter (H2)', () => {
  // Date.UTC, not new Date(y, m, d) — the latter builds a LOCAL midnight, and
  // the run fixtures below are UTC instants. Mixing the two makes the window
  // slide by the runner's offset: at UTC+7 the window opens at 17:00Z on the
  // 19th, so the "before the day" run at 23:00Z lands INSIDE it and two rows
  // render instead of one. The test passed only in UTC and westward, which is
  // where CI happens to run.
  const DAY_START = Date.UTC(2026, 6, 20, 0, 0, 0)
  const DAY_END = Date.UTC(2026, 6, 21, 0, 0, 0)

  it('renders only runs whose started_at falls within [startMs, endMs)', async () => {
    const inDay = makeRun({ run_id: 'run-in-day', started_at: '2026-07-20T14:00:00Z', result: 'In day.' })
    const beforeDay = makeRun({ run_id: 'run-before', started_at: '2026-07-19T23:00:00Z', result: 'Before.' })
    const afterDay = makeRun({ run_id: 'run-after', started_at: '2026-07-21T01:00:00Z', result: 'After.' })
    vi.mocked(fetchTaskRuns).mockResolvedValue([beforeDay, inDay, afterDay])

    renderList('task-1', undefined, { startMs: DAY_START, endMs: DAY_END })

    await waitFor(() => expect(screen.getAllByTestId('task-run-row')).toHaveLength(1))
    expect(screen.getByText('In day.')).toBeInTheDocument()
    expect(screen.queryByText('Before.')).not.toBeInTheDocument()
    expect(screen.queryByText('After.')).not.toBeInTheDocument()
  })

  it('shows the empty state when the task has runs, but none fall inside the requested day', async () => {
    const otherDay = makeRun({ run_id: 'run-other-day', started_at: '2026-06-01T09:00:00Z' })
    vi.mocked(fetchTaskRuns).mockResolvedValue([otherDay])

    renderList('task-1', undefined, { startMs: DAY_START, endMs: DAY_END })

    await waitFor(() => expect(screen.getByTestId('task-runs-empty')).toBeInTheDocument())
  })

  it('without dayRange, behaviour is unchanged (all runs render)', async () => {
    vi.mocked(fetchTaskRuns).mockResolvedValue([
      makeRun({ run_id: 'run-a', started_at: '2026-01-01T00:00:00Z' }),
      makeRun({ run_id: 'run-b', started_at: '2026-12-31T00:00:00Z' }),
    ])

    renderList()

    await waitFor(() => expect(screen.getAllByTestId('task-run-row')).toHaveLength(2))
  })
})

describe('TaskRunsList — error state', () => {
  it('shows an error message with a working Retry button', async () => {
    vi.mocked(fetchTaskRuns)
      .mockRejectedValueOnce(new ApiError(500, 'Server exploded.'))
      .mockResolvedValueOnce([makeRun()])

    renderList()

    await waitFor(() => expect(screen.getByTestId('task-runs-error')).toBeInTheDocument())
    expect(screen.getByText('Server exploded.')).toBeInTheDocument()

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /retry/i }))
    })

    await waitFor(() => expect(screen.getAllByTestId('task-run-row')).toHaveLength(1))
    expect(fetchTaskRuns).toHaveBeenCalledTimes(2)
  })
})
