// clear-row-settle.test.ts — the /clear row's EXIT on every settle outcome
// (delta re-review findings C1, C6, C12; architect ruling S3-02 —
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-investigations/
// combined-review/ARCHITECT-RULINGS.md, "S3-02 … three signals and no timer").
//
// Oracles:
//   Wire-signal contract (ruling S3-02) — the wire is the source of truth,
//       three signals, NO timer:
//     1. Refused / not performed: the server publishes message_status
//        {state: 'failed'} for the /clear's client_message_id (any refusal
//        path — busy-turn failure, helper-session refusal, or a plain
//        failure). The SPA never parses the assistant text; the row shows
//        the failed state honestly and the refresh intent retires. A
//        journaled failed replayed by reconnect catch-up resolves it the
//        same way.
//     2. Done: the clear-marker row stays the success signal (pinned in
//        clear-transcript-refetch.test.ts).
//     3. Held (stopped agent, busy-turn wait): NO terminal signal — the row
//        stays queued with NO deadline. Nothing may time it out, resolve it
//        or toast about it.
//   C1  (R3-CONSOLIDATED) — a discard (message_status discarded) or a
//       failed delivery resolves the row to a non-pending state with honest
//       copy — never the offline "Not sent yet, will be sent automatically"
//       text. Founder rulings stay untouched: the row shows QUEUED at once,
//       `received`/`working` never flip it midway, and a confirmed clear
//       still replaces the screen with the fresh view.
//   Ownership (S3-11's kept semantics) — an OLDER operation's settle never
//       touches a newer /clear's own intent.
//   C6  (R3-CONSOLIDATED) — a failed post-clear re-read is visible through
//       the EXISTING failed-history-read state (the shared ['messages', sid]
//       query error the screen renders with Retry — clear-refetch.ts's own
//       V3 claim), and the intent survives so the screen's Retry completes
//       the refresh.
//   C12 (R3-CONSOLIDATED) — mergeServerProjection must keep local
//       unconfirmed rows even when the /clear row cannot be found in the
//       bucket.
//   Retired with the ruling: the 90 s bounded wait (CLEAR_CONFIRM_TIMEOUT_MS),
//       the "unconfirmed" row state and its false "did not confirm" toast,
//       and the "/clear was not performed" text-prefix refusal parse — the
//       timer-based and text-based oracles below were deleted with them
//       (the steer authorises retiring the tests that pinned them).
//
// What is real: the chat store's send/frame paths and the real React Query
// client. What is stubbed: the HTTP edge ('@/lib/api/http' request) and the
// WS connection.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { queryClient } from '@/lib/queryClient'
import { ApiError } from '@/lib/api-error'
import { pendingClearRefetches } from './runtime-state'
import { useUiStore } from '@/store/ui'
import type { ServerFrame } from '@/lib/ws'
import type { ChatMessage } from './types'
import { clearHistoryPage, wireHistoryPage, type FixtureHistoryPage } from '../../../tests/chat-history-paging/legacy-wire'
import { narrationHistoryPage } from '../../../tests/chat-history-paging/legacy-fixtures'

const requestMock = vi.hoisted(() => vi.fn())

vi.mock('@/lib/api/http', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api/http')>()
  return {
    ...actual,
    request: requestMock,
  }
})

const SID = 'sess-clear-settle'
const MARKER_TEXT = 'Conversation context cleared'
const SUCCESS_REPLY = 'Context cleared. The conversation and its history are kept; the assistant continues from here with a fresh context.'

function rawUser(id: string, clientMessageId: string | undefined, content: string, ts: string): Record<string, unknown> {
  return { id, role: 'user', content, timestamp: ts, agent_id: 'jim', status: 'ok', ...(clientMessageId ? { client_message_id: clientMessageId } : {}) }
}
function rawAssistant(id: string, content: string, ts: string): Record<string, unknown> {
  return { id, role: 'assistant', content, timestamp: ts, agent_id: 'jim', status: 'ok' }
}
function rawSystem(id: string, content: string, ts: string): Record<string, unknown> {
  return { id, role: 'system', content, timestamp: ts, agent_id: 'jim', status: 'ok' }
}

/** The server's post-clear view: the marker first, then the reply. */
function rawPostClearProjection(n: number): FixtureHistoryPage {
  const t = (s: number) => `2026-10-11T00:00:0${s}Z`
  return clearHistoryPage(SID, [
    rawSystem(`srv-clear-marker-${n}`, MARKER_TEXT, t(0)),
    rawAssistant(`srv-a-reply-${n}`, SUCCESS_REPLY, t(1)),
  ], 2)
}

function localMessage(partial: Partial<ChatMessage> & Pick<ChatMessage, 'id' | 'role' | 'content'>): ChatMessage {
  return { timestamp: new Date().toISOString(), status: 'done', ...partial } as ChatMessage
}

function doneFrame(turnId: string | undefined, seq: number): ServerFrame {
  return {
    type: 'done', session_id: SID, message_id: 'a-reply', turn_id: turnId, seq, stats: { tokens: 1, cost: 0 },
  } as ServerFrame
}

function statusFrame(cmid: string, state: 'received' | 'working' | 'failed' | 'discarded', seq: number): ServerFrame {
  return {
    type: 'message_status', session_id: SID, client_message_id: cmid, state,
    ...(state === 'discarded' ? { reason: 'stopped_before_delivery' as const } : {}),
    seq,
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

function sentClientMessageId(index = 0): string {
  const cmid = (sentFrames[index] as { client_message_id?: string } | undefined)?.client_message_id
  expect(cmid, 'the send carried a client_message_id').toBeTruthy()
  return cmid!
}

function clearRowId(cmid: string): string {
  const b = bucket()
  const id = b.messageOrder.find((id) => {
    const m = b.messagesById[id]
    return m.role === 'user' && (m.clientMessageId ?? m.id) === cmid
  })
  expect(id, 'the /clear row is in the bucket').toBeTruthy()
  return id!
}

let sentFrames: unknown[] = []
let connectionOk: boolean
let toasts: string[]

beforeEach(() => {
  sentFrames = []
  connectionOk = true
  toasts = []
  requestMock.mockReset()
  queryClient.removeQueries()
  delete pendingClearRefetches[SID]
  vi.spyOn(useUiStore.getState(), 'addToast').mockImplementation(((t: { message: string }) => { toasts.push(t.message) }) as never)
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'jim', activeAgentType: null })
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
  useConnectionStore.setState({
    connection: { send: (p: unknown) => { sentFrames.push(p); return connectionOk }, close: () => {} } as never,
    isConnected: true,
  } as never)
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  delete pendingClearRefetches[SID]
})

describe('C1 — the /clear row exits on every settle outcome', () => {
  it('a discarded /clear (stop before delivery) resolves the row — never queued forever — and retires the operation', async () => {
    seedBucket(
      [localMessage({ id: 'a-live', role: 'assistant', content: '', status: 'streaming', isStreaming: true, turnId: 'turn-A' })],
      { isStreaming: true, activeTurnId: 'turn-A' },
    )

    useChatStore.getState().sendMessage('/clear')
    const cmid = sentClientMessageId(0)
    expect(bucket().messagesById[cmid]).toMatchObject({ deliveryStatus: 'queued' })
    expect(pendingClearRefetches[SID]).toBeTruthy()

    useChatStore.getState().handleFrame(statusFrame(cmid, 'discarded', 1))
    const row = bucket().messagesById[clearRowId(cmid)]
    // The exit: a non-pending, honest state — not 'queued', with no Retry
    // affordance (a discarded clear has nothing to resend; the text stays).
    expect(row?.deliveryStatus).toBe('discarded')
    expect(row?.status).toBe('done')
    // The stopped turn is untouched: its live bubble keeps streaming.
    expect(bucket().isStreaming).toBe(true)
    // A discarded /clear never writes a marker — the transcript re-read it
    // armed would only spin to expiry and then claim "not confirmed" after
    // the server already answered with the discard. Nothing left armed.
    expect(pendingClearRefetches[SID]).toBeUndefined()

    // And a later turn end therefore reads nothing.
    useChatStore.getState().handleFrame(doneFrame('turn-A', 2))
    await new Promise((r) => setTimeout(r, 20))
    expect(requestMock).not.toHaveBeenCalled()
  })

  it('a refused /clear — the server message_status failed — shows the failed row and retires the operation (ruling signal 1)', async () => {
    seedBucket(
      // The live turn's bubble carries its turn id — its own tokens stamped
      // it (frames.ts resolveTokenBubbleByMessageId), as in production.
      [localMessage({ id: 'a-live', role: 'assistant', content: '', status: 'streaming', isStreaming: true, turnId: 'turn-A' })],
      { isStreaming: true, activeTurnId: 'turn-A' },
    )

    useChatStore.getState().sendMessage('/clear')
    const cmid = sentClientMessageId(0)
    expect(pendingClearRefetches[SID]).toBeTruthy()

    // The refusal signal is the wire state itself — the explanatory text may
    // ride along as an ordinary assistant reply, but the SPA keys on the
    // failed message_status for the /clear's client_message_id.
    useChatStore.getState().handleFrame(statusFrame(cmid, 'failed', 1))

    const row = bucket().messagesById[clearRowId(cmid)]
    // The row shows the failed state honestly — never a queued look, never
    // an invented "not performed"/"not confirmed" state.
    expect(row?.deliveryStatus).toBe('failed')
    expect(row?.status).toBe('error')
    // The intent retires with the refusal: nothing armed, and a later turn
    // end therefore reads nothing and toasts nothing.
    expect(pendingClearRefetches[SID]).toBeUndefined()
    expect(requestMock).not.toHaveBeenCalled()
    useChatStore.getState().handleFrame(doneFrame('turn-later', 2))
    await new Promise((r) => setTimeout(r, 20))
    expect(requestMock).not.toHaveBeenCalled()
    expect(toasts).toEqual([])

    // The honest repair is Retry: resending the failed row re-arms a FRESH
    // operation for the new send.
    useChatStore.getState().resendMessage(clearRowId(cmid))
    expect(sentFrames).toHaveLength(2)
    expect(pendingClearRefetches[SID]?.clientMessageId).toBe(sentClientMessageId(1))
  })

  it('a refused /clear whose reply text matches NO known prefix still retires on the failed status (N11 — no text parsing)', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    useChatStore.getState().sendMessage('/clear')
    const cmid = sentClientMessageId(0)

    // Helper-session refusals (N11) word their reply differently; under the
    // ruling the wire state alone decides, whatever the text says.
    useChatStore.getState().handleFrame({
      type: 'token', session_id: SID, content: '/clear is not available in helper chats.', message_id: 'msg-refusal', turn_id: 'turn-ref', agent_id: 'jim', seq: 1,
    } as ServerFrame)
    useChatStore.getState().handleFrame(doneFrame('turn-ref', 2))
    await new Promise((r) => setTimeout(r, 10))
    useChatStore.getState().handleFrame(statusFrame(cmid, 'failed', 3))

    expect(bucket().messagesById[clearRowId(cmid)]?.deliveryStatus).toBe('failed')
    expect(pendingClearRefetches[SID]).toBeUndefined()
  })

  it('an OLDER /clear operation\'s settle never touches a NEWER /clear\'s intent (S3-11 ownership, pinned)', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    useChatStore.getState().sendMessage('/clear')
    const cmid1 = sentClientMessageId(0)
    expect(pendingClearRefetches[SID]?.clientMessageId).toBe(cmid1)

    // A second /clear replaces the first operation outright.
    useChatStore.getState().sendMessage('/clear')
    const cmid2 = sentClientMessageId(1)
    expect(pendingClearRefetches[SID]?.clientMessageId).toBe(cmid2)

    // The FIRST send's failed status arrives late (a straggler, or the
    // journaled replay of an earlier attempt): it retires only an operation
    // that still owns the slot — never the newer one.
    useChatStore.getState().handleFrame(statusFrame(cmid1, 'failed', 1))
    expect(pendingClearRefetches[SID]?.clientMessageId).toBe(cmid2)
    expect(bucket().messagesById[clearRowId(cmid2)]?.deliveryStatus).toBe('queued')

    // And the newer operation's own failed status still retires it.
    useChatStore.getState().handleFrame(statusFrame(cmid2, 'failed', 2))
    expect(pendingClearRefetches[SID]).toBeUndefined()
  })

  it('founder ruling: received/working still never flip the queued look midway (guard against the discard change)', () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    useChatStore.getState().sendMessage('/clear')
    const cmid = sentClientMessageId(0)

    useChatStore.getState().handleFrame(statusFrame(cmid, 'received', 1))
    expect(bucket().messagesById[cmid]?.deliveryStatus).toBe('queued')
    useChatStore.getState().handleFrame(statusFrame(cmid, 'working', 2))
    expect(bucket().messagesById[cmid]?.deliveryStatus).toBe('queued')
    expect(pendingClearRefetches[SID]).toBeTruthy()
  })

  it('a projection that cannot settle yet keeps the intent armed — reads continue on later turn ends (no read bound)', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    // The server kept the /clear row and replied, but no marker — the read
    // cannot settle this operation yet. Under the ruling there is NO bound:
    // the row stays queued and every later turn end re-reads, until a wire
    // signal (marker or failed) resolves the operation.
    requestMock.mockImplementation(async () => wireHistoryPage(SID, [
      rawUser('srv-u-refused', sentClientMessageId(0), '/clear', '2026-10-11T00:00:00Z'),
      rawAssistant('srv-a-refused', 'The clear was refused.', '2026-10-11T00:00:01Z'),
    ], 2))
    useChatStore.getState().sendMessage('/clear')
    const cmid = sentClientMessageId(0)

    for (let turn = 1; turn <= 3; turn++) {
      expect(pendingClearRefetches[SID], `still armed before read ${turn}`).toBeTruthy()
      useChatStore.getState().handleFrame(doneFrame(`turn-refused-${turn}`, turn))
      await vi.waitFor(() => expect(requestMock).toHaveBeenCalledTimes(turn))
      await new Promise((r) => setTimeout(r, 15))
    }
    // Still armed, still queued, and NEVER a false "did not confirm" toast.
    expect(pendingClearRefetches[SID]).toBeTruthy()
    expect(bucket().messagesById[clearRowId(cmid)]?.deliveryStatus).toBe('queued')
    expect(toasts).toEqual([])
  })
})

describe('ruling signal 3 — a HELD clear stays queued indefinitely, with no toast', () => {
  it('a /clear the server neither confirms nor refuses stays queued past any deadline — no timer, no toast, no read', () => {
    vi.useFakeTimers()
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    useChatStore.getState().sendMessage('/clear')
    const cmid = sentClientMessageId(0)
    expect(bucket().messagesById[cmid]).toMatchObject({ deliveryStatus: 'queued' })

    // Far past the retired 90 s bound: the server legitimately holds the
    // clear (stopped agent, busy-turn wait). The queued look is a founder
    // ruling; nothing may time it out.
    vi.advanceTimersByTime(10 * 60_000)
    expect(bucket().messagesById[cmid]?.deliveryStatus).toBe('queued')
    expect(pendingClearRefetches[SID], 'the intent stays armed for when the clear executes').toBeTruthy()
    expect(toasts).toEqual([])
    // No screen change was invented either.
    const b = bucket()
    expect(b.messageOrder.filter((id) => b.messagesById[id]?.content === MARKER_TEXT)).toHaveLength(0)
    expect(b.messageOrder).toContain('u-1')
    // And with no turn ended, nothing read.
    expect(requestMock).not.toHaveBeenCalled()
  })
})

describe('reconnect catch-up — a journaled failed resolves the intent (ruling signal 1)', () => {
  it('the journaled message_status failed, replayed after a reconnect, retires the armed intent', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    useChatStore.getState().sendMessage('/clear')
    const cmid = sentClientMessageId(0)
    expect(pendingClearRefetches[SID]).toBeTruthy()

    // The connection dropped before any signal arrived; on reconnect the
    // journaled failed is replayed through the same frame reducer (the seq
    // advances past what the bucket had seen).
    useChatStore.getState().handleFrame(statusFrame(cmid, 'failed', 7))

    expect(bucket().messagesById[clearRowId(cmid)]?.deliveryStatus).toBe('failed')
    expect(pendingClearRefetches[SID]).toBeUndefined()
    expect(toasts).toEqual([])
    // A later turn end reads nothing — the operation is settled.
    useChatStore.getState().handleFrame(doneFrame('turn-after-reconnect', 8))
    await new Promise((r) => setTimeout(r, 20))
    expect(requestMock).not.toHaveBeenCalled()
  })
})

describe('C12 — the merge keeps local unconfirmed rows even when the /clear row cannot be found', () => {
  it('a marker projection whose /clear row is missing from the bucket still keeps the unconfirmed tail', async () => {
    seedBucket([
      localMessage({ id: 'failed-local', role: 'user', content: 'failed earlier question', status: 'error', deliveryStatus: 'failed' }),
      localMessage({ id: 'queued-local', role: 'user', content: 'still queued question', deliveryStatus: 'queued' }),
    ])
    requestMock.mockImplementation(async () => rawPostClearProjection(1))
    useChatStore.getState().sendMessage('/clear')
    const cmid = sentClientMessageId(0)

    // SFH found no concrete path that loses the /clear row between the send
    // and the projection (C12: "no concrete path found"); force the loss so
    // the merge's missing-row path is pinned safe by construction rather
    // than by hope — the bucket is rebuilt without the /clear row and its
    // optimistic placeholder, keeping the unconfirmed rows.
    useChatStore.setState((s) => {
      const b = s.sessionsById[SID]
      const order = b.messageOrder.filter((id) => {
        const m = b.messagesById[id]
        if (!m) return false
        if (m.role === 'user' && (m.clientMessageId ?? m.id) === cmid) return false
        if (m.role === 'assistant') return false
        return true
      })
      const byId = Object.fromEntries(order.map((id) => [id, b.messagesById[id]]))
      return { ...s, sessionsById: { ...s.sessionsById, [SID]: { ...b, messagesById: byId, messageOrder: order } } } as never
    })
    expect(bucket().messageOrder.some((id) => (bucket().messagesById[id]?.clientMessageId ?? bucket().messagesById[id]?.id) === cmid)).toBe(false)

    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
    const b = bucket()
    // The unconfirmed tail survives the merge — dropping it would silently
    // erase a failed row (and its Retry) the user can still see above.
    expect(b.messageOrder).toContain('failed-local')
    expect(b.messagesById['failed-local']).toMatchObject({ status: 'error', deliveryStatus: 'failed' })
    expect(b.messageOrder).toContain('queued-local')
    expect(b.messagesById['queued-local']).toMatchObject({ deliveryStatus: 'queued' })
    // …and they sit AFTER the server's fresh view (the live tail).
    expect(b.messageOrder.indexOf('failed-local')).toBeGreaterThan(b.messageOrder.indexOf('srv-clear-marker-1'))
  })
})

describe('C6 — a failed post-clear re-read is visible, with the screen Retry completing the refresh', () => {
  it('the shared history query enters its error state even with history already loaded, and the intent survives for the Retry', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    // Production shape: the session's history is ALREADY loaded (the attach
    // read populated the shared entry) when the /clear re-read fails. A
    // failed re-read of a data-bearing entry must still surface — not hide
    // behind the cached data.
    queryClient.setQueryData(['messages', SID], narrationHistoryPage(SID, [
      { id: 'srv-u-loaded', role: 'user', content: 'earlier question', timestamp: '2026-10-10T23:00:00Z', agentId: 'jim', status: 'done' },
    ]))
    requestMock.mockImplementation(async () => {
      throw new ApiError(404, 'session gone')
    })
    useChatStore.getState().sendMessage('/clear')
    useChatStore.getState().handleFrame(doneFrame('turn-clear', 1))

    await vi.waitFor(() => expect(requestMock).toHaveBeenCalled())
    // clear-refetch.ts V3's own claim, now pinned: the read runs on the
    // SHARED history key, so its failure IS the screen's history-error state
    // ("Could not load messages." with Retry) — not a log-only silence.
    await vi.waitFor(() => {
      expect(queryClient.getQueryState(['messages', SID])?.status).toBe('error')
    })
    // Nothing stale was applied, and the operation survives for the Retry.
    expect(bucket().messagesById['srv-clear-marker-1']).toBeUndefined()
    expect(pendingClearRefetches[SID], 'the intent survives a failed read').toBeTruthy()

    // The screen's Retry (retryClearTranscript — the exact branch the error
    // screen's Retry button takes while an operation is pending) settles it.
    requestMock.mockImplementation(async () => rawPostClearProjection(1))
    useChatStore.getState().retryClearTranscript(SID)
    await vi.waitFor(() => {
      expect(bucket().messagesById['srv-clear-marker-1']?.content).toBe(MARKER_TEXT)
    })
    expect(queryClient.getQueryState(['messages', SID])?.status).toBe('success')
    expect(pendingClearRefetches[SID]).toBeUndefined()
  })
})
