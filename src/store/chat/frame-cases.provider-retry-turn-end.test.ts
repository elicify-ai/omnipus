// frame-cases.provider-retry-turn-end.test.ts — 6b UAT finding U3c
// (validator-report.md issue 3, evidence U3c-a.png / U3c-ping.png, verified
// twice): after a turn whose retries were EXHAUSTED, the stale "Retrying
// now…" line stayed above the composer in LATER turns, including turns with
// no retry at all. Root shape: provider-frames.ts sets providerRetryEvent on
// a provider_retry frame and only the recovered / waiting / provider_fallback
// paths clear it — a turn that ENDS (done, terminal error, user Stop) never
// retires the slot, so the next streaming turn (isStreaming true again)
// re-renders a line that describes a turn that no longer exists.
//
// Chief decision (6b fix round brief): the slot clears when the turn ends
// (failed, succeeded or stopped) and a new turn shows nothing until a NEW
// provider_retry frame for THAT turn arrives.
//
// Driven through the REAL useChatStore.handleFrame (frame-cases.provider-
// frames.test.ts precedent, not a hand-rolled reducer double). Oracles come
// from the chief decision above and the wire shapes the gateway already
// emits — never from the implementation under test.

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './store'
import { emptySessionState } from './session'
import { useSessionStore } from '@/store/session'
import { logDiagnostic } from '@/lib/telemetry'
import type { ServerFrame } from '@/lib/ws'

// Partial module mock: the telemetry sink is a process edge (mock at edges
// only) — everything else stays real.
vi.mock('@/lib/telemetry', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/telemetry')>()
  return { ...actual, logDiagnostic: vi.fn() }
})

const SID = 'session-provider-retry-turn-end'

beforeEach(() => {
  vi.mocked(logDiagnostic).mockClear()
  useSessionStore.setState({ activeSessionId: SID })
  useChatStore.setState({
    sessionsById: {
      [SID]: { ...emptySessionState(), isStreaming: true, activeTurnId: 'turn-1' },
    },
    messages: [],
    messagesById: {},
    isStreaming: true,
    providerRetryEvent: null,
  })
})

function bucket() {
  return useChatStore.getState().sessionsById[SID]
}

function providerRetryFrame(fields: { seq: number; turnId: string; attempt?: number }): ServerFrame {
  return {
    type: 'provider_retry',
    session_id: SID,
    turn_id: fields.turnId,
    provider: 'fakeprov',
    model: 'fake-model',
    retry_at: '2026-10-31T17:40:14Z',
    retry_after_seconds: 2,
    sent_at: '2026-10-31T17:40:12Z',
    attempt: fields.attempt ?? 2,
    max_attempts: 6,
    error_code: 'network',
    seq: fields.seq,
  }
}

// The turn SUCCEEDED: the gateway's terminator done for the announced turn
// (same shape the clear-retry screen fixture drives through expectValidFrame).
function turnDoneFrame(fields: { seq: number; turnId: string }): ServerFrame {
  return {
    type: 'done',
    session_id: SID,
    message_id: `a-reply-${fields.seq}`,
    turn_id: fields.turnId,
    seq: fields.seq,
    stats: { tokens: 1, cost: 0 },
  }
}

// The turn FAILED or was STOPPED: a terminal error frame — no
// client_message_id (that would be a message-scoped refusal, which keeps the
// turn running). The exhausted-retries U3d path ends with the mapped network
// message; the Stop path is the same frame with llm_error.code
// 'turn_canceled' (resolveClosedBubbleStatus → 'interrupted').
function turnErrorFrame(fields: { seq: number; code: 'network' | 'turn_canceled' }): ServerFrame {
  const message = fields.code === 'turn_canceled'
    ? 'Turn canceled.'
    : 'We couldn\'t reach the model provider. Check your internet connection and retry.'
  return {
    type: 'error',
    session_id: SID,
    message,
    payload: { llm_error: { code: fields.code, message, retryable: false } },
    seq: fields.seq,
  }
}

describe('provider_retry — the slot retires when its turn ends (6b U3c: stale "Retrying now…" line)', () => {
  it('a done for the turn retires the retry line — it must not survive into later turns (succeeded turn)', () => {
    useChatStore.getState().handleFrame(providerRetryFrame({ seq: 1, turnId: 'turn-1' }))
    expect(bucket().providerRetryEvent, 'instrument: the retry line is up while the turn runs').not.toBeNull()

    useChatStore.getState().handleFrame(turnDoneFrame({ seq: 2, turnId: 'turn-1' }))
    expect(bucket().isStreaming, 'instrument: done ended the turn').toBe(false)

    expect(bucket().providerRetryEvent,
      'the turn ended — a line that says a retry is coming describes nothing now').toBeNull()
  })

  it('a terminal error for the turn retires the retry line (failed turn — retries exhausted, U3d path)', () => {
    useChatStore.getState().handleFrame(providerRetryFrame({ seq: 1, turnId: 'turn-1' }))
    expect(bucket().providerRetryEvent).not.toBeNull()

    expect(bucket().isStreaming, 'instrument: retries belong to an active turn').toBe(true)
    expect(bucket().activeTurnId).toBe('turn-1')
    useChatStore.getState().handleFrame(turnErrorFrame({ seq: 2, code: 'network' }))
    expect(bucket().isStreaming, 'instrument: the terminal error ended the turn').toBe(false)

    expect(bucket().providerRetryEvent,
      'the exhausted turn failed — the stale line must go with it (U3c: it stayed through later turns)').toBeNull()
  })

  it('a user Stop retires the retry line (turn_canceled)', () => {
    useChatStore.getState().handleFrame(providerRetryFrame({ seq: 1, turnId: 'turn-1' }))
    expect(bucket().providerRetryEvent).not.toBeNull()

    useChatStore.getState().handleFrame(turnErrorFrame({ seq: 2, code: 'turn_canceled' }))
    expect(bucket().isStreaming, 'instrument: the stop acknowledgement ended the turn').toBe(false)

    expect(bucket().providerRetryEvent, 'the user stopped the turn — no retry line may outlive it').toBeNull()
  })

  it('a NEW turn re-arms the slot only from that turn\'s own retry frame (retirement is not a permanent off-switch)', () => {
    useChatStore.getState().handleFrame(providerRetryFrame({ seq: 1, turnId: 'turn-1' }))
    useChatStore.getState().handleFrame(turnDoneFrame({ seq: 2, turnId: 'turn-1' }))

    // The next turn streams with NO retry at first — and no stale event may
    // be waiting to render into it. (The retirement itself is pinned by the
    // three tests above; this test pins that a real retry still shows.)
    useChatStore.getState().handleFrame(providerRetryFrame({ seq: 3, turnId: 'turn-2', attempt: 2 }))

    expect(bucket().providerRetryEvent).toEqual(expect.objectContaining({
      turnId: 'turn-2',
      provider: 'fakeprov',
      model: 'fake-model',
      attempt: 2,
      maxAttempts: 6,
      errorCode: 'network',
    }))
  })
})
