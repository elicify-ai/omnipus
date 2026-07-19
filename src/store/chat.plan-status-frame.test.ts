/**
 * chat.plan-status-frame.test.ts — ADR-049 R1/R3/FR-099/US-10 AS-7.
 *
 * `plan_status` is a GLOBAL frame (no `session_id` on the wire — correlated
 * by `plan_id` instead, R3), unlike `goal_status`/`loop_status`. Asserts the
 * reducer invalidates `['plans']`/`['tasks']` (the "owner disabled surfaces
 * paused" scenario is driven entirely by the refetched `Plan.paused_reason`
 * field — PlanCard already renders it, see planStateColors.ts's
 * `planSecondaryChipLabel` — this reducer's only job is triggering that
 * refetch) and does NOT touch any chat-store session bucket.
 *
 * Traces to: `plan_status` (literal — NOT `plan_status_changed`, R3).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act } from 'react'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { queryClient } from '@/lib/queryClient'
import type { PlanStatusFrame } from '@/lib/api/generated/asyncapi-types'

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
  act(() => {
    useChatStore.setState({ sessionsById: {} })
    useSessionStore.setState({ activeSessionId: null, activeAgentId: null, activeAgentType: null })
  })
})

describe('chat handleFrame — plan_status (ADR-049 R1/R3)', () => {
  it('invalidates the plans query cache', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
    })
    expect(invalidateSpy).toHaveBeenCalledWith(expect.objectContaining({ queryKey: ['plans'] }))
  })

  it('invalidates the tasks query cache (a plan_status change can affect member tasks)', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    act(() => {
      useChatStore.getState().handleFrame(makeFrame())
    })
    expect(invalidateSpy).toHaveBeenCalledWith(expect.objectContaining({ queryKey: ['tasks'] }))
  })

  it('surfaces an owner-disabled paused state via the SAME invalidate path (no bespoke paused reducer)', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')
    act(() => {
      useChatStore.getState().handleFrame(
        makeFrame({ state: 'running', plan_phase: 'idle', paused_reason: 'owner_disabled' }),
      )
    })
    // The frame carries paused_reason, but this reducer's contract is only
    // to invalidate — PlanCard reads the refetched Plan.paused_reason field
    // directly (US-10 AS-7), not anything stored from this frame.
    expect(invalidateSpy).toHaveBeenCalledWith(expect.objectContaining({ queryKey: ['plans'] }))
  })

  it('is NOT routed through any session bucket (no session_id on the wire)', () => {
    act(() => {
      useSessionStore.setState({ activeSessionId: 'some-active-session' })
      useChatStore.getState().handleFrame(makeFrame())
    })
    // No bucket should have been created/touched by this global frame.
    expect(useChatStore.getState().sessionsById).toEqual({})
  })
})
