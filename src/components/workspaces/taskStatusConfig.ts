// taskStatusConfig — single source of truth for Task status labels/colors.
//
// Extracted from TaskDetailPanel.tsx so both the full task detail panel and
// the calendar's TaskRunStatusField (a read-only badge for the recurring-task
// EDIT slide-over) render the exact same status vocabulary — no drift between
// the two surfaces that show a task's lifecycle state.

import type { Task } from '@/lib/api'

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
}

/** Human-readable label for a status value, including the backend-derived "blocked" state. */
export function statusLabel(status: Task['status']): string {
  if (status === 'blocked') return 'Blocked'
  return STATUS_OPTIONS.find((o) => o.value === status)?.label ?? status
}
