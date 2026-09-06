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
//
// # Mount-state UI (library-b-c-design-2026-09-07.md "Add mount → existing
//   vault auto-detected")
//
// The backend already runs knowledge-base detection (and starts indexing) the
// moment a mount is attached — there is no separate "open vault" step. This
// register is where that shows: each row asks
// GET /library/{ws}/knowledge?path=… for ITS OWN mount path (one call per row
// here is deliberate and bounded — this list is a handful of operator-added
// grants, not the potentially-huge file listing C3's row icons had to avoid
// probing) and layers the `knowledge_index_progress` WebSocket frame on top
// (via useKnowledgeIndexStore — NO polling, matching KnowledgePanel's own
// contract). `resolveKnowledgeFirstRunState`/`KnowledgeEmptyState` are reused
// verbatim from the knowledge surface rather than re-implemented, so the
// honesty rules there (no invented percentages, "0 of 0" never shown, a
// finished index says nothing) apply here too.

import { useQuery } from '@tanstack/react-query'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Warning, SpinnerGap } from '@phosphor-icons/react'
import { fetchKnowledgeBaseInfo } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'
import { useKnowledgeIndexStore } from '@/store/knowledgeIndex'
import { resolveKnowledgeFirstRunState } from './knowledge/KnowledgePanel'
import { KnowledgeEmptyState } from './knowledge/KnowledgeEmptyState'
import { MountIcon } from './icons'

interface LibraryMountsDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The workspace's mounted entries, as returned by the work-tree listing. */
  mounts: LibraryEntry[]
  /** Needed to ask the per-mount knowledge-base-info question. */
  workspaceId: string | null
  workspaceName: string
  onUnmount: (entry: LibraryEntry) => void
  isPending: boolean
}

/**
 * One mount row's vault/indexing state. Its own component (not inlined in the
 * `mounts.map()` below) because it calls its own `useQuery` — hooks cannot be
 * called from inside a loop in the parent.
 */
function MountVaultState({ workspaceId, path }: { workspaceId: string | null; path: string }) {
  const progressByCollection = useKnowledgeIndexStore((s) => s.byCollection)
  const query = useQuery({
    queryKey: ['knowledge-base-info', workspaceId, path],
    queryFn: () => fetchKnowledgeBaseInfo(workspaceId as string, path),
    enabled: workspaceId !== null,
    refetchOnWindowFocus: false,
    retry: false,
  })

  if (query.isPending) {
    return (
      <p
        data-testid="library-mounts-vault-checking"
        role="status"
        className="mt-1.5 flex items-center gap-1.5 text-[11px] text-[var(--color-muted)]"
      >
        <SpinnerGap size={12} aria-hidden="true" className="animate-spin" />
        Checking whether this mount is a vault
      </p>
    )
  }
  // The check itself failing is not "ordinary folder" (E-9's reasoning
  // applies here too) — but this register's own job is revocation, not
  // knowledge-base diagnosis, so it says just enough to not overclaim and
  // leaves the detailed reason to the folder's own KnowledgePanel.
  if (query.isError) {
    return (
      <p
        data-testid="library-mounts-vault-error"
        role="alert"
        className="mt-1.5 text-[11px] text-[var(--color-error)]"
      >
        Could not check whether this mount is a vault.
      </p>
    )
  }

  // Mirrors KnowledgePanel.tsx's own lookup: a plain record read, keyed by
  // this info's own collection_id — resolveKnowledgeFirstRunState re-checks
  // the id itself, so a mis-keyed entry could not drive this row regardless.
  const info = query.data
  const progress = info.collection_id !== undefined ? progressByCollection[info.collection_id] : undefined
  const state = resolveKnowledgeFirstRunState(info, progress)
  // 'not_a_knowledge_base' and 'ready' both render nothing from
  // KnowledgeEmptyState with no `onCreateCollection` wired here — an
  // ordinary mounted folder says nothing extra, and a finished index says
  // nothing extra either, matching that component's own honesty rule (a
  // banner shown on every visit trains people to ignore the real warning).
  return <KnowledgeEmptyState state={state} className="mt-1.5 px-2.5 py-2" />
}

export function LibraryMountsDialog({
  open,
  onOpenChange,
  mounts,
  workspaceId,
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
                className="rounded border border-[var(--color-border)] px-3 py-2"
              >
                <div className="flex items-center gap-3">
                  {/* Locked icon system (C3): Mount = --color-mount, escalated
                      to --color-warning for a broad grant — same rule
                      LibraryEntryRow applies to this same entry in the tree. */}
                  <MountIcon
                    size={18}
                    className={mount.broad ? 'text-[var(--color-warning)]' : 'text-[var(--color-mount)]'}
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
                <MountVaultState workspaceId={workspaceId} path={entry.path} />
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
