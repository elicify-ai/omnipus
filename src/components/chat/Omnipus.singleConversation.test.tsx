// Chief ruling condition, 2026-10-14: spec US-1 AC4/6, FR-004/005/100/104;
// WC-1 (2026-10-09), corrected team-lead Q1=A: /new is refused, /clear
// clears context in the SAME conversation. No second Omnipus chat affordance.
// REAL entry/screen/composer, router, AssistantUI runtime, stores, HTTP client,
// generated validation and frame reducer. Only raw HTTP/socket/browser edges.
import type { ComponentType, ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryHistory, createRootRoute, createRoute, createRouter, Outlet, RouterProvider } from '@tanstack/react-router'
import { QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider } from '@assistant-ui/react'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { queryClient } from '@/lib/queryClient'
import { workspacesQueryKeys } from '@/lib/api'
import type { Agent, Session, Workspace } from '@/lib/api'
import type { AppState, Message, SandboxStatus, Session as WireSession, SessionPage, SlashCommand } from '@/lib/api/generated/openapi-types'
import type { ClientFrame, MessageFrame, ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { WsConnection } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { DEFAULT, MIA, OMNIPUS, OMNIPUS_MAIN, SALES, SALES_MAIN, session } from '../../../tests/omnipus-l1-ui-red/fixtures'
import { expectValidFrame, expectValidPage, page, pos, replay, snapshot } from '../../../tests/chat-history-paging/fixtures'

// Module namespaces / props are test-only, not wire formats. This QA branch
// predates these modules. An absent module fails the named case loudly, never
// skips it or fails collection. Both paths/exports were read on frozen afc4.
type EntryProps = {
  workspaces: Workspace[]; workspacesLoading: boolean; agents: Agent[];
  sessions: Session[]; onOpened: () => void;
}
const entryModules = import.meta.glob<{ OmnipusEntry: ComponentType<EntryProps> }>('../layout/sidebar/OmnipusEntry.tsx')
const screenModules = import.meta.glob<{ OmnipusChatScreen: ComponentType }>('./OmnipusChatScreen.tsx')
const AGENTS: Agent[] = [{ ...OMNIPUS, needs_model: false, model: 'fixture-model' }, MIA]
const SESSIONS: Session[] = [
  session(OMNIPUS_MAIN, 'omnipus', 'default', { type: 'main', needs_attention: false }),
  session(SALES_MAIN, 'mia', 'sales', { type: 'main', needs_attention: false }),
]
const PRIOR_SALES = { id: SALES_MAIN, type: 'main' as const, title: 'Mia main', agentId: 'mia' }
const BEFORE: Message = {
  id: 'before-clear', timestamp: '2026-10-14T00:00:00Z', agent_id: 'omnipus',
  type: 'message', role: 'user', view_membership: 'both', content: 'Existing Omnipus conversation', history_position: pos(1),
}
const MARKER: Message = {
  id: 'clear-marker', timestamp: '2026-10-14T00:01:00Z', agent_id: 'omnipus',
  type: 'system', role: 'system', view_membership: 'chat', content: 'Conversation context cleared', history_position: pos(2),
}
const COMMANDS: SlashCommand[] = [{ name: 'clear', label: '/clear', description: 'Clear this chat context', delivery: 'agent', available_while_streaming: true }]
const SANDBOX: SandboxStatus = { backend: 'fallback', available: true, kernel_level: false,
  policy_applied: false, seccomp_enabled: false, bind_ports_count: 0, auto_approve_effective: false }
const APP_STATE: AppState = { onboarding_complete: true, dev_mode_bypass: false,
  identity: { mode: 'local', edition: 'core', signed_in: true, blocked_reason: 'none' } }
const NEW_CHAT = /(?:\b(?:new|extra|another|second|start|create)\b.*\b(?:chat|conversation)\b|\b(?:chat|conversation)\b.*\b(?:new|extra|another|second)\b)/i
const scrollDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollIntoView')
let socket: WsConnection
let frames: ClientFrame[]
let requests: { method: string; path: string; body: unknown }[]
let unexpectedHttp: string[]
let sessionIds: (string | null)[]
let unsubscribe: () => void
let history: Message[]

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
}
function isSessionCreate(request: { method: string; path: string }) {
  return request.method === 'POST' && request.path.split('?')[0] === '/api/v1/sessions'
}
function isMintingMessage(frame: ClientFrame) {
  return frame.type === 'message' && !frame.session_id
}
function wireSession(s: Session): WireSession {
  return { id: s.id, agent_id: s.agent_id, workspace_id: s.workspace_id, title: s.title,
    type: s.type, status: 'active', created_at: s.created_at, updated_at: s.updated_at,
    channel: 'webchat', partitions: [], needs_attention: false,
    stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 1 } }
}
function RuntimeHost({ children }: { children: ReactNode }) {
  const runtime = useOmnipusRuntime()
  return <AssistantRuntimeProvider runtime={runtime}>{children}</AssistantRuntimeProvider>
}

beforeEach(() => {
  queryClient.clear()
  localStorage.clear()
  frames = []
  requests = []
  unexpectedHttp = []
  history = [BEFORE]
  vi.stubGlobal('matchMedia', (media: string) => ({ matches: false, media, onchange: null,
    addListener: () => {}, removeListener: () => {}, addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false }))
  // Exercise the screen's supported plain-list fallback: jsdom has no layout.
  vi.stubGlobal('ResizeObserver', undefined)
  // jsdom has no scrolling API; this geometry-only adapter leaves the actual
  // command palette, selection, composer and send behavior untouched.
  Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: vi.fn() })
  vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockImplementation(async (input, init) => {
    const url = new URL(String(input), 'https://test.invalid')
    const method = (init?.method ?? 'GET').toUpperCase()
    const path = url.pathname + url.search
    requests.push({ method, path, body: typeof init?.body === 'string' ? JSON.parse(init.body) : null })
    if (method === 'GET') {
      if (url.pathname === '/api/v1/workspaces') return jsonResponse([DEFAULT, SALES])
      if (url.pathname === '/api/v1/agents') return jsonResponse(AGENTS)
      if (url.pathname === '/api/v1/agents/omnipus/activity-runs') return jsonResponse([])
      if (url.pathname === '/api/v1/sessions') return jsonResponse({ sessions: SESSIONS.map(wireSession) } satisfies SessionPage)
      if (url.pathname === `/api/v1/sessions/${OMNIPUS_MAIN}/messages`) {
        const atClear = history[0]?.id === MARKER.id
        const fixture = page(history, { sessionId: OMNIPUS_MAIN, earlier: atClear, info: atClear ? {
          stopped_at_clear: true, stop_reason: 'clear', latest_clear: { entry_id: MARKER.id, history_position: pos(2) },
        } : {} })
        expectValidPage(fixture, atClear ? [pos(2)] : [pos(1)], 1)
        return jsonResponse(fixture)
      }
      if (url.pathname === '/api/v1/commands') return jsonResponse(COMMANDS)
      if (url.pathname === '/api/v1/skills' || url.pathname === '/api/v1/providers') return jsonResponse([])
      if (url.pathname === '/api/v1/security/sandbox-status') return jsonResponse(SANDBOX)
      if (url.pathname === '/api/v1/state') return jsonResponse(APP_STATE)
      if (url.pathname === '/api/v1/gateway/god-mode') return jsonResponse({ enabled: false, persisted: false, supported: true, available: true })
    }
    unexpectedHttp.push(`${method} ${path}`)
    throw new Error(`Unspecified HTTP process edge: ${method} ${path}`)
  }))
  socket = new WsConnection({ onConnected: () => {}, onDisconnected: () => {}, onError: () => {} })
  vi.spyOn(socket, 'send').mockImplementation((frame) => { frames.push(frame); return true })
  useConnectionStore.setState({ ...useConnectionStore.getInitialState(), connection: socket, isConnected: true })
  useUiStore.setState(useUiStore.getInitialState(), true)
  useWorkspacesStore.setState({ activeWorkspaceId: 'sales' })
  useSessionStore.setState({ ...useSessionStore.getInitialState(), activeSessionId: OMNIPUS_MAIN,
    activeAgentId: 'omnipus', activeAgentType: 'core', sessionByWorkspace: { sales: PRIOR_SALES } }, true)
  useChatStore.setState(useChatStore.getInitialState(), true)
  sessionIds = [OMNIPUS_MAIN]
  unsubscribe = useSessionStore.subscribe((state) => { sessionIds.push(state.activeSessionId) })
  queryClient.setQueryData(workspacesQueryKeys.list({ status: 'active' }), [DEFAULT, SALES])
  queryClient.setQueryData(['agents'], AGENTS)
  queryClient.setQueryData(['sessions'], SESSIONS)
})
afterEach(() => {
  cleanup()
  unsubscribe()
  queryClient.clear()
  socket.disconnect()
  useConnectionStore.setState({ connection: null, isConnected: false })
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  if (scrollDescriptor) Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', scrollDescriptor)
  else Reflect.deleteProperty(HTMLElement.prototype, 'scrollIntoView')
  expect(unexpectedHttp, 'every request is captured; unspecified HTTP never contacts a live service').toEqual([])
})

async function mountEntry() {
  const load = entryModules['../layout/sidebar/OmnipusEntry.tsx']
  if (!load) expect.fail('BLOCKED: OmnipusEntry not implemented — spec FR-004/005/100 and chief one-conversation ruling')
  const { OmnipusEntry } = await load()
  const onOpened = vi.fn()
  const root = createRootRoute({ component: Outlet })
  const home = createRoute({ getParentRoute: () => root, path: '/', component: () => <OmnipusEntry
    workspaces={[DEFAULT, SALES]} workspacesLoading={false} agents={AGENTS} sessions={SESSIONS} onOpened={onOpened} /> })
  const destination = createRoute({ getParentRoute: () => root, path: '/omnipus', component: () => <p>Existing Omnipus main opened</p> })
  const router = createRouter({ routeTree: root.addChildren([home, destination]), history: createMemoryHistory({ initialEntries: ['/'] }) })
  render(<QueryClientProvider client={queryClient}><RouterProvider router={router} /></QueryClientProvider>)
  return { router, onOpened, group: await screen.findByRole('group', { name: 'Omnipus' }) }
}
function completeConnectedHistory() {
  const selected = page([BEFORE], { sessionId: OMNIPUS_MAIN })
  expectValidPage(selected, [pos(1)], 1)
  const incoming: ServerFrame[] = [
    { ...snapshot(selected), seq: 1, boot_id: 'omnipus-single-boot' },
    { type: 'session_state', session_id: OMNIPUS_MAIN, user_id: 'single-conversation-reader', pending_approvals: [],
      emitted_at: BEFORE.timestamp, history_state: { status: 'available' } },
    replay(BEFORE, OMNIPUS_MAIN),
    { type: 'done', session_id: OMNIPUS_MAIN },
    { type: 'catch_up_complete', session_id: OMNIPUS_MAIN, seq: 1, boot_id: 'omnipus-single-boot', mode: 'snapshot' },
  ]
  incoming.forEach(expectValidFrame)
  act(() => incoming.forEach(frame => useChatStore.getState().handleFrame(frame)))
  expect(useChatStore.getState().sessionsById[OMNIPUS_MAIN].history.historyLoaded,
    'D6/D7: the connected first page really committed through snapshot ownership').toBe(true)
}
function messagesReads() {
  return requests.filter(request => request.method === 'GET' && request.path.split('?')[0] === `/api/v1/sessions/${OMNIPUS_MAIN}/messages`)
}
async function mountScreen() {
  const load = screenModules['./OmnipusChatScreen.tsx']
  if (!load) expect.fail('BLOCKED: OmnipusChatScreen not implemented — spec FR-005/100 and chief /omnipus ruling')
  const { OmnipusChatScreen } = await load()
  const root = createRootRoute({ component: () => <RuntimeHost><Outlet /></RuntimeHost> })
  const omnipus = createRoute({ getParentRoute: () => root, path: '/omnipus', component: () => <OmnipusChatScreen /> })
  const router = createRouter({ routeTree: root.addChildren([omnipus]), history: createMemoryHistory({ initialEntries: ['/omnipus'] }) })
  render(<QueryClientProvider client={queryClient}><RouterProvider router={router} /></QueryClientProvider>)
  const composer = await screen.findByTestId('chat-input')
  expect(composer).toBeEnabled()
  completeConnectedHistory()
  expect(await screen.findByText(BEFORE.content!)).toBeVisible()
  expect(messagesReads(), 'D6: connected opening never starts a parallel initial REST history read').toStrictEqual([])
  await waitFor(() => expect(queryClient.isFetching()).toBe(0))
  expect(router.state.location.pathname).toBe('/omnipus')
  return { router, composer }
}
function expectNoNewChatControl() {
  for (const role of ['button', 'link', 'menuitem', 'option'] as const) {
    expect(screen.queryByRole(role, { name: NEW_CHAT }), `no ${role} offers another conversation`).not.toBeInTheDocument()
  }
}
function expectSameSessionNoCreation(allowedHistory: (string | null)[] = [OMNIPUS_MAIN]) {
  expect(useSessionStore.getState().activeSessionId).toBe(OMNIPUS_MAIN)
  expect(useSessionStore.getState().activeAgentId).toBe('omnipus')
  expect(sessionIds.filter((id) => !allowedHistory.includes(id)), 'no transient replacement session').toEqual([])
  expect(sessionIds, 'even a transient startNewSession/null or optimistic extra is forbidden').not.toContain(null)
  expect(sessionIds).not.toContain('__pending')
  expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('sales')
  expect(useSessionStore.getState().sessionByWorkspace.sales).toEqual(PRIOR_SALES)
  expect(requests.filter(isSessionCreate)).toEqual([])
  expect(frames.filter(isMintingMessage)).toEqual([])
}

// Named cases are the proposed fresh CHECK mutation targets. No production
// mutation is authored here; healthy results alone are not failability proof.
describe('Omnipus one-conversation guarantee at its reachable UI', () => {
  it('OC-A fixed entry has no extra-chat button, menu, link or context-menu path', async () => {
    const { group } = await mountEntry()
    const button = within(group).getByRole('button', { name: /^Omnipus$/i })
    const user = userEvent.setup()
    expect(button).toBeVisible()
    expect(within(group).getAllByRole('button', { name: /^Omnipus$/i })).toEqual([button])
    expectNoNewChatControl()
    const disclosures = within(group).queryAllByRole('button').filter((control) =>
      control !== button && (control.hasAttribute('aria-haspopup') || control.hasAttribute('aria-expanded')))
    for (const disclosure of disclosures) {
      await user.click(disclosure)
      expectNoNewChatControl()
      await user.keyboard('{Escape}')
    }
    fireEvent.contextMenu(button)
    button.focus()
    await user.keyboard('{Shift>}{F10}{/Shift}{ContextMenu}{Control>}n{/Control}{Meta>}n{/Meta}')
    expectNoNewChatControl()
    // A harmless future menu is not forbidden by the spec. Its actual extra-
    // conversation actions are; inspect menuitems rather than banning menus.
    for (const menu of screen.queryAllByRole('menu')) {
      expect(within(menu).queryByRole('menuitem', { name: NEW_CHAT })).not.toBeInTheDocument()
    }
    expectSameSessionNoCreation()
    expect(frames).toEqual([])
  })
  it.each(['pointer', 'Enter', 'Space'] as const)('OC-AK %s opens only the existing main, never an extra conversation', async (input) => {
    useSessionStore.setState({ activeSessionId: SALES_MAIN, activeAgentId: 'mia' })
    sessionIds = [SALES_MAIN]
    const { group, router, onOpened } = await mountEntry()
    const button = within(group).getByRole('button', { name: /^Omnipus$/i })
    const user = userEvent.setup()
    if (input === 'pointer') await user.click(button)
    else { button.focus(); await user.keyboard(input === 'Space' ? ' ' : '{Enter}') }
    expect(await screen.findByText('Existing Omnipus main opened')).toBeVisible()
    expect(router.state.location.pathname).toBe('/omnipus')
    expect(onOpened.mock.calls).toEqual([[]])
    expect(frames).toEqual([{ type: 'attach_session', session_id: OMNIPUS_MAIN }])
    expectSameSessionNoCreation([SALES_MAIN, OMNIPUS_MAIN])
  })
  it('OC-B /omnipus mounts the real screen/composer and no new-chat action, including opened menus', async () => {
    await mountScreen()
    expect(screen.getByTestId('omnipus-chat-screen')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Omnipus settings' })).toBeVisible()
    expectNoNewChatControl()
    // A future header-menu button must be exercised, not left collapsed like
    // the old Default-sidebar false green. Do not follow unrelated settings.
    const triggers = screen.queryAllByRole('button').filter((button) => button.getAttribute('aria-haspopup') === 'menu')
    const user = userEvent.setup()
    for (const trigger of triggers) {
      await user.click(trigger)
      expectNoNewChatControl()
      await user.keyboard('{Escape}')
    }
    expectSameSessionNoCreation()
    expect(frames).toEqual([])
  })
  it.each(['Enter', 'send button'] as const)('OC-C-new /new by %s is visibly refused without any send or new session', async (input) => {
    const { composer } = await mountScreen()
    const user = userEvent.setup()
    const requestCount = requests.length
    await user.type(composer, '/new')
    if (input === 'Enter') await user.keyboard('{Enter}')
    else await user.click(screen.getByTestId('chat-send'))
    expect(await screen.findByText(/\/new no longer exists/i)).toBeVisible()
    expect(composer).toHaveValue('')
    expect(screen.getByText(BEFORE.content!)).toBeVisible()
    // The independently polled Activity read may run while typing. It is not
    // a command dispatch; every other new request, including any write, fails.
    expect(requests.slice(requestCount).filter((request) =>
      !(request.method === 'GET' && request.path === '/api/v1/agents/omnipus/activity-runs'))).toEqual([])
    expect(frames).toEqual([])
    expectSameSessionNoCreation()
  })
  it('OC-C-clear real composer sends /clear on the existing main and renders its confirmed context boundary in place', async () => {
    const { composer, router } = await mountScreen()
    const user = userEvent.setup()
    await user.type(composer, '/clear')
    await user.click(screen.getByTestId('chat-send'))
    await waitFor(() => expect(frames.filter((frame) => frame.type === 'message')).toHaveLength(1))
    const sent = frames.find((frame): frame is MessageFrame => frame.type === 'message')!
    expect(sent).toMatchObject({ type: 'message', session_id: OMNIPUS_MAIN, content: '/clear' })
    expect(sent.client_message_id).toEqual(expect.any(String))
    expectSameSessionNoCreation()
    // Raw server response + public inbound frame boundary. The real reducer
    // must re-read the same session and update the actual screen; no manual
    // local clear, setMessages or query-cache marker replacement is used.
    history = [MARKER]
    const receipt: ServerFrame = { type: 'message_status', session_id: OMNIPUS_MAIN, client_message_id: sent.client_message_id!, state: 'received' }
    const done: ServerFrame = { type: 'done', session_id: OMNIPUS_MAIN, turn_id: 'clear-reply', message_id: 'clear-reply-message', stats: { tokens: 1, cost: 0 } }
    act(() => { useChatStore.getState().handleFrame(receipt); useChatStore.getState().handleFrame(done) })
    expect(await screen.findByTestId('clear-context-divider')).toHaveTextContent('Conversation context cleared')
    expect(screen.queryByText(BEFORE.content!)).not.toBeInTheDocument()
    expect(messagesReads(), 'D6: only the explicit clear refresh reads a page; connected opening used its snapshot').toHaveLength(1)
    expect(router.state.location.pathname).toBe('/omnipus')
    expectSameSessionNoCreation()
    expect(frames.filter((frame) => frame.type === 'message')).toEqual([sent])
  })
  it('OC-instrument catches both an HTTP session POST and an untargeted minting frame', () => {
    const http = { method: 'POST', path: '/api/v1/sessions', body: { agent_id: 'omnipus' } }
    const mint: MessageFrame = { type: 'message', content: '/clear', agent_id: 'omnipus' }
    const targeted: MessageFrame = { ...mint, session_id: OMNIPUS_MAIN }
    expect(isSessionCreate(http)).toBe(true)
    expect(isSessionCreate({ method: 'GET', path: '/api/v1/sessions' })).toBe(false)
    expect(isMintingMessage(mint)).toBe(true)
    expect(isMintingMessage(targeted)).toBe(false)
    expect([{ ...http, method: 'GET' }, http].filter(isSessionCreate)).toEqual([http])
    expect([targeted, mint].filter(isMintingMessage)).toEqual([mint])
  })
})
