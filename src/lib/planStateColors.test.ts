// planStateColors.test.ts — Plan-state badge matrix (US-10 AS-3, SD-C6, SC-042).
//
// Dataset: Plan-state badge matrix (planning-goals-spec.md, Part C).

import { describe, it, expect } from 'vitest'
import {
  PLAN_STATE_COLORS,
  PLAN_STATE_LABELS,
  PLAN_STATE_ORDER,
  planStateColor,
  planStateLabel,
  planSecondaryChipLabel,
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
