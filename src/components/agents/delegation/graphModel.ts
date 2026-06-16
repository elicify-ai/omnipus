import dagre from '@dagrejs/dagre'
import type { Agent } from '@/lib/api'
import { isWorker } from '@/lib/api'

// ── Delegation-graph model (Spec-3 FR-6.2 / US-3 / NFR-7) ────────────────────
//
// Pure, framework-agnostic functions that turn the agent roster into an
// editable directed delegation graph and back into per-agent save payloads.
// This module holds ALL the editing rules so they can be unit-tested without a
// DOM, a canvas, or React Flow.
//
// EDGE DIRECTION: `A.delegation_policy.to` contains B  ⇒  edge A → B
// ("A may delegate to B"), labelled with A's `modes` (+ optional depth cap).
//
// HARD CONSTRAINT (NFR-7 / FR-6.2 / M-6): `accept_from` and `budget` are inert
// in v0.1.0 and MUST NOT be read or surfaced — doing so would falsely imply an
// active authorization boundary. Nothing in this module reads either field.

export type DelegationMode = 'await' | 'background' | 'task'

export const ALL_MODES: readonly DelegationMode[] = ['await', 'background', 'task']

/**
 * The mode set a NEW edge starts with. CRITICAL (correctness / security): the
 * backend `IsDelegationModeAllowed` treats `modes: []` as **ALL modes allowed**
 * (pkg/config/config.go), so an active edge MUST never carry an empty `modes`
 * array — that would persist "all allowed" while the UI shows "no modes". A new
 * edge therefore defaults to every mode, and the editor refuses to remove the
 * last remaining mode (keeps >= 1). The displayed modes always equal the
 * enforced modes.
 */
export const DEFAULT_EDGE_MODES: readonly DelegationMode[] = ['await', 'background', 'task']

/** Reference kind on a delegation edge — `local` now, `remote-a2a` reserved. */
export type DelegationRefKind = 'local' | 'remote-a2a'

/**
 * The editable per-source delegation state. One entry per source agent that
 * has (or gains) out-edges. `modes`/`depth` are the source agent's policy
 * label — in the v0.1.0 contract they are policy-wide, not per-target.
 */
export interface SourcePolicyEdit {
  /** Ordered target refs (the agent's `delegation_policy.to`). */
  to: Array<{ kind: DelegationRefKind; id: string }>
  modes: DelegationMode[]
  /** Depth cap when set (>= 0); undefined = unset. */
  depth?: number
}

/** A directed edge A → B in the rendered graph. */
export interface GraphEdgeModel {
  id: string
  /** Source agent id (the delegator). */
  source: string
  /** Target agent id (the delegatee). */
  target: string
  kind: DelegationRefKind
  /** The source agent's allowed modes (edge label). */
  modes: DelegationMode[]
  /** The source agent's depth cap, when set. */
  depth?: number
  /** True when the target id is not in the current roster. */
  unknownTarget: boolean
}

/** A node in the rendered graph = one agent. */
export interface GraphNodeModel {
  id: string
  name: string
  type: NonNullable<Agent['type']>
  color?: string
  isDefault: boolean
  isWorker: boolean
  /**
   * True when this node has no backing agent in the roster — a GHOST node
   * synthesised so that an edge pointing at a deleted target stays VISIBLE
   * (and therefore deletable) instead of being silently dropped by React Flow,
   * which discards edges whose endpoints have no matching node.
   */
  isGhost: boolean
  /** Layout position (deterministic, computed each load — never persisted). */
  position: { x: number; y: number }
}

export interface GraphModel {
  nodes: GraphNodeModel[]
  edges: GraphEdgeModel[]
}

const VALID_MODE = new Set<string>(ALL_MODES)

/** Normalise a raw modes array from the wire to the typed, de-duped union. */
export function normalizeModes(raw: unknown): DelegationMode[] {
  if (!Array.isArray(raw)) return []
  const out: DelegationMode[] = []
  for (const m of raw) {
    if (typeof m === 'string' && VALID_MODE.has(m) && !out.includes(m as DelegationMode)) {
      out.push(m as DelegationMode)
    }
  }
  return out
}

/**
 * Normalise a raw depth from the wire. The backend's documented uncapped
 * sentinel is `0` (and `<= 0` / absent) — `ResolveDelegationDepth` in
 * pkg/config/config.go returns "uncapped" for any non-positive depth. We map
 * that to `undefined` (= "no cap") so the model has ONE representation of
 * uncapped, round-trips are stable (save `0` ⇒ reload ⇒ still uncapped), and a
 * positive value is the only "capped" state. Only depth >= 1 is a real cap.
 */
function normalizeDepth(raw: unknown): number | undefined {
  return typeof raw === 'number' && Number.isFinite(raw) && raw >= 1 ? raw : undefined
}

/**
 * Build the per-source editable state keyed by agent id, reading ONLY `to`,
 * `modes`, and `depth` from each agent's `delegation_policy`. Every agent gets
 * an entry (workers included, always with `to: []`) so the editor can mutate
 * any source. `accept_from` and `budget` are never read.
 */
export function buildSourceEdits(agents: Agent[]): Record<string, SourcePolicyEdit> {
  const out: Record<string, SourcePolicyEdit> = {}
  for (const agent of agents) {
    const policy = agent.delegation_policy
    // Worker leaf rule: a worker is a delegation leaf and never has out-edges.
    const to = isWorker(agent)
      ? []
      : ((policy?.to ?? []).map((ref) => ({
          kind: (ref.kind === 'remote-a2a' ? 'remote-a2a' : 'local') as DelegationRefKind,
          id: ref.id,
        })))
    out[agent.id] = {
      to,
      modes: normalizeModes(policy?.modes),
      depth: normalizeDepth(policy?.depth),
    }
  }
  return out
}

/** Stable edge id for a source→target pair. */
export function edgeId(source: string, target: string): string {
  return `${source}->${target}`
}

/**
 * Compute a deterministic layered (top-down) layout with dagre. Positions are
 * NOT persisted in the contract, so they are recomputed every load. Roots
 * (default agent / orchestrator-style nodes with no in-edges) settle at the top.
 */
export function layoutGraph(
  nodeIds: GraphNodeModel[],
  edges: GraphEdgeModel[],
  opts: { nodeWidth?: number; nodeHeight?: number } = {},
): Record<string, { x: number; y: number }> {
  const nodeWidth = opts.nodeWidth ?? 220
  const nodeHeight = opts.nodeHeight ?? 92
  const g = new dagre.graphlib.Graph()
  g.setGraph({ rankdir: 'TB', nodesep: 60, ranksep: 90, marginx: 24, marginy: 24 })
  g.setDefaultEdgeLabel(() => ({}))

  for (const n of nodeIds) {
    g.setNode(n.id, { width: nodeWidth, height: nodeHeight })
  }
  for (const e of edges) {
    // Only lay out edges whose endpoints are both real nodes.
    if (g.hasNode(e.source) && g.hasNode(e.target)) {
      g.setEdge(e.source, e.target)
    }
  }
  dagre.layout(g)

  const positions: Record<string, { x: number; y: number }> = {}
  for (const n of nodeIds) {
    const dn = g.node(n.id)
    // dagre returns the node centre; React Flow wants the top-left corner.
    positions[n.id] = dn
      ? { x: dn.x - nodeWidth / 2, y: dn.y - nodeHeight / 2 }
      : { x: 0, y: 0 }
  }
  return positions
}

/**
 * Build the full render model (nodes + edges + deterministic layout) from the
 * roster and the editable per-source state. Edges come from each source's
 * `to`; the label carries that source's `modes`/`depth`.
 */
export function buildGraphModel(
  agents: Agent[],
  edits: Record<string, SourcePolicyEdit>,
): GraphModel {
  const byId = new Map(agents.map((a) => [a.id, a]))

  const baseNodes: GraphNodeModel[] = agents.map((a) => ({
    id: a.id,
    name: a.name,
    type: (a.type ?? 'custom') as NonNullable<Agent['type']>,
    color: a.color,
    isDefault: a.default === true,
    isWorker: isWorker(a),
    isGhost: false,
    position: { x: 0, y: 0 },
  }))

  const edges: GraphEdgeModel[] = []
  // Targets referenced by an edge but absent from the roster (deleted agents).
  // We synthesise a ghost node for each so the dangling edge stays visible and
  // can be removed by the user (React Flow drops edges with no endpoint node).
  const ghostIds = new Set<string>()
  for (const agent of agents) {
    const edit = edits[agent.id]
    if (!edit) continue
    for (const ref of edit.to) {
      const unknown = !byId.has(ref.id)
      if (unknown) ghostIds.add(ref.id)
      edges.push({
        id: edgeId(agent.id, ref.id),
        source: agent.id,
        target: ref.id,
        kind: ref.kind,
        modes: edit.modes,
        depth: edit.depth,
        unknownTarget: unknown,
      })
    }
  }

  const ghostNodes: GraphNodeModel[] = [...ghostIds].map((id) => ({
    id,
    name: `${id} (deleted)`,
    type: 'custom' as NonNullable<Agent['type']>,
    color: undefined,
    isDefault: false,
    isWorker: false,
    isGhost: true,
    position: { x: 0, y: 0 },
  }))

  const allNodes = [...baseNodes, ...ghostNodes]
  const positions = layoutGraph(allNodes, edges)
  const nodes = allNodes.map((n) => ({ ...n, position: positions[n.id] ?? n.position }))
  return { nodes, edges }
}

// ── Edge mutation rules ──────────────────────────────────────────────────────

export type ConnectionRejection =
  | 'self-edge'
  | 'duplicate'
  | 'worker-source'
  | 'unknown-source'

/**
 * Validate whether a new edge source → target may be created against the
 * current edit state + roster. Returns null when allowed, else a reason.
 *
 * Rules:
 *  - no self-edges (A → A)
 *  - no duplicate edges
 *  - worker nodes are delegation LEAVES — they may be a target but never a
 *    source (concept: worker `to: []`)
 */
export function validateConnection(
  source: string,
  target: string,
  edits: Record<string, SourcePolicyEdit>,
  workerIds: ReadonlySet<string>,
): ConnectionRejection | null {
  if (source === target) return 'self-edge'
  if (workerIds.has(source)) return 'worker-source'
  const edit = edits[source]
  if (!edit) return 'unknown-source'
  if (edit.to.some((ref) => ref.id === target)) return 'duplicate'
  return null
}

/**
 * Immutably add edge source → target (target appended to source's `to` as a
 * local ref). No-op on rejection — callers should `validateConnection` first
 * to surface a reason, but this stays internally consistent regardless.
 */
export function addDelegationEdge(
  edits: Record<string, SourcePolicyEdit>,
  source: string,
  target: string,
  workerIds: ReadonlySet<string>,
): Record<string, SourcePolicyEdit> {
  if (validateConnection(source, target, edits, workerIds) !== null) return edits
  const edit = edits[source]
  // A source with NO modes yet would persist `modes: []` = "all allowed" on the
  // backend (the empty-modes trap). Seed a sensible non-empty default so the
  // first edge enforces what the UI shows. Existing modes are left untouched.
  const modes = edit.modes.length === 0 ? [...DEFAULT_EDGE_MODES] : edit.modes
  return {
    ...edits,
    [source]: { ...edit, modes, to: [...edit.to, { kind: 'local', id: target }] },
  }
}

/** Immutably remove edge source → target (drops the ref from source's `to`). */
export function removeDelegationEdge(
  edits: Record<string, SourcePolicyEdit>,
  source: string,
  target: string,
): Record<string, SourcePolicyEdit> {
  const edit = edits[source]
  if (!edit) return edits
  if (!edit.to.some((ref) => ref.id === target)) return edits
  return {
    ...edits,
    [source]: { ...edit, to: edit.to.filter((ref) => ref.id !== target) },
  }
}

/**
 * Whether `mode` may be toggled OFF on `source` right now. The last remaining
 * mode can never be removed: an active edge with `modes: []` would persist
 * "all modes allowed" on the backend (the opposite of the user's intent). Use
 * this to disable the chip + show a tooltip in the editor.
 */
export function canToggleModeOff(
  edits: Record<string, SourcePolicyEdit>,
  source: string,
  mode: DelegationMode,
): boolean {
  const edit = edits[source]
  if (!edit) return false
  // Turning a mode ON is always fine; only removing the LAST one is blocked.
  if (!edit.modes.includes(mode)) return true
  return edit.modes.length > 1
}

/**
 * Immutably toggle a single mode on a source agent. Turning a mode on is always
 * allowed; turning one off is a NO-OP when it is the last remaining mode, so an
 * active edge can never reach `modes: []` (which the backend reads as "all
 * allowed"). Callers gate the UI with {@link canToggleModeOff}; this enforces
 * the same invariant in the model regardless.
 */
export function toggleSourceMode(
  edits: Record<string, SourcePolicyEdit>,
  source: string,
  mode: DelegationMode,
): Record<string, SourcePolicyEdit> {
  const edit = edits[source]
  if (!edit) return edits
  const has = edit.modes.includes(mode)
  if (has && edit.modes.length === 1) return edits // refuse to drop the last mode
  const next = has
    ? edit.modes.filter((m) => m !== mode)
    : [...edit.modes, mode]
  return { ...edits, [source]: { ...edit, modes: next } }
}

/** Immutably set a source agent's depth cap (undefined clears it). */
export function setSourceDepth(
  edits: Record<string, SourcePolicyEdit>,
  source: string,
  depth: number | undefined,
): Record<string, SourcePolicyEdit> {
  const edit = edits[source]
  if (!edit) return edits
  const clean = normalizeDepth(depth)
  return { ...edits, [source]: { ...edit, depth: clean } }
}

// ── Save payloads ─────────────────────────────────────────────────────────────

/**
 * The delegation_policy slice this UI persists for one source agent. ONLY
 * `to`, `modes`, and `depth` — never `accept_from`, never `budget`.
 *
 * `depth` is ALWAYS sent (never omitted) so that clearing the cap is honest:
 * the backend documents `0` as the uncapped sentinel (`ResolveDelegationDepth`
 * treats any non-positive depth as "uncapped"). Omitting `depth` would make the
 * backend PRESERVE the old value, so the UI would snap back to the previous cap
 * after refetch — a clear gesture that lies. Sending `0` for "no cap" makes the
 * displayed state match what is enforced and survives a round-trip.
 */
export interface DelegationSavePayload {
  to: Array<{ kind: DelegationRefKind; id: string }>
  modes: DelegationMode[]
  depth: number
}

/**
 * Build the delegation_policy save payload for one source agent's edit state.
 * `depth` is always present: a real cap when set (>= 1), else `0` = uncapped.
 */
export function buildSavePayload(edit: SourcePolicyEdit): DelegationSavePayload {
  return {
    to: edit.to.map((ref) => ({ kind: ref.kind, id: ref.id })),
    modes: [...edit.modes],
    depth: typeof edit.depth === 'number' ? edit.depth : 0,
  }
}

/**
 * Diff two edit maps and return the set of source agent ids whose delegation
 * policy changed (to / modes / depth). Only these need a PUT on save.
 */
export function changedSourceIds(
  before: Record<string, SourcePolicyEdit>,
  after: Record<string, SourcePolicyEdit>,
): string[] {
  const ids = new Set([...Object.keys(before), ...Object.keys(after)])
  const changed: string[] = []
  for (const id of ids) {
    if (!editsEqual(before[id], after[id])) changed.push(id)
  }
  return changed
}

/**
 * Reconcile a freshly-loaded server baseline with the user's in-flight edits,
 * preserving still-dirty edits. Used after a refetch (incl. post-save
 * invalidation): an agent the user HADN'T touched (its edit equalled the old
 * baseline) adopts the new server value; an agent the user HAD changed keeps
 * the pending edit so a partial save leaves only the genuinely-failed agents
 * dirty (and a background refetch never silently discards unsaved work).
 */
export function reconcileEdits(
  newBaseline: Record<string, SourcePolicyEdit>,
  prevBaseline: Record<string, SourcePolicyEdit>,
  prevEdits: Record<string, SourcePolicyEdit>,
): Record<string, SourcePolicyEdit> {
  const out: Record<string, SourcePolicyEdit> = {}
  for (const id of Object.keys(newBaseline)) {
    const wasDirty = !editsEqual(prevEdits[id], prevBaseline[id])
    // Keep the user's pending edit only when it was dirty AND the agent still
    // exists; otherwise take the authoritative server value.
    out[id] = wasDirty && prevEdits[id] ? prevEdits[id] : newBaseline[id]
  }
  // REFERENTIAL STABILITY (avoids a render loop): when the reconciled result is
  // content-identical to the input `prevEdits`, return the SAME `prevEdits`
  // reference so React's setState bails out of the re-render. Without this, the
  // effect would set a brand-new-but-equal object every refetch, churning forever.
  return editsMapEqual(prevEdits, out) ? prevEdits : out
}

/** Deep-equal two edit maps (same keys + each entry editsEqual). */
export function editsMapEqual(
  a: Record<string, SourcePolicyEdit>,
  b: Record<string, SourcePolicyEdit>,
): boolean {
  const aKeys = Object.keys(a)
  const bKeys = Object.keys(b)
  if (aKeys.length !== bKeys.length) return false
  for (const k of aKeys) {
    if (!(k in b)) return false
    if (!editsEqual(a[k], b[k])) return false
  }
  return true
}

export function editsEqual(a: SourcePolicyEdit | undefined, b: SourcePolicyEdit | undefined): boolean {
  if (!a || !b) return a === b
  if (a.depth !== b.depth) return false
  if (a.modes.length !== b.modes.length) return false
  for (let i = 0; i < a.modes.length; i++) {
    if (a.modes[i] !== b.modes[i]) return false
  }
  if (a.to.length !== b.to.length) return false
  for (let i = 0; i < a.to.length; i++) {
    if (a.to[i].id !== b.to[i].id || a.to[i].kind !== b.to[i].kind) return false
  }
  return true
}
