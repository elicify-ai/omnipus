// Shared helpers for the task create/edit forms (trigger, due, datetime conversions).
//
// These keep the CreateTaskSlideOver and the TaskDetailPanel in lockstep so a
// trigger built in the create form round-trips identically through the detail
// panel. All wire shapes are the generated TaskTrigger / Todo types — no
// hand-rolled wire structs (Constraint #8).

import type { TaskTrigger } from '@/lib/api'
import { parseRruleString, summarizeRecurrence } from '@/lib/calendar/recurrence'

export type TriggerKind = TaskTrigger['type'] // 'manual' | 'once' | 'every' | 'recurring'

/**
 * Build a TaskTrigger from a kind + config, dropping empty/undefined config keys.
 * Always returns a well-formed {type, config} object (config never null).
 */
export function buildTrigger(
  type: TriggerKind,
  config: { at_ms?: number; every_ms?: number; cron_expr?: string },
): TaskTrigger {
  const clean: Record<string, number | string> = {}
  if (config.at_ms != null) clean.at_ms = config.at_ms
  if (config.every_ms != null) clean.every_ms = config.every_ms
  if (config.cron_expr) clean.cron_expr = config.cron_expr
  return { type, config: clean }
}

/**
 * Convert an epoch-ms instant (or RFC3339 string) to the value a
 * `<input type="datetime-local">` expects (local time, `YYYY-MM-DDTHH:mm`).
 * Returns '' when the input is missing/invalid.
 */
export function toDatetimeLocalValue(value?: number | string | null): string {
  if (value == null || value === '') return ''
  const d = typeof value === 'number' ? new Date(value) : new Date(value)
  if (Number.isNaN(d.getTime())) return ''
  // Offset to local time, then slice off seconds/timezone.
  const tzOffsetMs = d.getTimezoneOffset() * 60_000
  return new Date(d.getTime() - tzOffsetMs).toISOString().slice(0, 16)
}

/**
 * Convert a `datetime-local` input value (local wall-clock) to epoch ms.
 * Returns null when empty/invalid.
 */
export function datetimeLocalToMs(value: string): number | null {
  if (!value) return null
  const ms = new Date(value).getTime()
  return Number.isNaN(ms) ? null : ms
}

/**
 * Convert a `datetime-local` input value to an RFC3339 UTC string for the
 * `due` field. Returns null when empty/invalid.
 */
export function datetimeLocalToIso(value: string): string | null {
  if (!value) return null
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? null : d.toISOString()
}

/**
 * Convert a `datetime-local` input value (or any date-ish string, e.g. an
 * RFC3339 `due` timestamp) to a `Date`, built on `datetimeLocalToMs`.
 * Returns null when missing/empty/invalid — the inline
 * `value ? new Date(value) : null` pattern this replaces at DateTimePicker
 * call sites, minus the risk of silently constructing an Invalid Date.
 */
export function datetimeLocalToDate(value?: string | null): Date | null {
  if (!value) return null
  const ms = datetimeLocalToMs(value)
  return ms == null ? null : new Date(ms)
}

/**
 * Convert a `Date` (or null) to the `datetime-local` input value a
 * DateTimePicker's `onChange` hands to form state, built on
 * `toDatetimeLocalValue`. Returns '' for null — the inline
 * `d ? toDatetimeLocalValue(d.getTime()) : ''` pattern this replaces.
 */
export function dateToDatetimeLocal(date: Date | null): string {
  return date ? toDatetimeLocalValue(date.getTime()) : ''
}

/**
 * Whether a trigger is a repeat-rule trigger (`every`/`recurring`). NARROWER
 * than the calendar-only boundary (see `isScheduledTrigger` below, which is
 * the one Board/List actually use to decide visibility) — this predicate is
 * for call sites that specifically care about REPEATING behaviour, not mere
 * schedule-bearing: `canDropTransition`'s repeating-task drag guard
 * (BoardView.tsx — a failed repeating task can't be restarted by dragging)
 * and TaskDetailPanel's raw-cron/rrule-hiding guard (a `once` trigger has no
 * rule string to hide, so it stays fully editable there). Accepts `trigger`
 * directly (not the whole `Task`) so callers can use it inline as
 * `isRecurringTrigger(t.trigger)`.
 */
export function isRecurringTrigger(trigger?: TaskTrigger | null): trigger is TaskTrigger {
  return trigger?.type === 'every' || trigger?.type === 'recurring'
}

/**
 * Whether a trigger carries ANY schedule — a concrete one-shot instant
 * (`once`), a fixed interval (`every`), or a repeat rule (`recurring`).
 * Superset of `isRecurringTrigger` (adds `once`). Board and List exclude
 * every task whose trigger matches this predicate — schedule-bearing tasks
 * live exclusively on the workspace calendar (operator ruling 2026-08-07:
 * "recurring and ALL scheduled tasks are calendar-only"). Only a `manual`
 * trigger (or no trigger at all — the default) leaves a task on Board/List.
 * Accepts `trigger` directly (not the whole `Task`) so BoardView/ListView can
 * call it inline as `isScheduledTrigger(t.trigger)`.
 */
export function isScheduledTrigger(trigger?: TaskTrigger | null): trigger is TaskTrigger {
  return trigger?.type === 'once' || trigger?.type === 'every' || trigger?.type === 'recurring'
}

/**
 * Defensive, read-only plain-English summary for a recurring trigger (FR-023).
 * Used ONLY by the generic TaskDetailPanel's defensive guard — normally
 * unreachable since Board/List exclude recurring tasks (isRecurringTrigger
 * above), reachable only via stale cache or a race. Deliberately never
 * includes `cron_expr` or `rrule` — no raw cron/rule string is ever displayed
 * outside the calendar editor (D8/D9). NOT the same as `triggerSummary` below,
 * which is a general-purpose label that DOES surface the raw cron string and
 * must not be reused for this guard.
 */
export function recurringTriggerSummary(trigger: TaskTrigger): string {
  if (trigger.type === 'every') {
    const ms = trigger.config?.every_ms
    if (typeof ms === 'number' && ms > 0) {
      const minutes = Math.round(ms / 60_000)
      return `Repeats every ${minutes} minute${minutes === 1 ? '' : 's'}`
    }
    return 'Repeats on a fixed interval'
  }
  // FR-023: a real plain-English summary of the actual rule, not a generic
  // placeholder — mirrors the calendar editor's own summarizeRecurrence, but
  // never renders the raw rrule/cron string (D8/D9).
  const rrule = trigger.config?.rrule
  const dtstartMs = trigger.config?.dtstart_ms
  if (typeof rrule === 'string' && typeof dtstartMs === 'number') {
    const state = parseRruleString(rrule, dtstartMs)
    if (state) {
      return summarizeRecurrence({ kind: 'rrule', state }, new Date(dtstartMs))
    }
  }
  return 'Repeats on a recurring schedule'
}

/** Human label for a trigger summary line. */
export function triggerSummary(trigger?: TaskTrigger | null): string {
  if (!trigger || trigger.type === 'manual') return 'Manual (drag to run)'
  if (trigger.type === 'once') {
    const at = trigger.config?.at_ms
    return `Once — ${at ? new Date(at).toLocaleString() : '(time unset)'}`
  }
  if (trigger.type === 'every') {
    const ms = trigger.config?.every_ms
    return `Every ${ms ? Math.round(ms / 60_000) + 'm' : '(interval unset)'}`
  }
  if (trigger.type === 'recurring') {
    return `Recurring — ${trigger.config?.cron_expr ?? '(no cron)'}`
  }
  return 'Manual (drag to run)'
}
