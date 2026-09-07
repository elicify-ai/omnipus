// LibraryCreateMenu — the unified "+" create control for the Library toolbar
// (feature C2).
//
// Before this, New folder / Add mount / Upload / Manage mounts were four
// separate icon buttons crowding the toolbar, with no single place to add a
// FIFTH and SIXTH create action (New vault, New workspace) without the row
// overflowing. This collapses all six into one DropdownMenu behind a single
// "+" trigger, scoped to what makes sense at the CURRENT location:
//   - New folder / Upload / Manage mounts / Add mount need a workspace open
//     (they act on "here"); New folder/Upload are additionally disabled
//     inside the reserved .library folder, same as the buttons they replace.
//   - New vault and New workspace are global actions and stay enabled even
//     at the Library's virtual root (New vault's own Location field lets you
//     pick a workspace regardless of where you opened the menu from).
//
// New vault's dialog (LibraryNewVaultDialog) and New workspace's slide-over
// (NewWorkspaceSlideOver) are mounted HERE rather than in LibraryExplorer —
// both are self-contained (own their own mutation, query invalidation, and
// toasts), so this menu is the only place LibraryExplorer.tsx needs to touch
// to gain both actions.
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
  Buildings,
  FolderPlus,
  FolderSimpleDashed,
  Plus,
  SpinnerGap,
  UploadSimple,
} from '@phosphor-icons/react'
import { LibraryNewVaultDialog } from './LibraryNewVaultDialog'
import { NewWorkspaceSlideOver } from '@/components/workspaces/NewWorkspaceSlideOver'
import type { LibraryEntry } from '@/lib/api'

export interface LibraryCreateMenuWorkspace {
  id: string
  name: string
}

interface LibraryCreateMenuProps {
  workspaceId: string | null
  workspaces: LibraryCreateMenuWorkspace[]
  isReservedLibraryDir: boolean
  mountedCount: number
  uploadPending: boolean
  onNewFolder: () => void
  onAddMount: () => void
  onManageMounts: () => void
  onUpload: () => void
  /** Called once the new vault is created, so the caller can navigate there. */
  onVaultCreated: (workspaceId: string, entry: LibraryEntry) => void
}

export function LibraryCreateMenu({
  workspaceId,
  workspaces,
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
  const [newWorkspaceOpen, setNewWorkspaceOpen] = useState(false)

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
          <DropdownMenuItem
            onSelect={() => setVaultDialogOpen(true)}
            disabled={workspaces.length === 0}
            data-testid="library-create-menu-new-vault"
            className="flex items-center gap-2"
          >
            <Books size={15} /> New vault
          </DropdownMenuItem>
          <DropdownMenuItem
            onSelect={() => setNewWorkspaceOpen(true)}
            data-testid="library-create-menu-new-workspace"
            className="flex items-center gap-2"
          >
            <Buildings size={15} /> New workspace
          </DropdownMenuItem>

          {inWorkspace && (
            <>
              <DropdownMenuSeparator />
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
                <FolderSimpleDashed size={15} /> Add a folder from your Mac
              </DropdownMenuItem>
              <DropdownMenuItem
                onSelect={onManageMounts}
                disabled={mountedCount === 0}
                data-testid="library-create-menu-manage-mounts"
                className="flex items-center gap-2"
              >
                <FolderSimpleDashed size={15} />
                {mountedCount === 0 ? 'Manage mounted folders' : `Manage ${mountedCount} mounted folders`}
              </DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>

      <LibraryNewVaultDialog
        open={vaultDialogOpen}
        onOpenChange={setVaultDialogOpen}
        workspaces={workspaces}
        defaultWorkspaceId={workspaceId}
        onCreated={onVaultCreated}
      />
      <NewWorkspaceSlideOver open={newWorkspaceOpen} onOpenChange={setNewWorkspaceOpen} />
    </>
  )
}
