/**
 * T-02 / T-12 workspace entry through the real store.
 *
 * Oracles: FR-004, BDD-02.1, BDD-02.2, BDD-02.3, datasets N03–N07,
 * founder Q1, foreground commitment step 1 (entry attaches without
 * acknowledgement). Expected values are those oracles, not the current
 * blank-composer path.
 *
 * Input channel (existing APIs, no new wire type):
 * - remembered pointer: localStorage `omnipus.sessionByWorkspace.v1`
 *   and the in-memory sessionByWorkspace map
 * - visibility: fetchSessions()
 *   - id present and workspace_id matches the workspace being entered → visible
 *   - successful list omits the id → deleted
 *   - id present but workspace_id is a different workspace → not visible
 *     (hidden / forbidden — never attach that target)
 *   - fetch rejects → timeout / offline / server-error (one class)
 * - Ava's member: fetchWorkspace(id).member_configs.ava, passed through
 *   the seam. The welcome id below is an opaque seam return, not a
 *   client-built main-session-… id.
 * - applied decision: useSessionStore.workspaceEntry, the resolveWorkspaceEntry
 *   result (exact / welcome / unavailable / failed-attempt)
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Session, Workspace, WorkspaceMemberConfig } from '@/lib/api'
import { useWorkspacesStore } from './workspacesStore'
import { useConnectionStore } from './connection'
import { useUiStore } from './ui'
import { useChatStore } from './chat'

const POINTER_KEY = 'omnipus.sessionByWorkspace.v1'
const WELCOME_ID = 'seam-welcome-ava'
const OWNER = 'mia'

const seam = vi.hoisted(() => {
  const mains = new Map<object, string | undefined>()
  return {
    mains,
    mainSessionIdOfMember: vi.fn((member: object): string | undefined => mains.get(member)),
    isMainSession: vi.fn((_session: unknown): boolean => false),
    sessionAttention: vi.fn((_session: unknown): 'unknown' => 'unknown'),
    attachAckFields: vi.fn((_bound: unknown): Record<string, unknown> => ({})),
  }
})

vi.mock('@/lib/nav/sessionCoreSeam', () => ({
  mainSessionIdOfMember: (member: object) => seam.mainSessionIdOfMember(member),
  isMainSession: (session: unknown) => seam.isMainSession(session),
  sessionAttention: (session: unknown) => seam.sessionAttention(session),
  attachAckFields: (bound: unknown) => seam.attachAckFields(bound),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSessions: vi.fn(),
    fetchWorkspace: vi.fn(),
  }
})

import { fetchSessions, fetchWorkspace } from '@/lib/api'
import { useSessionStore } from './session'

type Descriptor = { id: string; type: Session['type']; title: string | null; agentId: string | null }

function session(partial: {
  id: string
  agent_id: string
  workspace_id: string
  title?: string
  active_agent_id?: string
  updated_at?: string
}): Session {
  return {
    type: 'chat',
    title: partial.title ?? partial.id,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: partial.updated_at ?? '2026-01-01T00:00:00Z',
    message_count: 1,
    ...partial,
  }
}

function workspace(id: string, avaMember: WorkspaceMemberConfig): Workspace {
  return {
    id,
    name: id,
    revision: 'rev-1',
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    member_configs: { ava: avaMember },
  }
}

function resetAll() {
  useSessionStore.setState({
    activeSessionId: null,
    activeAgentId: null,
    activeAgentType: null,
    agentSelectionSource: 'auto',
    agentSelectionWorkspaceId: null,
    attachedSessionType: null,
    attachedTaskTitle: null,
    sessionByWorkspace: {},
    resolvingSessionForWorkspace: {},
    workspaceEntry: undefined,
  } as never)
  useWorkspacesStore.setState({ activeWorkspaceId: null })
  useConnectionStore.setState({
    connection: null,
    isConnected: false,
    connectionError: null,
  })
  useUiStore.setState({ toasts: [] })
  useChatStore.setState({
    pendingFirstSend: null,
    isStreaming: false,
    isReplaying: false,
  })
  seam.mains.clear()
  vi.mocked(fetchSessions).mockReset()
  vi.mocked(fetchWorkspace).mockReset()
  localStorage.clear()
}

function persisted(workspaceId: string): Descriptor | null | undefined {
  const raw = localStorage.getItem(POINTER_KEY)
  if (!raw) return undefined
  const parsed = JSON.parse(raw) as Record<string, Descriptor | null>
  if (!Object.prototype.hasOwnProperty.call(parsed, workspaceId)) return undefined
  return parsed[workspaceId]
}

function remember(workspaceId: string, descriptor: Descriptor | null) {
  useWorkspacesStore.setState({ activeWorkspaceId: workspaceId })
  useSessionStore.getState().setWorkspaceSessionDescriptor(workspaceId, descriptor)
  useSessionStore.setState({
    activeSessionId: null,
    activeAgentId: null,
    attachedSessionType: null,
    attachedTaskTitle: null,
    sessionByWorkspace: {},
    resolvingSessionForWorkspace: {},
  })
  useWorkspacesStore.setState({ activeWorkspaceId: workspaceId })
}

function connection() {
  const send = vi.fn().mockReturnValue(true)
  useConnectionStore.setState({ connection: { send, close: vi.fn(), isConnected: true } as never, isConnected: true })
  return send
}

function framesOf(send: ReturnType<typeof vi.fn>): Array<Record<string, unknown>> {
  return send.mock.calls.map((call) => call[0] as Record<string, unknown>)
}

function primeAva(member: WorkspaceMemberConfig, mainId: string | undefined) {
  seam.mains.set(member, mainId)
}

describe('enterWorkspaceChat uses workspace entry (T-02, N03–N07)', () => {
  beforeEach(resetAll)

  it('BDD-02.1 cold reload restores the exact extra and its immutable owner, not a newer chat or an ack', async () => {
    const ws = 'operations'
    const extra = session({
      id: 'extra-launch',
      agent_id: OWNER,
      active_agent_id: 'jim',
      workspace_id: ws,
      title: 'Launch notes',
      updated_at: '2026-01-01T00:00:00Z',
    })
    const newer = session({
      id: 'newer-other',
      agent_id: 'ava',
      workspace_id: ws,
      updated_at: '2026-06-01T00:00:00Z',
    })
    const member: WorkspaceMemberConfig = {}
    primeAva(member, WELCOME_ID)
    vi.mocked(fetchWorkspace).mockResolvedValue(workspace(ws, member))
    vi.mocked(fetchSessions).mockResolvedValue([newer, extra])
    remember(ws, { id: extra.id, type: 'chat', title: extra.title, agentId: 'jim' })
    const send = connection()

    await useSessionStore.getState().enterWorkspaceChat(ws)

    expect({
      sessionId: useSessionStore.getState().activeSessionId,
      agentId: useSessionStore.getState().activeAgentId,
      pointerId: useSessionStore.getState().sessionByWorkspace[ws]?.id ?? null,
      acknowledged: framesOf(send).some((frame) => frame.ack_attention === true),
      entry: (useSessionStore.getState() as { workspaceEntry?: { status?: string; acknowledged?: boolean } }).workspaceEntry ?? null,
    }).toEqual({
      sessionId: extra.id,
      agentId: OWNER,
      pointerId: extra.id,
      acknowledged: false,
      entry: { status: 'exact', acknowledged: false },
    })
  })

  it('BDD-02.1 login restores the exact extra the same way a cold reload does', async () => {
    const ws = 'operations-login'
    const extra = session({
      id: 'extra-login',
      agent_id: OWNER,
      active_agent_id: 'jim',
      workspace_id: ws,
      title: 'Login extra',
    })
    const member: WorkspaceMemberConfig = {}
    primeAva(member, WELCOME_ID)
    vi.mocked(fetchWorkspace).mockResolvedValue(workspace(ws, member))
    vi.mocked(fetchSessions).mockResolvedValue([extra])
    remember(ws, { id: extra.id, type: 'chat', title: extra.title, agentId: 'jim' })
    const send = connection()

    await useSessionStore.getState().enterWorkspaceChat(ws)

    expect({
      sessionId: useSessionStore.getState().activeSessionId,
      agentId: useSessionStore.getState().activeAgentId,
      acknowledged: framesOf(send).some((frame) => frame.ack_attention === true),
    }).toEqual({
      sessionId: extra.id,
      agentId: OWNER,
      acknowledged: false,
    })
  })

  it('BDD-02.1 a workspace-name click restores the exact main, using the immutable owner rather than a stale pointer agent', async () => {
    const ws = 'operations-name'
    const main = session({
      id: 'main-mia',
      agent_id: OWNER,
      active_agent_id: 'jim',
      workspace_id: ws,
      title: 'Mia main',
    })
    const newer = session({
      id: 'newer-extra',
      agent_id: OWNER,
      workspace_id: ws,
      updated_at: '2026-06-01T00:00:00Z',
    })
    const member: WorkspaceMemberConfig = {}
    primeAva(member, WELCOME_ID)
    vi.mocked(fetchWorkspace).mockResolvedValue(workspace(ws, member))
    vi.mocked(fetchSessions).mockResolvedValue([newer, main])
    useWorkspacesStore.setState({ activeWorkspaceId: ws })
    useSessionStore.getState().setWorkspaceSessionDescriptor(ws, {
      id: main.id,
      type: 'chat',
      title: main.title,
      agentId: 'jim',
    })
    useSessionStore.setState({ activeSessionId: null, activeAgentId: null })
    const send = connection()

    await useSessionStore.getState().enterWorkspaceChat(ws)

    expect({
      sessionId: useSessionStore.getState().activeSessionId,
      agentId: useSessionStore.getState().activeAgentId,
      acknowledged: framesOf(send).some((frame) => frame.ack_attention === true),
    }).toEqual({
      sessionId: main.id,
      agentId: OWNER,
      acknowledged: false,
    })
  })

  it('BDD-02.1 a modal workspace switch restores the exact main and does not acknowledge', async () => {
    const ws = 'operations-modal'
    const main = session({
      id: 'main-modal',
      agent_id: OWNER,
      active_agent_id: 'jim',
      workspace_id: ws,
    })
    const member: WorkspaceMemberConfig = {}
    primeAva(member, WELCOME_ID)
    vi.mocked(fetchWorkspace).mockResolvedValue(workspace(ws, member))
    vi.mocked(fetchSessions).mockResolvedValue([main])
    useWorkspacesStore.setState({ activeWorkspaceId: ws })
    useSessionStore.getState().setWorkspaceSessionDescriptor(ws, {
      id: main.id,
      type: 'chat',
      title: main.title,
      agentId: 'jim',
    })
    useSessionStore.setState({ activeSessionId: null, activeAgentId: 'jim' })
    const send = connection()

    await useSessionStore.getState().enterWorkspaceChat(ws)

    expect({
      sessionId: useSessionStore.getState().activeSessionId,
      agentId: useSessionStore.getState().activeAgentId,
      acknowledged: framesOf(send).some((frame) => frame.ack_attention === true),
    }).toEqual({
      sessionId: main.id,
      agentId: OWNER,
      acknowledged: false,
    })
  })

  it('N04 an absent pointer opens the validated Ava main, not a blank new chat', async () => {
    const ws = 'operations-absent'
    const member: WorkspaceMemberConfig = { heartbeat: { enabled: false } }
    primeAva(member, WELCOME_ID)
    vi.mocked(fetchWorkspace).mockResolvedValue(workspace(ws, member))
    vi.mocked(fetchSessions).mockResolvedValue([])
    useWorkspacesStore.setState({ activeWorkspaceId: ws })
    const send = connection()

    await useSessionStore.getState().enterWorkspaceChat(ws)

    expect({
      sessionId: useSessionStore.getState().activeSessionId,
      agentId: useSessionStore.getState().activeAgentId,
      persisted: persisted(ws),
      acknowledged: framesOf(send).some((frame) => frame.ack_attention === true),
      entry: (useSessionStore.getState() as { workspaceEntry?: unknown }).workspaceEntry ?? null,
    }).toEqual({
      sessionId: WELCOME_ID,
      agentId: 'ava',
      persisted: { id: WELCOME_ID, type: 'chat', title: null, agentId: 'ava' },
      acknowledged: false,
      entry: {
        status: 'welcome',
        sessionId: WELCOME_ID,
        agentId: 'ava',
        sendEnabled: true,
        acknowledged: false,
      },
    })
  })

  it('N04 a persisted null pointer is not a blank new chat; it opens the validated Ava main', async () => {
    const ws = 'operations-null'
    const member: WorkspaceMemberConfig = { heartbeat: { enabled: true, interval_minutes: 30, body: '' } }
    primeAva(member, WELCOME_ID)
    vi.mocked(fetchWorkspace).mockResolvedValue(workspace(ws, member))
    vi.mocked(fetchSessions).mockResolvedValue([])
    remember(ws, null)
    const send = connection()

    await useSessionStore.getState().enterWorkspaceChat(ws)

    expect({
      sessionId: useSessionStore.getState().activeSessionId,
      agentId: useSessionStore.getState().activeAgentId,
      acknowledged: framesOf(send).some((frame) => frame.ack_attention === true),
    }).toEqual({
      sessionId: WELCOME_ID,
      agentId: 'ava',
      acknowledged: false,
    })
  })

  it('N06 a deleted pointer opens the validated Ava main and never reuses the missing id', async () => {
    const ws = 'operations-deleted'
    const member: WorkspaceMemberConfig = {}
    primeAva(member, WELCOME_ID)
    vi.mocked(fetchWorkspace).mockResolvedValue(workspace(ws, member))
    vi.mocked(fetchSessions).mockResolvedValue([
      session({ id: 'someone-else', agent_id: 'jim', workspace_id: ws }),
    ])
    remember(ws, { id: 'deleted-chat', type: 'chat', title: 'Gone', agentId: OWNER })
    const send = connection()

    await useSessionStore.getState().enterWorkspaceChat(ws)

    expect({
      sessionId: useSessionStore.getState().activeSessionId,
      agentId: useSessionStore.getState().activeAgentId,
      attachedDeleted: framesOf(send).some((frame) => frame.session_id === 'deleted-chat'),
      acknowledged: framesOf(send).some((frame) => frame.ack_attention === true),
    }).toEqual({
      sessionId: WELCOME_ID,
      agentId: 'ava',
      attachedDeleted: false,
      acknowledged: false,
    })
  })

  it('N06 a pointer whose session belongs to another workspace is not opened', async () => {
    const ws = 'operations-hidden'
    const hidden = session({
      id: 'hidden-target',
      agent_id: 'judge',
      workspace_id: 'elsewhere',
      title: 'Hidden',
    })
    const member: WorkspaceMemberConfig = {}
    primeAva(member, WELCOME_ID)
    vi.mocked(fetchWorkspace).mockResolvedValue(workspace(ws, member))
    vi.mocked(fetchSessions).mockResolvedValue([hidden])
    remember(ws, { id: hidden.id, type: 'chat', title: hidden.title, agentId: 'judge' })
    const send = connection()

    await useSessionStore.getState().enterWorkspaceChat(ws)

    expect({
      sessionId: useSessionStore.getState().activeSessionId,
      openedHidden: framesOf(send).some((frame) => frame.session_id === hidden.id),
    }).toEqual({
      sessionId: WELCOME_ID,
      openedHidden: false,
    })
  })

  it('N03 with no validated Ava main, entry is unavailable: Retry, no send, no blank chat', async () => {
    const ws = 'operations-empty'
    const member: WorkspaceMemberConfig = {}
    primeAva(member, undefined)
    vi.mocked(fetchWorkspace).mockResolvedValue(workspace(ws, member))
    vi.mocked(fetchSessions).mockResolvedValue([])
    useWorkspacesStore.setState({ activeWorkspaceId: ws })
    const send = connection()

    await useSessionStore.getState().enterWorkspaceChat(ws)
    useChatStore.getState().sendMessage('should not send')

    const messageFrames = framesOf(send).filter((frame) => frame.type === 'message')
    expect({
      sessionId: useSessionStore.getState().activeSessionId,
      messages: messageFrames.length,
      persisted: persisted(ws),
      entry: (useSessionStore.getState() as { workspaceEntry?: unknown }).workspaceEntry ?? null,
    }).toEqual({
      sessionId: null,
      messages: 0,
      persisted: undefined,
      entry: {
        status: 'unavailable',
        sessionId: null,
        sendEnabled: false,
        acknowledged: false,
        retry: true,
      },
    })
  })

  it('N07 a fetch failure keeps the committed chat and the remembered pointer, with Retry and no fallback', async () => {
    const home = 'operations-home'
    const attempted = 'operations-away'
    const committed = session({
      id: 'committed-b',
      agent_id: OWNER,
      workspace_id: home,
      title: 'Committed',
    })
    const member: WorkspaceMemberConfig = {}
    primeAva(member, WELCOME_ID)
    vi.mocked(fetchWorkspace).mockImplementation(async (id: string) => workspace(id, member))
    vi.mocked(fetchSessions).mockRejectedValue(new Error('timeout'))
    useWorkspacesStore.setState({ activeWorkspaceId: home })
    const send = connection()
    useSessionStore.getState().attachToSession(committed.id, 'chat', committed.title, OWNER)
    send.mockClear()
    useUiStore.setState({ toasts: [] })
    localStorage.setItem(POINTER_KEY, JSON.stringify({
      ...JSON.parse(localStorage.getItem(POINTER_KEY) ?? '{}'),
      [attempted]: { id: 'remembered-a', type: 'chat', title: 'A', agentId: OWNER },
    }))
    useSessionStore.setState((state) => ({
      sessionByWorkspace: { ...state.sessionByWorkspace },
    }))
    delete (useSessionStore.getState().sessionByWorkspace as Record<string, unknown>)[attempted]
    useWorkspacesStore.setState({ activeWorkspaceId: attempted })

    await useSessionStore.getState().enterWorkspaceChat(attempted)

    const toasts = useUiStore.getState().toasts.map((toast) => toast.message)
    expect({
      sessionId: useSessionStore.getState().activeSessionId,
      agentId: useSessionStore.getState().activeAgentId,
      homePointer: useSessionStore.getState().sessionByWorkspace[home]?.id ?? null,
      attemptedPointer: persisted(attempted)?.id ?? null,
      fallbackToast: toasts.some((message) => /starting a new/i.test(message)),
      acknowledged: framesOf(send).some((frame) => frame.ack_attention === true),
      entry: (useSessionStore.getState() as { workspaceEntry?: unknown }).workspaceEntry ?? null,
    }).toEqual({
      sessionId: committed.id,
      agentId: OWNER,
      homePointer: committed.id,
      attemptedPointer: 'remembered-a',
      fallbackToast: false,
      acknowledged: false,
      entry: {
        status: 'failed-attempt',
        committed: { sessionId: committed.id, agentId: OWNER },
        attempted: { sessionId: 'remembered-a', sendEnabled: false },
        acknowledged: false,
        retry: true,
        fellBack: false,
      },
    })
  })
})
