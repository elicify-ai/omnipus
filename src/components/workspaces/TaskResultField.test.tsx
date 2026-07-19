/**
 * TaskResultField.test.tsx
 *
 * Unit coverage for the result field extracted out of TaskDetailPanel — self
 * gates to (status done|failed) AND a non-empty result, identical to
 * TaskDetailPanel's `showResult` (see CalendarEventSlideOver.test.tsx for the
 * calendar-side integration coverage).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { TaskResultField } from './TaskResultField'
import type { Task } from '@/lib/api'

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
