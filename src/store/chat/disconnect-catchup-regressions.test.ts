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
