// LibraryEntryRow — one row in the Library explorer's directory listing
// (library-spec.md D-2). A directory row navigates on click; a file row
// selects itself (opens the details strip in LibraryExplorer — see that
// file's placeholder-slot comment). Every action in the row's menu (Download
// / Rename / Move-or-copy / Delete) is real — wired straight to the Library
// REST endpoints via the callbacks LibraryExplorer passes down.

import { Folder, DotsThree, DownloadSimple, PencilSimple, ArrowsLeftRight, Trash, Eye } from '@phosphor-icons/react'
import { fileTypeMeta } from '@/components/chat/AttachmentCard'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { formatRelative } from '@/lib/formatRelative'
import { cn } from '@/lib/utils'
import type { LibraryEntry } from '@/lib/api'

/** Format a byte count as a compact human-readable size (mirrors WorkspaceMediaTab's formatBytes). */
export function formatLibrarySize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '—'
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let value = bytes / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value.toFixed(value >= 10 ? 0 : 1)} ${units[unit]}`
}

interface LibraryEntryRowProps {
  entry: LibraryEntry
  selected: boolean
  onOpenDirectory: (entry: LibraryEntry) => void
  onSelectFile: (entry: LibraryEntry) => void
  onDownload: (entry: LibraryEntry) => void
  onRename: (entry: LibraryEntry) => void
  onTransfer: (entry: LibraryEntry, mode: 'move' | 'copy') => void
  onDelete: (entry: LibraryEntry) => void
}

export function LibraryEntryRow({
  entry,
  selected,
  onOpenDirectory,
  onSelectFile,
  onDownload,
  onRename,
  onTransfer,
  onDelete,
}: LibraryEntryRowProps) {
  const { Icon, color } = entry.is_dir ? { Icon: Folder, color: '#D4AF37' } : fileTypeMeta(entry.name, entry.mime)

  function handleActivate() {
    if (entry.is_dir) onOpenDirectory(entry)
    else onSelectFile(entry)
  }

  return (
    <div
      role="button"
      tabIndex={0}
      data-testid={`library-row-${entry.path}`}
      onClick={handleActivate}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          handleActivate()
        }
      }}
      aria-current={selected ? 'true' : undefined}
      className={cn(
        'flex items-center gap-3 rounded-lg px-3 py-2 cursor-pointer transition-colors border border-transparent',
        selected
          ? 'bg-[var(--color-surface-2)] border-[var(--color-accent)]/40'
          : 'hover:bg-[var(--color-surface-2)]',
        // D-8: hidden entries must read as visually distinct even when the
        // Show Hidden toggle reveals them — dimmed, in addition to the
        // "hidden" badge below.
        entry.is_hidden && 'opacity-60',
      )}
    >
      <div
        className="shrink-0 w-8 h-8 rounded-md flex items-center justify-center"
        style={{ backgroundColor: `${color}22`, color }}
        aria-hidden="true"
      >
        <Icon size={18} weight={entry.is_dir ? 'fill' : 'regular'} />
      </div>

      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5 min-w-0">
          <p className="truncate text-sm text-[var(--color-secondary)]" title={entry.name}>
            {entry.name}
          </p>
          {entry.is_hidden && (
            <span
              className="shrink-0 text-[9px] uppercase tracking-wide px-1 py-0.5 rounded bg-[var(--color-surface-3)] text-[var(--color-muted)]"
              data-testid={`library-hidden-badge-${entry.path}`}
            >
              hidden
            </span>
          )}
        </div>
        <div className="flex items-center gap-1.5 mt-0.5 text-[11px] text-[var(--color-muted)]">
          <span>{entry.is_dir ? '—' : formatLibrarySize(entry.size)}</span>
          <span aria-hidden="true">·</span>
          <span title={entry.modified_at}>{formatRelative(entry.modified_at)}</span>
        </div>
      </div>

      {/* Row action menu — stop propagation so opening it doesn't also
          trigger the row's own onClick (navigate/select). */}
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            tabIndex={0}
            aria-label={`Actions for ${entry.name}`}
            data-testid={`library-row-menu-${entry.path}`}
            onClick={(e) => e.stopPropagation()}
            className="shrink-0 rounded p-1.5 text-[var(--color-muted)] hover:bg-[var(--color-surface-3)] hover:text-[var(--color-secondary)] transition-colors"
          >
            <DotsThree size={18} weight="bold" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
          {!entry.is_dir && (
            <DropdownMenuItem onSelect={() => onSelectFile(entry)} className="flex items-center gap-2">
              <Eye size={14} /> Details
            </DropdownMenuItem>
          )}
          {!entry.is_dir && (
            <DropdownMenuItem onSelect={() => onDownload(entry)} className="flex items-center gap-2">
              <DownloadSimple size={14} /> Download
            </DropdownMenuItem>
          )}
          <DropdownMenuItem onSelect={() => onRename(entry)} className="flex items-center gap-2">
            <PencilSimple size={14} /> Rename
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => onTransfer(entry, 'move')} className="flex items-center gap-2">
            <ArrowsLeftRight size={14} /> Move…
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => onTransfer(entry, 'copy')} className="flex items-center gap-2">
            <ArrowsLeftRight size={14} /> Copy…
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onSelect={() => onDelete(entry)}
            data-testid={`library-row-delete-${entry.path}`}
            className="flex items-center gap-2 text-[var(--color-error)]"
          >
            <Trash size={14} /> Delete
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
