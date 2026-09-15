/**
 * TaskActivityChip.approval.test.tsx
 *
 * Founder decision 2026-09-15: an "ask" tool inside a task run asks the
 * operator and the run waits for the answer. The task must show that it is
 * waiting — "waiting for your approval to use <tool>" — instead of a last
 * activity age that goes stale while nobody answers. The wait is read from the
 * shared approval queue, matched on the task run's own session. Expected
 * strings are written from the decision, not read back from the implementation.
 *
 * TaskCard renders use `showActions={false}`: the ▶/■ button needs a
 * QueryClientProvider and is unrelated to this chip.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import { TaskCard } from './TaskCard'
import { TaskActivityChip, taskAwaitingApproval } from './TaskActivityChip'
import { useToolApprovalStore, type PendingToolApproval } from '@/store/toolApproval'
import type { Task } from '@/lib/api'

const NOW = Date.parse('2026-09-15T12:00:00Z')

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Build the landing page',
    status: 'in_progress',
    action: 'llm',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-09-15T11:00:00Z',
    updated_at: '2026-09-15T11:00:00Z',
    session_id: 'session-task-1',
    last_activity_at: new Date(NOW - 7 * 60_000).toISOString(),
    ...overrides,
  }
}

function approval(overrides: Partial<PendingToolApproval> = {}): PendingToolApproval {
  return {
    approvalId: 'approval-1',
    toolCallId: 'call-1',
    toolName: 'write_file',
    args: {},
    agentId: 'builder',
    sessionId: 'session-task-1',
    turnId: 'turn-1',
    expiresAt: NOW + 600_000,
    workspaceId: 'ws-1',
    ...overrides,
  }
}

describe('taskAwaitingApproval', () => {
  it("finds the approval on the task run's own session", () => {
    expect(taskAwaitingApproval(makeTask(), [approval({ sessionId: 'other' }), approval()])).toEqual(
      expect.objectContaining({ toolName: 'write_file', sessionId: 'session-task-1' }),
    )
  })

  it("ignores another session's approval", () => {
    expect(taskAwaitingApproval(makeTask(), [approval({ sessionId: 'session-chat-9' })])).toBeNull()
  })

  it('is null for a task that is not running or has no run session', () => {
    expect(taskAwaitingApproval(makeTask({ status: 'next' }), [approval()])).toBeNull()
    expect(taskAwaitingApproval(makeTask({ session_id: undefined }), [approval()])).toBeNull()
  })
})

describe('TaskActivityChip while an approval is waiting', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(NOW)
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  })
  afterEach(() => {
    vi.useRealTimers()
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  })

  it('the card says the task is waiting for approval, naming the tool, instead of its age', () => {
    useToolApprovalStore.setState({ queue: [approval()] })
    render(<TaskCard task={makeTask()} onClick={() => {}} showActions={false} />)

    expect(screen.getByTestId('task-awaiting-approval')).toHaveTextContent(
      'In progress · waiting for your approval to use write_file',
    )
    expect(screen.queryByTestId('task-last-activity')).not.toBeInTheDocument()
  })

  it('the panel variant reads "Waiting for your approval to use <tool>"', () => {
    useToolApprovalStore.setState({ queue: [approval({ toolName: 'bash' })] })
    render(<TaskActivityChip task={makeTask()} variant="panel" />)

    expect(screen.getByTestId('task-awaiting-approval')).toHaveTextContent('Waiting for your approval to use bash')
  })

  it('shows even before the run reported any activity', () => {
    useToolApprovalStore.setState({ queue: [approval()] })
    render(<TaskActivityChip task={makeTask({ last_activity_at: undefined })} />)

    expect(screen.getByTestId('task-awaiting-approval')).toBeInTheDocument()
  })

  it('once the approval is answered the chip goes back to the last activity', () => {
    useToolApprovalStore.setState({ queue: [approval()] })
    render(<TaskCard task={makeTask()} onClick={() => {}} showActions={false} />)
    expect(screen.getByTestId('task-awaiting-approval')).toBeInTheDocument()

    act(() => {
      useToolApprovalStore.getState().markResolved('approval-1')
    })

    expect(screen.queryByTestId('task-awaiting-approval')).not.toBeInTheDocument()
    expect(screen.getByTestId('task-last-activity')).toHaveTextContent('In progress · last activity 7 min ago')
  })

  it("another session's approval does not mark this task as waiting", () => {
    useToolApprovalStore.setState({ queue: [approval({ sessionId: 'session-chat-9' })] })
    render(<TaskCard task={makeTask()} onClick={() => {}} showActions={false} />)

    expect(screen.queryByTestId('task-awaiting-approval')).not.toBeInTheDocument()
    expect(screen.getByTestId('task-last-activity')).toBeInTheDocument()
  })
})
