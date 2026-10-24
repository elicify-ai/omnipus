import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { getMessages, makeBucketMessages, useChatStore } from './chat'
import { emptySessionState } from './chat/session'
import type { ChatMessage, SessionChatState } from './chat'
import { queryClient } from '@/lib/queryClient'
import { useUiStore } from './ui'
import { seedCanonicalSession } from '../../tests/unit/fixtures/canonical-session'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useWorkspacesStore } from './workspacesStore'
import type { WsConnection } from '@/lib/ws'

const TEST_SESSION_ID = 'test-session-1'

function resetStore() {
  queryClient.clear()
  act(() => {
    useChatStore.getState().clearStreamingState()
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useSessionStore.setState({ ...useSessionStore.getInitialState(), activeSessionId: TEST_SESSION_ID }, true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useUiStore.setState(useUiStore.getInitialState(), true)
  })
}

beforeEach(resetStore)

// ── M4 (BLOCKER 1): workspace→turn binding ───────────────────────────────────
// Traces to: wave5a-wire-ui-spec.md M4 — a chat sent inside a workspace must
// stamp metadata.workspace_id so created/delegated tasks land on THIS workspace.
describe('chat store — M4 workspace→turn binding (metadata.workspace_id)', () => {
  it('attaches metadata.workspace_id when chatting inside a workspace', () => {
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'general-assistant' })
      seedCanonicalSession(TEST_SESSION_ID, 'general-assistant', '01JXWORKSPACE0000000000001')
      useWorkspacesStore.setState({ activeWorkspaceId: '01JXWORKSPACE0000000000001' })
      useChatStore.getState().sendMessage('do this in the workspace')
    })
    expect(mockSend).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'message',
        content: 'do this in the workspace',
        metadata: expect.objectContaining({ workspace_id: '01JXWORKSPACE0000000000001' }),
      }),
    )
  })

  it('omits metadata entirely on the global (non-workspace) chat', () => {
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'general-assistant' })
      seedCanonicalSession(TEST_SESSION_ID, 'general-assistant')
      useWorkspacesStore.setState({ activeWorkspaceId: null })
      useChatStore.getState().sendMessage('global chat, no workspace')
    })
    const payload = mockSend.mock.calls[0]?.[0] as Record<string, unknown>
    expect(payload).toMatchObject({ type: 'message', content: 'global chat, no workspace' })
    // No workspace and no per-turn model → metadata is omitted, matching the
    // backend's default-workspace fallback.
    expect(payload).not.toHaveProperty('metadata')
  })

  it('merges workspace_id with a per-turn model_name override', () => {
    const mockSend = vi.fn().mockReturnValue(true)
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
      useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: 'general-assistant' })
      seedCanonicalSession(TEST_SESSION_ID, 'general-assistant', '01JXWORKSPACE0000000000002')
      useWorkspacesStore.setState({ activeWorkspaceId: '01JXWORKSPACE0000000000002' })
      useChatStore.getState().sendMessage('use a specific model here', { model_name: 'z-ai/glm-5-turbo' })
    })
    expect(mockSend).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'message',
        metadata: expect.objectContaining({
          workspace_id: '01JXWORKSPACE0000000000002',
          model_name: 'z-ai/glm-5-turbo',
        }),
      }),
    )
  })
})


// Oracle: chief addenda 1/3 and QA-UNIT-REQUEST.md; a saved session's identity never comes from ambient navigation.
const OWNER = 'canonical-owner'
const WORKSPACE = 'canonical-workspace'
const AMBIENT = 'unrelated-ambient-workspace'
const MODES = [
  { label: 'ordinary send', streaming: false, source: 'detail' as const },
  { label: 'steering send', streaming: true, source: 'list' as const },
]

function bucketFor(messages: ChatMessage[], streaming = false): SessionChatState {
  return {
    ...emptySessionState(),
    ...makeBucketMessages(messages), toolCalls: {}, toolCallOrder: [], textAtToolCallStart: {},
    isStreaming: streaming, isReplaying: false, replayCompletedForSession: null,
    sessionTokens: 0, sessionCost: 0, rateLimitEvent: null, lastUserMessageAt: null,
    cancelStage: null, lastReceivedEventTime: null,
  }
}

function prepareSender(streaming = false) {
  const send = vi.fn<WsConnection['send']>().mockReturnValue(true)
  act(() => {
    useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: OWNER })
    useWorkspacesStore.setState({ activeWorkspaceId: AMBIENT })
    useChatStore.setState({ sessionsById: { [TEST_SESSION_ID]: bucketFor([], streaming) }, isStreaming: streaming })
    useConnectionStore.setState({
      connection: { send, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
      isConnected: true,
    })
  })
  return send
}

function keptMessage(id = 'kept-user'): ChatMessage {
  return {
    id, session_id: TEST_SESSION_ID, role: 'user', content: 'Keep the exact original question.  ',
    timestamp: '2026-10-22T00:00:00Z', status: 'error', deliveryStatus: 'failed',
    mediaRefs: ['media://original-image'],
    media: [{ type: 'image', url: '/api/v1/uploads/original.png', filename: 'original.png', contentType: 'image/png' }],
  }
}

describe('canonical outbound session identity', () => {
  it.each(MODES)('$label carries canonical workspace A, never ambient B', ({ streaming, source }) => {
    seedCanonicalSession(TEST_SESSION_ID, OWNER, WORKSPACE, source)
    const send = prepareSender(streaming)
    act(() => useChatStore.getState().sendMessage('canonical question', { clientMessageId: 'canonical-input' }))
    expect(send).toHaveBeenCalledTimes(1)
    expect(send.mock.calls[0][0]).toMatchObject({
      type: 'message', session_id: TEST_SESSION_ID, client_message_id: 'canonical-input', content: 'canonical question',
    })
    expect(send.mock.calls[0][0]).toHaveProperty('metadata', { workspace_id: WORKSPACE })
  })

  it.each(MODES)('$label omits workspace for a known workspace-less session despite ambient B', ({ streaming, source }) => {
    seedCanonicalSession(TEST_SESSION_ID, OWNER, undefined, source)
    const send = prepareSender(streaming)
    act(() => useChatStore.getState().sendMessage('global question', { clientMessageId: 'global-input' }))
    expect(send).toHaveBeenCalledTimes(1)
    expect(send.mock.calls[0][0]).toMatchObject({
      type: 'message', session_id: TEST_SESSION_ID, client_message_id: 'global-input', content: 'global question',
    })
    expect(send.mock.calls[0][0]).not.toHaveProperty('metadata.workspace_id')
  })

  it.each(MODES)('$label omits unknown canonical workspace instead of guessing ambient B', ({ streaming }) => {
    const send = prepareSender(streaming)
    act(() => useChatStore.getState().sendMessage('metadata not loaded', { clientMessageId: 'unknown-input' }))
    expect(send).toHaveBeenCalledTimes(1)
    expect(send.mock.calls[0][0]).toMatchObject({
      type: 'message', session_id: TEST_SESSION_ID, client_message_id: 'unknown-input', content: 'metadata not loaded',
    })
    expect(send.mock.calls[0][0]).not.toHaveProperty('metadata.workspace_id')
  })

  it('background Retry keeps target A owner/workspace, original media/ID and one bubble while B is foreground', () => {
    seedCanonicalSession(TEST_SESSION_ID, OWNER, WORKSPACE)
    seedCanonicalSession('foreground-session', 'foreground-agent', AMBIENT)
    const send = prepareSender()
    const original = keptMessage()
    const foreground = { ...keptMessage('foreground-user'), session_id: 'foreground-session', content: 'unrelated foreground' }
    act(() => {
      useSessionStore.setState({ activeSessionId: 'foreground-session', activeAgentId: 'foreground-agent' })
      useChatStore.setState({ sessionsById: {
        [TEST_SESSION_ID]: { ...bucketFor([original]), unansweredLastUserMessageId: original.id },
        'foreground-session': bucketFor([foreground]),
      } })
      useChatStore.getState().resendMessage(original.id)
    })
    expect(send.mock.calls).toEqual([[{
      type: 'message', session_id: TEST_SESSION_ID, client_message_id: original.id,
      content: original.content, agent_id: OWNER, media: ['media://original-image'],
      metadata: { workspace_id: WORKSPACE },
    }]])
    const state = useChatStore.getState()
    expect(getMessages(state.sessionsById[TEST_SESSION_ID])).toEqual([
      { ...original, status: 'done', deliveryStatus: 'sending' },
    ])
    expect(state.sessionsById[TEST_SESSION_ID].unansweredLastUserMessageId).toBeNull()
    expect(getMessages(state.sessionsById['foreground-session'])).toEqual([foreground])
    expect(useSessionStore.getState().activeSessionId).toBe('foreground-session')
  })

  it.each(['missing', 'blank'] as const)('Retry with %s canonical owner refuses visibly without changing failed/unanswered state', (kind) => {
    if (kind === 'blank') seedCanonicalSession(TEST_SESSION_ID, '', WORKSPACE)
    const send = prepareSender()
    const original = keptMessage()
    act(() => {
      useChatStore.setState({ sessionsById: {
        [TEST_SESSION_ID]: { ...bucketFor([original]), unansweredLastUserMessageId: original.id },
      } })
      useChatStore.getState().resendMessage(original.id)
    })
    expect(send).not.toHaveBeenCalled()
    expect(getMessages(useChatStore.getState().sessionsById[TEST_SESSION_ID])).toEqual([original])
    expect(useChatStore.getState().sessionsById[TEST_SESSION_ID].unansweredLastUserMessageId).toBe(original.id)
    expect(useUiStore.getState().toasts).toEqual([expect.objectContaining({ variant: 'error', message: expect.any(String) })])
    expect(useUiStore.getState().toasts[0].message.trim()).not.toBe('')
  })

  it.each(['Ask again', 'Generate again'] as const)('%s requests an answer in the attached session workspace, not ambient B', (action) => {
    seedCanonicalSession(TEST_SESSION_ID, OWNER, WORKSPACE)
    const send = prepareSender()
    const original = { ...keptMessage(), status: 'done' as const, deliveryStatus: 'received' as const,
      firstSendStatus: 'answer_not_started' as const }
    act(() => {
      useChatStore.setState({ sessionsById: {
        [TEST_SESSION_ID]: { ...bucketFor([original]), unansweredLastUserMessageId: original.id },
      } })
      if (action === 'Ask again') useChatStore.getState().resendMessage(original.id)
      else useChatStore.getState().generateFirstSendAgain(original.id)
    })
    expect(send).toHaveBeenCalledTimes(1)
    const payload = send.mock.calls[0][0]
    expect(payload).toMatchObject({
      type: 'message', session_id: TEST_SESSION_ID, agent_id: OWNER,
      content: original.content, media: ['media://original-image'], metadata: { workspace_id: WORKSPACE },
    })
    expect(payload).toHaveProperty('client_message_id', expect.any(String))
    if (payload.type !== 'message') throw new Error('BLOCKED: answer action did not send a MessageFrame')
    expect(payload.client_message_id).not.toBe('')
    if (action === 'Ask again') expect(payload.client_message_id).toBe(original.id)
    else expect(payload.client_message_id).not.toBe(original.id)
    expect(getMessages(useChatStore.getState().sessionsById[TEST_SESSION_ID]).filter((message) => message.id === original.id))
      .toHaveLength(1)
    expect(useSessionStore.getState().activeSessionId).toBe(TEST_SESSION_ID)
  })
})
