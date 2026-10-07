// BoardView.narrow.test.tsx — wave-3 join pack for side-panel-shell-spec.md
// Wave 3, SP-32 / SP-33 / SP-34:
//
//   SP-32 "Tasks keeps ALL THREE views (Board, List, Graph) narrowed — none
//          dropped in the panel."
//   SP-33 "Panel-width container queries (not window width); breakpoints
//          measured from real content (full Board ≈970px)."
//   SP-34 "Board narrow = cards stacked by status, single column."
//
// Oracle sources: spec §10 Wave 3 table, §13 SP-32..SP-34, wireframe §2F
// ("Board — stacked cards grouped by status, single column") and §9A ("the
// layout flips to the stacked narrow treatment exactly at 970px ... Resizing
// the browser WINDOW instead does nothing here — only this frame's own width
// does").
//
// JOIN STATUS (2026-10-05, work/side-panel-wave3-join-20261005) — the
// delivered narrow board renegotiated the 8773803cf pack's DOM seams, as
// that pack's header authorised for GREEN ("the pack is then adjusted
// deliberately, never silently weakened"). What changed and why, per seam:
//
//   1. `data-narrow` seam → NOT delivered. The narrow state is expressed the
//      way the browser itself decides it: real CSS container queries on the
//      component's own `@container` root, at the MEASURED breakpoint
//      `@max-[971px]` (the board's documented 6-column × 162px minimum
//      derivation — the spec's "≈970px, measured from real content"). jsdom
//      cannot evaluate container queries either way, so the class MECHANISM
//      is what a unit test can pin; the pixel flip itself stays a UAT
//      click-test row (unchanged from the RED pack's own deferred-truth note).
//   2. `board-narrow-stack` / `board-narrow-group` seams → NOT delivered.
//      The delivered narrow board does not swap to a separate stack: the six
//      status columns STAY MOUNTED as role="group" elements and the CONTAINER
//      QUERY restacks them into the single column, giving each group its own
//      narrow header band (label + count) while the wide shared header row
//      yields. The spec's observable content contract — groups in canonical
//      status order, each holding exactly its own tasks, one card home — is
//      asserted against those always-mounted groups, so the expectations are
//      unchanged and only the locators moved.
//   3. TasksPanel module path → the joined registry (registry.tsx
//      TasksPanelContent) registers WorkspaceTasksTab as the Tasks panel
//      content — no separate TasksPanel component exists. SP-32's "all three
//      views stay available" is asserted against that registered content.
//
// Browser-truth deferred to UAT/click-test (jsdom cannot verify it): the
// flip actually firing at ≈970px of PANEL width while a WINDOW resize does
// nothing (wireframe §9A demo), and the stacked column's visual layout.

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

  it('the narrow flip is expressed at the MEASURED container breakpoint (@max-[971px]) — board, header and groups all carry it', () => {
    // Oracle: SP-33 — "breakpoints measured from real content (full Board
    // ≈970px)": 6 status columns × 162px minimum = 972px, so the stacked
    // treatment fires while the container is ≤971px. The scroller, the
    // shared header row, the columns row AND every group must all key off
    // that one measured breakpoint (a second, different number would be an
    // unmeasured guess — exactly what SP-33 forbids).
    const mounted = renderBoard([makeTask({ title: 'Only task' })])
    const carriers = narrowVariantElements(mounted.container)
    expect(carriers.length).toBeGreaterThanOrEqual(4)
    // Every carrier uses the same measured breakpoint — no competing one.
    const foreignBreakpoints = carriers.flatMap((el) =>
      Array.from(el.classList)
        .filter((cls) => cls.startsWith('@max-[') && !cls.startsWith('@max-[971px]'))
        .map((cls) => cls),
    )
    expect(foreignBreakpoints).toEqual([])
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
    expect(classes.some((cls) => /^@/.test(cls) && cls !== '@container'), 'the toolbar uses a container-query layout variant').toBe(true)
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

  it('each group carries its own narrow header band (status label + live count); the shared wide header yields to it', () => {
    // Oracle: the narrow board must stay readable as a SINGLE column — each
    // stacked group is labelled by its status (spec §2F wireframe frame)
    // with its own count, while the wide board's shared sticky header row
    // (meaningless above a one-column stack) is the element that stands down.
    const mounted = renderBoard([
      makeTask({ id: 't-a', title: 'Alpha card', status: 'inbox' }),
      makeTask({ id: 't-b', title: 'Beta card', status: 'inbox' }),
    ])
    const inbox = columnGroup(mounted.container, 'Inbox')
    const header = inbox.firstElementChild as HTMLElement
    expect(header.className).toContain('hidden')
    expect(header.className).toContain('@max-[971px]:flex')
    expect(header.textContent).toContain('Inbox')
    expect(header.textContent).toContain('2')

    const sharedHeader = Array.from(mounted.container.querySelectorAll('*')).find((el) =>
      Array.from(el.classList).includes('@max-[971px]:hidden'),
    )
    expect(sharedHeader, 'the wide shared header row yields in the narrow stack').toBeDefined()
  })

  it('the columns row is the element the container query stacks (flex-col at the measured breakpoint) and each card has exactly ONE home', () => {
    const mounted = renderBoard([
      makeTask({ id: 't-a', title: 'Alpha card', status: 'inbox' }),
      makeTask({ id: 't-b', title: 'Beta card', status: 'failed' }),
    ])
    const row = Array.from(mounted.container.querySelectorAll('*')).find((el) =>
      Array.from(el.classList).includes('@max-[971px]:flex-col'),
    )
    expect(row, 'the columns row carries the single-column stacking').toBeDefined()
    expect(within(row as HTMLElement).getByText('Alpha card')).toBeInTheDocument()
    expect(within(row as HTMLElement).getByText('Beta card')).toBeInTheDocument()
    // The single column is the only card home: neither title occurs twice.
    expect(Array.from(mounted.container.querySelectorAll('*')).filter((el) => el.textContent === 'Alpha card')).toHaveLength(1)
    expect(Array.from(mounted.container.querySelectorAll('*')).filter((el) => el.textContent === 'Beta card')).toHaveLength(1)
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
