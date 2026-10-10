// Regression: side-panel-shell-spec.md FR-020/SP-29 moves an open Mail panel
// to the newly routed workspace; email-mail-view-spec.md US-3 AS-4 / FR-010
// requires that workspace's configured mailbox (or its connection error), not
// an empty state. The same-page hash transition keeps the React Query cache.
import React from 'react'
import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react'
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
  fetchAgents: vi.fn(async () => [{ figure: 'Omnipus', role: 'general', id: 'mia', name: 'Mia' }]),
  fetchVersion: vi.fn(async () => ({ version: 'test', build_sha: 'test' })),
  fetchWorkspaces: vi.fn(async () => [
    { id: 'ws-a', name: 'Workspace A', status: 'active', is_default: true },
    { id: 'ws-b', name: 'Workspace B', status: 'active', is_default: false },
    { id: 'ws-c', name: 'Workspace C', status: 'active', is_default: false },
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
const thirdMailbox = {
  agent_id: 'mia', workspace_id: 'ws-c', enabled: true, configured: true,
  username: 'mia-third@example.test',
}
let serverMailboxes = [initialMailbox]
let client: QueryClient | null = null
const originalMatchMedia = window.matchMedia

// jsdom fires popstate for a newly assigned hash but has no Navigation API.
// Browsers report this as a same-document push, distinct from Back/Forward's
// traverse; dispatch that event before assigning the hash, as the browser does.
function openHashLink(path: string) {
  window.navigation.dispatchEvent(Object.assign(new Event('navigate'), {
    navigationType: 'push', hashChange: true,
    destination: { url: new URL(`#${path}`, window.location.href).href, sameDocument: true },
  }))
  window.location.hash = path
}

// jsdom supplies real asynchronous Back/Forward + popstate, but not the
// preceding Navigation API event. Model only that missing platform event;
// never turn a history traversal into openHashLink's fresh hash push.
async function traverseHashHistory(direction: 'back' | 'forward', destinationPath: string) {
  let restoredEntry: { hash: string; entry: unknown } | null = null
  await act(async () => {
    const popstate = new Promise<{ hash: string; entry: unknown }>((resolve) => {
      // Capture both before the real hook's router replace, which owns and
      // rewrites history.state as well as the projected panel URL.
      window.addEventListener('popstate', (event) => resolve({
        hash: window.location.hash, entry: event.state?.entry,
      }), { once: true, capture: true })
    })
    window.navigation.dispatchEvent(Object.assign(new Event('navigate'), {
      navigationType: 'traverse', hashChange: true,
      destination: { url: new URL(`#${destinationPath}`, window.location.href).href, sameDocument: true },
    }))
    window.history[direction]()
    restoredEntry = await popstate
  })
  return restoredEntry
}

beforeEach(() => {
  vi.stubGlobal('navigation', new EventTarget())
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
  // The hook removes its Navigation API listener on unmount. Do that while
  // the platform stand-in still exists, before vi.unstubAllGlobals below.
  cleanup()
  client?.clear()
  client = null
  useUiStore.getState().closePanel()
  localStorage.removeItem('omnipus_auth_username')
  sessionStorage.removeItem('omnipus.mail-panel.intent.ws-a')
  sessionStorage.removeItem('omnipus.mail-panel.intent.ws-b')
  window.history.replaceState(null, '', '/')
  Object.defineProperty(window, 'matchMedia', { configurable: true, value: originalMatchMedia })
  vi.unstubAllGlobals()
})

describe('Mail stays in the newly routed workspace on a same-page hash navigation', () => {
  it('preserves Mail and its mailbox through stale Back/Forward entries, then adopts a fresh pushed Library link', async () => {
    // I1 / side-panel-shell-spec.md §8.2, US-7 AS-7 (SP-22/MAJ-206):
    // STORE wins on traversal; a subsequent fresh link still adopts its panel.
    // Seed stale Library entries on BOTH sides of the initial Mail landing.
    // The incidental agent key remains declared even for Library (§8.2).
    const mailPath = '/workspaces/ws-a/chat?panel=mail&agent=mia'
    const staleLibraryPath = '/workspaces/ws-a/chat?panel=library&agent=mia'
    window.history.replaceState({ entry: 'older-library' }, '', `/#${staleLibraryPath}`)
    window.history.pushState({ entry: 'mail' }, '', `/#${mailPath}`)
    window.history.pushState({ entry: 'newer-library' }, '', `/#${staleLibraryPath}`)
    expect(await traverseHashHistory('back', mailPath)).toEqual({
      hash: `#${mailPath}`, entry: 'mail',
    })
    const historyLength = window.history.length

    client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const router = createRouter({ routeTree, history: createHashHistory() })
    render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)

    async function expectMailSurvives() {
      await waitFor(() => {
        expect(useUiStore.getState().activePanel).toEqual({
          id: 'mail', context: { workspaceId: 'ws-a', mailboxId: 'mia' },
        })
        expect(router.state.location.pathname).toBe('/workspaces/ws-a/chat')
        expect(router.state.location.search).toEqual({ panel: 'mail', agent: 'mia' })
        expect(window.location.hash).toBe(`#${mailPath}`)
        expect(router.state.status).toBe('idle')
      })
      expect(screen.getByRole('complementary', { name: 'Mail' })).toBeVisible()
      await waitFor(() => expect(screen.getByRole('combobox', { name: 'Mailbox' }))
        .toHaveTextContent('Mia · mia-other@example.test'))
      expect(screen.queryByRole('complementary', { name: 'Library' })).not.toBeInTheDocument()
      expect(window.history.length).toBe(historyLength)
    }
    await expectMailSurvives()
    // Initial route adoption owns/replaces history.state. Seed the current
    // slot's test marker only AFTER it settles, preserving router metadata;
    // the unvisited stale Library slots still carry their original markers.
    await act(async () => {
      window.history.replaceState({ ...window.history.state, entry: 'mail' }, '', window.location.href)
    })

    // Capture the REAL restored hash before the hook's replace: neither
    // traversal may be faked by a push to the already-correct Mail URL.
    expect(await traverseHashHistory('back', staleLibraryPath)).toEqual({
      hash: `#${staleLibraryPath}`, entry: 'older-library',
    })
    await expectMailSurvives()
    expect(await traverseHashHistory('forward', mailPath)).toEqual({
      hash: `#${mailPath}`, entry: 'mail',
    })
    await expectMailSurvives()
    expect(await traverseHashHistory('forward', staleLibraryPath)).toEqual({
      hash: `#${staleLibraryPath}`, entry: 'newer-library',
    })
    await expectMailSurvives()

    // Do not "fix" traversal by suppressing fresh adoption too (delta 3).
    // openHashLink emits push and changes the real hash, with the same hook
    // still mounted; the requested Library must replace Mail in workspace B.
    const freshLibraryPath = '/workspaces/ws-b/chat?panel=library'
    act(() => { openHashLink(freshLibraryPath) })
    await waitFor(() => {
      expect(useUiStore.getState().activePanel).toEqual({
        id: 'library', context: { workspaceId: 'ws-b' },
      })
      expect(router.state.location.pathname).toBe('/workspaces/ws-b/chat')
      expect(router.state.location.search).toEqual({ panel: 'library' })
      expect(window.location.hash).toBe(`#${freshLibraryPath}`)
      expect(router.state.status).toBe('idle')
    })
    expect(await screen.findByRole('complementary', { name: 'Library' })).toBeVisible()
    expect(screen.queryByRole('combobox', { name: 'Mailbox' })).not.toBeInTheDocument()
    expect(screen.queryByRole('complementary', { name: 'Mail' })).not.toBeInTheDocument()
    expect(window.history.length).toBe(historyLength + 1)
  })

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
    act(() => { openHashLink('/workspaces/ws-b/chat?panel=mail&agent=mia') })

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

  it('keeps the open Mail mailbox on an ordinary workspace switch with no deep link (SP-29)', async () => {
    serverMailboxes = [initialMailbox, newMailbox]
    client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const router = createRouter({ routeTree, history: createHashHistory() })
    render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)

    await waitFor(() => expect(useUiStore.getState().activePanel).toMatchObject({
      id: 'mail', context: { workspaceId: 'ws-a', mailboxId: 'mia' },
    }))
    await screen.findByText('INBOX')

    // Sidebar's workspace button calls navigate with `to` and `params`, but
    // no search object. Unlike a new `?panel=mail` link, this is a follow.
    await act(async () => {
      await router.navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: 'ws-b' } })
    })
    await waitFor(() => expect(router.state.location.pathname).toBe('/workspaces/ws-b/chat'))
    expect(router.state.location.search.agent).toBeUndefined()
    await waitFor(() => expect(useUiStore.getState().activePanel).toMatchObject({
      id: 'mail', context: { workspaceId: 'ws-b', mailboxId: 'mia' },
    }))
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Mailbox' }))
      .toHaveTextContent('Mia · mia-outage@example.test'))
    expect(fetchMailFolders.mock.calls.some(([workspace]) => workspace === 'ws-b')).toBe(true)
  })

  it('shows the chooser without dialing when a new bare Mail link targets another workspace (SP-23)', async () => {
    serverMailboxes = [initialMailbox, newMailbox]
    client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const router = createRouter({ routeTree, history: createHashHistory() })
    render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)

    await waitFor(() => expect(useUiStore.getState().activePanel).toMatchObject({
      id: 'mail', context: { workspaceId: 'ws-a', mailboxId: 'mia' },
    }))
    await screen.findByText('INBOX')

    // A same-tab URL landing is a fresh link, not a workspace switch through
    // Sidebar's panel-follow path. B also has Mia, so inheriting A's mailbox
    // would start a real folders request rather than showing SP-23's chooser.
    act(() => { openHashLink('/workspaces/ws-b/chat?panel=mail') })
    await waitFor(() => expect(router.state.location.pathname).toBe('/workspaces/ws-b/chat'))
    await waitFor(() => expect(useUiStore.getState().activePanel).toMatchObject({
      id: 'mail', context: { workspaceId: 'ws-b', mailboxId: null },
    }))
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Mailbox' }))
      .toHaveTextContent('Choose a mailbox'))
    expect(fetchMailFolders.mock.calls.some(([workspace]) => workspace === 'ws-b')).toBe(false)
  })

  it('keeps consecutive bare Mail links in the chooser even when the middle workspace has no mailbox', async () => {
    serverMailboxes = [initialMailbox, thirdMailbox]
    client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const router = createRouter({ routeTree, history: createHashHistory() })
    render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)

    await waitFor(() => expect(useUiStore.getState().activePanel).toMatchObject({
      id: 'mail', context: { workspaceId: 'ws-a', mailboxId: 'mia' },
    }))
    await screen.findByText('INBOX')

    act(() => { openHashLink('/workspaces/ws-b/chat?panel=mail') })
    await waitFor(() => expect(router.state.location.pathname).toBe('/workspaces/ws-b/chat'))
    await waitFor(() => expect(useUiStore.getState().activePanel?.context.workspaceId).toBe('ws-b'))
    await screen.findByTestId('mail-choose-mailbox')

    act(() => { openHashLink('/workspaces/ws-c/chat?panel=mail') })
    await waitFor(() => expect(router.state.location.pathname).toBe('/workspaces/ws-c/chat'))
    await waitFor(() => expect(useUiStore.getState().activePanel).toMatchObject({
      id: 'mail', context: { workspaceId: 'ws-c', mailboxId: null },
    }))
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Mailbox' }))
      .toHaveTextContent('Choose a mailbox'))
    expect(fetchMailFolders.mock.calls.some(([workspace]) => workspace === 'ws-c')).toBe(false)
  })
})
