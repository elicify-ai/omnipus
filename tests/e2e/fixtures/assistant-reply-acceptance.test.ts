import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import {
  createHansReplyCompletionProbe,
  matchingHansReplyCompletions,
  matchingHansReplyTranscriptEntries,
} from './assistant-reply-acceptance'

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

  // Fix-round oracle: failed and truncated done frames must never count as success.
  it('R2 rejects a correlated failed completion with the existing failure explanation', () => {
    const failed = { ...done, message_id: 'row-last', stats: { turn_failed: true } }
    expect(() => matchingHansReplyCompletions(wire([failed]), sessionId, mergedRow))
      .toThrow(new Error('a failed fallback is not a successful follow-up answer'))
  })

  it('R2 rejects a correlated truncated completion with the existing truncation explanation', () => {
    const truncated = { ...done, message_id: 'row-last', stats: { truncated: true } }
    expect(() => matchingHansReplyCompletions(wire([truncated]), sessionId, mergedRow))
      .toThrow(new Error('a stopped or truncated answer is not completion'))
  })

  it('does not let an unrelated failed or truncated turn reject this row', () => {
    const unrelated = { ...done, session_id: 'different-session', stats: { turn_failed: true, truncated: true } }
    expect(matchingHansReplyCompletions(wire([unrelated]), sessionId, row)).toEqual([])
  })

  it('preserves acceptance when optional completion stats are absent', () => {
    const { stats: _stats, ...withoutStats } = done
    expect(matchingHansReplyCompletions(wire([withoutStats]), sessionId, row)).toEqual([withoutStats])
  })
})

// Oracle: FIX ROUND 1 item 1 explicitly permits primary OR merged IDs. The
// real two-entry JSONL input has narration on A and the expected nonce on B.
const agentId = 'hans-agent'
const nonce = 'hans-answer-nonce'
const transcriptRow = { ...row, mergedMessageIds: 'row-last' }
const narration = {
  id: 'row-first', role: 'assistant', agent_id: agentId, turn_id: row.turnId,
  content: 'Let me check before answering.',
}
const answer = {
  id: 'row-last', role: 'assistant', agent_id: agentId, turn_id: row.turnId,
  content: nonce, view_membership: 'chat', truncated: false,
}
const jsonl = (entries: readonly unknown[]) => entries.map((entry) => JSON.stringify(entry) + '\n').join('')

describe('matchingHansReplyTranscriptEntries — the real disk-poll predicate', () => {
  it('R1 accepts exactly the nonce entry on merged B after preamble A in the two-entry JSONL fixture', () => {
    // Vitest's Vite transform may rewrite new URL(asset, import.meta.url) to
    // an HTTP asset URL; the runner pins cwd to the repository for real FS input.
    const raw = fs.readFileSync(path.resolve('tests/e2e/fixtures/hans-multiround-transcript.jsonl'), 'utf8')
    expect(matchingHansReplyTranscriptEntries(raw, transcriptRow, agentId, row.turnId, nonce)).toEqual([answer])
  })

  it('R1 rejects a nonce on third ID C of the same turn when only B was merged', () => {
    const thirdEntry = { ...answer, id: 'unmerged-third-id' }
    expect(matchingHansReplyTranscriptEntries(jsonl([narration, thirdEntry]), transcriptRow, agentId, row.turnId, nonce))
      .toEqual([])
  })

  it('still accepts the nonce on the primary ID, with no merged IDs', () => {
    const primaryAnswer = { ...answer, id: row.messageId }
    expect(matchingHansReplyTranscriptEntries(jsonl([primaryAnswer]), row, agentId, row.turnId, nonce)).toEqual([primaryAnswer])
  })

  it('keeps the explicitly authorised primary ID eligible even when merged IDs exist', () => {
    const primaryAnswer = { ...answer, id: row.messageId }
    expect(matchingHansReplyTranscriptEntries(jsonl([primaryAnswer]), transcriptRow, agentId, row.turnId, nonce))
      .toEqual([primaryAnswer])
  })

  // Primary-ID inputs exercise every old restriction even before merged membership is fixed.
  it.each([
    ['a different turn', { turn_id: 'different-turn' }],
    ['a different agent', { agent_id: 'different-agent' }],
    ['a non-assistant role', { role: 'user' }],
    ['model-only membership', { view_membership: 'model' }],
    ['a truncated entry', { truncated: true }],
    ['content without the nonce', { content: 'different answer' }],
    ['non-string content', { content: null }],
  ])('preserves rejection for %s', (_name, change) => {
    const ineligible = { ...answer, id: row.messageId, ...change }
    expect(matchingHansReplyTranscriptEntries(jsonl([ineligible]), transcriptRow, agentId, row.turnId, nonce)).toEqual([])
  })

  it('rejects missing content instead of accepting an identity-only entry', () => {
    const { content: _content, ...withoutContent } = { ...answer, id: row.messageId }
    expect(matchingHansReplyTranscriptEntries(jsonl([withoutContent]), row, agentId, row.turnId, nonce)).toEqual([])
  })

  it('preserves multiple eligible entries so the outer exactly-one assertion rejects duplicates', () => {
    const primaryAnswer = { ...answer, id: row.messageId }
    expect(matchingHansReplyTranscriptEntries(jsonl([primaryAnswer, primaryAnswer]), row, agentId, row.turnId, nonce))
      .toEqual([primaryAnswer, primaryAnswer])
  })

  it('ignores only an unfinished final JSONL fragment after a complete eligible entry', () => {
    const primaryAnswer = { ...answer, id: row.messageId }
    expect(matchingHansReplyTranscriptEntries(jsonl([primaryAnswer]) + '{', row, agentId, row.turnId, nonce))
      .toEqual([primaryAnswer])
  })

  it('does not count an answer until its JSONL line is complete', () => {
    expect(matchingHansReplyTranscriptEntries(JSON.stringify(answer), transcriptRow, agentId, row.turnId, nonce)).toEqual([])
  })

  it('keeps a malformed complete JSONL row loud', () => {
    expect(() => matchingHansReplyTranscriptEntries('{\n', row, agentId, row.turnId, nonce)).toThrow(SyntaxError)
  })

  it('returns no entries for an empty transcript', () => {
    expect(matchingHansReplyTranscriptEntries('', row, agentId, row.turnId, nonce)).toEqual([])
  })
})

// Fake only the browser attribute-reading edge; selection, parsing and the
// probe used by the real checker are not mocked. Each fake owns its state.
function attributeReader(initial: Record<string, string | null>) {
  const attributes = { ...initial }
  return {
    attributes,
    getAttribute: async (name: string) => attributes[name] ?? null,
  }
}

const attributesFor = (snapshot: typeof transcriptRow) => ({
  'data-message-id': snapshot.messageId,
  'data-turn-id': snapshot.turnId,
  'data-merged-message-ids': snapshot.mergedMessageIds,
})

describe('createHansReplyCompletionProbe — refreshed row identity and useful mismatch evidence', () => {
  it('R3 re-reads late turn and merged attributes on the next poll without mutating earlier evidence', async () => {
    const reader = attributeReader({ ...attributesFor(transcriptRow), 'data-turn-id': null, 'data-merged-message-ids': null })
    const lastRoundDone = { ...done, message_id: 'row-last' }
    const probe = await createHansReplyCompletionProbe(reader, wire([lastRoundDone]), sessionId)
    const before = await probe()
    expect(before.row).toEqual({ messageId: row.messageId, turnId: null, mergedMessageIds: null })
    expect(before.completions).toEqual([])
    Object.assign(reader.attributes, attributesFor(transcriptRow))
    const after = await probe()
    expect(after.row).toEqual(transcriptRow)
    expect(after.completions).toEqual([lastRoundDone])
    expect(after.count).toBe(1)
    expect(before.row).toEqual({ messageId: row.messageId, turnId: null, mergedMessageIds: null })
  })

  it('R3 mismatch includes row snapshot, raw done count and schema issue text without token noise', async () => {
    const reader = attributeReader(attributesFor(transcriptRow))
    const otherTurn = { ...done, turn_id: 'different-turn' }
    const invalid = { ...done, message_id: true }
    const probe = await createHansReplyCompletionProbe(reader, wire([token, otherTurn, invalid]), sessionId)
    const sample = await probe()
    expect(sample.row).toEqual(transcriptRow)
    expect(sample.completions).toEqual([])
    // The probe's count is the exact value the real Playwright poll asserts.
    // Existing numeric 0 has no diagnostic detail and must fail this oracle.
    expect(sample.count).toEqual(expect.stringContaining('messageId="row-first"'))
    expect(sample.count).toEqual(expect.stringContaining('turnId="answer-turn"'))
    expect(sample.count).toEqual(expect.stringContaining('mergedMessageIds="row-last"'))
    expect(sample.count).toEqual(expect.stringContaining('doneFrames=2'))
    expect(sample.count).toEqual(expect.stringContaining('matches=0'))
    expect(sample.count).toEqual(expect.stringContaining('message_id: Expected string, received boolean'))
    expect(sample.count).not.toEqual(expect.stringContaining('content:'))
  })

  it('keeps numeric one for an actual single completion so the unchanged count assertion still applies', async () => {
    const probe = await createHansReplyCompletionProbe(attributeReader(attributesFor(transcriptRow)), wire([done]), sessionId)
    const sample = await probe()
    expect(sample.count).toBe(1)
    expect(sample.completions).toEqual([done])
  })

  it('keeps SyntaxError throwing through the real poll probe', async () => {
    const probe = await createHansReplyCompletionProbe(attributeReader(attributesFor(transcriptRow)), ['{'], sessionId)
    await expect(probe()).rejects.toThrow(SyntaxError)
  })
})
