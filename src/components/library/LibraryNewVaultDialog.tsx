// LibraryNewVaultDialog — creates a new Omnipus knowledge base ("vault")
// anywhere reachable from the Library (feature C2).
//
// Deliberately mirrors LibraryNewFolderDialog's pattern (a single primary
// text field, client-side name-shape validation, the same LibraryErrorBanner
// treatment for server-side rejections) rather than inventing a new dialog
// style — creating a vault is a sibling action to New Folder, just with an
// extra "where" choice attached (POST /library/{workspace_id}/vaults takes a
// workspace-scoped target the way mkdir does not need to, since mkdir always
// targets the CURRENT directory).
//
// Unlike LibraryNewFolderDialog, this dialog owns its own mutation rather
// than delegating it to LibraryExplorer: the target workspace is a field ON
// this form (not implied by "wherever the explorer currently is"), so the
// mutation's only meaningful input is what this dialog itself collects. This
// also keeps the create-vault plumbing out of LibraryExplorer.tsx, which
// this feature does not otherwise need to touch.
//
// "Land the user in the new vault" (the caller's job once this dialog
// reports success) is handled by the `onCreated` callback, not by this
// dialog navigating anything itself — a dialog has no business deciding what
// "being somewhere" means for its host explorer.

import { useEffect, useState } from 'react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { LibraryErrorBanner } from './LibraryErrorBanner'
import { getLibraryErrorMessage } from './libraryErrorMessage'
import { createVault, isApiError, libraryQueryKeys, type LibraryEntry } from '@/lib/api'
import { useUiStore } from '@/store/ui'

export interface LibraryNewVaultDialogWorkspace {
  id: string
  name: string
}

interface LibraryNewVaultDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Every workspace the picker can target, sorted for display. */
  workspaces: LibraryNewVaultDialogWorkspace[]
  /** Preselected on open — the workspace the Library is currently browsing, if any. */
  defaultWorkspaceId: string | null
  /** Called once the vault is created, so the caller can navigate there. */
  onCreated: (workspaceId: string, entry: LibraryEntry) => void
}

function cleanFolderInput(raw: string): string {
  return raw.trim().replace(/^\/+/, '').replace(/\/+$/, '')
}

export function LibraryNewVaultDialog({
  open,
  onOpenChange,
  workspaces,
  defaultWorkspaceId,
  onCreated,
}: LibraryNewVaultDialogProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [name, setName] = useState('')
  const [workspaceId, setWorkspaceId] = useState('')
  const [folder, setFolder] = useState('')
  const [error, setError] = useState<string>()

  // Reset on every open, seeded with "here" (current workspace, workspace
  // root) — a stale name or a leftover error from a previous attempt must
  // never bleed into a fresh one.
  useEffect(() => {
    if (!open) return
    setName('')
    setFolder('')
    setError(undefined)
    setWorkspaceId(defaultWorkspaceId ?? workspaces[0]?.id ?? '')
    // Only the dialog's OWN open transition matters here — re-seeding
    // because the caller passed new workspace data while it's already open
    // would yank the field out from under whatever the user just typed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  const trimmedName = name.trim()
  const hasSlash = trimmedName.includes('/') || trimmedName.includes('\\')
  const isDotName = trimmedName === '.' || trimmedName === '..'
  const nameInvalid = trimmedName.length === 0 || hasSlash || isDotName

  const cleanedFolder = cleanFolderInput(folder)
  const folderHasTraversal = cleanedFolder.split('/').some((seg) => seg === '..' || seg === '.')

  const invalid = nameInvalid || folderHasTraversal || workspaceId === ''

  const mutation = useMutation({
    mutationFn: () =>
      createVault(workspaceId, {
        name: trimmedName,
        parent_rel_path: cleanedFolder || undefined,
      }),
    onMutate: () => setError(undefined),
    onSuccess: (entry) => {
      void queryClient.invalidateQueries({ queryKey: ['library', workspaceId, 'entries'] })
      void queryClient.invalidateQueries({ queryKey: libraryQueryKeys.workspaces() })
      addToast({ message: `Vault "${trimmedName}" created.`, variant: 'success' })
      onOpenChange(false)
      onCreated(workspaceId, entry)
    },
    onError: (err) => {
      // The friendlier, name-collision-specific wording the create-vault
      // affordance promises: the server's own 409 text ("an entry already
      // exists at that path") is accurate but doesn't say WHAT kind of entry,
      // which is exactly what a person choosing a name wants to know here.
      if (isApiError(err) && err.status === 409) {
        setError('A folder or vault with that name already exists here.')
        return
      }
      setError(getLibraryErrorMessage(err, 'Could not create vault'))
    },
  })

  function handleSubmit() {
    if (invalid || mutation.isPending) return
    mutation.mutate()
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent data-testid="library-new-vault-dialog">
        <DialogHeader>
          <DialogTitle>New vault</DialogTitle>
          <DialogDescription>
            A vault is a knowledge base — notes, records, and saved views the agent can search.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <Label htmlFor="library-new-vault-name">Name</Label>
            <Input
              id="library-new-vault-name"
              data-testid="library-new-vault-name-input"
              value={name}
              autoFocus
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') handleSubmit()
              }}
            />
            {hasSlash && (
              <p className="text-xs text-[var(--color-error)]" data-testid="library-new-vault-name-slash">
                A vault name can't contain "/" or "\".
              </p>
            )}
            {!hasSlash && isDotName && (
              <p className="text-xs text-[var(--color-error)]" data-testid="library-new-vault-name-dot">
                "{trimmedName}" isn't a valid vault name.
              </p>
            )}
          </div>

          <div className="flex flex-col gap-2">
            <Label htmlFor="library-new-vault-workspace">Location</Label>
            <Select value={workspaceId} onValueChange={setWorkspaceId}>
              <SelectTrigger id="library-new-vault-workspace" data-testid="library-new-vault-workspace-select">
                <SelectValue placeholder="Choose a workspace" />
              </SelectTrigger>
              <SelectContent>
                {workspaces.map((ws) => (
                  <SelectItem key={ws.id} value={ws.id}>
                    {ws.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Input
              aria-label="Folder within the workspace (optional)"
              data-testid="library-new-vault-folder-input"
              value={folder}
              onChange={(e) => setFolder(e.target.value)}
              placeholder="Leave blank for the workspace root"
              className="font-mono text-sm"
            />
            {folderHasTraversal && (
              <p className="text-xs text-[var(--color-error)]" data-testid="library-new-vault-folder-traversal">
                A folder path can't contain "." or "..".
              </p>
            )}
          </div>

          {error && <LibraryErrorBanner message={error} testId="library-new-vault-error" />}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            onClick={handleSubmit}
            disabled={invalid || mutation.isPending}
            data-testid="library-new-vault-confirm"
          >
            {mutation.isPending ? 'Creating…' : 'Create vault'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
