// #1379: passive system entries are presentation, not turn boundaries.
// Oracles: the frontend dispatch requirements 1–3 and lifecycle-design-b.md's
// "Live UI and wire ruling". Store, history adapter, and system renderer are
// real; only the HTTP edge is stubbed. Existing dedup/user-bake cases are
// controls, not claims that those behaviors were broken before this fix.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { AskUserQuestionCard, ReplayMessageFrame } from '@/lib/api/generated/asyncapi-types'
import type { Message as WireMessage } from '@/lib/api/generated/openapi-types'
import { fetchSessionMessages } from '@/lib/api/sessions'
import { VirtualSystemMessageRow } from '@/components/chat/SystemMessageRows'
import { useChatStore } from './chat'
import { getMessages } from './chat/messages'
import { emptySessionState } from './chat/session'
import { sawReplayMessageThisTurn } from './chat/runtime-state'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'

const requestMock = vi.hoisted(() => vi.fn())
vi.mock('@/lib/api/http', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api/http')>(),
  request: requestMock,
}))

const SID = 'passive-system-session'
const AGENT = 'mia'
const ASSISTANT_ID = 'assistant-live'
const CALL_ID = 'tool-live'
const NOTICE_ID = 'saved-input-retention-expiry:report-1'
const NOTICE_TEXT = 'The helper report expired before delivery because of session retention.'
const TIMESTAMP = '2026-10-21T10:00:00Z'

function notice(id = NOTICE_ID): ReplayMessageFrame {
  return {
    type: 'replay_message', session_id: SID, role: 'system',
    id, content: NOTICE_TEXT, timestamp: TIMESTAMP, agent_id: AGENT,
  }
}

function bucket() {
  return useChatStore.getState().sessionsById[SID]
}

function startTool() {
  useChatStore.setState({
    sessionsById: {
      [SID]: {
        ...emptySessionState(), activeTurnId: 'turn-live',
        activeTurnAgentId: AGENT, isStreaming: true,
        replayCompletedForSession: SID,
      },
    },
  })
  useChatStore.getState().handleFrame({
    type: 'token', session_id: SID, message_id: ASSISTANT_ID,
    turn_id: 'turn-live', agent_id: AGENT, content: 'Checking the file.',
  })
  useChatStore.getState().handleFrame({
    type: 'tool_call_start', session_id: SID, call_id: CALL_ID,
    agent_id: AGENT, tool: 'read_file', params: { path: 'report.txt' },
  })
}

function systemRows() {
  return getMessages(bucket()).filter((message) => message.role === 'system').map((message) => (
    <VirtualSystemMessageRow key={message.id} message={message} />
  ))
}

beforeEach(() => {
  requestMock.mockReset()
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: AGENT })
  useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {}, pendingFirstSend: null })
  useChatStore.getState().setMessages([])
  delete sawReplayMessageThisTurn[SID]
})

describe('passive system replay entries (#1379)', () => {
  it.each([NOTICE_ID, 'ordinary-system-notice'])('preserves the running turn and pending tools for %s', (id) => {
    startTool()
    const before = structuredClone(bucket())
    // Nonempty real tool state makes clearing/baking observable.
    expect(before.toolCallOrder).toEqual([CALL_ID])
    expect(before.messagesById[ASSISTANT_ID].isStreaming).toBe(true)

    useChatStore.getState().handleFrame(notice(id))

    expect(bucket()).toEqual({
      ...before,
      messagesById: {
        ...before.messagesById,
        [id]: { id, role: 'system', content: NOTICE_TEXT, timestamp: TIMESTAMP, status: 'done', agentId: AGENT },
      },
      messageOrder: [...before.messageOrder, id],
      // Receiving a stored row may advance transport bookkeeping, not the turn.
      lastReceivedEventTime: TIMESTAMP,
    })
    expect(useChatStore.getState().toolCalls).toEqual(before.toolCalls)
    expect(useChatStore.getState().isStreaming).toBe(true)
  })

  it('leaves a parked question and its idle turn unchanged', () => {
    const card: AskUserQuestionCard = {
      card_id: 'question-1', session_id: SID, agent_id: AGENT,
      status: 'pending', created_at: TIMESTAMP,
      questions: [{ header: 'Destination', question: 'Where should the report go?', options: [{ label: 'Keep here' }] }],
    }
    useChatStore.getState().handleFrame({ type: 'ask_user_question', card })
    const before = structuredClone(bucket())

    useChatStore.getState().handleFrame(notice())

    expect(bucket().pendingAsk).toEqual(card)
    expect(useChatStore.getState().pendingAsk).toEqual(card)
    expect(bucket().isStreaming).toBe(before.isStreaming)
    expect(bucket().activeTurnId).toBe(before.activeTurnId)
    expect(bucket().activeTurnAgentId).toBe(before.activeTurnAgentId)
  })

  it.each([false, true])('does not manufacture or clear replay state when replaying=%s', (isReplaying) => {
    useChatStore.setState({
      sessionsById: {
        [SID]: { ...emptySessionState(), isReplaying, awaitingCatchUp: isReplaying, replayCompletedForSession: SID },
      },
    })
    sawReplayMessageThisTurn[SID] = isReplaying

    useChatStore.getState().handleFrame(notice())

    expect(sawReplayMessageThisTurn[SID]).toBe(isReplaying)
    expect(bucket().isReplaying).toBe(isReplaying)
    expect(bucket().awaitingCatchUp).toBe(isReplaying)
    expect(bucket().replayCompletedForSession).toBe(SID)
  })

  it('renders one stable notice after live, catch-up, and cold-history reload', async () => {
    useChatStore.getState().handleFrame(notice())
    // Put another entry after the notice: tail/content dedup cannot pass this.
    useChatStore.getState().handleFrame({
      type: 'replay_message', session_id: SID, role: 'user',
      id: 'later-user', content: 'Continue.', timestamp: TIMESTAMP,
    })
    useChatStore.getState().handleFrame({ ...notice(), timestamp: '2026-10-21T10:00:01Z' })
    useChatStore.getState().handleFrame({
      type: 'catch_up_complete', session_id: SID, boot_id: 'boot-1', seq: 1, mode: 'incremental',
    })

    expect(bucket().messageOrder).toEqual([NOTICE_ID, 'later-user'])
    const view = render(<>{systemRows()}</>)
    expect(screen.getAllByText(NOTICE_TEXT)).toHaveLength(1)
    expect(view.container.querySelectorAll(`[data-message-id="${NOTICE_ID}"]`)).toHaveLength(1)

    const storedNotice: WireMessage = {
      id: NOTICE_ID, role: 'system', type: 'system', agent_id: AGENT,
      content: NOTICE_TEXT, timestamp: TIMESTAMP, status: 'ok', view_membership: 'chat',
    }
    requestMock.mockResolvedValueOnce([storedNotice])
    // A reload discards local memory; the generated REST schema and adapter
    // must preserve the same identity before a later live/catch-up delivery.
    useChatStore.setState({ sessionsById: {} })
    useChatStore.getState().setMessages(await fetchSessionMessages(SID))
    useChatStore.getState().handleFrame(notice())
    useChatStore.getState().handleFrame(notice())
    view.rerender(<>{systemRows()}</>)

    expect(bucket().messageOrder).toEqual([NOTICE_ID])
    expect(Object.keys(bucket().messagesById)).toEqual([NOTICE_ID])
    expect(bucket().messagesById[NOTICE_ID]).toMatchObject({ role: 'system', content: NOTICE_TEXT, agentId: AGENT })
    expect(screen.getAllByText(NOTICE_TEXT)).toHaveLength(1)
    expect(view.container.querySelectorAll(`[data-message-id="${NOTICE_ID}"]`)).toHaveLength(1)
  })

  it('does not reconcile a passive system row against a pending user bubble', () => {
    const pendingUser = {
      id: 'client-pending', role: 'user' as const, content: 'Still queued.',
      timestamp: TIMESTAMP, status: 'done' as const, deliveryStatus: 'queued' as const,
    }
    useChatStore.setState({
      sessionsById: {
        [SID]: { ...emptySessionState(), messagesById: { [pendingUser.id]: pendingUser }, messageOrder: [pendingUser.id] },
      },
    })

    useChatStore.getState().handleFrame({ ...notice(), client_message_id: pendingUser.id })

    expect(bucket().messageOrder).toEqual([NOTICE_ID, pendingUser.id])
    expect(bucket().messagesById[pendingUser.id]).toEqual(pendingUser)
    expect(bucket().messagesById[NOTICE_ID]).toEqual({
      id: NOTICE_ID, role: 'system', content: NOTICE_TEXT,
      timestamp: TIMESTAMP, status: 'done', agentId: AGENT,
    })
  })

  it('keeps equal notice text with different stable IDs as separate rows', () => {
    useChatStore.getState().handleFrame(notice())
    const secondId = 'saved-input-retention-expiry:report-2'
    useChatStore.getState().handleFrame(notice(secondId))

    expect(bucket().messageOrder).toEqual([NOTICE_ID, secondId])
    render(<>{systemRows()}</>)
    expect(screen.getAllByText(NOTICE_TEXT)).toHaveLength(2)
  })

  it('still bakes historical pending tools at a user replay boundary', () => {
    startTool()
    useChatStore.getState().handleFrame({
      type: 'replay_message', session_id: SID, role: 'user',
      id: 'next-user', content: 'Next request.', timestamp: TIMESTAMP,
    })

    expect(bucket().toolCalls).toEqual({})
    expect(bucket().toolCallOrder).toEqual([])
    expect(bucket().toolCallOwnerMessageId).toEqual({})
    expect(bucket().messagesById[ASSISTANT_ID].tool_calls?.map((call) => call.id)).toEqual([CALL_ID])
  })
})
