/**
 * chat.plan-status-frame.test.ts — ADR-049 R1/R3/FR-099/US-10 AS-7.
 *
 * `plan_status` is a GLOBAL frame (no `session_id` on the wire — correlated
 * by `plan_id` instead, R3), unlike `goal_status`/`loop_status`. Asserts the
 * reducer invalidates `['plans']`/`['tasks']` (the "owner disabled surfaces
 * paused" scenario is driven entirely by the refetched `Plan.paused_reason`
 * field — PlansFilterBand already renders it, see planStateColors.ts's
 * `planSecondaryChipLabel` — this reducer's only job is triggering that
 * refetch) and does NOT touch any chat-store session bucket.
 *
 * 14-reviewer sign-off Finding #1 (HIGH, refetch storm): `plan_status` is
 * broadcast to every client on every plan MUTATION — plan execution can emit
 * dozens of these in quick succession. The handler now (a) DEBOUNCES bursts
 * into one invalidation pass per ~1s trailing-edge window instead of firing
 * per frame, and (b) SCOPES a precise, plan-id-targeted invalidation
 * (immediate refetch) alongside the broad `['plans']`/`['tasks']` prefixes,
 * which now use `refetchType: 'none'` (stale-mark only, no forced network
 * round trip — the existing 15s polls pick up the fresh state). These tests
 * advance fake timers past `PLAN_STATUS_INVALIDATE_DEBOUNCE_MS` (1000ms,
 * mirrored here rather than imported so a change to the constant is a
 * deliberate, visible test edit) before asserting on `invalidateQueries`.
 *
 * Traces to: `plan_status` (literal — NOT `plan_status_changed`, R3).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { queryClient } from '@/lib/queryClient'
import type { PlanStatusFrame } from '@/lib/api/generated/asyncapi-types'

// Mirrors the private `PLAN_STATUS_INVALIDATE_DEBOUNCE_MS` in chat.ts — kept
// as a local literal (not exported from chat.ts) so these tests fail loudly
// if the debounce window is ever widened without a matching test update.
const DEBOUNCE_MS = 1000

function makeFrame(overrides: Partial<PlanStatusFrame> = {}): PlanStatusFrame {
  return {
    type: 'plan_status',
    plan_id: 'plan-1',
    state: 'running',
    plan_phase: 'dispatching',
    progress: 0.4,
    ...overrides,
  }
}

beforeEach(() => {
  vi.useFakeTimers()
  act(() => {
    useChatStore.setState({ sessionsById: {} })
    useSessionStore.setState({ activeSessionId: null, activeAgentId: null, activeAgentType: null })
  })
})

afterEach(() => {
  // Flush any pending debounce timer this test left running (a fresh test
  // with real timers next would otherwise never fire it) before restoring
  // real timers.
  act(() => {
    vi.runOnlyPendingTimers()
  })
  vi.useRealTimers()
})

describe('chat handleFrame — plan_status (ADR-049 R1/R3)', () => {
  it('invalidates the plans query cache once the debounce window elapses', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
    })
    // Nothing fires synchronously — the debounce hasn't elapsed yet.
    expect(invalidateSpy).not.toHaveBeenCalled()

    act(() => {
      vi.advanceTimersByTime(DEBOUNCE_MS)
    })
    expect(invalidateSpy).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ['plans'], refetchType: 'none' }),
    )
  })

  it('invalidates the tasks query cache (a plan_status change can affect member tasks)', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
      vi.advanceTimersByTime(DEBOUNCE_MS)
    })
    expect(invalidateSpy).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ['tasks'], refetchType: 'none' }),
    )
  })

  it('also fires a precise, plan-id-scoped predicate invalidation (immediate refetch, not refetchType: none)', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    act(() => {
      useChatStore.getState().handleFrame(makeFrame({ plan_id: 'plan-scoped-1' }))
      vi.advanceTimersByTime(DEBOUNCE_MS)
    })
    const predicateCall = invalidateSpy.mock.calls.find(
      (call) => typeof (call[0] as { predicate?: unknown })?.predicate === 'function',
    )
    expect(predicateCall).toBeDefined()
    // The scoped call must NOT carry refetchType: 'none' — it's precise and
    // low-volume, so it can safely refetch immediately.
    expect((predicateCall![0] as { refetchType?: string }).refetchType).toBeUndefined()
    // Exercise the actual predicate: it must match a query keyed by this
    // plan's id under 'plans' or 'tasks', and reject unrelated queries.
    const predicate = (predicateCall![0] as { predicate: (q: { queryKey: readonly unknown[] }) => boolean }).predicate
    expect(predicate({ queryKey: ['plans', 'ws-1', 'plan-scoped-1'] })).toBe(true)
    expect(predicate({ queryKey: ['tasks', { plan_id: 'plan-scoped-1' }] })).toBe(true)
    expect(predicate({ queryKey: ['plans', 'ws-1', 'some-other-plan'] })).toBe(false)
    expect(predicate({ queryKey: ['devices'] })).toBe(false)
  })

  it('surfaces an owner-disabled paused state via the SAME invalidate path (no bespoke paused reducer)', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    act(() => {
      useChatStore.getState().handleFrame(
        makeFrame({ state: 'running', plan_phase: 'idle', paused_reason: 'owner_disabled' }),
      )
      vi.advanceTimersByTime(DEBOUNCE_MS)
    })
    // The frame carries paused_reason, but this reducer's contract is only
    // to invalidate — PlansFilterBand reads the refetched Plan.paused_reason field
    // directly (US-10 AS-7), not anything stored from this frame.
    expect(invalidateSpy).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: ['plans'], refetchType: 'none' }),
    )
  })

  it('is NOT routed through any session bucket (no session_id on the wire)', () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: 'some-active-session' })
      useChatStore.getState().handleFrame(makeFrame())
    })
    // No bucket should have been created/touched by this global frame.
    expect(useChatStore.getState().sessionsById).toEqual({})
  })

  // 14-reviewer sign-off Finding #1: the actual regression test — a burst of
  // rapid plan_status frames (the real-world shape: a busy plan execution
  // broadcasting dispatch/judge/synthesize updates within milliseconds of
  // each other) must coalesce into ONE invalidation pass, not one per frame.
  describe('coalescing a burst of rapid frames', () => {
    it('collapses 20 rapid-fire frames for the SAME plan into a single bounded invalidation pass', () => {
      const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
      act(() => {
        for (let i = 0; i < 20; i++) {
          useChatStore.getState().handleFrame(makeFrame({ plan_id: 'plan-1', progress: i / 20 }))
        }
      })
      // Still nothing — all 20 frames landed inside the same debounce window.
      expect(invalidateSpy).not.toHaveBeenCalled()

      act(() => {
        vi.advanceTimersByTime(DEBOUNCE_MS)
      })
      // Exactly 3 calls for one flush: one scoped predicate call (plan-1) +
      // the two broad refetchType:'none' prefixes — NOT 40 (20 frames × 2
      // calls/frame, the pre-fix behavior).
      expect(invalidateSpy).toHaveBeenCalledTimes(3)
    })

    it('collapses rapid-fire frames for MULTIPLE distinct plans into one flush, with one scoped call per distinct plan', () => {
      const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
      act(() => {
        for (let i = 0; i < 10; i++) {
          useChatStore.getState().handleFrame(makeFrame({ plan_id: 'plan-a' }))
          useChatStore.getState().handleFrame(makeFrame({ plan_id: 'plan-b' }))
        }
        vi.advanceTimersByTime(DEBOUNCE_MS)
      })
      // 2 scoped calls (plan-a, plan-b) + 2 broad calls = 4 — bounded
      // regardless of the 20 total frames that arrived.
      expect(invalidateSpy).toHaveBeenCalledTimes(4)
    })

    it('a second burst AFTER the first flush schedules and fires its own new pass', () => {
      const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
      act(() => {
        useChatStore.getState().handleFrame(makeFrame())
        vi.advanceTimersByTime(DEBOUNCE_MS)
      })
      expect(invalidateSpy).toHaveBeenCalledTimes(3)

      act(() => {
        useChatStore.getState().handleFrame(makeFrame())
        useChatStore.getState().handleFrame(makeFrame())
        vi.advanceTimersByTime(DEBOUNCE_MS)
      })
      expect(invalidateSpy).toHaveBeenCalledTimes(6)
    })
  })
})
