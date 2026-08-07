// UntrustedChildText.tsx — FE-7 / MAJ-12 React rendering face for child text.
//
// Wraps the pure `sanitizeChildText` utility (src/lib/sanitizeChildText.ts)
// in a small component that:
//   • renders the sanitized text as PLAIN TEXT (cheap path, no markdown
//     parse) when it carries no markdown markup, else as SANCTIONED MARKDOWN
//     via react-markdown WITHOUT rehype-raw (so any residual literal `<...>`
//     is escaped, never rendered as HTML);
//   • renders markdown links `[text](url)` as INERT, NON-CLICKABLE plain
//     text (the link text + a muted, dead URL) — never a real <a href>, so a
//     `javascript:`/`data:` URL cannot become an active anchor (the MAJ-12
//     "links non-clickable" guarantee at the render layer);
//   • ALWAYS shows an untrusted-origin chrome badge (FE-7: "untrusted-origin
//     chrome always visible") when `untrustedOrigin` is true, so a human can
//     never mistake child-emitted text for content the engine or a human
//     authored.
//
// This component is part of the FE-5 session-list surface write-set but is
// explicitly EXPORTED for the chat agent to reuse for FE-3 (in-chat child
// question rendering) — it owns no chat-thread rendering itself.

import { memo } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { ShieldWarning } from '@phosphor-icons/react'
import { sanitizeChildText } from '@/lib/sanitizeChildText'
import { cn } from '@/lib/utils'

/**
 * The untrusted-origin chrome badge — always visible on child-originated
 * content. "Child" here means the free-text originated from a delegated
 * sub-turn agent (not the engine, not the human). Exported standalone so the
 * session list can place it on a row header independently of the text body.
 */
export function UntrustedOriginBadge({
  className,
  label = 'child agent',
  testId = 'untrusted-origin-badge',
}: {
  className?: string
  /** Who the untrusted origin is, for the tooltip. */
  label?: string
  testId?: string
}) {
  return (
    <span
      data-testid={testId}
      title={`Text originated from a ${label} — rendered as sanitized text; links are non-clickable.`}
      className={cn(
        'inline-flex items-center gap-0.5 px-1 py-px rounded text-[9px] font-mono uppercase tracking-wide',
        'text-[var(--color-warning,#D4AF37)] bg-[var(--color-surface-2)] border border-[var(--color-warning,#D4AF37)]/40',
        'shrink-0 select-none',
        className,
      )}
    >
      <ShieldWarning size={9} weight="bold" aria-hidden="true" />
      untrusted
    </span>
  )
}

/**
 * The inert link renderer for child markdown. Renders the link's TEXT plus a
 * muted, dead copy of the URL — there is NO <a> href, so the link is
 * non-clickable and a `javascript:`/`data:`/`vbscript:` URL can never become
 * an active anchor. This is layer 3 of the sanitizeChildText defense (see
 * that module's header): markdown-link syntax is not HTML, so DOMPurify
 * cannot see it; this renderer neutralizes it instead.
 */
function InertLink({ children, href }: { children?: React.ReactNode; href?: string }) {
  const safeHref = typeof href === 'string' && href.length > 0 ? href : null
  return (
    <span
      data-testid="child-inert-link"
      className="text-[var(--color-secondary)] underline decoration-dotted decoration-[var(--color-muted)] cursor-not-allowed"
      title={safeHref ? `Link non-clickable (untrusted child text): ${safeHref}` : 'Link non-clickable (untrusted child text)'}
    >
      {children}
      {safeHref && (
        <span className="text-[var(--color-muted)] no-underline"> ({safeHref})</span>
      )}
    </span>
  )
}

/**
 * Props for <UntrustedChildText>.
 */
export interface UntrustedChildTextProps {
  /** Raw untrusted child text (will be sanitized before render). */
  text: string | null | undefined
  /**
   * Whether to show the untrusted-origin chrome badge. Per MAJ-12 this MUST
   * be true whenever the text originated from a child agent — the badge is
   * the "untrusted-origin chrome always visible" guarantee. Defaults true
   * (this component exists for untrusted text; a trusted caller can opt out
   * for reuse, but the session list never does).
   */
  untrustedOrigin?: boolean
  /** Visual density of the rendered text. */
  density?: 'compact' | 'normal'
  /** Optional className applied to the outer wrapper. */
  className?: string
  /** Override the badge label (who the untrusted origin is). */
  originLabel?: string
  /** Test hook. */
  testId?: string
}

/**
 * Restricted react-markdown component map for child text. NO `a` (links are
 * inert via `a` override), NO `img` (dropped to alt text), only sanctioned
 * block/inline elements. Inline-styled to match the ActivityPanel's compact
 * row aesthetic (Sovereign Deep palette tokens).
 */
const CHILD_MARKDOWN_COMPONENTS = {
  // Links render INERT — never a real anchor (MAJ-12).
  a: ({ children, href }: { children?: React.ReactNode; href?: string }) => (
    <InertLink href={href}>{children}</InertLink>
  ),
  // Images are DROPPED — render only the alt text, never fetch the src (a
  // child `![](javascript:...)` or tracker pixel must not fire).
  img: ({ alt }: { alt?: string }) =>
    alt ? <span className="text-[var(--color-muted)] italic">[image: {alt}]</span> : null,
  p: ({ children }: { children?: React.ReactNode }) => (
    <p className="whitespace-pre-wrap my-0.5">{children}</p>
  ),
  strong: ({ children }: { children?: React.ReactNode }) => (
    <strong className="font-semibold">{children}</strong>
  ),
  em: ({ children }: { children?: React.ReactNode }) => <em className="italic">{children}</em>,
  code: ({ children }: { children?: React.ReactNode }) => (
    <code className="font-mono text-[10px] px-1 py-px rounded bg-[var(--color-surface-2)] text-[var(--color-accent)]">
      {children}
    </code>
  ),
  pre: ({ children }: { children?: React.ReactNode }) => (
    <pre className="font-mono text-[10px] whitespace-pre-wrap break-all bg-[var(--color-surface-2)] p-1.5 rounded my-1">
      {children}
    </pre>
  ),
  ul: ({ children }: { children?: React.ReactNode }) => (
    <ul style={{ listStyleType: 'disc' }} className="pl-5 my-0.5 space-y-0.5">{children}</ul>
  ),
  ol: ({ children }: { children?: React.ReactNode }) => (
    <ol style={{ listStyleType: 'decimal' }} className="pl-5 my-0.5 space-y-0.5">{children}</ol>
  ),
  li: ({ children }: { children?: React.ReactNode }) => <li>{children}</li>,
  blockquote: ({ children }: { children?: React.ReactNode }) => (
    <blockquote className="border-l-2 border-[var(--color-accent)]/40 pl-2 my-0.5 italic text-[var(--color-muted)]">
      {children}
    </blockquote>
  ),
  // Headings kept minimal — child text is rarely structured, but sanctioned.
  h1: ({ children }: { children?: React.ReactNode }) => (
    <p className="font-semibold my-0.5">{children}</p>
  ),
  h2: ({ children }: { children?: React.ReactNode }) => (
    <p className="font-semibold my-0.5">{children}</p>
  ),
  h3: ({ children }: { children?: React.ReactNode }) => (
    <p className="font-semibold my-0.5">{children}</p>
  ),
  h4: ({ children }: { children?: React.ReactNode }) => (
    <p className="font-semibold my-0.5">{children}</p>
  ),
  h5: ({ children }: { children?: React.ReactNode }) => (
    <p className="font-semibold my-0.5">{children}</p>
  ),
  h6: ({ children }: { children?: React.ReactNode }) => (
    <p className="font-semibold my-0.5">{children}</p>
  ),
  hr: () => <hr className="my-1 border-[var(--color-border)]" />,
} as const

/**
 * Sanitized renderer for untrusted child text. Memoized — child text is
 * high-frequency (progress pings) and the sanitize pipeline should not re-run
 * on an unchanged string across unrelated re-renders.
 */
export const UntrustedChildText = memo(function UntrustedChildText({
  text,
  untrustedOrigin = true,
  density = 'compact',
  className,
  originLabel,
  testId = 'untrusted-child-text',
}: UntrustedChildTextProps) {
  const sanitized = sanitizeChildText(text ?? '')
  if (sanitized.length === 0) return null

  const textSize = density === 'compact' ? 'text-[10px]' : 'text-xs'

  // Always render through react-markdown — it handles plain prose (wraps it
  // in a <p>) AND sanctioned markdown identically, and a plain-text fast
  // path turned out to be unsound: inputs with NO `<tag>` token but real
  // markdown (`` `code` ``, `![img](url)`, `**bold**`) would wrongly bypass
  // the markdown parse. react-markdown on a short status line is cheap.
  return (
    <span
      data-testid={testId}
      className={cn(
        'inline flex flex-col gap-0.5 text-[var(--color-secondary)]',
        textSize,
        className,
      )}
    >
      {untrustedOrigin && <UntrustedOriginBadge label={originLabel} />}
      <span className="break-words [&_p]:my-0.5">
        <ReactMarkdown
          remarkPlugins={[remarkGfm]}
          components={CHILD_MARKDOWN_COMPONENTS}
          skipHtml
        >
          {sanitized}
        </ReactMarkdown>
      </span>
    </span>
  )
})
