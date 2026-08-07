import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Controls,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  reconnectEdge,
  useEdgesState,
  useNodesState,
  useReactFlow,
  type Connection,
  type Edge,
  type EdgeTypes,
  type NodeMouseHandler,
  type NodeTypes,
  type OnConnect,
  type OnReconnect,
} from '@xyflow/react'
import { GraphIcon, Info } from '@phosphor-icons/react'
import { useLibraryTabIndex } from '@/hooks/useLibraryTabIndex'
import type { Task } from '@/lib/api'
import { TaskNode } from './TaskNode'
import { DependencyEdge } from './DependencyEdge'
import {
  buildTaskGraph,
  statusVisual,
  type AgentLike,
  type TaskGraphNode,
} from './taskGraph'
import '@xyflow/react/dist/style.css'
import '../reactflow-theme.css'

// Registered once (stable identity) so React Flow doesn't warn about a new
// nodeTypes/edgeTypes object every render.
const NODE_TYPES: NodeTypes = { task: TaskNode }
const EDGE_TYPES: EdgeTypes = { dependency: DependencyEdge }

// Minimap node colour = the task's status colour, so the overview map reads
// like the canvas at a glance.
function minimapNodeColor(node: TaskGraphNode): string {
  return statusVisual(node.data?.task?.status).color
}

// Shared fitView tuning (UAT #26/S3 fix) — module-scope so both the
// mount-time `fitViewOptions` prop and every imperative `fitView()` call (our
// own re-fit effect below, AND the <Controls> "fit view" button, which is
// wired to the same options) agree on one floor, and so it's a stable object
// identity rather than a fresh literal every render.
//
// `minZoom: 0.8` is the fix for "at 50 nodes auto-fit renders labels at
// 3.22px, unreadable": the node title renders at ~14px at scale 1 (measured:
// the tester's own instrumentation showed rendered label px scales linearly
// with the viewport zoom — 3.22px / 0.23 zoom ≈ 19.59px / 1.40 zoom ≈ 14px
// base). Floors auto-fit's chosen scale at 0.8 keeps the title at >= ~11px —
// a practical legibility floor for compact UI microcopy (comparable to the
// smallest "caption"-tier type most design systems allow, e.g. iOS HIG's
// Caption 2 / Material's label-small). Below that floor, fitView deliberately
// stops trying to cram every node into the viewport and instead leaves the
// canvas LARGER than the viewport — the user pans/scrolls/uses the minimap to
// reach nodes outside the fitted view, rather than everything shrinking to
// illegible dots. The `minZoom={0.2}` interactive floor on <ReactFlow> below
// is unchanged, so a user who deliberately wants to zoom out further than
// this to see the whole graph at once still can — this clamp only bounds the
// automatic fit, never manual zoom.
const GRAPH_FIT_VIEW_OPTIONS = { padding: 0.25, maxZoom: 1.1, minZoom: 0.8 }

interface GraphViewProps {
  tasks: Task[]
  agents: AgentLike[]
  /** Opens the task detail (slide-over) — mirrors the Board's onTaskClick. */
  onTaskClick: (task: Task) => void
  /** Id of the currently-open task, so its node renders selected. */
  selectedTaskId?: string | null
  /**
   * Scope the canvas to a single Plan's member tasks + edges (ADR-051
   * plans-as-filter — `PlansFilterBand`'s tile selection sets the shared plan
   * filter and the Graph reads it). `null`/`undefined` = whole-workspace
   * "All" mode.
   */
  planId?: string | null
  /**
   * Whole-workspace "All" mode only (ignored when `planId` is set): exclude
   * non-DAG-relevant (unlinked) tasks from the canvas instead of rendering a
   * field of disconnected one-time-task dots. There is no separate tray UI —
   * the excluded count is surfaced inline via `GraphUnlinkedNotice` whenever
   * `buildTaskGraph`'s `unlinked` comes back non-empty. Defaults to false so
   * existing callers/tests that render every visible task as a node are
   * unaffected — the real Graph tab (WorkspaceGraphTab) opts in explicitly.
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
   * Remove a dependency edge (via its inline × button, or select-then-Backspace),
   * called with the same `(blockerId, blockedId)` pair the edge represents. When
   * provided, edges render as the custom `dependency` type (with the × button);
   * omit to keep edges plain and non-deletable.
   */
  onRemoveDependency?: (blockerId: string, blockedId: string) => void
  /**
   * Reconnect a dependency by dragging an edge endpoint onto another task.
   * Called with the OLD and the NEW `(blocker, blocked)` pair — the handler
   * removes the old edge and adds the new one (same-plan-guarded). When
   * provided, edges become reconnectable.
   */
  onReconnectDependency?: (
    oldDep: { blocker: string; blocked: string },
    newDep: { blocker: string; blocked: string },
  ) => void
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
  onReconnectDependency,
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

  // Same stable-identity treatment for the edge remove callback so injecting it
  // into edge `data` (edgesWithData below) doesn't churn the re-seed effect on
  // every parent re-render (the parent passes a fresh closure each time).
  // `editable` is a stable boolean (presence, not identity) gating the custom
  // edge type + its × button.
  const onRemoveRef = useRef(onRemoveDependency)
  onRemoveRef.current = onRemoveDependency
  const stableRemove = useCallback(
    (blocker: string, blocked: string) => onRemoveRef.current?.(blocker, blocked),
    [],
  )
  const editable = !!onRemoveDependency

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

  // Inject the (stable) remove callback into each edge's data and switch them
  // to the custom `dependency` type (the one with the × button) — but only when
  // editing is enabled, so read-only canvases (tests / non-editable embeddings)
  // keep the plain built-in smoothstep edge and its existing behaviour.
  const edgesWithData = useMemo<Edge[]>(
    () =>
      editable
        ? layout.edges.map((e) => ({
            ...e,
            type: 'dependency',
            data: { ...e.data, onRemove: stableRemove },
          }))
        : layout.edges,
    [layout.edges, editable, stableRemove],
  )

  const [nodes, setNodes, onNodesChange] = useNodesState<TaskGraphNode>(nodesWithOpen)
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>(edgesWithData)
  const { fitView } = useReactFlow<TaskGraphNode>()

  // Re-seed React Flow state whenever the computed layout changes (task added /
  // status changed / agent resolved / dependency edited). React Flow owns
  // interactive positions after mount, so we reset from the fresh dagre layout
  // on data change. All deps here are identity-stable across a parent re-render
  // that only passes fresh callback closures — see `stableOnOpen`/`stableRemove`
  // above — so this never fires just because the parent re-rendered.
  useEffect(() => {
    setNodes(nodesWithOpen)
    setEdges(edgesWithData)
  }, [nodesWithOpen, edgesWithData, setNodes, setEdges])

  // S2 UAT fix (#25) — re-fit the viewport whenever the rendered node SET
  // changes (the plan-scope filter narrowing "All" down to one plan, or
  // widening back). `fitView` on <ReactFlow> below only ever applies once, at
  // mount, so without this the canvas kept whatever scale/pan it had before
  // the scope change: scoping 50 tasks down to 3 left the camera at the
  // whole-workspace 0.23 zoom (the 3 nodes filling 0.5% of the canvas), and a
  // manual fit-view on that 3-node scope followed by scoping back to "All"
  // left the camera at that 3-node 1.40 zoom with 48 of 50 nodes clipped
  // off-screen — no cue but the minimap.
  //
  // Keyed on the SET of node ids (sorted, so member order never matters),
  // not on `layout.nodes`/`nodesWithOpen` themselves — those are fresh
  // objects on every data change (a status edit, a re-resolved agent name),
  // and re-fitting on every one of those would yank a user's deliberate
  // pan/zoom out from under them for no reason. Only an actual change in
  // WHICH tasks are visible re-fits; the previous key is tracked in a ref
  // (not state) so comparing it never itself triggers a render, and the
  // very first run (mount, `prevKey === undefined`) is skipped — the
  // `fitView` prop already frames the initial layout.
  const nodeIdsKey = useMemo(
    () => layout.nodes.map((n) => n.id).sort().join('␟'),
    [layout.nodes],
  )
  const prevNodeIdsKeyRef = useRef<string | undefined>(undefined)
  useEffect(() => {
    const prevKey = prevNodeIdsKeyRef.current
    prevNodeIdsKeyRef.current = nodeIdsKey
    if (prevKey === undefined || prevKey === nodeIdsKey) return
    void fitView(GRAPH_FIT_VIEW_OPTIONS)
  }, [nodeIdsKey, fitView])

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

  // Drag an edge endpoint onto another task to MOVE the dependency. Optimistically
  // reconnect the edge locally so it doesn't snap back before the refetch, then
  // hand the old + new (blocker, blocked) pairs to the parent, which persists the
  // move (same-plan-guarded) and re-seeds the canvas to server truth on settle —
  // so a rejected move reverts cleanly.
  const handleReconnect = useCallback<OnReconnect>(
    (oldEdge, newConnection) => {
      const { source, target } = newConnection
      if (!source || !target || source === target) return
      setEdges((els) => reconnectEdge(oldEdge, newConnection, els))
      onReconnectDependency?.(
        { blocker: oldEdge.source, blocked: oldEdge.target },
        { blocker: source, blocked: target },
      )
    },
    [onReconnectDependency, setEdges],
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

  // Nothing to graph: no DAG-relevant nodes. Tasks that are neither a member
  // of a Plan nor linked to another task by a dependency (`layout.unlinked`)
  // are deliberately NOT rendered on the DAG canvas — they live on the
  // Board/List — so an all-unlinked workspace shows the empty state here
  // rather than a canvas full of nothing. That empty state distinguishes
  // "genuinely no tasks" from "all-unlinked" via `layout.unlinked.length`
  // (see `GraphEmptyState`'s `unlinkedCount` prop) so it never claims zero
  // tasks exist when some do, just none of them qualify for the canvas. When
  // SOME (but not all) visible tasks are unlinked, `layout.nodes.length > 0`
  // so we fall through to the canvas below, which surfaces the excluded
  // count via the `graph-unlinked-notice` banner (S3 UAT fix #9) instead of
  // letting them vanish with no cue.
  if (layout.nodes.length === 0) {
    return <GraphEmptyState unlinkedCount={layout.unlinked.length} />
  }

  return (
    <div ref={canvasRef} className="absolute inset-0">
      <ReactFlow
        className="sovereign-flow"
        nodes={nodes}
        edges={edges}
        nodeTypes={NODE_TYPES}
        edgeTypes={EDGE_TYPES}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onNodeClick={handleNodeClick}
        onConnect={handleConnect}
        onEdgesDelete={handleEdgesDelete}
        onReconnect={handleReconnect}
        edgesReconnectable={!!onReconnectDependency}
        isValidConnection={isValidConnection}
        fitView
        fitViewOptions={GRAPH_FIT_VIEW_OPTIONS}
        minZoom={0.2}
        maxZoom={1.75}
        proOptions={{ hideAttribution: false }}
        nodesConnectable={!!onConnectDependency}
        nodesDraggable
        deleteKeyCode={onRemoveDependency ? ['Backspace', 'Delete'] : null}
        elementsSelectable
        defaultEdgeOptions={{ type: 'smoothstep' }}
      >
        <Controls
          showInteractive={false}
          fitViewOptions={GRAPH_FIT_VIEW_OPTIONS}
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
      {layout.unlinked.length > 0 && <GraphUnlinkedNotice count={layout.unlinked.length} />}
    </div>
  )
}

/**
 * S3 UAT fix (#9) — when the plan-scope filter is off and `collapseOrphans`
 * hid some (but not all) visible tasks off the canvas because they carry no
 * dependency and no plan membership, say so. The all-unlinked case already
 * has its own full `GraphEmptyState`; this is the partial case, which
 * previously dropped the excluded tasks with zero count and zero hint —
 * `taskGraph.ts`'s `buildTaskGraph` already computes exactly this number via
 * `unlinked`, it just never reached the screen.
 */
function GraphUnlinkedNotice({ count }: { count: number }) {
  return (
    <div
      data-testid="graph-unlinked-notice"
      className="pointer-events-none absolute left-1/2 top-3 z-10 -translate-x-1/2"
    >
      <div className="pointer-events-auto flex items-center gap-1.5 rounded-full border border-[var(--color-border)] bg-[var(--color-surface-2)]/95 px-3 py-1 text-[11px] text-[var(--color-muted)] shadow-[0_2px_8px_rgba(0,0,0,0.35)] backdrop-blur">
        <Info size={12} weight="fill" className="shrink-0 text-[var(--color-accent)]" />
        <span>
          {count} {count === 1 ? 'task' : 'tasks'} not shown here — not in a plan and not
          linked by a dependency. See the Board or List.
        </span>
      </div>
    </div>
  )
}

/**
 * Tasteful empty state — points the user to the Board to create work.
 *
 * `unlinkedCount` distinguishes two truly different situations that both
 * land here via `layout.nodes.length === 0`: genuinely zero graphable tasks
 * (`unlinkedCount` 0/undefined — "No tasks yet" is accurate) vs. every
 * visible task existing but none qualifying for the canvas because none is
 * in a Plan or linked by a dependency (`unlinkedCount > 0` — "No tasks yet"
 * would be false, since `layout.unlinked` proves tasks DO exist). Same
 * qualifying-conditions vocabulary as `GraphUnlinkedNotice` so the two
 * surfaces tell one consistent story.
 */
function GraphEmptyState({ unlinkedCount = 0 }: { unlinkedCount?: number }) {
  const hasUnlinked = unlinkedCount > 0
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
          {hasUnlinked ? 'No dependencies to graph yet' : 'No tasks yet'}
        </h2>
        <p className="mt-2 text-sm leading-relaxed text-[var(--color-muted)]">
          {hasUnlinked
            ? 'These tasks are not in a plan and not linked by a dependency, so there is nothing to graph yet. Add either on the Board and they will appear here — laid out left to right, with live status colour and a traceable critical path.'
            : 'Create a task on the Board and its dependencies will graph here — laid out left to right, with live status colour and a traceable critical path.'}
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
