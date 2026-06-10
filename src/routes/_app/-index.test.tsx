// Unit tests for the "/" route component — RootChatScreen.
//
// #417 regression coverage. The bug: RootChatScreen reflected ANY non-null
// activeSessionId to the URL on mount, so arriving at "/" with a STALE
// activeSessionId (left over from a previous route — sidebar "Chat", tasks
// redirect, New Chat, etc.) bounced the URL straight back to the old session.
//
// The structural fix makes the ROUTE the source of truth: "/" clears the
// session on mount and only advances the URL for a session MINTED while on this
// screen (a genuine null -> id transition observed AFTER the clear), gated by a
// clearedRef. These tests pin that contract and FAIL on the pre-fix component
// (which would navigate to /sessions/<stale> on mount).
//
// Design note: ChatScreen is mocked to a no-op to avoid pulling in the
// WebSocket store, AssistantUI hooks, and the full component tree. TanStack
// Router primitives are mocked so RootChatScreen renders in isolation.

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, act, waitFor } from '@testing-library/react'

// ── Mock heavy dependencies before any SPA imports ────────────────────────────

vi.mock('@/components/chat/ChatScreen', () => ({
  ChatScreen: () => null,
}))

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    // createFileRoute('/_app/')({ component }) → return an object exposing the
    // component so the test can render it directly.
    createFileRoute: (_path: string) => (opts: { component: React.ComponentType }) => ({
      ...opts,
    }),
    useNavigate: () => mockNavigate,
  }
})

// ── Store import (real Zustand store) ─────────────────────────────────────────
import { useSessionStore } from '@/store/session'

function resetStore() {
  useSessionStore.setState({
    activeSessionId: null,
    activeAgentId: null,
    activeAgentType: null,
    attachedSessionType: null,
    attachedTaskTitle: null,
  })
}

// ── Component under test (imported AFTER mocks) ───────────────────────────────
let RootChatScreen: React.ComponentType | null = null

describe('RootChatScreen — "/" route (fix #417)', () => {
  beforeEach(async () => {
    vi.clearAllMocks()
    mockNavigate.mockReset()
    resetStore()

    if (!RootChatScreen) {
      const mod = await import('./index')
      RootChatScreen = (mod.Route as unknown as { component: React.ComponentType }).component
    }
  })

  it('does NOT bounce the URL back to a stale activeSessionId on mount', async () => {
    // Given: a stale session is active (as if we just left /sessions/stale-old
    // and navigated to "/" via the sidebar / tasks redirect / New Chat).
    act(() => {
      useSessionStore.setState({ activeSessionId: 'stale-old', activeAgentId: 'mia' })
    })

    const Comp = RootChatScreen
    if (!Comp) throw new Error('RootChatScreen not loaded')

    await act(async () => {
      render(<Comp />)
    })

    // The mount clear settles activeSessionId to null...
    await waitFor(() => {
      expect(useSessionStore.getState().activeSessionId).toBeNull()
    })
    // ...and crucially the URL was NEVER bounced to the stale session. On the
    // pre-fix component this would be navigate({ to:'/sessions/$sessionId',
    // params:{ sessionId:'stale-old' }, ... }).
    expect(mockNavigate).not.toHaveBeenCalled()
  })

  it('advances the URL to /sessions/<id> for a session minted after the clear', async () => {
    const Comp = RootChatScreen
    if (!Comp) throw new Error('RootChatScreen not loaded')

    await act(async () => {
      render(<Comp />)
    })
    // Clear observed (clearedRef now true), no navigation yet.
    await waitFor(() => {
      expect(useSessionStore.getState().activeSessionId).toBeNull()
    })
    expect(mockNavigate).not.toHaveBeenCalled()

    // session_started after the first sent message mints a real session id.
    await act(async () => {
      useSessionStore.setState({ activeSessionId: 'new-sess' })
    })

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith({
        to: '/sessions/$sessionId',
        params: { sessionId: 'new-sess' },
        replace: true,
      })
    })
  })

  it('does NOT navigate for the panel-attach path (attachedSessionType set)', async () => {
    const Comp = RootChatScreen
    if (!Comp) throw new Error('RootChatScreen not loaded')

    await act(async () => {
      render(<Comp />)
    })
    await waitFor(() => {
      expect(useSessionStore.getState().activeSessionId).toBeNull()
    })

    // A session opened via the SessionPanel sets attachedSessionType, which must
    // suppress the route round-trip so it doesn't race the panel's replay.
    await act(async () => {
      useSessionStore.setState({ activeSessionId: 'panel-sess', attachedSessionType: 'task' })
    })

    // Give effects a chance to run, then assert no navigation happened.
    await act(async () => {
      await Promise.resolve()
    })
    expect(mockNavigate).not.toHaveBeenCalled()
  })
})
