// ADR-091: session_mode_update (client → server) send, and the
// session_mode_updated (server → client) ack's effect on per-session state.
//
// Mirrors the existing cancelStream/cancel_stage test pattern in chat.test.ts
// (same file, "cancelStream calls connection.send" / "cancel_stage frame
// handling" describe blocks) — same store, same connection mock shape.

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
    useChatStore.getState().clearStreamingState()
    useChatStore.setState({ sessionsById: {} })
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
    useSessionStore.setState({ activeSessionId: TEST_SESSION_ID, activeAgentId: null, activeAgentType: null })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })
}

beforeEach(resetStore)

describe('chat store — sendSessionModeUpdate (ADR-091, human-only per-chat Auto-approve)', () => {
  it('sends a session_mode_update frame with auto_approve: true', () => {
    const mockSend = vi.fn()
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
      useChatStore.getState().sendSessionModeUpdate(TEST_SESSION_ID, true)
    })
    expect(mockSend).toHaveBeenCalledWith({ type: 'session_mode_update', session_id: TEST_SESSION_ID, auto_approve: true })
  })

  it('sends a session_mode_update frame with auto_approve: false', () => {
    const mockSend = vi.fn()
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
      useChatStore.getState().sendSessionModeUpdate(TEST_SESSION_ID, false)
    })
    expect(mockSend).toHaveBeenCalledWith({ type: 'session_mode_update', session_id: TEST_SESSION_ID, auto_approve: false })
  })

  it('sends a session_mode_update frame with auto_approve: null (clears the modifier, reverts to inherited)', () => {
    const mockSend = vi.fn()
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
      useChatStore.getState().sendSessionModeUpdate(TEST_SESSION_ID, null)
    })
    expect(mockSend).toHaveBeenCalledWith({ type: 'session_mode_update', session_id: TEST_SESSION_ID, auto_approve: null })
  })

  it('surfaces a connection error and does not throw when there is no connection', () => {
    act(() => {
      useChatStore.getState().sendSessionModeUpdate(TEST_SESSION_ID, true)
    })
    expect(useConnectionStore.getState().connectionError).toMatch(/not connected/i)
  })

  it('surfaces a connection error when send() reports the connection dropped', () => {
    const mockSend = vi.fn(() => false)
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
      useChatStore.getState().sendSessionModeUpdate(TEST_SESSION_ID, true)
    })
    expect(useConnectionStore.getState().connectionError).toMatch(/connection dropped/i)
  })
})

describe('chat store — session_mode_updated frame (ADR-091 ack)', () => {
  it('sets autoApproveEffective on the target session bucket and syncs the foreground field', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_mode_updated',
        session_id: TEST_SESSION_ID,
        auto_approve_effective: true,
      })
    })
    expect(useChatStore.getState().sessionsById[TEST_SESSION_ID]?.autoApproveEffective).toBe(true)
    expect(useChatStore.getState().autoApproveEffective).toBe(true)
  })

  it('resolves auto_approve_effective: false the same way', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_mode_updated',
        session_id: TEST_SESSION_ID,
        auto_approve_effective: false,
      })
    })
    expect(useChatStore.getState().sessionsById[TEST_SESSION_ID]?.autoApproveEffective).toBe(false)
  })

  it('is null before any ack has arrived for a fresh session', () => {
    expect(useChatStore.getState().sessionsById[TEST_SESSION_ID]).toBeUndefined()
  })
})
