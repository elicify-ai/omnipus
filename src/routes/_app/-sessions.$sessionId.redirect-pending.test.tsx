// B25 transport finding: a legacy /sessions/{id} deep link mounted the inline
// ChatScreen while the workspace list that decides the redirect was still
// loading. That ChatScreen started a history read, then the redirect replaced
// it and the read was aborted (net::ERR_ABORTED) on every such open. The
// inline thread may mount only once the redirect is decided against.
import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'

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
let workspacesQuery: { data: unknown; isPending: boolean } = { data: undefined, isPending: true }
vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: vi.fn().mockImplementation(({ queryKey }: { queryKey: unknown[] }) => {
      if (JSON.stringify(queryKey).includes('workspaces')) return { ...workspacesQuery, isLoading: workspacesQuery.isPending, isError: false }
      return { data: detail, isLoading: false, isError: false, isPending: false }
    }),
  }
})

import { useConnectionStore } from '@/store/connection'
import { availableHistoryDetail } from '../../../tests/chat-history-paging/legacy-fixtures'
import { Route } from './sessions.$sessionId'

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

  it('does not mount the inline thread (and so no history read) while the workspace list is loading', () => {
    workspacesQuery = { data: undefined, isPending: true }
    render(<SessionRoute />)
    expect(screen.queryByTestId('inline-chat-screen')).toBeNull()
  })

  it('mounts the inline thread once the list resolves without that workspace', () => {
    workspacesQuery = { data: [], isPending: false }
    render(<SessionRoute />)
    expect(screen.getByTestId('inline-chat-screen'), 'positive control: the inline fallback still renders').toBeTruthy()
  })
})
