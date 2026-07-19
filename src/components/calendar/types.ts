// Shared contract for the workspace Calendar (FullCalendar v6) feature.
// Spec: docs/internal/specs/workspace-calendar-fullcalendar-spec.md (v2).
//
// not-wire-format: every type here is an internal view model. None of it crosses
// the gateway/SPA boundary, so Constraint #8 (contract-first) does not apply.
//
// This file is the integration seam for the parallel build: the pure mapping
// (eventMapping.ts), the FullCalendar wrapper (FullCalendarView.tsx), the toolbar
// (CalendarToolbar.tsx), and the host screen (CalendarScreen.tsx) all depend on
// the interfaces declared here.

import type { MutableRefObject } from 'react'
import type FullCalendar from '@fullcalendar/react'
import type {
  EventInput,
  EventClickArg,
  EventDropArg,
  DateSelectArg,
} from '@fullcalendar/core'
import type { DateClickArg } from '@fullcalendar/interaction'
import type { Task } from '@/lib/api'

/** The 7-member task status union (reused from the wire contract). */
export type TaskStatus = Task['status']

/** The four views offered by the toolbar (FR-006). */
export type CalendarViewName =
  | 'dayGridMonth'
  | 'timeGridWeek'
  | 'timeGridDay'
  | 'listWeek'

export const CALENDAR_VIEWS: CalendarViewName[] = [
  'dayGridMonth',
  'timeGridWeek',
  'timeGridDay',
  'listWeek',
]

/** Human labels for the view switcher. */
export const CALENDAR_VIEW_LABELS: Record<CalendarViewName, string> = {
  dayGridMonth: 'Month',
  timeGridWeek: 'Week',
  timeGridDay: 'Day',
  listWeek: 'Agenda',
}

/**
 * Phosphor icon keys used as the non-colour status cue on every chip (WCAG 1.4.1).
 * eventMapping.ts (pure, no React) emits the KEY; FullCalendarView maps key → component.
 */
export type StatusIconKey =
  | 'CheckCircle' // done
  | 'CircleNotch' // in_progress
  | 'Prohibit' //    blocked
  | 'XCircle' //     failed
  | 'Circle' //      inbox / next / planning (muted)
  | 'Clock' //       once-trigger "fires" chip (overrides status icon)

/**
 * extendedProps carried on every FullCalendar EventInput we produce.
 *
 * Discriminated on `kind` so illegal states (e.g. a task event without a
 * `taskId`) are unrepresentable — the consumer's switch on `kind` then
 * narrows `taskId`/`status` with no defensive runtime checks, and
 * `persistReschedule`'s switch can be exhaustive.
 */
export type CalendarEventExtProps =
  | { kind: 'task-due'; taskId: string; status: TaskStatus; icon: StatusIconKey }
  | { kind: 'task-fire'; taskId: string; status: TaskStatus; icon: 'Clock' }

/** Near-black chip text — clears WCAG AAA (>=7:1) on every chip background (SC-006b). */
export const CHIP_TEXT_COLOR = '#0A0A0B'

/**
 * Canonical status → chip style map (single source of truth, FR-005).
 * `bg` is the chip background; the icon is the StatusIconKey. All clear >=7:1
 * contrast with CHIP_TEXT_COLOR. `next`/`inbox`/`planning` are muted/slate.
 */
export interface ChipStyle {
  bg: string
  icon: StatusIconKey
}

export const STATUS_STYLE: Record<TaskStatus, ChipStyle> = {
  done: { bg: '#34D399', icon: 'CheckCircle' },
  in_progress: { bg: '#60A5FA', icon: 'CircleNotch' },
  blocked: { bg: '#FBBF24', icon: 'Prohibit' },
  failed: { bg: '#F87171', icon: 'XCircle' },
  inbox: { bg: '#94A3B8', icon: 'Circle' },
  next: { bg: '#94A3B8', icon: 'Circle' },
  planning: { bg: '#94A3B8', icon: 'Circle' },
}

/** Fallback style for an unknown/missing status. */
export const STATUS_STYLE_FALLBACK: ChipStyle = { bg: '#94A3B8', icon: 'Circle' }

/** The forwarded ref both FullCalendarView and CalendarToolbar share. */
export type CalendarApiRef = MutableRefObject<FullCalendar | null>

/** Props for the themed FullCalendar wrapper (FullCalendarView.tsx). */
export interface FullCalendarViewProps {
  events: EventInput[]
  calendarRef: CalendarApiRef
  initialView?: CalendarViewName
  /** While true, render the grid with a subtle loading affordance (I-1). */
  isLoading?: boolean
  /** When true and not loading, render the empty hint overlaid on the grid (I-1). */
  isEmpty?: boolean
  onEventDrop: (arg: EventDropArg) => void
  onEventClick: (arg: EventClickArg) => void
  onDateClick: (arg: DateClickArg) => void
  onDateSelect: (arg: DateSelectArg) => void
  /** Fired on FullCalendar datesSet so the toolbar can show the title + active view. */
  onDatesSet?: (title: string, view: CalendarViewName) => void
}

/** Props for the responsive Sovereign-Deep toolbar (CalendarToolbar.tsx). */
export interface CalendarToolbarProps {
  calendarRef: CalendarApiRef
  currentView: CalendarViewName
  /** Period label (e.g. "June 2026") supplied by the host from onDatesSet. */
  title: string
  onViewChange: (view: CalendarViewName) => void
  /**
   * Open the create slide-over. `date`, when supplied, is the calendar's
   * CURRENTLY VIEWED date (from `calendarApi.getDate()`) — the WCAG 2.1.1
   * keyboard-equivalent of the pointer-only dateClick/select prefill, so
   * creating a task while browsing a future/past period defaults to that
   * period instead of real-world "today". Omitted/undefined → no prefill.
   */
  onNewTask: (date?: Date) => void
}

