// KnowledgeSearch.test.tsx — ADR-067 US-5 / US-6 / US-8, FR-035..FR-037.
//
// The subject here is not "does a list render". It is the product requirement
// that this screen cannot present a partial answer as a whole one. Every
// expected value below is derived from the acceptance scenarios in §6 and from
// the contract's own field semantics — not from what the component happens to
// output.
//
// Three assertions are worth naming, because they are the ones that would let a
// regression through if written loosely:
//
//   * The incompleteness statement is asserted to be VISIBLE TEXT inside a
//     role="status" block that PRECEDES the results in DOM order. A `title`
//     attribute, an aria-label or a console warning would satisfy a naive
//     "the text is somewhere" assertion and would fail the requirement.
//
//   * The indeterminate state is asserted NEGATIVELY as well as positively:
//     no role="progressbar", no <progress>, no percent sign, and no "N of M"
//     anywhere in the honesty region. The fixture's own statement is
//     deliberately ratio-free, so any ratio on screen came from the component
//     inventing a denominator — which is the exact failure FR-036 forbids.
//
//   * "The collection is empty" and "nothing matched this query" are asserted
//     as DIFFERENT rendered sentences reached from different responses, because
//     a reader who cannot tell them apart has been told something false about
//     one of the two.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { KnowledgeSearchResponse as KnowledgeSearchResponseSchema } from '@/lib/api/generated/schemas'
import { KnowledgeSearch } from './KnowledgeSearch'
import type {
  KnowledgeSearchResponse,
  KnowledgeSearchHit,
  KnowledgeSearchFn,
} from './useKnowledgeSearch'

function hit(over: Partial<KnowledgeSearchHit> = {}): KnowledgeSearchHit {
  return { path: 'notes/a.md', title: 'A note', score: 1, kind: 'note', ...over }
}

/** Fixtures go through the generated zod schema, so a test can never be built
 *  on a payload the server could not send. */
function response(over: Partial<KnowledgeSearchResponse> = {}): KnowledgeSearchResponse {
  const base: KnowledgeSearchResponse = {
    collection_id: 'kb_1',
    hits: [hit()],
    incompleteness: {
      complete: true,
      total_known: true,
      total_files: 900,
      indexed_files: 900,
      statement: 'Searched the whole collection.',
    },
    limit_applied: 20,
    limit_clamped: false,
    ...over,
  }
  return KnowledgeSearchResponseSchema.parse(base) as KnowledgeSearchResponse
}

function renderSearch(res: KnowledgeSearchResponse | KnowledgeSearchFn, over: { onOpenNote?: (p: string) => void } = {}) {
  const searchFn: KnowledgeSearchFn =
    typeof res === 'function' ? res : vi.fn().mockResolvedValue(res)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const utils = render(
    <QueryClientProvider client={client}>
      <KnowledgeSearch
        workspaceId="ws-1"
        collectionId="kb_1"
        searchFn={searchFn}
        debounceMs={5}
        {...(over.onOpenNote ? { onOpenNote: over.onOpenNote } : {})}
      />
    </QueryClientProvider>,
  )
  return { ...utils, searchFn }
}

function type(text: string) {
  fireEvent.change(screen.getByLabelText('Search notes'), { target: { value: text } })
}

/** True when `a` comes before `b` in document order. */
function precedes(a: Element, b: Element): boolean {
  return (a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0
}

// ─────────────────────────────────────────────────────────────────────────────
// COMPLETE — US-6 AS-4
// ─────────────────────────────────────────────────────────────────────────────

describe('KnowledgeSearch — complete results', () => {
  it('shows the results and says nothing about PARTIAL-ness', async () => {
    renderSearch(
      response({
        hits: [
          hit({ path: 'notes/landlock.md', title: 'Landlock', excerpt: 'seccomp fallback' }),
          hit({ path: 'notes/seccomp.md', title: 'Seccomp' }),
        ],
      }),
    )
    type('landlock')

    await waitFor(() => expect(screen.getByTestId('knowledge-search-results')).toBeInTheDocument())
    expect(within(screen.getByTestId('knowledge-search-results')).getAllByRole('listitem')).toHaveLength(2)
    expect(screen.getByTestId('knowledge-search-count')).toHaveTextContent('2 results')

    // US-6 AS-4: a finished index shows no INCOMPLETENESS banner — never a
    // "Partial results" claim over a complete answer.
    expect(screen.queryByTestId('knowledge-search-incomplete')).toBeNull()
    expect(screen.queryByTestId('knowledge-search-indeterminate')).toBeNull()
    expect(screen.queryByTestId('knowledge-search-clamped')).toBeNull()
  })

  it('states the server\'s own completeness sentence beside a non-empty answer, not only a bare count', async () => {
    // THE DEFECT THIS PINS DOWN. Observed directly: query "ridge" (6 hits)
    // rendered ONLY "6 results" — no statement anywhere — while the same
    // panel's banner explicitly promises "Each set of search results below
    // states its own completeness, which is the answer to trust"
    // (KnowledgeEmptyState's index_status_unknown copy). The empty branches
    // (collection-empty, no-matches) already honoured that promise via
    // `knowledge-search-complete-statement`; has-hits did not.
    //
    // DIES ON: removing the knowledge-search-complete-statement line from the
    // has-hits branch, or gating it on anything other than a complete answer.
    const statement = 'Searched the whole of this knowledge base; its index was complete at query time.'
    renderSearch(
      response({
        hits: [
          hit({ path: 'notes/ridge-1.md', title: 'Ridge one' }),
          hit({ path: 'notes/ridge-2.md', title: 'Ridge two' }),
        ],
        incompleteness: {
          complete: true,
          total_known: true,
          total_files: 900,
          indexed_files: 900,
          statement,
        },
      }),
    )
    type('ridge')

    await waitFor(() => expect(screen.getByTestId('knowledge-search-count')).toHaveTextContent('2 results'))
    expect(screen.getByTestId('knowledge-search-complete-statement')).toHaveTextContent(statement)
  })

  it('does not repeat the statement when it was already said by the incomplete banner', async () => {
    // The incomplete/indeterminate banners already render `inc.statement` in
    // the reading flow, above the results (see the "incomplete results" and
    // "indeterminate" describe blocks below). Has-hits must not print the
    // exact same sentence a second time immediately underneath — that is
    // noise, not honesty.
    renderSearch(
      response({
        hits: [hit({ path: 'notes/landlock.md', title: 'Landlock' })],
        incompleteness: {
          complete: false,
          total_known: true,
          indexed_files: 4120,
          total_files: 12880,
          statement: 'Searched 4,120 of 12,880 notes — indexing is still running.',
        },
      }),
    )
    type('landlock')

    await screen.findByTestId('knowledge-search-incomplete')
    expect(screen.queryByTestId('knowledge-search-complete-statement')).toBeNull()
  })

  it('opens the note a reader clicks, by its path', async () => {
    const onOpenNote = vi.fn()
    renderSearch(response({ hits: [hit({ path: 'notes/landlock.md', title: 'Landlock' })] }), { onOpenNote })
    type('landlock')

    await waitFor(() => expect(screen.getByText('Landlock')).toBeInTheDocument())
    fireEvent.click(screen.getByText('Landlock'))
    expect(onOpenNote).toHaveBeenCalledWith('notes/landlock.md')
  })

  it('states WHY a hit has no excerpt instead of dropping the hit or inventing one', async () => {
    renderSearch(
      response({
        hits: [hit({ path: 'notes/gone.md', title: 'Gone', excerpt_unavailable: 'file_missing' })],
      }),
    )
    type('gone')

    await waitFor(() => expect(screen.getByText('Gone')).toBeInTheDocument())
    expect(screen.getByTestId('knowledge-search-excerpt-unavailable')).toHaveTextContent(
      /no longer there/i,
    )
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// INCOMPLETE — US-6 AS-2, AS-3; FR-035
// ─────────────────────────────────────────────────────────────────────────────

describe('KnowledgeSearch — incomplete results', () => {
  const statement = 'Searched 4,120 of 12,880 notes — indexing is still running.'

  function partial() {
    return response({
      hits: [hit({ path: 'notes/landlock.md', title: 'Landlock' })],
      incompleteness: {
        complete: false,
        total_known: true,
        indexed_files: 4120,
        total_files: 12880,
        statement,
      },
    })
  }

  it('shows the results AND states, in the reading flow, that they are partial', async () => {
    renderSearch(partial())
    type('landlock')

    const banner = await screen.findByTestId('knowledge-search-incomplete')

    // Announced, not decorative.
    expect(banner).toHaveAttribute('role', 'status')
    // The server's own words, rendered as visible text — not a title attribute,
    // not an aria-label, not a console warning.
    expect(within(banner).getByText(statement)).toBeVisible()
    expect(banner).toHaveTextContent(/partial results/i)
    // And where the reader cannot miss it: above the results, not after them.
    expect(precedes(banner, screen.getByTestId('knowledge-search-results'))).toBe(true)
    // The results themselves are still shown (FR-035: partial results ARE returned).
    expect(within(screen.getByTestId('knowledge-search-results')).getAllByRole('listitem')).toHaveLength(1)
  })

  it('shows the ratio of indexed to total, because here the total IS known (US-6 AS-2)', async () => {
    renderSearch(partial())
    type('landlock')

    const ratio = await screen.findByTestId('knowledge-search-indexed-ratio')
    expect(ratio).toHaveTextContent('4,120 of 12,880')
  })

  it('does not present the count as a settled total', async () => {
    renderSearch(partial())
    type('landlock')
    await waitFor(() => expect(screen.getByTestId('knowledge-search-count')).toBeInTheDocument())
    expect(screen.getByTestId('knowledge-search-count')).toHaveTextContent('1 result so far')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// INDETERMINATE — US-6 AS-1; FR-036
// ─────────────────────────────────────────────────────────────────────────────

describe('KnowledgeSearch — indeterminate (total unknown)', () => {
  // Deliberately ratio-free, so any ratio on screen was invented by the UI.
  const statement = 'Indexing is still running; the collection has not been fully counted yet.'

  function stillWalking(over: Record<string, unknown> = {}) {
    return response({
      hits: [hit({ path: 'notes/landlock.md', title: 'Landlock' })],
      incompleteness: {
        complete: false,
        total_known: false,
        indexed_files: 4120,
        statement,
        ...over,
      },
    })
  }

  it('says the index is still building and gives the count found so far', async () => {
    renderSearch(stillWalking())
    type('landlock')

    const banner = await screen.findByTestId('knowledge-search-indeterminate')
    expect(banner).toHaveAttribute('role', 'status')
    expect(within(banner).getByText(statement)).toBeVisible()
    expect(banner).toHaveTextContent(/total is not known yet/i)
    expect(screen.getByTestId('knowledge-search-indexed-so-far')).toHaveTextContent('4,120 notes indexed so far')
    expect(precedes(banner, screen.getByTestId('knowledge-search-results'))).toBe(true)
  })

  it('renders NO ratio, NO percentage and NO progress bar — there is no denominator', async () => {
    renderSearch(stillWalking())
    type('landlock')

    const banner = await screen.findByTestId('knowledge-search-indeterminate')
    expect(screen.queryByRole('progressbar')).toBeNull()
    expect(banner.querySelector('progress')).toBeNull()
    expect(banner.textContent ?? '').not.toMatch(/%/)
    // "4,120 of 12,880", "4120/12880" — any two numbers joined by a denominator
    // word or slash. The fixture statement contains none, so a match here is
    // the component supplying one.
    expect(banner.textContent ?? '').not.toMatch(/[\d,]+\s*(?:of|\/)\s*[\d,]+/i)
    // And no ARIA-only ratio smuggled onto the banner either.
    expect(banner).not.toHaveAttribute('aria-valuenow')
  })

  it('treats a claimed-known total with no NUMBER as indeterminate, not as a ratio', async () => {
    // The contract permits total_known=true with total_files absent. Trusting
    // the flag alone would render "4,120 of undefined notes".
    renderSearch(stillWalking({ total_known: true }))
    type('landlock')

    await screen.findByTestId('knowledge-search-indeterminate')
    expect(screen.queryByTestId('knowledge-search-incomplete')).toBeNull()
    expect(screen.queryByTestId('knowledge-search-indexed-ratio')).toBeNull()
    expect(screen.getByTestId('knowledge-search-indeterminate').textContent ?? '').not.toMatch(
      /undefined|NaN/,
    )
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// CLAMPING — US-8 AS-3; FR-037
// ─────────────────────────────────────────────────────────────────────────────

describe('KnowledgeSearch — clamped result counts', () => {
  it('says the count was clamped, and names both numbers', async () => {
    renderSearch(
      response({
        hits: [hit()],
        limit_applied: 100,
        limit_clamped: true,
        limit_requested: 400,
      }),
    )
    type('landlock')

    const notice = await screen.findByTestId('knowledge-search-clamped')
    expect(notice).toHaveTextContent(/clamped/i)
    expect(notice).toHaveTextContent('400')
    expect(notice).toHaveTextContent('100')
  })

  it('still says it was clamped when the server omitted the requested number', async () => {
    renderSearch(response({ hits: [hit()], limit_applied: 100, limit_clamped: true }))
    type('landlock')

    const notice = await screen.findByTestId('knowledge-search-clamped')
    expect(notice).toHaveTextContent(/clamped/i)
    expect(notice.textContent ?? '').not.toMatch(/undefined|NaN/)
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// A COUNT OF A TRUNCATED LIST — US-6 P0, FR-037's principle
// ─────────────────────────────────────────────────────────────────────────────

describe('KnowledgeSearch — the count never overstates what was returned', () => {
  it('says the list is the top N when it is exactly as long as the server\'s cap', async () => {
    // THE DEFECT. The gateway stops appending hits at `limit_applied`
    // (`if len(resp.Hits) >= applied { break }`) and sends NO total-match
    // count, and the SPA asks for 20 against a cap of 100 — so `limit_clamped`
    // is false, no banner fires, and a query matching five hundred notes
    // renders a bare "20 results" over `complete: true`. That is a confidently
    // incomplete answer produced by the one surface an operator can click.
    //
    // DIES ON: deleting the `knowledge-search-capped` block, or computing it
    // from `limit_clamped` (which is false here) instead of from the length.
    const hits = Array.from({ length: 20 }, (_, i) => hit({ path: `notes/n${i}.md`, title: `N${i}` }))
    renderSearch(response({ hits, limit_applied: 20, limit_clamped: false }))
    type('landlock')

    const capped = await screen.findByTestId('knowledge-search-capped')
    expect(capped.textContent ?? '').toMatch(/top 20/i)
    expect(capped.textContent ?? '').toMatch(/more notes may match/i)
  })

  it('says nothing of the sort when the list is shorter than the cap', async () => {
    // Short of the cap, the server ran out of matches rather than out of room,
    // so "there may be more" would be an invented doubt — the same class of
    // error as an invented certainty.
    renderSearch(response({ hits: [hit(), hit({ path: 'notes/b.md' })], limit_applied: 20 }))
    type('landlock')

    await waitFor(() => expect(screen.getByTestId('knowledge-search-count')).toHaveTextContent('2 results'))
    expect(screen.queryByTestId('knowledge-search-capped')).toBeNull()
  })
})

describe('KnowledgeSearch — results are announced, not just rendered', () => {
  it('puts the busy state and the count inside a polite live region', async () => {
    // A screen-reader user typing in the box was told the answer MIGHT be
    // partial (the honesty banners are role="status") and was never told that
    // an answer had arrived, or how much of one.
    //
    // DIES ON: removing role="status"/aria-live from the wrapper around the
    // busy line and the count.
    renderSearch(response({ hits: [hit(), hit({ path: 'notes/b.md' })] }))
    type('landlock')

    const count = await screen.findByTestId('knowledge-search-count')
    const live = count.closest('[aria-live]')
    expect(live).not.toBeNull()
    expect(live?.getAttribute('role')).toBe('status')
    expect(live?.getAttribute('aria-live')).toBe('polite')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// EMPTINESS, TOLD APART — edge case E-1; US-6 AS-5
// ─────────────────────────────────────────────────────────────────────────────

describe('KnowledgeSearch — empty results', () => {
  it('renders the SERVER\'s own sentence on a complete empty answer, not only its own', async () => {
    // THE DEFECT THIS PINS DOWN. The gateway returns an out-of-scope
    // collection_id as hits: [], complete: true, total_known: true, with the
    // statement "No knowledge base with that identifier is available in this
    // workspace." — a search that was never allowed to run at all. The client
    // used to drop that sentence (it rendered `statement` only inside the two
    // banners gated on an INCOMPLETE answer) and print "No notes match “x”." in
    // its place, which asserts a search happened over a collection it was never
    // permitted to see. The server writes the sentence precisely so the client
    // cannot phrase the answer for it.
    //
    // DIES ON: removing the `knowledge-search-complete-statement` line.
    renderSearch(
      response({
        hits: [],
        collection_id: 'kb_not_mine',
        incompleteness: {
          complete: true,
          total_known: true,
          statement: 'No knowledge base with that identifier is available in this workspace.',
        },
      }),
    )
    type('landlock')

    const empty = await screen.findByTestId('knowledge-search-no-matches')
    expect(within(empty).getByTestId('knowledge-search-complete-statement')).toHaveTextContent(
      'No knowledge base with that identifier is available in this workspace.',
    )
    // And it does not claim the collection is empty, which it has no evidence of.
    expect(screen.queryByTestId('knowledge-search-collection-empty')).toBeNull()
  })

  it('does not claim "no notes match" as a bare fact when the server said something else', async () => {
    // The client's own sentence is allowed — it is true either way — but it may
    // not be the ONLY thing on screen when the server qualified the answer.
    renderSearch(
      response({
        hits: [],
        incompleteness: {
          complete: true,
          total_known: true,
          statement: 'This knowledge base has not been indexed yet, so these results cover none of it.',
        },
      }),
    )
    type('landlock')

    const empty = await screen.findByTestId('knowledge-search-no-matches')
    expect(empty.textContent ?? '').toContain('has not been indexed yet')
  })

  it('says the COLLECTION is empty when the whole collection holds nothing', async () => {
    // ⚠ READ THIS FIXTURE FOR WHAT IT IS. `total_files: 0` on a COMPLETE answer
    // is contract-legal (the field is documented as present only when
    // total_known is true — a precondition, not a promise) but the gateway
    // never sends it: pkg/gateway/rest_knowledge.go assigns TotalFiles in one
    // place only, inside the `!Complete && Total > 0` arm. So this asserts the
    // CLASSIFIER's rule, and nothing about a payload today's server produces.
    // The reader learns a collection is empty from the indexer's own
    // idle-with-zero-files frame instead — see KnowledgePanel's
    // `empty_collection` state and its test.
    renderSearch(
      response({
        hits: [],
        incompleteness: {
          complete: true,
          total_known: true,
          total_files: 0,
          indexed_files: 0,
          statement: 'This collection has no files.',
        },
      }),
    )
    type('landlock')

    await waitFor(() =>
      expect(screen.getByTestId('knowledge-search-collection-empty')).toBeInTheDocument(),
    )
    expect(screen.queryByTestId('knowledge-search-no-matches')).toBeNull()
  })

  it('says THIS QUERY matched nothing when the collection is not empty', async () => {
    renderSearch(
      response({
        hits: [],
        incompleteness: {
          complete: true,
          total_known: true,
          total_files: 900,
          indexed_files: 900,
          statement: 'Searched the whole collection.',
        },
      }),
    )
    type('landlock')

    const empty = await screen.findByTestId('knowledge-search-no-matches')
    expect(empty).toHaveTextContent('landlock')
    expect(screen.queryByTestId('knowledge-search-collection-empty')).toBeNull()
  })

  it('claims neither while the index is still building (US-6 AS-5)', async () => {
    renderSearch(
      response({
        hits: [],
        incompleteness: {
          complete: false,
          total_known: false,
          indexed_files: 0,
          statement: 'Counting files…',
        },
      }),
    )
    type('landlock')

    await waitFor(() =>
      expect(screen.getByTestId('knowledge-search-no-matches-yet')).toBeInTheDocument(),
    )
    expect(screen.getByTestId('knowledge-search-no-matches-yet')).toHaveTextContent(
      /still being built/i,
    )
    // The indexing state outranks the empty-collection first run.
    expect(screen.queryByTestId('knowledge-search-collection-empty')).toBeNull()
    expect(screen.queryByTestId('knowledge-search-no-matches')).toBeNull()
    expect(screen.getByTestId('knowledge-search-indeterminate')).toBeInTheDocument()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Failure and disabled states
// ─────────────────────────────────────────────────────────────────────────────

describe('KnowledgeSearch — failure', () => {
  it('surfaces a failed search as a visible error, never as an empty result list', async () => {
    const searchFn: KnowledgeSearchFn = vi.fn().mockRejectedValue(new Error('Search failed (HTTP 500).'))
    renderSearch(searchFn)
    type('landlock')

    const banner = await screen.findByTestId('knowledge-search-error')
    expect(banner).toHaveTextContent('Search failed (HTTP 500).')
    // Critically: NOT reported as "no notes match", which would be a false
    // statement about the collection.
    expect(screen.queryByTestId('knowledge-search-no-matches')).toBeNull()
    expect(screen.queryByTestId('knowledge-search-collection-empty')).toBeNull()
  })
})
