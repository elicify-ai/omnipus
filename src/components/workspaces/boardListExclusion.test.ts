/**
 * boardListExclusion.test.ts
 *
 * Test 18 (calendar-recurrence-redesign-spec.md, FR-011/US-3): Board and
 * List MUST exclude tasks whose trigger.type ∈ {every, recurring} — those
 * are calendar-only (D3). The split is presentation-only (Explicit
 * Non-Behaviors: the task store/REST API are untouched), so this exercises
 * the exported predicate (`isRecurringTrigger`, taskFormFields.ts) plus the
 * exact filter shape BoardView.tsx/ListView.tsx apply, as a pure unit test —
 * no component mount required.
 */

import { describe, it, expect } from 'vitest'
import { isRecurringTrigger } from './taskFormFields'
import type { Task } from '@/lib/api'

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Task',
    action: 'llm',
    priority: 3,
    status: 'next',
    workspace_id: 'ws-1',
    surface: 'user',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

// Mirrors the exact predicate BoardView.tsx/ListView.tsx apply: the existing
// `surface !== 'user'` exclusion, UNCHANGED, plus the additive recurring
// exclusion this feature adds.
function userVisibleTasks(tasks: Task[]): Task[] {
  return tasks.filter(
    (t) => (t.surface === 'user' || t.surface === undefined) && !isRecurringTrigger(t.trigger),
  )
}

describe('isRecurringTrigger', () => {
  it('is true for every and recurring trigger types', () => {
    expect(isRecurringTrigger({ type: 'every', config: { every_ms: 60_000 } })).toBe(true)
    expect(isRecurringTrigger({ type: 'recurring', config: { cron_expr: '0 9 * * MON' } })).toBe(true)
  })

  it('is false for manual, once, undefined, and null triggers', () => {
    expect(isRecurringTrigger({ type: 'manual', config: {} })).toBe(false)
    expect(isRecurringTrigger({ type: 'once', config: { at_ms: 1234 } })).toBe(false)
    expect(isRecurringTrigger(undefined)).toBe(false)
    expect(isRecurringTrigger(null)).toBe(false)
  })
})

describe('Board/List recurring exclusion (FR-011, US-3)', () => {
  it('excludes every and recurring tasks, keeps manual and once tasks exactly as before', () => {
    const manualTask = makeTask({ id: 'manual-1', title: 'Manual task' })
    const onceTask = makeTask({
      id: 'once-1',
      title: 'Once task',
      trigger: { type: 'once', config: { at_ms: 1000 } },
    })
    const everyTask = makeTask({
      id: 'every-1',
      title: 'Every task',
      trigger: { type: 'every', config: { every_ms: 60_000 } },
    })
    const recurringTask = makeTask({
      id: 'recurring-1',
      title: 'Recurring task',
      trigger: { type: 'recurring', config: { cron_expr: '0 9 * * MON' } },
    })

    const visible = userVisibleTasks([manualTask, onceTask, everyTask, recurringTask])

    expect(visible.map((t) => t.id)).toEqual(['manual-1', 'once-1'])
    // Surviving tasks are untouched by the filter — same object reference,
    // no behaviour change for non-recurring tasks (Acceptance Scenario 4).
    expect(visible[0]).toBe(manualTask)
    expect(visible[1]).toBe(onceTask)
  })

  it('the recurring exclusion is additive — the existing surface !== "user" exclusion is unchanged', () => {
    const heartbeatTask = makeTask({ id: 'hb-1', surface: 'heartbeat' })
    const heartbeatRecurringTask = makeTask({
      id: 'hb-2',
      surface: 'heartbeat',
      trigger: { type: 'recurring', config: { cron_expr: '0 9 * * MON' } },
    })
    const userManualTask = makeTask({ id: 'user-1' })

    const visible = userVisibleTasks([heartbeatTask, heartbeatRecurringTask, userManualTask])

    expect(visible.map((t) => t.id)).toEqual(['user-1'])
  })

  it('a Board-visible task blocked_by a recurring task keeps its blocked_by reference intact (name resolution/rollups unaffected)', () => {
    // Edge Cases: "A Board-visible task blocked_by a calendar-only recurring
    // task → the blocker's name still resolves (the store is untouched by
    // the presentation split) and blocked-state rollups ignore trigger type;
    // the blocker simply has no Board/List card of its own." This predicate
    // is a pure array filter — it never mutates surviving tasks' fields, so
    // a `blocked_by` id pointing at an excluded recurring task still
    // resolves against the full (unfiltered) task list a caller may hold.
    const recurringBlocker = makeTask({
      id: 'blocker-recurring',
      title: 'Recurring blocker',
      trigger: { type: 'recurring', config: { cron_expr: '0 9 * * MON' } },
    })
    const blockedTask = makeTask({
      id: 'blocked-1',
      title: 'Blocked task',
      blocked_by: ['blocker-recurring'],
    })

    const visible = userVisibleTasks([recurringBlocker, blockedTask])

    // The recurring blocker gets no card of its own...
    expect(visible.map((t) => t.id)).toEqual(['blocked-1'])
    // ...but the blocked task's dependency reference is untouched.
    expect(visible[0].blocked_by).toEqual(['blocker-recurring'])
  })

  it('milestone_id and other fields on a surviving non-recurring task are unaffected', () => {
    const onceTaskWithMilestone = makeTask({
      id: 'once-milestone',
      trigger: { type: 'once', config: { at_ms: 5000 } },
      milestone_id: 'ms-1',
      priority: 1,
    })
    const visible = userVisibleTasks([onceTaskWithMilestone])
    expect(visible).toEqual([onceTaskWithMilestone])
  })
})
