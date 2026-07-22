// GoalPillTray.test.tsx — ADR-053 FE-1 / US-14.
//
// Covers the bottom-right per-goal-id pill tray: one pill per goal-id, all 8
// pill states, click-to-expand, and the latest-verdict enrichment from the
// judgeActivity store.

import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { GoalPillTray } from './GoalPillTray'
import { useChatStore } from '@/store/chat'
import { useJudgeActivityStore } from '@/store/judgeActivity'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

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

describe('GoalPillTray — empty', () => {
  beforeEach(() => {
    useChatStore.setState({ goalPills: {} })
    useJudgeActivityStore.getState().reset()
  })

  it('renders nothing when no goals are active', () => {
    const { container } = render(<GoalPillTray />)
    expect(container).toBeEmptyDOMElement()
  })
})

describe('GoalPillTray — per-goal-id pills', () => {
  beforeEach(() => {
    useChatStore.setState({ goalPills: {} })
    useJudgeActivityStore.getState().reset()
  })

  it('renders one pill for a single goal', () => {
    useChatStore.setState({ goalPills: { g1: makeGoal({ goal_id: 'g1' }) } })
    render(<GoalPillTray />)
    expect(screen.getByTestId('goal-pill-tray')).toBeInTheDocument()
    expect(screen.getByTestId('goal-pill-active')).toBeInTheDocument()
  })

  it('renders TWO pills for a session with two goals (per-goal-id)', () => {
    useChatStore.setState({
      goalPills: {
        g1: makeGoal({ goal_id: 'g1', condition: 'ship release' }),
        g2: makeGoal({ goal_id: 'g2', condition: 'fix bug #42' }),
      },
    })
    render(<GoalPillTray />)
    const pills = screen.getAllByTestId('goal-pill-wrapper')
    expect(pills).toHaveLength(2)
    expect(screen.getByText('ship release')).toBeInTheDocument()
    expect(screen.getByText('fix bug #42')).toBeInTheDocument()
  })

  it('uses _default key for a frame with no goal_id', () => {
    const frame = makeGoal()
    useChatStore.setState({ goalPills: { _default: frame } })
    render(<GoalPillTray />)
    const pill = screen.getByTestId('goal-pill-active')
    expect(pill).toHaveAttribute('data-goal-id', '_default')
  })
})

describe('GoalPillTray — 8-state rendering', () => {
  beforeEach(() => {
    useChatStore.setState({ goalPills: {} })
    useJudgeActivityStore.getState().reset()
  })

  const states: Array<[GoalStatusFrame['state'], string]> = [
    ['queued', 'goal-pill-queued'],
    ['active', 'goal-pill-active'],
    ['waiting_on_user', 'goal-pill-waiting'],
    ['judge_unavailable', 'goal-pill-judge-unavailable'],
    ['re-planning', 'goal-pill-replanning'],
    ['judging', 'goal-pill-judging'],
    ['done', 'goal-pill-done'],
    ['failed', 'goal-pill-failed'],
  ]

  for (const [state, testId] of states) {
    it(`renders the ${state} pill with the correct testId`, () => {
      useChatStore.setState({ goalPills: { g1: makeGoal({ state, goal_id: 'g1' }) } })
      render(<GoalPillTray />)
      expect(screen.getByTestId(testId)).toBeInTheDocument()
    })
  }
})

describe('GoalPillTray — click to expand', () => {
  beforeEach(() => {
    useChatStore.setState({ goalPills: {} })
    useJudgeActivityStore.getState().reset()
  })

  it('shows full condition + round accounting on click', () => {
    useChatStore.setState({ goalPills: { g1: makeGoal({ goal_id: 'g1' }) } })
    render(<GoalPillTray />)

    expect(screen.queryByTestId('goal-pill-expanded')).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('goal-pill-active'))

    const expanded = screen.getByTestId('goal-pill-expanded')
    expect(expanded).toBeInTheDocument()
    expect(screen.getByTestId('goal-pill-condition')).toHaveTextContent('ship the release')
    expect(screen.getByTestId('goal-pill-round')).toHaveTextContent('round 3/20')
    expect(screen.getByTestId('goal-pill-round')).toHaveTextContent('active loops 1/16')
  })

  it('shows the latest reason when expanded', () => {
    useChatStore.setState({ goalPills: { g1: makeGoal({ goal_id: 'g1', latest_reason: 'blocked on tests' }) } })
    render(<GoalPillTray />)
    fireEvent.click(screen.getByTestId('goal-pill-active'))
    expect(screen.getByText('blocked on tests')).toBeInTheDocument()
  })

  it('collapses on a second click', () => {
    useChatStore.setState({ goalPills: { g1: makeGoal({ goal_id: 'g1' }) } })
    render(<GoalPillTray />)
    const pill = screen.getByTestId('goal-pill-active')

    fireEvent.click(pill)
    expect(screen.getByTestId('goal-pill-expanded')).toBeInTheDocument()

    fireEvent.click(pill)
    expect(screen.queryByTestId('goal-pill-expanded')).not.toBeInTheDocument()
  })

  it('shows per-criterion verdict when a goal-scoped JudgeVerdict exists', () => {
    useChatStore.setState({ goalPills: { g1: makeGoal({ goal_id: 'g1' }) } })
    useJudgeActivityStore.getState().apply({
      type: 'judge_verdict',
      id: 'v1',
      scope: 'goal',
      round: 2,
      met: false,
      per_criterion: [
        { criterion_id: 'c1', met: true, reason: 'tests pass' },
        { criterion_id: 'c2', met: false, reason: 'lint failing' },
      ],
      model: 'glm-5',
      judged_at: '2026-07-22T10:00:00Z',
      judge_agent_id: 'judge',
    })
    render(<GoalPillTray />)
    fireEvent.click(screen.getByTestId('goal-pill-active'))

    expect(screen.getByTestId('goal-pill-verdict-criteria')).toBeInTheDocument()
    expect(screen.getByText('tests pass')).toBeInTheDocument()
    expect(screen.getByText('lint failing')).toBeInTheDocument()
  })
})
