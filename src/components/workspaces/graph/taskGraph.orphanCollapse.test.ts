/**
 * Tests for `isDagRelevant` and `buildTaskGraph`'s `collapseOrphans` option
 * (Plan Swimlane board redesign, fix #2 — orphan collapsing in the
 * whole-workspace "All" mode). Psychology-grounded: Attention — a
 * disconnected single node is noise; Gestalt — a connected component is
 * meaning. A task is DAG-relevant when it has a `blocked_by` edge, is the
 * `blocked_by` of another visible task, or carries a `plan_id`; everything
 * else collapses into `unlinked` instead of rendering as a lone dot.
 */

import { describe, it, expect } from 'vitest'
import { buildTaskGraph, isDagRelevant, buildBlockerReferencedIds, isDagRelevantFast } from './taskGraph'
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

describe('isDagRelevant', () => {
  it('is true for a task that has a blocked_by dependency', () => {
    const a = makeTask({ id: 'a' })
    const b = makeTask({ id: 'b', blocked_by: ['a'] })
    expect(isDagRelevant(b, [a, b])).toBe(true)
  })

  it('is true for a task that is the blocked_by target of another task (has a dependent)', () => {
    const a = makeTask({ id: 'a' })
    const b = makeTask({ id: 'b', blocked_by: ['a'] })
    expect(isDagRelevant(a, [a, b])).toBe(true)
  })

  it('is true for a task carrying a plan_id, even with zero edges', () => {
    const t = makeTask({ id: 'a', plan_id: 'plan-1' })
    expect(isDagRelevant(t, [t])).toBe(true)
  })

  it('is false for a standalone task: no blocked_by, no dependent, no plan', () => {
    const t = makeTask({ id: 'a' })
    expect(isDagRelevant(t, [t])).toBe(false)
  })

  it('is false when the only reference to it is its own blocked_by list (self, not a dependent)', () => {
    // Guard against a naive implementation that forgets to exclude the task itself.
    const t = makeTask({ id: 'a' })
    expect(isDagRelevant(t, [t])).toBe(false)
  })

  it('an empty-array blocked_by does not count as having a dependency', () => {
    const t = makeTask({ id: 'a', blocked_by: [] })
    expect(isDagRelevant(t, [t])).toBe(false)
  })
})

// 14-reviewer sign-off Finding #5 (perf): `isDagRelevant` itself is still an
// O(n) `.some()` scan per call (kept for backward compat — see its doc
// comment), but `buildTaskGraph`'s `collapseOrphans` pass no longer calls it
// per task; it precomputes `buildBlockerReferencedIds` ONCE and checks each
// task with `isDagRelevantFast` instead. These tests lock the equivalence
// between the two entry points and the precomputed set's own semantics
// (including the self-reference exclusion) directly.
describe('buildBlockerReferencedIds / isDagRelevantFast — precomputed-set fast path', () => {
  it('produces the id of a task that is referenced as a blocker by another task', () => {
    const a = makeTask({ id: 'a' })
    const b = makeTask({ id: 'b', blocked_by: ['a'] })
    const referenced = buildBlockerReferencedIds([a, b])
    expect(referenced.has('a')).toBe(true)
    expect(referenced.has('b')).toBe(false)
  })

  it('excludes a task that only references itself in its own blocked_by (self-loop, not a real dependent)', () => {
    const selfLoop = makeTask({ id: 'a', blocked_by: ['a'] })
    const referenced = buildBlockerReferencedIds([selfLoop])
    // Mirrors isDagRelevant's own `other.id !== task.id` exclusion — a task
    // referencing only itself must not count as "someone depends on me".
    expect(referenced.has('a')).toBe(false)
  })

  it('an empty candidate list produces an empty set', () => {
    expect(buildBlockerReferencedIds([]).size).toBe(0)
  })

  it('isDagRelevantFast agrees with isDagRelevant for every case in the suite above, given the equivalent precomputed set', () => {
    const cases: { task: Task; candidates: Task[] }[] = [
      { task: makeTask({ id: 'a' }), candidates: [makeTask({ id: 'a' }), makeTask({ id: 'b', blocked_by: ['a'] })] },
      { task: makeTask({ id: 'b', blocked_by: ['a'] }), candidates: [makeTask({ id: 'a' }), makeTask({ id: 'b', blocked_by: ['a'] })] },
      { task: makeTask({ id: 'a', plan_id: 'plan-1' }), candidates: [makeTask({ id: 'a', plan_id: 'plan-1' })] },
      { task: makeTask({ id: 'a' }), candidates: [makeTask({ id: 'a' })] },
      { task: makeTask({ id: 'a', blocked_by: ['a'] }), candidates: [makeTask({ id: 'a', blocked_by: ['a'] })] },
    ]
    for (const { task, candidates } of cases) {
      const referenced = buildBlockerReferencedIds(candidates)
      expect(isDagRelevantFast(task, referenced)).toBe(isDagRelevant(task, candidates))
    }
  })

  it('scales correctly on a larger mixed graph: precomputing once still matches the per-task isDagRelevant result for every task', () => {
    // 200 tasks: a long dependency chain (each blocked by the previous —
    // every non-terminal one is DAG-relevant via "has a dependent"), a
    // handful of plan members, and a batch of genuinely standalone orphans.
    const chain: Task[] = Array.from({ length: 150 }, (_, i) =>
      makeTask({ id: `chain-${i}`, blocked_by: i > 0 ? [`chain-${i - 1}`] : [] }),
    )
    const planMembers: Task[] = Array.from({ length: 25 }, (_, i) => makeTask({ id: `plan-${i}`, plan_id: 'plan-x' }))
    const orphans: Task[] = Array.from({ length: 25 }, (_, i) => makeTask({ id: `orphan-${i}` }))
    const all = [...chain, ...planMembers, ...orphans]

    const referenced = buildBlockerReferencedIds(all)
    for (const t of all) {
      expect(isDagRelevantFast(t, referenced)).toBe(isDagRelevant(t, all))
    }
    // Sanity: the last chain link has no dependent and no plan_id, but IS
    // itself blocked_by the previous link, so it's still relevant via that
    // check alone (the same reason the very first per-task equivalence loop
    // above already covers it) — every chain link is relevant.
    expect(isDagRelevantFast(chain[chain.length - 1], referenced)).toBe(true)
    // Every orphan is irrelevant; every plan member and every chain link is relevant.
    for (const o of orphans) expect(isDagRelevantFast(o, referenced)).toBe(false)
    for (const p of planMembers) expect(isDagRelevantFast(p, referenced)).toBe(true)
    for (const c of chain) expect(isDagRelevantFast(c, referenced)).toBe(true)
  })
})

describe('buildTaskGraph — collapseOrphans defaults to false (legacy behavior preserved)', () => {
  it('does not collapse anything when the option is omitted', () => {
    const tasks = [makeTask({ id: 'a' }), makeTask({ id: 'b' })]
    const { nodes, unlinked } = buildTaskGraph(tasks)
    expect(nodes).toHaveLength(2)
    expect(unlinked).toHaveLength(0)
  })

  it('does not collapse anything when collapseOrphans is explicitly false', () => {
    const tasks = [makeTask({ id: 'a' })]
    const { nodes, unlinked } = buildTaskGraph(tasks, [], { collapseOrphans: false })
    expect(nodes).toHaveLength(1)
    expect(unlinked).toHaveLength(0)
  })
})

describe('buildTaskGraph — collapseOrphans: true (whole-workspace orphan collapsing)', () => {
  it('collapses standalone one-time tasks into `unlinked`, rendering zero nodes for them', () => {
    const tasks = [
      makeTask({ id: 'lone1', title: 'Lone task 1' }),
      makeTask({ id: 'lone2', title: 'Lone task 2' }),
    ]
    const { nodes, unlinked } = buildTaskGraph(tasks, [], { collapseOrphans: true })
    expect(nodes).toHaveLength(0)
    expect(unlinked.map((t) => t.id).sort()).toEqual(['lone1', 'lone2'])
  })

  it('keeps a connected DAG fully on the canvas — a linked task is never sent to the tray', () => {
    const tasks = [
      makeTask({ id: 'a' }),
      makeTask({ id: 'b', blocked_by: ['a'] }),
      makeTask({ id: 'lone' }),
    ]
    const { nodes, edges, unlinked } = buildTaskGraph(tasks, [], { collapseOrphans: true })
    expect(nodes.map((n) => n.id).sort()).toEqual(['a', 'b'])
    expect(edges).toHaveLength(1)
    expect(unlinked.map((t) => t.id)).toEqual(['lone'])
  })

  it('keeps a plan member on the canvas even with zero dependency edges', () => {
    const tasks = [
      makeTask({ id: 'p1', plan_id: 'plan-x' }),
      makeTask({ id: 'lone' }),
    ]
    const { nodes, unlinked } = buildTaskGraph(tasks, [], { collapseOrphans: true })
    expect(nodes.map((n) => n.id)).toEqual(['p1'])
    expect(unlinked.map((t) => t.id)).toEqual(['lone'])
  })

  it('mixed workspace: multiple connected DAGs and multiple orphans all sort correctly', () => {
    const tasks = [
      makeTask({ id: 'a' }),
      makeTask({ id: 'b', blocked_by: ['a'] }),
      makeTask({ id: 'c' }),
      makeTask({ id: 'd', blocked_by: ['c'] }),
      makeTask({ id: 'x' }),
      makeTask({ id: 'y' }),
    ]
    const { nodes, unlinked } = buildTaskGraph(tasks, [], { collapseOrphans: true })
    expect(nodes.map((n) => n.id).sort()).toEqual(['a', 'b', 'c', 'd'])
    expect(unlinked.map((t) => t.id).sort()).toEqual(['x', 'y'])
  })

  it('never returns an unlinked task that is a subtask or non-user-surface (they were never graphable in the first place)', () => {
    const tasks = [
      makeTask({ id: 'visible' }),
      makeTask({ id: 'hb', surface: 'heartbeat' }),
      makeTask({ id: 'sub', parent_task_id: 'visible' }),
    ]
    const { nodes, unlinked } = buildTaskGraph(tasks, [], { collapseOrphans: true })
    // 'visible' has no blocked_by, no dependent, no plan -> collapses too.
    expect(nodes).toHaveLength(0)
    expect(unlinked.map((t) => t.id)).toEqual(['visible'])
  })

  it('an all-orphan workspace collapses to zero nodes and a full unlinked tray, with no dropped tasks', () => {
    const tasks = [makeTask({ id: 'a' }), makeTask({ id: 'b' }), makeTask({ id: 'c' })]
    const { nodes, edges, unlinked } = buildTaskGraph(tasks, [], { collapseOrphans: true })
    expect(nodes).toHaveLength(0)
    expect(edges).toHaveLength(0)
    expect(unlinked).toHaveLength(3)
  })
})
