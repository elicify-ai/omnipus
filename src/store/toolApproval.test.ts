// L1 amendment §1.2 R6 / §1.5: one root-scoped queue.
// Unit tests for the toolApproval Zustand store — specifically
// reconcileWithSessionState's reconnect-gap fix.
//
// SUPERSESSION (Train 3, amendment section 1 / bundle B3): the snapshot now
// CARRIES the full request — SessionStatePendingApproval requires tool_call_id,
// turn_id and args — so reconnect restores the complete approvable entry. The
// pre-B3 "honest Deny-only stub" pins are re-aimed to full-restore pins at the
// same strength; the '' sentinel they asserted is retired.
//
// Pure store-level tests (no rendering): given a session_state frame, does
// the QUEUE end up in the right shape?

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useToolApprovalStore } from './toolApproval'
import type { SessionStateFrame, ToolApprovalRequiredFrame } from '@/lib/ws'

beforeEach(() => {
  act(() => {
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  })
})

describe('reconcileWithSessionState — reconnect-gap full restore (bundle B3)', () => {
  it('restores the full entry (toolCallId, turnId, args, segments) from the snapshot for an id absent from the local queue', () => {
    const before = Date.now()
    const frame: SessionStateFrame = {
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [
        {
          approval_id: 'appr-unseen',
          tool_call_id: 'call-unseen',
          turn_id: 'turn-unseen',
          args: { path: 'reports/q4.md', mode: 'append' },
          segments: [{ segment_index: 0, command_text: 'Append to the Q4 report' }],
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
    const restored = queue[0]
    expect(restored.approvalId).toBe('appr-unseen')
    expect(restored.toolName).toBe('request_mount')
    expect(restored.agentId).toBe('agent-jim')
    expect(restored.sessionId).toBe('sess-1')
    // Bundle B3: the snapshot carries the FULL request — the same shape
    // enqueue() builds from a live tool_approval_required frame — so the
    // restored card is fully approvable, never a Deny-only stub.
    expect(restored.toolCallId).toBe('call-unseen')
    expect(restored.turnId).toBe('turn-unseen')
    expect(restored.args).toEqual({ path: 'reports/q4.md', mode: 'append' })
    expect(restored.segments).toEqual([{ segment_index: 0, command_text: 'Append to the Q4 report' }])
    // expires_in_ms is a RELATIVE delta from receipt, not an absolute
    // timestamp — expiresAt must be Date.now() + expires_in_ms, computed at
    // reconcile time, same conversion enqueue() uses for a live frame.
    expect(restored.expiresAt).toBeGreaterThanOrEqual(before + 200_000)
    expect(restored.expiresAt).toBeLessThanOrEqual(after + 200_000)
  })

  it('restores multiple unseen approvals in the same frame', () => {
    const frame: SessionStateFrame = {
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [
        {
          approval_id: 'appr-a',
          tool_call_id: 'call-a',
          turn_id: 'turn-a',
          args: {},
          session_id: 'sess-1',
          tool_name: 'fetch_url',
          agent_id: 'agent-main',
          expires_in_ms: 100_000,
        },
        {
          approval_id: 'appr-b',
          tool_call_id: 'call-b',
          turn_id: 'turn-b',
          args: {},
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
    const frame: SessionStateFrame = {
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [
        {
          approval_id: 'appr-zero',
          tool_call_id: 'call-zero',
          turn_id: 'turn-zero',
          args: {},
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

    const restored = useToolApprovalStore.getState().queue[0]
    expect(restored.expiresAt).toBeGreaterThanOrEqual(before)
    expect(restored.expiresAt).toBeLessThanOrEqual(after)
    // Nowhere near 1970 — the classic "forgot to add Date.now()" bug shape.
    expect(restored.expiresAt).toBeGreaterThan(before - 1000)
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

    const frame: SessionStateFrame = {
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [
        {
          approval_id: 'appr-live',
          tool_call_id: 'call-live',
          turn_id: 'turn-1',
          args: { url: 'https://example.com' },
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

  it('handles a mix in one frame: refresh an existing entry AND restore a newly-reported one, with no duplicates', () => {
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

    const frame: SessionStateFrame = {
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [
        {
          approval_id: 'appr-known',
          tool_call_id: 'call-known',
          turn_id: 'turn-known',
          args: { url: 'https://example.com' },
          session_id: 'sess-1',
          tool_name: 'fetch_url',
          agent_id: 'agent-main',
          expires_in_ms: 180_000,
        },
        {
          approval_id: 'appr-unseen',
          tool_call_id: 'call-unseen',
          turn_id: 'turn-unseen',
          args: { path: 'notes.txt' },
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
    expect(known?.toolCallId).toBe('call-known') // refreshed in place
    // Bundle B3: the newly-reported entry restores its REAL identity from the
    // snapshot — same full-restore contract as a fresh reconnect.
    expect(unseen?.toolCallId).toBe('call-unseen')
    expect(unseen?.turnId).toBe('turn-unseen')
    expect(unseen?.args).toEqual({ path: 'notes.txt' })
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

    const frame: SessionStateFrame = {
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
    const frame: ToolApprovalRequiredFrame = {
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
    const frame: ToolApprovalRequiredFrame = {
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
    const base: ToolApprovalRequiredFrame = {
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


describe('L1 K7 / amendment §1.2 R6 — root/acting/origin metadata', () => {
  it('L1 R6 / FR-092: keeps server-derived root, acting name, origin and room reference on a live entry', () => {
    const frame: ToolApprovalRequiredFrame = {
      type: 'tool_approval_required', approval_id: 'appr-root', tool_call_id: 'call-root',
      tool_name: 'bash', args: { command: 'npm run build' }, agent_id: 'helper',
      session_id: 'helper-session', root_session_id: 'omnipus-main',
      acting_agent_name: 'Setup helper', origin_label: 'for group Sales',
      room_ref: { room_id: 'room-sales', room_name: 'Sales', request_id: 'delivery-1', role: 'request' },
      turn_id: 'turn-root', expires_in_ms: 600_000,
    }
    act(() => useToolApprovalStore.getState().enqueue(frame))
    expect(useToolApprovalStore.getState().queue).toHaveLength(1)
    expect(useToolApprovalStore.getState().queue[0]).toMatchObject({
      sessionId: 'helper-session', rootSessionId: 'omnipus-main', actingAgentName: 'Setup helper',
      originLabel: 'for group Sales', roomRef: frame.room_ref,
      args: { command: 'npm run build' }, toolCallId: 'call-root', turnId: 'turn-root',
    })
  })

  it('L1 R6 / K7: optional root/name/origin survive snapshot hydration alongside the B3-required request identity', () => {
    const frame: SessionStateFrame = {
      type: 'session_state', user_id: 'user-1', emitted_at: '2026-10-14T09:00:00Z',
      pending_approvals: [{
        approval_id: 'appr-root-stub', tool_name: 'bash', agent_id: 'helper', session_id: 'helper-session',
        root_session_id: 'omnipus-main', acting_agent_name: 'Setup helper', origin_label: 'asked by Mia',
        // Bundle B3: the request identity is REQUIRED on the snapshot — the
        // pre-B3 "thin snapshot" literal is a contract violation now.
        tool_call_id: 'call-root-stub', turn_id: 'turn-root-stub', args: { command: 'npm run build' },
        expires_in_ms: 600_000,
      }],
    }
    act(() => useToolApprovalStore.getState().reconcileWithSessionState(frame))
    expect(useToolApprovalStore.getState().queue).toHaveLength(1)
    expect(useToolApprovalStore.getState().queue[0]).toMatchObject({
      rootSessionId: 'omnipus-main', actingAgentName: 'Setup helper', originLabel: 'asked by Mia',
      toolCallId: 'call-root-stub', turnId: 'turn-root-stub', args: { command: 'npm run build' },
    })
  })

  it('L1 R6: duplicate root-routed frame still produces one approval identity', () => {
    const frame: ToolApprovalRequiredFrame = {
      type: 'tool_approval_required', approval_id: 'appr-dup-root', tool_call_id: 'call-root',
      tool_name: 'bash', args: { command: 'npm run build' }, agent_id: 'helper',
      session_id: 'helper-session', root_session_id: 'omnipus-main', acting_agent_name: 'Setup helper',
      turn_id: 'turn-root', expires_in_ms: 600_000,
    }
    act(() => {
      useToolApprovalStore.getState().enqueue(frame)
      useToolApprovalStore.getState().enqueue(frame)
    })
    expect(useToolApprovalStore.getState().queue.map(a => a.approvalId)).toEqual(['appr-dup-root'])
    expect(useToolApprovalStore.getState().queue[0]).toMatchObject({ rootSessionId: 'omnipus-main' })
  })
})
