import { act } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { DoneFrame, SubagentStartFrame, TokenFrame, ToolCallStartFrame } from '@/lib/api/generated/ws-schemas'
import { queryClient } from '@/lib/queryClient'
import type { WsConnection } from '@/lib/ws'
import { getMessages, useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useWorkspacesStore } from './workspacesStore'
import { seedCanonicalSession } from '../../tests/unit/fixtures/canonical-session'

// Oracle: bundle-2 replay-(c) dispatch at 933d67084: an adopted local/tool
// placeholder takes the wire message_id as its entry id, before and after done.
// Existing real-entry coalescing and cross-turn collision protection stay intact.
// REAL: store, sender action, generated frame validators and frame resolver.
// ONLY DOUBLE: socket send, the process edge; no action/reducer/resolver is mocked.
// GREEN and implementation-mutation proof are deferred to a fresh CHECK instance.
const SID = 'wire-placeholder-session'
const AGENT = 'wire-placeholder-agent'
const TURN = 'wire-placeholder-turn'
const WIRE = 'server-answer-entry'
const INPUT = 'client-answer-input'
const ANSWER = 'The completed answer.'
const CALL = 'delegate-call'
const SPAN = 'delegate-span'
const send = vi.fn<WsConnection['send']>()

beforeEach(() => {
  queryClient.clear()
  seedCanonicalSession(SID, AGENT)
  send.mockReset().mockReturnValue(true)
  act(() => {
    useChatStore.getState().clearStreamingState()
    useChatStore.setState(useChatStore.getInitialState(), true)
    useSessionStore.setState({ ...useSessionStore.getInitialState(), activeSessionId: SID, activeAgentId: AGENT }, true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useConnectionStore.setState({
      ...useConnectionStore.getInitialState(),
      connection: { send } as unknown as WsConnection, isConnected: true,
    }, true)
  })
})

const bucket = () => useChatStore.getState().sessionsById[SID]
const assistants = () => getMessages(bucket()).filter((message) => message.role === 'assistant')

function token(messageId: string, content: string, turnId?: string) {
  act(() => useChatStore.getState().handleFrame(TokenFrame.parse({
    type: 'token', session_id: SID, message_id: messageId, content, agent_id: AGENT,
    ...(turnId === undefined ? {} : { turn_id: turnId }),
  })))
}

function done(messageId: string, turnId?: string) {
  act(() => useChatStore.getState().handleFrame(DoneFrame.parse({
    type: 'done', session_id: SID, message_id: messageId, stats: { tokens: 1, cost: 0 },
    ...(turnId === undefined ? {} : { turn_id: turnId }),
  })))
}

function senderPlaceholder() {
  act(() => useChatStore.getState().sendMessage('Start one answer.', { clientMessageId: INPUT }))
  expect(send, 'positive control: the real sender action reached its socket edge').toHaveBeenCalledTimes(1)
  expect(assistants()).toHaveLength(1)
  const placeholder = assistants()[0]
  expect(placeholder.content).toBe('')
  expect(placeholder.turnId).toBeUndefined()
  expect(placeholder.id, 'fixture begins with a local id, not the independently specified wire id').not.toBe(WIRE)
  return placeholder.id
}

function canonicalIdentity(order: string[]) {
  expect(assistants(), 'adoption retains one assistant, not a duplicate or missing placeholder').toHaveLength(1)
  const adopted = assistants()[0]
  expect.soft(adopted.id, 'rendered entry id must be the canonical wire message_id').toBe(WIRE)
  expect.soft(bucket().messageOrder, 'ordered keys use the same canonical id').toEqual(order)
  expect.soft(Object.keys(bucket().messagesById).sort(), 'lookup keys have no orphaned local placeholder')
    .toEqual([...order].sort())
  expect.soft(bucket().messagesById[WIRE]?.id, 'lookup entry and renderer expose the same wire identity').toBe(WIRE)
  return adopted
}

describe('wire message identity when step 2 adopts a local placeholder', () => {
  it('the sender placeholder takes the first token wire id and retains it after done', () => {
    const localId = senderPlaceholder()
    token(WIRE, ANSWER, TURN)
    const adopted = canonicalIdentity([INPUT, WIRE])
    expect(adopted.content).toBe(ANSWER)
    expect(adopted.turnId).toBe(TURN)
    expect.soft(bucket().messagesById[localId]).toBeUndefined()
    done(WIRE, TURN)
    expect(assistants()).toHaveLength(1)
    expect.soft(assistants()[0].id, 'completion must not leave the local sender id behind').toBe(WIRE)
    expect(assistants()[0]).toMatchObject({ content: ANSWER, turnId: TURN, status: 'done', isStreaming: false })
  })

  it('a tool-created placeholder rekeys its tool owner and span index without losing the call or span', () => {
    act(() => {
      useChatStore.getState().handleFrame(ToolCallStartFrame.parse({
        type: 'tool_call_start', session_id: SID, call_id: CALL, tool: 'delegate', params: {}, agent_id: AGENT,
      }))
      useChatStore.getState().handleFrame(SubagentStartFrame.parse({
        type: 'subagent_start', session_id: SID, span_id: SPAN, parent_call_id: CALL,
        task_label: 'Identity preservation fixture', agent_id: 'child-agent', child_session_id: 'child-session',
      }))
    })
    expect(assistants()).toHaveLength(1)
    const placeholder = assistants()[0]
    const localId = placeholder.id
    expect(localId).not.toBe(WIRE)
    expect(placeholder.content).toBe('')
    expect(placeholder.turnId).toBeUndefined()
    expect(placeholder.spans).toHaveLength(1)
    expect(bucket().toolCallOwnerMessageId).toEqual({ [CALL]: localId })
    expect(bucket().spanBySpanId).toEqual({ [SPAN]: { messageId: localId, spanIdx: 0 } })
    const originalSpans = structuredClone(placeholder.spans)

    token(WIRE, ANSWER, TURN)
    const adopted = canonicalIdentity([WIRE])
    expect(adopted.content).toBe(ANSWER)
    expect(adopted.spans, 'adoption preserves the actual span data').toEqual(originalSpans)
    expect.soft(bucket().toolCallOwnerMessageId, 'the pending call follows its rekeyed owner').toEqual({ [CALL]: WIRE })
    expect.soft(bucket().spanBySpanId, 'the span lookup follows the same rekeyed message').toEqual({
      [SPAN]: { messageId: WIRE, spanIdx: 0 },
    })
    expect.soft(bucket().messagesById[localId]).toBeUndefined()

    done(WIRE, TURN)
    expect(assistants()).toHaveLength(1)
    expect.soft(assistants()[0].id).toBe(WIRE)
    expect(assistants()[0]).toMatchObject({ content: ANSWER, turnId: TURN, status: 'done', isStreaming: false })
    expect(assistants()[0].tool_calls?.map((call) => call.id), 'the real done path still bakes the owned call').toEqual([CALL])
    expect(assistants()[0].spans).toEqual(originalSpans)
    expect.soft(bucket().spanBySpanId).toEqual({ [SPAN]: { messageId: WIRE, spanIdx: 0 } })
  })
})

describe('canonical identity controls outside local-placeholder adoption', () => {
  it('step 3 already rekeys an empty sender placeholder when the token has no turn id', () => {
    const localId = senderPlaceholder()
    token(WIRE, ANSWER)
    canonicalIdentity([INPUT, WIRE])
    expect(bucket().messagesById[localId]).toBeUndefined()
    done(WIRE)
    expect(assistants()).toHaveLength(1)
    expect(assistants()[0]).toMatchObject({ id: WIRE, content: ANSWER, status: 'done', isStreaming: false })
  })

  it('later real-entry segments in one turn keep their first canonical key rather than blanket rekeying', () => {
    token(WIRE, 'First ', TURN)
    token('second-real-entry', 'second.', TURN)
    expect(assistants().map(({ id, content, turnId }) => ({ id, content, turnId }))).toEqual([
      { id: WIRE, content: 'First second.', turnId: TURN },
    ])
    expect(bucket().messageOrder).toEqual([WIRE])
    expect(bucket().messagesById['second-real-entry']).toBeUndefined()
    done('second-real-entry', TURN)
    expect(assistants()[0]).toMatchObject({ id: WIRE, content: 'First second.', status: 'done', isStreaming: false })
  })

  it('a different turn reusing a real wire id cannot overwrite or append to the completed prior entry', () => {
    token(WIRE, 'First turn.', TURN)
    done(WIRE, TURN)
    const first = structuredClone(assistants()[0])
    expect(first).toMatchObject({ id: WIRE, content: 'First turn.', turnId: TURN, status: 'done' })
    token(WIRE, 'Second turn.', 'different-turn')
    expect(assistants()).toHaveLength(2)
    expect(assistants()[0]).toEqual(first)
    expect(assistants()[1].id).not.toBe(WIRE)
    expect(assistants()[1].id).not.toBe('')
    expect(assistants()[1]).toMatchObject({ content: 'Second turn.', turnId: 'different-turn', status: 'streaming' })
    done(WIRE, 'different-turn')
    expect(assistants()[0]).toEqual(first)
    expect(assistants()[1]).toMatchObject({ content: 'Second turn.', turnId: 'different-turn', status: 'done', isStreaming: false })
  })
})


// Review R1/W1 oracle: an occupied wire key cannot consume a completed answer.
const COMPLETED_ANSWER = 'Completed answer from turn A.'

function completedDestination() {
  const saved = bucket().messagesById[WIRE]
  expect(saved, 'positive control: the completed destination exists before the collision').toMatchObject({
    id: WIRE, role: 'assistant', content: COMPLETED_ANSWER, turnId: TURN, status: 'done', isStreaming: false,
  })
  return structuredClone(saved)
}

function distinctReply(saved: ReturnType<typeof completedDestination>, content: string) {
  const state = bucket()
  expect(state.messagesById[WIRE], 'an occupied destination must never overwrite the completed answer').toEqual(saved)
  expect(assistants(), 'keep the completed answer and exactly one new answer, without an orphan placeholder').toHaveLength(2)
  const replies = assistants().filter((message) => message.content === content)
  expect(replies, 'the new reply must be rendered once, not twice or dropped').toHaveLength(1)
  const reply = replies[0]
  expect(reply.id).toEqual(expect.any(String))
  expect(reply.id).not.toBe('')
  expect(reply.id, 'the new answer must not steal the completed entry key').not.toBe(WIRE)
  expect(new Set(state.messageOrder).size, 'ordered render positions have unique keys').toBe(state.messageOrder.length)
  expect(getMessages(state).map((message) => message.id), 'entry identities agree with their ordered lookup keys').toEqual(state.messageOrder)
  expect(Object.keys(state.messagesById).sort(), 'lookup has no orphan or aliased duplicate entry').toEqual([...state.messageOrder].sort())
  expect(state.messagesById[reply.id]).toEqual(reply)
  return reply
}

describe('occupied wire destinations preserve completed entries', () => {
  it('occupied step-2 destination preserves the completed entry and the new sender placeholder identity', () => {
    token(WIRE, COMPLETED_ANSWER, TURN)
    done(WIRE, TURN)
    const inputId = 'occupied-destination-input'
    const nextTurn = 'occupied-destination-next-turn'
    const nextText = 'New answer after an ordinary send.'
    act(() => useChatStore.getState().sendMessage('Continue after the completed answer.', { clientMessageId: inputId }))
    expect(send, 'the ordinary sender creates the next placeholder through its real socket edge').toHaveBeenCalledTimes(1)
    expect(assistants()).toHaveLength(2)
    const placeholder = assistants()[1]
    expect(placeholder).toMatchObject({ content: '', status: 'streaming', isStreaming: true })
    expect(placeholder.turnId, 'an unstamped current placeholder takes the step-2 adoption path').toBeUndefined()
    expect(placeholder.id).not.toBe(WIRE)
    const localId = placeholder.id
    expect(bucket().messageOrder).toEqual([WIRE, inputId, localId])
    const saved = completedDestination()

    token(WIRE, nextText, nextTurn)
    expect(distinctReply(saved, nextText)).toMatchObject({ id: localId, turnId: nextTurn, isStreaming: true })
    expect(bucket().messageOrder).toEqual([WIRE, inputId, localId])
    done(WIRE, nextTurn)
    expect(distinctReply(saved, nextText)).toMatchObject({ id: localId, turnId: nextTurn, status: 'done', isStreaming: false })
  })

  it('W1 step-3 occupied destination preserves W and gives turn C one distinct reply instead of rendering it twice', () => {
    token(WIRE, COMPLETED_ANSWER, TURN)
    done(WIRE, TURN)
    const placeholderId = 'empty-superseded-turn-b-placeholder'
    const placeholderTurn = 'superseded-turn-b'
    const incomingTurn = 'incoming-turn-c'
    const replyText = 'New reply belonging only to turn C.'
    act(() => {
      useChatStore.getState().appendMessage({
        id: placeholderId, role: 'assistant', content: '', timestamp: '2026-10-22T00:00:00Z',
        status: 'streaming', isStreaming: true, agentId: AGENT, turnId: placeholderTurn,
      })
      useChatStore.setState((state) => ({
        sessionsById: { ...state.sessionsById, [SID]: { ...state.sessionsById[SID], isStreaming: true } },
        isStreaming: true,
      }))
    })
    expect(bucket().messageOrder, 'W1 fixture is exactly [completed W, empty P]').toEqual([WIRE, placeholderId])
    expect(assistants().map(({ id, content, turnId, status, isStreaming }) => ({ id, content, turnId, status, isStreaming })))
      .toEqual([
        { id: WIRE, content: COMPLETED_ANSWER, turnId: TURN, status: 'done', isStreaming: false },
        { id: placeholderId, content: '', turnId: placeholderTurn, status: 'streaming', isStreaming: true },
      ])
    const saved = completedDestination()

    token(WIRE, replyText, incomingTurn)
    const reply = distinctReply(saved, replyText)
    expect(reply).toMatchObject({ turnId: incomingTurn, isStreaming: true })
    expect(reply.id, 'the abandoned turn-B placeholder is not the new turn-C entry').not.toBe(placeholderId)
    expect(bucket().messageOrder, 'only the saved answer and one new answer remain').toEqual([WIRE, reply.id])
    expect(bucket().messagesById[placeholderId], 'abandoned P is removed from the bucket').toBeUndefined()
    done(WIRE, incomingTurn)
    expect(distinctReply(saved, replyText)).toMatchObject({ id: reply.id, turnId: incomingTurn, status: 'done', isStreaming: false })
    expect(bucket().messageOrder).toEqual([WIRE, reply.id])
    expect(bucket().messagesById[placeholderId]).toBeUndefined()
  })
})
