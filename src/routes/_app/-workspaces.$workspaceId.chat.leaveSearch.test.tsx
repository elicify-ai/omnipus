// A navigation AWAY from the workspace chat must keep the destination's own
// query. Regression for the release-train red `terminal-outcome.spec.ts`
// (llm-chat shard): a supported `/#/settings?tab=chat` link requested from a
// live workspace chat came out as `/#/settings` (Providers tab) because the
// outgoing chat route's usePanelDeepLink rewrote the GLOBAL search with its
// "keep only panel/agent" updater while the transition was still pending.
//
// Oracle (usePanelDeepLink.ts header + the settings route's own contract):
// the hook owns the Chat route's search only (`panel`, `agent`); it must not
// clear keys that belong to the route being navigated to. A Settings link
// `?tab=chat` therefore ends with `tab=chat` in the address.
//
// Writers covered, one test each: (1) the cleanup effect on an in-app leave,
// (2) the popstate re-projection on a native same-document hash navigation,
// (3) the popstate re-projection on browser Back, (4) the store->URL
// projection effect when the panel store changes while leaving.
// Real routeTree + real hash router (the production history); only the heavy
// screens are stubbed. The destination query is asserted over a polled window
// AFTER the router reports the destination, so a late writer is still seen.

import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createHashHistory, RouterProvider } from '@tanstack/react-router'

vi.mock('@/components/chat/ChatScreen', () => ({ ChatScreen: () => null }))
vi.mock('@/components/screens/SettingsScreen', () => ({ SettingsScreen: () => null }))
vi.mock('@/components/workspaces/WorkspaceSettingsTab', () => ({ WorkspaceSettingsTab: () => null }))

vi.mock('framer-motion', () => ({
  motion: new Proxy(
    {},
    {
      get:
        (_: object, prop: string) =>
        React.forwardRef(({ children, ...props }: Record<string, unknown>, ref: React.Ref<unknown>) =>
          React.createElement(prop as string, { ...props, ref }, children as React.ReactNode)),
    },
  ),
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: vi.fn(async () => ({ onboarding_complete: true })),
    validateToken: vi.fn(async () => ({})),
    fetchNotifications: vi.fn(async () => ({ notifications: [], unread_count: 0 })),
    fetchTasks: vi.fn(async () => []),
    fetchAgents: vi.fn(async () => []),
    fetchVersion: vi.fn(async () => ({ version: 'test', build_sha: 'test' })),
    fetchWorkspaces: vi.fn(async () => [
      { id: 'ws-1', name: 'UAT Build', status: 'active', is_default: true },
    ]),
    fetchSessions: vi.fn(async () => []),
  }
})

import { routeTree } from '@/routeTree.gen'
import { useUiStore } from '@/store/ui'

const originalMatchMedia = window.matchMedia
beforeEach(() => {
  localStorage.setItem('omnipus_auth_username', 'uat-tester')
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: (query: string) => ({
      matches: false, media: query, onchange: null,
      addListener: () => {}, removeListener: () => {},
      addEventListener: () => {}, removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  })
})

afterEach(() => {
  localStorage.removeItem('omnipus_auth_username')
  Object.defineProperty(window, 'matchMedia', {
    configurable: true, writable: true, value: originalMatchMedia,
  })
  act(() => { useUiStore.setState({ activePanel: null }) })
  window.location.hash = ''
})

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

type TestRouter = ReturnType<typeof makeRouter>
function makeRouter() {
  return createRouter({ routeTree, history: createHashHistory() })
}

async function mountOnChat(hash = '#/workspaces/ws-1/chat') {
  window.location.hash = hash
  const client = makeClient()
  const router = makeRouter()
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)
  await waitFor(() => expect(router.state.status).toBe('idle'), { timeout: 5000 })
  await waitFor(() => expect(router.state.location.pathname).toBe(hash.slice(1).split('?')[0]), { timeout: 5000 })
  return { router, client }
}

/** Wait for the router to report Settings, then watch the search for a polled
 * window: a writer that fires late (after the route committed) still fails. */
async function expectSettingsKeepsTab(router: TestRouter) {
  await waitFor(() => expect(router.state.location.pathname).toBe('/settings'), { timeout: 5000 })
  await waitFor(() => expect(router.state.status).toBe('idle'), { timeout: 5000 })
  const until = Date.now() + 600
  while (Date.now() < until) {
    expect(router.state.location.search).toEqual({ tab: 'chat' })
    expect(window.location.hash).toBe('#/settings?tab=chat')
    await act(async () => { await new Promise((r) => setTimeout(r, 25)) })
  }
}

describe('leaving the workspace chat keeps the destination route query', () => {
  it('in-app navigation to /settings?tab=chat (cleanup effect)', async () => {
    const { router, client } = await mountOnChat()
    act(() => { void router.navigate({ to: '/settings', search: { tab: 'chat' } }) })
    await expectSettingsKeepsTab(router)
    client.clear()
  })

  it('native same-document hash navigation (popstate writer, the CI path)', async () => {
    const { router, client } = await mountOnChat()
    // A browser fires popstate synchronously for a same-document hash change.
    act(() => {
      window.location.hash = '#/settings?tab=chat'
      window.dispatchEvent(new PopStateEvent('popstate'))
    })
    await expectSettingsKeepsTab(router)
    client.clear()
  })

  it('browser Back from chat to /settings?tab=chat (popstate writer)', async () => {
    const { router, client } = await mountOnChat('#/settings?tab=chat')
    act(() => { void router.navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: 'ws-1' } }) })
    await waitFor(() => expect(router.state.location.pathname).toBe('/workspaces/ws-1/chat'), { timeout: 5000 })
    act(() => { window.history.back() })
    await expectSettingsKeepsTab(router)
    client.clear()
  })

  it('panel store change while leaving (projection effect)', async () => {
    const { router, client } = await mountOnChat()
    act(() => {
      void router.navigate({ to: '/settings', search: { tab: 'chat' } })
      useUiStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    })
    await expectSettingsKeepsTab(router)
    client.clear()
  })
})
