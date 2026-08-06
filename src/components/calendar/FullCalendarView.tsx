/**
 * FullCalendarView — Themed FullCalendar v6 wrapper for the workspace Calendar.
 *
 * Spec: docs/internal/specs/workspace-calendar-fullcalendar-spec.md (v2)
 * Implements: FullCalendarViewProps from ./types
 * Satisfies: FR-001, FR-005, FR-006, FR-007, FR-011, FR-013, FR-015, FR-017
 *            I-1 (loading/empty overlays), I-3 (hover "+" affordance), I-4 (44px touch),
 *            I-7 (themed popover — via fullcalendar-theme.css)
 */

import FullCalendar from '@fullcalendar/react'
import dayGridPlugin from '@fullcalendar/daygrid'
import timeGridPlugin from '@fullcalendar/timegrid'
import listPlugin from '@fullcalendar/list'
import interactionPlugin from '@fullcalendar/interaction'
import type {
  EventContentArg,
  DayCellContentArg,
  DatesSetArg,
} from '@fullcalendar/core'

import {
  CheckCircle,
  CircleNotch,
  Prohibit,
  XCircle,
  Circle,
  Clock,
  SkipForward,
} from '@phosphor-icons/react'

import '@/styles/fullcalendar-theme.css'

import type {
  FullCalendarViewProps,
  StatusIconKey,
  CalendarViewName,
  CalendarEventExtProps,
} from './types'
import { CHIP_TEXT_COLOR } from './types'
import { statusLabel } from '@/lib/statusColors'

// ---------------------------------------------------------------------------
// Icon map: StatusIconKey → Phosphor component (WCAG 1.4.1 non-colour cue)
// ---------------------------------------------------------------------------

type IconComponent = typeof CheckCircle

const ICON_MAP: Record<StatusIconKey, IconComponent> = {
  CheckCircle,
  CircleNotch,
  Prohibit,
  XCircle,
  Circle,
  Clock,
  SkipForward,
}

/**
 * Accessible-name status text for an occurrence chip (H3 fix). `statusLabel`
 * (from `@/lib/statusColors`) only knows the canonical 7-member `TaskStatus`
 * enum and silently falls back to `STATUS_LABELS.inbox` ("Inbox") for
 * anything outside it — including the two ADR-050 run-overlay synthetic
 * states `'scheduled'`/`'no_record'`, which are NOT `TaskStatus` members (its
 * own `status: string | undefined` signature let this compile without tsc
 * ever catching it). Exported for direct unit coverage — jsdom cannot lay out
 * FullCalendar itself (see CalendarScreen tests), so this mapping needs to be
 * testable independent of rendering.
 */
export function occurrenceStatusLabel(status: string): string {
  if (status === 'scheduled') return 'Scheduled'
  if (status === 'no_record') return 'No record'
  // `skipped` is a real `TaskRun.status` value (not a `TaskStatus` member) —
  // `statusLabel` only knows the 7-member `TaskStatus` enum and would
  // silently fall back to `STATUS_LABELS.inbox` ("Inbox") for it, exactly
  // the class of bug this function exists to prevent (see doc comment above).
  if (status === 'skipped') return 'Skipped'
  return statusLabel(status)
}

/**
 * The chip's tooltip text (L3 fix) — `ext.tooltip` when the extendedProps
 * variant carries one (`task-occurrence`/`-agg`/`-more`), else `undefined`.
 * `eventMapping.ts` has populated `tooltip` (the no-record explanation, the
 * bucket worst-wins breakdown, "first at HH:MM") since the run-overlay
 * landed, but nothing here ever read it, so it was invisible (BDD #9). A
 * plain `'tooltip' in ext` narrow rather than a `kind` switch since it's the
 * FIELD, not the kind, that varies (`task-due`/`task-fire` carry no tooltip
 * at all).
 */
export function extTooltip(ext: CalendarEventExtProps): string | undefined {
  return 'tooltip' in ext ? ext.tooltip : undefined
}

/**
 * The Agenda (`listWeek`) "now" divider — EventChip branches here instead of
 * the normal chip for a `kind: 'now-marker'` synthetic event. `aria-hidden`
 * since it's a pure visual divider, not a real, informative list item — see
 * `types.ts`'s `now-marker` variant doc comment for the full rationale.
 * Exported for direct unit testing (same reason as `occurrenceStatusLabel`/
 * `extTooltip` above — jsdom can't lay out a real FullCalendar render).
 *
 * Full-row-width rendering: `eventContent` (this component) only fills the
 * THIRD of three `<td>`s `@fullcalendar/list` renders per row (time | dot |
 * title — see `@fullcalendar/list/internal.js`'s `ListViewEventRow`), so a
 * naive `width:100%` here would only span the title column, not the row.
 * `eventClassNames` below tags the marker's `<tr>` with
 * `fc-sovereign-now-marker-row`; `fullcalendar-theme.css` uses that class to
 * hide the sibling time/graphic `<td>`s for JUST this row, so the title
 * `<td>` — the only cell left — expands to the row's full width.
 */
export function NowMarkerLine({ timeText }: { timeText: string }) {
  return (
    <div className="fc-sovereign-now-marker-line" aria-hidden="true">
      {timeText && <span className="fc-sovereign-now-marker-time">{timeText}</span>}
    </div>
  )
}

// ---------------------------------------------------------------------------
// eventContent renderer — chip with icon + title (+ time for timed events)
// ---------------------------------------------------------------------------

export function EventChip({ arg }: { arg: EventContentArg }) {
  const ext = arg.event.extendedProps as CalendarEventExtProps
  // now-marker carries none of the fields read below (icon/status/etc) — must
  // branch BEFORE any of them are touched. `timeLabel` (pre-formatted by
  // CalendarScreen), NOT `arg.timeText` — `@fullcalendar/list` hardcodes
  // `timeText: ""` for every row's custom eventContent (list view renders
  // the native time into its own separate `<td>` instead), so `arg.timeText`
  // is always empty in Agenda view — see types.ts's `now-marker` doc comment.
  if (ext.kind === 'now-marker') return <NowMarkerLine timeText={ext.timeLabel} />
  const icon = ext.icon as StatusIconKey | undefined
  const Icon = (icon && ICON_MAP[icon]) || Circle
  const bg = arg.event.backgroundColor || 'transparent'
  const isAllDay = arg.event.allDay
  const timeText = !isAllDay ? arg.timeText : undefined
  const tooltip = extTooltip(ext)

  // A11y choice (audit fix, option b — do NOT add tabIndex/role="button" here):
  // FullCalendar's own event harness (the `<a>` wrapping this eventContent,
  // built by @fullcalendar/core's getSegAnchorAttrs/EventContainer) already
  // sets tabIndex=0 and an Enter/Space keydown handler that fires `eventClick`
  // whenever an eventClick prop is registered (it is — see below). Verified by
  // reading @fullcalendar/core's internal-common.js: TableBlockEvent/
  // TableListItemEvent → StandardEvent/EventContainer both receive
  // `elAttrs: getSegAnchorAttrs(seg, context)`, and createAriaKeyboardAttrs
  // returns `{ tabIndex: 0, onKeyDown }`. So the chip is ALREADY a real
  // keyboard-operable focus stop with a working Enter/Space activation —
  // adding our own tabIndex/role="button" on this inner div would nest a
  // second interactive element inside FC's `<a>` and create a double tab
  // stop. What's actually missing is the ACCESSIBLE NAME: the icon that
  // conveys status is `aria-hidden`, so a screen reader announces only the
  // visible text (time + title) with no status. An `aria-label` on this root
  // (which has no name of its own) is picked up as the "name from content"
  // for FC's wrapping `<a>` per the accname spec, so it becomes what gets
  // announced on focus — title + status + time, meaningfully, with zero
  // duplicate stops.
  //
  // NOTE this whole harness is dayGridMonth/timeGridWeek/timeGridDay only.
  // `@fullcalendar/list` (Agenda) does NOT wrap eventContent in that same
  // anchor — its `ListViewEventRow` only builds one when NO custom
  // `eventContent` is supplied (its own `defaultGenerator` path); since this
  // file registers `eventContent` globally, no row in Agenda view — real
  // chip or `NowMarkerLine` — is currently keyboard-focusable at all
  // (verified via @fullcalendar/list's source and empirically). That's a
  // pre-existing gap in Agenda view generally, not something introduced by
  // or specific to the now-marker; a real fix would need Agenda's own
  // focus/keyboard harness, out of scope here.
  const statusText = occurrenceStatusLabel(ext.status)
  const chipLabel = [arg.event.title, statusText, timeText].filter(Boolean).join(', ')

  return (
    <div
      className="fc-sovereign-chip"
      aria-label={chipLabel}
      title={tooltip}
      style={{
        backgroundColor: bg,
        color: CHIP_TEXT_COLOR,
        display: 'flex',
        alignItems: 'center',
        gap: '3px',
        width: '100%',
        padding: '1px 4px',
        borderRadius: '4px',
        overflow: 'hidden',
        cursor: 'pointer',
      }}
    >
      <Icon size={12} weight="fill" color={CHIP_TEXT_COLOR} aria-hidden="true" />
      {timeText && (
        <span
          style={{
            fontSize: '0.65rem',
            fontWeight: 500,
            flexShrink: 0,
            color: CHIP_TEXT_COLOR,
          }}
        >
          {timeText}
        </span>
      )}
      <span
        style={{
          fontSize: '0.7rem',
          fontWeight: 600,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
          color: CHIP_TEXT_COLOR,
        }}
      >
        {arg.event.title}
      </span>
    </div>
  )
}

// ---------------------------------------------------------------------------
// dayCellContent renderer — Forge-Gold pill for today; hover "+" affordance
// via CSS class (I-3). The CSS in fullcalendar-theme.css styles the hook.
//
// Month-view (dayGridMonth) ONLY. `dayCellContent` is also invoked by
// FullCalendar for the all-day row's cells in timeGridWeek/timeGridDay — a
// prior version of this renderer applied the pill there too, which put a
// big static gold "today" dot in the all-day lane of Week/Day, visually
// competing with (and easily mistaken for) the REAL live now-indicator line
// those views already render natively (`nowIndicator={true}` below).
// Week/Day need no additional "today" marker: the header date + the live
// line already convey it, matching Google Calendar's convention of never
// decorating the all-day lane with a today marker in grid/day views.
// ---------------------------------------------------------------------------

export function DayCellRenderer({ arg }: { arg: DayCellContentArg }) {
  if (arg.view.type !== 'dayGridMonth') return null
  const isToday = arg.isToday
  return (
    <div className={`fc-sovereign-day-cell${isToday ? ' fc-sovereign-day-today' : ''}`}>
      <span
        className={isToday ? 'fc-sovereign-today-pill' : 'fc-sovereign-day-number'}
        aria-current={isToday ? 'date' : undefined}
      >
        {arg.dayNumberText}
      </span>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Loading shimmer overlay (I-1) — positioned over the grid, non-blocking
// ---------------------------------------------------------------------------

function LoadingOverlay() {
  return (
    <div
      aria-hidden="true"
      style={{
        position: 'absolute',
        inset: 0,
        pointerEvents: 'none',
        zIndex: 10,
        background:
          'linear-gradient(90deg, transparent 0%, rgba(212,175,55,0.06) 50%, transparent 100%)',
        backgroundSize: '200% 100%',
        animation: 'fc-sovereign-shimmer 1.8s ease-in-out infinite',
      }}
    />
  )
}

// ---------------------------------------------------------------------------
// Empty hint overlay (I-1) — shown only when !isLoading && isEmpty
// Rendered within the grid (not replacing it) so the grid is always visible.
// ---------------------------------------------------------------------------

function EmptyHint() {
  return (
    <div
      style={{
        position: 'absolute',
        inset: 0,
        pointerEvents: 'none',
        zIndex: 10,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}
    >
      <p
        style={{
          color: 'var(--color-muted)',
          fontSize: '0.875rem',
          fontFamily: 'var(--font-body)',
          background: 'var(--color-surface-1)',
          padding: '8px 16px',
          borderRadius: '6px',
          border: '1px solid var(--color-border)',
          pointerEvents: 'none',
        }}
      >
        No scheduled items — click a day to add one
      </p>
    </div>
  )
}

// ---------------------------------------------------------------------------
// FullCalendarView
// ---------------------------------------------------------------------------

export function FullCalendarView({
  events,
  calendarRef,
  initialView,
  isLoading,
  isEmpty,
  onEventDrop,
  onEventClick,
  onDateClick,
  onDateSelect,
  onDatesSet,
}: FullCalendarViewProps) {
  const handleDatesSet = (arg: DatesSetArg) => {
    onDatesSet?.(arg.view.title, arg.view.type as CalendarViewName, arg.view.activeStart, arg.view.activeEnd)
  }

  return (
    <div
      className="fc-sovereign-wrapper"
      style={{
        position: 'relative',
        width: '100%',
        height: '100%',
      }}
    >
      <FullCalendar
        ref={calendarRef}
        plugins={[dayGridPlugin, timeGridPlugin, listPlugin, interactionPlugin]}
        // Toolbar is driven externally by CalendarToolbar (FR-006, §3)
        headerToolbar={false}
        initialView={initialView ?? 'dayGridMonth'}
        // Week starts Monday (FR-006, §3)
        firstDay={1}
        // Full 24h grid (was 06:00–22:00 per spec §A7/F-15's documented
        // business-hours window, with Month/Agenda as its accepted fallback
        // for anything outside it). slotMinTime/slotMaxTime HARD-bound the
        // rendered grid — there is no "scroll past the edge"; content outside
        // them, including the live nowIndicator line itself, has no valid
        // position and simply doesn't render. That silently broke Week/Day's
        // "now" line for any time outside 6am-10pm (reported live from
        // Asia/Jakarta, ~23:20 local) — matching Google Calendar's own
        // Week/Day grids, which are always full 24h and scrollable, not
        // clipped to business hours. `scrollTime` keeps the convenient
        // default landing position so business hours are still visible
        // without scrolling on open.
        slotMinTime="00:00:00"
        slotMaxTime="24:00:00"
        scrollTime="08:00:00"
        // 12-hour time display
        eventTimeFormat={{
          hour: 'numeric',
          minute: '2-digit',
          hour12: true,
        }}
        slotLabelFormat={{
          hour: 'numeric',
          minute: '2-digit',
          hour12: true,
        }}
        // Layout
        height="100%"
        expandRows={true}
        // "+N more" popover on overflow — themed via .fc-popover in CSS (I-7)
        dayMaxEvents={true}
        // Drag-to-reschedule on; resize off (FR-008, FR-011)
        editable={true}
        eventDurationEditable={false}
        // Click/drag-to-create (FR-012)
        selectable={true}
        selectMirror={true}
        // Live indicator; Forge-Gold colour set via --fc-now-indicator-color (FR-017 M-2)
        nowIndicator={true}
        // Events from the host (already mapped by eventMapping.ts)
        events={events}
        // Custom renderers
        eventContent={(arg) => <EventChip arg={arg} />}
        dayCellContent={(arg) => <DayCellRenderer arg={arg} />}
        // Tags the now-marker's own <tr> so fullcalendar-theme.css can hide its
        // sibling time/graphic <td>s in Agenda view — see NowMarkerLine's doc
        // comment above for why (eventContent alone can't span the whole row).
        eventClassNames={(arg) =>
          (arg.event.extendedProps as CalendarEventExtProps).kind === 'now-marker'
            ? ['fc-sovereign-now-marker-row']
            : []
        }
        // Callback wiring (FullCalendarViewProps)
        eventDrop={onEventDrop}
        eventClick={onEventClick}
        dateClick={onDateClick}
        select={onDateSelect}
        datesSet={handleDatesSet}
        // List-view empty text — themed via CSS (v6 uses noEventsContent, not noEventsText)
        noEventsContent={() => (
          <span style={{ color: 'var(--color-muted)', fontSize: '0.875rem' }}>
            No scheduled items
          </span>
        )}
      />

      {/* Loading affordance: always render grid; overlay when loading (I-1) */}
      {isLoading && <LoadingOverlay />}

      {/* Empty hint: only when loaded AND no events (I-1) */}
      {!isLoading && isEmpty && <EmptyHint />}
    </div>
  )
}
