// useKnowledgeSearch — the server-state half of the knowledge-base search box
// (ADR-067 US-5, US-6, US-8; FR-035, FR-036, FR-037).
//
// Three things live here and nothing else: (1) the debounce, (2) the POST to
// the contract-defined search endpoint, and (3) the CLASSIFICATION of the
// response's own honesty fields into the states the UI must render. The
// classification is deliberately a pair of pure, exported functions rather than
// inline ternaries in the component, because they are the requirement — they
// are what a reviewer has to be able to read and a test has to be able to pin
// down without mounting React.
//
// ── Why classification is not "just read `complete`" ────────────────────────
//
// KnowledgeSearchIncompleteness carries THREE fields that interact:
// `complete`, `total_known`, and an OPTIONAL `total_files`. The contract says
// total_files is "present only when total_known is true" — which is not the
// same promise as "present whenever total_known is true". So there is a fourth,
// unnamed shape: incomplete, total_known=true, total_files ABSENT. A client
// that trusts total_known alone renders "Searched 4,120 of undefined notes",
// or worse, computes 4120/undefined and paints a NaN-wide progress bar.
//
// The rule this module enforces: a ratio is rendered only when BOTH numbers are
// actually in hand. Anything else degrades to INDETERMINATE — a count, no
// denominator. That is the whole point of FR-036: the total being unknown is a
// state to be REPORTED, never a gap to be filled with an invented number.
//
// ── Out-of-order responses ─────────────────────────────────────────────────
//
// A slow answer to an earlier query must never overwrite a later query's
// results. This is handled STRUCTURALLY rather than with a sequence counter:
// the debounced query text is part of the TanStack Query key, so each distinct
// query text owns its own cache entry, and a late resolution writes into the
// entry nobody is observing any more. Removing the query text from the key
// (or hoisting results into a `useState` written from a bare `.then`) breaks
// this — see the out-of-order test in useKnowledgeSearch.test.ts.
//
// ── The fetch is src/lib/api.ts's `searchKnowledge`, not a private one ─────
//
// An earlier revision built its own `fetch()` here, on the grounds that this
// wave did not own api.ts. That opted the one POST in this feature out of
// everything `request()` gives every other wire call: the schema-error counter,
// the dev-mode toast, `ApiSchemaError` carrying the raw body, `ApiError`'s
// status/code parsing, and the CSRF re-mint retry. Constraint #8 asks for
// "drop + counter + dev-mode toast on failure"; a bare `Schema.parse()`
// throwing a raw ZodError into a component is half of that and looks identical
// to the other half from the outside. The default fetcher is now the shared
// client and this module owns only the debounce and the classification.

import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { searchKnowledge } from '@/lib/api'
import type { components } from '@/lib/api/generated/openapi-types'

export type KnowledgeSearchRequest = components['schemas']['KnowledgeSearchRequest']
export type KnowledgeSearchResponse = components['schemas']['KnowledgeSearchResponse']
export type KnowledgeSearchHit = components['schemas']['KnowledgeSearchHit']
export type KnowledgeSearchIncompleteness = components['schemas']['KnowledgeSearchIncompleteness']

/** Debounce before a keystroke becomes a request. Long enough that typing a
 *  word is one query rather than five; short enough that the reader does not
 *  perceive a pause. */
export const KNOWLEDGE_SEARCH_DEBOUNCE_MS = 250

/**
 * The honesty state of one search response (US-6).
 *
 *  - `complete`      — the index covered the whole collection at query time.
 *                      These results are the whole answer and nothing further
 *                      needs saying (US-6 AS-4).
 *  - `incomplete`    — a partial answer WITH a usable denominator. A ratio of
 *                      indexed to total may be shown (US-6 AS-2).
 *  - `indeterminate` — a partial answer with NO usable denominator, either
 *                      because the tree is still being walked (total_known
 *                      false) or because the server reported a known total
 *                      without sending the number. A count, never a ratio
 *                      (US-6 AS-1, FR-036).
 */
export type KnowledgeSearchHonesty = 'complete' | 'incomplete' | 'indeterminate'

/**
 * What an empty result list actually means. "Nothing in the collection" and
 * "nothing matching this query" are different facts about the world and a
 * reader must be able to tell which one they are looking at (edge case E-1).
 *
 *  - `has-hits`         — not empty.
 *  - `collection-empty` — searched everything, and the collection itself holds
 *                         no files. Claimed ONLY on proof: a complete answer
 *                         whose total is known to be zero.
 *  - `no-matches`       — searched everything; this query matched nothing.
 *  - `still-indexing`   — empty SO FAR, but the index is incomplete, so this is
 *                         not evidence of either of the above. The indexing
 *                         state outranks the empty-collection first run
 *                         (US-6 AS-5).
 */
export type KnowledgeSearchEmptiness =
  | 'has-hits'
  | 'collection-empty'
  | 'no-matches'
  | 'still-indexing'

/**
 * classifyHonesty maps the response's incompleteness object onto the state the
 * UI must render. Pure, and the single place the "do we have a denominator?"
 * question is answered.
 */
export function classifyHonesty(inc: KnowledgeSearchIncompleteness): KnowledgeSearchHonesty {
  if (inc.complete) return 'complete'
  // A ratio needs BOTH numbers. total_known is the server's claim; the two
  // numbers actually being present is the proof. Require the proof.
  if (inc.total_known && typeof inc.total_files === 'number' && typeof inc.indexed_files === 'number') {
    return 'incomplete'
  }
  return 'indeterminate'
}

/**
 * classifyEmptiness answers "what does this empty list mean?" — see the
 * KnowledgeSearchEmptiness doc for the four answers and when each is earned.
 */
export function classifyEmptiness(res: KnowledgeSearchResponse): KnowledgeSearchEmptiness {
  if (res.hits.length > 0) return 'has-hits'
  const inc = res.incompleteness
  if (!inc.complete) return 'still-indexing'
  // Only a complete answer that also reports a known total of zero is proof of
  // an empty collection. A complete answer with no total attached tells us the
  // query matched nothing, which is all we may say.
  //
  // ⚠ THE GATEWAY DOES NOT CURRENTLY SEND THAT PROOF, AND THAT IS WHY THIS
  // BRANCH MUST NOT BE THE ONLY ROAD TO THE FACT. `total_files` is assigned in
  // exactly one place in pkg/gateway/rest_knowledge.go, inside the
  // `!Complete && Total > 0` arm of its switch, so a COMPLETE answer never
  // carries it and this branch is unreachable against that server today. It is
  // kept because the contract permits the shape (`total_files` is documented as
  // "present only when total_known is true", which is a precondition, not a
  // promise) and because inferring emptiness from its ABSENCE would be a
  // fabricated claim. The reader is told a collection is empty by the indexer's
  // own idle-with-zero-files frame instead — see KnowledgePanel's
  // `empty_collection` state — and the search box, which cannot tell the two
  // apart from this payload, says only what it knows and shows the server's own
  // sentence alongside it.
  if (inc.total_known && inc.total_files === 0) return 'collection-empty'
  return 'no-matches'
}

/**
 * True when the hit list is exactly as long as the cap the server applied —
 * i.e. the list may have been cut short and there is no way to ask for more.
 *
 * WHY THIS IS NOT THE SAME QUESTION AS `limit_clamped` (FR-037). `limit_clamped`
 * says "you asked for more than the maximum and I refused"; it is false whenever
 * the caller asked for a number the server was happy with — which is every
 * request this SPA makes, since it asks for 20 against a cap of 100. But the
 * gateway also stops appending hits the moment it has `limit_applied` of them
 * (`if len(resp.Hits) >= applied { break }`) and sends no total-match count, so
 * a query matching five hundred notes comes back as twenty hits with
 * `complete: true` and nothing marking the other four hundred and eighty.
 *
 * A bare "20 results" over that answer is a confidently incomplete one — the
 * precise failure US-6 exists to prevent, reached through the one surface an
 * operator can actually click. Equality is the only signal available, and it is
 * deliberately conservative in the safe direction: a collection with exactly
 * twenty matches is described as possibly having more, which understates
 * certainty rather than overstating it.
 */
export function isCappedAtLimit(res: KnowledgeSearchResponse): boolean {
  return res.hits.length > 0 && res.hits.length >= res.limit_applied
}

export interface KnowledgeSearchClamp {
  applied: number
  /** The limit the caller asked for. Absent when the server reported the clamp
   *  without echoing the request — the clamp is still stated, just without the
   *  refused number. */
  requested?: number
}

/**
 * clampOf returns the clamp to report, or null when nothing was clamped.
 * FR-037: the clamp is reported, never silently applied.
 */
export function clampOf(res: KnowledgeSearchResponse): KnowledgeSearchClamp | null {
  if (!res.limit_clamped) return null
  return res.limit_requested === undefined
    ? { applied: res.limit_applied }
    : { applied: res.limit_applied, requested: res.limit_requested }
}

/** The seam the component and its tests inject. Production default below. */
export type KnowledgeSearchFn = (
  workspaceId: string,
  body: KnowledgeSearchRequest,
  signal?: AbortSignal,
) => Promise<KnowledgeSearchResponse>

/**
 * postKnowledgeSearch — the production fetcher, which is simply the shared
 * client. Kept as a named export so the seam has one obvious default and a test
 * can assert that the component reaches the real client rather than only ever
 * seeing an injected stub.
 */
export const postKnowledgeSearch: KnowledgeSearchFn = (workspaceId, body, signal) =>
  searchKnowledge(workspaceId, body, signal)

export const knowledgeQueryKeys = {
  search: (
    workspaceId: string,
    collectionId: string,
    query: string,
    limit: number,
    offset: number,
  ) => ['knowledge', workspaceId, collectionId, 'search', query, limit, offset] as const,
}

export interface UseKnowledgeSearchOptions {
  workspaceId: string
  /** Undefined until the collection is known (e.g. detection still running).
   *  No request is issued without one. */
  collectionId?: string
  /** Raw, undebounced text straight from the input. */
  query: string
  limit?: number
  offset?: number
  debounceMs?: number
  enabled?: boolean
  searchFn?: KnowledgeSearchFn
}

export interface UseKnowledgeSearchResult {
  /** The text the currently displayed (or in-flight) results belong to. */
  debouncedQuery: string
  /** True between a keystroke and the debounce firing. */
  isDebouncing: boolean
  /** True while a request for the current debounced query is in flight. */
  isFetching: boolean
  /** True when the reader is waiting on something — debounce or request. */
  isBusy: boolean
  error: Error | null
  response: KnowledgeSearchResponse | undefined
  hits: KnowledgeSearchHit[]
  honesty: KnowledgeSearchHonesty | undefined
  emptiness: KnowledgeSearchEmptiness | undefined
  clamp: KnowledgeSearchClamp | null
  /** The hit list reached the server's applied cap — see isCappedAtLimit. */
  cappedAtLimit: boolean
}

export function useKnowledgeSearch(options: UseKnowledgeSearchOptions): UseKnowledgeSearchResult {
  const {
    workspaceId,
    collectionId,
    query,
    limit = 20,
    offset = 0,
    debounceMs = KNOWLEDGE_SEARCH_DEBOUNCE_MS,
    enabled = true,
    searchFn = postKnowledgeSearch,
  } = options

  const trimmed = query.trim()
  const [debouncedQuery, setDebouncedQuery] = useState(trimmed)

  // The fetcher is read through a ref inside queryFn so that swapping it (tests,
  // or a future api.ts client) never becomes part of the query key — the key
  // must describe the QUESTION, not the transport, or two identical questions
  // would occupy two cache entries and the out-of-order guarantee would be
  // keyed on the wrong thing.
  const searchFnRef = useRef(searchFn)
  searchFnRef.current = searchFn

  useEffect(() => {
    // Clearing the box takes effect at once: there is no pending answer worth
    // waiting for, and holding the previous term would leave stale results on
    // screen attributed to an empty query.
    if (trimmed === '') {
      setDebouncedQuery('')
      return
    }
    const t = setTimeout(() => setDebouncedQuery(trimmed), debounceMs)
    return () => clearTimeout(t)
  }, [trimmed, debounceMs])

  const active = enabled && collectionId !== undefined && debouncedQuery !== ''

  const result = useQuery({
    // debouncedQuery is IN the key on purpose — see the out-of-order note in
    // this file's header.
    queryKey: knowledgeQueryKeys.search(
      workspaceId,
      collectionId ?? '',
      debouncedQuery,
      limit,
      offset,
    ),
    queryFn: ({ signal }) =>
      searchFnRef.current(
        workspaceId,
        {
          query: debouncedQuery,
          collection_id: collectionId as string,
          limit,
          offset,
        },
        signal,
      ),
    enabled: active,
    retry: false,
    // A search answer is a statement about the index AT QUERY TIME. Serving it
    // from cache later would re-assert a stale incompleteness claim, so it is
    // never treated as fresh.
    staleTime: 0,
    gcTime: 0,
  })

  const response = active ? result.data : undefined

  const derived = useMemo(() => {
    if (!response) {
      return {
        hits: [] as KnowledgeSearchHit[],
        honesty: undefined,
        emptiness: undefined,
        clamp: null as KnowledgeSearchClamp | null,
        cappedAtLimit: false,
      }
    }
    return {
      hits: response.hits,
      honesty: classifyHonesty(response.incompleteness),
      emptiness: classifyEmptiness(response),
      clamp: clampOf(response),
      cappedAtLimit: isCappedAtLimit(response),
    }
  }, [response])

  const isDebouncing = trimmed !== debouncedQuery
  const isFetching = active && result.isFetching

  return {
    debouncedQuery,
    isDebouncing,
    isFetching,
    isBusy: isFetching || (isDebouncing && trimmed !== ''),
    error: active ? ((result.error as Error | null) ?? null) : null,
    response,
    ...derived,
  }
}
