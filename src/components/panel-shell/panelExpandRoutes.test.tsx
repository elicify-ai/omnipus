import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import type { PanelContext, PanelId } from './types'

const { libraryProps, browserProps } = vi.hoisted(() => ({
  libraryProps: vi.fn(),
  browserProps: vi.fn(),
}))

vi.mock('@/components/layout/AppShell', async () => {
  const ReactModule = await vi.importActual<typeof import('react')>('react')
  const { Outlet } = await vi.importActual<typeof import('@tanstack/react-router')>(
    '@tanstack/react-router',
  )
  return { AppShell: () => ReactModule.createElement(Outlet) }
})

vi.mock('@/components/chat/ChatScreen', () => ({
  ChatScreen: () => <div data-testid="chat-screen" />,
}))

vi.mock('@/components/library/LibraryExplorer', () => ({
  LibraryExplorer: (props: unknown) => {
    libraryProps(props)
    return <div data-testid="fullscreen-library" />
  },
}))

vi.mock('@/components/browser/BrowserLiveView', () => ({
  BrowserLiveView: (props: unknown) => {
    browserProps(props)
    return <div data-testid="fullscreen-browser" />
  },
}))

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchAppState: vi.fn(async () => ({
    onboarding_complete: true,
    identity: { signed_in: true },
  })),
}))

vi.mock('@/lib/panelTabPresence', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/panelTabPresence')>()),
  announcePanelTabPresence: vi.fn(() => ({ update: vi.fn(), stop: vi.fn() })),
}))

vi.mock('@/lib/libraryHandoff', () => ({
  announceLibraryPopoutClosed: vi.fn(),
  announceLibraryWorkspaceChanged: vi.fn(),
}))

import { panels } from './registry'
import { routeTree } from '@/routeTree.gen'

type RouteExpectation = {
  context: PanelContext
  routeId: string
  surfaceTestId: string
  assertContext: () => void
}

const routeExpectations = {
  library: {
    context: { workspaceId: 'workspace-current' },
    routeId: '/_app/library',
    surfaceTestId: 'fullscreen-library',
    assertContext: () => {
      expect(libraryProps).toHaveBeenCalledWith(
        expect.objectContaining({
          address: expect.objectContaining({ workspaceId: 'workspace-current' }),
        }),
      )
    },
  },
  browser: {
    context: { sessionId: 'session-current', agentId: 'agent-current' },
    routeId: '/_app/browser-live',
    surfaceTestId: 'fullscreen-browser',
    assertContext: () => {
      expect(browserProps).toHaveBeenCalledWith(
        expect.objectContaining({ sessionId: 'session-current', agentId: 'agent-current' }),
      )
    },
  },
} satisfies Partial<Record<PanelId, RouteExpectation>>

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
  vi.unstubAllGlobals()
})

describe('SP-38 registered panel expansion routes', () => {
  it('opens every registered panel on its own full-screen route without mounting Chat', async () => {
    vi.stubGlobal('scrollTo', vi.fn())

    for (const definition of panels) {
      const expected = routeExpectations[definition.id as keyof typeof routeExpectations]
      expect(expected, `${definition.id} needs a full-screen route expectation`).toBeDefined()
      if (!expected) continue

      const target = new URL(definition.expandTarget(expected.context), 'http://localhost')
      expect(target.hash, `${definition.id} must target the hash router`).toMatch(/^#\//)
      const history = createMemoryHistory({ initialEntries: [target.hash.slice(1)] })
      const router = createRouter({ routeTree, history })
      const mounted = render(<RouterProvider router={router} />)

      await waitFor(() => expect(router.state.status).toBe('idle'), { timeout: 5_000 })
      expect(router.state.matches.map((match) => match.routeId)).toContain(expected.routeId)
      expect(screen.getByTestId(expected.surfaceTestId)).toBeInTheDocument()
      expect(screen.queryByTestId('chat-screen')).not.toBeInTheDocument()
      expected.assertContext()

      mounted.unmount()
    }
  })
})
