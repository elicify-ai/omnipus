// KnowledgeReader — the reading layout for a note.
//
// ADR-067 STAGE 2: US-7 (read a note the way it was written), FR-011,
// FR-013a..d (via `preview/knowledgeMarkdown.tsx`), FR-062 (an outline for ANY
// markdown file), FR-063 (inbound links for the open note), FR-064 (the rail
// collapses to toggles when the pane is docked), US-7 AS-5, AS-6, AS-9.
//
// ── What this component is ───────────────────────────────────────────────────
// A reading COLUMN plus a rail region, and nothing else. It owns the markdown
// composition, the measure, the wide/docked decision, and scrolling the column
// to a heading. It owns NO rail content: the panels are supplied by the caller
// through `renderRails`, which receives the one thing a rail cannot work out for
// itself — whether it is being rendered narrow enough that it must collapse to a
// disclosure — plus the reader's own scroll-to-heading function.
//
// ── Why the rails are a slot and not props ───────────────────────────────────
// They used to be props (`outline`, `outlineStatus`, `backlinks`, …) rendered by
// two private components in this file, while `KnowledgeOutline.tsx` and
// `KnowledgeBacklinks.tsx` sat beside it implementing THE SAME TWO REQUIREMENTS
// against the same two endpoints. Two implementations of one requirement is the
// exact failure FR-013a codifies for the markdown pipeline, and the copies had
// already diverged in the way that matters: the standalone backlinks panel
// reported ambiguous resolutions (FR-041), refused to navigate to a node the
// graph marked non-existent (FR-065) and listed what the walk skipped (FR-112);
// the private one here did none of those and rendered every inbound link as an
// ordinary, certain, navigable button. The private copies are gone. There is one
// outline panel and one backlinks panel in this product.
//
// ── The reading measure ──────────────────────────────────────────────────────
// The column is capped at ~72 characters (`--kb-reading-measure`). The most
// common readability failure in a full-width pane is not the font or the
// contrast, it is a 200-character line: the eye loses the return sweep and
// re-reads the line it just finished. The rails sit BESIDE that column and
// share the leftover width, so widening the window makes the margins grow, not
// the lines.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode, RefObject } from 'react'
import { cn } from '@/lib/utils'
import {
  KnowledgeBaseMarkdown,
  type KbLinkResolution,
} from '../preview/knowledgeMarkdown'
import type { KnowledgeOutlineHeading } from '@/lib/api/generated/openapi-types'

/** `auto` measures the pane; `wide`/`docked` force one, which is what callers
 *  (and tests) use when the answer is already known. */
export type ReaderLayout = 'auto' | 'wide' | 'docked'

/** Below this many CSS pixels a side rail would steal width from the reading
 *  column instead of sitting beside it, so the rails become toggles (FR-064). */
export const DOCKED_MAX_WIDTH = 720

/** What `renderRails` is handed. */
export interface KnowledgeReaderRailContext {
  /**
   * True when the pane is too narrow for a side rail, so every panel must
   * render as a collapsed disclosure rather than splitting the column
   * (FR-064, US-7 AS-9). Panels pass this straight to their own `collapsible`.
   */
  collapsible: boolean
  /**
   * Move the reading column to one of the open note's headings.
   *
   * `index` is the heading's position in the outline and is used as a guard,
   * not as the address: the nth rendered heading is accepted only when its text
   * matches, otherwise the first heading with that text wins, otherwise nothing
   * moves. Scrolling to a heading that is not the one the reader clicked is
   * worse than not scrolling.
   */
  scrollToHeading: (heading: KnowledgeOutlineHeading, index?: number) => void
}

export interface KnowledgeReaderProps {
  /** Markdown source of the open note. */
  content: string
  /** Collection-relative path of the open note. Relative links resolve against
   *  its directory. */
  path: string
  layout?: ReaderLayout
  /** Opens another note. */
  onNavigate?: (path: string, heading?: string) => void
  /** Answers whether a wikilink target exists. Omit while the link graph is
   *  still loading: every wikilink is then `unknown` and none is marked
   *  unresolved (see `knowledgeMarkdown.tsx`). */
  resolveWikilink?: (target: string, heading?: string) => KbLinkResolution
  /** Resolves `![[image.png]]` to a loadable URL. */
  resolveEmbedUrl?: (target: string) => string | undefined
  /** Real address for a resolved in-collection link, so it can be copied,
   *  middle-clicked and opened in a new tab (FR-012). See knowledgeMarkdown. */
  linkHref?: (collectionPath: string, heading?: string) => string | undefined
  /** The rail panels. Omit for a bare reading column. */
  renderRails?: (ctx: KnowledgeReaderRailContext) => ReactNode
}

// ─────────────────────────────────────────────────────────────────────────────
// Layout measurement
// ─────────────────────────────────────────────────────────────────────────────

/** Effective layout for a container width. `auto` with no measurement yet (and
 *  in any environment without ResizeObserver) resolves to `wide`: the rails are
 *  rendered rather than hidden, so nothing disappears because a measurement was
 *  unavailable. */
function useEffectiveLayout(layout: ReaderLayout, ref: RefObject<HTMLDivElement | null>) {
  const [measured, setMeasured] = useState<'wide' | 'docked' | null>(null)

  useEffect(() => {
    if (layout !== 'auto') return
    const element = ref.current
    if (!element || typeof ResizeObserver === 'undefined') return
    const apply = (width: number) => setMeasured(width > 0 && width <= DOCKED_MAX_WIDTH ? 'docked' : 'wide')
    apply(element.getBoundingClientRect().width)
    const observer = new ResizeObserver((entries) => {
      for (const entry of entries) apply(entry.contentRect.width)
    })
    observer.observe(element)
    return () => observer.disconnect()
  }, [layout, ref])

  if (layout !== 'auto') return layout
  return measured ?? 'wide'
}

// ─────────────────────────────────────────────────────────────────────────────
// Heading navigation
// ─────────────────────────────────────────────────────────────────────────────

/**
 * Finds the rendered element for an outline entry.
 *
 * Chat's heading renderers take `{ children }` only, so no `id` can be attached
 * to a heading without replacing a slot the KB composition is not allowed to
 * replace (FR-013b). The outline therefore addresses headings POSITIONALLY,
 * with the text as a guard: the nth heading if its text matches, otherwise the
 * first heading whose text matches, otherwise nothing at all.
 *
 * "Nothing at all" is deliberate. Scrolling to a heading that is not the one
 * the reader clicked is worse than not scrolling.
 */
export function findHeadingElement(
  container: HTMLElement,
  index: number,
  text: string,
): HTMLElement | null {
  const headings = Array.from(container.querySelectorAll<HTMLElement>('h1, h2, h3, h4, h5, h6'))
  const wanted = text.trim()
  const positional = headings[index]
  if (positional && positional.textContent?.trim() === wanted) return positional
  return headings.find((element) => element.textContent?.trim() === wanted) ?? null
}

// ─────────────────────────────────────────────────────────────────────────────
// The reader
// ─────────────────────────────────────────────────────────────────────────────

export function KnowledgeReader({
  content,
  path,
  layout = 'auto',
  onNavigate,
  resolveWikilink,
  resolveEmbedUrl,
  linkHref,
  renderRails,
}: KnowledgeReaderProps) {
  const rootRef = useRef<HTMLDivElement | null>(null)
  const articleRef = useRef<HTMLElement | null>(null)
  const effectiveLayout = useEffectiveLayout(layout, rootRef)

  const scrollToHeading = useCallback((heading: KnowledgeOutlineHeading, index = -1) => {
    const container = articleRef.current
    if (!container) return
    const element = findHeadingElement(container, index, heading.text)
    element?.scrollIntoView?.({ block: 'start' })
  }, [])

  const handleHeadingLink = useCallback((headingText: string) => {
    const container = articleRef.current
    if (!container) return
    // A `#heading` href carries a slug, not the heading's text, so the
    // positional guard cannot help; match on either form.
    const element =
      findHeadingElement(container, -1, headingText) ??
      findHeadingElement(container, -1, headingText.replace(/-/g, ' '))
    element?.scrollIntoView?.({ block: 'start' })
  }, [])

  const railContext = useMemo<KnowledgeReaderRailContext>(
    () => ({ collapsible: effectiveLayout === 'docked', scrollToHeading }),
    [effectiveLayout, scrollToHeading],
  )

  const rails = renderRails?.(railContext)

  const article = (
    <article
      ref={articleRef}
      data-testid="knowledge-reader-article"
      // The measure lives in a custom property so the column and anything a
      // caller nests inside it agree on one number.
      style={{ ['--kb-reading-measure' as string]: '72ch', maxWidth: 'var(--kb-reading-measure)' }}
      className="w-full mx-auto px-1"
    >
      <KnowledgeBaseMarkdown
        content={content}
        notePath={path}
        onNavigate={onNavigate}
        onHeadingLink={handleHeadingLink}
        resolveWikilink={resolveWikilink}
        resolveEmbedUrl={resolveEmbedUrl}
        linkHref={linkHref}
      />
    </article>
  )

  if (effectiveLayout === 'docked') {
    return (
      <div ref={rootRef} data-testid="knowledge-reader" data-layout="docked" className="w-full">
        {rails ? (
          <div
            data-testid="knowledge-reader-rails"
            data-collapsible="true"
            className="mb-3 flex flex-col divide-y divide-[var(--color-border)] border-b border-[var(--color-border)]"
          >
            {rails}
          </div>
        ) : null}
        {article}
      </div>
    )
  }

  return (
    <div
      ref={rootRef}
      data-testid="knowledge-reader"
      data-layout="wide"
      className={cn('w-full flex items-start', rails ? 'gap-8' : '')}
    >
      <div className="min-w-0 flex-1">{article}</div>
      {rails ? (
        <aside
          data-testid="knowledge-reader-rails"
          data-collapsible="false"
          className="w-60 shrink-0 sticky top-2 max-h-[calc(100vh-6rem)] overflow-y-auto space-y-5 border-l border-[var(--color-border)] pl-3"
        >
          {rails}
        </aside>
      ) : null}
    </div>
  )
}
