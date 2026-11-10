// T5 supersedes the SP-33/SP-34 stacked Board. The real List fallback and
// 971/972/973px recovery are covered by WorkspaceTasksTab.layout.test.tsx.
// Retain the original unrelated coverage: canonical task homes, independent
// content, container ownership, all three views, and the no-search guard.
// The superseded layout assertions now pin six equal columns and a shared header.

import { describe, it, expect, vi } from 'vitest'
import { render, within, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Task } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchTasks: vi.fn(async () => []),
    fetchPlans: vi.fn(async () => []),
    fetchAgents: vi.fn(async () => []),
    fetchWorkspaceDelegation: vi.fn(async () => null),
    updateTask: vi.fn(),
    deletePlan: vi.fn(),
  }
})

// SP-32 clicks the Graph switcher entry; the Graph tab's own canvas internals
// are covered by its own test file — this pack pins WorkspaceTasksTab's view
// SEAM (which views exist and that none is dropped), so the tab is stubbed to
// a sentinel (the same boundary its own screen test draws).
vi.mock('./WorkspaceGraphTab', () => ({
  WorkspaceGraphTab: () => <div data-testid="graph-tab-sentinel">Graph Sentinel</div>,
}))

import { BoardView } from './BoardView'
import { WorkspaceTasksTab } from './WorkspaceTasksTab'

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: `t-${Math.random().toString(36).slice(2)}`,
    title: 'A task',
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

function renderBoard(tasks: Task[]) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <BoardView
        tasks={tasks}
        plans={[]}
        agents={[]}
        altitude="top-level"
        onTaskClick={vi.fn()}
        onTaskMove={vi.fn()}
        onMoveRejected={vi.fn()}
      />
    </QueryClientProvider>,
  )
}

function renderTasksTab() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <WorkspaceTasksTab workspaceId="ws-1" />
    </QueryClientProvider>,
  )
}

/** Every element whose class list contains any `@max-[971px]:` variant — the
 * measured container-query breakpoint the narrow board flips at. */
function narrowVariantElements(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll('*'))
    .filter((el) => Array.from(el.classList).some((cls) => cls.startsWith('@max-[971px]:')))
    .map((el) => el as HTMLElement)
}

// The canonical status lifecycle (src/lib/statusColors.ts STATUS_ORDER) —
// the spec's status model, the expected order's source, never the rendered
// output. COLUMNS is built straight off STATUS_ORDER with no reordering.
const CANONICAL_STATUSES = [
  { status: 'inbox', label: 'Inbox' },
  { status: 'next', label: 'Next' },
  { status: 'in_progress', label: 'In Progress' },
  { status: 'blocked', label: 'Blocked' },
  { status: 'done', label: 'Done' },
  { status: 'failed', label: 'Failed' },
] as const

function columnGroup(container: HTMLElement, label: string): HTMLElement {
  return within(container).getByRole('group', { name: `${label} column` })
}

function assertBoardContainer(mounted: ReturnType<typeof renderBoard>) {
  const root = mounted.container.firstElementChild
  expect(root, 'the actual Board root owns the container-query boundary').toHaveClass('@container')
}

describe('BoardView narrow — SP-33 mechanism: panel-width container query, not window width', () => {
  it('the board declares itself a CSS container (@container), so its breakpoints answer to PANEL width', () => {
    const mounted = renderBoard([makeTask({ title: 'Only task' })])
    assertBoardContainer(mounted)
  })

  it('instrument: removing the actual Board root container class fails the SAME assertion', () => {
    // Test-side DOM probe, not a production-code mutation or CHECK verdict.
    const mounted = renderBoard([makeTask({ title: 'Only task' })])
    expect(() => assertBoardContainer(mounted)).not.toThrow()
    mounted.container.firstElementChild?.classList.remove('@container')
    expect(() => assertBoardContainer(mounted)).toThrow(/to have class/)
  })

  it('T5 removes the old stacking variants and keeps six equal readable columns', () => {
    const mounted = renderBoard([makeTask({ title: 'Only task' })])
    expect(narrowVariantElements(mounted.container)).toEqual([])
    const columns = mounted.getAllByRole('group', { name: / column$/ })
    expect(columns).toHaveLength(6)
    for (const column of columns) expect(column).toHaveClass('flex-1', 'basis-0', 'min-w-[162px]')
  })

  it('no window-breakpoint class drives ANY board layout — the window is irrelevant (SP-33)', () => {
    // Oracle: wireframe §9A — "Resizing the browser WINDOW instead does
    // nothing here — only this frame's own width does." An `md:` (768px
    // window) class inside the board would reintroduce exactly the gap the
    // wireframe researched out of the old code.
    const mounted = renderBoard([
      makeTask({ id: 't-a', title: 'Alpha card', status: 'inbox' }),
      makeTask({ id: 't-b', title: 'Beta card', status: 'failed' }),
    ])
    const windowBreakpointEls = Array.from(mounted.container.querySelectorAll('*')).filter((el) =>
      Array.from(el.classList).some((cls) => /^md:/.test(cls)),
    )
    expect(windowBreakpointEls.map((el) => el.tagName)).toEqual([])
  })

  it('the Tasks toolbar responds to its own panel container, never a viewport layout breakpoint (SP-33)', () => {
    // SP-33 / wireframe §9 explicitly correct the earlier §2A description
    // of the old md: viewport collapse. This case was incorrectly converted
    // to an old-behaviour guard in 105e2c072; its original RED requirement is
    // restored. Child BoardView containers cannot satisfy a toolbar query.
    // Pin the toolbar's own container ancestor and container-query layout
    // classes; viewport-based grid classes cannot satisfy this requirement.
    const mounted = renderTasksTab()
    const toolbar = mounted.getByTestId('tasks-heading').parentElement?.parentElement
    expect(toolbar, 'the Tasks heading belongs to the real toolbar').not.toBeNull()
    expect(toolbar?.closest('[class~="@container"]'), 'SP-33: the toolbar has its own container').not.toBeNull()
    const classes = Array.from(toolbar?.classList ?? [])
    expect(classes.filter((cls) => /^(sm|md|lg|xl|2xl):/.test(cls)), 'SP-33: viewport width cannot drive toolbar layout').toEqual([])
    expect(classes.some((cls) => /^@/.test(cls) && cls !== '@container'), 'T1: the two-row toolbar no longer conditionally stacks').toBe(false)
  })
})

describe('BoardView narrow — SP-34: cards stacked by status, single column', () => {
  it('the board renders the six status groups as labelled columns, in canonical status order', () => {
    // Fixtures deliberately out of lifecycle order to catch an
    // insertion-order implementation; expected order comes from the spec's
    // status model (statusColors.ts canonical lifecycle), never from the
    // rendered output. The delivered narrow board keeps the six groups
    // mounted and restacks them by container query, so DOM order IS the
    // stack order.
    const mounted = renderBoard([
      makeTask({ id: 't-done', title: 'Shipped thing', status: 'done' }),
      makeTask({ id: 't-progress', title: 'Live thing', status: 'in_progress' }),
      makeTask({ id: 't-next', title: 'Ready thing', status: 'next' }),
    ])
    const groups = Array.from(mounted.container.querySelectorAll('[role="group"][aria-label$=" column"]'))
    const labels = groups.map((g) => g.getAttribute('aria-label')?.replace(/ column$/, ''))
    expect(labels).toEqual(CANONICAL_STATUSES.map((s) => s.label))
  })

  it('each status group holds exactly its own tasks — no card leaks across statuses', () => {
    const mounted = renderBoard([
      makeTask({ id: 't-done', title: 'Shipped thing', status: 'done' }),
      makeTask({ id: 't-progress', title: 'Live thing', status: 'in_progress' }),
    ])
    const inProgress = columnGroup(mounted.container, 'In Progress')
    expect(within(inProgress).getByText('Live thing')).toBeInTheDocument()
    expect(within(inProgress).queryByText('Shipped thing')).not.toBeInTheDocument()

    const done = columnGroup(mounted.container, 'Done')
    expect(within(done).getByText('Shipped thing')).toBeInTheDocument()
    expect(within(done).queryByText('Live thing')).not.toBeInTheDocument()
  })

  it('T5 keeps one shared header with exact per-status counts and no per-group header', () => {
    const mounted = renderBoard([
      makeTask({ id: 't-a', title: 'Alpha card', status: 'inbox' }),
      makeTask({ id: 't-b', title: 'Beta card', status: 'inbox' }),
    ])
    const inbox = columnGroup(mounted.container, 'Inbox')
    const sharedHeader = inbox.parentElement!.previousElementSibling as HTMLElement
    expect(sharedHeader).toHaveClass('sticky')
    expect(Array.from(sharedHeader.children).map((header) => header.textContent)).toEqual([
      'Inbox2', 'Next0', 'In Progress0', 'Blocked0', 'Done0', 'Failed0',
    ])
    for (const name of ['Next', 'In Progress', 'Blocked', 'Done', 'Failed']) {
      expect(columnGroup(mounted.container, name).children).toHaveLength(0)
    }
  })

  it('T5 keeps the columns in one horizontal row and each card has exactly ONE home', () => {
    const mounted = renderBoard([
      makeTask({ id: 't-a', title: 'Alpha card', status: 'inbox' }),
      makeTask({ id: 't-b', title: 'Beta card', status: 'failed' }),
    ])
    const row = columnGroup(mounted.container, 'Inbox').parentElement as HTMLElement
    expect(row).toHaveClass('flex')
    expect(row).not.toHaveClass('flex-col')
    expect(within(row).getByText('Alpha card')).toBeInTheDocument()
    expect(within(row).getByText('Beta card')).toBeInTheDocument()
    // T15 removes native hover titles; count actual card homes and caption text.
    const cards = Array.from(row.querySelectorAll('[role="button"]'))
    expect(cards).toHaveLength(2)
    expect(cards.map((card) => card.querySelector('p.line-clamp-2')?.textContent)).toEqual(['Alpha card', 'Beta card'])
  })
})

describe('TasksPanel content — SP-32: all three views stay available in the panel, none dropped', () => {
  // The joined registry registers WorkspaceTasksTab as the Tasks panel's
  // content (registry.tsx TasksPanelContent — the wave-3 shell slice's
  // WorkspaceScopedPanelContent wrapper hands it the workspace id). The
  // 8773803cf pack's BLOCKED-shaped import is gone because the module now
  // exists under this name; the assertions are unchanged.
  const renderPanel = () =>
    render(
      <QueryClientProvider client={makeClient()}>
        <WorkspaceTasksTab workspaceId="ws-1" />
      </QueryClientProvider>,
    )

  it('the panel content offers all three view switcher entries (Board, List, Graph)', () => {
    const mounted = renderPanel()
    expect(mounted.getByTestId('tasks-view-board')).toBeInTheDocument()
    expect(mounted.getByTestId('tasks-view-list')).toBeInTheDocument()
    expect(mounted.getByTestId('tasks-view-graph')).toBeInTheDocument()
  })

  it('switching to Graph keeps every option mounted — no view is dropped narrowed', () => {
    const mounted = renderPanel()
    fireEvent.click(mounted.getByTestId('tasks-view-graph'))
    expect(mounted.getByTestId('graph-tab-sentinel')).toBeInTheDocument()
    expect(mounted.getByTestId('tasks-view-board')).toBeInTheDocument()
    expect(mounted.getByTestId('tasks-view-list')).toBeInTheDocument()
    expect(mounted.getByTestId('tasks-view-graph')).toBeInTheDocument()
  })
})

describe('SP-42 guard — Tasks has NO free-text search control (out of scope, issue #1054)', () => {
  // GUARD, green by construction (the wireframe confirms today's code has no
  // search control): it fails if wave 3 adds search, which SP-42 explicitly
  // rules out. Labelled accordingly — this is characterization, not RED
  // verification, and is reported as such.
  it('no searchbox renders on the Tasks screen', () => {
    const mounted = renderTasksTab()
    expect(mounted.queryByRole('searchbox')).not.toBeInTheDocument()
    expect(mounted.container.textContent?.toLowerCase()).not.toContain('search tasks')
  })
})
