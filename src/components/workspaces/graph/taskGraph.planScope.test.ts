/**
 * Tests for `buildTaskGraph`'s plan-scoping (Plan Swimlane board redesign,
 * fix #1): when `options.planId` is set, the graph shows only that plan's
 * member tasks (`task.plan_id === planId`) and the `blocked_by` edges between
 * them — never orphan-collapsed (plan membership IS the scope), and still
 * subject to the existing surface/subtask visibility rule.
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

describe('buildTaskGraph — plan scoping', () => {
  it('shows only the given plan\'s member tasks, dropping tasks in other plans and unplanned tasks', () => {
    const tasks = [
      makeTask({ id: 'a', plan_id: 'plan-1' }),
      makeTask({ id: 'b', plan_id: 'plan-1' }),
      makeTask({ id: 'c', plan_id: 'plan-2' }),
      makeTask({ id: 'd' }), // no plan at all
    ]
    const { nodes } = buildTaskGraph(tasks, [], { planId: 'plan-1' })
    expect(nodes.map((n) => n.id).sort()).toEqual(['a', 'b'])
  })

  it('draws edges only between members of the scoped plan; drops an edge to a blocker outside the plan', () => {
    const tasks = [
      makeTask({ id: 'a', plan_id: 'plan-1' }),
      makeTask({ id: 'b', plan_id: 'plan-1', blocked_by: ['a'] }),
      makeTask({ id: 'outside' }), // not a member of plan-1
      makeTask({ id: 'c', plan_id: 'plan-1', blocked_by: ['outside'] }),
    ]
    const { nodes, edges } = buildTaskGraph(tasks, [], { planId: 'plan-1' })
    expect(nodes.map((n) => n.id).sort()).toEqual(['a', 'b', 'c'])
    // Only the a->b edge survives — the edge into 'c' pointed at a blocker
    // ('outside') that isn't a member of the scoped plan.
    expect(edges).toHaveLength(1)
    expect(edges[0]).toMatchObject({ source: 'a', target: 'b' })
  })

  it('an unrelated task never leaks in just because it blocks a plan member', () => {
    // 'outside' is NOT a plan-1 member, even though it blocks plan member 'b'.
    // Plan scope means membership, not "reachable via the DAG".
    const tasks = [
      makeTask({ id: 'outside' }),
      makeTask({ id: 'b', plan_id: 'plan-1', blocked_by: ['outside'] }),
    ]
    const { nodes, edges } = buildTaskGraph(tasks, [], { planId: 'plan-1' })
    expect(nodes.map((n) => n.id)).toEqual(['b'])
    expect(edges).toHaveLength(0)
  })

  it('returns no unlinked tasks when plan-scoped — orphan collapsing does not apply to plan scope', () => {
    const tasks = [
      makeTask({ id: 'a', plan_id: 'plan-1' }), // lone member, no edges
    ]
    // `collapseOrphans` isn't even representable alongside `planId` anymore
    // (BuildTaskGraphOptions is a discriminated union, review-gate fix #6) —
    // this now exercises the same guarantee via the plain plan-scope call.
    const { unlinked, nodes } = buildTaskGraph(tasks, [], { planId: 'plan-1' })
    expect(unlinked).toHaveLength(0)
    // Still rendered as a node — plan scope never collapses its own members.
    expect(nodes.map((n) => n.id)).toEqual(['a'])
  })

  it('an empty/nonexistent plan scope yields zero nodes, zero edges, zero unlinked', () => {
    const tasks = [makeTask({ id: 'a', plan_id: 'other-plan' })]
    const { nodes, edges, unlinked } = buildTaskGraph(tasks, [], { planId: 'plan-1' })
    expect(nodes).toHaveLength(0)
    expect(edges).toHaveLength(0)
    expect(unlinked).toHaveLength(0)
  })

  it('still applies the surface/subtask visibility rule inside a plan scope (matches the Board)', () => {
    const tasks = [
      makeTask({ id: 'a', plan_id: 'plan-1' }),
      makeTask({ id: 'hb', plan_id: 'plan-1', surface: 'heartbeat' }),
      makeTask({ id: 'sub', plan_id: 'plan-1', parent_task_id: 'a' }),
    ]
    const { nodes } = buildTaskGraph(tasks, [], { planId: 'plan-1' })
    expect(nodes.map((n) => n.id)).toEqual(['a'])
  })

  it('resolves agent avatar info the same way in plan scope as in the unscoped graph', () => {
    const tasks = [makeTask({ id: 'a', plan_id: 'plan-1', agent_id: 'mia' })]
    const agents = [{ id: 'mia', name: 'Mia', color: '#d4af37', icon: 'Robot' }]
    const { nodes } = buildTaskGraph(tasks, agents, { planId: 'plan-1' })
    expect(nodes[0].data.agentName).toBe('Mia')
    expect(nodes[0].data.agentColor).toBe('#d4af37')
  })
})
