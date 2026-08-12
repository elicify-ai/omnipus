// LibraryMountsDialog — the durable register of what this workspace can write
// to on the operator's real disk (ADR-063).
//
// # Why this exists separately from the tree
//
// Mounts appear in the file tree because that is where they physically are, and
// where the agent sees them. But approving a grant is a decision made once, in a
// moment, in a modal. REVOKING one is a decision made months later, by someone
// who has forgotten the grant exists and is not going to find it by browsing
// into the right folder. Only the second needs a permanent home in the
// interface, and this is it.
//
// It is deliberately reachable from a count in the header — a grant you cannot
// see is a grant you cannot review, and a count that only appears when there is
// something to count is the smallest honest version of that.

import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { FolderSimpleDashed, Warning } from '@phosphor-icons/react'
import type { LibraryEntry } from '@/lib/api'

interface LibraryMountsDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The workspace's mounted entries, as returned by the work-tree listing. */
  mounts: LibraryEntry[]
  workspaceName: string
  onUnmount: (entry: LibraryEntry) => void
  isPending: boolean
}

export function LibraryMountsDialog({
  open,
  onOpenChange,
  mounts,
  workspaceName,
  onUnmount,
  isPending,
}: LibraryMountsDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl" data-testid="library-mounts-dialog">
        <DialogHeader>
          <DialogTitle>Mounted folders</DialogTitle>
          <DialogDescription>
            Folders on your Mac that <strong>{workspaceName}</strong> can read and write.
            Everything else on your disk stays read-only.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-2">
          {mounts.length === 0 && (
            <p className="py-6 text-center text-sm text-[var(--color-muted)]">
              No folders are mounted.
            </p>
          )}

          {mounts.map((entry) => {
            const mount = entry.mount
            if (!mount) return null
            return (
              <div
                key={entry.path}
                data-testid={`library-mounts-row-${mount.name}`}
                className="flex items-center gap-3 rounded border border-[var(--color-border)] px-3 py-2"
              >
                <FolderSimpleDashed
                  size={18}
                  className={
                    mount.broad ? 'text-[var(--color-warning)]' : 'text-[var(--color-info)]'
                  }
                />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium">{mount.name}</span>
                    {mount.broad && (
                      <span className="flex items-center gap-1 text-[11px] text-[var(--color-warning)]">
                        <Warning size={12} /> Broad grant
                      </span>
                    )}
                  </div>
                  {/* The real path, because the name alone does not tell you
                      what you granted. */}
                  <p
                    className="truncate font-mono text-[11px] text-[var(--color-muted)]"
                    title={mount.host_path}
                  >
                    {mount.host_path}
                  </p>
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={isPending}
                  onClick={() => onUnmount(entry)}
                  data-testid={`library-mounts-unmount-${mount.name}`}
                >
                  Unmount
                </Button>
              </div>
            )
          })}
        </div>

        <p className="text-xs text-[var(--color-muted)]">
          Unmounting removes access only. Your files stay exactly where they are.
        </p>

        <DialogFooter>
          <Button onClick={() => onOpenChange(false)}>Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
