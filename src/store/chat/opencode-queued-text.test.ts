// #1899 B / #1895, docs/agents.md and team-lead's queued-starting-text row:
// the original run ends naturally without done while a follow-up is queued.
// Its first new message id closes the earlier bubble, not the whole chat.
import { act } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { getDroppedFrameCount, parseFrameSafe, resetDroppedFrameCount } from '@/lib/ws'
import type { ServerFrame, WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { getMessages } from './messages'
import { useChatStore } from './store'

const SID = 'opencode-helper-queued-starting-text-1895'
const WORKER = 'worker-1895'
const FIRST_TURN = 'worker-1895-starting-text'
const RESUMED_TURN = 'worker-1895-queued-followup'
const FIRST_MESSAGE = '37a89370-e42c-45b8-933e-1c4e94a6df33'
const RESUMED_MESSAGE = 'b5ce02ea-8151-46bf-a81d-fef617fd97c7'
const CLIENT_ID = 'person-queued-after-text-1895'
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
  if (parsed === null) throw new Error(`#1895 queued text fixture rejected by the real parser: ${frame.type}`)
  act(() => { useChatStore.getState().handleFrame(parsed as ServerFrame) })
}

function textRows(sessionOnly = false) {
  const state = useChatStore.getState()
  const messages = sessionOnly ? getMessages(state.sessionsById[SID]) : state.messages
  // Tool-only rows are checked separately for stale streaming flags below.
  return messages
    .filter((message) => message.role === 'user' || (message.role === 'assistant' && message.content.length > 0))
    .map((message) => message.role === 'user'
      ? { role: message.role, content: message.content }
      : {
          id: message.id, role: message.role, content: message.content,
          turnId: message.turnId, status: message.status, isStreaming: message.isStreaming,
        })
}

it.each(['sender', 'second viewer'] as const)('closes original queued-startup text at the first new message id while the follow-up remains streaming (%s)', (viewer) => {
  const send = vi.fn<WsConnection['send']>().mockReturnValue(true)
  const socket = { send, close: vi.fn() }
  act(() => {
    useConnectionStore.setState({ connection: socket as unknown as WsConnection, isConnected: true })
  })
  feed({
    type: 'token', session_id: SID, seq: 1, agent_id: WORKER,
    turn_id: FIRST_TURN, message_id: FIRST_MESSAGE, content: ORIGINAL,
  })
  expect(textRows(), 'instrument: the starting original answer is already visible').toStrictEqual([
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
  if (viewer === 'sender') {
    act(() => { useChatStore.getState().sendMessage(FOLLOWUP, { clientMessageId: CLIENT_ID }) })
    expect(send).toHaveBeenCalledTimes(1)
    expect(send).toHaveBeenCalledWith(expect.objectContaining({
      type: 'message', session_id: SID, agent_id: WORKER, content: FOLLOWUP, client_message_id: CLIENT_ID,
    }))
  }
  // The second subscriber receives the person's real echo but did not send.
  // It must close the original at the new-id boundary, not rely on the
  // sender's early visual close (ADR-070, Mid-turn steering, section 2.1).
  const originalStillOpen = viewer === 'second viewer'
  feed({ type: 'message_status', session_id: SID, seq: 4, client_message_id: CLIENT_ID, state: 'received' })
  feed({
    type: 'user_message', session_id: SID, seq: 5, id: 'saved-person-queued-after-text-1895',
    client_message_id: CLIENT_ID, content: FOLLOWUP, timestamp: '2026-11-20T12:09:00Z',
  })
  expect(textRows(), 'instrument: the second viewer still needs the new-id boundary to close the original').toStrictEqual([
    { id: FIRST_MESSAGE, role: 'assistant', content: ORIGINAL, turnId: FIRST_TURN, status: originalStillOpen ? 'streaming' : 'done', isStreaming: originalStillOpen },
    { role: 'user', content: FOLLOWUP },
  ])
  expect(useChatStore.getState().isStreaming, 'no idle gap before follow-up stdout').toBe(true)
  expect(useChatStore.getState().sessionsById[SID].isStreaming).toBe(true)
  // NO original done. Assert the boundary now, before the resumed final done
  // could mask a stale original streaming bubble by finalizing every row.
  feed({
    type: 'token', session_id: SID, seq: 6, agent_id: WORKER,
    turn_id: RESUMED_TURN, message_id: RESUMED_MESSAGE, content: ANSWER,
  })
  const boundaryRows = [
    { id: FIRST_MESSAGE, role: 'assistant', content: ORIGINAL, turnId: FIRST_TURN, status: 'done', isStreaming: false },
    { role: 'user', content: FOLLOWUP },
    { id: RESUMED_MESSAGE, role: 'assistant', content: ANSWER, turnId: RESUMED_TURN, status: 'streaming', isStreaming: true },
  ]
  expect.soft(textRows(), 'first new message id closes only the original bubble').toStrictEqual(boundaryRows)
  expect.soft(textRows(true), 'stored session agrees at the first resumed token').toStrictEqual(boundaryRows)
  expect.soft(useChatStore.getState().isStreaming, 'foreground remains busy for the resumed answer').toBe(true)
  expect.soft(useChatStore.getState().sessionsById[SID].isStreaming).toBe(true)
  feed({
    type: 'done', session_id: SID, seq: 7, turn_id: RESUMED_TURN, message_id: RESUMED_MESSAGE,
    stats: { tokens: 0, session_tokens_total: 0, cost: 0, duration_ms: 303 },
  })
  const finalRows = [
    { id: FIRST_MESSAGE, role: 'assistant', content: ORIGINAL, turnId: FIRST_TURN, status: 'done', isStreaming: false },
    { role: 'user', content: FOLLOWUP },
    { id: RESUMED_MESSAGE, role: 'assistant', content: ANSWER, turnId: RESUMED_TURN, status: 'done', isStreaming: false },
  ]
  expect.soft(textRows(), 'only the final resumed done makes both exact answers idle').toStrictEqual(finalRows)
  expect.soft(textRows(true)).toStrictEqual(finalRows)
  const state = useChatStore.getState()
  for (const messages of [state.messages, getMessages(state.sessionsById[SID])]) {
    expect.soft(messages.filter((message) => message.role === 'assistant' && (message.status === 'streaming' || message.isStreaming === true)), 'no text or tool-only assistant row remains streaming').toStrictEqual([])
  }
  expect.soft(state.isStreaming).toBe(false)
  expect.soft(state.sessionsById[SID].isStreaming).toBe(false)
  expect(getDroppedFrameCount(), 'all seven frames passed the real generated validator').toBe(0)
  expect(send, 'no extra request, reload or replay was needed').toHaveBeenCalledTimes(viewer === 'sender' ? 1 : 0)
})
