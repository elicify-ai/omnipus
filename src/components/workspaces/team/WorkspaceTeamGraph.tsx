import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
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
import { cn } from '@/lib/utils'
import { EdgeModeEditor, EdgeLabelChip } from './EdgeModeEditor'
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
  onRemoveMember?: (agentId: string) => void
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
  const initial = model.name.charAt(0).toUpperCase()

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
          {data.onRemoveMember && (
            <button
              type="button"
              aria-label={`Remove ${model.id} from team`}
              title="Remove from team"
              className="nodrag shrink-0 rounded p-1 text-[var(--color-warning)]/70 hover:bg-[var(--color-warning)]/15 hover:text-[var(--color-warning)]"
              onClick={(e) => {
                e.stopPropagation()
                data.onRemoveMember?.(model.id)
              }}
            >
              <X size={13} weight="bold" />
            </button>
          )}
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
        'focus-visible:outline-none',
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

      {/* Hover actions (edit / remove). pointer-events isolated via data-node-action. */}
      <div className="absolute right-1.5 top-1.5 z-10 flex items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100">
        {data.onOpenAgent && (
          <button
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
        {data.onRemoveMember && !model.isDefault && (
          <button
            type="button"
            data-node-action="remove"
            aria-label={`Remove ${model.name} from team`}
            title="Remove from this workspace's team"
            className="nodrag rounded p-1 text-[var(--color-muted)] hover:bg-[var(--color-error)]/15 hover:text-[var(--color-error)]"
            onClick={(e) => {
              e.stopPropagation()
              data.onRemoveMember?.(id)
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

  // Controlled node state so positions can be DRAGGED while dagre still provides
  // the initial layout. Dragged positions survive a background refetch.
  const [flowNodes, setFlowNodes] = useState<AgentFlowNode[]>(modelNodes)
  const draggedPositions = useRef<Record<string, { x: number; y: number }>>({})

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

  const isValidConnection: IsValidConnection<DelegationFlowEdge> = useCallback(
    (conn) => {
      if (!conn.source || !conn.target) return false
      return validateConnection(conn.source, conn.target, editState, workerIds) === null
    },
    [editState, workerIds],
  )

  const handleConnect = useCallback(
    (conn: Connection) => {
      // `conn.source` is the node the drag STARTED on (the delegator),
      // `conn.target` is where it was DROPPED (the delegate). source → target.
      if (!conn.source || !conn.target) return
      const reason = validateConnection(conn.source, conn.target, editState, workerIds)
      if (reason !== null) {
        onRejectConnection(REJECTION_MESSAGE[reason] ?? 'Connection not allowed.')
        return
      }
      onConnect(conn.source, conn.target)
    },
    [editState, workerIds, onConnect, onRejectConnection],
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
      )
      if (message) onRejectConnection(message)
    },
    [editState, workerIds, onRejectConnection],
  )

  return (
    <div
      data-testid="team-graph-canvas"
      className="h-full w-full rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-0)]"
    >
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
        elementsSelectable
        fitView
        fitViewOptions={{ padding: 0.25, maxZoom: 1.1 }}
        proOptions={{ hideAttribution: true }}
        defaultEdgeOptions={{ type: 'delegation' }}
        colorMode="dark"
      >
        <Background color="var(--color-border)" gap={20} />
        <Controls
          showInteractive={false}
          className="!border-[var(--color-border)] !bg-[var(--color-surface-1)]"
        />
      </ReactFlow>
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
