/**
 * goalSetupState.test.ts — review finding 14; FR-039 (DEL-F39–40).
 *
 * `isGoalRecordEmpty` gates two user-visible behaviours in ChatScreen: the
 * goal-aware thinking-indicator label ("Framing your goal" / "Setting
 * acceptance criteria") and the goal-setup failure line, which REWRITES
 * every failed tool call in the thread as "A step could not be completed
 * during goal setup — retrying." Both are correct only during the window
 * between a goal activating and its record being authored.
 *
 * The defect (finding 14): the predicate read `criteria` off the frame it
 * was handed. Routine `active` progress pushes carry no `criteria` (the
 * wire's "no record change on this push" contract), so the first push after
 * `set_goal` flipped the predicate back to true and left it there. Only
 * `goalPills` goes through `mergeGoalPillFrame`, which carries the authored
 * record forward.
 *
 * FR-039 (DEL-F39–40): the predicate is now asked about a SPECIFIC goal —
 * the producing message's own `goal_id` — joined to `goalPills[goalId]`,
 * never the "latest goal" scalar. An unknown goalId is neutral (false).
 *
 * Every case below drives the REAL store through `handleFrame`, with the
 * same frames the gateway sends, and then asks the predicate the same
 * keyed question ChatScreen asks — `isGoalRecordEmpty(goalId, goalPills)`.
 * Nothing here hands the predicate a pre-merged record.
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { isGoalRecordEmpty } from './goalSetupState'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

const SID = 'sess_goal'

type Criterion = NonNullable<GoalStatusFrame['criteria']>[number]

function criterion(text: string): Criterion {
  return {
    id: 'c1',
    kind: 'prose',
    judgment: 'boolean',
    text,
    author: { kind: 'agent', id: 'mia' },
    status: 'pending',
  }
}

/** A goal_status frame shaped exactly like the gateway's. */
function frame(overrides: Partial<GoalStatusFrame> = {}): GoalStatusFrame {
  return {
    type: 'goal_status',
    session_id: SID,
    goal_id: 'goal_abc',
    condition: 'Ship the release notes',
    round: 1,
    max_rounds: 20,
    latest_reason: '',
    active_loops: 1,
    cap: 4,
    state: 'active',
    ...overrides,
  }
}

/** Push a frame through the real store, the way the WS client does. */
function push(f: GoalStatusFrame): void {
  useChatStore.getState().handleFrame(f)
}

/** The exact expression ChatScreen evaluates — keyed by the goal's own id. */
function predicate(goalId: string | null | undefined): boolean {
  return isGoalRecordEmpty(goalId, useChatStore.getState().goalPills)
}

beforeEach(() => {
  useChatStore.setState({ sessionsById: {}, goalStatus: null, goalPills: {} })
  useSessionStore.setState({ activeSessionId: SID })
})

describe('isGoalRecordEmpty — the empty-record window (keyed by the goal\'s own id)', () => {
  it('is true while an active goal has no record yet (the /goal instant-activation window)', () => {
    push(frame())
    expect(predicate('goal_abc')).toBe(true)
  })

  it('is false once the agent authors the record via set_goal', () => {
    push(frame())
    push(frame({ criteria: [criterion('The notes are published.')] }))
    expect(predicate('goal_abc')).toBe(false)
  })

  it('finding 14: stays false across the routine criteria-less progress pushes that follow set_goal', () => {
    push(frame())
    push(frame({ criteria: [criterion('The notes are published.')] }))
    expect(predicate('goal_abc')).toBe(false)

    // Routine end-of-turn progress pushes. These carry NO criteria — that is
    // the wire contract for "no record change on this push", not an erased
    // record. Before the fix the FIRST of these flipped the predicate back to
    // true permanently.
    push(frame({ round: 2, latest_reason: 'still working' }))
    expect(predicate('goal_abc')).toBe(false)
    push(frame({ round: 3, state: 'judging' }))
    push(frame({ round: 3, latest_reason: 'one criterion unmet' }))
    expect(predicate('goal_abc')).toBe(false)
  })

  it('is false for a terminal goal even before any record was authored', () => {
    push(frame())
    push(frame({ state: 'done' }))
    expect(predicate('goal_abc')).toBe(false)
  })

  it('is false for an UNKNOWN association — no goalId, or a goalId with no pill (FR-039 neutral)', () => {
    push(frame())
    expect(predicate(null)).toBe(false)
    expect(predicate(undefined)).toBe(false)
    expect(predicate('')).toBe(false)
    expect(predicate('some_other_goal')).toBe(false)
  })

  it('re-opens the window for a SECOND goal that has no record, while the first still has one', () => {
    push(frame({ goal_id: 'goal_one', criteria: [criterion('First goal done.')] }))
    expect(predicate('goal_one')).toBe(false)

    // A different goal generation: its own pill key, its own empty record.
    push(frame({ goal_id: 'goal_two', condition: 'Second goal' }))
    expect(predicate('goal_two')).toBe(true)

    // …and the first goal's record is still intact, so a progress push for it
    // does not re-open the window either.
    push(frame({ goal_id: 'goal_one', round: 2 }))
    expect(predicate('goal_one')).toBe(false)
  })
})
