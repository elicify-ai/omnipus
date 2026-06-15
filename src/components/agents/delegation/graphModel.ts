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

function normalizeDepth(raw: unknown): number | undefined {
  return typeof raw === 'number' && Number.isFinite(raw) && raw >= 0 ? raw : undefined
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
    position: { x: 0, y: 0 },
  }))

  const edges: GraphEdgeModel[] = []
  for (const agent of agents) {
    const edit = edits[agent.id]
    if (!edit) continue
    for (const ref of edit.to) {
      edges.push({
        id: edgeId(agent.id, ref.id),
        source: agent.id,
        target: ref.id,
        kind: ref.kind,
        modes: edit.modes,
        depth: edit.depth,
        unknownTarget: !byId.has(ref.id),
      })
    }
  }

  const positions = layoutGraph(baseNodes, edges)
  const nodes = baseNodes.map((n) => ({ ...n, position: positions[n.id] ?? n.position }))
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
  return {
    ...edits,
    [source]: { ...edit, to: [...edit.to, { kind: 'local', id: target }] },
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

/** Immutably set a source agent's modes (policy-wide label). */
export function setSourceModes(
  edits: Record<string, SourcePolicyEdit>,
  source: string,
  modes: DelegationMode[],
): Record<string, SourcePolicyEdit> {
  const edit = edits[source]
  if (!edit) return edits
  return { ...edits, [source]: { ...edit, modes: normalizeModes(modes) } }
}

/** Immutably toggle a single mode on a source agent. */
export function toggleSourceMode(
  edits: Record<string, SourcePolicyEdit>,
  source: string,
  mode: DelegationMode,
): Record<string, SourcePolicyEdit> {
  const edit = edits[source]
  if (!edit) return edits
  const next = edit.modes.includes(mode)
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
 * `to`, `modes`, and `depth` — never `accept_from`, never `budget`. `depth` is
 * omitted entirely when unset so a partial PUT does not zero it.
 */
export interface DelegationSavePayload {
  to: Array<{ kind: DelegationRefKind; id: string }>
  modes: DelegationMode[]
  depth?: number
}

/** Build the delegation_policy save payload for one source agent's edit state. */
export function buildSavePayload(edit: SourcePolicyEdit): DelegationSavePayload {
  const payload: DelegationSavePayload = {
    to: edit.to.map((ref) => ({ kind: ref.kind, id: ref.id })),
    modes: [...edit.modes],
  }
  if (typeof edit.depth === 'number') payload.depth = edit.depth
  return payload
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

function editsEqual(a: SourcePolicyEdit | undefined, b: SourcePolicyEdit | undefined): boolean {
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
