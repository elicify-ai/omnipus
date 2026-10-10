// clear-transcript-refetch.test.ts — FR-030/031 (U10b), R3 round 2: the
// transcript re-read after a /clear, rebuilt per review round 1 (C1–C5).
//
// Oracles: docs/internal/specs/session-core-spec.md FR-030 ("clears the chat
// UI and model context with a marker by moving/advancing the ... display
// window start", "preserves the transcript", pending input kept) and the
// saved server reference
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/
// joint-merge-20261009/core-clear-reference-67345b1d7/{cmd_clear,clear_session}.go
// (one chat-view marker entry "Conversation context cleared", never pushed
// live; the clear dispatches only at the start of the session's own turn).
//
// The rules under test:
//   C1  the refresh intent belongs to the /clear OPERATION, recorded on every
//       send path (normal, offline-queue drain, Retry/resend), bound to that
//       send so the turn that was running at send-time cannot consume it;
//   C2  the re-read is a read that REJECTS on failure (real QueryClient
//       fetchQuery; only the network function is stubbed here) — on failure
//       nothing stale is applied and the intent survives;
//   C3  the fetched projection is MERGED into the bucket — server rows are
//       authoritative for what the server has, while unsaved/failed/pending
//       user messages (and their Retry state) and newer client-only system
//       rows survive;
//   C4  a busy bucket (streaming/replaying) defers the application and
//       applies it once idle; the intent clears only after the post-clear
//       projection, marker included, is applied.
//
// What is real: the chat store's sendMessage/resend paths, the frame
// reducer, the real React Query client (fetchQuery/rejection semantics), and
// the real bucket state machine. What is stubbed: the network edge only —
// GET /sessions/{id}/messages (fetchSessionMessages) and the WS connection.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { queryClient } from '@/lib/queryClient'
import { ApiError } from '@/lib/api-error'
import type { ServerFrame } from '@/lib/ws'
import type { ChatMessage } from './types'

// The ONLY stub: the network edge (GET /sessions/{id}/messages). Hoisted so
// the factory and the test's handle share one vi.fn; the real React Query
// client above stays real, so fetchQuery's rejection semantics are genuine.
const fetchSessionMessagesMock = vi.hoisted(() => vi.fn<() => Promise<ChatMessage[]>>())

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSessionMessages: fetchSessionMessagesMock,
  }
})

const fetchMock = fetchSessionMessagesMock

const SID = 'sess-clear-refetch'
const MARKER_TEXT = 'Conversation context cleared'
const SUCCESS_REPLY = 'Context cleared. The conversation and its history are kept; the assistant continues from here with a fresh context.'

// A post-clear server projection: the /clear user row (carrying the send's
// client_message_id — the server echo supersedes the local optimistic
// bubble), the command reply and the ONE chat-view marker entry.
function postClearProjection(clearClientMessageId?: string): ChatMessage[] {
  return [
    { id: 'u-clear', role: 'user', content: '/clear', clientMessageId: clearClientMessageId, timestamp: '2026-10-10T00:00:00Z', agentId: 'jim', status: 'done' },
    { id: 'a-reply', role: 'assistant', content: SUCCESS_REPLY, timestamp: '2026-10-10T00:00:01Z', agentId: 'jim', status: 'done' },
    { id: 'clear-marker-1', role: 'system', content: MARKER_TEXT, timestamp: '2026-10-10T00:00:02Z', agentId: 'jim', status: 'done' },
  ]
}

function preClearProjection(): ChatMessage[] {
  // What the server returns BEFORE the clear has executed (no marker yet).
  return [
    { id: 'u-1', role: 'user', content: 'earlier question', timestamp: '2026-10-09T23:00:00Z', agentId: 'jim', status: 'done' },
    { id: 'a-1', role: 'assistant', content: 'earlier answer', timestamp: '2026-10-09T23:00:05Z', agentId: 'jim', status: 'done' },
  ]
}

function doneFrame(turnId: string | undefined, seq: number): ServerFrame {
  return {
    type: 'done', session_id: SID, message_id: 'a-reply', turn_id: turnId, seq, stats: { tokens: 1, cost: 0 },
  } as ServerFrame
}

function seedBucket(messages: ChatMessage[], opts: { isStreaming?: boolean; activeTurnId?: string | null } = {}): void {
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      ...s.sessionsById,
      [SID]: {
        ...(s.sessionsById ?? {})[SID],
        messagesById: Object.fromEntries(messages.map((m) => [m.id, m])),
        messageOrder: messages.map((m) => m.id),
        isStreaming: opts.isStreaming ?? false,
        isReplaying: false,
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
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
}

function bucket(): ReturnType<typeof useChatStore.getState>['sessionsById'][string] {
  const b = useChatStore.getState().sessionsById[SID]
  expect(b, 'bucket exists').toBeTruthy()
  return b!
}

let sentFrames: unknown[] = []
let connectionOk: boolean

/** The client_message_id of the Nth outgoing frame — what the server echoes back on its own row. */
function sentClientMessageId(index = 0): string {
  const cmid = (sentFrames[index] as { client_message_id?: string } | undefined)?.client_message_id
  expect(cmid, 'the send carried a client_message_id').toBeTruthy()
  return cmid!
}

beforeEach(() => {
  sentFrames = []
  connectionOk = true
  fetchMock.mockReset()
  queryClient.removeQueries()
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

describe('C1 — the refresh belongs to the /clear operation, not to any done', () => {
  it('an idle /clear arms the refresh; its reply turn done consumes it and the marker lands', async () => {
    seedBucket([preClearProjection()[0]])
    fetchMock.mockImplementation(async () => postClearProjection(sentClientMessageId()))

    useChatStore.getState().sendMessage('/clear')
    expect(sentFrames).toHaveLength(1)
    expect(sentFrames[0]).toMatchObject({ type: 'message', content: '/clear', session_id: SID })

    // The reply turn (a different, newly-started turn) completes.
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))

    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(SID)
    })
    await vi.waitFor(() => {
      const b = bucket()
      expect(b.messageOrder).toEqual(['u-clear', 'a-reply', 'clear-marker-1'])
      expect(b.messagesById['clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
  })

  it('a /clear sent mid-turn is NOT consumed by the running turn\'s own done — only by the reply turn that follows', async () => {
    seedBucket(
      [{ id: 'a-live', role: 'assistant', content: '', timestamp: new Date().toISOString(), status: 'streaming', isStreaming: true }],
      { isStreaming: true, activeTurnId: 'turn-A' },
    )
    fetchMock.mockImplementation(async () => postClearProjection(sentClientMessageId()))

    // /clear is queued mid-turn; the gateway dispatches it only after A ends.
    useChatStore.getState().sendMessage('/clear')
    expect(sentFrames).toHaveLength(1)

    // Turn A — the turn that was running at send-time — completes first.
    useChatStore.getState().handleFrame(doneFrame('turn-A', 1))
    await new Promise((r) => setTimeout(r, 10))
    expect(fetchMock, "A's own done must not consume /clear's refresh").not.toHaveBeenCalled()

    // The clear dispatches; its reply turn completes.
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 2))
    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1)
    })
    await vi.waitFor(() => {
      expect(bucket().messagesById['clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
  })

  it("a done with no turn id cannot be told apart, so it is treated as the reply's (consumed)", async () => {
    seedBucket([preClearProjection()[0]])
    fetchMock.mockImplementation(async () => postClearProjection(sentClientMessageId()))

    useChatStore.getState().sendMessage('/clear')
    useChatStore.getState().handleFrame(doneFrame(undefined, 1))

    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(SID)
    })
  })

  it("a plain message's done never starts the refresh", async () => {
    seedBucket([preClearProjection()[0]])
    useChatStore.getState().sendMessage('hello there')
    useChatStore.getState().handleFrame(doneFrame('turn-x', 1))
    await new Promise((r) => setTimeout(r, 10))
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it("a failed /clear send arms nothing — and its Retry (resend path) does", async () => {
    seedBucket([preClearProjection()[0]])
    fetchMock.mockImplementation(async () => postClearProjection(sentClientMessageId()))

    // Initial send fails on the wire: the bubble is kept with Retry, nothing armed.
    connectionOk = false
    useChatStore.getState().sendMessage('/clear')
    await new Promise((r) => setTimeout(r, 10))
    const failedId = bucket().messageOrder.find((id) => bucket().messagesById[id]?.content === '/clear')
    expect(failedId, 'the failed /clear bubble is kept').toBeTruthy()
    const failed = bucket().messagesById[failedId!]!
    expect(failed.role).toBe('user')
    expect(failed.status).toBe('error')
    useChatStore.getState().handleFrame(doneFrame('turn-z', 1))
    await new Promise((r) => setTimeout(r, 10))
    expect(fetchMock).not.toHaveBeenCalled()

    // Retry: the resend path sends the SAME message successfully and arms the refresh.
    connectionOk = true
    useChatStore.getState().resendMessage(failed.id)
    expect(sentFrames).toHaveLength(2)
    useChatStore.getState().handleFrame(doneFrame('turn-retry', 2))

    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1)
    })
    await vi.waitFor(() => {
      expect(bucket().messagesById['clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
  })

  it("'/clear extra text' is the same server command (first-token grammar) and arms the refresh", async () => {
    seedBucket([preClearProjection()[0]])
    fetchMock.mockImplementation(async () => postClearProjection(sentClientMessageId()))

    useChatStore.getState().sendMessage('/clear ignore trailing words')
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))

    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1)
    })
  })

  it('the offline-queue drain path arms the refresh too (a drained /clear re-reads)', async () => {
    // Offline: the /clear is buffered, not sent.
    useConnectionStore.setState({
      connection: { send: (p: unknown) => { sentFrames.push(p); return true }, close: () => {} } as never,
      isConnected: false,
    } as never)
    seedBucket([preClearProjection()[0]])
    fetchMock.mockImplementation(async () => postClearProjection(sentClientMessageId()))

    useChatStore.getState().sendMessage('/clear')
    expect(sentFrames).toHaveLength(0)

    // Connection returns; the queue drains on the next opportunity.
    useConnectionStore.setState({ isConnected: true } as never)
    useChatStore.getState().drainOutboundQueue()
    expect(sentFrames).toHaveLength(1)

    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))
    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1)
    })
  })
})

describe('C2 — the re-read is a rejecting fetch; failure keeps the intent and applies nothing', () => {
  it('a failed read applies nothing stale and survives for the next opportunity', async () => {
    seedBucket([preClearProjection()[0]])
    fetchMock.mockRejectedValueOnce(new ApiError(404, 'messages not found'))

    useChatStore.getState().sendMessage('/clear')
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))

    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1)
    })
    await new Promise((r) => setTimeout(r, 10))
    // Nothing stale was applied: the bucket still holds its own rows (plus
    // the local /clear bubble, which DID reach the wire and is not yet
    // superseded by any server row) — and no projected row, no marker.
    const b = bucket()
    expect(b.messageOrder).not.toContain('u-clear')
    expect(b.messageOrder).not.toContain('a-reply')
    expect(b.messagesById['clear-marker-1']).toBeUndefined()
    expect(b.messageOrder).toContain('u-1')

    // The intent survived: the next opportunity (a later done) retries the read.
    fetchMock.mockImplementationOnce(async () => postClearProjection(sentClientMessageId()))
    useChatStore.getState().handleFrame(doneFrame('turn-clear-2', 2))
    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(2)
    })
    await vi.waitFor(() => {
      expect(bucket().messagesById['clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
  })

  it('a projection without the marker (the clear has not executed yet) keeps the intent and retries', async () => {
    seedBucket([preClearProjection()[0]])
    fetchMock.mockResolvedValueOnce(preClearProjection())

    useChatStore.getState().sendMessage('/clear')
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))

    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1)
    })
    // Server projection applied honestly, but no marker yet → intent kept.
    // (The local /clear bubble stays too: no server row has superseded it.)
    await vi.waitFor(() => {
      const b = bucket()
      expect(b.messageOrder).toContain('u-1')
      expect(b.messageOrder).toContain('a-1')
      expect(b.messagesById['clear-marker-1']).toBeUndefined()
    })

    fetchMock.mockImplementationOnce(async () => postClearProjection(sentClientMessageId()))
    useChatStore.getState().handleFrame(doneFrame('turn-clear-2', 2))
    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(2)
    })
    await vi.waitFor(() => {
      expect(bucket().messagesById['clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
  })
})

describe('C3 — the projection is merged, never replacing newer local state', () => {
  it('a failed follow-up send (and its Retry state) survives a delayed projection', async () => {
    seedBucket([preClearProjection()[0]])
    let releaseFetch: (value: ChatMessage[]) => void = () => {}
    fetchMock.mockReturnValueOnce(new Promise<ChatMessage[]>((resolve) => { releaseFetch = resolve }))

    useChatStore.getState().sendMessage('/clear')
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))
    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalled()
    })

    // While the read is in flight, the user sends a follow-up whose send fails.
    connectionOk = false
    useChatStore.getState().sendMessage('follow-up that fails')
    await new Promise((r) => setTimeout(r, 10))
    const failedId = bucket().messageOrder.find((id) => bucket().messagesById[id]?.content === 'follow-up that fails')
    expect(failedId).toBeTruthy()
    expect(bucket().messagesById[failedId!]?.status).toBe('error')

    // The delayed projection lands — it cannot contain the failed message.
    releaseFetch(postClearProjection(sentClientMessageId(0)))
    await vi.waitFor(() => {
      expect(bucket().messagesById['clear-marker-1']?.content).toBe(MARKER_TEXT)
    })

    // The failed message and its Retry affordance survive the merge.
    const b = bucket()
    expect(b.messagesById[failedId!]?.content).toBe('follow-up that fails')
    expect(b.messagesById[failedId!]?.status).toBe('error')
    expect(b.messagesById[failedId!]?.deliveryStatus).toBe('failed')
    expect(b.messageOrder).toContain(failedId!)
    // ...and it sits after the server rows (it happened after the clear).
    expect(b.messageOrder.indexOf(failedId!)).toBeGreaterThan(b.messageOrder.indexOf('clear-marker-1'))
  })

  it('newer client-only system rows (a /new refusal) survive; rows the server dropped do not', async () => {
    seedBucket([
      // A pre-clear system row the server projection drops (superseded view).
      { id: 'old-server-sys', role: 'system', content: 'old server marker', timestamp: '2026-10-09T22:00:00Z', agentId: 'jim', status: 'done' },
      preClearProjection()[0],
    ])
    fetchMock.mockImplementation(async () => postClearProjection(sentClientMessageId()))

    useChatStore.getState().sendMessage('/clear')
    // A client-only refusal lands AFTER the /clear was sent (armed time).
    useChatStore.getState().appendMessage({
      id: 'local-refusal-1', role: 'system', content: '/new no longer exists.', timestamp: new Date().toISOString(), status: 'done',
    })

    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))
    await vi.waitFor(() => {
      expect(bucket().messagesById['clear-marker-1']?.content).toBe(MARKER_TEXT)
    })

    const b = bucket()
    expect(b.messageOrder).toContain('local-refusal-1')
    expect(b.messagesById['local-refusal-1']?.content).toBe('/new no longer exists.')
    expect(b.messageOrder).not.toContain('old-server-sys')
  })
})

describe('C4 — a busy bucket defers the application and never abandons it', () => {
  it('the read landing mid-turn applies when the bucket goes idle (marker appears after idle)', async () => {
    seedBucket([preClearProjection()[0]])
    fetchMock.mockImplementation(async () => postClearProjection(sentClientMessageId()))

    useChatStore.getState().sendMessage('/clear')
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))
    await vi.waitFor(() => {
      expect(fetchMock).toHaveBeenCalled()
    })

    // A follow-up turn starts before the read resolves.
    seedBucket(
      [
        ...bucket().messageOrder.map((id) => bucket().messagesById[id]!),
        { id: 'a-live', role: 'assistant', content: '', timestamp: new Date().toISOString(), status: 'streaming', isStreaming: true },
      ],
      { isStreaming: true, activeTurnId: 'turn-followup' },
    )
    await new Promise((r) => setTimeout(r, 10))
    expect(bucket().messagesById['clear-marker-1']).toBeUndefined()

    // The follow-up turn ends; the held projection is applied then.
    useChatStore.getState().handleFrame(doneFrame('turn-followup', 2))
    await vi.waitFor(() => {
      expect(bucket().messagesById['clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
  })
})
