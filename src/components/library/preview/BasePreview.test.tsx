// BasePreview.test.tsx — the .base surface: tabs from the file's own view
// declarations (first selected), the collection resolved by the ancestor
// walk, each view's result fetched by its imported slug, and every non-happy
// state a stated answer (view-kinds-design-2026-09-03 §7).
//
// The fetch boundary is the injected loaders (the KnowledgeNoteView test-seam
// convention); no module mock, no network.
//
// react-shiki is mocked at its own module boundary, the same convention
// LibraryPreviewPane.test.tsx and KnowledgeNoteView.test.tsx already use —
// this file is about OUR wiring (the raw-view escape hatch mounts the real
// edit path and shows the real content), not about re-verifying Shiki's own
// syntax highlighting.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider, focusManager } from '@tanstack/react-query'
import type { KnowledgeBaseInfo, ViewResult } from '@/lib/api/generated/openapi-types'
import type { LibraryEntry } from '@/lib/api'
import { BasePreview } from './BasePreview'

vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => <pre data-testid="shiki">{children}</pre>,
}))

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

const BASE_CONTENT = `views:
  - type: table
    name: Outstanding
    filters:
      and:
        - 'status != "paid"'
  - type: table
    name: Aged
`

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

function info(over: Partial<KnowledgeBaseInfo> = {}): KnowledgeBaseInfo {
  return {
    workspace_id: 'ws-1',
    root_path: 'vault',
    is_knowledge_base: true,
    marker: 'omnipus_vault',
    collection_id: 'kb_1',
    ...over,
  }
}

function result(over: Partial<ViewResult> = {}): ViewResult {
  return {
    view: 'invoices--outstanding',
    label: 'Outstanding',
    parts: [{ part: 'table', source: { part: 'table' }, columns: ['file.name'] }],
    rows: [{ path: 'a.md', title: 'INV-A', cells: [], joins: [] }],
    complete: true,
    problems: [],
    ...over,
  }
}

interface Loaders {
  loadContent?: (ws: string, path: string) => Promise<{ content?: string; is_text: boolean; too_large: boolean }>
  loadInfo?: (ws: string, path: string) => Promise<KnowledgeBaseInfo>
  loadViewResult?: (ws: string, collectionId: string, view: string) => Promise<ViewResult>
}

function renderBase(loaders: Loaders = {}, e = entry()) {
  const loadContent =
    loaders.loadContent ?? vi.fn().mockResolvedValue({ content: BASE_CONTENT, is_text: true, too_large: false })
  const loadInfo =
    loaders.loadInfo ??
    vi.fn((_ws: string, path: string) =>
      path === 'vault' ? Promise.resolve(info()) : Promise.resolve(info({ is_knowledge_base: false, marker: 'none', collection_id: undefined, root_path: path })),
    )
  const loadViewResult = loaders.loadViewResult ?? vi.fn().mockResolvedValue(result())
  render(
    <QueryClientProvider client={makeClient()}>
      <BasePreview
        workspaceId="ws-1"
        entry={e}
        loadContent={loadContent}
        loadInfo={loadInfo}
        loadViewResult={loadViewResult}
      />
    </QueryClientProvider>,
  )
  return { loadContent, loadInfo, loadViewResult }
}

describe('BasePreview — tabs over the base file views', () => {
  it('renders one tab per declared view, first selected, and fetches its result by the imported slug', async () => {
    const { loadViewResult } = renderBase()

    const first = await screen.findByTestId('base-view-tab-invoices--outstanding')
    expect(first).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByTestId('base-view-tab-invoices--aged')).toHaveAttribute('aria-selected', 'false')

    await waitFor(() =>
      expect(loadViewResult).toHaveBeenCalledWith('ws-1', 'kb_1', 'invoices--outstanding'),
    )
    // The result renders: its one row count appears on the active tab.
    await waitFor(() => expect(first.textContent).toContain('1'))
    expect(loadViewResult).not.toHaveBeenCalledWith('ws-1', 'kb_1', 'invoices--aged')
  })

  // code-review finding #3(c) — the view-result query is a full server-side
  // view EVALUATION, not a static file read; TanStack Query's library
  // default (`refetchOnWindowFocus: true`) would refire it every time the
  // reader alt-tabs back into the app once its staleTime has elapsed, which
  // for an expensive fetch is wasted work on every refocus. It must stay
  // fetched exactly once across a long idle period plus a refocus.
  it('does not refire the (expensive) view-result fetch merely because the window regained focus', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const loadViewResult = vi.fn().mockResolvedValue(result())
      renderBase({ loadViewResult })
      await vi.waitFor(() => expect(loadViewResult).toHaveBeenCalledTimes(1))

      // Cross well past any sensible staleTime for this fetch.
      await vi.advanceTimersByTimeAsync(120_000)

      // Simulate the window losing then regaining focus (alt-tab away and back).
      focusManager.setFocused(false)
      focusManager.setFocused(true)
      await vi.advanceTimersByTimeAsync(50)

      expect(loadViewResult).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('switching tabs fetches the newly selected view', async () => {
    const { loadViewResult } = renderBase()
    const aged = await screen.findByTestId('base-view-tab-invoices--aged')
    fireEvent.click(aged)
    await waitFor(() => expect(loadViewResult).toHaveBeenCalledWith('ws-1', 'kb_1', 'invoices--aged'))
    expect(aged).toHaveAttribute('aria-selected', 'true')
  })

  it('renders a server refusal with its reason — never a blank panel', async () => {
    renderBase({
      loadViewResult: vi.fn().mockResolvedValue(
        result({
          parts: [],
          rows: [],
          complete: false,
          refusal: {
            code: 'unknown_view',
            reason: 'No view named invoices--outstanding is addressable here.',
            remedy: '',
          },
        }),
      ),
    })
    const refusal = await screen.findByTestId('view-refusal')
    expect(refusal.textContent).toContain('No view named invoices--outstanding')
  })

  it('renders the empty state with the view own filter text', async () => {
    renderBase({ loadViewResult: vi.fn().mockResolvedValue(result({ rows: [], parts: [] })) })
    const empty = await screen.findByTestId('view-empty')
    expect(empty.textContent).toContain('Nothing matches this view.')
    expect(empty.textContent).toContain('status != "paid"')
  })

  it('states plainly when the file is not inside a knowledge base', async () => {
    renderBase({
      loadInfo: vi
        .fn()
        .mockResolvedValue(info({ is_knowledge_base: false, marker: 'none', collection_id: undefined })),
    })
    const msg = await screen.findByTestId('base-preview-no-collection')
    expect(msg.textContent).toContain('not inside a knowledge base')
  })

  it('states plainly when the file declares no views', async () => {
    renderBase({
      loadContent: vi.fn().mockResolvedValue({ content: 'filters:\n  and: []\n', is_text: true, too_large: false }),
    })
    const msg = await screen.findByTestId('base-preview-no-views')
    expect(msg.textContent).toContain('declares no views')
    // No `views:` key at all — "declares no views" is an honest answer, so
    // there is nothing more to escape to than the raw file itself.
    expect(screen.getByTestId('base-preview-view-raw')).toBeInTheDocument()
    expect(screen.getByTestId('base-preview-download')).toBeInTheDocument()
  })

  // code-review finding #9 — a flow-style-YAML `views: [{name: All}]` block
  // is real (the file is not empty), but the indentation-walk parser
  // (baseViewNames.ts) cannot read that shape and reports zero views. Before
  // the fix this rendered "declares no views" (false — the file DOES declare
  // one) with no way to see or edit the raw file. The message must say
  // parsing failed, not that the file is empty, and BasePreview must offer
  // an escape hatch (view-kinds-design-2026-09-03 §7: "not a download and
  // not raw YAML" is the happy path, not a dead end when parsing fails).
  describe('the parser-failure dead end (flow-style YAML the parser cannot read)', () => {
    const FLOW_STYLE_CONTENT = 'views: [{name: All}]\n'

    it('states that the views could not be parsed, never that the file declares none', async () => {
      renderBase({
        loadContent: vi
          .fn()
          .mockResolvedValue({ content: FLOW_STYLE_CONTENT, is_text: true, too_large: false }),
      })
      const msg = await screen.findByTestId('base-preview-no-views')
      expect(msg.textContent).toContain('could not be parsed')
      expect(msg.textContent).not.toContain('declares no views')
    })

    it('offers a raw-view escape hatch that shows the actual file content', async () => {
      renderBase({
        loadContent: vi
          .fn()
          .mockResolvedValue({ content: FLOW_STYLE_CONTENT, is_text: true, too_large: false }),
      })
      await screen.findByTestId('base-preview-no-views')
      const viewRawButton = screen.getByTestId('base-preview-view-raw')
      fireEvent.click(viewRawButton)
      const shiki = await screen.findByTestId('shiki')
      expect(shiki.textContent).toContain('views: [{name: All}]')
    })

    it('offers a working Download action', async () => {
      const clickSpy = vi.fn()
      const originalCreateElement = document.createElement.bind(document)
      vi.spyOn(document, 'createElement').mockImplementation((tag: string) => {
        const el = originalCreateElement(tag)
        if (tag === 'a') el.addEventListener('click', clickSpy)
        return el
      })
      renderBase({
        loadContent: vi
          .fn()
          .mockResolvedValue({ content: FLOW_STYLE_CONTENT, is_text: true, too_large: false }),
      })
      await screen.findByTestId('base-preview-no-views')
      fireEvent.click(screen.getByTestId('base-preview-download'))
      expect(clickSpy).toHaveBeenCalledTimes(1)
      vi.restoreAllMocks()
    })
  })
})
