// Renderer for already-finalized assistant messages in the virtualized list.
// Shares every element renderer with the live AssistantUI renderer (markdown-text.tsx)
// via markdown-shared.tsx — the two now differ ONLY in their block-code path: this one
// renders a plain <pre><code> (Shiki is live-only, to keep this bundle light), the live
// one uses Shiki. Both route a ```mermaid fence to the shared <MermaidDiagram>.

import { useState, useRef, useEffect, useMemo, memo } from 'react'
import type { ReactNode } from 'react'
import { Copy, Check } from '@phosphor-icons/react'
import { useQuery } from '@tanstack/react-query'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'
import rehypeKatex from 'rehype-katex'
import 'katex/dist/katex.min.css'
import { rehypePhosphorEmoji } from '@/lib/rehype-phosphor-emoji'
import { resolveEffectivePreview } from '@/lib/preview-url'
import { fetchAboutInfo } from '@/lib/api'
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
  commonMarkdownComponents,
} from './markdown-shared'

// ── Historical block-code header (language label + copy button) ───────────────
// Mirrors CopyCodeHeader from shiki-highlighter.tsx but uses copyText() from
// media-actions.ts (Phase A) rather than a raw clipboard call, and uses a 1.5s
// reset (vs 2s in the live path) to stay snappy in the finalized view.

interface HistoricalCodeBlockProps {
  code: string
  className: string | undefined
  children: ReactNode
  language: string | undefined
}

function HistoricalCodeBlock({ code, className, children, language }: HistoricalCodeBlockProps) {
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
      <pre className="text-xs bg-[var(--color-surface-1)] rounded-b p-2 overflow-auto font-mono text-[var(--color-secondary)]">
        <code className={className}>{children}</code>
      </pre>
    </div>
  )
}

// ── Perf (gate 5 MEDIUM): stable component-map entries ────────────────────────
// `pre`/`code` close over nothing from HistoricalMessageMarkdown's render
// scope (only module-level helpers: classifyFence/codeText/MermaidDiagram/
// HistoricalCodeBlock) — hoisting them to module scope gives react-markdown
// the SAME function reference for these node types on every render/instance,
// instead of a brand-new inline-arrow identity every render. react-markdown
// treats a components-map entry's reference as that node type's "component
// type": a changed reference forces React to unmount+remount every element
// of that type rather than update it in place, even though the rendered
// output is identical. `a` is the one entry that legitimately varies
// per-render (it closes over `effectivePreview`, sourced from the /about
// query + the current hostname) — it is memoized separately below, keyed on
// a MEMOIZED `effectivePreview` (see the component body: resolveEffectivePreview
// itself returns a new object every call, so effectivePreview must be
// useMemo'd first, or keying linkRenderer on it would never actually skip
// recomputation).
const preRenderer = ({ children }: { children?: ReactNode }) => <>{children}</>

const codeRenderer = ({ children, className }: { children?: ReactNode; className?: string }) => {
  const { isBlock, language, text } = classifyFence(children, className)
  if (!isBlock) return <InlineCode>{children}</InlineCode>
  if (language === 'mermaid') {
    // Fenced content carries a trailing newline; trim so mermaid parses cleanly.
    return <MermaidDiagram code={text.replace(/\n$/, '')} />
  }
  return (
    <HistoricalCodeBlock code={codeText(children)} className={className} language={language}>
      {children}
    </HistoricalCodeBlock>
  )
}

// Base components map — everything except `a` (per-render-dependent — see
// above) is stable across every render and every HistoricalMessageMarkdown
// instance, so it lives at module scope rather than being reallocated as a
// fresh object literal inside the component body on every render.
const baseMarkdownComponents = {
  // Shared element renderers (paragraphs, headings, lists, strong/em,
  // blockquote, tables, hr) — single source of truth in markdown-shared.tsx.
  ...commonMarkdownComponents,

  // `pre` is a pass-through: the `code` renderer below emits its OWN block
  // wrapper (or a MermaidDiagram), so the default <pre> would double-wrap it
  // (the nested <pre><pre> seen in the DOM). Let `code` own the block layout.
  pre: preRenderer,

  // Block code: a `language-mermaid` fence routes to the shared diagram (parity
  // with the live renderer); all other block fences render with a copy header.
  // classifyFence centralises the block/inline + language detection — block-ness
  // must NOT key solely on a `language-` class, or bare/indented fences collapse
  // (see regression test).
  code: codeRenderer,

  // Phosphor-emoji spans (from rehypePhosphorEmoji) and lightbox images — shared.
  span: PhosphorEmojiSpan,
  img: MarkdownImage,
}

function HistoricalMessageMarkdownInner({ content }: { content: string }) {
  const { data: aboutInfo } = useQuery({ queryKey: ['about'], queryFn: fetchAboutInfo, staleTime: 5 * 60 * 1000 })
  const windowHostname = typeof window !== 'undefined' ? window.location.hostname : ''
  // Perf (gate 5 MEDIUM): resolveEffectivePreview allocates a brand-new
  // {hostname, port} object (or null) on EVERY call, even when aboutInfo and
  // windowHostname are unchanged — so calling it inline (as before) gave
  // `effectivePreview` a new identity every render regardless of whether
  // anything it's derived from actually changed. Memoizing it here on its
  // real, primitive inputs (aboutInfo is itself a stable react-query `data`
  // reference across renders where the cache hasn't changed; windowHostname
  // is a stable string) is the prerequisite for `linkRenderer` below to ever
  // actually skip recomputation.
  const effectivePreview = useMemo(
    () => resolveEffectivePreview(aboutInfo ?? null, windowHostname),
    [aboutInfo, windowHostname],
  )
  // createLinkRenderer allocates a new closure per call — previously called
  // inline in the `components` object literal on every render, which handed
  // react-markdown a new "component type" for `a` on every render and
  // remounted every anchor in the message (losing any DOM-level state on
  // it, and doing needless work on messages with many links). Now stable
  // across renders where effectivePreview hasn't changed.
  const linkRenderer = useMemo(() => createLinkRenderer(effectivePreview), [effectivePreview])
  const components = useMemo(
    () => ({ ...baseMarkdownComponents, a: linkRenderer }),
    [linkRenderer],
  )

  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm, remarkMath]}
      rehypePlugins={[rehypeKatex, rehypePhosphorEmoji]}
      components={components}
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
