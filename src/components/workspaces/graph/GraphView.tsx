import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  type Connection,
  type Edge,
  type NodeMouseHandler,
  type NodeTypes,
  type OnConnect,
} from '@xyflow/react'
import { CaretDown, GraphIcon, StackIcon } from '@phosphor-icons/react'
import { useLibraryTabIndex } from '@/hooks/useLibraryTabIndex'
import type { Task } from '@/lib/api'
import { cn } from '@/lib/utils'
import { TaskNode } from './TaskNode'
import {
  buildTaskGraph,
  statusVisual,
  type AgentLike,
  type TaskGraphNode,
} from './taskGraph'
import '@xyflow/react/dist/style.css'
import '../reactflow-theme.css'

// Registered once (stable identity) so React Flow doesn't warn about a new
// nodeTypes object every render.
const NODE_TYPES: NodeTypes = { task: TaskNode }

// Minimap node colour = the task's status colour, so the overview map reads
// like the canvas at a glance.
function minimapNodeColor(node: TaskGraphNode): string {
  return statusVisual(node.data?.task?.status).color
}

interface GraphViewProps {
  tasks: Task[]
  agents: AgentLike[]
  /** Opens the task detail (slide-over) — mirrors the Board's onTaskClick. */
  onTaskClick: (task: Task) => void
  /** Id of the currently-open task, so its node renders selected. */
  selectedTaskId?: string | null
  /**
   * Scope the canvas to a single Plan's member tasks + edges (Plan Swimlane
   * board redesign — the Board's lane ⑂ button sets this and navigates here).
   * `null`/`undefined` = whole-workspace "All" mode.
   */
  planId?: string | null
  /**
   * Whole-workspace "All" mode only (ignored when `planId` is set): collapse
   * non-DAG-relevant (unlinked) tasks into a single tray instead of rendering
   * a field of disconnected one-time-task dots. Defaults to false so existing
   * callers/tests that render every visible task as a node are unaffected —
   * the real Graph tab (WorkspaceGraphTab) opts in explicitly.
   */
  collapseOrphans?: boolean
  /**
   * Enable mouse-drag dependency creation. Dragging from one node's RIGHT
   * (source) handle to another's LEFT (target) handle calls this with
   * `(blockerId, blockedId)` — "blocker must finish before blocked", the exact
   * `blocked_by` semantics the edges already encode (buildTaskGraph lays edges
   * blocker→blocked). When omitted the canvas is read-only: nodes stay
   * non-connectable (the default for tests / non-editable embeddings).
   */
  onConnectDependency?: (blockerId: string, blockedId: string) => void
  /**
   * Remove a dependency edge (select it, then Backspace/Delete), called with
   * the same `(blockerId, blockedId)` pair the edge represents. Omit to keep
   * edges non-deletable.
   */
  onRemoveDependency?: (blockerId: string, blockedId: string) => void
}

/**
 * The Task DAG canvas. Lays the workspace's top-level tasks out left→right by
 * `blocked_by` dependency (dagre LR), renders each as a Sovereign Deep
 * TaskNode, and wires pan/zoom + minimap + controls. Clicking a node opens the
 * task detail via `onTaskClick`.
 *
 * Layout is memoised on the tasks/agents/planId identity and only recomputed
 * when those change — performant for ~50–100 nodes.
 */
function GraphViewInner({
  tasks,
  agents,
  onTaskClick,
  selectedTaskId,
  planId = null,
  collapseOrphans = false,
  onConnectDependency,
  onRemoveDependency,
}: GraphViewProps) {
  const layout = useMemo(
    () => buildTaskGraph(tasks, agents, planId != null ? { planId } : { collapseOrphans }),
    [tasks, agents, planId, collapseOrphans],
  )

  // `onTaskClick` is commonly passed as a fresh inline arrow function by the
  // parent (e.g. WorkspaceGraphTab's `onTaskClick={(task) => setSelectedTaskId(task.id)}`)
  // — a new identity every render. Route it through a ref + a permanently-stable
  // wrapper so nothing downstream (the per-node `onOpen` below, or the re-seed
  // effect that consumes it) ever depends on that identity. Without this, a
  // node click → parent re-render → new handler → `nodesWithOpen` recomputed →
  // re-seed effect fires → every user-dragged node position snaps back to the
  // dagre layout.
  const onTaskClickRef = useRef(onTaskClick)
  onTaskClickRef.current = onTaskClick
  const stableOnOpen = useCallback((task: Task) => onTaskClickRef.current(task), [])

  // React Flow wraps every node in its OWN focusable element
  // (`.react-flow__node`, role="group" tabIndex=0, with a built-in
  // Enter/Space handler that only toggles selection — it never calls
  // onNodeClick). Making TaskNode's own root focusable too would nest a
  // second tab stop inside that wrapper (WCAG 4.1.2, the same bug fixed on
  // the Board). Instead: mark every node `focusable: false` so React Flow's
  // wrapper drops out of the tab order, and hand each node the stable
  // `onOpen` callback above via `data` so TaskNode itself — now the sole tab
  // stop — can open the task on Enter/Space, calling the exact same
  // `onTaskClick` the mouse path (`handleNodeClick` below) already calls.
  //
  // Deliberately depends on `layout.nodes` (and the *stable* `stableOnOpen`,
  // never the raw `onTaskClick` prop) so this — and the re-seed effect below,
  // which is the only thing that consumes it — only recomputes when the
  // actual graph data changes, not on every parent re-render.
  const nodesWithOpen = useMemo<TaskGraphNode[]>(
    () =>
      layout.nodes.map((n) => ({
        ...n,
        focusable: false,
        // Nodes are never deletable — only dependency EDGES can be removed
        // (Backspace/Delete on a selected edge). Without this, the same delete
        // key would also drop a selected node from the canvas (local-only, then
        // restored on re-seed — a confusing flicker).
        deletable: false,
        data: { ...n.data, onOpen: stableOnOpen },
      })),
    [layout.nodes, stableOnOpen],
  )

  const [nodes, setNodes, onNodesChange] = useNodesState<TaskGraphNode>(nodesWithOpen)
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>(layout.edges)

  // Re-seed React Flow state whenever the computed layout changes (task added /
  // status changed / agent resolved). React Flow owns interactive positions
  // after mount, so we reset from the fresh dagre layout on data change. Both
  // deps here are identity-stable across a parent re-render that only passes
  // a new `onTaskClick` closure — see `stableOnOpen` above — so this never
  // fires just because the parent re-rendered.
  useEffect(() => {
    setNodes(nodesWithOpen)
    setEdges(layout.edges)
  }, [nodesWithOpen, layout.edges, setNodes, setEdges])

  // Reflect external selection (the open slide-over) onto the nodes.
  useEffect(() => {
    setNodes((current) =>
      current.map((n) =>
        n.selected === (n.id === selectedTaskId)
          ? n
          : { ...n, selected: n.id === selectedTaskId },
      ),
    )
  }, [selectedTaskId, setNodes])

  const handleNodeClick = useCallback<NodeMouseHandler>(
    (_event, node) => {
      const task = (node as TaskGraphNode).data?.task
      if (task) onTaskClick(task)
    },
    [onTaskClick],
  )

  // Mouse-drag dependency creation. A handle drag runs source(right) →
  // target(left), so `source` is the blocker and `target` is the blocked task
  // (matches buildTaskGraph's blocker→blocked edge direction). We do NOT
  // optimistically add the edge: the parent's mutation invalidates the tasks
  // query on success and the fresh dagre layout re-seeds the canvas with the
  // persisted edge; on failure nothing changed so nothing needs undoing.
  const handleConnect = useCallback<OnConnect>(
    (conn) => {
      const { source, target } = conn
      if (!source || !target || source === target) return
      onConnectDependency?.(source, target)
    },
    [onConnectDependency],
  )

  // Live drag validation — reject self-links and existing duplicates so the
  // connection line renders invalid before the user drops it. Cross-plan
  // rejection needs plan data the parent owns, so it surfaces as a drop-time
  // toast rather than here.
  const isValidConnection = useCallback(
    (conn: Connection | Edge) => {
      const { source, target } = conn
      if (!source || !target || source === target) return false
      return !edges.some((e) => e.source === source && e.target === target)
    },
    [edges],
  )

  // Edge removal (Backspace/Delete on a selected edge). React Flow drops the
  // edge from local state first; the parent persists the removal and, on
  // EITHER outcome, re-invalidates so the canvas re-seeds to server truth (a
  // failed delete restores the edge).
  const handleEdgesDelete = useCallback(
    (deleted: Edge[]) => {
      for (const e of deleted) {
        if (e.source && e.target) onRemoveDependency?.(e.source, e.target)
      }
    },
    [onRemoveDependency],
  )

  // React Flow renders the <Controls> zoom/fit buttons itself — no JSX site
  // here can carry the repo's explicit-tabIndex convention, so stamp them
  // post-render (WebKit Tab reachability; see useLibraryTabIndex).
  const canvasRef = useRef<HTMLDivElement>(null)
  useLibraryTabIndex(canvasRef)

  // Plan scope with fewer than 2 members can't have a dependency edge at
  // all — mirrors the Board disabling its lane ⑂ button in the same case.
  if (planId != null && layout.nodes.length < 2) {
    return <GraphPlanEmptyState />
  }

  // Truly nothing to show: no DAG-relevant nodes AND nothing collapsed into
  // the tray either (collapseOrphans off, or the workspace is genuinely
  // empty).
  if (layout.nodes.length === 0 && layout.unlinked.length === 0) {
    return <GraphEmptyState />
  }

  return (
    <div ref={canvasRef} className="absolute inset-0">
      <ReactFlow
        className="sovereign-flow"
        nodes={nodes}
        edges={edges}
        nodeTypes={NODE_TYPES}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onNodeClick={handleNodeClick}
        onConnect={handleConnect}
        onEdgesDelete={handleEdgesDelete}
        isValidConnection={isValidConnection}
        fitView
        fitViewOptions={{ padding: 0.25, maxZoom: 1.1 }}
        minZoom={0.2}
        maxZoom={1.75}
        proOptions={{ hideAttribution: false }}
        nodesConnectable={!!onConnectDependency}
        nodesDraggable
        deleteKeyCode={onRemoveDependency ? ['Backspace', 'Delete'] : null}
        elementsSelectable
        defaultEdgeOptions={{ type: 'smoothstep' }}
      >
        <Background
          variant={BackgroundVariant.Dots}
          gap={22}
          size={1}
          color="var(--color-border)"
        />
        <Controls
          showInteractive={false}
          className="!bottom-4 !left-4"
        />
        <MiniMap
          pannable
          zoomable
          nodeColor={minimapNodeColor}
          nodeStrokeWidth={2}
          className="!bottom-4 !right-4"
        />
      </ReactFlow>
      {layout.unlinked.length > 0 && (
        <UnlinkedTasksTray tasks={layout.unlinked} onTaskClick={onTaskClick} />
      )}
    </div>
  )
}

interface UnlinkedTasksTrayProps {
  tasks: Task[]
  onTaskClick: (task: Task) => void
}

/**
 * Attention/Gestalt-grounded collapse of one-time tasks with no DAG structure
 * (see `isDagRelevant` in taskGraph.ts) — a single chip instead of a field of
 * disconnected dots. Expands into a scrollable list on click so the collapsed
 * tasks stay discoverable (recognition, not memory); clicking a listed task
 * opens it via the same `onTaskClick` a node click uses.
 */
function UnlinkedTasksTray({ tasks, onTaskClick }: UnlinkedTasksTrayProps) {
  const [expanded, setExpanded] = useState(false)
  if (tasks.length === 0) return null

  return (
    <div className="absolute left-4 top-4 z-10 max-w-[280px]" data-testid="unlinked-tasks-tray">
      <button tabIndex={0}
        type="button"
        onClick={() => setExpanded((e) => !e)}
        aria-expanded={expanded}
        aria-controls="unlinked-tasks-tray-list"
        className="flex items-center gap-1.5 rounded-full border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-1.5 text-xs font-medium text-[var(--color-secondary)] shadow-[0_2px_8px_rgba(0,0,0,0.35)] transition-colors hover:border-[var(--color-accent)]/50"
      >
        <StackIcon size={13} weight="bold" className="text-[var(--color-muted)]" />
        {tasks.length} unlinked task{tasks.length === 1 ? '' : 's'}
        <CaretDown
          size={11}
          className={cn('text-[var(--color-muted)] transition-transform', expanded && 'rotate-180')}
        />
      </button>

      {expanded && (
        <div
          id="unlinked-tasks-tray-list"
          role="list"
          aria-label="Unlinked tasks"
          className="mt-1.5 max-h-72 overflow-y-auto rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] py-1 shadow-[0_4px_16px_rgba(0,0,0,0.4)]"
        >
          {tasks.map((task) => {
            const visual = statusVisual(task.status)
            return (
              <button tabIndex={0}
                key={task.id}
                type="button"
                role="listitem"
                onClick={() => onTaskClick(task)}
                className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-[var(--color-secondary)] transition-colors hover:bg-[var(--color-surface-2)]"
              >
                <span
                  aria-hidden
                  className="h-1.5 w-1.5 flex-shrink-0 rounded-full"
                  style={{ backgroundColor: visual.color }}
                />
                <span className="truncate">{task.title}</span>
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}

/** Tasteful empty state — points the user to the Board to create work. */
function GraphEmptyState() {
  return (
    <div
      className="absolute inset-0 flex items-center justify-center p-6"
      data-testid="graph-empty-state"
    >
      <div className="flex max-w-sm flex-col items-center text-center">
        <div className="relative mb-5">
          <div className="absolute inset-0 rounded-2xl bg-[var(--color-accent)]/10 blur-xl" />
          <div className="relative flex h-16 w-16 items-center justify-center rounded-2xl border border-[var(--color-accent)]/30 bg-[var(--color-surface-2)]">
            <GraphIcon size={30} weight="duotone" className="text-[var(--color-accent)]" />
          </div>
        </div>
        <h2 className="font-headline text-lg font-bold text-[var(--color-secondary)]">
          No tasks yet
        </h2>
        <p className="mt-2 text-sm leading-relaxed text-[var(--color-muted)]">
          Create a task on the Board and its dependencies will graph here — laid
          out left to right, with live status colour and a traceable critical path.
        </p>
      </div>
    </div>
  )
}

/**
 * Friendly empty state for a plan scoped to fewer than 2 member tasks — a
 * dependency edge needs at least two tasks to exist. Mirrors the Board
 * disabling its lane ⑂ (graph) action in the same case.
 */
function GraphPlanEmptyState() {
  return (
    <div
      className="absolute inset-0 flex items-center justify-center p-6"
      data-testid="graph-plan-empty-state"
    >
      <div className="flex max-w-sm flex-col items-center text-center">
        <div className="relative mb-5">
          <div className="absolute inset-0 rounded-2xl bg-[var(--color-accent)]/10 blur-xl" />
          <div className="relative flex h-16 w-16 items-center justify-center rounded-2xl border border-[var(--color-accent)]/30 bg-[var(--color-surface-2)]">
            <GraphIcon size={30} weight="duotone" className="text-[var(--color-accent)]" />
          </div>
        </div>
        <h2 className="font-headline text-lg font-bold text-[var(--color-secondary)]">
          This plan has no dependencies yet
        </h2>
        <p className="mt-2 text-sm leading-relaxed text-[var(--color-muted)]">
          Add a second task to this plan and link it with a dependency to see
          its DAG here — laid out left to right, with live status colour and a
          traceable critical path.
        </p>
      </div>
    </div>
  )
}

/**
 * Public Graph canvas. Wrapped in `ReactFlowProvider` so the inner view can use
 * React Flow hooks and the minimap/controls share one store instance.
 */
export function GraphView(props: GraphViewProps) {
  // Avoid mounting React Flow during SSR/first paint mismatch; it needs the DOM.
  const [mounted, setMounted] = useState(false)
  useEffect(() => setMounted(true), [])

  if (props.tasks.length > 0 && props.tasks.every((t) => t.surface && t.surface !== 'user')) {
    // All tasks are non-user-surface → nothing to graph.
    return <GraphEmptyState />
  }

  if (!mounted) {
    return <div className="absolute inset-0 bg-[var(--color-surface-0)]" />
  }

  return (
    <ReactFlowProvider>
      <GraphViewInner {...props} />
    </ReactFlowProvider>
  )
}
