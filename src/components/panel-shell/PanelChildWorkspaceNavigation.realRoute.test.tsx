// RED oracle: side-panel-shell-spec.md §8.3 MAJ-213, FR-009, FR-018.
// DRIVER: foreground PANEL-CONTEXT-DRIVER-CONTINUE-1806 approves real child
// address-bar / Back-Forward navigation through the existing route. No invented
// workspace control, no direct lifecycle announcements and no mocked producer.
// REAL: source Expand/ownership, registry, router/auth, Tasks, Calendar,
// presence and lifecycle. Controlled edges: Window, BroadcastChannel, geometry,
// generated API replies and occurrence HTTP. This is NOT native multi-tab UAT.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, waitFor, within } from '@testing-library/react'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import {
  apiEdges, occurrenceHttpEdge, PanelChannelEdge, popupEdge, signedInState,
} from '../../../tests/fixtures/pe1-panel-process-edges'

vi.mock('@/lib/api', async (importOriginal) => {
  const { apiEdges } = await import('../../../tests/fixtures/pe1-panel-process-edges')
  return { ...(await importOriginal<typeof import('@/lib/api')>()), ...apiEdges }
})

import { routeTree } from '@/routeTree.gen'
import { getPanelTabHandleRegistry, getPanelTabPresence } from '@/lib/panelTabPresence'
import { panels } from './registry'
import { SidePanelShell } from './SidePanelShell'
import { PanelTabPresenceBridge } from './PanelTabPresenceBridge'
import { usePanelShellStore } from './panelShellStore'

const A = 'ws-1'
const B = 'workspace-current'
const cases = [
  { id: 'tasks', title: 'Tasks', otherId: 'calendar', marker: 'tasks-heading' },
  { id: 'calendar', title: 'Calendar', otherId: 'tasks', marker: 'calendar-toolbar' },
] as const
// not-wire-format: test observation of a browser process edge, never a gateway type.
class ObservedChannel extends PanelChannelEdge {
  static sent: { channel: string; data: unknown }[] = []
  override postMessage(data: unknown) {
    ObservedChannel.sent.push({ channel: this.name, data })
    super.postMessage(data)
  }
}

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

const clients: QueryClient[] = []
function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  clients.push(client)
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>
}

beforeAll(async () => {
  await Promise.all([
    import('@/components/workspaces/WorkspaceTasksTab'),
    import('@/components/screens/CalendarScreen'),
  ])
})
beforeEach(() => {
  vi.clearAllMocks()
  apiEdges.fetchAppState.mockReset().mockResolvedValue(signedInState)
  occurrenceHttpEdge.reset(globalThis.fetch)
  vi.stubGlobal('fetch', occurrenceHttpEdge.fetch)
  vi.stubGlobal('BroadcastChannel', ObservedChannel)
  vi.stubGlobal('ResizeObserver', RowResizeObserver)
  vi.stubGlobal('DOMMatrixReadOnly', class { m22 = 1 })
  vi.stubGlobal('scrollTo', vi.fn())
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, value: 800 })
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, value: 600 })
  sessionStorage.clear()
  ObservedChannel.sent = []
  usePanelShellStore.setState({ activePanel: null, panelWidth: null, guardPending: false, historyPushed: false, toasts: [] })
  window.history.replaceState({}, '', '/#/workspaces/ws-1/chat')
})
afterEach(() => {
  cleanup() // Real owners/announcements stop before the channel boundary resets.
  for (const client of clients.splice(0)) client.clear()
  usePanelShellStore.getState().closePanel()
  PanelChannelEdge.reset()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  occurrenceHttpEdge.verifyNoUnsupportedFetches()
})

async function assertContent(id: 'tasks' | 'calendar', container: HTMLElement, workspaceId: string) {
  const view = within(container)
  if (id === 'tasks') {
    expect(await view.findByTestId('tasks-heading')).toHaveTextContent('Team Task Backlog')
    expect(view.getAllByRole('radio').map((control) => control.textContent)).toEqual(['Board', 'List', 'Graph'])
    await waitFor(() => expect(apiEdges.fetchTasks).toHaveBeenCalledWith({ workspace_id: workspaceId, surface: 'user' }))
  } else {
    expect(await view.findByTestId('calendar-toolbar')).toBeInTheDocument()
    expect(view.getByTestId('calendar-grid')).toBeInTheDocument()
    await waitFor(() => expect(apiEdges.fetchTasks).toHaveBeenCalledWith({ workspace_id: workspaceId }))
  }
  expect(view.queryByText('Something went wrong')).not.toBeInTheDocument()
  expect(view.queryByText(/can't open this panel/i)).not.toBeInTheDocument()
}

async function sourceAndChild(row: typeof cases[number]) {
  const child = popupEdge()
  const open = vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
  const source = render(<>
    <PanelTabPresenceBridge />
    <SidePanelShell panels={panels} username="dana" chat={<textarea aria-label="Source draft" defaultValue="Source draft survives" />} />
  </>, { wrapper })
  await act(async () => {
    usePanelShellStore.getState().openPanel(row.id, { workspaceId: A })
  })
  await assertContent(row.id, source.container, A)
  const draft = within(source.container).getByRole('textbox', { name: 'Source draft' })
  fireEvent.click(within(source.container).getByRole('button', { name: `Expand ${row.title} panel` }))
  expect(open, 'The real source Expand must establish ownership, not the test').toHaveBeenCalledExactlyOnceWith('about:blank', '_blank')
  expect(child.location.replace).toHaveBeenCalledTimes(1)
  const destination = child.location.replace.mock.calls[0]?.[0]
  if (!destination) throw new Error('BLOCKED: source Expand produced no child URL — FR-009 / FR-018')
  const url = new URL(destination, window.location.href)
  const search = new URLSearchParams(url.hash.split('?')[1])
  const popout = search.get('popout')
  expect(popout, 'The legitimate source-generated popout tag must be retained').toMatch(/^[A-Za-z0-9-]+$/)
  if (!popout) throw new Error('BLOCKED: source ownership tag missing — FR-018')
  expect(url.hash.split('?')[0]).toBe(`#/panel/${row.id}`)
  expect(search.get('workspace')).toBe(A)
  expect(child.opener).toBeNull()
  expect(usePanelShellStore.getState().activePanel).toBeNull()
  expect(getPanelTabHandleRegistry().get(`${row.id}:${A}`)).toBe(child)
  const history = createMemoryHistory({ initialEntries: [url.hash.slice(1)] })
  const router = createRouter({ routeTree, history })
  const mounted = render(<RouterProvider router={router} />, { wrapper })
  await assertContent(row.id, mounted.container, A)
  expect(router.state.matches.map((match) => match.routeId)).toContain('/_fullscreen/panel/$panelId')
  expect(within(mounted.container).getByTestId('fullscreen-panel')).toBeInTheDocument()
  expect(within(mounted.container).queryByTestId('app-shell')).not.toBeInTheDocument()
  await waitFor(() => expect(getPanelTabPresence()).toEqual([{ panelId: row.id, workspaceId: A }]))
  return { source, child, open, popout, history, router, mounted, draft }
}

type Session = Awaited<ReturnType<typeof sourceAndChild>>
async function moveChildToB(row: typeof cases[number], session: Session) {
  // Existing real route + real router navigation: equivalent to a user entering
  // the child's B URL in its address bar. No React rerender or store injection.
  await act(async () => {
    await session.router.navigate({
      to: '/panel/$panelId', params: { panelId: row.id },
      search: { workspace: B, popout: session.popout },
    })
  })
  await assertContent(row.id, session.mounted.container, B)
  expect(session.router.state.location.pathname).toBe(`/panel/${row.id}`)
  expect(session.router.state.location.search).toEqual({ workspace: B, popout: session.popout })
  await waitFor(() => expect(getPanelTabPresence(), 'MAJ-213: real child presence must follow B').toEqual([{ panelId: row.id, workspaceId: B }]))
}

async function backFromChild(session: Session) {
  // Only jsdom's missing native Window.close is replaced; ownership and the
  // context announcement are produced by the mounted production route itself.
  vi.stubGlobal('closed', false)
  const close = vi.spyOn(window, 'close').mockImplementation(() => {
    session.child.close()
    vi.stubGlobal('closed', session.child.closed)
  })
  fireEvent.click(within(session.mounted.container).getByRole('button', { name: 'Back to chat' }))
  await waitFor(() => expect(close).toHaveBeenCalledTimes(1))
  expect(session.child.closed).toBe(true)
  session.mounted.unmount()
  await act(async () => {}) // Deliver the actual channel messages to the owner.
}

function closedMessages() {
  return ObservedChannel.sent
    .filter((entry) => entry.channel === 'omnipus-panel-popout-lifecycle')
    .map((entry) => entry.data)
    .filter((data) => typeof data === 'object' && data !== null && 'type' in data && data.type === 'popout-closed')
}

describe.each(cases)('$title actual child workspace navigation', (row) => {
  it('unchanged-A positive control: actual source Expand and child Back restore A', async () => {
    const session = await sourceAndChild(row)
    await backFromChild(session)
    expect(closedMessages()).toEqual([{
      type: 'popout-closed', panelId: row.id, popoutId: session.popout, context: { workspaceId: A },
    }])
    await waitFor(() => expect(usePanelShellStore.getState().activePanel).toEqual({ id: row.id, context: { workspaceId: A } }))
    expect([...getPanelTabHandleRegistry().keys()]).toEqual([])
    await assertContent(row.id, session.source.container, A)
    expect(session.draft).toHaveValue('Source draft survives')
    expect(session.open).toHaveBeenCalledTimes(1)
  })

  it('MAJ-213: real A-to-B and Back-Forward navigation moves the retained handle to current B', async () => {
    const session = await sourceAndChild(row)
    await moveChildToB(row, session)
    await act(async () => session.history.back())
    await waitFor(() => expect(session.router.state.location.search).toEqual({ workspace: A, popout: session.popout }))
    await assertContent(row.id, session.mounted.container, A)
    await act(async () => session.history.forward())
    await waitFor(() => expect(session.router.state.location.search).toEqual({ workspace: B, popout: session.popout }))
    await assertContent(row.id, session.mounted.container, B)
    await waitFor(() => expect(getPanelTabPresence()).toEqual([{ panelId: row.id, workspaceId: B }]))
    await waitFor(() => expect(
      [...getPanelTabHandleRegistry().keys()],
      'MAJ-213: source-owned handle must move from A to the actual child current workspace B',
    ).toEqual([`${row.id}:${B}`]))
    expect(getPanelTabHandleRegistry().get(`${row.id}:${B}`)).toBe(session.child)
    act(() => usePanelShellStore.getState().openPanel(row.id, { workspaceId: B }))
    expect(session.child.focus, 'FR-009: B re-entry must focus the original owned child').toHaveBeenCalledTimes(1)
    expect(usePanelShellStore.getState().activePanel).toBeNull()
    expect(session.open).toHaveBeenCalledTimes(1)
    expect(session.child.location.replace).toHaveBeenCalledTimes(1)
    expect(session.router.state.location.search).toEqual({ workspace: B, popout: session.popout })
  })

  it('FR-009: old A is no longer already-open after real child navigation to B', async () => {
    const session = await sourceAndChild(row)
    await moveChildToB(row, session)
    act(() => usePanelShellStore.getState().openPanel(row.id, { workspaceId: A }))
    await waitFor(() => expect(
      usePanelShellStore.getState().activePanel,
      'FR-009: A must open docked; the retained child actually shows B, not A',
    ).toEqual({ id: row.id, context: { workspaceId: A } }))
    await assertContent(row.id, session.source.container, A)
    expect(session.child.focus, 'A entry must not yank the B child back').not.toHaveBeenCalled()
    expect(session.child.location.replace).toHaveBeenCalledTimes(1)
    expect(session.open).toHaveBeenCalledTimes(1)
    expect(session.router.state.location.search).toEqual({ workspace: B, popout: session.popout })
  })

  it('FR-018: Back publishes last-viewed B and re-docks only its original owning source at B', async () => {
    const session = await sourceAndChild(row)
    await moveChildToB(row, session)
    await backFromChild(session)
    expect(closedMessages(), 'FR-018: real Back producer must publish B, never open-time A').toEqual([{
      type: 'popout-closed', panelId: row.id, popoutId: session.popout, context: { workspaceId: B },
    }])
    await waitFor(() => expect(
      usePanelShellStore.getState().activePanel,
      'FR-018: original source must re-dock the child last-viewed workspace B',
    ).toEqual({ id: row.id, context: { workspaceId: B } }))
    await assertContent(row.id, session.source.container, B)
    expect([...getPanelTabHandleRegistry().keys()]).toEqual([])
    expect(session.draft).toHaveValue('Source draft survives')
    expect(session.open).toHaveBeenCalledTimes(1)
  })

  it('separate-panel negative control: child Back never replaces a different source panel', async () => {
    const session = await sourceAndChild(row)
    await moveChildToB(row, session)
    act(() => usePanelShellStore.getState().openPanel(row.otherId, { workspaceId: A }))
    await assertContent(row.otherId, session.source.container, A)
    const otherPanel = within(session.source.container).getByTestId('side-panel')
    await backFromChild(session)
    expect(usePanelShellStore.getState().activePanel).toEqual({ id: row.otherId, context: { workspaceId: A } })
    expect(within(session.source.container).getByTestId('side-panel')).toBe(otherPanel)
    expect(session.draft).toHaveValue('Source draft survives')
    expect([...getPanelTabHandleRegistry().keys()]).toEqual([])
  })

  it('invalid-scope negative control: a missing workspace renders recovery and publishes no valid child presence', async () => {
    const session = await sourceAndChild(row)
    await act(async () => {
      await session.router.navigate({
        to: '/panel/$panelId', params: { panelId: row.id }, search: { popout: session.popout },
      })
    })
    expect(within(session.mounted.container).getByRole('heading', { name: "Can't open this panel" })).toBeInTheDocument()
    expect(within(session.mounted.container).queryByTestId(row.marker)).not.toBeInTheDocument()
    await waitFor(() => expect(getPanelTabPresence()).toEqual([]))
    expect(usePanelShellStore.getState().activePanel).toBeNull()
    expect(closedMessages(), 'Invalid context must not manufacture a re-dock').toEqual([])
    expect(session.child.close).not.toHaveBeenCalled()
    expect(session.open).toHaveBeenCalledTimes(1)
  })
})
