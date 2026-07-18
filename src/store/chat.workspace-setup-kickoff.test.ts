// chat.workspace-setup-kickoff.test.ts — Unit C: the SPA auto-triggers
// Ava's (or the workspace's core_team lead's) workspace-setup interview the
// first time a freshly-created workspace (server-seeded
// `setup_pending: true`) is opened.
//
// `sendWorkspaceSetupKickoff` reuses `sendMessage`'s no-active-session
// ("new turn") wire mechanics — pending '__pending' placeholder bucket,
// `_validateOutboundFrame`, `isStreaming` — MINUS any user-visible bubble:
// the backend records this turn's kickoff frame as a SYSTEM-role transcript
// entry (a centered pill on replay), not a user message.
//
// Traces to: Unit C spec (SPA auto-triggers Ava's workspace-setup interview
// on a workspace's first open).

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useChatStore, buildWorkspaceSetupKickoffContent } from './chat'
import { useConnectionStore } from './connection'
import { useSessionStore } from './session'
import { useWorkspacesStore } from './workspacesStore'
import { useUiStore } from './ui'

const WORKSPACE_ID = 'ws-fresh-1'
const WORKSPACE_NAME = 'Acme Launch'
const AGENT_ID = 'ava'
const AGENT_TYPE = 'core' as const

function resetStore() {
  act(() => {
    useChatStore.getState().clearStreamingState()
    useChatStore.setState({
      sessionsById: {},
      messages: [],
      isStreaming: false,
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      sessionTokens: 0,
      sessionCost: 0,
      isReplaying: false,
      replayCompletedForSession: null,
      rateLimitEvent: null,
      lastUserMessageAt: null,
      cancelStage: null,
      lastReceivedEventTime: null,
      // Fix 2/3: reset the in-flight-kickoff tracker and the offline
      // queues between tests — zustand's setState merges, so a value left
      // by a prior test in this file would otherwise bleed into the next.
      pendingKickoff: null,
      outboundQueue: [],
      pendingDrainQueue: [],
    })
    useConnectionStore.setState({
      connection: null,
      isConnected: false,
      connectionError: null,
    })
    useSessionStore.setState({
      activeSessionId: null,
      activeAgentId: null,
      activeAgentType: null,
      sessionByWorkspace: {},
    })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
    useUiStore.setState({ toasts: [] })
  })
}

beforeEach(resetStore)

function connectMock(sendReturn = true) {
  const mockSend = vi.fn().mockReturnValue(sendReturn)
  act(() => {
    useConnectionStore.setState({
      connection: { send: mockSend, disconnect: vi.fn(), connect: vi.fn(), isConnected: true } as any,
      isConnected: true,
    })
  })
  return mockSend
}

function kickoff() {
  return useChatStore.getState().sendWorkspaceSetupKickoff({
    workspaceId: WORKSPACE_ID,
    workspaceName: WORKSPACE_NAME,
    agentId: AGENT_ID,
    agentType: AGENT_TYPE,
  })
}

describe('buildWorkspaceSetupKickoffContent', () => {
  it('produces the exact kickoff instruction text with the workspace name interpolated', () => {
    expect(buildWorkspaceSetupKickoffContent('Acme Launch')).toBe(
      'The workspace "Acme Launch" was just created. Introduce yourself and interview the user about this workspace\'s purpose so you can determine which agents and skills its team needs, then set up the team.',
    )
  })
})

describe('chat store — sendWorkspaceSetupKickoff', () => {
  it('sends the exact frame: metadata carries workspace_id + workspace_setup_kickoff:true, no session_id', () => {
    const mockSend = connectMock(true)
    let result: boolean | undefined
    act(() => {
      result = kickoff()
    })

    expect(result).toBe(true)
    expect(mockSend).toHaveBeenCalledTimes(1)
    const payload = mockSend.mock.calls[0][0]
    expect(payload).toEqual({
      type: 'message',
      content: buildWorkspaceSetupKickoffContent(WORKSPACE_NAME),
      agent_id: AGENT_ID,
      metadata: {
        workspace_id: WORKSPACE_ID,
        workspace_setup_kickoff: true,
      },
    })
    expect(payload).not.toHaveProperty('session_id')
  })

  it('appends NO user message bubble — only the streaming assistant placeholder', () => {
    connectMock(true)
    act(() => {
      kickoff()
    })

    const state = useChatStore.getState()
    expect(state.messages.filter((m) => m.role === 'user')).toHaveLength(0)
    expect(state.messages).toHaveLength(1)
    expect(state.messages[0]).toMatchObject({
      role: 'assistant',
      content: '',
      status: 'streaming',
      isStreaming: true,
    })
  })

  it('sets up isStreaming and activates the pending session, like a normal new turn', () => {
    connectMock(true)
    act(() => {
      kickoff()
    })

    const state = useChatStore.getState()
    expect(state.isStreaming).toBe(true)
    expect(useSessionStore.getState().activeSessionId).toBe('__pending')
  })

  it('sets the active agent (id + type) so the composer/thread header show it', () => {
    connectMock(true)
    act(() => {
      kickoff()
    })

    expect(useSessionStore.getState().activeAgentId).toBe(AGENT_ID)
    expect(useSessionStore.getState().activeAgentType).toBe(AGENT_TYPE)
  })

  it('bails (returns false, no send) when disconnected', () => {
    // No connection set — resetStore leaves connection:null, isConnected:false.
    let result: boolean | undefined
    act(() => {
      result = kickoff()
    })

    expect(result).toBe(false)
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(useChatStore.getState().messages).toHaveLength(0)
  })

  it('bails (returns false, no send) when a turn is already streaming', () => {
    const mockSend = connectMock(true)
    act(() => {
      useChatStore.setState({ isStreaming: true })
    })

    let result: boolean | undefined
    act(() => {
      result = kickoff()
    })

    expect(result).toBe(false)
    expect(mockSend).not.toHaveBeenCalled()
  })

  it('bails (returns false, no send) when a conversation already exists (activeSessionId !== null)', () => {
    const mockSend = connectMock(true)
    act(() => {
      useSessionStore.setState({ activeSessionId: 'existing-session-1' })
    })

    let result: boolean | undefined
    act(() => {
      result = kickoff()
    })

    expect(result).toBe(false)
    expect(mockSend).not.toHaveBeenCalled()
    // The existing session must be left completely untouched.
    expect(useSessionStore.getState().activeSessionId).toBe('existing-session-1')
  })

  it('rolls back the placeholder and surfaces a connection error when send() fails', () => {
    const mockSend = connectMock(false)
    let result: boolean | undefined
    act(() => {
      result = kickoff()
    })

    expect(result).toBe(false)
    expect(mockSend).toHaveBeenCalledTimes(1)

    const state = useChatStore.getState()
    expect(state.isStreaming).toBe(false)
    expect(state.messages).toHaveLength(0)
    expect(useConnectionStore.getState().connectionError).toMatch(/could not start/i)
  })

  it('never records the "__pending" sentinel in sessionByWorkspace (Fix 1)', () => {
    connectMock(true)
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE_ID })
    })
    act(() => {
      kickoff()
    })

    expect(useSessionStore.getState().activeSessionId).toBe('__pending')
    expect(WORKSPACE_ID in useSessionStore.getState().sessionByWorkspace).toBe(false)
  })

  it('claims the single in-flight-kickoff slot on send (pendingKickoff)', () => {
    connectMock(true)
    act(() => {
      kickoff()
    })

    expect(useChatStore.getState().pendingKickoff).toEqual({ workspaceId: WORKSPACE_ID })
  })

  it('bails (returns false, no send) when a DIFFERENT kickoff is already in flight', () => {
    const mockSend = connectMock(true)
    act(() => {
      useChatStore.setState({ pendingKickoff: { workspaceId: 'some-other-workspace' } })
    })

    let result: boolean | undefined
    act(() => {
      result = kickoff()
    })

    expect(result).toBe(false)
    expect(mockSend).not.toHaveBeenCalled()
  })
})

describe('chat store — sendMessage never records "__pending" either (Fix 1)', () => {
  it('does NOT write a sessionByWorkspace descriptor for the optimistic "__pending" no-session turn', () => {
    connectMock(true)
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE_ID })
    })

    act(() => {
      useChatStore.getState().sendMessage('hello')
    })

    expect(useSessionStore.getState().activeSessionId).toBe('__pending')
    expect(WORKSPACE_ID in useSessionStore.getState().sessionByWorkspace).toBe(false)
  })
})

describe('chat store — sendWorkspaceSetupKickoff full rollback + retry (Fix 2)', () => {
  it('resets activeSessionId to null and clears pendingKickoff on send failure, unblocking a retry', () => {
    connectMock(false)
    act(() => {
      kickoff()
    })

    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(useChatStore.getState().pendingKickoff).toBeNull()
    expect(useChatStore.getState().sessionsById['__pending']).toBeUndefined()

    // The agent selection made by the failed attempt is retained.
    expect(useSessionStore.getState().activeAgentId).toBe(AGENT_ID)

    // A retry (now that the WS is up) must actually be able to fire — the
    // store's own `activeSessionId !== null` and `pendingKickoff !== null`
    // guards must both be clear.
    const mockSend = connectMock(true)
    let result: boolean | undefined
    act(() => {
      result = kickoff()
    })

    expect(result).toBe(true)
    expect(mockSend).toHaveBeenCalledTimes(1)
  })
})

describe('chat store — sendMessage composer guard for a stuck "__pending" session (Fix 2)', () => {
  it('treats a stuck "__pending" + not-streaming session as no-active-session: fresh turn, no session_id on the wire', () => {
    const mockSend = connectMock(true)
    act(() => {
      useSessionStore.setState({ activeSessionId: '__pending' })
      useChatStore.setState({ isStreaming: false })
    })

    act(() => {
      useChatStore.getState().sendMessage('hello there')
    })

    expect(mockSend).toHaveBeenCalledTimes(1)
    const payload = mockSend.mock.calls[0][0]
    expect(payload).not.toHaveProperty('session_id')
    expect(payload).toMatchObject({ type: 'message', content: 'hello there' })
  })

  it('does NOT apply the guard while the "__pending" session is still streaming (mid-turn steering unaffected)', () => {
    const mockSend = connectMock(true)
    act(() => {
      useSessionStore.setState({ activeSessionId: '__pending' })
      useChatStore.setState({ isStreaming: true })
    })

    act(() => {
      useChatStore.getState().sendMessage('steer me')
    })

    // Mid-turn steering under '__pending' degrades to offline-style
    // buffering (sendMessage's own isStreaming branch) — no send call.
    expect(mockSend).not.toHaveBeenCalled()
    expect(useChatStore.getState().outboundQueue).toContain('steer me')
  })
})

describe('chat store — kickoff rejection via ErrorFrame (Fix 2)', () => {
  it('fully cleans up when the server rejects the kickoff (empty/absent session_id error frame)', () => {
    const mockSend = connectMock(true)
    act(() => {
      kickoff()
    })
    expect(useSessionStore.getState().activeSessionId).toBe('__pending')
    expect(useChatStore.getState().pendingKickoff).toEqual({ workspaceId: WORKSPACE_ID })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'error',
        message: 'workspace setup already in progress',
      })
    })

    // Full cleanup: back to a normal empty composer.
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(useChatStore.getState().pendingKickoff).toBeNull()
    expect(useChatStore.getState().sessionsById['__pending']).toBeUndefined()
    expect(useChatStore.getState().messages).toHaveLength(0)
    expect(useChatStore.getState().isStreaming).toBe(false)

    // Non-blocking — a toast, not a hard error.
    const toasts = useUiStore.getState().toasts
    expect(toasts.length).toBeGreaterThanOrEqual(1)
    expect(toasts.some((t) => /could not start the workspace setup interview/i.test(t.message))).toBe(true)

    // A subsequent sendMessage must start a NEW turn — no session_id:'__pending' on the wire.
    mockSend.mockClear()
    act(() => {
      useChatStore.getState().sendMessage('hello')
    })
    expect(mockSend).toHaveBeenCalledTimes(1)
    const payload = mockSend.mock.calls[0][0]
    expect(payload).not.toHaveProperty('session_id')
  })

  it('does NOT trigger the kickoff-reject cleanup for an ordinary message-turn error (no kickoff pending)', () => {
    const SID = 'ordinary-session-1'
    connectMock(true)
    act(() => {
      useSessionStore.setState({ activeSessionId: SID })
    })

    act(() => {
      useChatStore.getState().handleFrame({ type: 'error', session_id: SID, message: 'boom' })
    })

    // Generic C8 error handling ran instead — no kickoff-specific side effects.
    expect(useChatStore.getState().pendingKickoff).toBeNull()
    expect(useConnectionStore.getState().connectionError).toBe('boom')
  })
})

describe('chat store — late session_started ack across workspaces (Fix 3)', () => {
  it('does NOT foreground the kickoff session when the user navigated to a different workspace before the ack', () => {
    connectMock(true)
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE_ID })
    })
    act(() => {
      kickoff()
    })
    expect(useSessionStore.getState().activeSessionId).toBe('__pending')

    // The user navigates away to a different workspace before the ack
    // arrives — mirrors what WorkspaceTabContainer/enterWorkspaceChat does.
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-other' })
      useSessionStore.getState().startNewSession()
    })
    expect(useSessionStore.getState().activeSessionId).toBeNull()

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_started',
        session_id: 'sess-real-1',
        agent_id: AGENT_ID,
      })
    })

    // The current view (workspace B, no session) is left completely alone.
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(useChatStore.getState().messages).toHaveLength(0)
    expect(useChatStore.getState().isStreaming).toBe(false)

    // The real session is recorded under the ORIGINATING workspace.
    expect(useSessionStore.getState().sessionByWorkspace[WORKSPACE_ID]).toEqual({
      id: 'sess-real-1',
      type: 'chat',
      title: null,
      agentId: AGENT_ID,
    })

    // Bookkeeping cleared / freed regardless of which branch ran.
    expect(useChatStore.getState().pendingKickoff).toBeNull()
    expect(useChatStore.getState().sessionsById['__pending']).toBeUndefined()
  })

  it('forgrounds normally when the ack arrives while the user is still on the originating workspace', () => {
    connectMock(true)
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE_ID })
    })
    act(() => {
      kickoff()
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_started',
        session_id: 'sess-real-2',
        agent_id: AGENT_ID,
      })
    })

    expect(useSessionStore.getState().activeSessionId).toBe('sess-real-2')
    expect(useChatStore.getState().isStreaming).toBe(true)
    expect(useChatStore.getState().pendingKickoff).toBeNull()
    // The (now-migrated) bucket is the streaming assistant placeholder from kickoff.
    expect(useChatStore.getState().messages).toHaveLength(1)
  })

  it('plain sendMessage session_started ack is unaffected (no pendingKickoff involved)', () => {
    const mockSend = connectMock(true)
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE_ID })
    })

    act(() => {
      useChatStore.getState().sendMessage('plain first message')
    })
    expect(mockSend).toHaveBeenCalledTimes(1)

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_started',
        session_id: 'sess-plain-1',
        agent_id: AGENT_ID,
      })
    })

    expect(useSessionStore.getState().activeSessionId).toBe('sess-plain-1')
    // frame.agent_id (AGENT_ID) is what the ack carries — unrelated to the
    // kickoff's own agentId, confirming this path is untouched by Fix 3.
    expect(useSessionStore.getState().sessionByWorkspace[WORKSPACE_ID]).toEqual({
      id: 'sess-plain-1',
      type: 'chat',
      title: null,
      agentId: AGENT_ID,
    })
  })
})
