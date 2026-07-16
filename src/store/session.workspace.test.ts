// Unit tests for the workspace-session memory additions to session.ts:
//   - sessionByWorkspace: Record<string, WorkspaceSessionDescriptor | null>
//   - enterWorkspaceChat(workspaceId): restores or freshes per workspace
//
// All tests verify STATE OUTCOMES (not spy call counts) to avoid Zustand
// singleton spy-accumulation issues across tests.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useSessionStore, registerChatResetForReplay } from './session'
import { useWorkspacesStore } from './workspacesStore'
import { useConnectionStore } from './connection'
import { useUiStore } from './ui'

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

describe('setActiveSession — session type preservation (Wave-1 Bug 1 regression)', () => {
  beforeEach(resetAll)

  it('preserves the pre-existing attachedSessionType in sessionByWorkspace instead of falling back to "chat"', () => {
    // BDD: Given attachedSessionType is 'task' (set by a prior attachToSession/
    //   setAttachedContext call for the currently active session),
    //   When setActiveSession is called to switch the active agent while the
    //   session stays conceptually the same (e.g. ChatControls agent picker),
    //   Then the sessionByWorkspace descriptor must record 'task', not 'chat' —
    //   the second set() must not read attachedSessionType AFTER the first
    //   set() has already nulled it out.
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({ attachedSessionType: 'task', attachedTaskTitle: 'My task' })

    useSessionStore.getState().setActiveSession('sess-task-1', 'agent-1', null)

    const descriptor = useSessionStore.getState().sessionByWorkspace['ws-1']
    expect(descriptor?.type).toBe('task')
    expect(descriptor?.id).toBe('sess-task-1')
    // Same stale-read hazard as attachedSessionType above, one field below:
    // the second set()'s updater must use the pre-captured priorTaskTitle,
    // not state.attachedTaskTitle (which the first set() already nulled).
    expect(descriptor?.title).toBe('My task')
  })

  it('falls back to "chat" when there was no prior attachedSessionType', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({ attachedSessionType: null, attachedTaskTitle: null })

    useSessionStore.getState().setActiveSession('sess-plain', 'agent-1', null)

    expect(useSessionStore.getState().sessionByWorkspace['ws-1']?.type).toBe('chat')
  })

  it('still resets attachedSessionType/attachedTaskTitle on the store itself for the new session', () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({ attachedSessionType: 'channel', attachedTaskTitle: 'Old title' })

    useSessionStore.getState().setActiveSession('sess-new', 'agent-1', null)

    // The live attachedSessionType/attachedTaskTitle fields are still cleared
    // (unrelated to the sessionByWorkspace bug) — only the *captured* value
    // used for the sessionByWorkspace descriptor is preserved.
    expect(useSessionStore.getState().attachedSessionType).toBeNull()
  })
})

describe('attachToSession — no bucket wipe on failed send (Wave-1 Bug 2 regression)', () => {
  beforeEach(resetAll)

  it('does NOT reset the chat bucket when connection.send returns false', () => {
    const resetSpy = vi.fn()
    registerChatResetForReplay(resetSpy)

    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    const failingConnection = { send: vi.fn().mockReturnValue(false), close: vi.fn(), isConnected: true }
    useConnectionStore.setState({ connection: failingConnection as never, isConnected: true })

    const result = useSessionStore.getState().attachToSession('sess-fail', 'chat', 'Title', 'agent-1')

    expect(resetSpy).not.toHaveBeenCalled()
    // Connection error must be surfaced.
    expect(useConnectionStore.getState().connectionError).toMatch(/Could not attach/)
    // activeSessionId must NOT have been switched to the failed session.
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    // Round-2 fix: the return value is the caller-facing success signal —
    // useSelectSession (and other callers) key their abort logic off this.
    expect(result).toBe(false)
  })

  it('DOES reset the chat bucket once send is confirmed successful', () => {
    const resetSpy = vi.fn()
    registerChatResetForReplay(resetSpy)

    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    const okConnection = { send: vi.fn().mockReturnValue(true), close: vi.fn(), isConnected: true }
    useConnectionStore.setState({ connection: okConnection as never, isConnected: true })

    const result = useSessionStore.getState().attachToSession('sess-ok', 'chat', 'Title', 'agent-1')

    expect(resetSpy).toHaveBeenCalledWith('sess-ok')
    expect(useSessionStore.getState().activeSessionId).toBe('sess-ok')
    expect(result).toBe(true)
  })
})

describe('attachToSession — WS-down user feedback (fix 6)', () => {
  beforeEach(() => {
    resetAll()
    useUiStore.setState({ toasts: [] })
  })

  it('shows a user-visible toast when there is no WS connection (previously console.warn only)', () => {
    // No connection set up — attachToSession takes the offline branch.
    const result = useSessionStore.getState().attachToSession('sess-offline-toast', 'chat', 'Title', 'agent-1')

    const toasts = useUiStore.getState().toasts
    expect(toasts).toHaveLength(1)
    expect(toasts[0].message.toLowerCase()).toMatch(/not connected|connection/)
    expect(toasts[0].variant).toBe('warning')
    // Offline is a "recorded, will finish later" state, not a failure — the
    // caller should proceed (seed tokens / navigate), unlike the send-false
    // branch above which returns false.
    expect(result).toBe(true)
  })

  it('does NOT show a toast when the WS is connected and the attach succeeds', () => {
    const okConnection = { send: vi.fn().mockReturnValue(true), close: vi.fn(), isConnected: true }
    useConnectionStore.setState({ connection: okConnection as never, isConnected: true })

    const result = useSessionStore.getState().attachToSession('sess-online', 'chat', 'Title', 'agent-1')

    expect(useUiStore.getState().toasts).toHaveLength(0)
    expect(result).toBe(true)
  })

  it('still records activeSessionId (offline path is otherwise unchanged) alongside the new toast', () => {
    useSessionStore.getState().attachToSession('sess-offline-toast-2', 'task', 'Task Title', 'agent-2')

    expect(useSessionStore.getState().activeSessionId).toBe('sess-offline-toast-2')
    expect(useSessionStore.getState().attachedSessionType).toBe('task')
    expect(useUiStore.getState().toasts).toHaveLength(1)
  })
})

describe('pruneSessionDescriptor — round-2 fix: no zombie reattach after delete', () => {
  beforeEach(resetAll)

  it('nulls the sessionByWorkspace entry for a deleted session so entering that workspace does not reattach the dead id', () => {
    // BDD: Given workspace W's stored descriptor points at session S,
    //   When S is deleted (pruneSessionDescriptor(S) is called, e.g. from
    //   SearchModal's deleteMut.onSuccess),
    //   Then entering W afterwards must NOT attach S — it's gone.
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    useSessionStore.setState({
      activeSessionId: 'some-other-session',
      sessionByWorkspace: {
        'ws-1': { id: 'sess-deleted', type: 'chat', title: 'Deleted', agentId: 'agent-1' },
      },
    })

    useSessionStore.getState().pruneSessionDescriptor('sess-deleted')

    // The descriptor is nulled ("explicitly fresh"), not left pointing at the
    // dead id.
    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toBeNull()

    // Entering ws-1 now must start fresh, never attach the dead session id.
    useSessionStore.getState().enterWorkspaceChat('ws-1')
    expect(useSessionStore.getState().activeSessionId).toBeNull()
  })

  it('clears activeSessionId when the deleted session is the currently-attached one', () => {
    useSessionStore.setState({
      activeSessionId: 'sess-current',
      sessionByWorkspace: {
        'ws-1': { id: 'sess-current', type: 'chat', title: 'Current', agentId: 'agent-1' },
      },
    })

    useSessionStore.getState().pruneSessionDescriptor('sess-current')

    expect(useSessionStore.getState().activeSessionId).toBeNull()
  })

  it('does NOT touch activeSessionId when the deleted session is not the active one', () => {
    useSessionStore.setState({
      activeSessionId: 'sess-active',
      sessionByWorkspace: {
        'ws-1': { id: 'sess-other', type: 'chat', title: 'Other', agentId: 'agent-1' },
      },
    })

    useSessionStore.getState().pruneSessionDescriptor('sess-other')

    expect(useSessionStore.getState().activeSessionId).toBe('sess-active')
    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toBeNull()
  })

  it('preserves descriptors for OTHER workspaces untouched', () => {
    useSessionStore.setState({
      activeSessionId: null,
      sessionByWorkspace: {
        'ws-1': { id: 'sess-deleted', type: 'chat', title: 'Deleted', agentId: 'agent-1' },
        'ws-2': { id: 'sess-alive', type: 'task', title: 'Alive', agentId: 'agent-2' },
      },
    })

    useSessionStore.getState().pruneSessionDescriptor('sess-deleted')

    expect(useSessionStore.getState().sessionByWorkspace['ws-1']).toBeNull()
    expect(useSessionStore.getState().sessionByWorkspace['ws-2']).toEqual({
      id: 'sess-alive',
      type: 'task',
      title: 'Alive',
      agentId: 'agent-2',
    })
  })
})
