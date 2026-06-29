// MarkdownText — AssistantUI-aware markdown renderer (the LIVE streaming path).
// Reads text from MessagePrimitive context (no children prop needed).
// Uses MarkdownTextPrimitive from @assistant-ui/react-markdown with:
//   • Shiki syntax highlighting (vitesse-dark) + Mermaid diagram rendering
//   • Copy button on code blocks
//   • remark-gfm (tables, strikethrough, task lists)
//   • remark-math + rehype-katex (LaTeX/math rendering)
//   • rehype-phosphor-emoji (emoji → Phosphor icons)
//   • Image lightbox (click to expand)
//   • Sovereign Deep styling for inline code and links
//
// Every element renderer EXCEPT the block-code path (Shiki, here) comes from
// markdown-shared.tsx, shared verbatim with the finalized renderer
// (historical-markdown.tsx) so the live and reloaded views stay in parity.

import { memo } from 'react'
import { useQuery } from '@tanstack/react-query'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'
import rehypeKatex from 'rehype-katex'
import 'katex/dist/katex.min.css'
import {
  MarkdownTextPrimitive,
  unstable_memoizeMarkdownComponents as memoizeMarkdownComponents,
} from '@assistant-ui/react-markdown'
import { SyntaxHighlighter, CopyCodeHeader } from './shiki-highlighter'
import { rehypePhosphorEmoji } from '@/lib/rehype-phosphor-emoji'
import { resolveEffectivePreview } from '@/lib/preview-url'
import { fetchAboutInfo } from '@/lib/api'
import { PhosphorEmojiSpan, MarkdownImage, InlineCode, createLinkRenderer, commonMarkdownComponents } from './markdown-shared'

// ── Static component map (all renderers except `a`) ──────────────────────────
// memoizeMarkdownComponents wraps each renderer with React.memo and compares
// the AST node for bailout — this is performance-critical for streaming.
//
// The `a` renderer is NOT in this static map because it needs the `previewPort`
// value from /api/v1/about, which must be read inside a React component via
// useQuery. We pass a per-render `a` renderer via the `components` prop on
// MarkdownTextPrimitive; all other renderers come from this shared memoized map.

const staticMarkdownComponents = memoizeMarkdownComponents({
  // Shiki-powered block code (replaces default <pre><code> rendering).
  // Also handles language="mermaid" by routing to the shared MermaidDiagram.
  SyntaxHighlighter,

  // Language label + copy button above each code block
  CodeHeader: CopyCodeHeader,

  // Inline code (block code goes through SyntaxHighlighter) — shared.
  code: InlineCode,

  // Shared element renderers (headings, lists, paragraphs, strong/em, blockquote,
  // tables, hr) — single source of truth in markdown-shared.tsx.
  ...commonMarkdownComponents,

  // Span renderer: intercepts data-phosphor-icon spans from rehypePhosphorEmoji.
  span: PhosphorEmojiSpan,

  // Images: click-to-expand lightbox.
  img: MarkdownImage,
})

// ── MarkdownText component ────────────────────────────────────────────────────
// Usage: <MarkdownText /> inside MessagePrimitive.Parts (reads text from context).
//
// The `a` renderer is built inside the component so it can call useQuery to
// read the previewPort from /api/v1/about. The rewrite is skipped entirely
// when previewPort is falsy (aboutInfo not yet loaded), so port 0 is never
// substituted into a URL (which would produce ERR_UNSAFE_PORT). After
// aboutInfo loads and the component re-renders, the correct port is applied.

function MarkdownTextImpl() {
  const { data: aboutInfo } = useQuery({
    queryKey: ['about'],
    queryFn: fetchAboutInfo,
    staleTime: 5 * 60 * 1000,
  })

  // resolveEffectivePreview returns null when neither preview_origin nor
  // preview_port are usable; in that case we pass href through unchanged.
  // Substituting port 0 would produce ERR_UNSAFE_PORT (F-16); the helper
  // guards against that and against the preview_origin-yields-zero-port case.
  const effectivePreview = resolveEffectivePreview(
    aboutInfo ?? null,
    typeof window !== 'undefined' ? window.location.hostname : '',
  )

  const markdownComponents = {
    ...staticMarkdownComponents,
    // Links: legacy-host rewrite + scheme allow-list — shared with the finalized
    // renderer. Built per-render because it closes over effectivePreview.
    a: createLinkRenderer(effectivePreview),
  }

  return (
    <MarkdownTextPrimitive
      remarkPlugins={[remarkGfm, remarkMath]}
      rehypePlugins={[rehypeKatex, rehypePhosphorEmoji]}
      className="prose-sm prose-invert max-w-none text-[var(--color-secondary)] leading-relaxed"
      components={markdownComponents}
      smooth
    />
  )
}

export const MarkdownText = memo(MarkdownTextImpl)
