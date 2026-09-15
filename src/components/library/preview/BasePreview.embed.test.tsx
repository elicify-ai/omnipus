// BasePreview.embed.test.tsx — ADR-083 EMB-040/EMB-043/EMB-046/EMB-047/
// EMB-048/EMB-049: the extra behaviour the `embed` prop adds for a `.base`
// embed inside a knowledge-base note, on top of everything BasePreview.test.tsx
// and BasePreview.variant.test.tsx already pin.
//
// Two views with DIFFERENT rows throughout (never same-outcome fixtures) —
// the spec's own "two-views-one-file-do-not-cross" design instruction: a
// component that ignores `embed.viewName` and always shows views[0] is
// caught by asserting the SECOND view's own row appears when it is the one
// named.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider, QueryClient } from '@tanstack/react-query'
import type { KnowledgeBaseViews, ViewResult } from '@/lib/api/generated/openapi-types'
import type { LibraryEntry } from '@/lib/api'
import { BasePreview, type BasePreviewEmbedOptions } from './BasePreview'
import type { KbLinkResolution } from './knowledgeMarkdown'

vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => <pre data-testid="shiki">{children}</pre>,
  createJavaScriptRegexEngine: () => ({}),
}))

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function entry(): LibraryEntry {
  return {
    name: 'Tasks.base',
    path: 'vault/Tasks.base',
    is_dir: false,
    is_hidden: false,
    size: 42,
    modified_at: '2026-09-01T10:00:00Z',
    is_text_editable: true,
  }
}

function baseViews(): KnowledgeBaseViews {
  return {
    base_path: 'vault/Tasks.base',
    is_knowledge_base: true,
    collection_id: 'kb_1',
    collection_root: 'vault',
    source: 'Tasks.base',
    views: [
      { name: 'tasks--needs-daniel', label: 'Needs Daniel' },
      { name: 'tasks--awaiting-founder', label: 'Awaiting founder' },
    ],
    unloadable_count: 0,
  }
}

function resultFor(view: string): ViewResult {
  if (view === 'tasks--needs-daniel') {
    return {
      view,
      label: 'Needs Daniel',
      parts: [{ part: 'table', source: { part: 'table' }, columns: ['file.name', 'status'] }],
      rows: [{ path: 'a.md', title: 'Needs-Daniel-Only-Row', cells: [{ property: 'status', value: '[[Related Note]]' }], joins: [] }],
      complete: true,
      problems: [],
    }
  }
  return {
    view,
    label: 'Awaiting founder',
    parts: [{ part: 'table', source: { part: 'table' }, columns: ['file.name'] }],
    rows: [{ path: 'b.md', title: 'Awaiting-Founder-Only-Row', cells: [], joins: [] }],
    complete: true,
    problems: [],
  }
}

function renderEmbed(embed: BasePreviewEmbedOptions) {
  const loadContent = vi.fn().mockResolvedValue({ content: '', is_text: true, too_large: false })
  const loadBaseViews = vi.fn().mockResolvedValue(baseViews())
  const loadViewResult = vi.fn((_ws: string, _cid: string, view: string) => Promise.resolve(resultFor(view)))
  render(
    <QueryClientProvider client={makeClient()}>
      <BasePreview
        workspaceId="ws-1"
        entry={entry()}
        variant="inline"
        embed={embed}
        loadContent={loadContent}
        loadBaseViews={loadBaseViews}
        loadViewResult={loadViewResult}
      />
    </QueryClientProvider>,
  )
  return { loadViewResult }
}

describe('BasePreview — embed.viewName selects a SPECIFIC view (EMB-040)', () => {
  it('renders the SECOND view’s own row when it is the one named — not views[0]', async () => {
    renderEmbed({ viewName: 'tasks--awaiting-founder', showViewSwitcher: true })
    await waitFor(() => expect(screen.getByText('Awaiting-Founder-Only-Row')).toBeInTheDocument())
    expect(screen.queryByText('Needs-Daniel-Only-Row')).not.toBeInTheDocument()
  })

  it('fetches only the named view’s result, never the other view’s (EMB-045’s spirit at N=1)', async () => {
    const { loadViewResult } = renderEmbed({ viewName: 'tasks--awaiting-founder', showViewSwitcher: true })
    await waitFor(() => expect(screen.getByText('Awaiting-Founder-Only-Row')).toBeInTheDocument())
    expect(loadViewResult).toHaveBeenCalledWith('ws-1', 'kb_1', 'tasks--awaiting-founder')
    expect(loadViewResult).not.toHaveBeenCalledWith('ws-1', 'kb_1', 'tasks--needs-daniel')
  })
})

describe('BasePreview — no fragment named (EMB-043)', () => {
  it('renders NO tab list at all when showViewSwitcher is false, and shows the caption', async () => {
    renderEmbed({
      viewName: 'tasks--needs-daniel',
      showViewSwitcher: false,
      caption: 'Showing "Needs Daniel" — the first available view; this embed did not choose one.',
    })
    await waitFor(() => expect(screen.getByText('Needs-Daniel-Only-Row')).toBeInTheDocument())
    expect(screen.queryByTestId('base-preview-tablist')).not.toBeInTheDocument()
    expect(screen.getByTestId('base-preview-embed-caption').textContent).toMatch(/did not choose one/)
  })
})

describe('BasePreview — the tab list is hover/focus-revealed, not always visible, inside an embed (EMB-046)', () => {
  it('renders the tab list with the opacity-0/group-hover reveal classes when a switcher IS offered', async () => {
    renderEmbed({ viewName: 'tasks--needs-daniel', showViewSwitcher: true })
    await waitFor(() => expect(screen.getByTestId('base-preview-tablist')).toBeInTheDocument())
    const tablist = screen.getByTestId('base-preview-tablist')
    expect(tablist.className).toContain('opacity-0')
    expect(tablist.className).toMatch(/group-hover:opacity-100/)
  })

  it('does NOT hide the tab list this way in ordinary (non-embed) pane use — the positive control proving the class is embed-specific', async () => {
    const loadBaseViews = vi.fn().mockResolvedValue(baseViews())
    const loadViewResult = vi.fn((_ws: string, _cid: string, view: string) => Promise.resolve(resultFor(view)))
    render(
      <QueryClientProvider client={makeClient()}>
        <BasePreview
          workspaceId="ws-1"
          entry={entry()}
          loadContent={vi.fn().mockResolvedValue({ content: '', is_text: true, too_large: false })}
          loadBaseViews={loadBaseViews}
          loadViewResult={loadViewResult}
        />
      </QueryClientProvider>,
    )
    await waitFor(() => expect(screen.getByTestId('base-preview-tablist')).toBeInTheDocument())
    expect(screen.getByTestId('base-preview-tablist').className).not.toContain('opacity-0')
  })
})

describe('BasePreview — links inside an embedded view resolve against the note reader’s graph, not this view’s own rows (EMB-048)', () => {
  it('an overridden resolveWikilink answering "unresolved" is honoured — a state the view’s OWN internal resolver can never produce', async () => {
    // BasePreview's row-scoped fallback resolver (no `embed.resolveWikilink`)
    // can only ever answer 'resolved' or 'unknown' — see its own doc
    // comment. Rendering the UNRESOLVED marker here is therefore possible
    // ONLY if the override actually replaced it, never as a coincidence of
    // the default.
    const resolveWikilink = (): KbLinkResolution => ({ state: 'unresolved' })
    renderEmbed({
      viewName: 'tasks--needs-daniel',
      showViewSwitcher: true,
      resolveWikilink,
    })
    await waitFor(() => {
      expect(screen.getByTestId('viewpart-cell-link-unresolved')).toBeInTheDocument()
    })
  })

  it('an overridden linkHref is used to build the cell’s real address', async () => {
    const linkHref = vi.fn().mockReturnValue('/#/library?workspace=ws-1&path=notes/related.md')
    renderEmbed({
      viewName: 'tasks--needs-daniel',
      showViewSwitcher: true,
      resolveWikilink: () => ({ state: 'resolved', path: 'related.md' }),
      linkHref,
    })
    await waitFor(() => {
      const link = screen.getByTestId('viewpart-cell-link')
      expect(link.getAttribute('href')).toBe('/#/library?workspace=ws-1&path=notes/related.md')
    })
    expect(linkHref).toHaveBeenCalledWith('related.md')
  })
})
