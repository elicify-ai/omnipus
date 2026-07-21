/**
 * Tests for the Task DAG layout builder (buildTaskGraph) and status mapping.
 *
 * These exercise the pure layout logic — no React Flow mount required — so they
 * are deterministic and fast. They cover: a node per visible task, an edge per
 * resolvable blocked_by dependency, edge direction (blocker → blocked), orphan
 * edge dropping, surface/subtask filtering, agent-avatar resolution, and the
 * 7-state status colour map.
 */

import { describe, it, expect } from 'vitest'
import { buildTaskGraph, statusVisual, taskNodeVisual, STATUS_VISUALS, type AgentLike } from './taskGraph'
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

describe('buildTaskGraph — nodes', () => {
  it('renders one node per visible task', () => {
    const tasks = [
      makeTask({ id: 'a', title: 'Alpha' }),
      makeTask({ id: 'b', title: 'Bravo' }),
      makeTask({ id: 'c', title: 'Charlie' }),
    ]
    const { nodes } = buildTaskGraph(tasks)
    expect(nodes).toHaveLength(3)
    expect(nodes.map((n) => n.id).sort()).toEqual(['a', 'b', 'c'])
    // Each node carries its task + the 'task' node type.
    for (const n of nodes) {
      expect(n.type).toBe('task')
      expect(n.data.task.id).toBe(n.id)
    }
  })

  it('assigns finite, non-overlapping positions (dagre laid out)', () => {
    const tasks = [
      makeTask({ id: 'a' }),
      makeTask({ id: 'b', blocked_by: ['a'] }),
    ]
    const { nodes } = buildTaskGraph(tasks)
    for (const n of nodes) {
      expect(Number.isFinite(n.position.x)).toBe(true)
      expect(Number.isFinite(n.position.y)).toBe(true)
    }
    // LR layout: the blocked task (b) sits to the right of its blocker (a).
    const a = nodes.find((n) => n.id === 'a')!
    const b = nodes.find((n) => n.id === 'b')!
    expect(b.position.x).toBeGreaterThan(a.position.x)
  })

  it('hides non-user-surface tasks and subtasks (matches the Board)', () => {
    const tasks = [
      makeTask({ id: 'visible' }),
      makeTask({ id: 'hb', surface: 'heartbeat' }),
      makeTask({ id: 'sub', parent_task_id: 'visible' }),
    ]
    const { nodes } = buildTaskGraph(tasks)
    expect(nodes.map((n) => n.id)).toEqual(['visible'])
  })

  it('treats undefined surface as user-visible', () => {
    const tasks = [makeTask({ id: 'a', surface: undefined as unknown as Task['surface'] })]
    const { nodes } = buildTaskGraph(tasks)
    expect(nodes).toHaveLength(1)
  })
})

describe('buildTaskGraph — edges', () => {
  it('creates an edge per blocked_by dependency, blocker → blocked', () => {
    const tasks = [
      makeTask({ id: 'a', title: 'blocker' }),
      makeTask({ id: 'b', title: 'blocked', blocked_by: ['a'] }),
    ]
    const { edges } = buildTaskGraph(tasks)
    expect(edges).toHaveLength(1)
    // Edge runs FROM the blocker (a) TO the blocked task (b).
    expect(edges[0].source).toBe('a')
    expect(edges[0].target).toBe('b')
  })

  it('creates one edge per dependency when a task has several blockers', () => {
    const tasks = [
      makeTask({ id: 'a' }),
      makeTask({ id: 'b' }),
      makeTask({ id: 'c', blocked_by: ['a', 'b'] }),
    ]
    const { edges } = buildTaskGraph(tasks)
    expect(edges).toHaveLength(2)
    expect(edges.map((e) => e.id).sort()).toEqual(['a->c', 'b->c'])
  })

  it('drops orphan edges whose blocker is not visible', () => {
    const tasks = [
      // blocker 'ghost' is filtered out (heartbeat) → its edge must be dropped.
      makeTask({ id: 'ghost', surface: 'heartbeat' }),
      makeTask({ id: 'b', blocked_by: ['ghost', 'missing'] }),
    ]
    const { nodes, edges } = buildTaskGraph(tasks)
    expect(nodes.map((n) => n.id)).toEqual(['b'])
    expect(edges).toHaveLength(0)
  })

  it('styles in_progress edges as animated, done edges as muted', () => {
    const tasks = [
      makeTask({ id: 'a' }),
      makeTask({ id: 'live', status: 'in_progress', blocked_by: ['a'] }),
      makeTask({ id: 'fin', status: 'done', blocked_by: ['a'] }),
    ]
    const { edges } = buildTaskGraph(tasks)
    const live = edges.find((e) => e.target === 'live')!
    const fin = edges.find((e) => e.target === 'fin')!
    expect(live.animated).toBe(true)
    expect(fin.animated).toBe(false)
    // Muted (done) edges are more transparent than live ones.
    const liveOpacity = Number((live.style as { opacity: number }).opacity)
    const finOpacity = Number((fin.style as { opacity: number }).opacity)
    expect(finOpacity).toBeLessThan(liveOpacity)
  })
})

describe('buildTaskGraph — agent avatar resolution', () => {
  it('resolves agent colour + icon from the agents cache', () => {
    const agents: AgentLike[] = [
      { id: 'mia', name: 'Mia', color: '#d4af37', icon: 'Robot' },
    ]
    const tasks = [makeTask({ id: 'a', agent_id: 'mia' })]
    const { nodes } = buildTaskGraph(tasks, agents)
    expect(nodes[0].data.agentName).toBe('Mia')
    expect(nodes[0].data.agentColor).toBe('#d4af37')
    expect(nodes[0].data.agentIcon).toBe('Robot')
  })

  it('falls back to the wire agent_name then agent_id when uncached', () => {
    const tasks = [makeTask({ id: 'a', agent_id: 'unknown', agent_name: 'Wire Name' })]
    const { nodes } = buildTaskGraph(tasks, [])
    expect(nodes[0].data.agentName).toBe('Wire Name')
  })
})

describe('buildTaskGraph — node tab order (position-sorted, not fetch order)', () => {
  it('sorts nodes by layout position (x, then y) for stable LR tab order, independent of fetch order', () => {
    // A dependency chain a -> b -> c, fed in REVERSED order — dagre still
    // ranks 'a' leftmost (no blockers), 'c' rightmost (blocked by both 'b'
    // and, transitively, 'a'), so the pre-fix node array (built via
    // `visible.map`) came out in fetch order [c, b, a] — the opposite of the
    // visual left-to-right layout.
    const tasks = [
      makeTask({ id: 'c', title: 'Charlie', blocked_by: ['b'] }),
      makeTask({ id: 'b', title: 'Bravo', blocked_by: ['a'] }),
      makeTask({ id: 'a', title: 'Alpha' }),
    ]
    const { nodes } = buildTaskGraph(tasks)
    expect(nodes.map((n) => n.id)).toEqual(['a', 'b', 'c'])
  })

  it('holds the (x, then y) sort invariant generally, not just for this fixture', () => {
    const tasks = [
      makeTask({ id: 'c', blocked_by: ['b'] }),
      makeTask({ id: 'b', blocked_by: ['a'] }),
      makeTask({ id: 'a' }),
      makeTask({ id: 'd', blocked_by: ['a'] }),
    ]
    const { nodes } = buildTaskGraph(tasks)
    for (let i = 1; i < nodes.length; i++) {
      const prev = nodes[i - 1].position
      const cur = nodes[i].position
      expect(prev.x < cur.x || (prev.x === cur.x && prev.y <= cur.y)).toBe(true)
    }
  })
})

describe('buildTaskGraph — empty', () => {
  it('returns no nodes or edges for an empty task list', () => {
    const { nodes, edges } = buildTaskGraph([])
    expect(nodes).toHaveLength(0)
    expect(edges).toHaveLength(0)
  })
})

describe('statusVisual', () => {
  it('maps every lifecycle state to a distinct colour', () => {
    const colors = Object.values(STATUS_VISUALS).map((v) => v.color)
    expect(new Set(colors).size).toBe(colors.length)
  })

  it('uses Forge Gold for in_progress', () => {
    expect(statusVisual('in_progress').color.toLowerCase()).toBe('#d4af37')
  })

  it('falls back to inbox for an unknown status', () => {
    expect(statusVisual('totally-unknown')).toEqual(STATUS_VISUALS.inbox)
  })
})

describe('taskNodeVisual — Cancelled marker (ADR-052 FR-015/US-8, Gate-2 finding #1)', () => {
  it('overrides label/colour to "Cancelled" for a user-stopped task', () => {
    const visual = taskNodeVisual({ status: 'failed', cancel_reason: 'stopped_by_user' })
    expect(visual.label).toBe('Cancelled')
  })

  it('leaves a genuine failure as "Failed"', () => {
    const visual = taskNodeVisual({ status: 'failed', cancel_reason: undefined })
    expect(visual.label).toBe('Failed')
  })

  // Regression: taskGraph.ts previously returned `'var(--color-warning)'` for
  // the Cancelled override. TaskNode.tsx:126 builds the chip's tint fill via
  // a bare string concat (`${visual.color}1f`) — `var(--color-warning)1f` is
  // invalid CSS (`var()` doesn't accept a trailing token), so the
  // backgroundColor declaration is silently dropped and the chip renders
  // with orange text but no fill. This is the same bug class fixed for
  // TaskCard/PlansFilterBand/WorkspaceGraphTab via TASK_CANCELLED_COLOR
  // (see TaskCard.cancelled.test.tsx) — the graph node is the 4th surface.
  it('taskNodeVisual(cancelledTask).color is a literal hex, not a var(...) reference', () => {
    const visual = taskNodeVisual({ status: 'failed', cancel_reason: 'stopped_by_user' })
    expect(visual.color).not.toMatch(/^var\(/)
    expect(visual.color).toMatch(/^#[0-9a-fA-F]{6}$/)
  })

  it('the chip tint concat (`${color}1f`) yields a valid 8-digit hex string', () => {
    const visual = taskNodeVisual({ status: 'failed', cancel_reason: 'stopped_by_user' })
    const tint = `${visual.color}1f`
    expect(tint).toMatch(/^#[0-9a-fA-F]{8}$/)
  })
})
