// Q-T3 — founder ruling Q1 A (2026-10-27): selecting a chat through the
// sidebar / chooser / search must land on an address that NAMES the chat.
//
// The founder's expected behaviour: "Chat selection via sidebar/chooser/search
// (src/components/chat/useSelectSession.ts::selectSession, both known-workspace
// branches) navigates to /workspaces/$workspaceId/chat WITH ?open=<session id>,
// the same single-navigation contract as the session deep link (#1437,
// b3314e748), so a browser reload reopens the chat the user was in."
//
// The oracle is #1437's own Expected clause, mirrored here:
//   "A chat link, and a browser reload on it, always opens exactly that chat
//    (in its workspace), never the agent's default chat."
// The deep-link route already satisfies it (src/routes/_app/sessions.
// $sessionId.tsx::SessionRoute writes `search: { open: session.id }`); the
// sidebar/chooser/search path did not, so a reload after picking a chat in the
// sidebar returned to the workspace's REMEMBERED chat instead.
//
// Both known-workspace branches are covered: the workspace-switch branch and
// the in-place branch. The Unfiled branch is deliberately NOT changed - it has
// no workspace to name and keeps the standalone /sessions/$sessionId route.
//
// Real hook, real stores, real router seam. Only the router's navigate is a
// boundary double; the session, workspace, agent and store objects are real.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useSelectSession } from './useSelectSession'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import type { Agent, Session, Workspace } from '@/lib/api'

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => mockNavigate }
})

const agents = [
  { id: 'agent-chat-1', name: 'Chat Agent', type: 'core', role: 'general', status: 'active', figure: 'Omnipus', description: 'Chat agent', color: '#8E9196', icon: null },
] as unknown as Agent[]

const workspaces = [
  { id: 'ws-a', name: 'Project A', is_default: false, status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-04-01T00:00:00Z', updated_at: '2026-04-01T00:00:00Z' },
  { id: 'ws-b', name: 'Project B', is_default: false, status: 'active', pinned: false, pin_order: 0, task_count: 0, created_at: '2026-04-01T00:00:00Z', updated_at: '2026-04-01T00:00:00Z' },
] as unknown as Workspace[]

function makeSession(overrides: Partial<Session> & { id: string }): Session {
  return {
    agent_id: 'agent-chat-1', active_agent_id: 'agent-chat-1', title: 'Untitled', type: 'chat',
    created_at: '2026-04-01T00:00:00Z', updated_at: '2026-04-01T00:00:00Z', message_count: 1,
    ...overrides,
  } as Session
}

const attachToSessionSpy = vi.fn()
const setActiveAgentTypeSpy = vi.fn()
const setActiveSessionSpy = vi.fn()
const seedSessionTokensSpy = vi.fn()
const setActiveWorkspaceIdSpy = vi.fn()
const addToastSpy = vi.fn()
const onClose = vi.fn()

beforeEach(() => {
  mockNavigate.mockReset()
  attachToSessionSpy.mockReset()
  attachToSessionSpy.mockReturnValue(true)
  setActiveAgentTypeSpy.mockReset()
  setActiveSessionSpy.mockReset()
  seedSessionTokensSpy.mockReset()
  setActiveWorkspaceIdSpy.mockReset()
  addToastSpy.mockReset()
  onClose.mockReset()
  act(() => {
    useSessionStore.setState({
      attachToSession: attachToSessionSpy,
      setActiveAgentType: setActiveAgentTypeSpy,
      setActiveSession: setActiveSessionSpy,
    } as unknown as Parameters<typeof useSessionStore.setState>[0])
    useChatStore.setState({ seedSessionTokens: seedSessionTokensSpy } as unknown as Parameters<typeof useChatStore.setState>[0])
    useWorkspacesStore.setState({ activeWorkspaceId: null, setActiveWorkspaceId: setActiveWorkspaceIdSpy })
    useUiStore.setState({ addToast: addToastSpy } as unknown as Parameters<typeof useUiStore.setState>[0])
  })
})

function renderSelectSession() {
  return renderHook(() => useSelectSession({ agents, workspaces, onClose })).result
}

// #1437 Expected, mirrored: the destination address must NAME the selected
// chat, so a browser reload of that address reopens exactly that chat.
function expectOpenTarget(sessionId: string, workspaceId: string) {
  expect(mockNavigate, 'one selection is one navigation').toHaveBeenCalledTimes(1)
  expect(mockNavigate).toHaveBeenCalledWith({
    to: '/workspaces/$workspaceId/chat',
    params: { workspaceId },
    search: { open: sessionId },
  })
}

describe('Q-T3 — a chat chosen in the sidebar/chooser/search is named in the address (#1437 contract)', () => {
  it('in-place branch: the address names the selected chat, so a reload reopens it', () => {
    act(() => { useWorkspacesStore.setState({ activeWorkspaceId: 'ws-a' }) })
    const result = renderSelectSession()
    act(() => { result.current(makeSession({ id: 'sess-in-place', workspace_id: 'ws-a', title: 'In A' })) })
    expectOpenTarget('sess-in-place', 'ws-a')
  })

  it('workspace-switch branch: the address names the selected chat, so a reload reopens it', () => {
    act(() => { useWorkspacesStore.setState({ activeWorkspaceId: 'ws-a' }) })
    const result = renderSelectSession()
    act(() => { result.current(makeSession({ id: 'sess-switched', workspace_id: 'ws-b', title: 'In B' })) })
    expectOpenTarget('sess-switched', 'ws-b')
  })

  it('negative: an Unfiled session keeps the standalone session route and never claims a workspace chat', () => {
    const result = renderSelectSession()
    act(() => { result.current(makeSession({ id: 'sess-unfiled', title: 'Unfiled' })) })
    expect(mockNavigate).toHaveBeenCalledTimes(1)
    expect(mockNavigate).toHaveBeenCalledWith({ to: '/sessions/$sessionId', params: { sessionId: 'sess-unfiled' } })
  })
})
