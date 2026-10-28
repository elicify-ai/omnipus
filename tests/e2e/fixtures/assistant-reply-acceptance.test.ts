import { describe, expect, it } from 'vitest'
import { matchingHansReplyCompletions } from './assistant-reply-acceptance'

// Oracle: founder-approved Bug-Hans-a checker change, 2026-10-28, brief tasks
// 2(a)–(e): match the row's turn AND an exact primary/merged ID. The row's
// merged IDs are space-separated DOM attributes, not a same-turn wire guess.
// Real: the selection gate used by assertHansReplyPersisted, JSON decoding
// and generated DoneFrame validation. No mocked assertions or dependencies.
const sessionId = 'hans-session'
const row = { messageId: 'row-first', turnId: 'answer-turn', mergedMessageIds: null }
const mergedRow = { ...row, mergedMessageIds: 'row-middle row-last' }
const done = {
  type: 'done', session_id: sessionId, turn_id: row.turnId, message_id: row.messageId,
  stats: { turn_failed: false, truncated: false },
}
const token = {
  type: 'token', session_id: sessionId, turn_id: row.turnId, message_id: row.messageId, content: 'reply',
}
const wire = (frames: readonly unknown[]) => frames.map((frame) => JSON.stringify(frame))

describe('matchingHansReplyCompletions — the Hans browser checker selection gate', () => {
  it('(a) accepts a single-round completion whose ID equals the row ID', () => {
    expect(matchingHansReplyCompletions(wire([done]), sessionId, row)).toEqual([done])
  })

  it('(b) accepts a multi-round completion whose ID is the last merged row ID', () => {
    const lastRoundDone = { ...done, message_id: 'row-last' }
    expect(matchingHansReplyCompletions(wire([token, lastRoundDone]), sessionId, mergedRow)).toEqual([lastRoundDone])
  })

  it('(c) rejects another turn even when the completion ID is among this row’s merged IDs', () => {
    const otherTurnDone = { ...done, turn_id: 'different-turn', message_id: 'row-last' }
    expect(matchingHansReplyCompletions(wire([otherTurnDone]), sessionId, mergedRow)).toEqual([])
  })

  it.each(['unrelated-id', 'row-las', 'row-last-suffix'])(
    '(d) rejects matching-turn ID %s because it is not an exact row-ID member',
    (messageId) => {
      expect(matchingHansReplyCompletions(wire([{ ...done, message_id: messageId }]), sessionId, mergedRow)).toEqual([])
    },
  )

  it.each([
    ['empty input', []],
    ['tokens without completion', [token]],
  ] as const)('(e) rejects %s when no done frame exists', (_name, frames) => {
    expect(matchingHansReplyCompletions(wire(frames), sessionId, mergedRow)).toEqual([])
  })

  it('still accepts the primary row ID when merged IDs also exist', () => {
    expect(matchingHansReplyCompletions(wire([done]), sessionId, mergedRow)).toEqual([done])
  })

  it('keeps another session excluded even when the turn and primary row ID match', () => {
    expect(matchingHansReplyCompletions(wire([{ ...done, session_id: 'different-session' }]), sessionId, row)).toEqual([])
  })

  it('preserves duplicate completions so the outer checker’s exactly-one assertion rejects them', () => {
    expect(matchingHansReplyCompletions(wire([done, done]), sessionId, row)).toEqual([done, done])
  })

  it.each([
    ['a non-done frame', token],
    ['an invalid message ID type', { ...done, message_id: true }],
    ['invalid completion stats', { ...done, stats: { turn_failed: 'false' } }],
  ])('does not treat %s as a schema-valid completion', (_name, frame) => {
    expect(matchingHansReplyCompletions(wire([frame]), sessionId, row)).toEqual([])
  })

  it('keeps malformed JSON loud instead of silently ignoring a bad captured frame', () => {
    expect(() => matchingHansReplyCompletions(['{'], sessionId, row)).toThrow(SyntaxError)
  })
})
