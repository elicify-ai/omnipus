// BoardView.narrow.test.tsx — RED pack for side-panel-shell-spec.md Wave 3,
// SP-32 / SP-33 / SP-34:
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
// RED↔GREEN DOM contracts declared by this pack (the responsive states must
// be observable in the DOM for any jsdom test to be possible at all — jsdom
// cannot evaluate CSS container queries):
//   1. Each width-responding component declares itself a CSS container: the
//      repo's established Tailwind convention is the `@container` class on
//      the wrapper (WorkspaceTabContainer.tsx, CalendarToolbar.tsx do this
//      today). This is SP-33's normative mechanism — PANEL-container
//      queries, never window media queries. (Whether the narrow stacks carry
//      window-breakpoint classes is asserted per stack below; deliberate
//      retention of `md:` for the FULL-PAGE route is not forbidden here —
//      that surface is not a narrow panel layout.)
//   2. Each width-responding root carries `data-narrow="true"|"false"`
//      reflecting the measured narrow state. With jsdom's unmeasurable
//      (0-width) container the honest narrow-default is `true`.
//   3. The narrow board renders `data-testid="board-narrow-stack"` containing
//      one `data-testid="board-narrow-group"` per status present, ordered by
//      the canonical status lifecycle (src/lib/statusColors.ts: inbox →
//      next → in_progress → blocked → done → failed).
// GREEN may renegotiate any of these at CHECK; the pack is then adjusted
// deliberately, never silently weakened.
//
// RED evidence (2026-10-04, grepped + read WorkspaceTasksTab.tsx /
// BoardView.tsx): zero `@container`/container-type in either file (the
// wireframe §9 research still holds at base fe0b68fb0); the toolbar still
// stacks via Tailwind `md:grid md:grid-cols-…` (WorkspaceTasksTab.tsx
// toolbar row) — a 768px WINDOW breakpoint, exactly the SP-33 gap; no narrow
// stack exists. Every assertion below fails against this code.
//
// Browser-truth deferred to UAT/click-test (jsdom cannot verify it): the
// flip actually firing at ≈970px of PANEL width while a WINDOW resize does
// nothing (wireframe §9A demo).

import { describe, it, expect, vi } from 'vitest'
import { render, within, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Task } from '@/lib/api'

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

import { BoardView } from './BoardView'
import { WorkspaceTasksTab } from './WorkspaceTasksTab'

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

/** The declared narrow-state seam: the component's own root element. */
function rootOf(mounted: ReturnType<typeof renderBoard>): HTMLElement {
  const root = mounted.container.firstElementChild
  expect(root, 'BoardView must render a single root element').not.toBeNull()
  return root as HTMLElement
}

describe('BoardView narrow — SP-33 mechanism: panel-width container query, not window width', () => {
  it('the board declares itself a CSS container (@container), so its breakpoints answer to PANEL width', () => {
    const mounted = renderBoard([makeTask({ title: 'Only task' })])
    const declaresContainer = Array.from(mounted.container.querySelectorAll('*')).some((el) =>
      el.classList.contains('@container'),
    )
    expect(declaresContainer).toBe(true)
  })

  it('the Tasks screen declares a CSS container for its toolbar collapse (SP-33 gap named in wireframe §9)', () => {
    const mounted = renderTasksTab()
    const declaresContainer = Array.from(mounted.container.querySelectorAll('*')).some((el) =>
      el.classList.contains('@container'),
    )
    expect(declaresContainer).toBe(true)
  })

  it('the board root exposes its measured narrow state via data-narrow', () => {
    const mounted = renderBoard([makeTask({ title: 'Only task' })])
    const root = rootOf(mounted)
    expect(root.hasAttribute('data-narrow')).toBe(true)
    expect(['true', 'false']).toContain(root.getAttribute('data-narrow'))
  })
})

describe('BoardView narrow — SP-34: cards stacked by status, single column', () => {
  it('the narrow board renders ONE stack of status groups, in canonical status order', () => {
    // Fixtures deliberately out of lifecycle order to catch an insertion-order
    // implementation; expected order comes from the spec's status model
    // (statusColors.ts canonical lifecycle), never from the rendered output.
    const mounted = renderBoard([
      makeTask({ id: 't-done', title: 'Shipped thing', status: 'done' }),
      makeTask({ id: 't-progress', title: 'Live thing', status: 'in_progress' }),
      makeTask({ id: 't-next', title: 'Ready thing', status: 'next' }),
    ])
    const stack = mounted.getByTestId('board-narrow-stack')
    const groups = within(stack).queryAllByTestId('board-narrow-group')
    const statuses = groups.map((g) => g.getAttribute('data-status'))
    expect(statuses).toEqual(['next', 'in_progress', 'done'])
  })

  it('each narrow status group holds exactly its own tasks, under a status heading', () => {
    const mounted = renderBoard([
      makeTask({ id: 't-done', title: 'Shipped thing', status: 'done' }),
      makeTask({ id: 't-progress', title: 'Live thing', status: 'in_progress' }),
    ])
    const stack = mounted.getByTestId('board-narrow-stack')
    const groups = within(stack).queryAllByTestId('board-narrow-group')
    const byStatus = new Map(groups.map((g) => [g.getAttribute('data-status'), g]))

    const inProgress = byStatus.get('in_progress')
    expect(inProgress).toBeDefined()
    expect(within(inProgress as HTMLElement).getByText('Live thing')).toBeInTheDocument()
    expect(within(inProgress as HTMLElement).queryByText('Shipped thing')).not.toBeInTheDocument()
    // The group is labelled by its status (a single column must stay readable).
    expect((inProgress as HTMLElement).textContent).toContain('In progress')

    const done = byStatus.get('done')
    expect(done).toBeDefined()
    expect(within(done as HTMLElement).getByText('Shipped thing')).toBeInTheDocument()
    expect(within(done as HTMLElement).queryByText('Live thing')).not.toBeInTheDocument()
  })

  it('every task card lives inside the one narrow stack — the single column is the only card home', () => {
    const mounted = renderBoard([
      makeTask({ id: 't-a', title: 'Alpha card', status: 'inbox' }),
      makeTask({ id: 't-b', title: 'Beta card', status: 'failed' }),
    ])
    const stack = mounted.getByTestId('board-narrow-stack')
    expect(within(stack).getByText('Alpha card')).toBeInTheDocument()
    expect(within(stack).getByText('Beta card')).toBeInTheDocument()
  })

  it('the narrow stack is laid out by the CONTAINER, not by window-breakpoint classes (SP-33)', () => {
    const mounted = renderBoard([
      makeTask({ id: 't-a', title: 'Alpha card', status: 'inbox' }),
    ])
    const stack = mounted.getByTestId('board-narrow-stack')
    const usesWindowBreakpoint = Array.from(stack.querySelectorAll('*')).some((el) =>
      Array.from(el.classList).some((cls) => /^md:/.test(cls)),
    )
    expect(usesWindowBreakpoint).toBe(false)
  })
})

describe('TasksPanel — SP-32: all three views stay available in the panel, none dropped', () => {
  // The wave-3 Tasks PANEL content is new surface (the docked counterpart of
  // WorkspaceTasksTab). Contract path declared by this pack — mirrors how
  // LibraryPanel is the Library panel's content component.
  // A VARIABLE specifier keeps Vite's import-analysis from resolving (and
  // failing the whole FILE at transform time) while the module is missing —
  // the failure must surface at RUN time as this pack's own BLOCKED error
  // (RED discipline: a missing implementation fails loudly and names itself,
  // it never skips).
  const TASKS_PANEL_PATH = '@/components/workspaces/' + 'TasksPanel'
  const importTasksPanel = async () => {
    try {
      return await import(TASKS_PANEL_PATH)
    } catch {
      throw new Error(
        'BLOCKED: src/components/workspaces/TasksPanel.tsx not implemented — required by SP-6/SP-32 (§10 Wave 3: Tasks becomes a panel)',
      )
    }
  }

  const renderPanel = async () => {
    const { TasksPanel } = await importTasksPanel()
    return render(
      <QueryClientProvider client={makeClient()}>
        <TasksPanel
          context={{ workspaceId: 'ws-1' }}
          presentation="docked"
          close={() => {}}
          expand={() => {}}
          registerExpandContext={() => {}}
          onWidthSettle={() => {}}
        />
      </QueryClientProvider>,
    )
  }

  it('the panel content offers all three view switcher entries (Board, List, Graph)', async () => {
    const mounted = await renderPanel()
    expect(mounted.getByTestId('tasks-view-board')).toBeInTheDocument()
    expect(mounted.getByTestId('tasks-view-list')).toBeInTheDocument()
    expect(mounted.getByTestId('tasks-view-graph')).toBeInTheDocument()
  })

  it('switching to Graph keeps every option mounted — no view is dropped narrowed', async () => {
    const mounted = await renderPanel()
    fireEvent.click(mounted.getByTestId('tasks-view-graph'))
    expect(mounted.getByTestId('tasks-view-board')).toBeInTheDocument()
    expect(mounted.getByTestId('tasks-view-list')).toBeInTheDocument()
    expect(mounted.getByTestId('tasks-view-graph')).toBeInTheDocument()
  })
})

describe('SP-42 guard — Tasks has NO free-text search control (out of scope, issue #1054)', () => {
  // GUARD, green by construction today (the wireframe confirms today's code
  // has no search control): it fails if wave 3 adds search, which SP-42
  // explicitly rules out. Labelled accordingly — this is characterization,
  // not RED verification, and is reported as such.
  it('no searchbox renders on the Tasks screen', () => {
    const mounted = renderTasksTab()
    expect(mounted.queryByRole('searchbox')).not.toBeInTheDocument()
    expect(mounted.container.textContent?.toLowerCase()).not.toContain('search tasks')
  })
})
