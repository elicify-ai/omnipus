/**
 * Pure event-mapping module for the workspace Calendar (FullCalendar v6).
 * Spec: docs/internal/specs/workspace-calendar-fullcalendar-spec.md (v2)
 * §6 canonical map, FR-002, FR-003, FR-004, FR-005, FR-015.
 *
 * No React, no Phosphor imports. Emits StatusIconKey strings only.
 * not-wire-format: internal view model; never sent over the network.
 */

import type { EventInput } from '@fullcalendar/core'
import type { Task, Milestone } from '@/lib/api'
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
 * invariant (near-black text, border = background, draggable) so it is stated
 * once (FR-005). `extendedProps` is typed against the discriminated
 * CalendarEventExtProps union: the single authoritative construction site, so a
 * mismatched/illegal extendedProps shape is now a compile error.
 */
function makeEvent(
  id: string,
  start: Date,
  title: string,
  bg: string,
  allDay: boolean,
  extendedProps: CalendarEventExtProps,
): EventInput {
  return {
    id,
    allDay,
    start,
    title,
    backgroundColor: bg,
    borderColor: bg,
    textColor: CHIP_TEXT_COLOR,
    editable: true,
    extendedProps,
  }
}

// ─── Main mapping function ────────────────────────────────────────────────────

/**
 * Map workspace tasks and milestones into FullCalendar EventInput objects.
 *
 * Rules (§6, FR-002/003/004/005, F-01/F-06/F-10/F-19):
 *  - Tasks with `surface !== 'user'` (e.g. 'heartbeat') → excluded (FR-003, F-01).
 *  - Task with parseable `due` → all-day `:due` event coloured by status.
 *  - Task with `once` trigger and `config.at_ms` → timed `:fire` event, Clock icon.
 *  - Tasks with `every` or `recurring` triggers → no event in v1 (F-10).
 *  - A task may yield BOTH a `:due` and a `:fire` event (F-19).
 *  - Milestone with parseable `due_date` → all-day `:milestone` event, gold + Flag.
 *  - Unparseable dates → silently skipped, never throw (FR-003).
 *  - All produced events are `editable: true` (drag enabled).
 */
export function mapToCalendarEvents(
  tasks: Task[],
  milestones: Milestone[],
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
    // `every` and `recurring` → skipped in v1 (F-10).
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
