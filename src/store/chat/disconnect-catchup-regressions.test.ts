// disconnect-catchup-regressions.test.ts — two real-browser regressions found
// by the orchestrator running the combined branch (feat/823-seq-redo +
// squad/be-lane-c-spa) against a real embedded binary with a scripted model
// streaming tokens "t001".."t060". Both reproduce the EXACT captured frame
// order from that report; both are regressions vs the pre-#823 release
// build.

import { beforeEach, describe, expect, it } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import type { WsReceiveFrame } from '@/lib/ws'

const SID = 'sess-regress'
const MSG_ID = 'ad866ee6'
const TURN_ID = 'turn-regress'

function token(seq: number, content: string, extra: Record<string, unknown> = {}): WsReceiveFrame {
  return {
    type: 'token', session_id: SID, content, message_id: MSG_ID, turn_id: TURN_ID, agent_id: 'mia', seq, ...extra,
  } as WsReceiveFrame
}

function assistantBubbles() {
  const b = useChatStore.getState().sessionsById[SID]
  if (!b) return []
  return b.messageOrder.map((id) => b.messagesById[id]).filter((m) => m.role === 'assistant')
}

beforeEach(() => {
  useSessionStore.setState({ activeSessionId: SID })
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
  useConnectionStore.setState({ connection: null } as never)
})

// BUG 1 (critical): a hard disconnect mid-answer freezes the live bubble at
// whatever text arrived before the cut. Root cause: clearStreamingState()
// (fired on WS onDisconnected) marks the still-streaming bubble 'done', and
// the §4.2 overlap rule (added in pass 2, frames.ts::resolveTokenBubbleByMessageId)
// then silently ignores every catch-up/live token for that message_id,
// because it looks exactly like the F4 "already finalized" case. BE-DESIGN.md
// §6.3: "On disconnect ... stop closing bubbles". Exact captured order:
// tokens 325-332 (t001-t008) -> offline 20s -> attach_session{since_seq:332}
// -> session_state{active_turn} -> tokens 333-371 (t009-t040, SAME message_id)
// -> catch_up_complete{mode:incremental, seq:372} -> live t041-t060 ->
// done{message_id, seq:385}.
describe('BUG 1 — disconnect must not close the bubble (BE-DESIGN.md §6.3)', () => {
  it('a hard disconnect mid-stream, followed by incremental catch-up for the SAME message_id, resumes the SAME bubble with the full concatenated text', () => {
    // Pre-cut: t001..t008, seq 325..332.
    for (let i = 1; i <= 8; i++) {
      useChatStore.getState().handleFrame(token(324 + i, `t${String(i).padStart(3, '0')} `))
    }
    let bubbles = assistantBubbles()
    expect(bubbles).toHaveLength(1)
    expect(bubbles[0].content).toBe('t001 t002 t003 t004 t005 t006 t007 t008 ')

    // The socket drops — OmnipusRuntimeProvider.tsx's onDisconnected handler.
    useChatStore.getState().clearStreamingState()

    // 20s offline, then reconnect: session_state{active_turn} announces the
    // turn is STILL RUNNING server-side (ADR-082 D4/D5) — the gateway never
    // stopped; only this tab's connection did.
    useChatStore.getState().handleFrame({
      type: 'session_state', session_id: SID, user_id: 'u1', pending_approvals: [],
      active_turn: { turn_id: TURN_ID, agent_id: 'mia', started_at: '2026-09-24T00:00:00Z' }, emitted_at: '2026-09-24T00:00:20Z',
    } as WsReceiveFrame)

    // Incremental catch-up tail: t009..t040, seq 333..371 — SAME message_id.
    for (let i = 9; i <= 40; i++) {
      useChatStore.getState().handleFrame(token(324 + i, `t${String(i).padStart(3, '0')} `))
    }
    useChatStore.getState().handleFrame({
      type: 'catch_up_complete', session_id: SID, seq: 372, boot_id: 'boot-1', mode: 'incremental',
    } as WsReceiveFrame)

    // Live tail: t041..t060, seq 373..392 (monotonic — seq is a strictly
    // increasing per-session counter; the orchestrator's report abbreviated
    // this range for readability, but a self-consistent test must keep every
    // frame's seq properly ordered, done last).
    for (let i = 41; i <= 60; i++) {
      useChatStore.getState().handleFrame(token(372 + (i - 40), `t${String(i).padStart(3, '0')} `))
    }
    useChatStore.getState().handleFrame({
      type: 'done', session_id: SID, message_id: MSG_ID, turn_id: TURN_ID, seq: 393, stats: { tokens: 60, cost: 0.01 },
    } as WsReceiveFrame)

    bubbles = assistantBubbles()
    // ONE bubble — never a second, never stuck at t008.
    expect(bubbles).toHaveLength(1)
    const expected = Array.from({ length: 60 }, (_, i) => `t${String(i + 1).padStart(3, '0')} `).join('')
    expect(bubbles[0].content).toBe(expected)
    expect(bubbles[0].status).toBe('done')
    expect(bubbles[0].isStreaming).toBe(false)
  })
})

// BUG 2 (medium): sendMessage() (outbound-lifecycle.ts) mints an optimistic,
// empty assistant placeholder (a local generateId()) at send time, purely to
// render "the agent is about to reply" with zero latency. Under the OLD
// (#822) "last assistant message" heuristic the first live token
// transparently reused it. Under the NEW message_id-keyed resolution
// (pass 2), the first token creates a BRAND NEW bubble under the server's
// message_id, and the placeholder is never reconciled — it survives, empty,
// finalized to 'done' by the C8 sweep on the turn's `done` frame. No wire
// frame produces this second bubble: session_started, user_message,
// message_status x2, token x60 (one message_id), done.
describe('BUG 2 — the send-time optimistic placeholder must reconcile with the message_id bubble', () => {
  it('sending a message and receiving its streamed reply produces exactly ONE assistant bubble, never an orphaned empty one', () => {
    const conn = { send: () => true, close: () => {}, isConnected: true }
    useConnectionStore.setState({ connection: conn, isConnected: true } as never)
    useSessionStore.setState({ activeSessionId: null })

    // The real client-side send path — this is what actually mints the
    // optimistic placeholder BUG 2 is about. Driving handleFrame alone (as
    // every other test in this file/suite does) never exercises it, which is
    // exactly why the fixture-driven suite (F1-F8) never caught this.
    useChatStore.getState().sendMessage('check my tasks')

    const newSid = 'sess-regress-2'
    useChatStore.getState().handleFrame({
      type: 'session_started', session_id: newSid, agent_id: 'mia', seq: 1, boot_id: 'boot-1',
    } as WsReceiveFrame)
    useChatStore.getState().handleFrame({
      type: 'user_message', session_id: newSid, id: 'u1', client_message_id: 'whatever', content: 'check my tasks', timestamp: '2026-09-24T00:00:00Z', seq: 2,
    } as WsReceiveFrame)
    useChatStore.getState().handleFrame({
      type: 'message_status', session_id: newSid, client_message_id: 'whatever', state: 'received', seq: 3,
    } as WsReceiveFrame)
    useChatStore.getState().handleFrame({
      type: 'message_status', session_id: newSid, client_message_id: 'whatever', state: 'working', seq: 4,
    } as WsReceiveFrame)
    for (let i = 1; i <= 60; i++) {
      useChatStore.getState().handleFrame({
        type: 'token', session_id: newSid, content: `t${String(i).padStart(3, '0')} `, message_id: 'msg-real', turn_id: 'turn-real', agent_id: 'mia', seq: 4 + i,
      } as WsReceiveFrame)
    }
    useChatStore.getState().handleFrame({
      type: 'done', session_id: newSid, message_id: 'msg-real', turn_id: 'turn-real', seq: 65, stats: { tokens: 60, cost: 0.01 },
    } as WsReceiveFrame)

    const b = useChatStore.getState().sessionsById[newSid]!
    const assistantMsgs = b.messageOrder.map((id) => b.messagesById[id]).filter((m) => m.role === 'assistant')
    // Exactly ONE assistant bubble — no leftover empty placeholder.
    expect(assistantMsgs).toHaveLength(1)
    expect(assistantMsgs[0].content.length).toBeGreaterThan(0)
    expect(assistantMsgs[0].status).toBe('done')
  })
})

// Opus review round 2, item 1 ("update moved tools by call_id"): a
// disconnect no longer cancels a running tool call (BUG 1's fix) — the tool
// can still legitimately resolve and its result arrive afterward. But
// clearStreamingState's own bake step (outbound-lifecycle.ts) moves it OUT
// of the live bucket.toolCalls map and into its owning message's tool_calls
// array the instant the disconnect fires, so a subsequent tool_call_result
// for that call_id found no live entry — and was either silently dropped or
// (via appendUnmatchedToolError) rendered as a scary standalone error
// notice for a tool call that's actually fine.
describe('BE-DESIGN.md §6.3 — a tool_call_result for a tool already moved into the message reconciles it in place', () => {
  it('updates the baked tool_calls entry instead of dropping the result or rendering an unmatched-error notice', () => {
    useChatStore.getState().handleFrame(token(325, 'checking your tasks... '))
    useChatStore.getState().handleFrame({
      type: 'tool_call_start', session_id: SID, call_id: 'call-1', tool: 'list_tasks', params: {}, turn_id: TURN_ID, seq: 326,
    } as WsReceiveFrame)

    // Disconnect while the tool call is still running — §6.3: not cancelled,
    // just moved (baked) into the message.
    useChatStore.getState().clearStreamingState()
    let bucket = useChatStore.getState().sessionsById[SID]!
    expect(bucket.toolCalls['call-1']).toBeUndefined() // moved out of the live map
    let msg = bucket.messagesById[MSG_ID]
    expect(msg.tool_calls?.[0]).toMatchObject({ id: 'call-1', status: 'running' })

    // The real result arrives afterward (catch-up, or a late live frame).
    useChatStore.getState().handleFrame({
      type: 'tool_call_result', session_id: SID, call_id: 'call-1', tool: 'list_tasks', result: { tasks: [] }, status: 'success', seq: 327,
    } as WsReceiveFrame)

    bucket = useChatStore.getState().sessionsById[SID]!
    msg = bucket.messagesById[MSG_ID]
    // Reconciled IN PLACE on the message — not dropped, and no separate
    // "unmatched tool" error-notice bubble was created.
    expect(msg.tool_calls?.[0]).toMatchObject({ id: 'call-1', status: 'success', result: { tasks: [] } })
    const bubbles = assistantBubbles()
    expect(bubbles).toHaveLength(1)
  })
})

// Opus review round 2, item 2 (HIGH, founder decision Q1): the pending tail
// must render at the END of the thread and survive a snapshot rebuild
// (queued/sending/FAILED — 'failed' was previously missing entirely), and a
// reconnect that replays the sender's OWN message must not duplicate it.
describe('BE-DESIGN.md §4.7/founder Q1 — pending tail ordering and replay dedup', () => {
  const PT_SID = 'sess-pending-tail'

  it('a failed send survives a session_snapshot rebuild and stays positioned AFTER the history that streams in afterward', () => {
    useSessionStore.setState({ activeSessionId: PT_SID })
    useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)

    // A message the user sent, which failed to deliver — still sitting in
    // the bucket with deliveryStatus 'failed'.
    useChatStore.setState((s) => ({
      sessionsById: {
        ...s.sessionsById,
        [PT_SID]: {
          ...(s.sessionsById[PT_SID] ?? ({} as never)),
          messageOrder: ['failed-1'],
          messagesById: {
            'failed-1': { id: 'failed-1', role: 'user', content: 'did this send?', timestamp: '2026-09-24T00:00:00Z', deliveryStatus: 'failed' },
          },
        },
      },
    }) as never)

    // A session_snapshot rebuild arrives (e.g. a reload) — the failed send
    // must survive it, and any HISTORY that then replays must land BEFORE
    // it, not after.
    useChatStore.getState().handleFrame({
      type: 'session_snapshot', session_id: PT_SID, seq: 5, boot_id: 'boot-1', reason: 'unknown_position',
    } as WsReceiveFrame)
    useChatStore.getState().handleFrame({
      type: 'replay_message', session_id: PT_SID, id: 'hist-1', role: 'user', content: 'an earlier message', timestamp: '2026-09-23T23:59:00Z',
    } as WsReceiveFrame)
    useChatStore.getState().handleFrame({
      type: 'replay_message', session_id: PT_SID, id: 'hist-2', role: 'assistant', content: 'an earlier reply', timestamp: '2026-09-23T23:59:30Z',
    } as WsReceiveFrame)

    const bucket = useChatStore.getState().sessionsById[PT_SID]!
    // The failed send is STILL PRESENT (not silently dropped by the wipe)...
    expect(bucket.messagesById['failed-1']).toBeDefined()
    expect(bucket.messagesById['failed-1'].deliveryStatus).toBe('failed')
    // ...and it is LAST — history that streamed in after the wipe inserts
    // BEFORE it, never after (founder Q1: pending/failed renders at the end).
    expect(bucket.messageOrder).toEqual(['hist-1', 'hist-2', 'failed-1'])
  })

  it('a reconnect that replays the sender\'s own message reconciles the existing optimistic bubble instead of duplicating it', () => {
    useSessionStore.setState({ activeSessionId: PT_SID })
    useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)

    // The optimistic bubble sendMessage created, still keyed by the LOCAL
    // client_message_id (never re-keyed at send time).
    useChatStore.setState((s) => ({
      sessionsById: {
        ...s.sessionsById,
        [PT_SID]: {
          ...(s.sessionsById[PT_SID] ?? ({} as never)),
          messageOrder: ['client-abc'],
          messagesById: {
            'client-abc': { id: 'client-abc', role: 'user', content: 'check my tasks', timestamp: '2026-09-24T00:00:00Z', deliveryStatus: 'sending' },
          },
        },
      },
    }) as never)

    // Reconnect: a snapshot replays this SAME message, now with the
    // server's real id and this client_message_id.
    useChatStore.getState().handleFrame({
      type: 'session_snapshot', session_id: PT_SID, seq: 5, boot_id: 'boot-1', reason: 'unknown_position',
    } as WsReceiveFrame)
    // The snapshot wipe just cleared history but kept the pending 'sending'
    // bubble (client-abc) — restore it for this scenario (session_snapshot
    // itself already preserves it via applySnapshotHistoryWipe; re-seeding
    // here isolates the replay_message dedup behavior under test without
    // depending on that separate mechanism).
    useChatStore.setState((s) => {
      const b = s.sessionsById[PT_SID]!
      return { sessionsById: { ...s.sessionsById, [PT_SID]: { ...b, messageOrder: ['client-abc'], messagesById: { 'client-abc': { id: 'client-abc', role: 'user', content: 'check my tasks', timestamp: '2026-09-24T00:00:00Z', deliveryStatus: 'sending' } } } } }
    })
    useChatStore.getState().handleFrame({
      type: 'replay_message', session_id: PT_SID, id: 'server-real-id', client_message_id: 'client-abc', role: 'user', content: 'check my tasks', timestamp: '2026-09-24T00:00:00Z',
    } as WsReceiveFrame)

    const bucket = useChatStore.getState().sessionsById[PT_SID]!
    const userMsgs = bucket.messageOrder.map((id) => bucket.messagesById[id]).filter((m) => m.role === 'user')
    // Exactly ONE user bubble — not two.
    expect(userMsgs).toHaveLength(1)
    // Re-keyed to the server's real id, resolved out of pending status.
    expect(bucket.messagesById['server-real-id']).toBeDefined()
    expect(bucket.messagesById['server-real-id'].deliveryStatus).toBe('received')
    expect(bucket.messagesById['client-abc']).toBeUndefined()
  })
})
