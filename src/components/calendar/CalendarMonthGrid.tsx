/**
 * CalendarMonthGrid — the compact month grid (SP-39, side-panel wave 3).
 *
 * Wireframe: coordination/wireframes/side-panel-wave3.html §4 ("Calendar docked
 * … and in full screen") + §7 (phone). Month no longer routes away to a wider
 * surface — it renders this grid right inside the docked panel (461px default,
 * 320px floor) and on phone takeover, and the same grid fills full screen
 * ("same grid as docked, just more room" — cells grow with the container
 * because every cell is `aspect-ratio: 1`, so the layout is width-driven and
 * never jumps to a wider surface).
 *
 * Layout contract (wireframe `.cal-month-dow` / `.cal-month-grid` /
 * `.cal-month-cell`, mapped to repo tokens):
 *   - 7 equal columns (Mon-first, matching FullCalendar's `firstDay={1}`);
 *     cell height follows cell width (`aspect-ratio: 1`), so the grid adapts
 *     to ANY panel width without media queries.
 *   - Day-of-week header + cells use tokenized type (`--type-caption-size`,
 *     the 12px floor — the wireframe's 8.5/9px mockup values are below the
 *     design-system type floor and cannot be used in app code).
 *   - Other-month days dim (`opacity .35`); today carries the accent border.
 *   - Events render as 4px status-colored dots (max 3 per day, wireframe
 *     `.devt`), colored by the event's own FullCalendar `backgroundColor`
 *     (the same status color its Week/Day chip uses — data, not a style
 *     literal).
 *
 * Interaction contract (deliberately mirrors FullCalendar's own a11y stance,
 * see CalendarToolbar's WCAG 2.1.1 note):
 *   - A day cell is click-to-create (the pointer parity of FC's `dateClick`
 *     on an all-day cell); the keyboard-equivalent path is the toolbar's
 *     "New task" button, which pre-fills the currently viewed date — exactly
 *     as it does for the FC-rendered views.
 *   - Dots are pure visual markers (the wireframe gives them no interactive
 *     affordance) with a hover/fallback `title` naming the day's events.
 *     Opening/editing an event happens from Week/Day chips, as before.
 *
 * Data contract: FullCalendar stays mounted as the navigation/date engine in
 * every view (CalendarScreen hides its DOM while Month is active), so
 * `rangeStart`/`rangeEnd` are FullCalendar's own `activeStart`/`activeEnd`
 * from `datesSet` — the grid renders exactly the reported range (6 weeks for
 * `dayGridMonth`'s fixed 6-row grid) and never re-derives month math itself.
 */

import { useMemo } from 'react'
import type { EventInput } from '@fullcalendar/core'
import { cn } from '@/lib/utils'

/** Max dots rendered per day cell; further events collapse into the cell title. */
const MAX_DOTS_PER_DAY = 3

/** Local `YYYY-MM-DD` key for a calendar day (never UTC-shifted). */
function localDayKey(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

export interface CalendarMonthGridProps {
  /** FullCalendar's own visible-range start (`datesSet` → `activeStart`). */
  rangeStart: Date
  /** FullCalendar's own visible-range end, exclusive (`datesSet` → `activeEnd`). */
  rangeEnd: Date
  /** Host-filtered events (same array the Week/Day views render). */
  events: EventInput[]
  /** Day-cell click → create at that day (host routes to its create flow). */
  onDayClick?: (date: Date) => void
  /** While true, suppress the empty hint (same I-1 contract as FC's views). */
  isLoading?: boolean
  /** When true and not loading, the "no scheduled items" hint overlays the grid. */
  isEmpty?: boolean
}

export function CalendarMonthGrid({
  rangeStart,
  rangeEnd,
  events,
  onDayClick,
  isLoading = false,
  isEmpty = false,
}: CalendarMonthGridProps) {
  // ── Days of the reported range ────────────────────────────────────────────
  // Whole weeks between activeStart and activeEnd (dayGridMonth reports 42
  // days → 6 rows). Deriving the count from the reported range keeps the grid
  // honest to FullCalendar's own month math — no local re-derivation.
  const days = useMemo<Date[]>(() => {
    const DAY_MS = 86_400_000
    const total = Math.round((rangeEnd.getTime() - rangeStart.getTime()) / DAY_MS)
    if (total <= 0 || !Number.isFinite(total)) return []
    const count = Math.floor(total / 7) * 7
    return Array.from({ length: count }, (_, i) => {
      const d = new Date(rangeStart)
      d.setDate(d.getDate() + i)
      return d
    })
  }, [rangeStart, rangeEnd])

  // Mid-range day — for a month range this always lands inside the target
  // month (the 1st sits within the first rendered week, so start+21d is the
  // 15th–22nd). It is the "anchor month" the other-month dimming compares to.
  const anchorMonth = days.length > 21 ? days[21].getMonth() : -1

  // ── Events per day ────────────────────────────────────────────────────────
  // Dots mark an event's START day (this calendar's events are instants —
  // due/fire/occurrence — not multi-day spans). Keyed by local day.
  const eventsByDay = useMemo(() => {
    const map = new Map<string, EventInput[]>()
    for (const event of events) {
      if (!event.start) continue
      const start = new Date(event.start as string | Date)
      if (Number.isNaN(start.getTime())) continue
      const key = localDayKey(start)
      const list = map.get(key)
      if (list) list.push(event)
      else map.set(key, [event])
    }
    return map
  }, [events])

  const dowLabels = useMemo(() => {
    const fmt = new Intl.DateTimeFormat(undefined, { weekday: 'short' })
    // days[0] is the range's first day — a Monday, because FullCalendar is
    // configured `firstDay={1}`. Labels follow the visible grid, not a
    // hardcoded weekday list.
    return (days.length >= 7 ? days.slice(0, 7) : []).map((d) => fmt.format(d))
  }, [days])

  const todayKey = localDayKey(new Date())

  return (
    <div
      data-testid="calendar-month-grid"
      className="flex min-h-0 flex-1 flex-col overflow-y-auto"
    >
      {/* Day-of-week header — 7 equal columns, muted, centered (wireframe .cal-month-dow) */}
      <div
        aria-hidden="true"
        className={cn(
          'grid flex-shrink-0 grid-cols-7 gap-[var(--space-1)]',
          'px-[var(--space-2)] pt-[var(--space-1)] pb-[var(--space-0-5)]',
        )}
      >
        {dowLabels.map((label) => (
          <span
            key={label}
            className={cn(
              'text-center text-[length:var(--type-caption-size)] leading-[var(--type-caption-line-height)]',
              'text-[var(--color-muted)]',
            )}
          >
            {label}
          </span>
        ))}
      </div>

      {/* The grid — aspect-ratio cells make height follow width, so the same
          component serves the 320px floor, the 461px dock, phone takeover and
          full screen without a single width branch (SP-39). */}
      <div
        className={cn(
          'grid flex-shrink-0 grid-cols-7 gap-[var(--space-1)]',
          'px-[var(--space-2)] pt-[var(--space-0-5)] pb-[var(--space-2)]',
        )}
      >
        {days.map((day) => {
          const key = localDayKey(day)
          const dayEvents = eventsByDay.get(key) ?? []
          const isToday = key === todayKey
          const isOtherMonth = day.getMonth() !== anchorMonth
          const tooltip =
            dayEvents.length > 0
              ? `${day.toLocaleDateString()} — ${dayEvents
                  .slice(0, MAX_DOTS_PER_DAY)
                  .map((e) => e.title)
                  .join(', ')}${dayEvents.length > MAX_DOTS_PER_DAY ? `, +${dayEvents.length - MAX_DOTS_PER_DAY} more` : ''}`
              : undefined
          return (
            <div
              key={key}
              data-testid={`calendar-month-day-${key}`}
              title={tooltip}
              aria-label={tooltip}
              onClick={onDayClick ? () => onDayClick(new Date(day)) : undefined}
              className={cn(
                'relative flex min-h-0 flex-col overflow-hidden rounded-[4px]',
                'border bg-[var(--color-surface-1)]',
                'p-[var(--space-0-5)]',
                'aspect-square min-w-0',
                // Click affordance — the compact grid's cells are the
                // pointer-parity of FC's dateClick day cells. Background
                // (not border) so it never competes with today's accent
                // border. (Ternary, not `&&`: the colour lock must be able to
                // statically resolve every class fragment it scans.)
                onDayClick
                  ? 'cursor-pointer transition-colors hover:bg-[var(--color-surface-2)]'
                  : '',
                // Today carries the accent border (wireframe .cal-month-cell.today).
                isToday ? 'border-[var(--color-accent)]' : 'border-[var(--color-border)]',
                isOtherMonth && 'opacity-35',
              )}
            >
              <span
                aria-current={isToday ? 'date' : undefined}
                className={cn(
                  'text-[length:var(--type-caption-size)] leading-[var(--type-caption-line-height)]',
                  isToday
                    ? 'font-semibold text-[var(--color-secondary)]'
                    : 'text-[var(--color-muted)]',
                )}
              >
                {day.getDate()}
              </span>
              {/* Status dots (wireframe .devt) — color is the event's own
                  status color (data, like EventChip's background), never a
                  style literal. */}
              <span className="mt-auto flex min-h-0 gap-[var(--space-0-5)] overflow-hidden">
                {dayEvents.slice(0, MAX_DOTS_PER_DAY).map((event) => (
                  <span
                    key={String(event.id)}
                    className="h-[var(--space-1)] w-[var(--space-1)] flex-shrink-0 rounded-full"
                    style={{ backgroundColor: event.backgroundColor ?? 'var(--color-muted)' }}
                  />
                ))}
              </span>
            </div>
          )
        })}
      </div>

      {/* Empty hint — same wording as FullCalendarView's EmptyHint (I-1), shown
          only once loading has settled and the filtered day map is empty. */}
      {!isLoading && isEmpty && (
        <div className="pointer-events-none flex flex-1 items-start justify-center pb-[var(--space-3)]">
          <p
            className={cn(
              'rounded-[4px] border border-[var(--color-border)] bg-[var(--color-surface-1)]',
              'p-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]',
            )}
          >
            No scheduled items — click a day to add one
          </p>
        </div>
      )}
    </div>
  )
}
