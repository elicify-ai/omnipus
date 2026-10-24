// R6-F1: the workspace-lookup Retry must really retry. A stalled FIRST lookup
// (no data yet) must be aborted and a second request must start. Real
// TanStack Query and the real fetchWorkspaces/request path; only the network
// edge (fetch) and the inline ChatScreen are stand-ins.
import React from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { queryClient as client } from '@/lib/queryClient'
import * as api from '@/lib/api'

vi.mock('@/components/chat/ChatScreen', () => ({
  ChatScreen: () => <div data-testid="inline-chat-screen" />,
}))
const mockSessionId = 'sess-workspace-retry'
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
import { Route, REDIRECT_DECISION_WAIT_MS } from './sessions.$sessionId'

const SessionRoute = (Route as unknown as { component: React.ComponentType }).component

afterEach(() => { client.clear(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('workspace lookup Retry after the bounded wait', () => {
  it('aborts the stalled first lookup and starts a second real request', async () => {
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null, reconnectPhase: null, reconnectAttempt: 0 })
    detail = {
      ...availableHistoryDetail({ id: mockSessionId, agent_id: 'mia', title: 'Chat in ws-1', type: 'chat', workspace_id: 'ws-1',
        created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', message_count: 3 }),
      agent_removed: false,
    }
    // Every /workspaces request hangs until aborted; record each one's signal.
    const lookups: { url: string; signal: AbortSignal | undefined }[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (!url.includes('/workspaces')) return Promise.reject(new Error(`undeclared request ${url}`))
      lookups.push({ url, signal: init?.signal ?? undefined })
      return new Promise<Response>((_, reject) => {
        init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
      })
    }))
    // Forward through the REAL API implementation; observe the query's own
    // signals here independently of their arrival at the HTTP fetch edge.
    const workspaceReads = vi.spyOn(api, 'fetchWorkspaces')
    // Seed the detail query exactly as the route loader would.
    client.setQueryData(['session-detail', mockSessionId], detail)
    render(<QueryClientProvider client={client}><SessionRoute /></QueryClientProvider>)
    await waitFor(() => expect(lookups, 'positive instrument: the first real lookup started').toHaveLength(1))
    expect(lookups[0].signal, 'the query signal reaches the real request').toBeDefined()
    expect(workspaceReads).toHaveBeenCalledTimes(1)
    expect(workspaceReads).toHaveBeenNthCalledWith(1, { status: 'active', signal: expect.any(AbortSignal) })
    expect([...new URL(lookups[0].url, 'http://localhost').searchParams.entries()], 'first HTTP request has exactly the active-status filter').toStrictEqual([['status', 'active']])
    expect(lookups[0].signal).toBe(workspaceReads.mock.calls[0][0]?.signal)
    expect(lookups[0].signal).toBeInstanceOf(AbortSignal)
    const retry = await screen.findByRole('button', { name: 'Retry' }, { timeout: REDIRECT_DECISION_WAIT_MS + 3_000 })
    expect(screen.getByTestId('inline-chat-screen'), 'the stalled lookup opened the chat inline').toBeTruthy()
    expect(lookups[0].signal!.aborted, 'still waiting before Retry').toBe(false)
    await userEvent.click(retry)
    await waitFor(() => expect(lookups, 'Retry starts a SECOND real request').toHaveLength(2))
    expect(lookups[0].signal!.aborted, 'Retry aborts the stalled first request').toBe(true)
    expect(lookups[1].signal!.aborted, 'the new request is live').toBe(false)
    expect(workspaceReads).toHaveBeenCalledTimes(2)
    expect(workspaceReads).toHaveBeenNthCalledWith(2, { status: 'active', signal: expect.any(AbortSignal) })
    expect([...new URL(lookups[1].url, 'http://localhost').searchParams.entries()], 'second HTTP request has exactly the active-status filter').toStrictEqual([['status', 'active']])
    expect(lookups[1].signal).toBe(workspaceReads.mock.calls[1][0]?.signal)
    expect(lookups[1].signal).toBeInstanceOf(AbortSignal)
    expect(lookups[1].signal, 'the replacement request receives its own query signal').not.toBe(lookups[0].signal)
  }, 15_000)

  it('keeps the notice and the inline chat in separate layout slots', async () => {
    useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null, reconnectPhase: null, reconnectAttempt: 0 })
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => {})))
    client.setQueryData(['session-detail', mockSessionId], detail)
    render(<QueryClientProvider client={client}><SessionRoute /></QueryClientProvider>)
    const slot = await screen.findByTestId('workspace-lookup-chat-slot', undefined, { timeout: REDIRECT_DECISION_WAIT_MS + 3_000 })
    // ChatScreen is absolute inset-0; its slot must be a positioned box that
    // follows the notice in normal flow, so the chat cannot cover the notice.
    expect(slot.className).toContain('relative')
    expect(slot.className).toContain('flex-1')
    expect(slot.contains(screen.getByTestId('inline-chat-screen'))).toBe(true)
    const notice = screen.getByText(/^Still finding this chat's workspace\./u).parentElement!
    expect(notice.compareDocumentPosition(slot) & Node.DOCUMENT_POSITION_FOLLOWING, 'the notice precedes the chat slot').toBeTruthy()
    expect(slot.contains(notice), 'the notice is outside the chat slot').toBe(false)
  }, 15_000)
})
