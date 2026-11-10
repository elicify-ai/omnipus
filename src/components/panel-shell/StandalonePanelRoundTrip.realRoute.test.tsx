// Fix-round oracle: LIVE-1 and F2. Keep the production chat route's URL
// adoption/projection, registered Tasks/Library codecs/content, SidePanelShell,
// hash history and full-screen route real. Only the conversation leaf/chrome,
// generated API responses and browser geometry/channel edges are controlled.
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { createHashHistory, createRouter, Outlet, RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiEdges, PanelChannelEdge, signedInState } from '../../../tests/fixtures/pe1-panel-process-edges'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { usePanelShellStore } from './panelShellStore'
import { setLibraryEditorDirty, resolveDiscardConfirmDialog } from '@/components/library/preview/unsavedGuard'
import { panels } from './registry'
import { SidePanelShell } from './SidePanelShell'

vi.mock('@/lib/api', async (importOriginal) => {
  const { apiEdges } = await import('../../../tests/fixtures/pe1-panel-process-edges')
  return { ...(await importOriginal<typeof import('@/lib/api')>()), ...apiEdges }
})
// Chrome and the conversation itself are outside the transition under test.
// The production WorkspaceChatRoute still owns usePanelDeepLink, and the
// actual shell/registry/content below are not replaced by a dummy destination.
vi.mock('@/components/layout/AppShell', () => ({ AppShell: () => <Outlet /> }))
vi.mock('@/components/workspaces/WorkspaceTabContainer', () => ({ WorkspaceTabContainer: () => <Outlet /> }))
vi.mock('@/components/workspaces/WorkspaceChatTab', () => ({
  WorkspaceChatTab: () => <SidePanelShell panels={panels} username="dana" chat={<textarea data-testid="chat-input" defaultValue="Keep this chat draft" />} />,
}))

import { routeTree } from '@/routeTree.gen'

let rowWidth = 1280
class LayoutObserver {
  constructor(private readonly callback: ResizeObserverCallback) {}
  observe(target: Element) {
    this.callback([{ target, contentRect: DOMRect.fromRect({ width: rowWidth, height: 700 }) } as ResizeObserverEntry], this as unknown as ResizeObserver)
  }
  unobserve() {}
  disconnect() {}
}
const clients: QueryClient[] = []
const histories: ReturnType<typeof createHashHistory>[] = []

beforeAll(async () => {
  await Promise.all([import('@/components/workspaces/WorkspaceTasksTab'), import('@/components/library/LibraryPanel')])
})
beforeEach(() => {
  rowWidth = 1280
  vi.clearAllMocks()
  apiEdges.fetchAppState.mockReset().mockResolvedValue(signedInState)
  vi.stubGlobal('matchMedia', vi.fn((query: string) => ({
    matches: query === '(display-mode: standalone)', media: query, onchange: null,
    addEventListener: vi.fn(), removeEventListener: vi.fn(), addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: vi.fn(),
  })))
  vi.stubGlobal('ResizeObserver', LayoutObserver)
  vi.stubGlobal('BroadcastChannel', PanelChannelEdge)
  vi.stubGlobal('scrollTo', vi.fn())
  vi.spyOn(window, 'open').mockReturnValue(null)
  vi.spyOn(window, 'close').mockImplementation(() => {})
  usePanelShellStore.setState({ activePanel: null, panelWidth: null, guardPending: false, historyPushed: false, toasts: [] })
  useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
  setLibraryEditorDirty(false)
  sessionStorage.clear()
})
afterEach(() => {
  cleanup()
  for (const history of histories.splice(0)) history.destroy()
  for (const client of clients.splice(0)) client.clear()
  resolveDiscardConfirmDialog(false)
  setLibraryEditorDirty(false)
  usePanelShellStore.getState().closePanel()
  PanelChannelEdge.reset()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

async function mountRoute(path: string) {
  window.history.replaceState({}, '', `/#${path}`)
  const history = createHashHistory()
  histories.push(history)
  const router = createRouter({ routeTree, history })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  clients.push(client)
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)
  await waitFor(() => expect(router.state.status).toBe('idle'))
  return router
}

async function assertRealContent(panel: 'tasks' | 'library', fullscreen: boolean) {
  const owner = fullscreen ? await screen.findByTestId('fullscreen-panel') : await screen.findByTestId('side-panel')
  const view = within(owner)
  if (panel === 'tasks') {
    expect(await view.findByTestId('tasks-heading')).toHaveTextContent('Team Task Backlog')
    expect(view.getAllByRole('radio').map((button) => button.textContent)).toEqual(['Board', 'List', 'Graph'])
    await waitFor(() => expect(apiEdges.fetchTasks).toHaveBeenCalledWith({ workspace_id: 'ws-1', surface: 'user' }))
  } else if (fullscreen) {
    expect(await view.findByText('PE1 Library selection')).toBeInTheDocument()
    await waitFor(() => expect(apiEdges.fetchLibraryContentVersioned).toHaveBeenCalledWith('ws-1', 'Current.txt'))
  } else {
    expect(await view.findByTestId('library-row-Current.txt')).toHaveTextContent('Current.txt')
  }
  expect(screen.queryByText(/can't open this panel/i)).toBeNull()
  expect(screen.queryByText('The panel link is incomplete or no longer available.')).toBeNull()
}

async function openSource(panel: 'tasks' | 'library') {
  const router = await mountRoute(`/workspaces/ws-1/chat?panel=${panel}`)
  await assertRealContent(panel, false)
  if (panel === 'library') {
    fireEvent.click(screen.getByTestId('library-row-Current.txt'))
    expect(await screen.findByText('PE1 Library selection')).toBeInTheDocument()
  }
  expect(usePanelShellStore.getState().activePanel).toEqual({ id: panel, context: { workspaceId: 'ws-1' } })
  return router
}

describe('W2/N1 accepted Chat pathname owns Library projection', () => {
  it.each(['/workspaces/ws-1/chat', '/workspaces/ws-1/chat/'])('%s projects Library and restores it from the resulting URL after a fresh mount', async (entry) => {
    // Founder-confirmed oracle: bare accepted Chat URLs own the same panel
    // contract, regardless of their optional final slash. No preset query.
    const router = await mountRoute(entry)
    expect(router.state.location.pathname).toBe(entry)
    expect(router.state.location.search).toEqual({})
    expect(usePanelShellStore.getState().activePanel).toBeNull()
    act(() => usePanelShellStore.getState().openPanel('library', { workspaceId: 'ws-1' }))
    await assertRealContent('library', false)
    await waitFor(() => expect(router.state.location.search).toEqual({ panel: 'library' }))
    expect(new URLSearchParams(window.location.hash.split('?')[1]).get('panel')).toBe('library')
    const projectedURL = window.location.hash.slice(1)
    cleanup()
    router.history.destroy()
    act(() => usePanelShellStore.getState().closePanel())
    const reloaded = await mountRoute(projectedURL)
    await assertRealContent('library', false)
    expect(reloaded.state.location.search).toEqual({ panel: 'library' })
    expect(usePanelShellStore.getState().activePanel).toEqual({ id: 'library', context: { workspaceId: 'ws-1' } })
  })
})

describe('LIVE-1 real registered standalone expansion and return', () => {
  it('direct Tasks full-screen entry is a positive control for the production codec and route validation', async () => {
    const router = await mountRoute('/panel/tasks?workspace=ws-1')
    await assertRealContent('tasks', true)
    expect(router.state.location.search).toEqual({ workspace: 'ws-1' })
  })

  it.each(['tasks', 'library'] as const)('%s keeps its real context, renders the real full-screen destination and reopens on Back', async (panel) => {
    const router = await openSource(panel)
    fireEvent.click(screen.getByRole('button', { name: `Expand ${panel === 'tasks' ? 'Tasks' : 'Library'} panel` }))
    await assertRealContent(panel, true)
    expect(router.state.location.pathname).toBe(`/panel/${panel}`)
    expect(router.state.location.search).toEqual(panel === 'tasks' ? { workspace: 'ws-1' } : { workspace: 'ws-1', path: 'Current.txt' })
    const hash = new URL(window.location.href).hash
    expect(new URLSearchParams(hash.split('?')[1]).get('workspace')).toBe('ws-1')
    expect(window.open).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Back to chat' }))
    await assertRealContent(panel, false)
    expect(router.state.location.pathname).toBe('/workspaces/ws-1/chat')
    expect(router.state.location.search).toEqual({ panel })
    expect(usePanelShellStore.getState().activePanel).toEqual({
      id: panel, context: panel === 'tasks' ? { workspaceId: 'ws-1' } : { workspaceId: 'ws-1', path: 'Current.txt' },
    })
    expect(window.close).not.toHaveBeenCalled()
  })
})

describe('F2 standalone expansion with real narrow takeover history', () => {
  it('retains the dock until delayed route loading settles; cleanup cannot Back out of expansion; return reopens takeover', async () => {
    // 640 is below the shell's approved 680px takeover boundary, not an
    // invented delay/threshold for the new implementation.
    rowWidth = 640
    const router = await openSource('tasks')
    expect(screen.getByTestId('side-panel')).toHaveAttribute('data-takeover', 'true')
    expect(usePanelShellStore.getState().historyPushed).toBe(true)
    expect(window.history.state).toMatchObject({ sidePanel: 'tasks' })
    const back = vi.spyOn(window.history, 'back')
    let acceptLoad!: (state: typeof signedInState) => void
    apiEdges.fetchAppState.mockImplementationOnce(() => new Promise((resolve) => { acceptLoad = resolve }))
    fireEvent.click(screen.getByRole('button', { name: 'Expand Tasks panel' }))
    await waitFor(() => expect(acceptLoad).toBeTypeOf('function'))
    expect(usePanelShellStore.getState().activePanel).toEqual({ id: 'tasks', context: { workspaceId: 'ws-1' } })
    expect(screen.getByTestId('side-panel')).toHaveAttribute('data-takeover', 'true')
    expect(screen.queryByTestId('fullscreen-panel')).toBeNull()
    expect(back).not.toHaveBeenCalled()
    await act(async () => { acceptLoad(signedInState) })
    await assertRealContent('tasks', true)
    expect(router.state.location.pathname).toBe('/panel/tasks')
    expect(router.state.location.search).toEqual({ workspace: 'ws-1' })
    expect(back).not.toHaveBeenCalled()
    expect(window.open).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Back to chat' }))
    await assertRealContent('tasks', false)
    expect(router.state.location.pathname).toBe('/workspaces/ws-1/chat')
    expect(router.state.location.search).toEqual({ panel: 'tasks' })
    expect(screen.getByTestId('side-panel')).toHaveAttribute('data-takeover', 'true')
    expect(usePanelShellStore.getState().activePanel).toEqual({ id: 'tasks', context: { workspaceId: 'ws-1' } })
    expect(window.close).not.toHaveBeenCalled()
  })
})
