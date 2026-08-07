// planStateColors.test.ts — Plan-state badge matrix (US-10 AS-3, SD-C6, SC-042).
//
// Dataset: Plan-state badge matrix (planning-goals-spec.md, Part C).

import { describe, it, expect } from 'vitest'
import {
  PLAN_STATE_COLORS,
  PLAN_STATE_LABELS,
  PLAN_STATE_ORDER,
  PLAN_CANCELLED_COLOR,
  planStateColor,
  planStateLabel,
  planSecondaryChipLabel,
  planPhaseChip,
  planPhaseExplanation,
  planDisplayColor,
  AWAITING_SUPERVISION_EXPLANATION,
  STALLED_EXPLANATION,
} from './planStateColors'

describe('planStateColors — badge matrix', () => {
  it.each([
    ['draft', 'Draft', '#9ca3af'],
    ['approved', 'Approved', '#3B82F6'],
    ['running', 'Running', '#D4AF37'],
    ['done', 'Done', '#10b981'],
    ['failed', 'Failed', '#ef4444'],
  ] as const)('state=%s -> label=%s hex=%s', (state, label, hex) => {
    expect(planStateColor(state)).toBe(hex)
    expect(planStateLabel(state)).toBe(label)
    expect(PLAN_STATE_COLORS[state]).toBe(hex)
    expect(PLAN_STATE_LABELS[state]).toBe(label)
  })

  it('has exactly 5 states in PLAN_STATE_ORDER', () => {
    expect(PLAN_STATE_ORDER.length).toBe(5)
  })

  it('unknown/undefined state falls back to draft (forward-compat, R1)', () => {
    expect(planStateColor(undefined)).toBe(PLAN_STATE_COLORS.draft)
    expect(planStateColor('')).toBe(PLAN_STATE_COLORS.draft)
    expect(planStateColor('some-future-state')).toBe(PLAN_STATE_COLORS.draft)
    expect(planStateLabel('some-future-state')).toBe(PLAN_STATE_LABELS.draft)
  })

  it('never shares a colour value with a different state (no accidental aliasing)', () => {
    const hexes = Object.values(PLAN_STATE_COLORS)
    expect(new Set(hexes).size).toBe(hexes.length)
  })
})

// Gate-2 finding #1 regression: every consumer (PlansFilterBand,
// WorkspaceGraphTab) builds its badge tint fill as a bare string concat —
// `` `${displayColor}1a` `` — not a CSS function call. A `var(--x)` value
// concatenated with a trailing alpha token (`var(--color-warning)1a`) is not
// valid CSS (`var()` doesn't accept a trailing token past its closing paren),
// so that declaration would be silently dropped by the browser and the
// "Cancelled" badge would render with no background tint at all — orange
// text on a transparent pill, inconsistent with the red "Failed" pill (built
// from the already-literal-hex `PLAN_STATE_COLORS.failed`). This asserts
// directly on the exported constant/string, not on DOM/CSSOM behavior (which
// varies by environment and won't reliably reject invalid CSS for us).
describe('PLAN_CANCELLED_COLOR / planDisplayColor — literal-hex tint mechanism (Gate-2 finding #1)', () => {
  it('is a literal hex, not a var(...) CSS custom-property reference', () => {
    expect(PLAN_CANCELLED_COLOR).not.toMatch(/^var\(/)
    expect(PLAN_CANCELLED_COLOR).toMatch(/^#[0-9a-fA-F]{6}$/)
  })

  it('matches the --color-warning token value (#EAB308) it stands in for', () => {
    expect(PLAN_CANCELLED_COLOR).toBe('#EAB308')
  })

  it('planDisplayColor for a cancelled plan produces a valid 8-digit hex once the "1a" tint suffix is appended', () => {
    const tint = `${planDisplayColor({ state: 'failed', failed_reason: 'stopped_by_user' })}1a`
    expect(tint).toMatch(/^#[0-9a-fA-F]{8}$/)
  })

  it('is visually distinct from PLAN_STATE_COLORS.failed while using the same literal-hex mechanism', () => {
    expect(PLAN_CANCELLED_COLOR).not.toBe(PLAN_STATE_COLORS.failed)
    expect(PLAN_STATE_COLORS.failed).toMatch(/^#[0-9a-fA-F]{6}$/)
  })
})

describe('planSecondaryChipLabel — R1 secondary chips (plan_phase / paused_reason / failed_reason)', () => {
  it('returns null for a running plan with no phase/pause info', () => {
    expect(planSecondaryChipLabel({ state: 'running' })).toBeNull()
    expect(planSecondaryChipLabel({ state: 'running', plan_phase: 'idle' })).toBeNull()
  })

  it('shows the plan_phase for a running plan when not idle', () => {
    expect(planSecondaryChipLabel({ state: 'running', plan_phase: 'dispatching' })).toBe('dispatching')
    expect(planSecondaryChipLabel({ state: 'running', plan_phase: 'judging' })).toBe('judging')
  })

  it('prefers paused_reason over plan_phase for a paused running plan', () => {
    expect(
      planSecondaryChipLabel({ state: 'running', plan_phase: 'dispatching', paused_reason: 'owner agent disabled' }),
    ).toBe('paused — owner agent disabled')
  })

  it('returns null for draft/approved/done with no reason fields', () => {
    expect(planSecondaryChipLabel({ state: 'draft' })).toBeNull()
    expect(planSecondaryChipLabel({ state: 'approved' })).toBeNull()
    expect(planSecondaryChipLabel({ state: 'done' })).toBeNull()
  })

  it('distinguishes the three failed_reason variants (O1 r2 — never collapse to generic "Failed")', () => {
    expect(planSecondaryChipLabel({ state: 'failed', failed_reason: 'stopped_by_user' })).toBe('stopped by user')
    expect(planSecondaryChipLabel({ state: 'failed', failed_reason: 'judge_rounds_exhausted' })).toBe('judge rounds exhausted')
    expect(planSecondaryChipLabel({ state: 'failed', failed_reason: 'idle_expired' })).toBe('idle expired')
  })

  it('returns null for a failed plan with no failed_reason', () => {
    expect(planSecondaryChipLabel({ state: 'failed' })).toBeNull()
  })
})

describe('planPhaseChip — ADR-053 FE-2 §7 Graph plan-phase chip (D7, US-14)', () => {
  it('surfaces awaiting_supervision as the re-planning warning (operator-actionable)', () => {
    expect(planPhaseChip({ state: 'running', plan_phase: 'awaiting_supervision' })).toEqual({
      label: 'Re-planning — awaiting supervision',
      tone: 'warning',
    })
  })

  it.each([
    ['dispatching', 'Dispatching'],
    ['judging', 'Judging'],
    ['synthesizing', 'Synthesizing'],
  ] as const)('renders the %s sub-phase as a quieter info chip capitalized as %s', (phase, label) => {
    expect(planPhaseChip({ state: 'running', plan_phase: phase })).toEqual({ label, tone: 'info' })
  })

  it('returns null for a running plan on the idle sub-phase (nothing to surface)', () => {
    expect(planPhaseChip({ state: 'running', plan_phase: 'idle' })).toBeNull()
  })

  it('returns null for a running plan with no plan_phase', () => {
    expect(planPhaseChip({ state: 'running' })).toBeNull()
  })

  it('returns null when not running (plan_phase is a runtime-only sub-phase)', () => {
    expect(planPhaseChip({ state: 'approved', plan_phase: 'judging' })).toBeNull()
    expect(planPhaseChip({ state: 'failed', plan_phase: 'awaiting_supervision' })).toBeNull()
    expect(planPhaseChip({ state: 'draft', plan_phase: 'dispatching' })).toBeNull()
    expect(planPhaseChip({ state: 'done' })).toBeNull()
  })

  // Swimlane-board UAT fix — a running plan the engine has no dispatchable
  // or in-flight member for (pkg/agent/plan_engine.go's surfaceStallIfAny)
  // must render a distinct, actionable warning chip, never silently blend
  // into a quiet info chip the way dispatching/judging/synthesizing do.
  it('surfaces stalled as its own warning chip, distinct from awaiting_supervision', () => {
    expect(planPhaseChip({ state: 'running', plan_phase: 'stalled' })).toEqual({
      label: 'Stalled — needs a correction',
      tone: 'warning',
    })
  })
})

describe('planPhaseExplanation — phase-specific warning-chip copy (swimlane-board fix)', () => {
  it('resolves awaiting_supervision to its own explanation', () => {
    expect(planPhaseExplanation({ state: 'running', plan_phase: 'awaiting_supervision' })).toBe(
      AWAITING_SUPERVISION_EXPLANATION,
    )
  })

  it('resolves stalled to its own DIFFERENT explanation (never reuses the awaiting_supervision copy)', () => {
    const explanation = planPhaseExplanation({ state: 'running', plan_phase: 'stalled' })
    expect(explanation).toBe(STALLED_EXPLANATION)
    expect(explanation).not.toBe(AWAITING_SUPERVISION_EXPLANATION)
  })

  it('the stalled explanation never names a raw task UUID — it is static, owner-agent-only detail stays server-side', () => {
    // Guards against ever templating HandoverText (which DOES name internal
    // task IDs, e.g. "blocked on ... 7a2992f0-...") into this user-facing copy.
    expect(STALLED_EXPLANATION).not.toMatch(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i)
  })

  it('returns null for the quieter info-tone phases and for idle/no-phase', () => {
    expect(planPhaseExplanation({ state: 'running', plan_phase: 'dispatching' })).toBeNull()
    expect(planPhaseExplanation({ state: 'running', plan_phase: 'idle' })).toBeNull()
    expect(planPhaseExplanation({ state: 'running' })).toBeNull()
  })

  it('returns null when not running', () => {
    expect(planPhaseExplanation({ state: 'failed', plan_phase: 'stalled' })).toBeNull()
    expect(planPhaseExplanation({ state: 'draft', plan_phase: 'awaiting_supervision' })).toBeNull()
  })
})

// The copy a reader actually sees, asserted as text rather than as a constant
// reference — a test that only compares against the constant passes no matter
// what the constant says, which is how the pre-supervisor wording ("there's no
// in-app action for that yet — Stop this plan and create a new one with the fix
// instead") stayed shipped after corrections became real.
describe('planPhaseExplanation — the rendered copy is true of the current release', () => {
  const parked = () =>
    planPhaseExplanation({ state: 'running', plan_phase: 'awaiting_supervision' }) ?? ''
  const stalled = () => planPhaseExplanation({ state: 'running', plan_phase: 'stalled' }) ?? ''

  it('tells the parked-plan reader a supervisor is correcting it, and that Stop is the control', () => {
    expect(parked()).toBe(
      "This plan hit a dead end its own checks can't clear. A supervisor is reviewing it and will correct it automatically — no action needed. If you'd rather it stopped, Stop this plan (■); that halts the supervisor too.",
    )
  })

  it('tells the stalled-plan reader the same, in its own distinct words', () => {
    expect(stalled()).toBe(
      "This plan has no members it can currently dispatch or that are in progress, so it can't make progress right now. A supervisor is reviewing why and will correct it automatically. If you'd rather it stopped, Stop this plan (■).",
    )
    expect(stalled()).not.toBe(parked())
  })

  it.each([
    ['awaiting_supervision', parked],
    ['stalled', stalled],
  ])('neither phase (%s) still claims corrections are unavailable', (_phase, text) => {
    expect(text()).not.toMatch(/no in-app action/i)
    expect(text()).not.toMatch(/create a new (one|plan)/i)
  })

  it.each([
    ['awaiting_supervision', parked],
    ['stalled', stalled],
  ])('%s names the supervisor and offers Stop as the one real control', (_phase, text) => {
    expect(text()).toMatch(/supervisor/i)
    expect(text()).toContain('Stop this plan (■)')
  })

  it('never names a corrective verb as something the reader can do — those are the supervisor’s', () => {
    // append / supersede / targeted-retry / abandon are issued by
    // PlanSupervisor; this release ships no human control for any of them, so
    // the copy must not imply one (the honesty constraint in planStateColors.ts).
    for (const text of [parked(), stalled()]) {
      expect(text).not.toMatch(/supersede|targeted[- ]retry|append a tail/i)
    }
  })
})
