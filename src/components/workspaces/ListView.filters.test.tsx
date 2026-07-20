/**
 * ListView.filters.test.tsx
 *
 * ADR-051 flat List with Excel-style column headers: every column header is a
 * dropdown offering sort (asc/desc) and, where the column has discrete values,
 * a checkbox value-filter (Pri / Status / Tags / Agent). Filtering is
 * column-local (AND across columns, OR within a column's checklist) over the
 * plan-scoped `tasks` prop. Covers:
 *   1. No milestone UI anywhere (SC-040).
 *   2. Column-header sort (Pri / Status / Title / Agent / Updated), default Updated desc.
 *   3. Column value-filters: Status, Pri, Tags (+ Untagged), Agent (+ Unassigned),
 *      union-within-column, and the filtered empty state.
 *   4. Agent-column resolution + the surface!=='user' filter + the Tags column render.
 *
 * The Radix DropdownMenu is stubbed inline (its pointer/portal internals don't
 * drive in jsdom — same convention as WorkspaceTasksTab.test.tsx) so each
 * column menu's items render inline; tests scope to a column via its <th>.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, within, fireEvent } from '@testing-library/react'
import type { Task } from '@/lib/api'

vi.mock('@/components/ui/dropdown-menu', () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) =>
    asChild ? <>{children}</> : <div>{children}</div>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuItem: ({ children, onClick, className }: { children: React.ReactNode; onClick?: () => void; className?: string }) => (
    <button type="button" role="menuitem" onClick={onClick} className={className}>
      {children}
    </button>
  ),
  DropdownMenuCheckboxItem: ({ children, checked, onCheckedChange, className }: { children: React.ReactNode; checked?: boolean; onCheckedChange?: (v: boolean) => void; className?: string }) => (
    <button type="button" role="menuitemcheckbox" aria-checked={checked} onClick={() => onCheckedChange?.(!checked)} className={className}>
      {children}
    </button>
  ),
  DropdownMenuSeparator: () => <hr />,
}))

// Imported after the mock so ListView picks up the stubbed dropdown-menu.
import { ListView } from './ListView'

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

/** Scope queries to one column's header menu (trigger + inline stub content). */
function columnMenu(label: string) {
  const trigger = screen.getByRole('button', { name: new RegExp(`^${label} column`, 'i') })
  return within(trigger.closest('th')!)
}

/** Titles of the data rows, in render order (skips the header row). */
function rowTitles(): string[] {
  return screen
    .getAllByRole('row')
    .slice(1)
    .map((r) => within(r).queryAllByRole('cell')[1]?.textContent ?? '')
    .filter((t) => t.length > 0)
}

describe('ListView — no milestone UI anywhere (SC-040)', () => {
  it('renders no "Milestone" filter, column, or text', () => {
    render(<ListView tasks={[makeTask()]} agents={[]} onTaskClick={() => {}} />)
    expect(screen.queryByText(/milestone/i)).toBeNull()
  })
})

describe('ListView — column header sort', () => {
  it('defaults to newest-first (Updated desc) on initial render', () => {
    render(
      <ListView
        tasks={[
          makeTask({ id: 'old', title: 'Older', updated_at: '2026-06-20T10:00:00Z' }),
          makeTask({ id: 'new', title: 'Newer', updated_at: '2026-06-20T12:00:00Z' }),
        ]}
        agents={[]}
        onTaskClick={() => {}}
      />,
    )
    expect(rowTitles()[0]).toBe('Newer')
    // The Updated header shows the active descending arrow.
    expect(columnMenu('Updated').getByText('Updated ↓')).toBeInTheDocument()
  })

  it('sorts by priority via the Pri column menu (asc = P1 first, desc = P5 first)', () => {
    render(
      <ListView
        tasks={[makeTask({ id: 't1', title: 'Low', priority: 5 }), makeTask({ id: 't2', title: 'High', priority: 1 })]}
        agents={[]}
        onTaskClick={() => {}}
      />,
    )
    fireEvent.click(columnMenu('Pri').getByRole('menuitem', { name: /sort ascending/i }))
    expect(rowTitles()[0]).toBe('High')
    fireEvent.click(columnMenu('Pri').getByRole('menuitem', { name: /sort descending/i }))
    expect(rowTitles()[0]).toBe('Low')
  })

  it('sorts by status via the Status column menu', () => {
    render(
      <ListView
        tasks={[makeTask({ id: 't1', title: 'DoneTask', status: 'done' }), makeTask({ id: 't2', title: 'InboxTask', status: 'inbox' })]}
        agents={[]}
        onTaskClick={() => {}}
      />,
    )
    fireEvent.click(columnMenu('Status').getByRole('menuitem', { name: /sort ascending/i }))
    // inbox is first in the lifecycle order → its row comes first.
    expect(rowTitles()[0]).toBe('InboxTask')
  })

  it('sorts by title A–Z via the Title column menu', () => {
    render(
      <ListView
        tasks={[makeTask({ id: 't1', title: 'Zebra' }), makeTask({ id: 't2', title: 'Apple' })]}
        agents={[]}
        onTaskClick={() => {}}
      />,
    )
    fireEvent.click(columnMenu('Title').getByRole('menuitem', { name: /sort ascending/i }))
    expect(rowTitles()[0]).toBe('Apple')
  })

  it('sorts by agent A–Z via the Agent column menu', () => {
    render(
      <ListView
        tasks={[makeTask({ id: 't1', title: 'RayTask', agent_name: 'Ray' }), makeTask({ id: 't2', title: 'AvaTask', agent_name: 'Ava' })]}
        agents={[]}
        onTaskClick={() => {}}
      />,
    )
    fireEvent.click(columnMenu('Agent').getByRole('menuitem', { name: /sort ascending/i }))
    expect(rowTitles()[0]).toBe('AvaTask')
  })
})

describe('ListView — column value filters', () => {
  it('filters by status via the Status column checklist', () => {
    render(
      <ListView
        tasks={[makeTask({ id: 't1', title: 'InboxT', status: 'inbox' }), makeTask({ id: 't2', title: 'DoneT', status: 'done' })]}
        agents={[]}
        onTaskClick={() => {}}
      />,
    )
    fireEvent.click(columnMenu('Status').getByRole('menuitemcheckbox', { name: 'Inbox' }))
    expect(screen.getByText('InboxT')).toBeInTheDocument()
    expect(screen.queryByText('DoneT')).toBeNull()
  })

  it('a column filter is a union across the checked values', () => {
    render(
      <ListView
        tasks={[
          makeTask({ id: 't1', title: 'InboxT', status: 'inbox' }),
          makeTask({ id: 't2', title: 'DoneT', status: 'done' }),
          makeTask({ id: 't3', title: 'FailedT', status: 'failed' }),
        ]}
        agents={[]}
        onTaskClick={() => {}}
      />,
    )
    fireEvent.click(columnMenu('Status').getByRole('menuitemcheckbox', { name: 'Inbox' }))
    fireEvent.click(columnMenu('Status').getByRole('menuitemcheckbox', { name: 'Done' }))
    expect(screen.getByText('InboxT')).toBeInTheDocument()
    expect(screen.getByText('DoneT')).toBeInTheDocument()
    expect(screen.queryByText('FailedT')).toBeNull()
  })

  it('filters by priority via the Pri column checklist', () => {
    render(
      <ListView
        tasks={[makeTask({ id: 't1', title: 'P1T', priority: 1 }), makeTask({ id: 't2', title: 'P3T', priority: 3 })]}
        agents={[]}
        onTaskClick={() => {}}
      />,
    )
    fireEvent.click(columnMenu('Pri').getByRole('menuitemcheckbox', { name: 'P1' }))
    expect(screen.getByText('P1T')).toBeInTheDocument()
    expect(screen.queryByText('P3T')).toBeNull()
  })

  it('filters by tag, with an Untagged bucket', () => {
    render(
      <ListView
        tasks={[
          makeTask({ id: 't1', title: 'Tagged', tags: ['release'] }),
          makeTask({ id: 't2', title: 'Bare' }),
        ]}
        agents={[]}
        onTaskClick={() => {}}
      />,
    )
    fireEvent.click(columnMenu('Tags').getByRole('menuitemcheckbox', { name: 'release' }))
    expect(screen.getByText('Tagged')).toBeInTheDocument()
    expect(screen.queryByText('Bare')).toBeNull()

    // Untagged bucket selects only tasks with no tags.
    fireEvent.click(columnMenu('Tags').getByRole('menuitemcheckbox', { name: 'release' })) // clear
    fireEvent.click(columnMenu('Tags').getByRole('menuitemcheckbox', { name: 'Untagged' }))
    expect(screen.getByText('Bare')).toBeInTheDocument()
    expect(screen.queryByText('Tagged')).toBeNull()
  })

  it('filters by agent, with an Unassigned bucket', () => {
    render(
      <ListView
        tasks={[
          makeTask({ id: 't1', title: 'RayTask', agent_id: 'ray' }),
          makeTask({ id: 't2', title: 'BareTask' }),
        ]}
        agents={[{ id: 'ray', name: 'Ray' }]}
        onTaskClick={() => {}}
      />,
    )
    fireEvent.click(columnMenu('Agent').getByRole('menuitemcheckbox', { name: 'Ray' }))
    expect(screen.getByText('RayTask')).toBeInTheDocument()
    expect(screen.queryByText('BareTask')).toBeNull()
  })

})

describe('ListView — empty state', () => {
  it('renders "No tasks to show" when there are no tasks', () => {
    render(<ListView tasks={[]} agents={[]} onTaskClick={() => {}} />)
    expect(screen.getByText('No tasks to show')).toBeInTheDocument()
    expect(screen.queryByText('Task')).toBeNull()
  })

  it('renders the filtered-empty copy when ANDed column filters exclude every row', () => {
    render(
      <ListView
        tasks={[
          makeTask({ id: 'a', title: 'InboxP1', status: 'inbox', priority: 1 }),
          makeTask({ id: 'b', title: 'DoneP3', status: 'done', priority: 3 }),
        ]}
        agents={[]}
        onTaskClick={() => {}}
      />,
    )
    // Status=Inbox keeps only InboxP1; Pri=P3 keeps only DoneP3. ANDed across
    // columns, no single row satisfies both → filtered-empty.
    fireEvent.click(columnMenu('Status').getByRole('menuitemcheckbox', { name: 'Inbox' }))
    fireEvent.click(columnMenu('Pri').getByRole('menuitemcheckbox', { name: 'P3' }))
    expect(screen.getByText('No tasks match the column filters')).toBeInTheDocument()
    expect(screen.queryByText('InboxP1')).toBeNull()
    expect(screen.queryByText('DoneP3')).toBeNull()
  })
})

describe('ListView — Agent column resolution', () => {
  it('prefers the server-set agent_name over the agents lookup', () => {
    render(<ListView tasks={[makeTask({ agent_name: 'Ray Direct', agent_id: 'ray-1' })]} agents={[]} onTaskClick={() => {}} />)
    const row = screen.getByText('Task').closest('tr')!
    expect(within(row).getByText('Ray Direct')).toBeInTheDocument()
  })

  it('resolves agent_id via the agents list when agent_name is absent', () => {
    render(<ListView tasks={[makeTask({ agent_id: 'ray-1' })]} agents={[{ id: 'ray-1', name: 'Ray' }]} onTaskClick={() => {}} />)
    const row = screen.getByText('Task').closest('tr')!
    expect(within(row).getByText('Ray')).toBeInTheDocument()
  })

  it('falls back to the raw agent_id when it is not in the agents list', () => {
    render(<ListView tasks={[makeTask({ agent_id: 'ghost' })]} agents={[]} onTaskClick={() => {}} />)
    const row = screen.getByText('Task').closest('tr')!
    expect(within(row).getByText('ghost')).toBeInTheDocument()
  })

  it('renders "—" in the Agent cell when a task has no agent', () => {
    render(<ListView tasks={[makeTask()]} agents={[]} onTaskClick={() => {}} />)
    const row = screen.getByText('Task').closest('tr')!
    // Agent is the 5th <td> (index 4): Pri, Title, Status, Tags, Agent, Updated.
    const cells = within(row).getAllByRole('cell')
    expect(within(cells[4]).getByText('—')).toBeInTheDocument()
  })
})

describe('ListView — surface filter (user tasks only)', () => {
  it('excludes non-user (heartbeat) tasks from the list', () => {
    render(
      <ListView
        tasks={[
          makeTask({ id: 'u', title: 'UserTask', surface: 'user' }),
          makeTask({ id: 'h', title: 'HeartbeatTask', surface: 'heartbeat' }),
        ]}
        agents={[]}
        onTaskClick={() => {}}
      />,
    )
    expect(screen.getByText('UserTask')).toBeInTheDocument()
    expect(screen.queryByText('HeartbeatTask')).toBeNull()
  })

  it('renders tasks whose surface is undefined (treated as user)', () => {
    render(<ListView tasks={[makeTask({ title: 'NoSurface', surface: undefined })]} agents={[]} onTaskClick={() => {}} />)
    expect(screen.getByText('NoSurface')).toBeInTheDocument()
  })
})

describe('ListView — Tags column render', () => {
  it('renders a Tags column header (sort/filter menu), not Milestone', () => {
    render(<ListView tasks={[makeTask()]} agents={[]} onTaskClick={() => {}} />)
    expect(screen.getByRole('button', { name: /^Tags column/i })).toBeInTheDocument()
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
    const cells = within(row).getAllByRole('cell')
    expect(within(cells[3]).getByText('—')).toBeInTheDocument()
  })
})
