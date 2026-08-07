import dagre from '@dagrejs/dagre'
import type { Agent, WorkspaceDelegationEdge } from '@/lib/api'
import { isWorker } from '@/lib/api'
import { isSystemType } from '@/lib/agentKind'

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

/**
 * "direct" covers what used to be split across `await`/`background` — the
 * delegate tool's sync-vs-background dispatch is a runtime parameter of the
 * tool call itself, not a trust distinction the Team-graph edge gates
 * separately. An edge that allows "direct" allows both call patterns.
 * "task" = task_create-style delegation (a persistent task assigned to
 * another agent), unchanged.
 */
export type DelegationMode = 'direct' | 'task'

export const ALL_MODES: readonly DelegationMode[] = ['direct', 'task']

/**
 * The mode set a NEW edge starts with. CRITICAL (matches the backend contract):
 * the gateway treats an empty/absent `modes` list as **ALL modes allowed**
 * (WorkspaceDelegationEdge schema: "An empty/absent list means all modes are
 * allowed"). So an active edge must never carry `modes: []` while the UI shows
 * "no modes" — that would persist "all allowed", the opposite of intent. A new
 * edge therefore seeds every mode, and the editor refuses to remove the last
 * remaining mode (keeps >= 1). The displayed modes always equal the enforced.
 */
export const DEFAULT_EDGE_MODES: readonly DelegationMode[] = ['direct', 'task']

/**
 * Fallback depth used only when a caller doesn't supply a `default_depth`
 * (e.g. an undefined delegation response before the first load, or a test
 * fixture). Mirrors the backend's own backstop (`defaultMaxSubTurnDepth`) —
 * this is a UI-legibility default, not a duplicated source of truth: any real
 * `WorkspaceDelegation` response always carries a concrete, server-computed
 * `default_depth`.
 */
export const DEFAULT_DEPTH_FALLBACK = 3

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
 * applies". We model an UNSET cap as `undefined` at this normalisation layer —
 * a legacy edge persisted before this UI existed may still carry no depth, and
 * stays that way until the user next touches it (see `TeamEdgeEdit.depth`) —
 * and a real cap as a non-negative integer. A wire `0` is preserved as `0` (an
 * explicit "no onward hop" choice) — only negative / non-finite values
 * collapse to unset. The DISPLAY layer (`EdgeModeEditor`/`EdgeLabelChip`)
 * always resolves an unset depth to the workspace's concrete `defaultDepth`
 * before rendering — there is no user-facing "∞"/blank state anymore.
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
//
// PERSISTENCE NOTE: the DELEGATION PUT body (`updateWorkspaceDelegation`,
// `buildSaveEdges` below) is EDGES-ONLY, and in isolation the backend derives
// ITS team view as `core_team ∪ {every edge endpoint}` — an edgeless member
// wouldn't show up there. But the editor's autosave (WorkspaceTeamTab's
// `saveFn`) also fires a SEPARATE `updateWorkspace({ core_team: <full
// current member list> })` PUT, unconditionally, before the edges PUT — that
// one persists every current member directly, edge or no edge. So an
// edgeless member IS durably kept on the team once autosave lands; the only
// window it can appear to "vanish" in is the transient gap between adding it
// and the debounced save completing (see `isMemberPersisted` / the
// unsaved-member hint in WorkspaceTeamTab, and the D7 fix that made that
// debounce actually fire for an edgeless-only change). Drawing a delegation
// edge is what additionally lets the member delegate/be delegated to — it is
// not required just to keep it on the team.

export interface TeamEdgeEdit {
  from: string
  to: string
  modes: DelegationMode[]
  /**
   * Depth cap (>= 0). `undefined` only ever describes a legacy edge loaded
   * from the wire that has never been touched by this UI — every edge
   * CREATED or EDITED here (`addEdge`, `setEdgeDepth`) is seeded with a
   * concrete value (`TeamEditState.defaultDepth`) instead of `undefined`.
   */
  depth?: number
}

export interface TeamEditState {
  /** Agent ids on the team (node set), order preserved for stable layout. */
  members: string[]
  edges: TeamEdgeEdit[]
  /**
   * The workspace's currently-resolved depth ceiling (wire
   * `WorkspaceDelegation.default_depth`) — a concrete number a new or
   * cleared edge's depth is seeded with, and what the UI displays for any
   * edge whose own `depth` is still unset.
   */
  defaultDepth: number
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
  /**
   * True for a System agent (the Judge, ADR-049 D3) rendered as an IMPLICIT
   * team member. Backend model (`pkg/workspace/find_for_agent.go`'s
   * `isImplicitMember`): every System agent is an implicit member of EVERY
   * workspace — it can never be added to or removed from `core_team` (the
   * gateway 400s that attempt), so membership is resolved implicitly
   * everywhere. RENDER-ONLY: an implicit node is synthesised fresh from the
   * global agents list on every `buildTeamGraphModel` call (see
   * `implicitSystemNodes`) — it is never folded into `TeamEditState.members`
   * and therefore structurally cannot leak into `buildSaveEdges` /
   * `updateWorkspace`'s `core_team` payload (both only ever read
   * `TeamEditState.members`/`.edges`).
   */
  isImplicit: boolean
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
  /** Carried straight through from the edit state — see `TeamEditState.defaultDepth`. */
  defaultDepth: number
}

// ── Building the edit state from the wire ────────────────────────────────────

/**
 * Build the editor working state from the delegation contract. `team` (when the
 * backend computes it) is the authoritative node set; we also fold in every edge
 * endpoint so a malformed response never hides an edge. Falls back to `seedTeam`
 * (e.g. the workspace's core_team) when the response carries no team.
 */
export function buildTeamEditState(
  delegation:
    | { edges?: WorkspaceDelegationEdge[]; team?: string[]; default_depth?: number }
    | undefined,
  seedTeam: string[] = [],
): TeamEditState {
  const defaultDepth = delegation?.default_depth ?? DEFAULT_DEPTH_FALLBACK

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

  return { members: [...members], edges, defaultDepth }
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
 * System agents (the Judge, ADR-049 D3) rendered as implicit, non-removable
 * team-graph nodes. See `TeamNodeModel.isImplicit` for the backend model and
 * the render-only/never-persisted guarantee. `excludeIds` is the real node
 * set already computed from `state` — a System agent can never legitimately
 * land there (AddAgentPicker excludes `type: 'system'` from the add flow,
 * and the backend rejects a `core_team` write containing one), but the guard
 * keeps this function correct even against a hand-built/legacy edit state.
 */
function implicitSystemNodes(
  agents: Agent[],
  excludeIds: ReadonlySet<string>,
): TeamNodeModel[] {
  return agents
    .filter((a) => isSystemType(a.type) && !excludeIds.has(a.id))
    .map((a) => ({
      id: a.id,
      name: a.name,
      type: 'system' as NonNullable<Agent['type']>,
      role: roleLabel(a),
      color: a.color,
      icon: a.icon,
      isDefault: false,
      isWorker: false,
      isGhost: false,
      isImplicit: true,
      position: { x: 0, y: 0 },
    }))
}

/**
 * Build the full render model (nodes + edges + layout) from the edit state and
 * the agents cache. Members with no backing agent become ghost nodes so a
 * dangling edge stays visible (and deletable) instead of being dropped. Every
 * System agent is additionally appended as an implicit, render-only node (see
 * `implicitSystemNodes`) so the Judge is never invisible — it is never part of
 * `state`, so it is included in `nodes` for display but plays no part in the
 * edge/save model above.
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
        isImplicit: false,
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
      isImplicit: false,
      position: { x: 0, y: 0 },
    }
  })

  const allNodes: TeamNodeModel[] = [...baseNodes, ...implicitSystemNodes(agents, nodeIds)]

  const edges: TeamEdgeModel[] = state.edges.map((e) => ({
    id: teamEdgeId(e.from, e.to),
    from: e.from,
    to: e.to,
    modes: e.modes,
    depth: e.depth,
    unknownEndpoint: !byId.has(e.from) || !byId.has(e.to),
  }))

  const positions = layoutTeamGraph(allNodes, edges)
  const nodes = allNodes
    .map((n) => ({ ...n, position: positions[n.id] ?? n.position }))
    // Tab order must follow the VISUAL (top-down) layout, not the member/edge
    // fetch order — otherwise Tab jumps around the canvas unpredictably. Sort
    // by y (rank / depth from the roots) then x (position within a rank),
    // matching reading order for a top-down graph.
    .sort((a, b) => a.position.y - b.position.y || a.position.x - b.position.x)
  return { nodes, edges, defaultDepth: state.defaultDepth }
}

// ── Mutations (immutable) ────────────────────────────────────────────────────

export type ConnectionRejection = 'self-edge' | 'duplicate' | 'not-member' | 'system-target'

/**
 * Validate a candidate edge from → to against the current edit state.
 *   - no self-edges (A → A)
 *   - both endpoints must be team members
 *   - target must not be a System agent (ADR-049 D3/SD-C17)
 *   - no duplicate (from, to) pair
 *
 * Delegation is BOUNDED, not tier-gated: the Sprint-3 backend unlocked onward
 * delegation for any agent (the seed wires Planner, a `Subagent`/worker, →
 * Explorer/Researcher) and bounds depth per-edge instead. So ANY team member —
 * worker or not — may be a delegation SOURCE here; the per-edge depth cap (set
 * in the edge editor) is what limits how far a chain runs. We intentionally do
 * NOT block a worker source — doing so would make the FE unable to draw the very
 * Planner→Researcher edge the backend seeds. The trailing `workerIds` param is
 * kept for signature stability with the call sites but no longer gates the edge.
 *
 * `isSystemTarget` (SD-C17, defense-in-depth): a System agent (the Judge)
 * cannot become a team member through the supported flow at all
 * (`AddAgentPicker.tsx` already excludes `type: 'system'`), so this branch is
 * normally unreachable — but `validateConnection` had no type gate of its
 * own, so a hand-constructed/legacy edge targeting a System agent would have
 * silently validated. Optional (defaults to `false`) so existing callers that
 * don't yet resolve a target's type keep their current behavior.
 */
export function validateConnection(
  from: string,
  to: string,
  state: TeamEditState,
  _workerIds?: ReadonlySet<string>,
  isSystemTarget?: boolean,
): ConnectionRejection | null {
  if (from === to) return 'self-edge'
  if (!state.members.includes(from) || !state.members.includes(to)) return 'not-member'
  if (isSystemTarget) return 'system-target'
  if (state.edges.some((e) => e.from === from && e.to === to)) return 'duplicate'
  return null
}

/** Plain-language, per-repo-copywriting-convention message for each rejection reason. */
export const REJECTION_MESSAGE: Record<ConnectionRejection, string> = {
  'self-edge': 'An agent cannot delegate to itself.',
  duplicate: 'That delegation edge already exists.',
  'not-member': 'Both agents must be on the team first.',
  'system-target': 'System agents cannot be a delegation target.',
}

/**
 * Derive the toast message for a FAILED connection drag, given the
 * React Flow `FinalConnectionState`-shaped from/to node ids and validity.
 *
 * React Flow only invokes `onConnect` when `isValidConnection` passed — a
 * rejected drop (self-edge, duplicate, non-member) never reaches `onConnect`
 * at all, so a rejection handler wired there is unreachable dead code. The
 * drag is instead silently swallowed: no edge, no feedback (e.g. dragging a
 * handle back onto its own node produces a silent no-op). The one event
 * React Flow ALWAYS fires, valid or not, is `onConnectEnd` — this helper
 * recomputes the same rejection reason from its `FinalConnectionState` so a
 * caller wired to `onConnectEnd` can surface it.
 *
 * Returns `null` when there's nothing to report: the connection succeeded
 * (`isValid !== false`), or the drag was released without ever settling over
 * an identifiable target node (e.g. dropped on empty canvas — a normal
 * "cancel the drag" gesture, not a rejected attempt).
 */
export function rejectionMessageForFailedConnection(
  fromId: string | null | undefined,
  toId: string | null | undefined,
  isValid: boolean | null,
  state: TeamEditState,
  workerIds?: ReadonlySet<string>,
  isSystemTarget?: boolean,
): string | null {
  if (isValid !== false) return null
  if (!fromId || !toId) return null
  const reason = validateConnection(fromId, toId, state, workerIds, isSystemTarget)
  if (reason === null) return null
  return REJECTION_MESSAGE[reason] ?? 'Connection not allowed.'
}

/**
 * Immutably add an edge from → to, seeded with the default modes and the
 * workspace's concrete `defaultDepth` (never `undefined` — depth is always a
 * real number for an edge the user actively creates here).
 */
export function addEdge(
  state: TeamEditState,
  from: string,
  to: string,
  workerIds?: ReadonlySet<string>,
): TeamEditState {
  if (validateConnection(from, to, state, workerIds) !== null) return state
  return {
    ...state,
    edges: [
      ...state.edges,
      { from, to, modes: [...DEFAULT_EDGE_MODES], depth: state.defaultDepth },
    ],
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

/**
 * Immutably set the from→to edge's depth cap. Passing `undefined` (e.g. the
 * editor's depth field cleared to empty) no longer leaves the edge unset —
 * it resolves to the workspace's concrete `defaultDepth` instead, since depth
 * is always a real number for an edge the user is actively editing.
 */
export function setEdgeDepth(
  state: TeamEditState,
  from: string,
  to: string,
  depth: number | undefined,
): TeamEditState {
  const clean = normalizeDepth(depth) ?? state.defaultDepth
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
    ...state,
    members: state.members.filter((m) => m !== agentId),
    edges: state.edges.filter((e) => e.from !== agentId && e.to !== agentId),
  }
}

// ── Save payload ──────────────────────────────────────────────────────────────

/**
 * Serialise the edit state's edges to the wire `WorkspaceDelegationEdge[]`.
 *
 * CRITICAL — granularity is per-EDGE-TOUCH, not per-graph-save. `depth` is
 * carried through EXACTLY as it sits in the edit state: `e.depth` is only
 * ever a concrete number when the user actively created this edge (`addEdge`)
 * or explicitly set its depth (`setEdgeDepth`) — both seed a real number on
 * purpose. Every other edge — loaded from the wire with an unset depth and
 * never touched by either path this session — keeps `depth: undefined` here,
 * which `JSON.stringify` (see `updateWorkspaceDelegation`) omits from the
 * request body entirely. That is load-bearing: this save PUTs the WHOLE edge
 * set (not a per-edge patch — see `WorkspaceTeamTab.saveFn`), so if we
 * defaulted every unset depth to `state.defaultDepth` here, ONE unrelated
 * autosave (toggling a mode on a different edge, adding one new member) would
 * silently freeze the depth of EVERY untouched edge in the graph, permanently
 * opting them out of "dynamically track the live global default" (the
 * documented contract semantics — see `WorkspaceDelegationEdge.depth` in
 * `contracts/components/schemas/WorkspaceDelegation.yaml`) with zero
 * indication to the operator. Do NOT reintroduce `e.depth ?? state.defaultDepth`
 * here — that was exactly this bug. `modes` always carries >= 1 entry (the
 * editor guarantees it), so the backend never falls into its empty-modes =
 * "all allowed" branch for an edge the user is actively shaping.
 */
export function buildSaveEdges(state: TeamEditState): WorkspaceDelegationEdge[] {
  return state.edges.map((e) => ({
    from_agent: e.from,
    to_agent: e.to,
    modes: [...e.modes],
    depth: e.depth,
  }))
}

// ── Membership persistence ───────────────────────────────────────────────────
//
// The DELEGATION PUT (edges endpoint) persists EDGES only; the backend derives
// ITS view of team as core_team ∪ edge endpoints. In isolation that would mean
// an edgeless, non-core member never durably lands. In practice it does:
// WorkspaceTeamTab's `saveFn` always fires a SEPARATE PUT to the workspace
// itself (`updateWorkspace({ core_team: <full current member list> })`)
// BEFORE the edges PUT, and that PUT sets `core_team` directly — no edge
// required (see WorkspaceTeamTab.tsx's saveFn comment, and the D7 fix that
// made the debounced autosave actually FIRE for an edgeless-member-only
// change, which it previously did not).
//
// So "unsaved" here means "not yet reflected in the last-fetched
// `workspace.core_team`" — a transient window until the next autosave
// round-trip lands, not a permanent loss. These helpers let the UI flag that
// window honestly (see the "not saved yet" banner in WorkspaceTeamTab).

/** True if `agentId` will survive a save (core member, or has an incident edge). */
export function isMemberPersisted(
  state: TeamEditState,
  agentId: string,
  coreTeam: ReadonlySet<string>,
): boolean {
  if (coreTeam.has(agentId)) return true
  return state.edges.some((e) => e.from === agentId || e.to === agentId)
}

/**
 * Members that the next save will silently drop: on the team's node set but not
 * in core_team and not touched by any edge. The UI hints the user to connect
 * them with a delegation edge to keep them.
 */
export function unsavedMembers(
  state: TeamEditState,
  coreTeam: ReadonlySet<string>,
): string[] {
  return state.members.filter((id) => !isMemberPersisted(state, id, coreTeam))
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
