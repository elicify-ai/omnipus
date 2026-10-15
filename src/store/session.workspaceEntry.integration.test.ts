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
 * - WU-10 amendment: spec FR-107 / BDD-12.11 / dataset D4; ADR OSA-10 U8
 *   retires Ava-first. The first conversation member's generated main_session_id
 *   is read through the REAL seam; no client-built main-session-… id.
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
const WELCOME_ID = 'opaque-welcome-mia'
const OWNER = 'mia'

// WU-10: real generated member projection replaces the obsolete seam doubles.
import { MIA, JIM, OMNIPUS, WORKER, EXTERNAL_WORKER } from '../../tests/omnipus-l1-ui-red/fixtures'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchSessions: vi.fn(),
    fetchWorkspace: vi.fn(),
    fetchAgents: vi.fn(),
  }
})

import { fetchSessions, fetchWorkspace, fetchAgents } from '@/lib/api'
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

function workspace(id: string, member: WorkspaceMemberConfig): Workspace {
  return {
    id,
    name: id,
    revision: '1'.repeat(64),
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    core_team: ['mia'],
    member_configs: { mia: member },
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
  vi.mocked(fetchAgents).mockReset().mockResolvedValue([MIA, JIM, OMNIPUS, WORKER, EXTERNAL_WORKER])
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

// Historical helper name retained to keep the amendment narrow; the fixture
// now belongs to Mia (first colleague) and uses the generated server field.
function primeAva(member: WorkspaceMemberConfig, mainId: string | undefined) {
  Object.assign(member, { main_session_id: mainId })
}

describe('WU-10 ordered workspace entry — FR-107 / BDD-12.11 / dataset D4', () => {
  beforeEach(resetAll)

  it('BDD-12.11 Default skips hidden Omnipus and opens Mia through the real member field', async () => {
    const ws = 'default'
    vi.mocked(fetchWorkspace).mockResolvedValue({ ...workspace(ws, { main_session_id: WELCOME_ID }),
      is_default: true, core_team: ['omnipus', 'mia'], member_configs: {
        omnipus: { main_session_id: 'opaque-omnipus-main' }, mia: { main_session_id: WELCOME_ID },
      } })
    vi.mocked(fetchSessions).mockResolvedValue([])
    useWorkspacesStore.setState({ activeWorkspaceId: ws })
    const send = connection()
    await useSessionStore.getState().enterWorkspaceChat(ws)
    expect(useSessionStore.getState().activeSessionId).toBe(WELCOME_ID)
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
    expect(framesOf(send)).toEqual([{ type: 'attach_session', session_id: WELCOME_ID }])
  })

  it('FR-107 core_team order beats alphabetic and session recency order', async () => {
    const ws = 'ordered'
    vi.mocked(fetchWorkspace).mockResolvedValue({ ...workspace(ws, {}), core_team: ['jim', 'mia'], member_configs: {
      jim: { main_session_id: 'opaque-jim-first' }, mia: { main_session_id: WELCOME_ID },
    } })
    vi.mocked(fetchSessions).mockResolvedValue([session({ id: WELCOME_ID, agent_id: 'mia', workspace_id: ws, updated_at: '2026-10-13T00:00:00Z' })])
    useWorkspacesStore.setState({ activeWorkspaceId: ws })
    const send = connection()
    await useSessionStore.getState().enterWorkspaceChat(ws)
    expect(useSessionStore.getState().activeAgentId).toBe('jim')
    expect(framesOf(send)).toEqual([{ type: 'attach_session', session_id: 'opaque-jim-first' }])
  })

  it.each([{ coreTeam: [] }, { coreTeam: ['researcher', 'external'] }, { coreTeam: ['omnipus'] }])(
    'BDD-12.11 team %j with no conversation colleague gives the healthy empty state, not Retry', async ({ coreTeam }) => {
      const ws = 'empty-team'
      vi.mocked(fetchWorkspace).mockResolvedValue({ ...workspace(ws, {}), core_team: coreTeam,
        member_configs: { omnipus: { main_session_id: 'opaque-omnipus-main' } } })
      vi.mocked(fetchSessions).mockResolvedValue([])
      useWorkspacesStore.setState({ activeWorkspaceId: ws })
      const send = connection()
      await useSessionStore.getState().enterWorkspaceChat(ws)
      expect(useSessionStore.getState().workspaceEntry).toEqual({
        status: 'empty-team', sessionId: null, sendEnabled: false, setupOffer: true, acknowledged: false,
      })
      expect(useSessionStore.getState().activeSessionId).toBeNull()
      expect(framesOf(send)).toEqual([])
    },
  )

  it('FR-112 failed agent-list loading is Retry with no fabricated fallback or empty-team offer', async () => {
    const ws = 'failed-members'
    vi.mocked(fetchWorkspace).mockResolvedValue(workspace(ws, { main_session_id: WELCOME_ID }))
    vi.mocked(fetchSessions).mockResolvedValue([])
    vi.mocked(fetchAgents).mockRejectedValue(new Error('Agent roster offline'))
    useWorkspacesStore.setState({ activeWorkspaceId: ws })
    const send = connection()
    await useSessionStore.getState().enterWorkspaceChat(ws)
    expect(useSessionStore.getState().workspaceEntry).toEqual({
      status: 'failed-attempt', committed: null, attempted: { sessionId: '', sendEnabled: false },
      acknowledged: false, retry: true, fellBack: false,
    })
    expect(framesOf(send)).toEqual([])
  })

  it('FR-112 a remembered exact session stays usable when the agent roster fails', async () => {
    const ws = 'exact-with-roster-failure'
    const exact = session({ id: 'remembered-exact', agent_id: 'mia', workspace_id: ws })
    vi.mocked(fetchWorkspace).mockResolvedValue(workspace(ws, {}))
    vi.mocked(fetchSessions).mockResolvedValue([exact])
    vi.mocked(fetchAgents).mockRejectedValue(new Error('Agent roster offline'))
    remember(ws, { id: exact.id, type: 'chat', title: exact.title, agentId: 'mia' })
    const send = connection()
    await useSessionStore.getState().enterWorkspaceChat(ws)
    expect(useSessionStore.getState().workspaceEntry).toEqual({ status: 'exact', acknowledged: false })
    expect(framesOf(send)).toEqual([{ type: 'attach_session', session_id: exact.id }])
  })

  it('section 6.3 Workspace entry keeps sending blocked until its member request resolves', async () => {
    const ws = 'members-pending'
    let resolveWorkspace!: (value: Workspace) => void
    vi.mocked(fetchWorkspace).mockReturnValue(new Promise((resolve) => { resolveWorkspace = resolve }))
    vi.mocked(fetchSessions).mockResolvedValue([])
    useWorkspacesStore.setState({ activeWorkspaceId: ws })
    const send = connection()
    const pending = useSessionStore.getState().enterWorkspaceChat(ws)
    useChatStore.getState().sendMessage('must not reach any old destination')
    expect(useSessionStore.getState().resolvingSessionForWorkspace[ws]).toBe(true)
    expect(framesOf(send)).toEqual([])
    resolveWorkspace(workspace(ws, { main_session_id: WELCOME_ID }))
    await pending
    expect(useSessionStore.getState().activeSessionId).toBe(WELCOME_ID)
    expect(framesOf(send)).toEqual([{ type: 'attach_session', session_id: WELCOME_ID }])
  })
})

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
      agent_id: 'jim',
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

  it('N04 an absent pointer opens the validated first colleague main, not a blank new chat', async () => {
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
      agentId: 'mia',
      persisted: { id: WELCOME_ID, type: 'chat', title: null, agentId: 'mia' },
      acknowledged: false,
      entry: {
        status: 'welcome',
        sessionId: WELCOME_ID,
        agentId: 'mia',
        sendEnabled: true,
        acknowledged: false,
      },
    })
  })

  it('N04 a persisted null pointer is not a blank new chat; it opens the validated first colleague main', async () => {
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
      agentId: 'mia',
      acknowledged: false,
    })
  })

  it('N06 a deleted pointer opens the validated first colleague main and never reuses the missing id', async () => {
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
      agentId: 'mia',
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

  it('N03 with no validated first colleague main, entry is unavailable: Retry, no send, no blank chat', async () => {
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
