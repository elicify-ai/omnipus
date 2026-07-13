import { describe, it, expect } from 'vitest'
import type { Agent } from '@/lib/api'
import {
  addEdge,
  addMember,
  buildSaveEdges,
  buildTeamEditState,
  buildTeamGraphModel,
  canToggleModeOff,
  DEFAULT_DEPTH_FALLBACK,
  editsEqual,
  isMemberPersisted,
  unsavedMembers,
  normalizeDepth,
  normalizeModes,
  rejectionMessageForFailedConnection,
  removeEdge,
  removeMember,
  setEdgeDepth,
  teamEdgeId,
  toggleEdgeMode,
  validateConnection,
  REJECTION_MESSAGE,
  type TeamEditState,
} from './teamGraphModel'

// ── Test fixtures ─────────────────────────────────────────────────────────────

function agent(id: string, over: Partial<Agent> = {}): Agent {
  return {
    id,
    name: id.charAt(0).toUpperCase() + id.slice(1),
    type: 'Main',
    locked: false,
    status: 'active',
    soul: '',
    heartbeat: '',
    instructions: '',
    timeout_seconds: 60,
    max_tool_iterations: 10,
    steering_mode: 'one-at-a-time',
    heartbeat_enabled: false,
    heartbeat_interval: 0,
    ...over,
  } as Agent
}

const AGENTS: Agent[] = [
  agent('mia', { name: 'Mia', default: true, color: '#d4af37', icon: 'Robot' }),
  agent('jim', { name: 'Jim' }),
  agent('planner', { name: 'Planner', type: 'Subagent' }),
  agent('explorer', { name: 'Explorer', type: 'Subagent' }),
]

const WORKER_IDS = new Set(['planner', 'explorer'])

// A concrete depth distinct from DEFAULT_DEPTH_FALLBACK so tests can tell
// "seeded from the workspace's real default_depth" apart from "fell back to
// the FE's own defensive constant".
const WS_DEFAULT_DEPTH = 5

function state(over: Partial<TeamEditState> = {}): TeamEditState {
  return { members: [], edges: [], defaultDepth: WS_DEFAULT_DEPTH, ...over }
}

// ── normalize ─────────────────────────────────────────────────────────────────

describe('normalizeModes', () => {
  it('keeps valid modes, drops invalid + duplicates', () => {
    expect(normalizeModes(['direct', 'task', 'direct', 'bogus'])).toEqual(['direct', 'task'])
  })
  it('drops the retired await/background enum values', () => {
    expect(normalizeModes(['await', 'background', 'task'])).toEqual(['task'])
  })
  it('returns [] for non-arrays', () => {
    expect(normalizeModes(undefined)).toEqual([])
    expect(normalizeModes('direct')).toEqual([])
  })
})

describe('normalizeDepth', () => {
  it('keeps non-negative integers, drops negatives / non-finite', () => {
    expect(normalizeDepth(3)).toBe(3)
    expect(normalizeDepth(0)).toBe(0)
    expect(normalizeDepth(-1)).toBeUndefined()
    expect(normalizeDepth(NaN)).toBeUndefined()
    expect(normalizeDepth('3' as unknown)).toBeUndefined()
  })
})

// ── buildTeamEditState ──────────────────────────────────────────────────────

describe('buildTeamEditState', () => {
  it('builds members (team ∪ edge endpoints), edges, and defaultDepth from the contract', () => {
    const s = buildTeamEditState({
      team: ['mia', 'jim'],
      edges: [{ from_agent: 'jim', to_agent: 'planner', modes: ['direct'], depth: 2 }],
      default_depth: WS_DEFAULT_DEPTH,
    })
    // planner is referenced by an edge, so it joins the member set.
    expect(new Set(s.members)).toEqual(new Set(['mia', 'jim', 'planner']))
    expect(s.edges).toEqual([{ from: 'jim', to: 'planner', modes: ['direct'], depth: 2 }])
    expect(s.defaultDepth).toBe(WS_DEFAULT_DEPTH)
  })

  it('falls back to seedTeam (core_team) when team is empty', () => {
    const s = buildTeamEditState({ edges: [], default_depth: WS_DEFAULT_DEPTH }, ['mia', 'jim'])
    expect(new Set(s.members)).toEqual(new Set(['mia', 'jim']))
    expect(s.edges).toEqual([])
  })

  it('handles an undefined delegation gracefully, falling back to DEFAULT_DEPTH_FALLBACK', () => {
    const s = buildTeamEditState(undefined, ['mia'])
    expect(s.members).toEqual(['mia'])
    expect(s.edges).toEqual([])
    expect(s.defaultDepth).toBe(DEFAULT_DEPTH_FALLBACK)
  })

  it('falls back to DEFAULT_DEPTH_FALLBACK when default_depth is absent from the response', () => {
    const s = buildTeamEditState({ edges: [] })
    expect(s.defaultDepth).toBe(DEFAULT_DEPTH_FALLBACK)
  })

  it('preserves an unset depth on a legacy edge (never touched by this UI)', () => {
    const s = buildTeamEditState({
      edges: [{ from_agent: 'mia', to_agent: 'jim', modes: ['direct'] }],
      default_depth: WS_DEFAULT_DEPTH,
    })
    expect(s.edges[0].depth).toBeUndefined()
  })
})

// ── buildTeamGraphModel ──────────────────────────────────────────────────────

describe('buildTeamGraphModel', () => {
  const s: TeamEditState = state({
    members: ['mia', 'jim', 'planner'],
    edges: [
      { from: 'mia', to: 'jim', modes: ['direct'], depth: undefined },
      { from: 'jim', to: 'planner', modes: ['task'], depth: 2 },
    ],
  })

  it('renders one node per team agent with name/role/default/worker flags', () => {
    const { nodes } = buildTeamGraphModel(s, AGENTS)
    expect(nodes).toHaveLength(3)
    const mia = nodes.find((n) => n.id === 'mia')!
    expect(mia.name).toBe('Mia')
    expect(mia.isDefault).toBe(true)
    expect(mia.isWorker).toBe(false)
    expect(mia.role).toBe('Main agent')
    const planner = nodes.find((n) => n.id === 'planner')!
    expect(planner.isWorker).toBe(true)
    expect(planner.role).toBe('Subagent')
  })

  it('renders the delegation edges with their own modes + depth', () => {
    const { edges } = buildTeamGraphModel(s, AGENTS)
    expect(edges).toHaveLength(2)
    const e1 = edges.find((e) => e.id === teamEdgeId('mia', 'jim'))!
    expect(e1.from).toBe('mia')
    expect(e1.to).toBe('jim')
    expect(e1.modes).toEqual(['direct'])
    expect(e1.depth).toBeUndefined()
    const e2 = edges.find((e) => e.id === teamEdgeId('jim', 'planner'))!
    expect(e2.modes).toEqual(['task'])
    expect(e2.depth).toBe(2)
  })

  it('carries the edit state defaultDepth through to the render model', () => {
    const { defaultDepth } = buildTeamGraphModel(s, AGENTS)
    expect(defaultDepth).toBe(WS_DEFAULT_DEPTH)
  })

  it('synthesises a ghost node for an edge endpoint missing from the roster', () => {
    const withGhost: TeamEditState = state({
      members: ['mia', 'deleted-agent'],
      edges: [{ from: 'mia', to: 'deleted-agent', modes: ['direct'] }],
    })
    const { nodes, edges } = buildTeamGraphModel(withGhost, AGENTS)
    const ghost = nodes.find((n) => n.id === 'deleted-agent')!
    expect(ghost.isGhost).toBe(true)
    expect(edges[0].unknownEndpoint).toBe(true)
  })

  it('gives every node a layout position', () => {
    const { nodes } = buildTeamGraphModel(s, AGENTS)
    for (const n of nodes) {
      expect(typeof n.position.x).toBe('number')
      expect(typeof n.position.y).toBe('number')
    }
  })
})

// ── validateConnection ──────────────────────────────────────────────────────

describe('validateConnection', () => {
  const s: TeamEditState = state({
    members: ['mia', 'jim', 'planner'],
    edges: [{ from: 'mia', to: 'jim', modes: ['direct'] }],
  })

  it('rejects self-edges', () => {
    expect(validateConnection('mia', 'mia', s, WORKER_IDS)).toBe('self-edge')
  })
  it('ALLOWS a worker as a delegation source (bounded delegation, not tier-gated)', () => {
    // The backend unlocked onward delegation for any agent and bounds depth
    // per-edge — so a worker (Subagent) CAN be a source. This is the exact
    // edge the backend seeds (Planner → Explorer/Researcher).
    const s2 = { ...s, members: [...s.members, 'explorer'] }
    expect(validateConnection('planner', 'explorer', s2, WORKER_IDS)).toBeNull()
  })
  it('allows the seeded Planner→worker delegation edge to be drawn', () => {
    // Planner and Explorer are both workers; the user must be able to recreate
    // the seeded onward edge between them.
    const s2 = { ...s, members: [...s.members, 'explorer'] }
    expect(validateConnection('planner', 'explorer', s2, WORKER_IDS)).toBeNull()
  })
  it('rejects duplicate edges', () => {
    expect(validateConnection('mia', 'jim', s, WORKER_IDS)).toBe('duplicate')
  })
  it('rejects an endpoint that is not a team member', () => {
    expect(validateConnection('mia', 'ray', s, WORKER_IDS)).toBe('not-member')
  })
  it('allows a valid new edge', () => {
    expect(validateConnection('jim', 'planner', s, WORKER_IDS)).toBeNull()
  })
})

// ── rejectionMessageForFailedConnection — live-UAT regression ──
// React Flow only calls `onConnect` when `isValidConnection` passed, so a
// rejected drag (self-edge in particular) never reaches a handler wired to
// `onConnect` — it's silently swallowed with zero feedback. The fix wires
// `onConnectEnd` (which React Flow ALWAYS fires) to this helper instead.

describe('rejectionMessageForFailedConnection', () => {
  const s: TeamEditState = state({
    members: ['mia', 'jim', 'planner'],
    edges: [{ from: 'mia', to: 'jim', modes: ['direct'] }],
  })

  it('surfaces the self-edge message for a rejected self-drag (jim -> jim)', () => {
    // This is the exact drag the bug report reproduces: a handle dragged back
    // onto its own node.
    expect(rejectionMessageForFailedConnection('jim', 'jim', false, s, WORKER_IDS)).toBe(
      REJECTION_MESSAGE['self-edge'],
    )
  })

  it('surfaces the duplicate-edge message for a rejected re-drag of an existing edge', () => {
    expect(rejectionMessageForFailedConnection('mia', 'jim', false, s, WORKER_IDS)).toBe(
      REJECTION_MESSAGE['duplicate'],
    )
  })

  it('surfaces the not-member message for a rejected drag to a non-team id', () => {
    expect(rejectionMessageForFailedConnection('mia', 'ray', false, s, WORKER_IDS)).toBe(
      REJECTION_MESSAGE['not-member'],
    )
  })

  it('returns null when the connection was actually valid (isValid !== false)', () => {
    // onConnect already handles the success path — onConnectEnd must stay
    // silent so a successful drag doesn't ALSO pop a toast.
    expect(rejectionMessageForFailedConnection('jim', 'planner', true, s, WORKER_IDS)).toBeNull()
    expect(rejectionMessageForFailedConnection('jim', 'planner', null, s, WORKER_IDS)).toBeNull()
  })

  it('returns null when the drag never settled on an identifiable node (dropped on empty canvas)', () => {
    // A cancelled drag with no target handle nearby — not a rejected attempt,
    // just the user letting go. Must not produce a spurious toast.
    expect(rejectionMessageForFailedConnection('jim', null, false, s, WORKER_IDS)).toBeNull()
    expect(rejectionMessageForFailedConnection(undefined, 'jim', false, s, WORKER_IDS)).toBeNull()
    expect(rejectionMessageForFailedConnection(null, null, false, s, WORKER_IDS)).toBeNull()
  })
})

// ── membership persistence (edges-only PUT, backend derives team) ────────────

describe('isMemberPersisted / unsavedMembers', () => {
  const core = new Set(['mia'])

  it('treats a core_team member as persisted even with no edge', () => {
    const s: TeamEditState = state({ members: ['mia'], edges: [] })
    expect(isMemberPersisted(s, 'mia', core)).toBe(true)
    expect(unsavedMembers(s, core)).toEqual([])
  })

  it('treats a member with an incident edge as persisted (as source or target)', () => {
    const s: TeamEditState = state({
      members: ['mia', 'jim', 'planner'],
      edges: [{ from: 'jim', to: 'planner', modes: ['direct'] }],
    })
    expect(isMemberPersisted(s, 'jim', core)).toBe(true)
    expect(isMemberPersisted(s, 'planner', core)).toBe(true)
    expect(unsavedMembers(s, core)).toEqual([])
  })

  it('flags an edgeless, non-core member as unsaved (it would vanish on refetch)', () => {
    const s: TeamEditState = state({
      members: ['mia', 'jim', 'explorer'],
      edges: [{ from: 'mia', to: 'jim', modes: ['direct'] }],
    })
    expect(isMemberPersisted(s, 'explorer', core)).toBe(false)
    expect(unsavedMembers(s, core)).toEqual(['explorer'])
  })
})

// ── edge mutations ───────────────────────────────────────────────────────────

describe('addEdge', () => {
  it('adds an edge seeded with all default modes and the workspace defaultDepth', () => {
    const s: TeamEditState = state({ members: ['mia', 'jim'], edges: [] })
    const next = addEdge(s, 'mia', 'jim', WORKER_IDS)
    expect(next.edges).toHaveLength(1)
    expect(next.edges[0]).toMatchObject({ from: 'mia', to: 'jim' })
    // Default modes are non-empty so the backend never reads "all allowed".
    expect(next.edges[0].modes).toEqual(['direct', 'task'])
    // A brand-new edge always carries a CONCRETE depth — never undefined.
    expect(next.edges[0].depth).toBe(WS_DEFAULT_DEPTH)
  })
  it('is a no-op on an invalid (duplicate) connection', () => {
    const s: TeamEditState = state({
      members: ['mia', 'jim'],
      edges: [{ from: 'mia', to: 'jim', modes: ['direct'] }],
    })
    expect(addEdge(s, 'mia', 'jim', WORKER_IDS)).toBe(s)
  })
})

describe('removeEdge', () => {
  it('drops the matching edge', () => {
    const s: TeamEditState = state({
      members: ['mia', 'jim'],
      edges: [{ from: 'mia', to: 'jim', modes: ['direct'] }],
    })
    expect(removeEdge(s, 'mia', 'jim').edges).toEqual([])
  })
})

describe('toggleEdgeMode', () => {
  const base: TeamEditState = state({
    members: ['mia', 'jim'],
    edges: [{ from: 'mia', to: 'jim', modes: ['direct', 'task'] }],
  })

  it('turns a mode off', () => {
    const next = toggleEdgeMode(base, 'mia', 'jim', 'task')
    expect(next.edges[0].modes).toEqual(['direct'])
  })
  it('turns a mode on (an everyday path with only 2 modes: re-enable after removing one)', () => {
    // With the vocabulary down to exactly 2 values, "toggle the second mode
    // back on after removing the first" is a routine user path — assert the
    // "mode not yet present -> add it" branch appends (correct order, no
    // dedup issue) rather than only exercising the "turn it off" branch.
    const single: TeamEditState = state({
      members: ['mia', 'jim'],
      edges: [{ from: 'mia', to: 'jim', modes: ['direct'] }],
    })
    expect(canToggleModeOff(single, 'mia', 'jim', 'task')).toBe(true) // turning ON is always fine
    const next = toggleEdgeMode(single, 'mia', 'jim', 'task')
    expect(next.edges[0].modes).toEqual(['direct', 'task'])
  })
  it('refuses to remove the LAST remaining mode (no empty modes)', () => {
    const single: TeamEditState = state({
      members: ['mia', 'jim'],
      edges: [{ from: 'mia', to: 'jim', modes: ['direct'] }],
    })
    const next = toggleEdgeMode(single, 'mia', 'jim', 'direct')
    expect(next.edges[0].modes).toEqual(['direct'])
    expect(canToggleModeOff(single, 'mia', 'jim', 'direct')).toBe(false)
  })
  it('only mutates the targeted edge', () => {
    const two: TeamEditState = state({
      members: ['mia', 'jim', 'ray'],
      edges: [
        { from: 'mia', to: 'jim', modes: ['direct', 'task'] },
        { from: 'mia', to: 'ray', modes: ['direct', 'task'] },
      ],
    })
    const next = toggleEdgeMode(two, 'mia', 'jim', 'task')
    expect(next.edges[0].modes).toEqual(['direct'])
    expect(next.edges[1].modes).toEqual(['direct', 'task'])
  })
})

describe('setEdgeDepth', () => {
  const base: TeamEditState = state({
    members: ['mia', 'jim'],
    edges: [{ from: 'mia', to: 'jim', modes: ['direct'] }],
  })
  it('sets a positive cap', () => {
    expect(setEdgeDepth(base, 'mia', 'jim', 4).edges[0].depth).toBe(4)
  })
  it('resets to the workspace defaultDepth when passed undefined (never leaves it unset)', () => {
    const set = setEdgeDepth(base, 'mia', 'jim', 4)
    expect(setEdgeDepth(set, 'mia', 'jim', undefined).edges[0].depth).toBe(WS_DEFAULT_DEPTH)
  })
  it('resets a never-set (legacy) edge to defaultDepth when passed undefined', () => {
    expect(setEdgeDepth(base, 'mia', 'jim', undefined).edges[0].depth).toBe(WS_DEFAULT_DEPTH)
  })
})

// ── membership mutations ─────────────────────────────────────────────────────

describe('addMember / removeMember', () => {
  it('adds an agent as an isolated node', () => {
    const s: TeamEditState = state({ members: ['mia'], edges: [] })
    expect(addMember(s, 'jim').members).toEqual(['mia', 'jim'])
  })
  it('is a no-op for an existing member', () => {
    const s: TeamEditState = state({ members: ['mia'], edges: [] })
    expect(addMember(s, 'mia')).toBe(s)
  })
  it('removes the node AND every edge touching it', () => {
    const s: TeamEditState = state({
      members: ['mia', 'jim', 'planner'],
      edges: [
        { from: 'mia', to: 'jim', modes: ['direct'] },
        { from: 'jim', to: 'planner', modes: ['task'] },
      ],
    })
    const next = removeMember(s, 'jim')
    expect(next.members).toEqual(['mia', 'planner'])
    expect(next.edges).toEqual([]) // both edges touched jim
  })
})

// ── buildSaveEdges ───────────────────────────────────────────────────────────
//
// Regression coverage for the 4-reviewer-confirmed bug: `buildSaveEdges` used
// to coalesce EVERY edge's unset depth to `state.defaultDepth`, which — since
// this save PUTs the WHOLE edge set on every autosave, not a per-edge patch —
// silently froze the depth of edges the user never touched this session. The
// fix: `depth` is carried through exactly as it sits in the edit state, so an
// edge that is still `undefined` (never created via `addEdge` nor explicitly
// set via `setEdgeDepth`) stays `undefined` in the wire payload too (which
// `JSON.stringify` then omits from the actual request body, preserving the
// backend's "dynamically inherit the live global default" semantics).

describe('buildSaveEdges', () => {
  it('serialises to WorkspaceDelegationEdge[] with modes, preserving a concrete depth', () => {
    const s: TeamEditState = state({
      members: ['mia', 'jim', 'planner'],
      edges: [
        { from: 'mia', to: 'jim', modes: ['direct'], depth: undefined },
        { from: 'jim', to: 'planner', modes: ['task'], depth: 2 },
      ],
    })
    const wire = buildSaveEdges(s)
    expect(wire).toEqual([
      { from_agent: 'mia', to_agent: 'jim', modes: ['direct'], depth: undefined },
      { from_agent: 'jim', to_agent: 'planner', modes: ['task'], depth: 2 },
    ])
  })

  it('preserves an unset depth for a legacy/untouched edge instead of coalescing to defaultDepth', () => {
    const s: TeamEditState = state({
      members: ['mia', 'jim', 'planner', 'ray'],
      edges: [
        { from: 'mia', to: 'jim', modes: ['direct'] }, // no depth field at all
        { from: 'mia', to: 'planner', modes: ['task'], depth: undefined },
        { from: 'mia', to: 'ray', modes: ['direct'], depth: 0 }, // explicit "no onward hop"
      ],
    })
    const wire = buildSaveEdges(s)
    expect(wire).toHaveLength(3)
    // The explicit 0 ("no onward hop") must be preserved, not coerced to the default.
    expect(wire.find((e) => e.to_agent === 'ray')?.depth).toBe(0)
    // The two truly-unset edges stay unset — NOT coerced to state.defaultDepth.
    expect(wire.find((e) => e.to_agent === 'jim')?.depth).toBeUndefined()
    expect(wire.find((e) => e.to_agent === 'planner')?.depth).toBeUndefined()
  })

  it('regression: an unrelated edge touch (depth change on edge A) must NOT freeze edge B\'s unset depth', () => {
    // The exact scenario the 4 reviewers flagged: an operator explicitly sets
    // depth on ONE edge; a sibling edge they never opened this session must
    // survive the very next autosave with its depth still unset/dynamic.
    const base: TeamEditState = state({
      members: ['mia', 'jim', 'planner'],
      edges: [
        { from: 'mia', to: 'jim', modes: ['direct'] }, // untouched, legacy unset depth
        { from: 'jim', to: 'planner', modes: ['task'] }, // will be explicitly touched
      ],
    })
    const touched = setEdgeDepth(base, 'jim', 'planner', 7)

    const wire = buildSaveEdges(touched)
    // The explicitly-touched edge carries the concrete value the user set.
    expect(wire.find((e) => e.from_agent === 'jim' && e.to_agent === 'planner')?.depth).toBe(7)
    // The untouched sibling edge must still be unset in the save payload —
    // NOT silently coerced to state.defaultDepth by this unrelated save.
    expect(
      wire.find((e) => e.from_agent === 'mia' && e.to_agent === 'jim')?.depth,
    ).toBeUndefined()
  })
})

// ── editsEqual ───────────────────────────────────────────────────────────────

describe('editsEqual', () => {
  it('is order-independent for members and edges + mode order', () => {
    const a: TeamEditState = state({
      members: ['mia', 'jim'],
      edges: [{ from: 'mia', to: 'jim', modes: ['direct', 'task'] }],
    })
    const b: TeamEditState = state({
      members: ['jim', 'mia'],
      edges: [{ from: 'mia', to: 'jim', modes: ['task', 'direct'] }],
    })
    expect(editsEqual(a, b)).toBe(true)
  })
  it('detects a changed depth', () => {
    const a: TeamEditState = state({
      members: ['mia', 'jim'],
      edges: [{ from: 'mia', to: 'jim', modes: ['direct'], depth: 1 }],
    })
    const b: TeamEditState = state({
      members: ['mia', 'jim'],
      edges: [{ from: 'mia', to: 'jim', modes: ['direct'], depth: 2 }],
    })
    expect(editsEqual(a, b)).toBe(false)
  })
})
