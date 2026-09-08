// LibrarySearchBar — the ONE persistent Library search bar (founder decision,
// library-b-c-design-2026-09-07 §C1: a persistent bar that lives in the
// Library panel, NOT a ⌘K command palette — that option was considered and
// dropped; extended by unified-search-and-grep-spec.md US-1/US-2/US-4 to be
// this every workspace location's ONLY search input, MV-10). It renders:
//
//   1. A keyboard-reachable, debounced search input, always present — enabled
//      everywhere except the Library virtual root (US-4 AS-2) and while
//      collection detection for the current folder is still resolving.
//   2. INSIDE A VAULT: a segmented filter (All / Notes / Records / Views /
//      Attachments) with per-kind counts, plus the honesty surfaces the
//      retired KnowledgeSearch box carried (server statement, X-of-Y / "so
//      far" coverage, clamp disclosure, excerpt-unavailable markers — see
//      "Honest states" below).
//   3. IN A PLAIN FOLDER OR MOUNT (not inside a vault — unified-search-and-
//      grep-spec.md US-2): the FILES kind instead, over
//      POST .../library/{ws}/files/search — name hits always, content hits
//      under the engine's bounds, with an honest stopped-early banner when a
//      bound was hit. No filter tabs; one list, in the engine's own order.
//   4. Grouped results that REPLACE `children` (the file list) inline while a
//      query is present; clearing the query restores `children` exactly as
//      LibraryExplorer passed it in.
//
// Each vault result opens the right surface: a note or a record's note opens
// in the Library preview via `onOpenNote` (the same address-model path
// LibraryExplorer already wires KnowledgePanel's own onOpenNote through,
// translated from collection-relative to workspace-relative); a view opens as
// its evaluated result — GET .../knowledge/view needs only a collection_id
// and a view NAME (never a file path, which VaultSearchViewHit does not
// carry), so it is fetched and drawn with the same ViewPartsRenderer
// BasePreview uses, in a dialog, rather than requiring a `.base` file address
// that does not exist for this hit. A files-kind hit's path is ALREADY
// workspace-relative (no translation needed) and opens the same way.
//
// Honest states (mirrors useVaultSearch.ts / useFileSearch.ts): a failed
// request is a visible banner, never a silent empty list; a vault index that
// is not yet caught up sets `complete: false`, and the server's own
// `statement` (or the older `complete_reason`, for additive back-compat) is
// shown, in the reading flow, above the results, alongside an X-of-Y or
// "so far" coverage line that never invents a denominator (FR-036); a clamped
// result count says so (FR-037); a note hit whose excerpt could not be
// produced still renders, with an explicit marker, never dropped and never
// fabricated; a file-search walk that stopped early states the bound it hit.

import { useEffect, useId, useState } from 'react'
import type { ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  MagnifyingGlass,
  X,
  Warning,
  FileText,
  FileMagnifyingGlass,
  FolderSimple,
  IdentificationCard,
  SquaresFour,
  Paperclip,
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
  type VaultSearchAttachmentHit,
} from './useVaultSearch'
import {
  useFileSearch,
  type FileSearchFn,
  type FileSearchHit,
  type FileSearchResponse,
} from './useFileSearch'

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
  // unified-search-and-grep-spec.md US-1 (ported from the retired
  // KnowledgeSearch box's attachment-filename coverage): VaultSearchResponse.
  // attachments — absent (propindex-less builds, MV-9 carve-out) treated as [].
  { value: 'attachments', label: 'Attachments' },
]

/** Human sentences for the machine-readable reasons a file-search walk
 *  stopped early (unified-search-and-grep-spec.md MV-3 enum). Deliberately
 *  EXHAUSTIVE over the contract enum, matching KnowledgeSearch.tsx's
 *  EXCERPT_UNAVAILABLE_REASON precedent — adding a reason to
 *  FileSearchResponse.truncated_reason without a sentence here fails `tsc`
 *  instead of shipping a banner with a blank explanation. */
const FILE_SEARCH_TRUNCATED_REASON: Record<
  NonNullable<FileSearchResponse['truncated_reason']>,
  string
> = {
  max_files: 'Stopped early — too many files to search in one pass.',
  max_bytes: 'Stopped early — too much data to scan in one pass.',
  max_matches: 'Stopped early — reached the maximum number of matches.',
  max_depth: "Stopped early — this folder tree is deeper than the search follows.",
  deadline: 'Stopped early — the search ran out of time.',
  max_output: 'Stopped early — the results were too large to return in full.',
  root_lost:
    'Stopped early — the folder became unreadable while searching (it may have been moved, unmounted, or deleted).',
}

export interface LibrarySearchBarProps {
  /** null = the Library virtual root — the bar renders disabled, matching
   *  KnowledgePanel's own null handling (there is no folder to test). */
  workspaceId: string | null
  /** Workspace-relative folder currently browsed. */
  folderPath: string
  /** Opens a note (or a record's declaring note), or a plain-folder file
   *  search hit, in the Library preview. Receives a WORKSPACE-relative path —
   *  vault hits are collection-relative and translated here first; file hits
   *  are already workspace-relative (US-2/US-4). */
  onOpenNote: (workspacePath: string) => void
  /** Navigate INTO a matched folder. A directory hit (FileSearchHit.is_dir)
   *  addresses a container, so opening it as a file would select a directory
   *  in the preview pane instead of browsing it. */
  onOpenFolder?: (workspacePath: string) => void
  /** The file list to show while no query is active. Replaced entirely by
   *  grouped results while one is (library-b-c-design-2026-09-07 §C1). */
  children: ReactNode
  limit?: number
  debounceMs?: number
  /** Test seams; production uses the module defaults. */
  searchFn?: VaultSearchFn
  /** unified-search-and-grep-spec.md workstream B — the FILES-kind fetcher
   *  used in a plain folder/mount (not inside a vault). */
  searchFilesFn?: FileSearchFn
  loadCollectionInfo?: LoadCollectionInfoFn
  loadViewResult?: LoadViewResultFn
  className?: string
}

function countBadge(n: number, more = false) {
  return (
    <Badge
      variant="secondary"
      className="ml-1.5 px-1.5 py-0 text-[10px] leading-4"
    >
      {n}
      {more ? '+' : ''}
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
        {/* US-1 AS-4 (honesty port): a hit whose excerpt cannot be produced is
            still rendered — title and path, plus an explicit marker — never
            dropped, and never a fabricated excerpt. VaultSearchNoteHit reduces
            the retired KnowledgeSearchHit's 5-reason enum to one boolean
            (MV-9, recorded R2-MIN-010): the find path cannot attribute the old
            re-read reasons, so this says only what it knows. */}
        {hit.snippet !== undefined ? (
          <span className="text-xs leading-snug text-[var(--color-muted)]">{hit.snippet}</span>
        ) : hit.excerpt_unavailable === true ? (
          <span
            data-testid="vault-search-excerpt-unavailable"
            className="text-xs italic leading-snug text-[var(--color-muted)]"
          >
            No excerpt available for this note.
          </span>
        ) : null}
      </button>
    </li>
  )
}

function AttachmentRow({ hit, onOpen }: { hit: VaultSearchAttachmentHit; onOpen: () => void }) {
  return (
    <li>
      <button
        type="button"
        tabIndex={0}
        onClick={onOpen}
        data-testid="vault-search-attachment-hit"
        className="flex w-full flex-col items-start gap-0.5 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-[var(--color-surface-2)]"
      >
        <span className="flex items-center gap-1.5 text-sm text-[var(--color-secondary)]">
          <Paperclip size={13} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
          {hit.name}
        </span>
        <span className="text-[11px] text-[var(--color-muted)]">{hit.path}</span>
        {/* FR-039a: an attachment's contents are never read, by design — the
            match is on its filename alone, so there is never an excerpt to
            show and never a "could not be read" implication either. */}
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

/** One FileSearchHit — a NAME match (path only) or a CONTENT match (path,
 *  line number, excerpt, and up to 5 lines of optional context on each side —
 *  unified-search-and-grep-spec.md US-2 AS-1/AS-2, wire contract sketch §6).
 *  `onOpen` receives the hit's own workspace-relative path (FileSearchHit.path
 *  is already scoped to the workspace's confined Library root, unlike a vault
 *  hit's collection-relative path — no translation is needed here). */
function FileHitRow({ hit, onOpen }: { hit: FileSearchHit; onOpen: () => void }) {
  const isContent = hit.match_kind === 'content'
  const isDir = hit.is_dir === true
  return (
    <li>
      <button
        type="button"
        tabIndex={0}
        onClick={onOpen}
        data-testid={isContent ? 'file-search-content-hit' : 'file-search-name-hit'}
        className="flex w-full flex-col items-start gap-0.5 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-[var(--color-surface-2)]"
      >
        <span className="flex items-center gap-1.5 text-sm text-[var(--color-secondary)]">
          {isDir ? (
            <FolderSimple size={13} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
          ) : isContent ? (
            <FileMagnifyingGlass size={13} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
          ) : (
            <FileText size={13} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
          )}
          {hit.path}
          {isContent && hit.line !== undefined && (
            <span className="font-mono text-[10px] text-[var(--color-muted)]">:{hit.line}</span>
          )}
        </span>
        {isContent && (
          <div className="w-full font-mono text-xs leading-snug text-[var(--color-muted)]">
            {hit.context_before?.map((line, i) => (
              <p key={`before-${i}`} className="truncate opacity-60">
                {line}
              </p>
            ))}
            {hit.excerpt !== undefined && (
              <p className="truncate text-[var(--color-secondary)]">{hit.excerpt}</p>
            )}
            {hit.context_after?.map((line, i) => (
              <p key={`after-${i}`} className="truncate opacity-60">
                {line}
              </p>
            ))}
          </div>
        )}
      </button>
    </li>
  )
}

export function LibrarySearchBar({
  workspaceId,
  folderPath,
  onOpenNote,
  onOpenFolder,
  children,
  limit,
  debounceMs,
  searchFn,
  searchFilesFn,
  loadCollectionInfo,
  loadViewResult = fetchKnowledgeViewResult,
  className,
}: LibrarySearchBarProps) {
  const [text, setText] = useState('')
  const [filter, setFilter] = useState<VaultSearchKind>('all')
  const [openView, setOpenView] = useState<{ view: string; label: string } | null>(null)
  const inputId = useId()

  const {
    isActive: vaultIsActive,
    isBusy: vaultIsBusy,
    isResolvingCollection,
    collectionId,
    collectionRootPath,
    error: vaultError,
    response,
    counts,
    limit: effectiveLimit,
    coverage,
    clamp,
    notesCappedAtLimit,
  } = useVaultSearch({
    workspaceId,
    folderPath,
    query: text,
    ...(limit === undefined ? {} : { limit }),
    ...(debounceMs === undefined ? {} : { debounceMs }),
    ...(searchFn === undefined ? {} : { searchFn }),
    ...(loadCollectionInfo === undefined ? {} : { loadCollectionInfo }),
  })

  // ── Context switching (US-2/US-4) ─────────────────────────────────────────
  // workspaceId === null: the Library virtual root — no folder to search at
  // all (US-4 AS-2, the bar's existing disabled state). Otherwise, once
  // collection detection has resolved, a vault folder gets the knowledge
  // kinds above; anything else (a plain folder or a mount) gets the FILES
  // kind, which today's `disabled` used to fold into "not searchable" —
  // that was the exact gap US-2 exists to close.
  const isVaultMode = collectionId !== undefined
  const isFilesMode = workspaceId !== null && !isResolvingCollection && !isVaultMode

  const {
    isActive: filesIsActive,
    isBusy: filesIsBusy,
    error: filesError,
    response: filesResponse,
  } = useFileSearch({
    workspaceId,
    folderPath,
    query: text,
    enabled: isFilesMode,
    ...(debounceMs === undefined ? {} : { debounceMs }),
    ...(searchFilesFn === undefined ? {} : { searchFn: searchFilesFn }),
  })

  const isActive = isVaultMode ? vaultIsActive : filesIsActive
  const isBusy = isVaultMode ? vaultIsBusy : filesIsBusy
  const error = isVaultMode ? vaultError : filesError

  // Back to "All" whenever a fresh query starts — a filter chosen for a
  // previous query carrying over silently could hide every hit of a new one.
  useEffect(() => {
    if (!isActive) setFilter('all')
  }, [isActive])

  // Disabled ONLY at the virtual root or while detection is still resolving
  // which mode applies — NOT merely because this folder isn't a vault, which
  // used to be conflated with "nothing to search here" (the exact defect
  // US-2 exists to fix: a plain folder/mount now gets file search instead).
  const disabled = workspaceId === null || isResolvingCollection

  // A kind whose returned array fills the per-kind cap may have more matches
  // than shown; the badge renders "N+" so a plateaued count is never read as a
  // true total. "all" overflows if any single kind did. `notes` prefers the
  // server's own `notes_capped_at_limit` when the response states it
  // (honesty port, MV-9) over the length-based heuristic used for the kinds
  // the contract has no analogous flag for.
  const kindAtLimit = (kind: VaultSearchKind): boolean => {
    if (!response) return false
    if (kind === 'notes') return notesCappedAtLimit
    const attachments = response.attachments?.length ?? 0
    if (kind === 'all') {
      return (
        notesCappedAtLimit ||
        response.records.length >= effectiveLimit ||
        response.views.length >= effectiveLimit ||
        attachments >= effectiveLimit
      )
    }
    if (kind === 'attachments') return attachments >= effectiveLimit
    return response[kind].length >= effectiveLimit
  }
  const placeholder =
    workspaceId === null
      ? 'Open a workspace to search'
      : isResolvingCollection
        ? 'Checking this folder…'
        : isVaultMode
          ? 'Search notes, records, views'
          : 'Search files and folders'
  const ariaLabel = isVaultMode ? 'Search this knowledge base' : isFilesMode ? 'Search this folder' : 'Search'

  function openNote(path: string) {
    if (collectionRootPath === undefined) return
    onOpenNote(collectionPathToWorkspacePath(collectionRootPath, path))
  }

  // File-search hits are already workspace-relative (FileSearchHit.path is
  // scoped to the workspace's own confined Library root, unlike a vault
  // hit's collection-relative path) — no translation needed.
  function openFile(path: string) {
    onOpenNote(path)
  }

  const viewResultQuery = useQuery({
    queryKey: ['vault-search-view-result', workspaceId, collectionId, openView?.view],
    queryFn: ({ signal }) =>
      loadViewResult(workspaceId as string, collectionId as string, openView?.view as string, signal),
    enabled: openView !== null && workspaceId !== null && collectionId !== undefined,
    retry: false,
  })

  // ── Vault-mode honesty (US-1 honesty port) ────────────────────────────────
  const notReadyReason = response && !response.complete ? response.complete_reason : undefined
  const notReady = notReadyReason !== undefined || (response !== undefined && !response.complete)
  // The server's own sentence rides in `statement` going forward (mirrors the
  // retired KnowledgeSearchResponse's `incompleteness.statement`); the older
  // `complete_reason` field still wins when `statement` was not sent, so a
  // server that has not been upgraded to send it yet keeps working exactly as
  // before (additive-compatible, MV-9).
  const bannerStatement =
    response?.statement ?? notReadyReason ?? 'Partial results — the knowledge base index is not fully caught up yet.'
  const hasCompleteStatement = response?.complete === true && response.statement !== undefined

  return (
    <div data-testid="library-search-bar" className={cn('flex flex-col gap-2', className)}>
      <div className="relative">
        <MagnifyingGlass
          size={14}
          aria-hidden="true"
          className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-muted)]"
        />
        <label htmlFor={inputId} className="sr-only">
          {ariaLabel}
        </label>
        <Input
          id={inputId}
          type="search"
          value={text}
          disabled={disabled}
          onChange={(e) => setText(e.target.value)}
          placeholder={placeholder}
          aria-label={ariaLabel}
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

      {(!isActive || disabled) && children}

      {isActive && !disabled && (
        <div data-testid="library-search-active" className="flex flex-col gap-2">
          {error && (
            <LibraryErrorBanner message={error.message || 'Search failed.'} testId="library-search-error" />
          )}

          {!error && isVaultMode && (
            <Tabs value={filter} onValueChange={(v) => setFilter(v as VaultSearchKind)}>
              <TabsList data-testid="library-search-filters">
                {FILTER_TABS.map((tab) => (
                  <TabsTrigger key={tab.value} value={tab.value} data-testid={`library-search-filter-${tab.value}`}>
                    {tab.label}
                    {countBadge(counts[tab.value], kindAtLimit(tab.value))}
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

          {!error && isVaultMode && notReady && (
            <div
              role="status"
              data-testid="library-search-not-ready"
              className="flex items-start gap-2 rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 px-3 py-2"
            >
              <Warning size={14} weight="fill" aria-hidden="true" className="mt-0.5 shrink-0 text-[var(--color-warning)]" />
              <div className="flex-1 text-xs leading-snug text-[var(--color-warning)]">
                <p data-testid="library-search-statement">{bannerStatement}</p>
                {/* US-1 AS-2 honesty port (FR-036): a ratio is stated only
                    when BOTH a searched count and a known total are in hand;
                    otherwise a bare "so far" count — never an invented
                    denominator. */}
                {coverage === 'ratio' &&
                  typeof response?.notes_searched === 'number' &&
                  typeof response.notes_total_known === 'number' && (
                    <p data-testid="library-search-coverage-ratio">
                      {response.notes_searched.toLocaleString('en-US')} of{' '}
                      {response.notes_total_known.toLocaleString('en-US')} notes searched.
                    </p>
                  )}
                {coverage === 'so-far' && typeof response?.notes_searched === 'number' && (
                  <p data-testid="library-search-coverage-so-far">
                    {response.notes_searched.toLocaleString('en-US')} notes searched so far.
                  </p>
                )}
              </div>
            </div>
          )}

          {/* A complete answer carries a server-authored `statement` too — the
              only place it wins over the generic client sentences below (the
              not-ready banner above already shows it when the answer is
              partial). Mirrors the retired KnowledgeSearch's
              knowledge-search-complete-statement (US-1, honesty port). */}
          {!error && isVaultMode && hasCompleteStatement && response && (
            <p data-testid="library-search-complete-statement" className="text-xs leading-snug text-[var(--color-muted)]">
              {response.statement}
            </p>
          )}

          {!error && isVaultMode && clamp && (
            <div
              role="status"
              data-testid="library-search-clamped"
              className="flex items-start gap-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2"
            >
              <Warning size={14} aria-hidden="true" className="mt-0.5 shrink-0 text-[var(--color-muted)]" />
              <p className="flex-1 text-xs leading-snug text-[var(--color-muted)]">
                {clamp.requested === undefined
                  ? 'Result count clamped to the server’s maximum.'
                  : `Result count clamped to the server’s maximum — you asked for ${clamp.requested.toLocaleString('en-US')}.`}
              </p>
            </div>
          )}

          {/* The server's own statement, when it has one, is already shown
              above — by the not-ready banner while incomplete, or by
              library-search-complete-statement while complete — so this line
              never needs to repeat it. */}
          {!error && isVaultMode && response && counts.all === 0 && (
            <p role="status" data-testid="library-search-empty" className="text-xs leading-snug text-[var(--color-muted)]">
              No results for “{text.trim()}”.
            </p>
          )}

          {!error && isVaultMode && response && counts.all > 0 && (
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

              {(filter === 'all' || filter === 'attachments') && (response.attachments?.length ?? 0) > 0 && (
                <div>
                  {filter === 'all' && (
                    <p className="px-2 pb-1 text-[10px] font-medium uppercase tracking-wide text-[var(--color-muted)]">
                      Attachments
                    </p>
                  )}
                  <ul className="flex flex-col gap-1">
                    {(response.attachments ?? []).map((hit) => (
                      <AttachmentRow key={hit.path} hit={hit} onOpen={() => openNote(hit.path)} />
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

          {/* ── FILES kind (unified-search-and-grep-spec.md US-2/US-4) ──────
              A plain folder or mount, not inside a vault: name hits always;
              content hits under the engine's bounds. No filter tabs — one
              combined list, in the ENGINE's own relevance/path order
              (Ambiguity Audit A3: files are deterministic path-lexicographic)
              — never re-sorted client-side. */}
          {!error && !isVaultMode && filesResponse && (
            <>
              {filesResponse.truncated && filesResponse.truncated_reason !== undefined && (
                <div
                  role="status"
                  data-testid="library-search-truncated"
                  className="flex items-start gap-2 rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 px-3 py-2"
                >
                  <Warning
                    size={14}
                    weight="fill"
                    aria-hidden="true"
                    className="mt-0.5 shrink-0 text-[var(--color-warning)]"
                  />
                  <p className="flex-1 text-xs leading-snug text-[var(--color-warning)]">
                    {FILE_SEARCH_TRUNCATED_REASON[filesResponse.truncated_reason]}{' '}
                    {filesResponse.stats.files_visited.toLocaleString('en-US')} file
                    {filesResponse.stats.files_visited === 1 ? '' : 's'} searched.
                  </p>
                </div>
              )}

              {filesResponse.hits.length === 0 && (
                <p role="status" data-testid="library-search-empty" className="text-xs leading-snug text-[var(--color-muted)]">
                  No results for “{text.trim()}”.
                </p>
              )}

              {filesResponse.hits.length > 0 && (
                <ul data-testid="library-search-results" className="flex flex-col gap-1 overflow-y-auto">
                  {filesResponse.hits.map((hit, i) => (
                    <FileHitRow
                      key={`${hit.path}:${hit.line ?? 0}:${i}`}
                      hit={hit}
                      onOpen={() =>
                        hit.is_dir === true && onOpenFolder ? onOpenFolder(hit.path) : openFile(hit.path)
                      }
                    />
                  ))}
                </ul>
              )}
            </>
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
