// Store-level tests for server-confirmed approval resolution and workspace
// scope (UAT 2026-09-14: a resolved approval's dialog stayed stuck and showed
// in other workspaces). See src/store/toolApproval.ts's header for the design.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import {
  useToolApprovalStore,
  isApprovalInScope,
  RESOLVED_APPROVAL_MEMORY,
} from './toolApproval'
import type { WsSessionStateFrame, WsToolApprovalRequiredFrame } from '@/lib/ws'

function requiredFrame(
  id: string,
  extra: Partial<WsToolApprovalRequiredFrame> = {},
): WsToolApprovalRequiredFrame {
  return {
    type: 'tool_approval_required',
    approval_id: id,
    tool_call_id: `call-${id}`,
    tool_name: 'write_file',
    args: { path: 'e3-marker.txt' },
    agent_id: 'agent-x',
    session_id: 'sess-x',
    turn_id: 'turn-x',
    expires_in_ms: 300_000,
    ...extra,
  }
}

function snapshot(
  entries: Array<{ id: string; workspace_id?: string }>,
): WsSessionStateFrame {
  return {
    type: 'session_state',
    user_id: 'user-1',
    emitted_at: new Date().toISOString(),
    pending_approvals: entries.map((e) => ({
      approval_id: e.id,
      session_id: 'sess-x',
      tool_name: 'write_file',
      agent_id: 'agent-x',
      expires_in_ms: 300_000,
      ...(e.workspace_id ? { workspace_id: e.workspace_id } : {}),
    })),
  }
}

const ids = () => useToolApprovalStore.getState().queue.map((a) => a.approvalId)

beforeEach(() => {
  act(() => {
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  })
})

describe('markResolved — a server-confirmed resolution removes the approval for good', () => {
  it('removes the entry from the queue and records the id', () => {
    act(() => {
      useToolApprovalStore.getState().enqueue(requiredFrame('appr-a'))
      useToolApprovalStore.getState().enqueue(requiredFrame('appr-b'))
      useToolApprovalStore.getState().markResolved('appr-a')
    })
    expect(ids()).toEqual(['appr-b'])
    expect(useToolApprovalStore.getState().resolvedIds).toEqual(['appr-a'])
  })

  it('ignores a tool_approval_required frame for an id already resolved (frames crossing)', () => {
    act(() => {
      useToolApprovalStore.getState().markResolved('appr-late')
      useToolApprovalStore.getState().enqueue(requiredFrame('appr-late'))
    })
    expect(ids()).toEqual([])
  })

  it('a session_state snapshot still listing a resolved id neither keeps nor re-creates it', () => {
    act(() => {
      useToolApprovalStore.getState().enqueue(requiredFrame('appr-kept'))
      useToolApprovalStore.getState().enqueue(requiredFrame('appr-stale'))
      useToolApprovalStore.getState().markResolved('appr-stale')
      useToolApprovalStore.getState().markResolved('appr-never-seen')
      // Snapshot built before the resolutions, delivered after them.
      useToolApprovalStore
        .getState()
        .reconcileWithSessionState(
          snapshot([{ id: 'appr-kept' }, { id: 'appr-stale' }, { id: 'appr-never-seen' }]),
        )
    })
    expect(ids()).toEqual(['appr-kept'])
  })

  it('dequeue alone is only a local dismissal: a later snapshot that still lists the id restores it', () => {
    act(() => {
      useToolApprovalStore.getState().enqueue(requiredFrame('appr-closed'))
      useToolApprovalStore.getState().dequeue('appr-closed')
    })
    expect(ids()).toEqual([])
    act(() => {
      useToolApprovalStore.getState().reconcileWithSessionState(snapshot([{ id: 'appr-closed' }]))
    })
    // The agent is still waiting server-side, so it must come back (as a stub).
    expect(ids()).toEqual(['appr-closed'])
    expect(useToolApprovalStore.getState().queue[0].toolCallId).toBe('')
  })

  it('marking the same id twice records it once', () => {
    act(() => {
      useToolApprovalStore.getState().markResolved('appr-dup')
      useToolApprovalStore.getState().markResolved('appr-dup')
    })
    expect(useToolApprovalStore.getState().resolvedIds).toEqual(['appr-dup'])
  })

  it(`remembers at most ${RESOLVED_APPROVAL_MEMORY} ids, evicting the oldest first`, () => {
    const total = RESOLVED_APPROVAL_MEMORY + 5
    act(() => {
      for (let i = 0; i < total; i++) useToolApprovalStore.getState().markResolved(`appr-${i}`)
    })
    const remembered = useToolApprovalStore.getState().resolvedIds
    expect(remembered).toHaveLength(RESOLVED_APPROVAL_MEMORY)
    expect(remembered[0]).toBe('appr-5')
    expect(remembered[remembered.length - 1]).toBe(`appr-${total - 1}`)
  })
})

describe('workspace scope', () => {
  it('a live frame keeps its workspace_id on the queue entry', () => {
    act(() => {
      useToolApprovalStore.getState().enqueue(requiredFrame('appr-ws', { workspace_id: 'ws-a' }))
      useToolApprovalStore.getState().enqueue(requiredFrame('appr-nows'))
    })
    const [withWs, withoutWs] = useToolApprovalStore.getState().queue
    expect(withWs.workspaceId).toBe('ws-a')
    expect(withoutWs.workspaceId).toBeUndefined()
  })

  it('a reconnect stub keeps the snapshot workspace_id', () => {
    act(() => {
      useToolApprovalStore
        .getState()
        .reconcileWithSessionState(snapshot([{ id: 'appr-stub', workspace_id: 'ws-b' }]))
    })
    expect(useToolApprovalStore.getState().queue[0].workspaceId).toBe('ws-b')
  })

  it.each([
    { approvalWs: 'ws-a', active: 'ws-a', shown: true, why: 'same workspace' },
    { approvalWs: 'ws-a', active: 'ws-b', shown: false, why: 'another workspace' },
    { approvalWs: undefined, active: 'ws-b', shown: true, why: 'approval belongs to no workspace' },
    { approvalWs: 'ws-a', active: null, shown: true, why: 'no active workspace (all workspaces)' },
    { approvalWs: undefined, active: null, shown: true, why: 'neither side scoped' },
  ])('isApprovalInScope: $why → $shown', ({ approvalWs, active, shown }) => {
    expect(isApprovalInScope({ workspaceId: approvalWs }, active)).toBe(shown)
  })
})
