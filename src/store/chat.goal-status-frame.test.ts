/**
 * chat.goal-status-frame.test.ts — ADR-049 D6/US-12/FR-094/FR-099.
 *
 * `goal_status` is session-scoped (the frame always carries `session_id`,
 * `SESSION_SCOPED_FRAME_TYPES` in chat.ts) — asserts the reducer stores the
 * frame verbatim per-session (mirrors `chat.multisession.test.ts`'s bucket
 * pattern), including every pill state in the ADR-053 8-value enum
 * (queued/active/waiting_on_user/judge_unavailable/re-planning/judging/
 * done/failed — superseding the original 4-value active/
 * paused_judge_unavailable/brake_fired/cleared set, no back-compat), and
 * that a frame for a NON-active session does not leak into the foreground
 * `goalStatus` selector.
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

const SID_A = 'goal-status-test-sid-a'
const SID_B = 'goal-status-test-sid-b'

function makeFrame(overrides: Partial<GoalStatusFrame> = {}): GoalStatusFrame {
  return {
    type: 'goal_status',
    session_id: SID_A,
    condition: 'ship the release',
    round: 3,
    max_rounds: 20,
    latest_reason: 'tests still failing',
    active_loops: 1,
    cap: 16,
    state: 'active',
    ...overrides,
  }
}

function resetStores() {
  act(() => {
    useChatStore.setState({ sessionsById: {}, goalStatus: null, loopStatus: null })
    useSessionStore.setState({ activeSessionId: null, activeAgentId: null, activeAgentType: null })
  })
}

beforeEach(resetStores)

describe('chat handleFrame — goal_status (ADR-049 D6)', () => {
  it('stores the frame verbatim on the target session bucket', () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: SID_A })
      useChatStore.getState().handleFrame(makeFrame())
    })
    const bucket = useChatStore.getState().sessionsById[SID_A]
    expect(bucket.goalStatus).toEqual(makeFrame())
  })

  it('active session goalStatus selector reflects the latest frame', () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: SID_A })
      useChatStore.getState().handleFrame(makeFrame({ round: 5 }))
    })
    expect(useChatStore.getState().goalStatus?.round).toBe(5)
  })

  it('a frame for a non-active session does not leak into the foreground selector', () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: SID_B })
      useChatStore.getState().handleFrame(makeFrame({ session_id: SID_A, round: 7 }))
    })
    // Foreground reflects SID_B's (empty) bucket, not SID_A's frame.
    expect(useChatStore.getState().goalStatus ?? null).toBeNull()
    // But SID_A's own bucket did record it.
    expect(useChatStore.getState().sessionsById[SID_A]?.goalStatus?.round).toBe(7)
  })

  it.each([
    'queued',
    'waiting_on_user',
    'judge_unavailable',
    're-planning',
    'judging',
    'done',
    'failed',
  ] as const)('stores the %s state verbatim (no special-casing in the reducer)', (state) => {
    act(() => {
      useSessionStore.setState({ activeSessionId: SID_A })
      useChatStore.getState().handleFrame(makeFrame({ state }))
    })
    expect(useChatStore.getState().goalStatus?.state).toBe(state)
  })

  it('does not invalidate any query cache (goal_status only feeds the store, unlike plan_status)', () => {
    // Regression guard: goal_status must remain a pure store write — no
    // ['tasks']/['plans'] invalidation belongs to this frame type.
    act(() => {
      useSessionStore.setState({ activeSessionId: SID_A })
      useChatStore.getState().handleFrame(makeFrame())
    })
    expect(useChatStore.getState().sessionsById[SID_A]?.goalStatus).toBeTruthy()
  })
})
