// LibraryExplorer — the file explorer over workspace work/ trees
// (library-spec.md D-2). This is the ONE component both entry points (D-3)
// render: the sidebar opens it with `initialWorkspaceId` undefined (virtual
// root — every workspace as a top-level node); a workspace-scoped opener
// (e.g. a future chat/header-bar affordance, wired through
// `useUiStore.getState().openLibraryPanel(workspaceId)`) opens it with a real
// id and lands straight inside that workspace's work/ tree. Only the INITIAL
// selection differs — from there the same navigation (drill into a
// directory, back out via breadcrumb, "Library" crumb to return to the
// virtual root) is available regardless of how the panel was opened.
//
// Preview/edit pane (library-spec.md D-5) is explicitly OUT OF SCOPE for this
// component — see the "PREVIEW/EDIT PANE PLACEHOLDER" comment below, where a
// later task mounts CodeMirror/Shiki/<img>/<video>/Mermaid. Selecting a file
// today opens a details strip with real metadata + real actions (download,
// rename, move/copy, delete) — nothing fake, just narrower than the eventual
// preview.

import { useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Files,
  CaretRight,
  UploadSimple,
  X,
  ArrowSquareOut,
  Tray,
  SpinnerGap,
  FolderOpen,
  DownloadSimple,
  PencilSimple,
  ArrowsLeftRight,
  Trash,
} from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
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
import { QueryErrorState } from '@/components/shared/QueryErrorState'
import { fileTypeMeta } from '@/components/chat/AttachmentCard'
import { formatRelative } from '@/lib/formatRelative'
import { useUiStore } from '@/store/ui'
import { cn } from '@/lib/utils'
import {
  fetchLibraryWorkspaces,
  fetchLibraryEntries,
  deleteLibraryEntry,
  renameLibraryEntry,
  moveLibraryEntry,
  copyLibraryEntry,
  uploadLibraryFiles,
  libraryDownloadUrl,
  libraryQueryKeys,
  getErrorMessage,
} from '@/lib/api'
import type { LibraryEntry, LibraryTransferRequest, LibraryWorkspaceNode } from '@/lib/api'
import { LibraryEntryRow, formatLibrarySize } from './LibraryEntryRow'
import { LibraryRenameDialog } from './LibraryRenameDialog'
import { LibraryTransferDialog } from './LibraryTransferDialog'

export interface LibraryExplorerProps {
  /** undefined = start at the virtual root (D-3 sidebar entry point). */
  initialWorkspaceId?: string
  /** Omit to hide the Close button (e.g. the fullscreen pop-out route). */
  onClose?: () => void
  /** Omit to hide the pop-out button (the pop-out route itself has nowhere further to pop out to). */
  onPopOut?: () => void
  /** Extra classes for the root element — e.g. the pop-out route's `absolute inset-0` fill. */
  className?: string
}

function sortEntries(entries: LibraryEntry[]): LibraryEntry[] {
  return [...entries].sort((a, b) => {
    if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1
    return a.name.localeCompare(b.name)
  })
}

export function LibraryExplorer({ initialWorkspaceId, onClose, onPopOut, className }: LibraryExplorerProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const [workspaceId, setWorkspaceId] = useState<string | null>(initialWorkspaceId ?? null)
  const [path, setPath] = useState('')
  const [includeHidden, setIncludeHidden] = useState(false)
  const [selectedEntry, setSelectedEntry] = useState<LibraryEntry | null>(null)
  const [renameTarget, setRenameTarget] = useState<LibraryEntry | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<LibraryEntry | null>(null)
  const [transferTarget, setTransferTarget] = useState<{ entry: LibraryEntry; mode: 'move' | 'copy' } | null>(null)

  // Always fetched (cheap, small list) — backs the virtual-root listing AND
  // resolves the current workspace's display name for the breadcrumb + the
  // destination picker inside LibraryTransferDialog.
  const workspacesQuery = useQuery({
    queryKey: libraryQueryKeys.workspaces(),
    queryFn: fetchLibraryWorkspaces,
    staleTime: 30_000,
  })

  const entriesQuery = useQuery({
    queryKey: libraryQueryKeys.entries(workspaceId ?? '', path, includeHidden),
    queryFn: () => fetchLibraryEntries(workspaceId as string, path, includeHidden),
    enabled: workspaceId !== null,
    staleTime: 10_000,
  })

  const sortedWorkspaces = useMemo(
    () => [...(workspacesQuery.data ?? [])].sort((a, b) => a.name.localeCompare(b.name)),
    [workspacesQuery.data],
  )
  const sortedEntries = useMemo(() => sortEntries(entriesQuery.data ?? []), [entriesQuery.data])

  const currentWorkspaceName =
    sortedWorkspaces.find((w) => w.id === workspaceId)?.name ?? workspaceId ?? ''

  function invalidateEntries(wsId: string) {
    void queryClient.invalidateQueries({ queryKey: ['library', wsId, 'entries'] })
  }
  function invalidateWorkspaces() {
    void queryClient.invalidateQueries({ queryKey: libraryQueryKeys.workspaces() })
  }

  const deleteMutation = useMutation({
    mutationFn: ({ wsId, entryPath }: { wsId: string; entryPath: string }) => deleteLibraryEntry(wsId, entryPath),
    onSuccess: (_data, vars) => {
      invalidateEntries(vars.wsId)
      invalidateWorkspaces()
      addToast({ message: 'Deleted.', variant: 'success' })
      setDeleteTarget(null)
      setSelectedEntry((cur) => (cur && cur.path === vars.entryPath ? null : cur))
    },
    onError: (err) => {
      addToast({ message: getErrorMessage(err, 'Delete failed'), variant: 'error' })
    },
  })

  const renameMutation = useMutation({
    mutationFn: ({ wsId, from, to }: { wsId: string; from: string; to: string }) =>
      renameLibraryEntry(wsId, { from, to }),
    onSuccess: (updated, vars) => {
      invalidateEntries(vars.wsId)
      addToast({ message: 'Renamed.', variant: 'success' })
      setRenameTarget(null)
      setSelectedEntry((cur) => (cur && cur.path === vars.from ? updated : cur))
    },
    onError: (err) => {
      addToast({ message: getErrorMessage(err, 'Rename failed'), variant: 'error' })
    },
  })

  const transferMutation = useMutation({
    mutationFn: ({ mode, body }: { mode: 'move' | 'copy'; body: LibraryTransferRequest }) =>
      mode === 'move' ? moveLibraryEntry(body) : copyLibraryEntry(body),
    onSuccess: (_data, vars) => {
      invalidateEntries(vars.body.from_workspace_id)
      invalidateEntries(vars.body.to_workspace_id)
      invalidateWorkspaces()
      addToast({ message: vars.mode === 'move' ? 'Moved.' : 'Copied.', variant: 'success' })
      if (vars.mode === 'move') {
        setSelectedEntry((cur) => (cur && cur.path === vars.body.from_path ? null : cur))
      }
      setTransferTarget(null)
    },
    onError: (err) => {
      addToast({ message: getErrorMessage(err, `${transferTarget?.mode === 'copy' ? 'Copy' : 'Move'} failed`), variant: 'error' })
    },
  })

  const uploadMutation = useMutation({
    mutationFn: ({ wsId, files, dir }: { wsId: string; files: File[]; dir: string }) =>
      uploadLibraryFiles(wsId, files, dir),
    onSuccess: (data, vars) => {
      invalidateEntries(vars.wsId)
      invalidateWorkspaces()
      addToast({
        message: `Uploaded ${data.entries.length} file${data.entries.length === 1 ? '' : 's'}.`,
        variant: 'success',
      })
    },
    onError: (err) => {
      addToast({ message: getErrorMessage(err, 'Upload failed'), variant: 'error' })
    },
  })

  function handleOpenWorkspaceNode(node: LibraryWorkspaceNode) {
    setWorkspaceId(node.id)
    setPath('')
    setSelectedEntry(null)
  }
  function handleGoRoot() {
    setWorkspaceId(null)
    setPath('')
    setSelectedEntry(null)
  }
  function handleGoWorkspaceRoot() {
    setPath('')
    setSelectedEntry(null)
  }
  function handleOpenDirectory(entry: LibraryEntry) {
    setPath(entry.path)
    setSelectedEntry(null)
  }
  function handleBreadcrumbSegment(index: number, segments: string[]) {
    setPath(segments.slice(0, index + 1).join('/'))
    setSelectedEntry(null)
  }
  function handleDownload(entry: LibraryEntry) {
    if (!workspaceId) return
    const url = libraryDownloadUrl(workspaceId, entry.path)
    const a = document.createElement('a')
    a.href = url
    a.download = entry.name
    a.rel = 'noopener'
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
  }
  function handleFileInputChange(e: React.ChangeEvent<HTMLInputElement>) {
    const files = e.target.files
    if (!files || files.length === 0 || !workspaceId) return
    uploadMutation.mutate({ wsId: workspaceId, files: Array.from(files), dir: path })
    e.target.value = ''
  }

  const pathSegments = path ? path.split('/').filter(Boolean) : []

  return (
    <div className={cn('flex h-full flex-col', className)} data-testid="library-explorer">
      {/* Toolbar / breadcrumb row */}
      <div className="flex items-center gap-2 px-3 h-chrome-header min-h-chrome-header shrink-0 border-b border-[var(--color-border)] bg-[var(--color-surface-1)]">
        <nav aria-label="Library breadcrumb" className="flex items-center gap-1 min-w-0 flex-1 text-sm overflow-hidden">
          <button
            type="button"
            tabIndex={0}
            onClick={handleGoRoot}
            data-testid="library-crumb-root"
            className={cn(
              'flex items-center gap-1.5 shrink-0 rounded px-1.5 py-1 transition-colors',
              workspaceId === null
                ? 'text-[var(--color-accent)] font-medium'
                : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]',
            )}
          >
            <Files size={14} />
            Library
          </button>
          {workspaceId !== null && (
            <>
              <CaretRight size={12} className="text-[var(--color-muted)] shrink-0" aria-hidden="true" />
              <button
                type="button"
                tabIndex={0}
                onClick={handleGoWorkspaceRoot}
                data-testid="library-crumb-workspace"
                className={cn(
                  'truncate rounded px-1.5 py-1 transition-colors min-w-0',
                  pathSegments.length === 0
                    ? 'text-[var(--color-accent)] font-medium'
                    : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]',
                )}
              >
                {currentWorkspaceName}
              </button>
            </>
          )}
          {pathSegments.map((seg, i) => (
            <span key={`${seg}-${i}`} className="flex items-center gap-1 min-w-0">
              <CaretRight size={12} className="text-[var(--color-muted)] shrink-0" aria-hidden="true" />
              <button
                type="button"
                tabIndex={0}
                onClick={() => handleBreadcrumbSegment(i, pathSegments)}
                className={cn(
                  'truncate rounded px-1.5 py-1 transition-colors min-w-0',
                  i === pathSegments.length - 1
                    ? 'text-[var(--color-accent)] font-medium'
                    : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]',
                )}
              >
                {seg}
              </button>
            </span>
          ))}
        </nav>

        <div className="flex items-center gap-1 shrink-0">
          {workspaceId !== null && (
            <label
              htmlFor="library-show-hidden"
              className="flex items-center gap-1.5 text-xs text-[var(--color-muted)] mr-1 select-none cursor-pointer"
            >
              <Switch
                id="library-show-hidden"
                data-testid="library-show-hidden-toggle"
                checked={includeHidden}
                onCheckedChange={setIncludeHidden}
              />
              Show hidden
            </label>
          )}
          {workspaceId !== null && (
            <>
              <input
                tabIndex={0}
                ref={fileInputRef}
                type="file"
                multiple
                onChange={handleFileInputChange}
                data-testid="library-upload-input"
                className="hidden"
              />
              <button
                type="button"
                tabIndex={0}
                onClick={() => fileInputRef.current?.click()}
                disabled={uploadMutation.isPending}
                aria-label="Upload files"
                title="Upload files"
                data-testid="library-upload-button"
                className="rounded p-1.5 text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] transition-colors disabled:opacity-50"
              >
                {uploadMutation.isPending ? <SpinnerGap size={16} className="animate-spin" /> : <UploadSimple size={16} />}
              </button>
            </>
          )}
          {onPopOut && (
            <button
              type="button"
              tabIndex={0}
              onClick={onPopOut}
              aria-label="Open Library in a new tab"
              title="Open in new tab"
              data-testid="library-popout-button"
              className="rounded p-1.5 text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] transition-colors"
            >
              <ArrowSquareOut size={16} />
            </button>
          )}
          {onClose && (
            <button
              type="button"
              tabIndex={0}
              onClick={onClose}
              aria-label="Close Library"
              title="Close"
              data-testid="library-close-button"
              className="rounded p-1.5 text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] transition-colors"
            >
              <X size={16} />
            </button>
          )}
        </div>
      </div>

      {/* Body */}
      <div className="flex-1 min-h-0 overflow-y-auto p-2 relative">
        {workspaceId === null ? (
          <>
            {workspacesQuery.isLoading && <ListSkeleton />}
            {workspacesQuery.isError && (
              <QueryErrorState
                layout="fill"
                message="Could not load workspaces."
                onRetry={() => void workspacesQuery.refetch()}
                testId="library-workspaces-error"
              />
            )}
            {!workspacesQuery.isLoading && !workspacesQuery.isError && sortedWorkspaces.length === 0 && (
              <EmptyState icon={<Tray size={28} />} message="No workspaces yet." />
            )}
            {!workspacesQuery.isLoading &&
              !workspacesQuery.isError &&
              sortedWorkspaces.map((node) => (
                <button
                  key={node.id}
                  type="button"
                  tabIndex={0}
                  onClick={() => handleOpenWorkspaceNode(node)}
                  data-testid={`library-workspace-node-${node.id}`}
                  className="flex w-full items-center gap-3 rounded-lg px-3 py-2 hover:bg-[var(--color-surface-2)] text-left transition-colors"
                >
                  <Tray size={18} weight="fill" className="text-[var(--color-accent)] shrink-0" />
                  <span className="flex-1 truncate text-sm text-[var(--color-secondary)]">{node.name}</span>
                  <span className="text-xs text-[var(--color-muted)] shrink-0">
                    {node.entry_count} item{node.entry_count === 1 ? '' : 's'}
                  </span>
                </button>
              ))}
          </>
        ) : (
          <>
            {entriesQuery.isLoading && <ListSkeleton />}
            {entriesQuery.isError && (
              <QueryErrorState
                layout="fill"
                message="Could not load this folder."
                onRetry={() => void entriesQuery.refetch()}
                testId="library-entries-error"
              />
            )}
            {!entriesQuery.isLoading && !entriesQuery.isError && sortedEntries.length === 0 && (
              <EmptyState
                icon={<FolderOpen size={28} />}
                message={path ? 'This folder is empty.' : 'No files in this workspace yet.'}
              />
            )}
            {!entriesQuery.isLoading &&
              !entriesQuery.isError &&
              sortedEntries.map((entry) => (
                <LibraryEntryRow
                  key={entry.path}
                  entry={entry}
                  selected={selectedEntry?.path === entry.path}
                  onOpenDirectory={handleOpenDirectory}
                  onSelectFile={setSelectedEntry}
                  onDownload={handleDownload}
                  onRename={setRenameTarget}
                  onTransfer={(e, mode) => setTransferTarget({ entry: e, mode })}
                  onDelete={setDeleteTarget}
                />
              ))}
          </>
        )}
      </div>

      {/* ── PREVIEW/EDIT PANE PLACEHOLDER (library-spec.md D-5) ────────────────
          A LATER, SEPARATE task mounts the real preview/edit surface here:
          <img>/<video controls> for media, HistoricalMessageMarkdown +
          MermaidDiagram for markdown/diagrams, Shiki for code, and a
          lazy-loaded CodeMirror 6 editor for the editable text types (see the
          section-4 scope table in library-spec.md). Until then, selecting a
          file shows this metadata + real-action strip only — every control
          in it (Download/Rename/Move/Copy/Delete) is fully wired, nothing
          here is a placeholder EXCEPT the content-preview area itself. */}
      {selectedEntry && workspaceId !== null && (
        <LibraryDetailsStrip
          entry={selectedEntry}
          onClose={() => setSelectedEntry(null)}
          onDownload={handleDownload}
          onRename={() => setRenameTarget(selectedEntry)}
          onTransfer={(mode) => setTransferTarget({ entry: selectedEntry, mode })}
          onDelete={() => setDeleteTarget(selectedEntry)}
        />
      )}

      {/* ── Rename dialog ────────────────────────────────────────────────── */}
      <LibraryRenameDialog
        open={renameTarget !== null}
        onOpenChange={(open) => !open && setRenameTarget(null)}
        entry={renameTarget}
        siblingNames={new Set(sortedEntries.filter((e) => e.path !== renameTarget?.path).map((e) => e.name))}
        isPending={renameMutation.isPending}
        onSubmit={(to) => {
          if (!workspaceId || !renameTarget) return
          renameMutation.mutate({ wsId: workspaceId, from: renameTarget.path, to })
        }}
      />

      {/* ── Move / Copy dialog (D-9, cross-workspace) ──────────────────────── */}
      <LibraryTransferDialog
        open={transferTarget !== null}
        onOpenChange={(open) => !open && setTransferTarget(null)}
        mode={transferTarget?.mode ?? 'move'}
        entry={transferTarget?.entry ?? null}
        sourceWorkspaceId={workspaceId ?? ''}
        workspaces={sortedWorkspaces}
        isPending={transferMutation.isPending}
        onSubmit={(body) => {
          if (!transferTarget) return
          transferMutation.mutate({ mode: transferTarget.mode, body })
        }}
      />

      {/* ── Delete confirm (destructive action — hard confirm, no silent data loss) ── */}
      <AlertDialog open={deleteTarget !== null} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {deleteTarget?.is_dir ? 'folder' : 'file'}?</AlertDialogTitle>
            <AlertDialogDescription>
              {deleteTarget
                ? deleteTarget.is_dir
                  ? `"${deleteTarget.name}" and everything inside it will be permanently deleted. This cannot be undone.`
                  : `"${deleteTarget.name}" will be permanently deleted. This cannot be undone.`
                : ''}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={deleteMutation.isPending}
              data-testid="library-delete-confirm"
              onClick={() => {
                if (!workspaceId || !deleteTarget) return
                deleteMutation.mutate({ wsId: workspaceId, entryPath: deleteTarget.path })
              }}
            >
              {deleteMutation.isPending ? 'Deleting…' : 'Delete'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function ListSkeleton() {
  return (
    <div className="flex flex-col gap-1.5 p-1" data-testid="library-loading-skeleton">
      {[1, 2, 3, 4].map((i) => (
        <div
          key={i}
          className="h-9 rounded-lg bg-[var(--color-surface-2)] animate-pulse"
          style={{ width: `${70 + i * 6}%` }}
        />
      ))}
    </div>
  )
}

function EmptyState({ icon, message }: { icon: React.ReactNode; message: string }) {
  return (
    <div className="absolute inset-0 flex flex-col items-center justify-center gap-2 p-8 text-center text-[var(--color-muted)]">
      {icon}
      <p className="text-sm">{message}</p>
    </div>
  )
}

interface LibraryDetailsStripProps {
  entry: LibraryEntry
  onClose: () => void
  onDownload: (entry: LibraryEntry) => void
  onRename: () => void
  onTransfer: (mode: 'move' | 'copy') => void
  onDelete: () => void
}

function LibraryDetailsStrip({ entry, onClose, onDownload, onRename, onTransfer, onDelete }: LibraryDetailsStripProps) {
  const { Icon, color } = fileTypeMeta(entry.name, entry.mime)
  return (
    <div
      className="shrink-0 border-t border-[var(--color-border)] bg-[var(--color-surface-1)] p-3"
      data-testid="library-details-strip"
    >
      <div className="flex items-start gap-3">
        <div
          className="shrink-0 w-10 h-10 rounded-md flex items-center justify-center"
          style={{ backgroundColor: `${color}22`, color }}
          aria-hidden="true"
        >
          <Icon size={20} weight="fill" />
        </div>
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium text-[var(--color-secondary)]" title={entry.name}>
            {entry.name}
          </p>
          <p className="text-xs text-[var(--color-muted)] mt-0.5">
            {formatLibrarySize(entry.size)} · modified {formatRelative(entry.modified_at)}
            {entry.is_text_editable && ' · text'}
          </p>
        </div>
        <button
          type="button"
          tabIndex={0}
          onClick={onClose}
          aria-label="Close details"
          data-testid="library-details-close"
          className="shrink-0 rounded p-1 text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] transition-colors"
        >
          <X size={14} />
        </button>
      </div>
      <div className="flex items-center gap-1.5 mt-3">
        <Button variant="outline" size="sm" onClick={() => onDownload(entry)} className="gap-1.5 text-xs">
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
          className="gap-1.5 text-xs ml-auto text-[var(--color-muted)] hover:text-[var(--color-error)]"
        >
          <Trash size={13} /> Delete
        </Button>
      </div>
    </div>
  )
}
