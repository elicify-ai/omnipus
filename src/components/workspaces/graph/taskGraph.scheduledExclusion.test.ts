/**
 * Tests for `buildTaskGraph`'s schedule-bearing exclusion (issue #1682,
 * founder decision 2026-11-07, #team-b): scheduled tasks stay calendar-only
 * (operator ruling 2026-08-07), the same way Board and List already filter
 * them (BoardView.tsx::BoardView, ListView.tsx::ListView — both via
 * `isScheduledTrigger`). The Graph canvas had no such filter, so a scheduled
 * task that blocks — or was blocked by — an unscheduled task still rendered
 * as a node, with a Start button that writes a status instead of running it.
 *
 * Oracle sources (the implementation is NOT one of them):
 *   - Issue #1682: exclude in BOTH the workspace and the plan scope; edges
 *     to or from a hidden scheduled task are dropped; the unlinked notice
 *     does not count hidden scheduled tasks.
 *   - `isScheduledTrigger` (taskFormFields.ts) over the wire `TaskTrigger`
 *     vocabulary `{manual | once | recurring}` (generated/schemas.ts):
 *     schedule-bearing = `once` + `recurring`; `manual` or no trigger at all
 *     stays visible. (Older prose — boardListExclusion.test.ts's header,
 *     calendar-recurrence-redesign-spec.md D3 — still names an `every`
 *     trigger type; no such type exists in the current wire contract, and
 *     the 2026-08-07 ruling broadened the boundary to ALL schedule-bearing
 *     triggers, i.e. `once` + `recurring`.)
 *   - Plan scope is in scope deliberately: the issue says "in both the
 *     workspace and the plan scope", and Board applies the same filter
 *     scope-independently (ADR-051 D2 puts plan filtering upstream of the
 *     views, so a scheduled plan member never reaches BoardView in any
 *     scope); calendar-recurrence-redesign-spec.md grants no plan carve-out.
 *
 * Deliberately NOT pinned (spec is silent, both fix placements legitimate):
 * whether an unscheduled task whose ONLY links point at scheduled tasks
 * stays a node or collapses into `unlinked` under `collapseOrphans: true`.
 * The issue settles only that the scheduled tasks themselves vanish
 * everywhere — nodes, edges, and the unlinked count.
 */

import { describe, it, expect } from 'vitest'
import { buildTaskGraph } from './taskGraph'
import type { Task } from '@/lib/api'

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 't1',
    title: 'A task',
    action: 'llm',
    status: 'inbox',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    ...overrides,
  } as Task
}

// Trigger fixtures mirroring boardListExclusion.test.ts (the Board/List side
// of the same ruling) so both sides exercise identical trigger shapes.
const ONCE_TRIGGER = { type: 'once', config: { at_ms: 1000 } } as const
const RECURRING_TRIGGER = {
  type: 'recurring',
  config: { rrule: 'FREQ=WEEKLY;BYDAY=MO', dtstart_ms: 1234, tz: 'UTC' },
} as const

describe('buildTaskGraph — scheduled tasks are calendar-only (workspace scope, collapseOrphans off)', () => {
  it('excludes a `once` blocker: it is not a node, and no edge touches it', () => {
    // Issue #1682: a scheduled task that blocks an unscheduled task must not
    // appear on the canvas — the edge to it is dropped with it.
    const onceBlocker = makeTask({
      id: 'once-1',
      title: 'Once blocker',
      trigger: ONCE_TRIGGER,
    })
    const manual = makeTask({ id: 'manual-1', title: 'Manual task', blocked_by: ['once-1'] })

    const { nodes, edges } = buildTaskGraph([onceBlocker, manual])

    expect(nodes.map((n) => n.id)).toEqual(['manual-1'])
    expect(edges).toHaveLength(0)
  })

  it('excludes a `recurring` blocked task: it is not a node, and no edge touches it', () => {
    // Same exclusion for the scheduled task on the BLOCKED side of an edge.
    const manual = makeTask({ id: 'manual-1', title: 'Manual task' })
    const recurringBlocked = makeTask({
      id: 'recurring-1',
      title: 'Recurring task',
      blocked_by: ['manual-1'],
      trigger: RECURRING_TRIGGER,
    })

    const { nodes, edges } = buildTaskGraph([manual, recurringBlocked])

    expect(nodes.map((n) => n.id)).toEqual(['manual-1'])
    expect(edges).toHaveLength(0)
  })

  it('excludes both schedule-bearing trigger types in one chain: only the unscheduled middle task remains, zero edges', () => {
    // once → manual → recurring: BOTH hidden trigger types appear in one
    // dependency chain, each on a different side of the surviving task.
    const once = makeTask({ id: 'once-1', title: 'Once head', trigger: ONCE_TRIGGER })
    const manual = makeTask({ id: 'mid-1', title: 'Manual middle', blocked_by: ['once-1'] })
    const recurring = makeTask({
      id: 'recurring-1',
      title: 'Recurring tail',
      blocked_by: ['mid-1'],
      trigger: RECURRING_TRIGGER,
    })

    const { nodes, edges } = buildTaskGraph([once, manual, recurring])

    expect(nodes.map((n) => n.id)).toEqual(['mid-1'])
    expect(edges).toHaveLength(0)
  })

  it('a workspace of only scheduled tasks renders nothing — nodes, edges and unlinked all empty', () => {
    const once = makeTask({ id: 'once-1', trigger: ONCE_TRIGGER })
    const recurring = makeTask({ id: 'recurring-1', trigger: RECURRING_TRIGGER })

    const { nodes, edges, unlinked } = buildTaskGraph([once, recurring])

    expect(nodes).toHaveLength(0)
    expect(edges).toHaveLength(0)
    // Not in `unlinked` either — the unlinked notice must not count them.
    expect(unlinked).toHaveLength(0)
  })

  it('controls: an unscheduled task with the same links is still a node with its edge', () => {
    // Positive control (Binding Rule 4): identical shape to the exclusion
    // tests above but with a manual trigger — a filter that hides everything
    // must fail this assertion.
    const manualBlocker = makeTask({
      id: 'manual-1',
      title: 'Manual blocker',
      trigger: { type: 'manual', config: {} },
    })
    const manual = makeTask({ id: 'mid-1', title: 'Manual middle', blocked_by: ['manual-1'] })
    const manualTail = makeTask({
      id: 'manual-3',
      title: 'Manual tail',
      blocked_by: ['mid-1'],
      trigger: { type: 'manual', config: {} },
    })

    const { nodes, edges } = buildTaskGraph([manualBlocker, manual, manualTail])

    expect(nodes.map((n) => n.id)).toEqual(['manual-1', 'mid-1', 'manual-3'])
    expect(edges).toHaveLength(2)
    expect(edges[0]).toMatchObject({ source: 'manual-1', target: 'mid-1' })
    expect(edges[1]).toMatchObject({ source: 'mid-1', target: 'manual-3' })
  })

  it('controls: a task with no trigger at all stays visible, with its edge to a manual task', () => {
    // The trigger field is optional — absence is the default and keeps a task
    // on Board/List (2026-08-07 ruling), so it keeps it on the Graph too.
    const noTrigger = makeTask({ id: 'no-trigger-1', title: 'No trigger task' })
    const manual = makeTask({ id: 'manual-1', title: 'Manual task', blocked_by: ['no-trigger-1'] })

    const { nodes, edges } = buildTaskGraph([noTrigger, manual])

    expect(nodes.map((n) => n.id)).toEqual(['no-trigger-1', 'manual-1'])
    expect(edges).toHaveLength(1)
    expect(edges[0]).toMatchObject({ source: 'no-trigger-1', target: 'manual-1' })
  })
})

describe('buildTaskGraph — scheduled tasks are calendar-only (workspace scope, collapseOrphans on)', () => {
  it('excludes a `once` blocker: not a node, no edge, and not in the unlinked tray', () => {
    // Issue #1682: the unlinked notice does not count hidden scheduled tasks
    // — they vanish entirely, unlike a merely DAG-irrelevant unscheduled task.
    const onceBlocker = makeTask({
      id: 'once-1',
      title: 'Once blocker',
      trigger: ONCE_TRIGGER,
    })
    const manual = makeTask({ id: 'manual-1', title: 'Manual task', blocked_by: ['once-1'] })

    const { nodes, edges, unlinked } = buildTaskGraph([onceBlocker, manual], [], {
      collapseOrphans: true,
    })

    expect(nodes.map((n) => n.id)).not.toContain('once-1')
    expect(edges).toHaveLength(0)
    expect(unlinked.map((t) => t.id)).not.toContain('once-1')
  })

  it('excludes a `recurring` blocked task: not a node, no edge, and not in the unlinked tray', () => {
    const manual = makeTask({ id: 'manual-1', title: 'Manual task' })
    const recurringBlocked = makeTask({
      id: 'recurring-1',
      title: 'Recurring task',
      blocked_by: ['manual-1'],
      trigger: RECURRING_TRIGGER,
    })

    const { nodes, edges, unlinked } = buildTaskGraph([manual, recurringBlocked], [], {
      collapseOrphans: true,
    })

    expect(nodes.map((n) => n.id)).not.toContain('recurring-1')
    expect(edges).toHaveLength(0)
    expect(unlinked.map((t) => t.id)).not.toContain('recurring-1')
  })

  it('excludes lone scheduled tasks from the unlinked tray while a lone unscheduled task still collapses there', () => {
    // Three DAG-irrelevant tasks: the unscheduled one collapses into
    // `unlinked` (existing orphan behaviour), the two scheduled ones vanish.
    const once = makeTask({ id: 'once-1', trigger: ONCE_TRIGGER })
    const recurring = makeTask({ id: 'recurring-1', trigger: RECURRING_TRIGGER })
    const loneManual = makeTask({ id: 'lone-manual-1', title: 'Lone manual task' })

    const { nodes, unlinked } = buildTaskGraph([once, recurring, loneManual], [], {
      collapseOrphans: true,
    })

    expect(nodes).toHaveLength(0)
    expect(unlinked.map((t) => t.id)).toEqual(['lone-manual-1'])
  })
})

describe('buildTaskGraph — scheduled tasks are calendar-only (plan scope)', () => {
  it('excludes a `once` member that blocks another member: not a node, no edge', () => {
    // Issue #1682: "in both the workspace and the plan scope" — Board applies
    // the same filter scope-independently (ADR-051 D2), so plan scope hides
    // scheduled members too.
    const onceMember = makeTask({
      id: 'once-plan-1',
      title: 'Once plan member',
      plan_id: 'plan-1',
      trigger: ONCE_TRIGGER,
    })
    const manualMember = makeTask({
      id: 'manual-plan-1',
      title: 'Manual plan member',
      plan_id: 'plan-1',
      blocked_by: ['once-plan-1'],
    })

    const { nodes, edges } = buildTaskGraph([onceMember, manualMember], [], { planId: 'plan-1' })

    expect(nodes.map((n) => n.id)).toEqual(['manual-plan-1'])
    expect(edges).toHaveLength(0)
  })

  it('excludes a member blocked by a `recurring` member: not a node, no edge', () => {
    const manualMember = makeTask({
      id: 'manual-plan-1',
      title: 'Manual plan member',
      plan_id: 'plan-1',
    })
    const recurringMember = makeTask({
      id: 'recurring-plan-1',
      title: 'Recurring plan member',
      plan_id: 'plan-1',
      blocked_by: ['manual-plan-1'],
      trigger: RECURRING_TRIGGER,
    })

    const { nodes, edges } = buildTaskGraph([manualMember, recurringMember], [], {
      planId: 'plan-1',
    })

    expect(nodes.map((n) => n.id)).toEqual(['manual-plan-1'])
    expect(edges).toHaveLength(0)
  })

  it('controls: manual plan members keep rendering with their edges', () => {
    const a = makeTask({
      id: 'manual-plan-1',
      title: 'Manual plan member',
      plan_id: 'plan-1',
      trigger: { type: 'manual', config: {} },
    })
    const b = makeTask({
      id: 'manual-plan-2',
      title: 'Another manual member',
      plan_id: 'plan-1',
      blocked_by: ['manual-plan-1'],
    })

    const { nodes, edges } = buildTaskGraph([a, b], [], { planId: 'plan-1' })

    expect(nodes.map((n) => n.id)).toEqual(['manual-plan-1', 'manual-plan-2'])
    expect(edges).toHaveLength(1)
    expect(edges[0]).toMatchObject({ source: 'manual-plan-1', target: 'manual-plan-2' })
  })
})
