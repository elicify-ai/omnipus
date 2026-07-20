// dateFormat — shared "medium date + short time" formatter (Q1 dedup,
// task-run-history reviewer findings).
//
// Before this file, `Intl.DateTimeFormat(undefined, { dateStyle: 'medium',
// timeStyle: 'short' })` with a `'—'` empty fallback was duplicated verbatim
// in TaskRunsList.tsx and TaskRunStatusField.tsx (both ISO-string inputs),
// and re-implemented a second time under a different name/signature in
// TaskDetailPanel.tsx (ISO-string) and CalendarEventSlideOver.tsx (epoch-ms
// number, no fallback since call sites pre-guarded for null). One shared
// helper — accepting either shape — collapses all four onto a single
// implementation.

/**
 * Format an ISO-8601 date-time string OR a Unix epoch-ms number as a local
 * "medium date, short time" string (e.g. "Jul 20, 2026, 9:00 AM"). `null` /
 * `undefined` / an unparseable value all fall back to `'—'` — never throws.
 */
export function formatDateTime(input: string | number | null | undefined): string {
  if (input == null) return '—'
  const d = new Date(input)
  if (isNaN(d.getTime())) return '—'
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(d)
}
