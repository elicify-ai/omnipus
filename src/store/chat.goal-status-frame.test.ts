/**
 * chat.goal-status-frame.test.ts — ADR-049 D6/US-12/FR-094/FR-099.
 *
 * `goal_status` is session-scoped (the frame always carries `session_id`,
 * `SESSION_SCOPED_FRAME_TYPES` in chat.ts) — asserts the reducer stores the
 * frame verbatim per-session (mirrors `chat.multisession.test.ts`'s bucket
 * pattern), including every pill state in the ADR-053 9-value enum
 * (queued/active/waiting_on_user/judge_unavailable/re-planning/judging/
 * done/failed/cleared — superseding the original 4-value active/
 * paused_judge_unavailable/brake_fired/cleared set, no back-compat; UAT S3
 * later re-added `cleared` as the 9th value), and that a frame for a
 * NON-active session does not leak into the foreground `goalStatus`
 * selector.
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
    'cleared',
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

// ── Regression coverage: bc66345f follow-up ─────────────────────────────────
//
// bc66345f introduced a stable, unique-per-generation `goal_id` (a correct
// and necessary fix — the multi-goal pill tray cannot disambiguate goals
// without it). Before that fix every frame landed on the shared
// `'_default'` key and simply overwrote it, so `goalPills` never exceeded 1
// entry. After it, nothing evicted an entry, so the map grew WITHOUT BOUND
// — one permanent tombstone per terminated (done/failed/cleared) goal in a
// long-lived session. These tests prove `evictGoalPillsOverCap` bounds the
// map's cardinality regardless of what any component renders (the render
// half of the fix — a terminal pill staying briefly visible, then hiding —
// is covered separately in GoalPillTray.test.tsx).
describe('chat handleFrame — goalPills bound (regression fix, bc66345f follow-up)', () => {
  it('caps goalPills at 20 entries when many sequential terminal goals accumulate — no unbounded growth', () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: SID_A })
      for (let i = 1; i <= 25; i++) {
        useChatStore.getState().handleFrame(makeFrame({ goal_id: `g${i}`, state: 'done', round: i }))
      }
    })
    const pills = useChatStore.getState().sessionsById[SID_A]?.goalPills ?? {}
    // GOAL_PILLS_CAP in chat.ts is 20.
    expect(Object.keys(pills).length).toBeLessThanOrEqual(20)
    // Oldest entries evicted first (insertion order) — g1..g5 are gone,
    // the most recent 20 (g6..g25) survive.
    expect(pills.g1).toBeUndefined()
    expect(pills.g5).toBeUndefined()
    expect(pills.g6).toBeDefined()
    expect(pills.g25).toBeDefined()
  })

  it('never evicts a still-live (non-terminal) goal to make room — evicts the oldest terminal entry first', () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: SID_A })
      // Fill exactly to the cap with terminal goals.
      for (let i = 1; i <= 20; i++) {
        useChatStore.getState().handleFrame(makeFrame({ goal_id: `t${i}`, state: 'done', round: i }))
      }
      // A 21st, still-active goal arrives.
      useChatStore.getState().handleFrame(makeFrame({ goal_id: 'live-1', state: 'active', round: 1 }))
    })
    const pills = useChatStore.getState().sessionsById[SID_A]?.goalPills ?? {}
    expect(Object.keys(pills).length).toBeLessThanOrEqual(20)
    // The new live goal is present...
    expect(pills['live-1']?.state).toBe('active')
    // ...room was made by evicting the OLDEST terminal entry (t1), never a
    // live one.
    expect(pills.t1).toBeUndefined()
    expect(pills.t2).toBeDefined()
  })

  it.each(['done', 'failed', 'cleared'] as const)(
    'treats %s as terminal — eligible for eviction once the map is over cap',
    (state) => {
      act(() => {
        useSessionStore.setState({ activeSessionId: SID_A })
        useChatStore.getState().handleFrame(makeFrame({ goal_id: 'terminal-1', state }))
        // 20 more (non-terminal) goals push the map 1 entry over cap.
        for (let i = 1; i <= 20; i++) {
          useChatStore.getState().handleFrame(makeFrame({ goal_id: `filler${i}`, state: 'active', round: i }))
        }
      })
      const pills = useChatStore.getState().sessionsById[SID_A]?.goalPills ?? {}
      expect(Object.keys(pills).length).toBeLessThanOrEqual(20)
      expect(pills['terminal-1']).toBeUndefined()
    },
  )

  it('does not disturb goalPills while still under cap (verbatim store, no premature eviction)', () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: SID_A })
      useChatStore.getState().handleFrame(makeFrame({ goal_id: 'g1', state: 'done' }))
      useChatStore.getState().handleFrame(makeFrame({ goal_id: 'g2', state: 'active' }))
    })
    const pills = useChatStore.getState().sessionsById[SID_A]?.goalPills ?? {}
    expect(Object.keys(pills)).toHaveLength(2)
    expect(pills.g1?.state).toBe('done')
    expect(pills.g2?.state).toBe('active')
  })
})
