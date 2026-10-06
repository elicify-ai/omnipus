// PE1 route pack: add the THREE missing workspace-panel expectations without
// dropping the original Library/Browser/Mail contexts or unknown-link control.
// Exact-six inventory is mandatory. Registry/codecs/auth/routes/content REAL;
// replace former content/AppShell/presence mocks with generated API and browser
// process edges. Source-click/actual generated child proof lives in wave3FullScreen.
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, beforeAll, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { PanelContext, PanelId } from './types'
import { apiEdges, mailApiEdges, signedInState, PanelChannelEdge, BrowserSocketEdge } from '../../../tests/fixtures/pe1-panel-process-edges'

vi.mock('@/lib/api', async (importOriginal) => {
  const { apiEdges } = await import('../../../tests/fixtures/pe1-panel-process-edges')
  return { ...(await importOriginal<typeof import('@/lib/api')>()), ...apiEdges }
})
vi.mock('@/lib/api/mail', async (importOriginal) => {
  const { mailApiEdges } = await import('../../../tests/fixtures/pe1-panel-process-edges')
  return { ...(await importOriginal<typeof import('@/lib/api/mail')>()), ...mailApiEdges }
})

import { panels } from './registry'
import { routeTree } from '@/routeTree.gen'

const clients: QueryClient[] = []
function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  clients.push(client)
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

type RouteExpectation = {
  context: PanelContext
  surfaceTestId: string
  // Search keys the panel itself writes back once mounted (beyond the codec
  // output). Mail mirrors its on-screen location into the URL (D48, see
  // mailPanelDefinition.tsx): a mailbox with no folder/message chosen settles
  // on folder=inbox and an explicit-null message (the NULL_MARKER, ''). Every
  // other panel adds nothing.
  settledSearch?: Record<string, string>
}
const routeExpectations: Record<PanelId, RouteExpectation> = {
  library: { context: { workspaceId: 'workspace-current', path: 'Projects/Current.md' }, surfaceTestId: 'library-panel-fullscreen' },
  browser: { context: { sessionId: 'session-current', agentId: 'agent-current' }, surfaceTestId: 'browser-live-panel-fullscreen' },
  mail: { context: { workspaceId: 'workspace-current', mailboxId: 'agent-current' }, surfaceTestId: 'mail-panel', settledSearch: { folder: 'inbox', message: '' } },
  tasks: { context: { workspaceId: 'workspace-current' }, surfaceTestId: 'tasks-heading' },
  team: { context: { workspaceId: 'workspace-current' }, surfaceTestId: 'team-panel-fullscreen' },
  calendar: { context: { workspaceId: 'workspace-current' }, surfaceTestId: 'calendar-toolbar' },
}
// Spec §8.1 is the inventory oracle, never the production array itself.
const exactSix = ['browser', 'calendar', 'library', 'mail', 'tasks', 'team']

beforeAll(async () => {
  await Promise.all([
    import('@/components/workspaces/WorkspaceTasksTab'), import('@/components/workspaces/team/TeamPanel'),
    import('@/components/screens/CalendarScreen'), import('@/components/library/LibraryPanel'),
    import('@/components/workspaces/mail/MailPanel'), import('@/components/browser/BrowserLivePanel'),
  ])
})
beforeEach(() => {
  vi.clearAllMocks()
  apiEdges.fetchAppState.mockReset().mockResolvedValue(signedInState)
  vi.stubGlobal('BroadcastChannel', PanelChannelEdge)
  vi.stubGlobal('WebSocket', BrowserSocketEdge)
  vi.stubGlobal('scrollTo', vi.fn())
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.stubGlobal('DOMMatrixReadOnly', class { m22 = 1 })
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, value: 800 })
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, value: 600 })
})
afterEach(() => {
  cleanup()
  for (const client of clients.splice(0)) client.clear()
  PanelChannelEdge.reset()
  BrowserSocketEdge.reset()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

async function assertActualContent(id: PanelId) {
  // Concrete content + concrete context consumption replace mock-prop echoes.
  if (id === 'library') {
    expect(await screen.findByText('PE1 Library selection')).toBeInTheDocument()
    expect(apiEdges.fetchLibraryEntries).toHaveBeenCalledWith('workspace-current', 'Projects', false)
    expect(apiEdges.fetchLibraryContentVersioned).toHaveBeenCalledWith('workspace-current', 'Projects/Current.md')
  } else if (id === 'browser') {
    // Real viewer controls + concrete attachment, not a jsdom video claim.
    expect(screen.getByRole('textbox', { name: /^Address bar$/ })).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: /^Refresh page$/ })).toBeEnabled())
    expect(screen.getByRole('button', { name: /^Go back$/ })).toBeEnabled()
    expect(screen.getByRole('button', { name: /^Stop loading$/ })).toBeEnabled()
    await waitFor(() => expect(BrowserSocketEdge.sockets.flatMap((socket) => socket.sent)).toContainEqual({
      type: 'browser_attach', input_mode: 'dedicated', session_id: 'session-current', agent_id: 'agent-current',
    }))
  } else if (id === 'mail') {
    expect(await screen.findByRole('combobox', { name: /^Mailbox$/ })).toHaveTextContent('agent-current')
    expect(screen.getByRole('tab', { name: /^Inbox$/ })).toHaveAttribute('aria-selected', 'true')
    expect(mailApiEdges.fetchMailMessages).toHaveBeenCalledWith('workspace-current', 'agent-current', 'inbox', expect.objectContaining({ limit: 25 }))
  } else if (id === 'tasks') {
    // SP-32/33/36 body oracle, independent of the shell's Tasks title.
    expect(screen.getByTestId('tasks-heading')).toBeInTheDocument()
    expect(screen.getAllByRole('radio').map((control) => control.textContent)).toEqual(['Board', 'List', 'Graph'])
    expect(screen.getByTestId('tasks-view-board')).toHaveAttribute('aria-checked', 'true')
    expect((await screen.findAllByRole('group', { name: / column$/ })).map((group) => group.getAttribute('aria-label'))).toEqual([
      'Inbox column', 'Next column', 'In Progress column', 'Blocked column', 'Done column', 'Failed column',
    ])
    expect(screen.getByRole('heading', { name: /^Plans$/ })).toBeInTheDocument()
    expect(apiEdges.fetchTasks).toHaveBeenCalledWith({ workspace_id: 'workspace-current', surface: 'user' })
  } else if (id === 'team') {
    expect(await screen.findByRole('heading', { name: /^Team & delegation$/ })).toBeInTheDocument()
    expect(apiEdges.fetchWorkspace).toHaveBeenCalledWith('workspace-current')
    expect(apiEdges.fetchWorkspaceDelegation).toHaveBeenCalledWith('workspace-current')
  } else {
    expect(screen.getByTestId('calendar-grid')).toBeInTheDocument()
    expect(apiEdges.fetchTasks).toHaveBeenCalledWith({ workspace_id: 'workspace-current' })
  }
  expect(screen.queryByText('Something went wrong')).not.toBeInTheDocument()
  expect(screen.queryByRole('heading', { name: /can't open this panel/i })).not.toBeInTheDocument()
}

describe('SP-38 shell-owned full-screen routes retained by PE1', () => {
  it('round-trips every registered panel context through its search codec', () => {
    expect(Object.keys(routeExpectations).sort(), 'PE1 needs all exact SIX independent route expectations').toEqual(exactSix)
    expect(panels.map((definition) => definition.id).sort(), 'no missing or extra registered route').toEqual(exactSix)
    for (const definition of panels) {
      const expected = routeExpectations[definition.id]
      expect(expected, `${definition.id} needs a full-screen route expectation`).toBeDefined()
      expect(definition.fullScreen.fromSearch(definition.fullScreen.toSearch(expected.context))).toEqual(expected.context)
    }
  })

  it('renders every registered panel at #/panel/<id> without application chrome', async () => {
    expect(panels.map((definition) => definition.id).sort()).toEqual(exactSix)
    for (const definition of panels) {
      const expected = routeExpectations[definition.id]
      expect(expected, `${definition.id} needs a full-screen route expectation`).toBeDefined()
      const search = new URLSearchParams(definition.fullScreen.toSearch(expected.context))
      search.set('popout', `popout-${definition.id}`)
      const href = `#/panel/${definition.id}?${search.toString()}`
      expect(href).toMatch(new RegExp(`^#/panel/${definition.id}\\?`))
      const history = createMemoryHistory({ initialEntries: [href.slice(1)] })
      const router = createRouter({ routeTree, history })
      const mounted = render(<RouterProvider router={router} />, { wrapper })
      await waitFor(() => expect(router.state.status).toBe('idle'), { timeout: 5_000 })
      expect(router.state.matches.map((match) => match.routeId)).toContain('/_fullscreen/panel/$panelId')
      expect(await screen.findByTestId(expected.surfaceTestId)).toBeInTheDocument()
      expect(router.state.location.pathname).toBe(`/panel/${definition.id}`)
      expect(router.state.location.search).toEqual({ ...Object.fromEntries(search), ...expected.settledSearch })
      expect(screen.getByRole('button', { name: /^Back to chat$/ })).toBeInTheDocument()
      await assertActualContent(definition.id)
      expect(apiEdges.fetchAppState).toHaveBeenCalled()
      expect(screen.queryByTestId('app-shell')).not.toBeInTheDocument()
      expect(screen.queryByTestId('workspace-top-bar')).not.toBeInTheDocument()
      expect(screen.queryByTestId('workspace-header-menu')).not.toBeInTheDocument()
      // Application chrome = the app sidebar (its "Main navigation" landmark
      // and its aside) and the app main-content wrapper. Library's note reader
      // rails (<aside data-testid="knowledge-reader-rails">) are CONTENT inside
      // the full-screen panel, so they are the only complementary landmark
      // allowed; any other complementary landmark is chrome.
      expect(screen.queryByRole('navigation', { name: 'Main navigation' })).not.toBeInTheDocument()
      expect(screen.queryByTestId('app-main-content')).not.toBeInTheDocument()
      expect(
        screen
          .queryAllByRole('complementary')
          .filter((landmark) => landmark.getAttribute('data-testid') !== 'knowledge-reader-rails'),
      ).toEqual([])
      expect(screen.queryByTestId('side-panel-header')).not.toBeInTheDocument()
      mounted.unmount()
      apiEdges.fetchAppState.mockClear()
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

  for (const id of ['tasks', 'team', 'calendar'] as const) {
    it(`${id} incomplete workspace link remains fail-visible, not a blank fullscreen`, async () => {
      const history = createMemoryHistory({ initialEntries: [`/panel/${id}?popout=incomplete-${id}`] })
      const router = createRouter({ routeTree, history })
      render(<RouterProvider router={router} />, { wrapper })
      expect(await screen.findByRole('heading', { name: /can't open this panel/i })).toBeVisible()
      expect(screen.getByText('The panel link is incomplete or no longer available.', { exact: true })).toBeVisible()
      expect(screen.getByRole('link', { name: 'Back to Omnipus' })).toBeVisible()
      expect(screen.queryByTestId('fullscreen-panel')).not.toBeInTheDocument()
    })
  }
})
