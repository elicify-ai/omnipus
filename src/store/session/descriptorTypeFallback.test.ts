// #1449 round 6 — a fallback must never overwrite a KNOWN, more specific
// workspace descriptor with the generic 'chat'.
//
// The round-5 browser evidence run (5 serial runs of the committed
// narrow-header check, kept under
// /workspace/logs/1449/header/fix5-readiness/) failed 7 of 25
// case-executions, also at LOW load. A deep-linked MAIN session ended up with
// the generic descriptor {type:'chat', title:null}, so isMainSession was
// false, the header's kind label read "Extra chat", and the honest "agent
// names unavailable" warning could never render — the 30s readiness wait was
// structurally unsatisfiable, not slow.
//
// The order under test is the real one, as the kept browser artifacts show:
//   1. the deep-link route attaches the session and writes the REAL
//      descriptor (src/routes/_app/sessions.$sessionId.tsx, Step 3),
//   2. the WS opens and the lifecycle re-asserts the active session
//      (src/components/chat/OmnipusRuntimeProvider.tsx::reattachActiveSession)
//      — or the workspace entry resolves a session the list does not carry
//      (src/store/session/workspaceEntryFlow.ts::applyEntry),
//   3. the stored descriptor must STILL be the main's.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import type { Agent, Session, Workspace } from '@/lib/api'
import { fetchAgents, fetchSessionDetail, fetchSessions, fetchWorkspace } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { reattachActiveSession } from '@/components/chat/OmnipusRuntimeProvider'
import { runWorkspaceEntry } from './workspaceEntryFlow'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn(),
    fetchSessionDetail: vi.fn(),
    fetchSessions: vi.fn(),
    fetchWorkspace: vi.fn(),
  }
})

const WS = 'q81-workspace'
const SID = 'q81-main'
const AGENT = 'q81-agent'
const TITLE = 'Q81 standing main'

function mainSession(): Session {
  return {
    id: SID, agent_id: AGENT, workspace_id: WS, title: TITLE,
    type: 'main', status: 'active', message_count: 0,
    created_at: '2026-10-24T00:00:00Z', updated_at: '2026-10-24T00:00:00Z',
  } as unknown as Session
}

/** A ROOT chat that is NOT the workspace's main — the list's other page. */
function otherChat(): Session {
  return {
    id: 'q81-other', agent_id: AGENT, workspace_id: WS, title: 'Q81 other chat',
    type: 'chat', status: 'active', message_count: 0,
    created_at: '2026-10-24T00:00:00Z', updated_at: '2026-10-24T00:00:00Z',
  } as unknown as Session
}

function workspace(): Workspace {
  return {
    id: WS, name: 'Q81 Workspace', revision: '1'.repeat(64), status: 'active',
    pinned: false, pin_order: 0, task_count: 0,
    created_at: '2026-10-24T00:00:00Z', updated_at: '2026-10-24T00:00:00Z',
    core_team: [AGENT], member_configs: { [AGENT]: { main_session_id: SID } },
  } as unknown as Workspace
}

function agent(): Agent {
  return {
    id: AGENT, name: 'Q81 Reader', type: 'Main', locked: false, figure: 'Robot',
    role: 'general', status: 'active', revision: '0'.repeat(64),
    needs_model: false, max_tool_iterations: 8, max_tool_iterations_source: 'global',
  } as unknown as Agent
}

/** What the deep-link route writes in Step 3: the session's REAL descriptor. */
function routeWroteRealDescriptor(): void {
  useSessionStore.getState().setWorkspaceSessionDescriptor(WS, {
    id: SID, type: 'main', title: TITLE, agentId: AGENT,
  })
}

function connect() {
  const send = vi.fn().mockReturnValue(true)
  useConnectionStore.setState({ connection: { send } as never, isConnected: true })
  return send
}

beforeEach(() => {
  localStorage.clear()
  queryClient.clear()
  useSessionStore.setState({
    activeSessionId: null,
    activeAgentId: null,
    activeAgentType: null,
    attachedSessionType: null,
    attachedTaskTitle: null,
    sessionByWorkspace: {},
    workspaceEntry: null,
    resolvingSessionForWorkspace: {},
  })
  useWorkspacesStore.setState({ activeWorkspaceId: WS })
  useConnectionStore.setState({ connection: null, isConnected: false })
  vi.mocked(fetchSessions).mockResolvedValue([])
  vi.mocked(fetchWorkspace).mockResolvedValue(workspace())
  vi.mocked(fetchAgents).mockResolvedValue([agent()])
  vi.mocked(fetchSessionDetail).mockRejectedValue(new Error('not found'))
})

describe('a known workspace descriptor is never downgraded to the generic chat', () => {
  it('keeps the route-s known MAIN descriptor when the WS reattach re-asserts the session', () => {
    // The route attached the session and wrote the REAL descriptor.
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: AGENT })
    routeWroteRealDescriptor()

    // The WS opens: the lifecycle re-binds the active session.
    const send = connect()
    const attached = reattachActiveSession({ send } as never, () => {})

    expect(attached, 'the reattach never sent the attach frame').toBe(true)
    expect(send, 'the reattach never sent the attach frame').toHaveBeenCalled()
    expect(useSessionStore.getState().sessionByWorkspace[WS]).toMatchObject({
      id: SID, type: 'main', title: TITLE,
    })
  })

  it('keeps the known descriptor when the session is re-asserted without a type', () => {
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: AGENT })
    routeWroteRealDescriptor()

    // The writer itself: no session type, no title — the generic fallback.
    useSessionStore.getState().setActiveSession(SID, AGENT)

    expect(useSessionStore.getState().sessionByWorkspace[WS]).toMatchObject({
      id: SID, type: 'main', title: TITLE,
    })
  })

  it('keeps the known descriptor when the workspace entry resolves a session the list does not carry', async () => {
    // The list carries the OTHER chat; the workspace's MAIN is not on this page.
    vi.mocked(fetchSessions).mockResolvedValue([otherChat()])
    const send = connect()

    // The route's known descriptor for the workspace's MAIN.
    routeWroteRealDescriptor()

    await runWorkspaceEntry(WS)

    expect(send, 'the entry never attached — the path under test did not run').toHaveBeenCalled()
    expect(useSessionStore.getState().sessionByWorkspace[WS]).toMatchObject({
      id: SID, type: 'main', title: TITLE,
    })
  })

  it('still records the generic descriptor for a DIFFERENT session', () => {
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: AGENT })
    routeWroteRealDescriptor()

    useSessionStore.getState().setActiveSession('q81-other', AGENT)

    // The known MAIN must not leak onto another session.
    expect(useSessionStore.getState().sessionByWorkspace[WS]).toMatchObject({
      id: 'q81-other', type: 'chat', title: null,
    })
  })
})

describe('the deep-link order: who writes the descriptor', () => {
  it('the workspace entry does NOT write the descriptor in the deep-link order', async () => {
    // The deep link already attached the session and the route wrote the real
    // descriptor; the entry finds the chat already active and leaves it alone.
    vi.mocked(fetchSessions).mockResolvedValue([mainSession()])
    vi.mocked(fetchAgents).mockRejectedValue(new Error('roster unavailable'))
    const send = connect()
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: AGENT })
    routeWroteRealDescriptor()

    await runWorkspaceEntry(WS, SID)

    expect(send, 'the entry attached — it is not the early-return path').not.toHaveBeenCalled()
    expect(useSessionStore.getState().sessionByWorkspace[WS]).toMatchObject({
      id: SID, type: 'main', title: TITLE,
    })
  })
})
