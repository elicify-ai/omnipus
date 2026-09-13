// useCollectionLinkGraph — the collection-wide link evidence behind a base
// view's relation cells (WL-1's remaining half), shared by every surface that
// draws a ViewResult outside the note reader: the standalone `.base` pane
// (BasePreview) and the saved-view dialog the vault search bar opens
// (LibrarySearchBar).
//
// WHY THIS IS ONE REQUEST, NOT ONE PER ROW (UAT D-135). A view's relation
// cell holds a `[[wikilink]]` written in the ROW's own markdown file, so the
// collection-wide answer for a view's cells lives in the union of its rows'
// outbound-links graphs. Fetching that union used to mean one
// GET .../knowledge/graph?kind=links request PER ROW — measured as 40
// requests for a single 40-row view, and 136 hidden HTTP 429s after browsing
// ten bases in half a minute. The contract's `paths[]` parameter
// (contracts/openapi.yaml, getKnowledgeGraph) now carries the whole list in
// ONE request; this hook is the only SPA-side issuer of that shape, so "a
// view costs one link-graph request" is a property proven once here rather
// than re-implemented per surface.
//
// The row set is still bounded (COLLECTION_LINK_ROW_QUERY_CAP) and still
// filtered to rows that actually carry a `[[wikilink]]` cell
// (rowCarriesWikilink) — a request for rows with nothing to check is a
// request for nothing. Rows past the cap keep the same honest fallback they
// always had (row-title match, else `unknown`), never a silent promotion to
// `resolved`.

import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import type { UseQueryResult } from '@tanstack/react-query'

import { ApiError } from '@/lib/api-error'
import { fetchKnowledgeGraph } from '@/lib/api'
import type {
  KnowledgeGraphEdge,
  KnowledgeGraphNode,
} from '@/lib/api/generated/openapi-types'
import type { KnowledgeGraphLoader } from '../knowledge/KnowledgeBacklinks'
import type { KbLinkResolution } from './knowledgeMarkdown'

/** The production graph client every surface sharing this hook defaults to;
 *  tests inject their own through the component's `loadGraph` seam. */
export const defaultKnowledgeGraphLoader: KnowledgeGraphLoader = ({
  workspaceId,
  collectionId,
  kind,
  path,
  paths,
  hops,
  limit,
}) =>
  fetchKnowledgeGraph(workspaceId, {
    collectionId,
    kind,
    ...(path === undefined ? {} : { path }),
    ...(paths === undefined || paths.length === 0 ? {} : { paths }),
    ...(hops === undefined ? {} : { hops }),
    ...(limit === undefined ? {} : { limit }),
  })

/** Rows whose own markdown this surface wants link evidence for, at most.
 *  Deliberately below the server's `paths` bound (64) so a future caller
 *  cannot silently exceed it by raising this constant alone. */
export const COLLECTION_LINK_ROW_QUERY_CAP = 40

/** UAT D-135: does any cell of this row carry a `[[wikilink]]`? Only such a
 *  row has anything for the collection-wide link resolver to check. */
export function rowCarriesWikilink(row: { cells?: { value: string }[] }): boolean {
  return (row.cells ?? []).some((c) => c.value.includes('[['))
}

/** UAT D-135: page-wide ceiling on concurrent link-graph requests. One view
 *  costs ONE request now, but a dashboard can embed several views and the
 *  note reader still fetches per-note, so the ceiling stays: queued requests
 *  wait for a slot and the server's answer is never assumed. */
export const LINK_GRAPH_MAX_IN_FLIGHT = 4
let linkGraphInFlight = 0
const linkGraphWaiters: Array<() => void> = []
export async function withLinkGraphSlot<T>(run: () => Promise<T>): Promise<T> {
  if (linkGraphInFlight >= LINK_GRAPH_MAX_IN_FLIGHT) {
    await new Promise<void>((resolve) => linkGraphWaiters.push(resolve))
  }
  linkGraphInFlight += 1
  try {
    return await run()
  } finally {
    linkGraphInFlight -= 1
    linkGraphWaiters.shift()?.()
  }
}
/** Test seam: how many link-graph requests are on the wire right now. */
export function linkGraphInFlightCount(): number {
  return linkGraphInFlight
}

/** A KnowledgeGraphEdge's `to_path` basename, WITH its extension — mirrors
 *  KnowledgeNoteView.tsx's own private `basenameOf` exactly (edge matching
 *  compares against the collection-relative path a resolved edge reports,
 *  which keeps its extension). */
function basenameOf(path: string): string {
  const parts = path.split('/')
  return parts[parts.length - 1] || path
}

/** The record identifier a relation cell's `[[wikilink]]` token most often
 *  names, checked against the rows THIS view actually loaded (KB-8b) — no
 *  extension, unlike `basenameOf` above, because this is a row-identity
 *  comparison against titles and ids. */
function basenameNoExt(path: string): string {
  const base = path.split('/').pop() ?? path
  const dot = base.lastIndexOf('.')
  return dot <= 0 ? base : base.slice(0, dot)
}

export interface CollectionLinkRow {
  path: string
  title?: string
  id?: string
  cells?: { value: string }[]
}

/** The wikilink-carrying row paths of one ViewResult, first `cap` only. */
export function collectionLinkRowPaths(rows: CollectionLinkRow[], cap = COLLECTION_LINK_ROW_QUERY_CAP): string[] {
  const seen = new Set<string>()
  const paths: string[] = []
  for (const r of rows) {
    if (seen.has(r.path)) continue
    seen.add(r.path)
    if (!rowCarriesWikilink(r)) continue
    paths.push(r.path)
    if (paths.length >= cap) break
  }
  return paths
}

export interface UseCollectionLinkGraphArgs {
  workspaceId: string | null
  collectionId: string | undefined
  /** Collection-relative row paths that carry a `[[wikilink]]` — pass
   *  collectionLinkRowPaths(result.rows); an empty list issues nothing. */
  paths: string[]
  loadGraph: KnowledgeGraphLoader
}

export interface CollectionLinkGraph {
  edges: KnowledgeGraphEdge[]
  nodes: KnowledgeGraphNode[]
  /** How many link-graph requests failed (0 or 1 — this hook issues one). */
  failed: number
  /** Whether the failure was the gateway's own rate limiter (HTTP 429). */
  rateLimited: boolean
  query: UseQueryResult<{ edges: KnowledgeGraphEdge[]; nodes: KnowledgeGraphNode[] }, Error>
}

/**
 * ONE multi-path `kind=links` request covering every wikilink-carrying row of
 * one ViewResult (UAT D-135). Returns the union's edges and nodes plus the
 * two honesty facts a caller needs for its degraded-links banner: whether the
 * request failed, and whether it failed by being rate-limited.
 */
export function useCollectionLinkGraph({
  workspaceId,
  collectionId,
  paths,
  loadGraph,
}: UseCollectionLinkGraphArgs): CollectionLinkGraph {
  const query = useQuery({
    // Mirrors KnowledgeNoteView's own links-graph query key family — same
    // cache for the same (collection, path set), so two surfaces showing the
    // same rows share one request instead of issuing two.
    queryKey: ['library', workspaceId, 'knowledge', 'graph', 'links', collectionId, paths],
    queryFn: () =>
      withLinkGraphSlot(() =>
        loadGraph({
          workspaceId: workspaceId as string,
          collectionId: collectionId as string,
          kind: 'links' as const,
          paths,
        }),
      ),
    enabled: workspaceId !== null && collectionId !== undefined && paths.length > 0,
    staleTime: 60_000,
    retry: false,
    refetchOnWindowFocus: false,
  })

  const edges = useMemo(() => query.data?.edges ?? [], [query.data])
  const nodes = useMemo(() => query.data?.nodes ?? [], [query.data])
  const failed = query.isError ? 1 : 0
  const rateLimited =
    query.isError && query.error instanceof ApiError && query.error.status === 429

  return { edges, nodes, failed, rateLimited, query }
}

/**
 * The two-tier cell resolver this evidence supports — the SAME ladder
 * BasePreview has drawn since WL-1, factored out so the search bar's
 * saved-view dialog (Codex review #12) resolves a basename wikilink against
 * the collection rather than navigating to a literal not-found path.
 *
 * Tier 1 (real evidence): an edge whose link text or target names the token —
 * a genuine `resolved` (with the edge's collection-relative path) or
 * `unresolved`. Tier 2 (fallback): the target literally names one of the rows
 * THIS view loaded — resolved-or-unknown only, never `unresolved`, because
 * absence from a subset is not absence from the collection.
 */
export function makeCollectionLinkResolver(
  edges: KnowledgeGraphEdge[],
  nodes: KnowledgeGraphNode[],
  rows: CollectionLinkRow[],
): (target: string) => KbLinkResolution {
  return (target: string): KbLinkResolution => {
    const edge = edges.find(
      (e) => e.link_text === target || e.to_path === target || basenameOf(e.to_path) === target,
    )
    if (edge) {
      if (edge.resolution === 'unresolved') return { state: 'unresolved' }
      const node = nodes.find((n) => n.path === edge.to_path)
      if (node && node.exists === false) return { state: 'unresolved' }
      return { state: 'resolved', path: edge.to_path }
    }
    const match = rows.find(
      (r) => r.title === target || r.id === target || basenameNoExt(r.path) === target,
    )
    return match ? { state: 'resolved', path: match.path } : { state: 'unknown' }
  }
}
