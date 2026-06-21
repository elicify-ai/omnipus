// Task DAG graph model + auto-layout.
//
// Pure, side-effect-free helpers shared by the Graph view and its tests:
//   - the 7-state status → colour/label map (Sovereign Deep palette),
//   - the priority map,
//   - `buildTaskGraph`, which turns a list of tasks into dagre-laid-out
//     React Flow nodes + edges (blocker → blocked, left→right).
//
// Keeping this separate from the React component means the layout is unit
// testable without mounting React Flow (which needs a real ResizeObserver).

import dagre from '@dagrejs/dagre'
import { MarkerType, Position, type Edge, type Node } from '@xyflow/react'
import type { Task } from '@/lib/api'
import {
  STATUS_ANIMATED,
  STATUS_COLORS,
  STATUS_LABELS,
  STATUS_MUTED,
  type TaskStatus as SharedTaskStatus,
} from '@/lib/statusColors'

export type TaskStatus = Task['status']

/** Per-status presentation: chip label + the accent colour driving the node. */
export interface StatusVisual {
  label: string
  /** Primary hex colour for the chip / glow / edge of this status. */
  color: string
  /** True for statuses whose incident edges should animate (live work). */
  animated: boolean
  /** True for terminal/quiet statuses whose edges are muted. */
  muted: boolean
}

// 7-state lifecycle, projected from the single source of truth in
// `@/lib/statusColors` so the Graph, Board, roll-ups, and List can never drift.
// in_progress is Forge Gold (#D4AF37) — the marquee "live work" accent.
export const STATUS_VISUALS: Record<TaskStatus, StatusVisual> = Object.fromEntries(
  (Object.keys(STATUS_COLORS) as SharedTaskStatus[]).map((s) => [
    s,
    {
      label: STATUS_LABELS[s],
      color: STATUS_COLORS[s],
      animated: STATUS_ANIMATED[s],
      muted: STATUS_MUTED[s],
    },
  ]),
) as Record<TaskStatus, StatusVisual>

/** Resolve a status to its visual, tolerating unknown values from the wire. */
export function statusVisual(status: TaskStatus | string | undefined): StatusVisual {
  return STATUS_VISUALS[(status as TaskStatus) ?? 'inbox'] ?? STATUS_VISUALS.inbox
}

export const PRIORITY_LABELS: Record<number, string> = {
  1: 'P1',
  2: 'P2',
  3: 'P3',
  4: 'P4',
  5: 'P5',
}

/** The data each TaskNode renders. Lives on `node.data`. */
export interface TaskNodeData extends Record<string, unknown> {
  task: Task
  /** Resolved agent display name (registry name wins over the id). */
  agentName?: string
  /** Resolved agent avatar colour (hex). */
  agentColor?: string
  /** Resolved agent Phosphor icon name. */
  agentIcon?: string
}

export type TaskGraphNode = Node<TaskNodeData, 'task'>

// Node box dimensions handed to dagre. Must match the rendered TaskNode so the
// auto-layout reserves the right footprint and edges land on the handles.
export const NODE_WIDTH = 248
export const NODE_HEIGHT = 96

/** A minimal view of an agent for avatar resolution (subset of the wire Agent). */
export interface AgentLike {
  id: string
  name?: string
  color?: string
  icon?: string
}

/**
 * Build a left→right dependency DAG from the workspace's tasks.
 *
 * Nodes are top-level user-surface tasks; edges go blocker → blocked (an edge
 * for every `blocked_by` entry whose blocker is also visible). Dagre lays the
 * graph out with rank direction LR. Returns positioned React Flow nodes +
 * styled edges. Pure: same input → same output, no DOM access.
 */
export function buildTaskGraph(
  tasks: Task[],
  agents: AgentLike[] = [],
): { nodes: TaskGraphNode[]; edges: Edge[] } {
  // Match the Board: only top-level, user-surface tasks are graphed.
  const visible = tasks.filter(
    (t) =>
      (t.surface === 'user' || t.surface === undefined) && t.parent_task_id == null,
  )
  const visibleIds = new Set(visible.map((t) => t.id))
  const agentById = new Map(agents.map((a) => [a.id, a]))

  // ── Build the dagre graph for positioning ──────────────────────────────────
  const g = new dagre.graphlib.Graph()
  g.setDefaultEdgeLabel(() => ({}))
  g.setGraph({
    rankdir: 'LR',
    nodesep: 36, // vertical gap between siblings in a rank
    ranksep: 96, // horizontal gap between ranks (dependency depth)
    marginx: 24,
    marginy: 24,
  })

  for (const task of visible) {
    g.setNode(task.id, { width: NODE_WIDTH, height: NODE_HEIGHT })
  }

  interface PendingEdge {
    from: string
    to: string
  }
  const pendingEdges: PendingEdge[] = []
  for (const task of visible) {
    for (const blockerId of task.blocked_by ?? []) {
      // Defensive: a blocker hidden by the surface/parent filter (or deleted)
      // never yields a dangling edge — we only draw an edge when its blocker is
      // also a visible node.
      if (!visibleIds.has(blockerId)) continue
      g.setEdge(blockerId, task.id)
      pendingEdges.push({ from: blockerId, to: task.id })
    }
  }

  dagre.layout(g)

  // ── Project dagre output into React Flow nodes ──────────────────────────────
  const nodes: TaskGraphNode[] = visible.map((task) => {
    const pos = g.node(task.id)
    const agent = task.agent_id ? agentById.get(task.agent_id) : undefined
    return {
      id: task.id,
      type: 'task',
      // dagre centres nodes; React Flow positions by top-left corner.
      position: {
        x: (pos?.x ?? 0) - NODE_WIDTH / 2,
        y: (pos?.y ?? 0) - NODE_HEIGHT / 2,
      },
      data: {
        task,
        agentName: task.agent_name ?? agent?.name ?? task.agent_id,
        agentColor: agent?.color,
        agentIcon: agent?.icon,
      },
      sourcePosition: Position.Right,
      targetPosition: Position.Left,
    }
  })

  // ── Style edges by the *blocked* (target) task's status ────────────────────
  const taskById = new Map(visible.map((t) => [t.id, t]))
  const edges: Edge[] = pendingEdges.map(({ from, to }) => {
    const target = taskById.get(to)
    const visual = statusVisual(target?.status)
    return {
      id: `${from}->${to}`,
      source: from,
      target: to,
      type: 'smoothstep',
      animated: visual.animated,
      style: {
        stroke: visual.color,
        strokeWidth: 1.5,
        opacity: visual.muted ? 0.35 : 0.85,
      },
      markerEnd: {
        type: MarkerType.ArrowClosed,
        color: visual.color,
        width: 16,
        height: 16,
      },
    }
  })

  return { nodes, edges }
}
