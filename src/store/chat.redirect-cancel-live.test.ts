import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ErrorFrame, RedirectFrame } from '@/lib/api/generated/asyncapi-types'
import { getMessageStatusSuffix } from '@/lib/truncation'
import type { WsConnection } from '@/lib/ws'
import { __resetFinishedTurnIdsForTests, getMessages, useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useWorkspacesStore } from './workspacesStore'

// Oracle: S7 failure dispatch (2026-10-07): a redirect during streamed text
// keeps the entire partial reply and one "(interrupted)" marker, without a
// reload, with or without running helpers. Only the socket is faked; stores,
// sendMessage, inbound reducers, foreground sync and the suffix selector are real.
// The redirect transport does NOT call cancelStream/markLastMessageInterrupted;
// the server's typed cancellation must therefore supply the live interruption.
const SID = 's7-live-redirect'
const AGENT = 'jim'
const TURN = 's7-stopped-turn'
const NEXT_TURN = 's7-redirected-turn'
const STOP_COPY = 'This turn was stopped before it finished.'
const INSTRUCTION = 'now just say the word mango'
// S7b reported roughly 31k characters; test full equality, not a preview.
const PARTIAL = 'An animal description still being written.\n'.repeat(750)
const sender = { send: vi.fn<WsConnection['send']>() }

function resetStores() {
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    __resetFinishedTurnIdsForTests()
  })
}

beforeEach(() => {
  resetStores()
  sender.send.mockReset().mockReturnValue(true)
  act(() => {
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
    useSessionStore.getState().setActiveSession(SID, AGENT)
  })
})
afterEach(resetStores)

function startTurn(content = PARTIAL, turnId = TURN) {
  act(() => {
    const clientMessageId = `${turnId}-user`
    useChatStore.getState().sendMessage('Write animal descriptions.', { clientMessageId })
    // Real sends are acknowledged before the streamed answer. Leaving this
    // user message 'sending' would incorrectly model an unresolved pending tail.
    useChatStore.getState().handleFrame({
      type: 'message_status', session_id: SID, client_message_id: clientMessageId, state: 'working',
    })
    if (content) {
      useChatStore.getState().handleFrame({
        type: 'token', session_id: SID, agent_id: AGENT,
        turn_id: turnId, message_id: `${turnId}-message`, content,
      })
    }
  })
  expect(useChatStore.getState().isStreaming, 'instrument: a real turn is running').toBe(true)
}

function cancelFromServer() {
  const frame: ErrorFrame = {
    type: 'error', session_id: SID, message: STOP_COPY,
    payload: { llm_error: { code: 'turn_canceled', message: STOP_COPY, retryable: true } },
  }
  act(() => { useChatStore.getState().handleFrame(frame) })
}

function assistantReplies() {
  return useChatStore.getState().messages.filter((message) => message.role === 'assistant')
}

function expectStoppedReply(content: string) {
  expect(assistantReplies().map(({ content, status, isStreaming, errorCode }) => ({
    content, status, isStreaming, errorCode,
  })), 'S7: cancellation preserves the partial reply instead of replacing it with failure copy').toStrictEqual([
    { content, status: 'interrupted', isStreaming: false, errorCode: undefined },
  ])
  expect(useChatStore.getState().isStreaming, 'S7: the stopped turn is no longer streaming').toBe(false)
  expect(assistantReplies().map(getMessageStatusSuffix), 'S7: exactly one live interruption marker').toStrictEqual([
    '(interrupted)',
  ])
}

describe('S7 live typed cancellation after redirect', () => {
  it.each([false, true])('keeps every streamed character and one marker through the redirected answer (helpers=%s)', (withHelpers) => {
    startTurn()
    if (withHelpers) {
      act(() => {
        useChatStore.getState().handleFrame({
          type: 'subagent_start', session_id: SID, agent_id: 'helper-agent',
          span_id: 's7-span', parent_call_id: 's7-delegate', task_label: 'Keep writing fish',
          child_session_id: 's7-helper',
        })
        useChatStore.getState().handleFrame({
          type: 'token', session_id: 's7-helper', agent_id: 'helper-agent',
          turn_id: 's7-helper-turn', message_id: 's7-helper-message', content: 'A helper is still writing fish.',
        })
      })
    }
    const helperBefore = useChatStore.getState().sessionsById['s7-helper']
    const spansBefore = assistantReplies().flatMap((message) => message.spans ?? [])
    expect(spansBefore.map((span) => span.status)).toStrictEqual(withHelpers ? ['running'] : [])
    sender.send.mockClear()
    // Same transport boundary as the command: no optimistic local Stop.
    const redirect: RedirectFrame = { type: 'redirect', session_id: SID, instruction: INSTRUCTION }
    sender.send(redirect)
    expect(sender.send.mock.calls).toStrictEqual([[redirect]])
    expect(assistantReplies().map((message) => message.content)).toStrictEqual([PARTIAL])

    cancelFromServer()
    // Assert BEFORE done so terminal truncation metadata cannot hide this bug.
    expectStoppedReply(PARTIAL)
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done', session_id: SID, turn_id: TURN, message_id: `${TURN}-message`,
        stats: { tokens: 100, cost: 0 },
      })
    })
    expectStoppedReply(PARTIAL)

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'replay_message', session_id: SID, role: 'user', id: 'redirect-s7-instruction', content: INSTRUCTION,
      })
      useChatStore.getState().handleFrame({
        type: 'token', session_id: SID, agent_id: AGENT, turn_id: NEXT_TURN,
        message_id: 's7-next-message', content: 'mango',
      })
      useChatStore.getState().handleFrame({
        type: 'done', session_id: SID, turn_id: NEXT_TURN, message_id: 's7-next-message',
        stats: { tokens: 1, cost: 0 },
      })
    })
    expect(useChatStore.getState().messages.map(({ role, content }) => ({ role, content }))).toStrictEqual([
      { role: 'user', content: 'Write animal descriptions.' },
      { role: 'assistant', content: PARTIAL },
      { role: 'user', content: INSTRUCTION },
      { role: 'assistant', content: 'mango' },
    ])
    expect(assistantReplies().map((message) => [message.status, getMessageStatusSuffix(message)])).toStrictEqual([
      ['interrupted', '(interrupted)'], ['done', null],
    ])
    expect(assistantReplies().flatMap((message) => message.spans ?? [])).toStrictEqual(spansBefore)
    expect(useChatStore.getState().sessionsById['s7-helper']).toStrictEqual(helperBefore)
  })

  it('marks a typed cancellation before the first token without inventing partial text', () => {
    startTurn('')
    cancelFromServer()
    expectStoppedReply('')
  })

  it('does not change a prior successful reply when the current streamed turn is cancelled', () => {
    startTurn('The previous answer is complete.', 's7-prior-turn')
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done', session_id: SID, turn_id: 's7-prior-turn', stats: { tokens: 1, cost: 0 },
      })
    })
    const priorReply = assistantReplies()[0]
    startTurn()
    cancelFromServer()
    expect(assistantReplies()[0], 'S7: cancellation belongs only to the current reply').toStrictEqual(priorReply)
    expect(assistantReplies().map(({ content, status }) => ({ content, status }))).toStrictEqual([
      { content: 'The previous answer is complete.', status: 'done' },
      { content: PARTIAL, status: 'interrupted' },
    ])
    expect(assistantReplies().map(getMessageStatusSuffix)).toStrictEqual([null, '(interrupted)'])
  })

  it.each([
    ['network', 'We couldn’t reach the model provider. Check your internet connection and retry.'],
    ['turn_timed_out', 'The model provider didn’t finish this turn in time, so it was stopped. Retry — if it keeps happening, open Verbose chat for details.'],
  ] as const)('still replaces narration with failure copy for a genuine %s error', (code, message) => {
    startTurn()
    const frame: ErrorFrame = {
      type: 'error', session_id: SID, message,
      payload: { llm_error: { code, message, retryable: true } },
    }
    act(() => {
      useChatStore.getState().handleFrame(frame)
      useChatStore.getState().handleFrame({ type: 'done', session_id: SID, turn_id: TURN })
    })
    expect(assistantReplies().map(({ content, status, errorCode, isStreaming }) => ({
      content, status, errorCode, isStreaming,
    }))).toStrictEqual([{ content: message, status: 'error', errorCode: code, isStreaming: false }])
    expect(assistantReplies().map(getMessageStatusSuffix)).toStrictEqual([null])
    expect(useChatStore.getState().isStreaming).toBe(false)
  })

  it('keeps the existing legacy untyped cancellation behaviour', () => {
    startTurn()
    act(() => {
      useChatStore.getState().handleFrame({ type: 'error', session_id: SID, message: 'turn.cancel: user requested stop' })
    })
    expectStoppedReply(PARTIAL)
    expect(getMessages(useChatStore.getState().sessionsById[SID])).toStrictEqual(useChatStore.getState().messages)
  })
})
