// markdown-shared.tsx — single source of truth for the markdown element renderers
// used by BOTH chat markdown paths:
//   • markdown-text.tsx       — the live AssistantUI streaming renderer
//   • historical-markdown.tsx — the finalized / reloaded / virtualized renderer
//
// These two renderers exist because the live path is bound to AssistantUI's
// MarkdownTextPrimitive (reads text from context) while the finalized path is plain
// react-markdown over a content string — the two markdown pipelines themselves cannot
// collapse into one component. But everything they render — paragraphs, headings,
// lists, inline code, links, images, emoji spans, the mermaid routing, AND (as of the
// library-spec D-6 fix) the actual Shiki syntax-highlighting mechanism for block code —
// is identical and MUST stay in parity. Keeping hand-maintained copies drifted three
// times now: mermaid missing when finalized, the languageless-fence collapse, and
// block code silently losing highlighting on finalize/reload (Shiki was live-only).
// This module is that shared definition. The two renderers now differ ONLY in the
// chrome around block code (each owns its own header/copy-button markup — see
// ShikiCodeBlock below for exactly why that chrome, and the live path's
// mermaid-streaming check, could not also move here).
//
// Renderers take `({ children })` (and the few element-specific attrs) and do NOT
// spread arbitrary props: react-markdown passes a `node` prop that must not reach the
// DOM, and AssistantUI's metadata is not needed on the element. Memoization for the
// live path is handled by memoizeMarkdownComponents wrapping these — not by the
// renderer — so no prop forwarding is required here.

import type { ComponentPropsWithoutRef, CSSProperties, ReactNode } from 'react'
import type { Components } from 'react-markdown'
import { ShikiHighlighter, createJavaScriptRegexEngine } from 'react-shiki'
import { ChatImage } from './ChatImage'
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
// Delegates to ChatImage for the actual img + lightbox + action toolbar.
export function MarkdownImage({ src, alt }: ComponentPropsWithoutRef<'img'>) {
  if (!src || typeof src !== 'string') return null
  if (!isSafeHref(src)) {
    return alt ? <span className="text-xs text-[var(--color-muted)] italic">[image: {alt}]</span> : null
  }
  return <ChatImage src={src} alt={alt} />
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
      <a tabIndex={0}
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
// place.
export { MermaidDiagram }

// ── Shared Shiki block-code renderer (library-spec D-6) ───────────────────────
// The actual syntax-highlighting mechanism for non-mermaid block code, shared by
// both chat renderers. `historical-markdown.tsx`'s `HistoricalCodeBlock` used to
// fall back to a plain <pre><code> with no highlighting at all — the file's own
// (now-corrected) doc comment called this deliberate ("Shiki is live-only, to keep
// this bundle light"), but Shiki is already in the bundle for the live path
// regardless, so that reasoning didn't hold: the only real saving was the lazy
// per-language grammar fetch, which happens on demand from either call site. The
// result was highlighting that vanished the instant a message finalized or the page
// reloaded — the second parity drift between these renderers, after Mermaid.
//
// Each caller keeps its OWN header chrome (language label + copy button): that
// markup already differs deliberately (live: AssistantUI's `CodeHeader` slot,
// wired to `CopyCodeHeader` in shiki-highlighter.tsx, using a raw
// navigator.clipboard call with a 2s reset; historical: `HistoricalCodeBlock`'s
// own header, using the app's copyText() helper from media-actions.ts with a
// snappier 1.5s reset to stay responsive in the finalized view). Only the
// highlighted body — theme, styling, and the ShikiHighlighter call itself — is
// shared here, so it cannot drift a third time.
//
// What deliberately did NOT move here: shiki-highlighter.tsx's exported
// `SyntaxHighlighter` also special-cases language==="mermaid" by rendering
// `LiveMermaidBlock`, which calls `useMessage()` from `@assistant-ui/react` to
// read the LIVE message's streaming status — so a mermaid fence renders nothing
// until the block is complete, rather than repeatedly failing to parse a partial
// diagram mid-stream. `useMessage()` only resolves inside AssistantUI's
// MessagePrimitive context. The historical renderer runs entirely OUTSIDE that
// context (plain react-markdown over an already-finalized content string, never
// inside an AssistantUI message tree), so it cannot call that hook — and doesn't
// need to anyway, since a finalized message is by definition never "streaming".
// historical-markdown.tsx already special-cases `language === 'mermaid'` itself
// (via `classifyFence`, above) before block code is ever reached. Hoisting the
// mermaid branch here would mean either dragging the AssistantUI dependency into
// this otherwise AssistantUI-agnostic module, or branching at runtime on "do we
// have a message context" — not worth it for a check the historical path never
// needs. If a third caller ever needs Shiki, it belongs here too; if it also
// needs live-streaming mermaid awareness, that stays caller-side.
// The regex engine, and why it is not Shiki's default
//
// Shiki's default engine is Oniguruma, compiled to WebAssembly. The SPA is
// served under `script-src 'self'` (pkg/gateway/embed.go), and a browser
// refuses `WebAssembly.instantiate` under that directive without
// 'wasm-unsafe-eval'. react-shiki catches the CompileError and renders NOTHING
// - no highlighted code, no plain-text fallback, an empty box - for EVERY
// language, including 'text'. The console says so and nothing else does:
//
//   [react-shiki] highlight failed CompileError: WebAssembly.instantiate():
//   Compiling or instantiating WebAssembly module violates the following
//   Content Security policy directive because 'unsafe-eval' is not an allowed
//   source of script in ... "script-src 'self'"
//
// embed.go's own comment listed this as an ASSUMPTION - "Shiki needs no
// WebAssembly -> code blocks stop highlighting" - under a heading stating that
// the policy string was never measured. The assumption was wrong, and the
// symptom is worse than it predicted: the code VANISHES rather than merely
// losing its colour. It surfaced through the "View raw" escape hatch on a
// .base file (the 2026-09-05 UAT, D2), but it was never a .base problem - it
// is every code block the SPA renders, in chat and in the Library alike.
//
// THE FIX IS THE LIBRARY, NOT THE POLICY. embed.go states both the rule and
// the precedent: "NON-NEGOTIABLE: no 'unsafe-eval'. If a bundled library ever
// needs it, the library is reconfigured or replaced - FR-019a is exactly that
// move for PDF.js." Shiki ships a pure-JavaScript regex engine for exactly
// this situation, so it is configured here rather than the policy widened.
//
// `forgiving: true` is Shiki's own option for the JS engine's one real
// limitation: a few grammars use Oniguruma regex constructs JavaScript has no
// equivalent for. Forgiving skips those individual patterns instead of
// throwing, so an exotic grammar degrades to slightly coarser highlighting
// rather than back to the blank box this comment is about.
//
// Built ONCE at module scope: the engine is stateless, and constructing it per
// render would rebuild it on every keystroke of a streaming message.
const shikiRegexEngine = createJavaScriptRegexEngine({ forgiving: true })

export function ShikiCodeBlock({ language, code }: { language: string | undefined; code: string }) {
  return (
    <ShikiHighlighter
      language={language || 'text'}
      engine={shikiRegexEngine}
      theme="vitesse-dark"
      addDefaultStyles={false}
      className="!bg-[var(--color-surface-2)] !rounded-b-md overflow-x-auto block w-full"
      style={{
        padding: '0.75rem 1rem',
        fontSize: '11px',
        lineHeight: '1.65',
        fontFamily: '"JetBrains Mono", "Fira Code", monospace',
        margin: 0,
      }}
    >
      {code}
    </ShikiHighlighter>
  )
}

// ── Common block-level element renderers ──────────────────────────────────────
// Canonical styles (the superset previously only fully present in the finalized
// renderer): paragraphs preserve intentional whitespace and use the secondary text
// color; strong/em are styled; everything else matches. Spread into BOTH renderers'
// component maps so they cannot drift again.
export const commonMarkdownComponents = {
  // `whitespace-pre-wrap` is deliberate: it preserves single newlines inside a
  // paragraph as line breaks (overriding CommonMark's soft-break→space collapse).
  // LLM output is frequently hard-wrapped mid-paragraph; preserving that intent reads
  // better than reflowing into one run-on line. Applied to BOTH paths for parity.
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
  // `start` is forwarded so an ordered list that does not begin at 1 (e.g. "7.")
  // renders correctly — react-markdown delivers it as a prop, and dropping it would
  // reset every such list to 1.
  ol: ({ children, start }: { children?: ReactNode; start?: number }) => (
    <ol start={start} style={{ listStyleType: 'decimal' }} className="pl-6 my-2 space-y-1 text-[var(--color-secondary)]">
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
  // `style` is forwarded on table cells: remark-gfm encodes column alignment
  // (:---:) as a `style={{ textAlign }}` prop; dropping it left-aligns every column.
  th: ({ children, style }: { children?: ReactNode; style?: CSSProperties }) => (
    <th style={style} className="border border-[var(--color-border)] px-3 py-1.5 text-left font-semibold bg-[var(--color-surface-2)] text-[var(--color-secondary)]">
      {children}
    </th>
  ),
  td: ({ children, style }: { children?: ReactNode; style?: CSSProperties }) => (
    <td style={style} className="border border-[var(--color-border)] px-3 py-1.5 text-[var(--color-secondary)]">{children}</td>
  ),

  hr: () => <hr className="my-4 border-[var(--color-border)]" />,
  // `satisfies Partial<Components>` pins each renderer to react-markdown's component
  // contract at the DEFINITION site (not just where it's spread), so a renderer with a
  // wrong signature fails here — catching the asymmetric-drift this module exists to stop.
} satisfies Partial<Components>
