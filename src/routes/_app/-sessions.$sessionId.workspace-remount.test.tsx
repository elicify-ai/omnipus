// SFH r9 M1: a background refresh of the workspace list (e.g. the Sidebar's
// periodic refetch) must not re-mount the inline chat of a session whose
// workspace is not in the active list (for example an archived one). The chat
// DOM, and the focus inside it, survive idle -> fetching -> a successful
// refresh that still lacks the workspace; only the notice above it changes.
// Real TanStack Query (the app client) and the real fetchWorkspaces path; only
// fetch (network edge) and the inline ChatScreen are stand-ins.
import React from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { queryClient as client } from '@/lib/queryClient'
import { activeWorkspacesQueryOptions } from '@/lib/workspaceQueries'

let mounts = 0
vi.mock('@/components/chat/ChatScreen', async () => {
  const { useEffect } = await import('react')
  return {
    ChatScreen: () => {
      useEffect(() => { mounts += 1 }, [])
      return <div data-testid="inline-chat-screen"><textarea aria-label="Inline chat draft" /></div>
    },
  }
})
const mockSessionId = 'sess-workspace-remount'
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
import { availableHistoryDetail } from '../../../tests/chat-history-paging/legacy-fixtures'
import { Route } from './sessions.$sessionId'

const SessionRoute = (Route as unknown as { component: React.ComponentType }).component
let lookups: { settle: (response: Response) => void }[] = []
const otherWorkspaces = [{ id: 'ws-other', name: 'Other', is_default: false }]

beforeEach(() => {
  mounts = 0
  useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null, reconnectPhase: null, reconnectAttempt: 0 })
  detail = {
    ...availableHistoryDetail({ id: mockSessionId, agent_id: 'mia', title: 'Archived workspace chat', type: 'chat', workspace_id: 'ws-archived',
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', message_count: 3 }),
    agent_removed: false,
  }
  lookups = []
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    if (!url.includes('/workspaces')) return Promise.reject(new Error(`undeclared request ${url}`))
    return new Promise<Response>((resolve, reject) => {
      lookups.push({ settle: resolve })
      init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
    })
  }))
  client.setQueryData(['session-detail', mockSessionId], detail)
  // A fresh list that does not contain this chat's workspace: the route is idle.
  client.setQueryData(activeWorkspacesQueryOptions.queryKey, otherWorkspaces, { updatedAt: Date.now() })
})
afterEach(() => { client.clear(); vi.unstubAllGlobals() })

describe('background workspace refresh keeps the inline chat mounted', () => {
  it('retains the same chat node and its focus through idle, fetching and a refresh that still lacks the workspace', async () => {
    render(<QueryClientProvider client={client}><SessionRoute /></QueryClientProvider>)
    const chat = await screen.findByTestId('inline-chat-screen')
    expect(screen.queryByText(/this chat's workspace/u), 'positive control: an idle lookup shows no notice').toBeNull()
    expect(lookups, 'positive control: the fresh cached list needs no request').toHaveLength(0)
    const draft = screen.getByRole('textbox', { name: 'Inline chat draft' })
    act(() => { draft.focus() })
    expect(document.activeElement).toBe(draft)

    // Another producer of the shared query refreshes it in the background.
    act(() => { void client.refetchQueries({ queryKey: activeWorkspacesQueryOptions.queryKey, exact: true }) })
    await waitFor(() => expect(lookups, 'the background refresh is a real request').toHaveLength(1))
    expect(screen.getByText(/^Still finding this chat's workspace\./u), 'positive control: the route saw the lookup in flight').toBeTruthy()
    expect(screen.getByTestId('inline-chat-screen'), 'fetching keeps the same chat node').toBe(chat)
    expect(document.activeElement, 'fetching keeps focus inside the chat').toBe(draft)

    await act(async () => {
      // A schema-valid success that still lacks this chat's workspace.
      lookups[0].settle(new Response(JSON.stringify([]), { status: 200, headers: { 'content-type': 'application/json' } }))
    })
    await waitFor(() => expect(screen.queryByText(/this chat's workspace/u), 'the settled refresh ends the notice').toBeNull())
    expect(screen.getByTestId('inline-chat-screen'), 'the settled refresh keeps the same chat node').toBe(chat)
    expect(draft.isConnected, 'the focused control was never replaced').toBe(true)
    expect(document.activeElement, 'focus stays where the reader left it').toBe(draft)
    expect(mounts, 'the inline chat mounted exactly once').toBe(1)
  })
})
