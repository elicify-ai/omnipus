// Shiki syntax highlighter adapter for @assistant-ui/react-markdown.
// AssistantUI calls SyntaxHighlighter for every code block; CopyCodeHeader
// renders the language label + copy button above each block.
// Special case: language "mermaid" renders MermaidDiagram instead of Shiki.
//
// The actual Shiki rendering (theme, styling) lives in markdown-shared.tsx's
// ShikiCodeBlock — shared with historical-markdown.tsx's finalized-message
// renderer (library-spec D-6) so highlighting doesn't vanish when a message
// finalizes or the page reloads. Only the mermaid/useMessage streaming-status
// branch stays here — see ShikiCodeBlock's doc comment for why it can't share.

import { useState, useRef, useEffect } from 'react'
import { Copy, Check } from '@phosphor-icons/react'
import { useMessage } from '@assistant-ui/react'
import type { SyntaxHighlighterProps, CodeHeaderProps } from '@assistant-ui/react-markdown'
import { MermaidDiagram } from './mermaid-renderer'
import { ShikiCodeBlock } from './markdown-shared'
import { useUiStore } from '@/store/ui'

// ── Live mermaid block ────────────────────────────────────────────────────────
// On the live streaming path the diagram code arrives token-by-token, so the partial
// fence fails to parse on every keystroke. Pass the message's streaming status to
// MermaidDiagram so it renders NOTHING until the block is complete and the SVG is
// ready (no naked source, no error flicker). useMessage is safe here: SyntaxHighlighter
// is only ever rendered by markdown-text.tsx, inside an AssistantUI message context.

function LiveMermaidBlock({ code }: { code: string }) {
  const message = useMessage()
  const streaming = message.status?.type === 'running'
  return <MermaidDiagram code={code} streaming={streaming} />
}

// ── Syntax highlighter ────────────────────────────────────────────────────────

export function SyntaxHighlighter({ language, code }: Omit<SyntaxHighlighterProps, 'node'>) {
  if (language === 'mermaid') {
    return <LiveMermaidBlock code={code} />
  }

  return <ShikiCodeBlock language={language} code={code} />
}

// ── Copy button header ────────────────────────────────────────────────────────

export function CopyCodeHeader({ language, code }: Omit<CodeHeaderProps, 'node'>) {
  const [copied, setCopied] = useState(false)
  const resetTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    return () => {
      if (resetTimerRef.current) clearTimeout(resetTimerRef.current)
    }
  }, [])

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(code)
      setCopied(true)
      // Clear any existing reset timer before starting a new one
      if (resetTimerRef.current) clearTimeout(resetTimerRef.current)
      resetTimerRef.current = setTimeout(() => setCopied(false), 2000)
    } catch {
      useUiStore.getState().addToast({ message: 'Could not copy to clipboard', variant: 'error' })
    }
  }

  return (
    <div className="flex items-center justify-between px-3 py-1.5 bg-[var(--color-surface-2)] border-b border-[var(--color-border)] rounded-t-md">
      <span className="text-[10px] text-[var(--color-muted)] font-mono uppercase tracking-wide">
        {language || 'code'}
      </span>
      <button tabIndex={0}
        type="button"
        onClick={handleCopy}
        className="flex items-center gap-1 text-[10px] text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors"
        aria-label="Copy code to clipboard"
      >
        {copied ? (
          <>
            <Check size={11} weight="bold" className="text-[var(--color-success)]" />
            <span className="text-[var(--color-success)]">Copied!</span>
          </>
        ) : (
          <>
            <Copy size={11} />
            <span>Copy</span>
          </>
        )}
      </button>
    </div>
  )
}
