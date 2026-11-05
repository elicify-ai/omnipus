// #1449 D13 — the standalone session shell (the /sessions/{id} path when the
// chat's workspace cannot be resolved) has NO workspace header line, so the
// chat's own chrome has nowhere else to live: the task identity must still
// be on screen. It is shown as small muted text — no bar, no border — above
// the transcript.
//
// Real route component, real TanStack Query, real session store; the ChatScreen
// (the heavy part) and the network edge are the only stand-ins.
import React from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { queryClient as client } from '@/lib/queryClient'

vi.mock('@/components/chat/ChatScreen', () => ({
  ChatScreen: () => <div data-testid="inline-chat-screen" />,
}))
const mockSessionId = 'sess-task-label'
let detail: unknown = null
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

import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { availableHistoryDetail } from '../../../tests/chat-history-paging/legacy-fixtures'
import { Route } from './sessions.$sessionId'

const SessionRoute = (Route as unknown as { component: React.ComponentType }).component

beforeEach(() => {
  useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null, reconnectPhase: null, reconnectAttempt: 0 })
  useSessionStore.setState({ attachedSessionType: null, attachedTaskTitle: null })
  detail = {
    ...availableHistoryDetail({
      id: mockSessionId, agent_id: 'mia', title: 'Ship the deck', type: 'task',
      workspace_id: 'ws-unresolved', created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z', message_count: 0,
    }),
    agent_removed: false,
  }
  // The workspace lookup never resolves: the chat opens in the standalone
  // shell (the workspace is absent or unresolved).
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
    const url = String(input)
    if (!url.includes('/workspaces')) return Promise.reject(new Error(`undeclared request ${url}`))
    return new Promise<Response>(() => {})
  }))
  client.setQueryData(['session-detail', mockSessionId], detail)
})
afterEach(() => { client.clear(); vi.unstubAllGlobals() })

describe('the standalone session shell keeps the chat identity', () => {
  it('a task transcript opened without its workspace still says "Task: <title>"', async () => {
    render(<QueryClientProvider client={client}><SessionRoute /></QueryClientProvider>)
    await waitFor(() => expect(useSessionStore.getState().attachedSessionType).toBe('task'))
    const label = await screen.findByText('Task: Ship the deck')
    expect(label).toBeVisible()
    // The label is plain text — no bar, no border around it.
    expect(label.className).not.toMatch(/border/)
    expect(screen.getByTestId('inline-chat-screen')).toBeVisible()
  })
})
