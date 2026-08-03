// SessionTree — shared session-hierarchy primitives (ADR-057 US-19).
//
// ADR-057 §9's R-9 open question (hide subordinate sessions behind
// `?include_subordinate=true`, the `verifier` precedent, or nest them under
// their parent) was resolved by the OPERATOR as NESTED UNDER PARENT — an
// explicit override of the reviewer's cheaper flag-based recommendation,
// because drill-down into a delegated child session is the point of this
// feature, not something to hide by default. FR-091..FR-094 + FR-097 make
// this real UI/API work rather than a filter toggle.
//
// This file owns the two pieces genuinely shared between the two consumers:
//   - `flattenSessionTree` — turns a `SessionTreeNode[]` forest (the api.ts
//     shape from U12: `{ session, children, childrenLoaded }`) plus a set of
//     "currently expanded" session ids into an ordered, DEPTH-FIRST list of
//     visible rows. A collapsed node's children are dropped from the output
//     entirely (not merely CSS-hidden), so callers — including a
//     virtualizer — never have to hold a fan-out they aren't showing.
//   - `useSessionForest` — Sidebar's (W16f) lazy "expand a node, fetch its
//     direct children a page at a time" loop (FR-092/FR-097, BDD-103: one
//     request per expanded node, never the whole subtree).
//   - `SessionTree` — a generic forest renderer with an opt-in virtualized
//     mode (`@tanstack/react-virtual`, following the same
//     ResizeObserver-feature-detect / plain-fallback split already
//     established by `ChatScreen.tsx`'s `VirtualizedMessageList`, so
//     environments without `ResizeObserver` — and every existing test file
//     that doesn't explicitly stub one — get the fully-rendered plain list
//     with zero behavior change).
//   - `SessionExpandToggle` — the chevron control both surfaces use.
//
// SearchModal (W16g) does NOT use `useSessionForest` — search needs every
// session (roots AND descendants) queryable up front to find a match at any
// depth, so it fetches once with `flat: true` (FR-104) and nests the
// already-resident flat list into its own `SessionTreeNode[]` before handing
// it to `flattenSessionTree` directly. Both consumers share the flattening
// algorithm and the expand-toggle control; each owns its own data-fetching
// strategy because the two use cases are genuinely different (lazy paging
// vs. up-front search).

import { useCallback, useMemo, useState, type ReactNode, type RefObject } from 'react'
import { useQueries } from '@tanstack/react-query'
import { useVirtualizer } from '@tanstack/react-virtual'
import { CaretRight, CaretDown, CircleNotch } from '@phosphor-icons/react'
import { buildSessionTree, attachSessionChildren, fetchSessionPage, type Session, type SessionTreeNode } from '@/lib/api'

// ── Flattening ───────────────────────────────────────────────────────────────

export interface SessionTreeFlatRow {
  node: SessionTreeNode
  depth: number
  /** True iff the server reported at least one direct child (FR-091/FR-097 `child_count`). */
  hasChildren: boolean
  /** True iff this node is both expandable AND currently in the expanded set. */
  isExpanded: boolean
}

/**
 * Flattens a forest into the rows that are actually visible given the
 * current expand state. A node with `hasChildren` but not expanded
 * contributes exactly one row (itself) — its children, fetched or not, are
 * never walked. Depth-first, root order preserved, unbounded depth (a
 * multi-hop delegation chain nests correctly — BDD-103's depth-3 scenario).
 */
export function flattenSessionTree(
  nodes: SessionTreeNode[],
  expandedIds: ReadonlySet<string>,
  depth = 0,
): SessionTreeFlatRow[] {
  const rows: SessionTreeFlatRow[] = []
  for (const node of nodes) {
    const hasChildren = (node.session.child_count ?? 0) > 0
    const isExpanded = hasChildren && expandedIds.has(node.session.id)
    rows.push({ node, depth, hasChildren, isExpanded })
    if (isExpanded && node.children.length > 0) {
      rows.push(...flattenSessionTree(node.children, expandedIds, depth + 1))
    }
  }
  return rows
}

// ── Lazy per-node expansion (Sidebar's W16f use case) ─────────────────────────

export interface SessionForest {
  tree: SessionTreeNode[]
  expandedIds: ReadonlySet<string>
  toggleExpand: (sessionId: string) => void
  isLoadingChildren: (sessionId: string) => boolean
  isErrorChildren: (sessionId: string) => boolean
}

/**
 * Owns the "click a chevron, fetch that node's direct children a page at a
 * time" half of US-19 (FR-092/FR-097's per-expand paging: BDD-103's O
 * (expanded nodes) request count, never O(all sessions)). Each currently
 * expanded root gets its own `fetchSessionPage(undefined, undefined,
 * { parentSessionId })` query via `useQueries` — the hook itself always runs
 * (fixed call site), only the *array passed to it* grows/shrinks with the
 * expand set, which is the supported way to run a dynamic number of queries.
 *
 * Collapsing and re-expanding a node whose fetch previously errored is the
 * retry affordance: the query is dropped from `useQueries`' input entirely
 * on collapse and re-added fresh on re-expand.
 */
export function useSessionForest(roots: Session[]): SessionForest {
  const rootTree = useMemo(() => buildSessionTree(roots), [roots])
  const [expandedIds, setExpandedIds] = useState<Set<string>>(new Set())
  const expandedList = useMemo(() => Array.from(expandedIds), [expandedIds])

  const toggleExpand = useCallback((sessionId: string) => {
    setExpandedIds((prev) => {
      const next = new Set(prev)
      if (next.has(sessionId)) next.delete(sessionId)
      else next.add(sessionId)
      return next
    })
  }, [])

  const childQueries = useQueries({
    queries: expandedList.map((parentId) => ({
      queryKey: ['sessions', 'children', parentId] as const,
      queryFn: () => fetchSessionPage(undefined, undefined, { parentSessionId: parentId }),
      staleTime: 15_000,
    })),
  })

  // Deliberately NOT wrapped in useMemo: `useQueries` returns a fresh result
  // array on every render (its entries are not referentially stable even
  // when the underlying data hasn't changed), so a dependency array built
  // from it would either recompute every render anyway or under-fire on a
  // same-shape refetch. The inputs here are small (a session's own root
  // list plus whichever nodes are currently expanded) and
  // `buildSessionTree`/`attachSessionChildren` are cheap, pure, immutable
  // merges — recomputing plainly is simpler and strictly correct.
  let tree = rootTree
  expandedList.forEach((parentId, i) => {
    const data = childQueries[i]?.data
    if (data) tree = attachSessionChildren(tree, parentId, data.sessions)
  })

  const isLoadingChildren = useCallback(
    (sessionId: string) => {
      const i = expandedList.indexOf(sessionId)
      return i >= 0 && childQueries[i]?.isLoading === true
    },
    [expandedList, childQueries],
  )
  const isErrorChildren = useCallback(
    (sessionId: string) => {
      const i = expandedList.indexOf(sessionId)
      return i >= 0 && childQueries[i]?.isError === true
    },
    [expandedList, childQueries],
  )

  return { tree, expandedIds, toggleExpand, isLoadingChildren, isErrorChildren }
}

// ── Expand/collapse control (shared visual) ───────────────────────────────────

export function SessionExpandToggle({
  expanded,
  onToggle,
  loading,
  error,
  expandLabel,
  collapseLabel,
}: {
  expanded: boolean
  onToggle: () => void
  loading?: boolean
  error?: boolean
  expandLabel: string
  collapseLabel: string
}) {
  return (
    <button
      tabIndex={0}
      type="button"
      onClick={(e) => {
        e.stopPropagation()
        onToggle()
      }}
      aria-expanded={expanded}
      aria-label={expanded ? collapseLabel : expandLabel}
      title={error ? 'Could not load — click to retry' : expanded ? collapseLabel : expandLabel}
      className="flex shrink-0 items-center justify-center rounded p-1 -m-1 text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] transition-colors"
    >
      {loading ? (
        <CircleNotch size={11} className="animate-spin" />
      ) : expanded ? (
        <CaretDown size={11} className={error ? 'text-[var(--color-error)]' : undefined} />
      ) : (
        <CaretRight size={11} className={error ? 'text-[var(--color-error)]' : undefined} />
      )}
    </button>
  )
}

// ── Generic forest renderer (optionally virtualized, FR-094) ─────────────────

export interface SessionTreeVirtualizeOptions {
  scrollElementRef: RefObject<HTMLElement | null>
  estimateRowHeight: number
  overscan?: number
}

export interface SessionTreeProps {
  nodes: SessionTreeNode[]
  expandedIds: ReadonlySet<string>
  renderRow: (row: SessionTreeFlatRow) => ReactNode
  getRowKey?: (row: SessionTreeFlatRow) => string
  emptyState?: ReactNode
  /**
   * FR-094: SearchModal sets this so the mounted DOM node count is bounded
   * by the viewport, not by the result count. Sidebar's budget (≤ 9 roots +
   * on-demand children) doesn't need it and omits the prop — it gets the
   * plain (fully rendered) path unconditionally.
   *
   * Feature-detects `ResizeObserver` the same way `ChatScreen.tsx`'s
   * `VirtualizedMessageList` does: environments without it (jsdom by
   * default, and every existing test file here that doesn't explicitly
   * stub one) get the plain fully-rendered list rather than a silent
   * zero-row crash.
   */
  virtualize?: SessionTreeVirtualizeOptions
}

export function SessionTree({ nodes, expandedIds, renderRow, getRowKey, emptyState, virtualize }: SessionTreeProps) {
  const rows = useMemo(() => flattenSessionTree(nodes, expandedIds), [nodes, expandedIds])
  const keyOf = getRowKey ?? ((row: SessionTreeFlatRow) => row.node.session.id)

  if (rows.length === 0) return emptyState ? <>{emptyState}</> : null

  if (virtualize && typeof ResizeObserver !== 'undefined') {
    return (
      <VirtualizedSessionRows
        rows={rows}
        renderRow={renderRow}
        getRowKey={keyOf}
        scrollElementRef={virtualize.scrollElementRef}
        estimateRowHeight={virtualize.estimateRowHeight}
        overscan={virtualize.overscan ?? 8}
      />
    )
  }

  return (
    <>
      {rows.map((row) => (
        <div key={keyOf(row)}>{renderRow(row)}</div>
      ))}
    </>
  )
}

function VirtualizedSessionRows({
  rows,
  renderRow,
  getRowKey,
  scrollElementRef,
  estimateRowHeight,
  overscan,
}: {
  rows: SessionTreeFlatRow[]
  renderRow: (row: SessionTreeFlatRow) => ReactNode
  getRowKey: (row: SessionTreeFlatRow) => string
  scrollElementRef: RefObject<HTMLElement | null>
  estimateRowHeight: number
  overscan: number
}) {
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollElementRef.current,
    estimateSize: () => estimateRowHeight,
    overscan,
  })
  const items = virtualizer.getVirtualItems()
  return (
    <div
      style={{ height: virtualizer.getTotalSize(), position: 'relative', width: '100%' }}
      data-testid="session-tree-virtual-spacer"
    >
      {items.map((vItem) => {
        const row = rows[vItem.index]
        return (
          <div
            key={getRowKey(row)}
            data-index={vItem.index}
            ref={virtualizer.measureElement}
            style={{
              position: 'absolute',
              top: 0,
              left: 0,
              width: '100%',
              transform: `translateY(${vItem.start}px)`,
            }}
          >
            {renderRow(row)}
          </div>
        )
      })}
    </div>
  )
}
