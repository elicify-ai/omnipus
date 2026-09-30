import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { PanelContentProps, PanelContext, PanelId } from './types'

// These routes render panel content that reads via React Query (e.g. the
// library/browser panels' shellProps). The router alone provides no
// QueryClient, so every render() call here must be wrapped — matching the
// established pattern in src/test/screens.test.tsx: a fresh QueryClient per
// render via RTL's `wrapper` option.
function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={makeClient()}>{children}</QueryClientProvider>
}

const { renderedPanels } = vi.hoisted(() => ({
  renderedPanels: vi.fn<(panelId: PanelId, props: PanelContentProps) => void>(),
}))

vi.mock('@/components/layout/AppShell', () => ({
  AppShell: () => <div data-testid="app-shell" />,
}))

vi.mock('@/components/library/LibraryPanel', () => ({
  LibraryPanel: ({ shellProps }: { shellProps: PanelContentProps }) => {
    renderedPanels('library', shellProps)
    return <div data-testid="fullscreen-library" />
  },
}))

vi.mock('@/components/browser/BrowserLivePanel', () => ({
  BrowserLivePanel: ({ shellProps }: { shellProps: PanelContentProps }) => {
    renderedPanels('browser', shellProps)
    return <div data-testid="fullscreen-browser" />
  },
}))

vi.mock('@/components/workspaces/mail/MailPanel', () => ({
  // Mail's registered `content` (MailPanelContent, private to
  // mailPanelDefinition.tsx) derives narrower named props for MailPanel
  // instead of forwarding PanelContentProps directly like Library/Browser —
  // reconstruct the equivalent shape from those props for the assertion.
  MailPanel: (props: {
    workspaceId: string
    mailboxId?: string | null
    initialFolder?: string
    initialMessageRef?: string | null
  }) => {
    renderedPanels('mail', {
      context: {
        workspaceId: props.workspaceId,
        mailboxId: props.mailboxId ?? null,
      },
      presentation: 'fullscreen',
      close: () => undefined,
      expand: () => undefined,
      registerExpandContext: () => undefined,
      onWidthSettle: () => undefined,
    })
    return <div data-testid="fullscreen-mail" />
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

import { panels } from './registry'
import { routeTree } from '@/routeTree.gen'

type RouteExpectation = {
  context: PanelContext
  surfaceTestId: string
}

const routeExpectations = {
  library: {
    context: {
      workspaceId: 'workspace-current',
      path: 'Projects/Current.md',
    },
    surfaceTestId: 'fullscreen-library',
  },
  browser: {
    context: { sessionId: 'session-current', agentId: 'agent-current' },
    surfaceTestId: 'fullscreen-browser',
  },
  mail: {
    context: { workspaceId: 'workspace-current', mailboxId: 'agent-current' },
    surfaceTestId: 'fullscreen-mail',
  },
} satisfies Partial<Record<PanelId, RouteExpectation>>

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
  vi.unstubAllGlobals()
})

describe('SP-38 shell-owned full-screen routes', () => {
  it('round-trips every registered panel context through its search codec', () => {
    for (const definition of panels) {
      const expected = routeExpectations[definition.id as keyof typeof routeExpectations]
      expect(expected, `${definition.id} needs a full-screen route expectation`).toBeDefined()
      if (!expected) continue

      expect(definition.fullScreen.fromSearch(definition.fullScreen.toSearch(expected.context))).toEqual(
        expected.context,
      )
    }
  })

  it('renders every registered panel at #/panel/<id> without application chrome', async () => {
    vi.stubGlobal('scrollTo', vi.fn())

    for (const definition of panels) {
      const expected = routeExpectations[definition.id as keyof typeof routeExpectations]
      expect(expected, `${definition.id} needs a full-screen route expectation`).toBeDefined()
      if (!expected) continue

      const search = new URLSearchParams(definition.fullScreen.toSearch(expected.context))
      search.set('popout', `popout-${definition.id}`)
      const href = `#/panel/${definition.id}?${search.toString()}`
      expect(href).toMatch(new RegExp(`^#/panel/${definition.id}\\?`))

      const history = createMemoryHistory({ initialEntries: [href.slice(1)] })
      const router = createRouter({ routeTree, history })
      const mounted = render(<RouterProvider router={router} />, { wrapper })

      await waitFor(() => expect(router.state.status).toBe('idle'), { timeout: 5_000 })
      expect(router.state.matches.map((match) => match.routeId)).toContain(
        '/_fullscreen/panel/$panelId',
      )
      expect(await screen.findByTestId(expected.surfaceTestId)).toBeInTheDocument()
      expect(renderedPanels).toHaveBeenCalledWith(
        definition.id,
        expect.objectContaining({
          context: expected.context,
          presentation: 'fullscreen',
        }),
      )
      expect(screen.queryByTestId('app-shell')).not.toBeInTheDocument()
      expect(screen.queryByTestId('workspace-top-bar')).not.toBeInTheDocument()
      expect(screen.queryByTestId('workspace-header-menu')).not.toBeInTheDocument()
      expect(screen.queryByRole('complementary')).not.toBeInTheDocument()
      expect(screen.queryByTestId('side-panel-header')).not.toBeInTheDocument()

      mounted.unmount()
      renderedPanels.mockClear()
    }
  })

  it('shows a visible recovery link for an unknown panel instead of a blank page', async () => {
    const history = createMemoryHistory({ initialEntries: ['/panel/unknown?popout=unknown-1'] })
    const router = createRouter({ routeTree, history })
    render(<RouterProvider router={router} />, { wrapper })

    expect(await screen.findByRole('heading', { name: /can't open this panel/i })).toBeVisible()
    expect(screen.getByRole('link', { name: 'Back to Omnipus' })).toBeVisible()
    expect(screen.queryByTestId('app-shell')).not.toBeInTheDocument()
  })
})
