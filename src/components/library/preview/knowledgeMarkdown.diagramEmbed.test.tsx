// knowledgeMarkdown.diagramEmbed.test.tsx — the founder's ruling that an
// EMBEDDED DIAGRAM FILE is out of scope, made checkable.
//
// THE RULING (2026-09-11): "diagrams / obsydian diagrams are out of scope and
// will not be a feature of omnipus KBs". ADR-083 §5 previously routed
// `![[chart.mmd]]` to the mermaid renderer "in step 6"; that expectation is
// RETIRED, and §5's disposition row now reads as a permanent refusal. The
// code expresses it in `inlineEmbedTreatment` (knowledgeMarkdown.tsx), which
// returns `link-only` for `mermaid` with the ruling written beside it.
//
// WHAT THIS FILE IS FOR. A refusal that is only a comment is indistinguishable
// from an unfinished feature, which is exactly how the previous DEFERRAL
// comment read — and nothing failed if someone "finished" it. These tests fail
// if a mermaid renderer is ever mounted for an embedded `.mmd` file.
//
// ── SCOPE, STATED SO IT CANNOT DRIFT ────────────────────────────────────────
// A ```mermaid FENCED CODE BLOCK inside a note is a DIFFERENT MECHANISM from
// an embedded `.mmd` file, and it is NOT covered by the ruling. ADR-083 §2.1
// measures 163 fenced diagrams in the founder's own vault and records them as
// "Already works — a fenced code block, a different mechanism entirely";
// `kbMarkdownBase.tsx` routes `language === 'mermaid'` to the shared
// `MermaidDiagram` and that path is untouched. The second describe block below
// asserts the fence still renders, in the SAME note as a refused `.mmd` embed,
// so a later over-broad "remove mermaid from the knowledge base" change breaks
// a test instead of silently deleting 163 working diagrams.

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  KnowledgeBaseMarkdown,
  classifyEmbedKind,
  inlineEmbedTreatment,
  type EmbedResolution,
} from './knowledgeMarkdown'

vi.mock('@/components/chat/mermaid-renderer', () => ({
  MermaidDiagram: ({ code }: { code: string }) => <div data-testid="mermaid-diagram">{code}</div>,
}))
vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children?: React.ReactNode }) => <pre data-testid="shiki">{children}</pre>,
  createJavaScriptRegexEngine: () => ({}),
}))
vi.mock('@/components/chat/ChatImage', () => ({
  ChatImage: ({ src, alt }: { src: string; alt?: string }) => <img data-testid="chat-image" src={src} alt={alt} />,
}))
vi.mock('@/store/ui', () => ({ useUiStore: { getState: () => ({ addToast: vi.fn() }) } }))

const WORKSPACE = 'ws-1'
const DIAGRAM_PATH = 'assets/diagram.mmd'

/** A resolver that CONFIRMS the file exists. This is the load-bearing part:
 *  the refusal must hold for a target the graph proved is really there, not
 *  only for a missing one — otherwise the test would pass for the wrong
 *  reason and say nothing about the ruling. */
function resolvesDiagram(): EmbedResolution {
  return {
    state: 'resolved',
    path: DIAGRAM_PATH,
    url: `https://example.test/diagram.mmd`,
    workspaceId: WORKSPACE,
    workspacePath: DIAGRAM_PATH,
  }
}

function renderNote(content: string, resolveEmbedUrl: () => EmbedResolution = resolvesDiagram) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <KnowledgeBaseMarkdown content={content} notePath="notes/Note.md" resolveEmbedUrl={resolveEmbedUrl} />
    </QueryClientProvider>,
  )
}

describe('an embedded diagram FILE is deliberately not rendered (founder ruling, ADR-083 §5)', () => {
  it('renders a standalone, RESOLVED ![[diagram.mmd]] as a working link and mounts no diagram renderer', () => {
    renderNote('![[diagram.mmd]]')

    // Positive: the reader still gets a real, working reference to the file —
    // the refusal is "shown as a link", never "shown as nothing" and never a
    // broken-embed marker for a file that exists.
    const link = screen.getByTestId('markdown-link')
    expect(link).toBeInTheDocument()
    expect(link).toHaveTextContent('diagram.mmd')

    // The ruling itself: no diagram is drawn for an embedded diagram file.
    expect(screen.queryByTestId('mermaid-diagram')).not.toBeInTheDocument()
    // And it was not quietly promoted to some other inline mount either.
    expect(screen.queryByTestId('lazy-embed-mount')).not.toBeInTheDocument()
  })

  it('refuses it the same way when the embed is written with a heading fragment', () => {
    renderNote('![[diagram.mmd#Overview]]')

    expect(screen.getByTestId('markdown-link')).toBeInTheDocument()
    expect(screen.queryByTestId('mermaid-diagram')).not.toBeInTheDocument()
    expect(screen.queryByTestId('lazy-embed-mount')).not.toBeInTheDocument()
  })

  it('still classifies the target honestly as `mermaid` — the refusal is a rendering decision, not a lie about the file', () => {
    // The chosen expression of the ruling: `classifyEmbedKind` keeps telling
    // the truth about what a `.mmd` file IS (and stays in step with
    // `classifyLibraryEntry`, which must keep answering `mermaid` because the
    // standalone Library preview of a `.mmd` file is a different surface and
    // is NOT in scope of this ruling). What changed is
    // `inlineEmbedTreatment`, which refuses to render it inline.
    //
    // If a later change instead makes this return `other`, the refusal
    // silently joins the `.zip`/`README` bucket and this test is the warning
    // that the decision moved somewhere less visible.
    expect(classifyEmbedKind('diagram.mmd')).toBe('mermaid')
    expect(classifyEmbedKind('flow.mermaid')).toBe('mermaid')
  })

  it('decides `link-only` for the mermaid kind at the one place the ruling is enforced', () => {
    // THE DIRECT ORACLE, and it is not redundant with the render assertions
    // above. Flipping this case to 'block-mount' changes NO rendered output
    // today — the promotion gate would let the node through, but
    // `KnowledgeMarkdownLink` has no mermaid dispatch branch, so it still
    // falls back to a link. That is precisely how "someone started finishing
    // the feature" would look to every render-level test in this file: like
    // nothing at all. Asserting the decision itself is what closes that.
    expect(inlineEmbedTreatment('mermaid')).toBe('link-only')

    // Paired positives, so a mutation that made this function return
    // 'link-only' for EVERYTHING would not satisfy the line above.
    expect(inlineEmbedTreatment('audio')).toBe('block-mount')
    expect(inlineEmbedTreatment('video')).toBe('block-mount')
    expect(inlineEmbedTreatment('image')).toBe('image-node')
    expect(inlineEmbedTreatment('pdf')).toBe('block-mount-if-page')
  })
})

describe('a ```mermaid FENCE is a different mechanism and is NOT affected by the ruling', () => {
  it('draws a fenced diagram in the very same note that refuses an embedded .mmd file', () => {
    renderNote('![[diagram.mmd]]\n\n```mermaid\ngraph TD\n  A --> B\n```\n')

    // The fence renders — 163 of these exist in the founder's vault
    // (ADR-083 §2.1) and the ruling was about embedded FILES, not fences.
    const diagram = screen.getByTestId('mermaid-diagram')
    expect(diagram).toHaveTextContent('graph TD')

    // …while the embed beside it is still only a link. Both facts in one
    // render, so no future change can satisfy one by breaking the other.
    expect(screen.getByTestId('markdown-link')).toHaveTextContent('diagram.mmd')
  })

  it('draws a fenced diagram in a note with no embeds at all', () => {
    renderNote('```mermaid\nsequenceDiagram\n  A->>B: hi\n```\n')

    expect(screen.getByTestId('mermaid-diagram')).toHaveTextContent('sequenceDiagram')
  })
})
