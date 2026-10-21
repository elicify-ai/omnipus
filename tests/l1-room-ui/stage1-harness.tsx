// Stage-1 application integration boundary. No component, hook, store, router
// or feature switch is mocked. Only HTTP/WebSocket and browser APIs are doubled.
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { vi } from 'vitest'
import userEvent from '@testing-library/user-event'
import type {
  AboutResponse, ActivityEventsResponse, AgentToolsResponse, AppState,
  GlobalToolPolicies, NotificationList, PerformanceSettings, Session,
  SessionDetail, SessionPage, Task, ToolRegistryEntry, VersionResponse,
} from '@/lib/api/generated/openapi-types'
import type { ServerFrame, ToolApprovalRequiredFrame } from '@/lib/api/generated/asyncapi-types'
// tsconfig.tests follows this harness into the full application route tree.
// Include the ACTUAL production router type registration in that project too;
// type-only import is erased and never runs main.tsx or creates a second app.
import type {} from '@/main'
import { routeTree } from '@/routeTree.gen'
import { queryClient } from '@/lib/queryClient'
import { useAuthStore } from '@/store/auth'
import { useSidebarStore } from '@/store/sidebar'
import { useSessionStore } from '@/store/session'
import { useToolApprovalStore } from '@/store/toolApproval'
import { useSecretRequestStore } from '@/store/secretRequests'
import { useNotificationsStore } from '@/store/notifications'
import { useConnectionStore } from '@/store/connection'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { resetTokenValidationCache } from '@/routes/authValidation'
import { json, MAIN_ID, Network, NOW, resetRealStores, TestSocket, WS } from './harness'
import { availableDetail, page } from '../chat-history-paging/fixtures'

export const MAIN_PATH = `/workspaces/${WS}/chat`
export const SETTINGS_PATH = '/settings?tab=chat'

const originalHeight = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
const originalWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth')
const originalScrollTo = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTo')

// Browser layout edge only: jsdom has no measured viewport. TanStack's real
// virtualizer reads offsetHeight/offsetWidth, and a zero height draws no rows.
export function exposeTranscriptViewport() {
  for (const [property, original] of [['offsetHeight', originalHeight], ['offsetWidth', originalWidth]] as const) {
    Object.defineProperty(HTMLElement.prototype, property, {
      configurable: true,
      get() {
        return this.getAttribute('data-testid') === 'virtualized-message-list'
          ? 800 // Fixture viewport, not a size/timing oracle.
          : original?.get?.call(this) ?? 0
      },
    })
  }
}

const mainSession: Session = {
  id: MAIN_ID, type: 'main', agent_id: 'mia', workspace_id: WS,
  title: 'Mia main chat', status: 'active', protected: true, needs_attention: false,
  created_at: NOW, updated_at: NOW, channel: 'web', partitions: [],
  stats: { tokens_in: 0, tokens_out: 0, tokens_total: 0, cost: 0, tool_calls: 0, message_count: 0 },
}

export class Stage1Network extends Network {
  constructor() {
    super()
    const appState: AppState = {
      onboarding_complete: true, dev_mode_bypass: false,
      identity: { mode: 'platform', edition: 'hosted', signed_in: true },
    }
    const notifications: NotificationList = { notifications: [], unread_count: 0 }
    const version: VersionResponse = { version: '0.1.1', build_sha: 'ca09d06f9' }
    const about: AboutResponse = {
      version: '0.1.1', go_version: 'go1.26.9', os: 'linux', arch: 'amd64',
      uptime: '0s', uptime_seconds: 0, pid: 1, preview_enabled: false,
      warmup_timeout_seconds: 30, frame_ancestors_fallback: false, device_pairing_enabled: false,
    }
    const sessions: SessionPage = { sessions: [mainSession] }
    // D10: available empty history is a complete terminal page, not a
    // messages-only detail that a strict paging consumer must reject.
    const history = page([], { sessionId: MAIN_ID })
    const detail: SessionDetail = availableDetail(history, mainSession)
    const activity: ActivityEventsResponse = { events: [] }
    const performance: PerformanceSettings = { max_tool_iterations: 200 }
    const tasks: Task[] = []
    this.respond('GET', '/api/v1/state', () => json(appState))
    this.respond('GET', '/api/v1/notifications', () => json(notifications))
    this.respond('GET', '/api/v1/tasks', () => json(tasks))
    this.respond('GET', '/api/v1/version', () => json(version))
    this.respond('GET', '/api/v1/about', () => json(about))
    this.respond('GET', '/api/v1/gateway/god-mode', () => json({ enabled: false, available: true, supported: true, persisted: false }))
    this.respond('GET', '/api/v1/sessions', () => json(sessions))
    this.respond('GET', `/api/v1/sessions/${MAIN_ID}`, () => json(detail))
    this.respond('GET', `/api/v1/sessions/${MAIN_ID}/messages`, () => json(history))
    this.respond('GET', '/api/v1/skills', () => json([]))
    this.respond('GET', '/api/v1/commands', () => json([]))
    this.respond('GET', '/api/v1/providers', () => json([]))
    this.respond('GET', '/api/v1/activity', () => json(activity))
    this.respond('GET', '/api/v1/performance', () => json(performance))
    for (const agent of this.roster) {
      this.respond('GET', `/api/v1/agents/${agent.id}`, () => json(agent))
    }
    const tools: ToolRegistryEntry[] = [{
      name: 'write_file', description: 'Write a file', scope: 'general', category: 'filesystem', source: 'builtin',
    }]
    const agentTools: AgentToolsResponse = {
      revision: '0'.repeat(64), override_names: [], config: { builtin: { policies: {} } },
      tools: [], agent_type: 'core',
    }
    const globalPolicies: GlobalToolPolicies = { policies: { write_file: 'ask' } }
    this.respond('GET', '/api/v1/tools', () => json(tools))
    this.respond('GET', '/api/v1/agents/mia/tools', () => json(agentTools))
    this.respond('GET', '/api/v1/security/tool-policies', () => json(globalPolicies))
    this.respond('GET', '/api/v1/mcp-servers', () => json([]))
    // Deliberately supplied so a forbidden read remains a successful,
    // observable regression rather than being masked by a transport error.
    this.respond('GET', '/api/v1/secrets', () => json([]))
  }
}

export function resetStage1App() {
  resetRealStores()
  resetTokenValidationCache()
  if (!Element.prototype.scrollTo) {
    Object.defineProperty(Element.prototype, 'scrollTo', { configurable: true, value() {} })
  }
  vi.stubGlobal('matchMedia', (media: string) => ({
    matches: media === '(min-width: 1024px)' || media === '(min-width: 40rem)',
    media, onchange: null, addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => true,
  }))
  // jsdom implements rAF with an interval. Keep browser frame delivery on
  // the real timeout edge when an expiry case controls only Date/intervals.
  // WsConnection still parses/batches/dispatches every actual frame itself.
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => window.setTimeout(() => callback(performance.now()), 0))
  vi.stubGlobal('cancelAnimationFrame', (handle: number) => window.clearTimeout(handle))
  vi.stubGlobal('WebSocket', TestSocket)
  TestSocket.instances = []
  act(() => {
    useAuthStore.setState(useAuthStore.getInitialState(), true)
    useAuthStore.getState().setUsername('stage1-tester')
    useSidebarStore.setState({ ...useSidebarStore.getInitialState(), isOpen: true, isPinned: true }, true)
    useToolApprovalStore.setState(useToolApprovalStore.getInitialState(), true)
    useSecretRequestStore.getState().reset()
    useNotificationsStore.setState(useNotificationsStore.getInitialState(), true)
    useChatPreferencesStore.setState(useChatPreferencesStore.getInitialState(), true)
    useSessionStore.setState({
      attachedSessionType: 'main',
      sessionByWorkspace: { [WS]: { id: MAIN_ID, type: 'main', title: 'Mia main chat', agentId: 'mia' } },
    })
  })
}

export async function mountStage1App(path = MAIN_PATH) {
  const history = createMemoryHistory({ initialEntries: [path] })
  const router = createRouter({ routeTree, history, defaultPendingMinMs: 0 })
  await act(async () => { await router.load() })
  const mounted = render(<QueryClientProvider client={queryClient}><RouterProvider router={router} /></QueryClientProvider>)
  // Positive controls: the REAL shell and sidebar successfully rendered.
  await screen.findByRole('link', { name: 'Skip to content' })
  await screen.findByRole('button', { name: 'Sales' })
  await waitFor(() => {
    if (TestSocket.instances.length !== 1) throw new Error(`Expected one app WebSocket, got ${TestSocket.instances.length}`)
  })
  const socket = TestSocket.instances[0]!
  await act(async () => { socket.open() })
  return { ...mounted, router, socket }
}

export async function settledMainChat(socket: TestSocket) {
  // Select the member through the REAL sidebar after connection-open. The
  // provider's reattach resets attachedSessionType; a seeded id alone would
  // never exercise the main-only Add participant branch (false-green risk).
  await userEvent.setup().click(await screen.findByRole('button', { name: 'Mia' }))
  await screen.findByTestId('chat-input')
  await waitFor(() => {
    if (useSessionStore.getState().activeSessionId !== MAIN_ID || useSessionStore.getState().attachedSessionType !== 'main') {
      throw new Error('Expected the actual workspace entry to open Mia\'s main chat')
    }
    if (!socket.sent.some((frame) => frame.type === 'attach_session' && frame.session_id === MAIN_ID)) {
      throw new Error('Positive socket control: main chat has not attached')
    }
  })
}

export function approval(overrides: Partial<ToolApprovalRequiredFrame> = {}): ToolApprovalRequiredFrame {
  return {
    type: 'tool_approval_required', approval_id: 'stage1-approval',
    tool_call_id: 'stage1-call', tool_name: 'write_file', args: { path: 'stage1-request.txt' },
    agent_id: 'mia', session_id: MAIN_ID, turn_id: 'stage1-turn',
    // Fixture: five-minute live request, safely away from expiry; no timing oracle.
    expires_in_ms: 300_000, workspace_id: WS, ...overrides,
  }
}

export async function emitFromGateway(socket: TestSocket, frame: ServerFrame) {
  // TestSocket validates the generated schema; WsConnection parses/batches;
  // the actual provider dispatches into the real stores. No enqueue shortcuts.
  await act(async () => { socket.receive(frame) })
}

export async function stopStage1App() {
  cleanup()
  // Allow component unmount's browser timers and socket disconnection to settle.
  await new Promise<void>((resolve) => setTimeout(resolve, 0))
  useConnectionStore.getState().connection?.disconnect()
  queryClient.clear()
  localStorage.clear()
  document.cookie = 'csrf=; Max-Age=0; Path=/'
  for (const [property, original] of [['offsetHeight', originalHeight], ['offsetWidth', originalWidth]] as const) {
    if (original) Object.defineProperty(HTMLElement.prototype, property, original)
    else Reflect.deleteProperty(HTMLElement.prototype, property)
  }
  if (originalScrollTo) Object.defineProperty(Element.prototype, 'scrollTo', originalScrollTo)
  else Reflect.deleteProperty(Element.prototype, 'scrollTo')
  vi.unstubAllGlobals()
}
