// KbQueryFenceEmbed — ADR-083 embedded-content spec, Step 6: a ```query
// fenced code block in a knowledge-base note, per the spec's own
// "scope, restated so it cannot drift" (2026-09-09): "In: ... the inline
// data fence, ... query fences, mathematics (already working), ...".
//
// WHAT THIS RUNS, STATED HONESTLY: Obsidian's own `query` code block
// notation supports a small query LANGUAGE (`path:`, `tag:`, boolean
// combinators, etc.). This backend has no such language — it has ONE real,
// already-shipped search primitive: the human vault-search endpoint
// (`POST /library/{workspace_id}/knowledge/find`, `searchVault` in
// `@/lib/api`), which answers a single FREE-TEXT query against note bodies,
// record properties and saved-view names/labels in one call
// (library-b-c-design-2026-09-07 §C1 — the same engine
// `useVaultSearch.ts`'s persistent search bar already uses). This component
// therefore treats the ENTIRE fence body as one free-text query string and
// renders that endpoint's real results — it does NOT parse Obsidian's
// field-filter syntax, and never pretends to: a fence written as
// `path:"projects"` is sent to the search endpoint verbatim as the literal
// text `path:"projects"`, which is an honest (if perhaps unhelpful) search
// rather than a silently-ignored filter. There is no fabricated data path
// here — every render below is model on a REAL response from a REAL,
// already-contracted endpoint.
//
// Mounted through the same LazyEmbedMount budget every other embed kind
// uses (EMB-065) — a query fence issues a real network request, so it
// should not fire for a fence that is not near the viewport any more than a
// picture or a dashboard view does.
//
// Wired into the note markdown pipeline by `bfbb05948`:
// `knowledgeMarkdown.tsx`'s `KnowledgeMarkdownCode` (the `code` slot, using
// `classifyFence` from `@/components/chat/markdown-shared`) dispatches a
// block fence with `language === 'query'` here, passing the open note's own
// `workspaceId` and `KnowledgeBaseInfo.collection_id` off
// `KnowledgeLinkContext`. A `query` fence WITHOUT those two ids does not
// silently become an ordinary code block — see `KnowledgeMarkdownCode`'s own
// doc for the two reasons it distinguishes there.

import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { MagnifyingGlass, Warning } from '@phosphor-icons/react'
import { searchVault } from '@/lib/api'
import { ApiError } from '@/lib/api-error'
import {
  shouldRetryQuery,
  rateLimitAwareQueryRetryDelay,
  rateLimitedRetryDelayMs,
} from '@/lib/queryClient'
import type { components } from '@/lib/api/generated/openapi-types'
import { LazyEmbedMount } from './LazyEmbedMount'
import { stripWikilinkNotation } from './wikilinkNotation'

type VaultSearchResponse = components['schemas']['VaultSearchResponse']

/** UAT D-114: the lead sentence of the incomplete-answer notice, chosen from
 *  the server's own `complete_reason`. "Still indexing" is claimed ONLY when
 *  the reason itself says the index is not ready / catching up — the
 *  handler's own vocabulary (`rest_knowledge_find.go`: "index not ready
 *  yet", "still indexing", "freshness could not be verified"). Any other
 *  reason (a saved-view file or a record-type schema that failed to load —
 *  permanent until an operator fixes it) is stated as a part of the
 *  knowledge base that could not be searched, never as indexing progress.
 *  No reason at all gets the neutral sentence. Exported as a test seam. */
export function incompleteLead(reason: string | undefined): string {
  const r = (reason ?? '').trim()
  if (r === '') return 'These results may be incomplete.'
  if (/(indexing|not ready|catching up|freshness)/i.test(r)) {
    return 'This knowledge base is still indexing — results may be incomplete.'
  }
  return 'Part of this knowledge base could not be searched — results may be incomplete.'
}

/** A short results-list shape — smaller than a saved-view embed's reserved
 *  height, larger than a single-line notice (EMB-066 / test 96: heights
 *  differ per kind). */
export const QUERY_FENCE_EMBED_RESERVED_HEIGHT_PX = 160

/** Vault search caps hits per kind; a query fence is a compact inline
 *  summary, not the full search experience the persistent search bar
 *  already offers, so it asks for fewer per kind. */
const QUERY_FENCE_RESULT_LIMIT = 5

export interface KbQueryFenceEmbedProps {
  workspaceId: string
  collectionId: string
  /** The fence body, verbatim — see this file's header for why it is never
   *  parsed as a field-filter query language. */
  query: string
}

export function KbQueryFenceEmbed({ workspaceId, collectionId, query }: KbQueryFenceEmbedProps) {
  return (
    <LazyEmbedMount reservedHeight={QUERY_FENCE_EMBED_RESERVED_HEIGHT_PX} className="my-[var(--space-2-5)] block">
      <KbQueryFenceEmbedContent workspaceId={workspaceId} collectionId={collectionId} query={query} />
    </LazyEmbedMount>
  )
}

function KbQueryFenceEmbedContent({ workspaceId, collectionId, query }: KbQueryFenceEmbedProps) {
  const trimmed = query.trim()

  const searchQuery = useQuery<VaultSearchResponse>({
    // Deliberately the same shape useVaultSearch.ts's own key uses — a
    // second embed of the identical fence text in the same note dedupes
    // through TanStack Query rather than firing a second request.
    queryKey: ['kb-query-fence', workspaceId, collectionId, trimmed],
    queryFn: () => searchVault(workspaceId, { query: trimmed, collection_id: collectionId, limit: QUERY_FENCE_RESULT_LIMIT }),
    enabled: trimmed.length > 0,
    staleTime: 15_000,
    // F4 (SILENT-FAILURES-rate-limits-dd25339bf.md): honour a 429's
    // (capped) Retry-After instead of the blind app-wide curve — see
    // BasePreview.tsx's view-result query for the sibling fix and the full
    // rationale. Retry count/exclusions unchanged; only the 429 DELAY.
    retry: shouldRetryQuery,
    retryDelay: rateLimitAwareQueryRetryDelay,
  })

  // While WAITING on a retry after a 429, name the real wait instead of the
  // generic "Searching…" spinner — reads the SAME function the retryDelay
  // above is built from, so the two can never disagree.
  const throttledRetryDelayMs =
    searchQuery.isLoading && searchQuery.failureCount > 0
      ? rateLimitedRetryDelayMs(searchQuery.failureReason)
      : undefined

  const totalHits = useMemo(() => {
    const d = searchQuery.data
    if (!d) return 0
    return d.notes.length + d.records.length + d.views.length + (d.attachments?.length ?? 0)
  }, [searchQuery.data])

  if (trimmed.length === 0) {
    return (
      <div
        data-testid="kb-query-fence-empty-notation"
        className="rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/5 px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-utility-xs-size)] text-[var(--color-warning)]"
      >
        This query is empty — nothing to search for.
      </div>
    )
  }

  if (throttledRetryDelayMs !== undefined) {
    // F4: waiting on a retry the query is going to make anyway, honouring
    // (a capped) Retry-After — say so plainly, with the real wait, rather
    // than the indistinguishable-from-hung generic spinner.
    return (
      <div
        data-testid="kb-query-fence-throttled"
        className="flex items-center gap-[var(--space-2)] rounded-md border border-[var(--color-border)] px-[var(--space-2-5)] py-[var(--space-3)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]"
      >
        <MagnifyingGlass size={14} className="animate-pulse" /> Busy — retrying in{' '}
        {Math.ceil(throttledRetryDelayMs / 1000)}s
      </div>
    )
  }

  if (searchQuery.isLoading) {
    return (
      <div
        data-testid="kb-query-fence-loading"
        className="flex items-center gap-[var(--space-2)] rounded-md border border-[var(--color-border)] px-[var(--space-2-5)] py-[var(--space-3)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]"
      >
        <MagnifyingGlass size={14} className="animate-pulse" /> Searching…
      </div>
    )
  }

  if (searchQuery.isError) {
    // F4: a rate-limited refusal is named as such (never the generic
    // "Could not run this query."), which reads as broken when the query is
    // merely throttled.
    const rateLimited = searchQuery.error instanceof ApiError && searchQuery.error.isRateLimited()
    return (
      <div
        data-testid={rateLimited ? 'kb-query-fence-rate-limited' : 'kb-query-fence-error'}
        className="flex flex-col items-start gap-[var(--space-2)] rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/5 px-[var(--space-2-5)] py-[var(--space-2-5)] text-[length:var(--type-utility-xs-size)] text-[var(--color-warning)]"
      >
        <span className="flex items-center gap-[var(--space-1)]">
          <Warning size={14} />
          {rateLimited ? 'This query is rate-limited by the knowledge workspace limit.' : 'Could not run this query.'}
        </span>
        <button
          type="button"
          tabIndex={0}
          onClick={() => void searchQuery.refetch()}
          className="text-[length:var(--type-caption-size)] underline underline-offset-2"
        >
          Retry
        </button>
      </div>
    )
  }

  const data = searchQuery.data
  if (!data) {
    // NOT `return null`. With `networkMode: 'online'` (the default), a query
    // that is pending but PAUSED — the browser is offline — has
    // `isLoading === false`, `isError === false` and `data === undefined`,
    // so this branch is genuinely reachable. Returning nothing rendered the
    // fence as a hole in the note: no box, no border, no text, nothing to
    // tell the reader something was meant to be there at all.
    return (
      <div
        data-testid="kb-query-fence-waiting"
        className="flex items-center gap-[var(--space-2)] rounded-md border border-[var(--color-border)] px-[var(--space-2-5)] py-[var(--space-3)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]"
      >
        <MagnifyingGlass size={14} />
        {searchQuery.fetchStatus === 'paused'
          ? 'This query is waiting for a connection.'
          : 'This query has not run yet.'}
      </div>
    )
  }

  // Honest, not silent: an index still catching up says so instead of
  // reading as "nothing matches" (the same distinction searchVault's own
  // doc comment names — "still indexing" vs "no results"). The notice is
  // rendered ALONGSIDE whatever hits the answer already carries, never
  // instead of them: an incomplete answer returning two real matches used to
  // discard both, which is strictly less honest than "here is what we have
  // so far, and it may be incomplete".
  //
  // UAT D-114: "still indexing" was asserted for EVERY incomplete answer,
  // including one whose own reason was a permanent condition (two saved
  // view files that fail to load) on an index that was open and serving —
  // the banner contradicted itself and sent the reader to wait for a job
  // that had finished. The indexing claim is now made only when the
  // server's reason says so; any other reason is stated as what it is.
  const incompleteNotice = data.complete ? null : (
    <div
      data-testid="kb-query-fence-incomplete"
      className="mb-[var(--space-1)] rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]"
    >
      {incompleteLead(data.complete_reason)}
      {data.complete_reason ? ` (${data.complete_reason})` : ''}
    </div>
  )

  if (totalHits === 0) {
    return (
      <>
        {incompleteNotice}
        {/* A complete answer with no hits is a real "nothing matches"; an
            INCOMPLETE one is not, so it never claims to be — the notice
            above is the whole statement in that case. */}
        {data.complete ? (
          <div
            data-testid="kb-query-fence-no-results"
            className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]"
          >
            No results for “{trimmed}”.
          </div>
        ) : null}
      </>
    )
  }

  return (
    <>
      {incompleteNotice}
      <div
        data-testid="kb-query-fence-results"
        className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)]"
      >
        <p className="mb-[var(--space-1)] text-[var(--color-muted)]">
          Results for “{trimmed}” ({totalHits})
        </p>
        <ul className="space-y-[var(--space-1)]">
          {data.notes.map((hit) => (
            <li key={`note-${hit.path}`} data-testid="kb-query-fence-note-hit">
              <span className="font-medium">{hit.title}</span>
              {/* A snippet is a RAW BYTE EXCERPT of the note's own text, so a
                  frontmatter value like `owner: "[[Daniel Piatkowski]]"`
                  arrives with its brackets — the same WL-2 defect the Library
                  search bar fixed, on the same field from the same engine.
                  Stripped to display text, never rendered as a link: an
                  excerpt cannot claim a resolved/unresolved verdict. */}
              {hit.snippet ? (
                <span className="text-[var(--color-muted)]"> — {stripWikilinkNotation(hit.snippet)}</span>
              ) : null}
            </li>
          ))}
          {data.records.map((hit) => (
            <li key={`record-${hit.path}`} data-testid="kb-query-fence-record-hit">
              <span className="font-medium">{hit.title}</span>
              <span className="text-[var(--color-muted)]"> (record)</span>
            </li>
          ))}
          {data.views.map((hit) => (
            <li key={`view-${hit.view}`} data-testid="kb-query-fence-view-hit">
              <span className="font-medium">{hit.label}</span>
              <span className="text-[var(--color-muted)]"> (view)</span>
            </li>
          ))}
          {/* Attachments were COUNTED in `totalHits` and had no branch here,
              so a fence whose only matches were attachments rendered
              "Results (2)" above an empty list — and, because the count was
              non-zero, skipped the honest "No results" state entirely.
              Counting what you do not render is the defect; rendering them
              is the fix that keeps the two real matches nameable. */}
          {(data.attachments ?? []).map((hit) => (
            <li key={`attachment-${hit.path}`} data-testid="kb-query-fence-attachment-hit">
              <span className="font-medium">{hit.name}</span>
              <span className="text-[var(--color-muted)]"> (attachment)</span>
            </li>
          ))}
        </ul>
      </div>
    </>
  )
}
