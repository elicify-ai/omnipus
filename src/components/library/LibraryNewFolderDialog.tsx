// LibraryNewFolderDialog — creates a new directory in the CURRENT directory
// (library-spec.md D-2/D-8; UAT fix — mkdir worked at the API layer but had
// no reachable affordance anywhere in the explorer: toolbar had only Show
// hidden/Upload/Open-in-new-tab/Close, the per-file "..." menu had no
// "New Folder" action, right-click did nothing, and the upload input had no
// `webkitdirectory`. "Only the backend gained a capability nobody could
// reach.").
//
// Deliberately mirrors LibraryRenameDialog's pattern (single name field,
// client-side validation, same LibraryErrorBanner treatment) rather than
// introducing a new dialog style — this is a sibling action to Rename, not a
// different kind of interaction. POST /library/{workspace_id}/mkdir is
// idempotent (200 if a directory of this name already exists), so the
// client-side collision check here is a UX nicety (avoid a submit that
// visibly does nothing new) rather than a correctness requirement the way
// Rename's collision check is.

import { useEffect, useState } from 'react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { LibraryErrorBanner } from './LibraryErrorBanner'

interface LibraryNewFolderDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Sibling entry names in the CURRENT directory — used for the client-side collision nicety. */
  siblingNames: ReadonlySet<string>
  onSubmit: (name: string) => void
  isPending: boolean
  /** Set by the parent when the server rejects a mkdir attempt (e.g. a 409
   * because a regular FILE already exists at that name, or a 403 path
   * escape). Rendered as a persistent banner — same pattern as
   * Rename/Move — instead of the dialog silently reverting with no
   * explanation. The parent clears this whenever the dialog is (re)opened
   * or a new attempt starts. */
  error?: string
}

export function LibraryNewFolderDialog({
  open,
  onOpenChange,
  siblingNames,
  onSubmit,
  isPending,
  error,
}: LibraryNewFolderDialogProps) {
  const [name, setName] = useState('')

  useEffect(() => {
    if (open) setName('')
  }, [open])

  const trimmed = name.trim()
  const hasSlash = trimmed.includes('/')
  // Reject "..-prefixed" and any general traversal attempt client-side,
  // before it's ever sent — a directory name is a single path SEGMENT (this
  // dialog always creates inside the CURRENT directory), so "..' anywhere in
  // it is never a legitimate name, only an escape attempt.
  const hasTraversal = trimmed.includes('..')
  const collides = trimmed.length > 0 && siblingNames.has(trimmed)
  const invalid = trimmed.length === 0 || hasSlash || hasTraversal || collides

  function handleSubmit() {
    if (invalid) return
    onSubmit(trimmed)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent data-testid="library-new-folder-dialog">
        <DialogHeader>
          <DialogTitle>New folder</DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-2">
          <Label htmlFor="library-new-folder-input">Folder name</Label>
          <Input
            id="library-new-folder-input"
            data-testid="library-new-folder-input"
            value={name}
            autoFocus
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') handleSubmit()
            }}
          />
          {hasSlash && (
            <p className="text-xs text-[var(--color-error)]" data-testid="library-new-folder-slash">
              A folder name can't contain "/".
            </p>
          )}
          {!hasSlash && hasTraversal && (
            <p className="text-xs text-[var(--color-error)]" data-testid="library-new-folder-traversal">
              A folder name can't contain "..".
            </p>
          )}
          {!hasSlash && !hasTraversal && collides && (
            <p className="text-xs text-[var(--color-error)]" data-testid="library-new-folder-collision">
              An entry named "{trimmed}" already exists here.
            </p>
          )}
          {error && (
            <LibraryErrorBanner message={error} testId="library-new-folder-error" />
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            onClick={handleSubmit}
            disabled={invalid || isPending}
            data-testid="library-new-folder-confirm"
          >
            {isPending ? 'Creating…' : 'Create'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
