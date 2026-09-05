/**
 * shiki-highlighter.tsx — routing test: the chat CAN render Mermaid.
 *
 * AssistantUI calls SyntaxHighlighter for every fenced code block in an agent
 * message. This verifies the language routing: a ```mermaid fence renders via
 * LiveMermaidBlock → MermaidDiagram; any other language renders the Shiki code
 * block. LiveMermaidBlock reads the message's streaming status (useMessage) and
 * forwards it to MermaidDiagram so the live path renders nothing until the block
 * is complete.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { SyntaxHighlighterProps } from '@assistant-ui/react-markdown'
import { SyntaxHighlighter } from './shiki-highlighter'

// SyntaxHighlighter only reads { language, code }; `components` is a required prop
// of the AssistantUI type that the component ignores. Build minimal valid props.
function props(language: string, code: string): Omit<SyntaxHighlighterProps, 'node'> {
  return { language, code, components: {} } as Omit<SyntaxHighlighterProps, 'node'>
}

// Mutable message status the mocked useMessage reports (hoisted so the vi.mock
// factory can read it). Tests flip it to exercise streaming vs complete.
const msg = vi.hoisted(() => ({ status: 'complete' as string }))
vi.mock('@assistant-ui/react', () => ({
  useMessage: () => ({ status: { type: msg.status } }),
}))

// Sentinel for the diagram renderer: capture both the code AND the streaming flag
// LiveMermaidBlock forwards.
vi.mock('./mermaid-renderer', () => ({
  MermaidDiagram: ({ code, streaming }: { code: string; streaming?: boolean }) => (
    <div data-testid="mermaid-diagram" data-streaming={String(!!streaming)}>
      {code}
    </div>
  ),
}))
// Sentinel for the Shiki code-block path.
vi.mock('react-shiki', () => ({
  ShikiHighlighter: ({ children }: { children: React.ReactNode }) => (
    <pre data-testid="shiki">{children}</pre>
  ),
  // markdown-shared.tsx passes Shiki its pure-JS regex engine (the SPA's CSP
  // refuses the WebAssembly default); the module is mocked here, so this only
  // has to exist.
  createJavaScriptRegexEngine: () => ({}),
}))
// The module imports useUiStore (for CopyCodeHeader); stub it so the import resolves.
vi.mock('@/store/ui', () => ({ useUiStore: vi.fn() }))

beforeEach(() => {
  msg.status = 'complete'
})

describe('SyntaxHighlighter — Mermaid routing', () => {
  it('routes language="mermaid" to MermaidDiagram with the fence code', () => {
    render(<SyntaxHighlighter {...props('mermaid', 'graph TD; A-->B')} />)

    const diagram = screen.getByTestId('mermaid-diagram')
    expect(diagram).toBeInTheDocument()
    expect(diagram).toHaveTextContent('graph TD; A-->B')
    // It must NOT fall through to the Shiki code block.
    expect(screen.queryByTestId('shiki')).toBeNull()
  })

  it('forwards streaming=true while the message is running (render-nothing-until-complete)', () => {
    msg.status = 'running'
    render(<SyntaxHighlighter {...props('mermaid', 'graph TD; A-->B')} />)

    expect(screen.getByTestId('mermaid-diagram')).toHaveAttribute('data-streaming', 'true')
  })

  it('forwards streaming=false once the message is complete', () => {
    msg.status = 'complete'
    render(<SyntaxHighlighter {...props('mermaid', 'graph TD; A-->B')} />)

    expect(screen.getByTestId('mermaid-diagram')).toHaveAttribute('data-streaming', 'false')
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
