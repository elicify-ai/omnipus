/**
 * ws.new-frames-validation.test.ts — ADR-049 R3/FR-099/SC-049.
 *
 * The four new Planning & Goals WS frames (`goal_status`, `loop_status`,
 * `plan_status`, `judge_verdict`) are already part of the generated
 * `WsFrame` discriminated-union Zod schema (Wave 0) — `parseFrameSafe`
 * requires no code change to validate them. This suite pins that: valid
 * payloads parse, and malformed ones drop + increment the counter, exactly
 * like every other frame type (mirrors `describe('parseFrameSafe', ...)` in
 * ws.test.ts).
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { parseFrameSafe, resetDroppedFrameCount, getDroppedFrameCount } from './ws'

beforeEach(() => {
  resetDroppedFrameCount()
})

describe('parseFrameSafe — goal_status (ADR-049 R3)', () => {
  const valid = {
    type: 'goal_status',
    session_id: 's1',
    condition: 'ship the release',
    round: 3,
    max_rounds: 20,
    latest_reason: 'still failing',
    active_loops: 1,
    cap: 16,
    state: 'active',
  }

  it('parses a valid frame', () => {
    const result = parseFrameSafe(JSON.stringify(valid))
    expect(result).not.toBeNull()
    expect(result?.type).toBe('goal_status')
  })

  it('drops + increments the counter when session_id is missing', () => {
    const { session_id: _drop, ...rest } = valid
    const result = parseFrameSafe(JSON.stringify(rest))
    expect(result).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })

  it('drops an invalid state literal', () => {
    const result = parseFrameSafe(JSON.stringify({ ...valid, state: 'not_a_real_state' }))
    expect(result).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })
})

describe('parseFrameSafe — loop_status (ADR-049 R3)', () => {
  const valid = {
    type: 'loop_status',
    session_id: 's1',
    mode: 'interval',
    run: 2,
    max_runs: 10,
    next_delay: 900,
    state: 'active',
  }

  it('parses a valid frame', () => {
    const result = parseFrameSafe(JSON.stringify(valid))
    expect(result).not.toBeNull()
    expect(result?.type).toBe('loop_status')
  })

  it('parses without the optional next_delay field', () => {
    const { next_delay: _drop, ...rest } = valid
    const result = parseFrameSafe(JSON.stringify(rest))
    expect(result).not.toBeNull()
  })

  it('drops + increments the counter for an invalid mode', () => {
    const result = parseFrameSafe(JSON.stringify({ ...valid, mode: 'bogus_mode' }))
    expect(result).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })

  it('drops + increments the counter when session_id is missing', () => {
    const { session_id: _drop, ...rest } = valid
    const result = parseFrameSafe(JSON.stringify(rest))
    expect(result).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })
})

describe('parseFrameSafe — plan_status (ADR-049 R1/R3 — NO session_id)', () => {
  const valid = {
    type: 'plan_status',
    plan_id: 'plan-1',
    state: 'running',
    plan_phase: 'dispatching',
    progress: 0.5,
  }

  it('parses a valid frame (no session_id required — global frame)', () => {
    const result = parseFrameSafe(JSON.stringify(valid))
    expect(result).not.toBeNull()
    expect(result?.type).toBe('plan_status')
  })

  it('parses with the optional paused_reason field', () => {
    const result = parseFrameSafe(JSON.stringify({ ...valid, paused_reason: 'owner_disabled' }))
    expect(result).not.toBeNull()
  })

  it('drops + increments the counter for an invalid state literal', () => {
    const result = parseFrameSafe(JSON.stringify({ ...valid, state: 'bogus' }))
    expect(result).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })

  it('drops + increments the counter for progress out of 0..1 range', () => {
    const result = parseFrameSafe(JSON.stringify({ ...valid, progress: 1.5 }))
    expect(result).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })
})

describe('parseFrameSafe — judge_verdict (ADR-049 D2/D4/R3 — NO session_id)', () => {
  const valid = {
    type: 'judge_verdict',
    id: 'verdict-1',
    scope: 'task',
    task_id: 'task-1',
    round: 1,
    met: true,
    per_criterion: [{ criterion_id: 'crit-1', met: true, reason: 'ok' }],
    model: 'z-ai/glm-5-turbo',
    judged_at: '2026-07-19T12:05:00Z',
    judge_agent_id: 'judge',
  }

  it('parses a valid frame (no session_id required — global frame)', () => {
    const result = parseFrameSafe(JSON.stringify(valid))
    expect(result).not.toBeNull()
    expect(result?.type).toBe('judge_verdict')
  })

  it('drops + increments the counter for a missing per_criterion field', () => {
    const { per_criterion: _drop, ...rest } = valid
    const result = parseFrameSafe(JSON.stringify(rest))
    expect(result).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })

  it('drops + increments the counter for an invalid scope literal', () => {
    const result = parseFrameSafe(JSON.stringify({ ...valid, scope: 'bogus' }))
    expect(result).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })

  it('drops a per_criterion entry missing a required field', () => {
    const result = parseFrameSafe(
      JSON.stringify({ ...valid, per_criterion: [{ criterion_id: 'crit-1', met: true }] }),
    )
    expect(result).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })
})

/**
 * Regression: every `plan_phase` the backend can emit must be accepted by the
 * frame schema.
 *
 * `parseFrameSafe` DROPS any frame that fails `safeParse`, and the drop is
 * whole-frame — not just the offending field. So a phase value missing from
 * this enum silently discards the entire `plan_status` push for that plan,
 * taking `state` and `progress` down with it. The UI then only catches up on
 * its 15s poll, which is why this stayed invisible.
 *
 * `awaiting_supervision` was missing here while being a live value of
 * `Plan.plan_phase` (pkg/plan/plan.go) that `websocket.go` forwards verbatim
 * as `PlanPhase: p.PlanPhase` — found while adding `stalled`. Enumerated
 * explicitly rather than spot-checked so a future phase added to the Go
 * constants but not to the contract fails here instead of in production.
 */
describe('parseFrameSafe — plan_status accepts every emittable plan_phase', () => {
  const phases = [
    'dispatching',
    'judging',
    'synthesizing',
    'idle',
    'awaiting_supervision',
    'stalled',
  ] as const

  it.each(phases)('accepts plan_phase=%s', (phase) => {
    const frame = {
      type: 'plan_status',
      plan_id: 'p1',
      state: 'running',
      plan_phase: phase,
      progress: 0.5,
    }
    const result = parseFrameSafe(JSON.stringify(frame))
    expect(result, `plan_phase="${phase}" must not be dropped`).not.toBeNull()
    expect(result?.type).toBe('plan_status')
    expect(getDroppedFrameCount()).toBe(0)
  })

  it('still drops a genuinely unknown plan_phase', () => {
    const frame = {
      type: 'plan_status',
      plan_id: 'p1',
      state: 'running',
      plan_phase: 'not_a_real_phase',
      progress: 0.5,
    }
    expect(parseFrameSafe(JSON.stringify(frame))).toBeNull()
    expect(getDroppedFrameCount()).toBe(1)
  })
})
