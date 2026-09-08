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
export type VaultSearchAttachmentHit = components['schemas']['VaultSearchAttachmentHit']
export type KnowledgeBaseInfo = components['schemas']['KnowledgeBaseInfo']

/** Debounce before a keystroke becomes a request — same figure as
 *  useKnowledgeSearch's, for the same reason (a typed word is one request). */
export const VAULT_SEARCH_DEBOUNCE_MS = 250

/** The segmented filter's five positions (library-b-c-design-2026-09-07 §C1;
 *  `attachments` added by unified-search-and-grep-spec.md US-1/MV-9 — the
 *  ported attachment-filename search the retired KnowledgeSearch box also
 *  covered). */
export type VaultSearchKind = 'all' | 'notes' | 'records' | 'views' | 'attachments'

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
  attachments: number
}

function emptyCounts(): VaultSearchCounts {
  return { all: 0, notes: 0, records: 0, views: 0, attachments: 0 }
}

// ── Honesty port (unified-search-and-grep-spec.md US-1/MV-9/FR-036/FR-037) ──
//
// These mirror useKnowledgeSearch.ts's classifyHonesty/clampOf exactly in
// spirit — the retired KnowledgeSearch box's honesty guarantees must survive
// onto this bar unweakened — but the WIRE SHAPE they read is simpler:
// VaultSearchResponse states its coverage directly (`notes_searched` /
// `notes_total_known`) rather than through a nested incompleteness object
// with a separate boolean flag, and it carries no `limit_applied` echo (see
// vaultClampOf below).

/**
 * The coverage state of one search answer, for NOTES specifically — the only
 * kind the contract states a searched/total count for.
 *
 *  - `undefined` — either the answer is complete (nothing partial to
 *    disclose) or the server sent no coverage numbers at all.
 *  - `'ratio'`   — a partial answer WITH a usable denominator
 *    (`notes_total_known` is a number): "X of Y notes searched" may be shown.
 *  - `'so-far'`  — a partial answer with a count but NO denominator: a bare
 *    count, "so far" — never an invented total (FR-036).
 */
export type VaultSearchCoverage = 'ratio' | 'so-far' | undefined

export function classifyVaultCoverage(res: VaultSearchResponse): VaultSearchCoverage {
  if (res.complete) return undefined
  if (typeof res.notes_searched !== 'number') return undefined
  return typeof res.notes_total_known === 'number' ? 'ratio' : 'so-far'
}

export interface VaultSearchClamp {
  /** The number the caller asked for, when the server echoed it. Absent when
   *  the server reported the clamp without echoing the refused number — the
   *  clamp is still stated, just without that figure (mirrors
   *  useKnowledgeSearch.ts's KnowledgeSearchClamp). */
  requested?: number
}

/**
 * vaultClampOf returns the clamp to report, or null when nothing was
 * clamped (FR-037: a clamp is disclosed, never silently applied).
 *
 * UNLIKE the retired KnowledgeSearchResponse, VaultSearchResponse carries no
 * `limit_applied` echo — there is no server-stated "here is the cap you got
 * instead" number to show alongside `limit_requested`. Inventing one (e.g.
 * from the client's own default) would be exactly the fabricated-certainty
 * failure this whole feature refuses elsewhere, so the clamp is reported
 * without a specific applied figure.
 */
export function vaultClampOf(res: VaultSearchResponse): VaultSearchClamp | null {
  if (!res.limit_clamped) return null
  return res.limit_requested === undefined ? {} : { requested: res.limit_requested }
}

/**
 * isNotesCappedAtLimit — whether the NOTES list may have been cut short by
 * the per-kind cap. Prefers the server's own `notes_capped_at_limit` when the
 * response states it (server truth); falls back to the same length-vs-limit
 * heuristic the bar already uses for records/views/attachments, which have no
 * analogous server-stated flag.
 */
export function isNotesCappedAtLimit(res: VaultSearchResponse, limit: number): boolean {
  if (res.notes_capped_at_limit !== undefined) return res.notes_capped_at_limit
  return res.notes.length > 0 && res.notes.length >= limit
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
  /** The effective per-kind result cap. A kind whose array length equals this
   *  may have more matches than were returned, so the caller renders "N+". */
  limit: number
  /** The notes coverage state (US-1 AS-2/AS-3, FR-036) — undefined when the
   *  answer is complete or the server sent no coverage numbers. */
  coverage: VaultSearchCoverage
  /** The clamp to disclose (FR-037), or null when nothing was clamped. */
  clamp: VaultSearchClamp | null
  /** Whether the notes list may have been cut short by the per-kind cap —
   *  server truth (`notes_capped_at_limit`) when stated, else the same
   *  length-vs-limit heuristic the bar uses for the other kinds. */
  notesCappedAtLimit: boolean
  /** Finding F-I: KnowledgeBaseInfo.detection_error's message when
   *  detection could not complete (a marker exists but could not be read,
   *  or the root itself could not be stat-ed — E-9), OR the collection-info
   *  request itself failing outright (infoQuery.error). Either way,
   *  collectionId is already undefined (below) — this is what tells the
   *  caller WHY, so it can surface the failure instead of silently
   *  concluding "not a knowledge base" and falling through to a plain file
   *  walk over a folder whose detection genuinely never completed.
   *  KnowledgeBaseInfo.yaml's own contract requires the caller to surface
   *  this rather than downgrade it — KnowledgePanel already does; this bar
   *  was the gap. */
  detectionError: string | undefined
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

  // Finding F-I: detection can fail two different ways — the info REQUEST
  // itself errors (infoQuery.error), or the request succeeds but detection
  // could not complete for this folder (info.detection_error, E-9). Both
  // must surface; neither did before this fix, which is exactly how a
  // knowledge base with a genuine detection failure silently became "just
  // an ordinary folder" to this bar.
  const detectionError =
    info?.detection_error?.message ??
    (infoQuery.error instanceof Error ? infoQuery.error.message : undefined)

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
    // attachments is optional on the wire (absent ⇒ treated as [], MV-9's
    // platform carve-out) — a build without the properties index sends no
    // group at all rather than a bare empty one, and this is where "absent"
    // and "empty" collapse into the same displayed count.
    const attachments = response.attachments?.length ?? 0
    return {
      notes: response.notes.length,
      records: response.records.length,
      views: response.views.length,
      attachments,
      all: response.notes.length + response.records.length + response.views.length + attachments,
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
    // The effective per-kind cap. The server truncates each kind to this, so a
    // kind whose array length equals it may have more matches than shown — the
    // bar renders "N+" rather than a flat count that silently plateaus.
    limit,
    coverage: response ? classifyVaultCoverage(response) : undefined,
    clamp: response ? vaultClampOf(response) : null,
    notesCappedAtLimit: response ? isNotesCappedAtLimit(response, limit) : false,
    detectionError,
  }
}
