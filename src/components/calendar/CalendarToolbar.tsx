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

import { CaretLeft, CaretRight, CalendarBlank, Plus } from '@phosphor-icons/react'
import type { CalendarApi } from '@fullcalendar/core'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { cn } from '@/lib/utils'
import { AGENT_FILTER_ALL, AGENT_FILTER_UNASSIGNED } from './calendarAgentFilter'
import {
  CALENDAR_VIEWS,
  CALENDAR_VIEW_LABELS,
  type CalendarToolbarProps,
  type CalendarViewName,
} from './types'

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
  agentFilter,
  onAgentFilterChange,
  agentOptions,
  agentRosterError,
}: CalendarToolbarProps) {
  // ── FC API helpers ────────────────────────────────────────────────────────
  const getApi = () => calendarRef.current?.getApi()

  // `calendarRef` not being wired to a mounted FullCalendar instance should
  // NEVER happen in the live app — the ref is set by CalendarScreen before
  // the toolbar can render. A silent `getApi()?.foo()` no-op there would mask
  // a real wiring regression behind a dead button, indistinguishable from the
  // button simply doing nothing on purpose. `withApi` is the one place that
  // failure gets surfaced (console.warn, exactly once per click) — every
  // action below routes through it instead of its own ad-hoc optional chain,
  // so all five fail the identical, observable way. `onUnwired` lets a caller
  // (New task) still do something useful (open with no pre-filled date)
  // instead of a bare no-op; callers that truly have nothing else to do
  // (prev/next/today/view-change) omit it.
  const withApi = <T,>(label: string, fn: (api: CalendarApi) => T, onUnwired?: () => T) => {
    return (): T | undefined => {
      const api = getApi()
      if (!api) {
        console.warn(
          `[CalendarToolbar] ${label}: getApi() returned undefined (calendarRef not wired).`,
        )
        return onUnwired?.()
      }
      return fn(api)
    }
  }

  const handlePrev = withApi('prev', (api) => api.prev())
  const handleNext = withApi('next', (api) => api.next())
  const handleToday = withApi('today', (api) => api.today())

  const handleViewChange = (view: CalendarViewName) => {
    withApi('changeView', (api) => api.changeView(view))()
    onViewChange(view)
  }

  // WCAG 2.1.1 (keyboard) — dateClick/select on the FullCalendar grid are
  // pointer/drag-only, so a keyboard user has NO way to pick a target day when
  // creating a task by clicking a cell. This button is the keyboard-equivalent
  // path: it pre-fills the create form with the date the calendar is CURRENTLY
  // showing (`getApi().getDate()` — "today" when today is in view, otherwise
  // the reference day of the visible period) instead of opening with no date
  // at all, so browsing to e.g. a future month and creating a task doesn't
  // silently default to real-world "today". `getDate` itself is NOT
  // optional-called: `CalendarApi` guarantees it once `withApi` has already
  // confirmed `api` is live, so an optional call there could only ever mask a
  // broken test mock, never a real runtime gap.
  const handleNewTask = withApi(
    'newTask',
    (api) => onNewTask(api.getDate()),
    () => onNewTask(undefined),
  )

  // ── Shared touch-target class (WCAG 2.5.8 / I-4) ────────────────────────
  const touchTarget = 'pointer-coarse:min-h-[44px]'

  // ── Shared icon-button class (prev / next / today) ───────────────────────
  const navBtnClass = cn(
    'flex items-center justify-center shrink-0',
    'h-8 w-8 rounded-md text-[var(--color-muted)]',
    'hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]',
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
        <button tabIndex={0}
          type="button"
          data-testid="calendar-prev"
          aria-label="Go to previous period"
          onClick={handlePrev}
          className={navBtnClass}
        >
          <CaretLeft size={15} weight="bold" />
        </button>

        {/* today */}
        <button tabIndex={0}
          type="button"
          data-testid="calendar-today"
          aria-label="Go to today"
          onClick={handleToday}
          className={cn(
            'flex items-center gap-1.5 shrink-0',
            'h-8 px-2.5 rounded-md',
            'text-xs font-medium text-[var(--color-muted)]',
            'hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]',
            'transition-colors',
            touchTarget,
            'pointer-coarse:px-3',
          )}
        >
          <CalendarBlank size={14} aria-hidden="true" />
          <span>Today</span>
        </button>

        {/* next */}
        <button tabIndex={0}
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
          data-testid="calendar-title"
          className={cn(
            'ml-1 flex-1 min-w-0',
            'text-sm font-headline font-semibold',
            'text-[var(--color-secondary)] truncate',
          )}
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
        {/* View switcher — a group of four toggle buttons, NOT a tabpanel-driven
            tablist: there is no arrow-key roving-tabindex or aria-controls
            wired here, so `role="tablist"`/`role="tab"`/`aria-selected` would
            promise the ARIA tab pattern (Left/Right to move focus, one stop in
            the Tab order) without implementing it — a11y audit fix option (b).
            `role="group"` + `aria-pressed` per button correctly describes four
            independently-tabbable toggle buttons instead. */}
        <div
          role="group"
          aria-label="Calendar view"
          className="flex items-center gap-0.5 rounded-md bg-[var(--color-surface-2)] p-0.5"
        >
          {CALENDAR_VIEWS.map((view) => {
            const isActive = view === currentView
            return (
              <button tabIndex={0}
                key={view}
                type="button"
                aria-pressed={isActive}
                aria-label={CALENDAR_VIEW_LABELS[view]}
                data-testid={`calendar-view-${view}`}
                onClick={() => handleViewChange(view)}
                className={cn(
                  'flex items-center gap-1 px-2.5 h-7 rounded text-xs font-medium whitespace-nowrap',
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
                  <CalendarBlank size={15} weight="regular" aria-hidden="true" />
                </span>
                <span>{CALENDAR_VIEW_LABELS[view]}</span>
              </button>
            )
          })}
        </div>

        {/* Agent filter (FR-015 / US-4) — client-side, no refetch (SC-004).
            Rendered only when the host wires `onAgentFilterChange` (real
            usage: CalendarScreen); every other/legacy caller renders exactly
            as before. */}
        {onAgentFilterChange && (
          <div className="flex flex-col gap-0.5 shrink-0">
            <Select
              value={agentFilter ?? AGENT_FILTER_ALL}
              onValueChange={onAgentFilterChange}
            >
              <SelectTrigger
                data-testid="calendar-agent-filter"
                aria-label="Filter by agent"
                className={cn(
                  'h-8 text-xs bg-[var(--color-surface-2)] border-[var(--color-border)]',
                  'text-[var(--color-secondary)] w-auto min-w-[8rem]',
                  touchTarget,
                )}
              >
                <SelectValue placeholder="All agents" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={AGENT_FILTER_ALL} className="text-xs">
                  All agents
                </SelectItem>
                <SelectItem value={AGENT_FILTER_UNASSIGNED} className="text-xs">
                  Unassigned
                </SelectItem>
                {(agentOptions ?? []).map((a) => (
                  <SelectItem key={a.value} value={a.value} className="text-xs">
                    {a.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {/* Degrade notice (Edge Cases) — reuses the existing wording from
                CreateTaskSlideOver/TaskDetailPanel's team-roster fallback so
                the same failure reads identically everywhere in the app. */}
            {agentRosterError && (
              <p className="text-[10px] text-[var(--color-muted)] whitespace-nowrap">
                Team list unavailable — showing all agents
              </p>
            )}
          </div>
        )}

        {/* New task — primary CTA, Forge Gold */}
        <Button
          type="button"
          data-testid="calendar-new-task"
          aria-label="Create a new task"
          onClick={handleNewTask}
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
