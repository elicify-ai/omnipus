/**
 * TaskResultField.test.tsx
 *
 * Unit coverage for the result field extracted out of TaskDetailPanel — self
 * gates to (status done|failed) AND a non-empty result, identical to
 * TaskDetailPanel's `showResult` (see CalendarEventSlideOver.test.tsx for the
 * calendar-side integration coverage). Also covers the run-aware `occurrence`
 * prop (ADR-050 RD8, folded `{ms, run}` — M7 fix): when provided, the RUN's
 * status/result gate and render instead of the task's.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { TaskResultField } from './TaskResultField'
import type { Task, TaskRun } from '@/lib/api'

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

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
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

function makeRun(overrides: Partial<TaskRun> = {}): TaskRun {
  return {
    run_id: 'run-1',
    task_id: 'task-1',
    occurrence_ms: 1_800_000_000_000,
    status: 'done',
    result: 'Run result.',
    session_id: 'sess-run-1',
    kind: 'scheduled',
    started_at: '2026-07-20T09:00:00Z',
    ended_at: '2026-07-20T09:05:00Z',
    ...overrides,
  }
}

beforeEach(() => {
  mockAddToast.mockReset()
  Object.assign(navigator, {
    clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
  })
})

describe('TaskResultField — self-gating', () => {
  it('renders nothing for a task with no result', () => {
    const { container } = render(<TaskResultField task={makeTask({ status: 'done', result: undefined })} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('renders nothing for an in-progress task even if a result is somehow present', () => {
    const { container } = render(<TaskResultField task={makeTask({ status: 'in_progress', result: 'partial' })} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('renders the result for a done task', () => {
    render(<TaskResultField task={makeTask({ status: 'done', result: 'Found 3 anomalies.' })} />)
    expect(screen.getByText('Found 3 anomalies.')).toBeInTheDocument()
    expect(screen.getByText(/^result$/i)).toBeInTheDocument()
  })

  it('renders the result for a failed task', () => {
    render(<TaskResultField task={makeTask({ status: 'failed', result: 'Ran out of budget.' })} />)
    expect(screen.getByText('Ran out of budget.')).toBeInTheDocument()
  })
})

describe('TaskResultField — copy', () => {
  it('copies the result to the clipboard and shows a success toast', async () => {
    render(<TaskResultField task={makeTask({ status: 'done', result: 'Copy me.' })} />)

    fireEvent.click(screen.getByRole('button', { name: /copy result/i }))

    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledWith('Copy me.'))
    await waitFor(() => expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'success' }),
    ))
  })

  it('shows an error toast when the clipboard write fails', async () => {
    Object.assign(navigator, {
      clipboard: { writeText: vi.fn().mockRejectedValue(new Error('denied')) },
    })
    render(<TaskResultField task={makeTask({ status: 'done', result: 'Copy me.' })} />)

    fireEvent.click(screen.getByRole('button', { name: /copy result/i }))

    await waitFor(() => expect(mockAddToast).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'error' }),
    ))
  })
})

describe('TaskResultField — run-aware (`occurrence` prop, ADR-050 RD8 / M7)', () => {
  it('with a resolved run, renders the RUN result, not task.result', () => {
    // Task mirror shows an unrelated (e.g. more recent) result — the RUN's
    // own result must win when `occurrence.run` is provided.
    const task = makeTask({ status: 'done', result: 'Task-level (latest fire) result.' })
    const run = makeRun({ status: 'done', result: 'This occurrence’s own result.' })
    render(<TaskResultField task={task} occurrence={{ ms: run.occurrence_ms as number, run }} />)

    expect(screen.getByText('This occurrence’s own result.')).toBeInTheDocument()
    expect(screen.queryByText('Task-level (latest fire) result.')).not.toBeInTheDocument()
  })

  it('with a resolved run, gates on the RUN status — renders nothing for an in-progress run even if task.status is done', () => {
    const task = makeTask({ status: 'done', result: 'Stale task-level result.' })
    const run = makeRun({ status: 'in_progress', result: undefined })
    const { container } = render(<TaskResultField task={task} occurrence={{ ms: run.occurrence_ms as number, run }} />)

    expect(container).toBeEmptyDOMElement()
  })

  it('with a resolved failed run, shows that run’s failure result', () => {
    const task = makeTask({ status: 'done', result: 'Task says done.' })
    const run = makeRun({ status: 'failed', result: 'This occurrence failed.' })
    render(<TaskResultField task={task} occurrence={{ ms: run.occurrence_ms as number, run }} />)

    expect(screen.getByText('This occurrence failed.')).toBeInTheDocument()
  })

  it('without `occurrence`, behaviour is unchanged (reads task.result)', () => {
    render(<TaskResultField task={makeTask({ status: 'done', result: 'Plain task result.' })} />)
    expect(screen.getByText('Plain task result.')).toBeInTheDocument()
  })

  it('with `occurrence` present but no resolved `run`, falls back to task.result (the component itself does not suppress on occurrence-context-without-a-run — the slide-over gates whether to render this component at all via its own `showResultAndChat` check)', () => {
    const task = makeTask({ status: 'done', result: 'Task-level fallback result.' })
    render(<TaskResultField task={task} occurrence={{ ms: 1_800_000_000_000 }} />)

    expect(screen.getByText('Task-level fallback result.')).toBeInTheDocument()
  })
})
