// BasePreview.variant.test.tsx — the `pane`/`inline` layout switch ONLY
// (ADR-083 embedded-content spec, EMB-027/028). Everything about WHICH data
// is fetched, WHICH state renders and WHAT each state says is already
// covered by BasePreview.test.tsx and must not move — this file exists
// purely to prove `variant` changes layout and nothing else.
//
// Filename note: this is deliberately NOT `BasePreview.inline.test.tsx` — the
// spec's own Step 2 register (dashboards: saved-view label matching,
// EMB-040..049) reserves that filename for the separate work of resolving a
// `.base` embed's OWN named view. This file covers layout only, ahead of
// that work, and must not collide with it.
//
// The false-green trap named in the spec's own review for this family of
// test: asserting state identifiers and text are equal across variants is
// trivially true of a component that ignores `variant` entirely. The
// positive control — the outermost container's class list actually DIFFERS
// — is what proves the prop was read at all.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { KnowledgeBaseViews, ViewResult } from '@/lib/api/generated/openapi-types'
import type { LibraryEntry } from '@/lib/api'
import { BasePreview } from './BasePreview'

vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => <pre data-testid="shiki">{children}</pre>,
  createJavaScriptRegexEngine: () => ({}),
}))

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function entry(over: Partial<LibraryEntry> = {}): LibraryEntry {
  return {
    name: 'Invoices.base',
    path: 'vault/Invoices.base',
    is_dir: false,
    is_hidden: false,
    size: 128,
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
    views: [{ name: 'invoices--outstanding', label: 'Outstanding' }],
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

function renderBase(variant: 'pane' | 'inline' | undefined, loaders: Loaders = {}, e = entry()) {
  const loadContent =
    loaders.loadContent ?? vi.fn().mockResolvedValue({ content: '', is_text: true, too_large: false })
  const loadBaseViews = loaders.loadBaseViews ?? vi.fn().mockResolvedValue(baseViews())
  const loadViewResult = loaders.loadViewResult ?? vi.fn().mockResolvedValue(result())
  return render(
    <QueryClientProvider client={makeClient()}>
      <BasePreview
        workspaceId="ws-1"
        entry={e}
        loadContent={loadContent}
        loadBaseViews={loadBaseViews}
        loadViewResult={loadViewResult}
        {...(variant !== undefined ? { variant } : {})}
      />
    </QueryClientProvider>,
  )
}

describe('BasePreview — variant default and marker', () => {
  it('defaults to "pane" when the prop is omitted, matching every existing call site', async () => {
    renderBase(undefined)
    const container = await screen.findByTestId('base-preview')
    expect(container).toHaveAttribute('data-variant', 'pane')
  })
})

describe('BasePreview — inline vs. pane (EMB-027: same renderer, EMB-028: layout only)', () => {
  it('renders the SAME tab and SAME row-count text on the happy path in both variants', async () => {
    const pane = renderBase('pane')
    const paneTab = await pane.findByTestId('base-view-tab-invoices--outstanding')
    await waitFor(() => expect(paneTab.textContent).toContain('1'))
    const paneTabText = paneTab.textContent
    const paneContainerClass = pane.getByTestId('base-preview').className
    pane.unmount()

    const inline = renderBase('inline')
    const inlineTab = await inline.findByTestId('base-view-tab-invoices--outstanding')
    await waitFor(() => expect(inlineTab.textContent).toContain('1'))

    // The state — which tab, its label, its row count — is identical.
    expect(inlineTab.textContent).toBe(paneTabText)
    expect(inlineTab).toHaveAttribute('aria-selected', 'true')

    // The positive control: the outermost container's layout classes DIFFER.
    // MUTATION THIS DIES ON: a component that computes the same className
    // string regardless of `variant` — every assertion above this line would
    // still pass on that mutant.
    const inlineContainerClass = inline.getByTestId('base-preview').className
    expect(inlineContainerClass).not.toBe(paneContainerClass)
    expect(inlineContainerClass).not.toContain('h-full')
    expect(inline.getByTestId('base-preview')).toHaveAttribute('data-variant', 'inline')
  })

  it('renders the SAME non-happy "no views" state and text in both variants (EMB-028: non-happy states are unchanged)', async () => {
    const noViewsAnswer = baseViews({ views: [] })
    const pane = renderBase('pane', { loadBaseViews: vi.fn().mockResolvedValue(noViewsAnswer) })
    const paneMessage = await pane.findByTestId('base-preview-no-views')
    const paneMessageText = paneMessage.textContent
    expect(screen.getByTestId('base-preview-view-raw')).toBeInTheDocument()
    expect(screen.getByTestId('base-preview-download')).toBeInTheDocument()
    pane.unmount()

    const inline = renderBase('inline', { loadBaseViews: vi.fn().mockResolvedValue(noViewsAnswer) })
    const inlineMessage = await inline.findByTestId('base-preview-no-views')
    // MUTATION THIS DIES ON: a variant branch that swaps in a DIFFERENT
    // message, or drops the escape hatch, for `inline` — EMB-028 forbids a
    // layout variant from changing which states exist or how they render.
    expect(inlineMessage.textContent).toBe(paneMessageText)
    expect(inline.getByTestId('base-preview-view-raw')).toBeInTheDocument()
    expect(inline.getByTestId('base-preview-download')).toBeInTheDocument()
  })

  it('renders the SAME "not a knowledge base" state and text in both variants', async () => {
    const answer = baseViews({ is_knowledge_base: false, views: [] })
    const pane = renderBase('pane', { loadBaseViews: vi.fn().mockResolvedValue(answer) })
    const paneMessage = await pane.findByTestId('base-preview-no-collection')
    const paneMessageText = paneMessage.textContent
    pane.unmount()

    const inline = renderBase('inline', { loadBaseViews: vi.fn().mockResolvedValue(answer) })
    const inlineMessage = await inline.findByTestId('base-preview-no-collection')
    expect(inlineMessage.textContent).toBe(paneMessageText)
  })
})
