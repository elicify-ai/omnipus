import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  Handle,
  Position,
  ConnectionMode,
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
  type OnConnectStart,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import {
  Star,
  Stack,
  Lightning,
  Trash,
  X,
  Warning,
  DotsSixVertical,
} from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import {
  ALL_MODES,
  validateConnection,
  type DelegationMode,
  type GraphEdgeModel,
  type GraphNodeModel,
} from './graphModel'

// ── Mode-chip accents (reused from the old read-only TrustGraph) ─────────────
const MODE_CHIP_CLASS: Record<DelegationMode, string> = {
  await:
    'border-[var(--color-accent)]/40 bg-[var(--color-accent)]/10 text-[var(--color-accent)]',
  background:
    'border-[var(--color-info,#5B8DEF)]/40 bg-[var(--color-info,#5B8DEF)]/10 text-[var(--color-info,#9DBEFF)]',
  task:
    'border-[var(--color-success)]/40 bg-[var(--color-success)]/10 text-[var(--color-success)]',
}

// CSS selector of the per-node drag grip. Setting each node's `dragHandle` to
// this class means ONLY the grip moves the node — dragging the node BODY starts
// a delegation connection instead (the "easy-connect" pattern below). The two
// gestures never fight: body = connect, grip = move.
const DRAG_GRIP_SELECTOR = '.delegation-drag-grip'

// ── Node / edge data carried into React Flow ─────────────────────────────────
export interface AgentNodeData extends Record<string, unknown> {
  model: GraphNodeModel
}
export interface DelegationEdgeData extends Record<string, unknown> {
  model: GraphEdgeModel
  onEditModes: (source: string, mode: DelegationMode) => void
  onSetDepth: (source: string, depth: number | undefined) => void
  onDelete: (source: string, target: string) => void
  selected: boolean
  onSelect: (edgeId: string | null) => void
}

type AgentFlowNode = Node<AgentNodeData, 'agent'>
type DelegationFlowEdge = Edge<DelegationEdgeData, 'delegation'>

// Full-node-sized, invisible Handle. Absolutely fills the node body so the
// ENTIRE shape is the connect hit-area (the React Flow "easy-connect" recipe):
// a drag that starts anywhere on the body starts/ends a connection. `!opacity-0`
// keeps it invisible; the node body itself supplies the visible affordance.
const FULL_HANDLE_CLASS =
  'delegation-full-handle !absolute !inset-0 !left-0 !top-0 !h-full !w-full !min-h-0 !min-w-0 !translate-x-0 !translate-y-0 !transform-none !rounded-none !border-0 !bg-transparent !opacity-0'

// ── Drag grip: the ONLY part of a node that moves it ─────────────────────────
// Sits above the full-node connect handle (higher z-index) so a pointer-down on
// the grip hits the grip (→ node move via dragHandle), while a pointer-down
// anywhere else hits the handle (→ connection). `nopan` stops a grip drag from
// panning the canvas.
function DragGrip() {
  return (
    <div
      className={cn(
        'delegation-drag-grip nopan absolute right-1.5 top-1.5 z-10 flex h-5 w-5 cursor-grab items-center justify-center rounded text-[var(--color-muted)] opacity-0 transition-opacity hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] active:cursor-grabbing group-hover:opacity-100',
      )}
      title="Drag to reposition"
      aria-label="Drag to reposition node"
      data-testid="delegation-drag-grip"
    >
      <DotsSixVertical size={13} weight="bold" />
    </div>
  )
}

// ── Custom node: one agent ───────────────────────────────────────────────────
function AgentNode({ id, data }: NodeProps<AgentFlowNode>) {
  const { model } = data
  const initial = model.name.charAt(0).toUpperCase()

  // Easy-connect drop-target detection: while a connection is in progress and it
  // did NOT start on this node, this node is a candidate DROP TARGET — surface a
  // visible affordance so it's discoverable that the whole shape is droppable.
  const connection = useConnection()
  const isTarget = connection.inProgress && connection.fromNode?.id !== id

  // Worker (leaf) and ghost (deleted placeholder) nodes are TARGET-ONLY: they
  // may receive a delegation edge but must never START one. Only a real,
  // non-worker node can be a connection source.
  const canBeSource = !model.isWorker && !model.isGhost

  // GHOST node: an edge target with no backing agent (deleted). Render it with a
  // warning style + "(deleted)" label so the dangling edge is visible and the
  // user can select that edge and delete it. A ghost is target-only: it gets the
  // full-node TARGET handle (so the easy-connect drop works) but never a source.
  if (model.isGhost) {
    return (
      <div
        data-testid={`delegation-node-${model.id}`}
        data-ghost="true"
        className={cn(
          'group relative w-[220px] rounded-xl border border-dashed border-[var(--color-warning)]/60 bg-[var(--color-warning)]/5 px-3 py-2.5 shadow-sm transition-colors',
          isTarget && 'border-solid ring-2 ring-[var(--color-warning)]/70',
        )}
        title={`${model.id} no longer exists — its delegation edge is dangling. Click the edge to delete it.`}
      >
        {/* Full-node TARGET handle. `isConnectableStart={false}` blocks a drag
            from STARTING here, so a ghost can never be a source. */}
        <Handle
          type="target"
          position={Position.Top}
          isConnectableStart={false}
          className={cn(
            FULL_HANDLE_CLASS,
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
        </div>
      </div>
    )
  }

  return (
    <div
      data-testid={`delegation-node-${model.id}`}
      data-can-source={canBeSource ? 'true' : 'false'}
      className={cn(
        'group relative w-[220px] rounded-xl border bg-[var(--color-surface-1)] px-3 py-2.5 shadow-sm transition-colors',
        'border-[var(--color-border)]',
        // Hover affordance: the whole shape is grabbable to connect. A source
        // node shows a grab cursor + accent ring on hover so it's discoverable.
        canBeSource && 'cursor-grab hover:border-[var(--color-accent)]/60 hover:ring-1 hover:ring-[var(--color-accent)]/40',
        // Drop-target affordance during an in-progress connection.
        isTarget && 'border-[var(--color-accent)] ring-2 ring-[var(--color-accent)]/70',
      )}
      title={
        model.isWorker
          ? "Workers are delegation leaves — they receive work but never delegate onward"
          : `Drag from anywhere on ${model.name} onto another agent to delegate. Use the grip to reposition.`
      }
    >
      {/* Move grip — the only element that repositions the node (dragHandle). */}
      <DragGrip />

      {/* Full-node TARGET handle: every node can RECEIVE delegation. Rendered
          while a connection is in progress only when THIS node is a valid drop
          target (not the originating node), matching the easy-connect recipe so
          the source node doesn't immediately drop onto itself. Always present
          when idle so the handle exists before a drag begins. */}
      {(!connection.inProgress || isTarget) && (
        <Handle
          type="target"
          position={Position.Top}
          isConnectableStart={false}
          className={cn(
            FULL_HANDLE_CLASS,
            '!border-[var(--color-border)] !bg-[var(--color-surface-3)]',
          )}
        />
      )}

      {/* Full-node SOURCE handle: only NON-worker nodes may delegate onward.
          Rendered only while no connection is in progress (easy-connect) so the
          target handle above can take over once a drag starts. */}
      {canBeSource && !connection.inProgress && (
        <Handle
          type="source"
          position={Position.Bottom}
          className={cn(
            FULL_HANDLE_CLASS,
            '!border-[var(--color-accent)] !bg-[var(--color-accent)]',
          )}
        />
      )}

      <div className="pointer-events-none flex items-center gap-2.5">
        <div
          className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-sm font-bold text-[var(--color-secondary)]"
          style={{ backgroundColor: model.color ?? 'var(--color-surface-3)' }}
          aria-hidden="true"
        >
          {initial}
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
            <span className="rounded bg-[var(--color-surface-2)] px-1.5 py-0.5 text-[9px] font-medium uppercase tracking-wide text-[var(--color-muted)]">
              {model.type}
            </span>
            {model.isWorker && (
              <span
                className="inline-flex items-center gap-0.5 rounded border border-[var(--color-info,#5B8DEF)]/40 bg-[var(--color-info,#5B8DEF)]/10 px-1 py-0.5 text-[9px] font-medium uppercase tracking-wide text-[var(--color-info,#9DBEFF)]"
                title="Worker — a delegation leaf (receives work, never delegates onward)"
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

// ── Custom edge: directed delegation, click → inline editor ──────────────────
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

  // Hooks must run unconditionally (rules of hooks) — declare before any early
  // return. The depth input is a controlled draft so a partial value (e.g. an
  // empty field) doesn't fight the committed model value.
  const [depthDraft, setDepthDraft] = useState<string>(
    model?.depth != null ? String(model.depth) : '',
  )
  useEffect(() => {
    setDepthDraft(model?.depth != null ? String(model.depth) : '')
  }, [model?.depth])

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
            : model.unknownTarget
              ? 'var(--color-warning)'
              : 'var(--color-border)',
          strokeWidth: selected ? 2 : 1.5,
        }}
      />
      <EdgeLabelRenderer>
        <div
          data-testid={`delegation-edge-${model.source}-${model.target}`}
          style={{
            position: 'absolute',
            transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
            pointerEvents: 'all',
          }}
          className="nodrag nopan"
        >
          {selected ? (
            <div
              data-testid={`delegation-edge-editor-${model.source}-${model.target}`}
              className="w-56 rounded-lg border border-[var(--color-accent)]/60 bg-[var(--color-surface-1)] p-2.5 shadow-lg"
            >
              <div className="mb-2 flex items-center justify-between">
                <span className="text-[10px] font-medium uppercase tracking-wide text-[var(--color-muted)]">
                  Edge modes
                </span>
                <button
                  type="button"
                  aria-label="Close edge editor"
                  className="text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
                  onClick={() => data.onSelect(null)}
                >
                  <X size={12} weight="bold" />
                </button>
              </div>
              <div className="flex flex-wrap gap-1">
                {ALL_MODES.map((m) => {
                  const on = model.modes.includes(m)
                  // Refuse to remove the LAST mode: an active edge with no modes
                  // persists "all modes allowed" on the backend (the opposite of
                  // intent). Disable the only-remaining on-chip with a tooltip.
                  const isLastOn = on && model.modes.length === 1
                  return (
                    <button
                      key={m}
                      type="button"
                      data-testid={`edge-mode-toggle-${m}`}
                      disabled={isLastOn}
                      aria-disabled={isLastOn}
                      title={
                        isLastOn
                          ? 'At least one mode is required — an edge with no modes would allow ALL modes.'
                          : undefined
                      }
                      onClick={() => data.onEditModes(model.source, m)}
                      className={cn(
                        'rounded border px-1.5 py-0.5 font-mono text-[10px] lowercase transition-opacity',
                        on
                          ? MODE_CHIP_CLASS[m]
                          : 'border-[var(--color-border)] bg-[var(--color-surface-2)] text-[var(--color-muted)] opacity-60 hover:opacity-100',
                        isLastOn && 'cursor-not-allowed',
                      )}
                    >
                      {m}
                    </button>
                  )
                })}
              </div>
              <div className="mt-2 flex items-center gap-1.5">
                <Stack size={12} weight="bold" className="text-[var(--color-muted)]" />
                <span className="text-[10px] text-[var(--color-muted)]">depth</span>
                <input
                  type="number"
                  min={1}
                  inputMode="numeric"
                  data-testid="edge-depth-input"
                  value={depthDraft}
                  placeholder="∞"
                  title="Max delegation hops. Empty or 0 = uncapped (∞); clearing it persists as uncapped."
                  onChange={(e) => {
                    const v = e.target.value
                    setDepthDraft(v)
                    // Empty or a non-positive number means "no cap" (uncapped).
                    // The model stores that as undefined; on save it persists as
                    // the backend's `0` uncapped sentinel, so clearing is honest
                    // and survives a refetch (it won't snap back to the old cap).
                    if (v === '') {
                      data.onSetDepth(model.source, undefined)
                    } else {
                      const n = Number(v)
                      if (Number.isFinite(n)) {
                        data.onSetDepth(model.source, n >= 1 ? n : undefined)
                      }
                    }
                  }}
                  className="h-6 w-14 rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-1.5 text-[11px] text-[var(--color-secondary)] focus:border-[var(--color-accent)] focus:outline-none"
                />
                <Button
                  size="sm"
                  variant="ghost"
                  data-testid={`edge-delete-${model.source}-${model.target}`}
                  className="ml-auto h-6 gap-1 px-1.5 text-[10px] text-[var(--color-error,#EF5B5B)] hover:bg-[var(--color-error,#EF5B5B)]/10"
                  onClick={() => data.onDelete(model.source, model.target)}
                >
                  <Trash size={11} weight="bold" /> delete
                </Button>
              </div>
            </div>
          ) : (
            <button
              type="button"
              onClick={() => data.onSelect(id)}
              className="flex cursor-pointer items-center gap-1 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-1.5 py-0.5 shadow-sm hover:border-[var(--color-accent)]/50"
            >
              {model.modes.length === 0 ? (
                <span className="text-[9px] italic text-[var(--color-muted)]">no modes</span>
              ) : (
                model.modes.map((m) => (
                  <span
                    key={m}
                    className={cn(
                      'rounded border px-1 py-0 font-mono text-[9px] lowercase',
                      MODE_CHIP_CLASS[m],
                    )}
                  >
                    {m}
                  </span>
                ))
              )}
              {model.depth != null && (
                <span className="ml-0.5 inline-flex items-center gap-0.5 text-[9px] text-[var(--color-muted)]">
                  <Stack size={9} weight="bold" />
                  {model.depth}
                </span>
              )}
            </button>
          )}
        </div>
      </EdgeLabelRenderer>
    </>
  )
}

const nodeTypes = { agent: AgentNode }
const edgeTypes = { delegation: DelegationEdge }

export interface DelegationGraphProps {
  nodes: GraphNodeModel[]
  edges: GraphEdgeModel[]
  workerIds: ReadonlySet<string>
  /** Source agent edit map — needed for connection validation. */
  validateEdits: Parameters<typeof validateConnection>[2]
  onConnect: (source: string, target: string) => void
  onToggleMode: (source: string, mode: DelegationMode) => void
  onSetDepth: (source: string, depth: number | undefined) => void
  onDeleteEdge: (source: string, target: string) => void
  /** Surface a human reason when a connection is rejected (tooltip/toast). */
  onRejectConnection: (reason: string) => void
}

const REJECTION_MESSAGE: Record<string, string> = {
  'self-edge': 'An agent cannot delegate to itself.',
  duplicate: 'That delegation edge already exists.',
  'worker-source':
    "Workers are delegation leaves — they don't delegate onward.",
  'unknown-source': 'Unknown source agent.',
}

function DelegationGraphInner({
  nodes,
  edges,
  workerIds,
  validateEdits,
  onConnect,
  onToggleMode,
  onSetDepth,
  onDeleteEdge,
  onRejectConnection,
}: DelegationGraphProps) {
  const [selectedEdgeId, setSelectedEdgeId] = useState<string | null>(null)

  // Keep selection valid: drop it if the edge no longer exists.
  useEffect(() => {
    if (selectedEdgeId && !edges.some((e) => e.id === selectedEdgeId)) {
      setSelectedEdgeId(null)
    }
  }, [edges, selectedEdgeId])

  // Build the React Flow nodes from the model. `dragHandle` points at the grip
  // so the BODY initiates a connection (easy-connect) while the grip moves the
  // node. Worker/ghost nodes have no source handle so they can't start an edge.
  const modelNodes = useMemo<AgentFlowNode[]>(
    () =>
      nodes.map((model) => ({
        id: model.id,
        type: 'agent' as const,
        position: model.position,
        dragHandle: DRAG_GRIP_SELECTOR,
        data: { model },
        // With ConnectionMode.Loose a single per-node handle acts as both source
        // and target. `connectable` stays true for every node so each can at
        // least RECEIVE an edge; the worker/ghost source block is enforced by
        // omitting their source handle + onConnectStart/isValidConnection.
        connectable: true,
      })),
    [nodes],
  )

  // Controlled node state so positions can be DRAGGED (via onNodesChange) while
  // dagre still provides the initial layout. Positions are ephemeral (never
  // persisted) — that's intentional. We seed from `modelNodes` and reconcile on
  // every model change, PRESERVING any user-dragged position for a node that
  // still exists so a background refetch doesn't snap dragged nodes back.
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
      // Remember dragged positions so a later model-driven reconcile keeps them
      // instead of snapping back to the dagre layout.
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
        source: model.source,
        target: model.target,
        type: 'delegation' as const,
        markerEnd: { type: MarkerType.ArrowClosed, color: 'var(--color-border)' },
        data: {
          model,
          onEditModes: onToggleMode,
          onSetDepth,
          onDelete: onDeleteEdge,
          selected: model.id === selectedEdgeId,
          onSelect: handleSelectEdge,
        },
      })),
    [edges, selectedEdgeId, onToggleMode, onSetDepth, onDeleteEdge, handleSelectEdge],
  )

  // Reject invalid connections live (worker source / self / duplicate) so the
  // edge never visually attaches. With ConnectionMode.Loose the loose handle
  // could otherwise let a drag start on a worker; this is the live gate.
  const isValidConnection: IsValidConnection<DelegationFlowEdge> = useCallback(
    (conn) => {
      if (!conn.source || !conn.target) return false
      return validateConnection(conn.source, conn.target, validateEdits, workerIds) === null
    },
    [validateEdits, workerIds],
  )

  const handleConnect = useCallback(
    (conn: Connection) => {
      // DIRECTION (verified): `conn.source` is the node the drag STARTED on,
      // `conn.target` is the node it was DROPPED on. So source(dragged-from) →
      // target(dropped-on) = "source may delegate to target". We hand this
      // straight to onConnect(source, target) → addDelegationEdge(source,target).
      if (!conn.source || !conn.target) return
      const reason = validateConnection(conn.source, conn.target, validateEdits, workerIds)
      if (reason !== null) {
        onRejectConnection(REJECTION_MESSAGE[reason] ?? 'Connection not allowed.')
        return
      }
      onConnect(conn.source, conn.target)
    },
    [validateEdits, workerIds, onConnect, onRejectConnection],
  )

  const handleConnectStart = useCallback<OnConnectStart>(
    (_, params) => {
      // Belt-and-braces worker-source block: a worker should have no source
      // handle, but if a Loose drag is somehow initiated from one, surface the
      // reject toast immediately (isValidConnection also refuses to attach it).
      if (params.nodeId && workerIds.has(params.nodeId)) {
        onRejectConnection(REJECTION_MESSAGE['worker-source'])
      }
    },
    [workerIds, onRejectConnection],
  )

  const wrapperRef = useRef<HTMLDivElement>(null)

  return (
    <div
      ref={wrapperRef}
      data-testid="delegation-graph-canvas"
      className="h-full w-full rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-0,#0A0A0B)]"
    >
      <ReactFlow
        nodes={flowNodes}
        edges={flowEdges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodesChange={onNodesChange}
        onConnect={handleConnect}
        onConnectStart={handleConnectStart}
        isValidConnection={isValidConnection}
        onPaneClick={() => setSelectedEdgeId(null)}
        // Loose mode lets the single full-node handle act as both source and
        // target, which is what the easy-connect (whole-node) pattern requires.
        connectionMode={ConnectionMode.Loose}
        nodesDraggable
        nodesConnectable
        elementsSelectable
        fitView
        proOptions={{ hideAttribution: true }}
        defaultEdgeOptions={{ type: 'delegation' }}
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

export function DelegationGraph(props: DelegationGraphProps) {
  return (
    <ReactFlowProvider>
      <DelegationGraphInner {...props} />
    </ReactFlowProvider>
  )
}
