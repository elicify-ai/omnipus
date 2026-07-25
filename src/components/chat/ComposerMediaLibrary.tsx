// ADR-051 Rev 4 (Slice H) — composer integration for workspace media-library
// reuse (FR-022).
//
// Two pieces:
//   1. ComposerMediaLibraryButton — opens a dialog listing the active
//      workspace's media-library entries (GET /workspaces/{id}/media).
//      Selecting one calls attachWorkspaceMedia (POST .../media/attachments)
//      and stages the entry as a pending library attachment via
//      addLibraryAttachment (media://workspace/<ws>/<id> ref). omnipus-runtime
//      drains it on send — no re-upload, no File object.
//   2. LibraryAttachmentChips — renders the staged library attachments as
//      removable chips so the user sees what will be attached before sending.
//
// Both read the active workspace from the workspaces store; the library is a
// workspace-scoped concept, so the picker is only meaningful inside a
// workspace chat.
//
// Intentional divergence from the direct-upload path: this picker does NOT
// filter entries against attachment-adapter.ts's ACCEPT_LIST. That list gates
// *new* uploads through the SPA's file input/drag-drop; the workspace media
// library aggregates entries from every ingestion path — SPA uploads, channel
// deliveries (WhatsApp/Discord/etc.), agent-generated artifacts, sendfile —
// most of which never passed through ACCEPT_LIST to begin with. Reattaching
// an existing entry doesn't re-upload it, and the backend's per-turn media
// resolution (pkg/agent/loop_media.go::resolveMediaRefsWithOffload →
// buildDocumentInjection → docextract.Extract) already degrades ANY
// unextractable MIME type to an honest "content could not be extracted"
// notice rather than failing — the same graceful path non-SPA-origin entries
// already go through. Filtering this picker would just hide legitimately
// reusable library content the SPA itself never gated. If ACCEPT_LIST is ever
// promoted to a hard backend/provider constraint, revisit this.

import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { FolderOpen, SpinnerGap, WarningCircle, X, Check } from '@phosphor-icons/react'
import {
  fetchWorkspaceMedia,
  attachWorkspaceMedia,
  workspacesQueryKeys,
} from '@/lib/api'
import type { MediaLibraryEntry } from '@/lib/api'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useUiStore } from '@/store/ui'
import {
  addLibraryAttachment,
  removeLibraryAttachment,
  useLibraryAttachments,
  buildWorkspaceMediaRef,
} from '@/lib/library-attachment'
import { fileTypeMeta } from '@/components/chat/AttachmentCard'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import { cn } from '@/lib/utils'

/** Small byte formatter shared with the tab (kept local to avoid a new module). */
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

interface ComposerMediaLibraryButtonProps {
  /** Matches the composer's own attach-disabled gate (read-only session, etc.). */
  disabled?: boolean
  /** Tab order relative to the composer's other controls. */
  tabIndex?: number
}

export function ComposerMediaLibraryButton({ disabled, tabIndex }: ComposerMediaLibraryButtonProps) {
  const activeWorkspaceId = useWorkspacesStore((s) => s.activeWorkspaceId)
  const addToast = useUiStore((s) => s.addToast)
  const [open, setOpen] = useState(false)
  const [attachingId, setAttachingId] = useState<string | null>(null)

  const workspaceId = activeWorkspaceId ?? ''
  const enabled = !disabled && open && workspaceId !== ''

  const { data: entries = [], isLoading, isError } = useQuery({
    queryKey: workspacesQueryKeys.media(workspaceId),
    queryFn: () => fetchWorkspaceMedia(workspaceId),
    enabled,
    staleTime: 15_000,
  })

  async function handleSelect(entry: MediaLibraryEntry) {
    if (!workspaceId) return
    setAttachingId(entry.id)
    try {
      // FR-022: register the attachment server-side (POST .../media/attachments).
      // 204 No Content — the SPA threads the ref into the outgoing message.
      await attachWorkspaceMedia(workspaceId, entry.id)
      addLibraryAttachment({
        mediaId: entry.id,
        ref: buildWorkspaceMediaRef(workspaceId, entry.id),
        filename: entry.filename,
        contentType: entry.mime,
      })
      setOpen(false)
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      addToast({ message: `Couldn’t attach “${entry.filename}”: ${msg}`, variant: 'error' })
    } finally {
      setAttachingId(null)
    }
  }

  const buttonDisabled = disabled || !workspaceId

  return (
    <>
      <button
        type="button"
        disabled={buttonDisabled}
        tabIndex={tabIndex ?? 0}
        onClick={() => setOpen(true)}
        aria-label="Attach a file from the workspace library"
        title={workspaceId ? 'Attach from library' : 'No active workspace'}
        className={cn(
          'shrink-0 h-7 w-7 mb-1.5 rounded-full flex items-center justify-center',
          'text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-3)]',
          'transition-colors disabled:opacity-40 disabled:cursor-not-allowed',
        )}
        data-testid="composer-library-attach"
      >
        <FolderOpen size={16} />
      </button>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Attach from media library</DialogTitle>
            <DialogDescription>
              Reuse a file already in this workspace — it attaches without re-uploading.
            </DialogDescription>
          </DialogHeader>

          <div className="max-h-[50dvh] overflow-y-auto -mx-1 px-1">
            {isLoading && (
              <div className="flex items-center justify-center gap-2 py-8 text-sm text-[var(--color-muted)]">
                <SpinnerGap size={16} className="animate-spin" /> Loading library…
              </div>
            )}
            {isError && (
              <div className="flex flex-col items-center justify-center gap-2 py-8 text-sm text-[var(--color-muted)]">
                <WarningCircle size={20} className="text-[var(--color-error)]" />
                Couldn’t load the library.
              </div>
            )}
            {!isLoading && !isError && entries.length === 0 && (
              <div className="py-8 text-center text-sm text-[var(--color-muted)]">
                No files in this workspace yet. Upload one in chat first.
              </div>
            )}
            <ul className="flex flex-col gap-1" role="list">
              {entries.map((entry) => (
                <li key={entry.id}>
                  <button
                    type="button"
                    tabIndex={0}
                    onClick={() => handleSelect(entry)}
                    disabled={attachingId !== null}
                    className={cn(
                      'w-full flex items-center gap-3 rounded-md px-2.5 py-2 text-left',
                      'hover:bg-[var(--color-surface-2)] transition-colors disabled:opacity-60',
                    )}
                    data-testid={`library-pick-${entry.id}`}
                  >
                    <PickerRow entry={entry} busy={attachingId === entry.id} />
                  </button>
                </li>
              ))}
            </ul>
          </div>
        </DialogContent>
      </Dialog>
    </>
  )
}

function PickerRow({ entry, busy }: { entry: MediaLibraryEntry; busy: boolean }) {
  const { Icon, color } = fileTypeMeta(entry.filename, entry.mime)
  return (
    <>
      <div
        className="shrink-0 w-8 h-8 rounded-md flex items-center justify-center"
        style={{ backgroundColor: `${color}22`, color }}
        aria-hidden="true"
      >
        {busy ? <SpinnerGap size={16} className="animate-spin" /> : <Icon size={18} weight="fill" />}
      </div>
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium text-[var(--color-secondary)]" title={entry.filename}>
          {entry.filename}
        </p>
        <p className="text-[11px] text-[var(--color-muted)]">{formatBytes(entry.size)}</p>
      </div>
      {!busy && <Check size={16} className="shrink-0 text-[var(--color-muted)] opacity-0 group-hover:opacity-100" aria-hidden="true" />}
    </>
  )
}

/**
 * Renders staged library attachments as removable chips, shown alongside the
 * native composer attachment chips. Each chip removes via removeLibraryAttachment
 * (the ref is dropped before send, so onNew never threads it).
 */
export function LibraryAttachmentChips() {
  const attachments = useLibraryAttachments()
  if (attachments.length === 0) return null
  return (
    <div className="flex flex-wrap gap-1.5 px-1" data-testid="library-attachment-chips">
      {attachments.map((a) => {
        const { Icon, color } = fileTypeMeta(a.filename, a.contentType)
        return (
          <div
            key={a.id}
            className="relative shrink-0 flex items-center gap-2 pl-1.5 pr-2 py-1 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] max-w-[220px]"
            title={a.filename}
            data-testid={`library-chip-${a.mediaId}`}
          >
            <div
              className="shrink-0 w-7 h-7 rounded-md flex items-center justify-center"
              style={{ backgroundColor: `${color}22`, color }}
              aria-hidden="true"
            >
              <Icon size={16} weight="fill" />
            </div>
            <span className="truncate text-xs font-medium text-[var(--color-secondary)]">{a.filename}</span>
            <button
              type="button"
              tabIndex={0}
              onClick={() => removeLibraryAttachment(a.id)}
              aria-label={`Remove ${a.filename}`}
              className="flex items-center justify-center w-4 h-4 rounded-full text-[var(--color-muted)] hover:text-white hover:bg-[var(--color-error)] transition-colors"
            >
              <X size={10} weight="bold" />
            </button>
          </div>
        )
      })}
    </div>
  )
}
