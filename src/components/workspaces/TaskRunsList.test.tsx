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

function renderList(taskId = 'task-1', onNavigate?: () => void) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <TaskRunsList taskId={taskId} onNavigate={onNavigate} />
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
