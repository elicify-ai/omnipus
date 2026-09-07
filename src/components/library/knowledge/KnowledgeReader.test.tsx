/**
 * KnowledgeReader — the reading layout.
 *
 * ADR-067 US-7 AS-5/AS-6/AS-9, FR-060, FR-062, FR-063, FR-064, FR-012.
 *
 * ── What this file tests, and what it deliberately no longer tests ──────────
 * The reader owns the reading COLUMN, the wide/docked decision, and scrolling
 * the column to a heading. It no longer owns rail CONTENT: the outline and
 * linked-mentions panels are supplied through `renderRails`, and their
 * behaviour is asserted where it lives — KnowledgeOutline.test.tsx and
 * KnowledgeBacklinks.test.tsx. Two implementations of one requirement is what
 * this refactor removed; two test files asserting the same rendering would put
 * it back.
 *
 * The markdown pipeline underneath stays REAL — the outline's scroll target is
 * a heading that react-markdown actually rendered, so a test that "finds" a
 * heading proves the composition produced one. Only leaf dependencies (Shiki,
 * Mermaid, the lightbox, the preview shell) are stubbed.
 *
 * Every `describe` block names the mutation its assertions die on.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { KnowledgeReader, findHeadingElement, DOCKED_MAX_WIDTH } from './KnowledgeReader'
import type { KnowledgeOutlineHeading } from '@/lib/api/generated/openapi-types'

vi.mock('@/components/chat/mermaid-renderer', () => ({
  MermaidDiagram: ({ code }: { code: string }) => <div data-testid="mermaid-diagram">{code}</div>,
}))
vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => <pre data-testid="shiki">{children}</pre>,
  // markdown-shared.tsx passes Shiki its pure-JS regex engine (the SPA's CSP
  // refuses the WebAssembly default); the module is mocked here, so this only
  // has to exist.
  createJavaScriptRegexEngine: () => ({}),
}))
vi.mock('@/components/chat/ChatImage', () => ({
  ChatImage: ({ src, alt }: { src: string; alt?: string }) => <img data-testid="chat-image" src={src} alt={alt} />,
}))
vi.mock('@/store/ui', () => ({ useUiStore: { getState: () => ({ addToast: vi.fn() }) } }))
vi.mock('@tanstack/react-query', () => ({
  useQuery: () => ({ data: null }),
  useQueries: () => [],
}))
vi.mock('../preview/LibraryTextPreview', () => ({
  LibraryTextPreview: ({
    content,
    renderView,
  }: {
    content: string
    renderView: (draft: string) => React.ReactNode
  }) => <div data-testid="text-preview-shell">{renderView(content)}</div>,
}))

const NOTE = '# First heading\n\nsome prose\n\n## Second heading\n\nmore prose'

const HEADINGS: KnowledgeOutlineHeading[] = [
  { level: 1, text: 'First heading', slug: 'first-heading' },
  { level: 2, text: 'Second heading', slug: 'second-heading' },
]

let scrolled: Element[] = []
let originalScrollIntoView: unknown

beforeEach(() => {
  scrolled = []
  originalScrollIntoView = (Element.prototype as unknown as { scrollIntoView?: unknown }).scrollIntoView
  ;(Element.prototype as unknown as { scrollIntoView: () => void }).scrollIntoView = function (this: Element) {
    scrolled.push(this)
  }
})

afterEach(() => {
  ;(Element.prototype as unknown as { scrollIntoView?: unknown }).scrollIntoView = originalScrollIntoView
})

/** A rail stand-in that reports what the reader handed it and exercises the
 *  scroll callback. Deliberately not the real panels: this file is about the
 *  CONTRACT between reader and rails, not about either panel's rendering. */
function railProbe(heading: KnowledgeOutlineHeading = HEADINGS[1], index = 1) {
  return ({
    collapsible,
    scrollToHeading,
  }: {
    collapsible: boolean
    scrollToHeading: (h: KnowledgeOutlineHeading, i?: number) => void
  }) => (
    <button
      type="button"
      data-testid="rail-probe"
      data-collapsible={String(collapsible)}
      onClick={() => scrollToHeading(heading, index)}
    >
      rail
    </button>
  )
}

// ─────────────────────────────────────────────────────────────────────────────

describe('the rails are a slot, and the reader tells them how to render (FR-064, US-7 AS-9)', () => {
  // DIES ON: hard-coding `collapsible: false`, dropping `renderRails` from the
  // docked branch, or rendering the rails region when no rails were supplied.

  it('reports collapsible=false when there is room for a side rail', () => {
    render(<KnowledgeReader content={NOTE} path="notes/plan.md" layout="wide" renderRails={railProbe()} />)
    expect(screen.getByTestId('knowledge-reader').getAttribute('data-layout')).toBe('wide')
    expect(screen.getByTestId('knowledge-reader-rails').getAttribute('data-collapsible')).toBe('false')
    expect(screen.getByTestId('rail-probe').getAttribute('data-collapsible')).toBe('false')
  })

  it('reports collapsible=true when docked', () => {
    render(<KnowledgeReader content={NOTE} path="notes/plan.md" layout="docked" renderRails={railProbe()} />)
    expect(screen.getByTestId('knowledge-reader').getAttribute('data-layout')).toBe('docked')
    expect(screen.getByTestId('rail-probe').getAttribute('data-collapsible')).toBe('true')
  })

  it('renders the reading column with no rails region at all when none is supplied', () => {
    render(<KnowledgeReader content={NOTE} path="notes/plan.md" layout="wide" />)
    expect(screen.getByTestId('knowledge-reader-article')).toBeInTheDocument()
    expect(screen.queryByTestId('knowledge-reader-rails')).not.toBeInTheDocument()
  })

  it('falls back to the wide layout when the pane cannot be measured', () => {
    // No ResizeObserver in this environment: the rails must still RENDER.
    // Hiding them because a measurement was unavailable would remove the
    // outline from a pane that is perfectly wide enough for it.
    const saved = (globalThis as { ResizeObserver?: unknown }).ResizeObserver
    delete (globalThis as { ResizeObserver?: unknown }).ResizeObserver
    try {
      render(<KnowledgeReader content={NOTE} path="notes/plan.md" layout="auto" renderRails={railProbe()} />)
      expect(screen.getByTestId('knowledge-reader').getAttribute('data-layout')).toBe('wide')
      expect(screen.getByTestId('rail-probe')).toBeInTheDocument()
    } finally {
      if (saved !== undefined) (globalThis as { ResizeObserver?: unknown }).ResizeObserver = saved
    }
  })

  it('docks when the measured width is at or below the threshold', () => {
    // The boundary is spec'd as "at or below": DOCKED_MAX_WIDTH itself docks.
    class FakeRO {
      constructor(private cb: ResizeObserverCallback) {}
      observe(el: Element) {
        this.cb(
          [{ contentRect: { width: DOCKED_MAX_WIDTH } } as unknown as ResizeObserverEntry],
          this as unknown as ResizeObserver,
        )
        void el
      }
      disconnect() {}
      unobserve() {}
    }
    const saved = (globalThis as { ResizeObserver?: unknown }).ResizeObserver
    ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = FakeRO
    try {
      render(<KnowledgeReader content={NOTE} path="notes/plan.md" layout="auto" renderRails={railProbe()} />)
      expect(screen.getByTestId('knowledge-reader').getAttribute('data-layout')).toBe('docked')
    } finally {
      ;(globalThis as { ResizeObserver?: unknown }).ResizeObserver = saved
    }
  })
})

describe('scrolling to a heading (US-7 AS-5)', () => {
  // DIES ON: dropping the text guard from findHeadingElement (the wrong heading
  // would scroll), or removing scrollToHeading from the rail context.

  it('scrolls to the heading the rail asked for', () => {
    render(<KnowledgeReader content={NOTE} path="notes/plan.md" layout="wide" renderRails={railProbe()} />)
    fireEvent.click(screen.getByTestId('rail-probe'))
    expect(scrolled).toHaveLength(1)
    expect(scrolled[0].textContent).toBe('Second heading')
  })

  it('scrolls nowhere when the outline names a heading the note does not contain', () => {
    // Scrolling to the wrong heading is worse than not scrolling, so a
    // positional index whose text does not match must find nothing at all.
    render(
      <KnowledgeReader
        content={NOTE}
        path="notes/plan.md"
        layout="wide"
        renderRails={railProbe({ level: 2, text: 'Heading that is not there', slug: 'nope' }, 1)}
      />,
    )
    fireEvent.click(screen.getByTestId('rail-probe'))
    expect(scrolled).toHaveLength(0)
  })

  it('prefers a text match over the positional index', () => {
    // Derived from the rule, not from the code: "the nth heading IF its text
    // matches, otherwise the first heading whose text matches".
    render(<KnowledgeReader content={NOTE} path="notes/plan.md" layout="wide" renderRails={railProbe(HEADINGS[0], 9)} />)
    fireEvent.click(screen.getByTestId('rail-probe'))
    expect(scrolled).toHaveLength(1)
    expect(scrolled[0].textContent).toBe('First heading')
  })
})

describe('findHeadingElement (unit)', () => {
  it('takes the nth heading only when its text matches, else the first that does, else nothing', () => {
    const host = document.createElement('div')
    host.innerHTML = '<h1>Alpha</h1><h2>Beta</h2><h3>Alpha</h3>'
    expect(findHeadingElement(host, 0, 'Alpha')?.tagName).toBe('H1')
    // index 2 is an 'Alpha' — positional wins when the text agrees
    expect(findHeadingElement(host, 2, 'Alpha')?.tagName).toBe('H3')
    // index 1 is 'Beta' — text guard rejects it, first 'Alpha' wins
    expect(findHeadingElement(host, 1, 'Alpha')?.tagName).toBe('H1')
    expect(findHeadingElement(host, 0, 'Gamma')).toBeNull()
  })
})

describe('the reading column stays readable at width', () => {
  // DIES ON: removing the measure cap — the most common readability failure in
  // a wide pane is a 200-character line.

  it('caps the article at the reading measure', () => {
    render(<KnowledgeReader content={NOTE} path="notes/plan.md" layout="wide" />)
    const article = screen.getByTestId('knowledge-reader-article')
    expect(article.style.getPropertyValue('--kb-reading-measure')).toBe('72ch')
    expect(article.style.maxWidth).toBe('var(--kb-reading-measure)')
  })
})

describe('the note renders through the KB composition, not a plain one', () => {
  // DIES ON: swapping KnowledgeBaseMarkdown for chat's renderer here — the
  // wikilink and the private comment would both survive as literal text.

  it('renders wikilinks and hides private comments', () => {
    render(
      <KnowledgeReader content={'body %%private%% text\n\nlink to [[Other Note]]'} path="notes/plan.md" layout="wide" />,
    )
    const article = screen.getByTestId('knowledge-reader-article')
    expect(article.textContent).not.toContain('private')
    expect(within(article).getByTestId('markdown-link').getAttribute('data-kb-target')).toBe('Other Note')
  })

  it('resolves a relative link against the open note’s folder', () => {
    const onNavigate = vi.fn()
    render(
      <KnowledgeReader
        content={'[old](../archive/old.md)'}
        path="notes/2026/plan.md"
        onNavigate={onNavigate}
        layout="wide"
      />,
    )
    const article = screen.getByTestId('knowledge-reader-article')
    fireEvent.click(within(article).getByTestId('markdown-link'))
    expect(onNavigate).toHaveBeenCalledWith('notes/archive/old.md', undefined)
  })

  it('scrolls to a heading named by an in-document link', () => {
    // The href carries a SLUG, not the heading's text, so the lookup has to try
    // the de-slugified form too.
    render(<KnowledgeReader content={`${NOTE}\n\n[jump](#Second-heading)`} path="notes/plan.md" layout="wide" />)
    const article = screen.getByTestId('knowledge-reader-article')
    fireEvent.click(within(article).getByTestId('markdown-link'))
    expect(scrolled).toHaveLength(1)
    expect(scrolled[0].textContent).toBe('Second heading')
  })

  it('passes linkHref down so a resolved link has a real, copyable address (FR-012)', () => {
    // DIES ON: dropping `linkHref` from the props threaded into
    // KnowledgeBaseMarkdown — the link falls back to the no-href button form.
    render(
      <KnowledgeReader
        content={'[plan](notes/plan.md)'}
        // At the collection root, so `notes/plan.md` resolves to itself.
        path="index.md"
        layout="wide"
        linkHref={(p) => `/#/library?workspace=ws-1&path=${p}`}
      />,
    )
    const link = screen.getByTestId('markdown-link')
    expect(link.tagName).toBe('A')
    expect(link.getAttribute('href')).toBe('/#/library?workspace=ws-1&path=notes/plan.md')
  })
})
