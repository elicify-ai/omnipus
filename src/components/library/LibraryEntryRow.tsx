// LibraryEntryRow — one row in the Library explorer's directory listing
// (library-spec.md D-2). A directory row navigates on click; a file row
// selects itself (opens the details strip in LibraryExplorer — see that
// file's placeholder-slot comment). Every action in the row's menu (Download
// / Rename / Move-or-copy / Delete) is real — wired straight to the Library
// REST endpoints via the callbacks LibraryExplorer passes down.

import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import {
  DotsThree,
  DownloadSimple,
  PencilSimple,
  ArrowsLeftRight,
  Trash,
  Eye,
  LinkBreak,
} from '@phosphor-icons/react'
import { fileTypeMeta } from '@/components/chat/AttachmentCard'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { formatRelative } from '@/lib/formatRelative'
import { libraryDownloadUrl } from '@/lib/api'
import { classifyLibraryEntry } from './preview/libraryPreviewKind'
import { cn } from '@/lib/utils'
import type { LibraryEntry } from '@/lib/api'
import type { KnowledgeBaseInfo } from '@/lib/api/generated/openapi-types'
import { VaultIcon, FolderIcon, MountIcon } from './icons'

/** Format a byte count as a compact human-readable size. */
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
  /** Needed to build the media URL for inline image/video thumbnails. */
  workspaceId: string
  entry: LibraryEntry
  selected: boolean
  onOpenDirectory: (entry: LibraryEntry) => void
  onSelectFile: (entry: LibraryEntry) => void
  onDownload: (entry: LibraryEntry) => void
  onRename: (entry: LibraryEntry) => void
  onTransfer: (entry: LibraryEntry, mode: 'move' | 'copy') => void
  onDelete: (entry: LibraryEntry) => void
  /**
   * Revoke a mount. Optional because only a mounted row can produce it, and
   * absent means the row simply offers no unmount action.
   */
  onUnmount?: (entry: LibraryEntry) => void
}

export function LibraryEntryRow({
  workspaceId,
  entry,
  selected,
  onOpenDirectory,
  onSelectFile,
  onDownload,
  onRename,
  onTransfer,
  onDelete,
  onUnmount,
}: LibraryEntryRowProps) {
  const queryClient = useQueryClient()

  // A mount is a real folder on the operator's machine, not workspace storage.
  // It must never borrow the gold vault/folder icon: gold means "yours,
  // inside the workspace", and a write inside a mount lands on their actual
  // disk. Broad grants (home directory, filesystem root) shift to the
  // warning colour so "you mounted your whole home folder" is legible
  // without opening anything — that safety escalation predates, and is kept
  // alongside, the locked icon system's default Mount colour
  // (`--color-mount`, docs/internal/specs/library-b-c-design-2026-09-07.md
  // §"Icon system — LOCKED").
  const mount = entry.mount
  const mountColor = mount?.broad ? 'var(--color-warning)' : 'var(--color-mount)'

  // Vault detection (C3): is this directory itself a knowledge base? There is
  // no bulk "which of these children are vaults" endpoint — KnowledgeBaseInfo
  // is answered per folder (GET /library/{ws}/knowledge?path=…), and
  // KnowledgePanel already asks it for whichever folder LibraryExplorer is
  // CURRENTLY browsing, caching the answer under
  // ['knowledge-base-info', workspaceId, path] (see KnowledgePanel.tsx).
  // Reading that cache here is a passive lookup — it fires no request of its
  // own — so a directory the operator has already opened at least once this
  // session (as browsedDir, or via "New vault") shows its true Vault icon
  // retroactively when its PARENT listing renders; one never yet opened
  // falls back to the plain Folder icon, which is the honest default rather
  // than a guess. Explicitly NOT a per-row network probe (C3's requirement).
  const cachedKnowledgeInfo = entry.is_dir
    ? queryClient.getQueryData<KnowledgeBaseInfo>(['knowledge-base-info', workspaceId, entry.path])
    : undefined
  const isVault = cachedKnowledgeInfo?.is_knowledge_base === true

  const containerIcon = mount
    ? { Icon: MountIcon, color: mountColor }
    : entry.is_dir
      ? isVault
        ? { Icon: VaultIcon, color: 'var(--color-accent)' }
        : { Icon: FolderIcon, color: 'var(--color-muted)' }
      : null
  const fileMeta = containerIcon ? null : fileTypeMeta(entry.name, entry.mime)
  const color = containerIcon?.color ?? (fileMeta as NonNullable<typeof fileMeta>).color
  const kind = entry.is_dir ? 'other' : classifyLibraryEntry(entry)
  const isMedia = kind === 'image' || kind === 'video'
  // Falls back to the generic type icon if the media itself won't load, so a
  // broken or unreadable file degrades to exactly what it looked like before
  // rather than an empty box.
  const [thumbFailed, setThumbFailed] = useState(false)
  const showThumb = isMedia && !thumbFailed
  const thumbSrc = isMedia ? libraryDownloadUrl(workspaceId, entry.path) : ''

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
      {/* Inline media preview (operator direction, 2026-08-04: "images and
          videos should be previewed also inline in the file list itself").
          The real frame replaces the generic type glyph IN PLACE, so rows keep
          a single uniform height and the list stays scannable however many
          files it holds. Lazy-loaded, and `preload="metadata"` on video fetches
          a frame rather than the whole file — a directory of large videos must
          not become a directory of large downloads just by being listed. */}
      <div
        className="shrink-0 w-8 h-8 rounded-md flex items-center justify-center overflow-hidden"
        style={
          showThumb
            ? undefined
            : // `color-mix()` (not string-concatenating an alpha suffix onto
              // `color`): the container icons' colours are CSS custom
              // properties (`var(--color-accent)` etc.), and
              // `` `${color}22` `` — the old formula — produces the
              // syntactically invalid `"var(--color-accent)22"`, which the
              // browser silently drops, leaving no background tint at all.
              // `color-mix` works uniformly whether `color` is a var() or
              // (for file rows, via fileTypeMeta) a literal hex string.
              { backgroundColor: `color-mix(in srgb, ${color} 15%, transparent)`, color }
        }
        aria-hidden="true"
      >
        {showThumb ? (
          kind === 'image' ? (
            <img
              src={thumbSrc}
              alt=""
              loading="lazy"
              decoding="async"
              onError={() => setThumbFailed(true)}
              data-testid={`library-thumb-${entry.path}`}
              className="h-full w-full object-cover"
            />
          ) : (
            <video
              src={`${thumbSrc}#t=0.1`}
              muted
              playsInline
              preload="metadata"
              onError={() => setThumbFailed(true)}
              data-testid={`library-thumb-${entry.path}`}
              className="h-full w-full object-cover"
            />
          )
        ) : containerIcon ? (
          // The locked custom set (Workspace/Vault/Folder/Mount) already
          // encodes its own fill-vs-outline treatment per kind — no `weight`
          // prop to plumb through, unlike the Phosphor file icons below.
          <containerIcon.Icon size={18} />
        ) : (
          fileMeta && <fileMeta.Icon size={18} weight="regular" />
        )}
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
          {mount && (
            <span
              data-testid={`library-mount-badge-${entry.path}`}
              className={`shrink-0 text-[9px] uppercase tracking-wide px-1 py-0.5 rounded border ${
                mount.broad
                  ? 'border-[var(--color-warning)] text-[var(--color-warning)]'
                  : 'border-[var(--color-info)] text-[var(--color-info)]'
              }`}
            >
              {mount.broad ? 'Broad grant' : 'Mounted'}
            </span>
          )}
        </div>
        {/* The real destination is shown in the row, not behind a tooltip: it
            is the entire reason this entry behaves differently, and a grant the
            operator cannot see is a grant they cannot review. */}
        {mount ? (
          <p
            data-testid={`library-mount-target-${entry.path}`}
            className="truncate mt-0.5 text-[11px] font-mono text-[var(--color-muted)]"
            title={mount.host_path}
          >
            {mount.host_path}
            {mount.broad && ' — covers your entire home folder'}
          </p>
        ) : (
          <div className="flex items-center gap-1.5 mt-0.5 text-[11px] text-[var(--color-muted)]">
            <span>{entry.is_dir ? '—' : formatLibrarySize(entry.size)}</span>
            <span aria-hidden="true">·</span>
            <span title={entry.modified_at}>{formatRelative(entry.modified_at)}</span>
          </div>
        )}
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
          {/* A mount's menu deliberately differs. On the row above, Delete
              removes a working file; on a mounted row the same word sits over
              the operator's real repository. Move/Copy of the grant itself have
              no meaning, so they are not offered at all rather than shown and
              failing. */}
          {mount ? (
            <DropdownMenuItem
              onSelect={() => onUnmount?.(entry)}
              data-testid={`library-row-unmount-${entry.path}`}
              className="flex items-center gap-2 text-[var(--color-info)]"
            >
              <LinkBreak size={14} /> Unmount
              <span className="ml-auto text-[11px] text-[var(--color-muted)]">files stay</span>
            </DropdownMenuItem>
          ) : (
            <>
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
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
