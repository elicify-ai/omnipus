// Regression: side-panel-shell-spec.md FR-020/SP-29 moves an open Mail panel
// to the newly routed workspace; email-mail-view-spec.md US-3 AS-4 / FR-010
// requires that workspace's configured mailbox (or its connection error), not
// an empty state. The same-page hash transition keeps the React Query cache.
import React from 'react'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createHashHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/lib/api-error'

const { fetchMailboxes, fetchMailFolders } = vi.hoisted(() => ({
  fetchMailboxes: vi.fn(),
  fetchMailFolders: vi.fn(),
}))

vi.mock('@/components/chat/ChatScreen', () => ({ ChatScreen: () => null }))
vi.mock('framer-motion', () => ({
  motion: new Proxy({}, {
    get: (_: object, tag: string) =>
      React.forwardRef(({ children, ...props }: Record<string, unknown>, ref: React.Ref<unknown>) =>
        React.createElement(tag, { ...props, ref }, children as React.ReactNode)),
  }),
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchAppState: vi.fn(async () => ({ onboarding_complete: true })),
  validateToken: vi.fn(async () => ({})),
  fetchNotifications: vi.fn(async () => ({ notifications: [], unread_count: 0 })),
  fetchTasks: vi.fn(async () => []),
  fetchAgents: vi.fn(async () => [{ id: 'mia', name: 'Mia' }]),
  fetchVersion: vi.fn(async () => ({ version: 'test', build_sha: 'test' })),
  fetchWorkspaces: vi.fn(async () => [
    { id: 'ws-a', name: 'Workspace A', status: 'active', is_default: true },
    { id: 'ws-b', name: 'Workspace B', status: 'active', is_default: false },
  ]),
  fetchSessions: vi.fn(async () => []),
  fetchMailboxes,
}))
vi.mock('@/lib/api/mail', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api/mail')>(),
  fetchMailFolders,
  fetchMailMessages: vi.fn(async () => ({ messages: [], truncated: false, next_before_uid: null })),
  fetchMailSummary: vi.fn(async () => ({ items: [] })),
}))

import { routeTree } from '@/routeTree.gen'
import { useUiStore } from '@/store/ui'

const initialMailbox = {
  agent_id: 'mia', workspace_id: 'ws-a', enabled: true, configured: true,
  username: 'mia-other@example.test',
}
const newMailbox = {
  agent_id: 'mia', workspace_id: 'ws-b', enabled: true, configured: true,
  username: 'mia-outage@example.test',
}
let serverMailboxes = [initialMailbox]
let client: QueryClient | null = null
const originalMatchMedia = window.matchMedia

beforeEach(() => {
  window.history.replaceState(null, '', '/#/workspaces/ws-a/chat?panel=mail&agent=mia')
  localStorage.setItem('omnipus_auth_username', 'uat-tester')
  sessionStorage.removeItem('omnipus.mail-panel.intent.ws-a')
  sessionStorage.removeItem('omnipus.mail-panel.intent.ws-b')
  useUiStore.getState().closePanel()
  serverMailboxes = [initialMailbox]
  fetchMailboxes.mockReset().mockImplementation(async () => [...serverMailboxes])
  fetchMailFolders.mockReset().mockImplementation(async (workspaceId: string) => {
    if (workspaceId === 'ws-b') {
      throw new ApiError(502, 'mail server error: connect_refused', { code: 'connect_refused' })
    }
    return { folders: [{ slug: 'inbox', display_name: 'INBOX', total: 0, unread_count: 0 }] }
  })
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: (query: string) => ({
      matches: false, media: query, onchange: null,
      addListener: () => {}, removeListener: () => {},
      addEventListener: () => {}, removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  })
})

afterEach(() => {
  client?.clear()
  client = null
  useUiStore.getState().closePanel()
  localStorage.removeItem('omnipus_auth_username')
  sessionStorage.removeItem('omnipus.mail-panel.intent.ws-a')
  sessionStorage.removeItem('omnipus.mail-panel.intent.ws-b')
  window.history.replaceState(null, '', '/')
  Object.defineProperty(window, 'matchMedia', { configurable: true, value: originalMatchMedia })
})

describe('Mail stays in the newly routed workspace on a same-page hash navigation', () => {
  it('shows B’s newly configured but unreachable mailbox, not A’s cached list or an empty state', async () => {
    client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const router = createRouter({ routeTree, history: createHashHistory() })
    render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)

    // Establish the same live SPA state as the browser reproduction: Mail has
    // already fetched A's mailbox list before the external PUT configures B.
    await waitFor(() => expect(router.state.location.pathname).toBe('/workspaces/ws-a/chat'))
    const picker = await screen.findByRole('combobox', { name: 'Mailbox' })
    await waitFor(() => expect(picker).toHaveTextContent('Mia · mia-other@example.test'))
    expect(fetchMailboxes).toHaveBeenCalledTimes(1)

    // An API client outside the SPA configures B. Its PUT cannot invalidate
    // this page's React Query cache; only the mock server's roster changes.
    serverMailboxes = [initialMailbox, newMailbox]
    act(() => { window.location.hash = '/workspaces/ws-b/chat?panel=mail&agent=mia' })

    await waitFor(() => expect(router.state.location.pathname).toBe('/workspaces/ws-b/chat'))
    await waitFor(() => expect(useUiStore.getState().activePanel).toMatchObject({
      id: 'mail', context: { workspaceId: 'ws-b', mailboxId: 'mia' },
    }))
    expect(router.state.location.search).toMatchObject({ panel: 'mail', agent: 'mia' })

    // US-3 AS-4: configured + unreachable is a mailbox-specific connection
    // error, never the generic "no mailbox is configured" fallback.
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Mailbox' }))
      .toHaveTextContent('Mia · mia-outage@example.test'))
    const error = await screen.findByTestId('mail-folders-error')
    expect(within(error).getByText("Can't connect to this mailbox")).toBeInTheDocument()
    expect(within(error).getByText('Error class: connect_refused')).toBeInTheDocument()
    expect(screen.queryByTestId('mail-choose-mailbox')).not.toBeInTheDocument()
  })
})
