// LibraryTransferDialog — Move/Copy, including across two workspaces
// (library-spec.md D-9). Both destination workspace AND destination path are
// explicit fields here (mirroring LibraryTransferRequest's shape), so a
// same-workspace move/copy is simply the case where the destination
// workspace equals the source. Only ever reachable from the authenticated
// UI/CLI user — never wired to an agent tool (enforced server-side; D-9).
//
// The server rejects (409) a destination that already exists and never
// silently overwrites (see LibraryTransferRequest's contract doc) — there is
// no "overwrite" outcome an operation like this could produce. The dialog's
// confirm step is therefore the same two-stage gate as LibraryRenameDialog:
// open → review destination → explicit Move/Copy click, and any 409 the
// server does return is surfaced to the user as a real, visible error (never
// swallowed) rather than silently retried or ignored.

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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import type { LibraryEntry, LibraryTransferRequest, LibraryWorkspaceNode } from '@/lib/api'

interface LibraryTransferDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  mode: 'move' | 'copy'
  entry: LibraryEntry | null
  sourceWorkspaceId: string
  workspaces: LibraryWorkspaceNode[]
  onSubmit: (body: LibraryTransferRequest) => void
  isPending: boolean
}

export function LibraryTransferDialog({
  open,
  onOpenChange,
  mode,
  entry,
  sourceWorkspaceId,
  workspaces,
  onSubmit,
  isPending,
}: LibraryTransferDialogProps) {
  const [destWorkspaceId, setDestWorkspaceId] = useState(sourceWorkspaceId)
  const [destPath, setDestPath] = useState('')

  useEffect(() => {
    if (open && entry) {
      setDestWorkspaceId(sourceWorkspaceId)
      setDestPath(entry.path)
    }
  }, [open, entry, sourceWorkspaceId])

  if (!entry) return null

  const trimmedPath = destPath.trim().replace(/^\/+/, '')
  const isNoOp = trimmedPath === entry.path && destWorkspaceId === sourceWorkspaceId
  const invalid = trimmedPath.length === 0 || trimmedPath.includes('..') || isNoOp

  function handleSubmit() {
    if (invalid || !entry) return
    const body: LibraryTransferRequest = {
      from_workspace_id: sourceWorkspaceId,
      from_path: entry.path,
      to_workspace_id: destWorkspaceId,
      to_path: trimmedPath,
    }
    onSubmit(body)
  }

  const verb = mode === 'move' ? 'Move' : 'Copy'

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent data-testid="library-transfer-dialog">
        <DialogHeader>
          <DialogTitle>
            {verb} "{entry.name}"
          </DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-2">
            <Label htmlFor="library-transfer-workspace">Destination workspace</Label>
            <Select value={destWorkspaceId} onValueChange={setDestWorkspaceId}>
              <SelectTrigger id="library-transfer-workspace" data-testid="library-transfer-workspace">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {workspaces.map((ws) => (
                  <SelectItem key={ws.id} value={ws.id}>
                    {ws.name}
                  </SelectItem>
                ))}
                {/* Guard against the current workspace being absent from the
                    virtual-root list (e.g. it hasn't loaded yet) — always
                    offer at least the source so the picker is never empty. */}
                {!workspaces.some((ws) => ws.id === sourceWorkspaceId) && (
                  <SelectItem value={sourceWorkspaceId}>This workspace</SelectItem>
                )}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="library-transfer-path">Destination path</Label>
            <Input
              id="library-transfer-path"
              data-testid="library-transfer-path"
              value={destPath}
              autoFocus
              onChange={(e) => setDestPath(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') handleSubmit()
              }}
            />
            <p className="text-xs text-[var(--color-muted)]">
              Workspace-relative path, e.g. "reports/{entry.name}".
            </p>
            {isNoOp && (
              <p className="text-xs text-[var(--color-error)]" data-testid="library-transfer-noop">
                That's the same location "{entry.name}" is already at.
              </p>
            )}
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            onClick={handleSubmit}
            disabled={invalid || isPending}
            data-testid="library-transfer-confirm"
          >
            {isPending ? `${verb === 'Move' ? 'Moving' : 'Copying'}…` : verb}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
