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

// ── ADR-092 UX fix: pendingAutoApproveChoice — the composer toggle works
// before the first message ────────────────────────────────────────────────
//
// A brand-new chat has no server-known session yet, so the toggle can't
// send session_mode_update directly (see AutoApprovePicker.tsx). Instead it
// records ChatStore.pendingAutoApproveChoice, and the frame slice's
// session_started case (src/store/chat/slices/frames.ts) flushes it as a
// real session_mode_update the moment the server mints the real session id
// — BEFORE session_started's bucket-migration logic runs, in effect
// alongside the first turn's own dispatch.
describe('chat store — pendingAutoApproveChoice flushed by session_started (ADR-092 UX fix)', () => {
  function resetPendingChoiceScenario() {
    act(() => {
      useChatStore.getState().clearStreamingState()
      useChatStore.setState({ sessionsById: {}, pendingAutoApproveChoice: null })
      useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
      useSessionStore.setState({ activeSessionId: null, activeAgentId: null, activeAgentType: null })
      useWorkspacesStore.setState({ activeWorkspaceId: null })
    })
  }

  it('sends session_mode_update for the newly-minted session id and clears the pending choice', () => {
    resetPendingChoiceScenario()
    const mockSend = vi.fn()
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
      useChatStore.getState().setPendingAutoApproveChoice(true)
    })

    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: 'sess_new_1' })
    })

    expect(mockSend).toHaveBeenCalledWith({
      type: 'session_mode_update',
      session_id: 'sess_new_1',
      auto_approve: true,
    })
    expect(useChatStore.getState().pendingAutoApproveChoice).toBeNull()
  })

  it(
    'founder-ruling proof (2026-09-24): flip Auto on in a brand-new chat, send the first message, and the ' +
      'session_mode_update frame reaches the wire as the very next send after session_started — before ' +
      'anything else the client does — proving the choice is in flight to the server ahead of the first ' +
      'turn being able to decide any tool call',
    () => {
      resetPendingChoiceScenario()
      const mockSend = vi.fn((_frame: unknown) => true)
      act(() => {
        useConnectionStore.setState({
          connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
          isConnected: true,
        })
        useSessionStore.setState({ activeAgentId: 'mia' })
      })

      // 1) User flips Auto on BEFORE typing anything — no session exists yet.
      //    Per the founder's ruling, this must send NOTHING to the server.
      act(() => {
        useChatStore.getState().setPendingAutoApproveChoice(true)
      })
      expect(mockSend).not.toHaveBeenCalled()

      // 2) User sends the first message. This goes out with no session_id —
      //    the server will mint one and ack with session_started.
      act(() => {
        useChatStore.getState().sendMessage('hello')
      })
      expect(mockSend).toHaveBeenCalledTimes(1)
      expect(mockSend.mock.calls[0][0]).toMatchObject({ type: 'message', content: 'hello' })

      // 3) The server's session_started ack arrives. The pending choice must
      //    be flushed as session_mode_update as the very next frame sent —
      //    synchronously inside this same frame handler, not deferred — so
      //    it is already in flight before the agent loop (a separate
      //    goroutine, gated on an LLM round-trip before any tool call can be
      //    decided — see AutoApprovePicker.tsx's doc comment) reaches its
      //    first tool-call approval check.
      act(() => {
        useChatStore.getState().handleFrame({ type: 'session_started', session_id: 'sess_new_founder_proof' })
      })

      expect(mockSend).toHaveBeenCalledTimes(2)
      expect(mockSend.mock.calls[1][0]).toEqual({
        type: 'session_mode_update',
        session_id: 'sess_new_founder_proof',
        auto_approve: true,
      })
      expect(useChatStore.getState().pendingAutoApproveChoice).toBeNull()
    },
  )

  it('flipping the switch and never sending a message sends nothing to the server at all', () => {
    resetPendingChoiceScenario()
    const mockSend = vi.fn(() => true)
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
      useChatStore.getState().setPendingAutoApproveChoice(true)
      useChatStore.getState().setPendingAutoApproveChoice(false)
      useChatStore.getState().setPendingAutoApproveChoice(true)
    })

    expect(mockSend).not.toHaveBeenCalled()
  })

  it('carries a false choice the same way', () => {
    resetPendingChoiceScenario()
    const mockSend = vi.fn()
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
      useChatStore.getState().setPendingAutoApproveChoice(false)
    })

    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: 'sess_new_2' })
    })

    expect(mockSend).toHaveBeenCalledWith({
      type: 'session_mode_update',
      session_id: 'sess_new_2',
      auto_approve: false,
    })
    expect(useChatStore.getState().pendingAutoApproveChoice).toBeNull()
  })

  it('sends nothing extra when there is no pending choice — an ordinary first message is unaffected', () => {
    resetPendingChoiceScenario()
    const mockSend = vi.fn()
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
    })

    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: 'sess_new_3' })
    })

    expect(mockSend).not.toHaveBeenCalled()
  })

  it('startNewSession ("New chat") clears an abandoned pending choice so it never leaks onto the next chat', () => {
    resetPendingChoiceScenario()
    act(() => {
      useChatStore.getState().setPendingAutoApproveChoice(true)
    })
    expect(useChatStore.getState().pendingAutoApproveChoice).toBe(true)

    act(() => {
      useSessionStore.getState().startNewSession()
    })

    expect(useChatStore.getState().pendingAutoApproveChoice).toBeNull()
  })

  it('attachToSession (picking an existing chat) clears an abandoned pending choice', () => {
    resetPendingChoiceScenario()
    const mockSend = vi.fn(() => true)
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
      useChatStore.getState().setPendingAutoApproveChoice(true)
    })
    expect(useChatStore.getState().pendingAutoApproveChoice).toBe(true)

    act(() => {
      useSessionStore.getState().attachToSession('sess_existing_1', 'chat')
    })

    expect(useChatStore.getState().pendingAutoApproveChoice).toBeNull()
  })
})
