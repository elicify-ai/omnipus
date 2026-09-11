// KbQueryFenceEmbed.test.tsx — ADR-083 embedded-content spec, Step 6: a
// ```query fence renders REAL search results from the human vault-search
// endpoint (`searchVault`) — see the component's own header for exactly what
// it does and does not attempt (free-text only, no Obsidian field-filter
// syntax). Every negative assertion is paired with a positive one proving
// the real result list rendered, per this task's own pairing discipline.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { KbQueryFenceEmbed, QUERY_FENCE_EMBED_RESERVED_HEIGHT_PX } from './KbQueryFenceEmbed'
import { holdEmbedsOutOfView, scrollIntoView } from '@/test/intersectionObserver'
import type { components } from '@/lib/api/generated/openapi-types'

type VaultSearchResponse = components['schemas']['VaultSearchResponse']

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    searchVault: vi.fn(),
  }
})

import { searchVault } from '@/lib/api'

function emptyResponse(overrides: Partial<VaultSearchResponse> = {}): VaultSearchResponse {
  return {
    collection_id: 'kb_1',
    complete: true,
    notes: [],
    records: [],
    views: [],
    ...overrides,
  }
}

function renderEmbed(query = 'landlock seccomp') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <KbQueryFenceEmbed workspaceId="ws-1" collectionId="kb_1" query={query} />
    </QueryClientProvider>,
  )
}

describe('KbQueryFenceEmbed', () => {
  beforeEach(() => {
    vi.mocked(searchVault).mockReset()
  })

  it('sends the fence body as a free-text query to the real search endpoint', async () => {
    vi.mocked(searchVault).mockResolvedValue(emptyResponse({ notes: [{ path: 'a.md', title: 'A note' }] }))
    renderEmbed('landlock seccomp')

    await waitFor(() =>
      expect(searchVault).toHaveBeenCalledWith(
        'ws-1',
        expect.objectContaining({ query: 'landlock seccomp', collection_id: 'kb_1' }),
      ),
    )
  })

  it('renders real note/record/view hits from the response, each visibly', async () => {
    vi.mocked(searchVault).mockResolvedValue(
      emptyResponse({
        notes: [{ path: 'notes/a.md', title: 'Landlock notes', snippet: 'kernel sandboxing…' }],
        records: [{ path: 'records/r1.md', title: 'Sandbox record', cells: [] }],
        views: [{ view: 'v1', label: 'Sandbox board' }],
      }),
    )
    renderEmbed()

    expect(await screen.findByTestId('kb-query-fence-results')).toBeInTheDocument()
    expect(screen.getByTestId('kb-query-fence-note-hit')).toHaveTextContent('Landlock notes')
    expect(screen.getByTestId('kb-query-fence-note-hit')).toHaveTextContent('kernel sandboxing')
    expect(screen.getByTestId('kb-query-fence-record-hit')).toHaveTextContent('Sandbox record')
    expect(screen.getByTestId('kb-query-fence-view-hit')).toHaveTextContent('Sandbox board')
    // Paired negative: the no-results/loading/error states are all absent.
    expect(screen.queryByTestId('kb-query-fence-no-results')).not.toBeInTheDocument()
    expect(screen.queryByTestId('kb-query-fence-loading')).not.toBeInTheDocument()
    expect(screen.queryByTestId('kb-query-fence-error')).not.toBeInTheDocument()
  })

  it('shows a visible "no results" statement for a genuinely empty answer — never an empty box', async () => {
    vi.mocked(searchVault).mockResolvedValue(emptyResponse())
    renderEmbed('nothing matches this')

    const empty = await screen.findByTestId('kb-query-fence-no-results')
    expect(empty).toHaveTextContent('nothing matches this')
    expect(screen.queryByTestId('kb-query-fence-results')).not.toBeInTheDocument()
  })

  it('shows the loading state before the search resolves', () => {
    vi.mocked(searchVault).mockReturnValue(new Promise(() => {}))
    renderEmbed()

    expect(screen.getByTestId('kb-query-fence-loading')).toBeInTheDocument()
    expect(screen.queryByTestId('kb-query-fence-results')).not.toBeInTheDocument()
  })

  it('shows a visible, named error and a working retry when the search request fails', async () => {
    vi.mocked(searchVault).mockRejectedValue(new Error('network down'))
    renderEmbed()

    const error = await screen.findByTestId('kb-query-fence-error')
    expect(error).toHaveTextContent(/could not run this query/i)

    vi.mocked(searchVault).mockResolvedValueOnce(emptyResponse({ notes: [{ path: 'a.md', title: 'A' }] }))
    error.querySelector('button')?.click()
    await screen.findByTestId('kb-query-fence-results')
  })

  it('honestly reports an incomplete (still-indexing) answer rather than reading it as "no results"', async () => {
    vi.mocked(searchVault).mockResolvedValue(emptyResponse({ complete: false, complete_reason: 'index catching up' }))
    renderEmbed()

    const incomplete = await screen.findByTestId('kb-query-fence-incomplete')
    expect(incomplete).toHaveTextContent(/still indexing/i)
    expect(incomplete).toHaveTextContent('index catching up')
    expect(screen.queryByTestId('kb-query-fence-no-results')).not.toBeInTheDocument()
  })

  it('shows a distinct, honest marker for an empty fence — never sends a blank query', () => {
    renderEmbed('   ')

    expect(screen.getByTestId('kb-query-fence-empty-notation')).toBeInTheDocument()
    expect(searchVault).not.toHaveBeenCalled()
  })
})

// ── Regressions from the silent-failure audit (H6/M11, M3, M2) and the
//    test-coverage review (I13) ─────────────────────────────────────────────

describe('KbQueryFenceEmbed — states that used to render as nothing, or as a lie', () => {
  beforeEach(() => {
    vi.mocked(searchVault).mockReset()
  })

  it('renders ATTACHMENT hits it counts — a fence matching only attachments no longer shows "(2)" above an empty list (H6)', async () => {
    vi.mocked(searchVault).mockResolvedValue(
      emptyResponse({
        attachments: [
          { path: 'assets/spec.pdf', name: 'spec.pdf' },
          { path: 'assets/Q3-report.xlsx', name: 'Q3-report.xlsx' },
        ],
      }),
    )
    renderEmbed('quarterly report')

    const box = await screen.findByTestId('kb-query-fence-results')
    // The count and the list AGREE. `totalHits` included attachments while
    // the <ul> had no branch for them, so this box said "(2)" over zero rows
    // — and, because the count was non-zero, the honest "No results" state
    // was skipped too.
    expect(box).toHaveTextContent('(2)')
    expect(box.querySelectorAll('li')).toHaveLength(2)
    // Both matches are NAMEABLE, which is the whole point.
    expect(box).toHaveTextContent('spec.pdf')
    expect(box).toHaveTextContent('Q3-report.xlsx')
    expect(screen.getAllByTestId('kb-query-fence-attachment-hit')).toHaveLength(2)
  })

  it('counts and renders attachments ALONGSIDE the other kinds, so the header total matches the row count exactly', async () => {
    vi.mocked(searchVault).mockResolvedValue(
      emptyResponse({
        notes: [{ path: 'notes/a.md', title: 'Landlock notes' }],
        records: [{ path: 'records/r1.md', title: 'Sandbox record', cells: [] }],
        views: [{ view: 'v1', label: 'Sandbox board' }],
        attachments: [{ path: 'assets/spec.pdf', name: 'spec.pdf' }],
      }),
    )
    renderEmbed()

    const box = await screen.findByTestId('kb-query-fence-results')
    expect(box).toHaveTextContent('(4)')
    expect(box.querySelectorAll('li')).toHaveLength(4)
  })

  it('renders the incomplete-index notice AND the partial results it already holds (M3)', async () => {
    vi.mocked(searchVault).mockResolvedValue(
      emptyResponse({
        complete: false,
        complete_reason: 'index catching up',
        notes: [{ path: 'notes/a.md', title: 'Landlock notes' }],
        records: [{ path: 'records/r1.md', title: 'Sandbox record', cells: [] }],
      }),
    )
    renderEmbed()

    // The notice is up…
    const incomplete = await screen.findByTestId('kb-query-fence-incomplete')
    expect(incomplete).toHaveTextContent(/still indexing/i)

    // …and the two real hits it already had are NOT thrown away. The early
    // return discarded them, which is strictly less honest than "here is
    // what we have so far, and it may be incomplete".
    expect(screen.getByTestId('kb-query-fence-results')).toBeInTheDocument()
    expect(screen.getByText('Landlock notes')).toBeInTheDocument()
    expect(screen.getByText('Sandbox record')).toBeInTheDocument()
  })

  it('an INCOMPLETE answer with no hits shows only the notice — it never claims "No results", which it cannot know', async () => {
    vi.mocked(searchVault).mockResolvedValue(emptyResponse({ complete: false }))
    renderEmbed('nothing matched yet')

    expect(await screen.findByTestId('kb-query-fence-incomplete')).toBeInTheDocument()
    expect(screen.queryByTestId('kb-query-fence-no-results')).not.toBeInTheDocument()
    expect(screen.queryByTestId('kb-query-fence-results')).not.toBeInTheDocument()
  })

  it('positive control — a COMPLETE empty answer still says "No results", and shows no incomplete notice', async () => {
    vi.mocked(searchVault).mockResolvedValue(emptyResponse())
    renderEmbed('nothing matches this')

    expect(await screen.findByTestId('kb-query-fence-no-results')).toHaveTextContent('nothing matches this')
    expect(screen.queryByTestId('kb-query-fence-incomplete')).not.toBeInTheDocument()
  })

  it('strips [[wikilink]] notation out of a snippet — the same WL-2 defect the Library search bar fixed, same field, same engine (I13)', async () => {
    vi.mocked(searchVault).mockResolvedValue(
      emptyResponse({
        notes: [
          {
            path: 'notes/a.md',
            title: 'Acme Ltd',
            // A raw byte excerpt of frontmatter, exactly as the engine
            // returns it — brackets and all.
            snippet: 'owner: "[[Daniel Piatkowski]]" — renewal in Q3',
          },
        ],
      }),
    )
    renderEmbed('renewal')

    const hit = await screen.findByTestId('kb-query-fence-note-hit')
    expect(hit).toHaveTextContent('Daniel Piatkowski')
    expect(hit.textContent ?? '').not.toContain('[[')
    expect(hit.textContent ?? '').not.toContain(']]')
    // Plain text, never a link: a search excerpt cannot claim a
    // resolved/unresolved verdict.
    expect(hit.querySelector('a')).toBeNull()
  })

  it('renders an alias-bearing wikilink in a snippet as its ALIAS, matching what the note reader would show', async () => {
    vi.mocked(searchVault).mockResolvedValue(
      emptyResponse({
        notes: [{ path: 'notes/a.md', title: 'A', snippet: 'see [[CRM/Acme Ltd|Acme]] for detail' }],
      }),
    )
    renderEmbed('detail')

    const hit = await screen.findByTestId('kb-query-fence-note-hit')
    expect(hit).toHaveTextContent('see Acme for detail')
    expect(hit.textContent ?? '').not.toContain('CRM/Acme Ltd')
  })
})

describe('KbQueryFenceEmbed — a pending-but-not-loading query renders SOMETHING (M2)', () => {
  beforeEach(() => {
    vi.mocked(searchVault).mockReset()
  })

  it('renders a visible waiting box when the query is paused (offline), never an invisible hole in the note', async () => {
    // `networkMode: 'online'` is react-query's default; with the browser
    // offline a pending query is PAUSED: isLoading false, isError false,
    // data undefined. The old `return null` rendered the fence as nothing at
    // all — no box, no border, no text — so a reader saw a gap where a query
    // block used to be with no indication anything was meant to be there.
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false, networkMode: 'online' } },
    })
    vi.mocked(searchVault).mockReturnValue(new Promise(() => {}))
    const onlineSpy = vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(false)
    try {
      render(
        <QueryClientProvider client={qc}>
          <KbQueryFenceEmbed workspaceId="ws-1" collectionId="kb_1" query="landlock" />
        </QueryClientProvider>,
      )
      // Whichever of the two non-empty states it lands in, SOMETHING with a
      // border and words is on screen. The assertion that matters is that
      // the component never returns null for a fence the author wrote.
      await waitFor(() => {
        const shown =
          screen.queryByTestId('kb-query-fence-waiting') ?? screen.queryByTestId('kb-query-fence-loading')
        expect(shown).not.toBeNull()
        expect((shown?.textContent ?? '').trim().length).toBeGreaterThan(0)
      })
    } finally {
      onlineSpy.mockRestore()
    }
  })
})

// ── The lazy-mount budget, actually exercised (EMB-065/066) ─────────────────
//
// THE SHARPEST CASE OF THE FOUR. This component's own header says it is
// "mounted through the same LazyEmbedMount budget every other embed kind uses
// (EMB-065) — a query fence issues a real network request, so it should not
// fire for a fence that is not near the viewport". That request is the entire
// justification for gating, and until now it was the one thing this file
// never checked: jsdom defines no IntersectionObserver, `LazyEmbedMount`
// fails open without one, and so every test above fired the search
// immediately regardless of scroll position. A note with twenty query fences
// would have issued twenty vault searches on first paint and no test here
// would have noticed.

describe('KbQueryFenceEmbed — the lazy-mount budget (EMB-065/066)', () => {
  beforeEach(() => {
    vi.mocked(searchVault).mockReset()
  })

  it('issues NO vault search at all while the fence is out of view', () => {
    holdEmbedsOutOfView()
    vi.mocked(searchVault).mockResolvedValue(emptyResponse({ notes: [{ path: 'a.md', title: 'A note' }] }))
    renderEmbed('landlock seccomp')

    const wrapper = screen.getByTestId('lazy-embed-mount')
    expect(wrapper.getAttribute('data-mounted')).toBe('false')
    // The requirement itself: a real request that did not happen.
    expect(searchVault).not.toHaveBeenCalled()
    // And no results chrome of any kind — not even the loading state.
    expect(screen.queryByTestId('kb-query-fence-loading')).not.toBeInTheDocument()
    expect(screen.queryByTestId('kb-query-fence-results')).not.toBeInTheDocument()
  })

  it("reserves THIS kind's own height while unmounted (EMB-066)", () => {
    holdEmbedsOutOfView()
    vi.mocked(searchVault).mockResolvedValue(emptyResponse())
    renderEmbed('landlock seccomp')

    expect(screen.getByTestId('lazy-embed-mount').style.minHeight).toBe(
      `${QUERY_FENCE_EMBED_RESERVED_HEIGHT_PX}px`,
    )
  })

  it('fires the search exactly once, with the fence body, when the reader scrolls to it', async () => {
    holdEmbedsOutOfView()
    vi.mocked(searchVault).mockResolvedValue(
      emptyResponse({ notes: [{ path: 'notes/a.md', title: 'Landlock notes' }] }),
    )
    renderEmbed('landlock seccomp')

    const wrapper = screen.getByTestId('lazy-embed-mount')
    expect(searchVault).not.toHaveBeenCalled()

    scrollIntoView(wrapper)

    expect(wrapper.getAttribute('data-mounted')).toBe('true')
    // Paired positive: the deferred request is the SAME real request, with
    // the same arguments, that the eager tests above assert — deferred, not
    // dropped or altered.
    await waitFor(() =>
      expect(searchVault).toHaveBeenCalledWith(
        'ws-1',
        expect.objectContaining({ query: 'landlock seccomp', collection_id: 'kb_1' }),
      ),
    )
    expect(searchVault).toHaveBeenCalledTimes(1)
    expect(await screen.findByText('Landlock notes')).toBeInTheDocument()
  })
})
