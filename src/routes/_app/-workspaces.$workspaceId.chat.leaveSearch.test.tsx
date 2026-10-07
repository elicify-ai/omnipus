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
// Real routeTree + real router; only the heavy screens are stubbed.

import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'

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
})

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

const settle = () => act(async () => { await new Promise((r) => setTimeout(r, 250)) })

describe('leaving the workspace chat keeps the destination route query', () => {
  it('an in-app navigation to /settings?tab=chat ends with tab=chat', async () => {
    const client = makeClient()
    const router = createRouter({
      routeTree,
      history: createMemoryHistory({ initialEntries: ['/workspaces/ws-1/chat'] }),
    })
    render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)
    await waitFor(() => expect(router.state.status).toBe('idle'), { timeout: 5000 })
    expect(router.state.location.pathname).toBe('/workspaces/ws-1/chat')

    act(() => { void router.navigate({ to: '/settings', search: { tab: 'chat' } }) })
    await waitFor(() => expect(router.state.location.pathname).toBe('/settings'), { timeout: 5000 })
    await settle()

    expect(router.state.location.search).toEqual({ tab: 'chat' })
    client.clear()
  })
})
