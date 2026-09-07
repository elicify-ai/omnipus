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
import { useQuery } from '@tanstack/react-query'
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
import type { KnowledgeBaseViews, ViewResult } from '@/lib/api/generated/openapi-types'

import { LibraryCodePreview } from './LibraryCodePreview'
import { ViewPartsRenderer } from './viewparts/ViewPartsRenderer'

/** Test seams; production passes nothing and gets the shared clients. */
export interface BasePreviewLoaders {
  loadContent?: (workspaceId: string, path: string) => Promise<{ content?: string; is_text: boolean; too_large: boolean }>
  loadBaseViews?: (workspaceId: string, path: string) => Promise<KnowledgeBaseViews>
  loadViewResult?: (workspaceId: string, collectionId: string, view: string) => Promise<ViewResult>
}

export interface BasePreviewProps extends BasePreviewLoaders {
  workspaceId: string
  entry: LibraryEntry
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
}: BasePreviewProps) {
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

  // Selected tab, by slug so it survives a refetch; reset per file.
  const [selectedSlug, setSelectedSlug] = useState<string | undefined>(undefined)
  useEffect(() => setSelectedSlug(undefined), [entry.path])
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
  const resultQuery = useQuery({
    queryKey: ['library', workspaceId, 'knowledge', 'view-result', collectionId, selected?.name],
    queryFn: () => loadViewResult(workspaceId, collectionId as string, selected?.name as string),
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
            <div className="flex h-full min-h-0 flex-col" data-testid="base-preview-raw">
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
      <div className="flex h-full min-h-0 flex-col" data-testid="base-preview">
        {stateBody}
      </div>
    )
  }

  const result = resultQuery.data

  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="base-preview">
      {answer !== undefined && answer.unloadable_count > 0 && (
        <UnloadableNotice count={answer.unloadable_count} />
      )}

      {/* View tabs — the wireframe's tablist; first view selected by default.
          `name` is the server's slug, used verbatim as both the React key and
          the fetch address, so two tabs can never share either. */}
      <div
        role="tablist"
        aria-label="Views"
        className="flex shrink-0 gap-0.5 overflow-x-auto border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-1.5"
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

      {/* Body: the selected view's evaluated result. */}
      <div className="flex-1 overflow-auto bg-[var(--color-surface-0)]">
        {resultQuery.isLoading ? (
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
          <ViewPartsRenderer result={result} resolveImageUrl={resolveImageUrl} />
        ) : null}
      </div>
    </div>
  )
}
