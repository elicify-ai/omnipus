// KnowledgeOutline — the reading rail's heading outline for the open markdown
// file (ADR-067 US-7 AS-5, FR-062; spec §6, §9 E-17).
//
// FR-062 is deliberately wider than the rest of stage 2: an outline is parsed
// from the ONE file in hand and needs no index, so it is offered for ANY
// markdown file, knowledge base or not. Search and backlinks stay
// knowledge-base-only because those genuinely require an index. That is why
// this component takes only a workspace id and a workspace-relative path — no
// collection id — and why it renders happily when `is_knowledge_base` is false.
//
// ── The server sends a FLAT list, and that is not an oversight ───────────────
// `KnowledgeOutline.headings` is documented as "a FLAT list ordered as the
// headings appear in the document, with nesting carried by level, not a
// recursive tree. A flat list has one representation for any document,
// including one that skips from H1 to H3, where a tree would force the server
// to invent an intermediate node the author never wrote."
//
// This component must not undo that on the way to the screen. It therefore
// derives indentation from the level LADDER actually present
// (`outlineIndentDepths`) rather than from the level number: a note whose
// headings start at H3 indents from the left edge like any other, and an H1→H3
// jump costs exactly one indent step because that is one real level of nesting
// — no phantom H2 is drawn, and no reader is told one exists.
//
// ── Deep nesting must stay readable, and must not lose information ───────────
// Six levels of `level × 12px` marches a heading half-way across a narrow
// docked rail and then wraps every word. The indent is therefore CLAMPED at
// KNOWLEDGE_OUTLINE_MAX_INDENT_DEPTH steps. Clamping on its own would silently
// flatten H5 and H6 into H4, so every row also carries its true level as a
// visible "H5" chip: the indent conveys structure up to a readable bound, and
// the chip is lossless beyond it. Neither is a substitute for the other.
//
// ── Fetching ────────────────────────────────────────────────────────────────
// The loader is INJECTED (`loadOutline`), following LibraryPreviewPane's
// `MintLibraryPreviewToken` precedent, for the same reason it gives: this file
// does not own `src/lib/api.ts`, and `GET /library/{ws}/knowledge/outline` has
// no wrapper there yet. Making the prop REQUIRED rather than defaulting to a
// null constant is the difference that matters here — a missing wrapper is a
// compile error at the mount site instead of a panel that renders an
// apologetic empty state forever. Whoever mounts this passes the generated
// client; tests pass a stub, which is also the fetch boundary they mock at.

import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { WarningCircle } from '@phosphor-icons/react'
import { QueryErrorState } from '@/components/shared/QueryErrorState'
import { KnowledgeRailPanelHeader, type KnowledgeRailQualifier } from './KnowledgeRailPanel'
import type {
  KnowledgeOutline as KnowledgeOutlineResponse,
  KnowledgeOutlineHeading,
} from '@/lib/api/generated/openapi-types'

/** Arguments for `GET /api/v1/library/{workspace_id}/knowledge/outline`. */
export interface KnowledgeOutlineRequest {
  workspaceId: string
  /** Workspace-relative path of the markdown file, per the operation's `path` query parameter. */
  path: string
}

/** The injected client for the outline endpoint. */
export type KnowledgeOutlineLoader = (
  request: KnowledgeOutlineRequest,
) => Promise<KnowledgeOutlineResponse>

export function knowledgeOutlineQueryKey(workspaceId: string, path: string) {
  return ['library', 'knowledge', 'outline', workspaceId, path] as const
}

/**
 * How many indent steps a row may take before the indent stops growing. Beyond
 * this the row's level chip carries the depth instead — see the header note.
 */
export const KNOWLEDGE_OUTLINE_MAX_INDENT_DEPTH = 4

/** Pixels per indent step. */
const INDENT_STEP_PX = 12

/** Left padding of a depth-0 row. */
const INDENT_BASE_PX = 8

/**
 * Nesting depth for each heading, derived from the ladder of levels actually
 * present rather than from the level number itself.
 *
 * A heading's depth is the number of headings still open above it: pop every
 * enclosing level that is not strictly shallower, then take what remains. So
 * `[H3, H4]` is `[0, 1]` (a note that starts at H3 is not indented three steps
 * for nothing) and `[H1, H3]` is `[0, 1]` (one real level of nesting — the H2
 * the author never wrote is not invented here either).
 *
 * Exported because it is the whole of the nesting rule and is worth asserting
 * directly, without going through a render.
 */
export function outlineIndentDepths(headings: readonly KnowledgeOutlineHeading[]): number[] {
  const open: number[] = []
  return headings.map((heading) => {
    while (open.length > 0 && open[open.length - 1] >= heading.level) open.pop()
    const depth = open.length
    open.push(heading.level)
    return depth
  })
}

export interface KnowledgeOutlineProps {
  workspaceId: string
  /** Workspace-relative path of the open markdown file. */
  path: string
  /** See the header note — required, not defaulted, so a missing wiring is a compile error. */
  loadOutline: KnowledgeOutlineLoader
  /**
   * Move the reader to this heading. Owned by the caller because this component
   * does not own the rendered document: the KB markdown composition
   * (`LibraryMarkdownPreview.tsx`) emits no heading ids today, so there is no
   * `#slug` anchor to link to and no honest way for this panel to scroll one
   * into view on its own.
   */
  onNavigate: (heading: KnowledgeOutlineHeading) => void
  /** Slug of the heading the reader is currently at, if the caller tracks it. */
  activeSlug?: string
  /**
   * FR-064 / US-7 AS-9 — in a narrow docked rail the panel collapses to a
   * toggle instead of splitting the pane. Collapsed panels start closed and
   * render no body at all.
   */
  collapsible?: boolean
}

export function KnowledgeOutline({
  workspaceId,
  path,
  loadOutline,
  onNavigate,
  activeSlug,
  collapsible = false,
}: KnowledgeOutlineProps) {
  const [expanded, setExpanded] = useState(!collapsible)

  const query = useQuery({
    queryKey: knowledgeOutlineQueryKey(workspaceId, path),
    queryFn: () => loadOutline({ workspaceId, path }),
  })

  const headings = useMemo(() => query.data?.headings ?? [], [query.data])
  const depths = useMemo(() => outlineIndentDepths(headings), [headings])

  const qualifiers = useMemo<KnowledgeRailQualifier[]>(
    () =>
      query.data?.frontmatter_malformed
        ? [
            {
              label: 'frontmatter unreadable',
              detail:
                "This note's frontmatter is not valid YAML, so none of it was read. " +
                'The headings listed here are still complete.',
            },
          ]
        : [],
    [query.data],
  )

  return (
    <section data-testid="knowledge-outline" aria-label="Outline" className="flex flex-col min-h-0">
      <KnowledgeRailPanelHeader
        title="Outline"
        count={query.data ? headings.length : undefined}
        collapsible={collapsible}
        expanded={expanded}
        onToggle={() => setExpanded((v) => !v)}
        testId="knowledge-outline-toggle"
        // E-17, and the reason it is HERE and not only in the body: with
        // `collapsible` the body starts closed, so a warning that lives only
        // inside it renders nothing at all while the header still shows a
        // heading count. The count and its caveat travel together.
        qualifiers={qualifiers}
      />

      {expanded && (
        <div className="flex flex-col min-h-0 overflow-y-auto">
          {/* E-17. The file is still outlined and still indexed for body text;
              the malformed frontmatter is REPORTED, in the panel body where the
              reader cannot miss it — never a title attribute and never a
              console warning. It sits above the list and also shows for a note
              with no headings at all, because it is a fact about the file
              rather than a decoration on the list. */}
          {query.data?.frontmatter_malformed && (
            <p
              data-testid="knowledge-outline-frontmatter-malformed"
              className="flex items-start gap-2 px-3 py-2 text-xs text-[var(--color-warning)]"
            >
              <WarningCircle size={14} className="mt-px shrink-0" aria-hidden="true" />
              <span>
                This note&apos;s frontmatter is not valid YAML, so none of it was read. The headings
                below are still complete.
              </span>
            </p>
          )}

          {query.isPending && (
            // Indeterminate on purpose: there is no total to divide by, so
            // there is no bar and no percentage. See the same rule in
            // KnowledgeBacklinks.
            <p data-testid="knowledge-outline-loading" className="px-3 py-2 text-xs text-[var(--color-muted)]">
              Reading this note&apos;s headings…
            </p>
          )}

          {query.isError && (
            <QueryErrorState
              layout="fill"
              testId="knowledge-outline-error"
              message="Could not read this note's headings."
              onRetry={() => void query.refetch()}
            />
          )}

          {query.isSuccess && headings.length === 0 && (
            <p data-testid="knowledge-outline-empty" className="px-3 py-2 text-xs text-[var(--color-muted)]">
              This note has no headings.
            </p>
          )}

          {query.isSuccess && headings.length > 0 && (
            <ul className="flex flex-col py-1">
              {headings.map((heading, i) => {
                const depth = depths[i]
                const clamped = Math.min(depth, KNOWLEDGE_OUTLINE_MAX_INDENT_DEPTH)
                const isActive = activeSlug !== undefined && activeSlug === heading.slug
                const label = heading.text.trim()
                return (
                  <li key={`${heading.slug}-${i}`}>
                    <button
                      type="button"
                      data-testid="knowledge-outline-heading"
                      data-slug={heading.slug}
                      data-level={heading.level}
                      data-indent-depth={clamped}
                      aria-current={isActive ? 'true' : undefined}
                      onClick={() => onNavigate(heading)}
                      style={{ paddingLeft: `${INDENT_BASE_PX + clamped * INDENT_STEP_PX}px` }}
                      className={
                        'flex w-full items-baseline gap-2 py-1 pr-3 text-left text-xs transition-colors ' +
                        'hover:bg-[var(--color-surface-2)] ' +
                        (isActive
                          ? 'text-[var(--color-accent)]'
                          : 'text-[var(--color-secondary)] hover:text-[var(--color-secondary)]')
                      }
                    >
                      {/* Lossless where the indent is clamped — the chip always
                          states the real level. */}
                      <span
                        aria-hidden="true"
                        className="shrink-0 font-mono text-[10px] leading-4 text-[var(--color-muted)]"
                      >
                        H{heading.level}
                      </span>
                      <span className={label ? 'min-w-0 break-words' : 'min-w-0 italic text-[var(--color-muted)]'}>
                        {label || 'Untitled heading'}
                      </span>
                    </button>
                  </li>
                )
              })}
            </ul>
          )}
        </div>
      )}
    </section>
  )
}
