// Task DAG graph model + auto-layout.
//
// Pure, side-effect-free helpers shared by the Graph view and its tests:
//   - the 6-state status → colour/label map (Sovereign Deep palette),
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
  TASK_CANCELLED_COLOR,
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

// 6-state lifecycle, projected from the single source of truth in
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

/**
 * The visual a graph NODE renders (ADR-052 FR-015/US-8) — like `statusVisual`,
 * but a user-cancelled task (`status: 'failed'`, `cancel_reason:
 * 'stopped_by_user'`) overrides the label/colour to the orange "Cancelled"
 * marker instead of the genuine red "Failed", so it reads as distinct on the
 * node chip + left rail. Deliberately node-scoped (not folded into
 * `statusVisual`, which also colours dependency EDGES by their target task's
 * status) — extending the cancelled override to edges is out of this wave's
 * scope.
 */
export function taskNodeVisual(task: Pick<Task, 'status' | 'cancel_reason'>): StatusVisual {
  const base = statusVisual(task.status)
  if (task.status === 'failed' && task.cancel_reason === 'stopped_by_user') {
    // Literal hex, not a `var(--color-warning)` reference — TaskNode.tsx:126
    // string-concatenates an alpha suffix onto this value (`${visual.color}1f`);
    // `var(--color-warning)1f` is invalid CSS and silently drops the
    // backgroundColor declaration (Gate-2 finding #1, same bug class as
    // TaskCard/PlansFilterBand/WorkspaceGraphTab — see statusColors.ts's
    // TASK_CANCELLED_COLOR doc comment).
    return { ...base, label: 'Cancelled', color: TASK_CANCELLED_COLOR }
  }
  return base
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
  /**
   * Keyboard/mouse-activation callback GraphView injects per-node at render
   * time (see GraphView.tsx) — `buildTaskGraph` itself never sets this, only
   * the live view does. Typed directly on `TaskNodeData` (rather than a
   * separate `TaskNodeDataWithOpen` cast in TaskNode.tsx) so a RENAME or
   * RETYPE of this field is compiler-caught everywhere it's read (no cast to
   * silently paper over a mismatch). It does NOT catch a DROPPED injection —
   * `onOpen` is optional, so GraphView simply omitting it from `data` at the
   * call site still compiles cleanly; that gap is covered by tests
   * (TaskNode's dev-mode warning when `data.onOpen` is absent, and
   * GraphView.test.tsx's keyboard-operability assertions), not by the type.
   */
  onOpen?: (task: Task) => void
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
 * DAG-relevance test for orphan-collapsing (whole-workspace "All" mode of the
 * Graph tab — ADR-051 (plans-as-filter), psychology-grounded: Attention —
 * a disconnected single node is noise; Gestalt — a connected component is
 * meaning). A task is "DAG-relevant" — worth its own node on the canvas —
 * when it participates in some structure: it depends on something
 * (`blocked_by`), something depends on IT, or it is a member of a Plan (plan
 * membership is itself a grouping structure, even before any `blocked_by`
 * edge exists among the plan's members). Everything else is a one-time,
 * unlinked task that belongs in the "N unlinked tasks" tray, never rendered
 * as a lone dot.
 *
 * `candidates` should be the same population `buildTaskGraph` would render as
 * nodes (already surface/parent-filtered) — a dependent hiding outside that
 * population (a subtask, a heartbeat task) does not count as a visible
 * dependency relationship.
 */
export function isDagRelevant(task: Task, candidates: Task[]): boolean {
  if (task.plan_id) return true
  if ((task.blocked_by?.length ?? 0) > 0) return true
  return candidates.some(
    (other) => other.id !== task.id && (other.blocked_by ?? []).includes(task.id),
  )
}

/**
 * `planId` and `collapseOrphans` are mutually exclusive: plan scope is never
 * orphan-collapsed (the plan boundary IS the scope, so every member renders
 * even with zero edges), and `collapseOrphans` only means anything in the
 * whole-workspace "All" view. A discriminated union makes "both set" a type
 * error instead of a silently-ignored runtime combination.
 */
export type BuildTaskGraphOptions =
  | {
      /**
       * Scope the graph to a single Plan's member tasks (`task.plan_id ===
       * planId`) + the `blocked_by` edges between them (ADR-051 plans-as-filter
       * — plan-scoped graph).
       */
      planId: string
    }
  | {
      planId?: null
      /**
       * Whole-workspace "All" mode: when true, tasks that fail
       * `isDagRelevant` are excluded from `nodes` and returned via
       * `unlinked` instead, so the canvas never shows a field of
       * disconnected one-time-task dots. Defaults to false, preserving the
       * historical "every visible task is a node" behaviour for existing
       * callers/tests.
       */
      collapseOrphans?: boolean
    }

/**
 * Build a left→right dependency DAG from the workspace's tasks.
 *
 * Nodes are top-level user-surface tasks; edges go blocker → blocked (an edge
 * for every `blocked_by` entry whose blocker is also visible). Dagre lays the
 * graph out with rank direction LR. Returns positioned React Flow nodes +
 * styled edges, plus `unlinked` — the tasks collapsed out of the canvas by
 * `collapseOrphans` (always empty otherwise, and always empty in plan scope).
 * Pure: same input → same output, no DOM access.
 */
export function buildTaskGraph(
  tasks: Task[],
  agents: AgentLike[] = [],
  options: BuildTaskGraphOptions = {},
): { nodes: TaskGraphNode[]; edges: Edge[]; unlinked: Task[] } {
  // Can't destructure `collapseOrphans` directly alongside `planId` — it
  // only exists on the union's "no planId" branch, so TS needs the
  // discriminant check inline to narrow `options` before reading it.
  const planId = options.planId ?? null
  const collapseOrphans = options.planId == null ? (options.collapseOrphans ?? false) : false

  // Match the Board: only top-level, user-surface tasks are graphable at all.
  const graphable = tasks.filter(
    (t) =>
      (t.surface === 'user' || t.surface === undefined) && t.parent_task_id == null,
  )

  let visible: Task[]
  let unlinked: Task[]
  if (planId != null) {
    visible = graphable.filter((t) => t.plan_id === planId)
    unlinked = []
  } else if (collapseOrphans) {
    // Single pass — `isDagRelevant` is only evaluated once per task, not
    // once per filter (it itself scans `graphable`, so evaluating it twice
    // per task doubled that cost for no benefit).
    visible = []
    unlinked = []
    for (const t of graphable) {
      if (isDagRelevant(t, graphable)) visible.push(t)
      else unlinked.push(t)
    }
  } else {
    visible = graphable
    unlinked = []
  }

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
  const nodes: TaskGraphNode[] = visible
    .map((task): TaskGraphNode => {
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
    // Tab order must follow the VISUAL (LR) layout, not `tasks`' fetch order —
    // otherwise Tab jumps around the canvas unpredictably. Sort by x (rank /
    // dependency depth) then y (position within a rank), matching reading
    // order for a left-to-right DAG.
    .sort((a, b) => a.position.x - b.position.x || a.position.y - b.position.y)

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

  return { nodes, edges, unlinked }
}
