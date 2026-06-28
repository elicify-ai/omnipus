/**
 * CalendarToolbar — Responsive Sovereign-Deep toolbar for the workspace calendar.
 *
 * Spec: docs/internal/specs/workspace-calendar-fullcalendar-spec.md (v2)
 *   FR-006: Month/Week/Day/Agenda via custom toolbar driving the FC API.
 *   FR-007: phone-width → two rows (nav+title / views+New task); ≥44px targets (I-4, I-9).
 *   US-2/AS-2: prev() / next() / today() preserve calendarApi.getDate().
 *
 * Layout (container-query breakpoint relative to the `@container` wrapper the
 * host CalendarScreen provides):
 *   Wide (≥42rem / 672px, @2xl): ONE ROW — [prev today next] [title] ·· [Month Week Day Agenda] [New task]
 *   Narrow (<42rem / 672px):     TWO ROWS — row-1: [prev today next] [title]; row-2: [views] [New task]
 *
 * Tailwind v4 container-query sizes (theme.css):
 *   @sm=24rem, @md=28rem, @lg=32rem, @xl=36rem, @2xl=42rem, @6xl=72rem.
 *   @2xl (42rem=672px) is the collapse breakpoint — matches existing usage in
 *   ChatControls.tsx (the established project convention).
 *
 * Implementation note — two-row reflow:
 *   The toolbar renders two logical `<div>` groups inside a flex container with
 *   `flex-wrap`. On narrow the container wraps and each group takes full width
 *   (`w-full @2xl:w-auto`). The second group uses `order-3` so it always
 *   appears on its own row below the nav+title row on narrow.
 *
 * Phosphor icons: CaretLeft, CaretRight, CalendarBlank, Plus (confirmed in codebase).
 * No lucide, no emoji in UI chrome.
 */

import type { ReactNode } from 'react'
import { CaretLeft, CaretRight, CalendarBlank, Plus } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import {
  CALENDAR_VIEWS,
  CALENDAR_VIEW_LABELS,
  type CalendarToolbarProps,
  type CalendarViewName,
} from './types'

// Decorative icon rendered on narrow views (density aid, aria-hidden).
// All four views share CalendarBlank; label text is always present for WCAG.
const VIEW_ICONS: Record<CalendarViewName, ReactNode> = {
  dayGridMonth: <CalendarBlank size={15} weight="regular" aria-hidden="true" />,
  timeGridWeek: <CalendarBlank size={15} weight="regular" aria-hidden="true" />,
  timeGridDay: <CalendarBlank size={15} weight="regular" aria-hidden="true" />,
  listWeek: <CalendarBlank size={15} weight="regular" aria-hidden="true" />,
}

/**
 * CalendarToolbar drives the FullCalendar instance through the `calendarRef`
 * forwarded from CalendarScreen. It never owns its own date/view state —
 * that all lives in FullCalendar itself. The host pushes `currentView` and
 * `title` down after each `onDatesSet` event.
 */
export function CalendarToolbar({
  calendarRef,
  currentView,
  title,
  onViewChange,
  onNewTask,
}: CalendarToolbarProps) {
  // ── FC API helpers ────────────────────────────────────────────────────────
  const getApi = () => calendarRef.current?.getApi()

  const handlePrev = () => {
    getApi()?.prev()
  }

  const handleNext = () => {
    getApi()?.next()
  }

  const handleToday = () => {
    getApi()?.today()
  }

  const handleViewChange = (view: CalendarViewName) => {
    getApi()?.changeView(view)
    onViewChange(view)
  }

  // ── Shared touch-target class (WCAG 2.5.8 / I-4) ────────────────────────
  const touchTarget = 'pointer-coarse:min-h-[44px]'

  // ── Shared icon-button class (prev / next / today) ───────────────────────
  const navBtnClass = cn(
    'flex items-center justify-center shrink-0',
    'h-8 w-8 rounded-md text-[var(--color-muted)]',
    'hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]',
    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]/50',
    'transition-colors',
    touchTarget,
    // On coarse pointer, ensure square-ish target width too
    'pointer-coarse:w-11',
  )

  return (
    /*
     * Outer wrapper: `@container` consumers upstream provide the container.
     * This element uses `flex flex-wrap` so that on narrow widths the two
     * logical row groups each take `w-full` and stack vertically.
     */
    <div
      data-testid="calendar-toolbar"
      className={cn(
        'flex flex-wrap items-center gap-x-2 gap-y-1.5',
        'px-3 py-2',
        'border-b border-[var(--color-border)]',
        'bg-[var(--color-surface-1)]',
        'select-none',
      )}
    >
      {/* ── Row 1: nav controls + period title ─────────────────────────────
          `order-1` (default) ensures this group appears first.
          On wide (@2xl+), it grows to fill available space; on narrow it
          takes the full width so it sits alone on row 1.                   */}
      <div
        className={cn(
          'flex items-center gap-1 order-1',
          // Narrow: own full-width row. NOTE: no unconditional `flex-1` — its
          // flex-basis:0% overrides `w-full`, so both groups would stay on one
          // line and the view tabs/New-task overflowed off-screen (FR-007/I-9).
          'w-full min-w-0',
          // Wide (@2xl+): grow to push the view tabs + New-task to the far right.
          '@2xl:flex-1 @2xl:w-auto',
        )}
      >
        {/* prev */}
        <button
          type="button"
          data-testid="calendar-prev"
          aria-label="Go to previous period"
          onClick={handlePrev}
          className={navBtnClass}
        >
          <CaretLeft size={15} weight="bold" />
        </button>

        {/* today */}
        <button
          type="button"
          data-testid="calendar-today"
          aria-label="Go to today"
          onClick={handleToday}
          className={cn(
            'flex items-center gap-1.5 shrink-0',
            'h-8 px-2.5 rounded-md',
            'text-xs font-medium text-[var(--color-muted)]',
            'hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]/50',
            'transition-colors',
            touchTarget,
            'pointer-coarse:px-3',
          )}
        >
          <CalendarBlank size={14} aria-hidden="true" />
          <span>Today</span>
        </button>

        {/* next */}
        <button
          type="button"
          data-testid="calendar-next"
          aria-label="Go to next period"
          onClick={handleNext}
          className={navBtnClass}
        >
          <CaretRight size={15} weight="bold" />
        </button>

        {/* Period title — grows to consume remaining space in row 1 */}
        <h2
          className={cn(
            'ml-1 flex-1 min-w-0',
            'text-sm font-headline font-semibold',
            'text-[var(--color-secondary)] truncate',
          )}
          aria-live="polite"
          aria-atomic="true"
        >
          {title}
        </h2>
      </div>

      {/* ── Row 2: view switcher + New task ────────────────────────────────
          `order-3` keeps this group after both row-1 groups in document order.
          On narrow: `w-full` → own row. On wide (@2xl+): `w-auto` → same row
          as the nav group (which has flex-1, so this group sits at the end). */}
      <div
        className={cn(
          'flex items-center gap-1.5 order-3',
          'w-full @2xl:w-auto',
          'justify-between @2xl:justify-end',
        )}
      >
        {/* View switcher — four tabs */}
        <div
          role="tablist"
          aria-label="Calendar view"
          className="flex items-center gap-0.5 rounded-md bg-[var(--color-surface-2)] p-0.5"
        >
          {CALENDAR_VIEWS.map((view) => {
            const isActive = view === currentView
            return (
              <button
                key={view}
                type="button"
                role="tab"
                aria-selected={isActive}
                aria-label={CALENDAR_VIEW_LABELS[view]}
                data-testid={`calendar-view-${view}`}
                onClick={() => handleViewChange(view)}
                className={cn(
                  'flex items-center gap-1 px-2.5 h-7 rounded text-xs font-medium whitespace-nowrap',
                  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)]/50',
                  'transition-colors',
                  touchTarget,
                  'pointer-coarse:h-9 pointer-coarse:px-3',
                  isActive
                    ? [
                        'bg-[var(--color-surface-3)]',
                        'text-[var(--color-accent)]',
                        'font-semibold',
                        'shadow-sm',
                      ]
                    : [
                        'text-[var(--color-muted)]',
                        'hover:text-[var(--color-secondary)]',
                        'hover:bg-[var(--color-surface-2)]/60',
                      ],
                )}
              >
                {/* On narrow screens show the icon alongside the label for density */}
                <span className="@2xl:hidden" aria-hidden="true">
                  {VIEW_ICONS[view]}
                </span>
                <span>{CALENDAR_VIEW_LABELS[view]}</span>
              </button>
            )
          })}
        </div>

        {/* New task — primary CTA, Forge Gold */}
        <Button
          type="button"
          data-testid="calendar-new-task"
          aria-label="Create a new task"
          onClick={onNewTask}
          variant="default"
          size="sm"
          className={cn(
            'flex items-center gap-1.5 shrink-0',
            'h-8 px-3',
            touchTarget,
            'pointer-coarse:h-11 pointer-coarse:px-4',
          )}
        >
          <Plus size={14} weight="bold" aria-hidden="true" />
          <span>New task</span>
        </Button>
      </div>
    </div>
  )
}
