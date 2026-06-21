import dagre from '@dagrejs/dagre'
import type { Agent, WorkspaceDelegationEdge } from '@/lib/api'
import { isWorker } from '@/lib/api'

// ── Workspace Team delegation-graph model (M5 / Sprint 4 Team tab) ───────────
//
// Pure, framework-agnostic functions that turn a workspace's delegation contract
// (`WorkspaceDelegation`) + the global agents cache into an editable directed
// graph, and back into a `WorkspaceDelegationEdge[]` save payload.
//
// THIS DIFFERS from the per-AGENT delegation editor (src/components/agents/
// delegation): the workspace contract carries `modes` + `depth` PER EDGE (each
// WorkspaceDelegationEdge stands alone), and team membership is an explicit node
// set (core_team ∪ edge endpoints) that the editor adds to / removes from. So
// the model here is edge-keyed, not source-keyed.
//
// EDGE DIRECTION: edge `from_agent → to_agent` means "from_agent may delegate to
// to_agent", labelled by that edge's own `modes` (+ optional `depth` cap).

export type DelegationMode = 'await' | 'background' | 'task'

export const ALL_MODES: readonly DelegationMode[] = ['await', 'background', 'task']

/**
 * The mode set a NEW edge starts with. CRITICAL (matches the backend contract):
 * the gateway treats an empty/absent `modes` list as **ALL modes allowed**
 * (WorkspaceDelegationEdge schema: "An empty/absent list means all modes are
 * allowed"). So an active edge must never carry `modes: []` while the UI shows
 * "no modes" — that would persist "all allowed", the opposite of intent. A new
 * edge therefore seeds every mode, and the editor refuses to remove the last
 * remaining mode (keeps >= 1). The displayed modes always equal the enforced.
 */
export const DEFAULT_EDGE_MODES: readonly DelegationMode[] = ['await', 'background', 'task']

const VALID_MODE = new Set<string>(ALL_MODES)

/** Stable, order-independent client id for a from→to edge pair. */
export function teamEdgeId(from: string, to: string): string {
  return `${from}->${to}`
}

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
 * Normalise a raw depth from the wire. The backend documents `0` as "no onward
 * delegation past this hop" and "absent means the workspace/global default
 * applies". We model an UNSET cap as `undefined` (UI shows ∞ / inherits) and a
 * real cap as a positive integer. A wire `0` is preserved as `0` (an explicit
 * "no onward hop" choice) — only negative / non-finite values collapse to unset.
 */
export function normalizeDepth(raw: unknown): number | undefined {
  if (typeof raw !== 'number' || !Number.isFinite(raw)) return undefined
  if (raw < 0) return undefined
  return Math.floor(raw)
}

// ── Edit model ────────────────────────────────────────────────────────────────
//
// The editor's working state is two pieces:
//   - `members`: the ordered set of agent ids on the team (node set).
//   - `edges`:   the directed delegation edges, each with its own modes/depth.
// Both persist together via updateWorkspaceDelegation: membership is implicit in
// the union of `team` (sent as isolated members carry no edge) — but because the
// PUT body is edges-only, an isolated member with no edge is held client-side
// and re-derived from `team[]` on refetch. See buildSavePayload / mergeMembers.

export interface TeamEdgeEdit {
  from: string
  to: string
  modes: DelegationMode[]
  /** Depth cap (>= 0) when set; undefined = inherit/unset. */
  depth?: number
}

export interface TeamEditState {
  /** Agent ids on the team (node set), order preserved for stable layout. */
  members: string[]
  edges: TeamEdgeEdit[]
}

/** A node in the rendered graph = one team agent (or a ghost for a deleted id). */
export interface TeamNodeModel {
  id: string
  name: string
  type: NonNullable<Agent['type']>
  /** Role subtitle shown under the name (humanised type, falls back to type). */
  role: string
  color?: string
  icon?: string
  isDefault: boolean
  isWorker: boolean
  /** True when no backing agent exists for this id (referenced but deleted). */
  isGhost: boolean
  position: { x: number; y: number }
}

/** A directed edge in the rendered graph. */
export interface TeamEdgeModel {
  id: string
  from: string
  to: string
  modes: DelegationMode[]
  depth?: number
  /** True when an endpoint id is not a real agent in the roster. */
  unknownEndpoint: boolean
}

export interface TeamGraphModel {
  nodes: TeamNodeModel[]
  edges: TeamEdgeModel[]
}

// ── Building the edit state from the wire ────────────────────────────────────

/**
 * Build the editor working state from the delegation contract. `team` (when the
 * backend computes it) is the authoritative node set; we also fold in every edge
 * endpoint so a malformed response never hides an edge. Falls back to `seedTeam`
 * (e.g. the workspace's core_team) when the response carries no team.
 */
export function buildTeamEditState(
  delegation: { edges?: WorkspaceDelegationEdge[]; team?: string[] } | undefined,
  seedTeam: string[] = [],
): TeamEditState {
  const edges: TeamEdgeEdit[] = (delegation?.edges ?? []).map((e) => ({
    from: e.from_agent,
    to: e.to_agent,
    modes: normalizeModes(e.modes),
    depth: normalizeDepth(e.depth),
  }))

  const members = new Set<string>()
  for (const id of delegation?.team ?? []) members.add(id)
  if ((delegation?.team ?? []).length === 0) {
    for (const id of seedTeam) members.add(id)
  }
  for (const e of edges) {
    members.add(e.from)
    members.add(e.to)
  }

  return { members: [...members], edges }
}

/** Humanise an agent type into a short role subtitle. */
export function roleLabel(agent: Agent | undefined): string {
  if (!agent) return 'unknown'
  switch (agent.type) {
    case 'Main':
      return 'Main agent'
    case 'Subagent':
      return 'Subagent'
    case 'subagent_3p':
      return 'External CLI'
    case 'core':
      return 'Built-in'
    case 'system':
      return 'System'
    default:
      return agent.type ?? 'agent'
  }
}

// ── Layout ──────────────────────────────────────────────────────────────────

/**
 * Deterministic top-down dagre layout. Positions are NOT persisted in the
 * contract, so they are recomputed every load (and then preserved across drags
 * by the canvas). Roots (no in-edges) settle at the top.
 */
export function layoutTeamGraph(
  nodes: { id: string }[],
  edges: { from: string; to: string }[],
  opts: { nodeWidth?: number; nodeHeight?: number } = {},
): Record<string, { x: number; y: number }> {
  const nodeWidth = opts.nodeWidth ?? 220
  const nodeHeight = opts.nodeHeight ?? 92
  const g = new dagre.graphlib.Graph()
  g.setGraph({ rankdir: 'TB', nodesep: 64, ranksep: 96, marginx: 24, marginy: 24 })
  g.setDefaultEdgeLabel(() => ({}))

  for (const n of nodes) g.setNode(n.id, { width: nodeWidth, height: nodeHeight })
  for (const e of edges) {
    if (g.hasNode(e.from) && g.hasNode(e.to)) g.setEdge(e.from, e.to)
  }
  dagre.layout(g)

  const positions: Record<string, { x: number; y: number }> = {}
  for (const n of nodes) {
    const dn = g.node(n.id)
    // dagre returns node centres; React Flow wants the top-left corner.
    positions[n.id] = dn
      ? { x: dn.x - nodeWidth / 2, y: dn.y - nodeHeight / 2 }
      : { x: 0, y: 0 }
  }
  return positions
}

/**
 * Build the full render model (nodes + edges + layout) from the edit state and
 * the agents cache. Members with no backing agent become ghost nodes so a
 * dangling edge stays visible (and deletable) instead of being dropped.
 */
export function buildTeamGraphModel(
  state: TeamEditState,
  agents: Agent[],
): TeamGraphModel {
  const byId = new Map(agents.map((a) => [a.id, a]))

  // The node set is the union of members + every edge endpoint (defensive).
  const nodeIds = new Set<string>(state.members)
  for (const e of state.edges) {
    nodeIds.add(e.from)
    nodeIds.add(e.to)
  }

  const baseNodes: TeamNodeModel[] = [...nodeIds].map((id) => {
    const a = byId.get(id)
    if (!a) {
      return {
        id,
        name: id,
        type: 'Subagent' as NonNullable<Agent['type']>,
        role: 'deleted',
        color: undefined,
        icon: undefined,
        isDefault: false,
        isWorker: false,
        isGhost: true,
        position: { x: 0, y: 0 },
      }
    }
    return {
      id,
      name: a.name,
      type: (a.type ?? 'Main') as NonNullable<Agent['type']>,
      role: roleLabel(a),
      color: a.color,
      icon: a.icon,
      isDefault: a.default === true,
      isWorker: isWorker(a),
      isGhost: false,
      position: { x: 0, y: 0 },
    }
  })

  const edges: TeamEdgeModel[] = state.edges.map((e) => ({
    id: teamEdgeId(e.from, e.to),
    from: e.from,
    to: e.to,
    modes: e.modes,
    depth: e.depth,
    unknownEndpoint: !byId.has(e.from) || !byId.has(e.to),
  }))

  const positions = layoutTeamGraph(baseNodes, edges)
  const nodes = baseNodes.map((n) => ({ ...n, position: positions[n.id] ?? n.position }))
  return { nodes, edges }
}

// ── Mutations (immutable) ────────────────────────────────────────────────────

export type ConnectionRejection =
  | 'self-edge'
  | 'duplicate'
  | 'worker-source'
  | 'not-member'

/**
 * Validate a candidate edge from → to against the current edit state.
 *   - no self-edges (A → A)
 *   - no duplicate (from, to) pair
 *   - a worker (Subagent / subagent_3p) is a delegation LEAF — it may receive
 *     work but never delegates onward, so it can't be a source
 *   - both endpoints must be team members
 */
export function validateConnection(
  from: string,
  to: string,
  state: TeamEditState,
  workerIds: ReadonlySet<string>,
): ConnectionRejection | null {
  if (from === to) return 'self-edge'
  if (workerIds.has(from)) return 'worker-source'
  if (!state.members.includes(from) || !state.members.includes(to)) return 'not-member'
  if (state.edges.some((e) => e.from === from && e.to === to)) return 'duplicate'
  return null
}

/** Immutably add an edge from → to (seeded with the default modes). */
export function addEdge(
  state: TeamEditState,
  from: string,
  to: string,
  workerIds: ReadonlySet<string>,
): TeamEditState {
  if (validateConnection(from, to, state, workerIds) !== null) return state
  return {
    ...state,
    edges: [...state.edges, { from, to, modes: [...DEFAULT_EDGE_MODES], depth: undefined }],
  }
}

/** Immutably remove the edge from → to. */
export function removeEdge(state: TeamEditState, from: string, to: string): TeamEditState {
  if (!state.edges.some((e) => e.from === from && e.to === to)) return state
  return { ...state, edges: state.edges.filter((e) => !(e.from === from && e.to === to)) }
}

/** Whether `mode` may be toggled OFF on the from→to edge right now. */
export function canToggleModeOff(
  state: TeamEditState,
  from: string,
  to: string,
  mode: DelegationMode,
): boolean {
  const edge = state.edges.find((e) => e.from === from && e.to === to)
  if (!edge) return false
  if (!edge.modes.includes(mode)) return true // turning ON is always fine
  return edge.modes.length > 1
}

/**
 * Immutably toggle a single mode on the from→to edge. Turning ON is always
 * allowed; turning OFF is a NO-OP when it is the last remaining mode (so the
 * edge can never reach `modes: []`, which the backend reads as "all allowed").
 */
export function toggleEdgeMode(
  state: TeamEditState,
  from: string,
  to: string,
  mode: DelegationMode,
): TeamEditState {
  return {
    ...state,
    edges: state.edges.map((e) => {
      if (e.from !== from || e.to !== to) return e
      const has = e.modes.includes(mode)
      if (has && e.modes.length === 1) return e // refuse to drop the last mode
      const modes = has ? e.modes.filter((m) => m !== mode) : [...e.modes, mode]
      return { ...e, modes }
    }),
  }
}

/** Immutably set the from→to edge's depth cap (undefined clears it). */
export function setEdgeDepth(
  state: TeamEditState,
  from: string,
  to: string,
  depth: number | undefined,
): TeamEditState {
  const clean = normalizeDepth(depth)
  return {
    ...state,
    edges: state.edges.map((e) =>
      e.from === from && e.to === to ? { ...e, depth: clean } : e,
    ),
  }
}

/** Immutably add an agent to the team (node only; no edges). No-op if present. */
export function addMember(state: TeamEditState, agentId: string): TeamEditState {
  if (state.members.includes(agentId)) return state
  return { ...state, members: [...state.members, agentId] }
}

/**
 * Immutably remove an agent from the team. Drops the node AND every edge
 * touching it (a delegation to/from a non-member is meaningless).
 */
export function removeMember(state: TeamEditState, agentId: string): TeamEditState {
  return {
    members: state.members.filter((m) => m !== agentId),
    edges: state.edges.filter((e) => e.from !== agentId && e.to !== agentId),
  }
}

// ── Save payload ──────────────────────────────────────────────────────────────

/**
 * Serialise the edit state's edges to the wire `WorkspaceDelegationEdge[]`.
 * `depth` is sent only when explicitly set (the backend treats absence as
 * "inherit default"); `modes` always carries >= 1 entry (the editor guarantees
 * it), so the backend never falls into its empty-modes = "all allowed" branch
 * for an edge the user is actively shaping.
 */
export function buildSaveEdges(state: TeamEditState): WorkspaceDelegationEdge[] {
  return state.edges.map((e) => {
    const out: WorkspaceDelegationEdge = {
      from_agent: e.from,
      to_agent: e.to,
      modes: [...e.modes],
    }
    if (typeof e.depth === 'number') out.depth = e.depth
    return out
  })
}

// ── Equality / reconciliation ────────────────────────────────────────────────

export function editsEqual(a: TeamEditState, b: TeamEditState): boolean {
  if (a.members.length !== b.members.length) return false
  const am = new Set(a.members)
  for (const m of b.members) if (!am.has(m)) return false
  if (a.edges.length !== b.edges.length) return false
  const key = (e: TeamEdgeEdit) =>
    `${e.from}->${e.to}|${[...e.modes].sort().join(',')}|${e.depth ?? ''}`
  const ak = new Set(a.edges.map(key))
  for (const e of b.edges) if (!ak.has(key(e))) return false
  return true
}
