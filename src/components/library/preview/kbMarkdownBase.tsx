// kbMarkdownBase — the Library's markdown COMPOSITION, and only the composition.
//
// ADR-067 FR-011, FR-013a–d. Split out of `LibraryMarkdownPreview.tsx`
// verbatim; every comment below is that file's own reasoning, unchanged.
//
// ── Why it is its own module ────────────────────────────────────────────────
// Purely to break an import cycle, and the cycle is worth naming because
// re-creating it is easy. The Library's markdown PANE now mounts the stage-2
// reading view (`knowledge/KnowledgeNoteView` → `KnowledgeReader` →
// `preview/knowledgeMarkdown`), and stage 2 is built FROM the constants here —
// so if the pane and the constants share a file, that file imports a module
// that imports it back. `kbMarkdownComponents` is consumed at MODULE SCOPE by
// knowledgeMarkdown.tsx (`const InheritedLink = kbMarkdownComponents.a`), which
// is exactly where a cycle stops being a warning and becomes a crash: whichever
// side evaluates second reads `undefined`.
//
// Nothing else changed. `LibraryMarkdownPreview.tsx` re-exports every name
// below, so existing importers and tests are unaffected.

import type { ComponentPropsWithoutRef, ReactNode } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import remarkMath from 'remark-math'
import rehypeKatex from 'rehype-katex'
import 'katex/dist/katex.min.css'
import { rehypePhosphorEmoji } from '@/lib/rehype-phosphor-emoji'
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
} from '@/components/chat/markdown-shared'
import { CopyCodeHeader } from '@/components/chat/shiki-highlighter'

// ─────────────────────────────────────────────────────────────────────────────
// Divergence 2 — remark plugin: strip `%%private asides%%`
// ─────────────────────────────────────────────────────────────────────────────

/** Minimal structural view of the mdast nodes this plugin touches. Declared
 *  locally rather than imported from `@types/mdast`, which is only a transitive
 *  dependency here — nothing in package.json guarantees it stays resolvable. */
interface MdNode {
  type: string
  value?: string
  children?: MdNode[]
}

interface StripState {
  /** True while the walk is between an opening and a closing `%%`. Carried
   *  ACROSS nodes and across blocks: `%%` on its own line opens a comment that a
   *  later paragraph closes, which is the multi-line form Obsidian writes. */
  inside: boolean
}

const COMMENT_MARKER = '%%'

/** Containers that are removed entirely once a comment has emptied them, so a
 *  hidden aside leaves no empty `<p>`/`<li>` behind. `tableCell` and `tableRow`
 *  are deliberately absent: an emptied cell must stay, or the table's columns
 *  shift under the reader. */
const PRUNE_WHEN_EMPTIED = new Set([
  'paragraph',
  'heading',
  'emphasis',
  'strong',
  'delete',
  'blockquote',
  'listItem',
  'list',
  'link',
  'linkReference',
])

/** Consumes one text node's value, returning only the parts outside `%%…%%` and
 *  updating `state.inside` for the nodes that follow. */
function stripCommentsFromText(value: string, state: StripState): string {
  let out = ''
  let i = 0
  while (i < value.length) {
    const marker = value.indexOf(COMMENT_MARKER, i)
    if (marker === -1) {
      if (!state.inside) out += value.slice(i)
      return out
    }
    if (state.inside) {
      state.inside = false
    } else {
      out += value.slice(i, marker)
      state.inside = true
    }
    i = marker + COMMENT_MARKER.length
  }
  return out
}

/** Walks `node.children` in document order, rewriting text and dropping
 *  everything a comment swallows — including non-text nodes (`**bold**`, images,
 *  fences) that sit between the markers. */
function stripCommentsFromChildren(node: MdNode, state: StripState): void {
  if (!node.children) return
  const kept: MdNode[] = []
  for (const child of node.children) {
    if (child.type === 'text') {
      child.value = stripCommentsFromText(child.value ?? '', state)
      if (child.value !== '') kept.push(child)
      continue
    }
    if (child.children) {
      const hadChildren = child.children.length > 0
      stripCommentsFromChildren(child, state)
      if (hadChildren && child.children.length === 0 && PRUNE_WHEN_EMPTIED.has(child.type)) continue
      kept.push(child)
      continue
    }
    // Leaf with no children: `code`, `inlineCode`, `image`, `html`, `break`,
    // `thematicBreak`. Its own text is never scanned for markers (a `%%` inside a
    // fence is literal source), but it disappears if a comment is open around it.
    if (state.inside) continue
    kept.push(child)
  }
  node.children = kept
}

/**
 * remark plugin: hide `%%…%%` private asides, inline and multi-line.
 *
 * KB READER ONLY. Never add this to chat's plugin list (FR-013d) — see this
 * file's header for why hiding text in untrusted output is the wrong trade.
 *
 * An unclosed `%%` hides everything after it to the end of the document. That
 * matches Obsidian and is the safe direction to fail: a typo hides content the
 * author marked private rather than revealing it.
 */
export function remarkStripPrivateComments() {
  return (tree: unknown) => {
    stripCommentsFromChildren(tree as MdNode, { inside: false })
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Divergence 1 — the `a` slot
// ─────────────────────────────────────────────────────────────────────────────

// Chat's own link renderer, at module scope because the factory returns a fresh
// component on every call. `null` is the shared "no preview-host rewrite" case,
// exactly as chat passes it. Every href the shared sanitiser already accepts —
// http, https, mailto, tel — and every href it rejects for a genuinely unsafe
// SCHEME renders through this, unchanged.
const ChatMarkdownLink = createLinkRenderer(null)

/**
 * True for an href that names something inside the collection: no scheme of its
 * own, and resolving to http(s) against the page. `notes/plan.md`, `./a.md`,
 * `/vault/a.md` and `#heading` qualify.
 *
 * Protocol-relative (`//host/path`) deliberately does NOT: it has no scheme but
 * it is not in-collection, and chat strikes it through today. The divergence
 * being corrected here is relative PATHS, nothing wider.
 */
function isCollectionRelativeHref(href: string): boolean {
  if (!href) return false
  if (href.startsWith('//')) return false
  try {
    // Succeeds ⇒ the href carries its own scheme (including javascript:, data:,
    // file:), so it is not relative and the shared sanitiser's verdict stands.
    new URL(href)
    return false
  } catch {
    // Throws ⇒ no scheme. Fall through and resolve it.
  }
  try {
    const base = typeof location !== 'undefined' ? location.href : 'http://localhost/'
    const resolved = new URL(href, base)
    return resolved.protocol === 'http:' || resolved.protocol === 'https:'
  } catch {
    return false
  }
}

/**
 * The KB `a` slot. Relative links render as real links; everything else is
 * handed to chat's renderer untouched, so the scheme allow-list and the
 * struck-through treatment of `javascript:` et al are inherited, not restated.
 *
 * No `target="_blank"` on the relative branch: an in-collection link points at
 * another note, and opening the app in a second tab is not what a reader means
 * by following it. Resolving that click to a Library selection is stage 2
 * (FR-060); stage 1 owes only "readable, not struck through".
 */
function KbMarkdownLink({ href, children }: ComponentPropsWithoutRef<'a'>) {
  const raw = href ?? ''
  if (isCollectionRelativeHref(raw)) {
    return (
      <a
        tabIndex={0}
        href={raw}
        data-testid="markdown-link"
        className="text-[var(--color-accent)] underline underline-offset-2 hover:opacity-80 transition-opacity"
      >
        {children}
      </a>
    )
  }
  return <ChatMarkdownLink href={href}>{children}</ChatMarkdownLink>
}

// ─────────────────────────────────────────────────────────────────────────────
// The composition
// ─────────────────────────────────────────────────────────────────────────────

/** Block code: the same chrome chat's finalized renderer shows, assembled from
 *  the two components that already own it (`CopyCodeHeader`, `ShikiCodeBlock`)
 *  rather than a third hand-written header. markdown-shared.tsx's doc comment
 *  records that the highlighted body is shared and the header chrome is
 *  per-caller; this caller reuses an existing header instead of authoring one. */
function KbBlockCode({ code, language }: { code: string; language: string | undefined }) {
  return (
    <div className="my-2 rounded overflow-hidden">
      <CopyCodeHeader language={language} code={code} />
      <ShikiCodeBlock language={language} code={code} />
    </div>
  )
}

// MODULE SCOPE — see the perf note in this file's header. Every entry except `a`
// is chat's own renderer, by reference or by the same shared call.
export const kbMarkdownComponents = {
  // Paragraphs, headings, lists, strong/em, blockquote, tables, hr — imported by
  // reference, so they cannot drift from chat's.
  ...commonMarkdownComponents,

  // `pre` passes through: `code` below emits its own block wrapper, so the
  // default <pre> would double-wrap it. Same reason as chat's.
  pre: ({ children }: { children?: ReactNode }) => <>{children}</>,

  // Block/inline detection and mermaid routing come from the shared
  // `classifyFence` — block-ness must NOT key solely on a `language-` class or
  // bare and indented fences collapse.
  code: ({ children, className }: { children?: ReactNode; className?: string }) => {
    const { isBlock, language, text } = classifyFence(children, className)
    if (!isBlock) return <InlineCode>{children}</InlineCode>
    if (language === 'mermaid') {
      // Fenced content carries a trailing newline; trim so mermaid parses cleanly.
      return <MermaidDiagram code={text.replace(/\n$/, '')} />
    }
    return <KbBlockCode code={codeText(children)} language={language} />
  },

  span: PhosphorEmojiSpan,
  img: MarkdownImage,

  // ── The only divergence in this map ──
  a: KbMarkdownLink,
}

// MODULE SCOPE. Chat's remark list with exactly one plugin appended; chat's
// rehype list with nothing appended.
export const KB_REMARK_PLUGINS = [remarkGfm, remarkMath, remarkStripPrivateComments]
export const KB_REHYPE_PLUGINS = [rehypeKatex, rehypePhosphorEmoji]

/** Stage 1's reading view: chat's markdown pipeline, composed once more with a
 *  KB link slot and the private-comment strip.
 *
 *  STILL LIVE, AND NO LONGER WHAT THE LIBRARY PANE MOUNTS. The pane mounts the
 *  STAGE 2 composition (`preview/knowledgeMarkdown.tsx`, via
 *  `KnowledgeNoteView`), which is this same pipeline with the wikilink,
 *  callout, highlight and frontmatter plugins appended and a link slot that can
 *  resolve a target. Stage 2 is built from the constants above, so the two
 *  cannot drift; this is the plugin-free rendering, and the subject of the
 *  FR-011 / FR-013b assertions that are about the base composition rather than
 *  about the KB additions. */
export function KnowledgeMarkdown({ content }: { content: string }) {
  return (
    <ReactMarkdown
      remarkPlugins={KB_REMARK_PLUGINS}
      rehypePlugins={KB_REHYPE_PLUGINS}
      components={kbMarkdownComponents}
    >
      {content}
    </ReactMarkdown>
  )
}
