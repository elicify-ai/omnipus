import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'
import type { Message } from '@/lib/api/generated/openapi-types'
import type { WsConnection } from '@/lib/ws'
import { convertMessage } from '@/lib/omnipus-runtime'
import { getMessageStatusSuffix } from '@/lib/truncation'
import { queryClient } from '@/lib/queryClient'
import { entry, expectValidFrame, expectValidPage, page, replay, snapshot } from '../../tests/chat-history-paging/fixtures'
import { useChatStore, getMessages } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { emptySessionState } from './chat/session'
import { abortHistoryReads } from './chat/history'
import { pendingCancelAckSids } from './chat/runtime-state'

// closes #1671. Oracle: issue #1671 and ta3-brief.md ADDENDUM: a stopped
// answer is incomplete/cancelled on live, reload and replay, not just labelled
// "(interrupted)". Real store/history/frame reducers and runtime conversion;
// only fetch and outbound socket are process edges. No renderer/status mock.
const SID = 'stop-status-1671'
const TURN = 'stop-status-turn-1671'
const MID = 'stop-status-answer-1671'
const TEXT = 'This is the stopped partial answer.'
const sender = { send: vi.fn<WsConnection['send']>() }

function reset() {
  abortHistoryReads(SID)
  pendingCancelAckSids.clear()
  queryClient.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
}

beforeEach(() => {
  reset()
  sender.send.mockReset().mockReturnValue(true)
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'mia' })
  useChatStore.setState({ sessionsById: { [SID]: emptySessionState() } })
  useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
})
afterEach(() => { reset(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

function frame(value: ServerFrame) {
  expectValidFrame(value)
  useChatStore.getState().handleFrame(value)
}
function startLive() {
  frame({ type: 'token', session_id: SID, turn_id: TURN, message_id: MID, content: TEXT })
  expect(useChatStore.getState().sessionsById[SID].isStreaming, 'instrument: actual live turn exists').toBe(true)
}
function cancelledDone() {
  frame({ type: 'done', session_id: SID, turn_id: TURN, message_id: MID,
    stats: { truncated: true, truncation_reason: 'cancelled' } })
}
function reply() {
  const messages = getMessages(useChatStore.getState().sessionsById[SID])
  expect(messages.map(message => [message.id, message.content]), 'same answer, no duplicate or body rewrite').toEqual([[MID, TEXT]])
  return messages[0]
}
function expectStopped() {
  const message = reply()
  expect(message.isStreaming, 'the stopped answer is no longer running').not.toBe(true)
  expect(getMessageStatusSuffix(message), 'instrument: the actual stopped suffix is rendered').toBe('(interrupted)')
  expect(convertMessage(message, {}, [], {}, true).status,
    'closes #1671: an interrupted suffix must never conceal a complete runtime status').toEqual({ type: 'incomplete', reason: 'cancelled' })
}
function savedReply(): Message {
  // No invented status: the gateway's existing cancelled-truncation metadata
  // must be sufficient even when the cancel marker is on a different page.
  return entry(2, { id: MID, role: 'assistant', type: 'message', turn_id: TURN,
    content: TEXT, truncated: true, truncation_reason: 'cancelled' })
}

describe('#1671 stopped-turn status parity', () => {
  it('live cancelled done is incomplete, even without an earlier local Stop freeze', () => {
    startLive()
    // A second tab or transport ordering can learn cancellation from done;
    // it must not require this tab to have clicked Stop first.
    cancelledDone()
    expectStopped()
  })

  it('REST reload keeps cancelled metadata incomplete rather than complete', async () => {
    const source = savedReply()
    const saved = page([source], { sessionId: SID })
    expectValidPage(saved, [source.history_position!], 1)
    const reads: string[] = []
    vi.stubGlobal('fetch', async (url: RequestInfo | URL) => {
      reads.push(String(url))
      return new Response(JSON.stringify(saved), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    await useChatStore.getState().loadLatestHistory(SID)
    expect(reads, 'instrument: real REST history action performed exactly one network read').toHaveLength(1)
    expect(new URL(reads[0], 'http://localhost').pathname).toBe(`/api/v1/sessions/${SID}/messages`)
    expect(useChatStore.getState().sessionsById[SID].history.historyLoaded).toBe(true)
    expect(useChatStore.getState().sessionsById[SID].history.requests.latest).toMatchObject({ status: 'idle', error: null })
    expectStopped()
  })

  it('snapshot replay keeps the stopped answer incomplete after its terminal frames', () => {
    const source = savedReply()
    const saved = page([source], { sessionId: SID })
    expectValidPage(saved, [source.history_position!], 1)
    frame(snapshot(saved))
    frame({ type: 'session_state', session_id: SID, user_id: 'disposable-reader',
      pending_approvals: [], emitted_at: source.timestamp!, history_state: { status: 'available' } })
    frame({ ...replay(source, SID), truncated: true, truncation_reason: 'cancelled' })
    frame({ type: 'done', session_id: SID })
    frame({ type: 'catch_up_complete', session_id: SID, seq: 100, boot_id: 'history-boot', mode: 'snapshot' })
    expect(useChatStore.getState().sessionsById[SID].history.historyLoaded, 'instrument: replay committed').toBe(true)
    expectStopped()
  })

  it('control: a local Stop already marked interrupted stays incomplete after done', () => {
    startLive()
    expect(useChatStore.getState().cancelStream(SID)).toBe(true)
    expect(sender.send.mock.calls).toHaveLength(1)
    cancelledDone()
    expectStopped()
  })

  it('control: an ordinary successful answer remains complete without an interrupted suffix', () => {
    startLive()
    frame({ type: 'done', session_id: SID, turn_id: TURN, message_id: MID })
    const message = reply()
    expect(message.isStreaming).toBe(false)
    expect(getMessageStatusSuffix(message)).toBeNull()
    expect(convertMessage(message, {}, [], {}, true).status).toEqual({ type: 'complete', reason: 'stop' })
  })
})
