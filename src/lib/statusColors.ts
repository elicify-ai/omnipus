// Single source of truth for the 6-state task-lifecycle status palette
// (ADR-051 D5 removed the redundant `planning` status now that Plans are
// first-class; existing `planning` tasks are backfilled to `next`).
//
// The SAME task must read identically across every workspace view — the Board
// columns, the parent-card roll-up, the nested subtask rows, the List table,
// and the dependency Graph. Before this module each view hard-coded its own hex,
// so `in_progress` was Forge Gold (#d4af37) on the Graph but #EAB308 on the
// Board. That divergence both confused users and violated the Sovereign Deep
// brand (the marquee "live work" colour MUST be Forge Gold #D4AF37).
//
// Import STATUS_COLORS / STATUS_LABELS / statusColor from here everywhere a
// status needs a colour or a human label. Never re-hardcode a status hex.
//
// Palette (Sovereign Deep):
//   inbox       — neutral grey   (captured, untriaged)
//   next        — info blue      (triaged, ready)
//   in_progress — Forge Gold     (live work — the marquee accent #D4AF37)
//   blocked     — warning orange (unmet dependency)
//   done        — success green  (terminal, quiet)
//   failed      — error red      (terminal, loud)

import type { Task } from '@/lib/api'

export type TaskStatus = Task['status']

/** The six lifecycle states, in board/pipeline order. */
export const STATUS_ORDER: readonly TaskStatus[] = [
  'inbox',
  'next',
  'in_progress',
  'blocked',
  'done',
  'failed',
]

/** Per-status accent hex. The single source of truth for status colour. */
export const STATUS_COLORS: Record<TaskStatus, string> = {
  inbox: '#9ca3af', // neutral grey
  next: '#3B82F6', // info blue
  in_progress: '#D4AF37', // Forge Gold — the marquee "live work" accent
  blocked: '#F97316', // warning orange
  done: '#10b981', // success green
  failed: '#ef4444', // error red
}

/** Per-status human-readable label. */
export const STATUS_LABELS: Record<TaskStatus, string> = {
  inbox: 'Inbox',
  next: 'Next',
  in_progress: 'In Progress',
  blocked: 'Blocked',
  done: 'Done',
  failed: 'Failed',
}

/** Statuses whose incident graph edges animate (live work). */
export const STATUS_ANIMATED: Record<TaskStatus, boolean> = {
  inbox: false,
  next: false,
  in_progress: true,
  blocked: false,
  done: false,
  failed: false,
}

/** Terminal/quiet statuses whose graph edges are muted. */
export const STATUS_MUTED: Record<TaskStatus, boolean> = {
  inbox: false,
  next: false,
  in_progress: false,
  blocked: false,
  done: true,
  failed: false,
}

/** Resolve a status hex, tolerating unknown/undefined wire values (→ inbox). */
export function statusColor(status: TaskStatus | string | undefined): string {
  return STATUS_COLORS[(status as TaskStatus) ?? 'inbox'] ?? STATUS_COLORS.inbox
}

/** Resolve a status label, tolerating unknown/undefined wire values (→ inbox). */
export function statusLabel(status: TaskStatus | string | undefined): string {
  return STATUS_LABELS[(status as TaskStatus) ?? 'inbox'] ?? STATUS_LABELS.inbox
}

// ── Cancelled discriminator (ADR-052 FR-015/FR-028/US-8) ────────────────────
//
// A task Stopped by the user is persisted as `status: 'failed'` +
// `cancel_reason: 'stopped_by_user'` — NOT a new status, NOT a new board
// column. It renders as an orange "Cancelled" marker, distinct from a
// genuine red "Failed" (attempts exhausted), while staying in the Failed
// column/bucket everywhere a task's status is drawn (Board card, List status
// cell, Graph node). Matches the existing warning-orange token's VALUE
// (`--color-warning: #EAB308` in globals.css) rather than a new invented hex
// — mirrors `planStateColors.ts`'s PLAN_CANCELLED_COLOR, deliberately
// duplicated (not shared) because Task/Plan are different domains — see that
// module's header comment.

/**
 * Orange accent for a user-cancelled task — distinct from `STATUS_COLORS.failed`
 * (red). A LITERAL hex, not a `var(--color-warning)` CSS custom-property
 * reference (Gate-2 finding #1): every consumer (TaskCard, PlansFilterBand,
 * WorkspaceGraphTab) builds its pill tint by string-concatenating an alpha
 * suffix onto this value (`` `${color}1a` ``) — `var(--color-warning)1a` is not
 * valid CSS (`var()` doesn't accept a trailing token), so that declaration was
 * silently dropped and the "Cancelled" pill rendered with NO background tint
 * (orange text, transparent fill), unlike the red "Failed" pill built from the
 * literal-hex `STATUS_COLORS.failed`. A literal 6-digit hex keeps the concat
 * producing a valid 8-digit `#RRGGBBAA`.
 */
export const TASK_CANCELLED_COLOR = '#EAB308'

/** True when this task is `failed` specifically because the user Stopped it (not a genuine failure). */
export function isTaskCancelled(task: Pick<Task, 'status' | 'cancel_reason'>): boolean {
  return task.status === 'failed' && task.cancel_reason === 'stopped_by_user'
}

/** The status colour to render for this task — orange "Cancelled" overrides red "Failed". */
export function taskDisplayColor(task: Pick<Task, 'status' | 'cancel_reason'>): string {
  return isTaskCancelled(task) ? TASK_CANCELLED_COLOR : statusColor(task.status)
}

/** The status label to render for this task — "Cancelled" overrides "Failed". */
export function taskDisplayLabel(task: Pick<Task, 'status' | 'cancel_reason'>): string {
  return isTaskCancelled(task) ? 'Cancelled' : statusLabel(task.status)
}
