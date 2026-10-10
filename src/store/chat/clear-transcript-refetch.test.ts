// clear-transcript-refetch.test.ts — FR-030/031 (U10b) SPA half, R3 round 3.
//
// Oracles: docs/internal/specs/session-core-spec.md FR-030; the saved server
// reference core-clear-reference-67345b1d7/{cmd_clear,clear_session}.go (one
// chat-view marker entry "Conversation context cleared", never pushed live;
// the clear dispatches only at the start of the session's own turn); and
// contracts/components/schemas/MessageStatusFrame.yaml (a `received`
// delivery status is a durable transcript append).
//
// Round-3 rules under test (review round 2):
//   D1  a projection fetched while the bucket is streaming OR replaying is
//       KEPT and applied on every busy→idle transition (turn done/error,
//       replay clear, catch-up completion) without a new network read;
//   D2  the read belongs to ONE /clear operation: it is keyed by that
//       operation's client_message_id, an obsolete response is ignored, and
//       an intent is retired only by a projection carrying THAT operation's
//       marker (the marker after that operation's own user row);
//   D3  only genuinely unconfirmed input is preserved across the merge — a
//       confirmed (received/working) pre-clear row obeys the server's
//       post-clear window;
//   D4  the real REST adapter carries the generated wire client_message_id
//       into the user row, so server rows and optimistic bubbles
//       correlate — the tests stub the HTTP edge, not the adapter;
//   D5  the history Retry completes the recovery: fetch + apply, no /clear
//       resend;
//   D6  a second turn completion after the marker is applied re-reads
//       nothing.
//
// What is real: the chat store's send/resend/frame paths, the REST adapter
// (fetchSessionMessages → parseWireMessageList → rawToMessage), the real
// React Query client. What is stubbed: the network edge only — the HTTP
// request function in '@/lib/http' and the WS connection.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { queryClient } from '@/lib/queryClient'
import { ApiError } from '@/lib/api-error'
import { replayingStartedAt } from './runtime-state'
import type { ServerFrame } from '@/lib/ws'
import type { ChatMessage } from './types'

// The ONLY stub: the HTTP edge (GET /sessions/{id}/messages answers here).
// fetchSessionMessages and rawToMessage stay real, so the wire fixtures
// below go through the actual adapter — the correlation field this suite
// depends on is the adapter's own output, not a test fabrication.
const requestMock = vi.hoisted(() => vi.fn())

vi.mock('@/lib/api/http', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api/http')>()
  return {
    ...actual,
    request: requestMock,
  }
})

const SID = 'sess-clear-refetch'
const MARKER_TEXT = 'Conversation context cleared'
const SUCCESS_REPLY = 'Context cleared. The conversation and its history are kept; the assistant continues from here with a fresh context.'

// ── Raw (wire-shaped) fixtures — what the server actually answers ────────────

function rawUser(id: string, clientMessageId: string | undefined, content: string, ts: string): Record<string, unknown> {
  return { id, role: 'user', content, timestamp: ts, agent_id: 'jim', status: 'ok', ...(clientMessageId ? { client_message_id: clientMessageId } : {}) }
}
function rawAssistant(id: string, content: string, ts: string): Record<string, unknown> {
  return { id, role: 'assistant', content, timestamp: ts, agent_id: 'jim', status: 'ok' }
}
function rawSystem(id: string, content: string, ts: string): Record<string, unknown> {
  return { id, role: 'system', content, timestamp: ts, agent_id: 'jim', status: 'ok' }
}

/** The server's projection once clear #N has executed: its own user row (echoing
 * the send's client_message_id), the reply, and the ONE marker entry after it. */
function rawPostClearProjection(clearClientMessageId: string, n: number): Record<string, unknown>[] {
  const t = (s: number) => `2026-10-10T00:00:0${s}Z`
  return [
    rawUser(`srv-u-clear-${n}`, clearClientMessageId, '/clear', t(0)),
    rawAssistant(`srv-a-reply-${n}`, SUCCESS_REPLY, t(1)),
    rawSystem(`srv-clear-marker-${n}`, MARKER_TEXT, t(2)),
  ]
}

/** The server's projection BEFORE a clear has executed (no marker, no clear rows). */
function rawPreClearProjection(): Record<string, unknown>[] {
  return [
    rawUser('srv-u-1', 'cmid-earlier', 'earlier question', '2026-10-09T23:00:00Z'),
    rawAssistant('srv-a-1', 'earlier answer', '2026-10-09T23:00:05Z'),
  ]
}

// ── Local (ChatMessage) fixtures for seeding buckets ─────────────────────────

function localMessage(partial: Partial<ChatMessage> & Pick<ChatMessage, 'id' | 'role' | 'content'>): ChatMessage {
  return { timestamp: new Date().toISOString(), status: 'done', ...partial } as ChatMessage
}

// ── Harness ──────────────────────────────────────────────────────────────────

function doneFrame(turnId: string | undefined, seq: number): ServerFrame {
  return {
    type: 'done', session_id: SID, message_id: 'a-reply', turn_id: turnId, seq, stats: { tokens: 1, cost: 0 },
  } as ServerFrame
}

function seedBucket(messages: ChatMessage[], opts: { isStreaming?: boolean; isReplaying?: boolean; activeTurnId?: string | null } = {}): void {
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      ...s.sessionsById,
      [SID]: {
        ...(s.sessionsById ?? {})[SID],
        messagesById: Object.fromEntries(messages.map((m) => [m.id, m])),
        messageOrder: messages.map((m) => m.id),
        isStreaming: opts.isStreaming ?? false,
        isReplaying: opts.isReplaying ?? false,
        replayCompletedForSession: SID,
        activeTurnId: opts.activeTurnId ?? null,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        sessionTokens: 0,
        sessionCost: 0,
      },
    },
    messages: [],
    messagesById: {},
    isStreaming: opts.isStreaming ?? false,
    isReplaying: opts.isReplaying ?? false,
    replayCompletedForSession: SID,
  }))
}

function bucket(): ReturnType<typeof useChatStore.getState>['sessionsById'][string] {
  const b = useChatStore.getState().sessionsById[SID]
  expect(b, 'bucket exists').toBeTruthy()
  return b!
}

/** The client_message_id of the Nth outgoing frame — the operation's identity. */
function sentClientMessageId(index = 0): string {
  const cmid = (sentFrames[index] as { client_message_id?: string } | undefined)?.client_message_id
  expect(cmid, 'the send carried a client_message_id').toBeTruthy()
  return cmid!
}

let sentFrames: unknown[] = []
let connectionOk: boolean

beforeEach(() => {
  sentFrames = []
  connectionOk = true
  requestMock.mockReset()
  queryClient.removeQueries()
  delete replayingStartedAt[SID]
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'jim', activeAgentType: null })
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
  useConnectionStore.setState({
    connection: { send: (p: unknown) => { sentFrames.push(p); return connectionOk }, close: () => {} } as never,
    isConnected: true,
  } as never)
})

afterEach(() => {
  vi.restoreAllMocks()
})

// ── Round-2 rules that must keep holding ─────────────────────────────────────

describe('/clear re-read — C1/C3/D3/D4 through the real REST adapter', () => {
  it('an idle /clear re-reads at its reply turn; the marker lands; the optimistic bubble correlates away (D4)', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    requestMock.mockImplementation(async () => rawPostClearProjection(sentClientMessageId(), 1))

    useChatStore.getState().sendMessage('/clear')
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))

    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalled()
    })
    await vi.waitFor(() => {
      const b = bucket()
      // Exactly ONE /clear user bubble: the server echo superseded the local
      // optimistic row through the REAL adapter's client_message_id.
      const clearBubbles = b.messageOrder.filter((id) => b.messagesById[id]?.content === '/clear')
      expect(clearBubbles).toHaveLength(1)
      expect(b.messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
      expect(b.messageOrder).toEqual(['srv-u-clear-1', 'srv-a-reply-1', 'srv-clear-marker-1'])
    })
  })

  it('a failed follow-up send (and its Retry state) survives a delayed projection', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    let releaseFetch: (value: unknown[]) => void = () => {}
    requestMock.mockImplementationOnce(() => new Promise<unknown[]>((resolve) => { releaseFetch = resolve }))

    useChatStore.getState().sendMessage('/clear')
    const clearCmid = sentClientMessageId(0)
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))
    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalled()
    })

    connectionOk = false
    useChatStore.getState().sendMessage('follow-up that fails')
    await new Promise((r) => setTimeout(r, 10))
    const failedId = bucket().messageOrder.find((id) => bucket().messagesById[id]?.content === 'follow-up that fails')
    expect(failedId).toBeTruthy()
    expect(bucket().messagesById[failedId!]?.status).toBe('error')

    releaseFetch(rawPostClearProjection(clearCmid, 1))
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })

    const b = bucket()
    expect(b.messagesById[failedId!]?.content).toBe('follow-up that fails')
    expect(b.messagesById[failedId!]?.status).toBe('error')
    expect(b.messagesById[failedId!]?.deliveryStatus).toBe('failed')
    expect(b.messageOrder.indexOf(failedId!)).toBeGreaterThan(b.messageOrder.indexOf('srv-clear-marker-1'))
  })

})

describe('/clear re-read — D1/D2: the held projection and operation ownership', () => {
  it('a read landing while STREAMING is held, and a FAILING later read still ends with the marker (D1a)', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    let releaseRead1: (value: unknown[]) => void = () => {}
    requestMock.mockImplementationOnce(() => new Promise<unknown[]>((resolve) => { releaseRead1 = resolve }))

    useChatStore.getState().sendMessage('/clear')
    const clearCmid = sentClientMessageId(0)
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))
    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalledTimes(1)
    })

    // A follow-up turn starts before read #1 resolves.
    seedBucket(
      [...bucket().messageOrder.map((id) => bucket().messagesById[id]!), localMessage({ id: 'a-live', role: 'assistant', content: '', status: 'streaming', isStreaming: true })],
      { isStreaming: true, activeTurnId: 'turn-followup' },
    )
    // Read #1 succeeds — with the marker — while the bucket is streaming.
    releaseRead1(rawPostClearProjection(clearCmid, 1))
    await new Promise((r) => setTimeout(r, 10))
    // Deferred, not applied, and NOT erased: the next read fails…
    requestMock.mockImplementationOnce(async () => { throw new ApiError(404, 'messages not found') })
    // …and the stream ends. The held marker must surface without a good read.
    useChatStore.getState().handleFrame(doneFrame('turn-followup', 2))

    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
    // The HELD projection satisfied the idle transition — the queued failing
    // read was never consumed, because the held result already carried the
    // marker (a deferred success must not force a second network read).
    expect(requestMock).toHaveBeenCalledTimes(1)
    // And a later failing recovery attempt cannot take the marker away.
    useChatStore.getState().retryClearTranscript(SID)
    await new Promise((r) => setTimeout(r, 10))
    expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    expect(requestMock).toHaveBeenCalledTimes(1)
  })

  it('a read landing while REPLAYING is applied when replay ends with NO done/error (D1b)', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    let releaseRead1: (value: unknown[]) => void = () => {}
    requestMock.mockImplementationOnce(() => new Promise<unknown[]>((resolve) => { releaseRead1 = resolve }))

    useChatStore.getState().sendMessage('/clear')
    const clearCmid = sentClientMessageId(0)
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))
    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalledTimes(1)
    })

    // The bucket enters replay (a re-attach) before read #1 resolves…
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })], { isReplaying: true })
    replayingStartedAt[SID] = Date.now() - 5_000
    // …and read #1 lands — with the marker — during replay.
    releaseRead1(rawPostClearProjection(clearCmid, 1))
    await new Promise((r) => setTimeout(r, 10))
    expect(bucket().messagesById['srv-clear-marker-1']).toBeUndefined()

    // Replay ends with no done and no error; backdated start ⇒ immediate clear.
    useChatStore.getState().setReplaying(false)
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
    // No extra network read was needed.
    expect(requestMock).toHaveBeenCalledTimes(1)
  })

  it('operation ownership: an older read can neither retire nor satisfy a newer /clear (D2)', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    let releaseRead1: (value: unknown[]) => void = () => {}
    requestMock.mockImplementationOnce(() => new Promise<unknown[]>((resolve) => { releaseRead1 = resolve }))

    // Clear #1 finishes; its read is outstanding.
    useChatStore.getState().sendMessage('/clear')
    const cmid1 = sentClientMessageId(0)
    useChatStore.getState().handleFrame(doneFrame('turn-1', 1))
    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalledTimes(1)
    })

    // Clear #2 is sent and finishes while read #1 is still outstanding. Its
    // read is its OWN network read (keyed by its operation), not a join of
    // #1's outstanding request — queued BEFORE the done so it serves read
    // #2 — and it fails fast and non-retryably, so #2's intent survives for
    // a later opportunity.
    requestMock.mockImplementationOnce(async () => { throw new ApiError(404, 'messages not found') })
    useChatStore.getState().sendMessage('/clear')
    const cmid2 = sentClientMessageId(1)
    useChatStore.getState().handleFrame(doneFrame('turn-2', 2))
    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalledTimes(2)
    })
    await new Promise((r) => setTimeout(r, 10))

    // Read #1 (marker #1, before #2 executed) resolves: it must not retire #2
    // and must not present #1's rows as #2's post-clear view.
    releaseRead1(rawPostClearProjection(cmid1, 1))
    await new Promise((r) => setTimeout(r, 10))
    expect(bucket().messagesById['srv-clear-marker-2']).toBeUndefined()

    // Read #2 resolves: #2's own marker (after #2's own user row) retires #2.
    requestMock.mockImplementationOnce(async () => rawPostClearProjection(cmid2, 2))
    useChatStore.getState().handleFrame(doneFrame('turn-3', 3))
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-2']?.content).toBe(MARKER_TEXT)
    })
    expect(bucket().messageOrder).not.toContain('srv-clear-marker-1')
    expect(requestMock).toHaveBeenCalledTimes(3)
  })

  it('a confirmed (received) pre-clear row obeys the post-clear window; a failed one is kept (D3)', async () => {
    seedBucket([
      localMessage({ id: 'confirmed-1', role: 'user', content: 'confirmed earlier question', deliveryStatus: 'received' }),
      localMessage({ id: 'failed-1', role: 'user', content: 'failed earlier question', status: 'error', deliveryStatus: 'failed' }),
    ])
    requestMock.mockImplementation(async () => rawPostClearProjection(sentClientMessageId(), 1))

    useChatStore.getState().sendMessage('/clear')
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))

    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
    const b = bucket()
    // Durably-appended pre-clear input obeys the server's display window…
    expect(b.messageOrder).not.toContain('confirmed-1')
    // …while genuinely unconfirmed/failed input survives.
    expect(b.messageOrder).toContain('failed-1')
    expect(b.messagesById['failed-1']?.status).toBe('error')
  })

  it('a projection without THIS operation’s marker keeps the intent and retries', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    requestMock.mockImplementationOnce(async () => rawPreClearProjection())

    useChatStore.getState().sendMessage('/clear')
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))

    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalledTimes(1)
    })
    await vi.waitFor(() => {
      const b = bucket()
      expect(b.messageOrder).toContain('srv-u-1')
      expect(b.messagesById['srv-clear-marker-1']).toBeUndefined()
    })

    requestMock.mockImplementationOnce(async () => rawPostClearProjection(sentClientMessageId(0), 1))
    useChatStore.getState().handleFrame(doneFrame('turn-clear-2', 2))
    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalledTimes(2)
    })
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
  })

})

describe('/clear re-read — D5/D6: recovery and retirement', () => {
  it('a failed read applies nothing stale and the history Retry completes recovery without resending /clear (D5)', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    requestMock.mockImplementationOnce(async () => { throw new ApiError(404, 'messages not found') })

    useChatStore.getState().sendMessage('/clear')
    expect(sentFrames).toHaveLength(1)
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))

    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalledTimes(1)
    })
    await new Promise((r) => setTimeout(r, 10))
    expect(bucket().messagesById['srv-clear-marker-1']).toBeUndefined()

    // The screen's history Retry (no /clear resend) completes fetch + apply.
    requestMock.mockImplementationOnce(async () => rawPostClearProjection(sentClientMessageId(0), 1))
    useChatStore.getState().retryClearTranscript(SID)

    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
    expect(requestMock).toHaveBeenCalledTimes(2)
    // Exactly one /clear frame went out in total.
    expect(sentFrames).toHaveLength(1)
  })

  it('a second completion after the marker is applied re-reads nothing (D6)', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    requestMock.mockImplementation(async () => rawPostClearProjection(sentClientMessageId(), 1))

    useChatStore.getState().sendMessage('/clear')
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
    expect(requestMock).toHaveBeenCalledTimes(1)

    // A second done for the session: the operation is retired — no re-read.
    useChatStore.getState().handleFrame(doneFrame('turn-clear-2', 2))
    await new Promise((r) => setTimeout(r, 20))
    expect(requestMock).toHaveBeenCalledTimes(1)
  })

  it("a plain message's done never starts a refresh, and a failed /clear send arms nothing", async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    useChatStore.getState().sendMessage('hello there')
    useChatStore.getState().handleFrame(doneFrame('turn-x', 1))
    await new Promise((r) => setTimeout(r, 10))
    expect(requestMock).not.toHaveBeenCalled()

    connectionOk = false
    useChatStore.getState().sendMessage('/clear')
    await new Promise((r) => setTimeout(r, 10))
    useChatStore.getState().handleFrame(doneFrame('turn-y', 2))
    await new Promise((r) => setTimeout(r, 10))
    expect(requestMock).not.toHaveBeenCalled()
  })

  it("a /clear sent mid-turn is not consumed by the running turn's own done (C1 kept)", async () => {
    seedBucket(
      [localMessage({ id: 'a-live', role: 'assistant', content: '', status: 'streaming', isStreaming: true })],
      { isStreaming: true, activeTurnId: 'turn-A' },
    )
    requestMock.mockImplementation(async () => rawPostClearProjection(sentClientMessageId(), 1))

    useChatStore.getState().sendMessage('/clear')
    useChatStore.getState().handleFrame(doneFrame('turn-A', 1))
    await new Promise((r) => setTimeout(r, 10))
    expect(requestMock).not.toHaveBeenCalled()

    useChatStore.getState().handleFrame(doneFrame('turn-clear', 2))
    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalled()
    })
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
  })

  it("the offline-queue drain path arms the refresh, and '/clear extra text' is the same command", async () => {
    useConnectionStore.setState({
      connection: { send: (p: unknown) => { sentFrames.push(p); return true }, close: () => {} } as never,
      isConnected: false,
    } as never)
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    requestMock.mockImplementation(async () => rawPostClearProjection(sentClientMessageId(), 1))

    useChatStore.getState().sendMessage('/clear')
    expect(sentFrames).toHaveLength(0)
    useConnectionStore.setState({ isConnected: true } as never)
    useChatStore.getState().drainOutboundQueue()
    expect(sentFrames).toHaveLength(1)

    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))
    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalled()
    })
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
  })
})
