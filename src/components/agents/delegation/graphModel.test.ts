import { describe, it, expect } from 'vitest'
import type { Agent } from '@/lib/api'
import {
  addDelegationEdge,
  buildGraphModel,
  buildSavePayload,
  buildSourceEdits,
  changedSourceIds,
  edgeId,
  normalizeModes,
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
    heartbeat: '',
    instructions: '',
    timeout_seconds: 60,
    max_tool_iterations: 20,
    steering_mode: 'loop',
    tool_feedback: true,
    heartbeat_enabled: false,
    heartbeat_interval: 300,
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
  const worker = makeAgent({ id: 'w1', name: 'Worker One', type: 'worker' })
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
      type: 'worker',
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

  it('omits depth entirely when unset (no zeroing on partial PUT)', () => {
    const edit: SourcePolicyEdit = { to: [{ kind: 'local', id: 'ray' }], modes: ['await'] }
    const payload = buildSavePayload(edit)
    expect('depth' in payload).toBe(false)
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
