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
