// viewparts.test.tsx — one render test per view part, plus the two states
// the frame owns (refusal, empty) — view-kinds-design-2026-09-03 §3, §7.
//
// THE ORACLE IS THE DESIGN'S GATE RULES, not the components:
//
//   G2 — a number with a companion unit totals once per unit value, never
//   across units. The two-unit fixture's per-unit sums are 20,680.00 SGD
//   and 6,750.00 EUR; their arithmetic combinations (27,430.00; with the
//   excluded row, 31,630.00) are computed here BY HAND and asserted ABSENT
//   from the rendered output, everywhere a total can appear.
//
//   G3 — a row whose unit is missing is SHOWN, excluded from every total,
//   and counted. The fixture's unit-less row (4,200.00, no currency cell)
//   must render in the table AND the excluded counter line must show.
//
// Fixture values mirror the wireframe's accounting view so a human can
// cross-check the numbers against the visual spec.

import { describe, it, expect } from 'vitest'
import { render, screen, within, fireEvent } from '@testing-library/react'
import type {
  VaultFindRow,
  ViewResult,
  ViewResultPart,
} from '@/lib/api/generated/openapi-types'

import { TablePart } from './TablePart'
import { ListPart } from './ListPart'
import { TilesPart } from './TilesPart'
import { ColumnsPart } from './ColumnsPart'
import { CalendarPart } from './CalendarPart'
import { FiguresPart } from './FiguresPart'
import { ChartPart } from './ChartPart'
import { CrosstabPart } from './CrosstabPart'
import { ViewPartsRenderer } from './ViewPartsRenderer'

// ── Fixtures ────────────────────────────────────────────────────────────────

function row(path: string, title: string, cells: Record<string, string>): VaultFindRow {
  return {
    path,
    title,
    cells: Object.entries(cells).map(([property, value]) => ({ property, value })),
    joins: [],
  }
}

// The two-unit row set: 2×SGD, 1×EUR, 1 with NO currency cell (G3).
const ROWS: VaultFindRow[] = [
  row('inv-041.md', 'INV-2026-041', { client: 'HAVI', amount: '12480.00', currency: 'SGD', due_date: '2026-05-05', status: 'Sent' }),
  row('inv-038.md', 'INV-2026-038', { client: 'HAVI', amount: '8200.00', currency: 'SGD', due_date: '2026-05-12', status: 'Overdue' }),
  row('inv-039.md', 'INV-2026-039', { client: 'Nordwind', amount: '6750.00', currency: 'EUR', due_date: '2026-05-10', status: 'Sent' }),
  row('inv-042.md', 'INV-2026-042', { client: 'Aspire', amount: '4200.00', due_date: '2026-05-20', status: 'Sent' }),
]

// Hand-computed combinations that must NEVER render (G2):
const COMBINED_TWO_UNITS = '27,430.00' // 20,680 + 6,750
const COMBINED_WITH_EXCLUDED = '31,630.00' // + the unit-less 4,200
const FORBIDDEN_TOTALS = [COMBINED_TWO_UNITS, COMBINED_WITH_EXCLUDED, '27430.00', '31630.00']

const EXCLUDED_REASON = '1 row has no confirmed currency value and is excluded from every total (G3)'

const UNIT_TOTALS = [
  { property: 'amount', op: 'sum' as const, unit: 'SGD', value: '20680.00', count: 2 },
  { property: 'amount', op: 'sum' as const, unit: 'EUR', value: '6750.00', count: 1 },
]

function tablePart(over: Partial<ViewResultPart> = {}): ViewResultPart {
  return {
    part: 'table',
    source: {
      part: 'table',
      number: 'amount',
      unit: 'currency',
      grouping: [{ property: 'client' }],
      subtotals: { amount: 'sum' },
    },
    columns: ['file.name', 'client', 'due_date', 'status', 'amount'],
    groups: [
      {
        key: 'HAVI',
        count: 2,
        paths: ['inv-041.md', 'inv-038.md'],
        subtotals: [{ property: 'amount', op: 'sum', unit: 'SGD', value: '20680.00', count: 2 }],
      },
      {
        key: 'Nordwind',
        count: 1,
        paths: ['inv-039.md'],
        subtotals: [{ property: 'amount', op: 'sum', unit: 'EUR', value: '6750.00', count: 1 }],
      },
      {
        key: 'Aspire',
        count: 1,
        paths: ['inv-042.md'],
        subtotals: [],
        excluded_count: 1,
        excluded_reason: '1 row has no confirmed currency value and is excluded from every subtotal',
      },
    ],
    totals: UNIT_TOTALS,
    excluded_count: 1,
    excluded_reason: EXCLUDED_REASON,
    ...over,
  }
}

function assertNoCombinedTotal(container: HTMLElement) {
  for (const forbidden of FORBIDDEN_TOTALS) {
    expect(container.textContent).not.toContain(forbidden)
  }
}

// ── table ───────────────────────────────────────────────────────────────────

describe('TablePart', () => {
  it('renders group headers, rows, per-group per-unit subtotals, per-unit footer — and NO combined total (G2), with the excluded counter (G3)', () => {
    const { container } = render(<TablePart part={tablePart()} rows={ROWS} />)

    // Group header rows, in group order.
    const headers = screen.getAllByTestId('viewpart-group-header')
    expect(headers.map((h) => h.textContent)).toEqual([
      expect.stringContaining('HAVI'),
      expect.stringContaining('Nordwind'),
      expect.stringContaining('Aspire'),
    ])

    // Every row is SHOWN — including the unit-less one (G3 "shown").
    expect(screen.getAllByTestId('viewpart-table-row')).toHaveLength(4)
    expect(container.textContent).toContain('INV-2026-042')
    expect(container.textContent).toContain('4,200.00')

    // Per-group subtotal rows carry the unit.
    const subtotals = screen.getAllByTestId('viewpart-group-subtotal')
    expect(subtotals[0]?.textContent).toContain('SGD')
    expect(subtotals[0]?.textContent).toContain('20,680.00')
    expect(subtotals[1]?.textContent).toContain('EUR')
    expect(subtotals[1]?.textContent).toContain('6,750.00')

    // Footer: one total per unit, the G2 reason line, the G3 counter.
    const footer = screen.getByTestId('viewpart-totals-footer')
    expect(footer.textContent).toContain('SGD')
    expect(footer.textContent).toContain('20,680.00')
    expect(footer.textContent).toContain('EUR')
    expect(footer.textContent).toContain('6,750.00')
    expect(screen.getByTestId('viewpart-no-grand-total').textContent).toContain('No grand total')
    expect(screen.getByTestId('viewpart-excluded-line').textContent).toContain(EXCLUDED_REASON)

    // The heart of G2: no combined figure anywhere in the rendered output.
    assertNoCombinedTotal(container)

    // The unit-less row carries the warn mark (G3 "shown, excluded, marked").
    expect(screen.getAllByTestId('viewpart-excluded-mark').length).toBeGreaterThanOrEqual(1)
  })

  it('draws the unit inside the number cell and drops the unit property column (§5)', () => {
    render(
      <TablePart
        part={tablePart({ columns: ['file.name', 'client', 'amount', 'currency'] })}
        rows={ROWS}
      />,
    )
    const table = screen.getByTestId('viewpart-table')
    const headerCells = within(table).getAllByRole('columnheader').map((th) => th.textContent)
    expect(headerCells).not.toContain('currency')
    // First SGD row renders "12,480.00" with its unit alongside.
    const firstRow = screen.getAllByTestId('viewpart-table-row')[0]
    expect(firstRow?.textContent).toContain('12,480.00')
    expect(firstRow?.textContent).toContain('SGD')
  })

  // code-review finding #3(b) — `part.columns ?? [FILE_NAME_PROPERTY]` only
  // falls back for `undefined`; a part whose columns list is the EMPTY ARRAY
  // (a part that "declares no properties") sails straight through with
  // `columns.length === 0`, which makes the group-header row's
  // `colSpan={columns.length}` zero and the subtotal label's
  // `colSpan={columns.length - 1}` NEGATIVE — both invalid HTML that a real
  // browser silently clamps, hiding the underlying bug.
  it('never renders an invalid (zero or negative) colSpan when the part declares no properties', () => {
    const part = tablePart({ columns: [] })
    render(<TablePart part={part} rows={ROWS} />)
    const table = screen.getByTestId('viewpart-table')

    // The columns ladder's fallback must trigger for an EMPTY list exactly
    // as it already does for `undefined` — at least one column renders.
    expect(within(table).getAllByRole('columnheader').length).toBeGreaterThanOrEqual(1)

    // Every rendered colSpan (group headers, subtotal labels) must be a
    // valid, positive integer.
    const spanned = table.querySelectorAll('[colspan]')
    expect(spanned.length).toBeGreaterThan(0)
    spanned.forEach((td) => {
      expect(Number(td.getAttribute('colspan'))).toBeGreaterThanOrEqual(1)
    })

    // The subtotal content itself must still be findable — a broken colSpan
    // must never take the numbers down with it.
    const subtotals = screen.getAllByTestId('viewpart-group-subtotal')
    expect(subtotals[0]?.textContent).toContain('20,680.00')
  })
})

// ── list ────────────────────────────────────────────────────────────────────

describe('ListPart', () => {
  it('renders name + one detail per row, with the shared totals footer', () => {
    const part: ViewResultPart = {
      part: 'list',
      source: { part: 'list', number: 'amount', unit: 'currency' },
      columns: ['file.name', 'client'],
      totals: UNIT_TOTALS,
      excluded_count: 1,
      excluded_reason: EXCLUDED_REASON,
    }
    const { container } = render(<ListPart part={part} rows={ROWS} />)
    const items = screen.getAllByTestId('viewpart-list-row')
    expect(items).toHaveLength(4)
    expect(items[0]?.textContent).toContain('INV-2026-041')
    expect(items[0]?.textContent).toContain('HAVI')
    expect(screen.getByTestId('viewpart-excluded-line')).toBeInTheDocument()
    assertNoCombinedTotal(container)
  })
})

// ── tiles ───────────────────────────────────────────────────────────────────

describe('TilesPart', () => {
  it('renders one tile per row with an honest placeholder when no image can be resolved', () => {
    const part: ViewResultPart = {
      part: 'tiles',
      source: { part: 'tiles', image: 'cover' },
    }
    const rows = [
      row('havi.md', 'HAVI', { cover: 'img/havi.png' }),
      row('kestrel.md', 'Kestrel Partners', {}),
    ]
    render(<TilesPart part={part} rows={rows} />)
    expect(screen.getAllByTestId('viewpart-tile')).toHaveLength(2)
    const placeholders = screen.getAllByTestId('viewpart-tile-placeholder')
    expect(placeholders).toHaveLength(2) // no resolver passed → both placeholders
    expect(placeholders[0]?.textContent).toContain('havi.png')
    expect(placeholders[1]?.textContent).toContain('No image')
  })

  it('renders the resolved image when a resolver is provided', () => {
    const part: ViewResultPart = { part: 'tiles', source: { part: 'tiles', image: 'cover' } }
    const rows = [row('havi.md', 'HAVI', { cover: 'img/havi.png' })]
    const { container } = render(
      <TilesPart part={part} rows={rows} resolveImageUrl={(p) => `/dl/${p}`} />,
    )
    expect(container.querySelector('img')?.getAttribute('src')).toBe('/dl/img/havi.png')
  })
})

// ── columns (board) ─────────────────────────────────────────────────────────

describe('ColumnsPart', () => {
  it('renders one read-only column per choice value with counts, unassigned rows in a Not set column', () => {
    const part: ViewResultPart = {
      part: 'columns',
      source: { part: 'columns', choice: 'status', number: 'amount', unit: 'currency' },
    }
    const rows = [
      row('a.md', 'INV-A', { status: 'Sent', amount: '100.00', currency: 'SGD' }),
      row('b.md', 'INV-B', { status: 'Overdue', amount: '200.00', currency: 'SGD' }),
      row('c.md', 'INV-C', { status: 'Sent', amount: '300.00', currency: 'SGD' }),
      row('d.md', 'INV-D', { amount: '50.00', currency: 'SGD' }),
    ]
    render(<ColumnsPart part={part} rows={rows} />)
    const cols = screen.getAllByTestId('viewpart-board-column')
    expect(cols).toHaveLength(3) // Sent, Overdue, Not set
    expect(cols[0]?.textContent).toContain('Sent')
    expect(within(cols[0] as HTMLElement).getAllByTestId('viewpart-board-card')).toHaveLength(2)
    expect(cols[2]?.textContent).toContain('Not set')
    // Read-only: no draggable attribute, no button role on cards.
    expect(screen.queryAllByRole('button')).toHaveLength(0)
  })
})

// ── calendar ────────────────────────────────────────────────────────────────

describe('CalendarPart', () => {
  it('renders a month grid opening on the earliest dated row, events on their days', () => {
    const part: ViewResultPart = {
      part: 'calendar',
      source: { part: 'calendar', date: 'due_date' },
    }
    render(<CalendarPart part={part} rows={ROWS} />)
    // Earliest date in the fixture is 2026-05-05 → May 2026.
    expect(screen.getByTestId('viewpart-calendar-month').textContent).toContain('May 2026')
    const events = screen.getAllByTestId('viewpart-calendar-event')
    expect(events.map((e) => e.textContent)).toEqual(
      expect.arrayContaining(['INV-2026-041', 'INV-2026-038', 'INV-2026-039', 'INV-2026-042']),
    )
    // A month grid of full weeks: cell count is a multiple of 7.
    const days =
      screen.getAllByTestId('viewpart-calendar-day').length +
      screen.queryAllByTestId('viewpart-calendar-day-outside').length
    expect(days % 7).toBe(0)
  })

  // code-review finding #3(a) — the visible month is seeded from
  // `useState(initialMonth)`, whose argument React only reads on the FIRST
  // render. When the SAME CalendarPart instance later receives entirely
  // different rows (e.g. BasePreview swapping to a different view tab, which
  // keeps the same `key` in ViewPartsRenderer), `initialMonth` recomputes to
  // the new dataset's earliest month but `visible` state does not follow —
  // the grid keeps showing the OLD dataset's month forever. It must instead
  // reset once the earliest month of the underlying data actually changes.
  it('follows the data to a new initial month when the underlying rows change, not just the initial mount', () => {
    const mayPart: ViewResultPart = { part: 'calendar', source: { part: 'calendar', date: 'due_date' } }
    const { rerender } = render(<CalendarPart part={mayPart} rows={ROWS} />)
    expect(screen.getByTestId('viewpart-calendar-month').textContent).toContain('May 2026')

    // User pages forward within the same dataset — legitimate client state.
    fireEvent.click(screen.getByRole('button', { name: 'Next month' }))
    expect(screen.getByTestId('viewpart-calendar-month').textContent).toContain('June 2026')

    // The underlying dataset itself changes (e.g. a different view tab) —
    // a completely different row set, earliest date in August 2026.
    const augustRows: VaultFindRow[] = [row('aug-01.md', 'AUG-ONE', { due_date: '2026-08-10' })]
    rerender(<CalendarPart part={mayPart} rows={augustRows} />)
    expect(screen.getByTestId('viewpart-calendar-month').textContent).toContain('August 2026')
  })

  // code-review finding #3(d) — when the server already grouped the part's
  // rows (ViewResultPart.groups), the renderer must consume that grouping
  // rather than re-deriving day membership itself by rescanning every row's
  // raw date cell a second time. Proven by a case where the two disagree:
  // two rows share May 5's date cell, but the server's own group for that
  // day names only one of them (e.g. the other was excluded upstream for a
  // reason the raw date cell alone can't express) — the group's membership
  // must win over an independent rescan of the raw rows.
  it('consumes the server-provided groups for day membership instead of re-deriving them from raw rows', () => {
    const sameDayRows: VaultFindRow[] = [
      row('a.md', 'EVENT-A', { due_date: '2026-05-05' }),
      row('b.md', 'EVENT-B', { due_date: '2026-05-05' }),
    ]
    const partWithGroups: ViewResultPart = {
      part: 'calendar',
      source: { part: 'calendar', date: 'due_date' },
      groups: [{ key: '2026-05-05', count: 1, paths: ['a.md'], subtotals: [] }],
    }
    render(<CalendarPart part={partWithGroups} rows={sameDayRows} />)
    const events = screen.getAllByTestId('viewpart-calendar-event')
    expect(events.map((e) => e.textContent)).toEqual(['EVENT-A'])
  })
})

// ── figures ─────────────────────────────────────────────────────────────────

describe('FiguresPart', () => {
  it('renders one headline figure per (property, op, unit) and never a combined figure (G2), with the G3 counter', () => {
    const part: ViewResultPart = {
      part: 'figures',
      source: { part: 'figures', number: 'amount', unit: 'currency', aggregate: 'sum' },
      totals: UNIT_TOTALS,
      excluded_count: 1,
      excluded_reason: EXCLUDED_REASON,
    }
    const { container } = render(<FiguresPart part={part} />)
    const figures = screen.getAllByTestId('viewpart-figure')
    expect(figures).toHaveLength(2)
    expect(figures[0]?.textContent).toContain('SGD')
    expect(figures[0]?.textContent).toContain('20,680.00')
    expect(figures[1]?.textContent).toContain('EUR')
    expect(figures[1]?.textContent).toContain('6,750.00')
    expect(screen.getByTestId('viewpart-no-grand-total').textContent).toContain('No combined figure')
    expect(screen.getByTestId('viewpart-excluded-line').textContent).toContain(EXCLUDED_REASON)
    assertNoCombinedTotal(container)
  })
})

// ── chart ───────────────────────────────────────────────────────────────────

describe('ChartPart', () => {
  it('draws one line per unit series, never merged (G2), with a legend naming each unit', () => {
    const part: ViewResultPart = {
      part: 'chart',
      source: { part: 'chart', number: 'amount', unit: 'currency', date: 'due_date' },
      series: [
        {
          unit: 'SGD',
          points: [
            { key: '2026-05-01', value: '12480.00', count: 1 },
            { key: '2026-06-01', value: '8200.00', count: 1 },
          ],
        },
        {
          unit: 'EUR',
          points: [
            { key: '2026-05-01', value: '6750.00', count: 1 },
            { key: '2026-06-01', value: '2900.00', count: 1 },
          ],
        },
      ],
    }
    const { container } = render(<ChartPart part={part} />)
    expect(screen.getAllByTestId('viewpart-chart-line')).toHaveLength(2)
    const legend = screen.getByTestId('viewpart-chart-legend')
    expect(legend.textContent).toContain('SGD')
    expect(legend.textContent).toContain('EUR')
    // No merged series: 19,230.00 (12,480+6,750 at the same bucket) nowhere.
    expect(container.textContent).not.toContain('19,230.00')
  })

  it('draws a single-point series as a bar, and an empty series as a stated answer', () => {
    const onePoint: ViewResultPart = {
      part: 'chart',
      source: { part: 'chart', number: 'amount', date: 'due_date' },
      series: [{ points: [{ key: '2026-05-01', value: '100.00', count: 1 }] }],
    }
    const { unmount } = render(<ChartPart part={onePoint} />)
    expect(screen.getByTestId('viewpart-chart-bar')).toBeInTheDocument()
    unmount()

    render(<ChartPart part={{ part: 'chart', source: { part: 'chart' }, series: [] }} />)
    expect(screen.getByTestId('viewpart-chart').textContent).toContain('series is empty')
  })

  // code-review finding #8 — the domain used to be Math.max(0, …), which
  // silently discards every negative point: it maps below the 180px viewBox
  // (invisible), gives a single negative bar a NEGATIVE height, and an
  // all-negative series divides by a zero denominator and collapses onto the
  // "0" gridline. The fix widens the domain to the real min/max (never
  // clamped) and always keeps zero inside it, so a bar/line/point can always
  // anchor on a real zero baseline. PAD = {top:12,bottom:24}, HEIGHT=180 →
  // plotH=144, plot spans y=12 (top) to y=156 (bottom) — every y below is
  // hand-computed from that geometry, not read off the implementation.
  const PLOT_TOP = 12
  const PLOT_BOTTOM = 156

  function pointsOf(el: Element): Array<[number, number]> {
    const raw = el.getAttribute('points') ?? ''
    return raw
      .trim()
      .split(/\s+/)
      .filter((s) => s !== '')
      .map((pair) => {
        const [px, py] = pair.split(',').map(Number)
        return [px ?? NaN, py ?? NaN]
      })
  }

  it('a mixed positive/negative series draws a zero line strictly inside the domain, with both signs visible', () => {
    const part: ViewResultPart = {
      part: 'chart',
      source: { part: 'chart', number: 'amount', date: 'due_date' },
      series: [
        {
          points: [
            { key: '2026-05-01', value: '500', count: 1 },
            { key: '2026-06-01', value: '-300', count: 1 },
          ],
        },
      ],
    }
    render(<ChartPart part={part} />)
    // domain = [min(0,-300), max(0,500)] = [-300, 500], span 800.
    // y(500) = 156 - (800/800)*144 = 12 (top); y(-300) = 156 - 0 = 156 (bottom).
    const [p1, p2] = pointsOf(screen.getByTestId('viewpart-chart-line'))
    expect(p1?.[1]).toBeCloseTo(PLOT_TOP)
    expect(p2?.[1]).toBeCloseTo(PLOT_BOTTOM)
    // Zero sits strictly inside [-300, 500] → y(0) = 156 - (300/800)*144 = 102.
    const zeroLine = screen.getByTestId('viewpart-chart-zero-line')
    expect(Number(zeroLine.getAttribute('y1'))).toBeCloseTo(102)
    expect(Number(zeroLine.getAttribute('y2'))).toBeCloseTo(102)
  })

  it('an all-negative series uses the full plot height instead of collapsing onto the zero gridline', () => {
    const part: ViewResultPart = {
      part: 'chart',
      source: { part: 'chart', number: 'amount', date: 'due_date' },
      series: [
        {
          points: [
            { key: '2026-05-01', value: '-100', count: 1 },
            { key: '2026-05-08', value: '-300', count: 1 },
            { key: '2026-05-15', value: '-50', count: 1 },
          ],
        },
      ],
    }
    render(<ChartPart part={part} />)
    // domain = [min(0,-300), max(0,-50)] = [-300, 0], span 300 — the OLD
    // Math.max(0, …) domain would have been maxValue=0, dividing by zero and
    // pinning every point to the bottom edge (156). The fix must use the
    // full plot height instead.
    const pts = pointsOf(screen.getByTestId('viewpart-chart-line'))
    expect(pts).toHaveLength(3)
    // y(-100) = 156 - (200/300)*144 = 60; y(-300) = 156 (bottom); y(-50) = 36.
    expect(pts[0]?.[1]).toBeCloseTo(60)
    expect(pts[1]?.[1]).toBeCloseTo(PLOT_BOTTOM)
    expect(pts[2]?.[1]).toBeCloseTo(36)
    // Zero is the domain's own MAX here (an edge, not an interior point) —
    // already stated by the top axis label, so no separate zero line is owed.
    expect(screen.queryByTestId('viewpart-chart-zero-line')).not.toBeInTheDocument()
  })

  it('an all-zero series draws at mid-height, not collapsed onto the bottom edge', () => {
    const part: ViewResultPart = {
      part: 'chart',
      source: { part: 'chart', number: 'amount', date: 'due_date' },
      series: [
        {
          points: [
            { key: '2026-05-01', value: '0', count: 1 },
            { key: '2026-05-08', value: '0.00', count: 1 },
          ],
        },
      ],
    }
    render(<ChartPart part={part} />)
    // Flat domain [0,0] is padded to [-1,1] so the scale never divides by
    // zero: y(0) = 156 - ((0+1)/2)*144 = 84 — the exact vertical MIDPOINT of
    // the plot (12..156), never the old bottom-edge collapse at 156.
    const pts = pointsOf(screen.getByTestId('viewpart-chart-line'))
    for (const [, py] of pts) expect(py).toBeCloseTo(84)
  })

  it('a single negative point draws as a bar with a non-negative SVG height, anchored on zero', () => {
    const part: ViewResultPart = {
      part: 'chart',
      source: { part: 'chart', number: 'amount', date: 'due_date' },
      series: [{ points: [{ key: '2026-05-01', value: '-300', count: 1 }] }],
    }
    render(<ChartPart part={part} />)
    const bar = screen.getByTestId('viewpart-chart-bar')
    const height = Number(bar.getAttribute('height'))
    const y = Number(bar.getAttribute('y'))
    expect(height).toBeGreaterThan(0)
    expect(y).toBeGreaterThanOrEqual(PLOT_TOP)
    expect(y + height).toBeLessThanOrEqual(PLOT_BOTTOM + 0.001)
  })
})

// ── crosstab ────────────────────────────────────────────────────────────────

describe('CrosstabPart', () => {
  it('renders one grid per unit (never mixed, G2), sparse cells as dashes, with the G3 counter', () => {
    const part: ViewResultPart = {
      part: 'crosstab',
      source: { part: 'crosstab', number: 'amount', unit: 'currency' },
      crosstab: {
        row_property: 'client',
        column_property: 'status',
        row_keys: ['HAVI', 'Nordwind'],
        column_keys: ['Overdue', 'Sent'],
        cells: [
          { row: 'HAVI', column: 'Sent', unit: 'SGD', value: '12480.00', count: 1 },
          { row: 'HAVI', column: 'Overdue', unit: 'SGD', value: '8200.00', count: 1 },
          { row: 'Nordwind', column: 'Sent', unit: 'EUR', value: '6750.00', count: 1 },
        ],
        excluded_count: 1,
        excluded_reason: EXCLUDED_REASON,
      },
    }
    const { container } = render(<CrosstabPart part={part} />)
    const grids = screen.getAllByTestId('viewpart-crosstab-grid')
    expect(grids).toHaveLength(2) // one per unit — never mixed
    expect(grids[0]?.textContent).toContain('SGD')
    expect(grids[1]?.textContent).toContain('EUR')
    // The SGD grid must not show the EUR figure and vice versa.
    expect(grids[0]?.textContent).not.toContain('6,750.00')
    expect(grids[1]?.textContent).not.toContain('12,480.00')
    // Sparse positions render as an em dash, not a zero nobody computed:
    // SGD grid has 2 empty positions (Nordwind row), EUR grid has 3.
    const dashCells = [...container.querySelectorAll('td')].filter((td) => td.textContent === '—')
    expect(dashCells).toHaveLength(5)
    expect(screen.getByTestId('viewpart-excluded-line').textContent).toContain(EXCLUDED_REASON)
    assertNoCombinedTotal(container)
  })
})

// ── the frame: refusal, empty, truncation ───────────────────────────────────

function makeResult(over: Partial<ViewResult> = {}): ViewResult {
  return {
    view: 'invoices--outstanding',
    label: 'Outstanding',
    parts: [],
    rows: [],
    complete: true,
    problems: [],
    ...over,
  }
}

describe('ViewPartsRenderer — refusal state', () => {
  it('renders the server refusal verbatim: reason, remedy, code — never a blank', () => {
    render(
      <ViewPartsRenderer
        result={makeResult({
          refusal: {
            code: 'view_disabled',
            reason: 'This view is stored but disabled: its filter contains an untranslated expression.',
            remedy: 'Rewrite the filter with knowledge_configure, or re-import the base.',
          },
        })}
      />,
    )
    const refusal = screen.getByTestId('view-refusal')
    expect(refusal.textContent).toContain('untranslated expression')
    expect(screen.getByTestId('view-refusal-remedy').textContent).toContain('knowledge_configure')
    expect(refusal.textContent).toContain('view_disabled')
  })
})

describe('ViewPartsRenderer — empty state', () => {
  it('leads with the plain-words outcome and names what the view draws, never a bare blank', () => {
    render(<ViewPartsRenderer result={makeResult({ rows: [], complete: true, type: 'invoice' })} />)
    const empty = screen.getByTestId('view-empty')
    expect(empty.textContent).toContain('Nothing matches this view.')
    expect(empty.textContent).toContain('every invoice record')
  })

  it('distinguishes an incomplete empty from a true empty', () => {
    render(
      <ViewPartsRenderer
        result={makeResult({ rows: [], complete: false, complete_reason: 'index not yet built' })}
      />,
    )
    expect(screen.getByTestId('view-empty').textContent).toContain('Nothing to show yet.')
    expect(screen.getByTestId('view-empty-incomplete').textContent).toContain('index not yet built')
  })
})

describe('ViewPartsRenderer — part stack and truncation', () => {
  it('walks the parts in order and states when totals were withheld on a truncated row set', () => {
    const result = makeResult({
      rows: ROWS,
      rows_truncated: true,
      complete: false,
      complete_reason: 'row set exceeds the render bound',
      parts: [
        { part: 'figures', source: { part: 'figures', number: 'amount' }, totals: [] },
        { part: 'table', source: { part: 'table' }, columns: ['file.name'] },
      ],
    })
    render(<ViewPartsRenderer result={result} />)
    expect(screen.getByTestId('view-truncated').textContent).toContain('no totals were computed')
    const parts = screen.getByTestId('view-parts')
    const order = [...parts.querySelectorAll('[data-testid^="view-part-"]')].map((el) =>
      el.getAttribute('data-testid'),
    )
    expect(order).toEqual(['view-part-figures', 'view-part-table'])
  })
})
