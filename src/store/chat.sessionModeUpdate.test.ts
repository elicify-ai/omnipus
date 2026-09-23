// ADR-092: session_mode_update (client → server) send, and the
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
import { emptySessionState } from './chat/session'
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

describe('chat store — sendSessionModeUpdate (ADR-092, human-only per-chat Auto-approve)', () => {
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

describe('chat store — session_mode_updated frame (ADR-092 ack)', () => {
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

describe('chat store — session_state reconnect/reload snapshot carries the per-chat modifier (ADR-092 review finding D)', () => {
  // A plain WS reconnect or page reload never re-arrives as a fresh
  // session_mode_updated ack — only a LIVE session_mode_update send
  // produces that frame. Before this fix, reloading a page (or a gateway
  // restart clearing the server's in-memory modifier) silently dropped the
  // chat's per-chat Auto-approve state from the UI even though the server
  // still held it. SessionStateFrame.auto_approve_modifier is the field
  // that actually survives a reload/reconnect.
  function sessionStateFrame(overrides: { session_id?: string; auto_approve_modifier?: boolean | null } = {}) {
    return {
      type: 'session_state' as const,
      user_id: 'user-1',
      emitted_at: new Date().toISOString(),
      pending_approvals: [],
      session_id: TEST_SESSION_ID,
      ...overrides,
    }
  }

  it('a snapshot with auto_approve_modifier: true shows Auto on for that session', () => {
    act(() => {
      useChatStore.getState().handleFrame(sessionStateFrame({ auto_approve_modifier: true }))
    })
    expect(useChatStore.getState().sessionsById[TEST_SESSION_ID]?.autoApproveEffective).toBe(true)
  })

  it('a snapshot with auto_approve_modifier: null CLEARS a stale local true (e.g. after a gateway restart)', () => {
    // Simulate a stale local value from before reload/restart — the modifier
    // lived only in server memory and is gone now, so the snapshot reports
    // null and the SPA must follow, not keep showing the old "on".
    act(() => {
      useChatStore.setState((s) => ({
        sessionsById: {
          ...s.sessionsById,
          [TEST_SESSION_ID]: { ...emptySessionState(), ...(s.sessionsById[TEST_SESSION_ID] ?? {}), autoApproveEffective: true },
        },
      }))
    })
    expect(useChatStore.getState().sessionsById[TEST_SESSION_ID]?.autoApproveEffective).toBe(true)

    act(() => {
      useChatStore.getState().handleFrame(sessionStateFrame({ auto_approve_modifier: null }))
    })
    expect(useChatStore.getState().sessionsById[TEST_SESSION_ID]?.autoApproveEffective).toBeNull()
  })

  it('a snapshot with auto_approve_modifier absent also clears a stale local true', () => {
    act(() => {
      useChatStore.setState((s) => ({
        sessionsById: {
          ...s.sessionsById,
          [TEST_SESSION_ID]: { ...emptySessionState(), ...(s.sessionsById[TEST_SESSION_ID] ?? {}), autoApproveEffective: true },
        },
      }))
    })

    act(() => {
      useChatStore.getState().handleFrame(sessionStateFrame())
    })
    expect(useChatStore.getState().sessionsById[TEST_SESSION_ID]?.autoApproveEffective).toBeNull()
  })

  it('does not touch autoApproveEffective when the frame carries no session_id (the connection-open emit)', () => {
    act(() => {
      useChatStore.setState((s) => ({
        sessionsById: {
          ...s.sessionsById,
          [TEST_SESSION_ID]: { ...emptySessionState(), ...(s.sessionsById[TEST_SESSION_ID] ?? {}), autoApproveEffective: true },
        },
      }))
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_state',
        user_id: 'user-1',
        emitted_at: new Date().toISOString(),
        pending_approvals: [],
      })
    })
    // The connection-open emit carries neither session_id nor a meaningful
    // modifier — must not clobber whatever the active session already knew.
    expect(useChatStore.getState().sessionsById[TEST_SESSION_ID]?.autoApproveEffective).toBe(true)
  })
})
