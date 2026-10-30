/**
 * Workspace entry while the chat socket is still opening (#1449, CI run
 * 40958296852 on landing/t6 7476a737a, shard "E2E - ui-3": chat-header-narrow.spec.ts
 * "at a coarse pointer > a tap on each control opens only its own thing at every
 * coarse width" never saw the main chat's failed-roster warning).
 *
 * Mechanism, measured with a probe that opens the page's WebSocket late: a fresh page
 * publishes the WsConnection (OmnipusRuntimeProvider.tsx::WsLifecycle, `conn.connect();
 * setConnection(conn)`) BEFORE its socket is OPEN, while the entry's three REST loads can
 * finish first. WsConnection.send() returns false on a socket that is not OPEN, so the
 * entry's attach reported "connected but cannot send": the entry latched `failed-attempt`,
 * the composer stayed off, no `attach_session` was ever sent and nothing retried when the
 * socket opened a moment later.
 *
 * Oracles (specifications and existing contracts, not this implementation):
 *   - src/routes/_app/sessions.$sessionId.tsx, the deep-link route facing the same
 *     condition: "a CONNECTING socket must take the offline path, not a doomed send that
 *     flashes a spurious global error banner" - the attach goes out once the socket is open.
 *   - docs/using-omnipus-ui.md and docs/workspaces.md: "Restoring your conversation…" is
 *     the state while Omnipus chooses the chat; "Could not restore your last conversation.
 *     Retry to try again." is for a list, workspace details or agent list that fails to
 *     load, or an attach that fails. A connection that is still being established is
 *     neither a failure nor a reason to lose the chat.
 *   - The restored main chat must still be a MAIN chat: AddParticipant.tsx::MainChatAddParticipant
 *     renders the header's participant control only for `attachedSessionType === 'main'`.
 *   - Wave-1 Bug 2 (src/store/session.workspace.test.ts) and src/lib/nav/workspaceEntry.ts
 *     (a transport failure is a `failed-attempt` that keeps the committed chat): a failed
 *     load, and an attach whose send fails on a socket that is not opening, still fail at once.
 *   - The bound on the wait is a design choice with no external specification (a safety
 *     valve against a hang): the cases below pin only that it is neither tiny (a handshake
 *     of several seconds must still succeed) nor unbounded.
 *   - "Not waiting" is measured in SIMULATED time (fake timers, AT_ONCE_MS): a wrongly waiting
 *     entry would otherwise end at the bound in the very same state and the case would pass.
 *   - A connection that holds no socket is not "connecting" (first principles: no handshake is
 *     in progress), whatever constants the environment's WebSocket defines.
 *
 * REAL stores, entry flow, WsConnection and reattachActiveSession; only the network
 * edge (REST helpers, the WebSocket constructor) is replaced.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Session, Workspace } from '@/lib/api'
import { fetchAgents, fetchSessionDetail, fetchSessions, fetchWorkspace } from '@/lib/api'
import { ApiError } from '@/lib/api-error'
import { WsConnection } from '@/lib/ws'
import { reattachActiveSession } from '@/components/chat/OmnipusRuntimeProvider'
import { queryClient } from '@/lib/queryClient'
import { logDiagnostic } from '@/lib/telemetry'
import { supersedeNavigationIntent } from '@/store/session/workspaceEntryFlow'
import { MIA } from '../../tests/omnipus-l1-ui-red/fixtures'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useUiStore } from './ui'
import { useWorkspacesStore } from './workspacesStore'

const WS = 'late-socket-workspace'
const MAIN = 'late-socket-main'
const SEND_FAILED_BANNER = 'Could not attach to session — connection dropped. Please reconnect and try again.'
const RESTORE_FAILED_TOAST = 'Could not restore your last conversation. Retry to try again.'
const FAILED_ATTEMPT = {
  status: 'failed-attempt',
  committed: null,
  attempted: { sessionId: MAIN, sendEnabled: false },
  acknowledged: false,
  retry: true,
  fellBack: false,
}

vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchSessions: vi.fn(),
  fetchSessionDetail: vi.fn(),
  fetchWorkspace: vi.fn(),
  fetchAgents: vi.fn(),
}))
vi.mock('@/lib/telemetry', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/telemetry')>(),
  logDiagnostic: vi.fn(),
}))

function mainSession(): Session {
  return {
    id: MAIN, agent_id: 'mia', workspace_id: WS,
    title: 'Mia main chat', type: 'main', message_count: 3,
    created_at: '2026-10-29T12:00:00Z', updated_at: '2026-10-29T12:00:00Z',
  }
}

function workspace(): Workspace {
  return {
    id: WS, name: 'Late socket', revision: '1'.repeat(64), status: 'active',
    pinned: false, pin_order: 0, task_count: 0,
    created_at: '2026-10-29T12:00:00Z', updated_at: '2026-10-29T12:00:00Z',
    core_team: ['mia'], member_configs: { mia: { main_session_id: MAIN } },
  }
}

/** The remembered chat of a returning visitor: the entry restores exactly this. */
function rememberMain() {
  useSessionStore.getState().setWorkspaceSessionDescriptor(WS, {
    id: MAIN, type: 'main', title: 'Mia main chat', agentId: 'mia',
  })
}

/** A page-side WebSocket that starts CONNECTING (readyState 0) and opens on demand. */
class LateSocket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3
  static instances: LateSocket[] = []
  readyState = LateSocket.CONNECTING
  onopen: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  onclose: ((event: { code: number; reason: string }) => void) | null = null
  onerror: (() => void) | null = null
  send = vi.fn()
  close = vi.fn(() => {
    this.readyState = LateSocket.CLOSED
  })

  constructor() {
    LateSocket.instances.push(this)
  }

  /** What a browser does when the handshake completes: OPEN first, then the open event. */
  open() {
    this.readyState = LateSocket.OPEN
    this.onopen?.()
  }

  /** What a browser does when the handshake fails: CLOSED, then the close event. */
  failToOpen() {
    this.readyState = LateSocket.CLOSED
    this.onclose?.({ code: 1006, reason: '' })
  }
}

/** A WebSocket test double that defines OPEN and CLOSED but not CONNECTING (as the Stage-1 TestSocket does). */
class WebSocketDoubleWithoutConnecting {
  static readonly OPEN = 1
  static readonly CLOSED = 3
}

let connections: WsConnection[] = []

/** A real WsConnection that has connect()ed: its socket is CONNECTING and nothing is published yet. */
function connectingConnection(): { conn: WsConnection; socket: LateSocket } {
  vi.stubGlobal('WebSocket', LateSocket)
  const conn: WsConnection = new WsConnection({
    onFrames: vi.fn(),
    // OmnipusRuntimeProvider.tsx::WsLifecycle: publish the flag, then re-bind the active session.
    onConnected: () => {
      useConnectionStore.getState().setConnected(true)
      reattachActiveSession(conn, useConnectionStore.getState().setConnectionError)
    },
    onDisconnected: () => useConnectionStore.getState().recordDisconnect(null),
    onError: vi.fn(),
    onReconnectStateChange: vi.fn(),
  })
  connections.push(conn)
  conn.connect()
  const socket = LateSocket.instances.at(-1)
  if (!socket) throw new Error('WsConnection.connect() did not create a socket')
  return { conn, socket }
}

/**
 * OmnipusRuntimeProvider.tsx::WsLifecycle on a fresh page: connect(), then publish the
 * connection - before its socket has opened.
 */
function publishConnectingSocket(): LateSocket {
  const { conn, socket } = connectingConnection()
  useConnectionStore.getState().setConnection(conn)
  expect(conn.isConnected, 'precondition: the socket is not OPEN yet').toBe(false)
  return socket
}

function attachFrames(socket: LateSocket): Array<Record<string, unknown>> {
  return socket.send.mock.calls
    .map(([raw]) => JSON.parse(raw as string) as Record<string, unknown>)
    .filter((frame) => frame.type === 'attach_session')
}

/** Lets every already-resolved REST mock and promise continuation run (real timers). */
const settle = () => new Promise<void>((resolve) => setTimeout(resolve, 0))

/**
 * "At once" is measured in SIMULATED time: the cases that must not sit in the wait run on fake
 * timers and look at the store after this much simulated time (below the first reconnect backoff,
 * far below the 15 s wait bound). With real timers a wrongly waiting entry would still finish
 * when the bound fires and end in the very same state, so a pass would prove nothing.
 */
const AT_ONCE_MS = 500

function enterWorkspace(): Promise<void> {
  return useSessionStore.getState().enterWorkspaceChat(WS)
}

beforeEach(() => {
  queryClient.clear()
  localStorage.clear()
  vi.clearAllMocks()
  LateSocket.instances = []
  connections = []
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useUiStore.setState({ toasts: [] })
  useWorkspacesStore.setState({ activeWorkspaceId: WS })
  vi.mocked(fetchSessions).mockResolvedValue([mainSession()])
  vi.mocked(fetchWorkspace).mockResolvedValue(workspace())
  // The CI fixture answers /agents with a 500 (the oracle of the failed-roster warning).
  vi.mocked(fetchAgents).mockRejectedValue(new ApiError(500, 'agents unavailable'))
  vi.mocked(fetchSessionDetail).mockRejectedValue(new ApiError(404, 'not found'))
  rememberMain()
})

afterEach(() => {
  // Releases any entry still waiting, then stops the connections' own timers.
  supersedeNavigationIntent()
  for (const conn of connections) conn.disconnect()
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
})

describe('workspace entry while the chat socket is still opening', () => {
  it('waits for the socket, then restores the remembered main chat as a MAIN chat with exactly one attach_session', async () => {
    // A returning browser has its conversation list cached - the state in which a
    // re-bind that ran AFTER the attach would resolve an agent and drop the chat's type.
    queryClient.setQueryData(['sessions'], [mainSession()])
    const socket = publishConnectingSocket()

    const entry = enterWorkspace()
    await settle()

    const waiting = useSessionStore.getState()
    expect(waiting.resolvingSessionForWorkspace[WS], 'still "Restoring your conversation…"').toBe(true)
    expect(waiting.workspaceEntry, 'no verdict yet - in particular no failed restore').toBeNull()
    expect(waiting.activeSessionId).toBeNull()
    expect(useUiStore.getState().toasts, 'no warning for a connection that is still being established').toEqual([])
    expect(useConnectionStore.getState().connectionError, 'no spurious global connection error').toBeNull()
    expect(socket.send, 'nothing is sent on a socket that is not open').not.toHaveBeenCalled()

    socket.open()
    await entry

    const restored = useSessionStore.getState()
    expect(restored.workspaceEntry).toEqual({ status: 'exact', acknowledged: false })
    expect(restored.activeSessionId).toBe(MAIN)
    expect(restored.attachedSessionType, 'still a main chat: the header keeps its participant control').toBe('main')
    expect(restored.sessionByWorkspace[WS]).toEqual({ id: MAIN, type: 'main', title: 'Mia main chat', agentId: 'mia' })
    expect(restored.resolvingSessionForWorkspace[WS], 'the restoring state has ended').toBeUndefined()
    expect(useUiStore.getState().toasts).toEqual([])
    expect(useConnectionStore.getState().connectionError).toBeNull()
    const attaches = attachFrames(socket)
    expect(attaches, 'one attach, not one from the entry and another from the reconnect re-bind').toHaveLength(1)
    expect(attaches[0]).toMatchObject({ type: 'attach_session', session_id: MAIN })
  })

  it('does the same for the team main chosen when nothing is remembered (the welcome entry)', async () => {
    useSessionStore.setState({ sessionByWorkspace: {} })
    localStorage.clear()
    vi.mocked(fetchAgents).mockResolvedValue([MIA])
    const socket = publishConnectingSocket()

    const entry = enterWorkspace()
    await settle()
    expect(useSessionStore.getState().workspaceEntry).toBeNull()
    expect(useUiStore.getState().toasts).toEqual([])

    socket.open()
    await entry

    expect(useSessionStore.getState().workspaceEntry).toEqual({
      status: 'welcome', sessionId: MAIN, agentId: 'mia', sendEnabled: true, acknowledged: false,
    })
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN)
    expect(attachFrames(socket)).toHaveLength(1)
  })

  it('a hidden tab whose socket opens waits for focus instead of inventing a failure', async () => {
    const socket = publishConnectingSocket()
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)

    const entry = enterWorkspace()
    await settle()
    hidden.mockReturnValue(true)
    socket.open()
    await settle()

    expect(useSessionStore.getState().workspaceEntry, 'deferred, not failed').toBeNull()
    expect(useSessionStore.getState().resolvingSessionForWorkspace[WS]).toBe(true)
    expect(useUiStore.getState().toasts).toEqual([])
    expect(attachFrames(socket)).toEqual([])

    hidden.mockReturnValue(false)
    document.dispatchEvent(new Event('visibilitychange'))
    await entry

    expect(useSessionStore.getState().workspaceEntry).toEqual({ status: 'exact', acknowledged: false })
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN)
    expect(attachFrames(socket)).toHaveLength(1)
  })

  it('a socket that never opens ends the wait after a bounded time: the entry then fails with Retry, as it always did', async () => {
    vi.useFakeTimers()
    const socket = publishConnectingSocket()

    const entry = enterWorkspace()
    await vi.advanceTimersByTimeAsync(10_000)

    // 10 s in: a handshake of several seconds must still be waited for.
    expect(useSessionStore.getState().workspaceEntry).toBeNull()
    expect(useSessionStore.getState().resolvingSessionForWorkspace[WS]).toBe(true)
    expect(useUiStore.getState().toasts).toEqual([])

    await vi.advanceTimersByTimeAsync(10_000)
    await entry

    // 20 s in: not an unbounded hang.
    const state = useSessionStore.getState()
    expect(state.workspaceEntry).toEqual(FAILED_ATTEMPT)
    expect(state.activeSessionId, 'a failed attach records nothing').toBeNull()
    expect(state.resolvingSessionForWorkspace[WS]).toBeUndefined()
    expect(useConnectionStore.getState().connectionError).toBe(SEND_FAILED_BANNER)
    expect(socket.send, 'no doomed send on a socket that is not open').not.toHaveBeenCalled()
    expect(logDiagnostic).toHaveBeenCalledWith('sessionWorkspaceEntrySocketWaitTimedOut', { workspaceId: WS })
  })

  it('a socket that closes without ever opening ends the wait at once: the entry fails with Retry instead of waiting out the bound', async () => {
    vi.useFakeTimers()
    const socket = publishConnectingSocket()
    const entry = enterWorkspace()
    await vi.advanceTimersByTimeAsync(0)
    expect(useSessionStore.getState().resolvingSessionForWorkspace[WS]).toBe(true)

    socket.failToOpen()
    await vi.advanceTimersByTimeAsync(AT_ONCE_MS)

    expect(useSessionStore.getState().workspaceEntry, 'ended by the close itself, not by the 15 s bound').toEqual(FAILED_ATTEMPT)
    await entry
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(useConnectionStore.getState().connectionError).toBe(SEND_FAILED_BANNER)
    expect(socket.send).not.toHaveBeenCalled()
  })

  it('a newer navigation intent ends the wait at once, and a socket opening later attaches nothing for the old entry', async () => {
    vi.useFakeTimers()
    const socket = publishConnectingSocket()
    const entry = enterWorkspace()
    await vi.advanceTimersByTimeAsync(0)
    expect(useSessionStore.getState().resolvingSessionForWorkspace[WS]).toBe(true)

    supersedeNavigationIntent()
    await vi.advanceTimersByTimeAsync(AT_ONCE_MS)

    expect(useSessionStore.getState().resolvingSessionForWorkspace[WS], 'the superseded entry released its loading gate, not the 15 s bound').toBeUndefined()
    await entry
    socket.open()
    await vi.advanceTimersByTimeAsync(0)
    expect(attachFrames(socket)).toEqual([])
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(useSessionStore.getState().workspaceEntry).toBeNull()
  })

  it('an entry superseded while its loads were still in flight never starts waiting for the socket', async () => {
    vi.useFakeTimers()
    const socket = publishConnectingSocket()
    let releaseSessions!: (sessions: Session[]) => void
    vi.mocked(fetchSessions).mockImplementationOnce(() => new Promise((resolve) => { releaseSessions = resolve }))
    const entry = enterWorkspace()
    await vi.advanceTimersByTimeAsync(0)

    supersedeNavigationIntent()
    releaseSessions([mainSession()])
    await vi.advanceTimersByTimeAsync(AT_ONCE_MS)

    // A wait registered after the supersede could only end by the socket opening or by the bound.
    expect(useSessionStore.getState().resolvingSessionForWorkspace[WS], 'the stale entry ended without waiting').toBeUndefined()
    await entry
    expect(attachFrames(socket)).toEqual([])
    expect(useSessionStore.getState().workspaceEntry).toBeNull()
  })

  it('an entry that will not attach reports its failed load at once, without waiting for the socket', async () => {
    vi.useFakeTimers()
    const socket = publishConnectingSocket()
    vi.mocked(fetchSessions).mockRejectedValue(new ApiError(503, 'sessions unavailable'))

    const entry = enterWorkspace()
    await vi.advanceTimersByTimeAsync(AT_ONCE_MS)

    expect(useSessionStore.getState().workspaceEntry, 'reported before the 15 s bound could have fired').toEqual(FAILED_ATTEMPT)
    await entry
    expect(useUiStore.getState().toasts).toEqual([
      expect.objectContaining({ message: RESTORE_FAILED_TOAST, variant: 'warning', action: expect.objectContaining({ label: 'Retry' }) }),
    ])
    expect(socket.send).not.toHaveBeenCalled()
  })
})

describe('workspace entry on a connection that is not opening', () => {
  it('attaches at once when the socket is already open', async () => {
    const send = vi.fn().mockReturnValue(true)
    useConnectionStore.setState({ connection: { send, isConnected: true, isConnecting: false } as never, isConnected: true })

    await enterWorkspace()

    expect(useSessionStore.getState().workspaceEntry).toEqual({ status: 'exact', acknowledged: false })
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN)
    expect(useSessionStore.getState().attachedSessionType).toBe('main')
    expect(send).toHaveBeenCalledExactlyOnceWith({ type: 'attach_session', session_id: MAIN })
  })

  it.each([
    ['an OPEN socket whose send fails (Wave-1 Bug 2)', { isConnected: true, isConnecting: false }],
    ['a closed socket that is not opening (reconnect backoff)', { isConnected: false, isConnecting: false }],
    ['a connection that does not report readiness', {}],
  ])('%s: the entry fails at once with Retry and leaves the chat state untouched', async (_label, readiness) => {
    vi.useFakeTimers()
    const send = vi.fn().mockReturnValue(false)
    useConnectionStore.setState({ connection: { send, ...readiness } as never, isConnected: false })

    const entry = enterWorkspace()
    await vi.advanceTimersByTimeAsync(AT_ONCE_MS)

    expect(useSessionStore.getState().workspaceEntry, 'no wait: reported before the 15 s bound could have fired').toEqual(FAILED_ATTEMPT)
    await entry
    expect(useSessionStore.getState().activeSessionId, 'a failed attach records nothing').toBeNull()
    expect(send, 'the one real attempt').toHaveBeenCalledTimes(1)
    expect(useConnectionStore.getState().connectionError).toBe(SEND_FAILED_BANNER)
  })

  it('a real connection that holds no socket fails at once too, in an environment whose WebSocket has no CONNECTING constant', async () => {
    // Test doubles for WebSocket (tests/l1-room-ui/harness.tsx::TestSocket) define OPEN and CLOSED
    // only. With them `readyState === WebSocket.CONNECTING` reads `undefined === undefined`, which
    // must not turn "no socket" (never connected, or between reconnect attempts) into "still opening".
    vi.useFakeTimers()
    vi.stubGlobal('WebSocket', WebSocketDoubleWithoutConnecting)
    const conn = new WsConnection({
      onFrames: vi.fn(), onConnected: vi.fn(), onDisconnected: vi.fn(), onError: vi.fn(), onReconnectStateChange: vi.fn(),
    })
    connections.push(conn)
    useConnectionStore.getState().setConnection(conn)

    const entry = enterWorkspace()
    await vi.advanceTimersByTimeAsync(AT_ONCE_MS)

    expect(useSessionStore.getState().workspaceEntry, 'no socket is not "still opening": reported before the 15 s bound').toEqual(FAILED_ATTEMPT)
    await entry
    expect(useConnectionStore.getState().connectionError).toBe(SEND_FAILED_BANNER)
  })
})

describe('WsConnection.isConnecting', () => {
  it('is false before connect(), true while the socket is CONNECTING, false once it is OPEN and false after it closes', () => {
    vi.stubGlobal('WebSocket', LateSocket)
    const conn = new WsConnection({
      onFrames: vi.fn(), onConnected: vi.fn(), onDisconnected: vi.fn(), onError: vi.fn(), onReconnectStateChange: vi.fn(),
    })
    connections.push(conn)
    expect(conn.isConnecting, 'no socket yet').toBe(false)

    conn.connect()
    const socket = LateSocket.instances.at(-1) as LateSocket
    expect(conn.isConnecting, 'handshake in progress').toBe(true)
    expect(conn.isConnected).toBe(false)

    socket.open()
    expect(conn.isConnecting, 'handshake done').toBe(false)
    expect(conn.isConnected).toBe(true)

    socket.onclose?.({ code: 1006, reason: '' })
    expect(conn.isConnecting, 'dropped; the reconnect timer has not created a socket yet').toBe(false)
    expect(conn.isConnected).toBe(false)
  })

  // WHATWG HTML, WebSocket.readyState: CONNECTING 0, OPEN 1, CLOSING 2, CLOSED 3. A socket that is going
  // away changes state a task BEFORE its close event reaches WsConnection (which only then drops the
  // socket), so a read in between sees a non-null socket that is not connecting.
  it.each([
    ['CLOSING (the peer has begun closing; the close event is not delivered yet)', LateSocket.CLOSING],
    ['CLOSED (the handshake failed; the close event is not delivered yet)', LateSocket.CLOSED],
  ])('is false for a socket that is %s', (_label, readyState) => {
    const { conn, socket } = connectingConnection()
    expect(conn.isConnecting, 'precondition: handshake in progress').toBe(true)

    socket.readyState = readyState

    expect(conn.isConnecting).toBe(false)
  })

  it('is false while there is no socket, even where the environment WebSocket has no CONNECTING constant', () => {
    vi.stubGlobal('WebSocket', WebSocketDoubleWithoutConnecting)
    const conn = new WsConnection({
      onFrames: vi.fn(), onConnected: vi.fn(), onDisconnected: vi.fn(), onError: vi.fn(), onReconnectStateChange: vi.fn(),
    })
    connections.push(conn)

    expect(conn.isConnecting, 'no socket means no handshake in progress').toBe(false)
  })
})
