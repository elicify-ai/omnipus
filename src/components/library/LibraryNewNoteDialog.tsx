// LibraryNewNoteDialog — creates a new markdown note in the CURRENT directory
// (UAT #699 / D-115, 2026-09-13).
//
// Before this the create menu had no "New note" at all, and a brand-new
// knowledge base was a silent dead end: the only UI route to a note was
// Copy… on an existing file. The agent door could always create notes; the
// founder rule is that a UI gap is acceptable only when it is STATED, and the
// empty state's own "how do I add a note" sentence was not even rendered.
//
// Mirrors LibraryNewFolderDialog (single name field, client-side validation,
// same LibraryErrorBanner treatment) rather than inventing a new dialog
// style — this is the sibling action to New folder. The note is written
// through `createLibraryTextFile` (PUT .../content with the absent version
// token), so a name that is already taken is refused by the server with a
// 409 rather than overwriting: the client-side collision check here is a
// nicety that avoids a round trip, not the guarantee.
//
// The `.md` extension is appended when the name has no extension at all, so
// typing "Meeting notes" produces "Meeting notes.md". A name that already
// ends in `.md`/`.markdown` is kept as typed. Any OTHER extension is refused:
// this dialog creates NOTES, and a "note" named `data.json` would be a text
// file the knowledge index ignores — a surprise best refused up front.

import { useEffect, useState } from 'react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { LibraryErrorBanner } from './LibraryErrorBanner'
import { returnFocusToCreateMenu } from './returnFocusToCreateMenu'

interface LibraryNewNoteDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Sibling entry names in the CURRENT directory — used for the client-side collision nicety. */
  siblingNames: ReadonlySet<string>
  /** Receives the FINAL file name (extension applied). */
  onSubmit: (fileName: string) => void
  isPending: boolean
  /** Set by the parent when the server rejects the create (e.g. a 409
   * because the file appeared meanwhile). Rendered as a persistent banner;
   * the parent clears it whenever the dialog is (re)opened. */
  error?: string
}

const MARKDOWN_EXTS = new Set(['md', 'markdown'])

/**
 * The file name a typed note title resolves to: `.md` appended when there is
 * no extension, kept as typed when it already carries a markdown one.
 * Exported for the test; pure.
 */
export function resolveNoteFileName(typed: string): string {
  const trimmed = typed.trim()
  const dot = trimmed.lastIndexOf('.')
  if (dot > 0 && dot < trimmed.length - 1) {
    const ext = trimmed.slice(dot + 1).toLowerCase()
    if (MARKDOWN_EXTS.has(ext)) return trimmed
  }
  return `${trimmed}.md`
}

/** True when the typed name carries an extension that is not markdown. */
export function hasNonMarkdownExtension(typed: string): boolean {
  const trimmed = typed.trim()
  const dot = trimmed.lastIndexOf('.')
  if (dot <= 0 || dot === trimmed.length - 1) return false
  return !MARKDOWN_EXTS.has(trimmed.slice(dot + 1).toLowerCase())
}

export function LibraryNewNoteDialog({
  open,
  onOpenChange,
  siblingNames,
  onSubmit,
  isPending,
  error,
}: LibraryNewNoteDialogProps) {
  const [name, setName] = useState('')

  useEffect(() => {
    if (open) setName('')
  }, [open])

  const trimmed = name.trim()
  const hasSlash = trimmed.includes('/')
  // A note name is a single path SEGMENT (this dialog always creates inside
  // the CURRENT directory), so ".." anywhere in it is never a legitimate
  // name, only an escape attempt — named first, like New folder (D-128).
  const hasTraversal = trimmed.includes('..')
  const wrongExtension = !hasSlash && !hasTraversal && hasNonMarkdownExtension(trimmed)
  const fileName = trimmed.length === 0 ? '' : resolveNoteFileName(trimmed)
  const collides = fileName.length > 0 && siblingNames.has(fileName)
  const invalid = trimmed.length === 0 || hasSlash || hasTraversal || wrongExtension || collides

  function handleSubmit() {
    if (invalid) return
    onSubmit(fileName)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent data-testid="library-new-note-dialog" onCloseAutoFocus={returnFocusToCreateMenu}>
        <DialogHeader>
          <DialogTitle>New note</DialogTitle>
          <DialogDescription>
            A markdown file in the current folder. Inside a knowledge base it is indexed and
            searchable as soon as it is saved.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-2">
          <Label htmlFor="library-new-note-input">Note name</Label>
          <Input
            id="library-new-note-input"
            data-testid="library-new-note-input"
            value={name}
            autoFocus
            placeholder="Meeting notes"
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') handleSubmit()
            }}
          />
          {!invalid && fileName !== trimmed && (
            <p className="text-xs text-[var(--color-muted)]" data-testid="library-new-note-resolved">
              Will be saved as "{fileName}".
            </p>
          )}
          {hasTraversal && (
            <p className="text-xs text-[var(--color-error)]" data-testid="library-new-note-traversal">
              A note name can't contain "..".
            </p>
          )}
          {!hasTraversal && hasSlash && (
            <p className="text-xs text-[var(--color-error)]" data-testid="library-new-note-slash">
              A note name can't contain "/".
            </p>
          )}
          {wrongExtension && (
            <p className="text-xs text-[var(--color-error)]" data-testid="library-new-note-extension">
              A note is a markdown file — leave the extension off, or use ".md".
            </p>
          )}
          {!hasSlash && !hasTraversal && !wrongExtension && collides && (
            <p className="text-xs text-[var(--color-error)]" data-testid="library-new-note-collision">
              A file named "{fileName}" already exists here.
            </p>
          )}
          {error && <LibraryErrorBanner message={error} testId="library-new-note-error" />}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={invalid || isPending} data-testid="library-new-note-confirm">
            {isPending ? 'Creating…' : 'Create'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
