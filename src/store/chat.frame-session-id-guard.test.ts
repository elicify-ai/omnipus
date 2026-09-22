// chat.frame-session-id-guard.test.ts — ADR-091 WP-E cross-family review finding 18
// (src/store/chat/slices/frames.ts::createFrameSlice.handleFrame).
//
// FR-E-002 / edge case table: "A session-scoped frame arrives with no
// session_id" MUST be dropped with a diagnostic — never filed under the
// active session, and never reassigned to some OTHER session either. Before
// this fix, the missing-session_id check lived only inside the inner
// `targetSid` IIFE and returned `null` from THAT function, not from
// `handleFrame` — so execution kept going into `handleReplayAndStatusFrame`
// and the frame-type switch, where several case arms (`rate_limit`'s
// `targetSid ?? getActiveSid()`, `tool_approval_required`'s unconditional
// `enqueue(frame)`) filed the frame anyway. Worse, `token`/`done` — both
// session-scoped — could be silently reassigned to a pending
// cancellation-ack session (`pendingCancelAckSids`) *before* the drop check
// ran at all, because the cancel-ack disambiguation branch in the IIFE was
// checked first.
//
// This suite pins: the required-session check now runs at the very top of
// handleFrame and returns immediately; the cancel-ack reassignment can no
// longer apply to a session-scoped frame (token/done) even when exactly one
// session has a pending cancel ack outstanding; and every named frame type
// from the finding (done, rate_limit, tool_approval_required,
// session_started) is dropped when session_id is missing.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { useConnectionStore } from './connection'
import { useToolApprovalStore } from './toolApproval'
import { pendingCancelAckSids } from './chat/runtime-state'
import type { WsReceiveFrame } from '@/lib/ws'

const ACTIVE_SID = 'guard-active-session'
const OTHER_SID = 'guard-other-session-with-pending-cancel'

function resetStores() {
  act(() => {
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
    useSessionStore.setState({
      activeSessionId: ACTIVE_SID,
      activeAgentId: null,
      activeAgentType: null,
    })
    useConnectionStore.setState({ connectionError: null })
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  })
  pendingCancelAckSids.clear()
}

beforeEach(resetStores)

describe('chat frame routing — missing session_id is always dropped, never reassigned (finding 18)', () => {
  it('a done frame with no session_id is dropped when no cancel ack is pending', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        stats: { tokens: 1, cost: 0, duration_ms: 1 },
      } as unknown as WsReceiveFrame)
    })
    // Dropped: the active session's bucket must not exist/advance from this frame.
    expect(useChatStore.getState().sessionsById[ACTIVE_SID]).toBeUndefined()
    expect(useConnectionStore.getState().connectionError).toContain('session_id')
  })

  it('a done frame with no session_id is DROPPED, not reassigned, even while exactly one session has a pending cancel ack', () => {
    // Arrange: OTHER_SID has an outstanding cancel — this is the exact
    // scenario the F-S3 cancel-ack disambiguation exists for. Before the
    // fix, an untagged `done` here would be silently attributed to
    // OTHER_SID's bucket (clearing pendingCancelAckSids, and — if a bucket
    // existed — resetting its isReplaying/isStreaming state) because `done`
    // is also a member of CANCEL_ACK_FRAME_TYPES. Session-scoped frames must
    // never be inferred, only routed by their own session_id.
    pendingCancelAckSids.add(OTHER_SID)

    act(() => {
      useChatStore.getState().handleFrame({
        type: 'done',
        stats: { tokens: 1, cost: 0, duration_ms: 1 },
      } as unknown as WsReceiveFrame)
    })

    // The pending cancel-ack tracking for OTHER_SID must be untouched — a
    // real ack for it never arrived, only an untagged frame that must be
    // dropped outright.
    expect(pendingCancelAckSids.has(OTHER_SID)).toBe(true)
    expect(useConnectionStore.getState().connectionError).toContain('session_id')
  })

  it('a rate_limit frame with no session_id is dropped, not filed under the active session', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'rate_limit',
        scope: 'agent',
        resource: 'turns',
        policy_rule: 'sec26',
        retry_after_seconds: 60,
        agent_id: 'mia',
      } as unknown as WsReceiveFrame)
    })
    expect(useChatStore.getState().rateLimitEvent).toBeNull()
    expect(useConnectionStore.getState().connectionError).toContain('session_id')
  })

  it('a tool_approval_required frame with no session_id is dropped, never enqueued', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'tool_approval_required',
        approval_id: 'appr-guard',
        tool_call_id: 'call-guard',
        tool_name: 'write_file',
        args: { path: 'x.txt' },
        agent_id: 'agent-x',
        turn_id: 'turn-x',
        expires_in_ms: 300_000,
      } as unknown as WsReceiveFrame)
    })
    expect(useToolApprovalStore.getState().queue).toHaveLength(0)
    expect(useConnectionStore.getState().connectionError).toContain('session_id')
  })

  it('a session_started frame with no session_id is dropped — never sets activeSessionId or creates a bucket', () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: null, activeAgentId: null, activeAgentType: null })
      useChatStore.getState().handleFrame({
        type: 'session_started',
      } as unknown as WsReceiveFrame)
    })
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(Object.keys(useChatStore.getState().sessionsById)).toHaveLength(0)
    expect(useConnectionStore.getState().connectionError).toContain('session_id')
  })

  it('a properly tagged rate_limit frame is unaffected — still routes normally', () => {
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'rate_limit',
        session_id: ACTIVE_SID,
        scope: 'agent',
        resource: 'turns',
        policy_rule: 'sec26',
        retry_after_seconds: 60,
        agent_id: 'mia',
      } as unknown as WsReceiveFrame)
    })
    expect(useChatStore.getState().rateLimitEvent?.resource).toBe('turns')
    expect(useConnectionStore.getState().connectionError).toBeNull()
    // Sanity: the bucket itself carries the same event.
    expect(useChatStore.getState().sessionsById[ACTIVE_SID]?.rateLimitEvent?.resource).toBe('turns')
  })
})
