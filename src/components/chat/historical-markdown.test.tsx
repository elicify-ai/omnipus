/**
 * historical-markdown.tsx — Mermaid AND Shiki-highlighting parity in FINALIZED
 * chat messages.
 *
 * This is the renderer ChatScreen uses for every already-streamed assistant
 * message (ChatScreen.tsx:688). It is a SEPARATE renderer from the live
 * AssistantUI path (markdown-text.tsx → SyntaxHighlighter → MermaidDiagram):
 * once a turn finishes streaming, the message re-renders through THIS component.
 *
 * Regression target #1 (fixed in 3ed49f01): a ```mermaid fence used to revert to
 * a plain <pre><code> here (mermaid worked only while streaming, then silently
 * disappeared on finalize/reload).
 *
 * Regression target #2 (library-spec D-6): non-mermaid block code used to render
 * as a plain, unhighlighted <pre><code> here too — Shiki was wired into the LIVE
 * path only, so highlighting vanished the instant a message finalized or the page
 * reloaded. `HistoricalCodeBlock` now renders through the same `ShikiCodeBlock`
 * (markdown-shared.tsx) the live path uses. react-shiki itself is mocked below
 * (as in shiki-highlighter.mermaid.test.tsx) — these tests assert ROUTING
 * (Shiki vs. mermaid vs. inline), not real Shiki tokenization.
 *
 * These tests render through the REAL react-markdown so the `code`/`pre`
 * overrides actually fire on a real fenced block — the earlier shiki-highlighter
 * tests only exercised the live path and missed this entirely.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { HistoricalMessageMarkdown } from './historical-markdown'

// react-markdown stays REAL — that is the point: the fence must be parsed and the
// `code`/`pre` component overrides must run. Only the leaf deps are stubbed.
vi.mock('remark-gfm', () => ({ default: () => {} }))
vi.mock('remark-math', () => ({ default: () => {} }))
vi.mock('rehype-katex', () => ({ default: () => {} }))
vi.mock('katex/dist/katex.min.css', () => ({}))
vi.mock('@tanstack/react-query', () => ({
  useQuery: () => ({ data: null }),
}))
vi.mock('@/lib/api', () => ({ fetchAboutInfo: vi.fn().mockResolvedValue({}) }))
vi.mock('@/lib/rehype-phosphor-emoji', () => ({ rehypePhosphorEmoji: () => {} }))
vi.mock('./image-lightbox', () => ({ ImageLightbox: () => null }))

// Sentinel for the diagram renderer: lets us assert it was chosen and received the
// fence's code. The real MermaidDiagram is unit-tested in mermaid-renderer.test.tsx.
vi.mock('./mermaid-renderer', () => ({
  MermaidDiagram: ({ code }: { code: string }) => <div data-testid="mermaid-diagram">{code}</div>,
}))

// Sentinel for the (real, non-mermaid) block-code path: proves HistoricalCodeBlock
// now routes through ShikiCodeBlock/ShikiHighlighter instead of a manual
// <pre><code className="language-...">. Mirrors the mock in
// shiki-highlighter.mermaid.test.tsx — real Shiki tokenization is not exercised
// in unit tests anywhere in this codebase (WASM grammar loading is slow and
// non-deterministic across environments); only the routing is asserted here.
vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => (
    <pre data-testid="shiki">{children}</pre>
  ),
  // markdown-shared.tsx passes Shiki its pure-JS regex engine (the SPA's CSP
  // refuses the WebAssembly default); the module is mocked here, so this only
  // has to exist.
  createJavaScriptRegexEngine: () => ({}),
}))

// Stub copyText so we can assert the text it was called with without needing
// a real clipboard API (jsdom does not implement navigator.clipboard.writeText).
const mockCopyText = vi.fn().mockResolvedValue(undefined)
vi.mock('./media-actions', () => ({
  copyText: (...args: unknown[]) => mockCopyText(...args),
}))

// Stub addToast so error-path tests can assert it was called.
const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: {
    getState: () => ({ addToast: mockAddToast }),
  },
}))

describe('HistoricalMessageMarkdown — Mermaid in finalized messages', () => {
  beforeEach(() => {
    mockCopyText.mockClear()
    mockAddToast.mockClear()
  })
  it('renders a ```mermaid fence as a MermaidDiagram, not a code block', () => {
    render(<HistoricalMessageMarkdown content={'```mermaid\ngraph TD; A-->B\n```'} />)

    const diagram = screen.getByTestId('mermaid-diagram')
    expect(diagram).toBeInTheDocument()
    // The fence body is passed through, trailing newline trimmed.
    expect(diagram).toHaveTextContent('graph TD; A-->B')
    // It must NOT fall through to a plain <pre> code block.
    expect(document.querySelector('pre')).toBeNull()
  })

  it('renders a non-mermaid fence through Shiki, not a diagram (library-spec D-6)', () => {
    render(<HistoricalMessageMarkdown content={'```ts\nconst x = 1\n```'} />)

    // Routes through ShikiCodeBlock (react-shiki mocked above) — the parity fix:
    // this used to be a manual <pre><code class="language-ts"> with no highlighting.
    const shiki = screen.getByTestId('shiki')
    expect(shiki).toBeInTheDocument()
    expect(shiki).toHaveTextContent('const x = 1')
    expect(screen.queryByTestId('mermaid-diagram')).toBeNull()
    // Regression guard: must NOT fall back to the old manual, unhighlighted
    // <code class="language-ts"> node — that WAS the bug (highlighting present
    // while streaming, silently gone the instant the message finalized).
    expect(document.querySelector('code')).toBeNull()
  })

  it('does not double-wrap block code in nested <pre> elements', () => {
    // Regression: the old renderer let react-markdown's default <pre> wrap the
    // override's own <pre>, producing invalid <pre><pre>. The `pre` pass-through
    // fixes that — exactly one <pre> per fenced block (now Shiki's own, via the
    // mocked ShikiHighlighter).
    render(<HistoricalMessageMarkdown content={'```ts\nconst x = 1\n```'} />)

    const pres = document.querySelectorAll('pre')
    expect(pres).toHaveLength(1)
    expect(pres[0].querySelector('pre')).toBeNull()
  })

  it('renders inline `code` as an inline span, never a diagram or block', () => {
    render(<HistoricalMessageMarkdown content={'use `graph TD` inline'} />)

    expect(screen.queryByTestId('mermaid-diagram')).toBeNull()
    expect(document.querySelector('pre')).toBeNull()
    const code = document.querySelector('code')
    expect(code).toBeInTheDocument()
    expect(code).toHaveTextContent('graph TD')
  })

  // Regression guard: bare fences (no language tag) and 4-space indented code must
  // render as <pre><code> with whitespace preserved. Before the fix, `isBlock` was
  // false for these (no `language-` class), so they fell to the inline branch and
  // the pass-through `pre` dropped the wrapper entirely, collapsing newlines.
  it('renders a bare (no-language) multi-line fence through Shiki (regression guard)', () => {
    render(<HistoricalMessageMarkdown content={'```\nline one\nline two\n```'} />)

    const shiki = screen.getByTestId('shiki')
    expect(shiki).toBeInTheDocument()
    expect(shiki).toHaveTextContent('line one')
    expect(shiki).toHaveTextContent('line two')
    // Exactly one <pre> (Shiki's own), no diagram
    expect(document.querySelectorAll('pre')).toHaveLength(1)
    expect(screen.queryByTestId('mermaid-diagram')).toBeNull()
  })

  // Multi-line mermaid fence: internal newlines must survive into the diagram code;
  // only the trailing newline that react-markdown appends is stripped.
  it('passes multi-line mermaid fence content (newlines preserved) to MermaidDiagram', () => {
    render(<HistoricalMessageMarkdown content={'```mermaid\ngraph TD\n  A-->B\n  B-->C\n```'} />)

    const diagram = screen.getByTestId('mermaid-diagram')
    expect(diagram).toBeInTheDocument()
    // Internal newlines preserved; trailing newline trimmed.
    expect(diagram).toHaveTextContent('graph TD')
    expect(diagram).toHaveTextContent('A-->B')
    expect(diagram).toHaveTextContent('B-->C')
    // No <pre> alongside the diagram
    expect(document.querySelector('pre')).toBeNull()
  })

  it('renders an empty ```mermaid``` block as MermaidDiagram with empty code', () => {
    render(<HistoricalMessageMarkdown content={'```mermaid\n```'} />)

    const diagram = screen.getByTestId('mermaid-diagram')
    expect(diagram).toBeInTheDocument()
    // Empty content: trailing newline stripped → empty string
    expect(diagram).toHaveTextContent('')
    expect(document.querySelector('pre')).toBeNull()
  })

  it('renders prose before and after a mermaid fence alongside the diagram', () => {
    render(
      <HistoricalMessageMarkdown
        content={'Here is a diagram:\n\n```mermaid\ngraph TD; A-->B\n```\n\nAnd some text after.'}
      />,
    )

    const diagram = screen.getByTestId('mermaid-diagram')
    expect(diagram).toBeInTheDocument()
    // Surrounding prose paragraphs must still render
    expect(screen.getByText(/Here is a diagram/)).toBeInTheDocument()
    expect(screen.getByText(/And some text after/)).toBeInTheDocument()
  })
})

describe('HistoricalMessageMarkdown — copy header on block code', () => {
  beforeEach(() => {
    mockCopyText.mockClear()
    mockAddToast.mockClear()
  })

  it('renders a Copy button for a non-mermaid block fence', () => {
    render(<HistoricalMessageMarkdown content={'```ts\nconst x = 1\n```'} />)

    const copyButton = screen.getByRole('button', { name: /copy code/i })
    expect(copyButton).toBeInTheDocument()
  })

  it('shows the language label in the header bar', () => {
    render(<HistoricalMessageMarkdown content={'```ts\nconst x = 1\n```'} />)

    // The label text is the raw language string; CSS uppercase is a visual transform only.
    expect(screen.getByText('ts')).toBeInTheDocument()
  })

  it('clicking Copy calls copyText with the code text', async () => {
    render(<HistoricalMessageMarkdown content={'```ts\nconst x = 1\n```'} />)

    const copyButton = screen.getByRole('button', { name: /copy code/i })
    fireEvent.click(copyButton)

    // copyText is async; give the microtask queue a tick
    await Promise.resolve()

    expect(mockCopyText).toHaveBeenCalledTimes(1)
    expect(mockCopyText).toHaveBeenCalledWith('const x = 1\n')
  })

  it('does NOT render a Copy button for inline code', () => {
    render(<HistoricalMessageMarkdown content={'use `graph TD` inline'} />)

    expect(screen.queryByRole('button', { name: /copy code/i })).toBeNull()
  })

  it('shows "code" fallback label for a bare (no-language) fence', () => {
    render(<HistoricalMessageMarkdown content={'```\nline one\n```'} />)

    // Falls back to the "code" label when no language is present (CSS uppercase is visual only).
    expect(screen.getByText('code')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /copy code/i })).toBeInTheDocument()
  })
})
