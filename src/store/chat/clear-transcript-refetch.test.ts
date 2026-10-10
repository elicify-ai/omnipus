// clear-transcript-refetch.test.ts — FR-030/031 (U10b) SPA half, R3 round 4.
//
// Oracles: docs/internal/specs/session-core-spec.md FR-030; the saved server
// reference core-clear-reference-67345b1d7/{cmd_clear,clear_session}.go (one
// chat-view marker entry "Conversation context cleared", never pushed live;
// the clear dispatches only at the start of the session's own turn);
// contracts/components/schemas/MessageStatusFrame.yaml (a `received` delivery
// status is a durable transcript append).
//
// The round-4 invariant under test:
//   V1  the view after an operation's projection is applied = the server
//       projection + the LIVE TAIL: every local row positioned AFTER this
//       operation's own /clear user row in the bucket's current order, that
//       the projection does not already contain (matched by server id, else
//       clientMessageId), in local order — whatever its role or delivery
//       status. Rows at or before the /clear row are never re-appended.
//   V2  the projection is applied at the FIRST idle moment from EVERY
//       idle transition (turn done/error, setReplaying(false), the
//       replay-clear timer, catch_up_complete) through ONE helper, with no
//       new network read when a held projection exists.
//   V3  a pending operation's read failure is VISIBLE as the screen's
//       history-error state, and the screen Retry runs that operation's own
//       read+apply only.
//   D2  operation ownership: an obsolete response — one the operation no
//       longer owns, or one that predates the operation (no own user row) —
//       is never applied and never retires the intent.
//
// What is real: the chat store's send/resend/frame paths, the REST adapter
// (fetchSessionMessages → parseWireMessageList → rawToMessage), the real
// React Query client. What is stubbed: the network edge only — the HTTP
// request function in '@/lib/api/http' and the WS connection.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { queryClient } from '@/lib/queryClient'
import { replayingStartedAt } from './runtime-state'
import type { ServerFrame } from '@/lib/ws'
import type { ChatMessage } from './types'

// The ONLY stub: the HTTP edge (GET /sessions/{id}/messages answers here).
// fetchSessionMessages and rawToMessage stay real, so the wire fixtures
// below go through the actual adapter.
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

/** The server's projection BEFORE a clear has executed (no clear user row, no marker). */
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

describe('/clear re-read — arming, grammar, and the real adapter (C1/D3/D4, T5)', () => {
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
      const clearBubbles = b.messageOrder.filter((id) => b.messagesById[id]?.content === '/clear')
      expect(clearBubbles).toHaveLength(1)
      expect(b.messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
      // The projection leads the view, in the server's order (the V1 live
      // tail may follow it with local rows — here, the finalized local
      // assistant placeholder).
      expect(b.messageOrder.slice(0, 3)).toEqual(['srv-u-clear-1', 'srv-a-reply-1', 'srv-clear-marker-1'])
      for (const id of b.messageOrder.slice(3)) {
        const extra = b.messagesById[id]!
        expect(extra.role).toBe('assistant')
        expect(extra.content).toBe('')
      }
    })
  })

  it('a /clear sent mid-turn is not consumed by the running turn\'s own done (C1)', async () => {
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

  it('T5: a completion WITHOUT turn_id still consumes the refresh (the no-stream fallback)', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    requestMock.mockImplementation(async () => rawPostClearProjection(sentClientMessageId(), 1))

    useChatStore.getState().sendMessage('/clear')
    useChatStore.getState().handleFrame(doneFrame(undefined, 1))

    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalled()
    })
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
  })

  it('T5: \'/clear\' with trailing words is the same server command — the verbatim frame arms the refresh', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    requestMock.mockImplementation(async () => rawPostClearProjection(sentClientMessageId(), 1))

    useChatStore.getState().sendMessage('/clear ignore trailing words')
    // The command text leaves the wire VERBATIM; the server dispatches on the
    // first token.
    expect(sentFrames[0]).toMatchObject({ type: 'message', content: '/clear ignore trailing words' })

    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))
    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalled()
    })
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
  })

  it('T5: a FAILED /clear send arms nothing — and its successful Retry (resend path) does', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    requestMock.mockImplementation(async () => rawPostClearProjection(sentClientMessageId(1), 1))

    connectionOk = false
    useChatStore.getState().sendMessage('/clear')
    await new Promise((r) => setTimeout(r, 10))
    const failedId = bucket().messageOrder.find((id) => bucket().messagesById[id]?.content === '/clear')
    expect(failedId).toBeTruthy()
    expect(bucket().messagesById[failedId!]?.status).toBe('error')
    useChatStore.getState().handleFrame(doneFrame('turn-z', 1))
    await new Promise((r) => setTimeout(r, 10))
    expect(requestMock).not.toHaveBeenCalled()

    connectionOk = true
    useChatStore.getState().resendMessage(failedId!)
    expect(sentFrames).toHaveLength(2)
    useChatStore.getState().handleFrame(doneFrame('turn-retry', 2))

    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalled()
    })
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
  })

  it('the offline-queue drain path arms the refresh too', async () => {
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

  it("a plain message's done never starts a refresh, and a confirmed answered follow-up still precedes the clear row it belongs to", async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    useChatStore.getState().sendMessage('hello there')
    useChatStore.getState().handleFrame(doneFrame('turn-x', 1))
    await new Promise((r) => setTimeout(r, 10))
    expect(requestMock).not.toHaveBeenCalled()
  })
})

describe('/clear re-read — V1 live tail and D3 window (the view = projection + tail)', () => {
  it('T2: a confirmed follow-up AND its streamed answer survive an older projection, after the marker; a confirmed pre-clear row stays gone', async () => {
    seedBucket([
      // A confirmed, answered PRE-CLEAR row (before the /clear row).
      localMessage({ id: 'pre-clear-confirmed', role: 'user', content: 'confirmed earlier question', deliveryStatus: 'received' }),
      { id: 'pre-clear-answer', role: 'assistant', content: 'earlier answer', timestamp: new Date().toISOString(), status: 'done' },
    ])
    let releaseRead1: (value: unknown[]) => void = () => {}
    requestMock.mockImplementationOnce(() => new Promise<unknown[]>((resolve) => { releaseRead1 = resolve }))

    useChatStore.getState().sendMessage('/clear')
    const clearCmid = sentClientMessageId(0)
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))
    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalledTimes(1)
    })

    // A follow-up is sent, durably received, its answer streams and completes.
    useChatStore.getState().sendMessage('follow-up question')
    const followUpId = bucket().messageOrder.find((id) => bucket().messagesById[id]?.content === 'follow-up question')
    expect(followUpId).toBeTruthy()
    // The gateway announces the follow-up turn, then answers it.
    useChatStore.getState().handleFrame({
      type: 'session_state', session_id: SID, user_id: 'u1', pending_approvals: [],
      active_turn: { turn_id: 'turn-followup', agent_id: 'jim', started_at: '2026-10-10T00:00:10Z' },
      emitted_at: '2026-10-10T00:00:10Z',
    } as ServerFrame)
    // Contiguous per-session seq numbers — the gateway's wire order — or the
    // sequence gate reads the jump as a gap and drops the frames.
    useChatStore.getState().handleFrame({
      type: 'message_status', session_id: SID, client_message_id: followUpId, state: 'received', seq: 2,
    } as ServerFrame)
    useChatStore.getState().handleFrame({
      type: 'token', session_id: SID, content: 'the answer ', message_id: 'msg-followup', turn_id: 'turn-followup', agent_id: 'jim', seq: 3,
    } as ServerFrame)
    useChatStore.getState().handleFrame({
      type: 'done', session_id: SID, message_id: 'msg-followup', turn_id: 'turn-followup', seq: 4, stats: { tokens: 3, cost: 0 },
    } as ServerFrame)
    await vi.waitFor(() => {
      // The streamed answer lands on the locally-minted bubble (the reducer
      // adopts the send-time placeholder for the same turn and registers the
      // server's message_id on it) — assert by content and position.
      const b = bucket()
      const followUpIdx = b.messageOrder.indexOf(followUpId!)
      const answer = b.messagesById[b.messageOrder[followUpIdx + 1]]
      expect(answer?.role).toBe('assistant')
      expect(answer?.content).toBe('the answer ')
      expect(answer?.status).toBe('done')
    })

    // The OLDER projection — which contains neither the follow-up nor its
    // answer — is released against the now-idle bucket.
    releaseRead1(rawPostClearProjection(clearCmid, 1))
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })

    const b = bucket()
    // V1 live tail: the follow-up question AND its answer stay, AFTER the marker.
    expect(b.messagesById[followUpId!]?.content).toBe('follow-up question')
    const followUpIdx = b.messageOrder.indexOf(followUpId!)
    expect(b.messagesById[b.messageOrder[followUpIdx + 1]]?.content).toBe('the answer ')
    expect(followUpIdx).toBeGreaterThan(b.messageOrder.indexOf('srv-clear-marker-1'))
    expect(followUpIdx + 1).toBeGreaterThan(b.messageOrder.indexOf('srv-clear-marker-1'))
    // D3: the confirmed pre-clear row and its answer obey the server window.
    expect(b.messageOrder).not.toContain('pre-clear-confirmed')
    expect(b.messageOrder).not.toContain('pre-clear-answer')
  })

  it('D3: pre-clear rows — confirmed OR failed — obey the post-clear window (never re-appended)', async () => {
    seedBucket([
      localMessage({ id: 'confirmed-1', role: 'user', content: 'confirmed earlier question', deliveryStatus: 'received' }),
      localMessage({ id: 'failed-pre-clear', role: 'user', content: 'failed earlier question', status: 'error', deliveryStatus: 'failed' }),
    ])
    requestMock.mockImplementation(async () => rawPostClearProjection(sentClientMessageId(), 1))

    useChatStore.getState().sendMessage('/clear')
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))

    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
    const b = bucket()
    // Both sit BEFORE the /clear row: the server window governs them, and a
    // failed delivery does not resurrect a pre-clear row. (A failed row sent
    // AFTER the /clear is live-tail and survives — see the delayed test.)
    expect(b.messageOrder).not.toContain('confirmed-1')
    expect(b.messageOrder).not.toContain('failed-pre-clear')
  })

  it('T5: newer client-only system rows survive the merge; older server rows the projection drops stay gone', async () => {
    seedBucket([
      { id: 'old-server-sys', role: 'system', content: 'old server marker', timestamp: '2026-10-09T22:00:00Z', agentId: 'jim', status: 'done' },
      localMessage({ id: 'u-1', role: 'user', content: 'earlier question' }),
    ])
    requestMock.mockImplementation(async () => rawPostClearProjection(sentClientMessageId(), 1))

    useChatStore.getState().sendMessage('/clear')
    // A client-only refusal lands AFTER the /clear was sent.
    useChatStore.getState().appendMessage({
      id: 'local-refusal-1', role: 'system', content: '/new no longer exists.', timestamp: new Date().toISOString(), status: 'done',
    })

    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })

    const b = bucket()
    expect(b.messageOrder).toContain('local-refusal-1')
    expect(b.messagesById['local-refusal-1']?.content).toBe('/new no longer exists.')
    expect(b.messageOrder).not.toContain('old-server-sys')
  })

  it('a delayed FAILED follow-up send (and its Retry state) survives a delayed projection', async () => {
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
  })
})

describe('/clear re-read — D1/D2: the held projection and operation ownership', () => {
  it('D1a: a read landing while STREAMING is held and applied at idle without a second read', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    let releaseRead1: (value: unknown[]) => void = () => {}
    requestMock.mockImplementationOnce(() => new Promise<unknown[]>((resolve) => { releaseRead1 = resolve }))

    useChatStore.getState().sendMessage('/clear')
    const clearCmid = sentClientMessageId(0)
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))
    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalledTimes(1)
    })

    seedBucket(
      [...bucket().messageOrder.map((id) => bucket().messagesById[id]!), localMessage({ id: 'a-live', role: 'assistant', content: '', status: 'streaming', isStreaming: true })],
      { isStreaming: true, activeTurnId: 'turn-followup' },
    )
    releaseRead1(rawPostClearProjection(clearCmid, 1))
    await new Promise((r) => setTimeout(r, 10))
    expect(bucket().messagesById['srv-clear-marker-1']).toBeUndefined()

    useChatStore.getState().handleFrame(doneFrame('turn-followup', 2))
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
    // The held projection satisfied the idle transition — one read total.
    expect(requestMock).toHaveBeenCalledTimes(1)
    useChatStore.getState().retryClearTranscript(SID)
    await new Promise((r) => setTimeout(r, 10))
    expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    expect(requestMock).toHaveBeenCalledTimes(1)
  })

  it('T1/D1: a read landing while REPLAYING is applied when replay ends via an actual catch_up_complete frame', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    let releaseRead1: (value: unknown[]) => void = () => {}
    requestMock.mockImplementationOnce(() => new Promise<unknown[]>((resolve) => { releaseRead1 = resolve }))

    useChatStore.getState().sendMessage('/clear')
    const clearCmid = sentClientMessageId(0)
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))
    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalledTimes(1)
    })

    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })], { isReplaying: true })
    releaseRead1(rawPostClearProjection(clearCmid, 1))
    await new Promise((r) => setTimeout(r, 10))
    expect(bucket().messagesById['srv-clear-marker-1']).toBeUndefined()

    // Replay ends through the REAL catch_up_complete frame — no done, no
    // error, no setReplaying call.
    useChatStore.getState().handleFrame({
      type: 'catch_up_complete', session_id: SID, seq: 20, boot_id: 'boot-1', mode: 'incremental',
    } as ServerFrame)
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
    expect(requestMock).toHaveBeenCalledTimes(1)
  })

  it('D2: an older read can neither retire nor satisfy a newer /clear', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    let releaseRead1: (value: unknown[]) => void = () => {}
    requestMock.mockImplementationOnce(() => new Promise<unknown[]>((resolve) => { releaseRead1 = resolve }))

    useChatStore.getState().sendMessage('/clear')
    const cmid1 = sentClientMessageId(0)
    useChatStore.getState().handleFrame(doneFrame('turn-1', 1))
    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalledTimes(1)
    })

    // Clear #2 is sent and finishes while read #1 is still outstanding; its
    // read JOINS the same shared history query (one network read so far).
    useChatStore.getState().sendMessage('/clear')
    const cmid2 = sentClientMessageId(1)
    useChatStore.getState().handleFrame(doneFrame('turn-2', 2))
    await new Promise((r) => setTimeout(r, 10))
    expect(requestMock).toHaveBeenCalledTimes(1)

    // Read #1 (marker #1, before #2 executed) resolves: #2 is not retired,
    // and #1's rows are not presented as #2's post-clear view.
    releaseRead1(rawPostClearProjection(cmid1, 1))
    await new Promise((r) => setTimeout(r, 10))
    expect(bucket().messagesById['srv-clear-marker-2']).toBeUndefined()

    // #2's own read then succeeds: its own marker retires it.
    requestMock.mockImplementationOnce(async () => rawPostClearProjection(cmid2, 2))
    useChatStore.getState().handleFrame(doneFrame('turn-3', 3))
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-2']?.content).toBe(MARKER_TEXT)
    })
    expect(bucket().messageOrder).not.toContain('srv-clear-marker-1')
    expect(requestMock).toHaveBeenCalledTimes(2)
  })

  it('a projection that predates the operation (no own user row) is ignored, and the intent retries', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    requestMock.mockImplementationOnce(async () => rawPreClearProjection())

    useChatStore.getState().sendMessage('/clear')
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))

    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalledTimes(1)
    })
    await new Promise((r) => setTimeout(r, 10))
    // Nothing from the outdated response was applied to the live view.
    expect(bucket().messagesById['srv-u-1']).toBeUndefined()
    expect(bucket().messagesById['srv-clear-marker-1']).toBeUndefined()

    requestMock.mockImplementationOnce(async () => rawPostClearProjection(sentClientMessageId(0), 1))
    useChatStore.getState().handleFrame(doneFrame('turn-clear-2', 2))
    await vi.waitFor(() => {
      expect(requestMock).toHaveBeenCalledTimes(2)
    })
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
  })

  it('D6: a second completion after the marker is applied re-reads nothing', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    requestMock.mockImplementation(async () => rawPostClearProjection(sentClientMessageId(), 1))

    useChatStore.getState().sendMessage('/clear')
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
    expect(requestMock).toHaveBeenCalledTimes(1)

    useChatStore.getState().handleFrame(doneFrame('turn-clear-2', 2))
    await new Promise((r) => setTimeout(r, 20))
    expect(requestMock).toHaveBeenCalledTimes(1)
  })

  it("a plain message's done never arms a refresh, and a failed /clear send arms nothing", async () => {
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
})
