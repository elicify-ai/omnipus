// Unit tests for the workspace-session memory additions to session.ts:
//   - sessionByWorkspace: Record<string, WorkspaceSessionDescriptor | null>
//   - enterWorkspaceChat(workspaceId): restores or freshes per workspace
//
// All tests verify STATE OUTCOMES (not spy call counts) to avoid Zustand
// singleton spy-accumulation issues across tests.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useSessionStore } from './session'
import { useWorkspacesStore } from './workspacesStore'
import { useConnectionStore } from './connection'

// ── Helpers ───────────────────────────────────────────────────────────────────

function resetAll() {
  // Merge-only reset (no replace=true) to preserve Zustand action references.
  useSessionStore.setState({
    activeSessionId: null,
    activeAgentId: null,
    activeAgentType: null,
    attachedSessionType: null,
    attachedTaskTitle: null,
    sessionByWorkspace: {},
  })
  useWorkspacesStore.setState({
    activeWorkspaceId: null,
    activeMilestoneId: null,
    boardAltitude: 'top-level',
  })
  useConnectionStore.setState({
    connection: null,
    isConnected: false,
    connectionError: null,
    reconnectPhase: null,
    reconnectAttempt: 0,
    liteMode: false,
  })
}

function makeMockConnection() {
  return {
    send: vi.fn().mockReturnValue(true),
    close: vi.fn(),
    isConnected: true,
  }
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('sessionByWorkspace — written by attachToSession', () => {
  beforeEach(resetAll)

  it('records the session under the active workspace when WS is connected', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useConnectionStore.setState({ connection: makeMockConnection() as never, isConnected: true })

    useSessionStore.getState().attachToSession('sess-abc', 'chat', 'My chat', 'agent-1')

    const map = useSessionStore.getState().sessionByWorkspace
    expect(map['ws-1']).toEqual({
      id: 'sess-abc',
      type: 'chat',
      title: 'My chat',
      agentId: 'agent-1',
    })
  })

  it('records the session under the active workspace when WS is offline', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-2' })
    // No connection

    useSessionStore.getState().attachToSession('sess-offline', 'task', 'Task title', 'agent-2')

    const map = useSessionStore.getState().sessionByWorkspace
    expect(map['ws-2']).toEqual({
      id: 'sess-offline',
      type: 'task',
      title: 'Task title',
      agentId: 'agent-2',
    })
  })

  it('does NOT write to sessionByWorkspace when activeWorkspaceId is null', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: null })
    useConnectionStore.setState({ connection: makeMockConnection() as never, isConnected: true })

    useSessionStore.getState().attachToSession('sess-xyz', 'chat', undefined, undefined)

    expect(Object.keys(useSessionStore.getState().sessionByWorkspace)).toHaveLength(0)
  })
})

describe('sessionByWorkspace — written by startNewSession', () => {
  beforeEach(resetAll)

  it('writes null for the active workspace when starting a new session', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })

    useSessionStore.getState().startNewSession()

    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toBeNull()
  })

  it('does not write when there is no active workspace', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: null })
    useSessionStore.getState().startNewSession()

    expect(Object.keys(useSessionStore.getState().sessionByWorkspace)).toHaveLength(0)
  })
})

describe('enterWorkspaceChat — first visit (no descriptor)', () => {
  beforeEach(resetAll)

  it('calls startNewSession when activeSessionId is non-null on first visit', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    // Simulate a session from a different context being active
    useSessionStore.setState({ activeSessionId: 'other-sess', sessionByWorkspace: {} })

    useSessionStore.getState().enterWorkspaceChat('ws-1')

    // startNewSession sets activeSessionId to null
    expect(useSessionStore.getState().activeSessionId).toBeNull()
  })

  it('is a no-op (activeSessionId stays null) when already null on first visit', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({ activeSessionId: null, sessionByWorkspace: {} })

    useSessionStore.getState().enterWorkspaceChat('ws-1')

    // No startNewSession call should have occurred — activeSessionId stays null
    // and sessionByWorkspace still has no entry for ws-1 (startNewSession would
    // have written null for ws-1 if it was called).
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    // startNewSession would record null for ws-1 in sessionByWorkspace, so if it
    // was called, ws-1 would be in the map. Since it should NOT be called, it stays absent.
    expect('ws-1' in useSessionStore.getState().sessionByWorkspace).toBe(false)
  })
})

describe('enterWorkspaceChat — explicit fresh (descriptor is null)', () => {
  beforeEach(resetAll)

  it('clears active session when the workspace was previously freshly started and session is active', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({
      activeSessionId: 'some-session',
      sessionByWorkspace: { 'ws-1': null },
    })

    useSessionStore.getState().enterWorkspaceChat('ws-1')

    // startNewSession clears activeSessionId
    expect(useSessionStore.getState().activeSessionId).toBeNull()
  })

  it('is a no-op (activeSessionId stays null) when already null and workspace explicitly freshly started', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-fresh' })
    useSessionStore.setState({
      activeSessionId: null,
      sessionByWorkspace: { 'ws-fresh': null },
    })

    useSessionStore.getState().enterWorkspaceChat('ws-fresh')

    // Still null, no change
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    // The descriptor for ws-fresh stays null (not changed by a no-op)
    expect(useSessionStore.getState().sessionByWorkspace['ws-fresh']).toBeNull()
  })
})

describe('enterWorkspaceChat — restore stored session', () => {
  beforeEach(resetAll)

  it('attaches to the stored session when descriptor.id differs from activeSessionId', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    // No WS connection — uses the offline path of attachToSession
    useSessionStore.setState({
      activeSessionId: null,
      sessionByWorkspace: {
        'ws-1': { id: 'prev-sess', type: 'chat', title: 'Previous chat', agentId: 'agent-1' },
      },
    })

    useSessionStore.getState().enterWorkspaceChat('ws-1')

    // attachToSession (offline path) sets activeSessionId to the stored session's id
    expect(useSessionStore.getState().activeSessionId).toBe('prev-sess')
  })

  it('is a no-op when descriptor.id already matches activeSessionId (Bug 1: tab switch within workspace)', () => {
    // BDD: Given workspace ws-1 with session sess-123 active,
    //   When the user switches to the Board tab and back to Chat
    //   (enterWorkspaceChat fires again),
    //   Then neither startNewSession nor attachToSession is called —
    //   activeSessionId stays 'sess-123'.
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({
      activeSessionId: 'sess-123',
      sessionByWorkspace: {
        'ws-1': { id: 'sess-123', type: 'chat', title: 'Still here', agentId: 'agent-1' },
      },
    })

    useSessionStore.getState().enterWorkspaceChat('ws-1')

    // Session must be preserved — no change
    expect(useSessionStore.getState().activeSessionId).toBe('sess-123')
    // sessionByWorkspace must not be mutated
    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toEqual({
      id: 'sess-123',
      type: 'chat',
      title: 'Still here',
      agentId: 'agent-1',
    })
  })
})

describe('enterWorkspaceChat — workspace switch restores correct session', () => {
  beforeEach(resetAll)

  it('switches to ws-2 stored session when entering ws-2', () => {
    // No WS connection — offline path
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-2' })
    useSessionStore.setState({
      activeSessionId: 'sess-ws1',
      sessionByWorkspace: {
        'ws-1': { id: 'sess-ws1', type: 'chat', title: 'WS1 chat', agentId: 'mia' },
        'ws-2': { id: 'sess-ws2', type: 'chat', title: 'WS2 chat', agentId: 'jim' },
      },
    })

    useSessionStore.getState().enterWorkspaceChat('ws-2')

    // Should be on ws-2's session now
    expect(useSessionStore.getState().activeSessionId).toBe('sess-ws2')
  })
})

describe('setWorkspaceSessionDescriptor — explicit descriptor write', () => {
  beforeEach(resetAll)

  it('writes the descriptor under the given workspaceId regardless of activeWorkspaceId', () => {
    // BDD: Given activeWorkspaceId is null (user not currently on a workspace),
    //   When setWorkspaceSessionDescriptor is called with an explicit wsId and descriptor,
    //   Then sessionByWorkspace[wsId] holds that descriptor.
    //
    // This is the race-free handoff: the deep-link session route writes the
    // descriptor by explicit key BEFORE navigate(), so WorkspaceTabContainer's
    // enterWorkspaceChat fires AFTER the descriptor is already in place.
    useWorkspacesStore.setState({ activeWorkspaceId: null })

    useSessionStore.getState().setWorkspaceSessionDescriptor('ws-1', {
      id: 'sess-deep-link',
      type: 'chat',
      title: 'Deep-linked chat',
      agentId: 'mia',
    })

    const descriptor = useSessionStore.getState().sessionByWorkspace['ws-1']
    expect(descriptor).toEqual({
      id: 'sess-deep-link',
      type: 'chat',
      title: 'Deep-linked chat',
      agentId: 'mia',
    })
  })

  it('enterWorkspaceChat is a no-op after setWorkspaceSessionDescriptor when activeSessionId matches', () => {
    // BDD: Given a deep-link route that:
    //   1. Sets activeSessionId to A (via setActiveSession or attachToSession)
    //   2. Calls setWorkspaceSessionDescriptor('ws-1', { id: A, ... })
    //   3. Navigates to /workspaces/ws-1/chat
    //   When WorkspaceTabContainer mounts and calls enterWorkspaceChat('ws-1'),
    //   Then descriptor.id === activeSessionId → no-op (never calls startNewSession).
    useWorkspacesStore.setState({ activeWorkspaceId: null })
    useSessionStore.setState({ activeSessionId: 'sess-A' })

    // Step 2: deep-link route writes the descriptor before navigate.
    useSessionStore.getState().setWorkspaceSessionDescriptor('ws-1', {
      id: 'sess-A',
      type: 'chat',
      title: null,
      agentId: 'mia',
    })

    // Update workspace (simulating setActiveWorkspaceId call in the route effect).
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })

    // Step 3: enterWorkspaceChat fires on WorkspaceTabContainer mount.
    useSessionStore.getState().enterWorkspaceChat('ws-1')

    // activeSessionId must remain 'sess-A' — no startNewSession fired.
    expect(useSessionStore.getState().activeSessionId).toBe('sess-A')
    // The descriptor must be unchanged.
    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toEqual({
      id: 'sess-A',
      type: 'chat',
      title: null,
      agentId: 'mia',
    })
  })

  it('can write null to mark a workspace as fresh (used by startNewSession callers)', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-5' })
    useSessionStore.setState({
      sessionByWorkspace: {
        'ws-5': { id: 'old-sess', type: 'chat', title: null, agentId: null },
      },
    })

    useSessionStore.getState().setWorkspaceSessionDescriptor('ws-5', null)

    expect(useSessionStore.getState().sessionByWorkspace['ws-5']).toBeNull()
  })

  it('preserves other workspace descriptors when writing for a specific workspace', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: null })
    useSessionStore.setState({
      sessionByWorkspace: {
        'ws-other': { id: 'sess-other', type: 'chat', title: 'Other', agentId: 'jim' },
      },
    })

    useSessionStore.getState().setWorkspaceSessionDescriptor('ws-new', {
      id: 'sess-new',
      type: 'task',
      title: 'My task',
      agentId: 'ava',
    })

    expect(useSessionStore.getState().sessionByWorkspace['ws-other']).toEqual({
      id: 'sess-other',
      type: 'chat',
      title: 'Other',
      agentId: 'jim',
    })
    expect(useSessionStore.getState().sessionByWorkspace['ws-new']).toEqual({
      id: 'sess-new',
      type: 'task',
      title: 'My task',
      agentId: 'ava',
    })
  })
})
