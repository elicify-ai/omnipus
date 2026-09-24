// Omnipus — end-of-observation-window verdict for a real-LLM plan E2E.
//
// Pure (no Playwright, no I/O) so its rule can be unit-tested with vitest
// against recorded CI poll traces; the E2E spec feeds it its own
// GET /api/v1/plans/{id} samples.
//
// WHY THIS EXISTS: Conformance_t3b_TargetedRetryOnlyE2E used to judge "no
// wedge" from ONE poll taken the instant its fixed observation window closed,
// accepting only done/failed/awaiting_supervision. Its plan can never be met
// (m2's stub worker fails every run), so it cycles judge → hold → correction
// until its round budget runs out, and how many cycles fit in the window is
// pure model latency. Whenever the window happened to close while a judge
// round was in flight, a plan that was moving normally was reported as
// wedged. Release/v0.1.1 CI run 35823380746 (job 107060080905) is the
// recorded case: rounds 1-3 finished in 18/40/64 s, the third supervision
// turn took 4.5 min of model steps, and the window closed 61 s into round 4's
// judge turn — see plan-window-end.test.ts for the full trace.
//
// The honest question is not "which phase is the plan in at an arbitrary
// instant" but "has the plan been stuck in one phase for longer than the
// product itself allows". That is what this verdict answers.

/** One GET /api/v1/plans/{id} poll, as the spec recorded it. */
export interface PlanPollSample {
  /** Wall-clock time the poll was taken (Date.now()). */
  atMs: number
  state: string
  phase: string
  judgeRounds: number
}

export type PlanWindowEndVerdict =
  /** done or failed — a documented terminus. */
  | { kind: 'terminus' }
  /** Parked at the supervision hold. */
  | { kind: 'held' }
  /**
   * Mid-round and still inside the product's own bound for that round: keep
   * polling until deadlineMs; the round must end by then.
   */
  | { kind: 'in_round'; phase: string; sinceMs: number; deadlineMs: number }
  /** Anything else: the plan is not moving the way the product documents. */
  | { kind: 'wedged'; reason: string }

export const HOLD_PHASE = 'awaiting_supervision'

/** pkg/agent/plan_engine.go::planJudgeRoundTimeout — one plan judge round's wall-time bound. */
export const PLAN_JUDGE_ROUND_TIMEOUT_MS = 10 * 60_000
/** pkg/agent/plan_engine.go::defaultPlanEngineTickInterval. */
export const PLAN_ENGINE_TICK_MS = 30_000
/**
 * How long a plan may legitimately sit in one active-round phase: the judge
 * round bound plus two engine ticks for the phase change to be written. The
 * same cap is applied to dispatching and synthesizing, which have no product
 * timeout of their own; for the conformance plans that use this verdict every
 * member ends within seconds, so minutes in either phase would itself be a
 * fault.
 */
export const ACTIVE_ROUND_BOUND_MS = PLAN_JUDGE_ROUND_TIMEOUT_MS + 2 * PLAN_ENGINE_TICK_MS

const ACTIVE_ROUND_PHASES = new Set(['dispatching', 'judging', 'synthesizing'])

/**
 * Classify where the plan stands, from every poll the spec took, at nowMs.
 * The time a plan has spent in its current phase is measured from the first
 * of the consecutive polls that saw that same (state, phase, judge_rounds).
 */
export function planWindowEndVerdict(samples: readonly PlanPollSample[], nowMs: number): PlanWindowEndVerdict {
  const last = samples[samples.length - 1]
  if (!last) return { kind: 'wedged', reason: 'no poll was ever recorded' }
  if (last.state === 'done' || last.state === 'failed') return { kind: 'terminus' }
  if (last.phase === HOLD_PHASE) return { kind: 'held' }
  if (last.state !== 'running' || !ACTIVE_ROUND_PHASES.has(last.phase)) {
    return { kind: 'wedged', reason: `state="${last.state}" phase="${last.phase}" is neither a terminus, the hold, nor an active round` }
  }
  let first = samples.length - 1
  while (first > 0) {
    const prev = samples[first - 1]
    if (prev.state !== last.state || prev.phase !== last.phase || prev.judgeRounds !== last.judgeRounds) break
    first--
  }
  const sinceMs = samples[first].atMs
  const deadlineMs = sinceMs + ACTIVE_ROUND_BOUND_MS
  if (nowMs > deadlineMs) {
    return {
      kind: 'wedged',
      reason:
        `state="running" phase="${last.phase}" judge_rounds=${last.judgeRounds} unchanged for ` +
        `${Math.round((nowMs - sinceMs) / 1000)}s — longer than the ${ACTIVE_ROUND_BOUND_MS / 1000}s the product allows one round`,
    }
  }
  return { kind: 'in_round', phase: last.phase, sinceMs, deadlineMs }
}
