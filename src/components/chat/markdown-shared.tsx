// markdown-shared.tsx — single source of truth for the markdown element renderers
// used by BOTH chat markdown paths:
//   • markdown-text.tsx       — the live AssistantUI streaming renderer
//   • historical-markdown.tsx — the finalized / reloaded / virtualized renderer
//
// These two renderers exist because the live path is bound to AssistantUI's
// MarkdownTextPrimitive (reads text from context, Shiki block code) while the
// finalized path is plain react-markdown over a content string (plain <pre> block
// code) — they cannot collapse into one component. But everything that is NOT the
// block-code mechanism (paragraphs, headings, lists, inline code, links, images,
// emoji spans, and the mermaid routing) is identical and MUST stay in parity. Keeping
// two hand-maintained copies drifted three times (mermaid missing when finalized, the
// languageless-fence collapse, and silent strong/em + paragraph-whitespace divergence).
// This module is that shared definition; the two renderers now differ ONLY in their
// block-code path.
//
// Renderers take `({ children })` (and the few element-specific attrs) and do NOT
// spread arbitrary props: react-markdown passes a `node` prop that must not reach the
// DOM, and AssistantUI's metadata is not needed on the element. Memoization for the
// live path is handled by memoizeMarkdownComponents wrapping these — not by the
// renderer — so no prop forwarding is required here.

import { useState } from 'react'
import type { ComponentPropsWithoutRef, ReactNode } from 'react'
import { ImageLightbox } from './image-lightbox'
import { MermaidDiagram } from './mermaid-renderer'
import { rewriteLegacyURL, resolveEffectivePreview } from '@/lib/preview-url'
import { isSafeHref } from '@/lib/url-safe'
import { PHOSPHOR_EMOJI_ICONS } from '@/lib/phosphor-emoji-icons'

type EffectivePreview = ReturnType<typeof resolveEffectivePreview>

// ── Phosphor-emoji span ───────────────────────────────────────────────────────
// Renders <span data-phosphor-icon="IconName"> (emitted by rehypePhosphorEmoji) as
// the corresponding Phosphor icon. Icons resolve from an explicit allow-list
// (PHOSPHOR_EMOJI_ICONS) — never a wildcard `import * as`, which pulls the whole
// ~5MB icon set. An unknown name falls through to the original span.
export function PhosphorEmojiSpan({
  'data-phosphor-icon': iconName,
  children,
  ...props
}: ComponentPropsWithoutRef<'span'> & { 'data-phosphor-icon'?: string }) {
  const Icon = iconName
    ? (PHOSPHOR_EMOJI_ICONS as Record<string, (typeof PHOSPHOR_EMOJI_ICONS)[keyof typeof PHOSPHOR_EMOJI_ICONS] | undefined>)[
        iconName
      ]
    : undefined
  if (Icon) {
    return <Icon size={14} weight="regular" className="inline-block align-middle text-[var(--color-accent)] mx-0.5" />
  }
  return <span {...props}>{children}</span>
}

// ── Image with click-to-expand lightbox ───────────────────────────────────────
// Unsafe src schemes (javascript:, data:, …) are rejected: an alt becomes a muted
// "[image: alt]" placeholder, no alt renders nothing. Keyboard-accessible.
export function MarkdownImage({ src, alt }: ComponentPropsWithoutRef<'img'>) {
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

// ── Link renderer (preview-host rewrite + scheme allow-list) ──────────────────
// Built per-render because it closes over effectivePreview (from /api/v1/about).
// Rewrites legacy 0.0.0.0/localhost hosts to the current preview host, opens in a
// new tab, and renders unsafe schemes as struck-through plain text (no href).
export function createLinkRenderer(effectivePreview: EffectivePreview) {
  return function MarkdownLink({ href, children }: ComponentPropsWithoutRef<'a'>) {
    const rewritten = effectivePreview
      ? rewriteLegacyURL(href ?? '', effectivePreview.hostname, effectivePreview.port)
      : (href ?? '')
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
        href={rewritten}
        target="_blank"
        rel="noopener noreferrer"
        data-testid="markdown-link"
        className="text-[var(--color-accent)] underline underline-offset-2 hover:opacity-80 transition-opacity"
      >
        {children}
      </a>
    )
  }
}

// ── Inline code ───────────────────────────────────────────────────────────────
export function InlineCode({ children }: { children?: ReactNode }) {
  return (
    <code className="font-mono text-[11px] bg-[var(--color-surface-2)] px-1.5 py-0.5 rounded text-[var(--color-accent)]">
      {children}
    </code>
  )
}

// ── Fenced-code routing (the parity-critical part) ────────────────────────────
// codeText robustly extracts the text of react-markdown `children`. Today a fence
// is delivered as a single string, but a future plugin could produce an array or
// element; String(node) would yield "[object Object]" — this recurses arrays and
// ignores non-string leaves.
export function codeText(node: ReactNode): string {
  if (typeof node === 'string') return node
  if (Array.isArray(node)) return node.map(codeText).join('')
  return ''
}

export interface FenceInfo {
  isBlock: boolean
  language: string | undefined
  text: string
}

// classifyFence decides, for a react-markdown `code` node, whether it is block or
// inline and what its language is. Block-ness must NOT key solely on a `language-`
// class: a bare fence (no language) or a 4-space indented block produces a `code`
// node with no className, so it is detected via a newline in the content. Used by
// the finalized renderer; the live renderer receives a pre-parsed {language, code}
// from AssistantUI and does not need this. Both ultimately route language="mermaid"
// to the shared <MermaidDiagram>.
export function classifyFence(children: ReactNode, className: string | undefined): FenceInfo {
  const text = codeText(children)
  const hasLanguageClass = typeof className === 'string' && className.includes('language-')
  const isBlock = hasLanguageClass || text.includes('\n')
  const language = hasLanguageClass ? /language-([\w-]+)/.exec(className as string)?.[1] : undefined
  return { isBlock, language, text }
}

// Re-export the shared diagram component so both renderers route mermaid through one
// place (the only block-code piece that is identical across the two paths).
export { MermaidDiagram }

// ── Common block-level element renderers ──────────────────────────────────────
// Canonical styles (the superset previously only fully present in the finalized
// renderer): paragraphs preserve intentional whitespace and use the secondary text
// color; strong/em are styled; everything else matches. Spread into BOTH renderers'
// component maps so they cannot drift again.
export const commonMarkdownComponents = {
  p: ({ children }: { children?: ReactNode }) => (
    <p className="text-sm leading-relaxed text-[var(--color-secondary)] my-1.5 whitespace-pre-wrap">{children}</p>
  ),

  h1: ({ children }: { children?: ReactNode }) => (
    <h1 className="text-xl font-bold text-[var(--color-secondary)] mt-5 mb-2 border-b border-[var(--color-border)] pb-1">
      {children}
    </h1>
  ),
  h2: ({ children }: { children?: ReactNode }) => (
    <h2 className="text-lg font-semibold text-[var(--color-secondary)] mt-4 mb-2">{children}</h2>
  ),
  h3: ({ children }: { children?: ReactNode }) => (
    <h3 className="text-base font-semibold text-[var(--color-secondary)] mt-3 mb-1">{children}</h3>
  ),

  ul: ({ children }: { children?: ReactNode }) => (
    <ul style={{ listStyleType: 'disc' }} className="pl-6 my-2 space-y-1 text-[var(--color-secondary)]">
      {children}
    </ul>
  ),
  ol: ({ children }: { children?: ReactNode }) => (
    <ol style={{ listStyleType: 'decimal' }} className="pl-6 my-2 space-y-1 text-[var(--color-secondary)]">
      {children}
    </ol>
  ),
  li: ({ children }: { children?: ReactNode }) => (
    <li style={{ display: 'list-item' }} className="text-sm leading-relaxed">
      {children}
    </li>
  ),

  strong: ({ children }: { children?: ReactNode }) => (
    <strong className="font-semibold text-[var(--color-secondary)]">{children}</strong>
  ),
  em: ({ children }: { children?: ReactNode }) => <em className="italic">{children}</em>,

  blockquote: ({ children }: { children?: ReactNode }) => (
    <blockquote className="border-l-2 border-[var(--color-accent)]/50 pl-3 my-2 text-[var(--color-muted)] italic">
      {children}
    </blockquote>
  ),

  table: ({ children }: { children?: ReactNode }) => (
    <div className="overflow-x-auto my-2">
      <table className="min-w-full text-xs border-collapse">{children}</table>
    </div>
  ),
  th: ({ children }: { children?: ReactNode }) => (
    <th className="border border-[var(--color-border)] px-3 py-1.5 text-left font-semibold bg-[var(--color-surface-2)] text-[var(--color-secondary)]">
      {children}
    </th>
  ),
  td: ({ children }: { children?: ReactNode }) => (
    <td className="border border-[var(--color-border)] px-3 py-1.5 text-[var(--color-secondary)]">{children}</td>
  ),

  hr: () => <hr className="my-4 border-[var(--color-border)]" />,
}
