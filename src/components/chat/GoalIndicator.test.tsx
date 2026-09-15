// GoalIndicator.test.tsx — ADR-049 D6/US-12/FR-094/SD-C9; ADR-081 D5/D9
// (work-first goal flow) for the retired `queued` state.
//
// Pure presentational component, driven entirely by props (goalStatus/
// loopStatus). Covers the ADR-053 9-value pill enum (queued/active/
// waiting_on_user/judge_unavailable/re-planning/judging/done/failed/
// cleared) — superseding the original 4-value active/
// paused_judge_unavailable/brake_fired/cleared set, no back-compat.
// CORRECTED (regression review): this comment previously claimed "there is
// no `cleared` literal any more" — false as of the UAT S3 fix, which
// re-added `cleared` as a 9th value specifically so a user-initiated
// `/goal clear` renders as a deliberate stop, not a failure (see the
// `cleared` describe block below). "Renders nothing" is still covered by
// `goalStatus === null` — `cleared` is a normal non-null frame like any
// other terminal state. `queued` (ADR-081 D5/D9) is now the ONE non-null
// state that also renders nothing — it is retired and never emitted by the
// backend anymore; the wire-enum value survives only in the generated type
// (Constraint #8), so this component still defensively renders nothing for
// it rather than the pre-ADR-081 "queued — waiting for a free loop slot"
// summary line.

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { GoalIndicator } from './GoalIndicator'
import type { GoalStatusFrame, LoopStatusFrame } from '@/lib/api/generated/asyncapi-types'

function makeGoal(overrides: Partial<GoalStatusFrame> = {}): GoalStatusFrame {
  return {
    type: 'goal_status',
    session_id: 's1',
    condition: 'ship the release',
    round: 3,
    max_rounds: 20,
    latest_reason: 'tests still failing',
    active_loops: 1,
    cap: 16,
    state: 'active',
    ...overrides,
  }
}

function makeLoop(overrides: Partial<LoopStatusFrame> = {}): LoopStatusFrame {
  return {
    type: 'loop_status',
    session_id: 's1',
    mode: 'interval',
    run: 2,
    max_runs: 10,
    next_delay: 900,
    state: 'active',
    ...overrides,
  }
}

describe('GoalIndicator — active state', () => {
  it('renders the condition, round N/20, and the latest judge reason', () => {
    render(<GoalIndicator goalStatus={makeGoal()} />)
    expect(screen.getByTestId('goal-indicator')).toBeInTheDocument()
    expect(screen.getByTestId('goal-indicator-condition')).toHaveTextContent('ship the release')
    expect(screen.getByTestId('goal-indicator-round')).toHaveTextContent('round 3/20')
    expect(screen.getByTestId('goal-indicator-round')).toHaveTextContent('active loops 1/16')
    expect(screen.getByText('tests still failing')).toBeInTheDocument()
  })

  it('truncates a very long condition with a title tooltip (Edge Case: 4000-char condition)', () => {
    const longCondition = 'x'.repeat(4000)
    render(<GoalIndicator goalStatus={makeGoal({ condition: longCondition })} />)
    const el = screen.getByTestId('goal-indicator-condition')
    expect(el.textContent!.length).toBeLessThan(longCondition.length)
    expect(el).toHaveAttribute('title', longCondition)
  })
})

// ADR-081 D5/D9: `queued` is retired — the backend never emits it anymore.
// Unlike every other non-null state, it renders NOTHING (same as a null
// goalStatus), defensively, in case a stale/legacy frame ever carries it.
describe('GoalIndicator — queued state (retired, ADR-081 D5/D9)', () => {
  it('renders nothing for a queued frame — no summary line, no round line, no indicator container', () => {
    const { container } = render(<GoalIndicator goalStatus={makeGoal({ state: 'queued' })} />)
    expect(container).toBeEmptyDOMElement()
    expect(screen.queryByTestId('goal-indicator')).not.toBeInTheDocument()
    expect(screen.queryByTestId('goal-indicator-queued')).not.toBeInTheDocument()
    expect(screen.queryByTestId('goal-indicator-round')).not.toBeInTheDocument()
  })

  it('still renders the loop status line alongside a queued (suppressed) goal frame', () => {
    render(<GoalIndicator goalStatus={makeGoal({ state: 'queued' })} loopStatus={makeLoop()} />)
    expect(screen.getByTestId('loop-status-line')).toBeInTheDocument()
    expect(screen.queryByTestId('goal-indicator-queued')).not.toBeInTheDocument()
  })
})

describe('GoalIndicator — waiting_on_user state', () => {
  it('shows "waiting on you"', () => {
    render(<GoalIndicator goalStatus={makeGoal({ state: 'waiting_on_user' })} />)
    expect(screen.getByTestId('goal-indicator-waiting')).toHaveTextContent('waiting on you')
  })
})

describe('GoalIndicator — judge_unavailable state', () => {
  it('shows "paused — waiting on judge" and does not show the round line', () => {
    render(<GoalIndicator goalStatus={makeGoal({ state: 'judge_unavailable' })} />)
    expect(screen.getByTestId('goal-indicator-paused')).toHaveTextContent('paused — waiting on judge')
    expect(screen.queryByTestId('goal-indicator-round')).not.toBeInTheDocument()
  })
})

describe('GoalIndicator — re-planning state', () => {
  it('shows a re-planning summary line', () => {
    render(<GoalIndicator goalStatus={makeGoal({ state: 're-planning' })} />)
    expect(screen.getByTestId('goal-indicator-replanning')).toHaveTextContent('re-planning — awaiting your correction')
  })
})

describe('GoalIndicator — judging state', () => {
  it('shows "judging…"', () => {
    render(<GoalIndicator goalStatus={makeGoal({ state: 'judging' })} />)
    expect(screen.getByTestId('goal-indicator-judging')).toHaveTextContent('judging…')
  })
})

describe('GoalIndicator — done state', () => {
  it('shows "done" and still surfaces the latest judge reason', () => {
    render(<GoalIndicator goalStatus={makeGoal({ state: 'done' })} />)
    expect(screen.getByTestId('goal-indicator-done')).toHaveTextContent('done')
    expect(screen.getByText('tests still failing')).toBeInTheDocument()
  })
})

describe('GoalIndicator — failed state', () => {
  it('shows "failed"', () => {
    render(<GoalIndicator goalStatus={makeGoal({ state: 'failed' })} />)
    expect(screen.getByTestId('goal-indicator-failed')).toHaveTextContent('failed')
  })
})

describe('GoalIndicator — cleared state', () => {
  it('shows "cleared"', () => {
    render(<GoalIndicator goalStatus={makeGoal({ state: 'cleared' })} />)
    expect(screen.getByTestId('goal-indicator-cleared')).toHaveTextContent('cleared')
  })

  // Regression guard (mutation-testing finding): a mutation that rendered
  // `cleared` identically to `failed` (same testid `goal-indicator-failed`,
  // same error/red tone, text "failed") passed every other test here,
  // because nothing asserted `cleared`'s own testid/tone/text existed at
  // all. `cleared` means the user deliberately stopped the goal — it MUST
  // NOT read as a failure. This test fails under that exact mutation.
  it('renders cleared DISTINCTLY from failed — different testid, different text, not the error/red tone', () => {
    const { unmount } = render(<GoalIndicator goalStatus={makeGoal({ state: 'cleared' })} />)
    const clearedLine = screen.getByTestId('goal-indicator-cleared')
    expect(screen.queryByTestId('goal-indicator-failed')).not.toBeInTheDocument()
    expect(clearedLine).toHaveTextContent('cleared')
    expect(clearedLine).not.toHaveTextContent('failed')
    expect(clearedLine.className).toContain('color-muted')
    expect(clearedLine.className).not.toContain('color-error')
    unmount()

    render(<GoalIndicator goalStatus={makeGoal({ state: 'failed' })} />)
    const failedLine = screen.getByTestId('goal-indicator-failed')
    expect(failedLine.className).toContain('color-error')
  })
})

// Five states added by the joint ADR-084/ADR-085/ADR-086 delivery (C-39).
describe('GoalIndicator — judge_refused_god_mode state (JUDGE-FR-057a)', () => {
  it('shows a distinct, operator-actionable line naming god mode as the cause', () => {
    render(<GoalIndicator goalStatus={makeGoal({ state: 'judge_refused_god_mode' })} />)
    const line = screen.getByTestId('goal-indicator-judge-refused-god-mode')
    expect(line).toHaveTextContent('god mode')
    expect(screen.queryByTestId('goal-indicator-paused')).not.toBeInTheDocument()
  })
})

describe('GoalIndicator — judge_cas_loss state (JUDGE-FR-083)', () => {
  it('shows a distinct line, not the judge_unavailable one', () => {
    render(<GoalIndicator goalStatus={makeGoal({ state: 'judge_cas_loss' })} />)
    expect(screen.getByTestId('goal-indicator-judge-cas-loss')).toBeInTheDocument()
    expect(screen.queryByTestId('goal-indicator-paused')).not.toBeInTheDocument()
  })
})

describe('GoalIndicator — blocked state (JUDGE-FR-093)', () => {
  it('shows a distinct line, not the waiting_on_user one', () => {
    render(<GoalIndicator goalStatus={makeGoal({ state: 'blocked' })} />)
    expect(screen.getByTestId('goal-indicator-blocked')).toBeInTheDocument()
    expect(screen.queryByTestId('goal-indicator-waiting')).not.toBeInTheDocument()
  })
})

describe('GoalIndicator — claim_overturned state (JUDGE-FR-102)', () => {
  it('shows a distinct "claim overturned" line', () => {
    render(<GoalIndicator goalStatus={makeGoal({ state: 'claim_overturned' })} />)
    expect(screen.getByTestId('goal-indicator-claim-overturned')).toHaveTextContent('overturned')
  })
})

describe('GoalIndicator — expired state (ADR-086 GOAL-FR-028)', () => {
  it('shows "expired", distinct from failed and cleared', () => {
    render(<GoalIndicator goalStatus={makeGoal({ state: 'expired' })} />)
    expect(screen.getByTestId('goal-indicator-expired')).toHaveTextContent('expired')
    expect(screen.queryByTestId('goal-indicator-failed')).not.toBeInTheDocument()
    expect(screen.queryByTestId('goal-indicator-cleared')).not.toBeInTheDocument()
  })
})

describe('GoalIndicator — no frame', () => {
  it('renders nothing when goalStatus is null and there is no loop either', () => {
    const { container } = render(<GoalIndicator goalStatus={null} />)
    expect(container).toBeEmptyDOMElement()
  })
})

describe('GoalIndicator — loop status line', () => {
  it('renders mode/run/next-delay when a loopStatus is present, even with no goal', () => {
    render(<GoalIndicator goalStatus={null} loopStatus={makeLoop()} />)
    const line = screen.getByTestId('loop-status-line')
    expect(line).toHaveTextContent('every')
    expect(line).toHaveTextContent('run 2/10')
    expect(line).toHaveTextContent('15m')
  })

  it('renders self-paced mode without a next_delay', () => {
    const loop = makeLoop({ mode: 'self_paced', run: 4, max_runs: 4, next_delay: undefined })
    render(<GoalIndicator goalStatus={null} loopStatus={loop} />)
    const line = screen.getByTestId('loop-status-line')
    expect(line).toHaveTextContent('self-paced')
    expect(line).toHaveTextContent('run 4/4')
  })
})
