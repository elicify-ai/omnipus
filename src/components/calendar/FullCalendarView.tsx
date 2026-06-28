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
  Flag,
} from '@phosphor-icons/react'

import '@/styles/fullcalendar-theme.css'

import type { FullCalendarViewProps, StatusIconKey, CalendarViewName } from './types'
import { CHIP_TEXT_COLOR } from './types'

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
  Flag,
}

// ---------------------------------------------------------------------------
// eventContent renderer — chip with icon + title (+ time for timed events)
// ---------------------------------------------------------------------------

function EventChip({ arg }: { arg: EventContentArg }) {
  const icon = arg.event.extendedProps.icon as StatusIconKey | undefined
  const Icon = (icon && ICON_MAP[icon]) || Circle
  const bg = arg.event.backgroundColor || 'transparent'
  const isAllDay = arg.event.allDay

  return (
    <div
      className="fc-sovereign-chip"
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
      {!isAllDay && arg.timeText && (
        <span
          style={{
            fontSize: '0.65rem',
            fontWeight: 500,
            flexShrink: 0,
            color: CHIP_TEXT_COLOR,
          }}
        >
          {arg.timeText}
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
// ---------------------------------------------------------------------------

function DayCellRenderer({ arg }: { arg: DayCellContentArg }) {
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
      aria-live="polite"
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
    onDatesSet?.(arg.view.title, arg.view.type as CalendarViewName)
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
        // Time bounds for week/day views (spec §A7 / §5 edge)
        slotMinTime="06:00:00"
        slotMaxTime="22:00:00"
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
