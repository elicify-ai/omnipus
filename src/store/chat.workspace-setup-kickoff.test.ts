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
import { useChatStore, buildWorkspaceSetupKickoffContent, getMessages } from './chat'
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
      // Fix 3: kickoffAttemptStatus is also module-level store state now —
      // reset it too (clearStreamingState() above, called before this
      // setState, may itself have just written a 'failed' entry for
      // whatever kickoff was outstanding at the end of the PREVIOUS test).
      kickoffAttemptStatus: {},
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

  it('interpolates a SECOND, different workspace name correctly (not a fixture-frozen constant)', () => {
    expect(buildWorkspaceSetupKickoffContent('Northwind Q3 Rollout')).toBe(
      'The workspace "Northwind Q3 Rollout" was just created. Introduce yourself and interview the user about this workspace\'s purpose so you can determine which agents and skills its team needs, then set up the team.',
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

  it('a DIFFERENT input tuple (workspace/agent) produces a correspondingly different payload — not a fixture-frozen shape', () => {
    const mockSend = connectMock(true)
    let result: boolean | undefined
    act(() => {
      result = useChatStore.getState().sendWorkspaceSetupKickoff({
        workspaceId: 'ws-other-7',
        workspaceName: 'Beta Migration',
        agentId: 'jim',
        agentType: 'core',
      })
    })

    expect(result).toBe(true)
    expect(mockSend).toHaveBeenCalledTimes(1)
    const payload = mockSend.mock.calls[0][0]
    expect(payload).toEqual({
      type: 'message',
      content: buildWorkspaceSetupKickoffContent('Beta Migration'),
      agent_id: 'jim',
      metadata: {
        workspace_id: 'ws-other-7',
        workspace_setup_kickoff: true,
      },
    })
    expect(payload).not.toHaveProperty('session_id')
    // The placeholder bubble is stamped with THIS call's agent, not the
    // other fixture's — proves the payload isn't just a coincidentally-
    // matching hardcoded shape.
    expect(useChatStore.getState().messages[0]).toMatchObject({ agentId: 'jim' })
    expect(useChatStore.getState().pendingKickoff).toEqual({ workspaceId: 'ws-other-7' })
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
  it('fully cleans up when the server rejects the kickoff as a DUPLICATE (already-ran, benign) — informational toast', () => {
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

    // Fix 3: this is the DUPLICATE case (message matches /already/i) — a
    // benign, informational toast pointing at the sessions list, NOT the
    // generic "could not start" warning wording (that's reserved for a
    // GENUINE failure — see the sibling test below).
    const toasts = useUiStore.getState().toasts
    expect(toasts.length).toBeGreaterThanOrEqual(1)
    expect(toasts.some((t) => /workspace setup already ran/i.test(t.message))).toBe(true)
    expect(toasts.some((t) => t.variant === 'default')).toBe(true)
    expect(toasts.some((t) => /could not start the workspace setup interview/i.test(t.message))).toBe(false)

    // A subsequent sendMessage must start a NEW turn — no session_id:'__pending' on the wire.
    mockSend.mockClear()
    act(() => {
      useChatStore.getState().sendMessage('hello')
    })
    expect(mockSend).toHaveBeenCalledTimes(1)
    const payload = mockSend.mock.calls[0][0]
    expect(payload).not.toHaveProperty('session_id')
  })

  it('fully cleans up when the server rejects the kickoff for a GENUINE failure — warning toast, distinct from the duplicate wording', () => {
    connectMock(true)
    act(() => {
      kickoff()
    })

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'error',
        message: 'internal server error setting up the team',
      })
    })

    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(useChatStore.getState().pendingKickoff).toBeNull()

    const toasts = useUiStore.getState().toasts
    expect(toasts.some((t) => /could not start the workspace setup interview/i.test(t.message) && t.variant === 'warning')).toBe(true)
    expect(toasts.some((t) => /already ran/i.test(t.message))).toBe(false)
  })

  it('marks kickoffAttemptStatus "failed" for the rejected workspace (Fix 3)', () => {
    connectMock(true)
    act(() => {
      kickoff()
    })
    act(() => {
      useChatStore.getState().handleFrame({ type: 'error', message: 'boom, try again' })
    })
    expect(useChatStore.getState().kickoffAttemptStatus[WORKSPACE_ID]).toBe('failed')
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

describe('chat store — Fix 1: disconnect terminally clears the kickoff slot', () => {
  it('clearStreamingState (WS disconnect) clears pendingKickoff, drops the orphaned __pending bucket, and marks kickoffAttemptStatus failed', () => {
    connectMock(true)
    act(() => {
      kickoff()
    })
    expect(useChatStore.getState().pendingKickoff).toEqual({ workspaceId: WORKSPACE_ID })
    expect(useChatStore.getState().sessionsById['__pending']).toBeDefined()

    act(() => {
      useConnectionStore.setState({ isConnected: false, connection: null })
      useChatStore.getState().clearStreamingState()
    })

    expect(useChatStore.getState().pendingKickoff).toBeNull()
    expect(useChatStore.getState().sessionsById['__pending']).toBeUndefined()
    expect(useChatStore.getState().kickoffAttemptStatus[WORKSPACE_ID]).toBe('failed')
    // Fix 1: deliberately quiet — no toast, and activeSessionId is left
    // exactly as it was. Resetting it to null on RECONNECT is Fix 2's job
    // (OmnipusRuntimeProvider.reattachActiveSession), not this
    // disconnect-time cleanup.
    expect(useSessionStore.getState().activeSessionId).toBe('__pending')
    expect(useUiStore.getState().toasts).toHaveLength(0)
  })

  it('is a no-op when no kickoff is outstanding', () => {
    connectMock(true)
    act(() => {
      useConnectionStore.setState({ isConnected: false })
      useChatStore.getState().clearStreamingState()
    })
    expect(useChatStore.getState().pendingKickoff).toBeNull()
    expect(useChatStore.getState().kickoffAttemptStatus).toEqual({})
  })
})

describe('chat store — Fix 1: untagged reject after navigation does not misattribute', () => {
  it('clears the slot quietly and does NOT disturb an unrelated conversation that is now foreground', () => {
    connectMock(true)
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE_ID })
    })
    act(() => {
      kickoff()
    })
    expect(useSessionStore.getState().activeSessionId).toBe('__pending')

    // The user navigates away to a different workspace before the kickoff's
    // ack arrives (mirrors the Fix 3 "late session_started ack" test setup).
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-other' })
      useSessionStore.getState().startNewSession()
    })
    expect(useSessionStore.getState().activeSessionId).toBeNull()

    // ...and attaches to a REAL, unrelated, already-streaming conversation
    // there — the foreground this reject must leave completely alone.
    act(() => {
      useSessionStore.getState().attachToSession('sess-foreground', 'chat')
    })
    act(() => {
      useChatStore.getState().appendMessage({
        id: 'fg-msg-1',
        role: 'assistant',
        content: 'still working on it',
        timestamp: '2026-01-01T00:00:00Z',
        status: 'streaming',
        isStreaming: true,
      })
    })
    expect(useSessionStore.getState().activeSessionId).toBe('sess-foreground')

    // The dead kickoff's late reject finally arrives. A kickoff rejection
    // never carries a real session_id (there is no real session behind a
    // rejected kickoff turn), so this is untagged.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'error',
        message: 'workspace already had a team configured',
      })
    })

    // Cleaned up.
    expect(useChatStore.getState().pendingKickoff).toBeNull()
    expect(useChatStore.getState().sessionsById['__pending']).toBeUndefined()
    expect(useChatStore.getState().kickoffAttemptStatus[WORKSPACE_ID]).toBe('failed')
    // No toast — there is nothing on screen to retry FROM; the
    // workspace-open hook is what retries, on next open.
    expect(useUiStore.getState().toasts).toHaveLength(0)
    expect(useConnectionStore.getState().connectionError).toBeNull()

    // The foreground conversation is untouched — not errored, not
    // interrupted, still streaming exactly as it was.
    expect(useSessionStore.getState().activeSessionId).toBe('sess-foreground')
    const fgMsg = useChatStore.getState().messages.find((m) => m.id === 'fg-msg-1')
    expect(fgMsg).toMatchObject({ status: 'streaming', isStreaming: true, content: 'still working on it' })
  })
})

describe('chat store — Fix 1: a session-TAGGED error is never a kickoff reject', () => {
  it('a tagged error frame leaves pendingKickoff untouched and runs the generic C8 handling instead, even while a kickoff is pending', () => {
    const SID = 'ordinary-session-tagged'
    connectMock(true)
    act(() => {
      kickoff()
    })
    expect(useChatStore.getState().pendingKickoff).toEqual({ workspaceId: WORKSPACE_ID })

    act(() => {
      useSessionStore.setState({ activeSessionId: SID })
    })

    act(() => {
      useChatStore.getState().handleFrame({ type: 'error', session_id: SID, message: 'boom' })
    })

    // The kickoff's own state is completely untouched by a frame that
    // carries a real session_id — a kickoff rejection can never be tagged.
    expect(useChatStore.getState().pendingKickoff).toEqual({ workspaceId: WORKSPACE_ID })
    expect(useChatStore.getState().kickoffAttemptStatus[WORKSPACE_ID]).toBeUndefined()
    // Generic C8 error handling ran instead, scoped to SID.
    expect(useConnectionStore.getState().connectionError).toBe('boom')
    // No kickoff toast was raised for this unrelated, tagged error.
    expect(useUiStore.getState().toasts.some((t) => /workspace setup/i.test(t.message))).toBe(false)
  })
})

describe('chat store — Fix 1: collision guard prevents the __pending bucket hijack', () => {
  it('sendMessage enqueues (does not send, does not create a second __pending bucket) while a kickoff is pending', () => {
    const mockSend = connectMock(true)
    act(() => {
      kickoff()
    })
    mockSend.mockClear()

    act(() => {
      useSessionStore.getState().startNewSession()
    })
    expect(useSessionStore.getState().activeSessionId).toBeNull()

    act(() => {
      useChatStore.getState().sendMessage('a plain message')
    })

    expect(mockSend).not.toHaveBeenCalled()
    expect(useChatStore.getState().outboundQueue).toEqual(['a plain message'])
    // No second '__pending' turn was activated.
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    // The kickoff's own bucket is untouched — still exactly its own
    // placeholder, no co-mingled content from the collided send.
    const pendingBucket = useChatStore.getState().sessionsById['__pending']
    expect(pendingBucket).toBeDefined()
    expect(getMessages(pendingBucket!)).toHaveLength(1)
  })

  it('end-to-end: the queued message survives and is sent separately once the kickoff resolves (via the wrong-workspace migration branch)', () => {
    const mockSend = connectMock(true)
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE_ID })
    })
    act(() => {
      kickoff()
    })
    expect(useSessionStore.getState().activeSessionId).toBe('__pending')

    // Navigate to a different workspace with no session yet.
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-other' })
      useSessionStore.getState().startNewSession()
    })
    expect(useSessionStore.getState().activeSessionId).toBeNull()

    mockSend.mockClear()
    act(() => {
      useChatStore.getState().sendMessage('hello from ws-other')
    })
    expect(mockSend).not.toHaveBeenCalled()
    expect(useChatStore.getState().outboundQueue).toContain('hello from ws-other')

    // The kickoff's late ack arrives for the ORIGINATING workspace, which
    // the user has since left — resolved via the "wrong workspace"
    // migration branch (does not foreground), which also drains the queue.
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_started',
        session_id: 'sess-kickoff-real',
        agent_id: AGENT_ID,
      })
    })

    expect(useChatStore.getState().pendingKickoff).toBeNull()
    // The kickoff's real session is correctly recorded under ITS OWN
    // (originating) workspace — no cross-contamination with the plain
    // message's workspace.
    expect(useSessionStore.getState().sessionByWorkspace[WORKSPACE_ID]).toEqual({
      id: 'sess-kickoff-real',
      type: 'chat',
      title: null,
      agentId: AGENT_ID,
    })

    // The plain message survived — released from outboundQueue and sent as
    // an ordinary fresh turn under its OWN new '__pending' bucket, entirely
    // separate from the kickoff's now-migrated session.
    expect(useChatStore.getState().outboundQueue).toEqual([])
    expect(mockSend).toHaveBeenCalledTimes(1)
    const payload = mockSend.mock.calls[0][0]
    expect(payload).toMatchObject({ type: 'message', content: 'hello from ws-other' })
    expect(payload).not.toHaveProperty('session_id')
    expect(useSessionStore.getState().activeSessionId).toBe('__pending')
    // The user's message bubble is present — not lost.
    expect(useChatStore.getState().messages.some((m) => m.role === 'user' && m.content === 'hello from ws-other')).toBe(true)
  })
})
