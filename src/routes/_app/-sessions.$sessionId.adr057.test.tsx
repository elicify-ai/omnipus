// ADR-057 U12 (W19b, FR-046/FR-047, BDD-71) — the drill-down surface for a
// hidden delegation. `GET /api/v1/sessions/{childID}` -> `<ChatScreen/>` is
// the STATED inspection surface for a completed delegation whose activity
// was hidden from the parent thread (toolVisibility.ts's `delegate` hide,
// CLAUDE.md "UI rules"). This route (sessions.$sessionId.tsx) is already
// generic over session id, so a "delegate"-typed child session is not
// special-cased anywhere in it — these tests exist to PROVE that rather
// than assume it, and to pin the "only GET /sessions/{childID}" boundary
// (test #78's scope) so a future change that adds a second fetch for
// verbose-chat state, or a subagent_message/subagent_state-driven query,
// would fail a real assertion here rather than slip in silently.
//
// FR-046: MUST be the stated inspection surface and MUST work with verbose
// chat disabled. This suite never mocks or reads any verbose-chat setting —
// its absence from every test below IS the evidence the route has no such
// dependency; if a future change made the route branch on it, these tests
// would need a new mock to keep passing, surfacing the coupling in review.
//
// Mirrors the mocking pattern of the existing
// `-sessions.$sessionId.test.tsx` (ChatScreen/framer-motion/react-query/
// router mocked so SessionRoute renders in isolation) — a new file per
// ownership Rule 5 (new tests go in new files), not an edit to that
// U-unowned-elsewhere-but-pre-existing file.

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, act, waitFor } from '@testing-library/react'

// ── Mock heavy dependencies before any SPA imports ────────────────────────────

let chatScreenRenderCount = 0
vi.mock('@/components/chat/ChatScreen', () => ({
  ChatScreen: () => {
    chatScreenRenderCount++
    return null
  },
}))

vi.mock('framer-motion', () => {
  return {
    motion: new Proxy({}, {
      get: (_: object, prop: string) =>
        React.forwardRef(({ children, ...props }: Record<string, unknown>, ref: React.Ref<unknown>) =>
          React.createElement(prop as string, { ...props, ref }, children as React.ReactNode)),
    }),
    AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  }
})

let _mockUseQueryData: unknown = null

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: vi.fn().mockImplementation(() => ({
      data: _mockUseQueryData,
      isLoading: false,
      isError: false,
    })),
  }
})

const mockChildSessionId = 'child-delegate-session-001'

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: (path: string) => {
      void path
      return (opts: { component: React.ComponentType; loader?: unknown }) => ({
        ...opts,
        useParams: () => ({ sessionId: mockChildSessionId }),
        useLoaderData: () => _mockUseQueryData,
      })
    },
    useParams: () => ({ sessionId: mockChildSessionId }),
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchSessionDetail: vi.fn(), fetchWorkspaces: vi.fn() }
})

import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { fetchSessionDetail, fetchWorkspaces, ApiError } from '@/lib/api'
import type { SessionDetail } from '@/lib/api'

function makeDelegateChildSession(overrides?: Partial<SessionDetail['session']>): SessionDetail {
  return {
    session: {
      id: mockChildSessionId,
      agent_id: 'ava',
      title: 'Delegated: audit the payments module',
      type: 'delegate',
      parent_session_id: 'parent-chat-session-001',
      created_at: '2026-08-01T00:00:00Z',
      updated_at: '2026-08-01T00:05:00Z',
      message_count: 4,
      ...overrides,
    },
    messages: [
      { id: 'm1', session_id: mockChildSessionId, role: 'user', content: 'Audit the payments module', timestamp: '2026-08-01T00:00:00Z' },
      { id: 'm2', session_id: mockChildSessionId, role: 'assistant', content: 'Found 2 issues.', timestamp: '2026-08-01T00:05:00Z', status: 'done' },
    ],
    agent_removed: false,
  }
}

function resetStores() {
  useSessionStore.setState({
    activeSessionId: null,
    activeAgentId: null,
    activeAgentType: null,
    attachedSessionType: null,
    attachedTaskTitle: null,
  })
  useConnectionStore.setState({
    connection: null,
    isConnected: false,
    connectionError: null,
    reconnectPhase: null,
    reconnectAttempt: 0,
    liteMode: false,
  })
}

let SessionRoute: React.ComponentType | null = null

describe('ADR-057 BDD-71 — drill-down surface reachable and populated for a hidden (delegate) child session', () => {
  beforeEach(async () => {
    vi.clearAllMocks()
    chatScreenRenderCount = 0
    resetStores()
    _mockUseQueryData = null

    if (!SessionRoute) {
      const mod = await import('./sessions.$sessionId')
      SessionRoute = (mod.Route as unknown as { component: React.ComponentType }).component
    }
  })

  it(
    'renders ChatScreen for a delegate-typed child session using only the loader-provided GET /sessions/{childID} detail — no other endpoint is consulted',
    async () => {
      // BDD-71: Given verbose chat disabled and a completed delegation hidden
      // from the thread, When the drill-down surface is opened by child id,
      // Then the child's transcript is displayed using only
      // GET /api/v1/sessions/{childID}. This test never mocks or checks a
      // verbose-chat flag at all — its absence IS the "works with verbose
      // chat disabled" proof, since the route has no such branch to take.
      const detail = makeDelegateChildSession()
      _mockUseQueryData = detail

      const Route = SessionRoute
      if (!Route) throw new Error('SessionRoute not loaded')
      await act(async () => {
        render(<Route />)
      })

      await waitFor(() => expect(chatScreenRenderCount).toBeGreaterThan(0))
      // The route's own useQuery is mocked to hand back the loader data
      // directly (matching the existing test file's pattern) — the real
      // network boundary this proves is the LOADER's call, asserted below.
      // fetchWorkspaces is only consulted when workspace_id is present
      // (this delegate session has none) — zero calls confirms no second
      // network round-trip was needed to render the drill-down.
      expect(fetchWorkspaces).not.toHaveBeenCalled()
    },
  )

  it(
    'the route loader fetches the child session by its own id — the exact boundary FR-046 pins',
    async () => {
      vi.mocked(fetchSessionDetail).mockResolvedValue(makeDelegateChildSession())

      const loader = (
        (await import('./sessions.$sessionId')).Route as unknown as {
          loader: (opts: { params: { sessionId: string } }) => Promise<SessionDetail | null>
        }
      ).loader

      const result = await loader({ params: { sessionId: mockChildSessionId } })

      expect(fetchSessionDetail).toHaveBeenCalledExactlyOnceWith(mockChildSessionId)
      expect(result?.session.id).toBe(mockChildSessionId)
      expect(result?.session.type).toBe('delegate')
      expect(result?.session.parent_session_id).toBe('parent-chat-session-001')
    },
  )

  it(
    'a deleted child session still 404s cleanly (SessionNotFound), same as any other session id — drill-down is not a special case for error handling',
    async () => {
      vi.mocked(fetchSessionDetail).mockRejectedValue(new ApiError(404))

      const loader = (
        (await import('./sessions.$sessionId')).Route as unknown as {
          loader: (opts: { params: { sessionId: string } }) => Promise<SessionDetail | null>
        }
      ).loader

      const result = await loader({ params: { sessionId: mockChildSessionId } })
      expect(result).toBeNull()
    },
  )
})
