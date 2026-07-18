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
    })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
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
})
