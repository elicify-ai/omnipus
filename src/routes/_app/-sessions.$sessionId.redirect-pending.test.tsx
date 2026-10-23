// B25 transport finding: a legacy /sessions/{id} deep link mounted the inline
// ChatScreen while the workspace list that decides the redirect was still
// loading. That ChatScreen started a history read, then the redirect replaced
// it and the read was aborted (net::ERR_ABORTED) on every such open. The
// inline thread may mount only once the redirect is decided against.
import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen } from '@testing-library/react'

vi.mock('@/components/chat/ChatScreen', () => ({
  ChatScreen: () => <div data-testid="inline-chat-screen" />,
}))

const mockSessionId = 'sess-redirect-pending'
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (opts: { component: React.ComponentType; loader?: unknown }) => ({
      ...opts,
      useParams: () => ({ sessionId: mockSessionId }),
      useLoaderData: () => detail,
    }),
    useParams: () => ({ sessionId: mockSessionId }),
    useNavigate: () => vi.fn(),
  }
})

let detail: unknown = null
let workspacesQuery: { data: unknown; isPending: boolean; isPaused?: boolean } = { data: undefined, isPending: true }
const refetch = vi.fn()
vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: vi.fn().mockImplementation(({ queryKey }: { queryKey: unknown[] }) => {
      if (JSON.stringify(queryKey).includes('workspaces')) return { ...workspacesQuery, isPaused: workspacesQuery.isPaused ?? false, isLoading: workspacesQuery.isPending, isError: false, refetch }
      return { data: detail, isLoading: false, isError: false, isPending: false }
    }),
  }
})

import { useConnectionStore } from '@/store/connection'
import { availableHistoryDetail } from '../../../tests/chat-history-paging/legacy-fixtures'
import { Route, REDIRECT_DECISION_WAIT_MS } from './sessions.$sessionId'

function sessionDetail() {
  return {
    ...availableHistoryDetail({ id: mockSessionId, agent_id: 'mia', title: 'Chat in ws-1', type: 'chat', workspace_id: 'ws-1',
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', message_count: 3 }),
    agent_removed: false,
  }
}
const SessionRoute = (Route as unknown as { component: React.ComponentType }).component

describe('legacy session deep link while its redirect is undecided', () => {
  beforeEach(() => {
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null, reconnectPhase: null, reconnectAttempt: 0 })
    detail = sessionDetail()
  })

  it('shows a visible opening status, not the inline thread (no history read), while the workspace list is loading', () => {
    workspacesQuery = { data: undefined, isPending: true }
    render(<SessionRoute />)
    expect(screen.queryByTestId('inline-chat-screen')).toBeNull()
    expect(screen.getByRole('status'), 'never an empty chat area').toHaveTextContent('Opening chat…')
  })

  it('falls back to the inline thread when the workspace list stays pending past the bounded wait', () => {
    vi.useFakeTimers()
    try {
      workspacesQuery = { data: undefined, isPending: true }
      render(<SessionRoute />)
      expect(screen.queryByTestId('inline-chat-screen')).toBeNull()
      act(() => { vi.advanceTimersByTime(REDIRECT_DECISION_WAIT_MS - 1) })
      expect(screen.queryByTestId('inline-chat-screen'), 'still within the bound').toBeNull()
      act(() => { vi.advanceTimersByTime(1) })
      expect(screen.getByTestId('inline-chat-screen'), 'a stalled list cannot leave the chat empty').toBeTruthy()
      expect(screen.getByText(/^Still finding this chat's workspace\. The chat moves into its workspace once it is found\.$/u), 'the unresolved workspace and the later move are explained').toBeTruthy()
      refetch.mockClear()
      act(() => { screen.getByRole('button', { name: 'Retry' }).click() })
      expect(refetch, 'Retry replaces the stalled lookup').toHaveBeenCalledWith({ cancelRefetch: true })
    } finally { vi.useRealTimers() }
  })

  it('opens the inline thread at once when the list request is paused offline', () => {
    workspacesQuery = { data: undefined, isPending: true, isPaused: true }
    render(<SessionRoute />)
    expect(screen.getByTestId('inline-chat-screen'), 'its connection notice and Retry stay reachable').toBeTruthy()
    expect(screen.getByText(/^Waiting for the connection to find this chat's workspace\./u)).toBeTruthy()
  })

  it('mounts the inline thread once the list resolves without that workspace', () => {
    workspacesQuery = { data: [], isPending: false }
    render(<SessionRoute />)
    expect(screen.getByTestId('inline-chat-screen'), 'positive control: the inline fallback still renders').toBeTruthy()
    expect(screen.queryByText(/this chat's workspace/u), 'a resolved lookup shows no workspace notice').toBeNull()
  })
})
