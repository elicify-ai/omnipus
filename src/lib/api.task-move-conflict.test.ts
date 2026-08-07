// BDD: UAT round-2 N1 — board drag-to-column 409 message mapping.
// Traces to: pkg/agent/task_executor.go (ErrPlanNotExecuting,
// ErrPlanStateUnresolvable, ErrDispatchCapReached — the exact sentinel error
// strings this parses), pkg/gateway/rest_tasks.go's handleTaskPatch (the ONLY
// two call sites in that handler that ever return 409 for PATCH
// /tasks/{id}), pkg/plan/plan.go's PermitsMemberDispatch (PausedReason
// precedence over State) and FailedReason enum (stopped_by_user vs the three
// genuine-failure reasons).
//
// Commit 5d77f26a's plan-dispatch gate is correct and untouched by this
// suite — this only covers the CLIENT-side text a refused drag now shows,
// replacing the generic ApiError 409 default ("This conflicts with the
// current state. Please refresh and try again.") which is actively wrong for
// the plan-gate case (refreshing can never fix a draft/stopped/paused plan).

import { describe, it, expect } from 'vitest'
import { ApiError } from './api-error'
import { describeTaskMoveConflict, taskMoveErrorMessage } from './api'
import type { Plan } from './api'

function gateBody(planId: string, state: string, pausedReason = ''): string {
  // Mirrors task_executor.go's exact wrap:
  //   fmt.Errorf("%w: plan %q is %s (paused_reason=%q)", ErrPlanNotExecuting, t.PlanID, p.State, p.PausedReason)
  return JSON.stringify({
    error: `task_executor: parent plan is not in a dispatchable state (approved/running, unpaused): plan "${planId}" is ${state} (paused_reason="${pausedReason}")`,
  })
}

function unresolvableBody(planId: string): string {
  return JSON.stringify({
    error: `task_executor: parent plan's state could not be verified: plan "${planId}": plan store I/O error`,
  })
}

function capBody(): string {
  return JSON.stringify({
    error: 'task_executor: global dispatch cap reached (3/3 in flight), retry later',
  })
}

function conflict409(body: string): ApiError {
  // Reproduces exactly what ApiError.fromResponse produces for a 409 today:
  // the generic default `userMessage` (409 is a "known" status, so the raw
  // server text is discarded from userMessage) but the raw text preserved on
  // `.body` — this is the ONLY signal describeTaskMoveConflict has to work
  // with, matching production.
  return new ApiError(409, 'This conflicts with the current state. Please refresh and try again.', { body })
}

function makePlan(overrides: Partial<Plan> = {}): Plan {
  return {
    id: 'plan-1',
    workspace_id: 'ws-1',
    title: 'A Plan',
    state: 'failed',
    plan_phase: 'idle',
    owner_agent_id: 'jim',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-07-20T10:00:00Z',
    updated_at: '2026-07-20T10:00:00Z',
    ...overrides,
  }
}

describe('describeTaskMoveConflict', () => {
  it('maps a draft-plan gate refusal to an Execute-the-plan message', () => {
    const err = conflict409(gateBody('plan-1', 'draft'))
    const msg = describeTaskMoveConflict(err, [])
    expect(msg).toBe('This plan is still a draft — Execute it (from the Plans band above) before this task can run.')
    expect(msg).not.toMatch(/refresh/i)
  })

  it('maps a done-plan gate refusal to a "finished, cannot start" message', () => {
    const err = conflict409(gateBody('plan-1', 'done'))
    const msg = describeTaskMoveConflict(err, [])
    expect(msg).toBe("This plan has already finished — its tasks can't be started this way.")
  })

  it('maps a failed-plan gate refusal to Restart when failed_reason is stopped_by_user (cross-referencing the plans list)', () => {
    const err = conflict409(gateBody('plan-1', 'failed'))
    const plans = [makePlan({ id: 'plan-1', failed_reason: 'stopped_by_user' })]
    const msg = describeTaskMoveConflict(err, plans)
    expect(msg).toBe('This plan was stopped — Restart it (from the Plans band above) before this task can run.')
  })

  it('maps a failed-plan gate refusal to "cannot be restarted" for a genuine failure reason', () => {
    const err = conflict409(gateBody('plan-1', 'failed'))
    const plans = [makePlan({ id: 'plan-1', failed_reason: 'judge_rounds_exhausted' })]
    const msg = describeTaskMoveConflict(err, plans)
    expect(msg).toBe("This plan has failed and can't be restarted — its tasks can no longer run.")
  })

  it('maps a failed-plan gate refusal to "cannot be restarted" when the plan cannot be found in the local list at all (never guesses Restart is offered)', () => {
    const err = conflict409(gateBody('plan-1', 'failed'))
    const msg = describeTaskMoveConflict(err, [])
    expect(msg).toBe("This plan has failed and can't be restarted — its tasks can no longer run.")
  })

  it('paused_reason takes precedence over state (mirrors PermitsMemberDispatch checking PausedReason first) and names owner_disabled specifically', () => {
    // Even though `running` alone would be dispatchable, a non-empty
    // paused_reason must still refuse — and produce the actionable message.
    const err = conflict409(gateBody('plan-1', 'running', 'owner_disabled'))
    const msg = describeTaskMoveConflict(err, [])
    expect(msg).toBe(
      "This plan is paused because its owner agent is disabled — re-enable the agent to resume this plan's tasks.",
    )
  })

  it('falls back to a generic-but-honest paused message for a paused_reason value it does not special-case', () => {
    const err = conflict409(gateBody('plan-1', 'approved', 'some_future_reason'))
    const msg = describeTaskMoveConflict(err, [])
    expect(msg).toBe('This plan is paused (some_future_reason) — resolve that before this task can run.')
  })

  it('maps ErrPlanStateUnresolvable to a "could not verify" message, not a false refresh claim', () => {
    const err = conflict409(unresolvableBody('plan-1'))
    const msg = describeTaskMoveConflict(err, [])
    expect(msg).toBe("This plan's current state couldn't be verified — try moving this task again in a moment.")
  })

  it('maps ErrDispatchCapReached to a genuinely-retryable congestion message', () => {
    const err = conflict409(capBody())
    const msg = describeTaskMoveConflict(err, [])
    expect(msg).toBe('Too many tasks are starting at once — the server is at its dispatch limit. Try moving this task again in a moment.')
  })

  it('returns undefined for a 409 body matching neither known shape (never guesses)', () => {
    const err = conflict409(JSON.stringify({ error: 'some unrelated 409 the client does not recognize' }))
    expect(describeTaskMoveConflict(err, [])).toBeUndefined()
  })

  it('returns undefined when the body is missing entirely', () => {
    const err = new ApiError(409, 'generic')
    expect(describeTaskMoveConflict(err, [])).toBeUndefined()
  })

  it('returns undefined for a non-409 ApiError', () => {
    const err = new ApiError(500, 'boom', { body: gateBody('plan-1', 'draft') })
    expect(describeTaskMoveConflict(err, [])).toBeUndefined()
  })

  it('returns undefined for a non-ApiError', () => {
    expect(describeTaskMoveConflict(new Error('network down'), [])).toBeUndefined()
  })

  it('still matches when the body is the bare error string, not JSON-wrapped (defensive — real bodies are always JSON, but the parser must not throw)', () => {
    const raw = 'task_executor: parent plan is not in a dispatchable state (approved/running, unpaused): plan "plan-1" is draft (paused_reason="")'
    const err = conflict409(raw)
    expect(describeTaskMoveConflict(err, [])).toBe(
      'This plan is still a draft — Execute it (from the Plans band above) before this task can run.',
    )
  })
})

describe('taskMoveErrorMessage', () => {
  it('uses the specific mapping when describeTaskMoveConflict recognizes the body', () => {
    const err = conflict409(gateBody('plan-1', 'draft'))
    expect(taskMoveErrorMessage(err, [])).toBe(
      'This plan is still a draft — Execute it (from the Plans band above) before this task can run.',
    )
  })

  it('falls back to a neutral, non-committal message for an unrecognized 409 — never the "refresh and try again" claim', () => {
    const err = conflict409(JSON.stringify({ error: 'unrecognized' }))
    const msg = taskMoveErrorMessage(err, [])
    expect(msg).toBe('This move was rejected by the server — the task or its plan may be in a state that does not allow it right now.')
    expect(msg).not.toMatch(/refresh/i)
  })

  it('passes non-409 ApiErrors through to the ordinary userMessage', () => {
    const err = new ApiError(500, undefined)
    expect(taskMoveErrorMessage(err, [])).toBe('The server is unavailable. Please try again in a moment.')
  })

  it('passes a plain Error through to its own message', () => {
    expect(taskMoveErrorMessage(new Error('network down'), [])).toBe('network down')
  })

  it('falls back to the generic fallback string for a non-Error throw', () => {
    expect(taskMoveErrorMessage('literally a string', [])).toBe('Failed to move task')
  })
})
