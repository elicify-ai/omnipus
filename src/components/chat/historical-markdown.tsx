// Renderer for already-finalized assistant messages in the virtualized list.
// Shares every element renderer with the live AssistantUI renderer (markdown-text.tsx)
// via markdown-shared.tsx — including, as of the library-spec D-6 fix, the actual
// Shiki highlighting mechanism for block code (ShikiCodeBlock). The two renderers now
// differ ONLY in the chrome around block code: this one uses its own copyText()-based
// header (see HistoricalCodeBlock below), the live one uses AssistantUI's CodeHeader
// slot. Both route a ```mermaid fence to the shared <MermaidDiagram>.

import { useState, useRef, useEffect, memo } from 'react'
import type { ReactNode } from 'react'
import { Copy, Check } from '@phosphor-icons/react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'
import rehypeKatex from 'rehype-katex'
import 'katex/dist/katex.min.css'
import { rehypePhosphorEmoji } from '@/lib/rehype-phosphor-emoji'
import { useUiStore } from '@/store/ui'
import { copyText } from './media-actions'
import {
  PhosphorEmojiSpan,
  MarkdownImage,
  createLinkRenderer,
  InlineCode,
  classifyFence,
  codeText,
  MermaidDiagram,
  ShikiCodeBlock,
  commonMarkdownComponents,
} from './markdown-shared'

// ── Historical block-code header (language label + copy button) ───────────────
// Mirrors CopyCodeHeader from shiki-highlighter.tsx but uses copyText() from
// media-actions.ts (Phase A) rather than a raw clipboard call, and uses a 1.5s
// reset (vs 2s in the live path) to stay snappy in the finalized view. The
// highlighted body itself is ShikiCodeBlock (markdown-shared.tsx) — shared with
// the live path so highlighting no longer vanishes on finalize/reload.

interface HistoricalCodeBlockProps {
  code: string
  language: string | undefined
}

function HistoricalCodeBlock({ code, language }: HistoricalCodeBlockProps) {
  const [copied, setCopied] = useState(false)
  const resetTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    return () => {
      if (resetTimerRef.current) clearTimeout(resetTimerRef.current)
    }
  }, [])

  const handleCopy = async () => {
    try {
      await copyText(code)
      setCopied(true)
      if (resetTimerRef.current) clearTimeout(resetTimerRef.current)
      resetTimerRef.current = setTimeout(() => setCopied(false), 1500)
    } catch {
      useUiStore.getState().addToast({ message: 'Could not copy', variant: 'error' })
    }
  }

  return (
    <div className="my-2 rounded overflow-hidden">
      <div className="flex items-center justify-between px-3 py-1.5 bg-[var(--color-surface-2)] border-b border-[var(--color-border)] rounded-t">
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
      <ShikiCodeBlock language={language} code={code} />
    </div>
  )
}

// ── Perf (gate 5 MEDIUM, kept through the ADR-044 merge): the components map
// is fully static now — ADR-044 deleted the preview-host rewrite (no /about
// query, no per-render `a` variance; createLinkRenderer(null) is the shared
// "no rewrite" case), so the WHOLE map hoists to module scope. react-markdown
// treats each entry's reference as that node type's component type: a stable
// reference updates in place, a fresh inline object would remount every
// element of every type on each render.
const historicalMarkdownComponents = {
  // Shared element renderers (paragraphs, headings, lists, strong/em,
  // blockquote, tables, hr) — single source of truth in markdown-shared.tsx.
  ...commonMarkdownComponents,

  // `pre` is a pass-through: the `code` renderer below emits its OWN block
  // wrapper (or a MermaidDiagram), so the default <pre> would double-wrap it
  // (the nested <pre><pre> seen in the DOM). Let `code` own the block layout.
  pre: ({ children }: { children?: ReactNode }) => <>{children}</>,

  // Block code: a `language-mermaid` fence routes to the shared diagram (parity
  // with the live renderer); all other block fences render with a copy header.
  // classifyFence centralises the block/inline + language detection — block-ness
  // must NOT key solely on a `language-` class, or bare/indented fences collapse
  // (see regression test).
  code: ({ children, className }: { children?: ReactNode; className?: string }) => {
    const { isBlock, language, text } = classifyFence(children, className)
    if (!isBlock) return <InlineCode>{children}</InlineCode>
    if (language === 'mermaid') {
      // Fenced content carries a trailing newline; trim so mermaid parses cleanly.
      return <MermaidDiagram code={text.replace(/\n$/, '')} />
    }
    return <HistoricalCodeBlock code={codeText(children)} language={language} />
  },

  // Phosphor-emoji spans (from rehypePhosphorEmoji) and lightbox images — shared.
  span: PhosphorEmojiSpan,
  img: MarkdownImage,

  // Links: scheme allow-list only. Since ADR-044 (preview-on-main-listener),
  // `/preview/` shares the SPA's own origin — links render exactly as the
  // backend returned them (createLinkRenderer(null) is the shared "no rewrite"
  // case in markdown-shared.tsx — mirrors markdown-text.tsx).
  a: createLinkRenderer(null),
}

function HistoricalMessageMarkdownInner({ content }: { content: string }) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm, remarkMath]}
      rehypePlugins={[rehypeKatex, rehypePhosphorEmoji]}
      components={historicalMarkdownComponents}
    >
      {content}
    </ReactMarkdown>
  )
}

// Perf (gate 5 MEDIUM): memoized on `content` (the component's only prop —
// a primitive string, compared by value). Callers (VirtualAssistantMessageRow,
// via splitMessageParts) already hand each text slice a stable string across
// re-renders where that slice's text hasn't changed, so this skips a full
// react-markdown re-parse + re-render of every OTHER, unrelated message row
// whenever a single row's data changes elsewhere in the (virtualized) list.
export const HistoricalMessageMarkdown = memo(HistoricalMessageMarkdownInner)
