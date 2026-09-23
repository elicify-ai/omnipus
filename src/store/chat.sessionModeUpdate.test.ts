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

// ── ADR-092 founder ruling (2026-09-24): pendingAutoApproveChoice rides the
// FIRST MESSAGE itself ──────────────────────────────────────────────────────
//
// A brand-new chat has no server-known session yet, so the toggle can't
// send session_mode_update directly (see AutoApprovePicker.tsx). It records
// ChatStore.pendingAutoApproveChoice instead. That choice is no longer
// flushed as a SEPARATE session_mode_update after the session_started ack —
// a round trip that could race the agent loop's own first LLM call and let
// the new chat's first tool call be decided under the wrong mode.
// `sendMessage`'s no-active-session branch
// (src/store/chat/slices/outbound-lifecycle.ts) now sends it as
// `auto_approve` ON the very message frame that mints the session, so the
// server has already recorded it before that message is even admitted.
// `session_started` (src/store/chat/slices/frames.ts) then only reflects the
// same value locally into the new session's bucket and clears the pending
// field — no WS send at all.
describe('chat store — pendingAutoApproveChoice rides the first message frame (ADR-092)', () => {
  function resetPendingChoiceScenario() {
    act(() => {
      useChatStore.getState().clearStreamingState()
      useChatStore.setState({ sessionsById: {}, pendingAutoApproveChoice: null })
      useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
      useSessionStore.setState({ activeSessionId: null, activeAgentId: null, activeAgentType: null })
      useWorkspacesStore.setState({ activeWorkspaceId: null })
    })
  }

  it('sends the pending choice as auto_approve on the first message frame, then session_started reflects it locally and clears the pending field', () => {
    resetPendingChoiceScenario()
    const mockSend = vi.fn((_frame: unknown) => true)
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
      useChatStore.getState().setPendingAutoApproveChoice(true)
    })

    act(() => {
      useChatStore.getState().sendMessage('hello')
    })
    expect(mockSend).toHaveBeenCalledTimes(1)
    expect(mockSend.mock.calls[0][0]).toMatchObject({ type: 'message', content: 'hello', auto_approve: true })
    // The choice is still pending — not cleared merely by sending — so the
    // switch keeps showing it while the ack is in flight.
    expect(useChatStore.getState().pendingAutoApproveChoice).toBe(true)

    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: 'sess_new_1' })
    })

    // No second frame goes out — session_started never triggers a
    // session_mode_update send any more.
    expect(mockSend).toHaveBeenCalledTimes(1)
    expect(useChatStore.getState().pendingAutoApproveChoice).toBeNull()
    expect(useChatStore.getState().sessionsById['sess_new_1']?.autoApproveEffective).toBe(true)
    expect(useChatStore.getState().autoApproveEffective).toBe(true)
  })

  it(
    'founder-ruling proof (2026-09-24): flip Auto on in a brand-new chat and send the first message — ' +
      'auto_approve travels on that SAME, single frame, so there is no second send for a race to land ' +
      'behind the agent loop dispatching the turn',
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

      // 2) User sends the first message. auto_approve is carried on this
      //    exact frame — the one and only frame this turn's mint sends —
      //    so the server has the choice before it can even admit the
      //    message, let alone dispatch the turn to the agent loop.
      act(() => {
        useChatStore.getState().sendMessage('hello')
      })
      expect(mockSend).toHaveBeenCalledTimes(1)
      expect(mockSend.mock.calls[0][0]).toEqual(
        expect.objectContaining({ type: 'message', content: 'hello', auto_approve: true }),
      )

      // 3) The server's session_started ack arrives. Nothing further is
      //    sent — the choice was already in the server's hands on frame 1.
      act(() => {
        useChatStore.getState().handleFrame({ type: 'session_started', session_id: 'sess_new_founder_proof' })
      })
      expect(mockSend).toHaveBeenCalledTimes(1)
      expect(useChatStore.getState().pendingAutoApproveChoice).toBeNull()
    },
  )

  it('flipping the switch and never sending a message sends nothing to the server at all', () => {
    resetPendingChoiceScenario()
    const mockSend = vi.fn((_frame: unknown) => true)
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

  it('carries a false choice the same way, on the first message frame', () => {
    resetPendingChoiceScenario()
    const mockSend = vi.fn((_frame: unknown) => true)
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
      useChatStore.getState().setPendingAutoApproveChoice(false)
    })

    act(() => {
      useChatStore.getState().sendMessage('hello')
    })
    expect(mockSend).toHaveBeenCalledTimes(1)
    expect(mockSend.mock.calls[0][0]).toMatchObject({ type: 'message', auto_approve: false })

    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: 'sess_new_2' })
    })

    expect(mockSend).toHaveBeenCalledTimes(1)
    expect(useChatStore.getState().pendingAutoApproveChoice).toBeNull()
    expect(useChatStore.getState().sessionsById['sess_new_2']?.autoApproveEffective).toBe(false)
  })

  it('omits auto_approve from the first message frame, and sends nothing extra on session_started, when there is no pending choice', () => {
    resetPendingChoiceScenario()
    const mockSend = vi.fn((_frame: unknown) => true)
    act(() => {
      useConnectionStore.setState({
        connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as unknown as WsConnection,
        isConnected: true,
      })
    })

    act(() => {
      useChatStore.getState().sendMessage('hello')
    })
    expect(mockSend).toHaveBeenCalledTimes(1)
    expect(mockSend.mock.calls[0][0]).not.toHaveProperty('auto_approve')

    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: 'sess_new_3' })
    })

    expect(mockSend).toHaveBeenCalledTimes(1)
    expect(useChatStore.getState().sessionsById['sess_new_3']?.autoApproveEffective).toBeNull()
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
    const mockSend = vi.fn((_frame: unknown) => true)
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
