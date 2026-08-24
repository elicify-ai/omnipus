// KnowledgeSearch — the search box over one knowledge base, and the surface
// that has to tell the truth about what it just returned (ADR-067 US-5, US-6,
// US-8; FR-035, FR-036, FR-037).
//
// The backend refuses to give a confidently incomplete answer: every
// KnowledgeSearchResponse carries an incompleteness object in the SAME payload
// as its hits, precisely so a client cannot obtain results without also
// obtaining the sentence qualifying them. This component's job is to not undo
// that. Concretely, three rules it is built around:
//
//   1. A partial result says so IN THE READING FLOW — a bordered block above
//      the results, `role="status"`, carrying the server's own statement. Not a
//      tooltip, not a title attribute, not a console warning. If the reader can
//      miss it, the guarantee is gone and nothing about the screen looks wrong.
//
//   2. While the tree is still being walked the total is UNKNOWN, so there is
//      no denominator. This renders a count and the word "so far" — never a
//      ratio, never a percentage, and NEVER a <progress> or role="progressbar".
//      A progress bar drawn against an invented total is exactly the
//      confidently-wrong answer the whole feature refuses everywhere else, and
//      it is the most convincing possible way to state it.
//
//   3. A clamped count says it was clamped, with the number that was refused
//      where the server sent it (FR-037).
//
// And the fourth, which is about emptiness rather than incompleteness: "this
// collection has no notes" and "this query matched nothing" are different facts
// (edge case E-1), they are reached by different roads, and the reader gets a
// different sentence for each. The claim "the collection is empty" is made only
// on proof — a complete answer whose known total is zero. Anything weaker says
// "no matches", which is true either way. And while the index is still
// building, neither claim is available at all: the indexing state outranks the
// empty-collection first run (US-6 AS-5).
//
// Classification lives in useKnowledgeSearch.ts as pure functions; this file
// renders their output and owns no honesty logic of its own.

import { useId, useState } from 'react'
import { MagnifyingGlass, SpinnerGap, Warning, Info, FileText } from '@phosphor-icons/react'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { LibraryErrorBanner } from '../LibraryErrorBanner'
import {
  useKnowledgeSearch,
  type KnowledgeSearchFn,
  type KnowledgeSearchHit,
} from './useKnowledgeSearch'

/** Human sentences for the machine-readable reasons a hit carries no excerpt.
 *  The hit is still shown — a result is never silently dropped for want of an
 *  excerpt, and an excerpt is never fabricated.
 *
 *  This Record is deliberately EXHAUSTIVE over the contract enum rather than a
 *  lookup with a fallback string: adding a reason to
 *  KnowledgeSearchHit.excerpt_unavailable without a sentence for it then fails
 *  `tsc`, instead of shipping a hit whose reason renders as blank. That is how
 *  `attachment_not_read` was caught. */
const EXCERPT_UNAVAILABLE_REASON: Record<
  NonNullable<KnowledgeSearchHit['excerpt_unavailable']>,
  string
> = {
  file_unreadable: 'No excerpt: the file could not be read.',
  file_missing: 'No excerpt: the file is no longer there.',
  match_moved: 'No excerpt: the file changed since it was indexed.',
  budget_exhausted: 'No excerpt: the time budget for reading excerpts ran out.',
  // Not a failure: an attachment is found by its name and path, and its
  // contents are never opened (FR-039a), so there is nothing to quote.
  attachment_not_read: 'Matched on the file name — attachment contents are never read.',
}

function formatCount(n: number): string {
  return n.toLocaleString('en-US')
}

export interface KnowledgeSearchProps {
  workspaceId: string
  /** Undefined while the collection is still being detected — the box renders
   *  disabled rather than issuing a request it cannot scope. */
  collectionId?: string
  /** Opens a hit. Absent in read-only embeddings. */
  onOpenNote?: (path: string) => void
  limit?: number
  debounceMs?: number
  /** Test seam; production uses the module default in useKnowledgeSearch. */
  searchFn?: KnowledgeSearchFn
  className?: string
}

export function KnowledgeSearch({
  workspaceId,
  collectionId,
  onOpenNote,
  limit,
  debounceMs,
  searchFn,
  className,
}: KnowledgeSearchProps) {
  const [text, setText] = useState('')
  const inputId = useId()

  const {
    debouncedQuery,
    isBusy,
    error,
    response,
    hits,
    honesty,
    emptiness,
    clamp,
    cappedAtLimit,
  } = useKnowledgeSearch({
    workspaceId,
    collectionId,
    query: text,
    ...(limit === undefined ? {} : { limit }),
    ...(debounceMs === undefined ? {} : { debounceMs }),
    ...(searchFn === undefined ? {} : { searchFn }),
  })

  const inc = response?.incompleteness

  return (
    <div data-testid="knowledge-search" className={cn('flex flex-col gap-3', className)}>
      <div className="relative">
        <MagnifyingGlass
          size={14}
          aria-hidden="true"
          className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-muted)]"
        />
        <label htmlFor={inputId} className="sr-only">
          Search notes
        </label>
        <Input
          id={inputId}
          type="search"
          value={text}
          disabled={collectionId === undefined}
          onChange={(e) => setText(e.target.value)}
          placeholder="Search notes"
          aria-label="Search notes"
          className="pl-8"
        />
      </div>

      {error && (
        <LibraryErrorBanner
          message={error.message || 'Search failed.'}
          testId="knowledge-search-error"
        />
      )}

      {/* ── The honesty region ────────────────────────────────────────────
          Rendered ABOVE the results, in the reading flow, announced politely.
          Present whenever the answer is not the whole answer, and absent
          entirely when it is (US-6 AS-4). */}
      {inc && honesty === 'incomplete' && (
        <div
          role="status"
          data-testid="knowledge-search-incomplete"
          className="flex items-start gap-2 rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 px-3 py-2"
        >
          <Warning
            size={14}
            weight="fill"
            aria-hidden="true"
            className="mt-0.5 shrink-0 text-[var(--color-warning)]"
          />
          <div className="flex-1 text-xs leading-snug text-[var(--color-warning)]">
            <p className="font-medium">Partial results — the index is still being built.</p>
            <p data-testid="knowledge-search-incompleteness-statement">{inc.statement}</p>
            <p data-testid="knowledge-search-indexed-ratio">
              {formatCount(inc.indexed_files as number)} of {formatCount(inc.total_files as number)} notes
              searched.
            </p>
          </div>
        </div>
      )}

      {inc && honesty === 'indeterminate' && (
        <div
          role="status"
          data-testid="knowledge-search-indeterminate"
          className="flex items-start gap-2 rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 px-3 py-2"
        >
          <SpinnerGap
            size={14}
            aria-hidden="true"
            className="mt-0.5 shrink-0 animate-spin text-[var(--color-warning)]"
          />
          {/* No ratio, no percentage, no progress bar: the total is not known,
              so there is no denominator to divide by and none is invented. */}
          <div className="flex-1 text-xs leading-snug text-[var(--color-warning)]">
            <p className="font-medium">
              Partial results — still counting this collection, so the total is not known yet.
            </p>
            <p data-testid="knowledge-search-incompleteness-statement">{inc.statement}</p>
            {typeof inc.indexed_files === 'number' && (
              <p data-testid="knowledge-search-indexed-so-far">
                {formatCount(inc.indexed_files)} notes indexed so far.
              </p>
            )}
          </div>
        </div>
      )}

      {clamp && (
        <div
          role="status"
          data-testid="knowledge-search-clamped"
          className="flex items-start gap-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2"
        >
          <Info
            size={14}
            aria-hidden="true"
            className="mt-0.5 shrink-0 text-[var(--color-muted)]"
          />
          <p className="flex-1 text-xs leading-snug text-[var(--color-muted)]">
            {clamp.requested === undefined
              ? `Result count clamped to the server cap of ${formatCount(clamp.applied)}.`
              : `Result count clamped: you asked for ${formatCount(clamp.requested)}, the server cap is ${formatCount(clamp.applied)}.`}
          </p>
        </div>
      )}

      {/* ── The live region ───────────────────────────────────────────────
          "Searching…" and the result count are the two sentences that tell a
          reader the state of their own query, and both used to be plain <p>s.
          A screen-reader user who typed into the box was therefore told the
          answer might be partial (the banners above are role="status") and
          never told that an answer had arrived at all, or how much of one.
          One polite live region covers both, and the count sits inside it so
          each new answer is announced rather than only the first. */}
      <div role="status" aria-live="polite" className="flex flex-col gap-1 empty:hidden">
        {isBusy && (
          <p data-testid="knowledge-search-busy" className="text-xs text-[var(--color-muted)]">
            Searching…
          </p>
        )}

        {response && emptiness === 'has-hits' && (
          <p data-testid="knowledge-search-count" className="text-xs text-[var(--color-muted)]">
            {honesty === 'complete'
              ? `${formatCount(hits.length)} ${hits.length === 1 ? 'result' : 'results'}`
              : `${formatCount(hits.length)} ${hits.length === 1 ? 'result' : 'results'} so far`}
          </p>
        )}

        {/* The count above is a count of what was RETURNED, and the server stops
            appending the moment it has `limit_applied` hits without sending a
            total-match count. When the list is exactly that long it may have
            been cut short, and the qualifier says so in the reading flow rather
            than leaving "20 results" to be read as "there are 20". */}
        {response && cappedAtLimit && (
          <p
            data-testid="knowledge-search-capped"
            className="text-xs leading-snug text-[var(--color-muted)]"
          >
            This is the top {formatCount(response.limit_applied)} by relevance — the most this
            search returns at once. More notes may match than are listed here.
          </p>
        )}
      </div>

      {response && emptiness === 'has-hits' && (
        <ul data-testid="knowledge-search-results" className="flex flex-col gap-1">
            {hits.map((hit) => (
              <li key={hit.path}>
                <button
                  type="button"
                  tabIndex={0}
                  onClick={() => onOpenNote?.(hit.path)}
                  className="flex w-full flex-col items-start gap-0.5 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-[var(--color-surface-2)]"
                >
                  <span className="flex items-center gap-1.5 text-sm text-[var(--color-secondary)]">
                    <FileText size={13} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
                    {hit.title || hit.path}
                  </span>
                  <span className="text-[11px] text-[var(--color-muted)]">{hit.path}</span>
                  {hit.excerpt !== undefined ? (
                    <span className="text-xs leading-snug text-[var(--color-muted)]">{hit.excerpt}</span>
                  ) : hit.excerpt_unavailable !== undefined ? (
                    <span
                      data-testid="knowledge-search-excerpt-unavailable"
                      className="text-xs italic leading-snug text-[var(--color-muted)]"
                    >
                      {EXCERPT_UNAVAILABLE_REASON[hit.excerpt_unavailable]}
                    </span>
                  ) : null}
                </button>
              </li>
          ))}
        </ul>
      )}

      {/* ── Emptiness, told apart ───────────────────────────────────────────
          A complete answer carries a server-authored `statement` too, and it is
          the ONLY thing that distinguishes some empty answers from each other.
          An out-of-scope collection_id, for instance, comes back as hits: [],
          complete: true, statement: "No knowledge base with that identifier is
          available in this workspace." — a search that was never allowed to run.
          Rendering only the client's own "no notes match" over that asserts a
          search happened. The server writes the sentence precisely so the client
          cannot phrase its answer for it, so the sentence is shown. */}
      {response && emptiness === 'collection-empty' && (
        <div role="status" data-testid="knowledge-search-collection-empty" className="text-xs text-[var(--color-muted)]">
          <p>This collection has no notes yet.</p>
          {inc?.statement ? (
            <p data-testid="knowledge-search-complete-statement">{inc.statement}</p>
          ) : null}
        </div>
      )}

      {response && emptiness === 'no-matches' && (
        <div role="status" data-testid="knowledge-search-no-matches" className="text-xs leading-snug text-[var(--color-muted)]">
          <p>No results for “{debouncedQuery}”.</p>
          {inc?.statement ? (
            <p data-testid="knowledge-search-complete-statement">{inc.statement}</p>
          ) : null}
        </div>
      )}

      {response && emptiness === 'still-indexing' && (
        <p
          role="status"
          data-testid="knowledge-search-no-matches-yet"
          className="text-xs text-[var(--color-muted)]"
        >
          No matches so far. The index is still being built, so this is not the whole collection.
        </p>
      )}
    </div>
  )
}
