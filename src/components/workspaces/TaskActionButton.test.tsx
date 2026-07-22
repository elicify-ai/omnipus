/**
 * TaskActionButton.test.tsx — ADR-052 §6.8 button matrix for tasks.
 *
 * Covers:
 *   1. Standalone task matrix: idle (inbox/next/failed/cancelled) → Play
 *      (runTask for inbox/next/genuine-failed, restartTask for cancelled);
 *      in_progress → Stop; blocked/done → none.
 *   2. In-plan task matrix: Stop ONLY while in_progress; nothing otherwise.
 *   3. Confirm-modal gating on every ▶/■.
 *   4. Cancel dismisses without mutating.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TaskActionButton, taskActionFor } from './TaskActionButton'
import type { Task } from '@/lib/api'

const runTaskMock = vi.fn()
const stopTaskMock = vi.fn()
const restartTaskMock = vi.fn()

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    runTask: (id: string) => runTaskMock(id),
    stopTask: (id: string) => stopTaskMock(id),
    restartTask: (id: string) => restartTaskMock(id),
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: typeof mockAddToast }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Draft the memo',
    status: 'inbox',
    action: 'llm',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderButton(task: Task) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <TaskActionButton task={task} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  runTaskMock.mockReset().mockResolvedValue(makeTask({ status: 'in_progress' }))
  stopTaskMock.mockReset().mockResolvedValue(makeTask({ status: 'failed', cancel_reason: 'stopped_by_user' }))
  restartTaskMock.mockReset().mockResolvedValue(makeTask({ status: 'next' }))
  mockAddToast.mockReset()
})

describe('taskActionFor — ADR-052 §6.8 button matrix, standalone task (no plan_id)', () => {
  it('inbox → run', () => {
    expect(taskActionFor(makeTask({ status: 'inbox' }))).toBe('run')
  })
  it('next → run', () => {
    expect(taskActionFor(makeTask({ status: 'next' }))).toBe('run')
  })
  it('failed (genuine, no cancel_reason) → run', () => {
    expect(taskActionFor(makeTask({ status: 'failed' }))).toBe('run')
  })
  it('failed + cancel_reason stopped_by_user (cancelled) → restart', () => {
    expect(taskActionFor(makeTask({ status: 'failed', cancel_reason: 'stopped_by_user' }))).toBe('restart')
  })
  it('in_progress → stop', () => {
    expect(taskActionFor(makeTask({ status: 'in_progress' }))).toBe('stop')
  })
  it('blocked → none (backend-managed, no manual action)', () => {
    expect(taskActionFor(makeTask({ status: 'blocked' }))).toBeNull()
  })
  it('done → none (terminal)', () => {
    expect(taskActionFor(makeTask({ status: 'done' }))).toBeNull()
  })
})

describe('taskActionFor — ADR-053 FE-4/D7, in-plan task (plan_id set) — STATUS ONLY', () => {
  // FE-4/FR-145: plan members show status only across Board/List/Graph — no
  // per-member ▶/■/Play in ANY status. The plan owns lifecycle (a per-member
  // cancel with dependents bricks the plan — D7). This includes in_progress,
  // which the old ADR-052 matrix special-cased to 'stop'; FE-4 removed that.
  it('in_progress → none (the plan, not the operator, stops a member)', () => {
    expect(taskActionFor(makeTask({ plan_id: 'plan-1', status: 'in_progress' }))).toBeNull()
  })
  it('inbox → none (the plan drives its start)', () => {
    expect(taskActionFor(makeTask({ plan_id: 'plan-1', status: 'inbox' }))).toBeNull()
  })
  it('next → none', () => {
    expect(taskActionFor(makeTask({ plan_id: 'plan-1', status: 'next' }))).toBeNull()
  })
  it('failed (genuine) → none — only the plan restarts an in-plan member', () => {
    expect(taskActionFor(makeTask({ plan_id: 'plan-1', status: 'failed' }))).toBeNull()
  })
  it('failed + cancelled → none — restart happens via the plan, not per-member', () => {
    expect(taskActionFor(makeTask({ plan_id: 'plan-1', status: 'failed', cancel_reason: 'stopped_by_user' }))).toBeNull()
  })
  it('blocked → none', () => {
    expect(taskActionFor(makeTask({ plan_id: 'plan-1', status: 'blocked' }))).toBeNull()
  })
  it('done → none', () => {
    expect(taskActionFor(makeTask({ plan_id: 'plan-1', status: 'done' }))).toBeNull()
  })
})

describe('TaskActionButton — rendering per state (standalone)', () => {
  it('renders Run for an inbox task', () => {
    renderButton(makeTask({ status: 'inbox' }))
    expect(screen.getByRole('button', { name: 'Run task Draft the memo' })).toBeInTheDocument()
  })

  it('renders Play for a cancelled task', () => {
    renderButton(makeTask({ status: 'failed', cancel_reason: 'stopped_by_user' }))
    expect(screen.getByRole('button', { name: 'Play task Draft the memo' })).toBeInTheDocument()
  })

  it('renders Stop for an in_progress task', () => {
    renderButton(makeTask({ status: 'in_progress' }))
    expect(screen.getByRole('button', { name: 'Stop task Draft the memo' })).toBeInTheDocument()
  })

  it('renders nothing for a done task', () => {
    const { container } = renderButton(makeTask({ status: 'done' }))
    expect(container.querySelector('button')).toBeNull()
  })

  it('renders nothing for a blocked task', () => {
    const { container } = renderButton(makeTask({ status: 'blocked' }))
    expect(container.querySelector('button')).toBeNull()
  })
})

describe('TaskActionButton — rendering per state (in-plan, FE-4/D7 status-only)', () => {
  it('renders nothing for an in_progress plan member (the plan owns its lifecycle)', () => {
    const { container } = renderButton(makeTask({ plan_id: 'plan-1', status: 'in_progress' }))
    expect(container.querySelector('button')).toBeNull()
  })

  it('renders nothing for an idle in-plan member', () => {
    const { container } = renderButton(makeTask({ plan_id: 'plan-1', status: 'next' }))
    expect(container.querySelector('button')).toBeNull()
  })

  it('renders nothing for a failed in-plan member (restart happens via the plan)', () => {
    const { container } = renderButton(makeTask({ plan_id: 'plan-1', status: 'failed', cancel_reason: 'stopped_by_user' }))
    expect(container.querySelector('button')).toBeNull()
  })
})

describe('TaskActionButton — confirm-modal gating (ADR-052 FR-020) + chat-send toggle', () => {
  it('Run opens a confirm modal naming the task; the mutation does not fire until Confirm', async () => {
    const user = userEvent.setup()
    renderButton(makeTask({ status: 'inbox', title: 'Ship the release notes' }))
    await user.click(screen.getByRole('button', { name: 'Run task Ship the release notes' }))

    const dialog = screen.getByRole('alertdialog')
    expect(within(dialog).getByText(/Ship the release notes/)).toBeInTheDocument()
    expect(runTaskMock).not.toHaveBeenCalled()

    await user.click(within(dialog).getByRole('button', { name: 'Run' }))
    expect(runTaskMock).toHaveBeenCalledWith('task-1')
  })

  it('Cancel dismisses the Run confirm without calling runTask', async () => {
    const user = userEvent.setup()
    renderButton(makeTask({ status: 'inbox' }))
    await user.click(screen.getByRole('button', { name: 'Run task Draft the memo' }))
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(runTaskMock).not.toHaveBeenCalled()
  })

  it('Play (cancelled) confirms into restartTask, not runTask', async () => {
    const user = userEvent.setup()
    renderButton(makeTask({ status: 'failed', cancel_reason: 'stopped_by_user' }))
    await user.click(screen.getByRole('button', { name: 'Play task Draft the memo' }))
    const dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Play' }))
    expect(restartTaskMock).toHaveBeenCalledWith('task-1')
    expect(runTaskMock).not.toHaveBeenCalled()
  })

  it('Stop (the "chat-send toggle" slot) confirms into stopTask', async () => {
    const user = userEvent.setup()
    renderButton(makeTask({ status: 'in_progress' }))
    await user.click(screen.getByRole('button', { name: 'Stop task Draft the memo' }))
    const dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Stop' }))
    expect(stopTaskMock).toHaveBeenCalledWith('task-1')
  })

  it('a click on the action button never bubbles to an ancestor onClick (Board/List/Graph isolation)', async () => {
    const user = userEvent.setup()
    const ancestorClick = vi.fn()
    render(
      <QueryClientProvider client={makeClient()}>
        {/* eslint-disable-next-line jsx-a11y/no-static-element-interactions, jsx-a11y/click-events-have-key-events */}
        <div onClick={ancestorClick}>
          <TaskActionButton task={makeTask({ status: 'inbox' })} />
        </div>
      </QueryClientProvider>,
    )
    await user.click(screen.getByRole('button', { name: 'Run task Draft the memo' }))
    expect(ancestorClick).not.toHaveBeenCalled()
  })
})
