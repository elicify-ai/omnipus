import React from 'react'
import { cleanup, render, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PanelContentProps } from '@/components/panel-shell/types'

const { libraryProps } = vi.hoisted(() => ({
  libraryProps: vi.fn<(props: PanelContentProps) => void>(),
}))

vi.mock('@/components/chat/ChatScreen', () => ({ ChatScreen: () => null }))

vi.mock('@/components/library/LibraryPanel', () => ({
  LibraryPanel: ({ shellProps }: { shellProps: PanelContentProps }) => {
    libraryProps(shellProps)
    return null
  },
}))

vi.mock('framer-motion', () => ({
  motion: new Proxy({}, {
    get: (_target: object, prop: string) =>
      React.forwardRef(({ children, ...props }: Record<string, unknown>, ref: React.Ref<unknown>) =>
        React.createElement(prop, { ...props, ref }, children as React.ReactNode)),
  }),
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchAppState: vi.fn(async () => ({ onboarding_complete: true })),
  validateToken: vi.fn(async () => ({})),
  fetchNotifications: vi.fn(async () => ({ notifications: [], unread_count: 0 })),
  fetchTasks: vi.fn(async () => []),
  fetchAgents: vi.fn(async () => []),
  fetchVersion: vi.fn(async () => ({ version: 'test', build_sha: 'test' })),
  fetchWorkspaces: vi.fn(async () => [
    { id: 'ws-1', name: 'Workspace', status: 'active', is_default: true },
  ]),
  fetchSessions: vi.fn(async () => []),
}))

import { routeTree } from '@/routeTree.gen'
import { useUiStore } from '@/store/ui'

const originalMatchMedia = window.matchMedia

beforeEach(() => {
  localStorage.setItem('omnipus_auth_username', 'presentation-test')
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: () => ({
      matches: false,
      media: '',
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  })
  useUiStore.getState().closePanel()
})

afterEach(() => {
  cleanup()
  libraryProps.mockClear()
  localStorage.removeItem('omnipus_auth_username')
  Object.defineProperty(window, 'matchMedia', { configurable: true, value: originalMatchMedia })
  useUiStore.getState().closePanel()
})

describe('panel presentation through production entry points', () => {
  it('passes docked presentation when a chat deep link opens Library', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const router = createRouter({
      routeTree,
      history: createMemoryHistory({ initialEntries: ['/workspaces/ws-1/chat?panel=library'] }),
    })
    render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )

    await waitFor(() => expect(libraryProps).toHaveBeenCalledWith(
      expect.objectContaining({
        context: { workspaceId: 'ws-1' },
        presentation: 'docked',
      }),
    ), { timeout: 5_000 })
  })
})
