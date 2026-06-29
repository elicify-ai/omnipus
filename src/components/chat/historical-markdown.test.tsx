/**
 * historical-markdown.tsx — Mermaid rendering in FINALIZED chat messages.
 *
 * This is the renderer ChatScreen uses for every already-streamed assistant
 * message (ChatScreen.tsx:688). It is a SEPARATE renderer from the live
 * AssistantUI path (markdown-text.tsx → SyntaxHighlighter → MermaidDiagram):
 * once a turn finishes streaming, the message re-renders through THIS component.
 *
 * Regression target: a ```mermaid fence used to revert to a plain <pre><code>
 * here (mermaid worked only while streaming, then silently disappeared on
 * finalize/reload). These tests render through the REAL react-markdown so the
 * `code`/`pre` overrides actually fire on a real fenced block — the earlier
 * shiki-highlighter tests only exercised the live path and missed this entirely.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { HistoricalMessageMarkdown } from './historical-markdown'

// react-markdown stays REAL — that is the point: the fence must be parsed and the
// `code`/`pre` component overrides must run. Only the leaf deps are stubbed.
vi.mock('remark-gfm', () => ({ default: () => {} }))
vi.mock('remark-math', () => ({ default: () => {} }))
vi.mock('rehype-katex', () => ({ default: () => {} }))
vi.mock('katex/dist/katex.min.css', () => ({}))
vi.mock('@tanstack/react-query', () => ({
  useQuery: () => ({ data: { preview_port: 5001 } }),
}))
vi.mock('@/lib/api', () => ({ fetchAboutInfo: vi.fn().mockResolvedValue({}) }))
vi.mock('@/lib/rehype-phosphor-emoji', () => ({ rehypePhosphorEmoji: () => {} }))
vi.mock('./image-lightbox', () => ({ ImageLightbox: () => null }))

// Sentinel for the diagram renderer: lets us assert it was chosen and received the
// fence's code. The real MermaidDiagram is unit-tested in mermaid-renderer.test.tsx.
vi.mock('./mermaid-renderer', () => ({
  MermaidDiagram: ({ code }: { code: string }) => <div data-testid="mermaid-diagram">{code}</div>,
}))

describe('HistoricalMessageMarkdown — Mermaid in finalized messages', () => {
  it('renders a ```mermaid fence as a MermaidDiagram, not a code block', () => {
    render(<HistoricalMessageMarkdown content={'```mermaid\ngraph TD; A-->B\n```'} />)

    const diagram = screen.getByTestId('mermaid-diagram')
    expect(diagram).toBeInTheDocument()
    // The fence body is passed through, trailing newline trimmed.
    expect(diagram).toHaveTextContent('graph TD; A-->B')
    // It must NOT fall through to a plain <pre> code block.
    expect(document.querySelector('pre')).toBeNull()
  })

  it('renders a non-mermaid fence as a plain code block (not a diagram)', () => {
    render(<HistoricalMessageMarkdown content={'```ts\nconst x = 1\n```'} />)

    const pre = document.querySelector('pre')
    expect(pre).toBeInTheDocument()
    expect(pre).toHaveTextContent('const x = 1')
    expect(pre?.querySelector('code')?.className).toContain('language-ts')
    expect(screen.queryByTestId('mermaid-diagram')).toBeNull()
  })

  it('does not double-wrap block code in nested <pre> elements', () => {
    // Regression: the old renderer let react-markdown's default <pre> wrap the
    // override's own <pre>, producing invalid <pre><pre>. The `pre` pass-through
    // fixes that — exactly one <pre> per fenced block.
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
  it('renders a bare (no-language) multi-line fence inside a <pre> (regression guard)', () => {
    render(<HistoricalMessageMarkdown content={'```\nline one\nline two\n```'} />)

    const pre = document.querySelector('pre')
    expect(pre).toBeInTheDocument()
    expect(pre).toHaveTextContent('line one')
    expect(pre).toHaveTextContent('line two')
    // Exactly one <pre>, no diagram
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
