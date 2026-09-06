// LibrarySearchBar — the persistent Library search bar (founder decision,
// library-b-c-design-2026-09-07 §C1: a persistent bar that lives in the
// Library panel, NOT a ⌘K command palette — that option was considered and
// dropped). It renders:
//
//   1. A keyboard-reachable, debounced search input, always present.
//   2. A segmented filter (All / Notes / Records / Views) with per-kind
//      counts, shown once a query is active.
//   3. Grouped results that REPLACE `children` (the file list) inline while a
//      query is present; clearing the query restores `children` exactly as
//      LibraryExplorer passed it in.
//
// Each result opens the right surface: a note or a record's note opens in the
// Library preview via `onOpenNote` (the same address-model path
// LibraryExplorer already wires KnowledgePanel's own onOpenNote through); a
// view opens as its evaluated result — GET .../knowledge/view needs only a
// collection_id and a view NAME (never a file path, which VaultSearchViewHit
// does not carry), so it is fetched and drawn with the same ViewPartsRenderer
// BasePreview uses, in a dialog, rather than requiring a `.base` file address
// that does not exist for this hit.
//
// Honest states (mirrors useVaultSearch.ts / KnowledgeSearch.tsx): a failed
// request is a visible banner, never a silent empty list; an index that is
// not yet caught up sets `complete: false`, and the server's own
// `complete_reason` is shown, in the reading flow, above the results.

import { useEffect, useId, useState } from 'react'
import type { ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  MagnifyingGlass,
  X,
  Warning,
  FileText,
  IdentificationCard,
  SquaresFour,
  CircleNotch,
} from '@phosphor-icons/react'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import { cn } from '@/lib/utils'
import { fetchKnowledgeViewResult } from '@/lib/api'
import type { ViewResult } from '@/lib/api/generated/openapi-types'
import { ViewPartsRenderer } from '../preview/viewparts/ViewPartsRenderer'
import { collectionPathToWorkspacePath } from '../knowledge/KnowledgeBacklinks'
import { LibraryErrorBanner } from '../LibraryErrorBanner'
import {
  useVaultSearch,
  type LoadCollectionInfoFn,
  type VaultSearchFn,
  type VaultSearchKind,
  type VaultSearchNoteHit,
  type VaultSearchRecordHit,
  type VaultSearchViewHit,
} from './useVaultSearch'

/** Test seam for the view-result fetch; production uses the module default. */
export type LoadViewResultFn = (
  workspaceId: string,
  collectionId: string,
  view: string,
  signal?: AbortSignal,
) => Promise<ViewResult>

const FILTER_TABS: { value: VaultSearchKind; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'notes', label: 'Notes' },
  { value: 'records', label: 'Records' },
  { value: 'views', label: 'Views' },
]

export interface LibrarySearchBarProps {
  /** null = the Library virtual root — the bar renders disabled, matching
   *  KnowledgePanel's own null handling (there is no folder to test). */
  workspaceId: string | null
  /** Workspace-relative folder currently browsed. */
  folderPath: string
  /** Opens a note (or a record's declaring note) in the Library preview.
   *  Receives a WORKSPACE-relative path — hits are collection-relative, and
   *  the translation happens here, same convention as KnowledgePanel's
   *  onOpenNote. */
  onOpenNote: (workspacePath: string) => void
  /** The file list to show while no query is active. Replaced entirely by
   *  grouped results while one is (library-b-c-design-2026-09-07 §C1). */
  children: ReactNode
  limit?: number
  debounceMs?: number
  /** Test seams; production uses the module defaults. */
  searchFn?: VaultSearchFn
  loadCollectionInfo?: LoadCollectionInfoFn
  loadViewResult?: LoadViewResultFn
  className?: string
}

function countBadge(n: number) {
  return (
    <Badge
      variant="secondary"
      className="ml-1.5 px-1.5 py-0 text-[10px] leading-4"
    >
      {n}
    </Badge>
  )
}

function NoteRow({ hit, onOpen }: { hit: VaultSearchNoteHit; onOpen: () => void }) {
  return (
    <li>
      <button
        type="button"
        tabIndex={0}
        onClick={onOpen}
        data-testid="vault-search-note-hit"
        className="flex w-full flex-col items-start gap-0.5 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-[var(--color-surface-2)]"
      >
        <span className="flex items-center gap-1.5 text-sm text-[var(--color-secondary)]">
          <FileText size={13} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
          {hit.title || hit.path}
        </span>
        <span className="text-[11px] text-[var(--color-muted)]">{hit.path}</span>
        {hit.snippet !== undefined && (
          <span className="text-xs leading-snug text-[var(--color-muted)]">{hit.snippet}</span>
        )}
      </button>
    </li>
  )
}

function RecordRow({ hit, onOpen }: { hit: VaultSearchRecordHit; onOpen: () => void }) {
  return (
    <li>
      <button
        type="button"
        tabIndex={0}
        onClick={onOpen}
        data-testid="vault-search-record-hit"
        className="flex w-full flex-col items-start gap-1 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-[var(--color-surface-2)]"
      >
        <span className="flex items-center gap-1.5 text-sm text-[var(--color-secondary)]">
          <IdentificationCard size={13} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
          {hit.title || hit.path}
          {hit.record_type !== undefined && (
            <Badge variant="outline" className="px-1.5 py-0 text-[10px] leading-4">
              {hit.record_type}
            </Badge>
          )}
          {hit.id !== undefined && (
            <span className="font-mono text-[10px] text-[var(--color-muted)]">{hit.id}</span>
          )}
        </span>
        <span className="text-[11px] text-[var(--color-muted)]">{hit.path}</span>
        {hit.cells.length > 0 && (
          <span className="flex flex-wrap gap-x-3 gap-y-0.5 text-xs leading-snug text-[var(--color-muted)]">
            {hit.cells.slice(0, 4).map((cell) => (
              <span key={cell.property}>
                <span className="text-[var(--color-muted)]/70">{cell.property}:</span> {cell.value}
              </span>
            ))}
          </span>
        )}
      </button>
    </li>
  )
}

function ViewRow({ hit, onOpen }: { hit: VaultSearchViewHit; onOpen: () => void }) {
  return (
    <li>
      <button
        type="button"
        tabIndex={0}
        onClick={onOpen}
        data-testid="vault-search-view-hit"
        className="flex w-full items-center gap-1.5 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-[var(--color-surface-2)]"
      >
        <SquaresFour size={13} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
        <span className="flex-1 text-sm text-[var(--color-secondary)]">{hit.label}</span>
        {hit.type !== undefined && (
          <Badge variant="outline" className="px-1.5 py-0 text-[10px] leading-4">
            {hit.type}
          </Badge>
        )}
        {hit.kind !== undefined && (
          <Badge variant="muted" className="px-1.5 py-0 text-[10px] leading-4">
            {hit.kind}
          </Badge>
        )}
      </button>
    </li>
  )
}

export function LibrarySearchBar({
  workspaceId,
  folderPath,
  onOpenNote,
  children,
  limit,
  debounceMs,
  searchFn,
  loadCollectionInfo,
  loadViewResult = fetchKnowledgeViewResult,
  className,
}: LibrarySearchBarProps) {
  const [text, setText] = useState('')
  const [filter, setFilter] = useState<VaultSearchKind>('all')
  const [openView, setOpenView] = useState<{ view: string; label: string } | null>(null)
  const inputId = useId()

  const {
    isActive,
    isBusy,
    isResolvingCollection,
    collectionId,
    collectionRootPath,
    error,
    response,
    counts,
  } = useVaultSearch({
    workspaceId,
    folderPath,
    query: text,
    ...(limit === undefined ? {} : { limit }),
    ...(debounceMs === undefined ? {} : { debounceMs }),
    ...(searchFn === undefined ? {} : { searchFn }),
    ...(loadCollectionInfo === undefined ? {} : { loadCollectionInfo }),
  })

  // Back to "All" whenever a fresh query starts — a filter chosen for a
  // previous query carrying over silently could hide every hit of a new one.
  useEffect(() => {
    if (!isActive) setFilter('all')
  }, [isActive])

  const disabled = workspaceId === null || isResolvingCollection || collectionId === undefined
  const placeholder =
    workspaceId === null
      ? 'Search notes, records, views'
      : isResolvingCollection
        ? 'Checking this folder…'
        : collectionId === undefined
          ? 'Search available inside a vault'
          : 'Search notes, records, views'

  function openNote(path: string) {
    if (collectionRootPath === undefined) return
    onOpenNote(collectionPathToWorkspacePath(collectionRootPath, path))
  }

  const viewResultQuery = useQuery({
    queryKey: ['vault-search-view-result', workspaceId, collectionId, openView?.view],
    queryFn: ({ signal }) =>
      loadViewResult(workspaceId as string, collectionId as string, openView?.view as string, signal),
    enabled: openView !== null && workspaceId !== null && collectionId !== undefined,
    retry: false,
  })

  const notReadyReason = response && !response.complete ? response.complete_reason : undefined
  const notReady = notReadyReason !== undefined || (response !== undefined && !response.complete)

  return (
    <div data-testid="library-search-bar" className={cn('flex flex-col gap-2', className)}>
      <div className="relative">
        <MagnifyingGlass
          size={14}
          aria-hidden="true"
          className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-muted)]"
        />
        <label htmlFor={inputId} className="sr-only">
          Search this vault
        </label>
        <Input
          id={inputId}
          type="search"
          value={text}
          disabled={disabled}
          onChange={(e) => setText(e.target.value)}
          placeholder={placeholder}
          aria-label="Search this vault"
          data-testid="library-search-input"
          className="pl-8 pr-8"
        />
        {text !== '' && (
          <button
            type="button"
            tabIndex={0}
            onClick={() => setText('')}
            aria-label="Clear search"
            data-testid="library-search-clear"
            className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-0.5 text-[var(--color-muted)] transition-colors hover:text-[var(--color-secondary)]"
          >
            <X size={14} aria-hidden="true" />
          </button>
        )}
      </div>

      {!isActive && children}

      {isActive && (
        <div data-testid="library-search-active" className="flex flex-col gap-2">
          {error && (
            <LibraryErrorBanner message={error.message || 'Search failed.'} testId="library-search-error" />
          )}

          {!error && (
            <Tabs value={filter} onValueChange={(v) => setFilter(v as VaultSearchKind)}>
              <TabsList data-testid="library-search-filters">
                {FILTER_TABS.map((tab) => (
                  <TabsTrigger key={tab.value} value={tab.value} data-testid={`library-search-filter-${tab.value}`}>
                    {tab.label}
                    {countBadge(counts[tab.value])}
                  </TabsTrigger>
                ))}
              </TabsList>
            </Tabs>
          )}

          <div role="status" aria-live="polite" className="flex flex-col gap-1 empty:hidden">
            {isBusy && (
              <p data-testid="library-search-busy" className="text-xs text-[var(--color-muted)]">
                Searching…
              </p>
            )}
          </div>

          {!error && notReady && (
            <div
              role="status"
              data-testid="library-search-not-ready"
              className="flex items-start gap-2 rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 px-3 py-2"
            >
              <Warning size={14} weight="fill" aria-hidden="true" className="mt-0.5 shrink-0 text-[var(--color-warning)]" />
              <p className="flex-1 text-xs leading-snug text-[var(--color-warning)]">
                {notReadyReason ??
                  'Partial results — the vault index is not fully caught up yet.'}
              </p>
            </div>
          )}

          {!error && response && counts.all === 0 && (
            <p role="status" data-testid="library-search-empty" className="text-xs leading-snug text-[var(--color-muted)]">
              No results for “{text.trim()}”.
            </p>
          )}

          {!error && response && counts.all > 0 && (
            <div data-testid="library-search-results" className="flex flex-col gap-3 overflow-y-auto">
              {(filter === 'all' || filter === 'notes') && response.notes.length > 0 && (
                <div>
                  {filter === 'all' && (
                    <p className="px-2 pb-1 text-[10px] font-medium uppercase tracking-wide text-[var(--color-muted)]">
                      Notes
                    </p>
                  )}
                  <ul className="flex flex-col gap-1">
                    {response.notes.map((hit) => (
                      <NoteRow key={hit.path} hit={hit} onOpen={() => openNote(hit.path)} />
                    ))}
                  </ul>
                </div>
              )}

              {(filter === 'all' || filter === 'records') && response.records.length > 0 && (
                <div>
                  {filter === 'all' && (
                    <p className="px-2 pb-1 text-[10px] font-medium uppercase tracking-wide text-[var(--color-muted)]">
                      Records
                    </p>
                  )}
                  <ul className="flex flex-col gap-1">
                    {response.records.map((hit) => (
                      <RecordRow key={hit.path} hit={hit} onOpen={() => openNote(hit.path)} />
                    ))}
                  </ul>
                </div>
              )}

              {(filter === 'all' || filter === 'views') && response.views.length > 0 && (
                <div>
                  {filter === 'all' && (
                    <p className="px-2 pb-1 text-[10px] font-medium uppercase tracking-wide text-[var(--color-muted)]">
                      Views
                    </p>
                  )}
                  <ul className="flex flex-col gap-1">
                    {response.views.map((hit) => (
                      <ViewRow
                        key={hit.view}
                        hit={hit}
                        onOpen={() => setOpenView({ view: hit.view, label: hit.label })}
                      />
                    ))}
                  </ul>
                </div>
              )}

              {/* A non-"all" filter with zero hits for the KIND it names, while
                  other kinds still have hits (counts.all > 0) — say so rather
                  than rendering a silently empty panel. */}
              {filter !== 'all' && counts[filter] === 0 && (
                <p data-testid="library-search-filter-empty" className="px-2 text-xs text-[var(--color-muted)]">
                  No {filter} match “{text.trim()}”.
                </p>
              )}
            </div>
          )}
        </div>
      )}

      <Dialog open={openView !== null} onOpenChange={(open) => !open && setOpenView(null)}>
        <DialogContent className="max-w-3xl" data-testid="library-search-view-dialog">
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
              testId="library-search-view-error"
            />
          )}
          {viewResultQuery.data && <ViewPartsRenderer result={viewResultQuery.data} />}
        </DialogContent>
      </Dialog>
    </div>
  )
}
