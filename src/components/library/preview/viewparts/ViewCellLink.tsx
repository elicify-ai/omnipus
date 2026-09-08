// ViewCellLink — a base view's cell renders its stored `[[wikilink]]` as a
// real link (KB-8b), reusing the SAME parser and the SAME three-state honesty
// model the note-reading surface uses (`../knowledgeMarkdown.tsx`):
// `parseWikilink` interprets the token, `KbLinkState`/`KbLinkResolution` is
// the only vocabulary a resolution answers in, and `LINK_CLASS` /
// `UNVERIFIED_LINK_CLASS` / `UnresolvedLink` are the SAME objects the note
// reader draws with — not a second color choice that can drift from them.
//
// A view only knows the rows it loaded, never the whole collection, so its
// own `resolveWikilink` (BasePreview.tsx) can honestly answer `resolved`
// (found among the loaded rows) or `unknown` (not found in what it has) — it
// can never honestly answer `unresolved`, because absent from a subset is not
// the same fact as absent from the collection. This component does not
// invent that distinction; it only renders whichever of the three states its
// caller's resolver answers.
//
// This is a SEPARATE component from `CollectionLink`/`KnowledgeMarkdownLink`
// rather than a context consumer of them, because a view row is ALSO its own
// click target (KB-8a): a link inside a cell must stop its click from also
// opening the row underneath it (KB-8c), a concern the single-document
// reading surface never has (nothing there wraps a whole note in one click
// handler).

import type { MouseEvent, ReactNode } from 'react'
import {
  parseWikilink,
  WIKILINK_RE,
  LINK_CLASS,
  UNVERIFIED_LINK_CLASS,
  UnresolvedLink,
} from '../knowledgeMarkdown'
import type { KbLinkResolution } from '../knowledgeMarkdown'

/**
 * What a view can offer a cell's wikilink tokens. Mirrors
 * `KnowledgeLinkContextValue`'s own field shapes exactly (same
 * `resolveWikilink`/`linkHref` contracts) rather than inventing parallel
 * ones — this is the SAME injection point KB-8's ratified plan names, just
 * threaded as plain props instead of the single-document context, because a
 * view has many cells across many rows, not one open note.
 */
export interface ViewCellLinkResolver {
  /** Absent means "not answered yet", never "broken" — every wikilink then
   *  renders `unknown`, exactly as knowledgeMarkdown.tsx documents. */
  resolveWikilink?: (target: string, heading?: string) => KbLinkResolution
  /** Collection-relative path -> a real address, when the caller can build one. */
  linkHref?: (path: string) => string | undefined
  /** Opens the resolved target's note in place. */
  onOpenPath?: (path: string) => void
}

function stopClick(event: MouseEvent): void {
  event.stopPropagation()
}

function isModifiedClick(event: MouseEvent): boolean {
  return event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0
}

function CellWikilink({
  target,
  heading,
  text,
  resolver,
}: {
  target: string
  heading?: string
  text: string
  resolver: ViewCellLinkResolver
}) {
  // A same-note heading token carries no target — a relation cell has no
  // "current note" to scroll within, so this is left as plain text rather
  // than guessed into a link that points nowhere real.
  if (target === '') return <>{text}</>

  const resolution: KbLinkResolution = resolver.resolveWikilink
    ? resolver.resolveWikilink(target, heading)
    : { state: 'unknown' }

  if (resolution.state === 'unresolved') {
    return (
      <span onClick={stopClick} data-testid="viewpart-cell-link-unresolved">
        <UnresolvedLink detail={`no note in this collection matches "${target}"`}>{text}</UnresolvedLink>
      </span>
    )
  }

  const path = resolution.path ?? target
  const verified = resolution.state === 'resolved'
  const href = resolver.linkHref?.(path)
  const className = verified ? LINK_CLASS : UNVERIFIED_LINK_CLASS
  const srUnverified = !verified ? (
    <span className="sr-only"> (link target not verified against the rows shown here)</span>
  ) : null

  const handleClick = (event: MouseEvent<HTMLElement>) => {
    // Stop FIRST, unconditionally (KB-8c): whatever this click does or does
    // not do, it must never also fire an ancestor row's own open handler.
    event.stopPropagation()
    if (isModifiedClick(event)) return
    if (!resolver.onOpenPath) return
    event.preventDefault()
    resolver.onOpenPath(path)
  }

  if (href !== undefined) {
    return (
      <a
        tabIndex={0}
        href={href}
        data-testid="viewpart-cell-link"
        data-kb-state={verified ? 'resolved' : 'unknown'}
        className={className}
        onClick={handleClick}
      >
        {text}
        {srUnverified}
      </a>
    )
  }

  return (
    <button
      type="button"
      tabIndex={0}
      data-testid="viewpart-cell-link"
      data-kb-state={verified ? 'resolved' : 'unknown'}
      className={`inline text-left align-baseline ${className}`}
      onClick={handleClick}
    >
      {text}
      {srUnverified}
    </button>
  )
}

/**
 * One cell's rendered value (`viewResultData.ts::cellValue`), with any
 * `[[wikilink]]` token replaced by a real link. A value with no token
 * renders exactly as before — plain text, nothing wrapped.
 */
export function CellText({ value, resolver }: { value: string; resolver?: ViewCellLinkResolver }) {
  const re = new RegExp(WIKILINK_RE.source, 'g')
  let match: RegExpExecArray | null
  let last = 0
  let key = 0
  const nodes: ReactNode[] = []

  while ((match = re.exec(value)) !== null) {
    const parsed = parseWikilink(match[2] as string, match[1] === '!')
    if (!parsed) continue
    if (match.index > last) nodes.push(<span key={key++}>{value.slice(last, match.index)}</span>)
    nodes.push(
      <CellWikilink
        key={key++}
        target={parsed.target}
        heading={parsed.heading}
        text={parsed.text}
        resolver={resolver ?? {}}
      />,
    )
    last = match.index + match[0].length
  }

  if (nodes.length === 0) return <>{value}</>
  if (last < value.length) nodes.push(<span key={key++}>{value.slice(last)}</span>)
  return <>{nodes}</>
}
