// frame-cases.provider-frames.test.ts: provider-messages spec §8 failover-chain
// frame reducers — provider-frames.ts::handleProviderFrame — driven through the
// REAL useChatStore.handleFrame (frame-cases.catchup.test.ts precedent, not a
// hand-rolled reducer double). Expected values derive from the spec (§6 exact
// templates, §7.4/D17 once-per-CHAT-per-pair, MIN-104 qualifier, D12 hint gate)
// and from the backend rule in
// pkg/agent/fallback_note.go::queueProviderFallbackNote (frame always emitted,
// note only when the pair is fresh) — never from the implementation under
// test.

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { logDiagnostic } from '@/lib/telemetry'
import { assembleFallbackNoteMessage } from './slices/provider-frames'
import type { WsReceiveFrame } from '@/lib/ws'

// Partial module mock: the telemetry sink is a process edge (mock at edges
// only) — everything else in the module stays real. logDiagnostic is a no-op
// in test mode, so a spy is the only way to observe the skip was logged.
vi.mock('@/lib/telemetry', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/telemetry')>()
  return { ...actual, logDiagnostic: vi.fn() }
})

const SID = 'session-provider-frames'

// Spec §6 exact template (derived from the spec table row, not the code):
// "Answered by the Fallback model ({answered_model}) because
// {unavailable_model} was unavailable." — never "backup".
const EXPECTED_NOTE_TEXT =
  'Answered by the Fallback model (fallback-x) because primary-y was unavailable.'

beforeEach(() => {
  vi.mocked(logDiagnostic).mockClear()
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

function providerRetryFrame(fields: {
  seq: number
  turnId?: string
  provider?: string
  model?: string
  attempt?: number
}): WsReceiveFrame {
  return {
    type: 'provider_retry',
    session_id: SID,
    turn_id: fields.turnId ?? 'turn-1',
    provider: fields.provider ?? 'openrouter',
    model: fields.model ?? 'glm-5.3-flash',
    retry_at: '2026-09-27T00:00:30Z',
    retry_after_seconds: 30,
    sent_at: '2026-09-27T00:00:00Z',
    attempt: fields.attempt ?? 2,
    max_attempts: 3,
    error_code: 'rate_limited',
    seq: fields.seq,
  } as unknown as WsReceiveFrame
}

function providerFallbackFrame(fields: {
  seq: number
  turnId?: string
  answered?: string
  unavailable?: string
  code?: 'rate_limited' | 'model_retired'
}): WsReceiveFrame {
  return {
    type: 'provider_fallback',
    session_id: SID,
    turn_id: fields.turnId ?? 'turn-1',
    answered_model: fields.answered ?? 'fallback-x',
    unavailable_model: fields.unavailable ?? 'primary-y',
    unavailable_code: fields.code ?? 'rate_limited',
    seq: fields.seq,
  } as unknown as WsReceiveFrame
}

function replayFallbackFrame(fields: {
  entryId: string
  timestamp?: string
  answered?: string
  unavailable?: string
  message?: string
  code?: 'rate_limited' | 'model_retired'
}): WsReceiveFrame {
  return {
    type: 'replay_provider_fallback',
    session_id: SID,
    entry_id: fields.entryId,
    timestamp: fields.timestamp ?? '2026-09-27T00:00:00Z',
    answered_model: fields.answered ?? 'fallback-x',
    unavailable_model: fields.unavailable ?? 'primary-y',
    unavailable_code: fields.code ?? 'rate_limited',
    message: fields.message ?? EXPECTED_NOTE_TEXT,
  } as unknown as WsReceiveFrame
}

describe('provider_retry — fact threading (§7.1 item 3 / §8 item 2)', () => {
  it('threads every frame fact into ProviderRetryEventData; receivedAt is a real client-clock instant (C-11)', () => {
    useChatStore.getState().handleFrame(providerRetryFrame({ seq: 1 }))

    const ev = bucket().providerRetryEvent
    expect(ev).toEqual({
      turnId: 'turn-1',
      provider: 'openrouter',
      model: 'glm-5.3-flash',
      retryAt: '2026-09-27T00:00:30Z',
      sentAt: '2026-09-27T00:00:00Z',
      receivedAt: expect.any(String),
      attempt: 2,
      maxAttempts: 3,
    })
    // C-11: the receipt anchor exists and parses as an instant (client clock
    // at reducer entry — its exact value is nondeterministic by design).
    expect(Number.isNaN(Date.parse(ev!.receivedAt))).toBe(false)
  })

  it('MIN-104: same turn + same provider + model switch carries previousProvider/previousModel', () => {
    useChatStore.getState().handleFrame(providerRetryFrame({ seq: 1, model: 'glm-5.3-flash' }))
    useChatStore.getState().handleFrame(providerRetryFrame({ seq: 2, model: 'glm-5.3-air' }))

    expect(bucket().providerRetryEvent).toEqual({
      turnId: 'turn-1',
      provider: 'openrouter',
      model: 'glm-5.3-air',
      retryAt: '2026-09-27T00:00:30Z',
      sentAt: '2026-09-27T00:00:00Z',
      receivedAt: expect.any(String),
      attempt: 2,
      maxAttempts: 3,
      previousProvider: 'openrouter',
      previousModel: 'glm-5.3-flash',
    })
  })

  it('MIN-104: a provider switch within the turn carries NO previous candidate', () => {
    useChatStore.getState().handleFrame(providerRetryFrame({ seq: 1, provider: 'openrouter', model: 'glm-5.3-flash' }))
    useChatStore.getState().handleFrame(providerRetryFrame({ seq: 2, provider: 'anthropic', model: 'claude-x' }))

    expect(bucket().providerRetryEvent).toEqual({
      turnId: 'turn-1',
      provider: 'anthropic',
      model: 'claude-x',
      retryAt: '2026-09-27T00:00:30Z',
      sentAt: '2026-09-27T00:00:00Z',
      receivedAt: expect.any(String),
      attempt: 2,
      maxAttempts: 3,
    })
  })

  it("MIN-104: a NEW turn's first frame carries no qualifier (no previous candidate at all)", () => {
    useChatStore.getState().handleFrame(providerRetryFrame({ seq: 1, model: 'glm-5.3-flash' }))
    useChatStore.getState().handleFrame(providerRetryFrame({ seq: 2, turnId: 'turn-2', model: 'glm-5.3-air' }))

    expect(bucket().providerRetryEvent).toEqual({
      turnId: 'turn-2',
      provider: 'openrouter',
      model: 'glm-5.3-air',
      retryAt: '2026-09-27T00:00:30Z',
      sentAt: '2026-09-27T00:00:00Z',
      receivedAt: expect.any(String),
      attempt: 2,
      maxAttempts: 3,
    })
  })

  it('MIN-104: same turn + same provider + SAME model (attempt N+1 of one candidate) carries no qualifier', () => {
    useChatStore.getState().handleFrame(providerRetryFrame({ seq: 1, attempt: 2 }))
    useChatStore.getState().handleFrame(providerRetryFrame({ seq: 2, attempt: 3 }))

    expect(bucket().providerRetryEvent).toEqual({
      turnId: 'turn-1',
      provider: 'openrouter',
      model: 'glm-5.3-flash',
      retryAt: '2026-09-27T00:00:30Z',
      sentAt: '2026-09-27T00:00:00Z',
      receivedAt: expect.any(String),
      attempt: 3,
      maxAttempts: 3,
    })
  })
})

describe('provider_fallback — live path + the D17 once-per-CHAT-per-pair guard', () => {
  it('appends exactly one §6 note; a repeat frame for the SAME pair in ONE turn adds no second note', () => {
    useChatStore.getState().handleFrame(providerFallbackFrame({ seq: 10 }))
    useChatStore.getState().handleFrame(providerFallbackFrame({ seq: 11 }))

    const notes = fallbackNotes()
    expect(notes).toHaveLength(1)
    expect(notes[0].content).toBe(EXPECTED_NOTE_TEXT)
    expect(notes[0].note).toStrictEqual({ message: EXPECTED_NOTE_TEXT })
    expect(bucket().providerRetryEvent).toBeNull()
  })

  it('D17: the same pair in a NEW turn of the same chat adds no second note (once per CHAT per pair, not per turn)', () => {
    useChatStore.getState().handleFrame(providerFallbackFrame({ seq: 10, turnId: 'turn-1' }))
    useChatStore.getState().handleFrame(providerFallbackFrame({ seq: 11, turnId: 'turn-2' }))

    const notes = fallbackNotes()
    expect(notes).toHaveLength(1)
    expect(notes[0].content).toBe(EXPECTED_NOTE_TEXT)
  })

  it('a changed pair appends a second note (only a pair change re-notes, §7.4/D17)', () => {
    useChatStore.getState().handleFrame(providerFallbackFrame({ seq: 10 }))
    useChatStore.getState().handleFrame(providerFallbackFrame({ seq: 11, unavailable: 'primary-z' }))

    const notes = fallbackNotes()
    expect(notes).toHaveLength(2)
    expect(notes[0].content).toBe('Answered by the Fallback model (fallback-x) because primary-y was unavailable.')
    expect(notes[1].content).toBe('Answered by the Fallback model (fallback-x) because primary-z was unavailable.')
  })

  it('a pair already shown by replay seeds the guard — a live frame for that pair adds nothing (no live duplicate after load)', () => {
    useChatStore.getState().handleFrame(replayFallbackFrame({ entryId: 'e1' }))
    useChatStore.getState().handleFrame(providerFallbackFrame({ seq: 10, turnId: 'turn-2' }))

    const notes = fallbackNotes()
    expect(notes).toHaveLength(1)
    // The survivor is the REPLAYED row — proving the live frame appended nothing.
    expect(notes[0].id).toBe('e1')
  })

  it('a deduped repeat still clears the retry-indicator slot (the guard dedupes the note, not the frame)', () => {
    useChatStore.getState().handleFrame(providerRetryFrame({ seq: 5 }))
    expect(bucket().providerRetryEvent).not.toBeNull()
    useChatStore.getState().handleFrame(providerFallbackFrame({ seq: 6 }))
    useChatStore.getState().handleFrame(providerFallbackFrame({ seq: 7 }))

    expect(bucket().providerRetryEvent).toBeNull()
    expect(fallbackNotes()).toHaveLength(1)
  })

  it('contract violation: a frame missing answered_model is skipped + logged, and renders no invented copy', () => {
    const violated = {
      type: 'provider_fallback',
      session_id: SID,
      turn_id: 'turn-1',
      answered_model: undefined,
      unavailable_model: 'primary-y',
      unavailable_code: 'rate_limited',
      seq: 10,
    } as unknown as WsReceiveFrame
    useChatStore.getState().handleFrame(violated)

    expect(fallbackNotes()).toHaveLength(0)
    expect(vi.mocked(logDiagnostic)).toHaveBeenCalledWith('chatProviderFallbackNoteSkipped', {
      sessionId: SID,
      turnId: 'turn-1',
    })
    expect(bucket().providerRetryEvent).toBeNull()
  })

  it('D12: pickNewModelHint is set only when unavailable_code is model_retired', () => {
    useChatStore.getState().handleFrame(providerFallbackFrame({ seq: 10, code: 'model_retired' }))

    const notes = fallbackNotes()
    expect(notes).toHaveLength(1)
    expect(notes[0].note).toStrictEqual({ message: EXPECTED_NOTE_TEXT, pickNewModelHint: true })
  })

  it('D12 negative: a rate_limited fallback carries no pickNewModelHint key at all', () => {
    useChatStore.getState().handleFrame(providerFallbackFrame({ seq: 10, code: 'rate_limited' }))

    const notes = fallbackNotes()
    expect(notes).toHaveLength(1)
    // toStrictEqual: distinguishes key-absent from key-present-undefined —
    // the hint must be absent, not merely falsy.
    expect(notes[0].note).toStrictEqual({ message: EXPECTED_NOTE_TEXT })
  })

  it('D17: a pair that changes only in the ANSWERED model is a fresh pair (a different model answering re-notes)', () => {
    useChatStore.getState().handleFrame(providerFallbackFrame({ seq: 10 }))
    useChatStore.getState().handleFrame(providerFallbackFrame({ seq: 11, answered: 'fallback-w' }))

    const notes = fallbackNotes()
    expect(notes).toHaveLength(2)
    expect(notes[1].content).toBe('Answered by the Fallback model (fallback-w) because primary-y was unavailable.')
  })

  it('a live frame deduped as the FIRST fallback still clears the retry-indicator slot (replay-seeded pair)', () => {
    useChatStore.getState().handleFrame(providerRetryFrame({ seq: 5 }))
    expect(bucket().providerRetryEvent).not.toBeNull()
    // The replay carrier renders the note and seeds the pair; it does not
    // touch the live retry slot.
    useChatStore.getState().handleFrame(replayFallbackFrame({ entryId: 'e1' }))
    expect(bucket().providerRetryEvent).not.toBeNull()
    useChatStore.getState().handleFrame(providerFallbackFrame({ seq: 6 }))

    // Deduped (pair seeded) — yet the frame itself still clears the slot.
    expect(bucket().providerRetryEvent).toBeNull()
    expect(fallbackNotes()).toHaveLength(1)
  })
})

describe('replay_provider_fallback — persisted-note carrier (FB-2)', () => {
  it('renders the persisted note exactly; a duplicate carrier (same entry_id) appends nothing (idempotence guard)', () => {
    useChatStore.getState().handleFrame(replayFallbackFrame({ entryId: 'e1' }))
    useChatStore.getState().handleFrame(replayFallbackFrame({ entryId: 'e1' }))

    expect(fallbackNotes()).toHaveLength(1)
    expect(bucket().messagesById['e1']).toEqual({
      id: 'e1',
      role: 'system',
      content: EXPECTED_NOTE_TEXT,
      timestamp: '2026-09-27T00:00:00Z',
      status: 'done',
      isStreaming: false,
      providerFallbackNote: { message: EXPECTED_NOTE_TEXT },
    })
  })

  it('a carrier missing message AND both models is skipped + logged (no invented copy)', () => {
    const violated = {
      type: 'replay_provider_fallback',
      session_id: SID,
      entry_id: 'e-skip',
      timestamp: '2026-09-27T00:00:00Z',
      message: '',
    } as unknown as WsReceiveFrame
    useChatStore.getState().handleFrame(violated)

    expect(fallbackNotes()).toHaveLength(0)
    expect(vi.mocked(logDiagnostic)).toHaveBeenCalledWith('chatProviderFallbackNoteSkipped', {
      sessionId: SID,
      entryId: 'e-skip',
    })
  })
})

describe('assembleFallbackNoteMessage — §6 template + contract-violation guard', () => {
  it('returns the exact §6 template with both model names', () => {
    expect(assembleFallbackNoteMessage('fallback-x', 'primary-y')).toBe(EXPECTED_NOTE_TEXT)
  })

  it('returns null for an undefined answered model (never invented copy)', () => {
    expect(assembleFallbackNoteMessage(undefined, 'primary-y')).toBeNull()
  })

  it('returns null for an empty-string answered model', () => {
    expect(assembleFallbackNoteMessage('', 'primary-y')).toBeNull()
  })

  it('returns null for a missing unavailable model (both names are required by the template)', () => {
    expect(assembleFallbackNoteMessage('fallback-x', undefined)).toBeNull()
    expect(assembleFallbackNoteMessage('fallback-x', '')).toBeNull()
  })
})
