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
import { DownloadSimple, PencilSimple, ArrowsLeftRight, Trash, X, SpinnerGap } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { QueryErrorState } from '@/components/shared/QueryErrorState'
import { fileTypeMeta } from '@/components/chat/AttachmentCard'
import { formatRelative } from '@/lib/formatRelative'
import { formatLibrarySize } from './LibraryEntryRow'
import { fetchLibraryContent, libraryQueryKeys } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'
import { classifyLibraryEntry } from './preview/libraryPreviewKind'
import { LibraryImagePreview } from './preview/LibraryImagePreview'
import { LibraryVideoPreview } from './preview/LibraryVideoPreview'
import { LibraryMarkdownPreview } from './preview/LibraryMarkdownPreview'
import { LibraryMermaidPreview } from './preview/LibraryMermaidPreview'
import { LibraryCodePreview } from './preview/LibraryCodePreview'
import { LibraryDownloadCard } from './preview/LibraryDownloadCard'

export interface LibraryPreviewPaneProps {
  workspaceId: string
  entry: LibraryEntry
  onClose: () => void
  onDownload: (entry: LibraryEntry) => void
  onRename: () => void
  onTransfer: (mode: 'move' | 'copy') => void
  onDelete: () => void
}

export function LibraryPreviewPane({
  workspaceId,
  entry,
  onClose,
  onDownload,
  onRename,
  onTransfer,
  onDelete,
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

  const { Icon, color } = fileTypeMeta(liveEntry.name, liveEntry.mime)

  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="library-preview-pane">
      {/* Header — entry identity + the same real actions the prior details
          strip wired (Download/Rename/Move/Delete), plus Close. */}
      <div className="flex shrink-0 items-start gap-3 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] p-3">
        <div
          className="flex h-10 w-10 shrink-0 items-center justify-center rounded-md"
          style={{ backgroundColor: `${color}22`, color }}
          aria-hidden="true"
        >
          <Icon size={20} weight="fill" />
        </div>
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium text-[var(--color-secondary)]" title={liveEntry.name}>
            {liveEntry.name}
          </p>
          <p className="mt-0.5 text-xs text-[var(--color-muted)]">
            {formatLibrarySize(liveEntry.size)} · modified {formatRelative(liveEntry.modified_at)}
            {liveEntry.is_text_editable && ' · text'}
          </p>
        </div>
        <button
          type="button"
          tabIndex={0}
          onClick={onClose}
          aria-label="Close preview"
          data-testid="library-preview-close"
          className="shrink-0 rounded p-1 text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]"
        >
          <X size={14} />
        </button>
      </div>
      <div className="flex shrink-0 items-center gap-1.5 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 pb-2.5">
        <Button variant="outline" size="sm" onClick={() => onDownload(liveEntry)} className="gap-1.5 text-xs">
          <DownloadSimple size={13} /> Download
        </Button>
        <Button variant="outline" size="sm" onClick={onRename} className="gap-1.5 text-xs">
          <PencilSimple size={13} /> Rename
        </Button>
        <Button variant="outline" size="sm" onClick={() => onTransfer('move')} className="gap-1.5 text-xs">
          <ArrowsLeftRight size={13} /> Move…
        </Button>
        <Button
          variant="ghost"
          size="sm"
          onClick={onDelete}
          className="ml-auto gap-1.5 text-xs text-[var(--color-muted)] hover:text-[var(--color-error)]"
        >
          <Trash size={13} /> Delete
        </Button>
      </div>

      {/* Body — the actual preview/edit surface. */}
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
