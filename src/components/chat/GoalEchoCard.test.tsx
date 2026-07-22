// GoalEchoCard.test.tsx — ADR-053 FE-8 / US-3 / D11.

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { GoalEchoCard } from './GoalEchoCard'
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
    state: 'queued',
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

  it('renders literal commands when provided', () => {
    render(
      <GoalEchoCard
        frame={makeGoal()}
        literalCommands={['npm test', 'npm run lint']}
      />,
    )
    expect(screen.getByTestId('goal-echo-commands')).toBeInTheDocument()
    expect(screen.getByText('npm test')).toBeInTheDocument()
    expect(screen.getByText('npm run lint')).toBeInTheDocument()
  })

  it('hides the commands section when none are provided', () => {
    render(<GoalEchoCard frame={makeGoal()} />)
    expect(screen.queryByTestId('goal-echo-commands')).not.toBeInTheDocument()
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
