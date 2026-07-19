/**
 * OpenInChatButton.test.tsx
 *
 * Unit coverage for the "Open in Chat" button extracted out of TaskDetailPanel
 * (see CalendarEventSlideOver.test.tsx for the calendar-side integration
 * coverage, and TaskDetailPanel.test.tsx's "#250 regression" describe block
 * for the full-panel re-verification proving the refactor is behavior
 * preserving).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { OpenInChatButton } from './OpenInChatButton'
import type { Task } from '@/lib/api'

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
