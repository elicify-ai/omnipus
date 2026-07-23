// ADR-051 Rev 4 (Slice H) — Workspace Media library tab.
//
// Lists the workspace's persistent media-library entries (GET
// /workspaces/{id}/media) and supports explicit single-file delete (FR-008,
// DELETE /workspaces/{id}/media/{media_id}). Raw upload bytes live on disk
// under workspaces/<ws>/media/; this surface shows the manifest metadata
// (filename, mime, size, sha256, uploaded_at, source, refcount) and never
// fetches bytes until an entry is opened.
//
// Wire types are the generated MediaLibraryEntry (contract-first #8). The list
// is server-authoritative via TanStack Query; delete is a useMutation that
// invalidates the list query on success.

import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  FolderOpen,
  Trash,
  SpinnerGap,
  WarningCircle,
  Image as ImageIcon,
} from '@phosphor-icons/react'
import {
  fetchWorkspaceMedia,
  deleteWorkspaceMedia,
  workspacesQueryKeys,
} from '@/lib/api'
import type { MediaLibraryEntry } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { fileTypeMeta } from '@/components/chat/AttachmentCard'
import { formatRelative } from '@/lib/formatRelative'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

/** Format a byte count as a compact human-readable size. */
function formatBytes(bytes: number): string {
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

interface WorkspaceMediaTabProps {
  workspaceId: string
}

export function WorkspaceMediaTab({ workspaceId }: WorkspaceMediaTabProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [pendingDelete, setPendingDelete] = useState<MediaLibraryEntry | null>(null)

  const queryKey = workspacesQueryKeys.media(workspaceId)
  const { data: entries = [], isLoading, isError } = useQuery({
    queryKey,
    queryFn: () => fetchWorkspaceMedia(workspaceId),
    staleTime: 15_000,
  })

  const deleteMutation = useMutation({
    mutationFn: (mediaId: string) => deleteWorkspaceMedia(workspaceId, mediaId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey })
      addToast({ message: 'File deleted from the media library.', variant: 'success' })
    },
    onError: (err) => {
      const msg = err instanceof Error ? err.message : String(err)
      addToast({ message: `Delete failed: ${msg}`, variant: 'error' })
    },
  })

  function confirmDelete() {
    if (!pendingDelete) return
    deleteMutation.mutate(pendingDelete.id)
    setPendingDelete(null)
  }

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-full text-[var(--color-muted)] text-sm gap-2">
        <SpinnerGap size={16} className="animate-spin" />
        Loading media library…
      </div>
    )
  }

  if (isError) {
    return (
      <div
        className="flex flex-col items-center justify-center h-full gap-2 p-8 text-center text-[var(--color-muted)] text-sm"
        data-testid="workspace-media-error"
      >
        <WarningCircle size={24} className="text-[var(--color-error)]" />
        <p>Couldn’t load the media library.</p>
        <p className="text-xs">Check your connection and reopen the tab.</p>
      </div>
    )
  }

  if (entries.length === 0) {
    return (
      <div
        className="flex flex-col items-center justify-center h-full gap-3 p-8 text-center"
        data-testid="workspace-media-empty"
      >
        <FolderOpen size={32} className="text-[var(--color-muted)]" />
        <p className="text-sm text-[var(--color-secondary)]">No files in this workspace yet.</p>
        <p className="text-xs text-[var(--color-muted)] max-w-sm">
          Files you upload in chat are stored here and can be reused in later conversations without re-uploading.
        </p>
      </div>
    )
  }

  return (
    <div className="h-full overflow-y-auto" data-testid="workspace-media-tab">
      <div className="mx-auto max-w-4xl px-4 py-6 sm:px-6">
        <header className="mb-4">
          <h1 className="font-headline text-lg font-semibold text-[var(--color-secondary)]">
            Media library
          </h1>
          <p className="text-xs text-[var(--color-muted)] mt-0.5">
            {entries.length} file{entries.length === 1 ? '' : 's'} · uploaded files persist across conversations in this workspace
          </p>
        </header>

        <ul className="flex flex-col gap-2" role="list">
          {entries.map((entry) => (
            <MediaRow
              key={entry.id}
              entry={entry}
              onDelete={() => setPendingDelete(entry)}
            />
          ))}
        </ul>
      </div>

      <AlertDialog open={pendingDelete !== null} onOpenChange={(open) => !open && setPendingDelete(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete file from media library?</AlertDialogTitle>
            <AlertDialogDescription>
              {pendingDelete
                ? `“${pendingDelete.filename}” will be permanently removed from this workspace. This cannot be undone.`
                : ''}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={confirmDelete}
              disabled={deleteMutation.isPending}
              data-testid="media-delete-confirm"
            >
              {deleteMutation.isPending ? 'Deleting…' : 'Delete'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

interface MediaRowProps {
  entry: MediaLibraryEntry
  onDelete: () => void
}

function MediaRow({ entry, onDelete }: MediaRowProps) {
  const { Icon, color, label } = fileTypeMeta(entry.filename, entry.mime)
  return (
    <li
      className="flex items-center gap-3 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2.5"
      data-testid={`media-row-${entry.id}`}
    >
      <div
        className="shrink-0 w-9 h-9 rounded-md flex items-center justify-center"
        style={{ backgroundColor: `${color}22`, color }}
        aria-hidden="true"
      >
        <Icon size={20} weight="fill" />
      </div>

      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 min-w-0">
          <p className="truncate text-sm font-medium text-[var(--color-secondary)]" title={entry.filename}>
            {entry.filename}
          </p>
          <span className="shrink-0 text-[10px] uppercase tracking-wide text-[var(--color-muted)]">
            {label}
          </span>
        </div>
        <div className="flex items-center gap-2 mt-0.5 text-[11px] text-[var(--color-muted)]">
          <span>{formatBytes(entry.size)}</span>
          <span aria-hidden="true">·</span>
          <span title={entry.uploaded_at}>{formatRelative(entry.uploaded_at)}</span>
          {entry.source === 'tool_output' && (
            <>
              <span aria-hidden="true">·</span>
              <span className="inline-flex items-center gap-1">
                <ImageIcon size={11} /> tool output
              </span>
            </>
          )}
        </div>
      </div>

      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={onDelete}
        aria-label={`Delete ${entry.filename}`}
        title="Delete"
        className={cn('shrink-0 text-[var(--color-muted)] hover:text-[var(--color-error)]')}
        data-testid={`media-delete-${entry.id}`}
      >
        <Trash size={16} />
      </Button>
    </li>
  )
}
