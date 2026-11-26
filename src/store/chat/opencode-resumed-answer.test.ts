// #1895 / docs/agents.md, "What you see in an external helper's chat":
// a person's mid-run instruction is shown once, and the resumed answer is
// visible as its token arrives, without a reload. This guards the real parser
// and store; only the outgoing socket-send boundary is replaced.
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { getDroppedFrameCount, parseFrameSafe, resetDroppedFrameCount } from '@/lib/ws'
import type { ServerFrame, WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { getMessages } from './messages'
import { useChatStore } from './store'

const SID = 'opencode-helper-resume-1895'
const WORKER = 'worker-1895'
const FIRST_TURN = 'worker-1895-turn-1'
const RESUMED_TURN = 'worker-1895-turn-2'
const MESSAGE_ID = 'e0d6092b-3f7a-41ba-aad9-b72f6a86149c'
const CLIENT_ID = 'person-live-followup-1895'
const FOLLOWUP = "person's live follow-up 1895"
const ANSWER = 'RESUMED helper answer streamed live 1895'

function resetStores() {
  act(() => {
    useChatStore.getState().clearStreamingState()
    useChatStore.setState({
      ...useChatStore.getInitialState(), sessionsById: {}, messages: [], messagesById: {},
      pendingDrainQueue: [], outboundQueue: [], abandonedFirstSendIds: [],
    }, true)
    useSessionStore.setState({
      ...useSessionStore.getInitialState(), activeSessionId: SID, activeAgentId: WORKER,
      activeAgentType: null, sessionByWorkspace: {},
    }, true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
  })
  resetDroppedFrameCount()
}

beforeEach(resetStores)
afterEach(resetStores)

function feed(frame: ServerFrame) {
  // Do not bypass validation or silently skip a rejected fixture, as a probe
  // may do: every server frame must pass the same parser used by the socket.
  const parsed = parseFrameSafe(JSON.stringify(frame))
  if (parsed === null) throw new Error(`#1895 fixture rejected by the real parser: ${frame.type}`)
  act(() => { useChatStore.getState().handleFrame(parsed as ServerFrame) })
}

function visibleTail() {
  return useChatStore.getState().messages.slice(-2).map((message) => (
    message.role === 'user'
      ? { role: message.role, content: message.content }
      : {
          id: message.id, role: message.role, content: message.content,
          turnId: message.turnId, status: message.status, isStreaming: message.isStreaming,
        }
  ))
}

// Preserve the measured done-without-message_id shape. Sequence numbers are
// normalized to include the received acknowledgement and user echo in order.
describe('#1895 resumed external-helper answer through the real wire parser and chat store', () => {
  // CHECK-ROUND2 F1: this dedicated file must see the older baked tool-only
  // bubble, not just the last two text rows. A second viewer never sent the
  // instruction locally, so a sender-side early close cannot hide the defect.
  it('closes the older baked tool-call bubble at the first resumed token for a second viewer', () => {
    const send = vi.fn<WsConnection['send']>().mockReturnValue(true)
    act(() => {
      useConnectionStore.setState({
        connection: { send, close: vi.fn() } as unknown as WsConnection, isConnected: true,
      })
    })
    const callId = 'gate-f1-older-tool-call'
    feed({
      type: 'tool_call_start', session_id: SID, seq: 1, agent_id: WORKER,
      call_id: callId, tool: 'read_file', params: { path: 'fixture.txt' },
    })
    feed({
      type: 'tool_call_result', session_id: SID, seq: 2, agent_id: WORKER,
      call_id: callId, tool: 'read_file', result: 'ready', status: 'success',
    })
    const original = useChatStore.getState().messages.filter((message) => message.role === 'assistant')
    expect(original, 'instrument: create a real older tool-only bubble through the parser').toHaveLength(1)
    const originalId = original[0].id
    function assertOlderBubble(streaming: boolean) {
      const state = useChatStore.getState()
      // Fresh lookups are essential: baking replaces the old message object.
      for (const rows of [state.messages, getMessages(state.sessionsById[SID])]) {
        const older = rows.find((message) => message.id === originalId)
        expect(older, 'inspect the same older bubble in foreground and saved-session projections').toMatchObject({
          id: originalId, role: 'assistant', content: '',
          status: streaming ? 'streaming' : 'done', isStreaming: streaming,
        })
        if (!streaming) {
          expect(older?.tool_calls?.map(({ id, status }) => ({ id, status }))).toStrictEqual([
            { id: callId, status: 'success' },
          ])
        }
      }
    }
    assertOlderBubble(true)
    // SessionChatState documents live calls in toolCalls; only the token's
    // producer boundary bakes them onto the owner message (types.ts).
    for (const calls of [useChatStore.getState().toolCalls, useChatStore.getState().sessionsById[SID].toolCalls]) {
      expect(calls[callId]).toMatchObject({
        call_id: callId, tool: 'read_file', params: { path: 'fixture.txt' }, result: 'ready', status: 'success',
      })
    }
    feed({ type: 'message_status', session_id: SID, seq: 3, client_message_id: CLIENT_ID, state: 'received' })
    feed({
      type: 'user_message', session_id: SID, seq: 4, id: 'gate-f1-saved-person-row',
      client_message_id: CLIENT_ID, content: FOLLOWUP, timestamp: '2026-11-21T08:00:00Z',
    })
    assertOlderBubble(true) // No local send or original done has already closed it.
    expect(send).not.toHaveBeenCalled()
    feed({
      type: 'token', session_id: SID, seq: 5, agent_id: WORKER,
      turn_id: RESUMED_TURN, message_id: MESSAGE_ID, content: ANSWER,
    })
    assertOlderBubble(false) // Assert now, before a final done could mask a stale row.
    expect(visibleTail()).toStrictEqual([
      { role: 'user', content: FOLLOWUP },
      { id: MESSAGE_ID, role: 'assistant', content: ANSWER, turnId: RESUMED_TURN, status: 'streaming', isStreaming: true },
    ])
    expect(useChatStore.getState().isStreaming).toBe(true)
    expect(useChatStore.getState().sessionsById[SID].isStreaming).toBe(true)
    feed({
      type: 'done', session_id: SID, seq: 6, turn_id: RESUMED_TURN, message_id: MESSAGE_ID,
      stats: { tokens: 0, session_tokens_total: 0, cost: 0, duration_ms: 303 },
    })
    assertOlderBubble(false)
    expect(visibleTail()).toStrictEqual([
      { role: 'user', content: FOLLOWUP },
      { id: MESSAGE_ID, role: 'assistant', content: ANSWER, turnId: RESUMED_TURN, status: 'done', isStreaming: false },
    ])
    expect(useChatStore.getState().isStreaming).toBe(false)
    expect(useChatStore.getState().sessionsById[SID].isStreaming).toBe(false)
    expect(getDroppedFrameCount(), 'all six frames used the real generated validator').toBe(0)
    expect(send, 'second viewer needs no send, reload or replay to close the older bubble').not.toHaveBeenCalled()
  })

  it('shows the resumed answer after the person row while streaming, then completes that same row', () => {
    const send = vi.fn<WsConnection['send']>().mockReturnValue(true)
    const socket = { send, close: vi.fn() }
    act(() => {
      useConnectionStore.setState({ connection: socket as unknown as WsConnection, isConnected: true })
    })
    feed({
      type: 'tool_call_start', session_id: SID, seq: 1, agent_id: WORKER,
      call_id: 'fixture-ready-1895', tool: 'read_file', params: { path: 'fixture.txt' },
    })
    feed({
      type: 'tool_call_result', session_id: SID, seq: 2, agent_id: WORKER,
      call_id: 'fixture-ready-1895', tool: 'read_file', result: 'ready', status: 'success',
    })
    expect(useChatStore.getState().isStreaming, 'instrument: the person sends while the original helper is busy').toBe(true)
    act(() => { useChatStore.getState().sendMessage(FOLLOWUP, { clientMessageId: CLIENT_ID }) })
    expect(send).toHaveBeenCalledTimes(1)
    expect(send).toHaveBeenCalledWith(expect.objectContaining({
      type: 'message', session_id: SID, agent_id: WORKER, content: FOLLOWUP, client_message_id: CLIENT_ID,
    }))
    feed({ type: 'message_status', session_id: SID, seq: 3, client_message_id: CLIENT_ID, state: 'received' })
    feed({
      type: 'user_message', session_id: SID, seq: 4, id: 'saved-person-row-1895',
      client_message_id: CLIENT_ID, content: FOLLOWUP, timestamp: '2026-11-20T06:09:00Z',
    })
    const interruptedDone: ServerFrame = {
      type: 'done', session_id: SID, seq: 5, turn_id: FIRST_TURN,
      stats: { tokens: 0, session_tokens_total: 0, cost: 0, duration_ms: 4162 },
    }
    expect(interruptedDone, 'instrument: reproduce the original empty done, not a completed answer row').not.toHaveProperty('message_id')
    feed(interruptedDone)
    feed({
      type: 'token', session_id: SID, seq: 6, agent_id: WORKER,
      turn_id: RESUMED_TURN, message_id: MESSAGE_ID, content: ANSWER,
    })
    expect(visibleTail(), 'the open chat immediately shows the person followed by the resumed answer').toStrictEqual([
      { role: 'user', content: FOLLOWUP },
      { id: MESSAGE_ID, role: 'assistant', content: ANSWER, turnId: RESUMED_TURN, status: 'streaming', isStreaming: true },
    ])
    expect(useChatStore.getState().isStreaming).toBe(true)
    expect(useChatStore.getState().sessionsById[SID].isStreaming).toBe(true)
    expect(useChatStore.getState().messages.filter((message) => message.role === 'user' && message.content === FOLLOWUP)).toHaveLength(1)

    feed({
      type: 'done', session_id: SID, seq: 7, turn_id: RESUMED_TURN, message_id: MESSAGE_ID,
      stats: { tokens: 0, session_tokens_total: 0, cost: 0, duration_ms: 303 },
    })
    expect(visibleTail(), 'final done completes the same visible resumed row without reordering or duplication').toStrictEqual([
      { role: 'user', content: FOLLOWUP },
      { id: MESSAGE_ID, role: 'assistant', content: ANSWER, turnId: RESUMED_TURN, status: 'done', isStreaming: false },
    ])
    expect(useChatStore.getState().isStreaming).toBe(false)
    expect(useChatStore.getState().sessionsById[SID].isStreaming).toBe(false)
    expect(useChatStore.getState().messages.filter((message) => message.id === MESSAGE_ID)).toHaveLength(1)
    expect(getDroppedFrameCount(), 'all seven server frames were validated, never bypassed or dropped').toBe(0)
    expect(send, 'seeing/completing the resumed answer needs no reload, replay or additional client request').toHaveBeenCalledTimes(1)
  })
})
