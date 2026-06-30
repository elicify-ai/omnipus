import { describe, it, expect } from 'vitest'
import type { Agent } from '@/lib/api'
import {
  addDelegationEdge,
  buildGraphModel,
  buildSavePayload,
  buildSourceEdits,
  canToggleModeOff,
  changedSourceIds,
  DEFAULT_EDGE_MODES,
  edgeId,
  normalizeModes,
  reconcileEdits,
  removeDelegationEdge,
  setSourceDepth,
  toggleSourceMode,
  validateConnection,
  type SourcePolicyEdit,
} from './graphModel'

// ── Delegation graph MODEL tests (Spec-3 FR-6.2 / US-3 / NFR-7) ──────────────
//
// These exercise the pure graph model + save payload, NOT pixel rendering
// (React Flow needs ResizeObserver/canvas — out of scope here). They assert:
//  - edges are built correctly from each agent's `to`
//  - drawing A→B produces the right delegation_policy.to PUT payload for A
//  - deleting an edge removes the ref
//  - a worker SOURCE is rejected (worker leaf rule); a worker TARGET is allowed
//  - self-edges and duplicates are rejected
//  - accept_from / budget are NEVER read into the model or the save payload

// Distinctive values that must never leak through the model or save payload.
const SECRET_ACCEPT_FROM_ID = 'should-never-appear-acceptfrom'
const SECRET_BUDGET_COST = 4242
const SECRET_BUDGET_TOKENS = 999777

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: 'agent',
    name: 'Agent',
    type: 'core',
    locked: false,
    status: 'active',
    soul: '',
    timeout_seconds: 60,
    max_tool_iterations: 20,
    steering_mode: 'one-at-a-time',
    ...overrides,
  }
}

function roster(): Agent[] {
  const jim = makeAgent({
    id: 'jim',
    name: 'Jim',
    default: true,
    delegation_policy: {
      to: [{ kind: 'local', id: 'ray' }],
      accept_from: [{ kind: 'local', id: SECRET_ACCEPT_FROM_ID }],
      modes: ['await', 'task'],
      depth: 3,
      budget: { max_cost_usd: SECRET_BUDGET_COST, max_tokens: SECRET_BUDGET_TOKENS },
    },
  })
  const ray = makeAgent({ id: 'ray', name: 'Ray', type: 'core' })
  const worker = makeAgent({ id: 'w1', name: 'Worker One', type: 'Subagent' })
  return [jim, ray, worker]
}

describe('buildSourceEdits — reads only to/modes/depth', () => {
  it('builds an edit entry per agent from `to`, `modes`, `depth`', () => {
    const edits = buildSourceEdits(roster())
    expect(edits.jim.to).toEqual([{ kind: 'local', id: 'ray' }])
    expect(edits.jim.modes).toEqual(['await', 'task'])
    expect(edits.jim.depth).toBe(3)
    // Ray has no policy → empty.
    expect(edits.ray.to).toEqual([])
    expect(edits.ray.modes).toEqual([])
    expect(edits.ray.depth).toBeUndefined()
  })

  it('NEVER reads accept_from or budget into the model', () => {
    const edits = buildSourceEdits(roster())
    const json = JSON.stringify(edits)
    expect(json).not.toContain(SECRET_ACCEPT_FROM_ID)
    expect(json).not.toContain(String(SECRET_BUDGET_COST))
    expect(json).not.toContain(String(SECRET_BUDGET_TOKENS))
    // The SourcePolicyEdit type itself has no such keys.
    expect(Object.keys(edits.jim).sort()).toEqual(['depth', 'modes', 'to'])
  })

  it('forces a worker source to an empty `to` even if the wire carries one', () => {
    const rogueWorker = makeAgent({
      id: 'wbad',
      name: 'Rogue Worker',
      type: 'Subagent',
      delegation_policy: { to: [{ kind: 'local', id: 'ray' }], modes: ['await'] },
    })
    const edits = buildSourceEdits([rogueWorker, makeAgent({ id: 'ray', name: 'Ray' })])
    expect(edits.wbad.to).toEqual([])
  })

  it('normalizeModes dedupes and rejects invalid values', () => {
    expect(normalizeModes(['await', 'await', 'task', 'bogus'])).toEqual(['await', 'task'])
    expect(normalizeModes(undefined)).toEqual([])
    expect(normalizeModes('await')).toEqual([])
  })
})

describe('buildGraphModel — edges built from `to`', () => {
  it('produces a node per agent and an edge per `to` entry', () => {
    const agents = roster()
    const edits = buildSourceEdits(agents)
    const { nodes, edges } = buildGraphModel(agents, edits)
    expect(nodes.map((n) => n.id).sort()).toEqual(['jim', 'ray', 'w1'])
    expect(edges).toHaveLength(1)
    expect(edges[0]).toMatchObject({
      id: edgeId('jim', 'ray'),
      source: 'jim',
      target: 'ray',
      modes: ['await', 'task'],
      depth: 3,
      unknownTarget: false,
    })
  })

  it('flags the default agent and the worker node', () => {
    const agents = roster()
    const { nodes } = buildGraphModel(agents, buildSourceEdits(agents))
    expect(nodes.find((n) => n.id === 'jim')?.isDefault).toBe(true)
    expect(nodes.find((n) => n.id === 'w1')?.isWorker).toBe(true)
    expect(nodes.find((n) => n.id === 'ray')?.isWorker).toBe(false)
  })

  it('marks an edge to a missing target as unknownTarget', () => {
    const orphan = makeAgent({
      id: 'orphan',
      name: 'Orphan',
      delegation_policy: { to: [{ kind: 'local', id: 'ghost' }], modes: ['await'] },
    })
    const edits = buildSourceEdits([orphan])
    const { edges } = buildGraphModel([orphan], edits)
    expect(edges[0].unknownTarget).toBe(true)
  })

  it('computes deterministic positions for every node', () => {
    const agents = roster()
    const { nodes } = buildGraphModel(agents, buildSourceEdits(agents))
    for (const n of nodes) {
      expect(Number.isFinite(n.position.x)).toBe(true)
      expect(Number.isFinite(n.position.y)).toBe(true)
    }
  })
})

describe('validateConnection — editing rules', () => {
  const agents = roster()
  const edits = buildSourceEdits(agents)
  const workerIds = new Set(['w1'])

  it('allows a fresh non-worker source → target', () => {
    expect(validateConnection('jim', 'w1', edits, workerIds)).toBeNull()
  })

  it('rejects a self-edge (A → A)', () => {
    expect(validateConnection('jim', 'jim', edits, workerIds)).toBe('self-edge')
  })

  it('rejects a duplicate edge', () => {
    // jim → ray already exists in the roster.
    expect(validateConnection('jim', 'ray', edits, workerIds)).toBe('duplicate')
  })

  it('rejects a worker as the SOURCE (worker leaf rule)', () => {
    expect(validateConnection('w1', 'ray', edits, workerIds)).toBe('worker-source')
  })

  it('ALLOWS a worker as the TARGET', () => {
    expect(validateConnection('jim', 'w1', edits, workerIds)).toBeNull()
  })
})

describe('addDelegationEdge / removeDelegationEdge — immutable mutations', () => {
  const agents = roster()
  const workerIds = new Set(['w1'])

  it('drawing A→B appends B to A.to (local ref) immutably', () => {
    const before = buildSourceEdits(agents)
    const after = addDelegationEdge(before, 'jim', 'w1', workerIds)
    expect(after).not.toBe(before)
    expect(before.jim.to).toEqual([{ kind: 'local', id: 'ray' }]) // unchanged
    expect(after.jim.to).toEqual([
      { kind: 'local', id: 'ray' },
      { kind: 'local', id: 'w1' },
    ])
  })

  it('a rejected connection (worker source) is a no-op', () => {
    const before = buildSourceEdits(agents)
    const after = addDelegationEdge(before, 'w1', 'ray', workerIds)
    expect(after).toBe(before)
  })

  it('deleting an edge removes the ref', () => {
    const before = buildSourceEdits(agents)
    const after = removeDelegationEdge(before, 'jim', 'ray')
    expect(after.jim.to).toEqual([])
    expect(before.jim.to).toHaveLength(1) // original untouched
  })

  it('toggleSourceMode flips a mode on/off', () => {
    const before = buildSourceEdits(agents) // jim: ['await','task']
    const off = toggleSourceMode(before, 'jim', 'await')
    expect(off.jim.modes).toEqual(['task'])
    const back = toggleSourceMode(off, 'jim', 'background')
    expect(back.jim.modes).toEqual(['task', 'background'])
  })

  it('setSourceDepth sets and clears the cap', () => {
    const before = buildSourceEdits(agents)
    expect(setSourceDepth(before, 'jim', 5).jim.depth).toBe(5)
    expect(setSourceDepth(before, 'jim', undefined).jim.depth).toBeUndefined()
    // Negative depth is rejected → cleared.
    expect(setSourceDepth(before, 'jim', -1).jim.depth).toBeUndefined()
  })
})

describe('buildSavePayload — only to/modes/depth, never accept_from/budget', () => {
  it('produces the delegation_policy.to PUT payload for the source', () => {
    const agents = roster()
    const edits = addDelegationEdge(buildSourceEdits(agents), 'jim', 'w1', new Set(['w1']))
    const payload = buildSavePayload(edits.jim)
    expect(payload).toEqual({
      to: [
        { kind: 'local', id: 'ray' },
        { kind: 'local', id: 'w1' },
      ],
      modes: ['await', 'task'],
      depth: 3,
    })
  })

  it('sends depth: 0 (uncapped sentinel) when unset so clearing the cap persists', () => {
    // The backend reads depth<=0 as "uncapped" (ResolveDelegationDepth). Omitting
    // depth would make a partial PUT PRESERVE the old cap → the UI snaps back
    // after refetch. Sending 0 makes "no cap" honest and round-trip-stable.
    const edit: SourcePolicyEdit = { to: [{ kind: 'local', id: 'ray' }], modes: ['await'] }
    const payload = buildSavePayload(edit)
    expect(payload.depth).toBe(0)
  })

  it('the save payload never carries accept_from or budget keys', () => {
    const agents = roster()
    const payload = buildSavePayload(buildSourceEdits(agents).jim)
    const json = JSON.stringify(payload)
    expect(json).not.toContain('accept_from')
    expect(json).not.toContain('budget')
    expect(json).not.toContain(SECRET_ACCEPT_FROM_ID)
    expect(json).not.toContain(String(SECRET_BUDGET_COST))
    expect(Object.keys(payload).sort()).toEqual(['depth', 'modes', 'to'])
  })
})

describe('changedSourceIds — diff drives per-agent save', () => {
  const agents = roster()

  it('reports only the source agents whose policy changed', () => {
    const before = buildSourceEdits(agents)
    const after = addDelegationEdge(before, 'jim', 'w1', new Set(['w1']))
    expect(changedSourceIds(before, after)).toEqual(['jim'])
  })

  it('reports nothing when nothing changed', () => {
    const before = buildSourceEdits(agents)
    expect(changedSourceIds(before, before)).toEqual([])
  })

  it('detects mode-only and depth-only changes', () => {
    const before = buildSourceEdits(agents)
    expect(changedSourceIds(before, toggleSourceMode(before, 'jim', 'await'))).toEqual(['jim'])
    expect(changedSourceIds(before, setSourceDepth(before, 'jim', 9))).toEqual(['jim'])
  })
})

// ── Fix #1: an active edge must never carry `modes: []` ─────────────────────
// (backend reads empty modes as "ALL allowed" — the opposite of "no modes").
describe('empty-modes trap — a new edge defaults to a non-empty mode set', () => {
  it('DEFAULT_EDGE_MODES is non-empty', () => {
    expect(DEFAULT_EDGE_MODES.length).toBeGreaterThan(0)
  })

  it('drawing the FIRST edge from a source with no modes seeds default modes', () => {
    // Ray starts with modes: [] (no policy). Drawing ray→worker must NOT leave
    // modes empty (which would persist "all allowed").
    const agents = roster()
    const before = buildSourceEdits(agents)
    expect(before.ray.modes).toEqual([])
    const after = addDelegationEdge(before, 'ray', 'w1', new Set(['w1']))
    expect(after.ray.to).toEqual([{ kind: 'local', id: 'w1' }])
    expect(after.ray.modes).toEqual([...DEFAULT_EDGE_MODES])
    expect(after.ray.modes.length).toBeGreaterThan(0)
  })

  it('drawing an edge from a source that ALREADY has modes leaves them untouched', () => {
    const agents = roster()
    const before = buildSourceEdits(agents) // jim: ['await','task']
    const after = addDelegationEdge(before, 'jim', 'w1', new Set(['w1']))
    expect(after.jim.modes).toEqual(['await', 'task'])
  })

  it('the save payload of a fresh edge never has empty modes', () => {
    const agents = roster()
    const after = addDelegationEdge(buildSourceEdits(agents), 'ray', 'w1', new Set(['w1']))
    const payload = buildSavePayload(after.ray)
    expect(payload.modes.length).toBeGreaterThan(0)
  })
})

describe('toggleSourceMode — the last remaining mode cannot be removed', () => {
  const agents = roster()

  it('removing the last mode is a NO-OP (never reaches modes: [])', () => {
    const before = buildSourceEdits(agents)
    // Drive jim down to a single mode, then try to remove it.
    const oneMode = toggleSourceMode(before, 'jim', 'await') // ['await','task'] → ['task']
    expect(oneMode.jim.modes).toEqual(['task'])
    const stillOne = toggleSourceMode(oneMode, 'jim', 'task') // attempt to clear last
    expect(stillOne.jim.modes).toEqual(['task']) // unchanged
    expect(stillOne).toBe(oneMode) // identity preserved — true no-op
  })

  it('canToggleModeOff is false for the only-remaining mode, true otherwise', () => {
    const before = buildSourceEdits(agents)
    const oneMode = toggleSourceMode(before, 'jim', 'await') // ['task']
    expect(canToggleModeOff(oneMode, 'jim', 'task')).toBe(false)
    // Turning a currently-off mode on is always allowed.
    expect(canToggleModeOff(oneMode, 'jim', 'await')).toBe(true)
    // With two modes, either can be turned off.
    expect(canToggleModeOff(before, 'jim', 'await')).toBe(true)
    expect(canToggleModeOff(before, 'jim', 'task')).toBe(true)
  })
})

// ── Fix #2: a dangling-target edge must render a ghost node ──────────────────
describe('ghost nodes — a dangling edge target gets a visible placeholder node', () => {
  it('synthesises a ghost node for an edge whose target is not in the roster', () => {
    const orphan = makeAgent({
      id: 'orphan',
      name: 'Orphan',
      delegation_policy: { to: [{ kind: 'local', id: 'ghost-id' }], modes: ['await'] },
    })
    const agents = [orphan]
    const { nodes, edges } = buildGraphModel(agents, buildSourceEdits(agents))
    // The edge survives AND is flagged.
    expect(edges).toHaveLength(1)
    expect(edges[0].unknownTarget).toBe(true)
    // A ghost node exists for the missing target so React Flow won't drop it.
    const ghost = nodes.find((n) => n.id === 'ghost-id')
    expect(ghost).toBeDefined()
    expect(ghost?.isGhost).toBe(true)
    expect(ghost?.name).toContain('deleted')
    // Real nodes are NOT ghosts.
    expect(nodes.find((n) => n.id === 'orphan')?.isGhost).toBe(false)
  })

  it('the ghost-target edge is deletable (removeDelegationEdge drops the ref)', () => {
    const orphan = makeAgent({
      id: 'orphan',
      name: 'Orphan',
      delegation_policy: { to: [{ kind: 'local', id: 'ghost-id' }], modes: ['await'] },
    })
    const before = buildSourceEdits([orphan])
    const after = removeDelegationEdge(before, 'orphan', 'ghost-id')
    expect(after.orphan.to).toEqual([])
    const { nodes, edges } = buildGraphModel([orphan], after)
    expect(edges).toHaveLength(0)
    // With the edge gone, the ghost node is no longer synthesised.
    expect(nodes.find((n) => n.id === 'ghost-id')).toBeUndefined()
  })

  it('deduplicates ghost nodes when several edges point at the same missing id', () => {
    const a = makeAgent({
      id: 'a',
      name: 'A',
      delegation_policy: { to: [{ kind: 'local', id: 'gone' }], modes: ['await'] },
    })
    const b = makeAgent({
      id: 'b',
      name: 'B',
      delegation_policy: { to: [{ kind: 'local', id: 'gone' }], modes: ['await'] },
    })
    const agents = [a, b]
    const { nodes } = buildGraphModel(agents, buildSourceEdits(agents))
    expect(nodes.filter((n) => n.id === 'gone')).toHaveLength(1)
  })
})

// ── Fix #4: depth — uncapped sentinel + round-trip stability ─────────────────
describe('depth — 0/absent = uncapped, stays cleared across a round-trip', () => {
  it('treats a wire depth of 0 as uncapped (undefined in the model)', () => {
    const a = makeAgent({
      id: 'a',
      name: 'A',
      delegation_policy: { to: [], modes: ['await'], depth: 0 },
    })
    expect(buildSourceEdits([a]).a.depth).toBeUndefined()
  })

  it('a positive depth survives, a cleared depth saves as 0 and reloads as uncapped', () => {
    const edit: SourcePolicyEdit = { to: [{ kind: 'local', id: 'x' }], modes: ['await'], depth: 4 }
    expect(buildSavePayload(edit).depth).toBe(4)
    // Clear it → undefined → save as 0 → reload (buildSourceEdits) → undefined.
    const cleared: SourcePolicyEdit = { ...edit, depth: undefined }
    const saved = buildSavePayload(cleared)
    expect(saved.depth).toBe(0)
    const reloaded = makeAgent({
      id: 'a',
      name: 'A',
      delegation_policy: { to: saved.to, modes: saved.modes, depth: saved.depth },
    })
    expect(buildSourceEdits([reloaded]).a.depth).toBeUndefined()
  })
})

// ── Fix #3 (model half): reconcileEdits keeps dirty edits, adopts clean ones ──
describe('reconcileEdits — preserve still-dirty edits, adopt clean server values', () => {
  it('adopts the new server value for an agent the user had not touched', () => {
    const prevBaseline = { a: mkEdit(['await']) }
    const prevEdits = { a: mkEdit(['await']) } // clean
    const newBaseline = { a: mkEdit(['task']) } // server changed under us
    const out = reconcileEdits(newBaseline, prevBaseline, prevEdits)
    expect(out.a.modes).toEqual(['task'])
  })

  it('keeps the user edit for an agent that was still dirty (e.g. a failed save)', () => {
    const prevBaseline = { a: mkEdit(['await']) }
    const prevEdits = { a: mkEdit(['task', 'background']) } // user changed it
    const newBaseline = { a: mkEdit(['await']) } // save FAILED → server unchanged
    const out = reconcileEdits(newBaseline, prevBaseline, prevEdits)
    expect(out.a.modes).toEqual(['task', 'background']) // pending edit preserved
  })

  it('mixed: succeeded agent adopts server, failed agent stays dirty', () => {
    const prevBaseline = { jim: mkEdit(['await']), ray: mkEdit(['await']) }
    const prevEdits = { jim: mkEdit(['task']), ray: mkEdit(['task']) } // both dirty
    // jim saved (server now task), ray failed (server still await).
    const newBaseline = { jim: mkEdit(['task']), ray: mkEdit(['await']) }
    const out = reconcileEdits(newBaseline, prevBaseline, prevEdits)
    // jim now matches baseline (clean), ray keeps its pending edit (dirty).
    expect(changedSourceIds(newBaseline, out)).toEqual(['ray'])
    expect(out.jim.modes).toEqual(['task'])
    expect(out.ray.modes).toEqual(['task'])
  })
})

function mkEdit(modes: ('await' | 'background' | 'task')[]): SourcePolicyEdit {
  return { to: [], modes }
}
