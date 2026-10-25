// R7: a FAILED workspace lookup must explain itself and offer a Retry that
// really starts a new request; a lookup started by another producer of the
// same query (e.g. the sidebar, through the shared query options) must be
// abortable by that Retry; repeated Retry clicks must not stack requests.
// Real TanStack Query (the app client) and the real fetchWorkspaces/request
// path; only fetch (network edge) and the inline ChatScreen are stand-ins.
import React from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CancelledError, onlineManager, QueryClientProvider } from '@tanstack/react-query'
import { queryClient as client } from '@/lib/queryClient'
import { activeWorkspacesQueryOptions } from '@/lib/workspaceQueries'

vi.mock('@/components/chat/ChatScreen', () => ({
  ChatScreen: () => <div data-testid="inline-chat-screen" />,
}))
const mockSessionId = 'sess-workspace-failure'
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
type Lookup = { signal: AbortSignal | undefined; settle: (response: Response) => void }
let lookups: Lookup[] = []

beforeEach(() => {
  useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null, reconnectPhase: null, reconnectAttempt: 0 })
  detail = {
    ...availableHistoryDetail({ id: mockSessionId, agent_id: 'mia', title: 'Chat in ws-1', type: 'chat', workspace_id: 'ws-1',
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', message_count: 3 }),
    agent_removed: false,
  }
  lookups = []
  // Every /workspaces request stays open until the test settles or aborts it.
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    if (!url.includes('/workspaces')) return Promise.reject(new Error(`undeclared request ${url}`))
    return new Promise<Response>((resolve, reject) => {
      lookups.push({ signal: init?.signal ?? undefined, settle: resolve })
      init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
    })
  }))
  client.setQueryData(['session-detail', mockSessionId], detail)
})
afterEach(() => { onlineManager.setOnline(true); client.clear(); vi.unstubAllGlobals() })

const forbidden = () => new Response(JSON.stringify({ error: 'forbidden' }), { status: 403, headers: { 'content-type': 'application/json' } })

describe('workspace lookup failure and shared producers', () => {
  it('a failed lookup shows a plain explanation with a Retry that starts another real request', async () => {
    render(<QueryClientProvider client={client}><SessionRoute /></QueryClientProvider>)
    await waitFor(() => expect(lookups).toHaveLength(1))
    lookups[0].settle(forbidden())
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't find this chat's workspace, so the chat is open here instead.")
    expect(screen.getByTestId('inline-chat-screen'), 'the chat stays usable inline').toBeTruthy()
    await userEvent.click(within(alert).getByRole('button', { name: /^Retry$/u }))
    await waitFor(() => expect(lookups, 'Retry starts a second real request').toHaveLength(2))
    expect(lookups[1].signal?.aborted).toBe(false)
  })

  it('aborts a lookup started by another producer of the shared query, then starts a second one', async () => {
    // Another component (the sidebar) starts the request through the shared definition.
    const producer = client.fetchQuery({ ...activeWorkspacesQueryOptions }).catch((error: unknown) => {
      if (!(error instanceof CancelledError)) throw error
      expect(error).toBeInstanceOf(CancelledError)
    })
    await waitFor(() => expect(lookups, 'the other producer started the first real request').toHaveLength(1))
    expect(lookups[0].signal, 'the shared definition forwards the query signal').toBeDefined()
    render(<QueryClientProvider client={client}><SessionRoute /></QueryClientProvider>)
    const retry = await screen.findByRole('button', { name: 'Retry' }, { timeout: REDIRECT_DECISION_WAIT_MS + 3_000 })
    expect(lookups, 'the route joined the in-flight request instead of starting its own').toHaveLength(1)
    await Promise.all([producer, userEvent.click(retry)])
    await waitFor(() => expect(lookups).toHaveLength(2))
    expect(lookups[0].signal!.aborted, 'Retry aborts the request the other producer started').toBe(true)
    expect(lookups[1].signal!.aborted).toBe(false)
  }, 15_000)

  it('repeated Retry clicks leave exactly one live request', async () => {
    render(<QueryClientProvider client={client}><SessionRoute /></QueryClientProvider>)
    const retry = await screen.findByRole('button', { name: 'Retry' }, { timeout: REDIRECT_DECISION_WAIT_MS + 3_000 })
    const user = userEvent.setup()
    await user.click(retry); await user.click(retry); await user.click(retry)
    await waitFor(() => expect(lookups.length).toBeGreaterThanOrEqual(2))
    await waitFor(() => expect(lookups.filter(lookup => !lookup.signal?.aborted), 'never more than one live lookup').toHaveLength(1))
    expect(lookups.slice(0, -1).every(lookup => lookup.signal?.aborted), 'every earlier lookup was aborted').toBe(true)
  }, 15_000)

  it('a stale NON-EMPTY list without this workspace whose refresh fails still explains itself with Retry', async () => {
    client.setQueryData(activeWorkspacesQueryOptions.queryKey, [{ id: 'ws-other', name: 'Other', is_default: false }], { updatedAt: Date.now() - 60_000 })
    render(<QueryClientProvider client={client}><SessionRoute /></QueryClientProvider>)
    await waitFor(() => expect(lookups, 'the stale list is refreshed by a real request').toHaveLength(1))
    expect(screen.getByText(/^Still finding this chat's workspace\./u), 'a refresh in flight is a lookup in progress').toBeTruthy()
    lookups[0].settle(forbidden())
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't find this chat's workspace")
    expect(within(alert).getByRole('button', { name: /^Retry$/u })).toBeTruthy()
  })

  it('a cached EMPTY list: after a failed refresh, Retry hides the old error while the new request runs, which then settles', async () => {
    client.setQueryData(activeWorkspacesQueryOptions.queryKey, [], { updatedAt: Date.now() - 60_000 })
    render(<QueryClientProvider client={client}><SessionRoute /></QueryClientProvider>)
    await waitFor(() => expect(lookups).toHaveLength(1))
    lookups[0].settle(forbidden())
    const alert = await screen.findByRole('alert')
    await userEvent.click(within(alert).getByRole('button', { name: /^Retry$/u }))
    await waitFor(() => expect(lookups, 'Retry starts a second real request').toHaveLength(2))
    expect(screen.queryByText(/Couldn't find this chat's workspace/u), 'the old error is not shown while the new request runs').toBeNull()
    expect(screen.getByText(/^Still finding this chat's workspace\./u)).toBeTruthy()
    // Request two SETTLES: a list that still lacks this workspace ends the lookup quietly.
    lookups[1].settle(new Response(JSON.stringify([]), { status: 200, headers: { 'content-type': 'application/json' } }))
    await waitFor(() => expect(screen.queryByText(/this chat's workspace/u), 'a settled lookup shows no notice or error').toBeNull())
    expect(screen.getByTestId('inline-chat-screen')).toBeTruthy()
  })

  it('a cached EMPTY list: a Retry made while offline shows the offline notice, not the old error', async () => {
    client.setQueryData(activeWorkspacesQueryOptions.queryKey, [], { updatedAt: Date.now() - 60_000 })
    render(<QueryClientProvider client={client}><SessionRoute /></QueryClientProvider>)
    await waitFor(() => expect(lookups).toHaveLength(1))
    lookups[0].settle(forbidden())
    const alert = await screen.findByRole('alert')
    onlineManager.setOnline(false)
    await userEvent.click(within(alert).getByRole('button', { name: /^Retry$/u }))
    expect(await screen.findByText(/^Waiting for the connection to find this chat's workspace\./u)).toBeTruthy()
    expect(screen.queryByText(/Couldn't find this chat's workspace/u)).toBeNull()
  })

  it('a failed replacement stays a terminal error with Retry and inline chat, never a successful empty list', async () => {
    const key = activeWorkspacesQueryOptions.queryKey
    const failureMessage = "Couldn't find this chat's workspace, so the chat is open here instead. Retry to move it into its workspace."
    render(<QueryClientProvider client={client}><SessionRoute /></QueryClientProvider>)
    await waitFor(() => expect(lookups, 'the initial real request started').toHaveLength(1))
    lookups[0].settle(forbidden())
    const firstAlert = await screen.findByRole('alert')
    expect(firstAlert).toHaveTextContent(failureMessage)
    expect(client.getQueryState(key)).toMatchObject({ status: 'error', fetchStatus: 'idle', error: { status: 403 } })
    const firstError = client.getQueryState(key)?.error

    await userEvent.click(within(firstAlert).getByRole('button', { name: /^Retry$/u }))
    await waitFor(() => expect(lookups, 'Retry starts the replacement real request').toHaveLength(2))
    expect(client.getQueryState(key)?.fetchStatus).toBe('fetching')
    expect(screen.queryByText(failureMessage), 'the previous failure is not the replacement outcome').toBeNull()
    lookups[1].settle(forbidden())

    await waitFor(() => expect({ status: client.getQueryState(key)?.status, fetchStatus: client.getQueryState(key)?.fetchStatus },
      'r13 m03: the SECOND request failed; never normalize it into successful empty data')
      .toStrictEqual({ status: 'error', fetchStatus: 'idle' }))
    expect(client.getQueryState(key)?.error).toMatchObject({ status: 403 })
    expect(client.getQueryState(key)?.error, 'the replacement has its own terminal error, not the old one').not.toBe(firstError)
    expect(client.getQueryState(key)?.data, 'neither failure is an empty successful workspace list').toBeUndefined()
    const replacementAlert = await screen.findByRole('alert')
    expect(replacementAlert).toHaveTextContent(failureMessage)
    expect(replacementAlert).toBeVisible()
    expect(within(replacementAlert).getByRole('button', { name: /^Retry$/u })).toBeVisible()
    expect(screen.getByTestId('inline-chat-screen'), 'failed replacement keeps the chat open inline').toBeVisible()
    expect(lookups, 'both403 outcomes are non-retryable; no hidden third request').toHaveLength(2)
  })
})
