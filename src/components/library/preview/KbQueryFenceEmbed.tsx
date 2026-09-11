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
// Not wired into any note's markdown pipeline yet — that dispatch lives in
// kbMarkdownBase.tsx's / knowledgeMarkdown.tsx's `code` component override
// (see `classifyFence` in `@/components/chat/markdown-shared`, already used
// there for `language === 'mermaid'`), owned by a concurrent change. This is
// the component that dispatch is expected to mount for `language === 'query'`,
// and its call is exactly:
//
//   <KbQueryFenceEmbed workspaceId={workspaceId} collectionId={collectionId} query={text} />
//
// `collectionId` is the note's own `KnowledgeBaseInfo.collection_id` —
// `KnowledgeNoteView.tsx` already resolves this today (it is not a new
// dependency this component introduces); it simply is not threaded down
// into knowledgeMarkdown.tsx's markdown composition yet.

import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { MagnifyingGlass, Warning } from '@phosphor-icons/react'
import { searchVault } from '@/lib/api'
import type { components } from '@/lib/api/generated/openapi-types'
import { LazyEmbedMount } from './LazyEmbedMount'

type VaultSearchResponse = components['schemas']['VaultSearchResponse']

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
    <LazyEmbedMount reservedHeight={QUERY_FENCE_EMBED_RESERVED_HEIGHT_PX} className="my-3 block">
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
  })

  const totalHits = useMemo(() => {
    const d = searchQuery.data
    if (!d) return 0
    return d.notes.length + d.records.length + d.views.length + (d.attachments?.length ?? 0)
  }, [searchQuery.data])

  if (trimmed.length === 0) {
    return (
      <div
        data-testid="kb-query-fence-empty-notation"
        className="rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/5 px-3 py-2 text-xs text-[var(--color-warning)]"
      >
        This query is empty — nothing to search for.
      </div>
    )
  }

  if (searchQuery.isLoading) {
    return (
      <div
        data-testid="kb-query-fence-loading"
        className="flex items-center gap-2 rounded-md border border-[var(--color-border)] px-3 py-4 text-xs text-[var(--color-muted)]"
      >
        <MagnifyingGlass size={14} className="animate-pulse" /> Searching…
      </div>
    )
  }

  if (searchQuery.isError) {
    return (
      <div
        data-testid="kb-query-fence-error"
        className="flex flex-col items-start gap-2 rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/5 px-3 py-3 text-xs text-[var(--color-warning)]"
      >
        <span className="flex items-center gap-1.5">
          <Warning size={14} /> Could not run this query.
        </span>
        <button
          type="button"
          tabIndex={0}
          onClick={() => void searchQuery.refetch()}
          className="text-[11px] underline underline-offset-2"
        >
          Retry
        </button>
      </div>
    )
  }

  const data = searchQuery.data
  if (!data) return null

  // Honest, not silent: an index still catching up says so instead of
  // reading as "nothing matches" (the same distinction searchVault's own
  // doc comment names — "still indexing" vs "no results").
  if (!data.complete) {
    return (
      <div
        data-testid="kb-query-fence-incomplete"
        className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2 text-xs text-[var(--color-muted)]"
      >
        This knowledge base is still indexing — results may be incomplete.
        {data.complete_reason ? ` (${data.complete_reason})` : ''}
      </div>
    )
  }

  if (totalHits === 0) {
    return (
      <div
        data-testid="kb-query-fence-no-results"
        className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2 text-xs text-[var(--color-muted)]"
      >
        No results for “{trimmed}”.
      </div>
    )
  }

  return (
    <div
      data-testid="kb-query-fence-results"
      className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2 text-xs text-[var(--color-secondary)]"
    >
      <p className="mb-1.5 text-[var(--color-muted)]">
        Results for “{trimmed}” ({totalHits})
      </p>
      <ul className="space-y-1">
        {data.notes.map((hit) => (
          <li key={`note-${hit.path}`} data-testid="kb-query-fence-note-hit">
            <span className="font-medium">{hit.title}</span>
            {hit.snippet ? <span className="text-[var(--color-muted)]"> — {hit.snippet}</span> : null}
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
      </ul>
    </div>
  )
}
