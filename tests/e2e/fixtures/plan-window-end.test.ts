import { describe, expect, it } from 'vitest'
import { planWindowEndVerdict, type PlanPollSample } from './plan-window-end'

// Oracles come from the product, not from this helper:
//   - pkg/agent/plan_engine.go::planJudgeRoundTimeout = 10 minutes bounds one
//     plan-level judge round's total wall time; the round's own cleanup then
//     moves the plan off plan_phase=judging.
//   - pkg/agent/plan_engine.go::defaultPlanEngineTickInterval = 30 s; the
//     phase change lands on the engine's next pass.
// So a plan observed continuously at "judging" for up to 10 min + 2 ticks
// (660 s) is legitimately mid-round; longer than that is a wedge.
const ROUND_BOUND_MS = 10 * 60_000 + 2 * 30_000

const t = (iso: string) => Date.parse(iso)
const s = (iso: string, state: string, phase: string, judgeRounds: number): PlanPollSample => ({
  atMs: t(iso),
  state,
  phase,
  judgeRounds,
})

// The phase transitions of plan 01M36D4YX2VNXCC6ZH40GGE4F7, recorded from the
// Playwright trace of release/v0.1.1 CI run 35823380746, job 107060080905 (the
// first, failing attempt of Conformance_t3b_TargetedRetryOnlyE2E). The
// observation window opened at approval (05:52:21) and closed 540 s later;
// the last poll saw round 4's judge 61 s into its turn. Rounds 1-3 took 18 s,
// 40 s and 64 s; the third supervision turn took 4.5 min of model steps.
const ciTrace: PlanPollSample[] = [
  s('2026-09-23T05:52:21.448Z', 'approved', 'idle', 0),
  s('2026-09-23T05:52:51.613Z', 'running', 'idle', 0),
  s('2026-09-23T05:53:21.775Z', 'running', 'judging', 0),
  s('2026-09-23T05:53:39.870Z', 'running', 'awaiting_supervision', 1),
  s('2026-09-23T05:53:51.897Z', 'running', 'judging', 1),
  s('2026-09-23T05:54:32.002Z', 'running', 'awaiting_supervision', 2),
  s('2026-09-23T05:54:44.037Z', 'running', 'judging', 2),
  s('2026-09-23T05:55:48.209Z', 'running', 'awaiting_supervision', 3),
  s('2026-09-23T06:00:20.971Z', 'running', 'judging', 3),
  s('2026-09-23T06:01:21.000Z', 'running', 'judging', 3),
]
const windowClose = t('2026-09-23T06:01:21.448Z')

describe('planWindowEndVerdict', () => {
  it('CI run 35823380746: a judge round 61 s old at window close is mid-round, not a wedge', () => {
    expect(planWindowEndVerdict(ciTrace, windowClose)).toEqual({
      kind: 'in_round',
      phase: 'judging',
      sinceMs: t('2026-09-23T06:00:20.971Z'),
      deadlineMs: t('2026-09-23T06:00:20.971Z') + ROUND_BOUND_MS,
    })
  })

  it('that same round finishing into the hold is accepted as held', () => {
    const later = [...ciTrace, s('2026-09-23T06:02:05.000Z', 'running', 'awaiting_supervision', 4)]
    expect(planWindowEndVerdict(later, t('2026-09-23T06:02:05.000Z'))).toEqual({ kind: 'held' })
  })

  it('that same round exhausting the budget into failed is a terminus', () => {
    const later = [...ciTrace, s('2026-09-23T06:02:05.000Z', 'failed', 'judging', 4)]
    expect(planWindowEndVerdict(later, t('2026-09-23T06:02:05.000Z'))).toEqual({ kind: 'terminus' })
  })

  it('done is a terminus', () => {
    expect(planWindowEndVerdict([s('2026-09-23T06:00:00Z', 'done', 'synthesizing', 1)], t('2026-09-23T06:00:00Z'))).toEqual({
      kind: 'terminus',
    })
  })

  it('judging held past the product round bound is a wedge', () => {
    const since = t('2026-09-23T06:00:00.000Z')
    const samples = [
      s('2026-09-23T05:59:40.000Z', 'running', 'awaiting_supervision', 1),
      s('2026-09-23T06:00:00.000Z', 'running', 'judging', 1),
      { atMs: since + ROUND_BOUND_MS + 1, state: 'running', phase: 'judging', judgeRounds: 1 },
    ]
    const v = planWindowEndVerdict(samples, since + ROUND_BOUND_MS + 1)
    expect(v.kind).toBe('wedged')
  })

  it('boundary: exactly at the round bound is still mid-round; one ms past it is a wedge', () => {
    const since = t('2026-09-23T06:00:00.000Z')
    const samples = [s('2026-09-23T06:00:00.000Z', 'running', 'judging', 1)]
    expect(planWindowEndVerdict(samples, since + ROUND_BOUND_MS).kind).toBe('in_round')
    expect(planWindowEndVerdict(samples, since + ROUND_BOUND_MS + 1).kind).toBe('wedged')
  })

  it('the round clock restarts only when the phase actually changes, not on every poll', () => {
    const since = t('2026-09-23T06:00:00.000Z')
    const samples: PlanPollSample[] = []
    for (let i = 0; i <= 12; i++) samples.push({ atMs: since + i * 60_000, state: 'running', phase: 'judging', judgeRounds: 2 })
    // 12 minutes of consecutive polls all at the same judging round: wedged.
    expect(planWindowEndVerdict(samples, since + 12 * 60_000).kind).toBe('wedged')
  })

  it('a post-correction re-dispatch is mid-round', () => {
    const samples = [
      s('2026-09-23T06:00:00.000Z', 'running', 'awaiting_supervision', 1),
      s('2026-09-23T06:00:10.000Z', 'running', 'dispatching', 1),
    ]
    expect(planWindowEndVerdict(samples, t('2026-09-23T06:00:20.000Z')).kind).toBe('in_round')
  })

  it.each([
    ['running but idle', s('2026-09-23T06:00:00Z', 'running', 'idle', 1)],
    ['approved but never started', s('2026-09-23T06:00:00Z', 'approved', 'idle', 0)],
    ['an unknown phase', s('2026-09-23T06:00:00Z', 'running', 'mystery', 1)],
  ])('%s is a wedge', (_name, sample) => {
    expect(planWindowEndVerdict([sample], sample.atMs).kind).toBe('wedged')
  })

  it('no polls at all is a wedge', () => {
    expect(planWindowEndVerdict([], 0).kind).toBe('wedged')
  })
})
