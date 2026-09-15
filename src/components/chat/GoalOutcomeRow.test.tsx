/**
 * GoalOutcomeRow.test.tsx — the rendered goal outcome line (founder decision
 * 2026-09-14): the three founder-specified variants, the icon tone (Forge Gold
 * only for met), and the one-line truncation with expand.
 */

import { describe, it, expect } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { GoalOutcomeRow } from './GoalOutcomeRow'
import type { GoalOutcome } from '@/lib/goalOutcome'

const GOAL_TEXT = 'write e8-impossible.txt at the workspace root'
const UNMET_REASON = 'The file is 5 bytes; the criterion also requires exactly 500 bytes, which cannot both hold.'

function outcome(overrides: Partial<GoalOutcome> = {}): GoalOutcome {
  return {
    goal_id: 'goal_B',
    goal_text: GOAL_TEXT,
    ending: 'met',
    rounds_used: 1,
    max_rounds: 20,
    ended_at: '2026-09-14T06:29:31Z',
    ...overrides,
  }
}

describe('GoalOutcomeRow', () => {
  it('met: renders "Goal met — <goal text>" with the Judge summary and the met tone', () => {
    render(<GoalOutcomeRow outcome={outcome({ ending: 'met', criteria_total: 3 })} />)
    const row = screen.getByTestId('goal-outcome-line')
    expect(row).toHaveAttribute('data-goal-tone', 'met')
    expect(screen.getByTestId('goal-outcome-headline')).toHaveTextContent(`Goal met — ${GOAL_TEXT}`)
    expect(screen.getByTestId('goal-outcome-summary')).toHaveTextContent('The Judge confirmed all 3 criteria.')
    // Forge Gold is reserved for met.
    expect(row.querySelector('svg')?.getAttribute('class')).toContain('var(--color-accent)')
  })

  it("not met: renders the tries count from the data and the Judge's last reason, truncated to one line", () => {
    render(
      <GoalOutcomeRow outcome={outcome({ ending: 'rounds_exhausted', rounds_used: 5, max_rounds: 5, judge_reason: UNMET_REASON })} />,
    )
    const row = screen.getByTestId('goal-outcome-line')
    expect(row).toHaveAttribute('data-goal-tone', 'not_met')
    expect(screen.getByTestId('goal-outcome-headline')).toHaveTextContent(`Goal not met after 5 tries — ${GOAL_TEXT}`)
    const summary = screen.getByTestId('goal-outcome-summary')
    expect(summary).toHaveTextContent(`Judge: ${UNMET_REASON}`)
    expect(summary).toHaveClass('truncate')
    expect(row.querySelector('svg')?.getAttribute('class')).toContain('var(--color-warning)')
    expect(row.querySelector('svg')?.getAttribute('class')).not.toContain('var(--color-accent)')
  })

  it('stopped: renders "Goal stopped by you — <goal text>" with no Judge line', () => {
    render(<GoalOutcomeRow outcome={outcome({ ending: 'stopped_by_user', rounds_used: 2, judge_reason: UNMET_REASON })} />)
    const row = screen.getByTestId('goal-outcome-line')
    expect(row).toHaveAttribute('data-goal-tone', 'stopped')
    expect(screen.getByTestId('goal-outcome-headline')).toHaveTextContent(`Goal stopped by you — ${GOAL_TEXT}`)
    expect(screen.queryByTestId('goal-outcome-summary')).toBeNull()
    expect(row).not.toHaveTextContent('Judge')
  })

  it('starts collapsed and expands on click to reveal the extra Judge text', () => {
    render(<GoalOutcomeRow outcome={outcome({ ending: 'met', criteria_total: 2, judge_reason: 'Both files read back correctly.' })} />)
    const row = screen.getByTestId('goal-outcome-line') as HTMLDetailsElement
    const detail = screen.getByTestId('goal-outcome-detail')
    expect(row.open).toBe(false)
    expect(detail).not.toBeVisible()

    fireEvent.click(row.querySelector('summary')!)

    expect(row.open).toBe(true)
    expect(detail).toBeVisible()
    expect(detail).toHaveTextContent('Judge: Both files read back correctly.')
  })
})
