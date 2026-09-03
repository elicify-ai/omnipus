// CalendarPart — month grid, read-only (view-kinds-design-2026-09-03 §2.2
// calendar; wireframe "Calendar — group by a date"). Rows land on the day
// their date cell names; the grid starts on Monday, matching the wireframe.
// The visible month defaults to the month of the part's earliest dated row —
// a vault of past invoices opening on an empty "today" grid would look like
// an empty view, which is the failure the empty state exists to prevent —
// and the reader can page months with the arrows (client state only).

import { useMemo, useState } from 'react'
import { CaretLeft, CaretRight } from '@phosphor-icons/react'
import type { VaultFindRow, ViewResultPart } from '@/lib/api/generated/openapi-types'
import { cellValue } from './viewResultData'
import { TotalsFooter } from './PartChrome'

const DAY_HEADERS = ['M', 'T', 'W', 'T', 'F', 'S', 'S'] as const

/** First 10 chars of an ISO date cell ("2026-05-01…"), or undefined. */
function isoDay(value: string): string | undefined {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(value.trim())
  return m ? m[0].slice(0, 10) : undefined
}

export function CalendarPart({ part, rows }: { part: ViewResultPart; rows: VaultFindRow[] }) {
  const dateProperty = part.source.date

  const rowsByDay = useMemo(() => {
    const m = new Map<string, VaultFindRow[]>()
    if (dateProperty === undefined) return m
    for (const row of rows) {
      const day = isoDay(cellValue(row, dateProperty))
      if (day === undefined) continue
      if (!m.has(day)) m.set(day, [])
      m.get(day)?.push(row)
    }
    return m
  }, [rows, dateProperty])

  const initialMonth = useMemo(() => {
    const days = [...rowsByDay.keys()].sort()
    const first = days[0]
    if (first !== undefined) return { year: Number(first.slice(0, 4)), month: Number(first.slice(5, 7)) - 1 }
    const now = new Date()
    return { year: now.getFullYear(), month: now.getMonth() }
  }, [rowsByDay])

  const [visible, setVisible] = useState(initialMonth)

  const cells = useMemo(() => {
    const firstOfMonth = new Date(Date.UTC(visible.year, visible.month, 1))
    const mondayOffset = (firstOfMonth.getUTCDay() + 6) % 7 // Monday-start grid
    const daysInMonth = new Date(Date.UTC(visible.year, visible.month + 1, 0)).getUTCDate()
    const total = Math.ceil((mondayOffset + daysInMonth) / 7) * 7
    const out: Array<{ day: number; inMonth: boolean; iso: string }> = []
    for (let i = 0; i < total; i++) {
      const d = new Date(Date.UTC(visible.year, visible.month, i - mondayOffset + 1))
      out.push({
        day: d.getUTCDate(),
        inMonth: d.getUTCMonth() === visible.month,
        iso: d.toISOString().slice(0, 10),
      })
    }
    return out
  }, [visible])

  const monthLabel = new Date(Date.UTC(visible.year, visible.month, 1)).toLocaleDateString(undefined, {
    month: 'long',
    year: 'numeric',
    timeZone: 'UTC',
  })

  const step = (delta: number) => {
    setVisible((v) => {
      const d = new Date(Date.UTC(v.year, v.month + delta, 1))
      return { year: d.getUTCFullYear(), month: d.getUTCMonth() }
    })
  }

  return (
    <div className="flex min-h-0 flex-col" data-testid="viewpart-calendar">
      <div className="flex items-center gap-2 px-3 py-1.5">
        <button
          type="button"
          onClick={() => step(-1)}
          aria-label="Previous month"
          className="flex h-6 w-6 items-center justify-center rounded text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]"
        >
          <CaretLeft size={13} />
        </button>
        <span className="text-[12px] font-medium text-[var(--color-secondary)]" data-testid="viewpart-calendar-month">
          {monthLabel}
        </span>
        <button
          type="button"
          onClick={() => step(1)}
          aria-label="Next month"
          className="flex h-6 w-6 items-center justify-center rounded text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]"
        >
          <CaretRight size={13} />
        </button>
      </div>
      <div className="grid grid-cols-7 gap-px border-t border-[var(--color-border)] bg-[var(--color-border)]">
        {DAY_HEADERS.map((h, i) => (
          <div
            key={`${h}${i}`}
            className="bg-[var(--color-surface-1)] px-1.5 py-1 text-center text-[10px] uppercase text-[var(--color-muted)]"
          >
            {h}
          </div>
        ))}
        {cells.map((cell) => (
          <div
            key={cell.iso}
            className={`min-h-[3.5rem] bg-[var(--color-surface-0)] p-1 text-[10px] ${
              cell.inMonth ? '' : 'opacity-40'
            }`}
            data-testid={cell.inMonth ? 'viewpart-calendar-day' : 'viewpart-calendar-day-outside'}
          >
            <span className="text-[var(--color-muted)]">{cell.day}</span>
            {(rowsByDay.get(cell.iso) ?? []).map((row) => (
              <div
                key={row.path}
                className="mt-0.5 truncate rounded bg-[var(--color-surface-3)] px-1 py-0.5 text-[10px] text-[var(--color-secondary)]"
                title={row.title}
                data-testid="viewpart-calendar-event"
              >
                {row.title}
              </div>
            ))}
          </div>
        ))}
      </div>
      <TotalsFooter
        totals={part.totals ?? []}
        excludedCount={part.excluded_count}
        excludedReason={part.excluded_reason}
      />
    </div>
  )
}
