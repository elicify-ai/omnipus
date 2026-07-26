/**
 * BoardView.laneScroll.test.tsx
 *
 * UAT Finding 3: the board had ONE shared vertical scroller for the whole
 * row of status columns (measured `scrollHeight 4089 / clientHeight 583`,
 * 7.0x, on a SINGLE element) — the tallest lane (Inbox, 34 cards) set the
 * scroll range for every column, so past ~1,200px of scroll the other
 * columns went entirely blank while their headers still showed their real
 * counts, and an Inbox card and a Done card could never be on screen at the
 * same time.
 *
 * Fix: the outer container went from `overflow-auto` (both axes, wrapping a
 * `min-h-full`-only inner div) to `overflow-x-auto overflow-y-hidden` (this
 * level now ONLY ever scrolls horizontally, for narrow viewports/many
 * columns), and each `StatusColumn` gained its own `overflow-y-auto` bounded
 * by the row's stretched height (`min-h-0` on the row + the column) — so
 * each lane scrolls independently and its own header count stays meaningful
 * no matter how far a neighboring lane has been scrolled.
 *
 * jsdom has no real layout engine, so `scrollHeight`/`clientHeight` are both
 * always 0 here regardless of content — this can't reproduce the tester's
 * literal 7.0x ratio. What IS verifiable: the CSS wiring that produces
 * independent per-lane scroll containers (rather than one shared one) is
 * actually in place — one `overflow-y-auto` scroll container PER column,
 * not one around the whole row.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BoardView } from './BoardView'
import type { Task, Plan } from '@/lib/api'
import { STATUS_ORDER } from '@/lib/statusColors'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSubtasks: vi.fn().mockResolvedValue([]),
    tasksQueryKeys: {
      list: (p: unknown) => ['tasks', p],
      subtasks: (id: string) => ['tasks', id, 'subtasks'],
    },
  }
})

const baseTask = (overrides: Partial<Task> = {}): Task => ({
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
  updated_at: '2026-06-20T10:00:00Z',
  ...overrides,
})

const plans: Plan[] = []

function renderBoard(tasks: Task[]) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <BoardView
        tasks={tasks}
        plans={plans}
        agents={[]}
        altitude="top-level"
        onTaskClick={vi.fn()}
      />
    </QueryClientProvider>,
  )
}

describe('BoardView — independent per-lane vertical scroll (UAT Finding 3)', () => {
  it('each status column is its OWN overflow-y-auto scroll container (not one shared scroller)', () => {
    renderBoard([baseTask()])
    const columns = screen.getAllByLabelText(/column$/i)
    expect(columns.length).toBe(STATUS_ORDER.length)
    for (const col of columns) {
      expect(col).toHaveClass('overflow-y-auto')
      // `min-h-0` is what lets the column take the ROW's stretched height
      // (bounded) instead of growing with its own card count — without it,
      // `overflow-y-auto` has nothing finite to bound against.
      expect(col).toHaveClass('min-h-0')
    }
  })

  it('the outer container no longer shares ONE vertical scroller across all lanes (overflow-y-hidden, horizontal-only)', () => {
    const { container } = renderBoard([baseTask()])
    // The scroll wrapper is the direct parent of the `min-w-max` row wrapper.
    const rowWrapper = container.querySelector('.min-w-max')
    const scrollContainer = rowWrapper?.parentElement
    expect(scrollContainer).not.toBeNull()
    expect(scrollContainer).toHaveClass('overflow-x-auto')
    expect(scrollContainer).toHaveClass('overflow-y-hidden')
  })

  it('a lane with many cards does not blank out its sibling lanes — every column renders its own tasks independently', () => {
    const manyInbox = Array.from({ length: 34 }, (_, i) => baseTask({ id: `inbox-${i}`, title: `Inbox task ${i}` }))
    const doneTask = baseTask({ id: 'done-1', title: 'Finished thing', status: 'done' })
    renderBoard([...manyInbox, doneTask])

    // All 34 Inbox cards AND the one Done card are in the DOM at once (each
    // column scrolls independently — nothing is virtualized/hidden because a
    // sibling lane is "tall"), matching the UAT requirement that an Inbox
    // card and a Done card must be simultaneously visible.
    expect(screen.getAllByText(/^Inbox task \d+$/)).toHaveLength(34)
    expect(screen.getByText('Finished thing')).toBeInTheDocument()
  })
})

describe('BoardView — bounded-height chain (min-h-0 propagates row -> column, UAT Finding 3)', () => {
  // The test above only ever checked the COLUMN's own `min-h-0`
  // (BoardView.tsx:509). Mutation-testing review found that deleting
  // `min-h-0` from the columns ROW at :430 — the link that makes the
  // column's `min-h-0` meaningful in the first place — left every existing
  // assertion in this file passing, even though an unbounded row means the
  // row grows to its content's full height and the column's own
  // `overflow-y-auto` never has anything finite to bound against (lanes
  // would regrow to content height instead of scrolling independently).
  // This asserts every link of the chain BoardView.tsx documents in its own
  // comments: :264 (screen root) -> :287 (horizontal-scroll wrapper) -> :288
  // (row wrapper, `h-full`) -> :430 (columns row) -> :509 (each column).
  it('every link in the :264 -> :287 -> :288 -> :430 -> :509 bounded-height chain carries its required flex/min-h-0 wiring', () => {
    const { container } = renderBoard([baseTask()])

    // :264 — screen root: bounds itself to the page's remaining height.
    const root = container.firstElementChild
    expect(root).not.toBeNull()
    expect(root).toHaveClass('flex-1')
    expect(root).toHaveClass('min-h-0')
    expect(root).toHaveClass('overflow-hidden')

    // :287 — horizontal-scroll wrapper: bounded by the root's min-h-0, so it
    // never grows to the row wrapper's content height.
    const rowWrapperEl = container.querySelector('.min-w-max')
    const scrollContainer = rowWrapperEl?.parentElement
    expect(scrollContainer).not.toBeNull()
    expect(scrollContainer).toHaveClass('flex-1')
    expect(scrollContainer).toHaveClass('min-h-0')

    // :288 — row wrapper: stretches to the FULL bounded height handed down
    // by :287 — `h-full` is only meaningful because its parent is bounded.
    expect(rowWrapperEl).toHaveClass('h-full')

    // :430 — the columns ROW (StatusColumnsRow) — the exact link the
    // mutation-testing review found unguarded. Without `min-h-0` here, the
    // row (and therefore every column inside it) grows with content instead
    // of being bounded by the wrapper above, regardless of the column's own
    // `min-h-0`/`overflow-y-auto`.
    const columns = screen.getAllByLabelText(/column$/i)
    const row = columns[0].parentElement
    expect(row).not.toBeNull()
    expect(row).toHaveClass('flex-1')
    expect(row).toHaveClass('min-h-0')

    // :509 — each column: bounded by the row above, scrolls independently.
    for (const col of columns) {
      expect(col).toHaveClass('flex-1')
      expect(col).toHaveClass('min-h-0')
      expect(col).toHaveClass('overflow-y-auto')
    }
  })
})
