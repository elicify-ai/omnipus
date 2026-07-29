// LibraryRenameDialog — renames an entry WITHIN its current directory
// (library-spec.md D-2). Moving to a different directory (same workspace or
// cross-workspace) is LibraryTransferDialog's job (Move…/Copy…) — this dialog
// only edits the final path segment, so it never needs a destination-picker.
//
// The server rejects (409) a rename onto an already-occupied path and never
// silently overwrites (library-spec.md — LibraryRenameRequest doc). Since an
// actual overwrite is architecturally impossible, this dialog's "confirm
// step" for that class of destructive action is the dialog itself (open →
// review → explicit Rename click) PLUS a hard client-side block against any
// name that collides with a sibling already visible in the current listing —
// that is a strictly *more* honest signal than a generic "confirm overwrite"
// dialog would be, since overwrite was never actually going to happen.

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
import type { LibraryEntry } from '@/lib/api'

interface LibraryRenameDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  entry: LibraryEntry | null
  /** Sibling entry names in the SAME directory (excludes `entry` itself) — used for the client-side collision block. */
  siblingNames: ReadonlySet<string>
  onSubmit: (newPath: string) => void
  isPending: boolean
}

export function LibraryRenameDialog({ open, onOpenChange, entry, siblingNames, onSubmit, isPending }: LibraryRenameDialogProps) {
  const [name, setName] = useState('')

  useEffect(() => {
    if (open && entry) setName(entry.name)
  }, [open, entry])

  if (!entry) return null

  const trimmed = name.trim()
  const parentDir = entry.path.includes('/') ? entry.path.slice(0, entry.path.lastIndexOf('/')) : ''
  const hasSlash = trimmed.includes('/')
  const collides = trimmed.length > 0 && trimmed !== entry.name && siblingNames.has(trimmed)
  const invalid = trimmed.length === 0 || hasSlash || collides

  function handleSubmit() {
    if (invalid || !entry) return
    const newPath = parentDir ? `${parentDir}/${trimmed}` : trimmed
    onSubmit(newPath)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent data-testid="library-rename-dialog">
        <DialogHeader>
          <DialogTitle>Rename {entry.is_dir ? 'folder' : 'file'}</DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-2">
          <Label htmlFor="library-rename-input">New name</Label>
          <Input
            id="library-rename-input"
            data-testid="library-rename-input"
            value={name}
            autoFocus
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') handleSubmit()
            }}
          />
          {hasSlash && (
            <p className="text-xs text-[var(--color-error)]">
              A name can't contain "/" — use Move… to change its folder.
            </p>
          )}
          {collides && !hasSlash && (
            <p className="text-xs text-[var(--color-error)]" data-testid="library-rename-collision">
              An entry named "{trimmed}" already exists here.
            </p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            onClick={handleSubmit}
            disabled={invalid || isPending}
            data-testid="library-rename-confirm"
          >
            {isPending ? 'Renaming…' : 'Rename'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
