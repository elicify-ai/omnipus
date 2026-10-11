// clear-row-settle.test.ts — the /clear row's EXIT on every settle outcome
// (delta re-review findings C1, C6, C12; R1#1 SPA side).
//
// Oracles:
//   C1  (R3-CONSOLIDATED) — after a refusal ("/clear was not performed …"),
//       a discard (message_status discarded), a failed delivery or the
//       expiry bound, the row must resolve to a non-pending state with
//       honest copy — never the offline "Not sent yet, will be sent
//       automatically" text. Founder rulings stay untouched: the row shows
//       QUEUED at once, `received`/`working` never flip it midway, and a
//       confirmed clear still replaces the screen with the fresh view.
//   C6  (R3-CONSOLIDATED) — a failed post-clear re-read is visible through
//       the EXISTING failed-history-read state (the shared ['messages', sid]
//       query error the screen renders with Retry — clear-refetch.ts's own
//       V3 claim), and the intent survives so the screen's Retry completes
//       the refresh.
//   C12 (R3-CONSOLIDATED) — mergeServerProjection must keep local
//       unconfirmed rows even when the /clear row cannot be found in the
//       bucket.
//   R1#1 (R1-CONSOLIDATED, HIGH, SPA side) — a /clear the server never
//       confirms nor refuses (the lost-clear case) resolves to "not
//       confirmed" after a bounded wait, with no screen change.
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
import { pendingClearRefetches, clearConfirmTimers } from './runtime-state'
import { CLEAR_CONFIRM_TIMEOUT_MS, MAX_UNRESOLVED_READS } from './clear-refetch'
import { useUiStore } from '@/store/ui'
import type { ServerFrame } from '@/lib/ws'
import type { ChatMessage } from './types'

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
// pkg/agent/clear_while_busy.go::clearNotPerformedReply — the refusal the
// server publishes when the running turn could not be stopped.
const NOT_PERFORMED_REPLY = '/clear was not performed: the turn could not be stopped. Nothing was cleared; send /clear again.'

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
function rawPostClearProjection(n: number): Record<string, unknown>[] {
  const t = (s: number) => `2026-10-11T00:00:0${s}Z`
  return [
    rawSystem(`srv-clear-marker-${n}`, MARKER_TEXT, t(0)),
    rawAssistant(`srv-a-reply-${n}`, SUCCESS_REPLY, t(1)),
  ]
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
  delete clearConfirmTimers[SID]
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
  delete clearConfirmTimers[SID]
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

  it('a refused /clear — the "/clear was not performed" reply — resolves the row to not-performed and retires the operation', async () => {
    seedBucket(
      // The live turn's bubble carries its turn id — its own tokens stamped
      // it (frames.ts resolveTokenBubbleByMessageId), as in production.
      [localMessage({ id: 'a-live', role: 'assistant', content: '', status: 'streaming', isStreaming: true, turnId: 'turn-A' })],
      { isStreaming: true, activeTurnId: 'turn-A' },
    )

    useChatStore.getState().sendMessage('/clear')
    const cmid = sentClientMessageId(0)

    // The stopped turn settles first.
    useChatStore.getState().handleFrame(doneFrame('turn-A', 1))
    await new Promise((r) => setTimeout(r, 10))
    expect(requestMock).not.toHaveBeenCalled()

    // The refusal is published as its own token+done pair — and the done can
    // carry the STOPPED turn's id (pkg/gateway/webchat_channel.go::Send
    // reuses the recorded turn identity), exactly as in production.
    useChatStore.getState().handleFrame({
      type: 'token', session_id: SID, content: NOT_PERFORMED_REPLY, message_id: 'msg-refusal', turn_id: 'turn-A', agent_id: 'jim', seq: 2,
    } as ServerFrame)
    useChatStore.getState().handleFrame({
      type: 'done', session_id: SID, message_id: 'msg-refusal', turn_id: 'turn-A', seq: 3, stats: { tokens: 12, cost: 0 },
    } as ServerFrame)

    const row = bucket().messagesById[clearRowId(cmid)]
    expect(row?.deliveryStatus).toBe('not-performed')
    // Nothing was cleared — the transcript re-read would find no marker and
    // spin to a "did not confirm" toast that contradicts the reply on screen.
    expect(pendingClearRefetches[SID]).toBeUndefined()
    expect(requestMock).not.toHaveBeenCalled()
    // The refusal reply itself stays visible, and no expiry toast ever fires.
    expect(bucket().messageOrder.some((id) => bucket().messagesById[id]?.content === NOT_PERFORMED_REPLY)).toBe(true)
    useChatStore.getState().handleFrame(doneFrame('turn-later', 4))
    await new Promise((r) => setTimeout(r, 20))
    expect(toasts).toEqual([])
  })

  it('a server-reported FAILED /clear still marks the row for Retry (existing path, pinned)', async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    useChatStore.getState().sendMessage('/clear')
    const cmid = sentClientMessageId(0)

    useChatStore.getState().handleFrame(statusFrame(cmid, 'failed', 1))
    const row = bucket().messagesById[clearRowId(cmid)]
    expect(row?.deliveryStatus).toBe('failed')
    expect(row?.status).toBe('error')
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

  it(`a projection that cannot settle still expires at the read bound and resolves the row (max ${MAX_UNRESOLVED_READS} reads)`, async () => {
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    // The server kept the /clear row and replied, but no marker — the read
    // can never settle this operation.
    requestMock.mockImplementation(async () => [
      rawUser('srv-u-refused', sentClientMessageId(0), '/clear', '2026-10-11T00:00:00Z'),
      rawAssistant('srv-a-refused', 'The clear was refused.', '2026-10-11T00:00:01Z'),
    ])
    useChatStore.getState().sendMessage('/clear')
    const cmid = sentClientMessageId(0)

    for (let turn = 1; turn <= MAX_UNRESOLVED_READS; turn++) {
      expect(pendingClearRefetches[SID], `still armed before read ${turn}`).toBeTruthy()
      useChatStore.getState().handleFrame(doneFrame(`turn-refused-${turn}`, turn))
      await vi.waitFor(() => expect(requestMock).toHaveBeenCalledTimes(turn))
      await new Promise((r) => setTimeout(r, 15))
    }
    expect(pendingClearRefetches[SID]).toBeUndefined()
    expect(toasts.some((t) => /did not confirm your \/clear/i.test(t))).toBe(true)
    // R1#1's row half at the read bound: the row is no longer pending.
    expect(bucket().messagesById[clearRowId(cmid)]?.deliveryStatus).toBe('unconfirmed')
  })
})

describe('R1#1 — a /clear the server never confirms nor refuses resolves after a bounded wait', () => {
  it('resolves the row to unconfirmed with no screen change, tells the user once, and leaves nothing armed', () => {
    vi.useFakeTimers()
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    useChatStore.getState().sendMessage('/clear')
    const cmid = sentClientMessageId(0)
    expect(bucket().messagesById[cmid]).toMatchObject({ deliveryStatus: 'queued' })

    // Well inside the bound: still queued, nothing invented.
    vi.advanceTimersByTime(30_000)
    expect(bucket().messagesById[cmid]?.deliveryStatus).toBe('queued')
    expect(pendingClearRefetches[SID]).toBeTruthy()

    // Past the bound: the row resolves — "not confirmed — send /clear again"
    // — with NO screen change (no fabricated marker, no view wipe).
    vi.advanceTimersByTime(60_000)
    expect(bucket().messagesById[cmid]?.deliveryStatus).toBe('unconfirmed')
    expect(bucket().messagesById[cmid]?.status).toBe('done')
    expect(pendingClearRefetches[SID]).toBeUndefined()
    expect(toasts.some((t) => /did not confirm your \/clear/i.test(t))).toBe(true)
    const b = bucket()
    expect(b.messageOrder.filter((id) => b.messagesById[id]?.content === MARKER_TEXT)).toHaveLength(0)
    expect(b.messageOrder).toContain('u-1')

    // And nothing reads afterwards: a later turn end re-reads nothing.
    const reads = requestMock.mock.calls.length
    useChatStore.getState().handleFrame(doneFrame('turn-much-later', 1))
    expect(requestMock.mock.calls.length).toBe(reads)
  })

  it('a /clear the bound would fire on resolves only its OWN row — a newer /clear keeps its own operation', () => {
    vi.useFakeTimers()
    seedBucket([localMessage({ id: 'u-1', role: 'user', content: 'earlier question' })])
    useChatStore.getState().sendMessage('/clear')
    const cmid1 = sentClientMessageId(0)
    // The first /clear's fresh view lands: the operation retires normally.
    requestMock.mockImplementation(async () => rawPostClearProjection(1))
    useChatStore.getState().handleFrame(doneFrame('turn-clear-1', 1))
    // (The read is async under fake timers; retire it synchronously instead.)
    expect(pendingClearRefetches[SID]).toBeTruthy()

    // Time passes, but NOT yet past the first operation's own bound.
    vi.advanceTimersByTime(CLEAR_CONFIRM_TIMEOUT_MS / 2)

    // A second /clear is sent while the first read is outstanding: it REPLACES
    // the pending operation, so its bound runs a full window from NOW. Both
    // operations are deliberately NOT at the same fake time — that is what makes
    // a stale timer distinguishable from the live one.
    useChatStore.getState().sendMessage('/clear')
    const cmid2 = sentClientMessageId(1)
    expect(pendingClearRefetches[SID]?.clientMessageId).toBe(cmid2)

    // Past the FIRST operation's bound, well short of the second's. A stale
    // timer that survived the replacement would resolve cmid2 here.
    vi.advanceTimersByTime(CLEAR_CONFIRM_TIMEOUT_MS / 2 + 10_000)
    expect(bucket().messagesById[cmid2]?.deliveryStatus).not.toBe('unconfirmed')
    expect(pendingClearRefetches[SID]?.clientMessageId).toBe(cmid2)

    // Only the SECOND operation's own bound resolves its row.
    vi.advanceTimersByTime(CLEAR_CONFIRM_TIMEOUT_MS)
    expect(bucket().messagesById[cmid2]?.deliveryStatus).toBe('unconfirmed')
    expect(pendingClearRefetches[SID]).toBeUndefined()
    expect(cmid1).not.toBe(cmid2)
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
    queryClient.setQueryData(['messages', SID], [
      rawUser('srv-u-loaded', undefined, 'earlier question', '2026-10-10T23:00:00Z'),
    ])
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
