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
    // Regression guard: the OLD single-shared-scroller class combination
    // must not reappear on this element.
    expect(scrollContainer?.className).not.toMatch(/(?<!overflow-x-)\boverflow-auto\b/)
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
