import { createHash } from 'node:crypto'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, waitFor, within } from '@testing-library/react'
import { createRoot, type Root } from 'react-dom/client'
import { createMemoryHistory, createRootRoute, createRoute, createRouter, Outlet, RouterProvider } from '@tanstack/react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import type { PlanListResponse, Workspace, WorkspaceDelegation } from '@/lib/api/generated/openapi-types'

/**
 * Spec-first oracle: approved side-panel-shell-spec.md §6 opener-only re-dock,
 * FR-018/MAJ-208, §8.3 and SP-18/SP-30; PANEL-CLOSE-PRESENCE-ORDER-RED-2342.
 * Closing an owned Window restores its CURRENT panel once in its mounted source,
 * unless a different panel or a genuinely live matching full-page tab prevents it.
 * A queued leave from that CLOSED owned child must not remove the restored dock.
 *
 * Reuses the immutable childDocumentReload fixture's browser-realm scaffolding,
 * in this disjoint file. REAL: source shell Expand, registry, owner/watch callback,
 * PanelTabPresenceBridge, UI store, full-screen route/Back, context producer,
 * presence validation/listeners, Tasks content and router. No production module,
 * resolver, store action, or owner callback is replaced, spied or called directly.
 * EDGE ONLY: WindowProxy open/close/navigation, BroadcastChannel queue, periodic
 * browser clock, geometry and HTTP. Each queue packet was posted by real code;
 * sender/receiver realms and child tab/owner IDs are checked before delivery.
 * The handle's closed getter changes ONLY via its close() boundary method.
 *
 * The clock captures actual setInterval registrations and dispatches the real
 * 250ms watch callback as an explicit event; no elapsed-time threshold proves
 * logic. Date.now is frozen so stale-cache expiry cannot hide the delayed leave.
 * Only one child-to-source leave packet is withheld; channel payloads are never
 * edited or manufactured. Cross-channel delivery order, not source code, changes.
 *
 * Cases: watcher and real Back, leave-first/last, current A/B; no-close and
 * foreign-owner oracle rejection; live matching manual tab/Switch; different panel.
 * Gaps: native WindowProxy/browser transport/focus, full Chat/auth/Mail/Browser
 * content and UAT. Numeric/malformed-input coverage is outside this ordering unit.
 * GREEN, production mutation and independent CHECK are DEFERRED TO FRESH CHECK.
 * Author-side negative oracle probes are NOT an integrity-audit verdict.
 */

const A = 'close-presence-order-workspace-a'
const B = 'close-presence-order-workspace-b'
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
  if (!doc || (frame && win === sourceWindow)) throw new Error('BLOCKED: distinct child document unavailable — close-presence-order oracle')
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

const PRESENCE_CHANNEL = 'omnipus-panel-tab-presence'
const LIFECYCLE_CHANNEL = 'omnipus-panel-popout-lifecycle'

// not-wire-format: local browser transport envelope, never sent to the gateway.
type Delivery = { sender: ChannelEdge; receiver: ChannelEdge; data: unknown }

function messageRecord(data: unknown): Record<string, unknown> {
  if (data === null || typeof data !== 'object' || Array.isArray(data)) {
    throw new Error('BLOCKED: browser transport did not contain an object packet')
  }
  return data as Record<string, unknown>
}

class ChannelEdge {
  static endpoints = new Set<ChannelEdge>()
  static pending: Delivery[] = []
  static delivered: Delivery[] = []
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
        ChannelEdge.pending.push({ sender: this, receiver, data: structuredClone(data) })
      }
    }
  }
  deliver(packet: Delivery) {
    if (this.disconnected) throw new Error('BLOCKED: delivery targeted a disconnected browser endpoint')
    expect(currentRealm, 'transport must execute in its actual receiver realm').toBe(this.realm)
    expect(packet.receiver).toBe(this)
    expect(packet.sender.name).toBe(this.name)
    const event = new this.realm.win.MessageEvent('message', { data: packet.data })
    ChannelEdge.delivered.push(packet)
    for (const listener of this.listeners) listener(event)
  }
  close() {
    this.disconnected = true
    ChannelEdge.endpoints.delete(this)
  }
  static reset() {
    for (const endpoint of [...this.endpoints]) endpoint.close()
    this.pending = []
    this.delivered = []
  }
}

// not-wire-format: local process-edge clock registration; no app state is set.
type PeriodicEvent = { realm: Realm; callback: () => void; delay: number }

class ClockEdge {
  static nextId = 0
  static intervals = new Map<number, PeriodicEvent>()
  static ticks: string[] = []

  static install() {
    vi.stubGlobal('setInterval', (callback: () => void, delay: number) => {
      if (typeof callback !== 'function' || !currentRealm) {
        throw new Error('BLOCKED: unsupported periodic browser-clock registration')
      }
      const id = ++this.nextId
      this.intervals.set(id, { realm: currentRealm, callback, delay })
      return id
    })
    vi.stubGlobal('clearInterval', (id: number) => { this.intervals.delete(id) })
    vi.spyOn(Date, 'now').mockReturnValue(Date.parse('2026-10-06T00:00:00Z'))
  }

  static async tickClosePoll(realm: Realm) {
    // 250ms is the real watchPopoutClosed timer period, not a wait threshold.
    const due = [...this.intervals.values()].filter((event) => event.realm === realm && event.delay === 250)
    expect(due, 'instrument: exactly one source-owned close poll must be registered').toHaveLength(1)
    activate(realm)
    await act(async () => {
      this.ticks.push(`${realm.label}:close-poll`)
      due[0]!.callback()
    })
  }

  static reset() { this.intervals.clear(); this.ticks = []; this.nextId = 0 }
}

async function drain(realm: Realm, channel?: string) {
  activate(realm)
  while (ChannelEdge.pending.some((packet) => packet.receiver.realm === realm && (!channel || packet.receiver.name === channel))) {
    const delivered = ChannelEdge.pending.filter((packet) => packet.receiver.realm === realm && (!channel || packet.receiver.name === channel))
    ChannelEdge.pending = ChannelEdge.pending.filter((packet) => !delivered.includes(packet))
    await act(async () => {
      for (const packet of delivered) packet.receiver.deliver(packet)
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
    throw new Error(`Unsupported close-presence-order HTTP edge: ${method} ${url.pathname}${url.search}`)
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
  await import('@/components/workspaces/team/TeamPanel')
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
          <SidePanelShell panels={panels} username="close-presence-order-fixture" chat={<textarea data-testid="chat-input" aria-label="Source draft" defaultValue="Original source draft" />} />
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
  source.win.history.replaceState({}, '', '/#/workspaces/close-presence-order-workspace-a/chat')
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

function opaqueIdentity(workspaceId: string) {
  return createHash('sha256').update(`tasks:${workspaceId}`).digest('hex')
}

function latestPresence(realm: Realm, receiver: Realm) {
  const packets = ChannelEdge.delivered.filter((packet) =>
    packet.sender.realm === realm && packet.receiver.realm === receiver &&
    packet.sender.name === PRESENCE_CHANNEL && messageRecord(packet.data).type === 'presence',
  )
  const packet = packets.at(-1)
  if (!packet) throw new Error('BLOCKED: real child emitted no delivered presence')
  return packet
}

async function deliverOne(packet: Delivery) {
  expect(ChannelEdge.pending, 'instrument: only a real pending packet can be delivered').toContain(packet)
  ChannelEdge.pending.splice(ChannelEdge.pending.indexOf(packet), 1)
  activate(packet.receiver.realm)
  await act(async () => packet.receiver.deliver(packet))
}

function heldOwnedLeave(session: Session, presence: Delivery) {
  const leavePackets = ChannelEdge.pending.filter((packet) =>
    packet.sender.realm === session.mounted.realm && packet.receiver.realm === session.source &&
    packet.sender.name === PRESENCE_CHANNEL && messageRecord(packet.data).type === 'leave',
  )
  expect(leavePackets, 'instrument: hold exactly one real child-to-source leave').toHaveLength(1)
  const leave = leavePackets[0]!
  expect(leave.sender, 'leave must come from the actual matching child announcer endpoint').toBe(presence.sender)
  expect(leave.data).toEqual({ type: 'leave', tabId: messageRecord(presence.data).tabId })
  return leave
}

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
  expect(session.sourceApp.getPanelTabPresenceKeys()).toEqual([opaqueIdentity(workspaceId)])
}

function sourceSnapshot(session: Session) {
  activate(session.source)
  return {
    activePanel: session.sourceApp.useUiStore.getState().activePanel,
    presence: [...session.sourceApp.getPanelTabPresenceKeys()],
    handles: [...session.sourceApp.getPanelTabHandleRegistry().keys()],
    childClosed: session.child.handle.closed,
    childCloseCalls: session.child.close.mock.calls.length,
    sourceMounted: session.source.root !== undefined && session.source.container.isConnected,
    sourcePagehides: session.source.pagehides,
    otherActivePanel: session.otherApp.useUiStore.getState().activePanel,
    draft: (session.draft as HTMLTextAreaElement).value,
    sameDraftElement: within(session.source.container).getByRole('textbox', { name: 'Source draft' }) === session.draft,
    dockRendered: within(session.source.container).queryByTestId('side-panel') !== null,
    openCalls: session.open.mock.calls.length,
    navigateCalls: session.child.replace.mock.calls.length,
    switchActions: session.sourceApp.useUiStore.getState().toasts.map((toast) => toast.action?.label ?? null),
  }
}

type SourceSnapshot = ReturnType<typeof sourceSnapshot>

function assertDockedTasks(snapshot: SourceSnapshot, workspaceId: string) {
  expect(snapshot.activePanel, 'FR-018: owned-close source must contain the same Tasks dock').toEqual({
    id: 'tasks', context: { workspaceId },
  })
}

function assertSourceSurvived(snapshot: SourceSnapshot) {
  expect(snapshot.sourceMounted, 'source must remain mounted throughout child close').toBe(true)
  expect(snapshot.sourcePagehides, 'the original source document must not depart').toBe(0)
  expect(snapshot.otherActivePanel, 'FR-018: the non-owner must never restore').toBeNull()
  expect(snapshot.draft, 'no blank Chat: retain the original source draft').toBe('Original source draft')
  expect(snapshot.sameDraftElement, 'no blank Chat: retain its actual source DOM node').toBe(true)
  expect(snapshot.openCalls, 'SP-18/SP-30: never create a duplicate child').toBe(1)
  expect(snapshot.navigateCalls, 'existing child must never be re-navigated').toBe(1)
}

function printOrderReceipt(label: string, details: unknown) {
  console.info(`closePresenceOrder ${JSON.stringify({ label, details })}`)
}

beforeEach(() => {
  unsupportedHttp.length = 0
  httpEdge.mockClear()
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  vi.stubGlobal('BroadcastChannel', ChannelEdge)
  vi.stubGlobal('ResizeObserver', RowResizeObserver)
  vi.stubGlobal('fetch', httpEdge)
  ClockEdge.install()
})

afterEach(async () => {
  for (const realm of [...realms].reverse()) await unmountRealm(realm)
  ChannelEdge.reset()
  ClockEdge.reset()
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

describe('closePresenceOrder: real source restoration and delayed child presence leave', () => {
  const orders = [
    { label: 'positive watcher leave-first restores the source exactly once', mode: 'watcher', leaveFirst: true, workspaceId: A },
    { label: 'adverse watcher close-before-leave restores the source exactly once', mode: 'watcher', leaveFirst: false, workspaceId: A },
    { label: 'positive mounted Back leave-first restores CURRENT workspace B once', mode: 'back', leaveFirst: true, workspaceId: B },
    { label: 'adverse mounted Back close-before-leave restores CURRENT workspace B once', mode: 'back', leaveFirst: false, workspaceId: B },
  ] as const

  it.each(orders)('$label', async ({ label, mode, leaveFirst, workspaceId }) => {
    const session = await openSession()
    if (workspaceId === B) await navigateChild(session, B)
    const presence = latestPresence(session.mounted.realm, session.source)
    const identityPacket = messageRecord(presence.data)
    expect(identityPacket.identityKey).toBe(opaqueIdentity(workspaceId))
    expect(identityPacket.tabId).toMatch(/^[A-Za-z0-9-]{8,128}$/)
    expect(identityPacket.focusNonce).toMatch(/^[A-Za-z0-9-]{8,128}$/)
    const before = sourceSnapshot(session)
    expect(before.activePanel).toBeNull()
    expect(before.childClosed).toBe(false)
    expect(before.handles).toEqual([`tasks:${workspaceId}`])
    expect(before.presence).toEqual([opaqueIdentity(workspaceId)])
    assertSourceSurvived(before)
    const dockEvents: unknown[] = []
    let dockRemovals = 0
    const unsubscribe = session.sourceApp.useUiStore.subscribe((state, previous) => {
      if (state.activePanel === previous.activePanel) return
      if (state.activePanel?.id === 'tasks') dockEvents.push(state.activePanel)
      if (state.activePanel === null && previous.activePanel?.id === 'tasks') dockRemovals += 1
    })
    try {
      activate(session.mounted.realm)
      if (mode === 'back') {
        fireEvent.click(within(session.mounted.realm.container).getByRole('button', { name: 'Back to chat' }))
        await act(async () => {})
      } else {
        session.child.handle.close()
      }
      expect(session.child.close, 'the actual user close/Back boundary must execute close()').toHaveBeenCalledExactlyOnceWith()
      expect(session.child.handle.closed, 'closed is proven by close(), not injected state').toBe(true)
      expect(session.mounted.realm.pagehides).toBe(1)
      const leave = heldOwnedLeave(session, presence)
      expect(sourceSnapshot(session).activePanel, 'queued messages/clock cannot restore before owner is scheduled').toBeNull()
      if (leaveFirst) await deliverOne(leave)
      if (mode === 'watcher') {
        // Leave EVERY lifecycle message queued. Only the real poll observes close.
        await ClockEdge.tickClosePoll(session.source)
      } else {
        const closePackets = ChannelEdge.pending.filter((packet) =>
          packet.sender.realm === session.mounted.realm && packet.receiver.realm === session.source &&
          packet.sender.name === LIFECYCLE_CHANNEL && messageRecord(packet.data).type === 'popout-closed',
        )
        expect(closePackets, 'Back must post exactly one source-directed close announcement').toHaveLength(1)
        expect(closePackets[0]!.data).toEqual({ type: 'popout-closed', panelId: 'tasks', popoutId: session.popout, context: { workspaceId } })
        await deliverOne(closePackets[0]!)
      }
      const afterOwner = sourceSnapshot(session)
      if (!leaveFirst) {
        expect(ChannelEdge.pending, 'the exact owned-child leave must still be held after the owner callback').toContain(leave)
        expect(afterOwner.presence, 'prove the adverse order, without waiting for cache expiry').toEqual([opaqueIdentity(workspaceId)])
        await deliverOne(leave)
      }
      await unmountRealm(session.mounted.realm)
      await flushTabs(session.source, session.other, session.mounted.realm)
      const afterLeave = sourceSnapshot(session)
      printOrderReceipt(label, {
        before, afterOwner, afterLeave, dockEvents, dockRemovals,
        childTabId: identityPacket.tabId, ownerSessionId: session.popout,
        order: leaveFirst ? ['leave', mode, 'remaining-messages'] : [mode, 'leave', 'remaining-messages'],
        sourceDeliveries: ChannelEdge.delivered.filter((packet) => packet.receiver.realm === session.source).map((packet) => ({
          sender: packet.sender.realm.label, channel: packet.sender.name, data: packet.data,
        })),
        clockTicks: ClockEdge.ticks,
      })
      assertSourceSurvived(afterOwner)
      assertSourceSurvived(afterLeave)
      expect(afterLeave.childClosed).toBe(true)
      expect(afterLeave.handles).toEqual([])
      expect(afterLeave.presence).toEqual([])
      // The SAME spec oracle applies to both orders. Do not adapt it to null.
      assertDockedTasks(afterOwner, workspaceId)
      assertDockedTasks(afterLeave, workspaceId)
      expect(afterLeave.dockRendered, 'the restored Tasks dock must actually render').toBe(true)
      expect(dockEvents, 'FR-018: exactly one restoration despite later departure messages').toEqual([{ id: 'tasks', context: { workspaceId } }])
      expect(dockRemovals, 'a stale own-child presence must not remove the restored dock').toBe(0)
      expect(afterLeave.switchActions, 'a closed own child is not an already-open target').toEqual([])
    } finally {
      unsubscribe()
    }
  })

  it('negative no-close control rejects a false restoration oracle while the owned child remains live', async () => {
    const session = await openSession()
    await ClockEdge.tickClosePoll(session.source)
    const actual = sourceSnapshot(session)
    assertSourceSurvived(actual)
    expect(actual.activePanel).toBeNull()
    expect(actual.childClosed).toBe(false)
    expect(actual.childCloseCalls).toBe(0)
    expect([...session.sourceApp.getPanelTabHandleRegistry().entries()]).toEqual([[`tasks:${A}`, session.child.handle]])
    expect(actual.presence).toEqual([opaqueIdentity(A)])
    expect(() => assertDockedTasks(actual, A), 'instrument must REJECT the deliberately wrong no-close restoration claim').toThrowError(/FR-018: owned-close source must contain the same Tasks dock/)
    printOrderReceipt('negative no-close oracle rejected', actual)
  })

  it('negative foreign-owner Back is rejected by the real owner handler and by the false-restoration oracle', async () => {
    const session = await openSession()
    const foreign = childWindowBoundary()
    const url = new URL(session.child.destination()!, sourceWindow.location.href)
    const search = new URLSearchParams(url.hash.split('?')[1])
    search.set('popout', 'close-presence-foreign-owner-token')
    const foreignTab = await mountChild(makeRealm('foreign-owner-child'), foreign, `/#/panel/tasks?${search}`)
    await flushTabs(session.source, session.other, session.mounted.realm, foreignTab.realm)
    activate(foreignTab.realm)
    fireEvent.click(within(foreignTab.realm.container).getByRole('button', { name: 'Back to chat' }))
    await act(async () => {})
    expect(foreign.close).toHaveBeenCalledExactlyOnceWith()
    expect(foreign.handle.closed).toBe(true)
    const foreignClose = ChannelEdge.pending.filter((packet) => packet.sender.realm === foreignTab.realm &&
      packet.receiver.realm === session.source && packet.sender.name === LIFECYCLE_CHANNEL &&
      messageRecord(packet.data).type === 'popout-closed')
    expect(foreignClose).toHaveLength(1)
    expect(foreignClose[0]!.data).toEqual({ type: 'popout-closed', panelId: 'tasks', popoutId: 'close-presence-foreign-owner-token', context: { workspaceId: A } })
    await unmountRealm(foreignTab.realm)
    await flushTabs(session.source, session.other, session.mounted.realm, foreignTab.realm)
    await ClockEdge.tickClosePoll(session.source)
    const actual = sourceSnapshot(session)
    assertSourceSurvived(actual)
    expect(actual.activePanel, 'FR-018: foreign popout ID has no authority over this source').toBeNull()
    expect(actual.childClosed).toBe(false)
    expect(actual.childCloseCalls).toBe(0)
    expect([...session.sourceApp.getPanelTabHandleRegistry().entries()]).toEqual([[`tasks:${A}`, session.child.handle]])
    expect(actual.presence).toEqual([opaqueIdentity(A)])
    expect(() => assertDockedTasks(actual, A), 'instrument must REJECT the deliberately wrong foreign-owner restoration claim').toThrowError(/FR-018: owned-close source must contain the same Tasks dock/)
    printOrderReceipt('negative foreign-owner oracle rejected', actual)
  })

  it('negative truly live matching manual tab retains duplicate-dock prevention and a working Switch after owned child leave', async () => {
    const session = await openSession()
    const manual = childWindowBoundary()
    const manualTab = await mountChild(makeRealm('live-manual-child'), manual, `/#/panel/tasks?workspace=${A}`)
    await flushTabs(session.source, session.other, session.mounted.realm, manualTab.realm)
    const ownPresence = latestPresence(session.mounted.realm, session.source)
    const manualPresence = latestPresence(manualTab.realm, session.source)
    expect(messageRecord(ownPresence.data).tabId).not.toBe(messageRecord(manualPresence.data).tabId)
    expect(session.sourceApp.getPanelTabPresenceKeys()).toEqual([opaqueIdentity(A), opaqueIdentity(A)])
    session.child.handle.close()
    const ownLeave = heldOwnedLeave(session, ownPresence)
    await ClockEdge.tickClosePoll(session.source)
    expect(ChannelEdge.pending).toContain(ownLeave)
    await deliverOne(ownLeave)
    await unmountRealm(session.mounted.realm)
    await flushTabs(session.source, session.other, session.mounted.realm, manualTab.realm)
    const actual = sourceSnapshot(session)
    assertSourceSurvived(actual)
    expect(actual.activePanel, 'SP-18/SP-30: the separate LIVE matching panel still prevents duplicate docking').toBeNull()
    expect(actual.presence).toEqual([opaqueIdentity(A)])
    expect(actual.handles).toEqual([])
    expect(actual.switchActions).toEqual(['Switch'])
    expect(manual.handle.closed, 'negative control must be truly live, not stale own presence').toBe(false)
    expect(manual.close).toHaveBeenCalledTimes(0)
    const action = session.sourceApp.useUiStore.getState().toasts.at(-1)?.action
    if (!action) throw new Error('SP-18/SP-30: missing real Switch action for the live manual tab')
    activate(session.source)
    await act(async () => action.onClick())
    await flushTabs(session.source, session.other, manualTab.realm)
    expect(manual.focus).toHaveBeenCalledExactlyOnceWith()
    expect(session.child.focus).toHaveBeenCalledTimes(0)
    expect(sourceSnapshot(session).activePanel).toBeNull()
    expect(session.open).toHaveBeenCalledTimes(1)
    printOrderReceipt('negative live-other Switch preserved', actual)
  })

  it('negative different-panel source is preserved with owned-close before the delayed leave', async () => {
    const session = await openSession()
    const presence = latestPresence(session.mounted.realm, session.source)
    activate(session.source)
    fireEvent.click(within(session.source.container).getByRole('button', { name: 'Open Team' }))
    await act(async () => {})
    expect(session.sourceApp.useUiStore.getState().activePanel).toEqual({ id: 'team', context: { workspaceId: A } })
    session.child.handle.close()
    const leave = heldOwnedLeave(session, presence)
    await ClockEdge.tickClosePoll(session.source)
    expect(ChannelEdge.pending).toContain(leave)
    expect(session.sourceApp.useUiStore.getState().activePanel).toEqual({ id: 'team', context: { workspaceId: A } })
    await deliverOne(leave)
    await unmountRealm(session.mounted.realm)
    await flushTabs(session.source, session.other, session.mounted.realm)
    const actual = sourceSnapshot(session)
    assertSourceSurvived(actual)
    expect(actual.activePanel, 'FR-018: owned child cannot clobber a DIFFERENT source panel').toEqual({ id: 'team', context: { workspaceId: A } })
    expect(actual.childClosed).toBe(true)
    expect(actual.handles).toEqual([])
    expect(actual.presence).toEqual([])
    expect(actual.switchActions).toEqual([])
    printOrderReceipt('negative different-panel preserved', actual)
  })
})
