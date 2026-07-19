/**
 * Pure event-mapping module for the workspace Calendar (FullCalendar v6).
 * Spec: docs/internal/specs/workspace-calendar-fullcalendar-spec.md (v2)
 * §6 canonical map, FR-002, FR-003, FR-004, FR-005, FR-015.
 *
 * Also implements the occurrence-mapping half of the Calendar Recurrence
 * Redesign (docs/internal/specs/calendar-recurrence-redesign-spec.md, User
 * Story 2, FR-008/009/012, Integration Boundaries → FullCalendar v6): given
 * the server-expanded `TaskOccurrenceSet[]` from `GET /api/v1/tasks/occurrences`
 * (fetched by `useOccurrences.ts`), joins them to the task list by `task_id`
 * and renders `task-occurrence` / `task-occurrence-agg` / `task-occurrence-more`
 * chips. This closes the F-10 v1 scope cut: recurring (`every`/`recurring`
 * trigger) tasks previously produced zero calendar events at all.
 *
 * No React, no Phosphor imports. Emits StatusIconKey strings only.
 * not-wire-format: internal view model; never sent over the network. The
 * `TaskOccurrenceSet`/`DayBucket` input types ARE wire types (contract-first
 * #8) — imported directly from the generated openapi-types, never redeclared.
 */

import type { EventInput } from '@fullcalendar/core'
import type { Task, Milestone } from '@/lib/api'
import type { TaskOccurrenceSet } from '@/lib/api/generated/openapi-types'
import {
  CHIP_TEXT_COLOR,
  STATUS_STYLE,
  STATUS_STYLE_FALLBACK,
  MILESTONE_STYLE,
  type CalendarEventExtProps,
} from '@/components/calendar/types'

// ─── TZ-safe date helpers (FR-015, F-06) ────────────────────────────────────

/**
 * Parse a date string TZ-safely.
 *
 * - Date-only `YYYY-MM-DD`: constructed via LOCAL component parts so there is
 *   no UTC-midnight off-by-one in any timezone (DS-1 row 9 / F-06).
 * - Datetime string (contains 'T' or a space): passed to `new Date()` which
 *   gives a wall-clock instant; the local date components are used for
 *   placement by FullCalendar.
 * - Null / undefined / invalid → null (never throws).
 */
export function parseLocalDate(s: string | null | undefined): Date | null {
  if (!s) return null
  // Detect date-only: exactly YYYY-MM-DD (10 chars, digit-dash pattern)
  if (/^\d{4}-\d{2}-\d{2}$/.test(s)) {
    const year = parseInt(s.slice(0, 4), 10)
    const month = parseInt(s.slice(5, 7), 10) - 1 // 0-indexed
    const day = parseInt(s.slice(8, 10), 10)
    if (isNaN(year) || isNaN(month) || isNaN(day)) return null
    const d = new Date(year, month, day)
    return isNaN(d.getTime()) ? null : d
  }
  // Datetime or other ISO string — use native parser
  const d = new Date(s)
  return isNaN(d.getTime()) ? null : d
}

/**
 * Format a Date to `YYYY-MM-DD` using LOCAL date components.
 * Used to write milestone `due_date` on drag and to seed the create-form date
 * prefill (FR-015, F-06). Task `due` is RFC3339 date-time and is written via
 * `toISOString()`, NOT this helper. Never uses UTC methods — no off-by-one.
 */
export function formatLocalDate(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const dd = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${dd}`
}

/**
 * Parse a Unix epoch-milliseconds timestamp.
 * Returns null on missing / zero / NaN input (never throws).
 */
export function parseMs(ms: number | null | undefined): Date | null {
  if (ms == null || ms === 0) return null
  const d = new Date(ms)
  return isNaN(d.getTime()) ? null : d
}

// ─── Event builder ────────────────────────────────────────────────────────────

/**
 * Build the common FullCalendar EventInput shape — centralises the per-chip
 * invariant (near-black text, border = background) so it is stated once
 * (FR-005). `extendedProps` is typed against the discriminated
 * CalendarEventExtProps union: the single authoritative construction site, so a
 * mismatched/illegal extendedProps shape is now a compile error.
 *
 * `editable` defaults to `true` (drag enabled) so every pre-existing call site
 * (`:due`, `:fire`, `milestone`) is byte-for-byte unchanged. Recurring
 * occurrence/aggregated/truncation-marker chips pass `false` explicitly
 * (FR-012 — not draggable; the Explicit Non-Behaviors section forbids
 * per-occurrence drag-reschedule).
 */
function makeEvent(
  id: string,
  start: Date,
  title: string,
  bg: string,
  allDay: boolean,
  extendedProps: CalendarEventExtProps,
  editable: boolean = true,
): EventInput {
  return {
    id,
    allDay,
    start,
    title,
    backgroundColor: bg,
    borderColor: bg,
    textColor: CHIP_TEXT_COLOR,
    editable,
    extendedProps,
  }
}

// ─── Occurrence label/tooltip formatting (FR-009, MAJ-008) ────────────────────
//
// Pure display formatting of numbers the SERVER already computed
// (`DayBucket.interval_ms` / `.first_ms`) — NOT recurrence computation. The
// Explicit Non-Behaviors section forbids re-implementing cron/RRULE occurrence
// math client-side; picking a unit word for an already-known millisecond
// count, or formatting an already-known instant as HH:MM, is not that.

/**
 * Format a `DayBucket.interval_ms` as a short human label, e.g. `1_800_000` →
 * `"every 30 min"`. Picks the largest whole unit that evenly divides the
 * interval (day > hour > min > sec); falls back to a rounded second count for
 * a non-whole-unit interval (should not occur given the server's 60 s
 * validation floor, but never throws).
 */
export function formatIntervalLabel(intervalMs: number): string {
  const SEC = 1000
  const MIN = 60 * SEC
  const HOUR = 60 * MIN
  const DAY = 24 * HOUR
  if (intervalMs > 0 && intervalMs % DAY === 0) {
    const n = intervalMs / DAY
    return `every ${n} day${n === 1 ? '' : 's'}`
  }
  if (intervalMs > 0 && intervalMs % HOUR === 0) {
    const n = intervalMs / HOUR
    return `every ${n} hour${n === 1 ? '' : 's'}`
  }
  if (intervalMs > 0 && intervalMs % MIN === 0) {
    const n = intervalMs / MIN
    return `every ${n} min`
  }
  const n = Math.max(1, Math.round(intervalMs / SEC))
  return `every ${n} sec`
}

/**
 * Label for an aggregated `DayBucket` chip, e.g. `"· every 30 min"` or, when
 * the rule is irregular (`interval_ms === null`), `"· 4×/day"` (FR-009).
 */
export function formatBucketLabel(bucket: { count: number; interval_ms: number | null }): string {
  return bucket.interval_ms != null
    ? `· ${formatIntervalLabel(bucket.interval_ms)}`
    : `· ${bucket.count}×/day`
}

/** Format a Unix epoch-ms instant as a local 24h `HH:MM` string (tooltip use only, e.g. "first at 09:00"). */
export function formatTimeOfDay(ms: number): string {
  const d = new Date(ms)
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  return `${hh}:${mm}`
}

/**
 * The last calendar day covered by a `TaskOccurrenceSet`'s returned data
 * (max of every raw instant and every bucket's `day_start_ms`) — where the
 * truncation marker renders when `truncated: true`. Reads only timestamps the
 * server already returned; not a recurrence computation. `null` when the set
 * carries no data at all (defensive — should not occur when `truncated`).
 */
export function lastCoveredOccurrenceDayMs(set: TaskOccurrenceSet): number | null {
  let max: number | null = null
  for (const ms of set.occurrences_ms) {
    if (max === null || ms > max) max = ms
  }
  for (const bucket of set.day_buckets) {
    if (max === null || bucket.day_start_ms > max) max = bucket.day_start_ms
  }
  return max
}

// ─── Main mapping function ────────────────────────────────────────────────────

/**
 * Map workspace tasks, milestones, and (optionally) server-expanded recurring
 * occurrences into FullCalendar EventInput objects.
 *
 * Rules (§6, FR-002/003/004/005, F-01/F-06/F-19):
 *  - Tasks with `surface !== 'user'` (e.g. 'heartbeat') → excluded (FR-003, F-01).
 *  - Task with parseable `due` → all-day `:due` event coloured by status.
 *  - Task with `once` trigger and `config.at_ms` → timed `:fire` event, Clock icon.
 *  - A task may yield BOTH a `:due` and a `:fire` event (F-19).
 *  - Milestone with parseable `due_date` → all-day `:milestone` event, gold + Flag.
 *  - Unparseable dates → silently skipped, never throw (FR-003).
 *  - Due/fire/milestone events are `editable: true` (drag enabled) — unchanged
 *    regression baseline (Calendar Recurrence Redesign test 17).
 *
 * Occurrence rules (calendar-recurrence-redesign-spec.md, FR-008/009/012,
 * closes the F-10 v1 cut — `every`/`recurring` tasks no longer render zero
 * events): `occurrenceSets` (default `[]`, so existing 2-arg call sites are
 * unchanged) is joined to `tasks` by `task_id`:
 *  - A set whose `task_id` has no matching task is dropped entirely
 *    (defense-in-depth — the server's own selection predicate should already
 *    exclude it, but the client never trusts a dangling id).
 *  - Each raw instant in `occurrences_ms` → one timed `task-occurrence` chip
 *    (title = task title; time = the instant).
 *  - Each `DayBucket` → one all-day `task-occurrence-agg` chip on
 *    `day_start_ms`, titled `"{task title} {label}"` where `label` is derived
 *    from `interval_ms` (`formatBucketLabel` — never recomputed client-side),
 *    carrying a `"first at HH:MM"` tooltip from `first_ms`.
 *  - `truncated: true` → one all-day `task-occurrence-more` marker on the
 *    last covered day (`lastCoveredOccurrenceDayMs`) — never a silently
 *    emptier calendar beyond it.
 *  - All occurrence/aggregated/marker chips are `editable: false` (FR-012).
 */
export function mapToCalendarEvents(
  tasks: Task[],
  milestones: Milestone[],
  occurrenceSets: TaskOccurrenceSet[] = [],
): EventInput[] {
  const events: EventInput[] = []

  for (const task of tasks) {
    // In practice the only non-user surface is 'heartbeat'; allow-list on 'user'
    // to stay future-proof (FR-003, F-01).
    if (task.surface && task.surface !== 'user') continue

    const style = STATUS_STYLE[task.status] ?? STATUS_STYLE_FALLBACK

    // All-day :due event (FR-002).
    if (task.due) {
      const dueDate = parseLocalDate(task.due)
      if (dueDate !== null) {
        events.push(
          makeEvent(`task:${task.id}:due`, dueDate, task.title, style.bg, true, {
            kind: 'task-due',
            status: task.status,
            icon: style.icon,
            taskId: task.id,
          }),
        )
      }
    }

    // ── Timed :fire event for once triggers (FR-002, US-3) ───────────────────
    // `every` and `recurring` triggers never have a single `at_ms` — their
    // occurrences render via the separate occurrenceSets loop below instead
    // (closes the old F-10 v1 "recurring tasks are invisible" cut).
    if (task.trigger?.type === 'once' && task.trigger.config?.at_ms != null) {
      const fireDate = parseMs(task.trigger.config.at_ms)
      if (fireDate !== null) {
        events.push(
          makeEvent(`task:${task.id}:fire`, fireDate, task.title, style.bg, false, {
            kind: 'task-fire',
            status: task.status,
            icon: 'Clock', // always overrides the status icon on fire chips (§6)
            taskId: task.id,
          }),
        )
      }
    }
  }

  // ── Recurring occurrence chips (FR-008/009/012, closes F-10) ──────────────
  // Joined to `tasks` by `task_id`; a set for a task not in `tasks` is dropped
  // (defense-in-depth, per the endpoint's own task-selection predicate note).
  if (occurrenceSets.length > 0) {
    const taskById = new Map(tasks.map((t) => [t.id, t]))

    for (const set of occurrenceSets) {
      const task = taskById.get(set.task_id)
      if (!task) continue

      const style = STATUS_STYLE[task.status] ?? STATUS_STYLE_FALLBACK

      // Individual raw instants → timed task-occurrence chips.
      for (const ms of set.occurrences_ms) {
        const instant = parseMs(ms)
        if (instant === null) continue
        events.push(
          makeEvent(
            `task:${task.id}:occurrence:${ms}`,
            instant,
            task.title,
            style.bg,
            false,
            {
              kind: 'task-occurrence',
              taskId: task.id,
              status: task.status,
              icon: 'Clock',
            },
            false,
          ),
        )
      }

      // Aggregated days → one all-day task-occurrence-agg chip per bucket.
      for (const bucket of set.day_buckets) {
        const dayStart = parseMs(bucket.day_start_ms)
        if (dayStart === null) continue
        events.push(
          makeEvent(
            `task:${task.id}:occurrence-agg:${bucket.day_start_ms}`,
            dayStart,
            `${task.title} ${formatBucketLabel(bucket)}`,
            style.bg,
            true,
            {
              kind: 'task-occurrence-agg',
              taskId: task.id,
              status: task.status,
              icon: 'Clock',
              tooltip: `first at ${formatTimeOfDay(bucket.first_ms)}`,
            },
            false,
          ),
        )
      }

      // Truncated expansion → one non-interactive marker on the last covered day.
      if (set.truncated) {
        const lastMs = lastCoveredOccurrenceDayMs(set)
        const markerDate = lastMs === null ? null : parseMs(lastMs)
        if (markerDate !== null) {
          events.push(
            makeEvent(
              `task:${task.id}:occurrence-more`,
              markerDate,
              'More not shown',
              style.bg,
              true,
              {
                kind: 'task-occurrence-more',
                taskId: task.id,
                status: task.status,
                icon: 'Clock',
                tooltip: 'More occurrences not shown',
              },
              false,
            ),
          )
        }
      }
    }
  }

  // ── Milestone all-day events (FR-004) ────────────────────────────────────
  for (const m of milestones) {
    if (!m.due_date) continue
    const dueDate = parseLocalDate(m.due_date)
    if (dueDate === null) continue

    events.push(
      makeEvent(`milestone:${m.id}`, dueDate, m.name, MILESTONE_STYLE.bg, true, {
        kind: 'milestone',
        icon: 'Flag',
        milestoneId: m.id,
      }),
    )
  }

  return events
}
