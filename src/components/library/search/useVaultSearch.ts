// useVaultSearch — the server-state half of the persistent Library search bar
// (library-b-c-design-2026-09-07 §C1; founder decision: a persistent bar, not
// a command palette).
//
// Three things live here and nothing else, mirroring useKnowledgeSearch.ts's
// division of labour: (1) the debounce, (2) scoping the query to the vault the
// currently-browsed folder belongs to, and (3) the POST to the human-search
// endpoint plus grouping its response into per-kind counts.
//
// ── Why this needs its OWN collection lookup ────────────────────────────────
//
// searchVault is scoped by `collection_id`, exactly like the agent-facing
// KnowledgeSearch box — but KnowledgeSearch receives that id as a prop from
// KnowledgePanel, which resolves it via GET /library/{ws}/knowledge?path=...
// (fetchKnowledgeBaseInfo). This bar is mounted independently of that panel
// (it lives above the file LIST, not inside the knowledge-base surface), so it
// resolves the same question itself, through the SAME query key
// (['knowledge-base-info', workspaceId, path]) KnowledgePanel already uses —
// both ask the identical question about the identical folder, so TanStack
// Query serves them from one cache entry rather than doubling the request.
//
// ── Disabled, not degraded ──────────────────────────────────────────────────
//
// A folder that is not inside a vault (or whose vault status is still being
// checked) has no collection_id to search with. Rather than accepting text
// nobody can answer, the caller is told to render the input `disabled` in that
// case (see `collectionId`/`isResolvingCollection` below) — the same posture
// KnowledgeSearch already takes when its own `collectionId` prop is undefined.
//
// ── Out-of-order responses ─────────────────────────────────────────────────
//
// Exactly the useKnowledgeSearch discipline: the debounced query text (and the
// resolved collection id) are part of the TanStack Query key, so a late
// response for an earlier query can never overwrite a later one — it writes
// into a cache entry nobody is reading any more.

import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { fetchKnowledgeBaseInfo, searchVault } from '@/lib/api'
import type { components } from '@/lib/api/generated/openapi-types'

export type VaultSearchRequest = components['schemas']['VaultSearchRequest']
export type VaultSearchResponse = components['schemas']['VaultSearchResponse']
export type VaultSearchNoteHit = components['schemas']['VaultSearchNoteHit']
export type VaultSearchRecordHit = components['schemas']['VaultSearchRecordHit']
export type VaultSearchViewHit = components['schemas']['VaultSearchViewHit']
export type KnowledgeBaseInfo = components['schemas']['KnowledgeBaseInfo']

/** Debounce before a keystroke becomes a request — same figure as
 *  useKnowledgeSearch's, for the same reason (a typed word is one request). */
export const VAULT_SEARCH_DEBOUNCE_MS = 250

/** The segmented filter's four positions (library-b-c-design-2026-09-07 §C1). */
export type VaultSearchKind = 'all' | 'notes' | 'records' | 'views'

/** The seam the component and its tests inject. Production default below. */
export type VaultSearchFn = (
  workspaceId: string,
  body: VaultSearchRequest,
  signal?: AbortSignal,
) => Promise<VaultSearchResponse>

/** postVaultSearch — the production fetcher: simply the shared client. Named
 *  so the seam has one obvious default, matching useKnowledgeSearch's
 *  postKnowledgeSearch. */
export const postVaultSearch: VaultSearchFn = (workspaceId, body, signal) =>
  searchVault(workspaceId, body, signal)

/** Test seam for the collection-detection lookup; production uses the module
 *  default, `fetchKnowledgeBaseInfo`. */
export type LoadCollectionInfoFn = (workspaceId: string, path: string) => Promise<KnowledgeBaseInfo>

export const vaultSearchQueryKeys = {
  /** Deliberately the SAME key shape KnowledgePanel's own useQuery uses —
   *  see this file's header. */
  collectionInfo: (workspaceId: string, path: string) =>
    ['knowledge-base-info', workspaceId, path] as const,
  search: (workspaceId: string, collectionId: string, query: string, limit: number) =>
    ['vault-search', workspaceId, collectionId, query, limit] as const,
}

export interface VaultSearchCounts {
  all: number
  notes: number
  records: number
  views: number
}

function emptyCounts(): VaultSearchCounts {
  return { all: 0, notes: 0, records: 0, views: 0 }
}

export interface UseVaultSearchOptions {
  /** null = the Library virtual root (every workspace as a top-level node) —
   *  there is no folder to test, so no lookup is issued and the bar is
   *  disabled. */
  workspaceId: string | null
  /** Workspace-relative folder currently browsed. Resolution matches
   *  KnowledgePanel's: a subfolder of a vault reports the SAME collection_id
   *  as the vault root, so this bar keeps working while browsing inside one. */
  folderPath: string
  /** Raw, undebounced text straight from the input. */
  query: string
  limit?: number
  debounceMs?: number
  searchFn?: VaultSearchFn
  loadCollectionInfo?: LoadCollectionInfoFn
}

export interface UseVaultSearchResult {
  /** The text the currently displayed (or in-flight) results belong to. */
  debouncedQuery: string
  /** True once the box holds non-blank text — the caller's cue to replace the
   *  file list with results (library-b-c-design-2026-09-07 §C1). */
  isActive: boolean
  isDebouncing: boolean
  isFetching: boolean
  isBusy: boolean
  /** True while the collection-detection lookup for this folder is still in
   *  flight — the input should stay disabled rather than accept a query it
   *  cannot yet scope. */
  isResolvingCollection: boolean
  /** Undefined until detection resolves, OR when this folder is not inside a
   *  vault at all — either way there is nothing to search, and the input
   *  should render disabled. */
  collectionId: string | undefined
  /** The vault's root, workspace-relative — needed to translate a hit's
   *  collection-relative path back into a workspace path the Library address
   *  model understands (mirrors KnowledgePanel's collectionPathToWorkspacePath
   *  use). Undefined exactly when collectionId is. */
  collectionRootPath: string | undefined
  error: Error | null
  response: VaultSearchResponse | undefined
  counts: VaultSearchCounts
}

export function useVaultSearch(options: UseVaultSearchOptions): UseVaultSearchResult {
  const {
    workspaceId,
    folderPath,
    query,
    limit = 20,
    debounceMs = VAULT_SEARCH_DEBOUNCE_MS,
    searchFn = postVaultSearch,
    loadCollectionInfo = fetchKnowledgeBaseInfo,
  } = options

  const trimmed = query.trim()
  const [debouncedQuery, setDebouncedQuery] = useState(trimmed)

  // See useKnowledgeSearch.ts's identical comment: kept out of the query key
  // so swapping the fetcher (tests) never fragments the cache.
  const searchFnRef = useRef(searchFn)
  searchFnRef.current = searchFn

  useEffect(() => {
    if (trimmed === '') {
      setDebouncedQuery('')
      return
    }
    const t = setTimeout(() => setDebouncedQuery(trimmed), debounceMs)
    return () => clearTimeout(t)
  }, [trimmed, debounceMs])

  const infoQuery = useQuery({
    queryKey: vaultSearchQueryKeys.collectionInfo(workspaceId ?? '', folderPath),
    queryFn: () => loadCollectionInfo(workspaceId as string, folderPath),
    enabled: workspaceId !== null,
    // Detection is a marker stat, not a moving value — matches KnowledgePanel's
    // own reasoning for the same query.
    refetchOnWindowFocus: false,
    retry: false,
  })

  const info = infoQuery.data
  const collectionId =
    info && info.is_knowledge_base && info.detection_error === undefined ? info.collection_id : undefined
  const collectionRootPath = collectionId !== undefined ? info?.root_path : undefined
  const isResolvingCollection = workspaceId !== null && infoQuery.isPending

  const active = collectionId !== undefined && debouncedQuery !== ''

  const result = useQuery({
    // debouncedQuery AND collectionId are both in the key — see this file's
    // header on out-of-order responses.
    queryKey: vaultSearchQueryKeys.search(workspaceId ?? '', collectionId ?? '', debouncedQuery, limit),
    queryFn: ({ signal }) =>
      searchFnRef.current(
        workspaceId as string,
        { query: debouncedQuery, collection_id: collectionId as string, limit },
        signal,
      ),
    enabled: active,
    retry: false,
    // A search answer is a statement about the index AT QUERY TIME (mirrors
    // useKnowledgeSearch.ts) — never served stale.
    staleTime: 0,
    gcTime: 0,
  })

  const response = active ? result.data : undefined

  const counts = useMemo<VaultSearchCounts>(() => {
    if (!response) return emptyCounts()
    return {
      notes: response.notes.length,
      records: response.records.length,
      views: response.views.length,
      all: response.notes.length + response.records.length + response.views.length,
    }
  }, [response])

  const isDebouncing = trimmed !== debouncedQuery
  const isFetching = active && result.isFetching

  return {
    debouncedQuery,
    isActive: trimmed !== '',
    isDebouncing,
    isFetching,
    isBusy: isFetching || (isDebouncing && trimmed !== ''),
    isResolvingCollection,
    collectionId,
    collectionRootPath,
    error: active ? ((result.error as Error | null) ?? null) : null,
    response,
    counts,
  }
}
