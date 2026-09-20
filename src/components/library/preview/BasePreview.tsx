// BasePreview — a .base file opens as its views, not as a download and not
// as raw YAML (view-kinds-design-2026-09-03 §7; visual spec: the wireframe's
// "The accounting view" frame — view tabs, a filter strip, the part stack).
//
// WHERE THE DATA COMES FROM, and why there are exactly two fetches:
//
//   1. The VIEWS — GET /knowledge/base-views names the saved views whose
//      `source` is this .base file, each with the slug it is actually
//      addressed by, its label, and whether it can be served at all. The same
//      answer carries the enclosing collection, so there is no ancestor walk
//      here either.
//   2. The RESULT — GET /knowledge/view per selected tab, addressed by the
//      slug from (1) VERBATIM, validated at the edge by the generated zod
//      schema like every other SPA fetch. Only the selected view is fetched; a
//      tab's count badge appears once its result has been seen.
//
// THIS FILE USED TO READ THE .base ITSELF, and that is the defect it now
// closes (code-review findings #3 and #7). Import is one-shot (FR-102), so the
// .base's own `views:` block was the only list on hand — but re-deriving each
// view's slug from it meant mirroring the importer's slugger, and the mirror
// could not reproduce two things the importer does:
//
//   · Its SlugRegistry appends a collision counter over everything already
//     handed out. Two view names that kebab alike ("A/B" and "A B") therefore
//     collapsed onto ONE slug, and the second tab fetched the FIRST view and
//     rendered its rows under the second view's name — with two React children
//     sharing a key.
//   · The hand-rolled YAML walk took any `name:` line inside a view item as
//     the view's name, so a nested mapping key clobbered it, and the clobbered
//     name derived a slug no view file has: a valid view answered `unknown_view`.
//
// Both were re-derivations of facts the server already holds. The parser is
// deleted; nothing here reconstructs a slug, a label or a count.
//
// Every non-happy state is a stated answer: not-a-collection says so in plain
// words, a refusal renders the server's reason, an empty view leads with the
// outcome (ViewPartsRenderer), and view files this base owns that failed to
// load are reported as a count rather than as quietly missing tabs.

import { useEffect, useMemo, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Code, DownloadSimple, SpinnerGap, Warning } from '@phosphor-icons/react'

import { Button } from '@/components/ui/button'
import { QueryErrorState } from '@/components/shared/QueryErrorState'
import {
  fetchKnowledgeBaseViews,
  fetchKnowledgeViewResult,
  fetchLibraryContent,
  libraryDownloadUrl,
  libraryQueryKeys,
} from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'
import { ApiError } from '@/lib/api-error'
import {
  shouldRetryQuery,
  rateLimitAwareQueryRetryDelay,
  rateLimitedRetryDelayMs,
} from '@/lib/queryClient'
import type {
  KnowledgeBaseView,
  KnowledgeBaseViews,
  ViewResult,
} from '@/lib/api/generated/openapi-types'

import { LibraryCodePreview } from './LibraryCodePreview'
import { ViewPartsRenderer } from './viewparts/ViewPartsRenderer'
import {
  collectionLinkRowPaths as collectionLinkRowPathsFn,
  makeCollectionLinkResolver,
  useCollectionLinkGraph,
} from './useCollectionLinkGraph'
import {
  collectionPathToWorkspacePath,
  libraryNoteHref,
  type KnowledgeGraphLoader,
} from '../knowledge/KnowledgeBacklinks'
import type { KbLinkResolution } from './knowledgeMarkdown'
import { INLINE_PREVIEW_BOX_CLASS } from './libraryPreviewVariant'
import type { LibraryPreviewVariant } from './libraryPreviewVariant'
import { viewEvaluationPool, VIEW_EVALUATION_POOL_CEILING } from './viewEvaluationPool'

/** Test seams; production passes nothing and gets the shared clients. */
export interface BasePreviewLoaders {
  loadContent?: (workspaceId: string, path: string) => Promise<{ content?: string; is_text: boolean; too_large: boolean }>
  loadBaseViews?: (workspaceId: string, path: string) => Promise<KnowledgeBaseViews>
  loadViewResult?: (workspaceId: string, collectionId: string, view: string) => Promise<ViewResult>
  /** WL-1's remaining half (docs/internal/defect-list-wikilink-rendering-
   *  2026-09-08.md) — see `COLLECTION_LINK_ROW_QUERY_CAP`'s doc comment
   *  below for why this exists and what it fetches. Production default is
   *  the real client (`defaultLoadGraph`); tests inject a stub. */
  loadGraph?: KnowledgeGraphLoader
}

/**
 * WL-1's remaining half. A standalone `.base` pane (no host note to inherit a
 * resolver from, unlike an embedded view — see BasePreviewEmbedOptions'
 * `resolveWikilink` doc above) could previously only ever answer `resolved`
 * (the target names one of THIS view's own loaded rows) or `unknown` (it does
 * not) — because that was the only evidence in hand. Most relation-cell
 * targets are not the title of a row the SAME view happens to display, so
 * most links rendered `unknown` (white) even though the identical wikilink
 * text resolves perfectly well elsewhere in the collection and renders GOLD
 * there.
 *
 * The fix reuses the SAME mechanism the note reader uses — the link graph,
 * `kind: 'links'` (KnowledgeNoteView.tsx's own `resolveWikilink`) — rather
 * than inventing a second resolution path. It is deliberately NOT pointed at
 * the `.base` file's own path: a `.base` is YAML, and `BuildLinkGraph`
 * (pkg/knowledge/graph.go) only opens `.md`/`.markdown` sources for outbound
 * links, so a `.base` file's own `links` answer is always empty — that would
 * be a harmless no-op, not a fix.
 *
 * The relation cell's literal `[[wikilink]]` text is not written in the
 * `.base` file at all — it is written in the ROW's own markdown file (a
 * relation cell's rendered value IS that row's own frontmatter/body content;
 * pkg/knowledge/links.go: "a note can name another note in a frontmatter
 * field, and a rename has to rewrite it"). So the collection-wide answer for
 * this view's own cells lives in the union of its rows' own outbound-links
 * graphs — fetched since UAT D-135 as ONE multi-path `paths[]` request
 * through useCollectionLinkGraph (the shared hook this file and the search
 * bar's saved-view dialog both use), instead of one request per row.
 *
 * Bounded so a very large view cannot ask for an unbounded walk. Rows past
 * the cap keep the same honest fallback (row-title match, else `unknown`)
 * they always had — never silently promoted to `resolved`, which is the
 * exact dishonesty the three-state model exists to prevent.
 */
export {
  COLLECTION_LINK_ROW_QUERY_CAP,
  rowCarriesWikilink,
  LINK_GRAPH_MAX_IN_FLIGHT,
  withLinkGraphSlot,
  linkGraphInFlightCount,
} from './useCollectionLinkGraph'
import { defaultKnowledgeGraphLoader as defaultLoadGraph } from './useCollectionLinkGraph'

/**
 * ADR-083 EMB-040/EMB-043/EMB-046/EMB-047/EMB-048/EMB-049 — the extra
 * behaviour a `.base` embed inside a knowledge-base note needs that a plain
 * full-screen open of the file never did: a specific view chosen up front
 * (never `views[0]` by luck), a caption when the note author didn't name one,
 * a switcher hidden until the reader actually wants it, and links resolved
 * against the REAL note-reading link graph rather than this one view's own
 * loaded rows (WL-1 — a view's own resolver can only ever answer `resolved`
 * or `unknown`, never `unresolved`, so a target that plainly does not exist
 * in the collection still renders as merely "unverified").
 *
 * Omitting this prop entirely (the pane's own usage) is unaffected byte for
 * byte (EMB-028): no fetch, no state and no non-embed render path reads it.
 */
export interface BasePreviewEmbedOptions {
  /** The server's own slug (never reconstructed) for the view this embed
   *  resolved to. Selected on mount and whenever it changes; a reader may
   *  still switch tabs afterward when showViewSwitcher is true — that is a
   *  local UI choice, never written back (EMB-047's "an embed is not the
   *  vault"; there is nothing here TO write back to). */
  viewName: string
  /** Shown when the embed did not choose the view itself (EMB-043) — the
   *  note wrote no fragment, so the first view was picked automatically and
   *  the reader is told so, in place of a silent choice. */
  caption?: string
  /** EMB-043's other half: false renders no tab list at all (nothing to
   *  switch to when the note author expressed no preference). True renders
   *  the real, existing view switcher — the tab row already built for the
   *  full pane — visually hidden until pointer hover or keyboard focus
   *  enters the embed (EMB-046's "controls appear on hover and on focus, not
   *  at rest"); it is REAL functionality being gated, not decoration. */
  showViewSwitcher: boolean
  /** EMB-048: resolve a cell's `[[wikilink]]` against the note reader's own
   *  link graph instead of this view's own loaded rows. Omit to keep the
   *  view's own row-scoped resolver (never `unresolved`, only `resolved` or
   *  `unknown`) — the pre-embed default. */
  resolveWikilink?: (target: string, heading?: string) => KbLinkResolution
  /** Paired with resolveWikilink — the reader's own address builder, so a
   *  resolved cell link opens through the SAME address the rest of the note
   *  uses rather than one this view derives from its own collection root. */
  linkHref?: (collectionPath: string, heading?: string) => string | undefined
}

export interface BasePreviewProps extends BasePreviewLoaders {
  workspaceId: string
  entry: LibraryEntry
  /**
   * `pane` (default) fills the Library preview pane's own bounds; `inline`
   * sizes to a bounded, self-determined box so a `.base` embed (the "saved
   * view renderer" of ADR-083's US-3) sits in a note's text flow instead of
   * claiming the pane's height. This is the ONLY thing that differs between
   * the two — see libraryPreviewVariant.ts (EMB-027/028): every fetch, every
   * state (the tabs, the escape hatch, the unloadable notice) renders
   * identically either way.
   */
  variant?: LibraryPreviewVariant
  /** Present only for an inline embed that already resolved to a specific
   *  view — see BasePreviewEmbedOptions. Absent everywhere else. */
  embed?: BasePreviewEmbedOptions
  /**
   * Open another file in place, WORKSPACE-relative (KB-8a — mirrors
   * LibraryPreviewPane's own `onOpenNote` contract exactly, the same address
   * model `LibrarySearchBar`'s vault hits already use). A row's own note and a
   * relation cell's resolved link both funnel through this one callback.
   * Absent renders every part exactly as before — no row or cell link is a
   * click target.
   */
  onOpenNote?: (workspacePath: string) => void
}

// basenameNoExt/basenameOf — the row-identity and edge-target basename
// helpers this file's resolver used to define privately — moved to
// useCollectionLinkGraph.ts (makeCollectionLinkResolver) when the resolver
// itself became shared with the search bar's saved-view dialog.

/**
 * Triggers a real browser download of the file, the same click-a-detached-
 * anchor pattern LibraryExplorer's own `handleDownload` uses — self-contained
 * here so the "no views" escape hatch (below) works without the caller
 * having to thread an `onDownload` prop through LibraryPreviewPane.
 */
function downloadLibraryEntry(workspaceId: string, entry: LibraryEntry): void {
  const a = document.createElement('a')
  a.href = libraryDownloadUrl(workspaceId, entry.path)
  a.download = entry.name
  a.rel = 'noopener'
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
}

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex flex-1 items-center justify-center gap-[var(--space-2)] p-[var(--space-4)] text-center text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
      {children}
    </div>
  )
}

/** "N views could not be loaded" — the server's rejection count, said out
 *  loud. Silently showing fewer tabs than the base has views is the exact
 *  silent loss this surface exists to end. */
function UnloadableNotice({
  count,
  entries,
}: {
  count: number
  entries: NonNullable<KnowledgeBaseViews['unloadable']> | undefined
}) {
  return (
    <div
      className="flex shrink-0 flex-col gap-[var(--space-1)] border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]"
      data-testid="base-preview-unloadable"
    >
      <span className="flex items-center gap-[var(--space-1)]">
        <Warning size={13} />
        {count === 1
          ? '1 view from this file could not be loaded and is not shown.'
          : `${count} views from this file could not be loaded and are not shown.`}
      </span>
      {/* UAT D-70: name each missing view and state the loader's reason
          verbatim — the same words the agent door and the search bar use. */}
      {entries !== undefined && entries.length > 0 && (
        <ul className="flex flex-col gap-[var(--space-0-5)] pl-[var(--space-3)]" data-testid="base-preview-unloadable-list">
          {entries.map((e, i) => (
            <li key={`${e.code}-${i}`} data-testid="base-preview-unloadable-entry">
              <span className="font-medium text-[var(--color-secondary)]">{e.name ?? e.paths.join(', ')}</span>
              <span className="text-[var(--color-muted)]"> — {e.reason}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** UAT D-78: two views carrying one label were indistinguishable in the
 *  tablist. A label shared by more than one view is suffixed with the
 *  view's own name so each tab reads as itself. Exported as a test seam. */
export function tabLabelsFor(views: KnowledgeBaseView[]): Map<string, string> {
  const byLabel = new Map<string, number>()
  for (const v of views) byLabel.set(v.label, (byLabel.get(v.label) ?? 0) + 1)
  const out = new Map<string, string>()
  for (const v of views) out.set(v.name, (byLabel.get(v.label) ?? 0) > 1 ? `${v.label} (${v.name})` : v.label)
  return out
}

/** UAT D-78: the tab a reader lands on is the first view that can actually
 *  be served — never an unservable twin that happens to sort first. Falls
 *  back to the first view when none is servable. Exported as a test seam. */
export function defaultViewFor(views: KnowledgeBaseView[]): KnowledgeBaseView | undefined {
  return views.find((v) => v.unservable !== true) ?? views[0]
}

export function BasePreview({
  workspaceId,
  entry,
  loadContent = fetchLibraryContent,
  loadBaseViews = fetchKnowledgeBaseViews,
  loadViewResult = fetchKnowledgeViewResult,
  loadGraph = defaultLoadGraph,
  variant = 'pane',
  embed,
  onOpenNote,
}: BasePreviewProps) {
  // The ONE layout switch (EMB-028) — every state below still renders
  // through whichever of these two class strings is active; nothing about
  // WHICH state renders, or what it fetches, reads `variant` at all. `embed`
  // additionally opts the container into `group`, the hook the hidden-until-
  // hover tab list below hangs off (EMB-046) — harmless when there is no
  // such tab list to reveal (embed.showViewSwitcher === false).
  const containerClass =
    variant === 'inline' ? `flex ${INLINE_PREVIEW_BOX_CLASS} flex-col overflow-hidden rounded-md border border-[var(--color-border)]${(embed ? ' group' : '')}` : `flex h-full min-h-0 flex-col${(embed ? ' group' : '')}`
  // ── 1. Which views this .base owns, and where they run ────────────────────
  const viewsQuery = useQuery({
    queryKey: ['library', workspaceId, 'knowledge', 'base-views', entry.path],
    queryFn: () => loadBaseViews(workspaceId, entry.path),
    staleTime: 10_000,
    refetchOnWindowFocus: false,
  })

  const answer = viewsQuery.data
  const views = answer?.views ?? []
  const collectionId = answer?.collection_id
  const collectionRoot = answer?.collection_root

  // Selected tab, by slug so it survives a refetch; reset per file. An embed
  // seeds the view it already resolved (ADR-083 EMB-040) instead of always
  // starting at views[0] — the same reset effect re-seeds it if the file (or
  // the embed's own resolved view) changes under an already-mounted embed.
  const [selectedSlug, setSelectedSlug] = useState<string | undefined>(embed?.viewName)
  useEffect(() => setSelectedSlug(embed?.viewName), [entry.path, embed?.viewName])
  const selected = views.find((v) => v.name === selectedSlug) ?? defaultViewFor(views)
  const tabLabels = useMemo(() => tabLabelsFor(views), [views])

  // code-review finding #9 — the escape hatch for the "no views" dead end:
  // whether the "no views" state should show the raw file (view/edit, the
  // existing text-file edit path) instead of its stated message. Reset per
  // file so switching files never leaves a stale raw view mounted.
  const [showRaw, setShowRaw] = useState(false)
  useEffect(() => setShowRaw(false), [entry.path])

  // The raw file is fetched ONLY for that escape hatch, so a base that opens
  // as its views never pays for reading its own bytes. Nothing parses it.
  const rawQuery = useQuery({
    queryKey: libraryQueryKeys.content(workspaceId, entry.path),
    queryFn: () => loadContent(workspaceId, entry.path),
    enabled: showRaw,
    staleTime: 10_000,
  })

  // ── 2. The selected view's evaluated result ───────────────────────────────
  // code-review finding #3(c) — this is the EXPENSIVE fetch (a full view
  // evaluation, not a static file read), so window refocus must not refire
  // it on every alt-tab back into the app the way the library default would.
  // staleTime is raised to match: a minute is long enough that a reader
  // flipping between two apps never re-triggers evaluation mid-read, while
  // still refreshing well within a normal editing session.
  //
  // ADR-083 spec ~line 901 / ADR ~line 661 (M1) — "No more than 4 view
  // evaluations may be in flight page-wide at any instant" — a dashboard of
  // N modules inside LazyEmbedMount's mount margin all mount at once (that
  // primitive deliberately has no cap of its own — see its own module doc),
  // so without a bound here N mounts means N simultaneous evaluations
  // hitting the single Go binary. `viewEvaluationPool` (viewEvaluationPool.ts,
  // the same synchronous-reservation shape as pdfWorkerPool.ts / EMB-032,
  // applied to this ceiling of 4) is acquired BEFORE the fetch and released
  // the moment it settles — a view evaluation is one bounded fetch, not a
  // resource held for the component's whole mounted lifetime the way a PDF
  // worker is, so there is no "hold past success" case to reason about here.
  //
  // The lease is acquired against react-query's OWN queryFn `signal` — the
  // one TanStack Query itself recognises as a legitimate cancellation
  // (verified against its docs: "if consumed by your queryFn, unmount also
  // cancels the request", and the query's state then REVERTS rather than
  // recording an error — unlike a plain externally-thrown AbortError, which
  // would sit in the cache as a genuine error for a later remount to trip
  // over). Consuming it (destructuring `{ signal }` below) marks it
  // consumed; this file does not then wait for react-query's own internal
  // GC/observer-count timing to decide the query is unused — the explicit
  // `cancelQueries` call in the unmount effect below fires the SAME
  // recognised cancellation deterministically, the moment THIS component
  // unmounts, matching the queryKey exactly so only this instance's own
  // fetch is touched (a page can have many `.base` embeds sharing one
  // QueryClient). A lease still QUEUED at that point is removed from
  // `viewEvaluationPool`'s queue immediately (EMB-065 applied to a
  // still-queued lease — the same requirement pdfWorkerPool.ts's own unmount
  // test proves), freeing the slot for the next real waiter.
  const [resultQueued, setResultQueued] = useState(false)
  const queryClient = useQueryClient()
  const resultQueryKey = ['library', workspaceId, 'knowledge', 'view-result', collectionId, selected?.name]
  useEffect(() => {
    return () => {
      void queryClient.cancelQueries({ queryKey: resultQueryKey, exact: true })
    }
  }, [queryClient, workspaceId, collectionId, selected?.name])
  // Reset whenever a genuinely new fetch is about to start (file, tab, or
  // resolved-view change) — mirrors this file's own selectedSlug/showRaw
  // reset-effect convention above, so a stale `true` from a prior fetch can
  // never outlive the fetch that set it.
  useEffect(() => setResultQueued(false), [collectionId, selected?.name])
  const resultQuery = useQuery({
    queryKey: resultQueryKey,
    queryFn: async ({ signal }) => {
      setResultQueued(false)
      const lease = await viewEvaluationPool.acquire(signal, () => setResultQueued(true))
      setResultQueued(false)
      try {
        return await loadViewResult(workspaceId, collectionId as string, selected?.name as string)
      } finally {
        lease.release()
      }
    },
    enabled: collectionId !== undefined && selected !== undefined,
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    // F4 (SILENT-FAILURES-rate-limits-dd25339bf.md): this query previously
    // inherited the app-wide default retry timing, which ignores a 429's
    // Retry-After entirely — set explicitly here (rather than relying on
    // the ambient QueryClient's defaultOptions) so it is correct wherever
    // this component mounts, and independently testable. Retry COUNT/
    // exclusions are unchanged (shouldRetryQuery, the same predicate every
    // other query uses); only the DELAY for a 429 changes.
    retry: shouldRetryQuery,
    retryDelay: rateLimitAwareQueryRetryDelay,
  })

  const resolveImageUrl = useMemo(
    () => (vaultPath: string) =>
      libraryDownloadUrl(
        workspaceId,
        collectionRoot === undefined || collectionRoot === '' || collectionRoot === '.'
          ? vaultPath
          : `${collectionRoot}/${vaultPath}`,
      ),
    [workspaceId, collectionRoot],
  )

  // ── KB-8: row-open + relation-cell-link wiring ─────────────────────────────
  // Mirrors resolveImageUrl's own collection-root guard (undefined / '' / '.'
  // all mean "the collection IS the workspace root") rather than a second,
  // differently-shaped check.
  const toWorkspacePath = useMemo(
    () => (collectionRelativePath: string) =>
      collectionRoot === undefined || collectionRoot === '' || collectionRoot === '.'
        ? collectionRelativePath
        : collectionPathToWorkspacePath(collectionRoot, collectionRelativePath),
    [collectionRoot],
  )

  const linkHref = useMemo(
    () =>
      embed?.linkHref ??
      ((collectionRelativePath: string) => libraryNoteHref(workspaceId, toWorkspacePath(collectionRelativePath))),
    [workspaceId, toWorkspacePath, embed?.linkHref],
  )

  const onOpenPath = useMemo(
    () => (onOpenNote ? (collectionRelativePath: string) => onOpenNote(toWorkspacePath(collectionRelativePath)) : undefined),
    [onOpenNote, toWorkspacePath],
  )

  // See basenameNoExt's doc comment above: resolved-or-unknown only, from THIS
  // view's own rows — never `unresolved`, which this view cannot honestly
  // claim about the whole collection.
  const result = resultQuery.data

  // F4 — while WAITING on a retry after a 429, name the real wait instead of
  // the generic "Evaluating view…" spinner. `undefined` whenever there is
  // nothing (yet) to retry, or the last failure wasn't a 429 with a usable
  // Retry-After — those cases fall back to the generic spinner. Reads the
  // SAME function the retryDelay above is built from (rateLimitedRetryDelayMs)
  // so the displayed wait can never contradict the wait actually honoured.
  const resultThrottledRetryDelayMs =
    resultQuery.isLoading && resultQuery.failureCount > 0
      ? rateLimitedRetryDelayMs(resultQuery.failureReason)
      : undefined

  // F5b — a query that once had data keeps that data through a FAILED
  // background refetch (TanStack Query does not clear `data` on a
  // background error); `result !== undefined` is therefore exactly "we have
  // something to show", regardless of whether the query's CURRENT fetch
  // attempt is erroring. Used below to keep the pane's rows (and any open
  // RecordFieldEditor) mounted through a refused background reload instead
  // of tearing the whole pane down to the full error state.
  const resultIsBackgroundRefreshFailure = result !== undefined && resultQuery.isError

  // ── WL-1 remaining half: a collection-wide resolver for the standalone pane ─
  // See COLLECTION_LINK_ROW_QUERY_CAP's doc comment above for the full
  // rationale. Skipped entirely when an embed already supplies a complete
  // resolver (EMB-028: an embed's byte-for-byte-unaffected guarantee), and
  // whenever there is no result yet to draw row paths from.
  const linkRowPaths = useMemo(
    () =>
      embed?.resolveWikilink || !result
        ? ([] as string[])
        : collectionLinkRowPathsFn(result.rows),
    [result, embed?.resolveWikilink],
  )

  // UAT D-135: ONE multi-path kind=links request covers every
  // wikilink-carrying row — see useCollectionLinkGraph's own doc for why one
  // request per row (the old shape) tripped the gateway's rate limiter.
  const linkGraph = useCollectionLinkGraph({
    workspaceId,
    collectionId,
    paths: linkRowPaths,
    loadGraph,
  })

  // WL-1's fix is only as good as the evidence behind it, and the query is
  // `retry: false`. A failure contributes no edges and, until this banner
  // existed, said nothing — so the resolver silently dropped back to the old
  // row-title-match tier that can only answer `resolved` or `unknown`, and
  // every relation cell went white again. That is EXACTLY the symptom WL-1
  // was written to fix, reappearing with nothing on screen to show the fix
  // had stopped working. Stated once at page level below — the same
  // "handled ONE page-level statement, never per embed" treatment
  // `graph_unavailable` already gets in the note reader.
  const failedCollectionLinkQueries = linkGraph.failed
  // UAT D-135: whether the failure was the gateway's own rate limiter
  // (HTTP 429) — named in the banner rather than hidden behind a spinner,
  // so a reader can tell "throttled, wait" from "broken".
  const rateLimitedCollectionLinkQueries = linkGraph.rateLimited

  // ADR-083 EMB-048/WL-1: an embed's own `resolveWikilink` (the note reader's
  // real link graph) takes over completely when supplied. The row-scoped
  // fallback below is no longer resolved-or-unknown-only: it now checks each
  // loaded row's own link-graph edges FIRST and can return a real
  // `unresolved` verdict from that evidence. Only rows beyond
  // `COLLECTION_LINK_ROW_QUERY_CAP` — and a failed graph request, which the
  // banner above the table reports — fall through to the older guess, which
  // renders a genuinely broken link as merely unverified.
  const resolveWikilink = useMemo(() => {
    if (embed?.resolveWikilink) return embed.resolveWikilink
    if (!result) return undefined
    return makeCollectionLinkResolver(linkGraph.edges, linkGraph.nodes, result.rows)
  }, [result, embed?.resolveWikilink, linkGraph.edges, linkGraph.nodes])

  // ── States before a result can render ─────────────────────────────────────
  // Every one renders inside the SAME `base-preview` container, so "the base
  // surface mounted" is one stable fact regardless of which state it is in.
  const stateBody = (() => {
    if (viewsQuery.isLoading) {
      return (
        <Centered>
          <SpinnerGap size={16} className="animate-spin" /> Reading base file…
        </Centered>
      )
    }
    if (viewsQuery.isError || answer === undefined) {
      return (
        <QueryErrorState
          layout="fill"
          message="Could not read this base file."
          onRetry={() => void viewsQuery.refetch()}
          testId="base-preview-content-error"
        />
      )
    }
    if (!answer.is_knowledge_base) {
      return (
        <Centered>
          <p data-testid="base-preview-no-collection">
            This file is not inside a knowledge base, so its views have nowhere to run. Views are
            served from the collection the base was imported into.
          </p>
        </Centered>
      )
    }
    if (views.length === 0) {
      // code-review finding #9 — the escape hatch. View/edit reuses the same
      // shared edit path every other text file gets (LibraryCodePreview);
      // Download reuses the same authenticated download URL the rest of the
      // Library uses. Neither depends on anything having understood the file,
      // so both work whatever the reason there are no views.
      const raw = rawQuery.data
      const readable = raw?.is_text === true && !raw.too_large && raw.content !== undefined
      if (showRaw) {
        if (rawQuery.isLoading) {
          return (
            <Centered>
              <SpinnerGap size={16} className="animate-spin" /> Reading base file…
            </Centered>
          )
        }
        if (readable) {
          return (
            <div className={containerClass} data-testid="base-preview-raw" data-variant={variant}>
              <LibraryCodePreview workspaceId={workspaceId} entry={entry} content={raw.content as string} />
            </div>
          )
        }
      }
      // Zero views with rejections is NOT the same fact as zero views without
      // them, and saying "declares no views" over a file whose views all
      // failed to load would be false. The distinction is the server's now —
      // it is the one that read the view files.
      const allUnloadable = answer.unloadable_count > 0
      return (
        <Centered>
          <div className="flex flex-col items-center gap-[var(--space-2-5)]">
            <p data-testid="base-preview-no-views">
              {allUnloadable
                ? answer.unloadable_count === 1
                  ? 'The one view imported from this base file could not be loaded, so there is nothing to draw.'
                  : `All ${answer.unloadable_count} views imported from this base file could not be loaded, so there is nothing to draw.`
                : 'No views were imported from this base file, so there is nothing to draw.'}
            </p>
            <div className="flex items-center gap-[var(--space-2)]">
              <Button
                size="sm"
                variant="outline"
                onClick={() => setShowRaw(true)}
                data-testid="base-preview-view-raw"
                className="gap-[var(--space-1)]"
              >
                <Code size={14} /> View raw
              </Button>
              <Button
                size="sm"
                variant="outline"
                onClick={() => downloadLibraryEntry(workspaceId, entry)}
                data-testid="base-preview-download"
                className="gap-[var(--space-1)]"
              >
                <DownloadSimple size={14} /> Download
              </Button>
            </div>
          </div>
        </Centered>
      )
    }
    return undefined
  })()

  if (stateBody !== undefined) {
    return (
      <div className={containerClass} data-testid="base-preview" data-variant={variant}>
        {stateBody}
      </div>
    )
  }

  return (
    <div
      className={containerClass}
      data-testid="base-preview"
      data-variant={variant}
      {...(embed
        ? {
            title:
              'Links in this embedded view resolve against this note’s own links and may render differently from the same view opened in the Library.',
          }
        : {})}
    >
      {answer !== undefined && answer.unloadable_count > 0 && (
        <UnloadableNotice count={answer.unloadable_count} entries={answer.unloadable} />
      )}

      {/* EMB-043: the embed chose no view itself — say so, and offer nothing
          to switch to (there is no tab list at all in this branch). */}
      {embed?.caption !== undefined && (
        <p
          data-testid="base-preview-embed-caption"
          className="shrink-0 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
        >
          {embed.caption}
        </p>
      )}

      {/* View tabs — the wireframe's tablist; first view selected by default
          (or the embed's own resolved view, EMB-040). `name` is the server's
          slug, used verbatim as both the React key and the fetch address, so
          two tabs can never share either. Inside an embed with a switcher to
          offer, the whole row is hidden until pointer hover or keyboard
          focus reaches it (EMB-046) — real, existing tab-switching, gated,
          not decoration. */}
      {(embed === undefined || embed.showViewSwitcher) && (
        <div
          role="tablist"
          aria-label="Views"
          data-testid="base-preview-tablist"
          className={`flex shrink-0 gap-[var(--space-0-5)] overflow-x-auto border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-1)] ${
            embed
              ? 'opacity-0 transition-opacity duration-150 focus-within:opacity-100 group-hover:opacity-100 group-focus-within:opacity-100'
              : ''
          }`}
        >
        {views.map((v) => {
          const active = v.name === selected?.name
          return (
            <button
              key={v.name}
              type="button"
              tabIndex={0}
              role="tab"
              aria-selected={active}
              onClick={() => setSelectedSlug(v.name)}
              data-testid={`base-view-tab-${v.name}`}
              title={v.unservable === true ? v.unservable_reason : undefined}
              className={`-mb-[var(--border-width-hairline)] whitespace-nowrap border-b-2 px-[var(--space-2)] py-[var(--space-2)] text-[length:var(--type-caption-size)] transition-colors ${
                active
                  ? 'border-[var(--color-accent)] text-[var(--color-secondary)]'
                  : 'border-transparent text-[var(--color-muted)] hover:text-[var(--color-secondary)]'
              }`}
            >
              {tabLabels.get(v.name) ?? v.label}
              {v.unservable === true && (
                <Warning
                  size={12}
                  className="ml-[var(--space-1)] inline align-[-1px] text-[var(--color-warning)]"
                  data-testid={`base-view-tab-unservable-${v.name}`}
                />
              )}
              {active && result !== undefined && result.refusal === undefined && (
                <span className="ml-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{result.rows.length}</span>
              )}
            </button>
          )
        })}
        {/* UAT D-119 (2026-09-13): a HEALTHY base is text too. The raw
            editor used to exist only behind the "no views" empty state, so
            a base whose views all loaded had no way to be opened, read or
            repaired as the YAML it is. Gated on the listing's own
            is_text_editable (the backend half of D-119 now sets it for
            .base), never on the file having failed to parse. Library-only:
            an embed shows a view, not a file. */}
        {embed === undefined && entry.is_text_editable && (
          <button
            type="button"
            tabIndex={0}
            aria-pressed={showRaw}
            onClick={() => setShowRaw((v) => !v)}
            data-testid="base-preview-source-toggle"
            title={showRaw ? 'Back to the views' : 'Open the base file as text'}
            className={`-mb-[var(--border-width-hairline)] ml-auto flex shrink-0 items-center gap-[var(--space-1)] whitespace-nowrap border-b-2 px-[var(--space-2)] py-[var(--space-2)] text-[length:var(--type-caption-size)] transition-colors ${
              showRaw
                ? 'border-[var(--color-accent)] text-[var(--color-secondary)]'
                : 'border-transparent text-[var(--color-muted)] hover:text-[var(--color-secondary)]'
            }`}
          >
            <Code size={13} /> {showRaw ? 'Views' : 'Source'}
          </button>
        )}
        </div>
      )}

      {/* D-119: the base file as text, through the SAME view/edit shell every
          other text file gets (LibraryCodePreview → LibraryTextPreview), so a
          malformed base is repaired with the editor a healthy one is edited
          with. The views body below stays mounted but hidden, so toggling
          back costs no re-evaluation. A saved edit invalidates this base's
          own views list and its cached bytes; whether the text becomes a
          view is the importer's job (D-119's other half, D-13), not this
          editor's. */}
      {showRaw && (
        <div className="flex-1 overflow-auto bg-[var(--color-surface-0)]" data-testid="base-preview-raw">
          {rawQuery.isLoading ? (
            <Centered>
              <SpinnerGap size={16} className="animate-spin" /> Reading base file…
            </Centered>
          ) : rawQuery.data?.is_text === true && !rawQuery.data.too_large && rawQuery.data.content !== undefined ? (
            <LibraryCodePreview
              workspaceId={workspaceId}
              entry={entry}
              content={rawQuery.data.content}
              onSaved={() => {
                void queryClient.invalidateQueries({
                  queryKey: ['library', workspaceId, 'knowledge', 'base-views', entry.path],
                })
                void queryClient.invalidateQueries({ queryKey: libraryQueryKeys.content(workspaceId, entry.path) })
              }}
            />
          ) : (
            <QueryErrorState
              layout="fill"
              message="Could not read this base file as text."
              onRetry={() => void rawQuery.refetch()}
              testId="base-preview-raw-error"
            />
          )}
        </div>
      )}

      {/* WL-1 honesty surface. ONE page-level statement when the view's
          link-graph request failed — never one marker per cell, which is how
          the note reader treats `graph_unavailable` too. Without it, the
          whole collection-wide resolver degrades back to its pre-WL-1
          behaviour (relation cells rendering white/unverified) with nothing
          on screen to say the evidence never arrived; the query is
          `retry: false`, so a single blip is enough to trigger it. */}
      {failedCollectionLinkQueries > 0 && (
        <div
          data-testid="base-preview-link-graph-degraded"
          className="flex items-center justify-between gap-[var(--space-2-5)] border-b border-[var(--color-border)] bg-[var(--color-warning)]/10 px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-caption-size)] leading-snug text-[var(--color-warning)]"
        >
          <span>
            Link checking is incomplete for this view — its links show as unverified rather than
            confirmed broken.
            {/* UAT D-135: a throttled request is named as such, never hidden. */}
            {rateLimitedCollectionLinkQueries && (
              <span data-testid="base-preview-link-graph-rate-limited">
                {' '}
                The gateway rate-limited the link-checking request (HTTP 429); wait a moment before
                retrying.
              </span>
            )}
          </span>
          <button
            type="button"
            tabIndex={0}
            onClick={() => {
              if (linkGraph.query.isError) void linkGraph.query.refetch()
            }}
            data-testid="base-preview-link-graph-retry"
            className="shrink-0 rounded border border-current px-[var(--space-2)] py-[var(--space-0-5)] text-[length:var(--type-caption-size)] uppercase tracking-wide hover:opacity-80"
          >
            Retry
          </button>
        </div>
      )}

      {/* Body: the selected view's evaluated result. */}
      <div className="flex-1 overflow-auto bg-[var(--color-surface-0)]">
        {resultQueued ? (
          // M1 / ADR-083 spec ~line 901 — the visible waiting state for the
          // page-wide 4-evaluation ceiling, checked BEFORE the generic
          // loading branch below: `resultQuery.isLoading` is also true while
          // queued (react-query has no third state for "waiting on an
          // app-level admission gate"), so the honest, named reason must win
          // over the indistinguishable-from-a-slow-fetch generic spinner.
          <Centered>
            <span data-testid="base-preview-result-queued" className="flex items-center gap-[var(--space-2)]">
              <SpinnerGap size={16} className="animate-spin" />
              Only {VIEW_EVALUATION_POOL_CEILING} views can evaluate on this page at once. This one
              will run automatically once another finishes or scrolls out of view.
            </span>
          </Centered>
        ) : resultThrottledRetryDelayMs !== undefined ? (
          // F4: waiting on a retry the query is going to make anyway, honouring
          // (a capped) Retry-After — say so plainly, with the real wait, rather
          // than the indistinguishable-from-hung generic spinner.
          <Centered>
            <span data-testid="base-preview-result-throttled" className="flex items-center gap-[var(--space-2)]">
              <SpinnerGap size={16} className="animate-spin" />
              Busy — retrying in {Math.ceil(resultThrottledRetryDelayMs / 1000)}s
            </span>
          </Centered>
        ) : resultQuery.isLoading ? (
          <Centered>
            <SpinnerGap size={16} className="animate-spin" /> Evaluating view…
          </Centered>
        ) : resultQuery.isError && result === undefined ? (
          // F4: no data has EVER loaded for this view — a real failure, shown
          // in full. A rate-limited refusal is named as such (never the
          // generic message), because "Could not evaluate this view." reads
          // as broken when the view is merely throttled.
          resultQuery.error instanceof ApiError && resultQuery.error.isRateLimited() ? (
            <QueryErrorState
              layout="fill"
              message="This view is rate-limited by the knowledge workspace limit. Wait a moment and retry."
              onRetry={() => void resultQuery.refetch()}
              testId="base-preview-result-rate-limited"
            />
          ) : (
            <QueryErrorState
              layout="fill"
              message="Could not evaluate this view."
              onRetry={() => void resultQuery.refetch()}
              testId="base-preview-result-error"
            />
          )
        ) : result !== undefined ? (
          <>
            {/* F5b: we ALREADY have good data — a failed BACKGROUND refetch
                (e.g. a library_changed reload refused by the same rate
                limiter while a reader has a cell editor open) must not tear
                the pane down to the full error state, which would unmount
                any open RecordFieldEditor and its unsaved text/error message
                along with it. Say so in a small notice instead; the rows
                (and any open editor) stay exactly as they were. */}
            {resultIsBackgroundRefreshFailure && (
              <div
                data-testid="base-preview-result-refresh-failed"
                className="flex items-center justify-between gap-[var(--space-2-5)] border-b border-[var(--color-border)] bg-[var(--color-warning)]/10 px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-caption-size)] leading-snug text-[var(--color-warning)]"
              >
                <span>
                  {resultQuery.error instanceof ApiError && resultQuery.error.isRateLimited()
                    ? 'A refresh of this view was rate-limited by the knowledge workspace limit. Showing the last loaded data.'
                    : 'A refresh of this view failed. Showing the last loaded data.'}
                </span>
                <button
                  type="button"
                  tabIndex={0}
                  onClick={() => void resultQuery.refetch()}
                  data-testid="base-preview-result-refresh-retry"
                  className="shrink-0 rounded border border-current px-[var(--space-2)] py-[var(--space-0-5)] text-[length:var(--type-caption-size)] uppercase tracking-wide hover:opacity-80"
                >
                  Retry
                </button>
              </div>
            )}
            <ViewPartsRenderer
            result={result}
            resolveImageUrl={resolveImageUrl}
            {...(onOpenPath ? { onOpenPath } : {})}
            {...(resolveWikilink ? { resolveWikilink } : {})}
            linkHref={linkHref}
            // ADR-083 Step 5 (D-D). Passing workspaceId is what ENABLES inline
            // record editing — ViewPartsRenderer builds its edit context from it,
            // and without this line the editors exist but no view can reach them.
            // Which cells actually offer an editor is decided per-cell from the
            // wire (a derived cell never does; a relation cell gets the PICKER,
            // GAP-02 / #700), not here.
            workspaceId={workspaceId}
            // The picker's search is scoped by collection (the find endpoint
            // requires exactly one), so the collection this base's views run
            // over is part of the edit context. Guarded: collectionId is
            // absent while the knowledge-base info query is still loading —
            // relation cells render inert for that window and gain their
            // picker once it lands.
            {...(collectionId !== undefined ? { collectionId } : {})}
            // A successful write changes the stored record, so the rendered view
            // is now stale. Invalidate rather than patching in place: the server
            // owns derived columns, and a locally-patched row would show a stale
            // computed value beside a fresh one.
            //
            // ADR-083 §4.5 names FOUR caches, and this used to invalidate one
            // — the exact view key that hosted the edit, with `exact: true`.
            // Two embeds of the SAME view share that key, so the headline
            // scenario worked and hid the rest: a different saved view over
            // the same collection (the dashboard shape this ADR exists for)
            // kept showing the old value indefinitely, and the written note's
            // own body, outline and backlink rail kept pre-write frontmatter.
            // The two doc comments upstream described all four, which made it
            // worse than a plain omission — the next reader would believe it
            // was handled.
            onFieldWritten={(written) => {
              // (1) Every view-result query for THIS COLLECTION, not one
              //     view. Prefix match, deliberately without `exact`: the key
              //     shape is [...,'view-result', collectionId, viewName], so
              //     dropping the view name matches every view over it. Still
              //     scoped to the collection — §4.5 is explicit that a
              //     blanket sweep would re-evaluate every mounted view on the
              //     page for a one-field edit.
              void queryClient.invalidateQueries({
                queryKey: ['library', workspaceId, 'knowledge', 'view-result', collectionId],
              })
              // (2)-(4) The WRITTEN NOTE's own per-note caches. `written.path`
              //     was previously discarded; it is the only thing that makes
              //     this addressable without a new wire field. It is
              //     COLLECTION-relative (it comes straight off `VaultFindRow.
              //     path`, the same value `collectionLinkRowPaths` feeds to
              //     the graph endpoint), so the two WORKSPACE-keyed caches go
              //     through `toWorkspacePath` and the collection-keyed graph
              //     does not. Per-note, never collection-wide — §4.5 again: a
              //     collection-wide sweep would refetch every open note for a
              //     one-field edit.
              const collectionRelPath = written.path
              const workspacePath = toWorkspacePath(collectionRelPath)
              void queryClient.invalidateQueries({
                queryKey: libraryQueryKeys.content(workspaceId, workspacePath),
                exact: true,
              })
              void queryClient.invalidateQueries({
                queryKey: ['library', 'knowledge', 'outline', workspaceId, workspacePath],
                exact: true,
              })
              // The links graph is cached under TWO key shapes today — the
              // note reader's (`KnowledgeNoteView`) and this pane's own row
              // queries above. Both are invalidated because both can be
              // holding the pre-write answer for this note; unifying the two
              // shapes is a separate change and skipping either here would
              // leave a real stale rail behind.
              void queryClient.invalidateQueries({
                queryKey: ['library', 'knowledge', 'graph', 'links', workspaceId, collectionId, collectionRelPath],
                exact: true,
              })
              void queryClient.invalidateQueries({
                queryKey: ['library', workspaceId, 'knowledge', 'graph', 'links', collectionId, collectionRelPath],
                exact: true,
              })
            }}
            />
          </>
        ) : null}
      </div>
    </div>
  )
}
