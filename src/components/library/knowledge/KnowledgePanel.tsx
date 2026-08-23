// KnowledgePanel — the knowledge-base surface for whichever folder the Library
// is showing (ADR-067 US-4, US-6, E-1, E-9).
//
// WHAT THIS OWNS. Two things, and deliberately not more:
//
//   1. THE ANSWER ABOUT THIS FOLDER. It asks the contract endpoint
//      GET /library/{workspace_id}/knowledge?path=… whether this folder is a
//      knowledge base, combines that with whatever the indexer has last said
//      over the WebSocket, and resolves the two into ONE state a reader can
//      act on — see resolveKnowledgeFirstRunState below, which is a pure
//      function precisely so its ordering rules can be tested without a DOM.
//   2. COMPOSITION. When (and only when) the folder really is a knowledge
//      base, it renders KnowledgeSearch (a collection-level surface, which is
//      what this panel is scoped to) plus any `children` the caller adds. It
//      duplicates none of that component's behaviour — the incompleteness and
//      clamping statements on a search RESPONSE belong to KnowledgeSearch and
//      useKnowledgeSearch, and are not restated here. The note-level surfaces
//      — outline, backlinks, reader — are deliberately NOT here: they belong
//      to whichever pane has a note open, not to a folder.
//
// WHY PROGRESS ARRIVES AS A PROP AND NOT A FETCH. KnowledgeBaseInfo carries no
// index counts, on purpose: the contract states that a REST field for progress
// "would invite exactly the polling loop this decision exists to prevent".
// Progress is the `knowledge_index_progress` WebSocket frame. This panel
// therefore NEVER polls — it takes the latest frame as a prop and does nothing
// if none has arrived.
//
//   The frame is routed by src/store/chat.ts's handleFrame into
//   src/store/knowledgeIndex.ts, keyed by collection_id, and this panel reads
//   the latest frame for ITS collection from there. The `progress` prop remains
//   and still wins when supplied — it is the seam tests inject at, and the way
//   a caller that already holds a frame can pass one — but it is no longer the
//   only source, which is what made every knowledge base in the product report
//   "no indexing progress received" forever.
//
//   A collection the client has heard nothing about STILL renders
//   `index_status_unknown`: the gateway emits progress on mount and on each
//   drift check, so a browser that opens the Library between two of those
//   events legitimately has no news, and absence of news is not news.
//
// THE ONE RULE THAT OUTRANKS EVERYTHING HERE. A partial or unknown answer must
// be visible in the pane, in words, where a reader cannot miss it. Never a
// tooltip, never a console warning, and never a percentage computed against a
// total we do not have.

import { useQuery } from '@tanstack/react-query'
import { CircleNotch } from '@phosphor-icons/react'
import type { ReactNode } from 'react'

import type { KnowledgeIndexProgressFrame } from '@/lib/api/generated/asyncapi-types'
import type { KnowledgeBaseInfo } from '@/lib/api/generated/openapi-types'
import { fetchKnowledgeBaseInfo } from '@/lib/api'
import { useKnowledgeIndexStore } from '@/store/knowledgeIndex'
import { cn } from '@/lib/utils'

import { collectionPathToWorkspacePath } from './KnowledgeBacklinks'
import { KnowledgeEmptyState, type KnowledgeFirstRunState } from './KnowledgeEmptyState'
import { KnowledgeSearch } from './KnowledgeSearch'

/** Last path segment of a workspace-relative folder path; '' for the root. */
function folderNameOf(rootPath: string): string {
  const trimmed = rootPath.replace(/\/+$/, '')
  const cut = trimmed.lastIndexOf('/')
  return cut === -1 ? trimmed : trimmed.slice(cut + 1)
}

/**
 * What to call this collection: the name recorded in its marker (FR-024), and
 * failing that the folder's own name — which is what the contract's
 * `display_name` description prescribes as the fallback. Never an empty
 * heading.
 */
export function knowledgeDisplayName(info: KnowledgeBaseInfo): string {
  if (info.display_name && info.display_name.trim() !== '') return info.display_name
  const folder = folderNameOf(info.root_path)
  return folder !== '' ? folder : 'This collection'
}

/**
 * Resolve the detection answer plus the latest indexer frame into the single
 * state the reader is shown.
 *
 * THE ORDERING IS THE POINT, and every step of it is a stated requirement:
 *
 *   1. A detection ERROR outranks everything, including `is_knowledge_base`
 *      itself. E-9: "Detection fails loudly; the folder is not silently
 *      downgraded to 'ordinary'." The contract says the same in the field's
 *      own description — the caller MUST surface it rather than treating the
 *      folder as ordinary. So a false `is_knowledge_base` arriving alongside a
 *      detection_error must NOT produce the ordinary-folder state.
 *   2. Not a knowledge base → the ordinary-folder state, which is an ANSWER
 *      and not an error (US-4 AS-3).
 *   3. A progress frame for a DIFFERENT collection is ignored entirely. Two
 *      collections are open in one app all the time; letting the wrong one's
 *      counts drive this pane would be a fabricated answer of the worst kind
 *      — a plausible one.
 *   4. Indexing outranks the empty-collection first run. US-6 AS-5 states this
 *      as its own acceptance scenario: a newly created, still-indexing
 *      collection shows the indexing state, NOT "this collection is empty" —
 *      the latter would be a claim about a collection nobody has finished
 *      looking at.
 *   5. Only when the indexer says `idle` — its terminal success state — may an
 *      empty collection be called empty (E-1), or a full one be called ready.
 *   6. No frame at all → status unknown. NOT "ready": absence of news is not
 *      news, and calling an unverified index complete is the exact confident
 *      wrongness US-6 exists to prevent.
 */
export function resolveKnowledgeFirstRunState(
  info: KnowledgeBaseInfo,
  progress?: KnowledgeIndexProgressFrame,
): KnowledgeFirstRunState {
  // 1 — detection failure outranks every other answer (E-9).
  if (info.detection_error) {
    return {
      kind: 'detection_error',
      code: info.detection_error.code,
      message: info.detection_error.message,
      rootPath: info.root_path,
    }
  }

  // 2 — an ordinary folder.
  if (!info.is_knowledge_base) {
    return { kind: 'not_a_knowledge_base', rootPath: info.root_path }
  }

  const name = knowledgeDisplayName(info)

  // 3 — a frame belonging to another collection tells us nothing about this
  // one. `collection_id` is derived from the resolved real path (FR-031), so
  // two mounts of the same folder legitimately share it and both update.
  const applicable =
    progress && info.collection_id !== undefined && progress.collection_id === info.collection_id
      ? progress
      : undefined

  // 6 — nothing has reported. Completeness is unknown, and we say so.
  if (!applicable) return { kind: 'index_status_unknown', name }

  if (applicable.phase === 'failed') {
    return {
      kind: 'index_failed',
      name,
      message: applicable.error ?? 'Indexing stopped, and the indexer reported no reason.',
      indexedFiles: applicable.indexed_files,
    }
  }

  // 4 — indexing outranks the empty first run (US-6 AS-5).
  if (applicable.phase === 'enumerating' || applicable.phase === 'indexing') {
    return {
      kind: 'indexing',
      name,
      phase: applicable.phase,
      indexedFiles: applicable.indexed_files,
      totalKnown: applicable.total_known,
      totalFiles: applicable.total_files,
      skippedFiles: applicable.skipped_files,
    }
  }

  // 5 — idle: the terminal success state, and the only place a count may be
  // read as final.
  if (applicable.indexed_files === 0) {
    return { kind: 'empty_collection', name, skippedFiles: applicable.skipped_files }
  }
  return {
    kind: 'ready',
    name,
    indexedFiles: applicable.indexed_files,
    skippedFiles: applicable.skipped_files,
  }
}

export interface KnowledgePanelProps { // not-wire-format: SPA-only component props. Never serialized to or from the gateway.
  /** Workspace whose work tree holds the folder. null = the Library's virtual root, where there is no folder to test. */
  workspaceId: string | null
  /** Workspace-relative folder path. '' is the work-tree root. */
  path?: string
  /**
   * Latest `knowledge_index_progress` frame. Omit when none has arrived — the
   * panel then says completeness is unknown rather than assuming it is fine.
   * Never fetched or polled here; see this file's header.
   */
  progress?: KnowledgeIndexProgressFrame
  /** Create a knowledge base in this folder. Omit when unavailable — no dead button is rendered. */
  onCreateCollection?: () => void
  /** Create the first note in an empty collection. Same rule. */
  onCreateNote?: () => void
  /**
   * Open a note the reader picked out of search results. Receives a
   * WORKSPACE-relative path — search hits are collection-relative, and the
   * translation happens here so callers deal in one kind of path only (the
   * kind the Library address takes, FR-012).
   */
  onOpenNote?: (workspacePath: string) => void
  /** Test seam for the search request; production uses the module default. */
  searchFn?: React.ComponentProps<typeof KnowledgeSearch>['searchFn']
  /**
   * Extra knowledge-base surface composed by the caller, rendered beneath the
   * search box. Rendered only when the folder really is a knowledge base and
   * detection succeeded, so those features stay off everywhere else
   * (US-4 AS-3).
   */
  children?: ReactNode
  /** Test seam: overrides the contract call. Production passes nothing. */
  loadInfo?: (workspaceId: string, path: string) => Promise<KnowledgeBaseInfo>
  className?: string
}

export function KnowledgePanel({
  workspaceId,
  path = '',
  progress,
  onCreateCollection,
  onCreateNote,
  onOpenNote,
  searchFn,
  children,
  loadInfo = fetchKnowledgeBaseInfo,
  className,
}: KnowledgePanelProps) {
  // Subscribed BEFORE the early returns below, because hooks must be. The
  // whole map is selected rather than one entry: `collection_id` is not known
  // until detection resolves, and a selector cannot be reordered after a return.
  const progressByCollection = useKnowledgeIndexStore((s) => s.byCollection)

  const query = useQuery({
    queryKey: ['knowledge-base-info', workspaceId, path],
    queryFn: () => loadInfo(workspaceId as string, path),
    enabled: workspaceId !== null,
    // Detection is a marker stat, not a moving value — nothing here justifies
    // re-asking on every window focus. Progress, the part that DOES move,
    // arrives over the WebSocket instead.
    refetchOnWindowFocus: false,
    retry: false,
  })

  // The virtual root lists workspaces, not files. There is no folder to ask
  // about, so there is nothing honest to say.
  if (workspaceId === null) return null

  if (query.isPending) {
    return (
      <div
        data-testid="knowledge-panel-checking"
        role="status"
        className={cn(
          'flex items-center gap-2 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] px-4 py-2.5 text-xs text-[var(--color-muted)]',
          className,
        )}
      >
        <CircleNotch size={14} aria-hidden="true" className="animate-spin" />
        Checking whether this folder is a knowledge base
      </div>
    )
  }

  if (query.isError) {
    // The check itself failed. This is NOT the same as "an ordinary folder",
    // and must never be rendered as one — the same reasoning as E-9, applied
    // one layer out: there, detection ran and could not finish; here, we could
    // not ask at all. Either way the verdict is unknown, and saying "not a
    // knowledge base" would be an answer we do not have.
    const reason = query.error instanceof Error ? query.error.message : String(query.error)
    return (
      <div
        data-testid="knowledge-panel-error"
        role="alert"
        className={cn(
          'flex items-start gap-2 rounded-lg border border-[var(--color-error)]/40 bg-[var(--color-error)]/10 px-4 py-2.5',
          className,
        )}
      >
        <div className="min-w-0 flex-1 space-y-1 text-xs leading-relaxed text-[var(--color-error)]">
          <p>
            Omnipus could not check whether this folder is a knowledge base, so search and linked
            mentions are unavailable here. This is not the same as the folder being an ordinary
            one — the answer is unknown.
          </p>
          <p data-testid="knowledge-panel-error-reason" className="font-mono break-all opacity-90">
            {reason}
          </p>
        </div>
        {/* "Check again", not "Retry": the folder listing this panel sits above
            has its own Retry button, and two controls with the same
            accessible name on one screen are ambiguous to anyone navigating by
            name — a screen-reader user, or a test. This one names the thing it
            re-does. */}
        <button
          type="button"
          tabIndex={0}
          data-testid="knowledge-panel-retry"
          onClick={() => void query.refetch()}
          className="shrink-0 rounded-md border border-[var(--color-error)]/50 px-2 py-1 text-xs font-medium text-[var(--color-error)] transition-colors hover:bg-[var(--color-error)]/15"
        >
          Check again
        </button>
      </div>
    )
  }

  const info = query.data
  // An explicitly-passed frame wins; otherwise the live one for THIS collection.
  // resolveKnowledgeFirstRunState still checks the id itself (rule 3), so a
  // mis-keyed entry could not drive this pane even if one existed.
  const liveProgress =
    info.collection_id !== undefined ? progressByCollection[info.collection_id] : undefined
  const state = resolveKnowledgeFirstRunState(info, progress ?? liveProgress)

  // Knowledge-base features switch on for the right folders and stay off
  // everywhere else (US-4). "Everywhere else" includes a folder we failed to
  // classify — an unknown verdict does not switch anything on.
  const surfaceEnabled = info.is_knowledge_base && info.detection_error === undefined

  // US-4 AS-3, at the layout level. An ordinary folder with no collection to
  // create has nothing to show and nothing to offer, and the overwhelming
  // majority of Library browsing is ordinary folders. Returning null keeps an
  // empty, padded box off the top of every folder listing in the product —
  // KnowledgeEmptyState already renders nothing for this case, and this is the
  // matching decision one layer out so the wrapper does not survive it.
  if (state.kind === 'not_a_knowledge_base' && !onCreateCollection) return null

  return (
    <div data-testid="knowledge-panel" className={cn('flex flex-col gap-2', className)}>
      <KnowledgeEmptyState
        state={state}
        onCreateCollection={onCreateCollection}
        onCreateNote={onCreateNote}
      />
      {surfaceEnabled && (
        <div data-testid="knowledge-panel-surface" className="flex flex-col gap-2">
          <KnowledgeSearch
            workspaceId={info.workspace_id}
            collectionId={info.collection_id}
            searchFn={searchFn}
            onOpenNote={
              onOpenNote
                ? (hitPath) => onOpenNote(collectionPathToWorkspacePath(info.root_path, hitPath))
                : undefined
            }
          />
          {children}
        </div>
      )}
    </div>
  )
}
