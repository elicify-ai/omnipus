/**
 * boardListExclusion.test.ts
 *
 * Board and List MUST exclude every task with a schedule-bearing trigger —
 * `once` (fires at an absolute instant), `every` (fixed interval), or
 * `recurring` (repeat rule) — those are calendar-only. Only a `manual`
 * trigger (or no trigger at all, the default) keeps a task on Board/List.
 *
 * This broadens the original FR-011/US-3 boundary (which excluded only
 * `every`/`recurring`) per operator ruling 2026-08-07: "recurring and ALL
 * scheduled tasks are calendar-only" — a `once` task now also moves to
 * calendar-only, since it too carries a concrete firing instant. The split
 * remains presentation-only (the task store/REST API are untouched — the
 * task still exists and still runs, it just never renders as a Board card
 * or List row), so this exercises the exported predicate (`isScheduledTrigger`,
 * taskFormFields.ts) plus the exact filter shape BoardView.tsx/ListView.tsx
 * apply, as a pure unit test — no component mount required.
 *
 * `isRecurringTrigger` (the older, narrower every/recurring-only predicate)
 * is retained and still covered below — it now backs ONLY
 * repeating-specific call sites (BoardView.tsx's canDropTransition drag
 * guard, TaskDetailPanel's raw-cron-hiding guard), not the Board/List
 * visibility boundary itself.
 */

import { describe, it, expect } from 'vitest'
import { isRecurringTrigger, isScheduledTrigger } from './taskFormFields'
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
// `surface !== 'user'` exclusion, UNCHANGED, plus the broadened
// schedule-bearing exclusion this feature adds.
function userVisibleTasks(tasks: Task[]): Task[] {
  return tasks.filter(
    (t) => (t.surface === 'user' || t.surface === undefined) && !isScheduledTrigger(t.trigger),
  )
}

describe('isRecurringTrigger (narrower — repeating-specific call sites only)', () => {
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

describe('isScheduledTrigger (broadened — Board/List visibility boundary)', () => {
  it('is true for once, every, and recurring trigger types', () => {
    expect(isScheduledTrigger({ type: 'once', config: { at_ms: 1234 } })).toBe(true)
    expect(isScheduledTrigger({ type: 'every', config: { every_ms: 60_000 } })).toBe(true)
    expect(isScheduledTrigger({ type: 'recurring', config: { cron_expr: '0 9 * * MON' } })).toBe(true)
  })

  it('is false for manual, undefined, and null triggers', () => {
    expect(isScheduledTrigger({ type: 'manual', config: {} })).toBe(false)
    expect(isScheduledTrigger(undefined)).toBe(false)
    expect(isScheduledTrigger(null)).toBe(false)
  })
})

describe('Board/List schedule exclusion (broadened FR-011/US-3 boundary)', () => {
  it('excludes once, every, and recurring tasks — keeps ONLY the manual task (positive control)', () => {
    const manualTask = makeTask({ id: 'manual-1', title: 'Manual task' })
    const noTriggerTask = makeTask({ id: 'no-trigger-1', title: 'No trigger task' })
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

    const visible = userVisibleTasks([
      manualTask,
      noTriggerTask,
      onceTask,
      everyTask,
      recurringTask,
    ])

    // Positive control (Binding Rule 4): genuinely unscheduled tasks
    // (explicit `manual` trigger, or no trigger at all) MUST still appear —
    // a filter that hides everything must fail this assertion.
    expect(visible.map((t) => t.id)).toEqual(['manual-1', 'no-trigger-1'])
    // Surviving tasks are untouched by the filter — same object reference,
    // no behaviour change for unscheduled tasks (Acceptance Scenario 4).
    expect(visible[0]).toBe(manualTask)
    expect(visible[1]).toBe(noTriggerTask)
  })

  it('the schedule exclusion is additive — the existing surface !== "user" exclusion is unchanged', () => {
    const heartbeatTask = makeTask({ id: 'hb-1', surface: 'heartbeat' })
    const heartbeatScheduledTask = makeTask({
      id: 'hb-2',
      surface: 'heartbeat',
      trigger: { type: 'recurring', config: { cron_expr: '0 9 * * MON' } },
    })
    const userManualTask = makeTask({ id: 'user-1' })
    const userOnceTask = makeTask({
      id: 'user-2',
      trigger: { type: 'once', config: { at_ms: 1000 } },
    })

    const visible = userVisibleTasks([
      heartbeatTask,
      heartbeatScheduledTask,
      userManualTask,
      userOnceTask,
    ])

    expect(visible.map((t) => t.id)).toEqual(['user-1'])
  })

  it('a Board-visible task blocked_by a scheduled task keeps its blocked_by reference intact (name resolution/rollups unaffected)', () => {
    // Edge Cases: "A Board-visible task blocked_by a calendar-only scheduled
    // task → the blocker's name still resolves (the store is untouched by
    // the presentation split) and blocked-state rollups ignore trigger type;
    // the blocker simply has no Board/List card of its own." This predicate
    // is a pure array filter — it never mutates surviving tasks' fields, so
    // a `blocked_by` id pointing at an excluded scheduled task still
    // resolves against the full (unfiltered) task list a caller may hold.
    const onceBlocker = makeTask({
      id: 'blocker-once',
      title: 'Once blocker',
      trigger: { type: 'once', config: { at_ms: 1000 } },
    })
    const blockedTask = makeTask({
      id: 'blocked-1',
      title: 'Blocked task',
      blocked_by: ['blocker-once'],
    })

    const visible = userVisibleTasks([onceBlocker, blockedTask])

    // The scheduled blocker gets no card of its own...
    expect(visible.map((t) => t.id)).toEqual(['blocked-1'])
    // ...but the blocked task's dependency reference is untouched.
    expect(visible[0].blocked_by).toEqual(['blocker-once'])
  })

  it('tags and other fields on a surviving manual task are unaffected', () => {
    const manualTaskWithTags = makeTask({
      id: 'manual-tagged',
      tags: ['urgent'],
      priority: 1,
    })
    const visible = userVisibleTasks([manualTaskWithTags])
    expect(visible).toEqual([manualTaskWithTags])
  })
})
