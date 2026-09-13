/**
 * goalSetupState.test.ts — review finding 14.
 *
 * `isGoalRecordEmpty` gates two user-visible behaviours in ChatScreen: the
 * goal-aware thinking-indicator label ("Framing your goal" / "Setting
 * acceptance criteria") and the goal-setup failure line, which REWRITES
 * every failed tool call in the thread as "A step could not be completed
 * during goal setup — retrying." Both are correct only during the window
 * between a goal activating and its record being authored.
 *
 * The defect: the predicate read `criteria` off the frame it was handed.
 * The store files `goalStatus` VERBATIM, and a routine `active` progress
 * push carries no `criteria` (the wire's "no record change on this push"
 * contract), so the first progress push after `set_goal` flipped the
 * predicate back to true and left it there for the rest of the goal. Only
 * `goalPills` goes through `mergeGoalPillFrame`, which carries the authored
 * record forward.
 *
 * Every case below drives the REAL store through `handleFrame`, with the
 * same frames the gateway sends, and then asks the predicate the same
 * one-argument question ChatScreen asks — `isGoalRecordEmpty(goalStatus)`.
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

/** The exact expression ChatScreen evaluates. */
function predicate(): boolean {
  return isGoalRecordEmpty(useChatStore.getState().goalStatus)
}

beforeEach(() => {
  useChatStore.setState({ sessionsById: {}, goalStatus: null, goalPills: {} })
  useSessionStore.setState({ activeSessionId: SID })
})

describe('isGoalRecordEmpty — the empty-record window', () => {
  it('is true while an active goal has no record yet (the /goal instant-activation window)', () => {
    push(frame())
    expect(predicate()).toBe(true)
  })

  it('is false once the agent authors the record via set_goal', () => {
    push(frame())
    push(frame({ criteria: [criterion('The notes are published.')] }))
    expect(predicate()).toBe(false)
  })

  it('finding 14: stays false across the routine criteria-less progress pushes that follow set_goal', () => {
    push(frame())
    push(frame({ criteria: [criterion('The notes are published.')] }))
    expect(predicate()).toBe(false)

    // Routine end-of-turn progress pushes. These carry NO criteria — that is
    // the wire contract for "no record change on this push", not an erased
    // record. Before the fix the FIRST of these flipped the predicate back to
    // true permanently.
    push(frame({ round: 2, latest_reason: 'still working' }))
    expect(predicate()).toBe(false)
    push(frame({ round: 3, state: 'judging' }))
    push(frame({ round: 3, latest_reason: 'one criterion unmet' }))
    expect(predicate()).toBe(false)
  })

  it('is false for a terminal goal even before any record was authored', () => {
    push(frame())
    push(frame({ state: 'done' }))
    expect(predicate()).toBe(false)
  })

  it('is false for a null frame (no goal at all)', () => {
    expect(isGoalRecordEmpty(null)).toBe(false)
    expect(isGoalRecordEmpty(undefined)).toBe(false)
  })

  it('re-opens the window for a SECOND goal that has no record, while the first still has one', () => {
    push(frame({ goal_id: 'goal_one', criteria: [criterion('First goal done.')] }))
    expect(predicate()).toBe(false)

    // A different goal generation: its own pill key, its own empty record.
    push(frame({ goal_id: 'goal_two', condition: 'Second goal' }))
    expect(predicate()).toBe(true)

    // …and the first goal's record is still intact, so a progress push for it
    // does not re-open the window either.
    push(frame({ goal_id: 'goal_one', round: 2 }))
    expect(predicate()).toBe(false)
  })

})
