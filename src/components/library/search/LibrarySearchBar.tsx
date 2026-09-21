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

import { useEffect, useId, useMemo, useState } from 'react'
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
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
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
// Codex review #12: the saved-view dialog resolves a relation cell's
// `[[wikilink]]` against the COLLECTION, with the same evidence ladder and
// the same ONE multi-path request the base preview uses — never a second,
// divergent resolution path, and never the raw wikilink text as a path.
import {
  collectionLinkRowPaths,
  defaultKnowledgeGraphLoader,
  makeCollectionLinkResolver,
  useCollectionLinkGraph,
} from '../preview/useCollectionLinkGraph'
// WL-2: the note reader's own wikilink stripper, imported rather than
// reimplemented. A search hit's snippet and a record's cell value are RAW
// byte excerpts of file content — frontmatter included — so a match falling
// inside `owner: "[[Daniel Piatkowski]]"` arrives here with the brackets
// still on it. `stripWikilinkNotation`'s own doc carries the full rationale,
// including why this renders plain text and never a link. It lives in
// wikilinkNotation.ts because the `query` fence inside a note shows excerpts
// from the same engine's same field and needs the identical fix.
import { stripWikilinkNotation } from '../preview/wikilinkNotation'
// UAT D-129: one accent-folding rule for every client-side comparison, so
// the bar never disagrees with the engine about which words a hit matched.
import { foldForMatch, foldedMatchRanges } from './foldForMatch'
import { collectionPathToWorkspacePath, libraryNoteHref } from '../knowledge/KnowledgeBacklinks'
import type { KnowledgeGraphLoader } from '../knowledge/KnowledgeBacklinks'
import { LibraryErrorBanner } from '../LibraryErrorBanner'
import {
  useVaultSearch,
  VAULT_SEARCH_QUERY_MAX_CHARS,
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
  canceled: 'Search canceled.',
  max_output: 'Stopped early — the results were too large to return in full.',
  root_lost:
    'Stopped early — the folder became unreadable while searching (it may have been moved, unmounted, or deleted).',
}

/** Finding S3: FileSearchResponse.truncated_root names WHICH root went
 *  unreadable — the empty string means the workspace's own work tree (a
 *  legitimate, meaningful value, never "absent"; per its own schema comment
 *  the other roots are named mounts), so this must branch on `!== undefined`
 *  wherever it is read, never on truthiness. Rendering this is the only way
 *  a root_lost stop tells the reader WHICH mount died instead of just that
 *  one did. */
function truncatedRootLabel(root: string): string {
  return root === '' ? 'the workspace folder' : `the “${root}” mount`
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
   *  in the preview pane instead of browsing it.
   *
   *  Returns whether navigation actually happened — synchronously, or as a
   *  Promise (confirmDiscardLibraryEdits() is async: the discard-unsaved-
   *  edits dialog it opens can't return an answer synchronously). Finding
   *  S1: the caller (LibraryExplorer) gates real navigation behind
   *  confirmDiscardLibraryEdits(), which the user can decline (an unsaved-
   *  edits "Cancel"). A caller that declined must report `false` so this bar
   *  knows NOT to clear the query and its results — a user who cancelled to
   *  avoid losing unsaved work must not lose their search instead. */
  onOpenFolder?: (workspacePath: string) => boolean | Promise<boolean>
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
  /** Codex #12: the link-graph client behind the saved-view dialog's
   *  collection-aware cell resolver. Same seam BasePreview exposes. */
  loadGraph?: KnowledgeGraphLoader
  className?: string
}

function countBadge(n: number, more = false) {
  return (
    <Badge
      variant="secondary"
      className="ml-[var(--space-1)] px-[var(--space-1)] py-0 text-[length:var(--type-caption-size)] leading-4"
    >
      {n}
      {more ? '+' : ''}
    </Badge>
  )
}

/** "3 notes" / "1 record" — the per-kind phrase of the D-129/D-130 results
 *  summary, pluralised so a sentence a reader trusts never reads "1 records". */
function kindCountPhrase(n: number, kind: 'notes' | 'records' | 'views' | 'attachments'): string {
  const singular =
    kind === 'notes' ? 'note' : kind === 'records' ? 'record' : kind === 'views' ? 'view' : 'attachment'
  return `${n.toLocaleString('en-US')} ${n === 1 ? singular : kind}`
}

/** KB-6 coverage chips: which of the query's own words this hit actually
 *  contains — the "why did this appear" explanation AND the relevance
 *  signal in one mechanism (ratified design, 2026-09-08). Only shown for a
 *  2+-word query; a single word has nothing to "cover" differently from
 *  simply matching or not.
 *
 *  APPROXIMATION, disclosed rather than hidden: VaultSearchNoteHit carries
 *  title, path and a SNIPPET (an excerpt around the first matched term) —
 *  not the full note body — so a word present elsewhere in the note but
 *  outside the snippet window reads here as "not covered" even though the
 *  engine's own ranking already credited it. This is the same class of
 *  tradeoff KB-6d's client-side highlighting already accepts (cheap, no
 *  wire change, works today) rather than waiting on a contract change to
 *  carry true per-term coverage from the engine. Explicitly NOT a raw BM25
 *  number — KB-6/index.go's own stated rule — ordering plus which words
 *  matched is honest; a score is not. */
function vaultCoverage(query: string, hit: VaultSearchNoteHit): { term: string; found: boolean }[] {
  const words = Array.from(new Set(query.split(/\s+/).filter((w) => w.length > 0)))
  if (words.length < 2) return []
  // UAT D-129: one folding rule (foldForMatch) — `cafe` covers "Café", and
  // an NFC and an NFD spelling of the same word cover each other.
  const haystack = foldForMatch(`${hit.title ?? ''} ${hit.path} ${hit.snippet ?? ''}`)
  return words.map((term) => ({ term, found: haystack.includes(foldForMatch(term)) }))
}

function CoverageChips({ coverage }: { coverage: { term: string; found: boolean }[] }) {
  if (coverage.length === 0) return null
  const foundCount = coverage.filter((c) => c.found).length
  return (
    <span data-testid="vault-search-coverage" className="flex flex-wrap items-center gap-[var(--space-1)]">
      {coverage.map((c) => (
        <span
          key={c.term}
          data-testid={c.found ? 'vault-search-coverage-hit' : 'vault-search-coverage-miss'}
          className={cn(
            'rounded-sm px-[var(--space-1)] py-0 text-[length:var(--type-caption-size)] leading-4',
            c.found
              ? 'bg-[var(--color-accent)]/15 text-[var(--color-accent)]'
              : 'text-[var(--color-muted)] line-through opacity-60',
          )}
        >
          {c.term}
        </span>
      ))}
      <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        {foundCount} of {coverage.length}
      </span>
    </span>
  )
}

function NoteRow({ hit, query, onOpen }: { hit: VaultSearchNoteHit; query: string; onOpen: () => void }) {
  const coverage = vaultCoverage(query, hit)
  return (
    <li>
      <Button
        variant="ghost"
        onClick={onOpen}
        data-testid="vault-search-note-hit"
        className="h-auto w-full flex-col items-start gap-[var(--space-0-5)] whitespace-normal rounded-md px-[var(--space-2)] py-[var(--space-1)] text-left font-[var(--font-weight-regular)] hover:bg-[var(--color-surface-2)]"
      >
        <span className="flex items-center gap-[var(--space-1)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
          <FileText size={13} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
          {highlightQuery(hit.title || hit.path, query)}
        </span>
        <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{hit.path}</span>
        <CoverageChips coverage={coverage} />
        {/* US-1 AS-4 (honesty port): a hit whose excerpt cannot be produced is
            still rendered — title and path, plus an explicit marker — never
            dropped, and never a fabricated excerpt. VaultSearchNoteHit reduces
            the retired KnowledgeSearchHit's 5-reason enum to one boolean
            (MV-9, recorded R2-MIN-010): the find path cannot attribute the old
            re-read reasons, so this says only what it knows. */}
        {hit.snippet !== undefined ? (
          <span className="text-[length:var(--type-utility-xs-size)] leading-snug text-[var(--color-muted)]">
            {highlightQuery(stripWikilinkNotation(hit.snippet), query)}
          </span>
        ) : hit.excerpt_unavailable === true ? (
          <span
            data-testid="vault-search-excerpt-unavailable"
            className="text-[length:var(--type-utility-xs-size)] italic leading-snug text-[var(--color-muted)]"
          >
            No excerpt available for this note.
          </span>
        ) : null}
      </Button>
    </li>
  )
}

function AttachmentRow({ hit, onOpen }: { hit: VaultSearchAttachmentHit; onOpen: () => void }) {
  return (
    <li>
      <Button
        variant="ghost"
        onClick={onOpen}
        data-testid="vault-search-attachment-hit"
        className="h-auto w-full flex-col items-start gap-[var(--space-0-5)] whitespace-normal rounded-md px-[var(--space-2)] py-[var(--space-1)] text-left font-[var(--font-weight-regular)] hover:bg-[var(--color-surface-2)]"
      >
        <span className="flex items-center gap-[var(--space-1)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
          <Paperclip size={13} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
          {hit.name}
        </span>
        <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{hit.path}</span>
        {/* FR-039a: an attachment's contents are never read, by design — the
            match is on its filename alone, so there is never an excerpt to
            show and never a "could not be read" implication either. */}
      </Button>
    </li>
  )
}

/** Finding S2: VaultFindCell carries no "this is what matched" flag on the
 *  wire (checked against VaultFindCell.yaml before writing this — property
 *  and value only), so which of a record's cells explain the hit can only be
 *  APPROXIMATED client-side — the same disclosed tradeoff vaultCoverage/
 *  highlightQuery above already accept for notes. A cell whose property or
 *  value contains one of the query's words is sorted first (stable within
 *  each group) so that when cells are withheld below, the ones most likely
 *  to justify the match are the ones actually shown. */
function orderCellsForDisplay(
  cells: VaultSearchRecordHit['cells'],
  query: string,
): VaultSearchRecordHit['cells'] {
  const words = query
    .split(/\s+/)
    .filter((w) => w.length > 0)
    .map(foldForMatch)
  if (words.length === 0) return cells
  // UAT D-129: folded on both sides, so `zurich` credits a "Zürich" cell.
  const isMatch = (cell: VaultSearchRecordHit['cells'][number]) =>
    words.some((w) => foldForMatch(cell.value).includes(w) || foldForMatch(cell.property).includes(w))
  const matched = cells.filter(isMatch)
  const unmatched = cells.filter((c) => !isMatch(c))
  return [...matched, ...unmatched]
}

function RecordRow({ hit, query, onOpen }: { hit: VaultSearchRecordHit; query: string; onOpen: () => void }) {
  const orderedCells = orderCellsForDisplay(hit.cells, query)
  // UAT D-137: the "+N more" affordance looked expandable but sat inside
  // the row's open button, so clicking it opened the note. It is now its
  // own control, a SIBLING of the open button (a button may not nest one),
  // that reveals the withheld cells in place.
  const [expanded, setExpanded] = useState(false)
  const shownCells = expanded ? orderedCells : orderedCells.slice(0, 4)
  // Finding S2: silently slicing to 4 left a 7-property record showing four
  // cells NONE of which contain the search term, with nothing saying a cell
  // was withheld — a real match that reads as a non-match. Say how many were
  // left out.
  const withheldCount = hit.cells.length - shownCells.length
  return (
    <li className="flex flex-col">
      <Button
        variant="ghost"
        onClick={onOpen}
        data-testid="vault-search-record-hit"
        className="h-auto w-full flex-col items-start gap-[var(--space-1)] whitespace-normal rounded-md px-[var(--space-2)] py-[var(--space-1)] text-left font-[var(--font-weight-regular)] hover:bg-[var(--color-surface-2)]"
      >
        <span className="flex items-center gap-[var(--space-1)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
          <IdentificationCard size={13} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
          {hit.title || hit.path}
          {hit.record_type !== undefined && (
            <Badge variant="outline" className="px-[var(--space-1)] py-0 text-[length:var(--type-caption-size)] leading-4">
              {hit.record_type}
            </Badge>
          )}
          {hit.id !== undefined && (
            <span className="font-mono text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{hit.id}</span>
          )}
        </span>
        <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{hit.path}</span>
        {hit.cells.length > 0 && (
          <span className="flex flex-wrap items-center gap-x-[var(--space-2-5)] gap-y-[var(--space-0-5)] text-[length:var(--type-utility-xs-size)] leading-snug text-[var(--color-muted)]">
            {shownCells.map((cell) => (
              <span key={cell.property}>
                <span className="text-[var(--color-muted)]/70">{cell.property}:</span>{' '}
                {stripWikilinkNotation(cell.value)}
              </span>
            ))}
          </span>
        )}
      </Button>
      {(withheldCount > 0 || expanded) && (
        <Button
          variant="ghost"
          onClick={() => setExpanded((v) => !v)}
          aria-expanded={expanded}
          data-testid="vault-search-record-cells-more"
          className="h-auto self-start rounded px-[var(--space-2)] pb-[var(--space-1)] pt-0 text-[length:var(--type-caption-size)] leading-4 font-[var(--font-weight-regular)] text-[var(--color-muted)] underline-offset-2 hover:bg-transparent hover:underline"
        >
          {expanded ? 'Show fewer properties' : `+${withheldCount} more ${withheldCount === 1 ? 'property' : 'properties'}`}
        </Button>
      )}
    </li>
  )
}

function ViewRow({ hit, onOpen }: { hit: VaultSearchViewHit; onOpen: () => void }) {
  return (
    <li>
      <Button
        variant="ghost"
        onClick={onOpen}
        data-testid="vault-search-view-hit"
        className="h-auto w-full items-center gap-[var(--space-1)] whitespace-normal rounded-md px-[var(--space-2)] py-[var(--space-1)] text-left font-[var(--font-weight-regular)] hover:bg-[var(--color-surface-2)]"
      >
        <SquaresFour size={13} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
        <span className="flex-1 text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">{hit.label}</span>
        {hit.type !== undefined && (
          <Badge variant="outline" className="px-[var(--space-1)] py-0 text-[length:var(--type-caption-size)] leading-4">
            {hit.type}
          </Badge>
        )}
        {hit.kind !== undefined && (
          <Badge variant="muted" className="px-[var(--space-1)] py-0 text-[length:var(--type-caption-size)] leading-4">
            {hit.kind}
          </Badge>
        )}
      </Button>
    </li>
  )
}

/** KB-6d: client-side match highlighting. No match offsets exist on the
 *  wire, so this is a query-aware, case-insensitive substring split —
 *  approximate under smart/insensitive case and blind to which OCCURRENCE
 *  the engine actually matched (the ratified design's own accepted
 *  tradeoff: cheap and works for both search kinds today, versus a
 *  contract change to carry exact offsets). Colour is the Forge Gold
 *  accent (`--color-accent`), not yellow — the founder asked for yellow,
 *  but yellow reads as "warning" elsewhere in this palette; flagged in the
 *  defect writeup as reversible. */
function highlightQuery(text: string, query: string): ReactNode {
  const words = query.split(/\s+/).filter((w) => w.length > 0)
  if (words.length === 0) return text
  // UAT D-129: the match is found on the accent-folded text and the
  // highlight is sliced from the ORIGINAL, so `cafe` lights up "Café" as
  // the reader sees it, accent and all.
  const ranges = foldedMatchRanges(text, words)
  if (ranges.length === 0) return text
  const out: ReactNode[] = []
  let cursor = 0
  ranges.forEach(([start, end], i) => {
    if (start > cursor) out.push(text.slice(cursor, start))
    out.push(
      <span key={i} className="rounded-sm bg-[var(--color-accent)]/25 text-[var(--color-accent)]">
        {text.slice(start, end)}
      </span>,
    )
    cursor = end
  })
  if (cursor < text.length) out.push(text.slice(cursor))
  return out
}

/** One FileSearchHit — a NAME match (path only) or a CONTENT match (path,
 *  line number, excerpt, and up to 5 lines of optional context on each side —
 *  unified-search-and-grep-spec.md US-2 AS-1/AS-2, wire contract sketch §6).
 *  `onOpen` receives the hit's own workspace-relative path (FileSearchHit.path
 *  is already scoped to the workspace's confined Library root, unlike a vault
 *  hit's collection-relative path — no translation is needed here). `query`
 *  drives KB-6d's client-side highlight. */
function FileHitRow({
  hit,
  query,
  onOpen,
  interactive = true,
}: {
  hit: FileSearchHit
  query: string
  onOpen: () => void
  /** Finding R-2: false makes the row visibly inert (no click handler, no
   *  hover affordance) — used for a directory hit when the caller wired no
   *  onOpenFolder. FileHitRow cannot infer this from `onOpen` alone (the
   *  parent always passes SOME function); the parent computes it because it
   *  is the one holding onOpenFolder. */
  interactive?: boolean
}) {
  const isContent = hit.match_kind === 'content'
  const isDir = hit.is_dir === true
  // KB-7b/KB-6a: match_count is present only on a match_all_words
  // collapsed hit — one row now stands in for every matching line in the
  // file, so the count IS the "why did this file surface" and "how much is
  // here" signal a reader used to get for free from seeing 30 separate rows.
  const matchCount = hit.match_count
  return (
    <li>
      <Button
        variant="ghost"
        tabIndex={interactive ? 0 : -1}
        onClick={interactive ? onOpen : undefined}
        disabled={!interactive}
        aria-disabled={!interactive || undefined}
        data-testid={isContent ? 'file-search-content-hit' : 'file-search-name-hit'}
        className={cn(
          'h-auto w-full flex-col items-start gap-[var(--space-0-5)] whitespace-normal rounded-md px-[var(--space-2)] py-[var(--space-1)] text-left font-[var(--font-weight-regular)] disabled:opacity-70',
          interactive ? 'hover:bg-[var(--color-surface-2)]' : 'cursor-default hover:bg-transparent',
        )}
      >
        <span className="flex items-center gap-[var(--space-1)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
          {isDir ? (
            <FolderSimple size={13} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
          ) : isContent ? (
            <FileMagnifyingGlass size={13} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
          ) : (
            <FileText size={13} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
          )}
          {highlightQuery(hit.path, query)}
          {isContent && hit.line !== undefined && (
            <span className="font-mono text-[length:var(--type-caption-size)] text-[var(--color-muted)]">:{hit.line}</span>
          )}
          {matchCount !== undefined && matchCount > 1 && (
            <Badge
              variant="secondary"
              data-testid="file-search-match-count"
              className="px-[var(--space-1)] py-0 text-[length:var(--type-caption-size)] leading-4"
            >
              {matchCount} matches
            </Badge>
          )}
        </span>
        {isContent && (
          <div className="w-full font-mono text-[length:var(--type-utility-xs-size)] leading-snug text-[var(--color-muted)]">
            {hit.context_before?.map((line, i) => (
              <p key={`before-${i}`} className="truncate opacity-60">
                {highlightQuery(line, query)}
              </p>
            ))}
            {hit.excerpt !== undefined && (
              <p className="truncate text-[var(--color-secondary)]">{highlightQuery(hit.excerpt, query)}</p>
            )}
            {hit.context_after?.map((line, i) => (
              <p key={`after-${i}`} className="truncate opacity-60">
                {highlightQuery(line, query)}
              </p>
            ))}
          </div>
        )}
      </Button>
    </li>
  )
}

/** Finding F-K: the walk-accounting stats were almost entirely discarded —
 *  only files_visited rendered, and only inside the truncation banner, so a
 *  file pruned by .gitignore, filtered by a glob, or unreadable had no way
 *  to reach the reader at all. This renders every NONZERO skip reason as a
 *  compact, always-available line (not gated on truncation) — the sharpest
 *  case named in the finding is a file visible in the listing, invisible to
 *  search, with the search reporting "No results" and nothing else. */
function FileSearchStatsFooter({ stats }: { stats: FileSearchResponse['stats'] }) {
  const parts: string[] = []
  if (stats.files_pruned_ignored > 0) {
    parts.push(`${stats.files_pruned_ignored.toLocaleString('en-US')} skipped by .gitignore/.ignore`)
  }
  if (stats.files_filtered_glob !== undefined && stats.files_filtered_glob > 0) {
    parts.push(`${stats.files_filtered_glob.toLocaleString('en-US')} excluded by filters`)
  }
  if (stats.files_skipped_problems > 0) {
    parts.push(`${stats.files_skipped_problems.toLocaleString('en-US')} unreadable`)
  }
  if (stats.files_skipped_per_file_cap > 0) {
    parts.push(`${stats.files_skipped_per_file_cap.toLocaleString('en-US')} cut off at the per-file size cap`)
  }
  if (stats.hits_capped_per_file > 0) {
    parts.push(`${stats.hits_capped_per_file.toLocaleString('en-US')} files had matches capped`)
  }
  if (stats.files_skipped_binary !== undefined && stats.files_skipped_binary > 0) {
    parts.push(`${stats.files_skipped_binary.toLocaleString('en-US')} binary (name only)`)
  }
  if (stats.ignore_files_unreadable !== undefined && stats.ignore_files_unreadable > 0) {
    parts.push(
      `${stats.ignore_files_unreadable.toLocaleString('en-US')} .gitignore/.ignore file${stats.ignore_files_unreadable === 1 ? '' : 's'} could not be read`,
    )
  }
  if (parts.length === 0) return null
  return (
    <p data-testid="library-search-files-stats" className="px-[var(--space-2)] text-[length:var(--type-caption-size)] leading-snug text-[var(--color-muted)]">
      {parts.join(' · ')}.
    </p>
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
  loadGraph = defaultKnowledgeGraphLoader,
  className,
}: LibrarySearchBarProps) {
  const [text, setText] = useState('')
  const [filter, setFilter] = useState<VaultSearchKind>('all')
  // UAT D-132: an over-long query is refused HERE with a sentence and never
  // becomes a request — before, the generated client's own Zod check threw
  // and its raw issue list was rendered verbatim as the error.
  const queryLength = text.trim().length
  const queryTooLong = queryLength > VAULT_SEARCH_QUERY_MAX_CHARS
  const hookQuery = queryTooLong ? '' : text
  const [openView, setOpenView] = useState<{ view: string; label: string } | null>(null)
  const inputId = useId()

  const {
    isActive: vaultIsActive,
    isBusy: vaultIsBusy,
    isResolvingCollection,
    collectionId,
    collectionRootPath,
    collectionDisplayName,
    isInsideCollection,
    error: vaultError,
    response,
    counts,
    limit: effectiveLimit,
    coverage,
    clamp,
    notesCappedAtLimit,
    detectionError,
  } = useVaultSearch({
    workspaceId,
    folderPath,
    query: hookQuery,
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
  // Finding F-I: a folder whose knowledge-base DETECTION failed (E-9 — a
  // marker exists but could not be read, or the info request itself
  // errored) must not silently fall through to a plain file walk just
  // because collectionId ended up undefined the same way "not a knowledge
  // base" does. detectionError distinguishes the two so this bar can
  // surface the failure instead of quietly answering a different, wrong
  // question (filename hits only, no notes/records/views, read by the user
  // as "this vault is empty").
  const isFilesMode =
    workspaceId !== null && !isResolvingCollection && !isVaultMode && detectionError === undefined

  const {
    isActive: filesIsActive,
    isBusy: filesIsBusy,
    error: filesError,
    response: filesResponse,
    // Finding S4: `filesResponse` may still be a PREVIOUS query's answer
    // while a newer one sits in the debounce window (useFileSearch keeps it
    // on screen deliberately — see useFileSearch.ts's 429-grace-path
    // comment, which must NOT be removed). Copy describing those results
    // must name the query that actually produced them, not the live
    // keystrokes in the box.
    debouncedQuery: filesDebouncedQuery,
  } = useFileSearch({
    workspaceId,
    folderPath,
    query: hookQuery,
    enabled: isFilesMode,
    ...(debounceMs === undefined ? {} : { debounceMs }),
    ...(searchFilesFn === undefined ? {} : { searchFn: searchFilesFn }),
  })

  const isActive = isVaultMode ? vaultIsActive : filesIsActive
  const isBusy = isVaultMode ? vaultIsBusy : filesIsBusy
  // Finding F-I: detectionError always implies !isVaultMode (collectionId
  // is undefined whenever it is set — see useVaultSearch), so it slots into
  // the SAME branch filesError would otherwise occupy, and outranks it: a
  // folder whose detection genuinely failed has nothing useful to say via
  // a file-search error (isFilesMode is false there, so filesError is
  // always null anyway) — the detection failure IS the error to show.
  const error = isVaultMode ? vaultError : detectionError !== undefined ? new Error(detectionError) : filesError

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
          ? 'Search notes, records, views, attachments'
          : 'Search file and folder names'
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

  // Finding R-1: navigating INTO a directory hit must leave the search
  // state behind — the caller's own onOpenFolder doc says the point is to
  // browse into it, but `text` lives here, entirely inside this component,
  // so a parent's onOpenFolder (LibraryExplorer's setBrowsedDir + goTo) has
  // no way to clear it. Before this fix, isActive stayed true after the
  // navigation, LibrarySearchBar kept `children` (the folder listing)
  // unmounted per `{(!isActive || disabled) && children}`, and
  // useFileSearch simply re-ran the SAME query scoped to the new
  // folderPath — a click on a folder produced ANOTHER result list, never
  // the folder itself.
  async function openFolder(path: string) {
    // Finding S1: clearing unconditionally (before we know whether the
    // caller actually navigated) wiped the query even when the caller
    // declined — e.g. LibraryExplorer's onOpenFolder gates on
    // confirmDiscardLibraryEdits() and the user hit "Cancel" on that prompt.
    // Only clear once onOpenFolder itself reports navigation happened.
    //
    // `await` here is load-bearing, not decorative: onOpenFolder can return
    // a Promise (LibraryExplorer's does), and a raw, un-awaited Promise
    // object is itself truthy — `if (onOpenFolder?.(path))` would clear the
    // query immediately regardless of what the user actually answers.
    const navigated = await (onOpenFolder?.(path) ?? false)
    if (navigated) setText('')
  }

  const viewResultQuery = useQuery({
    queryKey: ['vault-search-view-result', workspaceId, collectionId, openView?.view],
    queryFn: ({ signal }) =>
      loadViewResult(workspaceId as string, collectionId as string, openView?.view as string, signal),
    enabled: openView !== null && workspaceId !== null && collectionId !== undefined,
    retry: false,
  })

  // ── Codex review #12: a collection-aware resolver for the saved-view dialog ──
  // The dialog's relation cells used to render with NO resolver, so
  // ViewCellLink fell back to the RAW wikilink target as the path — a
  // basename cell ("[[Sofia Marchetti]]") navigated to the literal
  // "vault/Sofia Marchetti" and landed on not-found. The dialog now draws
  // its evidence from the SAME one multi-path link-graph request the base
  // preview makes (useCollectionLinkGraph), over the dialog's own rows, and
  // resolves with the same two-tier ladder (makeCollectionLinkResolver) —
  // an edge names the real note, and only a target naming one of the rows
  // in THIS view falls back to resolved-or-unknown.
  const dialogViewResult = viewResultQuery.data
  const dialogLinkRowPaths = useMemo(
    () => (dialogViewResult ? collectionLinkRowPaths(dialogViewResult.rows) : []),
    [dialogViewResult],
  )
  const dialogLinkGraph = useCollectionLinkGraph({
    workspaceId,
    collectionId,
    paths: dialogLinkRowPaths,
    loadGraph,
  })
  const dialogResolveWikilink = useMemo(
    () =>
      dialogViewResult
        ? makeCollectionLinkResolver(dialogLinkGraph.edges, dialogLinkGraph.nodes, dialogViewResult.rows)
        : undefined,
    [dialogViewResult, dialogLinkGraph.edges, dialogLinkGraph.nodes],
  )

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
    <div data-testid="library-search-bar" className={cn('flex flex-col gap-[var(--space-2)]', className)}>
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
          className="pl-[var(--space-5)] pr-[var(--space-5)]"
        />
        {text !== '' && (
          <IconButton
            onClick={() => setText('')}
            aria-label="Clear search"
            data-testid="library-search-clear"
            className="absolute right-2 top-1/2 h-auto w-auto -translate-y-1/2 rounded p-[var(--space-0-5)] text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]"
          >
            <X size={14} aria-hidden="true" />
          </IconButton>
        )}
      </div>

      {queryTooLong && (
        <p role="alert" data-testid="library-search-query-too-long" className="text-[length:var(--type-utility-xs-size)] leading-snug text-[var(--color-error)]">
          Search text is limited to {VAULT_SEARCH_QUERY_MAX_CHARS.toLocaleString('en-US')} characters — yours is{' '}
          {queryLength.toLocaleString('en-US')}. Shorten it to search.
        </p>
      )}

      {(!isActive || disabled) && children}

      {isActive && !disabled && (
        <div data-testid="library-search-active" className="flex flex-col gap-[var(--space-2)]">
          {error && (
            <LibraryErrorBanner message={error.message || 'Search failed.'} testId="library-search-error" />
          )}

          {/* UAT D-133 — the bar never changes what it searches without
              saying so. Inside a vault it searches the WHOLE knowledge base
              (the browsed folder is only where the reader happens to be);
              outside one it matches file and folder names, not note
              contents, and says which folder. */}
          {!error && (isVaultMode || isFilesMode) && (
            <p data-testid="library-search-mode" className="px-[var(--space-2)] text-[length:var(--type-caption-size)] leading-snug text-[var(--color-muted)]">
              {isVaultMode
                ? isInsideCollection
                  ? `Searching the whole knowledge base${collectionDisplayName ? ` “${collectionDisplayName}”` : ''} — this folder is inside it.`
                  : `Searching this knowledge base${collectionDisplayName ? ` “${collectionDisplayName}”` : ''}.`
                : `Searching file and folder names under “${folderPath === '' ? 'the workspace root' : folderPath}” — not note contents. This folder is not inside a knowledge base.`}
            </p>
          )}

          {/* UAT D-129/D-130 (leftover wording round) — ONE summary sentence
              under the tabs stating exactly what was searched, how many hits
              exist, and how many are shown. A kind at its per-kind cap is a
              LOWER bound ("at least N … the search returns at most N per
              kind"), never a fabricated total; an uncapped answer says every
              hit is shown. Attachment hits are name-only matches (FR-039a),
              stated beside the counts so the note count is never read as
              covering them. */}
          {!error && isVaultMode && response && counts.all > 0 && (
            <p data-testid="library-search-results-summary" className="px-[var(--space-2)] text-[length:var(--type-caption-size)] leading-snug text-[var(--color-muted)]">
              {(() => {
                const kinds = (['notes', 'records', 'views', 'attachments'] as const).filter((k) => counts[k] > 0)
                const phrases = kinds.map((k) =>
                  kindAtLimit(k) ? `at least ${kindCountPhrase(counts[k], k)}` : kindCountPhrase(counts[k], k),
                )
                const capped = kinds.some((k) => kindAtLimit(k))
                if (capped) {
                  return (
                    <>
                      Hits for “{text.trim()}”: {phrases.join(', ')} — the search returns at most {effectiveLimit} per
                      kind, so more may exist.
                    </>
                  )
                }
                return (
                  <>
                    Showing all {counts.all.toLocaleString('en-US')} {counts.all === 1 ? 'hit' : 'hits'} for “
                    {text.trim()}”: {phrases.join(', ')}.
                  </>
                )
              })()}
              {response.attachments !== undefined && ' Attachments are matched by filename only, never their contents.'}
            </p>
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

          <div role="status" aria-live="polite" className="flex flex-col gap-[var(--space-1)] empty:hidden">
            {isBusy && (
              <p data-testid="library-search-busy" className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
                Searching…
              </p>
            )}
          </div>

          {!error && isVaultMode && notReady && (
            <div
              role="status"
              data-testid="library-search-not-ready"
              className="flex items-start gap-[var(--space-2)] rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 px-[var(--space-2-5)] py-[var(--space-2)]"
            >
              <Warning size={14} weight="fill" aria-hidden="true" className="mt-[var(--space-0-5)] shrink-0 text-[var(--color-warning)]" />
              <div className="flex-1 text-[length:var(--type-utility-xs-size)] leading-snug text-[var(--color-warning)]">
                <p data-testid="library-search-statement">{bannerStatement}</p>
                {/* US-1 AS-2 honesty port (FR-036): a ratio is stated only
                    when BOTH a searched count and a known total are in hand;
                    otherwise a bare "so far" count — never an invented
                    denominator. */}
                {coverage === 'ratio' &&
                  typeof response?.notes_searched === 'number' &&
                  typeof response.notes_total_known === 'number' && (
                    <p data-testid="library-search-coverage-ratio">
                      {/* UAT D-129: the pair counts markdown NOTES whose full
                          text the index holds — attachments are name-only
                          (FR-039a) and are never inside this number — so the
                          sentence says exactly what was searched. */}
                      Searched the full text of {response.notes_searched.toLocaleString('en-US')} of{' '}
                      {response.notes_total_known.toLocaleString('en-US')} notes.
                    </p>
                  )}
                {coverage === 'so-far' && typeof response?.notes_searched === 'number' && (
                  <p data-testid="library-search-coverage-so-far">
                    Searched the full text of {response.notes_searched.toLocaleString('en-US')} notes so far.
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
            <p data-testid="library-search-complete-statement" className="text-[length:var(--type-utility-xs-size)] leading-snug text-[var(--color-muted)]">
              {response.statement}
            </p>
          )}

          {!error && isVaultMode && clamp && (
            <div
              role="status"
              data-testid="library-search-clamped"
              className="flex items-start gap-[var(--space-2)] rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-[var(--space-2-5)] py-[var(--space-2)]"
            >
              <Warning size={14} aria-hidden="true" className="mt-[var(--space-0-5)] shrink-0 text-[var(--color-muted)]" />
              <p className="flex-1 text-[length:var(--type-utility-xs-size)] leading-snug text-[var(--color-muted)]">
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
            <p role="status" data-testid="library-search-empty" className="text-[length:var(--type-utility-xs-size)] leading-snug text-[var(--color-muted)]">
              No results for “{text.trim()}”.
            </p>
          )}

          {!error && isVaultMode && response && counts.all > 0 && (
            <div data-testid="library-search-results" className="flex flex-col gap-[var(--space-2-5)] overflow-y-auto">
              {(filter === 'all' || filter === 'notes') && response.notes.length > 0 && (
                <div>
                  {filter === 'all' && (
                    <p className="px-[var(--space-2)] pb-[var(--space-1)] text-[length:var(--type-caption-size)] font-medium uppercase tracking-wide text-[var(--color-muted)]">
                      Notes
                    </p>
                  )}
                  <ul className="flex flex-col gap-[var(--space-1)]">
                    {response.notes.map((hit) => (
                      <NoteRow key={hit.path} hit={hit} query={text.trim()} onOpen={() => openNote(hit.path)} />
                    ))}
                  </ul>
                </div>
              )}

              {(filter === 'all' || filter === 'records') && response.records.length > 0 && (
                <div>
                  {filter === 'all' && (
                    <p className="px-[var(--space-2)] pb-[var(--space-1)] text-[length:var(--type-caption-size)] font-medium uppercase tracking-wide text-[var(--color-muted)]">
                      Records
                    </p>
                  )}
                  <ul className="flex flex-col gap-[var(--space-1)]">
                    {response.records.map((hit) => (
                      <RecordRow key={hit.path} hit={hit} query={text.trim()} onOpen={() => openNote(hit.path)} />
                    ))}
                  </ul>
                </div>
              )}

              {(filter === 'all' || filter === 'views') && response.views.length > 0 && (
                <div>
                  {filter === 'all' && (
                    <p className="px-[var(--space-2)] pb-[var(--space-1)] text-[length:var(--type-caption-size)] font-medium uppercase tracking-wide text-[var(--color-muted)]">
                      Views
                    </p>
                  )}
                  <ul className="flex flex-col gap-[var(--space-1)]">
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
                    <p className="px-[var(--space-2)] pb-[var(--space-1)] text-[length:var(--type-caption-size)] font-medium uppercase tracking-wide text-[var(--color-muted)]">
                      Attachments
                    </p>
                  )}
                  <ul className="flex flex-col gap-[var(--space-1)]">
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
                <p data-testid="library-search-filter-empty" className="px-[var(--space-2)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
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
              {filesResponse.truncated && (
                <div
                  role="status"
                  data-testid="library-search-truncated"
                  className="flex items-start gap-[var(--space-2)] rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 px-[var(--space-2-5)] py-[var(--space-2)]"
                >
                  <Warning
                    size={14}
                    weight="fill"
                    aria-hidden="true"
                    className="mt-[var(--space-0-5)] shrink-0 text-[var(--color-warning)]"
                  />
                  <p className="flex-1 text-[length:var(--type-utility-xs-size)] leading-snug text-[var(--color-warning)]">
                    {/* Finding F-J: the schema states truncated_reason
                        "present exactly when truncated is true" but does
                        not enforce it on the wire (no `required`, no
                        dependentRequired), so `{truncated: true}` alone
                        validates. Before this fix the whole banner was
                        gated on the reason ALSO being present, so that
                        shape rendered nothing at all — a bounded result
                        presented, in effect, as complete. A generic
                        sentence now covers the reason-less case. */}
                    {filesResponse.truncated_reason !== undefined
                      ? FILE_SEARCH_TRUNCATED_REASON[filesResponse.truncated_reason]
                      : 'Stopped early — this search did not finish.'}{' '}
                    {/* Finding S3: files_visited alone, next to "too many
                        files to search in one pass", can read as nonsense —
                        a walk that burned its whole Files budget on 40,000
                        directories and 12 files reported only "12 files
                        searched". dirs_visited is optional (older engines
                        never sent it — MV-3-adjacent back-compat, matching
                        this same field's own schema comment), so it is
                        included only when present, never invented as 0. */}
                    {filesResponse.stats.dirs_visited !== undefined && (
                      <>
                        {filesResponse.stats.dirs_visited.toLocaleString('en-US')} director
                        {filesResponse.stats.dirs_visited === 1 ? 'y' : 'ies'} and{' '}
                      </>
                    )}
                    {filesResponse.stats.files_visited.toLocaleString('en-US')} file
                    {filesResponse.stats.files_visited === 1 ? '' : 's'} searched.
                    {/* Finding S3: when a mount is lost (root_lost), the
                        response NAMES which one via truncated_root — shown
                        only then, since the schema only ever sends it
                        alongside that reason (and only when more than one
                        root was searched). `!== undefined`, not truthiness:
                        '' is the legitimate "workspace's own work tree"
                        value, not "absent". */}
                    {filesResponse.truncated_root !== undefined && (
                      <> Unreadable root: {truncatedRootLabel(filesResponse.truncated_root)}.</>
                    )}
                  </p>
                </div>
              )}

              {filesResponse.hits.length === 0 && (
                <div className="flex flex-col gap-[var(--space-1)]">
                  {/* Finding S4: `text` is the LIVE keystroke value; during
                      the debounce window it can already name a query that
                      has not run yet, while `filesResponse` (and this empty
                      state) still belongs to the PREVIOUS one.
                      filesDebouncedQuery is the query that actually produced
                      this response — the retention of the stale response
                      itself is load-bearing for the 429 grace path
                      (useFileSearch.ts) and must stay; only the label
                      changes. */}
                  <p role="status" data-testid="library-search-empty" className="text-[length:var(--type-utility-xs-size)] leading-snug text-[var(--color-muted)]">
                    No results for “{filesDebouncedQuery}”.
                  </p>
                  {/* Finding F-K, sharpest case: a file can be visible in
                      the listing, pruned from search by .gitignore, and
                      "No results" alone reads as "the term is not in this
                      folder" — when the truth is "a file that could have
                      matched was never searched". Surface that here since
                      it is exactly where a reader needs it most. */}
                  <FileSearchStatsFooter stats={filesResponse.stats} />
                </div>
              )}

              {filesResponse.hits.length > 0 && (
                <>
                  <ul data-testid="library-search-results" className="flex flex-col gap-[var(--space-1)] overflow-y-auto">
                    {filesResponse.hits.map((hit, i) => (
                      <FileHitRow
                        key={`${hit.path}:${hit.line ?? 0}:${i}`}
                        hit={hit}
                        // Finding S4: highlight against the query that
                        // actually produced these hits, not the live text —
                        // see the empty-state comment above for why they can
                        // differ during the debounce window.
                        query={filesDebouncedQuery}
                        // Finding R-2: a directory hit must be opened as a
                        // FOLDER or not at all — falling through to openFile
                        // (which addresses a NOTE/file, not a container)
                        // when no onOpenFolder handler is wired would
                        // select a directory in the preview pane, the exact
                        // failure this prop's own doc comment says must not
                        // happen. interactive is false in exactly that
                        // case, so the row renders visibly inert instead.
                        interactive={hit.is_dir !== true || onOpenFolder !== undefined}
                        onOpen={() => {
                          if (hit.is_dir === true) {
                            openFolder(hit.path)
                            return
                          }
                          openFile(hit.path)
                        }}
                      />
                    ))}
                  </ul>
                  <FileSearchStatsFooter stats={filesResponse.stats} />
                </>
              )}
            </>
          )}
        </div>
      )}

      <Dialog open={openView !== null} onOpenChange={(open) => !open && setOpenView(null)}>
        <DialogContent className="max-w-3xl" data-testid="library-search-view-dialog">
          <DialogHeader>
            <DialogTitle>{openView?.label}</DialogTitle>
            <DialogDescription>
              {/* UAT D-136: name the file the view lives in and offer to open
                  it — the view is reachable as a tab there. */}
              {viewResultQuery.data?.source !== undefined ? (
                <span data-testid="library-search-view-source">
                  Saved view from{' '}
                  <Button
                    variant="link"
                    data-testid="library-search-view-source-open"
                    onClick={() => {
                      const src = viewResultQuery.data?.source
                      if (src !== undefined) {
                        openNote(src)
                        setOpenView(null)
                      }
                    }}
                    className="text-[color:inherit] text-[length:inherit] font-[var(--font-weight-regular)] underline underline-offset-2 hover:text-[var(--color-secondary)]"
                  >
                    {viewResultQuery.data.source}
                  </Button>
                </span>
              ) : (
                'Saved view'
              )}
            </DialogDescription>
          </DialogHeader>
          {viewResultQuery.isPending && (
            <div role="status" className="flex items-center gap-[var(--space-2)] py-[var(--space-4)] text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
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
          {/* UAT D-72: the SAME row-open and relation-cell link wiring the
              base preview has, so a `[[Sofia Marchetti]]` cell is a real
              link here too instead of raw brackets. Codex #12: the cell is
              RESOLVED against the collection first (dialogResolveWikilink),
              so the click opens the note the link graph names — never the
              literal wikilink text as a path. */}
          {viewResultQuery.data && (
            <ViewPartsRenderer
              result={viewResultQuery.data}
              onOpenPath={(p) => {
                openNote(p)
                setOpenView(null)
              }}
              {...(dialogResolveWikilink ? { resolveWikilink: dialogResolveWikilink } : {})}
              {...(workspaceId !== null && collectionRootPath !== undefined
                ? {
                    linkHref: (p: string) =>
                      libraryNoteHref(workspaceId, collectionPathToWorkspacePath(collectionRootPath, p)),
                  }
                : {})}
            />
          )}
        </DialogContent>
      </Dialog>
    </div>
  )
}
