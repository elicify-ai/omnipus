// WU-10 FR-047 / K9; US-5, BDD-5.1/5.2/5.8; chief amendment 2026-10-14.
// The SERVER offers setup in Omnipus's existing main. Creating/opening an
// empty workspace must never send the retired client kickoff. Explicit Team
// setup remains W18. Offer/failure text comes from the dispatch/spec authority.
// REAL sidebar/container/tabs/chat/runtime/router/stores/HTTP validators/frame
// reducer; only raw HTTP, socket send and absent browser geometry are doubled.
import type { ComponentType, ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryHistory, createRootRoute, createRoute, createRouter, Outlet, RouterProvider, useParams } from '@tanstack/react-router'
import { QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider } from '@assistant-ui/react'
import { Sidebar } from '@/components/layout/Sidebar'
import { WorkspaceTabContainer } from './WorkspaceTabContainer'
import { WorkspaceChatTab } from './WorkspaceChatTab'
import { WorkspaceTeamTab } from './WorkspaceTeamTab'
import { queryClient } from '@/lib/queryClient'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { workspacesQueryKeys } from '@/lib/api'
import type { Session } from '@/lib/api'
import type { AppState, Message, Session as WireSession, SessionPage, Workspace } from '@/lib/api/generated/openapi-types'
import type { ClientFrame, MessageFrame, ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { wireHistoryPage } from '../../../tests/chat-history-paging/legacy-wire'
import { WsConnection } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useSidebarStore } from '@/store/sidebar'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import { DEFAULT, MIA, OMNIPUS, OMNIPUS_MAIN, SALES, SALES_MAIN, delegation, session, workspace } from '../../../tests/omnipus-l1-ui-red/fixtures'

const screens = import.meta.glob<{ OmnipusChatScreen: ComponentType }>('../chat/OmnipusChatScreen.tsx')
const EMPTY = workspace('research', { core_team: [], member_configs: {}, setup_pending: true })
const AGENTS = [{ ...OMNIPUS, model: 'fixture-model', needs_model: false }, MIA]
const SESSIONS = [session(OMNIPUS_MAIN, 'omnipus', 'default', { type: 'main', needs_attention: false }),
  session(SALES_MAIN, 'mia', 'sales', { type: 'main', needs_attention: false })]
// Independent exact text supplied by the WU-10 server-offer requirement. Do
// not import a producer constant or expect server-internal target metadata.
const OFFER = 'Workspace setup offered for Research.'
const FAILURE = 'The setup offer for Research could not be started. Open its Team tab to set up its team.'
const OFFER_ROW: Message = { id: 'offer-row', agent_id: 'omnipus', timestamp: '2026-10-14T00:00:00Z', type: 'system', role: 'system', content: OFFER }
const FAILURE_ROW: Message = { id: 'offer-failure-row', agent_id: 'omnipus', timestamp: '2026-10-14T00:01:00Z', type: 'system', role: 'system', content: FAILURE }
const APP_STATE: AppState = { onboarding_complete: true, dev_mode_bypass: false,
  identity: { mode: 'local', edition: 'core', signed_in: true, blocked_reason: 'none' } }
const scrollDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollIntoView')
let socket: WsConnection
let frames: ClientFrame[]
let requests: { method: string; path: string; body: unknown }[]
let unexpected: string[]
let workspaces: Workspace[]
let history: Message[]

function response(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
}
function asWire(s: Session): WireSession {
  return { id: s.id, agent_id: s.agent_id, workspace_id: s.workspace_id, title: s.title, type: s.type,
    status: 'active', created_at: s.created_at, updated_at: s.updated_at, channel: 'webchat', partitions: [], needs_attention: false,
    stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 } }
}
function kickoffFrames(input: readonly ClientFrame[]) {
  return input.filter((frame) => {
    if (!('metadata' in frame)) return false
    const metadata = frame.metadata
    return typeof metadata === 'object' && metadata !== null && Object.hasOwn(metadata, 'workspace_setup_kickoff')
  })
}
function chatFrames() { return frames.filter((frame): frame is MessageFrame => frame.type === 'message') }
function expectNoClientKickoff() { expect(kickoffFrames(frames)).toEqual([]) }
function RuntimeHost({ children }: { children: ReactNode }) {
  const runtime = useOmnipusRuntime()
  return <AssistantRuntimeProvider runtime={runtime}>{children}</AssistantRuntimeProvider>
}
function routeWorkspaceId() {
  const { workspaceId } = useParams({ strict: false })
  if (!workspaceId) expect.fail('BLOCKED: test route lacks required workspace id')
  return workspaceId
}
function WorkspaceHost() { return <WorkspaceTabContainer workspaceId={routeWorkspaceId()} /> }
function ChatTab() { return <WorkspaceChatTab workspaceId={routeWorkspaceId()} /> }
function TeamTab() { return <WorkspaceTeamTab workspaceId={routeWorkspaceId()} /> }

beforeEach(() => {
  localStorage.clear()
  queryClient.clear()
  frames = []
  requests = []
  unexpected = []
  workspaces = [DEFAULT, SALES, EMPTY]
  history = []
  document.cookie = 'csrf=setup-offer-test; path=/'
  vi.stubGlobal('ResizeObserver', undefined)
  vi.stubGlobal('matchMedia', (media: string) => ({ matches: media.includes('min-width'), media, onchange: null,
    addListener: () => {}, removeListener: () => {}, addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false }))
  Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: vi.fn() })
  vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockImplementation(async (input, init) => {
    const url = new URL(String(input), 'https://test.invalid')
    const method = (init?.method ?? 'GET').toUpperCase()
    const path = url.pathname + url.search
    const body: unknown = typeof init?.body === 'string' ? JSON.parse(init.body) : null
    requests.push({ method, path, body })
    if (method === 'POST' && url.pathname === '/api/v1/workspaces') {
      workspaces = [DEFAULT, SALES, EMPTY]
      return response({ ...EMPTY, persistence_status: 'complete', activation_status: 'active', changed_fields: ['workspaces.research'] } satisfies Workspace)
    }
    if (method === 'GET') {
      if (url.pathname === '/api/v1/workspaces') return response(url.searchParams.get('status') === 'archived' ? [] : workspaces)
      if (url.pathname === '/api/v1/workspaces/research') return response(EMPTY)
      if (url.pathname === '/api/v1/workspaces/research/delegation') return response(delegation(EMPTY))
      if (url.pathname === '/api/v1/workspaces/research/rooms') return response([])
      if (url.pathname === '/api/v1/agents') return response(AGENTS)
      if (url.pathname === '/api/v1/sessions') {
        const workspaceId = url.searchParams.get('workspace_id')
        return response({ sessions: SESSIONS.filter((s) => !workspaceId || s.workspace_id === workspaceId).map(asWire) } satisfies SessionPage)
      }
      if (url.pathname === `/api/v1/sessions/${OMNIPUS_MAIN}/messages`) return response(wireHistoryPage(OMNIPUS_MAIN, history, history.length))
      if (url.pathname === '/api/v1/agents/omnipus/activity-runs') return response([])
      if (url.pathname === '/api/v1/skills' || url.pathname === '/api/v1/providers' || url.pathname === '/api/v1/commands') return response([])
      if (url.pathname === '/api/v1/security/sandbox-status') return response({ backend: 'fallback', available: true,
        kernel_level: false, policy_applied: false, seccomp_enabled: false, bind_ports_count: 0 })
      if (url.pathname === '/api/v1/state') return response(APP_STATE)
      if (url.pathname === '/api/v1/gateway/god-mode') return response({ enabled: false, supported: true, available: true, persisted: false })
    }
    unexpected.push(`${method} ${path}`)
    throw new Error(`Unspecified setup-offer HTTP edge: ${method} ${path}`)
  }))
  socket = new WsConnection({ onConnected: () => {}, onDisconnected: () => {}, onError: () => {} })
  vi.spyOn(socket, 'send').mockImplementation((frame) => { frames.push(frame); return true })
  useConnectionStore.setState({ ...useConnectionStore.getInitialState(), connection: socket, isConnected: true }, true)
  useUiStore.setState(useUiStore.getInitialState(), true)
  useSidebarStore.setState({ isOpen: true, isPinned: true })
  useWorkspacesStore.setState({ activeWorkspaceId: null })
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useChatStore.setState(useChatStore.getInitialState(), true)
  queryClient.setQueryData(['agents'], AGENTS)
  queryClient.setQueryData(['sessions'], SESSIONS)
})
afterEach(() => {
  cleanup()
  queryClient.clear()
  socket.disconnect()
  useConnectionStore.setState({ connection: null, isConnected: false })
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  if (scrollDescriptor) Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', scrollDescriptor)
  else Reflect.deleteProperty(HTMLElement.prototype, 'scrollIntoView')
  document.cookie = 'csrf=; Max-Age=0; path=/'
  expect(unexpected, 'no real HTTP and no silent extra session/command endpoint').toEqual([])
})

async function mountApp(initial: string) {
  const load = screens['../chat/OmnipusChatScreen.tsx']
  if (!load) expect.fail('BLOCKED: OmnipusChatScreen missing — required by FR-047 setup offer into existing main')
  const { OmnipusChatScreen } = await load()
  const root = createRootRoute({ component: () => <RuntimeHost><Sidebar /><Outlet /></RuntimeHost> })
  const home = createRoute({ getParentRoute: () => root, path: '/', component: () => <p>Workspace creation fixture</p> })
  const workspaceRoute = createRoute({ getParentRoute: () => root, path: '/workspaces/$workspaceId', component: WorkspaceHost })
  const chat = createRoute({ getParentRoute: () => workspaceRoute, path: 'chat', component: ChatTab })
  const team = createRoute({ getParentRoute: () => workspaceRoute, path: 'team', component: TeamTab })
  const omnipus = createRoute({ getParentRoute: () => root, path: '/omnipus', component: () => <OmnipusChatScreen /> })
  const router = createRouter({ routeTree: root.addChildren([home, workspaceRoute.addChildren([chat, team]), omnipus]),
    history: createMemoryHistory({ initialEntries: [initial] }) })
  render(<QueryClientProvider client={queryClient}><RouterProvider router={router} /></QueryClientProvider>)
  return router
}
async function expectEmptyResearch() {
  expect(await screen.findByTestId('workspace-entry-empty-team')).toHaveTextContent('No agents in Research yet')
  expect(screen.getByRole('button', { name: /^Set up a team$/i })).toBeVisible()
  await waitFor(() => expect(queryClient.isFetching()).toBe(0))
  expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('research')
}
function seedOmnipus() {
  useSessionStore.setState({ activeSessionId: OMNIPUS_MAIN, activeAgentId: 'omnipus', activeAgentType: 'core' })
  useWorkspacesStore.setState({ activeWorkspaceId: 'research' })
}
async function expectSystemRows(failed: boolean) {
  const offer = await screen.findByText(OFFER, { exact: true })
  expect(offer).toBeVisible()
  const offerRow = offer.closest('[data-message-role]')
  expect(offerRow).toHaveAttribute('data-message-role', 'system')
  expect(offerRow).toHaveAttribute('data-message-id', OFFER_ROW.id)
  if (failed) {
    const failure = await screen.findByText(FAILURE, { exact: true })
    expect(failure).toBeVisible()
    expect(failure.closest('[data-message-role]')).toHaveAttribute('data-message-role', 'system')
    expect(offer.compareDocumentPosition(failure) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
  } else expect(screen.queryByText(FAILURE, { exact: true })).not.toBeInTheDocument()
  expect(useSessionStore.getState().activeSessionId).toBe(OMNIPUS_MAIN)
  expect(chatFrames()).toEqual([])
  expectNoClientKickoff()
}

describe('FR-047 server-owned workspace setup offer', () => {
  it('creation and later opening of an empty workspace send no chat frame and no client kickoff marker', async () => {
    workspaces = [DEFAULT, SALES]
    const router = await mountApp('/')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'New workspace' }))
    await user.type(screen.getByRole('textbox', { name: 'New workspace name' }), 'Research{Enter}')
    await expectEmptyResearch()
    expect(router.state.location.pathname).toBe('/workspaces/research/chat')
    expect(queryClient.getQueryData<Workspace[]>(workspacesQueryKeys.list({ status: 'active' }))?.find((w) => w.id === 'research')?.core_team).toEqual([])
    expect(requests.filter((r) => r.method === 'POST')).toEqual([{ method: 'POST', path: '/api/v1/workspaces', body: { name: 'Research' } }])
    expect(chatFrames()).toEqual([])
    expectNoClientKickoff()
    await act(async () => { await router.navigate({ to: '/' }) })
    await act(async () => { await router.navigate({ to: '/workspaces/$workspaceId/chat', params: { workspaceId: 'research' } }) })
    await expectEmptyResearch()
    expect(chatFrames()).toEqual([])
    expectNoClientKickoff()
  })
  it('a direct empty-workspace load with setup_pending=true never starts the retired client kickoff', async () => {
    await mountApp('/workspaces/research/chat')
    await expectEmptyResearch()
    expect(chatFrames()).toEqual([])
    expectNoClientKickoff()
    expect(requests.filter((r) => r.method !== 'GET')).toEqual([])
  })
  it('BDD-5.2 Set up a team still opens the existing Omnipus main with W18, not a kickoff', async () => {
    const router = await mountApp('/workspaces/research/team')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /^Set up a team$/i }))
    expect(await screen.findByTestId('omnipus-chat-screen')).toBeVisible()
    expect(router.state.location.pathname).toBe('/omnipus')
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('research')
    expect(useSessionStore.getState().activeSessionId).toBe(OMNIPUS_MAIN)
    expect(frames.filter((f) => f.type === 'attach_session')).toEqual([{ type: 'attach_session', session_id: OMNIPUS_MAIN }])
    expect(chatFrames()).toHaveLength(1)
    expect(chatFrames()[0]).toMatchObject({ type: 'message', session_id: OMNIPUS_MAIN,
      workflow_start: { workflow: 'W18', target_workspace_id: 'research' } })
    // Chat binding is default; the distinct W18 workflow target remains research.
    expect(chatFrames()[0].metadata ?? {}).toHaveProperty('workspace_id', 'default')
    expectNoClientKickoff()
  })
  it.each([
    { carrier: 'history', failed: false }, { carrier: 'history', failed: true },
    { carrier: 'replay', failed: false }, { carrier: 'replay', failed: true },
  ])('Omnipus $carrier restores system offer text (failure=$failed) without sending a kickoff', async ({ carrier, failed }) => {
    seedOmnipus()
    const rows = failed ? [OFFER_ROW, FAILURE_ROW] : [OFFER_ROW]
    history = carrier === 'history' ? rows : []
    await mountApp('/omnipus')
    expect(await screen.findByTestId('omnipus-chat-screen')).toBeVisible()
    if (carrier === 'replay') {
      await waitFor(() => expect(queryClient.isFetching()).toBe(0))
      act(() => {
        for (const row of rows) {
          const frame: ServerFrame = { type: 'replay_message', session_id: OMNIPUS_MAIN,
            id: row.id, role: 'system', agent_id: 'omnipus', content: row.content!, timestamp: row.timestamp }
          useChatStore.getState().handleFrame(frame)
        }
        useChatStore.getState().handleFrame({ type: 'done', session_id: OMNIPUS_MAIN, stats: { frames_emitted: rows.length } })
      })
    }
    await expectSystemRows(failed)
    expect(requests.some((r) => r.path === `/api/v1/sessions/${OMNIPUS_MAIN}/messages`)).toBe(true)
  })
  it('ordinary Omnipus history does not manufacture a setup offer or a failure note', async () => {
    seedOmnipus()
    history = [{ ...OFFER_ROW, id: 'ordinary-system', content: 'Ordinary system information.' }]
    await mountApp('/omnipus')
    expect(await screen.findByText('Ordinary system information.', { exact: true })).toBeVisible()
    expect(screen.queryByText(OFFER, { exact: true })).not.toBeInTheDocument()
    expect(screen.queryByText(FAILURE, { exact: true })).not.toBeInTheDocument()
    expect(chatFrames()).toEqual([])
  })
  it('the kickoff detector rejects the retired key even when its value is false', () => {
    const plain: MessageFrame = { type: 'message', session_id: OMNIPUS_MAIN, content: 'ordinary' }
    const yes: MessageFrame = { ...plain, metadata: { workspace_setup_kickoff: true } }
    const no: MessageFrame = { ...plain, metadata: { workspace_setup_kickoff: false } }
    expect(kickoffFrames([plain, yes, no])).toEqual([yes, no])
    expect(kickoffFrames([plain])).toEqual([])
  })
})
