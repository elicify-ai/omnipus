// Unit tests for the toolApproval Zustand store — specifically
// reconcileWithSessionState's reconnect-gap fix.
//
// Pure store-level tests (no rendering): given a session_state frame, does
// the QUEUE end up in the right shape? See
// src/components/agents/ToolApprovalModal.readablePreviews.test.tsx for the
// end-to-end proof that a stub entry actually renders the
// "Approval Details Unavailable" card — this file only proves the store side
// of that contract.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useToolApprovalStore } from './toolApproval'
import type { WsSessionStateFrame, WsToolApprovalRequiredFrame } from '@/lib/ws'

beforeEach(() => {
  act(() => {
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  })
})

describe('reconcileWithSessionState — reconnect-gap stub creation', () => {
  it('adds a stub entry for a pending_approvals id absent from the local queue', () => {
    const before = Date.now()
    const frame: WsSessionStateFrame = {
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [
        {
          approval_id: 'appr-unseen',
          session_id: 'sess-1',
          tool_name: 'request_mount',
          agent_id: 'agent-jim',
          expires_in_ms: 200_000,
        },
      ],
      emitted_at: new Date().toISOString(),
    }

    act(() => {
      useToolApprovalStore.getState().reconcileWithSessionState(frame)
    })
    const after = Date.now()

    const queue = useToolApprovalStore.getState().queue
    expect(queue).toHaveLength(1)
    const stub = queue[0]
    expect(stub.approvalId).toBe('appr-unseen')
    expect(stub.toolName).toBe('request_mount')
    expect(stub.agentId).toBe('agent-jim')
    expect(stub.sessionId).toBe('sess-1')
    // The collision-free sentinel ToolApprovalModal.tsx's isReconnectStub
    // reads — see reconcileWithSessionState's comment for why '' can never
    // collide with a genuine live tool_approval_required frame.
    expect(stub.toolCallId).toBe('')
    expect(stub.turnId).toBe('')
    expect(stub.args).toEqual({})
    // expires_in_ms is a RELATIVE delta from receipt, not an absolute
    // timestamp — expiresAt must be Date.now() + expires_in_ms, computed at
    // reconcile time, same conversion enqueue() uses for a live frame.
    expect(stub.expiresAt).toBeGreaterThanOrEqual(before + 200_000)
    expect(stub.expiresAt).toBeLessThanOrEqual(after + 200_000)
  })

  it('adds stubs for multiple unseen approvals in the same frame', () => {
    const frame: WsSessionStateFrame = {
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [
        {
          approval_id: 'appr-a',
          session_id: 'sess-1',
          tool_name: 'fetch_url',
          agent_id: 'agent-main',
          expires_in_ms: 100_000,
        },
        {
          approval_id: 'appr-b',
          session_id: 'sess-1',
          tool_name: 'bash',
          agent_id: 'agent-main',
          expires_in_ms: 150_000,
        },
      ],
      emitted_at: new Date().toISOString(),
    }

    act(() => {
      useToolApprovalStore.getState().reconcileWithSessionState(frame)
    })

    const queue = useToolApprovalStore.getState().queue
    expect(queue.map((a) => a.approvalId).sort()).toEqual(['appr-a', 'appr-b'])
  })

  it('a delta of 0 lands expiresAt at (approximately) now, not the 1970 epoch', () => {
    // Guards the relative-vs-absolute conversion specifically: a bug that
    // used s.expires_in_ms directly as expiresAt (instead of
    // Date.now() + s.expires_in_ms) would still "work" for large deltas by
    // accident-of-magnitude, but would be unmistakably wrong at 0.
    const before = Date.now()
    const frame: WsSessionStateFrame = {
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [
        {
          approval_id: 'appr-zero',
          session_id: 'sess-1',
          tool_name: 'request_mount',
          agent_id: 'agent-jim',
          expires_in_ms: 0,
        },
      ],
      emitted_at: new Date().toISOString(),
    }

    act(() => {
      useToolApprovalStore.getState().reconcileWithSessionState(frame)
    })
    const after = Date.now()

    const stub = useToolApprovalStore.getState().queue[0]
    expect(stub.expiresAt).toBeGreaterThanOrEqual(before)
    expect(stub.expiresAt).toBeLessThanOrEqual(after)
    // Nowhere near 1970 — the classic "forgot to add Date.now()" bug shape.
    expect(stub.expiresAt).toBeGreaterThan(before - 1000)
  })
})

describe('reconcileWithSessionState — refresh, not duplicate, for an approval already queued', () => {
  it('refreshes expiresAt in place rather than adding a second entry when the same id arrives again', () => {
    const oldExpiresAt = Date.now() + 5_000
    act(() => {
      useToolApprovalStore.setState({
        queue: [
          {
            approvalId: 'appr-live',
            toolCallId: 'call-live',
            toolName: 'fetch_url',
            args: { url: 'https://example.com' },
            agentId: 'agent-main',
            sessionId: 'sess-1',
            turnId: 'turn-1',
            expiresAt: oldExpiresAt,
          },
        ],
      })
    })

    const frame: WsSessionStateFrame = {
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [
        {
          approval_id: 'appr-live',
          session_id: 'sess-1',
          tool_name: 'fetch_url',
          agent_id: 'agent-main',
          expires_in_ms: 250_000,
        },
      ],
      emitted_at: new Date().toISOString(),
    }

    const before = Date.now()
    act(() => {
      useToolApprovalStore.getState().reconcileWithSessionState(frame)
    })
    const after = Date.now()

    const queue = useToolApprovalStore.getState().queue
    // Still exactly one entry for this id — reconnecting must never duplicate it.
    expect(queue).toHaveLength(1)
    expect(queue[0].approvalId).toBe('appr-live')
    // The full original live-frame data (args, toolCallId, turnId) survives —
    // this is the REFRESH path (spread the existing entry), not a stub
    // replacing it; a stub would have wiped args/toolCallId/turnId.
    expect(queue[0].toolCallId).toBe('call-live')
    expect(queue[0].turnId).toBe('turn-1')
    expect(queue[0].args).toEqual({ url: 'https://example.com' })
    // expiresAt refreshed to the NEW server-reported delta, not left stale
    // and not re-stubbed.
    expect(queue[0].expiresAt).toBeGreaterThanOrEqual(before + 250_000)
    expect(queue[0].expiresAt).toBeLessThanOrEqual(after + 250_000)
    expect(queue[0].expiresAt).toBeGreaterThan(oldExpiresAt)
  })

  it('handles a mix in one frame: refresh an existing entry AND stub a newly-reported one, with no duplicates', () => {
    act(() => {
      useToolApprovalStore.setState({
        queue: [
          {
            approvalId: 'appr-known',
            toolCallId: 'call-known',
            toolName: 'fetch_url',
            args: { url: 'https://example.com' },
            agentId: 'agent-main',
            sessionId: 'sess-1',
            turnId: 'turn-known',
            expiresAt: Date.now() + 5_000,
          },
        ],
      })
    })

    const frame: WsSessionStateFrame = {
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [
        {
          approval_id: 'appr-known',
          session_id: 'sess-1',
          tool_name: 'fetch_url',
          agent_id: 'agent-main',
          expires_in_ms: 180_000,
        },
        {
          approval_id: 'appr-unseen',
          session_id: 'sess-1',
          tool_name: 'request_mount',
          agent_id: 'agent-jim',
          expires_in_ms: 200_000,
        },
      ],
      emitted_at: new Date().toISOString(),
    }

    act(() => {
      useToolApprovalStore.getState().reconcileWithSessionState(frame)
    })

    const queue = useToolApprovalStore.getState().queue
    expect(queue).toHaveLength(2)
    const known = queue.find((a) => a.approvalId === 'appr-known')
    const unseen = queue.find((a) => a.approvalId === 'appr-unseen')
    expect(known?.toolCallId).toBe('call-known') // refreshed, not stubbed
    expect(unseen?.toolCallId).toBe('') // stubbed, not a live frame
  })

  it('still removes a queued approval the server no longer reports as pending', () => {
    // Pre-existing behaviour (unchanged by the reconnect-gap fix) — guards
    // against a regression where "add stubs" accidentally stops pruning.
    act(() => {
      useToolApprovalStore.setState({
        queue: [
          {
            approvalId: 'appr-resolved-elsewhere',
            toolCallId: 'call-x',
            toolName: 'fetch_url',
            args: {},
            agentId: 'agent-main',
            sessionId: 'sess-1',
            turnId: 'turn-x',
            expiresAt: Date.now() + 5_000,
          },
        ],
      })
    })

    const frame: WsSessionStateFrame = {
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [],
      emitted_at: new Date().toISOString(),
    }

    act(() => {
      useToolApprovalStore.getState().reconcileWithSessionState(frame)
    })

    expect(useToolApprovalStore.getState().queue).toHaveLength(0)
  })
})

// ADR-092 D4/FR-025: CommandSegmentInfo carried on a live
// tool_approval_required frame must land on the queue entry — this is the
// store-side half of the per-segment display proof; the render-side half
// lives in ToolApprovalModal.scopeAndSegments.test.tsx.
describe('enqueue — carries segments through (ADR-092 D4)', () => {
  it('copies frame.segments onto the new queue entry', () => {
    const frame: WsToolApprovalRequiredFrame = {
      type: 'tool_approval_required',
      approval_id: 'appr-chain-1',
      tool_call_id: 'call-chain-1',
      tool_name: 'bash',
      args: { command: 'npm run build && git push' },
      agent_id: 'agent-main',
      session_id: 'sess-1',
      turn_id: 'turn-1',
      expires_in_ms: 120_000,
      segments: [
        { segment_index: 0, command_text: 'npm run build', resolved_binary: 'npm' },
        { segment_index: 1, command_text: 'git push', resolved_binary: 'git' },
      ],
    }

    act(() => {
      useToolApprovalStore.getState().enqueue(frame)
    })

    const entry = useToolApprovalStore.getState().queue[0]
    expect(entry.segments).toEqual(frame.segments)
  })

  it('leaves segments undefined when the frame carried none', () => {
    const frame: WsToolApprovalRequiredFrame = {
      type: 'tool_approval_required',
      approval_id: 'appr-plain-1',
      tool_call_id: 'call-plain-1',
      tool_name: 'fetch_url',
      args: { url: 'https://example.com' },
      agent_id: 'agent-main',
      session_id: 'sess-1',
      turn_id: 'turn-2',
      expires_in_ms: 120_000,
    }

    act(() => {
      useToolApprovalStore.getState().enqueue(frame)
    })

    expect(useToolApprovalStore.getState().queue[0].segments).toBeUndefined()
  })

  it('updates segments in place when a duplicate frame for the same approval arrives', () => {
    const base: WsToolApprovalRequiredFrame = {
      type: 'tool_approval_required',
      approval_id: 'appr-dup-1',
      tool_call_id: 'call-dup-1',
      tool_name: 'bash',
      args: { command: 'rm -rf /tmp/x' },
      agent_id: 'agent-main',
      session_id: 'sess-1',
      turn_id: 'turn-3',
      expires_in_ms: 120_000,
    }
    act(() => {
      useToolApprovalStore.getState().enqueue(base)
    })
    expect(useToolApprovalStore.getState().queue).toHaveLength(1)
    expect(useToolApprovalStore.getState().queue[0].segments).toBeUndefined()

    act(() => {
      useToolApprovalStore.getState().enqueue({
        ...base,
        segments: [{ segment_index: 0, command_text: 'rm -rf /tmp/x', resolved_binary: 'rm' }],
      })
    })

    expect(useToolApprovalStore.getState().queue).toHaveLength(1)
    expect(useToolApprovalStore.getState().queue[0].segments).toEqual([
      { segment_index: 0, command_text: 'rm -rf /tmp/x', resolved_binary: 'rm' },
    ])
  })
})
