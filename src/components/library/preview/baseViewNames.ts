// baseViewNames.ts — enumerate the views a .base file declares, and derive
// the saved-view slug each one was imported under.
//
// WHY THE SPA READS THE .base FILE AT ALL. Import is one-shot: a `.base`
// file's views are translated into `<vault>/.omnipus-vault/views/<slug>.yaml`
// and the source is never re-read on the query path (ViewDef.source,
// FR-102). But no endpoint lists which saved views came from which source
// file, so the ONE honest way to open `Invoices.base` as its views is to
// read the names the file itself declares and address each one by the slug
// the importer derived — `kebab(<base stem>)--kebab(<view name>)`, mirroring
// pkg/vaultimport/util.go's SlugRegistry. The importer's rare collision
// suffix (`-2`) is NOT reproducible here; a slug that misses answers as the
// `unknown_view` refusal, which the renderer shows with its reason — an
// honest miss, never a silent blank.
//
// THE PARSER IS DELIBERATELY NOT A YAML LIBRARY. It extracts exactly two
// things — each view item's `name:` and the raw text of its `filters:` block
// (shown in the empty state, so "nothing matched" can say what was looked
// for) — with an indentation walk over the `views:` list. Adding a YAML
// dependency for two keys of a file this SPA never writes fails the
// no-new-deps bar; a view whose name this walk cannot see simply does not
// get a tab, and the file is still fully readable through the raw edit path.

export interface BaseViewRef {
  /** The view's display name, exactly as the .base file declares it. */
  name: string
  /** The saved-view slug the importer derived for it. */
  slug: string
  /** Raw text of the view's own `filters:` block (dedented), when one is
   *  declared — for the empty state's "what was looked for" line. */
  filterText?: string
}

/** Mirror of pkg/vaultimport/util.go's kebab(): lowercase, collapse every
 *  non-[a-z0-9] run into one hyphen, trim hyphens. */
export function kebab(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

/** Mirror of SlugRegistry.Slug without its collision counter. */
export function baseViewSlug(baseFileName: string, viewName: string): string {
  const stem = baseFileName.replace(/\.[^.]*$/, '')
  const slug = `${kebab(stem)}--${kebab(viewName)}`
  return slug === '--' ? 'view' : slug
}

function unquote(s: string): string {
  const t = s.trim()
  if (t.length >= 2 && ((t.startsWith('"') && t.endsWith('"')) || (t.startsWith("'") && t.endsWith("'")))) {
    return t.slice(1, -1)
  }
  return t
}

function indentOf(line: string): number {
  return line.length - line.trimStart().length
}

/**
 * True when the raw file text declares a top-level `views:` key AT ALL,
 * in any form the indentation-walk parser below can or cannot read (code
 * review finding #9). `parseBaseViews`'s own block-start regex only matches
 * the BLOCK form (`views:` with nothing else on the line); a flow-style
 * declaration such as `views: [{name: All}]` never matches it, so
 * `parseBaseViews` returns an empty list for a file that is very much NOT
 * declaring zero views. This lenient, prefix-only check exists so the
 * caller can tell those two zero-views cases apart and say the honest one:
 * "this file declares no views" (this returns false) vs. "this file's views
 * could not be parsed" (this returns true, `parseBaseViews` still empty).
 */
export function hasViewsBlock(content: string): boolean {
  return /^views:/m.test(content)
}

/**
 * The views a .base file declares, in declaration order. Unparseable or
 * unnamed items are skipped — they cannot be addressed, so they cannot be a
 * tab. An empty answer is an answer ("this base declares no views"), which
 * the caller must render as such, never as a blank — UNLESS `hasViewsBlock`
 * above says a `views:` key exists at all, in which case the empty answer
 * means the parser failed to read it, not that the file has none.
 */
export function parseBaseViews(content: string, baseFileName: string): BaseViewRef[] {
  const lines = content.split('\n')
  const out: BaseViewRef[] = []

  // 1. Find the top-level `views:` key.
  let i = lines.findIndex((l) => /^views:\s*(#.*)?$/.test(l))
  if (i < 0) return out
  i++

  // 2. Walk its block: everything more indented than column 0, until the next
  //    top-level key. Blank lines and comments never terminate the block.
  let itemIndent = -1
  let current: { name?: string; filterLines?: string[]; filterIndent?: number; inFilters: boolean } | null = null

  const flush = () => {
    if (current?.name !== undefined && current.name !== '') {
      const ref: BaseViewRef = {
        name: current.name,
        slug: baseViewSlug(baseFileName, current.name),
      }
      const ft = (current.filterLines ?? []).join('\n').trimEnd()
      if (ft !== '') ref.filterText = ft
      out.push(ref)
    }
    current = null
  }

  for (; i < lines.length; i++) {
    const line = lines[i]
    const trimmed = line.trim()
    if (trimmed === '' || trimmed.startsWith('#')) continue
    const indent = indentOf(line)
    if (indent === 0) break // next top-level key ends the views block

    const isItemStart = /^-(\s|$)/.test(trimmed)
    if (isItemStart && (itemIndent === -1 || indent <= itemIndent)) {
      flush()
      itemIndent = indent
      current = { inFilters: false }
      // `- name: Outstanding` — the first field may share the dash's line.
      const rest = trimmed.replace(/^-\s*/, '')
      const nm = /^name:\s*(.*)$/.exec(rest)
      if (nm && current) current.name = unquote(nm[1])
      continue
    }
    if (current === null) continue

    // Field lines of the current item.
    const nm = /^name:\s*(.*)$/.exec(trimmed)
    if (nm && indentOf(line) <= (current.filterIndent ?? Infinity)) {
      current.name = unquote(nm[1])
      current.inFilters = false
      continue
    }
    if (/^filters:\s*(.*)$/.test(trimmed)) {
      current.inFilters = true
      current.filterIndent = indent
      const inline = /^filters:\s*(.+)$/.exec(trimmed)
      current.filterLines = inline ? [inline[1]] : []
      continue
    }
    if (current.inFilters) {
      if (indent > (current.filterIndent ?? 0)) {
        current.filterLines = current.filterLines ?? []
        current.filterLines.push(line.slice((current.filterIndent ?? 0) + 2))
      } else {
        current.inFilters = false
      }
    }
  }
  flush()
  return out
}
