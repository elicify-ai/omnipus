// #1428 step 2 RED harness. Real stores, real composer, real Sidebar.
// Network and the router are the only doubles. Oracles are spec C r9, not the
// spike and not the current implementation.
import { act, type RenderResult, render, screen, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AssistantRuntimeProvider } from '@assistant-ui/react'
import { useEffect, type ReactNode } from 'react'
import { useComposerRuntime, useThreadRuntime } from '@assistant-ui/react'
import { expect, vi } from 'vitest'
import type { ClientFrame, ServerFrame } from '@/lib/api/generated/asyncapi-types'
import type { ChatMessage } from '@/store/chat'
import { makeBucketMessages } from '@/store/chat/messages'
import { emptyHistoryState } from '@/store/chat/history-state'
import type { Agent, Session, Workspace } from '@/lib/api'
import { WsConnection } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useSidebarStore } from '@/store/sidebar'
import { useAuthStore } from '@/store/auth'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import { OmnipusComposer } from '@/components/chat/composer/OmnipusComposer'
import { Sidebar } from '@/components/layout/Sidebar'
import { SearchModal } from '@/components/search/SearchModal'

if (typeof window !== 'undefined' && !window.matchMedia) {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: true,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }),
  })
}
if (typeof HTMLElement !== 'undefined') {
  HTMLElement.prototype.hasPointerCapture = () => false
  HTMLElement.prototype.scrollIntoView = () => {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
}

const mockNavigate = vi.hoisted(() => vi.fn())

vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: '/workspaces/w1/chat' }),
  useNavigate: () => mockNavigate,
  useRouter: () => ({ navigate: mockNavigate }),
  useSearch: () => ({}),
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string }) => (
    <a href={to} tabIndex={0} {...rest}>{children}</a>
  ),
}))

const apiMocks = vi.hoisted(() => ({
  fetchSessions: vi.fn(),
  fetchAgents: vi.fn(),
  fetchWorkspaces: vi.fn(),
  fetchWorkspace: vi.fn(),
  deleteSession: vi.fn(),
  createSession: vi.fn(),
  uploadFiles: vi.fn(),
  fetchCommands: vi.fn(),
  fetchSkills: vi.fn(),
  logout: vi.fn(),
  fetchAppState: vi.fn(),
  fetchGodMode: vi.fn(),
  fetchSessionPage: vi.fn(),
  createWorkspace: vi.fn(),
}))

export { apiMocks }

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSessions: apiMocks.fetchSessions,
    fetchAgents: apiMocks.fetchAgents,
    fetchWorkspaces: apiMocks.fetchWorkspaces,
    fetchWorkspace: apiMocks.fetchWorkspace,
    deleteSession: apiMocks.deleteSession,
    createSession: apiMocks.createSession,
    uploadFiles: apiMocks.uploadFiles,
    fetchCommands: apiMocks.fetchCommands,
    fetchSkills: apiMocks.fetchSkills,
    logout: apiMocks.logout,
    fetchAppState: apiMocks.fetchAppState,
    fetchGodMode: apiMocks.fetchGodMode,
    fetchSessionPage: apiMocks.fetchSessionPage,
    createWorkspace: apiMocks.createWorkspace,
  }
})

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: '/mock-avatar.svg' }))

/** Exact strings from spec C r9. Not copied from the running app. */
export const NOT_STOPPED = 'Not stopped. Press Stop again.'
export const OLD_TAB_STOP = 'Not stopped. Reload this page.'
export const SWITCHED = 'Not sent because you switched chats'
export const CONN_FAILED = 'Not sent: the connection failed. Send it again when connected.'
export const NOT_CONNECTED = 'Not connected. Try again when connected.'
export const UNSUPPORTED_BANNER = 'This page and the server are different versions. Your unsent text is kept below. Reload the page to send.'
export const RESTORED_LINE = 'Restored, not sent. Send it again or discard it.'
export const UNCERTAIN_LINE = 'This may already have reached the agent. Check that chat before sending it again.'
export const UPLOAD_404 = 'This chat no longer exists'
export const UPLOAD_409 = 'File not sent because of a page error. Reload if it happens again.'
export const UPLOAD_413 = 'File too large'
export const UPLOAD_NETWORK = 'Upload failed. Try again.'

export const CLIENT_COMMANDS = [
  {
    name: 'stop-redirect',
    label: '/stop-redirect',
    description: 'Stop this chat and continue with a new instruction',
    delivery: 'client' as const,
    available_while_streaming: true,
    argument_hint: '<instruction>',
  },
  {
    name: 'cancel',
    label: '/cancel',
    description: 'Cancel the current turn',
    delivery: 'client' as const,
    available_while_streaming: true,
  },
  {
    name: 'stop',
    label: '/stop',
    description: 'Stop the current turn',
    delivery: 'client' as const,
    available_while_streaming: true,
  },
]

export function makeWorkspace(overrides: Partial<Workspace> = {}): Workspace {
  return {
    id: 'w1',
    name: 'Sales',
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    revision: '0'.repeat(64),
    ...overrides,
  }
}

export function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: 'mia',
    name: 'Mia',
    type: 'core',
    ...overrides,
  } as Agent
}

export function makeSession(overrides: Partial<Session> = {}): Session {
  return {
    id: 'chat-a',
    agent_id: 'mia',
    active_agent_id: 'mia',
    title: 'Sales chat',
    type: 'chat',
    created_at: '2026-07-16T09:00:00Z',
    updated_at: '2026-07-16T11:00:00Z',
    message_count: 1,
    workspace_id: 'w1',
    protected: false,
    ...overrides,
  } as Session
}

export class RecordingConnection {
  readonly sent: ClientFrame[] = []
  fail = false
  throwOnSend = false
  send(frame: ClientFrame): boolean {
    if (this.throwOnSend) throw new Error('OPEN socket send threw')
    if (this.fail) return false
    this.sent.push(structuredClone(frame))
    return true
  }
}

export function framesOf(type: ClientFrame['type'], sent: ClientFrame[] = recording.sent): ClientFrame[] {
  return sent.filter((frame) => frame.type === type)
}

let recording = new RecordingConnection()
let queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
let view: RenderResult | null = null

export const composerProbe = {
  text: '',
  attachments: 0,
}

/** Spec 4.5 rule 5: a runtime append is a submit entry. The probe calls the real thread runtime. */
export const runtimeProbe = {
  append: (text: string): void => {
    void text
    throw new Error('runtime append probe is not mounted')
  },
}

function RuntimeProbe() {
  const thread = useThreadRuntime()
  runtimeProbe.append = (text: string) => {
    thread.append(text)
  }
  return null
}

function ComposerProbe() {
  const runtime = useComposerRuntime()
  useEffect(() => {
    const read = () => {
      const state = runtime.getState()
      composerProbe.text = state.text ?? ''
      composerProbe.attachments = state.attachments?.length ?? 0
    }
    read()
    return runtime.subscribe(read)
  }, [runtime])
  return null
}

function ChatSurface({ sidebar }: { sidebar: boolean }) {
  const runtime = useOmnipusRuntime()
  return (
    <AssistantRuntimeProvider runtime={runtime}>
      <ComposerProbe />
      <RuntimeProbe />
      <OmnipusComposer />
      {sidebar ? (
        <>
          <Sidebar />
          <SearchModal />
        </>
      ) : null}
    </AssistantRuntimeProvider>
  )
}

export function resetBindingWorld(): RecordingConnection {
  cleanup()
  view = null
  recording = new RecordingConnection()
  queryClient.clear()
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  localStorage.clear()
  act(() => {
    useSessionStore.setState(useSessionStore.getInitialState(), true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useUiStore.setState(useUiStore.getInitialState(), true)
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useSidebarStore.setState({ isPinned: true, isOpen: true })
    useAuthStore.setState({ username: 'ada', token: 't', role: 'owner' } as never)
    useConnectionStore.setState({
      connection: recording as unknown as WsConnection,
      isConnected: true,
      reconnectPhase: null,
      connectionError: null,
    })
    useWorkspacesStore.getState().setActiveWorkspaceId('w1')
    useSessionStore.getState().setActiveSession('chat-a', 'mia', 'core')
  })
  composerProbe.text = ''
  composerProbe.attachments = 0
  runtimeProbe.append = () => {
    throw new Error('runtime append probe is not mounted')
  }
  apiMocks.fetchSessions.mockReset().mockResolvedValue([])
  apiMocks.fetchAgents.mockReset().mockResolvedValue([makeAgent()])
  apiMocks.fetchWorkspaces.mockReset().mockResolvedValue([makeWorkspace()])
  apiMocks.fetchWorkspace.mockReset().mockResolvedValue(makeWorkspace())
  apiMocks.deleteSession.mockReset().mockResolvedValue(undefined)
  apiMocks.createSession.mockReset().mockResolvedValue({ id: 'created', agent_id: 'mia' })
  apiMocks.uploadFiles.mockReset().mockResolvedValue({ files: [] })
  apiMocks.fetchCommands.mockReset().mockResolvedValue(CLIENT_COMMANDS)
  apiMocks.fetchSkills.mockReset().mockResolvedValue([])
  apiMocks.logout.mockReset().mockResolvedValue(undefined)
  apiMocks.fetchAppState.mockReset().mockResolvedValue({
    onboarding_complete: true,
    dev_mode_bypass: false,
    identity: { mode: 'platform', edition: 'hosted', signed_in: true },
  })
  apiMocks.fetchGodMode.mockReset().mockResolvedValue({
    enabled: false, available: true, supported: true, persisted: false,
  })
  apiMocks.fetchSessionPage.mockReset().mockResolvedValue({ sessions: [] })
  apiMocks.createWorkspace.mockReset()
  mockNavigate.mockReset()
  return recording
}

export function mountComposer(options: { sidebar?: boolean } = {}): RenderResult {
  view = render(
    <QueryClientProvider client={queryClient}>
      <ChatSurface sidebar={options.sidebar ?? false} />
    </QueryClientProvider>,
  )
  return view
}

export function input(): HTMLTextAreaElement {
  return screen.getByTestId('chat-input') as HTMLTextAreaElement
}

export async function typeIntoComposer(text: string): Promise<void> {
  const user = userEvent.setup()
  await user.click(input())
  await user.type(input(), text)
}

export function sent(): ClientFrame[] {
  return recording.sent
}

export function connection(): RecordingConnection {
  return recording
}

/**
 * Spec 4.4.4: reach a new chat by deleting the open saved chat through the
 * real Sidebar "Search sessions" button. Never enterNewChat, openSearchModal()
 * or setActiveSession(null) from a test.
 */
export async function deleteOpenChatThroughSidebar(title: string): Promise<void> {
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Search sessions' }))
  const del = await screen.findByRole('button', { name: `Delete ${title}` })
  await user.click(del)
  await user.click(screen.getByRole('button', { name: 'Delete' }))
  const close = screen.queryByRole('button', { name: 'Close' })
  if (close) await user.click(close)
}

export function seedSavedChat(session: Session, workspace?: Workspace, agent?: Agent): void {
  const ws = workspace ?? makeWorkspace({ id: session.workspace_id ?? 'w1' })
  const ag = agent ?? makeAgent({ id: session.agent_id, name: session.agent_id === 'mia' ? 'Mia' : session.agent_id })
  apiMocks.fetchSessions.mockResolvedValue([session])
  apiMocks.fetchAgents.mockResolvedValue([ag])
  apiMocks.fetchWorkspaces.mockResolvedValue([ws])
  act(() => {
    useWorkspacesStore.getState().setActiveWorkspaceId(ws.id)
    useSessionStore.getState().setActiveSession(session.id, ag.id, 'core')
  })
}

export class FakeSocket {
  static readonly OPEN = 1
  static readonly CLOSED = 3
  static instances: FakeSocket[] = []
  readyState = 0
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  readonly sent: ClientFrame[] = []
  send = (data: string) => {
    this.sent.push(JSON.parse(data) as ClientFrame)
  }
  constructor(readonly url: string) { FakeSocket.instances.push(this) }
  open(): void {
    this.readyState = FakeSocket.OPEN
    this.onopen?.(new Event('open'))
  }
  receive(frame: ServerFrame): void {
    this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(frame) }))
  }
  close(code = 1000, reason = ''): void {
    this.readyState = FakeSocket.CLOSED
    this.onclose?.(new CloseEvent('close', { code, reason, wasClean: code === 1000 }))
  }
}

export function installFakeWebSocket(): void {
  FakeSocket.instances = []
  vi.stubGlobal('WebSocket', FakeSocket)
}

/** Spec C-1: do not hard-code K. Assert the shape and that two mints differ. */
export function assertNewChatKey(value: unknown): string {
  expect(typeof value, 'spec 4.2: new_chat_key is a string').toBe('string')
  const key = value as string
  expect(key, 'spec 4.2: 16..128 of [A-Za-z0-9_-]').toMatch(/^[A-Za-z0-9_-]{16,128}$/)
  return key
}

export function deliverFrame(frame: ServerFrame): void {
  // Today's session_state handler reads pending_approvals before any
  // product behaviour under test. A spec snapshot that omits it is still a
  // session_state; the empty list is the handler's "none pending" input.
  // Narrow on the discriminator first: spreading the ServerFrame union is
  // not a legal object spread, and `in` against the required field narrows
  // the true branch to never.
  let safe: ServerFrame = frame
  if (frame.type === 'session_state') {
    const pending = Object.prototype.hasOwnProperty.call(frame, 'pending_approvals')
      ? frame.pending_approvals
      : []
    safe = { ...frame, pending_approvals: pending }
  }
  act(() => {
    useChatStore.getState().handleFrame(safe as never)
  })
}

/** A streaming assistant answer in the open chat, so Stop has something to freeze. */
export function seedStreamingAnswer(sessionId: string, content = 'partial answer'): void {
  const bubble: ChatMessage = {
    id: 'live-bubble-1',
    role: 'assistant',
    content,
    timestamp: '2026-01-01T00:00:00Z',
    status: 'streaming',
    isStreaming: true,
    agentId: 'mia',
  }
  const bucket = makeBucketMessages([bubble])
  act(() => {
    useChatStore.setState((s) => ({
      ...s,
      sessionsById: {
        ...(s.sessionsById ?? {}),
        [sessionId]: {
          ...((s.sessionsById ?? {})[sessionId] ?? {}),
          ...bucket,
          history: emptyHistoryState(),
          isStreaming: true,
          isReplaying: false,
          toolCalls: {},
          toolCallOrder: [],
          // releaseSessionHistory reads this map. A partial bucket throws.
          textAtToolCallStart: {},
        },
      },
      messages: [bubble],
      isStreaming: true,
      isReplaying: false,
    }))
  })
}

export function controlFrames(frames: ClientFrame[]): ClientFrame[] {
  return frames.filter((frame) => frame.type === 'message' || frame.type === 'redirect' || frame.type === 'cancel')
}

export async function flushInbound(): Promise<void> {
  // Fake timers (the 1006 retry) never fire rAF until they are advanced.
  if (vi.isFakeTimers()) {
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    return
  }
  await act(async () => {
    await new Promise<void>((resolve) => {
      if (typeof requestAnimationFrame === 'function') requestAnimationFrame(() => resolve())
      else setTimeout(resolve, 0)
    })
  })
}
