// GoalEchoCard.test.tsx — ADR-053 FE-8 / US-3 / D11; criteria breakdown per
// ADR-074 D5.2 / judgment-first FR-011 (US-6, test 19 component half).

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { GoalEchoCard } from './GoalEchoCard'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

type GoalCriterion = NonNullable<GoalStatusFrame['criteria']>[number]

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
    state: 'queued',
    ...overrides,
  }
}

function makeCriterion(overrides: Partial<GoalCriterion> = {}): GoalCriterion {
  return {
    kind: 'prose',
    text: 'the release notes are written',
    author: { kind: 'agent', id: 'mia' },
    status: 'pending',
    ...overrides,
  }
}

describe('GoalEchoCard', () => {
  it('renders the compiled condition + round accounting', () => {
    render(<GoalEchoCard frame={makeGoal()} />)
    expect(screen.getByTestId('goal-echo-card')).toBeInTheDocument()
    expect(screen.getByTestId('goal-echo-condition')).toHaveTextContent('ship the release')
    expect(screen.getByTestId('goal-echo-round')).toHaveTextContent('20 rounds')
    expect(screen.getByTestId('goal-echo-round')).toHaveTextContent('16 concurrent loops')
  })

  // US-6 S1: pending goal with 2 prose + 1 marker check → 3 rows, text
  // first, verbatim command chip on the check row only.
  it('itemizes the criteria breakdown plain-language-first with a verifies-via chip on technical rows', () => {
    const frame = makeGoal({
      criteria: [
        makeCriterion({ text: 'the release notes are written' }),
        makeCriterion({ text: 'the changelog covers every user-facing change' }),
        makeCriterion({
          kind: 'check',
          text: 'the test suite passes',
          check: { command: 'go test ./...', expected_exit_code: 0 },
        }),
      ],
    })
    render(<GoalEchoCard frame={frame} />)

    const rows = screen.getAllByTestId('goal-echo-criterion')
    expect(rows).toHaveLength(3)
    expect(rows[0]).toHaveTextContent('the release notes are written')
    expect(rows[1]).toHaveTextContent('the changelog covers every user-facing change')
    expect(rows[2]).toHaveTextContent('the test suite passes')

    // Exactly ONE chip — the check row's — carrying the command VERBATIM.
    const chips = screen.getAllByTestId('goal-echo-verifies-via')
    expect(chips).toHaveLength(1)
    expect(chips[0]).toHaveTextContent('go test ./...')
    expect(chips[0]).toHaveTextContent('expected exit 0')
  })

  it('renders a behavior payload as a verifies-via chip (tool + counts)', () => {
    const frame = makeGoal({
      criteria: [
        makeCriterion({
          kind: 'behavior',
          text: 'research draws on real sources',
          behavior: { tool: 'search_web', min_count: 3 },
        }),
      ],
    })
    render(<GoalEchoCard frame={frame} />)
    const chip = screen.getByTestId('goal-echo-verifies-via')
    expect(chip).toHaveTextContent('search_web')
    expect(chip).toHaveTextContent('at least 3 times')
  })

  // US-6 S4 (negative): `[kind]` classification tokens are not user-facing.
  it('renders NO [kind] tokens anywhere on the card', () => {
    const frame = makeGoal({
      criteria: [
        makeCriterion(),
        makeCriterion({
          kind: 'check',
          text: 'tests pass',
          check: { command: 'go test ./...', expected_exit_code: 0 },
        }),
      ],
    })
    const { container } = render(<GoalEchoCard frame={frame} />)
    expect(container.textContent).not.toMatch(/\[(check|prose|behavior)\]/)
  })

  it('hides the criteria section when the frame carries none (legacy frames)', () => {
    render(<GoalEchoCard frame={makeGoal()} />)
    expect(screen.queryByTestId('goal-echo-criteria')).not.toBeInTheDocument()
  })

  it('shows the conversational confirmation prompt (no buttons/form)', () => {
    render(<GoalEchoCard frame={makeGoal()} />)
    expect(screen.getByText(/Reply to confirm/)).toBeInTheDocument()
    // No form elements — conversational confirm only
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('shows singular "loop" when cap is 1', () => {
    render(<GoalEchoCard frame={makeGoal({ cap: 1 })} />)
    expect(screen.getByTestId('goal-echo-round')).toHaveTextContent('1 concurrent loop')
  })
})
