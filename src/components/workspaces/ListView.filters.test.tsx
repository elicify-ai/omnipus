/**
 * ListView.filters.test.tsx
 *
 * ADR-051 flat List with Excel-style column headers: each column header is a
 * dropdown. Sortable columns (Pri / Title / Status / Agent / Updated) offer
 * sort asc/desc; columns with discrete values (Pri / Status / Tags / Agent)
 * offer a checkbox value-filter — so Tags is filter-only and Title/Updated are
 * sort-only. Filtering is column-local (AND across columns, OR within a
 * column's checklist) over the plan-scoped `tasks` prop. Covers:
 *   1. No milestone UI anywhere (SC-040).
 *   2. Column-header sort (Pri / Status / Title / Agent / Updated), default Updated desc.
 *   3. Column value-filters: Status, Pri, Tags (+ Untagged), Agent (+ Unassigned),
 *      union-within-column, present-values-only, and the Clear-filter action.
 *   4. Empty states: unfiltered ("No tasks to show") and filtered ("No tasks match…").
 *   5. Stale-value pruning when the plan scope changes.
 *   6. Agent-column resolution + the surface!=='user' filter + the Tags column render.
 *
 * The Radix DropdownMenu is stubbed inline (its pointer/portal internals don't
 * drive in jsdom — same convention as WorkspaceTasksTab.test.tsx) so each
 * column menu's items render inline; tests scope to a column via its <th>.
 *
 * Wrapped in a QueryClientProvider (ADR-052 §6.8): each row now renders a
 * TaskActionButton (▶/■), which calls `useMutation`/`useQueryClient` — those
 * hooks throw without a QueryClient in the tree, even when no test here ever
 * clicks the button.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, within, fireEvent, type RenderResult } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
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

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

/** Render ListView under a QueryClientProvider — exposes the client too so
 * `rerenderList` can reuse the SAME provider instance across a rerender. */
function renderList(ui: React.ReactElement): RenderResult & { client: QueryClient } {
  const client = makeClient()
  const utils = render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
  return { ...utils, client }
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
    renderList(<ListView tasks={[makeTask()]} agents={[]} onTaskClick={() => {}} />)
    expect(screen.queryByText(/milestone/i)).toBeNull()
  })
})

describe('ListView — column header sort', () => {
  it('defaults to newest-first (Updated desc) on initial render', () => {
    renderList(
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
    renderList(
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
    renderList(
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
    renderList(
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
    renderList(
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
    renderList(
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
    renderList(
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
    renderList(
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
    renderList(
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
    renderList(
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

    // The Unassigned bucket selects only tasks with no agent — exercises the
    // UNASSIGNED sentinel path (mirror of the Tags Untagged case above).
    fireEvent.click(columnMenu('Agent').getByRole('menuitemcheckbox', { name: 'Ray' })) // clear Ray
    fireEvent.click(columnMenu('Agent').getByRole('menuitemcheckbox', { name: 'Unassigned' }))
    expect(screen.getByText('BareTask')).toBeInTheDocument()
    expect(screen.queryByText('RayTask')).toBeNull()
  })

  it('offers only the values present in the current task set, in canonical order', () => {
    renderList(
      <ListView
        tasks={[makeTask({ id: 't1', status: 'inbox' }), makeTask({ id: 't2', status: 'done' })]}
        agents={[]}
        onTaskClick={() => {}}
      />,
    )
    const names = columnMenu('Status')
      .getAllByRole('menuitemcheckbox')
      .map((el) => el.textContent)
    // Only inbox + done are present; absent statuses aren't offered, and present
    // ones keep the canonical STATUS_ORDER order (inbox before done).
    expect(names).toEqual(['Inbox', 'Done'])
    expect(columnMenu('Status').queryByRole('menuitemcheckbox', { name: 'Failed' })).toBeNull()
  })

  it('the Clear filter action resets a column filter', () => {
    renderList(
      <ListView
        tasks={[makeTask({ id: 't1', title: 'InboxT', status: 'inbox' }), makeTask({ id: 't2', title: 'DoneT', status: 'done' })]}
        agents={[]}
        onTaskClick={() => {}}
      />,
    )
    fireEvent.click(columnMenu('Status').getByRole('menuitemcheckbox', { name: 'Inbox' }))
    expect(screen.queryByText('DoneT')).toBeNull()
    // "Clear filter" appears once a value is checked and restores every row.
    fireEvent.click(columnMenu('Status').getByRole('menuitem', { name: /clear filter/i }))
    expect(screen.getByText('InboxT')).toBeInTheDocument()
    expect(screen.getByText('DoneT')).toBeInTheDocument()
  })

  it('prunes a checked filter value once it leaves the plan scope (no stuck-empty list)', () => {
    const rayTask = makeTask({ id: 't1', title: 'RayTask', agent_id: 'ray' })
    const bareTask = makeTask({ id: 't2', title: 'BareTask' })
    const { rerender, client } = renderList(
      <ListView tasks={[rayTask, bareTask]} agents={[{ id: 'ray', name: 'Ray' }]} onTaskClick={() => {}} />,
    )
    fireEvent.click(columnMenu('Agent').getByRole('menuitemcheckbox', { name: 'Ray' }))
    expect(screen.getByText('RayTask')).toBeInTheDocument()
    expect(screen.queryByText('BareTask')).toBeNull()

    // Plan scope changes and Ray's task is gone. The stale 'Ray' key must be
    // pruned — BareTask shows, no filtered-empty copy, and Ray is no longer an
    // offered value.
    rerender(
      <QueryClientProvider client={client}>
        <ListView tasks={[bareTask]} agents={[{ id: 'ray', name: 'Ray' }]} onTaskClick={() => {}} />
      </QueryClientProvider>,
    )
    expect(screen.getByText('BareTask')).toBeInTheDocument()
    expect(screen.queryByText('No tasks match the column filters')).toBeNull()
    expect(columnMenu('Agent').queryByRole('menuitemcheckbox', { name: 'Ray' })).toBeNull()
  })

})

describe('ListView — empty state', () => {
  it('renders "No tasks to show" when there are no tasks', () => {
    renderList(<ListView tasks={[]} agents={[]} onTaskClick={() => {}} />)
    expect(screen.getByText('No tasks to show')).toBeInTheDocument()
    expect(screen.queryByText('Task')).toBeNull()
  })

  it('renders the filtered-empty copy when ANDed column filters exclude every row', () => {
    renderList(
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
    renderList(<ListView tasks={[makeTask({ agent_name: 'Ray Direct', agent_id: 'ray-1' })]} agents={[]} onTaskClick={() => {}} />)
    const row = screen.getByText('Task').closest('tr')!
    expect(within(row).getByText('Ray Direct')).toBeInTheDocument()
  })

  it('resolves agent_id via the agents list when agent_name is absent', () => {
    renderList(<ListView tasks={[makeTask({ agent_id: 'ray-1' })]} agents={[{ id: 'ray-1', name: 'Ray' }]} onTaskClick={() => {}} />)
    const row = screen.getByText('Task').closest('tr')!
    expect(within(row).getByText('Ray')).toBeInTheDocument()
  })

  it('falls back to the raw agent_id when it is not in the agents list', () => {
    renderList(<ListView tasks={[makeTask({ agent_id: 'ghost' })]} agents={[]} onTaskClick={() => {}} />)
    const row = screen.getByText('Task').closest('tr')!
    expect(within(row).getByText('ghost')).toBeInTheDocument()
  })

  it('renders "—" in the Agent cell when a task has no agent', () => {
    renderList(<ListView tasks={[makeTask()]} agents={[]} onTaskClick={() => {}} />)
    const row = screen.getByText('Task').closest('tr')!
    // Agent is the 5th <td> (index 4): Pri, Title, Status, Tags, Agent, Updated.
    const cells = within(row).getAllByRole('cell')
    expect(within(cells[4]).getByText('—')).toBeInTheDocument()
  })
})

describe('ListView — surface filter (user tasks only)', () => {
  it('excludes non-user (heartbeat) tasks from the list', () => {
    renderList(
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
    renderList(<ListView tasks={[makeTask({ title: 'NoSurface', surface: undefined })]} agents={[]} onTaskClick={() => {}} />)
    expect(screen.getByText('NoSurface')).toBeInTheDocument()
  })
})

describe('ListView — Tags column render', () => {
  it('renders a Tags column header (sort/filter menu), not Milestone', () => {
    renderList(<ListView tasks={[makeTask()]} agents={[]} onTaskClick={() => {}} />)
    expect(screen.getByRole('button', { name: /^Tags column/i })).toBeInTheDocument()
    expect(screen.queryByRole('columnheader', { name: /milestone/i })).toBeNull()
  })

  it('renders tag chips in a row, capped with a "+N" overflow indicator', () => {
    const task = makeTask({ tags: ['release', 'urgent', 'q3', 'extra'] })
    renderList(<ListView tasks={[task]} agents={[]} onTaskClick={() => {}} />)

    const row = screen.getByText('Task').closest('tr')!
    expect(within(row).getByText('release')).toBeInTheDocument()
    expect(within(row).getByText('urgent')).toBeInTheDocument()
    expect(within(row).getByText('+2')).toBeInTheDocument()
  })

  it('renders "—" in the Tags cell when a task has no tags', () => {
    renderList(<ListView tasks={[makeTask()]} agents={[]} onTaskClick={() => {}} />)
    const row = screen.getByText('Task').closest('tr')!
    const cells = within(row).getAllByRole('cell')
    expect(within(cells[3]).getByText('—')).toBeInTheDocument()
  })
})
