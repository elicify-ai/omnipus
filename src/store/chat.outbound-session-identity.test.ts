import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useWorkspacesStore } from './workspacesStore'
import type { WsConnection } from '@/lib/ws'

const TEST_SESSION_ID = 'test-session-1'

function resetStore() {
  act(() => {
    // F-S3: clearStreamingState() also drains the module-scoped
    // pendingCancelAckSids tracker (see chat.ts) — call it before rebuilding
    // state so a cancelStream(sessionId) from a PRIOR test can never bleed
    // into this test's missing-session_id frame-routing fallback.
    useChatStore.getState().clearStreamingState()
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      isStreaming: false,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      sessionTokens: 0,
      sessionCost: 0,
      isReplaying: false,
      replayCompletedForSession: null,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
    })
    useConnectionStore.setState({
      connection: null,
      isConnected: false,
      connectionError: null,
    })
    useSessionStore.setState({
      activeSessionId: TEST_SESSION_ID,
      activeAgentId: null,
      activeAgentType: null,
    })
    // M4: default to "not in a workspace" so the global-chat assertions hold
    // unless a test explicitly sets an active workspace.
    useWorkspacesStore.setState({ activeWorkspaceId: null })
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

