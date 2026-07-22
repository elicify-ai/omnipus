/**
 * ListView.actions.test.tsx — ADR-052 §6.8 (row ▶/■ action column) + FR-015/
 * US-8 (orange "Cancelled" vs red "Failed" status cell).
 *
 * The full per-state action button matrix lives in TaskActionButton.test.tsx
 * — this file proves ListView wires it as a row action, isolated from the
 * row's own onClick, and that the status cell reads the cancelled
 * discriminator.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ListView } from './ListView'
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

beforeEach(() => {
  runTaskMock.mockReset()
  stopTaskMock.mockReset()
  restartTaskMock.mockReset()
})

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Task',
    status: 'inbox',
    action: 'llm',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:05:00Z',
    ...overrides,
  }
}

function renderList(tasks: Task[], onTaskClick: (t: Task) => void = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <ListView tasks={tasks} agents={[]} onTaskClick={onTaskClick} />
    </QueryClientProvider>,
  )
}

describe('ListView — row action column (ADR-052 §6.8)', () => {
  it('renders a Play action for an idle task', () => {
    renderList([makeTask({ status: 'inbox', title: 'Draft the memo' })])
    expect(screen.getByRole('button', { name: 'Run task Draft the memo' })).toBeInTheDocument()
  })

  it('renders a Stop action for an in_progress task', () => {
    renderList([makeTask({ status: 'in_progress', title: 'Ship it' })])
    expect(screen.getByRole('button', { name: 'Stop task Ship it' })).toBeInTheDocument()
  })

  it('renders no action for a done task, leaving the cell empty', () => {
    renderList([makeTask({ status: 'done' })])
    expect(screen.queryByRole('button', { name: /task Task/ })).not.toBeInTheDocument()
  })

  it('renders NO action for an in-plan member in ANY status — status only (ADR-053 FE-4/D7, FR-145)', () => {
    // FE-4/D7: plan members show status only across Board/List/Graph — no
    // per-member ▶/■/Play in any status, in_progress included. The plan owns
    // member lifecycle; a per-member cancel with dependents would brick the
    // plan. To adjust a member: Stop the plan → change → continue.
    const { rerender } = render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ListView tasks={[makeTask({ plan_id: 'plan-1', status: 'in_progress', title: 'Member' })]} agents={[]} onTaskClick={vi.fn()} />
      </QueryClientProvider>,
    )
    expect(screen.queryByRole('button', { name: /task Member/ })).not.toBeInTheDocument()

    rerender(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ListView tasks={[makeTask({ plan_id: 'plan-1', status: 'next', title: 'Member' })]} agents={[]} onTaskClick={vi.fn()} />
      </QueryClientProvider>,
    )
    expect(screen.queryByRole('button', { name: /task Member/ })).not.toBeInTheDocument()
  })

  it('clicking the row action never fires the row onClick (open task) — isolation', async () => {
    const user = userEvent.setup()
    const onTaskClick = vi.fn()
    renderList([makeTask({ status: 'inbox', title: 'Draft the memo' })], onTaskClick)
    await user.click(screen.getByRole('button', { name: 'Run task Draft the memo' }))
    expect(onTaskClick).not.toHaveBeenCalled()
  })

  it('the row title button still opens the task normally', async () => {
    const user = userEvent.setup()
    const onTaskClick = vi.fn()
    renderList([makeTask({ status: 'inbox', title: 'Draft the memo' })], onTaskClick)
    await user.click(screen.getByRole('button', { name: /Draft the memo, status/ }))
    expect(onTaskClick).toHaveBeenCalledTimes(1)
  })
})

describe('ListView — orange "Cancelled" vs red "Failed" status cell (ADR-052 FR-015/US-8)', () => {
  it('a task Stopped by the user renders "Cancelled" in the status cell, not "Failed"', () => {
    renderList([makeTask({ status: 'failed', cancel_reason: 'stopped_by_user' })])
    const row = screen.getAllByRole('row')[1]
    expect(within(row).getByText('Cancelled')).toBeInTheDocument()
    expect(within(row).queryByText('Failed')).not.toBeInTheDocument()
  })

  it('a genuinely-failed task renders "Failed" in the status cell, not "Cancelled"', () => {
    renderList([makeTask({ status: 'failed' })])
    const row = screen.getAllByRole('row')[1]
    expect(within(row).getByText('Failed')).toBeInTheDocument()
    expect(within(row).queryByText('Cancelled')).not.toBeInTheDocument()
  })

  it('the row aria-label reflects "Cancelled", not "Failed", for a user-stopped task', () => {
    renderList([makeTask({ status: 'failed', cancel_reason: 'stopped_by_user', title: 'Draft the memo' })])
    expect(screen.getByRole('button', { name: 'Draft the memo, status Cancelled' })).toBeInTheDocument()
  })
})
