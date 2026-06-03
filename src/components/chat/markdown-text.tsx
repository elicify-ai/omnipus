// MarkdownText — AssistantUI-aware markdown renderer.
// Reads text from MessagePrimitive context (no children prop needed).
// Uses MarkdownTextPrimitive from @assistant-ui/react-markdown with:
//   • Shiki syntax highlighting (vitesse-dark) + Mermaid diagram rendering
//   • Copy button on code blocks
//   • remark-gfm (tables, strikethrough, task lists)
//   • remark-math + rehype-katex (LaTeX/math rendering)
//   • rehype-phosphor-emoji (emoji → Phosphor icons)
//   • Image lightbox (click to expand)
//   • Sovereign Deep styling for inline code and links

import { memo, useState } from 'react'
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
import { ImageLightbox } from './image-lightbox'
import { rehypePhosphorEmoji } from '@/lib/rehype-phosphor-emoji'
import { rewriteLegacyURL, resolveEffectivePreview } from '@/lib/preview-url'
import { isSafeHref } from '@/lib/url-safe'
import { fetchAboutInfo } from '@/lib/api'
import { PHOSPHOR_EMOJI_ICONS } from '@/lib/phosphor-emoji-icons'
import type { ComponentPropsWithoutRef } from 'react'

// ── Phosphor icon span renderer ───────────────────────────────────────────────
// Renders <span data-phosphor-icon="IconName"> as the corresponding Phosphor icon.
// Icons are resolved from an explicit allow-list (PHOSPHOR_EMOJI_ICONS) rather
// than a wildcard `import * as` — see src/lib/phosphor-emoji-icons.tsx. An
// unknown name falls through to the original span (preserving prior behavior:
// only names present in the icon set were ever rendered as icons).

function PhosphorEmojiSpan({ 'data-phosphor-icon': iconName, children, ...props }: ComponentPropsWithoutRef<'span'> & { 'data-phosphor-icon'?: string }) {
  const Icon = iconName ? PHOSPHOR_EMOJI_ICONS[iconName] : undefined
  if (Icon) {
    return <Icon size={14} weight="regular" className="inline-block align-middle text-[var(--color-accent)] mx-0.5" />
  }
  return <span {...props}>{children}</span>
}

// ── Image renderer with lightbox ──────────────────────────────────────────────

function MarkdownImage({ src, alt }: ComponentPropsWithoutRef<'img'>) {
  const [open, setOpen] = useState(false)
  if (!src) return null
  // Reject unsafe src schemes (javascript:, data:, etc.) — V2.C / FE H1
  if (!isSafeHref(src)) {
    if (alt) {
      return (
        <span className="text-xs text-[var(--color-muted)] italic">[image: {alt}]</span>
      )
    }
    return null
  }
  return (
    <>
      <img
        src={src}
        alt={alt || ''}
        loading="lazy"
        className="max-w-full rounded-md cursor-zoom-in border border-[var(--color-border)] hover:border-[var(--color-accent)]/50 transition-colors"
        onClick={() => setOpen(true)}
        role="button"
        tabIndex={0}
        onKeyDown={(e) => e.key === 'Enter' && setOpen(true)}
        aria-label={alt ? `View: ${alt}` : 'View image'}
      />
      {open && <ImageLightbox src={src} alt={alt} onClose={() => setOpen(false)} />}
    </>
  )
}

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
  // Also handles language="mermaid" by routing to MermaidDiagram.
  SyntaxHighlighter,

  // Language label + copy button above each code block
  CodeHeader: CopyCodeHeader,

  // Inline code (distinct from block code, which goes through SyntaxHighlighter)
  code: ({ children, ...props }) => (
    <code
      {...props}
      className="font-mono text-[11px] bg-[var(--color-surface-2)] px-1.5 py-0.5 rounded text-[var(--color-accent)]"
    >
      {children}
    </code>
  ),

  // Lists — explicit styles since Tailwind v4 doesn't include @tailwindcss/typography prose by default
  ul: ({ children, ...props }) => (
    <ul {...props} style={{ listStyleType: 'disc' }} className="pl-6 my-2 space-y-1 text-[var(--color-secondary)]">{children}</ul>
  ),
  ol: ({ children, ...props }) => (
    <ol {...props} style={{ listStyleType: 'decimal' }} className="pl-6 my-2 space-y-1 text-[var(--color-secondary)]">{children}</ol>
  ),
  li: ({ children, ...props }) => (
    <li {...props} style={{ display: 'list-item' }} className="text-sm leading-relaxed">{children}</li>
  ),

  // Headings — sized distinctly from body text
  h1: ({ children, ...props }) => (
    <h1 {...props} className="text-xl font-bold text-[var(--color-secondary)] mt-5 mb-2 border-b border-[var(--color-border)] pb-1">{children}</h1>
  ),
  h2: ({ children, ...props }) => (
    <h2 {...props} className="text-lg font-semibold text-[var(--color-secondary)] mt-4 mb-2">{children}</h2>
  ),
  h3: ({ children, ...props }) => (
    <h3 {...props} className="text-base font-semibold text-[var(--color-secondary)] mt-3 mb-1">{children}</h3>
  ),

  // Paragraphs
  p: ({ children, ...props }) => (
    <p {...props} className="text-sm leading-relaxed my-1.5">{children}</p>
  ),

  // Blockquotes
  blockquote: ({ children, ...props }) => (
    <blockquote {...props} className="border-l-2 border-[var(--color-accent)]/50 pl-3 my-2 text-[var(--color-muted)] italic">{children}</blockquote>
  ),

  // Tables
  table: ({ children, ...props }) => (
    <div className="overflow-x-auto my-2">
      <table {...props} className="min-w-full text-xs border-collapse">{children}</table>
    </div>
  ),
  th: ({ children, ...props }) => (
    <th {...props} className="border border-[var(--color-border)] px-3 py-1.5 text-left font-semibold bg-[var(--color-surface-2)] text-[var(--color-secondary)]">{children}</th>
  ),
  td: ({ children, ...props }) => (
    <td {...props} className="border border-[var(--color-border)] px-3 py-1.5 text-[var(--color-secondary)]">{children}</td>
  ),

  // Horizontal rule
  hr: (props) => (
    <hr {...props} className="my-4 border-[var(--color-border)]" />
  ),

  // Span renderer: intercepts data-phosphor-icon spans from rehypePhosphorEmoji
  span: PhosphorEmojiSpan,

  // Images: click-to-expand lightbox
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
    a: ({ href, children, ...props }: ComponentPropsWithoutRef<'a'>) => {
      const rewritten = effectivePreview
        ? rewriteLegacyURL(href ?? '', effectivePreview.hostname, effectivePreview.port)
        : (href ?? '')
      // Scheme allow-list: reject javascript:, data:, vbscript:, etc. — V2.C / FE H1.
      // Sanitized links render as plain text with a subtle visual cue; no href is set.
      if (!isSafeHref(rewritten)) {
        return (
          <span
            data-testid="markdown-link"
            title="Link removed: unsafe URL scheme"
            className="text-[var(--color-muted)] line-through decoration-dotted cursor-not-allowed"
          >
            {children}
          </span>
        )
      }
      return (
        <a
          {...props}
          href={rewritten}
          target="_blank"
          rel="noopener noreferrer"
          data-testid="markdown-link"
          className="text-[var(--color-accent)] underline underline-offset-2 hover:opacity-80 transition-opacity"
        >
          {children}
        </a>
      )
    },
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
