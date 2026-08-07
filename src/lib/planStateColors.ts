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

// ── ADR-053 FE-2 §7 (D7, US-14) — Graph plan-phase chip ─────────────────────
//
// The Graph view surfaces the plan's runtime sub-phase as a chip beside the
// state badge so an operator lands on actionable context when a plan tile
// auto-switches to Graph (FE-2). `plan_phase` is a runtime-only sub-phase
// while `state == running` (NOT a state itself — see planStateColors header).
//
// `awaiting_supervision` is the marquee case: the plan reached all-
// terminal-but-unmet and durably holds for an owner correction. The goal-pill
// crosswalk (R§8.10) reconstructs THIS persisted phase as "re-planning" (so
// it survives restart, reconstructing as re-planning NOT "waiting_on_user").
// FE-2 surfaces it as an operator-actionable warning chip — the owner must
// append a correction (tail / supersede / targeted-retry) for the plan to
// continue. The other non-idle sub-phases (dispatching/judging/synthesizing)
// render as a quieter info chip — a live-progress hint, not actionable.

export type PlanPhaseChipTone = 'warning' | 'info'

export interface PlanPhaseChip { // not-wire-format: pure UI display type (chip label + tone); no json tags, never serialised across the wire
  label: string
  tone: PlanPhaseChipTone
}

/**
 * The Graph-view plan-phase chip (FE-2). Returns:
 *  - `{ 'Re-planning — awaiting supervision', 'warning' }` for
 *    `plan_phase: 'awaiting_supervision'` (the durable re-planning hold);
 *  - `{ '<Capitalized phase>', 'info' }` for the other non-idle running
 *    sub-phases (dispatching / judging / synthesizing);
 *  - `null` when there's nothing to surface (not running, `idle`, or no phase).
 *
 * `paused_reason` is intentionally NOT handled here — a paused running plan
 * shows its pause via `planSecondaryChipLabel` elsewhere; this helper is the
 * single source of truth for the FE-2 phase chip alone.
 */
export function planPhaseChip(plan: {
  state: PlanState
  plan_phase?: string
}): PlanPhaseChip | null {
  if (plan.state !== 'running') return null
  const phase = plan.plan_phase
  if (!phase || phase === 'idle') return null
  if (phase === 'awaiting_supervision') {
    return { label: 'Re-planning — awaiting supervision', tone: 'warning' }
  }
  // `stalled` (swimlane-board UAT fix) — a DIFFERENT warning-tone condition
  // from awaiting_supervision (a live DAG nothing can currently
  // dispatch, vs. a judge dead end on an all-terminal one). Never collapse
  // the two into the same label/copy — see planPhaseExplanation below for
  // why they also carry distinct explanation text.
  if (phase === 'stalled') {
    return { label: 'Stalled — needs a correction', tone: 'warning' }
  }
  return { label: phase.charAt(0).toUpperCase() + phase.slice(1), tone: 'info' }
}

// ── S2 UAT finding — `awaiting_supervision` plain-language copy ───────
//
// The warning chip above communicates urgency but not WHAT is needed. Before
// this fix the only explanation anywhere was a hover-only `title` tooltip on
// the WorkspaceGraphTab chip (dead on touch, easy to miss entirely, and not
// wired into PlansFilterBand's tile at all). This constant is the single,
// ALWAYS-VISIBLE explanation both surfaces render as plain text — not a
// tooltip — so a first-time user gets it without hovering or tapping anything.
//
// Honesty constraint (do not relax without re-checking the backend): word this
// ONLY for mechanisms that actually exist, and never as if the reader clicks
// them. The corrective verbs the phase turns on — append a tail, supersede a
// done member, targeted-retry a frozen member, or abandon — are issued by
// PlanSupervisor, the built-in adjudicator woken for this phase (ADR-055).
// They are the SUPERVISOR's actions, not user-facing controls, and this
// release adds no human control for any of them; naming one as something the
// reader can do would repeat the exact complaint this copy was written to fix,
// just in the opposite direction. What genuinely changed is that an autonomous
// supervisor is now working on the plan — worth saying, because it tells the
// reader why waiting is the right move — and that Stop now halts the
// supervisor along with the plan. Stop remains the one REAL control here: the
// plan stays in `state: 'running'` throughout this phase, so
// `PlanActionButton` (ADR-052 §6.8) renders the ■ Stop action right next to
// this chip. Name that, and only that.
export const AWAITING_SUPERVISION_EXPLANATION =
  "This plan hit a dead end its own checks can't clear. A supervisor is reviewing it and will correct it automatically — no action needed. If you'd rather it stopped, Stop this plan (■); that halts the supervisor too."

// ── Stalled plan-phase copy (swimlane-board fix) ────────────────────────────
//
// `plan_phase: 'stalled'` (backend detection: pkg/agent/plan_engine.go's
// planStallReason/surfaceStallIfAny) — the plan is `running` with real work
// still outstanding, but no member is currently dispatchable or in flight
// (e.g. blocked on a dependency this plan's own dispatch loop can never
// itself resolve). This is a GENUINELY DIFFERENT condition from
// `awaiting_supervision` above (that one is a judge dead end on an
// all-terminal DAG; this one is a live DAG nothing can currently move) and
// must never reuse that constant's copy — the two conditions need their own
// explanations, mirrored via `tone: 'warning'` chips.
//
// Kept intentionally STATIC/generic — the backend's HandoverText reason
// names internal task UUIDs for the SUPERVISOR to act on (surfaced via the
// existing async-notifier wake), never meant for this chip: rendering those
// IDs here would expose meaningless internal identifiers to a human reader.
// It carries the same honesty constraint as the constant above, and for the
// same reason: a supervisor is reviewing this too, so the old "no in-app
// action yet — create a new plan" wording is now simply wrong. The one real
// control available is likewise the same: Stop (the plan stays
// `state: 'running'` throughout, so `PlanActionButton` still renders ■).
export const STALLED_EXPLANATION =
  "This plan has no members it can currently dispatch or that are in progress, so it can't make progress right now. A supervisor is reviewing why and will correct it automatically. If you'd rather it stopped, Stop this plan (■)."

/**
 * Plain-language explanation for a warning-tone `planPhaseChip` — PHASE-
 * SPECIFIC (never a single generic string), so `awaiting_supervision`
 * and `stalled` each render their own accurate copy instead of silently
 * sharing one. Returns null for every other phase (mirrors `planPhaseChip`'s
 * own null-for-nothing-to-show contract) — callers gate rendering on this
 * return value being non-null, not on `chip.tone === 'warning'` directly,
 * so a future third warning-tone phase can't accidentally inherit the wrong
 * text the way a `chip.tone`-only gate would allow.
 */
export function planPhaseExplanation(plan: { state: PlanState; plan_phase?: string }): string | null {
  if (plan.state !== 'running') return null
  switch (plan.plan_phase) {
    case 'awaiting_supervision':
      return AWAITING_SUPERVISION_EXPLANATION
    case 'stalled':
      return STALLED_EXPLANATION
    default:
      return null
  }
}
