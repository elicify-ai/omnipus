/**
 * OpenInChatButton.test.tsx
 *
 * Unit coverage for the "Open in Chat" button extracted out of TaskDetailPanel
 * (see CalendarEventSlideOver.test.tsx for the calendar-side integration
 * coverage, and TaskDetailPanel.test.tsx's "#250 regression" describe block
 * for the full-panel re-verification proving the refactor is behavior
 * preserving). Also covers the run-aware `run` prop (ADR-050 RD8): when
 * provided, it opens THAT run's session instead of task.session_id.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { OpenInChatButton } from './OpenInChatButton'
import type { Task, TaskRun } from '@/lib/api'

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => mockNavigate,
}))

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Task',
    action: 'llm',
    priority: 3,
    status: 'in_progress',
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
    session_id: 'sess-run-42',
    kind: 'scheduled',
    started_at: '2026-07-20T09:00:00Z',
    ended_at: '2026-07-20T09:05:00Z',
    ...overrides,
  }
}

beforeEach(() => {
  mockNavigate.mockReset()
})

describe('OpenInChatButton — self-gating', () => {
  it('renders nothing when task.session_id is unset', () => {
    const { container } = render(<OpenInChatButton task={makeTask({ session_id: undefined })} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('renders the button when task.session_id is set', () => {
    render(<OpenInChatButton task={makeTask({ session_id: 'sess-1' })} />)
    expect(screen.getByRole('button', { name: /open in chat/i })).toBeInTheDocument()
  })
})

describe('OpenInChatButton — navigation', () => {
  it('navigates to /sessions/$sessionId and calls onNavigate on click', async () => {
    const onNavigate = vi.fn()
    render(<OpenInChatButton task={makeTask({ session_id: 'sess-42' })} onNavigate={onNavigate} />)

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /open in chat/i }))
    })

    expect(mockNavigate).toHaveBeenCalledWith(
      expect.objectContaining({ to: '/sessions/$sessionId', params: { sessionId: 'sess-42' } }),
    )
    expect(onNavigate).toHaveBeenCalled()
  })

  it('does not throw when onNavigate is omitted', async () => {
    render(<OpenInChatButton task={makeTask({ session_id: 'sess-42' })} />)

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /open in chat/i }))
    })

    expect(mockNavigate).toHaveBeenCalled()
  })
})

describe('OpenInChatButton — run-aware (`run` prop, ADR-050 RD8)', () => {
  it('with a resolved `run`, navigates to the RUN session, not task.session_id', async () => {
    const task = makeTask({ session_id: 'sess-task-latest' })
    const run = makeRun({ session_id: 'sess-run-own-occurrence' })
    render(<OpenInChatButton task={task} run={run} />)

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /open in chat/i }))
    })

    expect(mockNavigate).toHaveBeenCalledWith(
      expect.objectContaining({ to: '/sessions/$sessionId', params: { sessionId: 'sess-run-own-occurrence' } }),
    )
  })

  it('renders when `run.session_id` is set even if task.session_id is unset', () => {
    const task = makeTask({ session_id: undefined })
    const run = makeRun({ session_id: 'sess-run-only' })
    render(<OpenInChatButton task={task} run={run} />)

    expect(screen.getByRole('button', { name: /open in chat/i })).toBeInTheDocument()
  })

  it('without `run`, behaviour is unchanged (reads task.session_id)', async () => {
    render(<OpenInChatButton task={makeTask({ session_id: 'sess-plain' })} />)

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /open in chat/i }))
    })

    expect(mockNavigate).toHaveBeenCalledWith(
      expect.objectContaining({ params: { sessionId: 'sess-plain' } }),
    )
  })
})
