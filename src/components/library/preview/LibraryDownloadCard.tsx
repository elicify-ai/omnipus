// LibraryDownloadCard — the "everything else" fallback (library-spec.md
// section 4: "a metadata card plus a Download action. Do not pretend to
// preview a binary."). Also the honest fallback when GET .../content reports
// `is_text: false` or `too_large: true` despite the entry's extension looking
// editable — see LibraryPreviewPane, which is the sole place that decides
// when to render this vs. an actual preview.

import { DownloadSimple } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { fileTypeMeta } from '@/components/chat/AttachmentCard'
import { formatRelative } from '@/lib/formatRelative'
import { formatLibrarySize } from '../LibraryEntryRow'
import type { LibraryEntry } from '@/lib/api'

export type LibraryDownloadReason = 'binary' | 'too_large' | 'unsupported'

const REASON_COPY: Record<LibraryDownloadReason, string> = {
  binary: "This file isn't text — download it to view it in the right application.",
  too_large: 'This file is too large to preview inline. Download it instead.',
  unsupported: "There's no inline preview for this file type. Download it to view it.",
}

interface LibraryDownloadCardProps {
  entry: LibraryEntry
  reason: LibraryDownloadReason
  onDownload: (entry: LibraryEntry) => void
}

export function LibraryDownloadCard({ entry, reason, onDownload }: LibraryDownloadCardProps) {
  const { Icon, color, label } = fileTypeMeta(entry.name, entry.mime)
  return (
    <div
      className="flex flex-1 min-h-0 flex-col items-center justify-center gap-4 overflow-auto p-8 text-center"
      data-testid="library-download-card"
    >
      <div
        className="flex h-16 w-16 shrink-0 items-center justify-center rounded-xl"
        style={{ backgroundColor: `${color}22`, color }}
        aria-hidden="true"
      >
        <Icon size={32} weight="fill" />
      </div>
      <div>
        <p className="text-sm font-medium text-[var(--color-secondary)]" title={entry.name}>
          {entry.name}
        </p>
        <p className="mt-1 text-xs text-[var(--color-muted)]">
          {label} · {formatLibrarySize(entry.size)} · modified {formatRelative(entry.modified_at)}
        </p>
      </div>
      <p className="max-w-xs text-xs text-[var(--color-muted)]">{REASON_COPY[reason]}</p>
      <Button size="sm" onClick={() => onDownload(entry)} data-testid="library-download-card-button" className="gap-1.5">
        <DownloadSimple size={14} /> Download
      </Button>
    </div>
  )
}
