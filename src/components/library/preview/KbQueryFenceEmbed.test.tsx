// KbQueryFenceEmbed.test.tsx — ADR-083 embedded-content spec, Step 6: a
// ```query fence renders REAL search results from the human vault-search
// endpoint (`searchVault`) — see the component's own header for exactly what
// it does and does not attempt (free-text only, no Obsidian field-filter
// syntax). Every negative assertion is paired with a positive one proving
// the real result list rendered, per this task's own pairing discipline.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { KbQueryFenceEmbed } from './KbQueryFenceEmbed'
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
