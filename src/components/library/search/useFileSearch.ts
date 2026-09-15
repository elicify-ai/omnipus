// useFileSearch — the server-state half of the Library search bar's FILES kind
// (unified-search-and-grep-spec.md workstream B/C, US-2/US-4). Mirrors
// useVaultSearch.ts's division of labour — debounce, request, and response
// shaping — but with a different concurrency discipline (MV-11):
//
// ── Why this hook manages its OWN AbortController instead of leaning on
//    TanStack Query's per-key caching (the way useVaultSearch/useKnowledgeSearch
//    do) ─────────────────────────────────────────────────────────────────────
//
// The vault/knowledge hooks solve out-of-order responses by giving every
// distinct query text its OWN cache entry — a late answer to an old query
// writes into a cache nobody reads any more, but the OLD REQUEST ITSELF is
// never cancelled; it just runs to completion in the background.
//
// The engine behind file search holds a shared, gateway-wide 2-slot walk
// semaphore (MV-11) — leaving superseded walks running would starve that
// scarce resource on every keystroke. So this hook holds AT MOST ONE search
// in flight: a debounced query change aborts whatever was still running via
// `AbortSignal` before starting the next one, which cancels the server-side
// walk too (searchFiles/`request()` pass the signal straight to `fetch()`).
//
// ── 429 (busy) handling (MV-11) ──────────────────────────────────────────
//
// The endpoint sits behind the same 2-slot semaphore agent `grep` calls share.
// An over-cap REST request answers 429 with `Retry-After`. The bar must never
// error-flash a typist over that — it keeps whatever results are already on
// screen, waits 500 ms, and retries EXACTLY ONCE. A second failure (429 again,
// or anything else) surfaces as a normal error; this is a grace period for a
// transient collision with another search, not infinite silent retrying.
//
// ── "Am I still the answer anyone is waiting for?" ──────────────────────
//
// Every fetch's resolution (success OR failure) is checked against a request
// token before touching state. A response for a request this hook itself
// superseded (new keystroke, folder navigated away from, query cleared) is
// simply discarded — this also means an aborted fetch's rejection never has
// to be told apart from a real network failure by inspecting error shape.

import { useEffect, useRef, useState } from 'react'
import { searchFiles } from '@/lib/api'
import { ApiError } from '@/lib/api-error'
import type { components } from '@/lib/api/generated/openapi-types'

export type FileSearchRequest = components['schemas']['FileSearchRequest']
export type FileSearchResponse = components['schemas']['FileSearchResponse']
export type FileSearchHit = components['schemas']['FileSearchHit']

/** Same figure as the vault/knowledge bars — a typed word is one request. */
export const FILE_SEARCH_DEBOUNCE_MS = 250

/** MV-11: the human bar's interactive deadline (the engine's own default is
 *  10s; a person waiting on a keystroke gets the tighter bound). */
export const FILE_SEARCH_DEADLINE_MS = 3000

/** MV-11: wait this long after a 429 before the one permitted retry. */
export const FILE_SEARCH_RETRY_DELAY_MS = 500

/** The seam the component and its tests inject. Production default below. */
export type FileSearchFn = (
  workspaceId: string,
  body: FileSearchRequest,
  signal?: AbortSignal,
) => Promise<FileSearchResponse>

/** postFileSearch — the production fetcher: simply the shared client. */
export const postFileSearch: FileSearchFn = (workspaceId, body, signal) =>
  searchFiles(workspaceId, body, signal)

export interface UseFileSearchOptions {
  /** null = the Library virtual root — there is no folder to search. */
  workspaceId: string | null
  /** Workspace-relative folder currently browsed; scopes the walk
   *  (`FileSearchRequest.path`). '' searches the whole workspace root. */
  folderPath: string
  /** Raw, undebounced text straight from the input. */
  query: string
  /** False suppresses every request — used when the bar is in a different
   *  mode (e.g. a vault folder, where useVaultSearch is the active hook). */
  enabled?: boolean
  debounceMs?: number
  searchFn?: FileSearchFn
}

export interface UseFileSearchResult {
  /** The text the currently displayed (or in-flight) results belong to. */
  debouncedQuery: string
  isActive: boolean
  isDebouncing: boolean
  isFetching: boolean
  isBusy: boolean
  /** True while retrying after a 429 — the caller may use this to avoid
   *  double-announcing "Searching…", though the default UI does not. */
  isRetrying: boolean
  error: Error | null
  response: FileSearchResponse | undefined
}

export function useFileSearch(options: UseFileSearchOptions): UseFileSearchResult {
  const {
    workspaceId,
    folderPath,
    query,
    enabled = true,
    debounceMs = FILE_SEARCH_DEBOUNCE_MS,
    searchFn = postFileSearch,
  } = options

  const trimmed = query.trim()
  const [debouncedQuery, setDebouncedQuery] = useState(trimmed)

  useEffect(() => {
    if (trimmed === '') {
      setDebouncedQuery('')
      return
    }
    const t = setTimeout(() => setDebouncedQuery(trimmed), debounceMs)
    return () => clearTimeout(t)
  }, [trimmed, debounceMs])

  const searchFnRef = useRef(searchFn)
  searchFnRef.current = searchFn

  const [response, setResponse] = useState<FileSearchResponse | undefined>(undefined)
  const [error, setError] = useState<Error | null>(null)
  const [isFetching, setIsFetching] = useState(false)
  const [isRetrying, setIsRetrying] = useState(false)

  // Identifies the current logical request. Bumped every time the effect
  // below re-runs for a new (or cleared) query, so a settling promise from a
  // superseded request can tell it is stale without inspecting what it threw.
  const requestTokenRef = useRef(0)
  const controllerRef = useRef<AbortController | null>(null)
  const retryTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const active = enabled && workspaceId !== null && debouncedQuery !== ''

  useEffect(() => {
    // A superseded request is cancelled server-side too — the walk semaphore
    // is a scarce, shared 2-slot resource (MV-11), not just a local courtesy.
    controllerRef.current?.abort()
    controllerRef.current = null
    const myToken = ++requestTokenRef.current
    const isCurrent = () => requestTokenRef.current === myToken

    if (!active) {
      setResponse(undefined)
      setError(null)
      setIsFetching(false)
      setIsRetrying(false)
      return
    }

    function run(attempt: number) {
      const controller = new AbortController()
      controllerRef.current = controller
      setIsFetching(true)
      if (attempt > 0) {
        setIsRetrying(true)
      } else {
        // R-3: a NEW logical request (attempt 0) must not carry a PREVIOUS
        // query's error forward. Before this, `error` was only ever cleared
        // in the success branch below, so a query that failed left its
        // error on screen through every subsequent query's own fetch —
        // LibrarySearchBar renders the banner unconditionally and its
        // `!error &&` guards suppress every result block, so a working
        // query B rendered query A's stale failure (and no results) for B's
        // entire in-flight duration. `response` is deliberately NOT reset
        // here — the 429 grace path (below) needs whatever is already on
        // screen to stay put while it retries.
        setError(null)
      }

      void searchFnRef
        .current(
          workspaceId as string,
          {
            query: debouncedQuery,
            ...(folderPath === '' ? {} : { path: folderPath }),
            // MV-11/FR-016: the human bar ALWAYS sends a literal, smart-case
            // query — regex metacharacters typed into the box match
            // literally, and no parse error can ever surface from the bar.
            regex: false,
            case: 'smart',
            include_hidden: false,
            // KB-7b: the bar always asks for document-level AND (every
            // query word present somewhere in the file, collapsed to one
            // hit per file) — "quarterly report" now finds a file that
            // discusses both words in different paragraphs, matching how a
            // person actually reads the query, instead of only a file with
            // that literal phrase on one line.
            match_all_words: true,
            // KB-6c: one bare line is rarely enough to judge a hit — this
            // supersedes the earlier v1 decision (spec R2-MIN-005) to leave
            // context at 0. The engine already fills context_before/after;
            // asking for 1 needs no backend or contract change beyond this.
            context_lines: 1,
            limits: { deadline_ms: FILE_SEARCH_DEADLINE_MS },
          },
          controller.signal,
        )
        .then((res) => {
          if (!isCurrent()) return
          setResponse(res)
          setError(null)
          setIsFetching(false)
          setIsRetrying(false)
        })
        .catch((err: unknown) => {
          if (!isCurrent()) return
          const status = err instanceof ApiError ? err.status : undefined
          if (status === 429 && attempt === 0) {
            // MV-11: keep whatever is already on screen, retry once, no
            // error flash for a typist who happened to collide with another
            // search.
            retryTimeoutRef.current = setTimeout(() => {
              retryTimeoutRef.current = null
              if (isCurrent()) run(1)
            }, FILE_SEARCH_RETRY_DELAY_MS)
            return
          }
          setError(err instanceof Error ? err : new Error(String(err)))
          setIsFetching(false)
          setIsRetrying(false)
        })
    }

    run(0)

    return () => {
      controllerRef.current?.abort()
      // A pending 429 retry (MV-11) is a live timer, not just an in-flight
      // request — left uncleared, a superseded or unmounted hook would still
      // fire it and occupy a walk-semaphore slot nothing will ever abort.
      if (retryTimeoutRef.current !== null) {
        clearTimeout(retryTimeoutRef.current)
        retryTimeoutRef.current = null
      }
    }
    // searchFn is deliberately NOT a dependency — it is read through
    // searchFnRef (see useVaultSearch.ts's identical comment) so that
    // swapping the fetcher (tests) never re-triggers a request on its own.
  }, [active, workspaceId, folderPath, debouncedQuery])

  const isDebouncing = trimmed !== debouncedQuery

  return {
    debouncedQuery,
    isActive: trimmed !== '',
    isDebouncing,
    isFetching: active && isFetching,
    isBusy: (active && isFetching) || (isDebouncing && trimmed !== ''),
    isRetrying,
    error: active ? error : null,
    response: active ? response : undefined,
  }
}
