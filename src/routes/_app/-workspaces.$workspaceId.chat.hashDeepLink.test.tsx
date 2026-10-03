import React from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createHashHistory, createRouter, RouterProvider } from '@tanstack/react-router'

vi.mock('@/components/chat/ChatScreen', () => ({ ChatScreen: () => null }))
vi.mock('@/components/library/LibraryPanel', () => ({
  LibraryPanel: () => <div data-testid="hash-library-content">Library content</div>,
}))
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
      { id: 'ws-1', name: 'Workspace One', status: 'active', is_default: true },
    ]),
    fetchSessions: vi.fn(async () => []),
  }
})

import { routeTree } from '@/routeTree.gen'
import { useUiStore } from '@/store/ui'

class RowResizeObserver {
  constructor(private readonly callback: ResizeObserverCallback) {}
  observe() {
    this.callback(
      [{ contentRect: { width: 1280 } as DOMRectReadOnly } as ResizeObserverEntry],
      this as unknown as ResizeObserver,
    )
  }
  unobserve() {}
  disconnect() {}
}

const originalMatchMedia = window.matchMedia

beforeEach(() => {
  window.history.replaceState(null, '', '/#/workspaces/ws-1/chat?panel=library')
  localStorage.setItem('omnipus_auth_username', 'uat-tester')
  useUiStore.getState().closePanel()
  vi.stubGlobal('ResizeObserver', RowResizeObserver)
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
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
  useUiStore.getState().closePanel()
  localStorage.removeItem('omnipus_auth_username')
  window.history.replaceState(null, '', '/')
  Object.defineProperty(window, 'matchMedia', { configurable: true, value: originalMatchMedia })
  vi.unstubAllGlobals()
})

describe('workspace Chat hash-history hard load', () => {
  it('adopts panel=library before projection and preserves it in the hash', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const router = createRouter({ routeTree, history: createHashHistory() })

    render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )

    await waitFor(() => expect(router.state.status).toBe('idle'), { timeout: 5000 })
    await waitFor(() => expect(useUiStore.getState().activePanel?.id).toBe('library'), {
      timeout: 5000,
    })
    expect(await screen.findByRole('complementary', { name: 'Library' })).toBeInTheDocument()
    expect(window.location.hash).toContain('/workspaces/ws-1/chat?panel=library')

    client.clear()
  })
})
