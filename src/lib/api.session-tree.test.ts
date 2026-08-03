// ADR-057 U12 (W16d, US-19, FR-091/FR-097) — pure, exported session-tree
// assembly primitives. GET /sessions never returns a whole forest (it pages
// over roots, or one node's direct children via parent_session_id, or a
// flat list under flat=true — US-19's design note: "the client assembles
// the tree; no layer ever has to hold the whole forest"). These are the
// seams U24 (sidebar tree, search tree — cross-unit request, spec line
// ~1033) builds that incremental-expand UI on top of.
//
// New file per ownership Rule 5 (every unit's new tests go in new files).
// No network mocking needed — these are pure functions over the Session
// shape, independent of fetchSessions()'s own query-building/response-
// parsing (covered separately in __adr052__sessionVisibilityParams.test.ts).

import { describe, it, expect } from 'vitest'
import {
  buildSessionTree,
  attachSessionChildren,
  insertOrphanSessionAsRoot,
  findSessionNode,
  type Session,
} from '@/lib/api'

function makeSession(overrides: Partial<Session> & { id: string }): Session {
  return {
    agent_id: 'jim',
    title: 'Untitled',
    type: 'chat',
    created_at: '2026-08-01T00:00:00Z',
    updated_at: '2026-08-01T00:00:00Z',
    message_count: 0,
    ...overrides,
  }
}

describe('buildSessionTree', () => {
  it('wraps a flat page of sessions as top-level nodes with empty children', () => {
    const roots = [makeSession({ id: 'root-1' }), makeSession({ id: 'root-2' })]
    const tree = buildSessionTree(roots)

    expect(tree).toHaveLength(2)
    expect(tree[0].session.id).toBe('root-1')
    expect(tree[0].children).toEqual([])
    expect(tree[1].session.id).toBe('root-2')
  })

  it('a session with child_count 0 starts childrenLoaded:true (nothing to fetch)', () => {
    const [node] = buildSessionTree([makeSession({ id: 'leaf', child_count: 0 })])
    expect(node.childrenLoaded).toBe(true)
  })

  it('a session with child_count > 0 starts childrenLoaded:false (must be expanded)', () => {
    const [node] = buildSessionTree([makeSession({ id: 'parent', child_count: 24 })])
    expect(node.childrenLoaded).toBe(false)
  })

  it('a session with no child_count (e.g. from a detail fetch) defaults to childrenLoaded:true', () => {
    const [node] = buildSessionTree([makeSession({ id: 'no-count' })])
    expect(node.childrenLoaded).toBe(true)
  })
})

describe('findSessionNode', () => {
  it('finds a top-level node', () => {
    const tree = buildSessionTree([makeSession({ id: 'root-1' })])
    expect(findSessionNode(tree, 'root-1')?.session.id).toBe('root-1')
  })

  it('finds a node nested at depth 3 (US-19 AS-3)', () => {
    let tree = buildSessionTree([makeSession({ id: 'root', child_count: 1 })])
    tree = attachSessionChildren(tree, 'root', [makeSession({ id: 'depth-1', parent_session_id: 'root', child_count: 1 })])
    tree = attachSessionChildren(tree, 'depth-1', [makeSession({ id: 'depth-2', parent_session_id: 'depth-1', child_count: 1 })])
    tree = attachSessionChildren(tree, 'depth-2', [makeSession({ id: 'depth-3', parent_session_id: 'depth-2' })])

    const found = findSessionNode(tree, 'depth-3')
    expect(found?.session.id).toBe('depth-3')
  })

  it('returns undefined for an id not present anywhere in the tree', () => {
    const tree = buildSessionTree([makeSession({ id: 'root-1' })])
    expect(findSessionNode(tree, 'nope')).toBeUndefined()
  })
})

describe('attachSessionChildren', () => {
  it('attaches children under the matching root and marks it loaded', () => {
    const tree = buildSessionTree([makeSession({ id: 'parent', child_count: 2 })])
    const children = [makeSession({ id: 'child-a', parent_session_id: 'parent' }), makeSession({ id: 'child-b', parent_session_id: 'parent' })]

    const updated = attachSessionChildren(tree, 'parent', children)

    expect(updated[0].childrenLoaded).toBe(true)
    expect(updated[0].children.map((c) => c.session.id)).toEqual(['child-a', 'child-b'])
  })

  it('attaches children at any depth, only touching that one node (BDD-103: expanding one level does not disturb siblings)', () => {
    let tree = buildSessionTree([
      makeSession({ id: 'root-a', child_count: 1 }),
      makeSession({ id: 'root-b', child_count: 1 }),
    ])
    tree = attachSessionChildren(tree, 'root-a', [makeSession({ id: 'root-a-child', parent_session_id: 'root-a' })])

    // root-b is untouched — still unexpanded, same node reference preserved
    // (a real property: React/Zustand consumers can skip re-rendering an
    // unaffected subtree because its object identity is unchanged).
    const rootBBefore = tree[1]
    tree = attachSessionChildren(tree, 'root-a', [
      makeSession({ id: 'root-a-child', parent_session_id: 'root-a' }),
      makeSession({ id: 'root-a-child-2', parent_session_id: 'root-a' }),
    ])
    expect(tree[1]).toBe(rootBBefore)
    expect(tree[0].children.map((c) => c.session.id)).toEqual(['root-a-child', 'root-a-child-2'])
  })

  it('is a no-op (same node references, new array) when parentId is not present anywhere in the tree', () => {
    const tree = buildSessionTree([makeSession({ id: 'root-1' })])
    const originalNode = tree[0]

    const updated = attachSessionChildren(tree, 'not-in-tree', [makeSession({ id: 'orphan-child' })])

    expect(updated).not.toBe(tree)
    expect(updated[0]).toBe(originalNode)
    expect(updated[0].children).toEqual([])
  })

  it('finds and updates a node nested several levels deep without flattening the tree', () => {
    let tree = buildSessionTree([makeSession({ id: 'root', child_count: 1 })])
    tree = attachSessionChildren(tree, 'root', [makeSession({ id: 'mid', parent_session_id: 'root', child_count: 1 })])

    tree = attachSessionChildren(tree, 'mid', [makeSession({ id: 'leaf', parent_session_id: 'mid' })])

    expect(tree).toHaveLength(1)
    expect(tree[0].children).toHaveLength(1)
    expect(tree[0].children[0].children).toHaveLength(1)
    expect(tree[0].children[0].children[0].session.id).toBe('leaf')
  })
})

describe('insertOrphanSessionAsRoot (BDD-106)', () => {
  it('appends a session not present anywhere in the tree as a new root', () => {
    const tree = buildSessionTree([makeSession({ id: 'root-1' })])
    const orphan = makeSession({ id: 'orphan-1', parent_session_id: 'deleted-parent' })

    const updated = insertOrphanSessionAsRoot(tree, orphan)

    expect(updated).toHaveLength(2)
    expect(updated[1].session.id).toBe('orphan-1')
    expect(updated[1].session.parent_session_id).toBe('deleted-parent')
  })

  it('is a no-op returning the SAME tree reference when the session already exists anywhere in the tree', () => {
    let tree = buildSessionTree([makeSession({ id: 'root', child_count: 1 })])
    tree = attachSessionChildren(tree, 'root', [makeSession({ id: 'child', parent_session_id: 'root' })])

    const updated = insertOrphanSessionAsRoot(tree, makeSession({ id: 'child', parent_session_id: 'root' }))

    expect(updated).toBe(tree)
    expect(updated).toHaveLength(1)
  })
})
