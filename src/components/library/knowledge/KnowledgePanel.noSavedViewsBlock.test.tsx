// RED test for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan row 73
// (new, FR-VA-030 — founder-direction amendment 2026-09-29, folding #1013 in).
//
// Oracle: FR-VA-030, verbatim — "The Library MUST NOT render a separate top
// 'Saved views' block (today KnowledgeViewsList.tsx::KnowledgeViewsList
// inside KnowledgePanel.tsx::KnowledgePanel) and MUST NOT open a view in a
// modal (KnowledgeViewDialog). Views appear ONLY as inline entries in the
// Library tree (FR-VA-001, per-kind icon) and open in the preview pane
// (FR-VA-009 case 'view'). Agents are unaffected: knowledge_describe/
// knowledge_find keep listing views."
//
// MOCK BOUNDARY: only the network is mocked (loadInfo/loadViews, the same
// injected-fetch seam KnowledgePanel.test.tsx's own "collection Views list"
// suite already uses for this exact component) — KnowledgePanel and
// KnowledgeViewsList are both real, so a break in either fails this test.
// This mounts the real production wiring LibraryExplorer uses
// (LibraryExplorer -> KnowledgePanel, unchanged by this spec) rather than
// LibraryExplorer's own much larger fetch-mocking harness, because the
// assertion below is entirely about KnowledgePanel's own composition, which
// LibraryExplorer does not alter.
//
// Today this is a real, presently-passing-the-OLD-way integration: with a
// collection that has saved views, KnowledgePanel.test.tsx's own
// "renders the Views list inside a detected knowledge base with a
// collection id" test asserts `knowledge-views-list` IS present and the
// view's label IS shown — confirming the block this test says must be GONE
// is still mounted and rendering today.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	npx vitest run src/components/library/knowledge/KnowledgePanel.noSavedViewsBlock.test.tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import type { KnowledgeBaseInfo } from '@/lib/api/generated/openapi-types'
import { KnowledgePanel } from './KnowledgePanel'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

const COLLECTION_ID = 'kb_fr_va_030'

function makeInfo(over: Partial<KnowledgeBaseInfo> = {}): KnowledgeBaseInfo {
  return {
    workspace_id: 'ws_va030',
    root_path: 'notes/vault',
    is_knowledge_base: true,
    marker: 'omnipus_vault',
    collection_id: COLLECTION_ID,
    display_name: 'Research vault',
    ...over,
  }
}

describe('KnowledgePanel — no top "Saved views" block or dialog (FR-VA-030, TDD row 73)', () => {
  it('renders no "Saved views" block for a collection that has saved views', async () => {
    const loadInfo = vi.fn().mockResolvedValue(makeInfo())
    // Today's loadViews prop is exactly what makes KnowledgeViewsList mount
    // and show a view label (see KnowledgePanel.test.tsx's UAT D-13 suite) —
    // feeding it a real view here proves this test is not vacuously true
    // (there IS something a "Saved views" block would have shown).
    const loadViews = vi.fn().mockResolvedValue({
      collection_id: COLLECTION_ID,
      views: [{ name: 'authored--active', label: 'Active invoices' }],
      unloadable_count: 0,
    })

    render(
      <QueryClientProvider client={makeClient()}>
        <KnowledgePanel
          workspaceId="ws_va030"
          path="notes/vault"
          loadInfo={loadInfo}
          loadViews={loadViews}
        />
      </QueryClientProvider>,
    )

    // Wait for the COMPOSED surface to actually render (not merely for
    // loadInfo to have been called) — surfaceEnabled's branch is what would
    // mount KnowledgeViewsList, so the assertion below must run against a
    // settled render, never a still-loading one that would pass vacuously.
    await screen.findByTestId('knowledge-panel-surface')
    await waitFor(() => expect(loadViews).toHaveBeenCalled())

    expect(screen.queryByTestId('knowledge-views-list')).not.toBeInTheDocument()
    expect(screen.queryByText('Saved views')).not.toBeInTheDocument()
    expect(screen.queryByTestId('knowledge-views-dialog')).not.toBeInTheDocument()
    expect(screen.queryByTestId('knowledge-views-item')).not.toBeInTheDocument()
  })
})
