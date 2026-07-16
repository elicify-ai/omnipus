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
  type Edge,
  type NodeMouseHandler,
  type NodeTypes,
} from '@xyflow/react'
import { GraphIcon } from '@phosphor-icons/react'
import type { Task } from '@/lib/api'
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
}

/**
 * The Task DAG canvas. Lays the workspace's top-level tasks out left→right by
 * `blocked_by` dependency (dagre LR), renders each as a Sovereign Deep
 * TaskNode, and wires pan/zoom + minimap + controls. Clicking a node opens the
 * task detail via `onTaskClick`.
 *
 * Layout is memoised on the tasks/agents identity and only recomputed when
 * those change — performant for ~50–100 nodes.
 */
function GraphViewInner({ tasks, agents, onTaskClick, selectedTaskId }: GraphViewProps) {
  const layout = useMemo(() => buildTaskGraph(tasks, agents), [tasks, agents])

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

  if (layout.nodes.length === 0) {
    return <GraphEmptyState />
  }

  return (
    <div className="absolute inset-0">
      <ReactFlow
        className="sovereign-flow"
        nodes={nodes}
        edges={edges}
        nodeTypes={NODE_TYPES}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onNodeClick={handleNodeClick}
        fitView
        fitViewOptions={{ padding: 0.25, maxZoom: 1.1 }}
        minZoom={0.2}
        maxZoom={1.75}
        proOptions={{ hideAttribution: false }}
        nodesConnectable={false}
        nodesDraggable
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
