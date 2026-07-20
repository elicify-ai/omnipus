/**
 * ListView.filters.test.tsx
 *
 * ADR-051 flat/minimal List. Filtering (plan / agent / tag) is owned by the
 * Tasks screen toolbar — the List has NO filter bar of its own any more (it
 * used to duplicate them). Covers:
 *   1. No milestone UI anywhere (SC-040).
 *   2. No filter dropdowns render (the redundant boxed filter row is gone).
 *   3. Clickable column headers sort by Priority / Status / Updated.
 *   4. The Tags column renders tag chips (capped with "+N" overflow).
 */

import { describe, it, expect } from 'vitest'
import { render, screen, within, fireEvent } from '@testing-library/react'
import { ListView } from './ListView'
import type { Task } from '@/lib/api'

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

describe('ListView — no milestone UI anywhere (SC-040)', () => {
  it('renders no "Milestone" filter, column, or text', () => {
    render(<ListView tasks={[makeTask()]} agents={[]} onTaskClick={() => {}} />)
    expect(screen.queryByText(/milestone/i)).toBeNull()
  })
})

describe('ListView — no filter bar (filtering owned by the toolbar)', () => {
  it('renders no filter dropdowns of its own', () => {
    const tasks = [makeTask({ id: 't1', tags: ['release'] })]
    render(<ListView tasks={tasks} agents={[{ id: 'ray', name: 'Ray' }]} onTaskClick={() => {}} />)
    // The old view had 5 boxed Radix Selects (status/priority/plan/tag/agent);
    // they are gone — the toolbar (plan band + Agent + Tags) owns filtering.
    expect(screen.queryAllByRole('combobox')).toHaveLength(0)
  })
})

describe('ListView — sortable column headers', () => {
  it('sorts by priority (most-critical first) when the Pri header is clicked', () => {
    render(
      <ListView
        tasks={[makeTask({ id: 't1', title: 'Low', priority: 5 }), makeTask({ id: 't2', title: 'High', priority: 1 })]}
        agents={[]}
        onTaskClick={() => {}}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /sort by pri/i }))
    const rows = screen.getAllByRole('row') // [0] = header, [1..] = data
    expect(within(rows[1]).getByText('High')).toBeInTheDocument()
    // Re-click toggles direction (least-critical first).
    fireEvent.click(screen.getByRole('button', { name: /sort by pri/i }))
    expect(within(screen.getAllByRole('row')[1]).getByText('Low')).toBeInTheDocument()
  })

  it('sorts by status order when the Status header is clicked', () => {
    render(
      <ListView
        tasks={[makeTask({ id: 't1', title: 'DoneTask', status: 'done' }), makeTask({ id: 't2', title: 'InboxTask', status: 'inbox' })]}
        agents={[]}
        onTaskClick={() => {}}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /sort by status/i }))
    // inbox is first in the lifecycle order → its row comes first.
    expect(within(screen.getAllByRole('row')[1]).getByText('InboxTask')).toBeInTheDocument()
  })

  it('exposes Updated as a sortable header (default view)', () => {
    render(<ListView tasks={[makeTask()]} agents={[]} onTaskClick={() => {}} />)
    expect(screen.getByRole('button', { name: /sort by updated/i })).toBeInTheDocument()
  })
})

describe('ListView — Tags column', () => {
  it('renders a Tags column header, not Milestone', () => {
    render(<ListView tasks={[makeTask()]} agents={[]} onTaskClick={() => {}} />)
    expect(screen.getByRole('columnheader', { name: 'Tags' })).toBeInTheDocument()
    expect(screen.queryByRole('columnheader', { name: /milestone/i })).toBeNull()
  })

  it('renders tag chips in a row, capped with a "+N" overflow indicator', () => {
    const task = makeTask({ tags: ['release', 'urgent', 'q3', 'extra'] })
    render(<ListView tasks={[task]} agents={[]} onTaskClick={() => {}} />)

    const row = screen.getByText('Task').closest('tr')!
    expect(within(row).getByText('release')).toBeInTheDocument()
    expect(within(row).getByText('urgent')).toBeInTheDocument()
    expect(within(row).getByText('+2')).toBeInTheDocument()
  })

  it('renders "—" in the Tags cell when a task has no tags', () => {
    render(<ListView tasks={[makeTask()]} agents={[]} onTaskClick={() => {}} />)
    const row = screen.getByText('Task').closest('tr')!
    // Both the Tags cell and the Agent cell render "—" for a bare task — scope
    // to the 4th <td> (Pri, Title, Status, Tags, Agent, Updated) specifically.
    const cells = within(row).getAllByRole('cell')
    expect(within(cells[3]).getByText('—')).toBeInTheDocument()
  })
})
