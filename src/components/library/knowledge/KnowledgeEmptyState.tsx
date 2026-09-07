// KnowledgeEmptyState — the first-run and not-yet-ready states of a knowledge
// base, as one presentational component (ADR-067 US-4, US-6, E-1, E-9).
//
// WHY THIS EXISTS AS ITS OWN COMPONENT. Every one of these states used to be
// "an empty pane", which is indistinguishable from a broken one. A folder that
// is not a collection, a collection still being indexed, a collection with no
// notes in it, and a detection that FAILED are four different answers, and a
// reader has to be able to tell them apart without reading a console.
//
// THE HONESTY RULES THIS COMPONENT ENFORCES (ADR-067 FR-035, FR-036, and the
// KnowledgeIndexProgressFrame contract's "HONESTY WHILE ENUMERATING" note):
//
//   1. While the total is UNKNOWN — `total_known: false`, i.e. the tree is
//      still being walked — this renders an INDETERMINATE state. It states a
//      count found so far and nothing else. It renders no ratio, no
//      percentage, and no progress bar carrying a value, because every one of
//      those requires a denominator we do not have. A bar sitting at some
//      computed percentage of an invented total is a confidently wrong answer,
//      which is the single failure this whole feature exists to refuse.
//   2. "0 of 0" is never rendered. A ratio appears only when the total is
//      known AND greater than zero (US-6 AS-1 names "0 of 0" explicitly).
//   3. The `ready` state renders NOTHING. A finished index gets no banner at
//      all — not a green tick, not a "complete" line (US-6 AS-4, AS-6). A
//      reassurance shown on every visit trains people to ignore the place the
//      real warning will appear.
//   4. Skipped files are never silent (FR-112, FR-044, FR-111). A non-zero
//      skip count is stated wherever it is known, including in states that are
//      otherwise "fine".
//   5. NO DEAD AFFORDANCES. Where an action has no handler wired — because the
//      endpoint behind it does not exist yet — this renders a sentence saying
//      so, never a button that looks live and does nothing. A control that
//      appears to work and changes nothing is the exact anti-pattern this
//      project banned after the Delegation Graph screen (ADR-037).
//
// Precedence between the states is NOT decided here — it is decided by
// resolveKnowledgeFirstRunState() in KnowledgePanel.tsx, which is where the
// ordering rules (detection failure outranks everything; indexing outranks the
// empty-collection first run) can be unit-tested on their own.

import {
  Books,
  FileText,
  FolderSimple,
  Warning,
  WarningCircle,
} from '@phosphor-icons/react'
import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

/**
 * One resolved answer about a folder, ready to render.
 *
 * Mirrors the two contract types this surface is driven by — KnowledgeBaseInfo
 * (REST) and KnowledgeIndexProgressFrame (WebSocket) — but is deliberately a
 * VIEW state, not a wire type: it is never serialised, never crosses the
 * gateway boundary, and exists so that the ordering decision and the rendering
 * decision are separable. Hard constraint #8 governs wire formats; this is not
 * one.
 */
export type KnowledgeFirstRunState =
  /** Detection could not complete. E-9: never silently downgraded to "ordinary folder". */
  | {
      kind: 'detection_error'
      code: 'marker_unreadable' | 'root_unreadable' | 'root_missing' | 'not_a_directory'
      /** The server's own explanation, shown verbatim. */
      message: string
      rootPath: string
    }
  /** An ordinary folder — markers absent, and detection succeeded in saying so. */
  | { kind: 'not_a_knowledge_base'; rootPath: string }
  /** First index (or a rebuild) is under way. Results from here are partial. */
  | {
      kind: 'indexing'
      name: string
      phase: 'enumerating' | 'indexing'
      indexedFiles: number
      totalKnown: boolean
      totalFiles?: number
      skippedFiles?: number
    }
  /** Indexing stopped with an error. Contract: the client MUST surface it. */
  | { kind: 'index_failed'; name: string; message: string; indexedFiles: number }
  /** Recognised, but no progress has been reported, so completeness is unknown. */
  | { kind: 'index_status_unknown'; name: string }
  /** A real, common state: a knowledge base with no notes in it (E-1). */
  | { kind: 'empty_collection'; name: string; skippedFiles?: number }
  /** Indexed and current. Renders nothing at all. */
  | { kind: 'ready'; name: string; indexedFiles: number; skippedFiles?: number }

export interface KnowledgeEmptyStateProps {
  state: KnowledgeFirstRunState
  /**
   * Create a knowledge base in this folder. Omit when creation is not
   * available — the component then explains that it is unavailable instead of
   * rendering a button that does nothing.
   */
  onCreateCollection?: () => void
  /** Create the first note in an empty collection. Same rule as above. */
  onCreateNote?: () => void
  className?: string
}

/** Human-readable reason detection could not complete, per error code. */
const DETECTION_ERROR_REASON: Record<
  Extract<KnowledgeFirstRunState, { kind: 'detection_error' }>['code'],
  string
> = {
  marker_unreadable:
    'A collection marker is there, but Omnipus could not read it — most often a permissions problem.',
  root_unreadable: 'Omnipus could not read this folder — most often a permissions problem.',
  root_missing: 'This folder is no longer at the location Omnipus has recorded for it.',
  not_a_directory: 'This path points at a file, not a folder.',
}

/**
 * A ratio may be shown only when the total is genuinely known AND is not zero.
 *
 * The zero guard is not defensive tidiness — US-6 AS-1 names "0 of 0" as a
 * forbidden rendering, because it reads as a finished job on an empty
 * collection when in fact it means the count has not arrived.
 */
export function canShowIndexRatio(totalKnown: boolean, totalFiles?: number): boolean {
  return totalKnown && typeof totalFiles === 'number' && totalFiles > 0
}

function Shell({
  icon,
  title,
  testId,
  tone = 'neutral',
  children,
  className,
}: {
  icon: ReactNode
  title: string
  testId: string
  tone?: 'neutral' | 'error' | 'notice'
  children: ReactNode
  className?: string
}) {
  return (
    <section
      data-testid={testId}
      // role=status (a polite live region) rather than role=alert: these are
      // states of the surface, announced when they change, not interruptions.
      // The one genuine failure state overrides this — see DetectionError.
      role="status"
      className={cn(
        'flex items-start gap-3 rounded-lg border px-4 py-3',
        tone === 'error'
          ? 'border-[var(--color-error)]/40 bg-[var(--color-error)]/10'
          : tone === 'notice'
            ? 'border-[var(--color-accent)]/30 bg-[var(--color-accent)]/5'
            : 'border-[var(--color-border)] bg-[var(--color-surface-2)]',
        className,
      )}
    >
      <span
        className={cn(
          'mt-0.5 shrink-0',
          tone === 'error' ? 'text-[var(--color-error)]' : 'text-[var(--color-accent)]',
        )}
        aria-hidden="true"
      >
        {icon}
      </span>
      <div className="min-w-0 flex-1">
        <h3
          className={cn(
            'text-sm font-semibold',
            tone === 'error' ? 'text-[var(--color-error)]' : 'text-[var(--color-secondary)]',
          )}
        >
          {title}
        </h3>
        <div className="mt-1 space-y-2 text-xs leading-relaxed text-[var(--color-muted)]">
          {children}
        </div>
      </div>
    </section>
  )
}

/**
 * The skipped-files line (FR-112). Rendered wherever a skip count is known and
 * non-zero, including in states that are otherwise healthy — files Omnipus
 * chose not to index must never simply vanish from the reader's picture of the
 * collection.
 */
function SkippedFiles({ count }: { count?: number }) {
  if (!count || count <= 0) return null
  return (
    <p data-testid="knowledge-skipped-files">
      {count} {count === 1 ? 'file was' : 'files were'} skipped and not indexed — symbolic links,
      paths leading outside the collection, or files that could not be read.
    </p>
  )
}

export function KnowledgeEmptyState({
  state,
  onCreateCollection,
  onCreateNote,
  className,
}: KnowledgeEmptyStateProps) {
  switch (state.kind) {
    // ── A finished, current index says nothing at all ────────────────────────
    // US-6 AS-4 and AS-6. Not a tick, not a "complete" line, not a banner that
    // fades. The absence IS the signal: anything shown here is worth reading.
    case 'ready':
      return null

    // ── Detection failed (E-9) ───────────────────────────────────────────────
    case 'detection_error':
      return (
        <section
          data-testid="knowledge-state-detection-error"
          // role=alert, not status: this is the one state where Omnipus does
          // not know the answer, and the reader must not act on the folder as
          // though it were ordinary.
          role="alert"
          className={cn(
            'flex items-start gap-3 rounded-lg border border-[var(--color-error)]/40 bg-[var(--color-error)]/10 px-4 py-3',
            className,
          )}
        >
          <WarningCircle
            size={18}
            weight="fill"
            aria-hidden="true"
            className="mt-0.5 shrink-0 text-[var(--color-error)]"
          />
          <div className="min-w-0 flex-1">
            <h3 className="text-sm font-semibold text-[var(--color-error)]">
              Omnipus could not check this folder
            </h3>
            <div className="mt-1 space-y-2 text-xs leading-relaxed text-[var(--color-error)]/90">
              <p>{DETECTION_ERROR_REASON[state.code]}</p>
              <p data-testid="knowledge-detection-error-message" className="font-mono break-all">
                {state.message}
              </p>
              <p>
                Because the check did not complete, Omnipus is not treating{' '}
                <span className="font-mono break-all">{state.rootPath || 'this folder'}</span> as an
                ordinary folder either. Whether it is a knowledge base is unknown until the folder
                can be read.
              </p>
            </div>
          </div>
        </section>
      )

    // ── (b) An ordinary folder ───────────────────────────────────────────────
    //
    // US-4 AS-3: an ordinary folder is "an ordinary folder with no
    // knowledge-base features". With no `onCreateCollection` wired there is no
    // offer to make — and a permanent card above every listing in every
    // workspace, explaining that a feature the reader never asked for is
    // switched off and cannot be switched on, IS a knowledge-base feature
    // switched on everywhere. It is also the failure rule 3 of this file's own
    // header names: a banner shown on every visit trains people to ignore the
    // place the real warning will appear.
    //
    // So: with an offer, say what the offer is. Without one, say nothing. This
    // is the only state whose silence depends on a prop, and it depends on it
    // because the prop is the entire reason the state has anything to say.
    case 'not_a_knowledge_base':
      if (!onCreateCollection) return null
      return (
        <Shell
          testId="knowledge-state-not-a-knowledge-base"
          icon={<FolderSimple size={18} />}
          title="Not a knowledge base"
          className={className}
        >
          <p>
            This folder has no collection marker, so search and linked mentions stay switched off
            here. Heading outlines still work on any markdown file.
          </p>
          <p>
            Creating one writes an <span className="font-mono">.omnipus-vault</span> folder inside
            this workspace. Omnipus never creates an{' '}
            <span className="font-mono">.obsidian</span> folder, and never changes one it finds.
          </p>
          <button
            type="button"
            tabIndex={0}
            data-testid="knowledge-create-collection"
            onClick={onCreateCollection}
            className="inline-flex h-8 items-center gap-2 rounded-md bg-[var(--color-accent)] px-3 text-xs font-semibold text-[var(--color-primary)] transition-colors hover:bg-[var(--color-accent-hover)]"
          >
            <Books size={14} weight="fill" aria-hidden="true" />
            Create a collection here
          </button>
        </Shell>
      )

    // ── (a) An existing vault, recognised, being indexed ─────────────────────
    case 'indexing': {
      const showRatio = canShowIndexRatio(state.totalKnown, state.totalFiles)
      return (
        <Shell
          testId="knowledge-state-indexing"
          icon={<Books size={18} weight="fill" />}
          title={`Indexing ${state.name}`}
          tone="notice"
          className={className}
        >
          <p>
            The collection was recognised. Omnipus is reading it now, so anything you search for
            here is drawn from a partial index until this finishes.
          </p>

          {showRatio ? (
            // Total known: a real ratio, and a bar that carries a real value.
            <>
              <p data-testid="knowledge-index-progress-ratio">
                {state.indexedFiles} of {state.totalFiles} files indexed.
              </p>
              <div
                role="progressbar"
                aria-valuemin={0}
                aria-valuemax={state.totalFiles}
                aria-valuenow={state.indexedFiles}
                aria-label={`${state.indexedFiles} of ${state.totalFiles} files indexed`}
                className="h-1.5 w-full overflow-hidden rounded-full bg-[var(--color-surface-3)]"
              >
                <div
                  className="h-full bg-[var(--color-accent)] transition-all duration-500"
                  style={{
                    width: `${Math.min(100, (state.indexedFiles / (state.totalFiles as number)) * 100)}%`,
                  }}
                />
              </div>
            </>
          ) : (
            // Total UNKNOWN. A count found so far and an indeterminate bar.
            // The bar deliberately fills the whole track and pulses: a
            // part-filled bar would be read as a fraction of a total nobody
            // has. aria-valuenow is OMITTED, which is what makes this an
            // indeterminate progressbar to assistive technology rather than
            // one silently reporting zero.
            <>
              <p data-testid="knowledge-index-progress-indeterminate">
                {state.indexedFiles} {state.indexedFiles === 1 ? 'file' : 'files'} indexed so far.
                Omnipus is still counting the collection, so the total is not yet known.
              </p>
              <div
                role="progressbar"
                aria-valuemin={0}
                aria-label="Indexing — total not yet known"
                aria-valuetext="Total not yet known"
                className="h-1.5 w-full overflow-hidden rounded-full bg-[var(--color-surface-3)]"
              >
                <div className="h-full w-full animate-pulse bg-[var(--color-accent)]" />
              </div>
            </>
          )}

          <SkippedFiles count={state.skippedFiles} />
        </Shell>
      )
    }

    // ── Indexing failed ──────────────────────────────────────────────────────
    case 'index_failed':
      return (
        <section
          data-testid="knowledge-state-index-failed"
          role="alert"
          className={cn(
            'flex items-start gap-3 rounded-lg border border-[var(--color-error)]/40 bg-[var(--color-error)]/10 px-4 py-3',
            className,
          )}
        >
          <Warning
            size={18}
            weight="fill"
            aria-hidden="true"
            className="mt-0.5 shrink-0 text-[var(--color-error)]"
          />
          <div className="min-w-0 flex-1">
            <h3 className="text-sm font-semibold text-[var(--color-error)]">
              Indexing {state.name} stopped
            </h3>
            <div className="mt-1 space-y-2 text-xs leading-relaxed text-[var(--color-error)]/90">
              <p data-testid="knowledge-index-failed-message" className="font-mono break-all">
                {state.message}
              </p>
              <p>
                {state.indexedFiles} {state.indexedFiles === 1 ? 'file was' : 'files were'} indexed
                before it stopped. Search still works over those, and only those — treat every
                result here as incomplete.
              </p>
            </div>
          </div>
        </section>
      )

    // ── Recognised, but nothing has told us how far indexing has got ─────────
    case 'index_status_unknown':
      return (
        <Shell
          testId="knowledge-state-index-status-unknown"
          icon={<Books size={18} weight="fill" />}
          title={state.name}
          className={className}
        >
          <p>
            This folder is a knowledge base. No indexing progress has arrived since you opened it,
            so Omnipus cannot tell you here how far the index has got. Each set of search results
            below states its own completeness, which is the answer to trust.
          </p>
        </Shell>
      )

    // ── (c) An empty collection — a real state, not a fault ──────────────────
    case 'empty_collection':
      return (
        <Shell
          testId="knowledge-state-empty"
          icon={<FileText size={18} />}
          title={`${state.name} is empty`}
          className={className}
        >
          <p>
            This is a knowledge base with no notes in it yet. Nothing is wrong — there is simply
            nothing to read or search until a note exists.
          </p>
          {onCreateNote ? (
            <button
              type="button"
              tabIndex={0}
              data-testid="knowledge-create-note"
              onClick={onCreateNote}
              className="inline-flex h-8 items-center gap-2 rounded-md bg-[var(--color-accent)] px-3 text-xs font-semibold text-[var(--color-primary)] transition-colors hover:bg-[var(--color-accent-hover)]"
            >
              <FileText size={14} weight="fill" aria-hidden="true" />
              Write the first note
            </button>
          ) : (
            <p data-testid="knowledge-create-note-unavailable">
              Add or upload a markdown file to this folder and Omnipus will index it automatically.
            </p>
          )}
          <SkippedFiles count={state.skippedFiles} />
        </Shell>
      )
  }
}
