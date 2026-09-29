// workspaces.$workspaceId.chat.deepLink.test.tsx — RED pack for
// side-panel-shell-spec.md §8.2/§8.1 runtime deep-link restore behaviour
// (wave 1), mounted through the REAL routeTree + a real memory-history
// router (same technique as -library.deep-link.test.tsx), so the guard,
// the route match and the search-param restore all run for real. ChatScreen
// is stubbed to `() => null` (the -sessions.$sessionId.workspace.test.tsx
// precedent) — this file is about panel/URL wiring, not the chat UI itself.
//
// Oracle: §8.2 + §11's BDD scenarios "Deep link restores the panel",
// "Reload restores the open panel — EXCEPT Browser", "Reload does NOT
// restore the Browser panel (SP-28)", "Unknown panel id is dropped
// visibly" — and §12 test 6.
//
// RED evidence (2026-09-27): the chat route has no `panel` search schema and
// nothing reads it into the ui store at all (grep: zero references to
// `panel` as a search key anywhere under src/routes or src/store/ui.ts) —
// every assertion below expecting the store's activePanel to reflect the
// URL, or the URL to be replaced for a dropped id, fails.

import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'

vi.mock('@/components/chat/ChatScreen', () => ({
  ChatScreen: () => null,
}))

vi.mock('framer-motion', () => ({
  motion: new Proxy(
    {},
    {
      get:
        (_: object, prop: string) =>
        // eslint-disable-next-line react/display-name
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
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  })
})

afterEach(() => {
  localStorage.removeItem('omnipus_auth_username')
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: originalMatchMedia,
  })
})

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function activePanelId(): string | null {
  const state = useUiStore.getState() as unknown as { activePanel?: { id: string } | null }
  return state.activePanel?.id ?? null
}

describe('workspace Chat route — panel deep-link restore (§8.2, wave 1)', () => {
  it('a cold ?panel=library deep link restores the Library panel (US-7 AS-1/AS-2, §11 "Deep link restores the panel")', async () => {
    const client = makeClient()
    const router = createRouter({
      routeTree,
      history: createMemoryHistory({ initialEntries: ['/workspaces/ws-1/chat?panel=library'] }),
    })
    render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )

    await waitFor(() => expect(router.state.status).toBe('idle'), { timeout: 5000 })
    await waitFor(() => expect(activePanelId()).toBe('library'), { timeout: 5000 })

    client.clear()
  })

  it('?panel=browser is ALWAYS dropped with a URL replace — never restored, no session id ever placed in the URL (SP-21+SP-28)', async () => {
    const client = makeClient()
    const router = createRouter({
      routeTree,
      history: createMemoryHistory({
        initialEntries: ['/workspaces/ws-1/chat?panel=browser&session=sess-1&agent=agent-1'],
      }),
    })
    render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )

    await waitFor(() => expect(router.state.status).toBe('idle'), { timeout: 5000 })
    await waitFor(() => {
      // No Browser panel restored ...
      expect(activePanelId()).not.toBe('browser')
      // ... and the URL was replaced to drop `panel` (and its session
      // context) entirely — never left sitting in the address bar.
      const search = router.state.location.search as Record<string, unknown>
      expect(search.panel).toBeUndefined()
      expect(search.session).toBeUndefined()
    })

    client.clear()
  })

  it('an unknown panel id is dropped with a URL replace and no panel opens (US-7 AS-4)', async () => {
    const client = makeClient()
    const router = createRouter({
      routeTree,
      history: createMemoryHistory({ initialEntries: ['/workspaces/ws-1/chat?panel=bogus'] }),
    })
    render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )

    await waitFor(() => expect(router.state.status).toBe('idle'), { timeout: 5000 })
    await waitFor(() => {
      expect(activePanelId()).toBeNull()
      const search = router.state.location.search as Record<string, unknown>
      expect(search.panel).toBeUndefined()
    })

    client.clear()
  })
})
