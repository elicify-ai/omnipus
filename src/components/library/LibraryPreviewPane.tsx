// LibraryPreviewPane — the Library preview + edit pane (library-spec.md D-5 /
// section 4). Mounted by LibraryExplorer.tsx into its
// "PREVIEW/EDIT PANE PLACEHOLDER" slot, replacing the narrower
// metadata-only details strip that shipped ahead of this task.
//
// Owns: the entry header (icon/name/meta + the existing
// Download/Rename/Move/Delete/Close actions — unchanged from the prior
// strip), and the actual content surface, chosen by `classifyLibraryEntry`:
//   image/video  -> plain <img>/<video controls>, no content fetch needed
//                   (the raw download URL IS the media source).
//   markdown     -> LibraryMarkdownPreview (HistoricalMessageMarkdown + Mermaid)
//   mermaid      -> LibraryMermaidPreview (MermaidDiagram)
//   text         -> LibraryCodePreview (ShikiCodeBlock + CodeMirror)
//   other        -> LibraryDownloadCard
//
// For the three text kinds, GET .../content's `is_text`/`too_large` flags are
// the AUTHORITATIVE check (LibraryEntry.is_text_editable, used to reach
// "text" in the first place, is only a best-effort listing-time hint per its
// own schema doc) — either flag failing falls back to LibraryDownloadCard
// rather than rendering garbage, per this task's wiring requirement.

import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { X, SpinnerGap } from '@phosphor-icons/react'
import { QueryErrorState } from '@/components/shared/QueryErrorState'
import { fetchLibraryContent, libraryQueryKeys } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'
import { classifyLibraryEntry } from './preview/libraryPreviewKind'
import { LibraryImagePreview } from './preview/LibraryImagePreview'
import { LibraryVideoPreview } from './preview/LibraryVideoPreview'
import { LibraryMarkdownPreview } from './preview/LibraryMarkdownPreview'
import { LibraryMermaidPreview } from './preview/LibraryMermaidPreview'
import { LibraryCodePreview } from './preview/LibraryCodePreview'
import { LibraryDownloadCard } from './preview/LibraryDownloadCard'
import { PreviewHeaderSlotProvider } from './preview/previewHeaderSlot'

// Every control in the single header row is a bare icon, matching the browser
// panel's toolbar treatment: no border, no fill, hover as the only chrome.
export const LIBRARY_ICON_BTN =
  'flex h-7 w-7 shrink-0 items-center justify-center rounded transition-colors ' +
  'text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] ' +
  'disabled:cursor-not-allowed disabled:opacity-40 ' +
  'pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]'

export interface LibraryPreviewPaneProps {
  workspaceId: string
  entry: LibraryEntry
  onClose: () => void
  /** Still required: the download CARD (too-large / binary / unsupported
   *  fallbacks) is the only remaining download affordance inside the pane. */
  onDownload: (entry: LibraryEntry) => void
}

export function LibraryPreviewPane({
  workspaceId,
  entry,
  onClose,
  onDownload,
}: LibraryPreviewPaneProps) {
  const kind = classifyLibraryEntry(entry)
  const needsContent = kind === 'markdown' || kind === 'mermaid' || kind === 'text'

  // Tracks the entry's OWN display metadata (size/modified) so a successful
  // save updates the header immediately without waiting for the parent's
  // entries-list refetch to flow a fresh `entry` prop back down.
  const [liveEntry, setLiveEntry] = useState(entry)
  useEffect(() => {
    setLiveEntry(entry)
  }, [entry])

  const contentQuery = useQuery({
    queryKey: libraryQueryKeys.content(workspaceId, entry.path),
    queryFn: () => fetchLibraryContent(workspaceId, entry.path),
    enabled: needsContent,
    staleTime: 10_000,
  })

  const [headerSlot, setHeaderSlot] = useState<HTMLDivElement | null>(null)

  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="library-preview-pane">
      {/* ONE header row (operator direction, 2026-08-04). Was three: a tall
          identity strip (40px type icon, name, size, modified), a row of
          outline buttons (Download / Rename / Move / Delete), and the
          view/edit/save toolbar inside the body — roughly 130px of chrome
          above a pane that already only gets half the panel's height.

          The four actions were not moved, they were REMOVED: every entry row
          already carries the identical set in its own DotsThree menu
          (LibraryEntryRow), so the strip was a second copy of a menu the user
          had just used to open the file. Size/modified went with it — the list
          row shows both, one click away.

          Close stays. It is the ONLY way to dismiss the pane (LibraryExplorer
          clears selectedEntry from here and from destructive mutations, not
          from a second click on the row), so dropping it would strand an open
          preview. It is an icon like the rest.

          The middle slot is filled by whichever body mounts an editor — see
          previewHeaderSlot for why that is a portal and not lifted state. */}
      <div className="flex h-9 shrink-0 items-center gap-2 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-2">
        <p
          className="min-w-0 flex-1 truncate text-xs font-medium text-[var(--color-secondary)]"
          title={liveEntry.name}
          data-testid="library-preview-title"
        >
          {liveEntry.name}
        </p>
        <div ref={setHeaderSlot} className="flex shrink-0 items-center gap-0.5" />
        <button
          type="button"
          tabIndex={0}
          onClick={onClose}
          aria-label="Close preview"
          title="Close preview"
          data-testid="library-preview-close"
          className={LIBRARY_ICON_BTN}
        >
          <X size={15} />
        </button>
      </div>

      {/* Body — the actual preview/edit surface. */}
      <PreviewHeaderSlotProvider slot={headerSlot}>
      <div className="flex flex-1 min-h-0 flex-col overflow-hidden">
        {kind === 'image' && <LibraryImagePreview workspaceId={workspaceId} entry={liveEntry} />}
        {kind === 'video' && <LibraryVideoPreview workspaceId={workspaceId} entry={liveEntry} />}

        {needsContent && contentQuery.isLoading && (
          <div className="flex flex-1 items-center justify-center gap-2 text-xs text-[var(--color-muted)]">
            <SpinnerGap size={16} className="animate-spin" /> Loading file…
          </div>
        )}
        {needsContent && contentQuery.isError && (
          <QueryErrorState
            layout="fill"
            message="Could not load this file."
            onRetry={() => void contentQuery.refetch()}
            testId="library-content-error"
          />
        )}
        {needsContent && contentQuery.isSuccess && (
          <LibraryTextBody
            workspaceId={workspaceId}
            entry={liveEntry}
            kind={kind as 'markdown' | 'mermaid' | 'text'}
            content={contentQuery.data}
            onDownload={onDownload}
            onSaved={setLiveEntry}
          />
        )}

        {kind === 'other' && <LibraryDownloadCard entry={liveEntry} reason="unsupported" onDownload={onDownload} />}
      </div>
      </PreviewHeaderSlotProvider>
    </div>
  )
}

// Split out so the "honour is_text/too_large" branch reads as one clear
// decision rather than nesting inside the parent's already-busy render body.
function LibraryTextBody({
  workspaceId,
  entry,
  kind,
  content,
  onDownload,
  onSaved,
}: {
  workspaceId: string
  entry: LibraryEntry
  kind: 'markdown' | 'mermaid' | 'text'
  content: { content?: string; is_text: boolean; too_large: boolean }
  onDownload: (entry: LibraryEntry) => void
  onSaved: (entry: LibraryEntry) => void
}) {
  if (content.too_large) {
    return <LibraryDownloadCard entry={entry} reason="too_large" onDownload={onDownload} />
  }
  if (!content.is_text || content.content === undefined) {
    return <LibraryDownloadCard entry={entry} reason="binary" onDownload={onDownload} />
  }
  const text = content.content
  if (kind === 'markdown') {
    return <LibraryMarkdownPreview workspaceId={workspaceId} entry={entry} content={text} onSaved={onSaved} />
  }
  if (kind === 'mermaid') {
    return <LibraryMermaidPreview workspaceId={workspaceId} entry={entry} content={text} onSaved={onSaved} />
  }
  return <LibraryCodePreview workspaceId={workspaceId} entry={entry} content={text} onSaved={onSaved} />
}
