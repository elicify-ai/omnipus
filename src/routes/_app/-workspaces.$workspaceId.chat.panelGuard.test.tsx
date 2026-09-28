// -workspaces.$workspaceId.chat.panelGuard.test.tsx — gap pack from
// pr-test-analyzer F2: the MAJ-205 ROUTER BLOCKER half of CRIT-001/FR-013
// (US-7 AS-8, §5 state machine), mounted through the REAL routeTree + a real
// memory-history router (same technique as
// -workspaces.$workspaceId.chat.deepLink.test.tsx, which this file does not
// touch).
//
// Oracle (side-panel-shell-spec.md, derived before re-reading the hook):
//   US-7 AS-8: "Given ANY router navigation while the Library panel has
//   unsaved edits ..., When the navigation is intercepted by the router
//   blocker (MAJ-205, FR-013), Then the discard-confirmation appears and a
//   cancel leaves the route, URL and panel unchanged — the navigation never
//   happens."
//   §5 state machine: "URL-initiated transitions ... are gated by a ROUTER
//   BLOCKER ...: while the open panel has unsaved edits, ANY router
//   navigation runs the outgoing panel's beforeLeave; on cancel the
//   navigation does not happen — route, address and panel stay unchanged."
//
// Characterisation pack: GREEN implements this (usePanelDeepLink's
// useBlocker). Unit boundary: the REAL routeTree → REAL chat route → REAL
// usePanelDeepLink blocker → REAL unsavedGuard; ChatScreen and the settings
// page body are stubbed (this file is about panel/URL/guard wiring, not
// those UIs); the dialog answer goes through the guard module's public API.

import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'

vi.mock('@/components/chat/ChatScreen', () => ({
  ChatScreen: () => null,
}))

// F2's navigation target: keep the test about the blocker, not settings UI.
vi.mock('@/components/workspaces/WorkspaceSettingsTab', () => ({
  WorkspaceSettingsTab: () => null,
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
import {
  setLibraryEditorDirty,
  isLibraryEditorDirty,
  getDiscardConfirmDialogOpen,
  resolveDiscardConfirmDialog,
} from '@/components/library/preview/unsavedGuard'

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
  act(() => {
    useUiStore.setState({ activePanel: null })
  })
  setLibraryEditorDirty(false)
  if (getDiscardConfirmDialogOpen()) resolveDiscardConfirmDialog(true)
})

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function activePanelId(): string | null {
  return useUiStore.getState().activePanel?.id ?? null
}

function searchOf(router: { state: { location: { search: unknown } } }): Record<string, unknown> {
  return router.state.location.search as Record<string, unknown>
}

/** Mount the REAL chat route with Library restored from the URL, then make
 * the editor dirty — the US-7 AS-8 pre-state. */
async function mountDirtyChatRoute() {
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

  setLibraryEditorDirty(true)
  return { router, client }
}

describe('chat route — MAJ-205 router blocker with a dirty docked Library (US-7 AS-8)', () => {
  it('an in-app navigation away from chat prompts, and CANCEL keeps route, URL, panel and edit (US-7 AS-8)', async () => {
    const { router, client } = await mountDirtyChatRoute()

    act(() => {
      void router.navigate({ to: '/workspaces/$workspaceId/settings', params: { workspaceId: 'ws-1' } })
    })

    // The guard runs BEFORE the route, address or panel move.
    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true), { timeout: 5000 })
    expect(router.state.location.pathname).toBe('/workspaces/ws-1/chat')
    expect(searchOf(router).panel).toBe('library')

    act(() => {
      resolveDiscardConfirmDialog(false)
    })
    await act(async () => {})

    // "a cancel leaves the route, URL and panel unchanged — the navigation
    // never happens."
    expect(router.state.location.pathname).toBe('/workspaces/ws-1/chat')
    expect(searchOf(router).panel).toBe('library')
    expect(activePanelId()).toBe('library')
    expect(isLibraryEditorDirty()).toBe(true)
    client.clear()
  })

  it('CONFIRMING the prompt lets the navigation proceed to the target route (US-7 AS-8)', async () => {
    const { router, client } = await mountDirtyChatRoute()

    act(() => {
      void router.navigate({ to: '/workspaces/$workspaceId/settings', params: { workspaceId: 'ws-1' } })
    })

    await waitFor(() => expect(getDiscardConfirmDialogOpen()).toBe(true), { timeout: 5000 })

    act(() => {
      resolveDiscardConfirmDialog(true)
    })

    await waitFor(
      () => expect(router.state.location.pathname).toBe('/workspaces/ws-1/settings'),
      { timeout: 5000 },
    )
    client.clear()
  })
})
