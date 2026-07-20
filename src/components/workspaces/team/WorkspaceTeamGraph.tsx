import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import {
  ReactFlow,
  ReactFlowProvider,
  Controls,
  Handle,
  Position,
  MarkerType,
  BaseEdge,
  EdgeLabelRenderer,
  getBezierPath,
  applyNodeChanges,
  useConnection,
  type Node,
  type Edge,
  type Connection,
  type NodeProps,
  type NodeChange,
  type EdgeProps,
  type IsValidConnection,
  type OnConnectEnd,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import '../reactflow-theme.css'
import { Star, Lightning, Trash, Warning, PencilSimple, X } from '@phosphor-icons/react'
import { IconRenderer } from '@/components/shared/IconRenderer'
import { cn, initialOf } from '@/lib/utils'
import { EdgeModeEditor, EdgeLabelChip } from './EdgeModeEditor'
import { AgentDelegatePicker } from './AgentDelegatePicker'
import { useLibraryTabIndex } from '@/hooks/useLibraryTabIndex'
import {
  validateConnection,
  rejectionMessageForFailedConnection,
  REJECTION_MESSAGE,
  type DelegationMode,
  type TeamEdgeModel,
  type TeamNodeModel,
  type TeamEditState,
} from './teamGraphModel'

// ── Node / edge data carried into React Flow ─────────────────────────────────
interface AgentNodeData extends Record<string, unknown> {
  model: TeamNodeModel
  onOpenAgent?: (agentId: string) => void
  /** Always supplied by `modelNodes` below (WorkspaceTeamGraphProps.onRemoveMember
   *  is required) — not optional. A node with no way to remove a team member
   *  would be a real gap, not a legitimate "sometimes absent" case. */
  onRemoveMember: (agentId: string) => void
}
interface DelegationEdgeData extends Record<string, unknown> {
  model: TeamEdgeModel
  defaultDepth: number
  onToggleMode: (from: string, to: string, mode: DelegationMode) => void
  onSetDepth: (from: string, to: string, depth: number | undefined) => void
  onDelete: (from: string, to: string) => void
  selected: boolean
  onSelect: (edgeId: string | null) => void
}

type AgentFlowNode = Node<AgentNodeData, 'agent'>
type DelegationFlowEdge = Edge<DelegationEdgeData, 'delegation'>

// ── Canvas-global state, threaded via context (NOT per-node data) ────────────
//
// `allNodes` / `editState` / `workerIds` / `onDelegate` used to be copied into
// every node's own `data` object (see git history) so the keyboard "Delegate…"
// picker (WCAG 2.1.1) could list valid targets with the same `validateConnection`
// predicate the drag gesture uses. That made `modelNodes` (below) rebuild on
// EVERY edge edit — editState changes on every mode toggle / depth change / edge
// add-remove — which produced a brand-new `data` object for EVERY node on EVERY
// edit. xyflow's own `NodeWrapper` (the internal element React Flow renders
// each node inside) is memoised and only re-renders a node when that node's
// object — including its `data` — changes identity, so a fresh `data` object
// for every node on every edit defeated that memoisation for the whole canvas
// over a change that only ever concerns the one node whose Delegate menu is
// open.
//
// Custom nodes render inside the `<ReactFlow>` React tree (not a portal to a
// separate root), so an ordinary context works: a provider wraps the canvas
// with the global state, `AgentNode` reads it here and forwards it to
// `AgentDelegatePicker` as props (that component's own prop API is unchanged —
// it stays directly testable with explicit props, no provider required). This
// keeps `modelNodes` depending only on the actual per-node model, so a node's
// `data` object — and therefore its React Flow node object — stays referentially
// stable across an edge edit that doesn't touch that node.
interface TeamGraphCanvasContextValue {
  allNodes: readonly TeamNodeModel[]
  editState: TeamEditState
  workerIds: ReadonlySet<string>
  /** Create a from→to edge — the identical handler wired to the canvas's
   *  `onConnect`, so a keyboard-created edge shares one mutation path. */
  onDelegate: (from: string, to: string) => void
}

const TeamGraphCanvasContext = createContext<TeamGraphCanvasContextValue | null>(null)

function useTeamGraphCanvasContext(): TeamGraphCanvasContextValue {
  const ctx = useContext(TeamGraphCanvasContext)
  if (!ctx) {
    throw new Error('useTeamGraphCanvasContext must be used within WorkspaceTeamGraph')
  }
  return ctx
}

// Full-node-sized TARGET handle. Fills the node body so the ENTIRE shape is the
// DROP hit-area for COMPLETING a connection (the React Flow target-only
// easy-connect recipe). Invisible, and pointer-events are gated by the caller on
// an in-progress connection: ON while connecting (captures the drop), `none`
// when idle so a body click/drag passes through and MOVES the node instead.
const FULL_TARGET_HANDLE_CLASS =
  'team-full-target !absolute !inset-0 !left-0 !top-0 !h-full !w-full !min-h-0 !min-w-0 !translate-x-0 !translate-y-0 !transform-none !rounded-none !border-0 !bg-transparent !opacity-0'

// ── Custom node: one agent ───────────────────────────────────────────────────
function AgentNode({ id, data }: NodeProps<AgentFlowNode>) {
  const { model } = data
  const initial = initialOf(model.name)
  const { allNodes, editState, workerIds, onDelegate } = useTeamGraphCanvasContext()

  const connection = useConnection()
  const isTarget = connection.inProgress && connection.fromNode?.id !== id
  const targetHandleStyle = {
    pointerEvents: connection.inProgress ? ('all' as const) : ('none' as const),
  }

  // Delegation is BOUNDED, not tier-gated (Sprint-3 backend): ANY real team
  // member may be a delegation SOURCE — including a worker (the backend seeds
  // Planner→Researcher, both workers). Only a GHOST (deleted, no backing agent)
  // can't start a connection. Depth is bounded per-edge in the edge editor.
  const canBeSource = !model.isGhost

  if (model.isGhost) {
    return (
      <div
        data-testid={`team-node-${model.id}`}
        data-ghost="true"
        className={cn(
          'group relative w-[220px] rounded-xl border border-dashed border-[var(--color-warning)]/60 bg-[var(--color-warning)]/5 px-3 py-2.5 shadow-sm transition-colors',
          isTarget && 'border-solid ring-2 ring-[var(--color-warning)]/70',
        )}
        title={`${model.id} no longer exists — its delegation edge is dangling. Click the edge to delete it, or remove this node.`}
      >
        <Handle
          type="target"
          position={Position.Top}
          isConnectableStart={false}
          style={targetHandleStyle}
          className={cn(
            FULL_TARGET_HANDLE_CLASS,
            '!border-[var(--color-warning)] !bg-[var(--color-warning)]',
          )}
        />
        <div className="flex items-center gap-2.5">
          <div
            className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-[var(--color-warning)]/15 text-[var(--color-warning)]"
            aria-hidden="true"
          >
            <Warning size={16} weight="fill" />
          </div>
          <div className="min-w-0 flex-1">
            <span className="block truncate font-headline text-sm font-bold text-[var(--color-warning)]">
              {model.id}
            </span>
            <span className="mt-0.5 block text-[9px] font-medium uppercase tracking-wide text-[var(--color-warning)]/80">
              deleted — dangling edge
            </span>
          </div>
          <button tabIndex={0}
            type="button"
            aria-label={`Remove ${model.id} from team`}
            title="Remove from team"
            className="nodrag shrink-0 rounded p-1 text-[var(--color-warning)]/70 hover:bg-[var(--color-warning)]/15 hover:text-[var(--color-warning)]"
            onClick={(e) => {
              e.stopPropagation()
              data.onRemoveMember(model.id)
            }}
          >
            <X size={13} weight="bold" />
          </button>
        </div>
      </div>
    )
  }

  return (
    <div
      data-testid={`team-node-${model.id}`}
      data-can-source={canBeSource ? 'true' : 'false'}
      role={data.onOpenAgent ? 'button' : undefined}
      tabIndex={data.onOpenAgent ? 0 : undefined}
      onClick={
        data.onOpenAgent
          ? (e) => {
              // Skip clicks that landed on a connection Handle or an action
              // button — those have their own behaviour.
              const t = e.target as HTMLElement
              if (t.closest('[data-handleid]') || t.closest('[data-node-action]')) return
              data.onOpenAgent?.(id)
            }
          : undefined
      }
      onKeyDown={
        data.onOpenAgent
          ? (e) => {
              // Mirror the onClick guard above: Enter/Space on a nested
              // Handle or action button (Delegate…/Edit/Remove) bubbles up
              // to this node's own onKeyDown — without this guard,
              // preventDefault() below cancels the nested control's native
              // activation and opens the agent's edit panel instead of
              // running the action the user actually focused (e.g. Remove).
              const t = e.target as HTMLElement
              if (t.closest('[data-handleid]') || t.closest('[data-node-action]')) return
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                data.onOpenAgent?.(id)
              }
            }
          : undefined
      }
      className={cn(
        'group relative w-[220px] cursor-grab rounded-xl border bg-[var(--color-surface-1)] px-3 py-2.5 shadow-sm transition-colors active:cursor-grabbing',
        'border-[var(--color-border)] hover:border-[var(--color-accent)]/50',
        isTarget && 'border-[var(--color-accent)] ring-2 ring-[var(--color-accent)]/70',
      )}
      title={`Click to edit ${model.name} (applies everywhere). Drag the gold dot onto another agent to delegate. Drag the body to reposition.`}
    >
      {/* Full-node TARGET handle: drop ANYWHERE on this shape to connect TO it. */}
      <Handle
        type="target"
        position={Position.Top}
        isConnectableStart={false}
        style={targetHandleStyle}
        className={cn(
          FULL_TARGET_HANDLE_CLASS,
          '!border-[var(--color-border)] !bg-[var(--color-surface-3)]',
        )}
      />

      {/* SOURCE dot: the ONLY place a connection may START. Non-worker only. */}
      {canBeSource && (
        <Handle
          type="source"
          position={Position.Bottom}
          className="!h-2.5 !w-2.5 !border-[var(--color-accent)] !bg-[var(--color-accent)]"
        />
      )}

      {/* Hover actions (delegate / edit / remove). pointer-events isolated via data-node-action. */}
      <div className="absolute right-1.5 top-1.5 z-10 flex items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100">
        {/* Keyboard equivalent of the drag-to-delegate gesture (WCAG 2.1.1 /
            2.5.7 — creating an edge is otherwise drag-only). */}
        {canBeSource && (
          <AgentDelegatePicker
            source={model}
            nodes={allNodes}
            editState={editState}
            workerIds={workerIds}
            onDelegate={onDelegate}
          />
        )}
        {data.onOpenAgent && (
          <button tabIndex={0}
            type="button"
            data-node-action="edit"
            aria-label={`Edit ${model.name}`}
            title="Edit the global agent"
            className="nodrag rounded p-1 text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]"
            onClick={(e) => {
              e.stopPropagation()
              data.onOpenAgent?.(id)
            }}
          >
            <PencilSimple size={12} weight="bold" />
          </button>
        )}
        {!model.isDefault && (
          <button tabIndex={0}
            type="button"
            data-node-action="remove"
            aria-label={`Remove ${model.name} from team`}
            title="Remove from this workspace's team"
            className="nodrag rounded p-1 text-[var(--color-muted)] hover:bg-[var(--color-error)]/15 hover:text-[var(--color-error)]"
            onClick={(e) => {
              e.stopPropagation()
              data.onRemoveMember(id)
            }}
          >
            <Trash size={12} weight="bold" />
          </button>
        )}
      </div>

      <div className="pointer-events-none flex items-center gap-2.5">
        <div
          className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-sm font-bold text-[var(--color-secondary)]"
          style={{ backgroundColor: model.color ?? 'var(--color-surface-3)' }}
          aria-hidden="true"
        >
          {model.icon ? (
            <IconRenderer icon={model.icon} size={16} className="text-[var(--color-secondary)]" />
          ) : (
            initial
          )}
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className="truncate font-headline text-sm font-bold text-[var(--color-secondary)]">
              {model.name}
            </span>
            {model.isDefault && (
              <Star
                size={12}
                weight="fill"
                className="shrink-0 text-[var(--color-accent)]"
                aria-label="Default agent"
              />
            )}
          </div>
          <div className="mt-0.5 flex items-center gap-1">
            <span className="truncate text-[10px] font-medium text-[var(--color-muted)]">
              {model.role}
            </span>
            {model.isWorker && (
              <span
                className="inline-flex items-center gap-0.5 rounded border border-[var(--color-info)]/40 bg-[var(--color-info)]/10 px-1 py-0.5 text-[9px] font-medium uppercase tracking-wide text-[var(--color-info)]"
                title="Worker — a delegation-only agent. It may both receive work and delegate onward (depth is bounded per edge)."
              >
                <Lightning size={9} weight="fill" /> worker
              </span>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

// ── Custom edge: directed delegation, click → inline modes/depth editor ───────
function DelegationEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  markerEnd,
  data,
}: EdgeProps<DelegationFlowEdge>) {
  const [edgePath, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
  })
  const model = data?.model
  const selected = data?.selected ?? false

  // Focus management for the chip <-> inline editor swap. Opening the editor
  // moves focus INTO it (EdgeModeEditor's own autofocus on its first mode
  // chip, see EdgeModeEditor.tsx). When it CLOSES, React Flow unmounts the
  // editor and mounts the chip in its place — the editor's focused DOM node
  // is simply gone, so without this the browser silently drops focus to
  // <body>. Restore it to the chip that (re)opens the editor, mirroring the
  // AgentDelegatePicker onCloseAutoFocus pattern used elsewhere in this file
  // (its dropdown's default focus-restore is suppressed in favour of
  // explicitly refocusing the trigger).
  const chipRef = useRef<HTMLButtonElement>(null)
  const wasSelectedRef = useRef(selected)
  useEffect(() => {
    if (wasSelectedRef.current && !selected) {
      chipRef.current?.focus()
    }
    wasSelectedRef.current = selected
  }, [selected])

  if (!model || !data) {
    return <BaseEdge id={id} path={edgePath} markerEnd={markerEnd} />
  }

  return (
    <>
      <BaseEdge
        id={id}
        path={edgePath}
        markerEnd={markerEnd}
        style={{
          stroke: selected
            ? 'var(--color-accent)'
            : model.unknownEndpoint
              ? 'var(--color-warning)'
              : 'var(--color-border)',
          strokeWidth: selected ? 2 : 1.5,
        }}
      />
      <EdgeLabelRenderer>
        <div
          data-testid={`team-edge-${model.from}-${model.to}`}
          style={{
            position: 'absolute',
            transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
            pointerEvents: 'all',
          }}
          className="nodrag nopan"
        >
          {selected ? (
            <EdgeModeEditor
              model={model}
              defaultDepth={data.defaultDepth}
              onToggleMode={data.onToggleMode}
              onSetDepth={data.onSetDepth}
              onDelete={data.onDelete}
              onClose={() => data.onSelect(null)}
            />
          ) : (
            <EdgeLabelChip
              ref={chipRef}
              model={model}
              defaultDepth={data.defaultDepth}
              onClick={() => data.onSelect(id)}
            />
          )}
        </div>
      </EdgeLabelRenderer>
    </>
  )
}

const nodeTypes = { agent: AgentNode }
const edgeTypes = { delegation: DelegationEdge }

export interface WorkspaceTeamGraphProps {
  nodes: TeamNodeModel[]
  edges: TeamEdgeModel[]
  workerIds: ReadonlySet<string>
  /** The current edit state — needed for live connection validation. */
  editState: TeamEditState
  /** The workspace's currently-resolved depth ceiling — threaded down to
   *  every edge's inline editor/label so depth always shows a concrete number. */
  defaultDepth: number
  onConnect: (from: string, to: string) => void
  onToggleMode: (from: string, to: string, mode: DelegationMode) => void
  onSetDepth: (from: string, to: string, depth: number | undefined) => void
  onDeleteEdge: (from: string, to: string) => void
  onRemoveMember: (agentId: string) => void
  onRejectConnection: (reason: string) => void
  /** Click a non-ghost node → open the global agent's edit slide-over. */
  onOpenAgent?: (agentId: string) => void
}

function WorkspaceTeamGraphInner({
  nodes,
  edges,
  workerIds,
  editState,
  defaultDepth,
  onConnect,
  onToggleMode,
  onSetDepth,
  onDeleteEdge,
  onRemoveMember,
  onRejectConnection,
  onOpenAgent,
}: WorkspaceTeamGraphProps) {
  const [selectedEdgeId, setSelectedEdgeId] = useState<string | null>(null)

  useEffect(() => {
    if (selectedEdgeId && !edges.some((e) => e.id === selectedEdgeId)) {
      setSelectedEdgeId(null)
    }
  }, [edges, selectedEdgeId])

  // SD-C17 defense-in-depth: resolve a candidate target's Agent `type`
  // straight off the rendered node set so `validateConnection` can reject a
  // System-agent target even though a System agent cannot become a team
  // member through the supported flow (AddAgentPicker already excludes it).
  const isSystemNodeId = useCallback(
    (id: string | null | undefined) => nodes.some((n) => n.id === id && n.type === 'system'),
    [nodes],
  )

  const isValidConnection: IsValidConnection<DelegationFlowEdge> = useCallback(
    (conn) => {
      if (!conn.source || !conn.target) return false
      return validateConnection(conn.source, conn.target, editState, workerIds, isSystemNodeId(conn.target)) === null
    },
    [editState, workerIds, isSystemNodeId],
  )

  const handleConnect = useCallback(
    (conn: Connection) => {
      // `conn.source` is the node the drag STARTED on (the delegator),
      // `conn.target` is where it was DROPPED (the delegate). source → target.
      if (!conn.source || !conn.target) return
      const reason = validateConnection(conn.source, conn.target, editState, workerIds, isSystemNodeId(conn.target))
      if (reason !== null) {
        onRejectConnection(REJECTION_MESSAGE[reason] ?? 'Connection not allowed.')
        return
      }
      onConnect(conn.source, conn.target)
    },
    [editState, workerIds, isSystemNodeId, onConnect, onRejectConnection],
  )

  // Bug fix (live-UAT): React Flow only calls `onConnect` when
  // `isValidConnection` passed — a REJECTED drag (self-edge, duplicate,
  // non-member) never reaches `handleConnect` above at all, so a self-edge
  // drop (e.g. jim → jim) produced no edge and zero feedback, and the drop
  // fell through to the node's own click handler (opening its profile panel)
  // with no explanation. `onConnectEnd` is the one connect-lifecycle event
  // React Flow fires unconditionally, valid or not, so it's the only place a
  // rejected attempt can be observed and surfaced.
  const handleConnectEnd = useCallback<OnConnectEnd>(
    (_event, connectionState) => {
      const message = rejectionMessageForFailedConnection(
        connectionState.fromNode?.id,
        connectionState.toNode?.id,
        connectionState.isValid,
        editState,
        workerIds,
        isSystemNodeId(connectionState.toNode?.id),
      )
      if (message) onRejectConnection(message)
    },
    [editState, workerIds, isSystemNodeId, onRejectConnection],
  )

  // Keyboard "Delegate…" picker (WCAG 2.1.1) → the SAME validated mutation
  // path as a canvas drag: adapts (from, to) into the `Connection` shape
  // `handleConnect` expects (handle ids are irrelevant here — this node has
  // exactly one source Handle and the target Handle fills the whole node).
  const handleDelegate = useCallback(
    (from: string, to: string) =>
      handleConnect({ source: from, target: to, sourceHandle: null, targetHandle: null }),
    [handleConnect],
  )

  // Depends ONLY on the actual per-node model (+ the two stable node-level
  // callbacks) — NOT on editState/workerIds/handleDelegate, which are
  // canvas-global and now flow through TeamGraphCanvasContext instead. This is
  // what keeps a node's `data` object (and therefore its React Flow node
  // object) referentially stable across an edge edit that doesn't touch that
  // node, so xyflow's memo'd NodeWrapper re-renders a node only when the node
  // object — including data — changes identity, instead of on every edit.
  const modelNodes = useMemo<AgentFlowNode[]>(
    () =>
      nodes.map((model) => ({
        id: model.id,
        type: 'agent' as const,
        position: model.position,
        data: { model, onOpenAgent, onRemoveMember },
        connectable: true,
      })),
    [nodes, onOpenAgent, onRemoveMember],
  )

  // Canvas-global state for AgentNode/AgentDelegatePicker — see
  // TeamGraphCanvasContext above. Memoized so the provider's own value
  // identity only changes when one of these four actually changes (it still
  // changes on every edge edit, by design — editState IS the thing that
  // edits — but that no longer cascades into `modelNodes` above).
  const canvasContextValue = useMemo<TeamGraphCanvasContextValue>(
    () => ({ allNodes: nodes, editState, workerIds, onDelegate: handleDelegate }),
    [nodes, editState, workerIds, handleDelegate],
  )

  // Controlled node state so positions can be DRAGGED while dagre still provides
  // the initial layout. Dragged positions survive a background refetch.
  const [flowNodes, setFlowNodes] = useState<AgentFlowNode[]>(modelNodes)
  const draggedPositions = useRef<Record<string, { x: number; y: number }>>({})

  // React Flow renders the <Controls> zoom/fit buttons itself — no JSX site
  // here can carry the repo's explicit-tabIndex convention, so stamp them
  // post-render (WebKit Tab reachability; see useLibraryTabIndex).
  const canvasDomRef = useRef<HTMLDivElement>(null)
  useLibraryTabIndex(canvasDomRef)

  useEffect(() => {
    setFlowNodes(
      modelNodes.map((n) => {
        const dragged = draggedPositions.current[n.id]
        return dragged ? { ...n, position: dragged } : n
      }),
    )
  }, [modelNodes])

  const onNodesChange = useCallback((changes: NodeChange<AgentFlowNode>[]) => {
    setFlowNodes((prev) => {
      const next = applyNodeChanges(changes, prev)
      for (const change of changes) {
        if (change.type === 'position' && change.position) {
          draggedPositions.current[change.id] = change.position
        }
      }
      return next
    })
  }, [])

  const handleSelectEdge = useCallback((edgeId: string | null) => {
    setSelectedEdgeId(edgeId)
  }, [])

  const flowEdges = useMemo<DelegationFlowEdge[]>(
    () =>
      edges.map((model) => ({
        id: model.id,
        source: model.from,
        target: model.to,
        type: 'delegation' as const,
        markerEnd: { type: MarkerType.ArrowClosed, color: 'var(--color-border)' },
        data: {
          model,
          defaultDepth,
          onToggleMode,
          onSetDepth,
          onDelete: onDeleteEdge,
          selected: model.id === selectedEdgeId,
          onSelect: handleSelectEdge,
        },
      })),
    [edges, selectedEdgeId, defaultDepth, onToggleMode, onSetDepth, onDeleteEdge, handleSelectEdge],
  )

  return (
    <div
      ref={canvasDomRef}
      data-testid="team-graph-canvas"
      className="h-full w-full bg-[var(--color-surface-0)]"
    >
      <TeamGraphCanvasContext.Provider value={canvasContextValue}>
        <ReactFlow
          className="sovereign-flow"
          nodes={flowNodes}
          edges={flowEdges}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          onNodesChange={onNodesChange}
          onConnect={handleConnect}
          onConnectEnd={handleConnectEnd}
          isValidConnection={isValidConnection}
          onPaneClick={() => setSelectedEdgeId(null)}
          nodesDraggable
          nodesConnectable
          // Audit fix 5c: React Flow's own `.react-flow__node` wrapper gets a
          // SECOND tabIndex=0 (redundant tab stop) whenever `nodesFocusable` is
          // true (the default). AgentNode already sets its own tabIndex/role on
          // the inner content div when it's clickable (`data.onOpenAgent`), so
          // that inner element stays reachable with this off — this just removes
          // the duplicate outer stop, without touching drag/connect/select.
          nodesFocusable={false}
          elementsSelectable
          fitView
          fitViewOptions={{ padding: 0.25, maxZoom: 1.1 }}
          proOptions={{ hideAttribution: true }}
          defaultEdgeOptions={{ type: 'delegation' }}
          colorMode="dark"
        >
          <Controls
            showInteractive={false}
            className="!border-[var(--color-border)] !bg-[var(--color-surface-1)]"
          />
        </ReactFlow>
      </TeamGraphCanvasContext.Provider>
    </div>
  )
}

export function WorkspaceTeamGraph(props: WorkspaceTeamGraphProps) {
  return (
    <ReactFlowProvider>
      <WorkspaceTeamGraphInner {...props} />
    </ReactFlowProvider>
  )
}
