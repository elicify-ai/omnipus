// taskStatusConfig — single source of truth for Task status labels/colors.
//
// Extracted from TaskDetailPanel.tsx so both the full task detail panel and
// the calendar's TaskRunStatusField (a read-only badge for the recurring-task
// EDIT slide-over) render the exact same status vocabulary — no drift between
// the two surfaces that show a task's lifecycle state.
//
// `skipped` is a `TaskRun.status`-only value (a scheduled fire the backend's
// overlap guard declined to run because the previous occurrence was still
// `in_progress`) — NOT a `Task['status']` member. It's included in
// `STATUS_BADGE`/`statusLabel` below because both `TaskRunsList.tsx` and
// `TaskRunStatusField.tsx` key these maps off a `TaskRun['status']` value
// (a strict superset-minus-`next`/`inbox`/`planning`/`blocked` of
// `Task['status']`, plus `skipped`), not off `Task['status']` itself.

import type { Task } from '@/lib/api'
import type { TaskRun } from '@/lib/api'

// User-settable status options (blocked is excluded — it is backend-derived
// and read-only). Theme-token colours. `text-[color:…]` keeps these as
// inline-var text colours (no raw Tailwind palette) so status renderers
// track "The Sovereign Deep" tokens.
export const STATUS_OPTIONS: { value: Task['status']; label: string; color: string }[] = [
  { value: 'inbox',       label: 'Inbox',       color: 'text-[var(--color-muted)]' },
  { value: 'next',        label: 'Next',        color: 'text-[color:var(--color-accent)]' },
  { value: 'planning',    label: 'Planning',    color: 'text-[color:var(--color-muted)]' },
  { value: 'in_progress', label: 'In Progress', color: 'text-[color:var(--color-warning)]' },
  { value: 'done',        label: 'Done',        color: 'text-[color:var(--color-success)]' },
  { value: 'failed',      label: 'Failed',      color: 'text-[color:var(--color-error)]' },
]

export const STATUS_BADGE: Record<string, string> = {
  inbox:       'text-[var(--color-muted)] bg-white/5',
  next:        'text-[color:var(--color-accent)] bg-[var(--color-accent)]/10',
  planning:    'text-[color:var(--color-muted)] bg-white/5',
  in_progress: 'text-[color:var(--color-warning)] bg-[var(--color-warning)]/10',
  blocked:     'text-[color:var(--color-warning)] bg-[var(--color-warning)]/10',
  done:        'text-[color:var(--color-success)] bg-[var(--color-success)]/10',
  failed:      'text-[color:var(--color-error)] bg-[var(--color-error)]/10',
  // TaskRun-only outcome (overlap guard) — the established design-system
  // orange (`--color-cancelled`, #F97316), NOT `--color-warning` (yellow,
  // already used for `blocked`/`in_progress` above) — distinct so a skipped
  // run never reads as the same color as those.
  skipped:     'text-[color:var(--color-cancelled)] bg-[var(--color-cancelled)]/10',
}

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
