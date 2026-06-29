/**
 * shiki-highlighter.tsx — routing test: the chat CAN render Mermaid.
 *
 * AssistantUI calls SyntaxHighlighter for every fenced code block in an agent
 * message. This verifies the language routing: a ```mermaid fence renders
 * MermaidDiagram; any other language renders the Shiki code block. This is the
 * wiring that lets the chat render Mermaid diagrams from markdown.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { SyntaxHighlighterProps } from '@assistant-ui/react-markdown'
import { SyntaxHighlighter } from './shiki-highlighter'

// SyntaxHighlighter only reads { language, code }; `components` is a required prop
// of the AssistantUI type that the component ignores. Build minimal valid props.
function props(language: string, code: string): Omit<SyntaxHighlighterProps, 'node'> {
  return { language, code, components: {} } as Omit<SyntaxHighlighterProps, 'node'>
}

// Sentinel for the diagram renderer so we can assert it was chosen + got the code.
vi.mock('./mermaid-renderer', () => ({
  MermaidDiagram: ({ code }: { code: string }) => <div data-testid="mermaid-diagram">{code}</div>,
}))
// Sentinel for the Shiki code-block path.
vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children: React.ReactNode }) => (
    <pre data-testid="shiki">{children}</pre>
  ),
}))
// The module imports useUiStore (for CopyCodeHeader); stub it so the import resolves.
vi.mock('@/store/ui', () => ({ useUiStore: vi.fn() }))

describe('SyntaxHighlighter — Mermaid routing', () => {
  it('routes language="mermaid" to MermaidDiagram with the fence code', () => {
    render(<SyntaxHighlighter {...props('mermaid', 'graph TD; A-->B')} />)

    const diagram = screen.getByTestId('mermaid-diagram')
    expect(diagram).toBeInTheDocument()
    expect(diagram).toHaveTextContent('graph TD; A-->B')
    // It must NOT fall through to the Shiki code block.
    expect(screen.queryByTestId('shiki')).toBeNull()
  })

  it('routes a non-mermaid language to the Shiki code block (not MermaidDiagram)', () => {
    render(<SyntaxHighlighter {...props('ts', 'const x = 1')} />)

    expect(screen.getByTestId('shiki')).toBeInTheDocument()
    expect(screen.queryByTestId('mermaid-diagram')).toBeNull()
  })

  it('routes an empty/absent language to Shiki (default), never Mermaid', () => {
    render(<SyntaxHighlighter {...props('', 'plain text')} />)

    expect(screen.getByTestId('shiki')).toBeInTheDocument()
    expect(screen.queryByTestId('mermaid-diagram')).toBeNull()
  })
})
