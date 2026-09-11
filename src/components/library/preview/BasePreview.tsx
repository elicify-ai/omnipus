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
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query'
import { Code, DownloadSimple, SpinnerGap, Warning } from '@phosphor-icons/react'

import { Button } from '@/components/ui/button'
import { QueryErrorState } from '@/components/shared/QueryErrorState'
import {
  fetchKnowledgeBaseViews,
  fetchKnowledgeGraph,
  fetchKnowledgeViewResult,
  fetchLibraryContent,
  libraryDownloadUrl,
  libraryQueryKeys,
} from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'
import type {
  KnowledgeBaseViews,
  KnowledgeGraphEdge,
  KnowledgeGraphNode,
  ViewResult,
} from '@/lib/api/generated/openapi-types'

import { LibraryCodePreview } from './LibraryCodePreview'
import { ViewPartsRenderer } from './viewparts/ViewPartsRenderer'
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
 * this view's own cells lives in each ROW's own outbound-links graph, fetched
 * by that row's collection-relative path — exactly how KnowledgeNoteView
 * fetches its answer for the one note it has open, just pointed at a bounded
 * set of paths (one per loaded row) instead of one.
 *
 * Bounded so a very large view cannot fan out into hundreds of parallel graph
 * fetches. Rows past the cap keep the same honest fallback (row-title match,
 * else `unknown`) they always had — never silently promoted to `resolved`,
 * which is the exact dishonesty the three-state model exists to prevent.
 */
const COLLECTION_LINK_ROW_QUERY_CAP = 40

const defaultLoadGraph: KnowledgeGraphLoader = ({ workspaceId, collectionId, kind, path, hops, limit }) =>
  fetchKnowledgeGraph(workspaceId, {
    collectionId,
    kind,
    ...(path === undefined ? {} : { path }),
    ...(hops === undefined ? {} : { hops }),
    ...(limit === undefined ? {} : { limit }),
  })

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

/** The record identifier a relation cell's `[[wikilink]]` token most often
 *  names, checked against the rows THIS view actually loaded (KB-8b). This is
 *  the FALLBACK tier of `resolveWikilink` below — it never has the whole
 *  collection's link graph, only its own row set, so on its own it can
 *  honestly answer `resolved` (found here) or `unknown` (not found in what it
 *  has) — never `unresolved`, which would claim knowledge of the whole
 *  collection this tier alone does not have. (The FIRST tier, WL-1's
 *  collection-wide row-link-graph lookup, can honestly answer `unresolved`.) */
function basenameNoExt(path: string): string {
  const base = path.split('/').pop() ?? path
  const dot = base.lastIndexOf('.')
  return dot <= 0 ? base : base.slice(0, dot)
}

/** A KnowledgeGraphEdge's `to_path` basename, WITH its extension — mirrors
 *  KnowledgeNoteView.tsx's own private `basenameOf` exactly (edge matching
 *  compares against the collection-relative path a resolved edge reports,
 *  which keeps its extension; `basenameNoExt` above is a different, row-
 *  identity comparison and must not be reused here). */
function basenameOf(path: string): string {
  const parts = path.split('/')
  return parts[parts.length - 1] || path
}

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
    <div className="flex flex-1 items-center justify-center gap-2 p-6 text-center text-xs text-[var(--color-muted)]">
      {children}
    </div>
  )
}

/** "N views could not be loaded" — the server's rejection count, said out
 *  loud. Silently showing fewer tabs than the base has views is the exact
 *  silent loss this surface exists to end. */
function UnloadableNotice({ count }: { count: number }) {
  return (
    <div
      className="flex shrink-0 items-center gap-1.5 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-1.5 text-[11px] text-[var(--color-warning)]"
      data-testid="base-preview-unloadable"
    >
      <Warning size={13} />
      {count === 1
        ? '1 view from this file could not be loaded and is not shown.'
        : `${count} views from this file could not be loaded and are not shown.`}
    </div>
  )
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
    (variant === 'inline'
      ? `flex ${INLINE_PREVIEW_BOX_CLASS} flex-col overflow-hidden rounded-md border border-[var(--color-border)]`
      : 'flex h-full min-h-0 flex-col') + (embed ? ' group' : '')
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
  const selected = views.find((v) => v.name === selectedSlug) ?? views[0]

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

  // ── WL-1 remaining half: a collection-wide resolver for the standalone pane ─
  // See COLLECTION_LINK_ROW_QUERY_CAP's doc comment above for the full
  // rationale. Skipped entirely when an embed already supplies a complete
  // resolver (EMB-028: an embed's byte-for-byte-unaffected guarantee), and
  // whenever there is no result yet to draw row paths from.
  const collectionLinkRowPaths = useMemo(() => {
    if (embed?.resolveWikilink) return [] as string[]
    if (!result) return [] as string[]
    const seen = new Set<string>()
    const paths: string[] = []
    for (const r of result.rows) {
      if (seen.has(r.path)) continue
      seen.add(r.path)
      paths.push(r.path)
      if (paths.length >= COLLECTION_LINK_ROW_QUERY_CAP) break
    }
    return paths
  }, [result, embed?.resolveWikilink])

  const collectionLinkQueries = useQueries({
    queries: collectionLinkRowPaths.map((rowPath) => ({
      // Mirrors KnowledgeNoteView's own links-graph query key shape — same
      // cache, same request, just addressed by a row's path instead of the
      // one open note's path.
      queryKey: ['library', workspaceId, 'knowledge', 'graph', 'links', collectionId, rowPath],
      queryFn: () =>
        loadGraph({ workspaceId, collectionId: collectionId as string, kind: 'links' as const, path: rowPath }),
      enabled: collectionId !== undefined,
      staleTime: 60_000,
      retry: false,
      refetchOnWindowFocus: false,
    })),
  })

  const collectionLinkEdges = useMemo(() => {
    const edges: KnowledgeGraphEdge[] = []
    for (const q of collectionLinkQueries) {
      if (q.data) edges.push(...q.data.edges)
    }
    return edges
  }, [collectionLinkQueries])

  const collectionLinkNodes = useMemo(() => {
    const nodes: KnowledgeGraphNode[] = []
    for (const q of collectionLinkQueries) {
      if (q.data) nodes.push(...q.data.nodes)
    }
    return nodes
  }, [collectionLinkQueries])

  // WL-1's fix is only as good as the evidence behind it, and every one of
  // these queries is `retry: false`. A failed one contributes no edges and,
  // until now, said nothing — so the resolver silently dropped back to the
  // old row-title-match tier that can only answer `resolved` or `unknown`,
  // and every relation cell went white again. That is EXACTLY the symptom
  // WL-1 was written to fix, reappearing with nothing on screen to show the
  // fix had stopped working. Counted here and stated once at page level
  // below — the same "handled ONE page-level statement, never per embed"
  // treatment `graph_unavailable` already gets in the note reader.
  const failedCollectionLinkQueries = useMemo(
    () => collectionLinkQueries.filter((q) => q.isError).length,
    [collectionLinkQueries],
  )

  // ADR-083 EMB-048/WL-1: an embed's own `resolveWikilink` (the note reader's
  // real link graph) takes over completely when supplied. The row-scoped
  // fallback below is no longer resolved-or-unknown-only: it now checks each
  // loaded row's own link-graph edges FIRST and can return a real
  // `unresolved` verdict from that evidence. Only rows beyond
  // `COLLECTION_LINK_ROW_QUERY_CAP` — and cells whose graph query failed,
  // which the banner above the table reports — fall through to the older
  // guess, which renders a genuinely broken link as merely unverified.
  const resolveWikilink = useMemo(() => {
    if (embed?.resolveWikilink) return embed.resolveWikilink
    if (!result) return undefined
    return (target: string): KbLinkResolution => {
      // 1. WL-1: real, collection-wide evidence first — the SAME edge the
      //    note reader would see, drawn from whichever loaded row's own
      //    markdown actually carries this wikilink text (see the cap's doc
      //    comment for why this can be a real `resolved`/`unresolved`
      //    verdict rather than the old resolved-or-unknown-only guess).
      const edge = collectionLinkEdges.find(
        (e) => e.link_text === target || e.to_path === target || basenameOf(e.to_path) === target,
      )
      if (edge) {
        if (edge.resolution === 'unresolved') return { state: 'unresolved' }
        const node = collectionLinkNodes.find((n) => n.path === edge.to_path)
        if (node && node.exists === false) return { state: 'unresolved' }
        return { state: 'resolved', path: edge.to_path }
      }
      // 2. Fallback: does the target literally name one of the rows THIS
      //    view loaded (KB-8b) — resolved-or-unknown only.
      const match = result.rows.find(
        (r) => r.title === target || r.id === target || basenameNoExt(r.path) === target,
      )
      return match ? { state: 'resolved', path: match.path } : { state: 'unknown' }
    }
  }, [result, embed?.resolveWikilink, collectionLinkEdges, collectionLinkNodes])

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
          <div className="flex flex-col items-center gap-3">
            <p data-testid="base-preview-no-views">
              {allUnloadable
                ? answer.unloadable_count === 1
                  ? 'The one view imported from this base file could not be loaded, so there is nothing to draw.'
                  : `All ${answer.unloadable_count} views imported from this base file could not be loaded, so there is nothing to draw.`
                : 'No views were imported from this base file, so there is nothing to draw.'}
            </p>
            <div className="flex items-center gap-2">
              <Button
                size="sm"
                variant="outline"
                onClick={() => setShowRaw(true)}
                data-testid="base-preview-view-raw"
                className="gap-1.5"
              >
                <Code size={14} /> View raw
              </Button>
              <Button
                size="sm"
                variant="outline"
                onClick={() => downloadLibraryEntry(workspaceId, entry)}
                data-testid="base-preview-download"
                className="gap-1.5"
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
        <UnloadableNotice count={answer.unloadable_count} />
      )}

      {/* EMB-043: the embed chose no view itself — say so, and offer nothing
          to switch to (there is no tab list at all in this branch). */}
      {embed?.caption !== undefined && (
        <p
          data-testid="base-preview-embed-caption"
          className="shrink-0 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-1 text-[11px] text-[var(--color-muted)]"
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
          className={`flex shrink-0 gap-0.5 overflow-x-auto border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-1.5 ${
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
              className={`-mb-px whitespace-nowrap border-b-2 px-2.5 py-2 text-[13px] transition-colors ${
                active
                  ? 'border-[var(--color-accent)] text-[var(--color-secondary)]'
                  : 'border-transparent text-[var(--color-muted)] hover:text-[var(--color-secondary)]'
              }`}
            >
              {v.label}
              {v.unservable === true && (
                <Warning
                  size={12}
                  className="ml-1 inline align-[-1px] text-[var(--color-warning)]"
                  data-testid={`base-view-tab-unservable-${v.name}`}
                />
              )}
              {active && result !== undefined && result.refusal === undefined && (
                <span className="ml-1.5 text-[10px] text-[var(--color-muted)]">{result.rows.length}</span>
              )}
            </button>
          )
        })}
        </div>
      )}

      {/* WL-1 honesty surface. ONE page-level statement when any of the
          per-row link-graph queries failed — never one marker per cell, which
          is how the note reader treats `graph_unavailable` too. Without it,
          the whole collection-wide resolver degrades back to its pre-WL-1
          behaviour (relation cells rendering white/unverified) with nothing
          on screen to say the evidence never arrived; the queries are
          `retry: false`, so a single blip is enough to trigger it. */}
      {failedCollectionLinkQueries > 0 && (
        <div
          data-testid="base-preview-link-graph-degraded"
          className="flex items-center justify-between gap-3 border-b border-[var(--color-border)] bg-[var(--color-warning)]/10 px-3 py-2 text-[11px] leading-snug text-[var(--color-warning)]"
        >
          <span>
            Link checking is incomplete for {failedCollectionLinkQueries}{' '}
            {failedCollectionLinkQueries === 1 ? 'row' : 'rows'} — their links show as unverified
            rather than confirmed broken.
          </span>
          <button
            type="button"
            tabIndex={0}
            onClick={() => {
              for (const q of collectionLinkQueries) if (q.isError) void q.refetch()
            }}
            data-testid="base-preview-link-graph-retry"
            className="shrink-0 rounded border border-current px-2 py-0.5 text-[10px] uppercase tracking-wide hover:opacity-80"
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
            <span data-testid="base-preview-result-queued" className="flex items-center gap-2">
              <SpinnerGap size={16} className="animate-spin" />
              Only {VIEW_EVALUATION_POOL_CEILING} views can evaluate on this page at once. This one
              will run automatically once another finishes or scrolls out of view.
            </span>
          </Centered>
        ) : resultQuery.isLoading ? (
          <Centered>
            <SpinnerGap size={16} className="animate-spin" /> Evaluating view…
          </Centered>
        ) : resultQuery.isError ? (
          <QueryErrorState
            layout="fill"
            message="Could not evaluate this view."
            onRetry={() => void resultQuery.refetch()}
            testId="base-preview-result-error"
          />
        ) : result !== undefined ? (
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
            // wire (a derived or relation cell never does), not here.
            workspaceId={workspaceId}
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
        ) : null}
      </div>
    </div>
  )
}
