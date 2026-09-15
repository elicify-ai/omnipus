// KnowledgeViewsList — the collection's own "Views" list (UAT D-13, web
// half), rendered inside KnowledgePanel's surface.
//
// WHY THIS EXISTS. A view authored with knowledge_configure's
// create_view/write_view writes `.omnipus-vault/views/<slug>.yaml` and NO
// `.base` file, so the ONLY views listing before this — the base preview's —
// could not see it: base-views is FILE-addressed (`?path=<.base>`) and the
// base preview only mounts for a `.base` entry. Such a view answered
// correctly over the API while having no UI surface at all. This list is
// addressed by COLLECTION (GET .../knowledge/views), so it shows every saved
// view the collection owns — authored and imported alike — and opens each
// one's evaluated result in a dialog, the same way a view hit in the vault
// search bar opens.
//
// Base previews and dashboard embeds keep listing by `source`; this list is
// additive, not a replacement.
//
// HONESTY RULES, inherited from the surfaces it mirrors:
//   - every view name is the SERVER's slug, passed VERBATIM to the view
//     endpoint (never re-derived — see fetchKnowledgeCollectionViews);
//   - an unservable view is LISTED, visibly disabled, with the loader's own
//     reason — never silently omitted;
//   - view files that failed to load are counted and named, not dropped;
//   - a failed list request is a visible banner with a Retry, never a silent
//     empty list.

import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { CircleNotch, SquaresFour, Warning } from '@phosphor-icons/react'

import {
  fetchKnowledgeCollectionViews,
  fetchKnowledgeViewResult,
} from '@/lib/api'
import type {
  KnowledgeCollectionViews,
  ViewResult,
} from '@/lib/api/generated/openapi-types'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

import { collectionPathToWorkspacePath, libraryNoteHref } from './KnowledgeBacklinks'
import { LibraryErrorBanner } from '../LibraryErrorBanner'
import { ViewPartsRenderer } from '../preview/viewparts/ViewPartsRenderer'
// The dialog's relation cells resolve against the collection with the SAME
// shared evidence ladder the base preview and the search bar's dialog use —
// one multi-path link-graph request, never a per-row fan-out (UAT D-135).
import {
  collectionLinkRowPaths,
  defaultKnowledgeGraphLoader,
  makeCollectionLinkResolver,
  useCollectionLinkGraph,
} from '../preview/useCollectionLinkGraph'

/** Test seam for the views list; production uses the module default. */
export type LoadCollectionViewsFn = (
  workspaceId: string,
  collectionId: string,
  signal?: AbortSignal,
) => Promise<KnowledgeCollectionViews>

/** Test seam for one view's evaluated result; production uses the module default. */
export type LoadViewResultFn = (
  workspaceId: string,
  collectionId: string,
  view: string,
  signal?: AbortSignal,
) => Promise<ViewResult>

export interface KnowledgeViewsListProps {
  workspaceId: string
  /** KnowledgeBaseInfo.collection_id — the address of the list. */
  collectionId: string
  /** Workspace-relative collection root, for building note hrefs. */
  collectionRootPath: string
  loadViews?: LoadCollectionViewsFn
  loadViewResult?: LoadViewResultFn
}

export function KnowledgeViewsList({
  workspaceId,
  collectionId,
  collectionRootPath,
  loadViews = fetchKnowledgeCollectionViews,
  loadViewResult = fetchKnowledgeViewResult,
}: KnowledgeViewsListProps) {
  const [openView, setOpenView] = useState<{ name: string; label: string } | null>(null)

  const viewsQuery = useQuery({
    queryKey: ['knowledge-collection-views', workspaceId, collectionId],
    queryFn: ({ signal }) => loadViews(workspaceId, collectionId, signal),
    staleTime: 30_000,
    retry: false,
    refetchOnWindowFocus: false,
  })

  // An empty collection list is NOT news — most collections author no views —
  // so an empty answer renders nothing rather than an empty box with a
  // heading. A FAILED answer is the opposite: always visible, never a silent
  // empty list.
  const views = viewsQuery.data?.views ?? []
  if (viewsQuery.isError) {
    return (
      <div data-testid="knowledge-views-error" className="flex flex-col gap-2">
        <LibraryErrorBanner
          message={
            viewsQuery.error instanceof Error
              ? viewsQuery.error.message
              : 'Could not read this knowledge base\'s saved views.'
          }
          testId="knowledge-views-error-banner"
        />
        <button
          type="button"
          tabIndex={0}
          onClick={() => void viewsQuery.refetch()}
          data-testid="knowledge-views-retry"
          className="self-start rounded border border-[var(--color-border)] px-2 py-1 text-xs text-[var(--color-secondary)] transition-colors hover:bg-[var(--color-surface-2)]"
        >
          Try again
        </button>
      </div>
    )
  }
  if (viewsQuery.isPending || views.length === 0) return null

  return (
    // The list is capped at roughly a third of the docked Library panel and
    // scrolls ITSELF beyond that (UAT layout finding, 2026-09-14): unbounded,
    // 69 saved views made this list 3,524px tall inside a 900px viewport,
    // pushing the file listing ~3.5kpx off-screen in a clipped container the
    // mouse wheel cannot scroll (scrollTop stays 0) — the folder was
    // effectively unnavigable. max-height on the wrapper with overflow-y-auto
    // keeps every view reachable by scrolling the list, never the page.
    <div
      data-testid="knowledge-views-list"
      className="flex max-h-[min(384px,45vh)] flex-col gap-1 overflow-y-auto"
    >
      <p className="px-2 text-[10px] font-medium uppercase tracking-wide text-[var(--color-muted)]">
        Saved views
      </p>
      <ul className="flex flex-col gap-1">
        {views.map((v) => {
          const unservable = v.unservable === true
          return (
            <li key={v.name}>
              <button
                type="button"
                tabIndex={0}
                disabled={unservable}
                aria-disabled={unservable || undefined}
                data-testid="knowledge-views-item"
                data-view={v.name}
                onClick={() => setOpenView({ name: v.name, label: v.label })}
                className={cn(
                  'flex w-full flex-col items-start gap-0.5 rounded-md px-2 py-1.5 text-left transition-colors',
                  unservable
                    ? 'cursor-default opacity-60'
                    : 'hover:bg-[var(--color-surface-2)]',
                )}
              >
                <span className="flex w-full items-center gap-1.5 text-sm text-[var(--color-secondary)]">
                  <SquaresFour size={13} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
                  <span className="flex-1 truncate">{v.label}</span>
                  {v.kind !== undefined && (
                    <Badge variant="outline" className="px-1.5 py-0 text-[10px] leading-4">
                      {v.kind}
                    </Badge>
                  )}
                </span>
                {/* Provenance, when there is any: an imported view names the
                    `.base` it came from; an authored view belongs to the
                    collection itself (D-13's whole point). */}
                <span className="w-full truncate text-[11px] text-[var(--color-muted)]">
                  {v.source !== undefined ? `from ${v.source}` : 'authored in this knowledge base'}
                </span>
                {unservable && (
                  <span
                    data-testid="knowledge-views-unservable"
                    className="text-left text-[11px] leading-snug text-[var(--color-warning)]"
                  >
                    {v.unservable_reason ?? "This view can't be served."}
                  </span>
                )}
              </button>
            </li>
          )
        })}
      </ul>
      {(viewsQuery.data?.unloadable_count ?? 0) > 0 && viewsQuery.data?.unloadable && (
        <p
          data-testid="knowledge-views-unloadable"
          className="flex items-start gap-1.5 px-2 text-[11px] leading-snug text-[var(--color-warning)]"
        >
          <Warning size={13} aria-hidden="true" className="mt-0.5 shrink-0" />
          <span>
            {viewsQuery.data.unloadable_count === 1
              ? '1 view file could not be loaded and is not shown.'
              : `${viewsQuery.data.unloadable_count} view files could not be loaded and are not shown.`}{' '}
            {viewsQuery.data.unloadable[0]?.reason}
          </span>
        </p>
      )}

      <KnowledgeViewDialog
        workspaceId={workspaceId}
        collectionId={collectionId}
        collectionRootPath={collectionRootPath}
        openView={openView}
        onClose={() => setOpenView(null)}
        loadViewResult={loadViewResult}
      />
    </div>
  )
}

/** The evaluated result of the one clicked view — the search bar's saved-view
 *  dialog shape, drawn over this panel's own collection evidence. */
function KnowledgeViewDialog({
  workspaceId,
  collectionId,
  collectionRootPath,
  openView,
  onClose,
  loadViewResult,
}: {
  workspaceId: string
  collectionId: string
  collectionRootPath: string
  openView: { name: string; label: string } | null
  onClose: () => void
  loadViewResult: LoadViewResultFn
}) {
  const viewResultQuery = useQuery({
    queryKey: ['knowledge-panel-view-result', workspaceId, collectionId, openView?.name],
    queryFn: ({ signal }) =>
      loadViewResult(workspaceId, collectionId, openView?.name as string, signal),
    enabled: openView !== null,
    retry: false,
  })

  // UAT D-135 / Codex #12 wiring: one multi-path request over the dialog's
  // own wikilink-carrying rows, and the shared two-tier resolver.
  const viewResult = viewResultQuery.data
  const linkRowPaths = useMemo(
    () => (viewResult ? collectionLinkRowPaths(viewResult.rows) : []),
    [viewResult],
  )
  const linkGraph = useCollectionLinkGraph({
    workspaceId,
    collectionId,
    paths: linkRowPaths,
    loadGraph: defaultKnowledgeGraphLoader,
  })
  const resolveWikilink = useMemo(
    () =>
      viewResult
        ? makeCollectionLinkResolver(linkGraph.edges, linkGraph.nodes, viewResult.rows)
        : undefined,
    [viewResult, linkGraph.edges, linkGraph.nodes],
  )
  const linkHref = useMemo(
    () => (collectionPath: string) =>
      libraryNoteHref(workspaceId, collectionPathToWorkspacePath(collectionRootPath, collectionPath)),
    [workspaceId, collectionRootPath],
  )

  return (
    <Dialog open={openView !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-3xl" data-testid="knowledge-views-dialog">
        <DialogHeader>
          <DialogTitle>{openView?.label}</DialogTitle>
          <DialogDescription>Saved view</DialogDescription>
        </DialogHeader>
        {viewResultQuery.isPending && (
          <div role="status" className="flex items-center gap-2 py-6 text-sm text-[var(--color-muted)]">
            <CircleNotch size={16} aria-hidden="true" className="animate-spin" />
            Loading view…
          </div>
        )}
        {viewResultQuery.isError && (
          <LibraryErrorBanner
            message={
              viewResultQuery.error instanceof Error
                ? viewResultQuery.error.message
                : 'Could not load this view.'
            }
            testId="knowledge-views-dialog-error"
          />
        )}
        {viewResult && (
          <ViewPartsRenderer
            result={viewResult}
            {...(resolveWikilink ? { resolveWikilink } : {})}
            linkHref={linkHref}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}
