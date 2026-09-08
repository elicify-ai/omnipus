// LibraryCreateMenu — the unified "+" create control for the Library toolbar
// (feature C2).
//
// Before this, New folder / Add mount / Upload / Manage mounts were four
// separate icon buttons crowding the toolbar, with no single place to add a
// FIFTH action (New vault) without the row overflowing. This collapses them
// into one DropdownMenu behind a single "+" trigger, scoped to what makes
// sense at the CURRENT location:
//   - Every action here needs a workspace open (they all act on "here") and
//     is absent at the Library's virtual root. New folder/Upload/New
//     knowledge base are additionally disabled inside the reserved .library
//     folder, since none of them should write into that server-managed
//     namespace.
//
// "New workspace" used to live in this menu too. It is NOT offered here
// (KB-3 UX fix, 2026-09-08): a workspace is not a Library item, the sidebar's
// own inline create-workspace row is the one sanctioned entry point for it,
// and a second entry point sitting directly above "New knowledge base"
// invited mis-clicks between two very different outcomes. Do not re-add it.
//
// New vault's dialog (LibraryNewVaultDialog) is mounted HERE rather than in
// LibraryExplorer — it is self-contained (owns its own mutation, query
// invalidation, and toasts) — so this menu is the only place LibraryExplorer
// needs to touch to gain the action. It creates at the CURRENT workspace and
// directory (mirroring LibraryNewFolderDialog), so those are passed straight
// through rather than offered as a picker.
import { useState } from 'react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Books,
  FolderPlus,
  Plus,
  SpinnerGap,
  UploadSimple,
} from '@phosphor-icons/react'
import { LibraryNewVaultDialog } from './LibraryNewVaultDialog'
import { MountFolderIcon } from './icons'
import type { LibraryEntry } from '@/lib/api'

interface LibraryCreateMenuProps {
  workspaceId: string | null
  /** Display name of workspaceId, for the New-vault dialog's destination line. */
  workspaceName: string
  /** The directory currently browsed within the workspace; '' = workspace root. */
  browsedDir: string
  isReservedLibraryDir: boolean
  mountedCount: number
  uploadPending: boolean
  onNewFolder: () => void
  onAddMount: () => void
  onManageMounts: () => void
  onUpload: () => void
  /** Called once the new vault is created, so the caller can navigate there. */
  onVaultCreated: (entry: LibraryEntry) => void
}

export function LibraryCreateMenu({
  workspaceId,
  workspaceName,
  browsedDir,
  isReservedLibraryDir,
  mountedCount,
  uploadPending,
  onNewFolder,
  onAddMount,
  onManageMounts,
  onUpload,
  onVaultCreated,
}: LibraryCreateMenuProps) {
  const [vaultDialogOpen, setVaultDialogOpen] = useState(false)

  const inWorkspace = workspaceId !== null
  const canWriteHere = inWorkspace && !isReservedLibraryDir

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            tabIndex={0}
            aria-label="Create"
            title="Create"
            data-testid="library-create-menu-trigger"
            className="rounded p-1.5 text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] transition-colors"
          >
            <Plus size={16} />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" data-testid="library-create-menu">
          {inWorkspace && (
            <>
              <DropdownMenuItem
                onSelect={() => setVaultDialogOpen(true)}
                disabled={!canWriteHere}
                data-testid="library-create-menu-new-vault"
                className="flex items-center gap-2"
              >
                <Books size={15} /> New knowledge base
              </DropdownMenuItem>
              <DropdownMenuItem
                onSelect={onNewFolder}
                disabled={!canWriteHere}
                data-testid="library-create-menu-new-folder"
                className="flex items-center gap-2"
              >
                <FolderPlus size={15} /> New folder
              </DropdownMenuItem>
              <DropdownMenuItem
                onSelect={onUpload}
                disabled={!canWriteHere || uploadPending}
                data-testid="library-create-menu-upload"
                className="flex items-center gap-2"
              >
                {uploadPending ? <SpinnerGap size={15} className="animate-spin" /> : <UploadSimple size={15} />}
                Upload files
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                onSelect={onAddMount}
                data-testid="library-create-menu-add-mount"
                className="flex items-center gap-2"
              >
                <MountFolderIcon size={15} /> Add a folder from your Mac
              </DropdownMenuItem>
              <DropdownMenuItem
                onSelect={onManageMounts}
                disabled={mountedCount === 0}
                data-testid="library-create-menu-manage-mounts"
                className="flex items-center gap-2"
              >
                <MountFolderIcon size={15} />
                {mountedCount === 0 ? 'Manage mounted folders' : `Manage ${mountedCount} mounted folders`}
              </DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>

      {workspaceId !== null && (
        <LibraryNewVaultDialog
          open={vaultDialogOpen}
          onOpenChange={setVaultDialogOpen}
          workspaceId={workspaceId}
          workspaceName={workspaceName}
          parentPath={browsedDir}
          onCreated={onVaultCreated}
        />
      )}
    </>
  )
}
