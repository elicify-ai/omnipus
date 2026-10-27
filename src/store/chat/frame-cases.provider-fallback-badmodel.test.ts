// frame-cases.provider-fallback-badmodel.test.ts — founder decision Q70
// (a bad_model primary falls back to the backup model), review round 3
// findings CR R3-3 / SFH R3-1 / PRT R3-F1: a bad_model hop's fallback notice
// must explain the configuration problem — the model was NOT FOUND OR NOT
// OFFERED by its provider — in the LIVE chat exactly as the persisted note
// does; live and replay must render the same sentence.
//
// Oracles (read before the fix, never from the code under test):
//   - Q70 rule 1: the notice appears live and persisted alike and says
//     plainly that the first model was not found or not offered by its
//     provider.
//   - The backend's saved sentence — pkg/agent/fallback_note.go::
//     fallbackNoteText, bad_model branch — is the authoritative persisted
//     text the replay carrier's `message` field carries after a reload.
//   - Live/replay consistency: the live-rendered note text must equal the
//     replay-rendered note text for the same hop (the SPA may not keep a
//     second, vaguer template of its own).
//
// Seam: the REAL useChatStore.handleFrame over real frames — the
// frame-cases.provider-frames.test.ts precedent, not a hand-rolled reducer
// double. No module mocks: logDiagnostic is a no-op in test mode and nothing
// here asserts it.
//
// Deliberate gap: providerFallbackNote.pickNewModelHint is not asserted —
// the finding is the explanation text; the D12 hint gate (model_retired
// only) is unchanged and the backend sets no hint for model_not_found.

import { beforeEach, describe, expect, it } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import type { ServerFrame } from '@/lib/ws'

const SID = 'session-badmodel-live'

// The backend's persisted sentences (pkg/agent/fallback_note.go::
// fallbackNoteText) — what the replay carrier's `message` carries per hop.
const BAD_MODEL_SAVED_NOTE =
  'Answered by the Fallback model (final-x2) because primary-y was not found or is not offered by its provider.'
const ORDINARY_SAVED_NOTE =
  'Answered by the Fallback model (final-x2) because primary-y was unavailable.'

beforeEach(() => {
  useSessionStore.setState({ activeSessionId: SID })
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
})

function bucket() {
  return useChatStore.getState().sessionsById[SID]
}

/** All fallback notes visible in the bucket, in message order. */
function fallbackNotes() {
  const b = bucket()
  return b.messageOrder
    .map((id) => b.messagesById[id])
    .filter((m) => m.providerFallbackNote != null)
    .map((m) => ({ id: m.id, content: m.content, note: m.providerFallbackNote }))
}

function liveFallbackFrame(fields: {
  seq: number
  answered?: string
  unavailable?: string
  code?: 'rate_limited' | 'model_retired' | 'model_not_found'
}): ServerFrame {
  return {
    type: 'provider_fallback',
    session_id: SID,
    turn_id: 'turn-1',
    answered_model: fields.answered ?? 'final-x2',
    unavailable_model: fields.unavailable ?? 'primary-y',
    unavailable_code: fields.code ?? 'rate_limited',
    seq: fields.seq,
  } as unknown as ServerFrame
}

function replayCarrierFrame(fields: {
  entryId: string
  answered?: string
  unavailable?: string
  message: string
  code?: 'rate_limited' | 'model_retired' | 'model_not_found'
}): ServerFrame {
  return {
    type: 'replay_provider_fallback',
    session_id: SID,
    entry_id: fields.entryId,
    timestamp: '2026-10-26T00:00:00Z',
    answered_model: fields.answered ?? 'final-x2',
    unavailable_model: fields.unavailable ?? 'primary-y',
    unavailable_code: fields.code ?? 'rate_limited',
    message: fields.message,
  } as unknown as ServerFrame
}

describe('provider_fallback model_not_found — Q70 live/replay notice consistency (CR R3-3 / SFH R3-1 / PRT R3-F1)', () => {
  it('renders the not-found explanation live, identical to the persisted note its replay carries', () => {
    useChatStore
      .getState()
      .handleFrame(
        liveFallbackFrame({ seq: 1, answered: 'final-x2', unavailable: 'primary-y', code: 'model_not_found' }),
      )
    useChatStore
      .getState()
      .handleFrame(
        replayCarrierFrame({
          entryId: 'entry-badmodel',
          answered: 'final-x2',
          unavailable: 'primary-y',
          message: BAD_MODEL_SAVED_NOTE,
          code: 'model_not_found',
        }),
      )

    const notes = fallbackNotes()
    expect(notes).toHaveLength(2)
    const [live, replay] = notes
    // The persisted path renders the server sentence (expected green).
    expect(replay.content).toBe(BAD_MODEL_SAVED_NOTE)
    // The LIVE path must render the same explanation — the configuration
    // problem the user must fix. Fails today: the live template says only
    // "was unavailable", hiding that the model was not found/offered.
    expect(live.content).toBe(replay.content)
    expect(live.content).toBe(BAD_MODEL_SAVED_NOTE)
  })

  it('replay carrier renders the backend sentence when no live frame arrived (control: the persisted path is not the defect)', () => {
    useChatStore
      .getState()
      .handleFrame(
        replayCarrierFrame({
          entryId: 'entry-replay-only',
          answered: 'final-x2',
          unavailable: 'primary-y',
          message: BAD_MODEL_SAVED_NOTE,
          code: 'model_not_found',
        }),
      )

    const notes = fallbackNotes()
    expect(notes).toHaveLength(1)
    expect(notes[0].content).toBe(BAD_MODEL_SAVED_NOTE)
  })

  it('an ordinary unavailable reason still renders the plain unavailable line, live matching replay (control)', () => {
    useChatStore
      .getState()
      .handleFrame(
        liveFallbackFrame({ seq: 1, answered: 'final-x2', unavailable: 'primary-y', code: 'rate_limited' }),
      )
    useChatStore
      .getState()
      .handleFrame(
        replayCarrierFrame({
          entryId: 'entry-rate-limited',
          answered: 'final-x2',
          unavailable: 'primary-y',
          message: ORDINARY_SAVED_NOTE,
          code: 'rate_limited',
        }),
      )

    const notes = fallbackNotes()
    expect(notes).toHaveLength(2)
    const [live, replay] = notes
    expect(replay.content).toBe(ORDINARY_SAVED_NOTE)
    expect(live.content).toBe(replay.content)
    expect(live.content).toBe(ORDINARY_SAVED_NOTE)
  })
})
