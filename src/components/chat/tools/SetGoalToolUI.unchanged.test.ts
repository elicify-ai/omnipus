/**
 * SetGoalToolUI.unchanged.test.ts — co-ordination fix, 2026-09-08 (GX-C).
 *
 * A separate wave is adding an `unchanged` boolean to `set_goal`'s result
 * payload, marking a call that submitted an IDENTICAL duplicate of the
 * already-registered record — the operator's reported repro showed THREE
 * identical goal cards stacked in the thread from three such duplicate
 * calls. This wave handles the field defensively: `classifySetGoalCall`
 * renders NO card at all for a result carrying `unchanged: true`, and
 * treats the field as optional — its absence (older backend, or a
 * genuinely new/changed record) is exactly today's behavior.
 *
 * Pure-function unit tests against `isUnchangedSetGoalResult` and
 * `classifySetGoalCall` — no component rendering needed, these are the
 * exact decision points ChatScreen.tsx's replay loop and the live
 * `SetGoalToolUI` registration both consult.
 */

import { describe, it, expect } from 'vitest'
import { classifySetGoalCall, isUnchangedSetGoalResult } from './SetGoalToolUI'

const REGISTERED_RESULT = {
  goal_id: 'goal_1',
  definition: 'Ship the release notes',
  criteria: [{ id: 'c1', text: 'release notes are published', judgment: 'boolean' }],
  dod: [],
}

describe('isUnchangedSetGoalResult', () => {
  it('is true for a raw payload carrying unchanged: true', () => {
    expect(isUnchangedSetGoalResult({ ...REGISTERED_RESULT, unchanged: true })).toBe(true)
  })

  it('is true for the replay envelope shape ({ text: "<json>" })', () => {
    expect(
      isUnchangedSetGoalResult({ text: JSON.stringify({ ...REGISTERED_RESULT, unchanged: true }) }),
    ).toBe(true)
  })

  it('is true for a live-path JSON STRING payload', () => {
    expect(isUnchangedSetGoalResult(JSON.stringify({ ...REGISTERED_RESULT, unchanged: true }))).toBe(true)
  })

  it('is false when the field is absent — older backend / today\'s behavior unchanged', () => {
    expect(isUnchangedSetGoalResult(REGISTERED_RESULT)).toBe(false)
  })

  it('is false when unchanged is explicitly false (a genuinely new/changed record)', () => {
    expect(isUnchangedSetGoalResult({ ...REGISTERED_RESULT, unchanged: false })).toBe(false)
  })

  it('is false for a non-record result (still-running / failed call)', () => {
    expect(isUnchangedSetGoalResult(undefined)).toBe(false)
    expect(isUnchangedSetGoalResult(null)).toBe(false)
    expect(isUnchangedSetGoalResult('some plain error text')).toBe(false)
  })
})

describe('classifySetGoalCall — unchanged duplicate suppression', () => {
  it('renders NO card (hidden) for a completed call whose result carries unchanged: true', () => {
    const presentation = classifySetGoalCall({
      result: { ...REGISTERED_RESULT, unchanged: true },
      isRunning: false,
      isError: false,
      verboseChatEnabled: false,
    })
    expect(presentation).toBe('hidden')
  })

  it('renders the card as before when unchanged is absent (today\'s behavior, unaffected)', () => {
    const presentation = classifySetGoalCall({
      result: REGISTERED_RESULT,
      isRunning: false,
      isError: false,
      verboseChatEnabled: false,
    })
    expect(presentation).toBe('card')
  })

  it('verbose chat still shows the raw call in full even when unchanged: true', () => {
    const presentation = classifySetGoalCall({
      result: { ...REGISTERED_RESULT, unchanged: true },
      isRunning: false,
      isError: false,
      verboseChatEnabled: true,
    })
    expect(presentation).toBe('raw')
  })
})
