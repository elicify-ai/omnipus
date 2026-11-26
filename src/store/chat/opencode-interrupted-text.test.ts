// #1895, team-lead item 3: the original answer has text but no done of its
// own when a person's interruption resumes the same external conversation.
// Final done must leave BOTH answers visible and finalized, not just turn 2.
import { act } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { getDroppedFrameCount, parseFrameSafe, resetDroppedFrameCount } from '@/lib/ws'
import type { ServerFrame, WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { getMessages } from './messages'
import { useChatStore } from './store'

const SID = 'opencode-helper-interrupted-text-1895'
const WORKER = 'worker-1895'
const FIRST_TURN = 'worker-1895-turn-1'
const RESUMED_TURN = 'worker-1895-turn-2'
const FIRST_MESSAGE = '5e561d72-40a3-446a-966a-e1fdce30e88f'
const RESUMED_MESSAGE = 'e0d6092b-3f7a-41ba-aad9-b72f6a86149c'
const CLIENT_ID = 'person-after-original-text-1895'
const ORIGINAL = 'Original helper text before the person interrupts 1895'
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
  const parsed = parseFrameSafe(JSON.stringify(frame))
  if (parsed === null) throw new Error(`#1895 text fixture rejected by the real parser: ${frame.type}`)
  act(() => { useChatStore.getState().handleFrame(parsed as ServerFrame) })
}

function textRows() {
  // Ignore tool-only display rows here; the separate no-streaming assertion
  // below covers every assistant row, including any tool-only placeholder.
  return useChatStore.getState().messages
    .filter((message) => message.role === 'user' || (message.role === 'assistant' && message.content.length > 0))
    .map((message) => message.role === 'user'
      ? { role: message.role, content: message.content }
      : {
          id: message.id, role: message.role, content: message.content,
          turnId: message.turnId, status: message.status, isStreaming: message.isStreaming,
        })
}

it('preserves and finalizes original text and the separate resumed answer when only the resumed turn gets done', () => {
  const send = vi.fn<WsConnection['send']>().mockReturnValue(true)
  const socket = { send, close: vi.fn() }
  act(() => {
    useConnectionStore.setState({ connection: socket as unknown as WsConnection, isConnected: true })
  })
  feed({
    type: 'token', session_id: SID, seq: 1, agent_id: WORKER,
    turn_id: FIRST_TURN, message_id: FIRST_MESSAGE, content: ORIGINAL,
  })
  expect(textRows(), 'instrument: original text is already a visible streaming bubble before the interruption').toStrictEqual([
    { id: FIRST_MESSAGE, role: 'assistant', content: ORIGINAL, turnId: FIRST_TURN, status: 'streaming', isStreaming: true },
  ])
  feed({
    type: 'tool_call_start', session_id: SID, seq: 2, agent_id: WORKER,
    call_id: 'fixture-ready-1895', tool: 'read_file', params: { path: 'fixture.txt' },
  })
  feed({
    type: 'tool_call_result', session_id: SID, seq: 3, agent_id: WORKER,
    call_id: 'fixture-ready-1895', tool: 'read_file', result: 'ready', status: 'success',
  })
  expect(useChatStore.getState().isStreaming).toBe(true)
  act(() => { useChatStore.getState().sendMessage(FOLLOWUP, { clientMessageId: CLIENT_ID }) })
  expect(send).toHaveBeenCalledTimes(1)
  expect(send).toHaveBeenCalledWith(expect.objectContaining({
    type: 'message', session_id: SID, agent_id: WORKER, content: FOLLOWUP, client_message_id: CLIENT_ID,
  }))
  feed({ type: 'message_status', session_id: SID, seq: 4, client_message_id: CLIENT_ID, state: 'received' })
  feed({
    type: 'user_message', session_id: SID, seq: 5, id: 'saved-person-after-text-1895',
    client_message_id: CLIENT_ID, content: FOLLOWUP, timestamp: '2026-11-20T06:09:00Z',
  })
  // Deliberately NO done for FIRST_TURN. Suppressing that done must not leave
  // the earlier assistant row streaming forever when the continuation ends.
  feed({
    type: 'token', session_id: SID, seq: 6, agent_id: WORKER,
    turn_id: RESUMED_TURN, message_id: RESUMED_MESSAGE, content: ANSWER,
  })
  expect(textRows().map(({ role, content }) => ({ role, content })), 'both exact answers remain separate and in order, without a reload').toStrictEqual([
    { role: 'assistant', content: ORIGINAL },
    { role: 'user', content: FOLLOWUP },
    { role: 'assistant', content: ANSWER },
  ])
  expect(useChatStore.getState().isStreaming).toBe(true)
  expect(useChatStore.getState().sessionsById[SID].isStreaming).toBe(true)
  feed({
    type: 'done', session_id: SID, seq: 7, turn_id: RESUMED_TURN, message_id: RESUMED_MESSAGE,
    stats: { tokens: 0, session_tokens_total: 0, cost: 0, duration_ms: 303 },
  })
  expect.soft(textRows(), 'final done preserves and finalizes BOTH text bubbles, not only the resumed one').toStrictEqual([
    { id: FIRST_MESSAGE, role: 'assistant', content: ORIGINAL, turnId: FIRST_TURN, status: 'done', isStreaming: false },
    { role: 'user', content: FOLLOWUP },
    { id: RESUMED_MESSAGE, role: 'assistant', content: ANSWER, turnId: RESUMED_TURN, status: 'done', isStreaming: false },
  ])
  expect.soft(useChatStore.getState().messages.filter((message) => message.role === 'assistant' && (message.status === 'streaming' || message.isStreaming === true)), 'no visible or tool-only assistant row is left streaming').toStrictEqual([])
  expect.soft(useChatStore.getState().isStreaming, 'foreground chat finishes idle').toBe(false)
  expect.soft(useChatStore.getState().sessionsById[SID].isStreaming, 'stored session finishes idle').toBe(false)
  expect.soft(getMessages(useChatStore.getState().sessionsById[SID]).filter((message) => message.role === 'assistant' && (message.status === 'streaming' || message.isStreaming === true)), 'session rows also have no stale streaming state').toStrictEqual([])
  expect(getDroppedFrameCount(), 'all seven frames passed the actual runtime validator').toBe(0)
  expect(send, 'no extra request, reload or replay was needed').toHaveBeenCalledTimes(1)
})
