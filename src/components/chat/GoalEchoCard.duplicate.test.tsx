/**
 * GoalEchoCard.duplicate.test.tsx
 *
 * UAT defect C: the goal card printed the SAME sentence twice — once as
 * `goal-echo-statement` and again immediately below as `goal-echo-condition`.
 *
 * The two fields are genuinely different things on the wire:
 *   - `condition` is the raw text the goal was set with — every
 *     `emitGoalStatusFrameWithCriteriaAndDoD` call site passes `rec.Prompt`.
 *   - `definition` is the compiler's one-sentence SMART restatement
 *     (`GoalStatusFrame.yaml`, ADR-080 D-STATEMENT).
 *
 * They collide because the compiler is instructed to stay "close to the
 * setter's own words" (`pkg/agent/goal_compile_llm.go`), so a goal set as one
 * clean sentence comes back restated as that same sentence. The card rendered
 * both unconditionally.
 *
 * The rule this file pins: each distinct thing renders exactly ONCE. A
 * restatement that merely adds a full stop or fixes capitalisation is the same
 * sentence to a reader and must not produce a second line either.
 */

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { GoalEchoCard, isSameGoalSentence } from './GoalEchoCard'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

function makeGoal(overrides: Partial<GoalStatusFrame> = {}): GoalStatusFrame {
  return {
    type: 'goal_status',
    session_id: 's1',
    condition: 'ship the release',
    round: 0,
    max_rounds: 20,
    latest_reason: '',
    active_loops: 0,
    cap: 16,
    state: 'active',
    ...overrides,
  }
}

describe('isSameGoalSentence', () => {
  it('matches identical sentences', () => {
    expect(isSameGoalSentence('Ship the release.', 'Ship the release.')).toBe(true)
  })

  it('matches across a terminal full stop the restatement added', () => {
    expect(isSameGoalSentence('Ship the release.', 'Ship the release')).toBe(true)
  })

  it('matches across capitalisation the restatement fixed', () => {
    expect(isSameGoalSentence('Ship the release', 'ship the release')).toBe(true)
  })

  it('matches across collapsed whitespace and padding', () => {
    expect(isSameGoalSentence('  Ship  the release ', 'Ship the release')).toBe(true)
  })

  it('does NOT match two genuinely different sentences', () => {
    expect(
      isSameGoalSentence('Ship the release.', 'All pkg/plan tests pass and lint is clean.'),
    ).toBe(false)
  })

  it('does NOT match a sentence against a marker token', () => {
    expect(isSameGoalSentence('Ship the release.', 'goal_marker_a1b2')).toBe(false)
  })
})

describe('GoalEchoCard — the statement is not printed twice', () => {
  it('renders the sentence once when the restatement equals the raw condition', () => {
    const { container } = render(
      <GoalEchoCard
        frame={makeGoal({
          condition: 'Ship the release with a written changelog.',
          definition: 'Ship the release with a written changelog.',
        })}
      />,
    )

    expect(screen.getByTestId('goal-echo-statement')).toHaveTextContent(
      'Ship the release with a written changelog.',
    )
    expect(screen.queryByTestId('goal-echo-condition')).not.toBeInTheDocument()

    // The sentence itself appears exactly once in the rendered card.
    const occurrences = (container.textContent ?? '').split(
      'Ship the release with a written changelog.',
    ).length - 1
    expect(occurrences).toBe(1)
  })

  it('renders the sentence once when the restatement differs only by punctuation and case', () => {
    render(
      <GoalEchoCard
        frame={makeGoal({ condition: 'ship the release', definition: 'Ship the release.' })}
      />,
    )

    expect(screen.getByTestId('goal-echo-statement')).toHaveTextContent('Ship the release.')
    expect(screen.queryByTestId('goal-echo-condition')).not.toBeInTheDocument()
  })

  it('still renders BOTH when they say different things, with the raw text captioned', () => {
    render(
      <GoalEchoCard
        frame={makeGoal({
          condition: 'make the tests pass',
          definition: 'All pkg/plan tests pass and golangci-lint reports zero issues.',
        })}
      />,
    )

    expect(screen.getByTestId('goal-echo-statement')).toHaveTextContent(
      'All pkg/plan tests pass and golangci-lint reports zero issues.',
    )
    expect(screen.getByTestId('goal-echo-condition')).toHaveTextContent('make the tests pass')
    // Two anonymous sentences is what made the duplicate hard to read in the
    // first place — the secondary line says what it is.
    expect(screen.getByTestId('goal-echo-condition-caption')).toHaveTextContent('Set as')
  })

  it('keeps the marker token visible alongside its restatement (marker-path goals)', () => {
    render(
      <GoalEchoCard
        frame={makeGoal({
          condition: 'goal_marker_a1b2',
          definition: 'Ship the release with a written changelog.',
        })}
      />,
    )

    expect(screen.getByTestId('goal-echo-condition')).toHaveTextContent('goal_marker_a1b2')
  })

  it('leads with the condition, uncaptioned, when there is no restatement at all', () => {
    render(<GoalEchoCard frame={makeGoal()} />)

    expect(screen.queryByTestId('goal-echo-statement')).not.toBeInTheDocument()
    expect(screen.getByTestId('goal-echo-condition')).toHaveTextContent('ship the release')
    expect(screen.queryByTestId('goal-echo-condition-caption')).not.toBeInTheDocument()
  })

  it('renders no condition line at all when the frame carries an empty condition', () => {
    render(<GoalEchoCard frame={makeGoal({ condition: '', definition: 'Ship it.' })} />)

    expect(screen.getByTestId('goal-echo-statement')).toHaveTextContent('Ship it.')
    expect(screen.queryByTestId('goal-echo-condition')).not.toBeInTheDocument()
  })
})
