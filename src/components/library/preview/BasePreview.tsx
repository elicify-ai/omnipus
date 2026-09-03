// BasePreview — a .base file opens as its views, not as a download and not
// as raw YAML (view-kinds-design-2026-09-03 §7; visual spec: the wireframe's
// "The accounting view" frame — view tabs, a filter strip, the part stack).
//
// WHERE THE DATA COMES FROM, and why there are three fetches:
//
//   1. The FILE — the .base itself names its views (`views: - name: …`).
//      Import is one-shot (FR-102): the file's views were translated into
//      saved views under `.omnipus-vault/views/`, and no endpoint lists which
//      saved views came from which file, so the file's own declarations are
//      the tab list, each addressed by the importer's slug (baseViewNames.ts).
//   2. The COLLECTION — the view-result endpoint speaks collection_id, which
//      only KnowledgeBaseInfo reports. The .base's ancestor folders are asked
//      (deepest first, the same walk KnowledgeNoteView documents) and the
//      nearest enclosing collection wins; nested collections stay correct.
//   3. The RESULT — GET /knowledge/view per selected tab, validated at the
//      edge by the generated zod schema like every other SPA fetch. Only the
//      selected view is fetched; a tab's count badge appears once its result
//      has been seen.
//
// Every non-happy state is a stated answer: not-a-collection says so in plain
// words, a refusal renders the server's reason, an empty view leads with the
// outcome and shows the filter it looked with (ViewPartsRenderer).

import { useEffect, useMemo, useState } from 'react'
import { useQueries, useQuery } from '@tanstack/react-query'
import { SpinnerGap } from '@phosphor-icons/react'

import { QueryErrorState } from '@/components/shared/QueryErrorState'
import {
  fetchKnowledgeBaseInfo,
  fetchKnowledgeViewResult,
  fetchLibraryContent,
  libraryDownloadUrl,
  libraryQueryKeys,
} from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'
import type { KnowledgeBaseInfo, ViewResult } from '@/lib/api/generated/openapi-types'

import { noteAncestorDirs } from '../knowledge/KnowledgeNoteView'
import { parseBaseViews } from './baseViewNames'
import type { BaseViewRef } from './baseViewNames'
import { ViewPartsRenderer } from './viewparts/ViewPartsRenderer'

/** Test seams; production passes nothing and gets the shared clients. */
export interface BasePreviewLoaders {
  loadContent?: (workspaceId: string, path: string) => Promise<{ content?: string; is_text: boolean; too_large: boolean }>
  loadInfo?: (workspaceId: string, path: string) => Promise<KnowledgeBaseInfo>
  loadViewResult?: (workspaceId: string, collectionId: string, view: string) => Promise<ViewResult>
}

export interface BasePreviewProps extends BasePreviewLoaders {
  workspaceId: string
  entry: LibraryEntry
}

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex flex-1 items-center justify-center gap-2 p-6 text-center text-xs text-[var(--color-muted)]">
      {children}
    </div>
  )
}

export function BasePreview({
  workspaceId,
  entry,
  loadContent = fetchLibraryContent,
  loadInfo = fetchKnowledgeBaseInfo,
  loadViewResult = fetchKnowledgeViewResult,
}: BasePreviewProps) {
  // ── 1. The .base file: which views exist, and their slugs ─────────────────
  const contentQuery = useQuery({
    queryKey: libraryQueryKeys.content(workspaceId, entry.path),
    queryFn: () => loadContent(workspaceId, entry.path),
    staleTime: 10_000,
  })

  const views: BaseViewRef[] = useMemo(() => {
    const c = contentQuery.data
    if (!c?.is_text || c.too_large || c.content === undefined) return []
    return parseBaseViews(c.content, entry.name)
  }, [contentQuery.data, entry.name])

  // Selected tab, by slug so it survives a content refetch; reset per file.
  const [selectedSlug, setSelectedSlug] = useState<string | undefined>(undefined)
  useEffect(() => setSelectedSlug(undefined), [entry.path])
  const selected = views.find((v) => v.slug === selectedSlug) ?? views[0]

  // ── 2. The enclosing collection, nearest ancestor first ───────────────────
  const ancestors = useMemo(() => noteAncestorDirs(entry.path), [entry.path])
  const ancestorQueries = useQueries({
    queries: ancestors.map((dir) => ({
      // The SAME key KnowledgePanel and KnowledgeNoteView use, so the walk is
      // usually already cached from browsing to this folder.
      queryKey: ['knowledge-base-info', workspaceId, dir],
      queryFn: () => loadInfo(workspaceId, dir),
      retry: false,
      refetchOnWindowFocus: false,
    })),
  })
  const { collectionId, collectionRoot, walkSettled } = useMemo(() => {
    for (let i = 0; i < ancestors.length; i++) {
      const info = ancestorQueries[i]?.data
      if (info?.is_knowledge_base && info.collection_id !== undefined) {
        return { collectionId: info.collection_id, collectionRoot: ancestors[i], walkSettled: true }
      }
    }
    const settled = ancestorQueries.every((q) => q.isSuccess || q.isError)
    return { collectionId: undefined, collectionRoot: undefined, walkSettled: settled }
  }, [ancestors, ancestorQueries])

  // ── 3. The selected view's evaluated result ───────────────────────────────
  const resultQuery = useQuery({
    queryKey: ['library', workspaceId, 'knowledge', 'view-result', collectionId, selected?.slug],
    queryFn: () => loadViewResult(workspaceId, collectionId as string, selected?.slug as string),
    enabled: collectionId !== undefined && selected !== undefined,
    staleTime: 10_000,
  })

  const resolveImageUrl = useMemo(
    () => (vaultPath: string) =>
      libraryDownloadUrl(
        workspaceId,
        collectionRoot === undefined || collectionRoot === '' ? vaultPath : `${collectionRoot}/${vaultPath}`,
      ),
    [workspaceId, collectionRoot],
  )

  // ── States before a result can render ─────────────────────────────────────
  // Every one renders inside the SAME `base-preview` container, so "the base
  // surface mounted" is one stable fact regardless of which state it is in.
  const stateBody = (() => {
    if (contentQuery.isLoading) {
      return (
        <Centered>
          <SpinnerGap size={16} className="animate-spin" /> Reading base file…
        </Centered>
      )
    }
    if (contentQuery.isError) {
      return (
        <QueryErrorState
          layout="fill"
          message="Could not read this base file."
          onRetry={() => void contentQuery.refetch()}
          testId="base-preview-content-error"
        />
      )
    }
    if (views.length === 0) {
      return (
        <Centered>
          <p data-testid="base-preview-no-views">
            This base file declares no views the preview can name, so there is nothing to draw.
          </p>
        </Centered>
      )
    }
    if (walkSettled && collectionId === undefined) {
      return (
        <Centered>
          <p data-testid="base-preview-no-collection">
            This file is not inside a knowledge base, so its views have nowhere to run. Views are
            served from the collection the base was imported into.
          </p>
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
      {/* View tabs — the wireframe's tablist; first view selected by default. */}
      <div
        role="tablist"
        aria-label="Views"
        className="flex shrink-0 gap-0.5 overflow-x-auto border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-1.5"
      >
        {views.map((v) => {
          const active = v.slug === selected?.slug
          return (
            <button
              key={v.slug}
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => setSelectedSlug(v.slug)}
              data-testid={`base-view-tab-${v.slug}`}
              className={`-mb-px whitespace-nowrap border-b-2 px-2.5 py-2 text-[13px] transition-colors ${
                active
                  ? 'border-[var(--color-accent)] text-[var(--color-secondary)]'
                  : 'border-transparent text-[var(--color-muted)] hover:text-[var(--color-secondary)]'
              }`}
            >
              {v.name}
              {active && result !== undefined && result.refusal === undefined && (
                <span className="ml-1.5 text-[10px] text-[var(--color-muted)]">{result.rows.length}</span>
              )}
            </button>
          )
        })}
      </div>

      {/* Body: the selected view's evaluated result. */}
      <div className="flex-1 overflow-auto bg-[var(--color-surface-0)]">
        {!walkSettled && collectionId === undefined ? (
          <Centered>
            <SpinnerGap size={16} className="animate-spin" /> Finding this base’s collection…
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
            filterText={selected?.filterText}
            resolveImageUrl={resolveImageUrl}
          />
        ) : null}
      </div>
    </div>
  )
}
