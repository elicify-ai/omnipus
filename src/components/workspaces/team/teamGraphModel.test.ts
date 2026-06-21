import { describe, it, expect } from 'vitest'
import type { Agent } from '@/lib/api'
import {
  addEdge,
  addMember,
  buildSaveEdges,
  buildTeamEditState,
  buildTeamGraphModel,
  canToggleModeOff,
  editsEqual,
  isMemberPersisted,
  unsavedMembers,
  normalizeDepth,
  normalizeModes,
  removeEdge,
  removeMember,
  setEdgeDepth,
  teamEdgeId,
  toggleEdgeMode,
  validateConnection,
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

// ── normalize ─────────────────────────────────────────────────────────────────

describe('normalizeModes', () => {
  it('keeps valid modes, drops invalid + duplicates', () => {
    expect(normalizeModes(['await', 'task', 'await', 'bogus'])).toEqual(['await', 'task'])
  })
  it('returns [] for non-arrays', () => {
    expect(normalizeModes(undefined)).toEqual([])
    expect(normalizeModes('await')).toEqual([])
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
  it('builds members (team ∪ edge endpoints) and edges from the contract', () => {
    const state = buildTeamEditState({
      team: ['mia', 'jim'],
      edges: [{ from_agent: 'jim', to_agent: 'planner', modes: ['await'], depth: 2 }],
    })
    // planner is referenced by an edge, so it joins the member set.
    expect(new Set(state.members)).toEqual(new Set(['mia', 'jim', 'planner']))
    expect(state.edges).toEqual([
      { from: 'jim', to: 'planner', modes: ['await'], depth: 2 },
    ])
  })

  it('falls back to seedTeam (core_team) when team is empty', () => {
    const state = buildTeamEditState({ edges: [] }, ['mia', 'jim'])
    expect(new Set(state.members)).toEqual(new Set(['mia', 'jim']))
    expect(state.edges).toEqual([])
  })

  it('handles an undefined delegation gracefully', () => {
    const state = buildTeamEditState(undefined, ['mia'])
    expect(state.members).toEqual(['mia'])
    expect(state.edges).toEqual([])
  })
})

// ── buildTeamGraphModel ──────────────────────────────────────────────────────

describe('buildTeamGraphModel', () => {
  const state: TeamEditState = {
    members: ['mia', 'jim', 'planner'],
    edges: [
      { from: 'mia', to: 'jim', modes: ['await', 'background'], depth: undefined },
      { from: 'jim', to: 'planner', modes: ['task'], depth: 2 },
    ],
  }

  it('renders one node per team agent with name/role/default/worker flags', () => {
    const { nodes } = buildTeamGraphModel(state, AGENTS)
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
    const { edges } = buildTeamGraphModel(state, AGENTS)
    expect(edges).toHaveLength(2)
    const e1 = edges.find((e) => e.id === teamEdgeId('mia', 'jim'))!
    expect(e1.from).toBe('mia')
    expect(e1.to).toBe('jim')
    expect(e1.modes).toEqual(['await', 'background'])
    expect(e1.depth).toBeUndefined()
    const e2 = edges.find((e) => e.id === teamEdgeId('jim', 'planner'))!
    expect(e2.modes).toEqual(['task'])
    expect(e2.depth).toBe(2)
  })

  it('synthesises a ghost node for an edge endpoint missing from the roster', () => {
    const withGhost: TeamEditState = {
      members: ['mia', 'deleted-agent'],
      edges: [{ from: 'mia', to: 'deleted-agent', modes: ['await'] }],
    }
    const { nodes, edges } = buildTeamGraphModel(withGhost, AGENTS)
    const ghost = nodes.find((n) => n.id === 'deleted-agent')!
    expect(ghost.isGhost).toBe(true)
    expect(edges[0].unknownEndpoint).toBe(true)
  })

  it('gives every node a layout position', () => {
    const { nodes } = buildTeamGraphModel(state, AGENTS)
    for (const n of nodes) {
      expect(typeof n.position.x).toBe('number')
      expect(typeof n.position.y).toBe('number')
    }
  })
})

// ── validateConnection ──────────────────────────────────────────────────────

describe('validateConnection', () => {
  const state: TeamEditState = {
    members: ['mia', 'jim', 'planner'],
    edges: [{ from: 'mia', to: 'jim', modes: ['await'] }],
  }

  it('rejects self-edges', () => {
    expect(validateConnection('mia', 'mia', state, WORKER_IDS)).toBe('self-edge')
  })
  it('ALLOWS a worker as a delegation source (bounded delegation, not tier-gated)', () => {
    // The backend unlocked onward delegation for any agent and bounds depth
    // per-edge — so a worker (Subagent) CAN be a source. This is the exact
    // edge the backend seeds (Planner → Explorer/Researcher).
    const s = { ...state, members: [...state.members, 'explorer'] }
    expect(validateConnection('planner', 'explorer', s, WORKER_IDS)).toBeNull()
  })
  it('allows the seeded Planner→worker delegation edge to be drawn', () => {
    // Planner and Explorer are both workers; the user must be able to recreate
    // the seeded onward edge between them.
    const s = { ...state, members: [...state.members, 'explorer'] }
    expect(validateConnection('planner', 'explorer', s, WORKER_IDS)).toBeNull()
  })
  it('rejects duplicate edges', () => {
    expect(validateConnection('mia', 'jim', state, WORKER_IDS)).toBe('duplicate')
  })
  it('rejects an endpoint that is not a team member', () => {
    expect(validateConnection('mia', 'ray', state, WORKER_IDS)).toBe('not-member')
  })
  it('allows a valid new edge', () => {
    expect(validateConnection('jim', 'planner', state, WORKER_IDS)).toBeNull()
  })
})

// ── membership persistence (edges-only PUT, backend derives team) ────────────

describe('isMemberPersisted / unsavedMembers', () => {
  const core = new Set(['mia'])

  it('treats a core_team member as persisted even with no edge', () => {
    const s: TeamEditState = { members: ['mia'], edges: [] }
    expect(isMemberPersisted(s, 'mia', core)).toBe(true)
    expect(unsavedMembers(s, core)).toEqual([])
  })

  it('treats a member with an incident edge as persisted (as source or target)', () => {
    const s: TeamEditState = {
      members: ['mia', 'jim', 'planner'],
      edges: [{ from: 'jim', to: 'planner', modes: ['await'] }],
    }
    expect(isMemberPersisted(s, 'jim', core)).toBe(true)
    expect(isMemberPersisted(s, 'planner', core)).toBe(true)
    expect(unsavedMembers(s, core)).toEqual([])
  })

  it('flags an edgeless, non-core member as unsaved (it would vanish on refetch)', () => {
    const s: TeamEditState = {
      members: ['mia', 'jim', 'explorer'],
      edges: [{ from: 'mia', to: 'jim', modes: ['await'] }],
    }
    expect(isMemberPersisted(s, 'explorer', core)).toBe(false)
    expect(unsavedMembers(s, core)).toEqual(['explorer'])
  })
})

// ── edge mutations ───────────────────────────────────────────────────────────

describe('addEdge', () => {
  it('adds an edge seeded with all default modes', () => {
    const state: TeamEditState = { members: ['mia', 'jim'], edges: [] }
    const next = addEdge(state, 'mia', 'jim', WORKER_IDS)
    expect(next.edges).toHaveLength(1)
    expect(next.edges[0]).toMatchObject({ from: 'mia', to: 'jim' })
    // Default modes are non-empty so the backend never reads "all allowed".
    expect(next.edges[0].modes).toEqual(['await', 'background', 'task'])
  })
  it('is a no-op on an invalid (duplicate) connection', () => {
    const state: TeamEditState = {
      members: ['mia', 'jim'],
      edges: [{ from: 'mia', to: 'jim', modes: ['await'] }],
    }
    expect(addEdge(state, 'mia', 'jim', WORKER_IDS)).toBe(state)
  })
})

describe('removeEdge', () => {
  it('drops the matching edge', () => {
    const state: TeamEditState = {
      members: ['mia', 'jim'],
      edges: [{ from: 'mia', to: 'jim', modes: ['await'] }],
    }
    expect(removeEdge(state, 'mia', 'jim').edges).toEqual([])
  })
})

describe('toggleEdgeMode', () => {
  const base: TeamEditState = {
    members: ['mia', 'jim'],
    edges: [{ from: 'mia', to: 'jim', modes: ['await', 'task'] }],
  }

  it('turns a mode off', () => {
    const next = toggleEdgeMode(base, 'mia', 'jim', 'task')
    expect(next.edges[0].modes).toEqual(['await'])
  })
  it('turns a mode on', () => {
    const next = toggleEdgeMode(base, 'mia', 'jim', 'background')
    expect(next.edges[0].modes).toEqual(['await', 'task', 'background'])
  })
  it('refuses to remove the LAST remaining mode (no empty modes)', () => {
    const single: TeamEditState = {
      members: ['mia', 'jim'],
      edges: [{ from: 'mia', to: 'jim', modes: ['await'] }],
    }
    const next = toggleEdgeMode(single, 'mia', 'jim', 'await')
    expect(next.edges[0].modes).toEqual(['await'])
    expect(canToggleModeOff(single, 'mia', 'jim', 'await')).toBe(false)
  })
  it('only mutates the targeted edge', () => {
    const two: TeamEditState = {
      members: ['mia', 'jim', 'ray'],
      edges: [
        { from: 'mia', to: 'jim', modes: ['await', 'task'] },
        { from: 'mia', to: 'ray', modes: ['await', 'task'] },
      ],
    }
    const next = toggleEdgeMode(two, 'mia', 'jim', 'task')
    expect(next.edges[0].modes).toEqual(['await'])
    expect(next.edges[1].modes).toEqual(['await', 'task'])
  })
})

describe('setEdgeDepth', () => {
  const base: TeamEditState = {
    members: ['mia', 'jim'],
    edges: [{ from: 'mia', to: 'jim', modes: ['await'] }],
  }
  it('sets a positive cap', () => {
    expect(setEdgeDepth(base, 'mia', 'jim', 4).edges[0].depth).toBe(4)
  })
  it('clears the cap with undefined', () => {
    const set = setEdgeDepth(base, 'mia', 'jim', 4)
    expect(setEdgeDepth(set, 'mia', 'jim', undefined).edges[0].depth).toBeUndefined()
  })
})

// ── membership mutations ─────────────────────────────────────────────────────

describe('addMember / removeMember', () => {
  it('adds an agent as an isolated node', () => {
    const state: TeamEditState = { members: ['mia'], edges: [] }
    expect(addMember(state, 'jim').members).toEqual(['mia', 'jim'])
  })
  it('is a no-op for an existing member', () => {
    const state: TeamEditState = { members: ['mia'], edges: [] }
    expect(addMember(state, 'mia')).toBe(state)
  })
  it('removes the node AND every edge touching it', () => {
    const state: TeamEditState = {
      members: ['mia', 'jim', 'planner'],
      edges: [
        { from: 'mia', to: 'jim', modes: ['await'] },
        { from: 'jim', to: 'planner', modes: ['task'] },
      ],
    }
    const next = removeMember(state, 'jim')
    expect(next.members).toEqual(['mia', 'planner'])
    expect(next.edges).toEqual([]) // both edges touched jim
  })
})

// ── buildSaveEdges ───────────────────────────────────────────────────────────

describe('buildSaveEdges', () => {
  it('serialises to WorkspaceDelegationEdge[] with modes, omitting unset depth', () => {
    const state: TeamEditState = {
      members: ['mia', 'jim', 'planner'],
      edges: [
        { from: 'mia', to: 'jim', modes: ['await', 'background'], depth: undefined },
        { from: 'jim', to: 'planner', modes: ['task'], depth: 2 },
      ],
    }
    const wire = buildSaveEdges(state)
    expect(wire).toEqual([
      { from_agent: 'mia', to_agent: 'jim', modes: ['await', 'background'] },
      { from_agent: 'jim', to_agent: 'planner', modes: ['task'], depth: 2 },
    ])
    // The first edge omits depth entirely (inherit), not depth: 0.
    expect('depth' in wire[0]).toBe(false)
  })
})

// ── editsEqual ───────────────────────────────────────────────────────────────

describe('editsEqual', () => {
  it('is order-independent for members and edges + mode order', () => {
    const a: TeamEditState = {
      members: ['mia', 'jim'],
      edges: [{ from: 'mia', to: 'jim', modes: ['await', 'task'] }],
    }
    const b: TeamEditState = {
      members: ['jim', 'mia'],
      edges: [{ from: 'mia', to: 'jim', modes: ['task', 'await'] }],
    }
    expect(editsEqual(a, b)).toBe(true)
  })
  it('detects a changed depth', () => {
    const a: TeamEditState = {
      members: ['mia', 'jim'],
      edges: [{ from: 'mia', to: 'jim', modes: ['await'], depth: 1 }],
    }
    const b: TeamEditState = {
      members: ['mia', 'jim'],
      edges: [{ from: 'mia', to: 'jim', modes: ['await'], depth: 2 }],
    }
    expect(editsEqual(a, b)).toBe(false)
  })
})
