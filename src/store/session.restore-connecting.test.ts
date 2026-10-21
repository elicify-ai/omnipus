/**
 * FR-004 restore/reconnect contract: a visible remembered conversation survives
 * an initial CONNECTING socket; only an OPEN socket's refused send is a failure.
 * Real connection, stores, workspace entry and reconnect attach. Only HTTP and
 * the physical WebSocket boundary are substituted; no gateway binary involved.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fetchAgents, fetchSessions, fetchWorkspace, type Session, type Workspace } from '@/lib/api'
import type { ClientFrame, ServerFrame } from '@/lib/api/generated/asyncapi-types'
import { WsConnection } from '@/lib/ws'
import { reattachActiveSession } from '@/components/chat/OmnipusRuntimeProvider'
import { workspaceEntryBlocksSend } from '@/lib/nav/workspaceEntry'
import { useChatStore } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useUiStore } from './ui'
import { useWorkspacesStore } from './workspacesStore'

vi.mock('@/lib/api', async (original) => ({
  ...await original<typeof import('@/lib/api')>(),
  fetchAgents: vi.fn(),
  fetchSessions: vi.fn(),
  fetchWorkspace: vi.fn(),
}))

const WORKSPACE = 'restore-workspace'
const SESSION = 'remembered-conversation'
const remembered: Session = {
  id: SESSION, agent_id: 'jim', workspace_id: WORKSPACE, type: 'chat',
  title: 'Remember this conversation', message_count: 2,
  created_at: '2026-10-20T00:00:00Z', updated_at: '2026-10-20T00:00:00Z',
}
const workspace: Workspace = {
  id: WORKSPACE, name: 'Restore workspace', revision: 'a'.repeat(64), status: 'active',
  pinned: false, pin_order: 0, task_count: 0, core_team: ['jim'],
  member_configs: { jim: { main_session_id: 'different-main-conversation' } },
  created_at: '2026-10-20T00:00:00Z', updated_at: '2026-10-20T00:00:00Z',
}

class SocketBoundary {
  static OPEN = 1
  static instances: SocketBoundary[] = []
  readyState = 0
  onopen: (() => void) | null = null
  onclose: ((event: { code: number; reason: string }) => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  send = vi.fn<(value: string) => void>()
  constructor() { SocketBoundary.instances.push(this) }
  close() { this.readyState = 3 }
  open() { this.readyState = SocketBoundary.OPEN; this.onopen?.() }
  receive(frame: ServerFrame) { this.onmessage?.(new MessageEvent('message', { data: JSON.stringify(frame) })) }
}

let connection: WsConnection

function sentAttachments(socket: SocketBoundary): ClientFrame[] {
  return socket.send.mock.calls.map(([raw]) => JSON.parse(raw) as ClientFrame)
    .filter((frame) => frame.type === 'attach_session')
}

beforeEach(() => {
  localStorage.clear()
  vi.clearAllMocks()
  vi.stubGlobal('WebSocket', SocketBoundary)
  SocketBoundary.instances = []
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useChatStore.setState(useChatStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useUiStore.setState({ toasts: [] })
  useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE })
  useSessionStore.getState().setWorkspaceSessionDescriptor(WORKSPACE, {
    id: SESSION, type: 'chat', title: remembered.title, agentId: 'jim',
  })
  // Simulate reload: only the browser's persisted pointer remains.
  useSessionStore.setState({ sessionByWorkspace: {} })
  vi.mocked(fetchSessions).mockResolvedValue([remembered])
  vi.mocked(fetchWorkspace).mockResolvedValue(workspace)
  vi.mocked(fetchAgents).mockResolvedValue([])
  connection = new WsConnection({
    onConnected: () => {
      useConnectionStore.getState().setConnected(true)
      reattachActiveSession(connection, useConnectionStore.getState().setConnectionError)
    },
    onDisconnected: () => useConnectionStore.getState().setConnected(false),
    onError: useConnectionStore.getState().setConnectionError,
    onFrames: (frames) => { for (const frame of frames) useChatStore.getState().handleFrame(frame) },
  })
  connection.connect()
  // Same ordering as WsLifecycle: the object is registered BEFORE onopen.
  useConnectionStore.getState().setConnection(connection)
})

afterEach(() => {
  connection.disconnect()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('remembered conversation while the initial socket connects', () => {
  it('retains the exact conversation and attaches it once on open, without asking the user to Retry', async () => {
    const socket = SocketBoundary.instances[0]
    expect(connection.isConnected).toBe(false)

    await useSessionStore.getState().enterWorkspaceChat(WORKSPACE)

    expect(useSessionStore.getState().activeSessionId).toBe(SESSION)
    expect(useSessionStore.getState().activeAgentId).toBe('jim')
    expect(useSessionStore.getState().workspaceEntry).toEqual({ status: 'exact', acknowledged: false })
    expect(useSessionStore.getState().sessionByWorkspace[WORKSPACE]?.id).toBe(SESSION)
    expect(workspaceEntryBlocksSend(useSessionStore.getState(), WORKSPACE)).toBe(false)
    expect(sentAttachments(socket)).toEqual([])
    expect(useUiStore.getState().toasts.some((toast) => toast.message.includes('Could not restore'))).toBe(false)

    socket.open()

    expect(sentAttachments(socket)).toEqual([{ type: 'attach_session', session_id: SESSION }])
    expect(useChatStore.getState().sessionsById[SESSION]?.isReplaying).toBe(true)
    expect(useConnectionStore.getState().connectionError).toBeNull()

    socket.receive({
      type: 'replay_message', session_id: SESSION, id: 'saved-reply', role: 'assistant',
      agent_id: 'jim', content: 'This is the saved conversation.', timestamp: remembered.created_at,
    })
    socket.receive({
      type: 'browser_handover_notice', session_id: SESSION, message_id: 'saved-handover',
      text: 'You took over the browser. Send a message to return control.',
    })
    await vi.waitFor(() => {
      expect(useChatStore.getState().sessionsById[SESSION]?.messageOrder).toEqual(['saved-reply', 'saved-handover'])
    })
    const restored = useChatStore.getState().sessionsById[SESSION].messagesById
    expect(restored['saved-reply'].content).toBe('This is the saved conversation.')
    expect(restored['saved-handover'].content).toBe('You took over the browser. Send a message to return control.')
  })

  it('still refuses an OPEN socket send failure and preserves the previous committed conversation', async () => {
    SocketBoundary.instances[0].open()
    useSessionStore.getState().setActiveSession('previous-chat', 'mia')
    // Preserve the intended remembered destination after seeding the previous chat.
    useSessionStore.getState().setWorkspaceSessionDescriptor(WORKSPACE, {
      id: SESSION, type: 'chat', title: remembered.title, agentId: 'jim',
    })
    const socket = SocketBoundary.instances[0]
    socket.send.mockImplementation(() => { throw new Error('Socket write refused') })

    await useSessionStore.getState().enterWorkspaceChat(WORKSPACE)

    expect(sentAttachments(socket)).toEqual([{ type: 'attach_session', session_id: SESSION }])
    expect(socket.send).toHaveBeenCalledTimes(1)
    expect(useSessionStore.getState().activeSessionId).toBe('previous-chat')
    expect(useSessionStore.getState().workspaceEntry).toMatchObject({
      status: 'failed-attempt', retry: true, fellBack: false,
      attempted: { sessionId: SESSION, sendEnabled: false },
    })
    expect(workspaceEntryBlocksSend(useSessionStore.getState(), WORKSPACE)).toBe(true)
    expect(useConnectionStore.getState().connectionError).toBe(
      'Could not attach to session — connection dropped. Please reconnect and try again.',
    )
  })

  it('does not invent a successful restore when the session list itself fails', async () => {
    vi.mocked(fetchSessions).mockRejectedValue(new Error('Session list unavailable'))

    await useSessionStore.getState().enterWorkspaceChat(WORKSPACE)
    SocketBoundary.instances[0].open()

    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(useSessionStore.getState().workspaceEntry).toMatchObject({ status: 'failed-attempt', retry: true, fellBack: false })
    expect(sentAttachments(SocketBoundary.instances[0])).toEqual([])
    expect(workspaceEntryBlocksSend(useSessionStore.getState(), WORKSPACE)).toBe(true)
  })

  it('attaches normally when the socket was already OPEN before workspace entry', async () => {
    const socket = SocketBoundary.instances[0]
    socket.open()

    await useSessionStore.getState().enterWorkspaceChat(WORKSPACE)

    expect(useSessionStore.getState().activeSessionId).toBe(SESSION)
    expect(useSessionStore.getState().workspaceEntry).toEqual({ status: 'exact', acknowledged: false })
    expect(sentAttachments(socket)).toEqual([{ type: 'attach_session', session_id: SESSION }])
  })
})
