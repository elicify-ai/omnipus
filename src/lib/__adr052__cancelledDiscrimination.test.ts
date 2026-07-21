// ADR-052 Wave 2 — cancelled-vs-failed discrimination tests (agent "qa-spa").
//
// US-8 / FR-015 / FR-028: a task terminated by Stop MUST be distinguishable
// from a genuine failure — `status === 'failed' && cancel_reason ===
// 'stopped_by_user'` — without a new board column or status enum value.
//
// At the time this file was written, a `grep -rn "cancel_reason" src/components`
// and `grep -rn "isCancelled" src/lib src/components` (excluding the generated
// wire types) found NO shared isCancelled(task)-style helper anywhere in the
// SPA — TaskCard.tsx/BoardView.tsx/ListView.tsx do not yet reference
// `cancel_reason` at all (the frontend-lead's US-8 wave had not landed a
// helper as of this run). Per the mission brief ("if a shared helper/util
// exists (or lands) for isCancelled(task), test it; else test the raw
// predicate ... against fixture tasks — a stable, component-free contract"),
// this file tests the RAW predicate directly against Task fixtures built from
// the real generated Task schema. This is intentionally resilient to WHICHEVER
// component eventually renders the orange "Cancelled" marker — the predicate
// itself is the stable contract (US-8 Acceptance 1, FR-015).
//
// If/when a shared `isCancelled(task)` helper lands, a follow-up test file
// should import and test it directly, exercising the same fixture table
// below so the two suites can be diffed for equivalence.
//
// Traces to: docs/internal/specs/autonomous-agent-plan-execution-spec.md
//   US-8 (line 159), FR-015 (line 671), FR-028 (line 684)

import { describe, it, expect } from 'vitest'
import { Task as TaskSchema } from '@/lib/api/generated/schemas'
import type { z } from 'zod'

type Task = z.infer<typeof TaskSchema>

// The raw predicate under contract (US-8 Acceptance 1 / FR-015): a task is
// the "Cancelled" (orange) variant of Failed iff BOTH status is failed AND
// cancel_reason is the literal 'stopped_by_user'. This is deliberately
// re-implemented here (not imported) since no shared helper exists yet — see
// file header. Kept byte-simple so it is trivially auditable against the FR.
function isCancelledTask(task: Pick<Task, 'status' | 'cancel_reason'>): boolean {
  return task.status === 'failed' && task.cancel_reason === 'stopped_by_user'
}

function fixtureTask(overrides: Partial<Task> & Record<string, unknown> = {}): Task {
  const raw = {
    id: 'task-1',
    title: 'Fixture task',
    action: 'llm' as const,
    status: 'failed' as const,
    workspace_id: 'ws-1',
    owner: 'jim',
    created_by: 'jim',
    created_at: '2026-07-20T00:00:00Z',
    updated_at: '2026-07-20T00:00:00Z',
    ...overrides,
  }
  const parsed = TaskSchema.parse(raw)
  return parsed
}

describe('ADR-052 US-8/FR-015/FR-028 — isCancelled raw predicate (component-free contract)', () => {
  it('a task Stopped by the user (failed + stopped_by_user) IS cancelled', () => {
    const t = fixtureTask({ status: 'failed', cancel_reason: 'stopped_by_user' })
    expect(isCancelledTask(t)).toBe(true)
  })

  it('a genuinely-failed task (failed, no cancel_reason) is NOT cancelled — the differentiation case', () => {
    const raw = fixtureTask({ status: 'failed' })
    // Ensure the fixture truly omits the field rather than defaulting it.
    const withoutReason = { ...raw } as Partial<Task>
    delete withoutReason.cancel_reason
    expect(isCancelledTask(withoutReason as Task)).toBe(false)
  })

  it('a genuinely-failed task with cancel_reason explicitly null is NOT cancelled (restart-cleared state)', () => {
    const t = fixtureTask({ status: 'failed', cancel_reason: null })
    expect(isCancelledTask(t)).toBe(false)
  })

  it('an in_progress task is never cancelled, even if cancel_reason were somehow set', () => {
    const t = fixtureTask({ status: 'in_progress', cancel_reason: 'stopped_by_user' })
    expect(isCancelledTask(t)).toBe(false)
  })

  it('a done task is never cancelled', () => {
    const t = fixtureTask({ status: 'done' })
    expect(isCancelledTask(t)).toBe(false)
  })

  it('a next task (never run) is never cancelled', () => {
    const t = fixtureTask({ status: 'next' })
    expect(isCancelledTask(t)).toBe(false)
  })

  it.each([
    ['failed', 'stopped_by_user', true],
    ['failed', undefined, false],
    ['failed', null, false],
    ['in_progress', 'stopped_by_user', false],
    ['done', undefined, false],
    ['blocked', undefined, false],
    ['inbox', undefined, false],
    ['next', undefined, false],
  ] as const)('status=%s cancel_reason=%s -> isCancelled=%s', (status, cancel_reason, expected) => {
    const overrides: Partial<Task> & Record<string, unknown> = { status }
    if (cancel_reason !== undefined) overrides.cancel_reason = cancel_reason
    const t = fixtureTask(overrides)
    expect(isCancelledTask(t)).toBe(expected)
  })

  it('two fixture tasks differing ONLY in cancel_reason produce two DIFFERENT isCancelled results (not a hardcoded true/false)', () => {
    const cancelled = fixtureTask({ status: 'failed', cancel_reason: 'stopped_by_user' })
    const genuine = fixtureTask({ status: 'failed' })
    const genuineNoReason = { ...genuine } as Partial<Task>
    delete genuineNoReason.cancel_reason
    expect(isCancelledTask(cancelled)).not.toBe(isCancelledTask(genuineNoReason as Task))
  })
})

// ── Plan-level equivalent, for parity with the existing planSecondaryChipLabel
// contract (src/lib/planStateColors.ts, already covers failed_reason at the
// plan level — see planStateColors.test.ts). This confirms the SAME
// discriminator shape (status/state + reason literal) holds at BOTH levels,
// which is the "Stop = clear / Play = set, three levels" symmetry (FR-022).

describe('ADR-052 US-8 — plan-level cancelled discriminator uses the same shape as task-level', () => {
  it('a plan failed with reason stopped_by_user is the cancelled variant (mirrors task-level predicate)', () => {
    function isCancelledPlan(plan: { state: string; failed_reason?: string }): boolean {
      return plan.state === 'failed' && plan.failed_reason === 'stopped_by_user'
    }
    expect(isCancelledPlan({ state: 'failed', failed_reason: 'stopped_by_user' })).toBe(true)
    expect(isCancelledPlan({ state: 'failed', failed_reason: 'judge_rounds_exhausted' })).toBe(false)
    expect(isCancelledPlan({ state: 'failed', failed_reason: 'idle_expired' })).toBe(false)
    expect(isCancelledPlan({ state: 'failed' })).toBe(false)
    expect(isCancelledPlan({ state: 'running' })).toBe(false)
  })
})
