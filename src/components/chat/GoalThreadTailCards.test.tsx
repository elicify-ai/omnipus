// GoalThreadTailCards.test.tsx — ADR-053 FE-8 / US-3; ADR-074 D5.2 /
// judgment-first FR-011 (US-6 S1/S3, test 19 component half): the `queued`
// pill renders the echo card with real itemization from the frame's
// `criteria` breakdown, and a G-5 `waiting_on_user` pause on an ACTIVE goal
// never renders the confirm card (R2-03's kept negative).

import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { GoalThreadTailCards } from './GoalThreadTailCards'
import { useChatStore } from '@/store/chat'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

function makeGoal(overrides: Partial<GoalStatusFrame> = {}): GoalStatusFrame {
  return {
    type: 'goal_status',
    session_id: 's1',
    condition: 'write the launch post',
    round: 0,
    max_rounds: 20,
    latest_reason: '',
    active_loops: 0,
    cap: 16,
    state: 'queued',
    ...overrides,
  }
}

describe('GoalThreadTailCards', () => {
  beforeEach(() => {
    useChatStore.setState({ goalPills: {} })
  })

  it('renders nothing with no pills', () => {
    const { container } = render(<GoalThreadTailCards />)
    expect(container).toBeEmptyDOMElement()
  })

  it('renders the echo card with real itemization for a queued pill carrying criteria', () => {
    useChatStore.setState({
      goalPills: {
        _default: makeGoal({
          criteria: [
            {
              kind: 'prose',
              text: 'the post names every shipped feature',
              author: { kind: 'agent', id: 'mia' },
              status: 'pending',
            },
            {
              kind: 'check',
              text: 'the site builds',
              check: { command: 'npm run build', expected_exit_code: 0 },
              author: { kind: 'agent', id: 'mia' },
              status: 'pending',
            },
          ],
        }),
      },
    })
    render(<GoalThreadTailCards />)
    expect(screen.getByTestId('goal-thread-tail-cards')).toBeInTheDocument()
    // Criteria render through the shared CriteriaBreakdown (D5.4).
    expect(screen.getAllByRole('listitem')).toHaveLength(2)
    expect(screen.getByText('verifies via:')).toBeInTheDocument()
    expect(screen.getByText('npm run build -> exit 0')).toBeInTheDocument()
  })

  // US-6 S3 negative (R2-03): an ACTIVE goal paused waiting_on_user is NOT a
  // pending-confirm state — the confirm card must not render.
  it('does NOT render the confirm card for a waiting_on_user pause', () => {
    useChatStore.setState({
      goalPills: { g1: makeGoal({ goal_id: 'g1', state: 'waiting_on_user', round: 3 }) },
    })
    const { container } = render(<GoalThreadTailCards />)
    expect(container).toBeEmptyDOMElement()
  })

  it('does NOT render the confirm card for an active goal', () => {
    useChatStore.setState({
      goalPills: { g1: makeGoal({ goal_id: 'g1', state: 'active', round: 1 }) },
    })
    const { container } = render(<GoalThreadTailCards />)
    expect(container).toBeEmptyDOMElement()
  })
})
