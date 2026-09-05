// BasePreview.test.tsx — the .base surface: tabs from the SERVER's list of
// the views this base owns (first selected), each result fetched by the slug
// the server gave verbatim, and every non-happy state a stated answer
// (view-kinds-design-2026-09-03 §7).
//
// THE POINT OF THE REWRITE. This suite used to feed the component .base YAML
// and assert on slugs the component derived itself. That derivation was the
// defect (code-review findings #3 and #7): it could not reproduce the
// importer's collision counter, so two view names that kebab alike collapsed
// onto one slug and the second tab rendered the first view's rows; and a
// nested `name:` key clobbered the display name, so a valid view answered
// `unknown_view`. The component now asks the server, and the tests assert
// that it uses what it is told — including the collision case a client-side
// derivation cannot get right.
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
import type { KnowledgeBaseViews, ViewResult } from '@/lib/api/generated/openapi-types'
import type { LibraryEntry } from '@/lib/api'
import { BasePreview } from './BasePreview'

vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => <pre data-testid="shiki">{children}</pre>,
  // markdown-shared.tsx passes Shiki its pure-JS regex engine (the SPA's CSP
  // refuses the WebAssembly default); the module is mocked here, so this only
  // has to exist.
  createJavaScriptRegexEngine: () => ({}),
}))

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

const BASE_CONTENT = `views:
  - type: table
    name: Outstanding
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

function baseViews(over: Partial<KnowledgeBaseViews> = {}): KnowledgeBaseViews {
  return {
    base_path: 'vault/Invoices.base',
    is_knowledge_base: true,
    collection_id: 'kb_1',
    collection_root: 'vault',
    source: 'Invoices.base',
    views: [
      { name: 'invoices--outstanding', label: 'Outstanding' },
      { name: 'invoices--aged', label: 'Aged' },
    ],
    unloadable_count: 0,
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
  loadBaseViews?: (ws: string, path: string) => Promise<KnowledgeBaseViews>
  loadViewResult?: (ws: string, collectionId: string, view: string) => Promise<ViewResult>
}

function renderBase(loaders: Loaders = {}, e = entry()) {
  const loadContent =
    loaders.loadContent ?? vi.fn().mockResolvedValue({ content: BASE_CONTENT, is_text: true, too_large: false })
  const loadBaseViews = loaders.loadBaseViews ?? vi.fn().mockResolvedValue(baseViews())
  const loadViewResult = loaders.loadViewResult ?? vi.fn().mockResolvedValue(result())
  render(
    <QueryClientProvider client={makeClient()}>
      <BasePreview
        workspaceId="ws-1"
        entry={e}
        loadContent={loadContent}
        loadBaseViews={loadBaseViews}
        loadViewResult={loadViewResult}
      />
    </QueryClientProvider>,
  )
  return { loadContent, loadBaseViews, loadViewResult }
}

describe('BasePreview — tabs over the views the server says this base owns', () => {
  it('renders one tab per view, first selected, and fetches its result by the server slug', async () => {
    const { loadBaseViews, loadViewResult } = renderBase()

    await waitFor(() => expect(loadBaseViews).toHaveBeenCalledWith('ws-1', 'vault/Invoices.base'))

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

  // Finding #3, the defect this endpoint exists to close. The importer's
  // SlugRegistry appends a collision counter over everything it has already
  // handed out, so "A/B" and "A B" become `invoices--a-b` and
  // `invoices--a-b-2`. A client re-deriving slugs from the .base file cannot
  // reproduce that counter and produced `invoices--a-b` for BOTH: the second
  // tab fetched the first view, rendered its rows under the second view's
  // name, and both tabs shared a React key. Nothing here derives anything —
  // the two slugs come from the server, so the two tabs are two views.
  it('keeps two views whose names kebab alike distinct — separate tabs, separate fetches', async () => {
    const { loadViewResult } = renderBase({
      loadBaseViews: vi.fn().mockResolvedValue(
        baseViews({
          views: [
            { name: 'invoices--a-b', label: 'A/B' },
            { name: 'invoices--a-b-2', label: 'A B' },
          ],
        }),
      ),
    })

    const first = await screen.findByTestId('base-view-tab-invoices--a-b')
    const second = await screen.findByTestId('base-view-tab-invoices--a-b-2')
    expect(first).not.toBe(second)
    expect(first.textContent).toContain('A/B')
    expect(second.textContent).toContain('A B')

    await waitFor(() => expect(loadViewResult).toHaveBeenCalledWith('ws-1', 'kb_1', 'invoices--a-b'))
    fireEvent.click(second)
    await waitFor(() => expect(loadViewResult).toHaveBeenCalledWith('ws-1', 'kb_1', 'invoices--a-b-2'))
  })

  // Finding #7: a view whose display name differs from its slug must still be
  // addressed by the slug. The old walk derived the address from whatever
  // `name:` line it saw last, and a nested mapping key sent it looking for a
  // view file that does not exist.
  it('addresses a view by its slug even when the label shares nothing with it', async () => {
    const { loadViewResult } = renderBase({
      loadBaseViews: vi.fn().mockResolvedValue(
        baseViews({
          views: [{ name: 'invoices--outstanding', label: 'Everything still owed to us' }],
        }),
      ),
    })
    const tab = await screen.findByTestId('base-view-tab-invoices--outstanding')
    expect(tab.textContent).toContain('Everything still owed to us')
    await waitFor(() =>
      expect(loadViewResult).toHaveBeenCalledWith('ws-1', 'kb_1', 'invoices--outstanding'),
    )
  })

  it('says out loud when some of this base view files could not be loaded', async () => {
    renderBase({ loadBaseViews: vi.fn().mockResolvedValue(baseViews({ unloadable_count: 2 })) })
    const notice = await screen.findByTestId('base-preview-unloadable')
    expect(notice.textContent).toContain('2 views')
    expect(notice.textContent).toContain('could not be loaded')
  })

  it('marks a view the server says cannot be served, and still offers it', async () => {
    renderBase({
      loadBaseViews: vi.fn().mockResolvedValue(
        baseViews({
          views: [
            { name: 'invoices--outstanding', label: 'Outstanding' },
            {
              name: 'invoices--everything',
              label: 'Everything',
              unservable: true,
              unservable_reason: 'stored disabled because a filter clause could not be translated',
            },
          ],
        }),
      ),
    })
    await screen.findByTestId('base-view-tab-invoices--everything')
    expect(screen.getByTestId('base-view-tab-unservable-invoices--everything')).toBeInTheDocument()
    expect(
      screen.queryByTestId('base-view-tab-unservable-invoices--outstanding'),
    ).not.toBeInTheDocument()
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

  it('renders the empty state naming what the view draws', async () => {
    renderBase({
      loadViewResult: vi.fn().mockResolvedValue(result({ rows: [], parts: [], type: 'invoice' })),
    })
    const empty = await screen.findByTestId('view-empty')
    expect(empty.textContent).toContain('Nothing matches this view.')
    expect(empty.textContent).toContain('every invoice record')
  })

  it('never reads the base file itself while it has views to draw', async () => {
    const { loadContent } = renderBase()
    await screen.findByTestId('base-view-tab-invoices--outstanding')
    expect(loadContent).not.toHaveBeenCalled()
  })

  it('states plainly when the file is not inside a knowledge base', async () => {
    renderBase({
      loadBaseViews: vi.fn().mockResolvedValue(
        baseViews({
          is_knowledge_base: false,
          collection_id: undefined,
          collection_root: undefined,
          source: undefined,
          views: [],
        }),
      ),
    })
    const msg = await screen.findByTestId('base-preview-no-collection')
    expect(msg.textContent).toContain('not inside a knowledge base')
  })

  it('surfaces a failed base-views lookup as a retryable error, never a blank', async () => {
    renderBase({ loadBaseViews: vi.fn().mockRejectedValue(new Error('boom')) })
    expect(await screen.findByTestId('base-preview-content-error')).toBeInTheDocument()
  })

  // code-review finding #9 — a base with no drawable views must never be a
  // dead end. The two zero-views facts are DISTINGUISHED by the server now
  // (it is the side that read the view files): nothing imported at all, vs.
  // every view file this base owns failing to load. Both keep the escape
  // hatch.
  describe('a base with no views to draw', () => {
    it('says nothing was imported when nothing failed either', async () => {
      renderBase({ loadBaseViews: vi.fn().mockResolvedValue(baseViews({ views: [] })) })
      const msg = await screen.findByTestId('base-preview-no-views')
      expect(msg.textContent).toContain('No views were imported')
      expect(msg.textContent).not.toContain('could not be loaded')
    })

    it('says the views failed to load when they did, never that there were none', async () => {
      renderBase({
        loadBaseViews: vi.fn().mockResolvedValue(baseViews({ views: [], unloadable_count: 3 })),
      })
      const msg = await screen.findByTestId('base-preview-no-views')
      expect(msg.textContent).toContain('could not be loaded')
      expect(msg.textContent).not.toContain('No views were imported')
    })

    it('offers a raw-view escape hatch that shows the actual file content', async () => {
      renderBase({
        loadBaseViews: vi.fn().mockResolvedValue(baseViews({ views: [] })),
        loadContent: vi
          .fn()
          .mockResolvedValue({ content: 'views: [{name: All}]\n', is_text: true, too_large: false }),
      })
      await screen.findByTestId('base-preview-no-views')
      fireEvent.click(screen.getByTestId('base-preview-view-raw'))
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
      renderBase({ loadBaseViews: vi.fn().mockResolvedValue(baseViews({ views: [] })) })
      await screen.findByTestId('base-preview-no-views')
      fireEvent.click(screen.getByTestId('base-preview-download'))
      expect(clickSpy).toHaveBeenCalledTimes(1)
      vi.restoreAllMocks()
    })
  })
})
