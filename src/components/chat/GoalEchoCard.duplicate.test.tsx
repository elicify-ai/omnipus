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
 * They also collide BY CONSTRUCTION on the card's primary path:
 * `buildFrameFromSetGoalResult` (SetGoalToolUI.tsx) fills BOTH fields from
 * the single sentence a `set_goal` result carries, so every tool-authored
 * goal card is this case. The first fix suppressed the `goal-echo-condition`
 * element — which deterministically broke
 * tests/e2e/goal-work-first.spec.ts's "the condition line is always
 * populated once a record exists". Hence the rule below.
 *
 * The rule this file pins, in two halves:
 *   1. each distinct thing renders exactly ONCE — a restatement that merely
 *      adds a full stop or fixes capitalisation is the same sentence to a
 *      reader and must not produce a second line either; and
 *   2. a rendered record card ALWAYS exposes `goal-echo-condition`, whether
 *      or not the restatement says the same thing. When the two coincide the
 *      single lead line carries both identities (`goal-echo-condition`
 *      nested inside `goal-echo-statement`), so one sentence satisfies both
 *      halves at once.
 */

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { GoalEchoCard, isSameGoalSentence } from './GoalEchoCard'
import {
  buildFrameFromSetGoalResult,
  parseSetGoalResult,
} from './tools/SetGoalToolUI'
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
    // Half 2: the condition is still addressable — and it is the SAME
    // element, not a second copy of the sentence.
    const condition = screen.getByTestId('goal-echo-condition')
    expect(condition).toHaveTextContent('Ship the release with a written changelog.')
    expect(screen.getByTestId('goal-echo-statement')).toContainElement(condition)
    // No "Set as" caption: there is no second thing to caption.
    expect(screen.queryByTestId('goal-echo-condition-caption')).not.toBeInTheDocument()

    // The sentence itself appears exactly once in the rendered card.
    const occurrences = (container.textContent ?? '').split(
      'Ship the release with a written changelog.',
    ).length - 1
    expect(occurrences).toBe(1)
  })

  it('renders the sentence once when the restatement differs only by punctuation and case', () => {
    const { container } = render(
      <GoalEchoCard
        frame={makeGoal({ condition: 'ship the release', definition: 'Ship the release.' })}
      />,
    )

    expect(screen.getByTestId('goal-echo-statement')).toHaveTextContent('Ship the release.')
    const condition = screen.getByTestId('goal-echo-condition')
    expect(screen.getByTestId('goal-echo-statement')).toContainElement(condition)
    expect(screen.queryByTestId('goal-echo-condition-caption')).not.toBeInTheDocument()
    // One line, not two: the raw lower-case spelling is not printed again
    // underneath the restatement.
    const text = container.textContent ?? ''
    expect(text.split('hip the release').length - 1).toBe(1)
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
    // The one case with no condition to expose: the frame carries none. (The
    // `set_goal` path cannot produce this — it fills `condition` from the
    // result's own definition — so the e2e contract below is unaffected.)
    render(<GoalEchoCard frame={makeGoal({ condition: '', definition: 'Ship it.' })} />)

    expect(screen.getByTestId('goal-echo-statement')).toHaveTextContent('Ship it.')
    expect(screen.queryByTestId('goal-echo-condition')).not.toBeInTheDocument()
  })
})

// ── The e2e contract, on the shape the primary path actually produces ────────
//
// tests/e2e/goal-work-first.spec.ts:129 asserts that a rendered record card
// exposes `[data-testid="goal-echo-condition"]`, commented "the condition
// line is always populated once a record exists". Every card that assertion
// ever sees is built by `buildFrameFromSetGoalResult` from a real `set_goal`
// result — which sets `condition` and `definition` to the SAME string, by
// construction. So suppressing the condition whenever the two agree broke
// that gate deterministically, on every run, for every goal.
//
// This block drives the real production builder (not a hand-written frame)
// so the two can never drift: if `buildFrameFromSetGoalResult` ever stops
// filling both fields identically, these tests are exercising whatever it
// does instead.

describe('GoalEchoCard — a card built from a real set_goal result exposes its condition', () => {
  const SENTENCE = 'Ship a playable browser tetris game.'

  function renderFromSetGoalResult() {
    const payload = JSON.stringify({
      mode: 'register',
      goal_id: 'goal-e2e-1',
      definition: SENTENCE,
      criteria: [
        {
          kind: 'prose',
          judgment: 'boolean',
          text: 'the page renders a playable board',
          author: { kind: 'agent', id: 'ray' },
          status: 'pending',
        },
      ],
      dod: [],
    })
    const parsed = parseSetGoalResult(payload)
    expect(parsed).not.toBeNull()
    const view = buildFrameFromSetGoalResult(parsed!, undefined)
    // Guard the premise: the builder really does produce the colliding shape.
    expect(view.frame.condition).toBe(view.frame.definition)
    return render(<GoalEchoCard frame={view.frame} showProgress={view.hasLiveProgress} />)
  }

  it('renders goal-echo-condition — the element the e2e gate waits for', () => {
    renderFromSetGoalResult()

    const condition = screen.getByTestId('goal-echo-condition')
    expect(condition).toBeInTheDocument()
    expect(condition).toHaveTextContent(SENTENCE)
  })

  it('does not print the sentence twice while doing so', () => {
    const { container } = renderFromSetGoalResult()

    const occurrences = (container.textContent ?? '').split(SENTENCE).length - 1
    expect(occurrences).toBe(1)
  })

  it('keeps goal-echo-statement addressable, with exactly the sentence as its text', () => {
    // Pinned because ToolCallBadge.test.tsx and goal-card-anchoring.test.tsx
    // assert `[data-testid="goal-echo-statement"]`.textContent === the
    // definition, verbatim — nesting the condition inside it must not add a
    // caption or any other stray text to that element.
    const { container } = renderFromSetGoalResult()

    expect(container.querySelector('[data-testid="goal-echo-statement"]')?.textContent).toBe(SENTENCE)
    expect(screen.queryByTestId('goal-echo-condition-caption')).not.toBeInTheDocument()
  })
})
