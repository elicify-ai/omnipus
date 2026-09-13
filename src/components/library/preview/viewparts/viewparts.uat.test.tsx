// viewparts.uat.test.tsx — UAT 2026-09-13 D-68 (declared summary never
// drawn) and D-69 (calendar plots nothing, unscheduled records dropped).
// Each test fails on the pre-fix renderer.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, within, fireEvent } from '@testing-library/react'
import type { VaultFindRow, ViewResult, ViewResultPart } from '@/lib/api/generated/openapi-types'
import { CalendarPart } from './CalendarPart'
import { ViewPartsRenderer } from './ViewPartsRenderer'

function row(path: string, title: string, cells: Record<string, string>): VaultFindRow {
  return {
    path,
    title,
    cells: Object.entries(cells).map(([property, value]) => ({ property, value })),
    joins: [],
  }
}

const ROWS: VaultFindRow[] = [
  row('a.md', 'Core Platform Migration', { start: '2026-09-13', budget: '132000' }),
  row('b.md', 'Customer Portal Redesign', { start: '2026-09-13', budget: '215000' }),
  row('c.md', 'Scalar List', { budget: '' }),
  row('d.md', 'Broken FM', {}),
]

describe('UAT D-68 — a view’s own `aggregates` (the .base `summaries:`) are drawn', () => {
  it('renders each aggregate with its value and scope in one sentence beneath the parts', () => {
    const result: ViewResult = {
      view: 'projects--all-projects',
      label: 'All Projects',
      parts: [{ part: 'table', source: { part: 'table' }, columns: ['budget'] }],
      rows: ROWS,
      complete: true,
      problems: [],
      aggregates: [
        { op: 'sum', label: 'sum(budget)', value: '914,564', scope: 'over 9 of 12 evaluated rows (12 shown)' },
      ],
    }
    render(<ViewPartsRenderer result={result} />)
    // DIES ON the old renderer: `aggregates` was never read, so 914,564
    // appeared nowhere in the panel.
    const footer = screen.getByTestId('view-aggregates')
    expect(within(footer).getByText('914,564')).toBeInTheDocument()
    expect(footer.textContent).toContain('sum(budget)')
    expect(footer.textContent).toContain('over 9 of 12 evaluated rows')
  })

  it('draws nothing extra when the view declares no aggregates', () => {
    const result: ViewResult = {
      view: 'v',
      label: 'V',
      parts: [{ part: 'table', source: { part: 'table' }, columns: ['budget'] }],
      rows: ROWS,
      complete: true,
      problems: [],
    }
    render(<ViewPartsRenderer result={result} />)
    expect(screen.queryByTestId('view-aggregates')).not.toBeInTheDocument()
  })
})

describe('UAT D-69 — the calendar accounts for every row', () => {
  const datedPart: ViewResultPart = { part: 'calendar', source: { part: 'calendar', date: 'start' } }

  it('plots the dated rows and lists the rows with no date beneath the grid, with a count', () => {
    render(<CalendarPart part={datedPart} rows={ROWS} />)
    expect(screen.getAllByTestId('viewpart-calendar-event')).toHaveLength(2)
    // DIES ON the old part: the two undated rows vanished without a word.
    const strip = screen.getByTestId('viewpart-calendar-unscheduled')
    expect(strip.textContent).toContain('2 records have no start and are not on the calendar')
    expect(within(strip).getAllByTestId('viewpart-calendar-unscheduled-row')).toHaveLength(2)
    expect(within(strip).getByText('Scalar List')).toBeInTheDocument()
    expect(within(strip).getByText('Broken FM')).toBeInTheDocument()
  })

  it('opens an unscheduled row through the same row-open action as a plotted one', () => {
    const onOpenPath = vi.fn()
    render(<CalendarPart part={datedPart} rows={ROWS} onOpenPath={onOpenPath} />)
    fireEvent.click(screen.getByRole('button', { name: 'Open Scalar List' }))
    expect(onOpenPath).toHaveBeenCalledWith('c.md')
  })

  it('shows no strip when every row is dated', () => {
    render(<CalendarPart part={datedPart} rows={ROWS.slice(0, 2)} />)
    expect(screen.queryByTestId('viewpart-calendar-unscheduled')).not.toBeInTheDocument()
  })

  it('says the grid is empty because the part names no date property, and lists every row', () => {
    // The pre-fix shape of an imported `layout: calendar` view: a calendar
    // part with no `date` binding — the grid drew every month empty and
    // said nothing.
    render(<CalendarPart part={{ part: 'calendar', source: { part: 'calendar' } }} rows={ROWS} />)
    expect(screen.getByTestId('viewpart-calendar-no-date').textContent).toMatch(/names no date property/)
    expect(screen.queryAllByTestId('viewpart-calendar-event')).toHaveLength(0)
    expect(screen.getAllByTestId('viewpart-calendar-unscheduled-row')).toHaveLength(4)
  })
})

// ── UAT 2026-09-13 D-35 / D-73 / D-74 / D-76 / D-77 ─────────────────────────

describe('UAT D-35 — a declared display_name is the column heading, not the machine key', () => {
  it('prints "Days Open" for formula.days_open when the view declares it', () => {
    const result: ViewResult = {
      view: 'projects--all-projects',
      label: 'All Projects',
      parts: [{ part: 'table', source: { part: 'table' }, columns: ['file.name', 'budget', 'formula.days_open'] }],
      rows: ROWS,
      complete: true,
      problems: [],
      property_config: { 'formula.days_open': { display_name: 'Days Open' } },
    }
    render(<ViewPartsRenderer result={result} />)
    const headers = screen.getAllByRole('columnheader').map((h) => h.textContent)
    // DIES ON the old renderer: `property_config` never reached the table, so
    // the heading was the key "formula.days_open".
    expect(headers).toContain('Days Open')
    expect(headers).not.toContain('formula.days_open')
    expect(headers).toContain('budget') // an undeclared column keeps its key
  })
})

describe('UAT D-73 — a chart over dates uses a time axis, with ticks and axis titles', () => {
  const chart: ViewResultPart = {
    part: 'chart',
    source: { part: 'chart', date: 'start', number: 'budget' },
    series: [
      {
        points: [
          { key: '2026-01-01', value: '100', count: 1 },
          { key: '2026-01-15', value: '200', count: 1 },
          { key: '2026-06-01', value: '300', count: 1 },
        ],
      },
    ],
  }

  it('places a fortnight and four-and-a-half months in proportion, not one slot each', () => {
    const { container } = render(<ViewPartsRenderer result={{ view: 'v', label: 'V', parts: [chart], rows: ROWS, complete: true, problems: [] }} />)
    const cx = Array.from(container.querySelectorAll('circle')).map((c) => Number(c.getAttribute('cx')))
    expect(cx).toHaveLength(3)
    const fortnight = (cx[1] ?? 0) - (cx[0] ?? 0)
    const months = (cx[2] ?? 0) - (cx[1] ?? 0)
    // DIES ON the old renderer: both gaps were identical (82 px apart).
    expect(fortnight).toBeGreaterThan(0)
    expect(months / fortnight).toBeGreaterThan(5)
  })

  it('draws intermediate ticks and names both axes by their property', () => {
    render(<ViewPartsRenderer result={{ view: 'v', label: 'V', parts: [chart], rows: ROWS, complete: true, problems: [] }} />)
    expect(screen.getAllByTestId('viewpart-chart-x-label').length).toBeGreaterThanOrEqual(5)
    expect(screen.getByTestId('viewpart-chart-x-title').textContent).toContain('start')
    expect(screen.getByTestId('viewpart-chart-y-title').textContent).toContain('budget')
  })

  it('keeps categories one slot apart when the keys are not dates', () => {
    const cats: ViewResultPart = {
      ...chart,
      series: [{ points: [{ key: 'alpha', value: '1', count: 1 }, { key: 'beta', value: '2', count: 1 }, { key: 'gamma', value: '3', count: 1 }] }],
    }
    const { container } = render(<ViewPartsRenderer result={{ view: 'v', label: 'V', parts: [cats], rows: ROWS, complete: true, problems: [] }} />)
    const cx = Array.from(container.querySelectorAll('circle')).map((c) => Number(c.getAttribute('cx')))
    expect((cx[1] ?? 0) - (cx[0] ?? 0)).toBeCloseTo((cx[2] ?? 0) - (cx[1] ?? 0), 5)
  })
})

describe('UAT D-74 — a row with no record id says why it has no editor', () => {
  const result: ViewResult = {
    view: 'projects--all-projects',
    label: 'All Projects',
    type: 'project',
    parts: [{ part: 'table', source: { part: 'table' }, columns: ['file.name', 'budget'] }],
    rows: [
      { ...row('a.md', 'Core Platform Migration', { budget: '1' }), id: 'PRJ-0001', version_token: 'v1' },
      row('c.md', 'Scalar List', { budget: '' }),
    ],
    complete: true,
    problems: [],
  }

  it('marks the id-less row, and only that row, when the grid is editable', () => {
    render(<ViewPartsRenderer result={result} workspaceId="ws-1" />)
    const rows = screen.getAllByTestId('viewpart-table-row')
    // DIES ON the old renderer: no mark existed at all.
    expect(within(rows[1] as HTMLElement).getByTestId('viewpart-row-no-id')).toBeInTheDocument()
    expect(within(rows[0] as HTMLElement).queryByTestId('viewpart-row-no-id')).toBeNull()
    expect(within(rows[1] as HTMLElement).getByTestId('viewpart-row-no-id').getAttribute('title')).toMatch(/no record id/i)
  })

  it('shows no mark in a read-only grid, where no row has an editor anyway', () => {
    render(<ViewPartsRenderer result={result} />)
    expect(screen.queryByTestId('viewpart-row-no-id')).toBeNull()
  })
})

describe('UAT D-76 / D-77 — board columns are ordered as text, and long titles stay inside their column', () => {
  const board: ViewResultPart = { part: 'columns', source: { part: 'columns', choice: 'status' } }
  const rows = [
    row('p.md', 'Paused one', { status: 'paused' }),
    row('a.md', 'A'.repeat(203), { status: 'active' }),
    row('d.md', 'Done one', { status: 'done' }),
    row('n.md', 'No status', {}),
  ]

  it('orders the columns active, done, paused — then "not set" last', () => {
    render(<ViewPartsRenderer result={{ view: 'v', label: 'V', parts: [board], rows, complete: true, problems: [] }} />)
    const cols = screen.getAllByTestId('viewpart-board-column').map((c) => c.textContent ?? '')
    // DIES ON the old renderer: first-seen order (paused, active, done).
    expect(cols[0]).toMatch(/^active/i)
    expect(cols[1]).toMatch(/^done/i)
    expect(cols[2]).toMatch(/^paused/i)
    expect(cols[3]).toMatch(/not set/i)
  })

  it('wraps a 203-character title inside its card instead of overflowing', () => {
    render(<ViewPartsRenderer result={{ view: 'v', label: 'V', parts: [board], rows, complete: true, problems: [] }} />)
    const title = screen
      .getAllByTestId('viewpart-board-card-title')
      .find((t) => (t.textContent ?? '').length === 203)
    // DIES ON the old renderer: the title was an inline `truncate` span
    // (no effect inside a block button) with no wrapping rule.
    expect(title).toBeDefined()
    expect(title?.className).toContain('break-words')
    expect(title?.className).toContain('min-w-0')
  })
})
