// Renderer for already-finalized assistant messages in the virtualized list.
// Mirrors markdown-text.tsx (the live-streaming AssistantUI renderer) so the
// post-streaming view matches the live view — same headings, lists, tables,
// blockquotes, inline code, link styling and rewrite. Block code uses a plain
// <pre><code> instead of Shiki to keep the historical bundle light.

import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'
import rehypeKatex from 'rehype-katex'
import 'katex/dist/katex.min.css'
import { rehypePhosphorEmoji } from '@/lib/rehype-phosphor-emoji'
import { rewriteLegacyURL, resolveEffectivePreview } from '@/lib/preview-url'
import { isSafeHref } from '@/lib/url-safe'
import { fetchAboutInfo } from '@/lib/api'
import { ImageLightbox } from './image-lightbox'
import { PHOSPHOR_EMOJI_ICONS } from '@/lib/phosphor-emoji-icons'
import type { ComponentPropsWithoutRef } from 'react'

// Icons resolved from an explicit allow-list (PHOSPHOR_EMOJI_ICONS) — never a
// wildcard `import * as` (which pulls the whole ~5MB icon set). See
// src/lib/phosphor-emoji-icons.tsx. Mirrors markdown-text.tsx.
function PhosphorEmojiSpan({ 'data-phosphor-icon': iconName, children, ...props }: ComponentPropsWithoutRef<'span'> & { 'data-phosphor-icon'?: string }) {
  const Icon = iconName ? PHOSPHOR_EMOJI_ICONS[iconName] : undefined
  if (Icon) {
    return <Icon size={14} weight="regular" className="inline-block align-middle text-[var(--color-accent)] mx-0.5" />
  }
  return <span {...props}>{children}</span>
}

function HistoricalImage({ src, alt }: ComponentPropsWithoutRef<'img'>) {
  const [open, setOpen] = useState(false)
  if (!src || typeof src !== 'string') return null
  if (!isSafeHref(src)) {
    return alt ? <span className="text-xs text-[var(--color-muted)] italic">[image: {alt}]</span> : null
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

export function HistoricalMessageMarkdown({ content }: { content: string }) {
  const { data: aboutInfo } = useQuery({ queryKey: ['about'], queryFn: fetchAboutInfo, staleTime: 5 * 60 * 1000 })
  const effectivePreview = resolveEffectivePreview(aboutInfo ?? null, typeof window !== 'undefined' ? window.location.hostname : '')

  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm, remarkMath]}
      rehypePlugins={[rehypeKatex, rehypePhosphorEmoji]}
      components={{
        // Paragraphs
        p: ({ children }) => (
          <p className="text-sm leading-relaxed text-[var(--color-secondary)] my-1.5 whitespace-pre-wrap">{children}</p>
        ),

        // Headings
        h1: ({ children }) => (
          <h1 className="text-xl font-bold text-[var(--color-secondary)] mt-5 mb-2 border-b border-[var(--color-border)] pb-1">{children}</h1>
        ),
        h2: ({ children }) => (
          <h2 className="text-lg font-semibold text-[var(--color-secondary)] mt-4 mb-2">{children}</h2>
        ),
        h3: ({ children }) => (
          <h3 className="text-base font-semibold text-[var(--color-secondary)] mt-3 mb-1">{children}</h3>
        ),

        // Lists
        ul: ({ children }) => (
          <ul style={{ listStyleType: 'disc' }} className="pl-6 my-2 space-y-1 text-[var(--color-secondary)]">{children}</ul>
        ),
        ol: ({ children }) => (
          <ol style={{ listStyleType: 'decimal' }} className="pl-6 my-2 space-y-1 text-[var(--color-secondary)]">{children}</ol>
        ),
        li: ({ children }) => (
          <li style={{ display: 'list-item' }} className="text-sm leading-relaxed">{children}</li>
        ),

        // Strong / em
        strong: ({ children }) => <strong className="font-semibold text-[var(--color-secondary)]">{children}</strong>,
        em: ({ children }) => <em className="italic">{children}</em>,

        // Blockquote
        blockquote: ({ children }) => (
          <blockquote className="border-l-2 border-[var(--color-accent)]/50 pl-3 my-2 text-[var(--color-muted)] italic">{children}</blockquote>
        ),

        // Tables (GFM)
        table: ({ children }) => (
          <div className="overflow-x-auto my-2">
            <table className="min-w-full text-xs border-collapse">{children}</table>
          </div>
        ),
        th: ({ children }) => (
          <th className="border border-[var(--color-border)] px-3 py-1.5 text-left font-semibold bg-[var(--color-surface-2)] text-[var(--color-secondary)]">{children}</th>
        ),
        td: ({ children }) => (
          <td className="border border-[var(--color-border)] px-3 py-1.5 text-[var(--color-secondary)]">{children}</td>
        ),

        // Horizontal rule
        hr: () => <hr className="my-4 border-[var(--color-border)]" />,

        // Code (inline + block — basic <pre><code>; Shiki only on live messages)
        code: ({ children, className }) => {
          const isBlock = typeof className === 'string' && className.includes('language-')
          if (isBlock) {
            return (
              <pre className="text-xs bg-[var(--color-surface-1)] rounded p-2 overflow-auto my-2 font-mono text-[var(--color-secondary)]">
                <code className={className}>{children}</code>
              </pre>
            )
          }
          return (
            <code className="font-mono text-[11px] bg-[var(--color-surface-2)] px-1.5 py-0.5 rounded text-[var(--color-accent)]">
              {children}
            </code>
          )
        },

        // Phosphor-emoji spans from rehypePhosphorEmoji
        span: PhosphorEmojiSpan,

        // Images: click-to-expand lightbox
        img: HistoricalImage,

        // Links: rewrite legacy 0.0.0.0/localhost URLs to the current host;
        // open in a new tab; reject unsafe schemes.
        a: ({ href, children }) => {
          const rewritten = effectivePreview
            ? rewriteLegacyURL(href ?? '', effectivePreview.hostname, effectivePreview.port)
            : (href ?? '')
          if (!isSafeHref(rewritten)) {
            return (
              <span title="Link removed: unsafe URL scheme" className="text-[var(--color-muted)] line-through decoration-dotted cursor-not-allowed">
                {children}
              </span>
            )
          }
          return (
            <a
              href={rewritten}
              target="_blank"
              rel="noopener noreferrer"
              className="text-[var(--color-accent)] underline underline-offset-2 hover:opacity-80 transition-opacity"
            >
              {children}
            </a>
          )
        },
      }}
    >
      {content}
    </ReactMarkdown>
  )
}
