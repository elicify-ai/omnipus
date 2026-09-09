// LibraryNewVaultDialog — creates a new Omnipus knowledge base ("vault")
// at the CURRENT Library location (feature C2; KB-3 fix; WL-3 fix removed
// the read-only Location line, 2026-09-09).
//
// Mirrors LibraryNewFolderDialog exactly: the workspace and parent directory
// are context the caller already knows (the user is standing in them), not a
// choice this dialog should re-ask for — a workspace picker and a free-text
// path box only duplicate state the explorer already has and invite typos
// the server then rejects. The only field left to fill in is the name, the
// same as New folder: no destination is displayed here either.
//
// Unlike LibraryNewFolderDialog, this dialog still owns its own mutation
// (POST /library/{workspace_id}/vaults takes a workspace-scoped target the
// way mkdir does not need to) rather than delegating it to LibraryExplorer —
// that keeps the create-vault plumbing out of LibraryExplorer.tsx.
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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { LibraryErrorBanner } from './LibraryErrorBanner'
import { getLibraryErrorMessage } from './libraryErrorMessage'
import { createVault, isApiError, libraryQueryKeys, type LibraryEntry } from '@/lib/api'
import { useUiStore } from '@/store/ui'

interface LibraryNewVaultDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The workspace the Library is currently browsing — the create target. */
  workspaceId: string
  /** The directory currently browsed within that workspace; '' = workspace root. */
  parentPath: string
  /** Called once the vault is created, so the caller can navigate there. */
  onCreated: (entry: LibraryEntry) => void
}

export function LibraryNewVaultDialog({
  open,
  onOpenChange,
  workspaceId,
  parentPath,
  onCreated,
}: LibraryNewVaultDialogProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [name, setName] = useState('')
  const [error, setError] = useState<string>()

  // Reset on every open — a stale name or a leftover error from a previous
  // attempt must never bleed into a fresh one.
  useEffect(() => {
    if (!open) return
    setName('')
    setError(undefined)
  }, [open])

  const trimmedName = name.trim()
  const hasSlash = trimmedName.includes('/') || trimmedName.includes('\\')
  const isDotName = trimmedName === '.' || trimmedName === '..'
  const nameInvalid = trimmedName.length === 0 || hasSlash || isDotName

  const mutation = useMutation({
    mutationFn: () =>
      createVault(workspaceId, {
        name: trimmedName,
        parent_rel_path: parentPath || undefined,
      }),
    onMutate: () => setError(undefined),
    onSuccess: (entry) => {
      void queryClient.invalidateQueries({ queryKey: ['library', workspaceId, 'entries'] })
      void queryClient.invalidateQueries({ queryKey: libraryQueryKeys.workspaces() })
      addToast({ message: `Knowledge base "${trimmedName}" created.`, variant: 'success' })
      onOpenChange(false)
      onCreated(entry)
    },
    onError: (err) => {
      // The friendlier, name-collision-specific wording the create-vault
      // affordance promises: the server's own 409 text ("an entry already
      // exists at that path") is accurate but doesn't say WHAT kind of entry,
      // which is exactly what a person choosing a name wants to know here.
      if (isApiError(err) && err.status === 409) {
        setError('A folder or knowledge base with that name already exists here.')
        return
      }
      setError(getLibraryErrorMessage(err, 'Could not create knowledge base'))
    },
  })

  function handleSubmit() {
    if (nameInvalid || mutation.isPending) return
    mutation.mutate()
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent data-testid="library-new-vault-dialog">
        <DialogHeader>
          <DialogTitle>New knowledge base</DialogTitle>
          <DialogDescription>
            A knowledge base is notes, records, and saved views the agent can search.
          </DialogDescription>
        </DialogHeader>
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
              A knowledge base name can't contain "/" or "\".
            </p>
          )}
          {!hasSlash && isDotName && (
            <p className="text-xs text-[var(--color-error)]" data-testid="library-new-vault-name-dot">
              "{trimmedName}" isn't a valid knowledge base name.
            </p>
          )}
          {error && <LibraryErrorBanner message={error} testId="library-new-vault-error" />}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            onClick={handleSubmit}
            disabled={nameInvalid || mutation.isPending}
            data-testid="library-new-vault-confirm"
          >
            {mutation.isPending ? 'Creating…' : 'Create knowledge base'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
