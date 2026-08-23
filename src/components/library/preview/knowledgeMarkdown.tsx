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
// From kbMarkdownBase, NOT from LibraryMarkdownPreview: that file now mounts
// the stage-2 reading view this module is part of, so importing it here would
// be a cycle — and `kbMarkdownComponents` is read at module scope below, which
// is where a cycle crashes instead of merely warning.
import { kbMarkdownComponents, KB_REHYPE_PLUGINS, KB_REMARK_PLUGINS } from './kbMarkdownBase'

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

const WIKILINK_RE = /(!?)\[\[([^[\]\n]+)\]\]/g

const IMAGE_EXTENSIONS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'avif', 'ico'])

export interface ParsedWikilink {
  /** Path or basename before `#` and `|`. Empty for a same-note heading link. */
  target: string
  /** Heading after `#`, if any. */
  heading?: string
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
  const heading = hash === -1 ? undefined : head.slice(hash + 1).trim() || undefined
  if (target === '' && !heading) return null

  return {
    target,
    heading,
    text: alias && alias !== '' ? alias : head,
    embed,
  }
}

function extensionOf(target: string): string {
  const dot = target.lastIndexOf('.')
  return dot === -1 ? '' : target.slice(dot + 1).toLowerCase()
}

export interface KbWikilinkOptions {
  /**
   * Resolves an embedded attachment (`![[diagram.png]]`) to a URL the browser
   * can load. Returning undefined is the HONEST answer when the collection has
   * not been resolved yet: the embed then renders as a visibly-marked reference
   * instead of a broken image icon.
   */
  resolveEmbedUrl?: (target: string) => string | undefined
}

/**
 * remark plugin: `[[Note]]`, `[[Note|alias]]`, `[[Note#Heading]]`,
 * `[[folder/Note]]` and `![[image.png]]`.
 *
 * A wikilink becomes an ordinary `link` node carrying its parts as data
 * attributes, so it lands on the `a` slot — the one slot this composition is
 * permitted to replace. Resolution (does that note exist?) happens THERE, from
 * React context, because it is per-note data and a remark plugin cannot read
 * context. The plugin itself makes no claim about whether a target exists.
 *
 * An image embed becomes an `image` node and renders through CHAT's `img`
 * renderer, untouched — resolving the URL here is what keeps that slot
 * inherited rather than replaced.
 */
export function remarkKbWikilinks(options: KbWikilinkOptions = {}) {
  return (tree: unknown) => {
    mapTextNodes(tree as MdNode, (node) =>
      splitTextNode(node, WIKILINK_RE, (match) => {
        const parsed = parseWikilink(match[2], match[1] === '!')
        if (!parsed) return null

        if (parsed.embed && IMAGE_EXTENSIONS.has(extensionOf(parsed.target))) {
          const url = options.resolveEmbedUrl?.(parsed.target)
          if (url) {
            return { type: 'image', url, alt: parsed.text, children: [] }
          }
          // Fall through: an unresolvable embed is reported, never faked.
        }

        return {
          type: 'link',
          url: '',
          data: {
            hProperties: {
              'data-kb-wikilink': '',
              'data-kb-target': parsed.target,
              ...(parsed.heading ? { 'data-kb-heading': parsed.heading } : {}),
              ...(parsed.embed ? { 'data-kb-embed': '' } : {}),
            },
          },
          children: [{ type: 'text', value: parsed.text }],
        }
      }),
    )
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

/** A link whose target the reader has verified. */
const LINK_CLASS =
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
const UNVERIFIED_LINK_CLASS =
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

function UnresolvedLink({ children, detail }: { children?: ReactNode; detail: string }) {
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
      {isEmbed ? (
        <span className="ml-1 text-[10px] uppercase tracking-wide text-[var(--color-muted)]">
          embed shown as a link
        </span>
      ) : null}
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
    'data-kb-embed'?: string
  },
) {
  const { href, children } = props
  const ctx = useContext(KnowledgeLinkContext)
  const isWikilink = props['data-kb-wikilink'] !== undefined

  if (isWikilink) {
    const target = props['data-kb-target'] ?? ''
    const heading = props['data-kb-heading']
    const isEmbed = props['data-kb-embed'] !== undefined

    // `[[#Heading]]` — same note.
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
        isEmbed={isEmbed}
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
    () => [...KB_BASE_REMARK_PLUGINS, [remarkKbWikilinks, { resolveEmbedUrl }]] as RemarkPlugins,
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
