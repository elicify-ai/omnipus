import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, waitFor, within } from '@testing-library/react'
import { createRoot, type Root } from 'react-dom/client'
import { createMemoryHistory, createRootRoute, createRoute, createRouter, Outlet, RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import type { PlanListResponse, Workspace, WorkspaceDelegation } from '@/lib/api/generated/openapi-types'

/**
 * RED oracle: founder PANEL-CHILD-RELOAD-RED-1955; side-panel-shell spec §8.3
 * (MAJ-002/MAJ-202/MAJ-213, SP-18/SP-30), and opener-only restoration FR-018.
 * A child DOCUMENT's pagehide is not evidence that its WINDOW is closed.
 *
 * REAL: source Expand, registered PanelTabPresenceBridge owner, registry,
 * FullScreenPanelRoute, real router/hooks, Tasks content/context producer,
 * presence, channel validation/delivery listeners, and close polling.
 * EDGE FAKES: browser WindowProxy/navigation/close, BroadcastChannel transport,
 * ResizeObserver geometry, and HTTP. No production module or callback is mocked.
 *
 * The source uses the actual jsdom Window; child/non-owner use separate iframe
 * Windows/Documents, with fresh app module graphs. Child pagehide uses its native event
 * target. Transport queues only messages posted by production code and delivers
 * them in the RECEIVER's realm, never by calling a lifecycle handler directly.
 * Reload replaces the child document/module graph, not the returned handle.
 * The handle's readonly closed getter changes ONLY through handle.close().
 *
 * Cases: retained live child; genuine user close vs delayed close message;
 * explicit Back; actual A→B router navigation; source teardown; other-panel
 * and non-owner negatives; child reload at A and current B. Numeric-boundary
 * and malformed-input classes are outside this document-lifecycle regression.
 * CHECK probes (DEFERRED): forget a live handle on passive departure; remove
 * close polling; restore to open-time A instead of current B. GREEN, mutation,
 * and independent integrity verdict are DEFERRED TO FRESH CHECK.
 * Gaps: native browser reload/WindowProxy semantics, authentication, full Chat
 * content, Browser/Mail identities, and visible browser focus remain UNVERIFIED.
 */

const A = 'child-reload-workspace-a'
const B = 'child-reload-workspace-b'
// Vitest aliases top-level window/defaultView to globalThis. Use the actual
// jsdom Window exposed by its environment, so rebinding child globals cannot
// overwrite the source realm's constructors or turn child events into source ones.
const sourceWindow = (globalThis as typeof globalThis & {
  jsdom: { window: Window & typeof globalThis }
}).jsdom.window
const sourceDocument = sourceWindow.document

// not-wire-format: local browser-realm fixture, never persisted or sent to the gateway.
type Realm = {
  label: string
  win: Window & typeof globalThis
  document: Document
  container: HTMLDivElement
  frame?: HTMLIFrameElement
  root?: Root
  client?: QueryClient
  pagehides: number
  stopObserving: () => void
}

const realms: Realm[] = []
let currentRealm: Realm
const unsupportedHttp: string[] = []

function makeRealm(label: string, useSource = false): Realm {
  const frame = useSource ? undefined : sourceDocument.createElement('iframe')
  if (frame) sourceDocument.body.append(frame)
  const win = (frame?.contentWindow ?? sourceWindow) as Window & typeof globalThis
  const doc = win.document
  if (!doc || (frame && win === sourceWindow)) throw new Error('BLOCKED: distinct child document unavailable — child-reload oracle')
  const container = doc.createElement('div')
  doc.body.append(container)
  const realm: Realm = { label, win, document: doc, container, frame, pagehides: 0, stopObserving: () => {} }
  // jsdom has no layout/scrolling; this is a Window geometry edge, not routing.
  vi.spyOn(win, 'scrollTo').mockImplementation(() => {})
  const observePagehide = () => { realm.pagehides += 1 }
  win.addEventListener('pagehide', observePagehide)
  realm.stopObserving = () => win.removeEventListener('pagehide', observePagehide)
  realms.push(realm)
  return realm
}

function activate(realm: Realm): void {
  currentRealm = realm
  vi.stubGlobal('window', realm.win)
  vi.stubGlobal('document', realm.document)
  for (const name of ['Node', 'Element', 'HTMLElement', 'Event', 'CustomEvent', 'MessageEvent', 'MouseEvent', 'KeyboardEvent'] as const) {
    vi.stubGlobal(name, realm.win[name])
  }
  vi.stubGlobal('getComputedStyle', realm.win.getComputedStyle.bind(realm.win))
  vi.stubGlobal('scrollTo', realm.win.scrollTo.bind(realm.win))
  vi.stubGlobal('localStorage', realm.win.localStorage)
  vi.stubGlobal('sessionStorage', realm.win.sessionStorage)
}

// not-wire-format: browser transport packet; the payload comes ONLY from real postMessage calls.
type Delivery = { receiver: ChannelEdge; data: unknown }

class ChannelEdge {
  static endpoints = new Set<ChannelEdge>()
  static pending: Delivery[] = []
  readonly realm = currentRealm
  private listeners = new Set<(event: MessageEvent<unknown>) => void>()
  private disconnected = false

  constructor(readonly name: string) { ChannelEdge.endpoints.add(this) }
  addEventListener(type: string, listener: (event: MessageEvent<unknown>) => void) {
    if (type === 'message') this.listeners.add(listener)
  }
  removeEventListener(type: string, listener: (event: MessageEvent<unknown>) => void) {
    if (type === 'message') this.listeners.delete(listener)
  }
  postMessage(data: unknown) {
    if (this.disconnected) throw new DOMException('Channel is closed', 'InvalidStateError')
    for (const receiver of ChannelEdge.endpoints) {
      if (receiver !== this && receiver.name === this.name) {
        ChannelEdge.pending.push({ receiver, data: structuredClone(data) })
      }
    }
  }
  deliver(data: unknown) {
    if (this.disconnected) return
    const event = new this.realm.win.MessageEvent('message', { data })
    for (const listener of this.listeners) listener(event)
  }
  close() {
    this.disconnected = true
    ChannelEdge.endpoints.delete(this)
  }
  static reset() {
    for (const endpoint of [...this.endpoints]) endpoint.close()
    this.pending = []
  }
}

async function drain(realm: Realm, channel?: string) {
  activate(realm)
  while (ChannelEdge.pending.some((packet) => packet.receiver.realm === realm && (!channel || packet.receiver.name === channel))) {
    const delivered = ChannelEdge.pending.filter((packet) => packet.receiver.realm === realm && (!channel || packet.receiver.name === channel))
    ChannelEdge.pending = ChannelEdge.pending.filter((packet) => !delivered.includes(packet))
    await act(async () => {
      for (const packet of delivered) packet.receiver.deliver(packet.data)
    })
  }
}

async function flushTabs(...tabs: Realm[]) {
  const callerRealm = currentRealm
  try {
    while (ChannelEdge.pending.some((packet) => tabs.includes(packet.receiver.realm))) {
      for (const tab of tabs) await drain(tab)
    }
  } finally {
    activate(callerRealm)
  }
}

class RowResizeObserver {
  constructor(private readonly callback: ResizeObserverCallback) {}
  observe() {
    this.callback([{ contentRect: { width: 1280 } as DOMRectReadOnly } as ResizeObserverEntry], this as unknown as ResizeObserver)
  }
  unobserve() {}
  disconnect() {}
}

function workspace(id: string): Workspace {
  return {
    revision: '2'.repeat(64), id, name: 'Child reload fixture', status: 'active',
    pinned: false, pin_order: 0, task_count: 0, core_team: [],
    created_at: '2026-09-26T00:00:00Z', updated_at: '2026-09-26T00:00:00Z',
  }
}

const httpEdge = vi.fn<typeof fetch>(async (input, init) => {
  const raw = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
  const url = new URL(raw, 'http://localhost:3000')
  const method = init?.method ?? (typeof input === 'object' && 'method' in input ? input.method : 'GET')
  const reject = (): never => {
    unsupportedHttp.push(`${method} ${url.pathname}${url.search}`)
    throw new Error(`Unsupported child-reload HTTP edge: ${method} ${url.pathname}${url.search}`)
  }
  if (method !== 'GET' || url.origin !== 'http://localhost:3000') return reject()
  let body: unknown
  if (url.pathname === '/api/v1/tasks') {
    if (![A, B].includes(url.searchParams.get('workspace_id') ?? '') || url.searchParams.get('surface') !== 'user') return reject()
    body = []
  } else if (url.pathname === '/api/v1/agents') {
    body = []
  } else {
    const match = /^\/api\/v1\/workspaces\/([^/]+)(?:\/(plans|delegation))?$/.exec(url.pathname)
    if (!match || ![A, B].includes(match[1]!)) return reject()
    if (match[2] === 'plans') {
      const plans: PlanListResponse = { plans: [], total: 0 }
      body = plans
    } else if (match[2] === 'delegation') {
      const delegation: WorkspaceDelegation = { revision: '2'.repeat(64), workspace_id: match[1]!, team: [], edges: [], default_depth: 3 }
      body = delegation
    } else {
      body = workspace(match[1]!)
    }
  }
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
})

async function loadApp(realm: Realm) {
  activate(realm)
  vi.resetModules()
  const [shell, bridge, registry, store, presence] = await Promise.all([
    import('@/components/panel-shell/SidePanelShell'),
    import('@/components/panel-shell/PanelTabPresenceBridge'),
    import('@/components/panel-shell/registry'),
    import('@/store/ui'),
    import('@/lib/panelTabPresence'),
  ])
  // Preload the REAL lazy content, rather than widening a DOM wait for imports.
  await import('@/components/workspaces/WorkspaceTasksTab')
  return { ...shell, ...bridge, ...registry, ...store, ...presence }
}

type App = Awaited<ReturnType<typeof loadApp>>

function createClient(realm: Realm) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  realm.client = client
  return client
}

async function mountSource(realm: Realm, app: App, nonowner = false) {
  activate(realm)
  const { SidePanelShell, PanelTabPresenceBridge, panels, useUiStore } = app
  const client = createClient(realm)
  realm.root = createRoot(realm.container)
  await act(async () => {
    realm.root!.render(
      <QueryClientProvider client={client}>
        <PanelTabPresenceBridge />
        {!nonowner && <>
          <Button onClick={() => useUiStore.getState().openPanel('tasks', { workspaceId: A })}>Open Tasks A</Button>
          <Button onClick={() => useUiStore.getState().openPanel('tasks', { workspaceId: B })}>Open Tasks B</Button>
          <Button onClick={() => useUiStore.getState().openPanel('team', { workspaceId: A })}>Open Team</Button>
          <SidePanelShell panels={panels} username="child-reload-fixture" chat={<textarea data-testid="chat-input" aria-label="Source draft" defaultValue="Original source draft" />} />
        </>}
      </QueryClientProvider>,
    )
  })
}

// not-wire-format: a browser WindowProxy stand-in whose document changes on reload.
function childWindowBoundary() {
  let closedByCall = false
  let childDocument: Realm | undefined
  let destination: string | undefined
  const handle = {
    get closed() { return closedByCall },
    opener: sourceWindow as Window | null,
    focus: vi.fn<() => void>(),
    location: { replace: vi.fn<(url: string) => void>((url) => { destination = url }) },
    close: vi.fn<() => void>(() => {
      if (closedByCall) return
      closedByCall = true
      if (childDocument) {
        activate(childDocument)
        childDocument.win.dispatchEvent(new childDocument.win.Event('pagehide'))
      }
    }),
  }
  return {
    handle: handle as unknown as Window,
    focus: handle.focus,
    close: handle.close,
    replace: handle.location.replace,
    destination: () => destination,
    bindDocument(realm: Realm) {
      childDocument = realm
      Object.defineProperty(realm.win, 'closed', { configurable: true, get: () => handle.closed })
      realm.win.close = handle.close
      realm.win.focus = handle.focus
    },
  }
}

type ChildBoundary = ReturnType<typeof childWindowBoundary>

async function mountChild(realm: Realm, child: ChildBoundary, destination: string) {
  child.bindDocument(realm)
  const app = await loadApp(realm)
  const { Route } = await import('@/routes/_fullscreen.panel.$panelId')
  // Use the actual file-route component and hooks, not mocked Route/useSearch.
  // The isolated route tree omits unrelated app/auth screens; this is not UAT.
  const rootRoute = createRootRoute({ component: Outlet })
  const fullscreen = createRoute({ getParentRoute: () => rootRoute, id: '_fullscreen', component: Outlet })
  const panelRoute = Route.update({
    getParentRoute: () => fullscreen, id: '/panel/$panelId', path: '/panel/$panelId',
  } as Parameters<typeof Route.update>[0])
  const routeTree = rootRoute.addChildren([fullscreen.addChildren([panelRoute])])
  const path = new URL(destination, sourceWindow.location.href).hash.slice(1)
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: [path] }) })
  const client = createClient(realm)
  realm.root = createRoot(realm.container)
  await act(async () => {
    realm.root!.render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)
  })
  await waitFor(() => expect(within(realm.container).getByTestId('fullscreen-panel')).toBeInTheDocument())
  expect(router.state.matches.map((match) => match.routeId)).toContain('/_fullscreen/panel/$panelId')
  expect(within(realm.container).getByRole('button', { name: 'Back to chat' })).toHaveTextContent('Back to chat')
  expect(within(realm.container).getByTestId('tasks-heading')).toHaveTextContent('Team Task Backlog')
  return { realm, app, router }
}

async function unmountRealm(realm: Realm) {
  activate(realm)
  if (realm.root) {
    await act(async () => realm.root!.unmount())
    realm.root = undefined
  }
  realm.client?.clear()
}

async function openSession() {
  const source = makeRealm('source', true)
  const sourceApp = await loadApp(source)
  const child = childWindowBoundary()
  const open = vi.spyOn(source.win, 'open').mockReturnValue(child.handle)
  source.win.history.replaceState({}, '', '/#/workspaces/child-reload-workspace-a/chat')
  await mountSource(source, sourceApp)
  fireEvent.click(within(source.container).getByRole('button', { name: 'Open Tasks A' }))
  await waitFor(() => expect(within(source.container).getByTestId('tasks-heading')).toHaveTextContent('Team Task Backlog'))
  const draft = within(source.container).getByRole('textbox', { name: 'Source draft' })
  fireEvent.click(within(source.container).getByRole('button', { name: 'Expand Tasks panel' }))
  await act(async () => {})
  expect(open, 'source Expand, not a test registration, creates the one handle').toHaveBeenCalledExactlyOnceWith('about:blank', '_blank')
  expect(child.replace).toHaveBeenCalledTimes(1)
  const destination = child.destination()
  if (!destination) throw new Error('BLOCKED: actual source Expand produced no child destination — §8.3')
  const url = new URL(destination, source.win.location.href)
  const search = new URLSearchParams(url.hash.split('?')[1])
  const popout = search.get('popout')
  expect(url.hash.split('?')[0]).toBe('#/panel/tasks')
  expect(search.get('workspace')).toBe(A)
  expect(popout).toMatch(/^[A-Za-z0-9-]+$/)
  if (!popout) throw new Error('BLOCKED: actual source Expand produced no owner tag — FR-018')
  expect(child.handle.opener).toBeNull()
  expect(child.handle.closed).toBe(false)
  expect(child.close).toHaveBeenCalledTimes(0)
  expect(sourceApp.useUiStore.getState().activePanel).toBeNull()
  expect([...sourceApp.getPanelTabHandleRegistry().entries()]).toEqual([[`tasks:${A}`, child.handle]])

  const other = makeRealm('nonowner')
  const otherApp = await loadApp(other)
  await mountSource(other, otherApp, true)
  const mounted = await mountChild(makeRealm('child-document-1'), child, destination)
  expect(mounted.realm.win).not.toBe(source.win)
  expect(mounted.realm.document).not.toBe(source.document)
  expect(other.win).not.toBe(source.win)
  expect(mounted.app.useUiStore).not.toBe(sourceApp.useUiStore)
  expect(otherApp.useUiStore).not.toBe(sourceApp.useUiStore)
  await flushTabs(source, other, mounted.realm)
  activate(source)
  expect(sourceApp.getPanelTabPresenceKeys()).toEqual([sourceApp.panelPresenceKey({ panelId: 'tasks', workspaceId: A })])
  expect([...sourceApp.getPanelTabHandleRegistry().entries()]).toEqual([[`tasks:${A}`, child.handle]])
  expect(source.pagehides).toBe(0)
  expect(mounted.realm.pagehides).toBe(0)
  expect(draft).toHaveValue('Original source draft')
  return { source, sourceApp, child, open, popout, other, otherApp, mounted, draft }
}

type Session = Awaited<ReturnType<typeof openSession>>

async function navigateChild(session: Session, workspaceId: string) {
  activate(session.mounted.realm)
  await act(async () => {
    await session.mounted.router.navigate({
      to: '/panel/$panelId', params: { panelId: 'tasks' },
      search: { workspace: workspaceId, popout: session.popout },
    })
  })
  await flushTabs(session.source, session.other, session.mounted.realm)
  activate(session.source)
  expect(session.mounted.router.state.location.search).toEqual({ workspace: workspaceId, popout: session.popout })
  expect([...session.sourceApp.getPanelTabHandleRegistry().entries()]).toEqual([[`tasks:${workspaceId}`, session.child.handle]])
  expect(session.sourceApp.getPanelTabPresenceKeys()).toEqual([session.sourceApp.panelPresenceKey({ panelId: 'tasks', workspaceId })])
}

async function closeByWatcher(session: Session, workspaceId: string) {
  const observedRestorations: unknown[] = []
  const unsubscribe = session.sourceApp.useUiStore.subscribe((state, previous) => {
    if (state.activePanel !== previous.activePanel && state.activePanel?.id === 'tasks') observedRestorations.push(state.activePanel)
  })
  try {
    session.child.handle.close()
    expect(session.child.handle.closed, 'the real-close control must make the boundary readable as closed').toBe(true)
    expect(session.child.close).toHaveBeenCalledTimes(1)
    await unmountRealm(session.mounted.realm)
    // Delay the lifecycle-channel packet deliberately. The REAL 250ms watcher
    // must detect handle.closed; this cannot pass only on a close announcement.
    await drain(session.source, 'omnipus-panel-tab-presence')
    await act(async () => {
      await vi.waitFor(() => expect(session.sourceApp.useUiStore.getState().activePanel).toEqual({ id: 'tasks', context: { workspaceId } }))
    })
    expect(observedRestorations).toEqual([{ id: 'tasks', context: { workspaceId } }])
    expect([...session.sourceApp.getPanelTabHandleRegistry().entries()]).toEqual([])
    await flushTabs(session.source, session.other, session.mounted.realm)
    expect(observedRestorations, 'a later real route close message must not restore twice').toEqual([{ id: 'tasks', context: { workspaceId } }])
    expect(session.otherApp.useUiStore.getState().activePanel, 'FR-018: a non-owner app must never re-dock this child').toBeNull()
    expect(session.draft).toHaveValue('Original source draft')
    expect(session.open).toHaveBeenCalledTimes(1)
    expect(session.child.replace).toHaveBeenCalledTimes(1)
  } finally {
    unsubscribe()
  }
}

beforeEach(() => {
  unsupportedHttp.length = 0
  httpEdge.mockClear()
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  vi.stubGlobal('BroadcastChannel', ChannelEdge)
  vi.stubGlobal('ResizeObserver', RowResizeObserver)
  vi.stubGlobal('fetch', httpEdge)
})

afterEach(async () => {
  for (const realm of [...realms].reverse()) await unmountRealm(realm)
  ChannelEdge.reset()
  for (const realm of realms.splice(0)) {
    realm.stopObserving()
    realm.container.remove()
    realm.frame?.remove()
  }
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  vi.resetModules()
  expect(unsupportedHttp, 'no unexpected HTTP may be converted into an apparent lifecycle RED').toEqual([])
})

describe('real source Expand and distinct child-document reload lifecycle', () => {
  it('positive control: unchanged source owns one live child and re-entry focuses it without duplicate Expand', async () => {
    const session = await openSession()
    fireEvent.click(within(session.source.container).getByRole('button', { name: 'Open Tasks A' }))
    expect(session.child.focus).toHaveBeenCalledExactlyOnceWith()
    expect(session.sourceApp.useUiStore.getState().activePanel).toBeNull()
    expect([...session.sourceApp.getPanelTabHandleRegistry().entries()]).toEqual([[`tasks:${A}`, session.child.handle]])
    expect(session.child.handle.closed).toBe(false)
    expect(session.child.close).toHaveBeenCalledTimes(0)
    expect(session.open).toHaveBeenCalledTimes(1)
    expect(session.child.replace).toHaveBeenCalledTimes(1)
  })

  it('positive control: an actual child close is observed once by the owner watcher, not a broadcast shortcut', async () => {
    const session = await openSession()
    await closeByWatcher(session, A)
  })

  it('positive control: actual mounted child Back closes the handle and restores only its original opener', async () => {
    const session = await openSession()
    activate(session.mounted.realm)
    fireEvent.click(within(session.mounted.realm.container).getByRole('button', { name: 'Back to chat' }))
    await act(async () => {})
    expect(session.child.close).toHaveBeenCalledExactlyOnceWith()
    expect(session.child.handle.closed).toBe(true)
    await unmountRealm(session.mounted.realm)
    await flushTabs(session.source, session.other, session.mounted.realm)
    activate(session.source)
    expect(session.sourceApp.useUiStore.getState().activePanel).toEqual({ id: 'tasks', context: { workspaceId: A } })
    expect(session.otherApp.useUiStore.getState().activePanel).toBeNull()
    expect([...session.sourceApp.getPanelTabHandleRegistry().entries()]).toEqual([])
    expect(session.draft).toHaveValue('Original source draft')
  })

  it('positive control: actual child A-to-B route navigation preserves current-workspace ownership on user close', async () => {
    const session = await openSession()
    await navigateChild(session, B)
    await closeByWatcher(session, B)
  })

  it('negative control: source pagehide loses its handle but neither closes the child nor impersonates child departure', async () => {
    const session = await openSession()
    activate(session.source)
    session.source.win.dispatchEvent(new session.source.win.Event('pagehide'))
    expect(session.source.pagehides).toBe(1)
    expect(session.mounted.realm.pagehides, 'native event targets must keep source and child departure distinct').toBe(0)
    expect(session.child.handle.closed).toBe(false)
    expect(session.child.close).toHaveBeenCalledTimes(0)
    expect([...session.sourceApp.getPanelTabHandleRegistry().entries()]).toEqual([])
    expect(session.sourceApp.useUiStore.getState().activePanel).toBeNull()
    fireEvent.click(within(session.source.container).getByRole('button', { name: 'Open Tasks A' }))
    expect(session.sourceApp.useUiStore.getState().activePanel).toBeNull()
    expect(session.sourceApp.useUiStore.getState().toasts.at(-1)?.action?.label).toBe('Switch')
    expect(session.open).toHaveBeenCalledTimes(1)
  })

  it('negative control: closing the child never replaces a different panel already open in the source', async () => {
    const session = await openSession()
    fireEvent.click(within(session.source.container).getByRole('button', { name: 'Open Team' }))
    await act(async () => {})
    expect(session.sourceApp.useUiStore.getState().activePanel).toEqual({ id: 'team', context: { workspaceId: A } })
    session.child.handle.close()
    await unmountRealm(session.mounted.realm)
    await flushTabs(session.source, session.other, session.mounted.realm)
    expect(session.sourceApp.useUiStore.getState().activePanel).toEqual({ id: 'team', context: { workspaceId: A } })
    expect(session.otherApp.useUiStore.getState().activePanel).toBeNull()
    expect([...session.sourceApp.getPanelTabHandleRegistry().entries()]).toEqual([])
    expect(session.open).toHaveBeenCalledTimes(1)
  })

  it.each([A, B])('child-only pagehide and new document retain the open Window at %s, re-entry and future real-close ownership', async (workspaceId) => {
    const session = await openSession()
    if (workspaceId === B) await navigateChild(session, B)
    const oldChild = session.mounted.realm
    const oldChildApp = session.mounted.app
    const reloadDestination = `/#${session.mounted.router.state.location.href}`
    activate(oldChild)
    oldChild.win.dispatchEvent(new oldChild.win.Event('pagehide'))
    await unmountRealm(oldChild)
    await flushTabs(session.source, session.other, oldChild)

    // These are instrument checks BEFORE the reload assertion: the old child
    // really left, the source did NOT leave, and close() was never called.
    expect(oldChild.pagehides).toBe(1)
    expect(session.source.pagehides).toBe(0)
    expect(session.child.close).toHaveBeenCalledTimes(0)
    expect(session.child.handle.closed).toBe(false)
    activate(session.source)
    const handlesAfterPagehide = [...session.sourceApp.getPanelTabHandleRegistry().entries()]
    const dockAfterPagehide = session.sourceApp.useUiStore.getState().activePanel
    expect(session.sourceApp.getPanelTabPresenceKeys(), 'old child document presence ends on its real pagehide').toEqual([])

    // Execute the new document's REAL route/presence/context producer even on
    // the pre-fix run. Retention is checked against the departure-time snapshot:
    // a later announcement must not hide a transient ownership loss.
    session.mounted = await mountChild(makeRealm('child-document-2'), session.child, reloadDestination)
    expect(session.mounted.realm.document).not.toBe(oldChild.document)
    expect(session.mounted.app.useUiStore, 'the reloaded document must have a fresh app module graph').not.toBe(oldChildApp.useUiStore)
    await flushTabs(session.source, session.other, session.mounted.realm)
    activate(session.source)
    expect(session.sourceApp.getPanelTabPresenceKeys()).toEqual([session.sourceApp.panelPresenceKey({ panelId: 'tasks', workspaceId })])
    expect(session.mounted.router.state.location.search).toEqual({ workspace: workspaceId, popout: session.popout })
    expect(session.child.handle.closed).toBe(false)
    expect(session.child.close).toHaveBeenCalledTimes(0)
    expect(
      handlesAfterPagehide,
      '§8.3: child document pagehide is not user close; source must retain its still-open Window handle',
    ).toEqual([[`tasks:${workspaceId}`, session.child.handle]])
    expect(dockAfterPagehide, 'child reload must not prematurely re-dock').toBeNull()
    expect(session.sourceApp.useUiStore.getState().activePanel).toBeNull()
    expect([...session.sourceApp.getPanelTabHandleRegistry().entries()]).toEqual([[`tasks:${workspaceId}`, session.child.handle]])
    fireEvent.click(within(session.source.container).getByRole('button', { name: workspaceId === A ? 'Open Tasks A' : 'Open Tasks B' }))
    expect(session.child.focus).toHaveBeenCalledExactlyOnceWith()
    expect(session.sourceApp.useUiStore.getState().activePanel).toBeNull()
    expect(session.open).toHaveBeenCalledTimes(1)
    expect(session.child.replace).toHaveBeenCalledTimes(1)
    await closeByWatcher(session, workspaceId)
  })
})
