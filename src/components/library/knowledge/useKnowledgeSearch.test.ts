// useKnowledgeSearch.test.ts — ADR-067 US-5 / US-6 / US-8, FR-035..FR-037.
//
// Two halves:
//
//   1. The pure classifiers. Every expected value below is derived from the
//      CONTRACT (contracts/components/schemas/KnowledgeSearchIncompleteness.yaml
//      and KnowledgeSearchResponse.yaml) and from the acceptance scenarios in
//      §6, not from reading what the functions happen to return. The interesting
//      input is the one the contract permits but nobody thinks about:
//      total_known=true with total_files ABSENT — "present only when total_known
//      is true" is not "present whenever total_known is true", so a client that
//      trusts the flag alone renders a ratio with an undefined denominator.
//
//   2. The hook's timing behaviour: debounce, and the guarantee that a slow
//      answer to an earlier query cannot overwrite a later query's results.
//
// Response fixtures are validated against the GENERATED zod schema before use,
// so a fixture cannot drift into a shape the server could never send — a test
// built on an impossible payload proves nothing.

import { describe, it, expect, vi } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { createElement, type ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { KnowledgeSearchResponse as KnowledgeSearchResponseSchema } from '@/lib/api/generated/schemas'
import {
  useKnowledgeSearch,
  classifyHonesty,
  classifyEmptiness,
  clampOf,
  isCappedAtLimit,
  KNOWLEDGE_SEARCH_DEBOUNCE_MS,
  type KnowledgeSearchResponse,
  type KnowledgeSearchIncompleteness,
  type KnowledgeSearchHit,
} from './useKnowledgeSearch'

function hit(over: Partial<KnowledgeSearchHit> = {}): KnowledgeSearchHit {
  return { path: 'notes/a.md', title: 'A', score: 1, kind: 'note', ...over }
}

/** Builds a response and PARSES it through the generated schema, so the fixture
 *  is provably a shape the contract allows. */
function response(over: Partial<KnowledgeSearchResponse> = {}): KnowledgeSearchResponse {
  const base: KnowledgeSearchResponse = {
    collection_id: 'kb_1',
    hits: [hit()],
    incompleteness: { complete: true, total_known: true, statement: 'Searched the whole collection.' },
    limit_applied: 20,
    limit_clamped: false,
    ...over,
  }
  return KnowledgeSearchResponseSchema.parse(base) as KnowledgeSearchResponse
}

function incompleteness(over: Partial<KnowledgeSearchIncompleteness>): KnowledgeSearchIncompleteness {
  return { complete: false, total_known: false, statement: 'Still working.', ...over }
}

function wrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children)
}

// ─────────────────────────────────────────────────────────────────────────────
// classifyHonesty — US-6 AS-1, AS-2, AS-4; FR-035, FR-036
// ─────────────────────────────────────────────────────────────────────────────

describe('classifyHonesty', () => {
  it('is complete when the index covered the whole collection (US-6 AS-4)', () => {
    expect(
      classifyHonesty(incompleteness({ complete: true, total_known: true, total_files: 900, indexed_files: 900 })),
    ).toBe('complete')
  })

  it('is incomplete when partial WITH both numbers, so a ratio may be shown (US-6 AS-2)', () => {
    expect(
      classifyHonesty(
        incompleteness({ complete: false, total_known: true, indexed_files: 4120, total_files: 12880 }),
      ),
    ).toBe('incomplete')
  })

  it('is indeterminate while the tree is still being walked (US-6 AS-1, FR-036)', () => {
    expect(
      classifyHonesty(incompleteness({ complete: false, total_known: false, indexed_files: 4120 })),
    ).toBe('indeterminate')
  })

  it('is indeterminate when the total is claimed known but the NUMBER is absent', () => {
    // The contract permits this: total_files is "present only when total_known
    // is true", which does not promise presence. Without both numbers there is
    // no denominator, and FR-036 forbids inventing one.
    expect(
      classifyHonesty(incompleteness({ complete: false, total_known: true, indexed_files: 4120 })),
    ).toBe('indeterminate')
  })

  it('is indeterminate when the total is known but the indexed count is absent', () => {
    expect(
      classifyHonesty(incompleteness({ complete: false, total_known: true, total_files: 12880 })),
    ).toBe('indeterminate')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// classifyEmptiness — edge case E-1, US-6 AS-5
// ─────────────────────────────────────────────────────────────────────────────

describe('classifyEmptiness', () => {
  it('reports has-hits whenever anything matched', () => {
    expect(classifyEmptiness(response())).toBe('has-hits')
  })

  // ⚠ THE FIXTURE BELOW IS CONTRACT-LEGAL AND THE GATEWAY NEVER SENDS IT.
  // `total_files` is assigned in exactly one place in
  // pkg/gateway/rest_knowledge.go — inside the `!Complete && Total > 0` arm —
  // so a COMPLETE answer never carries it and this branch is unreachable
  // against that server today. The rule is still worth pinning: inferring
  // emptiness from the ABSENCE of a total would be a fabricated claim, and the
  // next thing to notice is that the reader learns a collection is empty from
  // the indexer's idle-with-zero-files frame instead (KnowledgePanel's
  // `empty_collection` state), not from this classifier.
  it('reports collection-empty only on proof: complete, total known, total zero (E-1)', () => {
    expect(
      classifyEmptiness(
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
      ),
    ).toBe('collection-empty')
  })

  it('reports no-matches for a complete search of a non-empty collection', () => {
    expect(
      classifyEmptiness(
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
      ),
    ).toBe('no-matches')
  })

  it('does NOT claim an empty collection when the total is unavailable', () => {
    // Complete, but no total was sent. "The collection is empty" would be an
    // unearned claim; "nothing matched" is true either way.
    expect(
      classifyEmptiness(
        response({
          hits: [],
          incompleteness: { complete: true, total_known: false, statement: 'Searched everything indexed.' },
        }),
      ),
    ).toBe('no-matches')
  })

  it('reports still-indexing rather than empty while the index is partial (US-6 AS-5)', () => {
    expect(
      classifyEmptiness(
        response({
          hits: [],
          incompleteness: {
            complete: false,
            total_known: true,
            indexed_files: 10,
            total_files: 12880,
            statement: 'Searched 10 of 12,880 notes.',
          },
        }),
      ),
    ).toBe('still-indexing')
  })

  it('reports still-indexing even when the total is zero so far, never collection-empty', () => {
    // A brand new knowledge base whose walk has not yet found anything. The
    // indexing state outranks the empty-collection first run (US-6 AS-5).
    expect(
      classifyEmptiness(
        response({
          hits: [],
          incompleteness: {
            complete: false,
            total_known: false,
            indexed_files: 0,
            statement: 'Counting files…',
          },
        }),
      ),
    ).toBe('still-indexing')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// clampOf — US-8 AS-3, FR-037
// ─────────────────────────────────────────────────────────────────────────────

describe('isCappedAtLimit', () => {
  // Derived from the endpoint's behaviour, not from the function: the gateway
  // stops appending hits once it holds `limit_applied` of them and sends no
  // total-match count, so "exactly as many hits as the cap" is the ONLY signal
  // that the list may have been cut short. `limit_clamped` answers a different
  // question — "you asked for more than the maximum" — and is false for every
  // request this SPA makes.
  it('is true when the hit count reaches the applied cap', () => {
    const hits = Array.from({ length: 3 }, (_, i) => hit({ path: `n${i}.md` }))
    expect(isCappedAtLimit(response({ hits, limit_applied: 3 }))).toBe(true)
  })

  it('is false when the list is shorter than the cap — the server ran out of matches', () => {
    expect(isCappedAtLimit(response({ hits: [hit()], limit_applied: 3 }))).toBe(false)
  })

  it('is false for an empty answer — there is nothing to have truncated', () => {
    expect(isCappedAtLimit(response({ hits: [], limit_applied: 20 }))).toBe(false)
  })

  it('does not key off limit_clamped, which is false on every capped answer the SPA gets', () => {
    const hits = Array.from({ length: 20 }, (_, i) => hit({ path: `n${i}.md` }))
    const res = response({ hits, limit_applied: 20, limit_clamped: false })
    expect(clampOf(res)).toBeNull()
    expect(isCappedAtLimit(res)).toBe(true)
  })
})

describe('clampOf', () => {
  it('returns null when nothing was clamped', () => {
    expect(clampOf(response({ limit_clamped: false, limit_applied: 20 }))).toBeNull()
  })

  it('reports both the cap and the refused number (US-8 AS-3)', () => {
    expect(
      clampOf(response({ limit_clamped: true, limit_applied: 100, limit_requested: 400 })),
    ).toEqual({ applied: 100, requested: 400 })
  })

  it('still reports the clamp when the server omitted the requested number', () => {
    expect(clampOf(response({ limit_clamped: true, limit_applied: 100 }))).toEqual({ applied: 100 })
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// The hook: debounce and ordering
// ─────────────────────────────────────────────────────────────────────────────

describe('useKnowledgeSearch', () => {
  // REAL timers on purpose. Fake timers and @testing-library's waitFor do not
  // cooperate here (waitFor polls on a timer that a frozen clock never fires,
  // and `shouldAdvanceTime` re-couples the fake clock to real elapsed time,
  // which makes a "249 ms have passed" assertion depend on how long React took
  // to render). The debounce is therefore asserted by its OBSERVABLE
  // consequence — five keystrokes produce ONE request, for the final term —
  // which is both the actual requirement and immune to clock games.
  const settle = (ms: number) => act(async () => { await new Promise((r) => setTimeout(r, ms)) })

  it('collapses a burst of keystrokes into ONE request, for the final term', async () => {
    const searchFn = vi.fn().mockResolvedValue(response())
    const { rerender } = renderHook(
      ({ q }: { q: string }) =>
        useKnowledgeSearch({ workspaceId: 'ws-1', collectionId: 'kb_1', query: q, searchFn }),
      { wrapper: wrapper(), initialProps: { q: '' } },
    )

    rerender({ q: 'l' })
    rerender({ q: 'la' })
    rerender({ q: 'lan' })
    rerender({ q: 'land' })
    rerender({ q: 'landl' })

    await waitFor(() => expect(searchFn).toHaveBeenCalled())
    await settle(KNOWLEDGE_SEARCH_DEBOUNCE_MS * 2)

    // Undebounced, this would be ['l', 'la', 'lan', 'land', 'landl'].
    expect(searchFn.mock.calls.map((c) => (c[1] as { query: string }).query)).toEqual(['landl'])
    expect(searchFn.mock.calls[0]?.[1]).toMatchObject({ collection_id: 'kb_1', limit: 20, offset: 0 })
  })

  it('issues no request at all for a blank or whitespace-only query', async () => {
    const searchFn = vi.fn().mockResolvedValue(response())
    renderHook(
      () => useKnowledgeSearch({ workspaceId: 'ws-1', collectionId: 'kb_1', query: '   ', searchFn }),
      { wrapper: wrapper() },
    )
    await settle(KNOWLEDGE_SEARCH_DEBOUNCE_MS * 3)
    expect(searchFn).not.toHaveBeenCalled()
  })

  it('issues no request without a collection id', async () => {
    const searchFn = vi.fn().mockResolvedValue(response())
    renderHook(
      () => useKnowledgeSearch({ workspaceId: 'ws-1', query: 'landlock', searchFn }),
      { wrapper: wrapper() },
    )
    await settle(KNOWLEDGE_SEARCH_DEBOUNCE_MS * 3)
    expect(searchFn).not.toHaveBeenCalled()
  })

  it('a slow answer to an EARLIER query never overwrites the results of a later one', async () => {
    // The failure this guards: the reader types "land", then "landlock". The
    // "land" request is slow and resolves LAST. Without per-query isolation the
    // results on screen would silently revert to the earlier query's answer
    // while the box reads "landlock" — a wrong answer that looks entirely
    // normal, which is the class of defect this whole feature exists to refuse.
    const resolvers: Record<string, (r: KnowledgeSearchResponse) => void> = {}
    const searchFn = vi.fn(
      (_ws: string, body: { query: string }) =>
        new Promise<KnowledgeSearchResponse>((resolve) => {
          resolvers[body.query] = resolve
        }),
    )

    const { result, rerender } = renderHook(
      ({ q }: { q: string }) =>
        useKnowledgeSearch({ workspaceId: 'ws-1', collectionId: 'kb_1', query: q, searchFn }),
      { wrapper: wrapper(), initialProps: { q: '' } },
    )

    rerender({ q: 'land' })
    await waitFor(() => expect(resolvers['land']).toBeDefined())

    rerender({ q: 'landlock' })
    await waitFor(() => expect(resolvers['landlock']).toBeDefined())

    // The LATER query answers first…
    await act(async () => {
      resolvers['landlock']?.(response({ hits: [hit({ path: 'notes/landlock.md', title: 'Landlock' })] }))
      await Promise.resolve()
    })
    await waitFor(() => expect(result.current.hits).toHaveLength(1))
    expect(result.current.hits[0]?.path).toBe('notes/landlock.md')

    // …and the earlier, slower one answers second. It must change nothing.
    await act(async () => {
      resolvers['land']?.(
        response({
          hits: [
            hit({ path: 'notes/land-a.md', title: 'Land A' }),
            hit({ path: 'notes/land-b.md', title: 'Land B' }),
          ],
        }),
      )
      await Promise.resolve()
    })
    await settle(60)

    expect(result.current.debouncedQuery).toBe('landlock')
    expect(result.current.hits.map((h) => h.path)).toEqual(['notes/landlock.md'])
  })

  it('surfaces the classified honesty and clamp alongside the hits', async () => {
    const searchFn = vi.fn().mockResolvedValue(
      response({
        hits: [hit()],
        incompleteness: {
          complete: false,
          total_known: true,
          indexed_files: 4120,
          total_files: 12880,
          statement: 'Searched 4,120 of 12,880 notes — indexing is still running.',
        },
        limit_applied: 100,
        limit_clamped: true,
        limit_requested: 400,
      }),
    )
    const { result } = renderHook(
      () => useKnowledgeSearch({ workspaceId: 'ws-1', collectionId: 'kb_1', query: 'x', searchFn }),
      { wrapper: wrapper() },
    )
    await waitFor(() => expect(result.current.response).toBeDefined())
    expect(result.current.honesty).toBe('incomplete')
    expect(result.current.emptiness).toBe('has-hits')
    expect(result.current.clamp).toEqual({ applied: 100, requested: 400 })
  })
})
