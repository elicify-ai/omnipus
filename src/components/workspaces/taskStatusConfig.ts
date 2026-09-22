// taskStatusConfig — single source of truth for Task status labels.
//
// Extracted from TaskDetailPanel.tsx so both the full task detail panel and
// the calendar's TaskRunStatusField (a read-only badge for the recurring-task
// EDIT slide-over) render the exact same status vocabulary — no drift between
// the two surfaces that show a task's lifecycle state. Status pill COLOURS
// live in `StatusBadge.tsx` (a component, not a class-string helper here) —
// see that file's doc comment for why.
//
// `skipped` is a `TaskRun.status`-only value (a scheduled fire the backend's
// overlap guard declined to run because the previous occurrence was still
// `in_progress`) — NOT a `Task['status']` member. `statusLabel` below widens
// to accept it because both `TaskRunsList.tsx` and `TaskRunStatusField.tsx`
// call it with a `TaskRun['status']` value (a strict superset-minus-`next`/
// `inbox`/`blocked` of `Task['status']`, plus `skipped`), not `Task['status']`
// itself.

import type { Task } from '@/lib/api'
import type { TaskRun } from '@/lib/api'

// User-settable status options (blocked is excluded — it is backend-derived
// and read-only). Colour classes for these are resolved by TaskDetailPanel's
// own local `statusOptionTextClass` (a literal `switch`, not a data lookup,
// kept in that file rather than here so the design-system static scanners —
// which cannot resolve a class-returning function across a module boundary —
// see the switch in the same file as its JSX call site; see that function's
// own doc comment).
export const STATUS_OPTIONS: { value: Task['status']; label: string }[] = [
  { value: 'inbox',       label: 'Inbox' },
  { value: 'next',        label: 'Next' },
  { value: 'in_progress', label: 'In Progress' },
  { value: 'done',        label: 'Done' },
  { value: 'failed',      label: 'Failed' },
]

/**
 * Human-readable label for a status value. Widened to accept both
 * `Task['status']` (the 7-state task lifecycle, including the backend-derived
 * "blocked" state) and `TaskRun['status']` (the run-outcome vocabulary,
 * including "skipped") — `TaskRunsList.tsx`/`TaskRunStatusField.tsx` call
 * this with a `TaskRun['status']` value, which is not assignable to
 * `Task['status']` now that `skipped` exists on runs only.
 */
export function statusLabel(status: Task['status'] | TaskRun['status']): string {
  if (status === 'blocked') return 'Blocked'
  if (status === 'skipped') return 'Skipped'
  return STATUS_OPTIONS.find((o) => o.value === status)?.label ?? status
}
