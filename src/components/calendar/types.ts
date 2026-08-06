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

/**
 * `task-occurrence-agg` (aggregated day bucket) chip status domain (ADR-050
 * RD6, docs/internal/specs/task-run-history-spec.md §4.2's four-state rule).
 * `'scheduled'` = no matching run yet, occurrence instant still in the future
 * ("today's Clock chip", now deliberately decoupled from `task.status` — see
 * `SCHEDULED_STYLE`). `'no_record'` = no matching run, occurrence instant
 * already in the past (pruned by retention or the schedule was edited since
 * it fired) — must never be confused with a future `'scheduled'` fire.
 *
 * Kept as the full `TaskStatus | 'scheduled' | 'no_record'` union (9 members)
 * ONLY for `task-occurrence-agg`'s legacy-fallback branch (`resolveBucketChip`
 * in eventMapping.ts) — bucket chips have carried a raw `TaskStatus` value
 * historically and this type stays wide enough to keep doing so if that
 * fallback is ever revisited. An INDIVIDUAL `task-occurrence` chip's status
 * never comes from `task.status` at all (see `RunDerivedChipStatus`) — the
 * two variants have different real domains, which is why they get different
 * types (M6).
 *
 * `'skipped'` is included here too (not a `TaskStatus` member — see
 * `RunDerivedChipStatus`'s doc comment) so the aggregated-bucket worst-wins
 * resolution (`resolveBucketWorstWins` in eventMapping.ts) can resolve a
 * bucket to `'skipped'` when that's the day's worst outcome.
 */
export type OccurrenceChipStatus = TaskStatus | 'scheduled' | 'no_record' | 'skipped'

/**
 * `task-occurrence` (individual instant) chip status domain (ADR-050 RD6,
 * task-run-history-spec.md §4.2). An individual occurrence's chip status
 * comes from exactly one of two sources — never `task.status`:
 *   - a matched `TaskRun`'s own status: `'done' | 'in_progress' | 'failed'`
 *     (`TaskRun.status`'s wire enum — no `blocked`/`inbox`/`next`/`planning`
 *     member exists on a run).
 *   - the day-vs-now synthetic states when no run matched: `'scheduled'`
 *     (future) / `'no_record'` (past).
 * Narrower than `OccurrenceChipStatus` (which stays the full `TaskStatus`
 * union for `task-occurrence-agg`'s legacy fallback) precisely because this
 * variant's domain excludes the other four `TaskStatus` members entirely.
 *
 * `'skipped'` (added alongside the backend's overlap-guard run-outcome) is a
 * fifth REAL `TaskRun.status` member — a scheduled fire that never ran
 * because the previous occurrence was still `in_progress` — not a synthetic
 * day-vs-now state like `'scheduled'`/`'no_record'`. It renders as its own
 * distinct orange chip (`SKIPPED_STYLE`) rather than falling through
 * `STATUS_STYLE` (which stays `TaskStatus`-only — `skipped` is NOT a
 * `Task.status` value).
 */
export type RunDerivedChipStatus = 'done' | 'in_progress' | 'failed' | 'skipped' | 'scheduled' | 'no_record'

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
  | 'Circle' //      inbox / next (muted)
  | 'Clock' //       once-trigger "fires" chip (overrides status icon)
  | 'SkipForward' //  TaskRun.status === 'skipped' (overlap-guard, run-only)

/**
 * extendedProps carried on every FullCalendar EventInput we produce.
 *
 * Discriminated on `kind` so illegal states (e.g. a task without a `taskId`)
 * are unrepresentable — the consumer's switch on `kind` then narrows
 * `taskId`/`status` with no defensive runtime checks, and `persistReschedule`'s
 * switch can be exhaustive.
 *
 * Calendar Recurrence Redesign (docs/internal/specs/calendar-recurrence-redesign-spec.md,
 * Integration Boundaries → FullCalendar v6, FR-009/FR-012) adds three occurrence
 * kinds, server-expanded via `GET /api/v1/tasks/occurrences` and joined into
 * events by `eventMapping.mapToCalendarEvents`'s new `occurrenceSets` param:
 *   - `task-occurrence`     — one raw instant (Week/Day views, or ≤3/day in Month).
 *   - `task-occurrence-agg` — one aggregated day (>3 occurrences/day in overview
 *     ranges, D6); `tooltip` carries the pre-formatted "first at HH:MM" string.
 *   - `task-occurrence-more` — a truncation marker on the last covered day when
 *     the server's response for that task was capped (`truncated: true`).
 * All three reuse `icon: 'Clock'` (no new StatusIconKey member) and are always
 * `editable: false` on the produced EventInput (FR-012 — not draggable); the
 * marker is additionally intended to be non-interactive at the render layer.
 * `status`/`icon` are kept on every member (not just the due/fire ones) so
 * FullCalendarView.tsx's existing unconditional `ext.icon` / `ext.status`
 * reads (EventChip) keep type-checking without that file needing to change
 * just to accommodate this union growing.
 *
 * Per-occurrence run overlay (ADR-050 RD6, docs/internal/specs/task-run-history-spec.md
 * §4.1/§4.2) widens `task-occurrence`/`task-occurrence-agg` further: those two
 * kinds stop reading `task.status` for their color/glyph entirely — see
 * `RunDerivedChipStatus` (individual instants) / `OccurrenceChipStatus`
 * (buckets) and the four-state rule implemented in `eventMapping.ts`'s
 * `resolveOccurrenceChipState`/`resolveBucketChip`.
 * `task-occurrence` carries `occurrenceMs` (the join key threaded to
 * `CalendarScreen.handleEventClick` → `CalendarEventSlideOver`'s
 * `selectedOccurrenceMs`) plus the matched run's `runId`/`sessionId`/
 * `hasResult` when one exists. `task-occurrence-agg` carries `dayStartMs`
 * (the bucket's own join key) instead, plus `dayEndMs` — the wire's
 * `DayBucket.day_end_ms` (delta-review HIGH fix), the server's own DST-aware
 * civil-next-midnight boundary for this bucket's window, carried through
 * verbatim so the client never recomputes it with a fixed-24h assumption
 * that would diverge from the server's `run_counts` tally window (and the
 * drilled-in run list) on a DST-transition day.
 */
export type CalendarEventExtProps =
  | { kind: 'task-due'; taskId: string; status: TaskStatus; icon: StatusIconKey }
  | { kind: 'task-fire'; taskId: string; status: TaskStatus; icon: 'Clock' }
  | {
      kind: 'task-occurrence'
      taskId: string
      status: RunDerivedChipStatus
      icon: StatusIconKey
      /** The raw occurrence instant (ms) this chip renders — the calendar-overlay join key. */
      occurrenceMs: number
      /** Present only when `occurrence_runs` matched a run for this instant (spec §4.2 row 1). */
      runId?: string
      sessionId?: string
      hasResult?: boolean
      /** Present only for the "No record" state — explains why (spec §4.2 row 4). */
      tooltip?: string
    }
  | {
      kind: 'task-occurrence-agg'
      taskId: string
      status: OccurrenceChipStatus
      icon: StatusIconKey
      tooltip: string
      /** The bucket's own `day_start_ms` join key (distinct from an individual `occurrenceMs`). */
      dayStartMs: number
      /**
       * The bucket's own `day_end_ms` (exclusive) — the server's DST-aware
       * civil-next-midnight boundary, threaded verbatim (delta-review HIGH
       * fix) so `CalendarScreen` never recomputes it as a fixed 24h span.
       */
      dayEndMs: number
    }
  | { kind: 'task-occurrence-more'; taskId: string; status: TaskStatus; icon: 'Clock'; tooltip: string }
  /**
   * Synthetic live "now" divider — Agenda (`listWeek`) view ONLY, added
   * because `@fullcalendar/list` has no time axis and structurally cannot
   * render FullCalendar's built-in `nowIndicator` (Week/Day only — confirmed
   * working there, untouched). `CalendarScreen` appends exactly one of these
   * to `events` per render, ONLY while `listWeek` is the active view and the
   * live-ticking "now" timestamp falls inside FullCalendar's own visible
   * range; FullCalendar's own start-time sort then places it chronologically
   * among that day's real rows, same as Google Calendar's agenda view.
   * `FullCalendarView`'s `EventChip` branches to a bare `NowMarkerLine`
   * divider instead of a real chip, and `CalendarScreen.handleEventClick`
   * treats it as non-interactive (mirrors `task-occurrence-more`, above).
   *
   * `timeLabel` is pre-formatted by `CalendarScreen` at construction time
   * (NOT read from FullCalendar's own `arg.timeText` at render time):
   * `@fullcalendar/list` hardcodes `timeText: ""` for every custom
   * `eventContent` in list/Agenda view (it renders the native time into its
   * own separate `<td>` instead), so `arg.timeText` is always empty there —
   * confirmed by reading `@fullcalendar/list`'s source and empirically in a
   * real browser. Carrying the label as data sidesteps that entirely.
   */
  | { kind: 'now-marker'; timeLabel: string }

/** Near-black chip text — clears WCAG AAA (>=7:1) on every chip background (SC-006b). */
export const CHIP_TEXT_COLOR = '#0A0A0B'

/**
 * Canonical status → chip style map (single source of truth, FR-005).
 * `bg` is the chip background; the icon is the StatusIconKey. All clear >=7:1
 * contrast with CHIP_TEXT_COLOR. `next`/`inbox` are muted/slate.
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
}

/** Fallback style for an unknown/missing status. */
export const STATUS_STYLE_FALLBACK: ChipStyle = { bg: '#94A3B8', icon: 'Circle' }

/**
 * "Scheduled" occurrence chip style (ADR-050 RD6) — no run yet, instant still
 * in the future. Deliberately NOT `task.status`-colored: the whole point of
 * the run overlay is decoupling an individual occurrence's own state from the
 * parent task's aggregate status (a future fire must never render red merely
 * because a PRIOR occurrence's run failed). Reuses the same muted-neutral bg
 * already proven >=7:1 vs `CHIP_TEXT_COLOR` as `STATUS_STYLE.inbox/next/planning`.
 */
export const SCHEDULED_STYLE: ChipStyle = { bg: '#94A3B8', icon: 'Clock' }

/**
 * "No record" occurrence chip style — no run, instant already in the past
 * (pruned by retention, or the schedule was edited since it fired). Same
 * neutral bg family as `SCHEDULED_STYLE` but a distinct glyph (Circle, not
 * Clock) so it reads as visually different from both a future "Scheduled"
 * chip and any status-colored run chip (task-run-history-spec.md §4.2).
 */
export const NO_RECORD_STYLE: ChipStyle = { bg: '#94A3B8', icon: 'Circle' }

/**
 * "Skipped" occurrence chip style — a real `TaskRun.status` value (not a
 * synthetic day-vs-now state): a scheduled fire the backend's overlap guard
 * declined to run because the previous occurrence was still `in_progress`.
 * Distinct orange (operator direction: "make it orange... so it is
 * transparent to the user what happened") — reuses the SAME hex as
 * `src/lib/statusColors.ts`'s `blocked` (`#F97316` / `--color-cancelled` in
 * `src/styles/globals.css`), this codebase's one established "orange", never
 * a newly invented hue. `SkipForward` (not `Prohibit`, already `blocked`;
 * not `Warning`/`WarningCircle`, already error-banner meaning) reads as
 * "this fire was skipped".
 */
export const SKIPPED_STYLE: ChipStyle = { bg: '#F97316', icon: 'SkipForward' }

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
  /**
   * Fired on FullCalendar datesSet so the toolbar can show the title + active
   * view. `activeStart`/`activeEnd` are FullCalendar's own visible-range Dates
   * (half-open, `activeEnd` exclusive) — added by the Calendar Recurrence
   * Redesign (docs/internal/specs/calendar-recurrence-redesign-spec.md,
   * "Occurrence expansion endpoint") so the host can key its
   * `useOccurrences` query to the visible range without re-deriving it from
   * the FullCalendar ref elsewhere.
   */
  onDatesSet?: (title: string, view: CalendarViewName, activeStart: Date, activeEnd: Date) => void
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

  // ── Agent filter (FR-015 / US-4) ──────────────────────────────────────────
  // All four are optional and travel together: the toolbar renders the Agent
  // dropdown only when `onAgentFilterChange` is supplied. This keeps every
  // pre-existing caller/test (which predates this feature) compiling
  // unchanged; CalendarScreen — the only real caller — always supplies all
  // four, so the live toolbar always shows the filter.
  /** Currently selected filter value: `AGENT_FILTER_ALL` / `AGENT_FILTER_UNASSIGNED` (see `./calendarAgentFilter`) or an agent id. */
  agentFilter?: string
  onAgentFilterChange?: (value: string) => void
  /** Workspace-roster entries beyond the built-in "All agents"/"Unassigned" sentinels — `{value,label}` shape matches ListView's FilterSelect pattern (and `buildTaskAssigneeItems`'s output). */
  agentOptions?: { value: string; label: string }[]
  /** True when the workspace team roster failed to load — shows the same degrade notice used by CreateTaskSlideOver/TaskDetailPanel ("Team list unavailable — showing all agents"). */
  agentRosterError?: boolean
}

