// KnowledgeSearch.attachment.test.tsx — ADR-067 FR-050a(a), FR-039a.
//
// The requirement is an invariant, not a feature: a hit that carries no excerpt
// carries the reason there is none, so a reader is never shown a result with
// nothing under it and no explanation.
//
// An ATTACHMENT is the case that had no reason to give. FR-039a forbids opening
// an attachment's contents for any purpose, so there is nothing to quote — and
// the contract's `excerpt_unavailable` enum had no member for it, so the hit
// arrived at this component with neither field and rendered as a bare title
// over empty space. FR-050a(a)'s amendment added `attachment_not_read`.
//
// Two things are asserted, and the second is the one that matters:
//   1. the reason is rendered at all;
//   2. it does not read as a FAILURE. Nothing went wrong and nothing was even
//      attempted; a sentence like "could not be read" would tell the reader
//      their file is broken when it is perfectly fine.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { KnowledgeSearchResponse as KnowledgeSearchResponseSchema } from '@/lib/api/generated/schemas'
import { KnowledgeSearch } from './KnowledgeSearch'
import type {
  KnowledgeSearchResponse,
  KnowledgeSearchHit,
  KnowledgeSearchFn,
} from './useKnowledgeSearch'

function attachmentResponse(over: Partial<KnowledgeSearchHit> = {}): KnowledgeSearchResponse {
  const base: KnowledgeSearchResponse = {
    collection_id: 'kb_1',
    hits: [
      {
        path: 'img/diagram-v3.png',
        title: 'diagram-v3',
        score: 1,
        kind: 'attachment',
        excerpt_unavailable: 'attachment_not_read',
        ...over,
      },
    ],
    incompleteness: {
      complete: true,
      total_known: true,
      total_files: 12,
      indexed_files: 12,
      statement: 'Searched the whole collection.',
    },
    limit_applied: 20,
    limit_clamped: false,
  }
  // Through the generated zod schema, so this fixture cannot be a payload the
  // server could not send. Before the contract gained the enum member, THIS
  // LINE is what threw.
  return KnowledgeSearchResponseSchema.parse(base) as KnowledgeSearchResponse
}

function renderSearch(res: KnowledgeSearchResponse) {
  const searchFn: KnowledgeSearchFn = vi.fn().mockResolvedValue(res)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <KnowledgeSearch workspaceId="ws-1" collectionId="kb_1" searchFn={searchFn} debounceMs={5} />
    </QueryClientProvider>,
  )
}

describe('KnowledgeSearch — attachment hits (FR-050a a)', () => {
  it('accepts attachment_not_read as a contract-valid reason', () => {
    // Guards the generated schema itself: with the enum member missing this
    // parse throws, which is exactly how the payload used to be dropped at the
    // SPA edge rather than rendered.
    expect(() => attachmentResponse()).not.toThrow()
  })

  it('explains the missing excerpt instead of leaving a bare title', async () => {
    renderSearch(attachmentResponse())
    fireEvent.change(screen.getByLabelText('Search notes'), { target: { value: 'diagram' } })

    await waitFor(() => expect(screen.getByText('diagram-v3')).toBeInTheDocument())
    const reason = screen.getByTestId('knowledge-search-excerpt-unavailable')
    expect(reason.textContent?.trim()).not.toBe('')
  })

  it('does not describe an attachment as a failure', async () => {
    renderSearch(attachmentResponse())
    fireEvent.change(screen.getByLabelText('Search notes'), { target: { value: 'diagram' } })

    await waitFor(() => expect(screen.getByText('diagram-v3')).toBeInTheDocument())
    const text = screen.getByTestId('knowledge-search-excerpt-unavailable').textContent ?? ''

    // Nothing went wrong: the file is fine and was never opened by design.
    expect(text).not.toMatch(/could not be read/i)
    expect(text).not.toMatch(/no longer there/i)
    expect(text).not.toMatch(/ran out/i)
    // And it says what DID happen, in words rather than the enum value.
    expect(text).toMatch(/file name|filename/i)
    expect(text).not.toMatch(/attachment_not_read/)
  })
})
