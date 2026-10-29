// Test-only process-edge harness for omnipus-system-agent-spec US-17.
// REAL: QueryClient, router, stores, HTTP client, WsConnection/parser/dispatcher.
// DOUBLES: fetch and the browser WebSocket. No production helper is spied on.
import type { ComponentType, ReactNode } from 'react'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory, createRootRoute, createRoute, createRouter,
  Outlet, RouterProvider,
} from '@tanstack/react-router'
import { vi } from 'vitest'
import type { Agent, AgentActivityRun, Room, SandboxStatus, SessionPage, Workspace } from '@/lib/api/generated/openapi-types'
import type {
  ClientFrame, RoomDeliveryState, RoomEntry, RoomSnapshotFrame, ServerFrame,
} from '@/lib/api/generated/asyncapi-types'
import { makeAgent } from '@/test/factories'
import { queryClient } from '@/lib/queryClient'
import { parseFrameSafe, WsConnection } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'

export const WS = 'sales'
export const ROOM_ID = 'launch'
export const MAIN_ID = 'main-mia-sales'
export const NOW = '2026-10-14T10:00:00Z'

export const agents: Agent[] = [
  makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'idle' }),
  makeAgent({ id: 'jim', name: 'Jim', type: 'core', status: 'idle' }),
  makeAgent({ id: 'researcher2', name: 'Researcher2', type: 'Main', status: 'idle' }),
  makeAgent({ id: 'ray', name: 'Ray', type: 'Main', status: 'idle' }),
  makeAgent({ id: 'gp', name: 'GP Worker', type: 'Subagent' }),
  makeAgent({ id: 'external', name: 'External Worker', type: 'subagent_3p' }),
  makeAgent({ id: 'judge', name: 'Judge', type: 'system' }),
  makeAgent({ id: 'omnipus', name: 'Omnipus', type: 'core' }),
]

export function workspace(overrides: Partial<Workspace> = {}): Workspace {
  return {
    id: WS, name: 'Sales', status: 'active', pinned: false, pin_order: 0,
    task_count: 0, created_at: NOW, updated_at: NOW,
    revision: '0'.repeat(64), is_default: false,
    core_team: ['mia', 'jim', 'gp', 'external', 'judge'],
    member_configs: {
      mia: { main_session_id: MAIN_ID },
      jim: { main_session_id: 'main-jim-sales' },
      researcher2: { main_session_id: 'main-researcher2-sales' },
    },
    ...overrides,
  }
}

export function room(overrides: Partial<Room> = {}): Room {
  return {
    id: ROOM_ID, workspace_id: WS, kind: 'squad', name: 'Launch',
    members: ['mia', 'jim', 'researcher2'], muted: [], paused: false,
    generation: 0, hidden: false, allocated_cost_micro_usd: 10_000,
    allocated_cost_unpriced: false, needs_attention: false,
    ...overrides,
  }
}

export function entry(overrides: Partial<RoomEntry> = {}): RoomEntry {
  return {
    id: 'room-message-1', room_id: ROOM_ID, seq: 1,
    client_message_id: 'client-room-message-1', author: { kind: 'user' },
    text: 'status?', addressees: ['mia', 'jim', 'researcher2'],
    after_pause: false, deliveries: [], timestamp: NOW,
    ...overrides,
  }
}

export function delivery(
  state: RoomDeliveryState['state'], overrides: Partial<RoomDeliveryState> = {},
): RoomDeliveryState {
  return { agent_id: 'jim', request_id: 'delivery-jim-1', state, ...overrides }
}

export function snapshot(overrides: Partial<RoomSnapshotFrame> = {}): RoomSnapshotFrame {
  return {
    type: 'room_snapshot', room_id: ROOM_ID, seq: 1,
    entries: [], members: ['mia', 'jim', 'researcher2'], muted: [],
    member_states: [], paused: false, generation: 0,
    allocated_cost_micro_usd: 10_000, allocated_cost_unpriced: false,
    complete: true, ...overrides,
  }
}

// Exact planned import paths, not fallback implementations. Missing files fail
// inside the named case, not in collection. Errors from existing modules propagate.
const plannedModules = import.meta.glob<Record<string, unknown>>([
  '/src/lib/api/rooms.ts', '/src/store/rooms.ts',
  '/src/components/rooms/RoomScreen.tsx', '/src/components/rooms/SquadDialog.tsx',
  '/src/components/rooms/ParticipantsPanel.tsx',
  '/src/components/chat/AddParticipant.tsx', '/src/components/chat/RoomTrafficEntry.tsx',
  '/src/components/chat/ChatHeaderSlot.tsx',
  '/src/components/layout/sidebar/WorkspaceAgentList.tsx',
])

export async function requiredExport<T>(path: string, name: string, spec: string): Promise<T> {
  if (!plannedModules['/src/components/layout/sidebar/WorkspaceAgentList.tsx']) {
    throw new Error('Test instrument error: the glob cannot see the known existing WorkspaceAgentList module')
  }
  const load = plannedModules[path]
  const absolutePath = `${process.cwd()}${path}`
  if (!load) {
    throw new Error(`BLOCKED: ${absolutePath}::${name} not implemented — required by ${spec}`)
  }
  const module = await load()
  if (!(name in module)) {
    throw new Error(`BLOCKED: ${absolutePath}::${name} not exported — required by ${spec}`)
  }
  return module[name] as T
}

// not-wire-format: public React props confirmed by team-lead on 2026-10-14.
export type RoomScreenProps = { workspaceId: string; roomId: string }

export function loadRoomScreen(): Promise<ComponentType<RoomScreenProps>> {
  return requiredExport('/src/components/rooms/RoomScreen.tsx', 'RoomScreen', 'FR-175; US-17; K45')
}

// not-wire-format: observed test transport calls, not an API payload definition.
export interface ObservedRequest {
  method: string
  path: string
  query: string
  body: unknown
  credentials: RequestCredentials | undefined
  headers: Headers
}

type Handler = (request: ObservedRequest) => Response | Promise<Response>

export function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

export class Network {
  readonly requests: ObservedRequest[] = []
  readonly unexpected: string[] = []
  readonly handlers = new Map<string, Handler>()
  rooms: Room[] = [room()]
  currentRoom: Room = room()
  roster: Agent[] = [...agents]
  workspaces: Workspace[] = [workspace()]

  constructor() {
    this.respond('GET', '/api/v1/agents', () => json(this.roster))
    this.respond('GET', '/api/v1/workspaces', () => json(this.workspaces))
    this.respond('GET', `/api/v1/workspaces/${WS}`, () => json(this.workspaces[0]))
    const emptySessions: SessionPage = { sessions: [] }
    this.respond('GET', '/api/v1/sessions', () => json(emptySessions))
    // Existing ChatScreen siblings query these even while the participant
    // dialog is closed. Keep exact routes: any other request still fails.
    const sandboxStatus: SandboxStatus = {
      backend: 'fallback', available: false, kernel_level: false,
      policy_applied: false, seccomp_enabled: false, bind_ports_count: 0,
      kernel_sandbox_active: false, auto_approve_effective: false,
      god_mode_active: false,
    }
    const noActivityRuns: AgentActivityRun[] = []
    this.respond('GET', '/api/v1/security/sandbox-status', () => json(sandboxStatus))
    this.respond('GET', '/api/v1/agents/mia/activity-runs', () => json(noActivityRuns))
    this.respond('GET', '/api/v1/agents/omnipus/activity-runs', () => json(noActivityRuns))
    this.respond('GET', `/api/v1/workspaces/${WS}/rooms`, () => json(this.rooms))
    this.respond('GET', `/api/v1/rooms/${ROOM_ID}`, () => json(this.currentRoom))
    this.respond('POST', `/api/v1/rooms/${ROOM_ID}/messages`, () => new Response(null, { status: 202 }))
    this.respond('GET', '/api/v1/auth/validate', () => json({ valid: true }))
    vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => this.fetch(input, init))
  }

  respond(method: string, path: string, handler: Handler): void {
    this.handlers.set(`${method} ${path}`, handler)
  }

  writes(): ObservedRequest[] {
    return this.requests.filter((request) => !['GET', 'HEAD'].includes(request.method))
  }

  async fetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
    const url = new URL(input instanceof Request ? input.url : String(input), 'http://localhost')
    const request: ObservedRequest = {
      method: (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase(),
      path: url.pathname, query: url.search,
      body: typeof init?.body === 'string' ? JSON.parse(init.body) as unknown : null,
      credentials: init?.credentials, headers: new Headers(init?.headers),
    }
    this.requests.push(request)
    const handler = this.handlers.get(`${request.method} ${request.path}`)
    if (!handler) {
      const key = `${request.method} ${request.path}${request.query}`
      this.unexpected.push(key)
      throw new Error(`Unexpected test network request: ${key}`)
    }
    return handler(request)
  }
}

// The fake lives below the REAL WsConnection. It never parses, filters, routes,
// deduplicates or fabricates a response to a sent frame.
export class TestSocket {
  static readonly OPEN = 1
  static readonly CLOSED = 3
  static instances: TestSocket[] = []
  readonly sent: ClientFrame[] = []
  readyState = 0
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null

  constructor(readonly url: string) { TestSocket.instances.push(this) }
  open(): void { this.readyState = TestSocket.OPEN; this.onopen?.(new Event('open')) }
  send(data: string): void { this.sent.push(JSON.parse(data) as ClientFrame) }
  close(code = 1000, reason = ''): void {
    this.readyState = TestSocket.CLOSED
    this.onclose?.(new CloseEvent('close', { code, reason }))
  }
  receive(frame: ServerFrame): void {
    // Instrument control: a mistyped fixture cannot masquerade as a reducer bug.
    if (parseFrameSafe(frame) === null) throw new Error(`Invalid generated room fixture: ${JSON.stringify(frame)}`)
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(frame) }))
  }
}

// not-wire-format: minimal planned public store API, no invented state layout.
export interface RoomStorePublic {
  getState(): { attachRoom(roomId: string): void }
  getInitialState(): object
  setState(state: object, replace: true): void
}

export async function loadRoomStore(): Promise<RoomStorePublic> {
  return requiredExport('/src/store/rooms.ts', 'useRoomStore', 'K45; FR-201/217')
}

export function resetRealStores(): void {
  // jsdom is not a layout browser. These browser-edge stand-ins allow real
  // controls to mount; focus/geometry are explicitly browser/UAT rows.
  vi.stubGlobal('matchMedia', (media: string) => ({
    matches: false, media, onchange: null,
    addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {},
    dispatchEvent: () => true,
  }))
  vi.stubGlobal('ResizeObserver', class {
    observe() {} unobserve() {} disconnect() {}
  })
  vi.stubGlobal('scrollTo', () => {})
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {}
  localStorage.clear()
  act(() => {
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useUiStore.setState(useUiStore.getInitialState(), true)
    useWorkspacesStore.setState({ activeWorkspaceId: WS })
    useSessionStore.setState({ activeSessionId: MAIN_ID, activeAgentId: 'mia', activeAgentType: 'core' })
  })
  queryClient.clear()
  queryClient.setDefaultOptions({ queries: { retry: false }, mutations: { retry: false } })
  document.cookie = 'csrf=room-red-test; Path=/'
}

export function connectSocket() {
  TestSocket.instances = []
  const receivedFrames: ServerFrame[] = []
  vi.stubGlobal('WebSocket', TestSocket)
  const connection = new WsConnection({
    onFrames: (frames) => act(() => {
      receivedFrames.push(...frames)
      frames.forEach((frame) => useChatStore.getState().handleFrame(frame))
    }),
    onConnected: () => useConnectionStore.getState().setConnected(true),
    onDisconnected: () => useConnectionStore.getState().setConnected(false),
    onError: (message) => useConnectionStore.getState().setConnectionError(message),
    onReconnectStateChange: (phase, attempt) => useConnectionStore.getState().setReconnectState(phase, attempt),
  })
  act(() => {
    useConnectionStore.getState().setConnection(connection)
    connection.connect()
  })
  const socket = TestSocket.instances[0]
  if (!socket) throw new Error('Test WebSocket was not created')
  act(() => socket.open())
  const emit = async (frame: ServerFrame) => {
    const before = receivedFrames.length
    act(() => socket.receive(frame))
    await waitFor(() => {
      if (receivedFrames.length !== before + 1) throw new Error(`Socket parser has not dispatched ${frame.type}`)
    })
  }
  return { connection, socket, emit }
}

export async function mountAt(node: ReactNode, path = `/workspaces/${WS}/rooms/${ROOM_ID}`) {
  const root = createRootRoute({ component: () => <QueryClientProvider client={queryClient}><Outlet /></QueryClientProvider> })
  const app = createRoute({ getParentRoute: () => root, id: '_app', component: Outlet })
  const wsRoute = createRoute({ getParentRoute: () => app, path: 'workspaces/$workspaceId', component: Outlet })
  const roomRoute = createRoute({ getParentRoute: () => wsRoute, path: 'rooms/$roomId', component: () => node })
  const chatRoute = createRoute({ getParentRoute: () => wsRoute, path: 'chat', component: () => node })
  const sessionRoute = createRoute({ getParentRoute: () => app, path: 'sessions/$sessionId', component: () => <div>Opened main chat</div> })
  const indexRoute = createRoute({ getParentRoute: () => app, path: '/', component: () => node })
  const router = createRouter({
    routeTree: root.addChildren([app.addChildren([wsRoute.addChildren([roomRoute, chatRoute]), sessionRoute, indexRoute])]),
    history: createMemoryHistory({ initialEntries: [path] }), defaultPendingMinMs: 0,
  })
  await act(async () => { await router.load() })
  const result = render(<RouterProvider router={router} />)
  return { ...result, router }
}

export async function openRoom(net: Network, state: RoomSnapshotFrame = snapshot()) {
  const RoomScreen = await loadRoomScreen()
  const store = await loadRoomStore()
  act(() => store.setState(store.getInitialState(), true))
  const { connection, socket, emit } = connectSocket()
  const mounted = await mountAt(<RoomScreen workspaceId={net.currentRoom.workspace_id} roomId={net.currentRoom.id} />)
  await waitFor(() => {
    if (!socket.sent.some((frame) => frame.type === 'attach_room' && frame.room_id === net.currentRoom.id)) {
      throw new Error('K45: RoomScreen must attach its room on the existing hub connection')
    }
  })
  await emit(state)
  // All complete-room tests start from an observable successful mount, not empty negatives.
  if (state.complete) await screen.findByTestId('room-header')
  return { ...mounted, connection, socket, emit, store }
}

export function stopHarness(connection?: WsConnection): void {
  cleanup()
  connection?.disconnect()
  queryClient.clear()
  document.cookie = 'csrf=; Max-Age=0; Path=/'
  vi.unstubAllGlobals()
}
