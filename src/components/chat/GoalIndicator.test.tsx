// GoalIndicator.test.tsx — ADR-049 D6/US-12/FR-094/SD-C9.
//
// Pure presentational component, driven entirely by props (goalStatus/
// loopStatus) — mirrors the BDD "Goal indicator states" scenario outline
// exactly: active/paused_judge_unavailable/brake_fired/cleared.

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

describe('GoalIndicator — paused_judge_unavailable state', () => {
  it('shows "paused — waiting on judge" and does not show the round line', () => {
    render(<GoalIndicator goalStatus={makeGoal({ state: 'paused_judge_unavailable' })} />)
    expect(screen.getByTestId('goal-indicator-paused')).toHaveTextContent('paused — waiting on judge')
    expect(screen.queryByTestId('goal-indicator-round')).not.toBeInTheDocument()
  })
})

describe('GoalIndicator — brake_fired state', () => {
  it('shows "winding down (bound reached)"', () => {
    render(<GoalIndicator goalStatus={makeGoal({ state: 'brake_fired' })} />)
    expect(screen.getByTestId('goal-indicator-brake')).toHaveTextContent('winding down (bound reached)')
  })
})

describe('GoalIndicator — cleared state', () => {
  it('renders nothing (indicator removed)', () => {
    const { container } = render(<GoalIndicator goalStatus={makeGoal({ state: 'cleared' })} />)
    expect(container).toBeEmptyDOMElement()
  })

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
    const { next_delay: _drop, ...rest } = makeLoop({ mode: 'self_paced', run: 4, max_runs: 4 })
    render(<GoalIndicator goalStatus={null} loopStatus={rest as LoopStatusFrame} />)
    const line = screen.getByTestId('loop-status-line')
    expect(line).toHaveTextContent('self-paced')
    expect(line).toHaveTextContent('run 4/4')
  })
})
