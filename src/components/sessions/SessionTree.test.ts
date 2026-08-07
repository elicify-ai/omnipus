// SessionTree.tsx post-review fix — Defect 3:
//
//   "SessionTree renders one page of children while the badge reports the
//   true count. child_count says 120, only 50 (the server's default page
//   size) ever render, and there was no 'load more' affordance. Separately,
//   `hasChildren` is gated on child_count while rendering is gated on
//   children.length — so a session whose child_count is stale/wrong (> 0
//   but nothing actually resolves) renders an expanded toggle with nothing
//   beneath it and no explanation."
//
// These tests exercise the REAL useSessionForest hook (against a mocked
// fetchSessionPage) and the REAL flattenSessionTree — proving:
//   1. hasMoreChildren()/loadMoreChildren() page a wide fan-out to
//      completion instead of silently stopping at page 1.
//   2. flattenSessionTree's new `childrenEmpty` flag distinguishes "loaded,
//      genuinely zero children" from "not fetched yet" / "still loading".
//
// New file, per this repo's per-unit test-file convention.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import React from 'react'
import { buildSessionTree, attachSessionChildren, type Session } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchSessionPage: vi.fn() }
})

import { fetchSessionPage } from '@/lib/api'
import { useSessionForest, flattenSessionTree } from './SessionTree'

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

function makeWrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return React.createElement(QueryClientProvider, { client }, children)
  }
}
function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function makeChildren(count: number, parentId: string, offset = 0): Session[] {
  return Array.from({ length: count }, (_, i) =>
    makeSession({ id: `${parentId}-child-${offset + i}`, parent_session_id: parentId }),
  )
}

beforeEach(() => {
  vi.mocked(fetchSessionPage).mockReset()
})
afterEach(() => {
  vi.restoreAllMocks()
})

describe('useSessionForest — Defect 3: per-node "load more" paging', () => {
  it('a wide fan-out (120 children, 50/page) exposes hasMoreChildren=true after page 1, and loadMoreChildren fetches the rest', async () => {
    const root = makeSession({ id: 'root-1', child_count: 120 })

    // Page 1: 50 children + next_cursor.
    vi.mocked(fetchSessionPage).mockResolvedValueOnce({
      sessions: makeChildren(50, 'root-1', 0),
      nextCursor: '50',
    })

    const { result } = renderHook(() => useSessionForest([root]), { wrapper: makeWrapper(makeClient()) })

    act(() => { result.current.toggleExpand('root-1') })

    await waitFor(() => expect(result.current.tree[0].children).toHaveLength(50))

    // Positive lower bound: prove the badge/actual mismatch really exists
    // before asserting the fix's affordance reacts to it.
    expect(root.child_count).toBe(120)
    expect(result.current.tree[0].children).toHaveLength(50)
    expect(result.current.hasMoreChildren('root-1')).toBe(true)

    // Page 2: the remaining 70, no further cursor.
    vi.mocked(fetchSessionPage).mockResolvedValueOnce({
      sessions: makeChildren(70, 'root-1', 50),
    })

    act(() => { result.current.loadMoreChildren('root-1') })

    await waitFor(() => expect(result.current.tree[0].children).toHaveLength(120))

    // All 120 are present (page 1's + page 2's — appended, not replaced).
    const ids = result.current.tree[0].children.map((c) => c.session.id)
    expect(ids).toContain('root-1-child-0')
    expect(ids).toContain('root-1-child-119')
    expect(new Set(ids).size).toBe(120)

    // No more pages left.
    expect(result.current.hasMoreChildren('root-1')).toBe(false)

    // The second fetch requested the offset from the first page's cursor.
    expect(fetchSessionPage).toHaveBeenCalledTimes(2)
    expect(fetchSessionPage).toHaveBeenNthCalledWith(2, undefined, undefined, { parentSessionId: 'root-1', offset: 50 })
  })

  it('loadMoreChildren is a no-op when there is no further page (hasMoreChildren is false)', async () => {
    const root = makeSession({ id: 'root-2', child_count: 2 })
    vi.mocked(fetchSessionPage).mockResolvedValueOnce({ sessions: makeChildren(2, 'root-2', 0) })

    const { result } = renderHook(() => useSessionForest([root]), { wrapper: makeWrapper(makeClient()) })
    act(() => { result.current.toggleExpand('root-2') })
    await waitFor(() => expect(result.current.tree[0].children).toHaveLength(2))

    expect(result.current.hasMoreChildren('root-2')).toBe(false)

    act(() => { result.current.loadMoreChildren('root-2') })

    // No additional fetch was issued — still exactly 1 call.
    expect(fetchSessionPage).toHaveBeenCalledTimes(1)
    expect(result.current.tree[0].children).toHaveLength(2)
  })

  it('collapsing and re-expanding drops accumulated pages — re-expand starts fresh at page 1', async () => {
    const root = makeSession({ id: 'root-3', child_count: 100 })
    vi.mocked(fetchSessionPage).mockResolvedValueOnce({
      sessions: makeChildren(50, 'root-3', 0),
      nextCursor: '50',
    })

    const { result } = renderHook(() => useSessionForest([root]), { wrapper: makeWrapper(makeClient()) })
    act(() => { result.current.toggleExpand('root-3') })
    await waitFor(() => expect(result.current.tree[0].children).toHaveLength(50))

    vi.mocked(fetchSessionPage).mockResolvedValueOnce({ sessions: makeChildren(50, 'root-3', 50) })
    act(() => { result.current.loadMoreChildren('root-3') })
    await waitFor(() => expect(result.current.tree[0].children).toHaveLength(100))

    // Collapse.
    act(() => { result.current.toggleExpand('root-3') })
    expect(result.current.expandedIds.has('root-3')).toBe(false)

    // Re-expand. Note: react-query's own cache (staleTime 15s) may serve
    // page 1's data (queryKey ['sessions','children','root-3',0]) without a
    // fresh network call — that's correct caching, not the property under
    // test here. What matters is that the SECOND page (offset 50) is no
    // longer part of the requested set: re-expanding must not silently
    // resurrect the previously-accumulated 100-item state.
    act(() => { result.current.toggleExpand('root-3') })

    await waitFor(() => expect(result.current.tree[0].children).toHaveLength(50))
    // Deliberately NOT 100 — page 2 was dropped by the collapse, exactly
    // the documented retry/reset affordance.
    expect(result.current.tree[0].children).not.toHaveLength(100)
  })
})

describe('flattenSessionTree — Defect 3: childrenEmpty distinguishes "loaded, zero children" from "not fetched"', () => {
  it('childrenEmpty is true when expanded + childrenLoaded + zero actual children (stale child_count)', () => {
    let tree = buildSessionTree([makeSession({ id: 'stale-parent', child_count: 5 })])
    // Simulate a resolved fetch that came back empty despite child_count=5 —
    // exactly the "backend bug" scenario this fix must not choke on.
    tree = attachSessionChildren(tree, 'stale-parent', [])

    const rows = flattenSessionTree(tree, new Set(['stale-parent']))
    const parentRow = rows.find((r) => r.node.session.id === 'stale-parent')!

    expect(parentRow.hasChildren).toBe(true)
    expect(parentRow.isExpanded).toBe(true)
    expect(parentRow.node.childrenLoaded).toBe(true)
    expect(parentRow.node.children).toHaveLength(0)
    expect(parentRow.childrenEmpty).toBe(true)
  })

  it('childrenEmpty is false for the same node BEFORE its children have been fetched (not yet loaded, not "empty")', () => {
    const tree = buildSessionTree([makeSession({ id: 'unfetched-parent', child_count: 5 })])
    const rows = flattenSessionTree(tree, new Set(['unfetched-parent']))
    const parentRow = rows.find((r) => r.node.session.id === 'unfetched-parent')!

    expect(parentRow.isExpanded).toBe(true)
    expect(parentRow.node.childrenLoaded).toBe(false)
    expect(parentRow.childrenEmpty).toBe(false)
  })

  it('childrenEmpty is false once the fetch resolves with real children (positive control)', () => {
    let tree = buildSessionTree([makeSession({ id: 'ok-parent', child_count: 2 })])
    tree = attachSessionChildren(tree, 'ok-parent', [
      makeSession({ id: 'ok-child-1', parent_session_id: 'ok-parent' }),
      makeSession({ id: 'ok-child-2', parent_session_id: 'ok-parent' }),
    ])

    const rows = flattenSessionTree(tree, new Set(['ok-parent']))
    const parentRow = rows.find((r) => r.node.session.id === 'ok-parent')!

    expect(parentRow.node.children).toHaveLength(2)
    expect(parentRow.childrenEmpty).toBe(false)
  })

  it('childrenEmpty is false for a collapsed node regardless of load state (only relevant while expanded)', () => {
    let tree = buildSessionTree([makeSession({ id: 'collapsed-parent', child_count: 5 })])
    tree = attachSessionChildren(tree, 'collapsed-parent', [])

    // Not in the expanded set.
    const rows = flattenSessionTree(tree, new Set())
    const parentRow = rows.find((r) => r.node.session.id === 'collapsed-parent')!

    expect(parentRow.isExpanded).toBe(false)
    expect(parentRow.childrenEmpty).toBe(false)
  })
})
