// knowledgeMarkdown.tsx — the KNOWLEDGE-BASE markdown composition.
//
// ADR-067 STAGE 2: FR-011, FR-013a/b/c/d, FR-060 (wikilinks, aliases, heading
// links, path links, embeds), FR-061 (callouts, highlights, frontmatter not
// rendered as body), FR-065 (unresolved links marked and not navigable),
// US-7 AS-1..AS-4, AS-7, AS-8, US-10 AS-1/AS-2.
//
// ── This is a THIRD COMPOSITION, not a second pipeline (FR-013a) ─────────────
// `LibraryMarkdownPreview.tsx`'s header records the standing decision: what is
// forbidden is duplicating the PARSER, the PLUGIN STACK and the ELEMENT
// RENDERERS — those were hand-copied once and drifted three times. What was
// never forbidden is another COMPOSITION over the same shared definitions.
//
// The inheritance chain here is literal, by object reference:
//
//   chat/markdown-shared.tsx  →  commonMarkdownComponents
//        ↓ (spread)
//   LibraryMarkdownPreview.tsx →  kbMarkdownComponents   (stage 1: %% + relative links)
//        ↓ (spread)
//   knowledgeMarkdown.tsx      →  knowledgeMarkdownComponents (stage 2: this file)
//
// Every entry except `a` is inherited by reference. `p`, `h1`–`h3`, `ul`, `ol`,
// `li`, `strong`, `em`, `blockquote`, `table`, `th`, `td`, `hr`, `pre`, `code`
// (fence classification + mermaid routing + Shiki), `span` and `img` are chat's
// own objects — not re-declared, so they cannot drift.
//
// ── The two permitted divergences (FR-013b), and nothing else ────────────────
//   (1) the `a` slot — `KnowledgeMarkdownLink` below: wikilinks, in-document
//       heading links, collection-relative path links, and a visible unresolved
//       state. Everything it does not recognise is handed to the INHERITED
//       renderer, so external and unsafe hrefs behave exactly as in chat.
//   (2) appended remark plugins — private-comment stripping (inherited from
//       stage 1, not re-implemented), frontmatter suppression, callouts,
//       highlights, wikilinks/embeds.
// There is no third divergence: no rehype plugin is added (KB_REHYPE_PLUGINS is
// re-exported from stage 1 unchanged), and no other component slot is replaced.
//
// ── Chat is untouched (FR-013d) ──────────────────────────────────────────────
// Nothing in this file is imported by any chat module, and no chat array or map
// is mutated. `%%…%%` therefore still renders LITERALLY in chat, which is the
// point: chat carries untrusted model and tool output, where hiding the text
// between two markers hides content FROM the reader instead of protecting them.
// There is no compiler check for either property — both are asserted by test.
//
// ── Perf, non-negotiable (FR-013c) ───────────────────────────────────────────
// `knowledgeMarkdownComponents` is a MODULE-SCOPE CONSTANT. react-markdown keys
// each entry by object reference and treats it as that node type's component
// type, so a map rebuilt per render remounts every element on every keystroke.
// Per-note data reaches the link slot through React context instead — context
// changes re-render, it does not remount.

import { createContext, useContext, useMemo } from 'react'
import type { ComponentProps, ComponentPropsWithoutRef, ReactNode } from 'react'
import ReactMarkdown from 'react-markdown'
import { useQuery } from '@tanstack/react-query'
import { SpinnerGap, Warning } from '@phosphor-icons/react'
// From kbMarkdownBase, NOT from LibraryMarkdownPreview: that file now mounts
// the stage-2 reading view this module is part of, so importing it here would
// be a cycle — and `kbMarkdownComponents` is read at module scope below, which
// is where a cycle crashes instead of merely warning.
import { kbMarkdownComponents, KB_REHYPE_PLUGINS, KB_REMARK_PLUGINS } from './kbMarkdownBase'
import { libraryEntryExt, type LibraryPreviewKind } from './libraryPreviewKind'
import { fetchKnowledgeBaseViews, fetchLibraryContent, fetchLibraryEntries, libraryQueryKeys } from '@/lib/api'
import { LazyEmbedMount } from './LazyEmbedMount'
import { matchBaseView } from './baseViewMatch'
import { sliceTranscludedContent } from './noteTransclusion'
import { BasePreview, type BasePreviewEmbedOptions } from './BasePreview'

type RemarkPlugins = ComponentProps<typeof ReactMarkdown>['remarkPlugins']

// ─────────────────────────────────────────────────────────────────────────────
// Shared mdast shapes
// ─────────────────────────────────────────────────────────────────────────────

/** Minimal structural view of the mdast nodes these plugins touch. Declared
 *  locally rather than imported from `@types/mdast`, which is only a transitive
 *  dependency — nothing in package.json guarantees it stays resolvable. Same
 *  reasoning (and same shape) as `LibraryMarkdownPreview.tsx`. */
interface MdNode {
  type: string
  value?: string
  url?: string
  alt?: string | null
  depth?: number
  children?: MdNode[]
  data?: {
    hName?: string
    hProperties?: Record<string, string | string[] | undefined>
  }
  position?: { start: { line: number }; end: { line: number } }
}

interface MdRoot extends MdNode {
  children: MdNode[]
}

/** The vfile a unified transformer receives as its second argument. Only
 *  `value` is used, and only by the frontmatter plugin, which needs the SOURCE
 *  bytes — the parsed tree alone cannot tell frontmatter apart from a genuine
 *  horizontal rule followed by a setext heading. */
interface MdFile {
  value?: unknown
}

/** Depth-first walk over every node, parent-first. */
function walk(node: MdNode, visit: (node: MdNode) => void): void {
  visit(node)
  for (const child of node.children ?? []) walk(child, visit)
}

// ─────────────────────────────────────────────────────────────────────────────
// Divergence 2a — frontmatter suppression (FR-061, US-7 AS-4)
// ─────────────────────────────────────────────────────────────────────────────

/**
 * remark plugin: drop a leading YAML frontmatter block.
 *
 * Measured, not assumed: with the plugin set this app actually ships (no
 * remark-frontmatter), remark parses
 *
 *     ---
 *     title: My note
 *     ---
 *
 * as a `thematicBreak` followed by a **setext heading** whose text is the YAML
 * body. That is precisely the "renders as a heading or a horizontal rule"
 * failure US-7 AS-4 forbids.
 *
 * Detection reads the SOURCE, not the tree, because a horizontal rule followed
 * by a setext heading is indistinguishable from frontmatter once parsed — and
 * a tree-shape heuristic would eat a legitimate document that opens with a rule.
 *
 * An UNCLOSED opening `---` strips nothing. That is the honest direction to
 * fail: the reader sees the raw text and can tell something is wrong, rather
 * than the renderer silently swallowing the rest of the document. (The backend
 * reports the same condition as `frontmatter_malformed` on the outline.)
 */
export function remarkKbFrontmatter() {
  return (tree: unknown, file?: unknown) => {
    const root = tree as MdRoot
    if (!root?.children?.length) return
    const source = String((file as MdFile | undefined)?.value ?? '')
    if (!/^---[ \t]*\r?\n/.test(source)) return

    const lines = source.split(/\r?\n/)
    let closeLine = -1
    for (let i = 1; i < lines.length; i++) {
      if (/^---[ \t]*$/.test(lines[i])) {
        closeLine = i + 1 // 1-based, matching mdast positions
        break
      }
    }
    if (closeLine === -1) return // malformed: leave it visible

    root.children = root.children.filter((child) => {
      const end = child.position?.end.line
      // A node with no position cannot be proven to be frontmatter — keep it.
      return end === undefined || end > closeLine
    })
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Divergence 2b — callouts (FR-061, US-7 AS-3)
// ─────────────────────────────────────────────────────────────────────────────

const CALLOUT_HEAD = /^\[!([A-Za-z0-9_-]+)\]([+-]?)[ \t]*(.*)$/

/**
 * remark plugin: render `> [!note] Title` as a styled callout with no raw
 * `[!note]` marker left in the text.
 *
 * The blockquote is RENAMED to a `div` (`data.hName`) rather than given extra
 * props, because chat's `blockquote` renderer takes `{ children }` only and
 * drops everything else — a `data-callout` attribute hung on the mdast node
 * would never reach the DOM. A `div` has no override in the components map, so
 * react-markdown renders it with the class names below and the callout's body
 * still renders through chat's own paragraph/list/code renderers.
 */
export function remarkKbCallouts() {
  return (tree: unknown) => {
    walk(tree as MdNode, (node) => {
      if (node.type !== 'blockquote') return
      const firstBlock = node.children?.[0]
      if (!firstBlock || firstBlock.type !== 'paragraph') return
      const firstInline = firstBlock.children?.[0]
      if (!firstInline || firstInline.type !== 'text') return

      const value = firstInline.value ?? ''
      const newlineAt = value.indexOf('\n')
      const headLine = newlineAt === -1 ? value : value.slice(0, newlineAt)
      const match = CALLOUT_HEAD.exec(headLine)
      if (!match) return

      const [, rawType, fold, title] = match
      const kind = rawType.toLowerCase()

      // Strip the marker line from the body text.
      firstInline.value = newlineAt === -1 ? '' : value.slice(newlineAt + 1)
      if (firstInline.value === '' && firstBlock.children?.length === 1) {
        node.children = (node.children ?? []).slice(1)
      }

      node.data = {
        hName: 'div',
        hProperties: {
          className: ['kb-callout', `kb-callout-${kind}`],
          'data-kb-callout': kind,
          ...(fold ? { 'data-kb-callout-fold': fold } : {}),
        },
      }
      node.children = [
        {
          type: 'paragraph',
          data: {
            hName: 'div',
            hProperties: { className: ['kb-callout-title'], 'data-kb-callout-title': '' },
          },
          children: [{ type: 'text', value: title.trim() !== '' ? title.trim() : rawType }],
        },
        ...(node.children ?? []),
      ]
    })
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Divergence 2c — highlights (FR-061, US-7 AS-3)
// ─────────────────────────────────────────────────────────────────────────────

const HIGHLIGHT_RE = /==([^=\n]+)==/g

/** Splits a text node on a regex, replacing each match with a node the caller
 *  builds. Returns null when nothing matched, so callers can leave the node
 *  untouched (and keep its position, which the frontmatter plugin relies on). */
function splitTextNode(
  node: MdNode,
  pattern: RegExp,
  build: (match: RegExpExecArray) => MdNode | null,
): MdNode[] | null {
  const value = node.value ?? ''
  const re = new RegExp(pattern.source, pattern.flags.includes('g') ? pattern.flags : `${pattern.flags}g`)
  let last = 0
  let match: RegExpExecArray | null
  const out: MdNode[] = []
  let matched = false

  while ((match = re.exec(value)) !== null) {
    const replacement = build(match)
    if (!replacement) continue
    matched = true
    if (match.index > last) out.push({ type: 'text', value: value.slice(last, match.index) })
    out.push(replacement)
    last = match.index + match[0].length
  }
  if (!matched) return null
  if (last < value.length) out.push({ type: 'text', value: value.slice(last) })
  return out
}

/** Applies a text-node splitter across the whole tree, in place. */
function mapTextNodes(root: MdNode, split: (node: MdNode) => MdNode[] | null): void {
  walk(root, (node) => {
    if (!node.children?.length) return
    // `code` and `inlineCode` carry their text in `value`, not in children, so
    // fenced and inline code are never rewritten — a `==` or `[[` inside a code
    // span is literal source and must stay literal.
    const next: MdNode[] = []
    let changed = false
    for (const child of node.children) {
      if (child.type !== 'text') {
        next.push(child)
        continue
      }
      const replacement = split(child)
      if (!replacement) {
        next.push(child)
        continue
      }
      changed = true
      next.push(...replacement)
    }
    if (changed) node.children = next
  })
}

/**
 * remark plugin: `==text==` renders as a highlight with no `==` markers left.
 *
 * The node is an `emphasis` renamed to `mark`, so it goes through
 * mdast-util-to-hast's ordinary inline path and lands on a tag with no override
 * in the components map — i.e. it adds a highlight without taking a slot.
 */
export function remarkKbHighlights() {
  return (tree: unknown) => {
    mapTextNodes(tree as MdNode, (node) =>
      splitTextNode(node, HIGHLIGHT_RE, (match) => ({
        type: 'emphasis',
        data: { hName: 'mark', hProperties: { className: ['kb-highlight'] } },
        children: [{ type: 'text', value: match[1] }],
      })),
    )
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Divergence 2d — wikilinks and embeds (FR-060, US-7 AS-1, AS-2)
// ─────────────────────────────────────────────────────────────────────────────

export const WIKILINK_RE = /(!?)\[\[([^[\]\n]+)\]\]/g

const IMAGE_EXTENSIONS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'avif', 'ico'])

export interface ParsedWikilink {
  /** Path or basename before `#` and `|`. Empty for a same-note heading link. */
  target: string
  /** Heading after `#`, if any. Mutually exclusive with `block` — a block
   *  reference never populates this (ADR-083 EMB-036). */
  heading?: string
  /** Block anchor with the leading `^` removed, for a `[[Note#^abc123]]` block
   *  reference — a distinct addressing form from a heading, kept separate so a
   *  block id is never matched against heading text (ADR-083 EMB-011, EMB-036,
   *  mirroring `pkg/knowledge/links.go`'s `Link.BlockID`/`Link.Heading` split). */
  block?: string
  /** Display text: the alias when one was given, else the raw inner text. */
  text: string
  /** True for the `![[…]]` embed form. */
  embed: boolean
}

/** Parses the inside of a `[[…]]`, in Obsidian's order: alias last, heading
 *  before it. Exported because the outline/backlink rails parse the same forms.
 *  Returns null for an empty or whitespace-only body. */
export function parseWikilink(inner: string, embed = false): ParsedWikilink | null {
  const bar = inner.indexOf('|')
  const head = (bar === -1 ? inner : inner.slice(0, bar)).trim()
  const alias = bar === -1 ? undefined : inner.slice(bar + 1).trim()
  if (head === '' && !alias) return null

  const hash = head.indexOf('#')
  const target = (hash === -1 ? head : head.slice(0, hash)).trim()
  const fragment = hash === -1 ? undefined : head.slice(hash + 1).trim() || undefined
  if (target === '' && !fragment) return null

  // `^` prefixes a block anchor, not a heading (Obsidian's own distinction —
  // see the `block` field's doc comment above).
  const isBlock = fragment !== undefined && fragment.startsWith('^')
  const heading = isBlock ? undefined : fragment
  const block = isBlock ? fragment.slice(1) || undefined : undefined

  return {
    target,
    heading,
    block,
    text: alias && alias !== '' ? alias : head,
    embed,
  }
}

// ── Inline kind classification (ADR-083 EMB-034) ─────────────────────────────
//
// A DELIBERATE, EXTENSION-ONLY sibling of `libraryPreviewKind.ts`'s
// `classifyLibraryEntry` — not a call to it. That function's parameter type
// requires `mime` and `is_text_editable`; a link-graph edge carries neither
// (there is no single-entry GET an embed could use to fetch them), so
// fabricating them to satisfy the type would be exactly the invented evidence
// this feature refuses everywhere else. The two are therefore SEPARATE
// implementations sharing the same extension tables, and their answers are
// allowed to diverge for a file whose extension does not name its kind — an
// image, a video or a text file served with no recognisable extension all
// classify as `other` here, where the pane's classifier would have used the
// server's mime hint or its `is_text_editable` flag to do better.
const EMBED_VIDEO_EXTENSIONS = new Set(['mp4', 'webm', 'mov', 'mkv', 'avi', 'm4v', 'ogv'])
const EMBED_HTML_EXTENSIONS = new Set(['html', 'htm'])
const EMBED_AUDIO_EXTENSIONS = new Set(['mp3', 'm4a', 'aac', 'ogg', 'opus', 'wav', 'flac'])

/** Classifies an embed's WRITTEN target by extension alone. See the header
 *  note above — this is not `classifyLibraryEntry` and must not become a call
 *  to it. `'text'` is never returned: distinguishing an editable text file
 *  from an opaque one needs the server's `is_text_editable` hint, which an
 *  embed target does not carry, so an extensionless or unrecognised target is
 *  honestly `'other'` rather than a guessed `'text'`. */
export function classifyEmbedKind(target: string): LibraryPreviewKind {
  const e = libraryEntryExt(target)
  if (IMAGE_EXTENSIONS.has(e)) return 'image'
  if (EMBED_VIDEO_EXTENSIONS.has(e)) return 'video'
  if (EMBED_HTML_EXTENSIONS.has(e)) return 'html'
  if (e === 'pdf') return 'pdf'
  if (EMBED_AUDIO_EXTENSIONS.has(e)) return 'audio'
  if (e === 'base') return 'base'
  if (e === 'md' || e === 'markdown') return 'markdown'
  if (e === 'mmd' || e === 'mermaid') return 'mermaid'
  return 'other'
}

/** The kinds Step 1 mounts an inline renderer for. Every other kind — `pdf`,
 *  `base` and `markdown` included — MUST fall back to the link treatment
 *  today (ADR-083 EMB-025): their renderers (the PDF pool, the saved-view
 *  renderer, one-level transclusion) are separate, not-yet-landed phases of
 *  the same ADR, and an embed must never claim to render a kind it cannot
 *  actually mount. `html`, `other` and `text` never gain one — that part of
 *  EMB-025 is permanent, not a "not yet". */
const KINDS_WITH_INLINE_RENDERER: ReadonlySet<LibraryPreviewKind> = new Set(['image'])

/** What the reader knows about an embed's target — the resolver's answer.
 *
 *  Five states, none collapsible into another (ADR-083 EMB-012):
 *   - `loading`    — the link graph request is in flight. No evidence either
 *                    way; render a reserved placeholder, no marker.
 *   - `graph_unavailable` — the request failed, OR it succeeded with no edges
 *                    and no skips for a note that plainly has links to check.
 *                    Handled ONE page-level statement, never per embed.
 *   - `resolved`   — a matching edge exists, its resolution succeeded, and
 *                    the graph's node list does not contradict it.
 *   - `unresolved` — a matching edge exists and confirms the target does not
 *                    exist (and the walk did not merely skip it — see
 *                    `indeterminate`).
 *   - `indeterminate` — the graph loaded and answered, but the evidence does
 *                    not support either verdict: no matching edge at all, a
 *                    skipped target, a truncated answer, or the edge and node
 *                    lists disagreeing with each other. MUST NOT be rendered
 *                    as "nothing in this knowledge base is named X". */
export type EmbedResolutionState = 'resolved' | 'unresolved' | 'loading' | 'graph_unavailable' | 'indeterminate'

export interface EmbedResolution {
  state: EmbedResolutionState
  /** Workspace-relative URL the browser can load. Present only when resolved. */
  url?: string
  /** Collection-relative path of the resolved target. Present only when resolved. */
  path?: string
  /** True when the unresolved target lay outside the collection root rather
   *  than simply not matching anything (ADR-083 EMB-017, EMB-023). The
   *  reason text for this case MUST NOT contain the escaping path. */
  outsideRoot?: boolean
  /** Human-readable explanation. Always set for `indeterminate` and
   *  `unresolved` — "no reason available" when nothing more specific is
   *  known, per EMB-013, rather than the field being omitted. */
  reason?: string
  /** True when more than one graph edge matched this embed's key (EMB-018). */
  ambiguous?: boolean
  /** The alternative targets not chosen. Present only when ambiguous. */
  candidates?: string[]
  /** The workspace the resolved target lives in — present only when
   *  resolved. A `.base`/markdown inline renderer needs this to query the
   *  Library and knowledge endpoints itself; it is not derivable from `url`,
   *  which is already an absolute download link, not a query key. */
  workspaceId?: string
  /** WORKSPACE-relative path of the resolved target (ADR-083 dashboards/
   *  transclusion note: a graph edge's own `path` is COLLECTION-relative —
   *  see KnowledgeNoteView's `toWorkspacePath`). Present only when resolved. */
  workspacePath?: string
}

export interface KbWikilinkOptions {
  /**
   * Resolves an embedded target (`![[diagram.png]]`, `![[report.pdf]]`, …) to
   * a URL and a state honestly reflecting what the link graph currently
   * knows. Omitting this altogether (as opposed to it returning a definite
   * state) is itself honest — it means the caller has no resolution to offer
   * at all, e.g. outside a knowledge base — and every embed then renders the
   * visibly-marked reference treatment.
   */
  resolveEmbedUrl?: (target: string, heading?: string, block?: string) => EmbedResolution
}

/** Default resolution when the caller offers no resolver at all (e.g. outside
 *  a knowledge base) — genuinely unknown, never a confident verdict. */
const NO_RESOLVER_EMBED_RESOLUTION: EmbedResolution = {
  state: 'indeterminate',
  reason: 'no reason available',
}

/**
 * remark plugin: `[[Note]]`, `[[Note|alias]]`, `[[Note#Heading]]`,
 * `[[folder/Note]]` and `![[image.png]]`.
 *
 * A plain wikilink becomes an ordinary `link` node carrying its parts as data
 * attributes, so it lands on the `a` slot — the one slot this composition is
 * permitted to replace. Resolution (does that note exist?) happens THERE, from
 * React context, because it is per-note data and a remark plugin cannot read
 * context.
 *
 * An EMBED is different: its resolution is looked up HERE, eagerly, because
 * the answer decides which kind of node to emit, not just how to style one —
 * `classifyEmbedKind` (extension-only, EMB-034) plus the resolver's state
 * decide between an `image` node (rendered through CHAT's inherited `img`
 * slot, untouched) and a link-fallback node carrying the full resolution
 * serialised as data attributes, read back by `KnowledgeMarkdownLink`'s embed
 * branch below. The plugin itself makes no claim beyond what the resolver
 * told it — an embed whose kind has no inline renderer yet (pdf, base,
 * markdown — EMB-025) always falls back, even when `resolved`.
 */
export function remarkKbWikilinks(options: KbWikilinkOptions = {}) {
  return (tree: unknown) => {
    mapTextNodes(tree as MdNode, (node) =>
      splitTextNode(node, WIKILINK_RE, (match) => {
        const parsed = parseWikilink(match[2], match[1] === '!')
        if (!parsed) return null

        if (!parsed.embed) {
          return {
            type: 'link',
            url: '',
            data: {
              hProperties: {
                'data-kb-wikilink': '',
                'data-kb-target': parsed.target,
                ...(parsed.heading ? { 'data-kb-heading': parsed.heading } : {}),
              },
            },
            children: [{ type: 'text', value: parsed.text }],
          }
        }

        const kind = classifyEmbedKind(parsed.target)
        const resolution =
          options.resolveEmbedUrl?.(parsed.target, parsed.heading, parsed.block) ??
          NO_RESOLVER_EMBED_RESOLUTION

        if (KINDS_WITH_INLINE_RENDERER.has(kind) && resolution.state === 'resolved' && resolution.url) {
          return { type: 'image', url: resolution.url, alt: parsed.text, children: [] }
        }
        // Every other combination is reported, never faked: a resolved
        // non-image embed (no inline renderer for its kind yet), an
        // unresolved/indeterminate/loading/graph_unavailable target, or an
        // image whose resolver returned no URL despite `resolved` state.

        const candidates = resolution.candidates ?? []
        return {
          type: 'link',
          url: '',
          data: {
            hProperties: {
              'data-kb-wikilink': '',
              'data-kb-target': parsed.target,
              ...(parsed.heading ? { 'data-kb-heading': parsed.heading } : {}),
              ...(parsed.block ? { 'data-kb-block': parsed.block } : {}),
              'data-kb-embed': '',
              'data-kb-embed-kind': kind,
              'data-kb-embed-state': resolution.state,
              ...(resolution.path ? { 'data-kb-embed-path': resolution.path } : {}),
              ...(resolution.reason ? { 'data-kb-embed-reason': resolution.reason } : {}),
              ...(resolution.outsideRoot ? { 'data-kb-embed-outside-root': '' } : {}),
              ...(resolution.ambiguous ? { 'data-kb-embed-ambiguous': '' } : {}),
              ...(candidates.length > 0 ? { 'data-kb-embed-candidates': candidates.join(', ') } : {}),
              // ADR-083 Step 2/3 — a `.base` (dashboard) or markdown
              // (transclusion) embed needs its own workspace query key to
              // mount a real renderer; see EmbedResolution's own doc.
              ...(resolution.workspaceId ? { 'data-kb-embed-workspace-id': resolution.workspaceId } : {}),
              ...(resolution.workspacePath
                ? { 'data-kb-embed-workspace-path': resolution.workspacePath }
                : {}),
            },
          },
          children: [{ type: 'text', value: parsed.text }],
        }
      }),
    )
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Divergence 2e — block promotion for dashboards and transclusion
// (ADR-083 Step 2/Step 3)
// ─────────────────────────────────────────────────────────────────────────────

/** True for a resolved `.base` or markdown embed node — the only two kinds
 *  that mount BLOCK content (a table, another note's own headings and
 *  paragraphs) rather than an inline picture or a styled span. Everything
 *  else (image, an unresolved/indeterminate/loading state, a resolved embed
 *  of a kind with no inline renderer) is unaffected and keeps rendering
 *  wherever `remarkKbWikilinks` put it. */
function isPromotableBlockEmbedNode(node: MdNode): boolean {
  const props = node.data?.hProperties
  if (!props || props['data-kb-embed'] === undefined) return false
  const kind = props['data-kb-embed-kind']
  const state = props['data-kb-embed-state']
  return (kind === 'base' || kind === 'markdown') && state === 'resolved'
}

function isBlankTextNode(node: MdNode): boolean {
  return node.type === 'text' && (node.value ?? '').trim() === ''
}

function promoteStandaloneEmbeds(parent: MdNode): void {
  if (!parent.children) return
  parent.children = parent.children.map((child) => {
    if (child.type === 'paragraph' && child.children) {
      const meaningful = child.children.filter((c) => !isBlankTextNode(c))
      if (meaningful.length === 1 && isPromotableBlockEmbedNode(meaningful[0] as MdNode)) {
        const embedNode = meaningful[0] as MdNode
        embedNode.data = {
          ...embedNode.data,
          hProperties: {
            ...(embedNode.data?.hProperties ?? {}),
            // Marks that THIS occurrence stood alone in its own paragraph —
            // the only shape a block-level renderer is safe to mount in
            // (nesting a table inside a <p> the note wrote around inline
            // text would be invalid HTML). An embed mixed inline with other
            // words keeps today's link-fallback treatment untouched.
            'data-kb-embed-standalone': '',
          },
        }
        return embedNode
      }
    }
    promoteStandaloneEmbeds(child)
    return child
  })
}

/**
 * remark plugin: a `![[Tasks.base#View]]` or `![[Note]]` that is the ONLY
 * thing in its paragraph is hoisted to replace that paragraph, so its
 * rendered content sits at block level — a sibling of other paragraphs and
 * headings — instead of nested inside a `<p>`, which the browser cannot
 * validly contain block content in and would silently reflow around.
 *
 * Runs AFTER `remarkKbWikilinks` (it reads the `data-kb-embed-*` attributes
 * that plugin already wrote) and is therefore appended per-render alongside
 * it in `KnowledgeBaseMarkdown`, not in the module-scope base list.
 */
export function remarkKbPromoteBlockEmbeds() {
  return (tree: unknown) => {
    promoteStandaloneEmbeds(tree as MdNode)
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Divergence 1 — the `a` slot
// ─────────────────────────────────────────────────────────────────────────────

/** What the reader knows about a wikilink's target.
 *
 *  `unknown` is a first-class state and is NOT the same as `unresolved`: before
 *  the link graph has loaded, the reader has no evidence either way, and
 *  marking a link "unresolved" on no evidence is the same class of confidently
 *  wrong answer this feature refuses everywhere else. */
export type KbLinkState = 'resolved' | 'unresolved' | 'unknown'

export interface KbLinkResolution {
  state: KbLinkState
  /** Collection-relative path of the resolved target, when known. */
  path?: string
}

export interface KnowledgeLinkContextValue {
  /** Collection-relative path of the note being read; relative links resolve
   *  against its directory. */
  notePath?: string
  /** Answers "does this wikilink target exist, and where?". Absent means the
   *  graph is not loaded — every wikilink is then `unknown`, never unresolved. */
  resolveWikilink?: (target: string, heading?: string) => KbLinkResolution
  /** Opens another note in the reader. Absent means links render but do not
   *  navigate (they never fall through to a full page load). */
  onNavigate?: (path: string, heading?: string) => void
  /** Scrolls to a heading inside the open note. */
  onHeadingLink?: (heading: string) => void
  /**
   * The REAL address of an in-collection note, so a resolved link is a link:
   * copyable, middle-clickable, openable in a new tab, and reachable with the
   * back button (FR-012). Given a collection-relative path, return the app
   * address for it — `KnowledgeBacklinks.libraryNoteHref` already builds one.
   *
   * Returning undefined (or omitting this) is honest rather than convenient:
   * the link then renders as a BUTTON, not as an `<a href>`. An anchor whose
   * href is a bare collection path (`notes/plan.md`) with navigation suppressed
   * by `event.preventDefault()` looks like a link and behaves like one for a
   * plain click, but middle-click, ctrl/cmd-click and "Open link in new tab"
   * never fire onClick — they navigate the browser to a relative URL that
   * resolves to nothing and take the reader out of the SPA. That is the exact
   * hazard the unresolved case documents and avoids by not being an anchor.
   */
  linkHref?: (collectionPath: string, heading?: string) => string | undefined
}

const KnowledgeLinkContext = createContext<KnowledgeLinkContextValue>({})

export const KnowledgeLinkProvider = KnowledgeLinkContext.Provider

/** The inherited link renderer — stage 1's `a` slot, which itself delegates
 *  external and unsafe hrefs to CHAT's renderer. Taken by reference so the
 *  scheme allow-list and the struck-through unsafe treatment cannot drift. */
const InheritedLink = kbMarkdownComponents.a as (props: ComponentPropsWithoutRef<'a'>) => ReactNode

/** A link whose target the reader has verified. Exported so any OTHER surface
 *  that resolves a wikilink against its own data (e.g. a base view's cells,
 *  KB-8b) draws the identical treatment instead of a second color choice that
 *  can drift from this one. */
export const LINK_CLASS =
  'text-[var(--color-accent)] underline underline-offset-2 hover:opacity-80 transition-opacity'

/**
 * A link whose target has NOT been verified — the `unknown` state, i.e. the
 * link graph has not loaded yet.
 *
 * It must not be drawn as a verified link. `unknown` had exactly the same
 * rendering as `resolved` — same accent colour, same solid underline, same
 * href, same live click handler — and differed only by a `data-kb-state`
 * attribute, which is invisible. The file already argues that marking a link
 * unresolved on no evidence is confidently wrong; drawing it as VERIFIED on no
 * evidence is the same error pointed the other way, and it is the more
 * expensive one, because the reader clicks it.
 *
 * A dashed rule and the note's own text colour say "this is a link, and nobody
 * has checked it yet" without claiming either verdict. The sr-only sentence
 * says it in words for anyone not reading the border.
 */
export const UNVERIFIED_LINK_CLASS =
  'text-[var(--color-secondary)] border-b border-dashed border-[var(--color-muted)] hover:opacity-80 transition-opacity'

/** Resolves a collection-relative href against the open note's directory.
 *
 *  Returns null when the link cannot address something inside the collection —
 *  an absolute path, or a traversal that climbs above the collection root
 *  (US-10 AS-1/AS-2). A null here is rendered as UNRESOLVED and is not
 *  navigable: the reading surface never asks for a path it was told not to
 *  reach. */
export function resolveCollectionPath(notePath: string | undefined, href: string): string | null {
  if (href === '' || href.startsWith('/')) return null
  const baseParts = (notePath ?? '').split('/').slice(0, -1)
  const out: string[] = [...baseParts]
  for (const segment of href.split('/')) {
    if (segment === '' || segment === '.') continue
    if (segment === '..') {
      if (out.length === 0) return null // climbs out of the collection
      out.pop()
      continue
    }
    out.push(segment)
  }
  return out.length === 0 ? null : out.join('/')
}

/** True for an href with its own scheme (`https:`, `javascript:`, `mailto:`) or
 *  a protocol-relative `//host/path` — i.e. NOT a collection path. Those are
 *  handed to the inherited renderer untouched. */
function hasOwnScheme(href: string): boolean {
  if (href.startsWith('//')) return true
  try {
    new URL(href)
    return true
  } catch {
    return false
  }
}

/** The inert "not verified as existing" treatment — no href, no click handler,
 *  same reasoning as the file header's UNVERIFIED_LINK_CLASS note. Exported for
 *  the same reason: reused as-is by any other surface honestly rendering the
 *  `unresolved` KbLinkState, rather than redrawn from a second copy. */
export function UnresolvedLink({ children, detail }: { children?: ReactNode; detail: string }) {
  return (
    <span
      data-testid="markdown-link"
      data-kb-unresolved="true"
      title={detail}
      className="text-[var(--color-muted)] border-b border-dotted border-[var(--color-muted)] cursor-not-allowed"
    >
      {children}
      <span className="sr-only"> (unresolved link: {detail})</span>
    </span>
  )
}

/**
 * One in-collection link, in the two renderings a click can safely have.
 *
 * WITH a real address (`linkHref` supplied) it is an anchor, and a modified
 * click is left entirely to the browser — that is the whole point of having a
 * URL. WITHOUT one it is a BUTTON: there is no href, so there is nothing to
 * middle-click, ctrl-click or "open in new tab" into a relative URL that
 * resolves to nothing. It never renders an anchor whose only defence against
 * leaving the SPA is `event.preventDefault()` in onClick, because three of the
 * four ways to follow a link never call onClick at all.
 */
function CollectionLink({
  path,
  heading,
  kind,
  target,
  verified,
  isEmbed,
  ambiguousDetail,
  children,
}: {
  /** Collection-relative path this link points at. */
  path: string
  heading?: string
  kind: 'wikilink' | 'path'
  /** What `data-kb-target` reports: the written target for a wikilink, the
   *  resolved path for a path link. */
  target: string
  /** True when the target's EXISTENCE has been confirmed. False renders the
   *  unverified treatment — see UNVERIFIED_LINK_CLASS. */
  verified: boolean
  isEmbed?: boolean
  /** Set when more than one graph edge matched (ADR-083 EMB-018): the first
   *  in response order is shown, and this names the alternatives instead of
   *  staying quiet about the tie-break. */
  ambiguousDetail?: string
  children?: ReactNode
}) {
  const ctx = useContext(KnowledgeLinkContext)
  const href = ctx.linkHref?.(path, heading)

  const shared = {
    'data-testid': 'markdown-link',
    'data-kb-link': kind,
    'data-kb-target': target,
    'data-kb-path': path,
    'data-kb-state': verified ? 'resolved' : 'unknown',
    ...(isEmbed ? { 'data-kb-embed': '' } : {}),
    ...(ambiguousDetail ? { 'data-kb-embed-ambiguous': '' } : {}),
  } as const

  const body = (
    <>
      {children}
      {!verified ? (
        <span className="sr-only">
          {' '}
          (link target not verified: this collection&rsquo;s link graph has not loaded, so Omnipus
          cannot say whether this note exists)
        </span>
      ) : null}
      {ambiguousDetail ? <span className="sr-only"> ({ambiguousDetail})</span> : null}
      {isEmbed ? <EmbedBadge /> : null}
    </>
  )

  const className = verified ? LINK_CLASS : UNVERIFIED_LINK_CLASS

  if (href !== undefined) {
    return (
      <a
        tabIndex={0}
        href={href}
        {...shared}
        className={className}
        onClick={(event) => {
          // A modified click means "open this somewhere else" and the href is
          // there precisely so the browser can honour it. Do not intercept.
          if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) {
            return
          }
          if (!ctx.onNavigate) return
          event.preventDefault()
          ctx.onNavigate(path, heading)
        }}
      >
        {body}
      </a>
    )
  }

  return (
    <button
      type="button"
      tabIndex={0}
      {...shared}
      className={`inline text-left align-baseline ${className}`}
      onClick={() => ctx.onNavigate?.(path, heading)}
    >
      {body}
    </button>
  )
}

/** The badge marking a fallback rendering as standing in for an embed —
 *  shared by every embed treatment that still is, in some sense, a link
 *  (resolved-but-no-renderer-kind, indeterminate, confirmed missing). */
function EmbedBadge() {
  return (
    <span className="ml-1 text-[10px] uppercase tracking-wide text-[var(--color-muted)]">
      embed shown as a link
    </span>
  )
}

/** Sized reserved space for an embed while the link graph request is still in
 *  flight (ADR-083 EMB-015). No text, no link, no marker of any kind — a
 *  placeholder claims nothing about whether the target exists. */
function LoadingEmbedPlaceholder() {
  return (
    <span
      data-testid="markdown-link"
      data-kb-embed=""
      data-kb-embed-state="loading"
      aria-hidden="true"
      className="inline-block h-5 w-24 align-middle rounded bg-[var(--color-muted)]/20 animate-pulse"
    />
  )
}

/** An embed the reader cannot say anything about because the note's WHOLE
 *  link graph is unavailable — the request failed, or it answered with
 *  nothing for a note that plainly has links to check (ADR-083 EMB-014). That
 *  condition gets exactly ONE page-level statement, rendered once by the
 *  caller (`KnowledgeNoteView`) — this per-embed spot renders neither a
 *  marker nor a persistent placeholder, only the note's own written text,
 *  inert. */
function GraphUnavailableEmbed({ children }: { children?: ReactNode }) {
  return (
    <span
      data-testid="markdown-link"
      data-kb-embed=""
      data-kb-embed-state="graph_unavailable"
      className="text-[var(--color-secondary)]"
    >
      {children}
      <span className="sr-only">
        {' '}
        (this note&rsquo;s link graph is unavailable right now — see the notice above)
      </span>
    </span>
  )
}

/** The "could not be checked" marker (ADR-083 EMB-013) — visually and
 *  textually DISTINCT from `UnresolvedLink`'s missing-file marker.
 *  Rendering the missing-file marker for this state would assert absence
 *  from evidence the graph has already said is incomplete — a skipped
 *  directory, a truncated answer, or simply no matching edge — which is the
 *  exact false statement this state exists to prevent. */
function IndeterminateEmbedLink({ children, reason }: { children?: ReactNode; reason: string }) {
  return (
    <span
      data-testid="markdown-link"
      data-kb-embed=""
      data-kb-embed-state="indeterminate"
      title={reason}
      className="text-[var(--color-muted)] border-b border-dotted border-[var(--color-warning)] cursor-not-allowed"
    >
      {children}
      <span className="sr-only"> (could not be checked: {reason})</span>
      <EmbedBadge />
    </span>
  )
}

/** A refusal distinguished from "this file does not exist" (ADR-083 EMB-017,
 *  EMB-023, EMB-024): the written target tried to leave the collection root.
 *  The escaping path is deliberately NOT shown here — EMB-017 requires the
 *  reader's marker to contain no path segment (unlike the agent surface,
 *  EMB-024, which does show it). */
function ContainmentRefusedEmbed({ children }: { children?: ReactNode }) {
  return (
    <span
      data-testid="markdown-link"
      data-kb-embed=""
      data-kb-embed-state="unresolved"
      data-kb-embed-outside-root="true"
      title="this embed points outside the knowledge base and was refused"
      className="text-[var(--color-muted)] border-b border-dotted border-[var(--color-error)] cursor-not-allowed"
    >
      {children}
      <span className="sr-only"> (embed refused: it points outside this knowledge base)</span>
      <EmbedBadge />
    </span>
  )
}

/**
 * Dispatches an embed's FALLBACK rendering (the mdast `image` short-circuit
 * in `remarkKbWikilinks` already handled the one case that renders inline)
 * on the resolver's own state — never on `ctx.resolveWikilink`, which is a
 * different resolver with a different match key (ADR-083 EMB-011's `embed`
 * flag). This is what keeps an embed's verdict from being read off the
 * wrong edge when a plain `[[Note]]` and an embed `![[Note]]` of the same
 * target coexist in one document.
 */
function EmbedFallback({
  target,
  state,
  path,
  reason,
  outsideRoot,
  ambiguous,
  candidates,
  children,
}: {
  target: string
  state: string
  path?: string
  reason?: string
  outsideRoot: boolean
  ambiguous: boolean
  candidates?: string
  children?: ReactNode
}) {
  if (state === 'loading') return <LoadingEmbedPlaceholder />
  if (state === 'graph_unavailable') return <GraphUnavailableEmbed>{children}</GraphUnavailableEmbed>
  if (state === 'indeterminate') {
    return <IndeterminateEmbedLink reason={reason ?? 'no reason available'}>{children}</IndeterminateEmbedLink>
  }
  if (state === 'unresolved') {
    if (outsideRoot) return <ContainmentRefusedEmbed>{children}</ContainmentRefusedEmbed>
    return (
      <UnresolvedLink detail={reason ?? `no file in this collection matches "${target}"`}>
        {children}
      </UnresolvedLink>
    )
  }
  // 'resolved' — the target exists, but this embed's kind has no inline
  // renderer yet (or the image branch's own URL check failed despite a
  // resolved edge): a real, working link to the file, styled and badged
  // exactly like any other confirmed embed shown as a link.
  return (
    <CollectionLink
      path={path ?? target}
      kind="wikilink"
      target={target}
      verified
      isEmbed
      ambiguousDetail={
        ambiguous
          ? `more than one file matched this embed's target; showing the first — also matches: ${candidates ?? 'no other candidates were reported'}`
          : undefined
      }
    >
      {children}
    </CollectionLink>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// The two rich inline mounts — a saved data view (ADR-083 Step 2, dashboards)
// and a transcluded note (Step 3). Both are reached ONLY for a STANDALONE,
// RESOLVED embed (see remarkKbPromoteBlockEmbeds above); an embed mixed
// inline with other text, or not yet resolved, keeps EmbedFallback's
// existing treatment. `KbBaseEmbedMount`/`KbTransclusionMount` are thin
// LazyEmbedMount wrappers ONLY — no query, not even the "which view does
// this resolve to" lookup, is issued until the embed is near the viewport
// (EMB-065). The real work (`KbBaseEmbedContent`/`KbTransclusionContent`)
// lives entirely inside LazyEmbedMount's children, so a forty-embed
// dashboard issues zero network requests for anything below the fold on
// first paint — the mount budget is proven once, generically, in
// LazyEmbedMount's own tests, not re-implemented per kind here.
// ─────────────────────────────────────────────────────────────────────────────

// Matches INLINE_PREVIEW_BOX_CLASS's 28rem (libraryPreviewVariant.ts) — the
// height BasePreview's own inline box settles to, so mounting causes no
// reflow at all, not merely "one accepted reflow" (EMB-066).
const BASE_EMBED_RESERVED_HEIGHT_PX = 448
// EMB-066's own words: "a transclusion's reserved height is a fixed three
// lines" — three lines at this reader's body line-height.
const TRANSCLUSION_RESERVED_HEIGHT_PX = 72

/** EMB-060/N1: every embed inside a transcluded note — whatever kind it is —
 *  falls back to a link with a stated reason, NEVER `indeterminate` (the
 *  could-not-be-checked marker EMB-060 explicitly forbids here) and NEVER
 *  `resolved`. A resolver that never resolves is what makes a second level
 *  of transclusion structurally UNREACHABLE rather than merely undetected:
 *  remarkKbWikilinks only ever emits a promotable, rich-renderable node when
 *  `resolution.state === 'resolved'`, so a nested note's own embeds can
 *  never produce one — there is nothing for a loop detector or a depth cap
 *  to catch, because there is no second level for either to reach. */
function nestedTranscludedEmbedResolver(): EmbedResolution {
  return {
    state: 'unresolved',
    reason: 'embeds inside a transcluded note are shown as links — open the note itself to see them',
  }
}

function EmbedMountPlaceholder() {
  return (
    <div
      data-testid="kb-embed-mount-loading"
      aria-hidden="true"
      className="flex items-center justify-center gap-2 rounded-md border border-[var(--color-border)] px-3 py-6 text-xs text-[var(--color-muted)]"
    >
      <SpinnerGap size={14} className="animate-spin" /> Loading…
    </div>
  )
}

function EmbedMountError({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div
      data-testid="kb-embed-mount-error"
      className="flex flex-col items-center gap-2 rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/5 px-3 py-6 text-center text-xs text-[var(--color-warning)]"
    >
      <Warning size={16} />
      <span>{message}</span>
      {onRetry && (
        <button
          type="button"
          tabIndex={0}
          onClick={onRetry}
          className="text-[11px] underline underline-offset-2"
        >
          Retry
        </button>
      )}
    </div>
  )
}

function dirnameOf(path: string): string {
  const i = path.lastIndexOf('/')
  return i <= 0 ? '' : path.slice(0, i)
}

/** ADR-083 Step 2 — a `![[Data.base#View]]` standing alone in its paragraph.
 *  Resolves the VIEW inside the already-resolved FILE: fetches this file's
 *  own directory listing to get a full `LibraryEntry` (a graph edge carries
 *  neither `size`, `modified_at` nor `is_text_editable`, all required by
 *  `BasePreview`'s prop — there is no single-entry GET, so the parent
 *  directory's listing is the one endpoint that already returns them) and
 *  this file's views, matches the written fragment against the server's
 *  display labels (EMB-040/EMB-041), and mounts the SAME `BasePreview` the
 *  full pane uses (EMB-027) — never a second renderer. */
function KbBaseEmbedMount({
  workspaceId,
  workspacePath,
  viewFragment,
}: {
  workspaceId: string
  workspacePath: string
  viewFragment?: string
}) {
  // EMB-065: no fetch of any kind — not even the "which view does this
  // resolve to" lookup — starts until the embed is near the viewport. The
  // actual work lives in KbBaseEmbedContent, mounted only as LazyEmbedMount's
  // children; this outer component's only job is deciding WHEN.
  return (
    <LazyEmbedMount reservedHeight={BASE_EMBED_RESERVED_HEIGHT_PX} className="my-3 block">
      <KbBaseEmbedContent workspaceId={workspaceId} workspacePath={workspacePath} viewFragment={viewFragment} />
    </LazyEmbedMount>
  )
}

function KbBaseEmbedContent({
  workspaceId,
  workspacePath,
  viewFragment,
}: {
  workspaceId: string
  workspacePath: string
  viewFragment?: string
}) {
  const ctx = useContext(KnowledgeLinkContext)
  const parentDir = useMemo(() => dirnameOf(workspacePath), [workspacePath])

  const entriesQuery = useQuery({
    queryKey: libraryQueryKeys.entries(workspaceId, parentDir, false),
    queryFn: () => fetchLibraryEntries(workspaceId, parentDir, false),
    staleTime: 30_000,
  })
  // EMB-045: the SAME query key BasePreview's own internal fetch uses once
  // mounted below, so N embeds of one file — and this resolving lookup
  // alongside them — share ONE network request via the query cache, not one
  // fetch each.
  const viewsQuery = useQuery({
    queryKey: ['library', workspaceId, 'knowledge', 'base-views', workspacePath],
    queryFn: () => fetchKnowledgeBaseViews(workspaceId, workspacePath),
    staleTime: 10_000,
    refetchOnWindowFocus: false,
  })

  if (entriesQuery.isLoading || viewsQuery.isLoading) return <EmbedMountPlaceholder />

  const entry = entriesQuery.data?.find((e) => e.path === workspacePath)
  if (entriesQuery.isError || !entry) {
    return (
      <EmbedMountError message="Could not read this file's details." onRetry={() => void entriesQuery.refetch()} />
    )
  }
  if (viewsQuery.isError || !viewsQuery.data) {
    return (
      <EmbedMountError
        message="Could not read this base file's views."
        onRetry={() => void viewsQuery.refetch()}
      />
    )
  }

  const match = matchBaseView(viewsQuery.data.views, viewFragment)

  if (match.kind === 'not_found') {
    const labels = viewsQuery.data.views.map((v) => v.label)
    return (
      <div
        data-testid="kb-base-embed-missing-view"
        className="rounded-md border border-dashed border-[var(--color-warning)]/50 px-3 py-3 text-xs text-[var(--color-warning)]"
      >
        {viewFragment ? `No view named "${viewFragment}" in ${entry.name}.` : `${entry.name} declares no views.`}
        {labels.length > 0 && <> Views that do exist: {labels.join(', ')}.</>}
      </div>
    )
  }

  if (match.kind === 'ambiguous') {
    return (
      <div
        data-testid="kb-base-embed-ambiguous-view"
        className="rounded-md border border-dashed border-[var(--color-warning)]/50 px-3 py-3 text-xs text-[var(--color-warning)]"
      >
        The view label "{match.label}" matches more than one view in {entry.name} — rename one to make this
        embed unambiguous. Matches: {match.matches.map((v) => v.name).join(', ')}.
      </div>
    )
  }

  const embed: BasePreviewEmbedOptions = {
    viewName: match.view.name,
    showViewSwitcher: !match.chosenByDefault,
    ...(match.chosenByDefault
      ? { caption: `Showing "${match.view.label}" — the first available view; this embed did not choose one.` }
      : {}),
    ...(ctx.resolveWikilink ? { resolveWikilink: ctx.resolveWikilink } : {}),
    ...(ctx.linkHref ? { linkHref: ctx.linkHref } : {}),
  }

  return (
    <BasePreview
      workspaceId={workspaceId}
      entry={entry}
      variant="inline"
      embed={embed}
      {...(ctx.onNavigate ? { onOpenNote: (p: string) => ctx.onNavigate?.(p) } : {})}
    />
  )
}

/** ADR-083 Step 3 — a `![[Note]]` / `![[Note#Heading]]` / `![[Note#^block]]`
 *  standing alone in its paragraph. Fetches the SAME content request the
 *  full-screen preview uses (so a note already open costs nothing extra,
 *  EMB-059), slices it CLIENT-SIDE, and renders the slice through the SAME
 *  composition — with `resolveEmbedUrl` replaced by a resolver that NEVER
 *  resolves (EMB-060/N1). `resolveWikilink` is deliberately OMITTED for the
 *  nested pass: the outer note's link-graph answer only covers the OUTER
 *  note's own outbound edges, so reusing it here would read absence-from-a-
 *  different-note's-edges as "this note does not exist" — a confidently
 *  wrong answer of exactly the kind this whole feature refuses. Plain links
 *  inside the transcluded note render `unknown` (honestly unverified)
 *  instead, and stay navigable via the same `linkHref`/`onNavigate`. */
function KbTransclusionMount({
  workspaceId,
  workspacePath,
  collectionPath,
  heading,
  block,
}: {
  workspaceId: string
  workspacePath: string
  /** Collection-relative — the transcluded note's OWN relative-link base,
   *  distinct from `workspacePath` (the fetch address). */
  collectionPath: string
  heading?: string
  block?: string
}) {
  // EMB-065: the content fetch itself does not start until the embed is near
  // the viewport — see KbBaseEmbedMount's identical note above.
  return (
    <LazyEmbedMount reservedHeight={TRANSCLUSION_RESERVED_HEIGHT_PX} className="my-3 block">
      <KbTransclusionContent
        workspaceId={workspaceId}
        workspacePath={workspacePath}
        collectionPath={collectionPath}
        heading={heading}
        block={block}
      />
    </LazyEmbedMount>
  )
}

function KbTransclusionContent({
  workspaceId,
  workspacePath,
  collectionPath,
  heading,
  block,
}: {
  workspaceId: string
  workspacePath: string
  collectionPath: string
  heading?: string
  block?: string
}) {
  const ctx = useContext(KnowledgeLinkContext)
  const contentQuery = useQuery({
    queryKey: libraryQueryKeys.content(workspaceId, workspacePath),
    queryFn: () => fetchLibraryContent(workspaceId, workspacePath),
  })

  if (contentQuery.isLoading) return <EmbedMountPlaceholder />
  if (contentQuery.isError || !contentQuery.data) {
    return <EmbedMountError message="Could not read this note." onRetry={() => void contentQuery.refetch()} />
  }

  const raw = contentQuery.data
  if (raw.is_text !== true || raw.too_large === true || raw.content === undefined) {
    return <EmbedMountError message="This note is too large to show inline." />
  }

  const sliced = sliceTranscludedContent(raw.content, heading, block)

  // EMB-062: a stated empty region — never blank space, never a failure
  // marker. Checked before "not found" so a genuinely empty note (no
  // fragment asked for) never falls through to the wrong message.
  if (sliced.found && sliced.text.trim() === '') {
    return (
      <div
        data-testid="kb-transclusion-empty"
        className="rounded-md border border-dashed border-[var(--color-border)] px-3 py-4 text-xs text-[var(--color-muted)]"
      >
        This note is empty.
      </div>
    )
  }

  if (!sliced.found) {
    return (
      <div
        data-testid="kb-transclusion-not-found"
        className="rounded-md border border-dashed border-[var(--color-warning)]/50 px-3 py-4 text-xs text-[var(--color-warning)]"
      >
        {block
          ? `No block anchored "${block}" was found in this note.`
          : `No heading "${heading}" was found in this note.`}
      </div>
    )
  }

  return (
    <div
      data-testid="kb-transclusion"
      className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)]/40 px-4 py-3"
    >
      <KnowledgeBaseMarkdown
        content={sliced.text}
        notePath={collectionPath}
        {...(ctx.onNavigate ? { onNavigate: ctx.onNavigate } : {})}
        {...(ctx.onHeadingLink ? { onHeadingLink: ctx.onHeadingLink } : {})}
        {...(ctx.linkHref ? { linkHref: ctx.linkHref } : {})}
        resolveEmbedUrl={nestedTranscludedEmbedResolver}
      />
    </div>
  )
}

/**
 * The KB `a` slot — the only replaced entry in the components map.
 *
 * Four recognised shapes, then delegation:
 *   1. a wikilink node from `remarkKbWikilinks`;
 *   2. an in-document heading link (`#slug`);
 *   3. a collection-relative path link (`notes/plan.md`, `./a.md`);
 *   4. anything else → the INHERITED renderer, unchanged.
 *
 * Unresolved targets render as a non-anchor (FR-065): there is no `href` to
 * follow and no click handler, so clicking cannot navigate — a disabled-looking
 * `<a href>` would still navigate on middle-click or Enter.
 */
function KnowledgeMarkdownLink(
  props: ComponentPropsWithoutRef<'a'> & {
    'data-kb-wikilink'?: string
    'data-kb-target'?: string
    'data-kb-heading'?: string
    'data-kb-block'?: string
    'data-kb-embed'?: string
    'data-kb-embed-kind'?: string
    'data-kb-embed-state'?: string
    'data-kb-embed-path'?: string
    'data-kb-embed-reason'?: string
    'data-kb-embed-outside-root'?: string
    'data-kb-embed-ambiguous'?: string
    'data-kb-embed-candidates'?: string
    'data-kb-embed-standalone'?: string
    'data-kb-embed-workspace-id'?: string
    'data-kb-embed-workspace-path'?: string
  },
) {
  const { href, children } = props
  const ctx = useContext(KnowledgeLinkContext)
  const isWikilink = props['data-kb-wikilink'] !== undefined

  if (isWikilink) {
    const target = props['data-kb-target'] ?? ''
    const heading = props['data-kb-heading']
    const isEmbed = props['data-kb-embed'] !== undefined

    // `[[#Heading]]` — same note. An embed of the same form (`![[#Heading]]`)
    // is self-referential transclusion, out of scope (ADR-083 US-7) — it
    // shares this branch harmlessly, since there is nothing more useful to
    // do with an empty target than scroll to the heading.
    if (target === '' && heading) {
      return (
        <a
          tabIndex={0}
          href={`#${heading}`}
          data-testid="markdown-link"
          data-kb-heading-link="true"
          className={LINK_CLASS}
          onClick={(event) => {
            event.preventDefault()
            ctx.onHeadingLink?.(heading)
          }}
        >
          {children}
        </a>
      )
    }

    if (isEmbed) {
      // ADR-083 Step 2/Step 3 — a `.base` or markdown embed that stood ALONE
      // in its own paragraph (remarkKbPromoteBlockEmbeds marked it
      // `data-kb-embed-standalone`) and resolved to a real file mounts the
      // rich block renderer instead of the link fallback below. Everything
      // else — mixed inline with other text, unresolved, indeterminate,
      // loading, graph_unavailable — keeps the existing treatment untouched.
      const standalone = props['data-kb-embed-standalone'] !== undefined
      const embedKind = props['data-kb-embed-kind']
      const embedState = props['data-kb-embed-state']
      const embedWorkspaceId = props['data-kb-embed-workspace-id']
      const embedWorkspacePath = props['data-kb-embed-workspace-path']
      if (standalone && embedState === 'resolved' && embedWorkspaceId && embedWorkspacePath) {
        if (embedKind === 'base') {
          return (
            <KbBaseEmbedMount
              workspaceId={embedWorkspaceId}
              workspacePath={embedWorkspacePath}
              viewFragment={heading}
            />
          )
        }
        if (embedKind === 'markdown') {
          return (
            <KbTransclusionMount
              workspaceId={embedWorkspaceId}
              workspacePath={embedWorkspacePath}
              collectionPath={props['data-kb-embed-path'] ?? target}
              heading={heading}
              block={props['data-kb-block']}
            />
          )
        }
      }

      return (
        <EmbedFallback
          target={target}
          state={props['data-kb-embed-state'] ?? 'indeterminate'}
          {...(props['data-kb-embed-path'] !== undefined ? { path: props['data-kb-embed-path'] } : {})}
          {...(props['data-kb-embed-reason'] !== undefined ? { reason: props['data-kb-embed-reason'] } : {})}
          outsideRoot={props['data-kb-embed-outside-root'] !== undefined}
          ambiguous={props['data-kb-embed-ambiguous'] !== undefined}
          {...(props['data-kb-embed-candidates'] !== undefined
            ? { candidates: props['data-kb-embed-candidates'] }
            : {})}
        >
          {children}
        </EmbedFallback>
      )
    }

    const resolution: KbLinkResolution = ctx.resolveWikilink
      ? ctx.resolveWikilink(target, heading)
      : { state: 'unknown' }

    if (resolution.state === 'unresolved') {
      return (
        <UnresolvedLink detail={`no note in this collection matches "${target}"`}>{children}</UnresolvedLink>
      )
    }

    const path = resolution.path ?? target
    return (
      <CollectionLink
        path={path}
        heading={heading}
        kind="wikilink"
        target={target}
        // `unknown` is drawn differently from `resolved` — the graph has not
        // loaded, so nobody has checked this target either way.
        verified={resolution.state === 'resolved'}
      >
        {children}
      </CollectionLink>
    )
  }

  const raw = href ?? ''

  // In-document heading link.
  if (raw.startsWith('#')) {
    const heading = raw.slice(1)
    return (
      <a
        tabIndex={0}
        href={raw}
        data-testid="markdown-link"
        data-kb-heading-link="true"
        className={LINK_CLASS}
        onClick={(event) => {
          event.preventDefault()
          ctx.onHeadingLink?.(heading)
        }}
      >
        {children}
      </a>
    )
  }

  // Anything carrying its own scheme is not a collection path — inherited.
  if (raw === '' || hasOwnScheme(raw)) {
    return <InheritedLink {...props} />
  }

  const [pathPart, hashPart] = raw.split('#')
  const resolved = resolveCollectionPath(ctx.notePath, pathPart)
  if (resolved === null) {
    return (
      <UnresolvedLink detail={`"${raw}" points outside this collection`}>{children}</UnresolvedLink>
    )
  }

  // A path link's CONTAINMENT is proven — resolveCollectionPath above returned
  // a path inside the collection or refused — so it is drawn as a real link.
  // That is a different claim from "this file exists", which nothing here
  // checks; existence is the link graph's answer and the graph is not consulted
  // for path links.
  return (
    <CollectionLink
      path={resolved}
      {...(hashPart ? { heading: hashPart } : {})}
      kind="path"
      target={resolved}
      verified
    >
      {children}
    </CollectionLink>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// The composition
// ─────────────────────────────────────────────────────────────────────────────

// MODULE SCOPE — see the perf note in this file's header (FR-013c). Every entry
// except `a` is inherited by reference from stage 1, which inherits chat's.
export const knowledgeMarkdownComponents = {
  ...kbMarkdownComponents,
  a: KnowledgeMarkdownLink,
}

/** The parameterless half of the appended remark plugins, module scope.
 *
 *  Stage 1's list (`remarkGfm`, `remarkMath`, `remarkStripPrivateComments`) is
 *  SPREAD IN, not restated — `%%…%%` stripping has one implementation, because
 *  a transform that HIDES text is the last thing that should exist twice.
 *
 *  Order is deliberate:
 *   • frontmatter first, while node positions still describe the original
 *     source — it is the only plugin here that reads them;
 *   • comment stripping next, so a wikilink or highlight inside `%%…%%` never
 *     becomes a rendered element;
 *   • highlights before wikilinks (appended at render time), so `==[[Note]]==`
 *     yields a highlighted, working link rather than stray `==` text. */
export const KB_BASE_REMARK_PLUGINS = [
  remarkKbFrontmatter,
  ...KB_REMARK_PLUGINS,
  remarkKbCallouts,
  remarkKbHighlights,
]

export { KB_REHYPE_PLUGINS }

export interface KnowledgeBaseMarkdownProps extends KnowledgeLinkContextValue, KbWikilinkOptions {
  content: string
}

/**
 * The knowledge-base reading view: chat's markdown pipeline, composed once more
 * with a KB link slot and the appended KB remark plugins.
 *
 * The plugin ARRAY is memoized on `resolveEmbedUrl` rather than being a module
 * constant, because the wikilink plugin needs per-collection data. That is a
 * different constraint from FR-013c, which is about the COMPONENTS map:
 * react-markdown keys components by object reference (a fresh map remounts
 * every element), while plugins are re-run on every render regardless. Callers
 * should still pass a stable `resolveEmbedUrl`.
 */
export function KnowledgeBaseMarkdown({
  content,
  resolveEmbedUrl,
  ...link
}: KnowledgeBaseMarkdownProps) {
  const remarkPlugins = useMemo(
    () =>
      [
        ...KB_BASE_REMARK_PLUGINS,
        [remarkKbWikilinks, { resolveEmbedUrl }],
        // Runs AFTER remarkKbWikilinks — it reads the data-kb-embed-* shape
        // that plugin just wrote (ADR-083 Step 2/3 block promotion).
        remarkKbPromoteBlockEmbeds,
      ] as RemarkPlugins,
    [resolveEmbedUrl],
  )
  const linkValue = useMemo<KnowledgeLinkContextValue>(
    () => ({
      notePath: link.notePath,
      resolveWikilink: link.resolveWikilink,
      onNavigate: link.onNavigate,
      onHeadingLink: link.onHeadingLink,
      linkHref: link.linkHref,
    }),
    [link.notePath, link.resolveWikilink, link.onNavigate, link.onHeadingLink, link.linkHref],
  )

  return (
    <KnowledgeLinkProvider value={linkValue}>
      <ReactMarkdown
        remarkPlugins={remarkPlugins}
        rehypePlugins={KB_REHYPE_PLUGINS}
        components={knowledgeMarkdownComponents}
      >
        {content}
      </ReactMarkdown>
    </KnowledgeLinkProvider>
  )
}
