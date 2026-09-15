// wikilinkNotation.ts — the `[[…]]` / `![[…]]` NOTATION parser, and nothing
// else. One module, one job: recognise the notation and say what it means.
//
// WHY ITS OWN FILE. `knowledgeMarkdown.tsx` owns the knowledge-base markdown
// COMPOSITION, which imports every inline embed mount it can dispatch to
// (`KbQueryFenceEmbed`, `KbAudioEmbedMount`, …). Those mounts in turn need
// the parser — a query fence shows search snippets, which are raw byte
// excerpts of note text and therefore arrive with wikilink brackets intact
// (WL-2). Importing the parser back out of the composition module would make
// a real runtime import cycle: composition -> mount -> composition. Parsing
// notation depends on nothing in the composition, so it moves down here and
// both sides import it.
//
// `knowledgeMarkdown.tsx` re-exports these names so its existing importers
// (the outline and backlink rails, the Library search bar, the tests) are
// unaffected by where the definitions physically live.

export const WIKILINK_RE = /(!?)\[\[([^[\]\n]+)\]\]/g

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
  /** 1-based page number from a `[[doc.pdf#page=3]]` fragment (ADR-083 Step 6,
   *  EMB-105, US-12 AS-4) — a THIRD fragment form alongside heading and block,
   *  mutually exclusive with both. Whether that page actually exists in the
   *  document is a render-time question `LibraryPdfPreview` already answers;
   *  parsing only recognises the notation. */
  page?: number
  /** Display text: the alias when one was given, else the raw inner text.
   *  Never the raw digits of a `width` (below) — a bar segment is read as
   *  EITHER a size OR a caption, never both, and never the wrong one. */
  text: string
  /** True for the `![[…]]` embed form. */
  embed: boolean
  /** Pixel width from `![[target|400]]` / `![[target|400x300]]` (a height, if
   *  given, is read and discarded — see `EMBED_WIDTH_PATTERN`'s own doc).
   *  Only ever populated for the EMBED form (ADR-083 EMB-030: "a size given
   *  after a bar applies to pictures only", and only an embed can be a
   *  picture) — a plain `[[target|400]]` reference link's bar segment is
   *  always a caption, even when it happens to look like digits. Whether the
   *  TARGET actually turns out to be a picture is decided later, from its
   *  resolved kind — this field only reports what the notation itself said. */
  width?: number
}

/** `pkg/knowledge/knowledge_edit.go`'s own `embedWidthPattern` — EMB-030's
 *  exact data constraint ("A size given after `|` in an embed MUST match
 *  `^\d+(x\d+)?$` to be read as a size; anything else is display text."),
 *  mirrored here so read and write agree on what counts as a size rather
 *  than each having its own idea. Capture group 1 is the WIDTH only; an
 *  optional `x<height>` suffix is recognised (so it is never mistaken for
 *  caption text) but discarded — `LibraryImagePreview`'s existing `width`
 *  prop is the one sizing path this reads into, and it takes pixels wide,
 *  not a separate height. */
const EMBED_WIDTH_PATTERN = /^(\d+)(?:x\d+)?$/

/** A `page=N` fragment (ADR-083 Step 6, EMB-105) — the third form a `#`
 *  fragment can take, alongside a heading and a `^`-prefixed block anchor.
 *  Recognised on any target's fragment, not just a `.pdf` one: whether the
 *  target is actually a PDF is `classifyEmbedKind`'s job, downstream of
 *  parsing, not this pattern's. */
const PAGE_FRAGMENT_PATTERN = /^page=(\d+)$/

/** Parses the inside of a `[[…]]`, in Obsidian's order: alias last, heading
 *  before it. Exported because the outline/backlink rails parse the same forms.
 *  Returns null for an empty or whitespace-only body. */
export function parseWikilink(inner: string, embed = false): ParsedWikilink | null {
  const bar = inner.indexOf('|')
  const head = (bar === -1 ? inner : inner.slice(0, bar)).trim()
  const barContent = bar === -1 ? undefined : inner.slice(bar + 1).trim()
  if (head === '' && !barContent) return null

  const hash = head.indexOf('#')
  const target = (hash === -1 ? head : head.slice(0, hash)).trim()
  const fragment = hash === -1 ? undefined : head.slice(hash + 1).trim() || undefined
  if (target === '' && !fragment) return null

  // `^` prefixes a block anchor, not a heading (Obsidian's own distinction —
  // see the `block` field's doc comment above). `page=N` is the third,
  // mutually exclusive form (see PAGE_FRAGMENT_PATTERN's own doc).
  const isBlock = fragment !== undefined && fragment.startsWith('^')
  const pageMatch = !isBlock && fragment !== undefined ? PAGE_FRAGMENT_PATTERN.exec(fragment) : null
  const heading = isBlock || pageMatch ? undefined : fragment
  const block = isBlock ? fragment.slice(1) || undefined : undefined
  const page = pageMatch ? Number.parseInt(pageMatch[1] as string, 10) : undefined

  // EMB-030: on an EMBED, a bar segment shaped like a size is read as one —
  // never as the caption a size was never meant to be, even when the target
  // turns out not to be a picture (there it is simply inert, per EMB-030's
  // own "applies only to pictures" rule) rather than silently eating the
  // display text. A plain (non-embed) wikilink never has a size to give, so
  // its bar segment is always an alias, digits or not.
  const widthMatch = embed && barContent !== undefined ? EMBED_WIDTH_PATTERN.exec(barContent) : null
  const width = widthMatch ? Number.parseInt(widthMatch[1] as string, 10) : undefined
  const alias = widthMatch ? undefined : barContent

  return {
    target,
    heading,
    block,
    text: alias && alias !== '' ? alias : head,
    embed,
    ...(width !== undefined ? { width } : {}),
    ...(page !== undefined ? { page } : {}),
  }
}

/** WL-2: a search hit's snippet, or a record's cell value, is a RAW excerpt
 *  of file content — frontmatter included, since the search engine never
 *  renders markdown, it just returns bytes around the matched term. A note
 *  whose match falls inside a `[[wikilink]]` (very common: `owner:
 *  "[[Daniel Piatkowski]]"` is ordinary frontmatter) therefore arrives with
 *  the brackets still on it, on every surface that shows an excerpt rather
 *  than running the note through `KnowledgeReader`.
 *
 *  Fixed the same way the sibling defect was: reuse the note reader's own
 *  parser instead of writing a second one. This strips the notation down to
 *  the SAME display text `remarkKbWikilinks` would produce (the alias when
 *  one was given, else the raw target), so the example above reads as
 *  `owner: "Daniel Piatkowski"`.
 *
 *  Lives HERE, exported, rather than privately in one consumer: the Library
 *  search bar and the note reader's own `query` fence both show excerpts
 *  from the same engine's `snippet` field, and a second private copy is the
 *  same "one contract, two places to fix it" hazard that let the second
 *  surface ship with the defect the first had already fixed.
 *
 *  Deliberately produces PLAIN TEXT, never a link: an excerpt is not the
 *  document, so it cannot honestly claim a resolved/unresolved verdict the
 *  way a real note render can — opening the row shows the real note, where
 *  the same target renders as a real, resolved link. */
export function stripWikilinkNotation(text: string): string {
  // A fresh RegExp, not the shared `WIKILINK_RE` instance: that module-scope
  // object is also driven by `remarkKbWikilinks` below, and a `g`-flag regex
  // carries mutable `lastIndex` state — reusing the same instance here would
  // race it.
  const re = new RegExp(WIKILINK_RE.source, WIKILINK_RE.flags)
  return text.replace(re, (whole: string, bang: string, inner: string) => {
    const parsed = parseWikilink(inner, bang === '!')
    return parsed ? parsed.text : whole
  })
}
