import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useSelectSession } from './useSelectSession'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { Agent, Session, Workspace } from '@/lib/api'

// useSelectSession behavioral coverage.
//
// Ported from SessionPanel.test.tsx's "Q4" describe blocks (workspace scoping
// + open-in-workspace) BEFORE SessionPanel.tsx was deleted as dead code —
// `openSessionPanel` had zero production callers (superseded by SearchModal
// and the Sidebar session accordion, both of which call this hook directly).
// This file is now the only behavioral coverage of useSelectSession.ts.
//
// Traces to: temporal-puzzling-melody.md W2-1
// Traces to: sprint-i-historical-replay-fidelity-spec.md FR-I-014

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

const agents = [
  {
    id: 'agent-chat-1',
    name: 'Chat Agent',
    type: 'core',
    status: 'active',
    description: 'Chat agent',
    color: '#ff0000',
    icon: null,
  },
  {
    id: 'agent-task-1',
    name: 'Task Agent',
    type: 'custom',
    status: 'active',
    description: 'Task agent',
    color: '#00ff00',
    icon: null,
  },
] as unknown as Agent[]

const workspaces = [
  {
    id: 'ws-a',
    name: 'Project A',
    is_default: false,
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2026-04-01T00:00:00Z',
    updated_at: '2026-04-01T00:00:00Z',
  },
  {
    id: 'ws-b',
    name: 'Project B',
    is_default: false,
    status: 'active',
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2026-04-01T00:00:00Z',
    updated_at: '2026-04-01T00:00:00Z',
  },
] as unknown as Workspace[]

function makeSession(overrides: Partial<Session> & { id: string }): Session {
  return {
    agent_id: 'agent-chat-1',
    active_agent_id: 'agent-chat-1',
    title: 'Untitled',
    type: 'chat',
    created_at: '2026-04-01T00:00:00Z',
    updated_at: '2026-04-01T01:00:00Z',
    message_count: 1,
    ...overrides,
  } as Session
}

const attachToSessionSpy = vi.fn()
const setActiveAgentTypeSpy = vi.fn()
const seedSessionTokensSpy = vi.fn()
const setActiveWorkspaceIdSpy = vi.fn()
const onClose = vi.fn()

beforeEach(() => {
  mockNavigate.mockReset()
  attachToSessionSpy.mockReset()
  setActiveAgentTypeSpy.mockReset()
  seedSessionTokensSpy.mockReset()
  setActiveWorkspaceIdSpy.mockReset()
  onClose.mockReset()

  act(() => {
    useSessionStore.setState({
      attachToSession: attachToSessionSpy,
      setActiveAgentType: setActiveAgentTypeSpy,
    } as unknown as Parameters<typeof useSessionStore.setState>[0])
    useChatStore.setState({
      seedSessionTokens: seedSessionTokensSpy,
    } as unknown as Parameters<typeof useChatStore.setState>[0])
    useWorkspacesStore.setState({
      activeWorkspaceId: null,
      setActiveWorkspaceId: setActiveWorkspaceIdSpy,
    })
  })
})

function renderSelectSession() {
  const { result } = renderHook(() => useSelectSession({ agents, workspaces, onClose }))
  return result
}

// ── Workspace-switch branch ────────────────────────────────────────────────

describe('useSelectSession — workspace-switch branch', () => {
  it('sets the active workspace, attaches, closes, and navigates when the session belongs to a DIFFERENT existing workspace', () => {
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-a' })
    })
    const result = renderSelectSession()
    const session = makeSession({ id: 'sess-b', workspace_id: 'ws-b', title: 'In B' })

    act(() => {
      result.current(session)
    })

    expect(setActiveWorkspaceIdSpy).toHaveBeenCalledWith('ws-b')
    expect(attachToSessionSpy).toHaveBeenCalledTimes(1)
    expect(attachToSessionSpy).toHaveBeenCalledWith('sess-b', 'chat', 'In B', 'agent-chat-1')
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/workspaces/$workspaceId/chat',
      params: { workspaceId: 'ws-b' },
    })
  })

  it('does NOT switch workspace when the session belongs to the already-active workspace', () => {
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-a' })
    })
    const result = renderSelectSession()
    const session = makeSession({ id: 'sess-a', workspace_id: 'ws-a', title: 'In A' })

    act(() => {
      result.current(session)
    })

    expect(setActiveWorkspaceIdSpy).not.toHaveBeenCalled()
    expect(attachToSessionSpy).toHaveBeenCalledWith('sess-a', 'chat', 'In A', 'agent-chat-1')
    // Known workspace — still navigates (in-place branch), just without a workspace switch.
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/workspaces/$workspaceId/chat',
      params: { workspaceId: 'ws-a' },
    })
  })
})

// ── In-place branch (same workspace / no active workspace) ─────────────────

describe('useSelectSession — in-place branch', () => {
  it('attaches, closes, and navigates in place when the session already belongs to the active workspace', () => {
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-a' })
    })
    const result = renderSelectSession()
    const session = makeSession({ id: 'sess-a', workspace_id: 'ws-a', title: 'In A' })

    act(() => {
      result.current(session)
    })

    // Already the active workspace — no switch needed.
    expect(setActiveWorkspaceIdSpy).not.toHaveBeenCalled()
    expect(attachToSessionSpy).toHaveBeenCalledWith('sess-a', 'chat', 'In A', 'agent-chat-1')
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/workspaces/$workspaceId/chat',
      params: { workspaceId: 'ws-a' },
    })
  })
})

// ── HIGH bug fix: "Unfiled" sessions must never attach-and-vanish ──────────

describe('useSelectSession — Unfiled session (HIGH: never attach-and-vanish)', () => {
  it('attaches and navigates to the standalone /sessions/$sessionId route when the session has no workspace_id', () => {
    const result = renderSelectSession()
    const session = makeSession({ id: 'sess-orphan', title: 'Orphan Session' })
    // No workspace_id set.

    act(() => {
      result.current(session)
    })

    expect(attachToSessionSpy).toHaveBeenCalledWith('sess-orphan', 'chat', 'Orphan Session', 'agent-chat-1')
    expect(onClose).toHaveBeenCalledTimes(1)
    // Must navigate somewhere the attach is visible — never silently attach with
    // no route rendering it (the previous bug: no navigation call at all).
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/sessions/$sessionId',
      params: { sessionId: 'sess-orphan' },
    })
  })

  it('attaches and navigates to /sessions/$sessionId when workspace_id points at a DELETED workspace', () => {
    const result = renderSelectSession()
    const session = makeSession({
      id: 'sess-deleted-ws',
      workspace_id: 'ws-deleted-and-gone',
      title: 'Deleted Workspace Session',
    })

    act(() => {
      result.current(session)
    })

    // The deleted workspace is not in `workspaces`, so no setActiveWorkspaceId call.
    expect(setActiveWorkspaceIdSpy).not.toHaveBeenCalled()
    expect(attachToSessionSpy).toHaveBeenCalledWith(
      'sess-deleted-ws',
      'chat',
      'Deleted Workspace Session',
      'agent-chat-1',
    )
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/sessions/$sessionId',
      params: { sessionId: 'sess-deleted-ws' },
    })
  })

  it('still navigates to /sessions/$sessionId for an Unfiled session even from a non-chat context (no active workspace)', () => {
    // Regression for the reported symptom: selecting an Unfiled session from a
    // non-chat route (e.g. /agents, /settings) previously attached in the
    // background and called navigate() zero times — the click looked swallowed.
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: null })
    })
    const result = renderSelectSession()
    const session = makeSession({ id: 'sess-swallowed', title: 'Would Have Vanished' })

    act(() => {
      result.current(session)
    })

    expect(mockNavigate).toHaveBeenCalledTimes(1)
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/sessions/$sessionId',
      params: { sessionId: 'sess-swallowed' },
    })
  })
})

// ── attach -> seed-tokens -> set-agent-type sequence ────────────────────────

describe('useSelectSession — attach -> seedSessionTokens -> setActiveAgentType sequence', () => {
  it('seeds the token counter with total_tokens when the session has tokens', () => {
    const result = renderSelectSession()
    const session = makeSession({ id: 'sess-tok', title: 'Tokens', total_tokens: 4200 })

    act(() => {
      result.current(session)
    })

    expect(seedSessionTokensSpy).toHaveBeenCalledWith(4200)
  })

  it('does NOT seed the token counter when total_tokens is 0 or undefined', () => {
    const result = renderSelectSession()

    act(() => {
      result.current(makeSession({ id: 'sess-zero-tok', title: 'Zero', total_tokens: 0 }))
    })
    act(() => {
      result.current(makeSession({ id: 'sess-no-tok', title: 'None' }))
    })

    expect(seedSessionTokensSpy).not.toHaveBeenCalled()
  })

  it('sets activeAgentType from the resolved agent for a chat-type session', () => {
    const result = renderSelectSession()
    const session = makeSession({ id: 'sess-chat', title: 'Chat', agent_id: 'agent-chat-1', active_agent_id: 'agent-chat-1' })

    act(() => {
      result.current(session)
    })

    expect(setActiveAgentTypeSpy).toHaveBeenCalledWith('core')
  })

  it('does NOT set activeAgentType for a task-type session', () => {
    const result = renderSelectSession()
    const session = makeSession({
      id: 'sess-task',
      title: 'Task',
      type: 'task',
      agent_id: 'agent-task-1',
      active_agent_id: 'agent-task-1',
    })

    act(() => {
      result.current(session)
    })

    expect(attachToSessionSpy).toHaveBeenCalledWith('sess-task', 'task', 'Task', 'agent-task-1')
    expect(setActiveAgentTypeSpy).not.toHaveBeenCalled()
  })

  it('calls attachToSession with two different session IDs for two different clicks (differentiation test)', () => {
    const result = renderSelectSession()

    act(() => {
      result.current(makeSession({ id: 'sess-first', title: 'First' }))
    })
    act(() => {
      result.current(makeSession({ id: 'sess-second', title: 'Second' }))
    })

    expect(attachToSessionSpy).toHaveBeenCalledTimes(2)
    expect(attachToSessionSpy.mock.calls[0][0]).toBe('sess-first')
    expect(attachToSessionSpy.mock.calls[1][0]).toBe('sess-second')
    expect(attachToSessionSpy.mock.calls[0][0]).not.toBe(attachToSessionSpy.mock.calls[1][0])
  })
})
