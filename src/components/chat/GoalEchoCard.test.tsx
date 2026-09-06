// GoalEchoCard.test.tsx — ADR-053 FE-8 / US-3 / D11; criteria breakdown per
// ADR-074 D5.2 / judgment-first FR-011 (US-6, test 19 component half).

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
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
  // first, verbatim command chip on the check row only. Rendered by the
  // SHARED CriteriaBreakdown (D5.4) — the chip format asserted here is the
  // shared formatVerifiesVia contract, identical on every surface.
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

    // The shared criteria list mounts inside the goal-echo criteria section.
    expect(screen.getByTestId('goal-echo-criteria')).toBeInTheDocument()
    const rows = screen.getAllByRole('listitem')
    expect(rows).toHaveLength(3)
    expect(rows[0]).toHaveTextContent('the release notes are written')
    expect(rows[1]).toHaveTextContent('the changelog covers every user-facing change')
    expect(rows[2]).toHaveTextContent('the test suite passes')

    // Exactly ONE chip — the check row's — carrying the command VERBATIM in
    // the shared "command -> exit N" format.
    expect(screen.getAllByText('verifies via:')).toHaveLength(1)
    expect(screen.getByText('go test ./... -> exit 0')).toBeInTheDocument()
  })

  it('renders a behavior payload as a verifies-via chip (tool + counts, shared format)', () => {
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
    expect(screen.getByText('verifies via:')).toBeInTheDocument()
    expect(screen.getByText('search_web x3+')).toBeInTheDocument()
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

  // ADR-078 D1: the card now also offers click-to-confirm buttons. The prose
  // hint stays as a secondary line below them (a channel user with no card
  // can still confirm by typing).
  it('shows the conversational confirmation prompt AND the Confirm/Cancel/Amend buttons in the queued state', () => {
    render(<GoalEchoCard frame={makeGoal()} />)
    expect(screen.getByText(/Reply to confirm/)).toBeInTheDocument()
    expect(screen.getByTestId('goal-echo-actions')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /confirm/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /amend/i })).toBeInTheDocument()
  })

  // ADR-078 D1: Confirm sends the bare confirm token, Cancel sends
  // `/goal clear`, Amend pre-fills the composer and sends nothing — each
  // button fires exactly its own callback.
  it('fires onConfirm/onCancel/onAmend on click, and only the clicked one', () => {
    const onConfirm = vi.fn()
    const onCancel = vi.fn()
    const onAmend = vi.fn()
    render(<GoalEchoCard frame={makeGoal()} onConfirm={onConfirm} onCancel={onCancel} onAmend={onAmend} />)

    fireEvent.click(screen.getByTestId('goal-echo-confirm'))
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onCancel).not.toHaveBeenCalled()
    expect(onAmend).not.toHaveBeenCalled()

    fireEvent.click(screen.getByTestId('goal-echo-cancel'))
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onAmend).not.toHaveBeenCalled()

    fireEvent.click(screen.getByTestId('goal-echo-amend'))
    expect(onAmend).toHaveBeenCalledTimes(1)
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  // ADR-078 D1 / G-5 negative: the buttons render ONLY while the card is
  // pending confirmation (`queued`) — a card left mounted for any other
  // status (e.g. an active goal) shows no buttons.
  it('does NOT render the action buttons when the frame is not in the queued state', () => {
    render(<GoalEchoCard frame={makeGoal({ state: 'active' })} />)
    expect(screen.queryByTestId('goal-echo-actions')).not.toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    // Prose hint still shown regardless of state (existing behavior).
    expect(screen.getByText(/Reply to confirm/)).toBeInTheDocument()
  })

  it('shows singular "loop" when cap is 1', () => {
    render(<GoalEchoCard frame={makeGoal({ cap: 1 })} />)
    expect(screen.getByTestId('goal-echo-round')).toHaveTextContent('1 concurrent loop')
  })
})
