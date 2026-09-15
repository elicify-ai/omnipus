/**
 * goalOutcome.test.ts — the goal outcome line's copy and insertion rules
 * (founder decision 2026-09-14).
 *
 * Oracle: the founder's own wording, not the implementation —
 *   "Goal met — <goal text>" (+ one-line Judge summary if available)
 *   "Goal not met after N tries — <goal text>" (+ the Judge's last reason),
 *       N = the actual rounds used, taken from the data, never hardcoded
 *   "Goal stopped by you — <goal text>"
 * Users see "tries", never "rounds".
 */

import { describe, it, expect } from 'vitest'
import {
  buildGoalOutcomeInsertion,
  describeGoalOutcome,
  goalOutcomeHeadline,
  type GoalOutcome,
} from './goalOutcome'
import type { GoalOutcomeFrame } from '@/lib/api/generated/asyncapi-types'
import type { ChatMessage } from '@/store/chat'

const GOAL_TEXT = 'write a file called e4-marker.txt containing the word RELOAD'
const UNMET_REASON = 'The file is 5 bytes; the criterion also requires exactly 500 bytes.'

function outcome(overrides: Partial<GoalOutcome> = {}): GoalOutcome {
  return {
    goal_id: 'goal_A',
    goal_text: GOAL_TEXT,
    ending: 'met',
    rounds_used: 1,
    max_rounds: 20,
    ended_at: '2026-09-14T05:43:40Z',
    ...overrides,
  }
}

describe('describeGoalOutcome — met', () => {
  it('reads "Goal met — <goal text>" with a one-line Judge summary built from the criteria count', () => {
    const o = outcome({ ending: 'met', criteria_total: 4, judge_reason: 'All four checks passed on read-back.' })
    const copy = describeGoalOutcome(o)
    expect(goalOutcomeHeadline(o)).toBe(`Goal met — ${GOAL_TEXT}`)
    expect(copy.tone).toBe('met')
    expect(copy.summary).toBe('The Judge confirmed all 4 criteria.')
    expect(copy.detail).toBe('Judge: All four checks passed on read-back.')
  })

  it('uses the singular for a single criterion', () => {
    expect(describeGoalOutcome(outcome({ ending: 'met', criteria_total: 1 })).summary).toBe(
      'The Judge confirmed the one criterion.',
    )
  })

  it('falls back to the Judge reason as the summary when no criteria count is present', () => {
    const copy = describeGoalOutcome(outcome({ ending: 'met', judge_reason: 'Read back RELOAD.' }))
    expect(copy.summary).toBe('Judge: Read back RELOAD.')
    expect(copy.detail).toBeUndefined()
  })

  it('shows no summary at all when neither a criteria count nor a reason exists', () => {
    const copy = describeGoalOutcome(outcome({ ending: 'met' }))
    expect(copy.summary).toBeUndefined()
    expect(copy.detail).toBeUndefined()
  })
})

describe('describeGoalOutcome — not met after N tries', () => {
  it('takes N from rounds_used in the data (a limit of 5, not 20)', () => {
    const o = outcome({ ending: 'rounds_exhausted', rounds_used: 5, max_rounds: 5, judge_reason: UNMET_REASON })
    expect(goalOutcomeHeadline(o)).toBe(`Goal not met after 5 tries — ${GOAL_TEXT}`)
    expect(describeGoalOutcome(o).tone).toBe('not_met')
  })

  it('reports rounds_used, not max_rounds, when the two differ', () => {
    const o = outcome({ ending: 'rounds_exhausted', rounds_used: 3, max_rounds: 5 })
    expect(describeGoalOutcome(o).label).toBe('Goal not met after 3 tries')
  })

  it('uses "1 try" in the singular', () => {
    const o = outcome({ ending: 'rounds_exhausted', rounds_used: 1, max_rounds: 1 })
    expect(describeGoalOutcome(o).label).toBe('Goal not met after 1 try')
  })

  it("carries the Judge's last unmet reason as the one-line summary", () => {
    const o = outcome({ ending: 'rounds_exhausted', rounds_used: 5, max_rounds: 5, judge_reason: `  ${UNMET_REASON}  ` })
    expect(describeGoalOutcome(o).summary).toBe(`Judge: ${UNMET_REASON}`)
  })

  it('has no summary when no Judge reason was recorded', () => {
    const o = outcome({ ending: 'rounds_exhausted', rounds_used: 5, max_rounds: 5 })
    expect(describeGoalOutcome(o).summary).toBeUndefined()
  })
})

describe('describeGoalOutcome — stopped by you', () => {
  it('reads "Goal stopped by you — <goal text>" with no Judge text', () => {
    const o = outcome({ ending: 'stopped_by_user', rounds_used: 2, judge_reason: UNMET_REASON })
    const copy = describeGoalOutcome(o)
    expect(goalOutcomeHeadline(o)).toBe(`Goal stopped by you — ${GOAL_TEXT}`)
    expect(copy.tone).toBe('stopped')
    expect(copy.summary).toBeUndefined()
    expect(copy.detail).toBeUndefined()
  })
})

describe('describeGoalOutcome — any other ending', () => {
  it('is a neutral not-met line with the tries count from the data and the reason only on expand', () => {
    const o = outcome({ ending: 'other', rounds_used: 2, max_rounds: 7, judge_reason: UNMET_REASON })
    const copy = describeGoalOutcome(o)
    expect(goalOutcomeHeadline(o)).toBe(`Goal not met — ${GOAL_TEXT}`)
    expect(copy.tone).toBe('not_met')
    expect(copy.summary).toBe('Ended after 2 of 7 tries.')
    expect(copy.detail).toBe(`Judge: ${UNMET_REASON}`)
  })
})

describe('user-facing copy never says "rounds"', () => {
  it.each(['met', 'rounds_exhausted', 'stopped_by_user', 'other'] as const)('%s', (ending) => {
    const copy = describeGoalOutcome(outcome({ ending, rounds_used: 4, max_rounds: 6, criteria_total: 2 }))
    for (const text of [copy.label, copy.summary, copy.detail]) {
      if (text) expect(text).not.toMatch(/round/i)
    }
  })
})

describe('buildGoalOutcomeInsertion', () => {
  const MESSAGE_ID = 'goal-outcome-goal_A-1789367371829960000'

  function frame(overrides: Partial<GoalOutcomeFrame> = {}): GoalOutcomeFrame {
    return {
      type: 'goal_outcome',
      session_id: 'sid-1',
      message_id: MESSAGE_ID,
      outcome: outcome({ ending: 'rounds_exhausted', rounds_used: 5, max_rounds: 5 }),
      ...overrides,
    }
  }

  const user: ChatMessage = { id: 'u1', role: 'user', content: '/goal x', timestamp: '2026-09-14T05:00:00Z', status: 'done' }

  it('appends one system message stamped with the frame id, the outcome, and the ending time', () => {
    const patch = buildGoalOutcomeInsertion({ messagesById: { u1: user }, messageOrder: ['u1'] }, frame())
    expect(patch).not.toBeNull()
    expect(patch!.messageOrder).toEqual(['u1', MESSAGE_ID])
    const msg = patch!.messagesById[MESSAGE_ID]
    expect(msg.role).toBe('system')
    expect(msg.goalOutcome?.goal_id).toBe('goal_A')
    expect(msg.timestamp).toBe('2026-09-14T05:43:40Z')
    expect(msg.content).toBe(`Goal not met after 5 tries — ${GOAL_TEXT}`)
  })

  it('returns null when a message with that id is already present (cold load or an earlier push)', () => {
    const existing: ChatMessage = { id: MESSAGE_ID, role: 'system', content: 'x', timestamp: 't', status: 'done' }
    expect(
      buildGoalOutcomeInsertion({ messagesById: { [MESSAGE_ID]: existing }, messageOrder: [MESSAGE_ID] }, frame()),
    ).toBeNull()
  })
})
