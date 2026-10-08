/**
 * T-12 atomic selection through the real hook and the real store.
 *
 * Oracles: FR-004, FR-009, FR-011, BDD-02.3, BDD-02.4, BDD-E03, dataset N08.
 * The displayed session, the send destination, and the saved pointer are one
 * tuple. A late or failed attempt must not split it, and the owner is
 * session.agent_id — never active_agent_id.
 *
 * The seam is mocked. Acknowledgement fields are absent on these validating
 * attaches; the shown-commit frames are asserted in the foreground-ack file.
 */
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import type { Agent, Session, Workspace } from '@/lib/api'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useConnectionStore } from '@/store/connection'
import { useUiStore } from '@/store/ui'

const navigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => navigate }
})

vi.mock('@/lib/nav/sessionCoreSeam', () => ({
  mainSessionIdOfMember: () => undefined,
  isMainSession: () => false,
  sessionAttention: () => 'unknown',
  attachAckFields: () => ({ ack_attention: true, observed_bound: 'goal-1' }),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchSessions: vi.fn(), fetchWorkspace: vi.fn() }
})

import { fetchSessions } from '@/lib/api'
import { useSelectSession } from './useSelectSession'

const OWNER = 'mia'
const MUTABLE = 'jim'

function workspace(id: string): Workspace {
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
  }
}

function chat(partial: {
  id: string
  workspace_id: string
  agent_id?: string
  active_agent_id?: string
  title?: string
}): Session {
  return {
    agent_id: partial.agent_id ?? OWNER,
    active_agent_id: partial.active_agent_id ?? MUTABLE,
    title: partial.title ?? partial.id,
    type: 'chat',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    message_count: 1,
    ...partial,
  } as Session
}

const agents = [{ id: OWNER, name: 'Mia', type: 'core' }] as unknown as Agent[]

function resetAll() {
  navigate.mockReset()
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
  })
  useWorkspacesStore.setState({ activeWorkspaceId: null })
  useConnectionStore.setState({ connection: null, isConnected: false, connectionError: null })
  useUiStore.setState({ toasts: [] })
  useChatStore.setState({ isStreaming: false, isReplaying: false, pendingFirstSend: null })
  vi.mocked(fetchSessions).mockReset()
  localStorage.clear()
}

function connect(send: ReturnType<typeof vi.fn>) {
  useConnectionStore.setState({
    connection: { send, close: vi.fn(), isConnected: true } as never,
    isConnected: true,
    connectionError: null,
  })
}

function tuple() {
  const ws = useWorkspacesStore.getState().activeWorkspaceId
  const state = useSessionStore.getState()
  return {
    workspaceId: ws,
    sessionId: state.activeSessionId,
    agentId: state.activeAgentId,
    pointerId: ws ? state.sessionByWorkspace[ws]?.id ?? null : null,
  }
}

function frames(send: ReturnType<typeof vi.fn>): Array<Record<string, unknown>> {
  return send.mock.calls.map((call) => call[0] as Record<string, unknown>)
}

describe('atomic selection (T-12, N08, BDD-02.3, BDD-02.4, E03)', () => {
  beforeEach(resetAll)

  it('a failed cross-workspace attach leaves the committed workspace, session, owner, and pointer together', () => {
    const send = vi.fn().mockReturnValue(true)
    connect(send)
    const home = chat({ id: 'session-a', workspace_id: 'ws-a', agent_id: OWNER, active_agent_id: OWNER })
    const other = chat({ id: 'session-b', workspace_id: 'ws-b' })
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-a' })
    useSessionStore.getState().attachToSession(home.id, 'chat', home.title, OWNER)

    send.mockReturnValue(false)
    const { result } = renderHook(() => useSelectSession({
      agents,
      workspaces: [workspace('ws-a'), workspace('ws-b')],
      onClose: vi.fn(),
    }))
    act(() => result.current(other))

    expect({
      ...tuple(),
      navigated: navigate.mock.calls.length,
      acknowledged: frames(send).some((frame) => frame.ack_attention === true),
    }).toEqual({
      workspaceId: 'ws-a',
      sessionId: home.id,
      agentId: OWNER,
      pointerId: home.id,
      navigated: 0,
      acknowledged: false,
    })
  })

  it('N08 a late A success after B wins does not replace B or acknowledge A', async () => {
    const send = vi.fn().mockReturnValue(true)
    connect(send)
    const committed = chat({ id: 'session-b', workspace_id: 'ws-b', agent_id: OWNER, active_agent_id: OWNER })
    const late = chat({ id: 'session-a', workspace_id: 'ws-a', agent_id: OWNER, active_agent_id: OWNER })
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-b' })
    useSessionStore.getState().attachToSession(committed.id, 'chat', committed.title, OWNER)
    send.mockClear()
    localStorage.setItem('omnipus.sessionByWorkspace.v1', JSON.stringify({
      ...JSON.parse(localStorage.getItem('omnipus.sessionByWorkspace.v1') ?? '{}'),
      'ws-a': { id: late.id, type: 'chat', title: late.title, agentId: OWNER },
    }))

    let release: (value: Session[]) => void = () => {}
    vi.mocked(fetchSessions).mockReturnValue(new Promise((resolve) => {
      release = resolve
    }))
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-a' })
    const entry = useSessionStore.getState().enterWorkspaceChat('ws-a')
    const during = tuple()

    const { result } = renderHook(() => useSelectSession({
      agents,
      workspaces: [workspace('ws-a'), workspace('ws-b')],
      onClose: vi.fn(),
    }))
    act(() => result.current(committed))
    release([late])
    await act(async () => {
      await entry
    })

    expect({
      during,
      after: tuple(),
      acknowledgedA: frames(send).some((frame) => frame.session_id === late.id && frame.ack_attention === true),
    }).toEqual({
      during: {
        workspaceId: 'ws-a',
        sessionId: committed.id,
        agentId: OWNER,
        pointerId: null,
      },
      after: {
        workspaceId: 'ws-b',
        sessionId: committed.id,
        agentId: OWNER,
        pointerId: committed.id,
      },
      acknowledgedA: false,
    })
  })

  it('E03 A to B to A settles on A and the immutable owner, not the mutable agent', () => {
    const send = vi.fn().mockReturnValue(true)
    connect(send)
    const first = chat({ id: 'session-a', workspace_id: 'ws-a', agent_id: OWNER, active_agent_id: MUTABLE })
    const middle = chat({ id: 'session-b', workspace_id: 'ws-a', agent_id: 'ava', active_agent_id: 'ava' })
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-a' })
    const { result } = renderHook(() => useSelectSession({
      agents,
      workspaces: [workspace('ws-a')],
      onClose: vi.fn(),
    }))
    act(() => {
      result.current(first)
      result.current(middle)
      result.current(first)
    })
    act(() => {
      useChatStore.getState().sendMessage('hello from A')
    })
    const message = frames(send).filter((frame) => frame.type === 'message').at(-1)

    expect({
      ...tuple(),
      sendAgent: message?.agent_id ?? null,
      acknowledged: frames(send).some((frame) => frame.ack_attention === true),
    }).toEqual({
      workspaceId: 'ws-a',
      sessionId: first.id,
      agentId: OWNER,
      pointerId: first.id,
      sendAgent: OWNER,
      acknowledged: false,
    })
  })

  it('a settled selection keeps the displayed session, the pointer, and the send destination on the immutable owner', () => {
    const send = vi.fn().mockReturnValue(true)
    connect(send)
    const selected = chat({ id: 'session-a', workspace_id: 'ws-a', agent_id: OWNER, active_agent_id: MUTABLE })
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-a' })
    const { result } = renderHook(() => useSelectSession({
      agents,
      workspaces: [workspace('ws-a')],
      onClose: vi.fn(),
    }))
    act(() => result.current(selected))
    act(() => {
      useChatStore.getState().sendMessage('hello')
    })
    const message = frames(send).filter((frame) => frame.type === 'message').at(-1)

    expect({
      displayed: useSessionStore.getState().activeSessionId,
      pointer: useSessionStore.getState().sessionByWorkspace['ws-a']?.id ?? null,
      sendAgent: message?.agent_id ?? null,
    }).toEqual({
      displayed: selected.id,
      pointer: selected.id,
      sendAgent: OWNER,
    })
  })
})
