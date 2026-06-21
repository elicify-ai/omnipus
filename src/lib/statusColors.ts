// Single source of truth for the 7-state task-lifecycle status palette.
//
// The SAME task must read identically across every workspace view — the Board
// columns, the parent-card roll-up, the nested subtask rows, the List table,
// and the dependency Graph. Before this module each view hard-coded its own hex,
// so `in_progress` was Forge Gold (#d4af37) on the Graph but #EAB308 on the
// Board, and `planning` flipped between blue and purple. That divergence both
// confused users and violated the Sovereign Deep brand (the marquee "live work"
// colour MUST be Forge Gold #D4AF37).
//
// Import STATUS_COLORS / STATUS_LABELS / statusColor from here everywhere a
// status needs a colour or a human label. Never re-hardcode a status hex.
//
// Palette (Sovereign Deep):
//   inbox       — neutral grey   (captured, untriaged)
//   next        — info blue      (triaged, ready)
//   planning    — info blue lt   (agent decomposing — a lighter blue, NOT purple)
//   in_progress — Forge Gold     (live work — the marquee accent #D4AF37)
//   blocked     — warning orange (unmet dependency)
//   done        — success green  (terminal, quiet)
//   failed      — error red      (terminal, loud)

import type { Task } from '@/lib/api'

export type TaskStatus = Task['status']

/** The seven lifecycle states, in board/pipeline order. */
export const STATUS_ORDER: readonly TaskStatus[] = [
  'inbox',
  'next',
  'planning',
  'in_progress',
  'blocked',
  'done',
  'failed',
]

/** Per-status accent hex. The single source of truth for status colour. */
export const STATUS_COLORS: Record<TaskStatus, string> = {
  inbox: '#9ca3af', // neutral grey
  next: '#3B82F6', // info blue
  planning: '#60A5FA', // lighter info blue (coherent with `next`, NOT purple)
  in_progress: '#D4AF37', // Forge Gold — the marquee "live work" accent
  blocked: '#F97316', // warning orange
  done: '#10b981', // success green
  failed: '#ef4444', // error red
}

/** Per-status human-readable label. */
export const STATUS_LABELS: Record<TaskStatus, string> = {
  inbox: 'Inbox',
  next: 'Next',
  planning: 'Planning',
  in_progress: 'In Progress',
  blocked: 'Blocked',
  done: 'Done',
  failed: 'Failed',
}

/** Statuses whose incident graph edges animate (live work). */
export const STATUS_ANIMATED: Record<TaskStatus, boolean> = {
  inbox: false,
  next: false,
  planning: false,
  in_progress: true,
  blocked: false,
  done: false,
  failed: false,
}

/** Terminal/quiet statuses whose graph edges are muted. */
export const STATUS_MUTED: Record<TaskStatus, boolean> = {
  inbox: false,
  next: false,
  planning: false,
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
