// Single source of truth for the 5-state Plan lifecycle palette (ADR-049).
//
// Mirrors src/lib/statusColors.ts exactly (SD-C6) — but is a DELIBERATELY
// SEPARATE module. Plan state and Task status are different domains (a
// `failed` Plan and a `failed` Task are not the same thing) and must never
// share a colour-lookup symbol — this codebase already paid down that exact
// divergence once for tasks (see statusColors.ts's header comment) and SD-C6
// says explicitly: never repeat it for plans.
//
// Round-1 Grill Reconciliation R1 is the authoritative source for the 5-value
// enum: draft, approved, running, done, failed. `plan_phase` and
// `paused_reason`/`failed_reason` are NOT states — they are secondary chips a
// caller may render alongside the primary badge (see PlansFilterBand.tsx) — this
// module only maps the 5 closed `PlanState` values (+ an unknown fallback).

import type { Plan } from '@/lib/api'

export type PlanState = Plan['state']

/** The five lifecycle states, in natural progression order. */
export const PLAN_STATE_ORDER: readonly PlanState[] = [
  'draft',
  'approved',
  'running',
  'done',
  'failed',
]

/** Per-state accent hex. The single source of truth for Plan badge colour. */
export const PLAN_STATE_COLORS: Record<PlanState, string> = {
  draft: '#9ca3af', // neutral grey — being authored
  approved: '#3B82F6', // info blue — locked in, transitional
  running: '#D4AF37', // Forge Gold — the marquee "live" accent
  done: '#10b981', // success green — terminal, quiet
  failed: '#ef4444', // error red — terminal, loud
}

/** Per-state human-readable label. */
export const PLAN_STATE_LABELS: Record<PlanState, string> = {
  draft: 'Draft',
  approved: 'Approved',
  running: 'Running',
  done: 'Done',
  failed: 'Failed',
}

/**
 * Resolve a Plan-state hex, tolerating an unrecognised/undefined wire value
 * by falling back to `draft` (forward-compat belt-and-suspenders per R1 —
 * an unrecognised FUTURE state renders as draft rather than crashing the
 * badge). KEEP this fallback — it is intentional, not a bug.
 */
export function planStateColor(state: PlanState | string | undefined): string {
  return PLAN_STATE_COLORS[(state as PlanState) ?? 'draft'] ?? PLAN_STATE_COLORS.draft
}

/** Resolve a Plan-state label, with the same unknown→draft fallback. */
export function planStateLabel(state: PlanState | string | undefined): string {
  return PLAN_STATE_LABELS[(state as PlanState) ?? 'draft'] ?? PLAN_STATE_LABELS.draft
}

// ── Cancelled discriminator (ADR-052 FR-015/US-8) ───────────────────────────
//
// A user-Stop sets `state: 'failed'` + `failed_reason: 'stopped_by_user'` —
// NOT a new PlanState, NOT a new board column. It must render as an orange
// "Cancelled" marker, visually distinct from a genuine red "Failed"
// (judge_rounds_exhausted / idle_expired), while staying in the same Failed
// bucket everywhere a plan's state badge is drawn (PlansFilterBand tiles,
// WorkspaceGraphTab's plan info strip). Matches the existing warning-orange
// token's VALUE (`--color-warning: #EAB308` in globals.css, already the
// "blocked"/paused accent) rather than inventing a new hex — dark-first
// tokens, single source of truth.

/**
 * Orange accent for a user-cancelled plan — distinct from `PLAN_STATE_COLORS.failed`
 * (red). A LITERAL hex, not a `var(--color-warning)` CSS custom-property
 * reference (Gate-2 finding #1): every consumer (PlansFilterBand,
 * WorkspaceGraphTab) builds its badge tint by string-concatenating an alpha
 * suffix onto this value (`` `${color}1a` ``) — `var(--color-warning)1a` is not
 * valid CSS (`var()` doesn't accept a trailing token), so that declaration was
 * silently dropped and the "Cancelled" badge rendered with NO background tint,
 * unlike the red "Failed" badge built from the literal-hex `PLAN_STATE_COLORS.failed`.
 * A literal 6-digit hex keeps the concat producing a valid 8-digit `#RRGGBBAA`.
 * Kept in sync by hand with `statusColors.ts`'s `TASK_CANCELLED_COLOR` — see
 * that module's header comment for why the two aren't a shared symbol.
 */
export const PLAN_CANCELLED_COLOR = '#EAB308'

/** True when this plan is `failed` specifically because the user Stopped it (not a genuine failure). */
export function isPlanCancelled(plan: Pick<Plan, 'state' | 'failed_reason'>): boolean {
  return plan.state === 'failed' && plan.failed_reason === 'stopped_by_user'
}

/** The badge colour to render for this plan — orange "Cancelled" overrides red "Failed". */
export function planDisplayColor(plan: Pick<Plan, 'state' | 'failed_reason'>): string {
  return isPlanCancelled(plan) ? PLAN_CANCELLED_COLOR : planStateColor(plan.state)
}

/** The badge label to render for this plan — "Cancelled" overrides "Failed". */
export function planDisplayLabel(plan: Pick<Plan, 'state' | 'failed_reason'>): string {
  return isPlanCancelled(plan) ? 'Cancelled' : planStateLabel(plan.state)
}

/**
 * Secondary-chip label for a running/failed plan's sub-state (R1 — never
 * treated as a `PlanState` itself). Returns null when there is nothing to
 * show (e.g. `plan_phase: 'idle'`, or no `paused_reason`/`failed_reason`).
 */
export function planSecondaryChipLabel(plan: {
  state: PlanState
  plan_phase?: string
  paused_reason?: string
  failed_reason?: string
}): string | null {
  if (plan.state === 'running') {
    if (plan.paused_reason) return `paused — ${plan.paused_reason}`
    if (plan.plan_phase && plan.plan_phase !== 'idle') return plan.plan_phase
    return null
  }
  if (plan.state === 'failed' && plan.failed_reason) {
    switch (plan.failed_reason) {
      case 'stopped_by_user':
        return 'stopped by user'
      case 'judge_rounds_exhausted':
        return 'judge rounds exhausted'
      case 'idle_expired':
        return 'idle expired'
      default:
        return plan.failed_reason
    }
  }
  return null
}
