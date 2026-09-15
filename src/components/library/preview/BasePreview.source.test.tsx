// BasePreview.source.test.tsx — UAT 2026-09-13 D-119 (web half): a HEALTHY
// .base must be openable as text, not only a broken one.
//
// Before this, the raw editor existed solely behind the "no views" empty
// state (`base-preview-view-raw`), so a base whose views all loaded had no
// raw/edit affordance anywhere — the preview header held one button (Close)
// and the API said is_text_editable:false. The backend half now reports the
// file as text (pkg/library/entries.go, `.base` in textExtensions); this half
// draws the "Source" toggle on the tab strip whenever the entry is text-
// editable, and it reuses the SAME edit path every other text file gets
// (LibraryCodePreview → LibraryTextPreview), so a malformed base is repaired
// through exactly the editor a healthy one is edited with.
//
// Dies on: dropping the toggle (test 1), or gating it on something other
// than `is_text_editable` (test 2, the negative control).

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { KnowledgeBaseViews, ViewResult } from '@/lib/api/generated/openapi-types'
import type { LibraryEntry } from '@/lib/api'
import { BasePreview } from './BasePreview'
import { PreviewHeaderSlotProvider } from './previewHeaderSlot'

vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => <pre data-testid="shiki">{children}</pre>,
  createJavaScriptRegexEngine: () => ({}),
}))

const BASE_CONTENT = 'views:\n  - type: table\n    name: Outstanding\n'

function entry(over: Partial<LibraryEntry> = {}): LibraryEntry {
  return {
    name: 'Invoices.base',
    path: 'vault/Invoices.base',
    is_dir: false,
    is_hidden: false,
    size: BASE_CONTENT.length,
    modified_at: '2026-09-01T10:00:00Z',
    is_text_editable: true,
    ...over,
  }
}

function baseViews(): KnowledgeBaseViews {
  return {
    base_path: 'vault/Invoices.base',
    is_knowledge_base: true,
    collection_id: 'kb_1',
    collection_root: 'vault',
    source: 'Invoices.base',
    views: [{ name: 'invoices--outstanding', label: 'Outstanding' }],
    unloadable: [],
    unloadable_count: 0,
  }
}

function result(): ViewResult {
  return {
    view: 'invoices--outstanding',
    label: 'Outstanding',
    parts: [{ part: 'table', source: { part: 'table' }, columns: ['file.name'] }],
    rows: [{ path: 'a.md', title: 'INV-A', cells: [], joins: [] }],
    complete: true,
    problems: [],
  }
}

function renderBase(e: LibraryEntry) {
  const loadContent = vi.fn().mockResolvedValue({ content: BASE_CONTENT, is_text: true, too_large: false })
  // The View/Edit controls portal into the pane's header slot
  // (previewHeaderSlot.tsx); a real slot element stands in for the pane so
  // the test can prove Edit is actually reachable from the Source view.
  const slot = document.createElement('div')
  slot.setAttribute('data-testid', 'header-slot')
  document.body.appendChild(slot)
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <PreviewHeaderSlotProvider slot={slot}>
      <BasePreview
        workspaceId="ws-1"
        entry={e}
        loadContent={loadContent}
        loadBaseViews={vi.fn().mockResolvedValue(baseViews())}
        loadViewResult={vi.fn().mockResolvedValue(result())}
        loadGraph={vi.fn().mockResolvedValue({
          collection_id: 'kb_1',
          kind: 'links',
          nodes: [],
          edges: [],
          skipped: [],
          truncated: false,
        })}
      />
      </PreviewHeaderSlotProvider>
    </QueryClientProvider>,
  )
  return { loadContent }
}

describe('D-119 — a healthy .base can be opened as text', () => {
  it('shows a Source toggle beside the view tabs; it opens the real text editor over the file, and toggles back', async () => {
    const { loadContent } = renderBase(entry())
    await screen.findByTestId('base-view-tab-invoices--outstanding')
    // A healthy base never reads its own bytes until asked to.
    expect(loadContent).not.toHaveBeenCalled()

    const toggle = screen.getByTestId('base-preview-source-toggle')
    fireEvent.click(toggle)

    const raw = await screen.findByTestId('base-preview-raw')
    expect(raw).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('shiki')).toHaveTextContent('name: Outstanding'))
    expect(loadContent).toHaveBeenCalledWith('ws-1', 'vault/Invoices.base')
    // The same view/edit shell every text file gets — Edit is reachable.
    expect(screen.getByTestId('library-preview-mode-edit')).toBeInTheDocument()

    fireEvent.click(screen.getByTestId('base-preview-source-toggle'))
    await waitFor(() => expect(screen.queryByTestId('base-preview-raw')).not.toBeInTheDocument())
    expect(screen.getByTestId('base-view-tab-invoices--outstanding')).toBeInTheDocument()
  })

  it('offers no Source toggle when the listing says the entry is not text-editable', async () => {
    renderBase(entry({ is_text_editable: false }))
    await screen.findByTestId('base-view-tab-invoices--outstanding')
    expect(screen.queryByTestId('base-preview-source-toggle')).not.toBeInTheDocument()
  })
})
