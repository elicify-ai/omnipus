// LibraryAddMountDialog — pick a real folder on this Mac and make it writable
// inside a workspace (ADR-063 FR-7.1).
//
// # Why there is a browser here instead of a native picker
//
// A web page CANNOT open the operating system's folder picker and learn a real
// filesystem path — the browser deliberately withholds it, and
// <input webkitdirectory> yields file contents rather than a location. So this
// cannot work the way every native app has taught people it works. The gateway
// lists folders instead (GET /system/folders) and the operator navigates them
// here. Typing a path stays available for anyone who prefers it.
//
// # Why the verdict is shown before the choice, not after
//
// Every listed folder carries its own mountable/broad verdict, so a refused
// folder is disabled at the point of selection and a broad one is flagged
// before it is picked. Accepting a choice and only then refusing it is the
// worse control: it teaches people to click through refusals.

import { useEffect, useState } from 'react'
import { CaretUp, FolderSimple, Warning, Prohibit, CheckCircle, SpinnerGap } from '@phosphor-icons/react'
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
import { fetchHostFolders, type HostFolderListing } from '@/lib/api'

interface LibraryAddMountDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Confirmed target path. The parent owns the create call and its errors. */
  onConfirm: (hostPath: string) => void
  isPending: boolean
  /** Server-side failure text, surfaced verbatim rather than re-worded. */
  error?: string
}

export function LibraryAddMountDialog({
  open,
  onOpenChange,
  onConfirm,
  isPending,
  error,
}: LibraryAddMountDialogProps) {
  const [path, setPath] = useState('')
  const [browsing, setBrowsing] = useState(false)
  const [listing, setListing] = useState<HostFolderListing | null>(null)
  const [listError, setListError] = useState<string>()
  const [loading, setLoading] = useState(false)

  // Reset on every open so a previous attempt's path and error never bleed
  // into a fresh one — this dialog grants disk access, and a stale prefill is
  // the kind of thing someone confirms without re-reading.
  useEffect(() => {
    if (open) {
      setPath('')
      setBrowsing(false)
      setListing(null)
      setListError(undefined)
    }
  }, [open])

  async function load(target?: string) {
    setLoading(true)
    setListError(undefined)
    try {
      const next = await fetchHostFolders(target)
      setListing(next)
    } catch (err) {
      setListError(err instanceof Error ? err.message : 'Could not read that folder.')
    } finally {
      setLoading(false)
    }
  }

  function toggleBrowse() {
    const next = !browsing
    setBrowsing(next)
    if (next && !listing) void load(path.trim() || undefined)
  }

  // The selected row's verdict, when the current path is one we have listed.
  const selected = listing?.entries.find((e) => e.path === path)
  const trimmed = path.trim()
  const canSubmit = trimmed.length > 0 && !isPending && selected?.mountable !== false

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl" data-testid="library-add-mount-dialog">
        <DialogHeader>
          <DialogTitle>Add a folder to this workspace</DialogTitle>
          <DialogDescription>
            The agent will be able to read and write here. Everything else on your disk stays
            read-only.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-3">
          <label htmlFor="mount-path" className="text-xs uppercase tracking-wide text-[var(--color-muted)]">
            Folder on your Mac
          </label>
          <Input
            id="mount-path"
            value={path}
            onChange={(e) => setPath(e.target.value)}
            placeholder="/Users/you/Documents/projects/my-repo"
            className="font-mono text-sm"
            data-testid="library-add-mount-path"
          />

          {selected?.mountable === false && (
            <p
              className="flex items-start gap-2 text-sm text-[var(--color-error)]"
              data-testid="library-add-mount-refused"
            >
              <Prohibit size={16} className="mt-0.5 shrink-0" />
              {selected.reason ?? 'This folder cannot be mounted.'}
            </p>
          )}
          {selected?.broad && selected.mountable !== false && (
            <p
              className="flex items-start gap-2 text-sm text-[var(--color-warning)]"
              data-testid="library-add-mount-broad"
            >
              <Warning size={16} className="mt-0.5 shrink-0" />
              {selected.reason ?? 'This is a broad grant.'}
            </p>
          )}
          {selected && selected.mountable && !selected.broad && (
            <p className="flex items-start gap-2 text-sm text-[var(--color-success)]">
              <CheckCircle size={16} className="mt-0.5 shrink-0" />
              Scoped to this folder and what is inside it.
            </p>
          )}

          <div className="flex items-center gap-2">
            <Button type="button" variant="outline" size="sm" onClick={toggleBrowse}>
              {browsing ? 'Hide browser' : 'Browse…'}
            </Button>
            <span className="text-xs text-[var(--color-muted)]">
              A web page cannot open the Mac folder picker, so Omnipus lists your folders instead.
            </span>
          </div>

          {browsing && (
            <div className="rounded border border-[var(--color-border)] overflow-hidden">
              <div className="flex items-center gap-2 px-2 py-1.5 border-b border-[var(--color-border)] bg-[var(--color-surface-2)]">
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  disabled={!listing?.parent || loading}
                  onClick={() => listing?.parent && void load(listing.parent)}
                  aria-label="Go up one folder"
                >
                  <CaretUp size={14} />
                </Button>
                <span className="truncate font-mono text-xs text-[var(--color-muted)]">
                  {listing?.path ?? '…'}
                </span>
                {loading && <SpinnerGap size={14} className="animate-spin ml-auto" />}
              </div>

              <div className="max-h-56 overflow-y-auto" data-testid="library-add-mount-browser">
                {listError && <p className="p-3 text-sm text-[var(--color-error)]">{listError}</p>}
                {!listError && listing?.entries.length === 0 && (
                  <p className="p-3 text-sm text-[var(--color-muted)]">No folders here.</p>
                )}
                {listing?.entries.map((entry) => (
                  <button
                    key={entry.path}
                    type="button"
                    tabIndex={0}
                    onClick={() => {
                      setPath(entry.path)
                      if (entry.mountable) void load(entry.path)
                    }}
                    data-testid={`library-add-mount-row-${entry.name}`}
                    className={`flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm border-b border-[var(--color-border)] last:border-b-0 hover:bg-[var(--color-surface-2)] ${
                      path === entry.path ? 'bg-[var(--color-surface-3)]' : ''
                    }`}
                  >
                    <FolderSimple
                      size={15}
                      weight="fill"
                      className={
                        entry.mountable === false
                          ? 'text-[var(--color-muted)]'
                          : 'text-[var(--color-accent)]'
                      }
                    />
                    <span className={entry.mountable === false ? 'text-[var(--color-muted)]' : ''}>
                      {entry.name}
                    </span>
                    {entry.mountable === false && (
                      <span className="ml-auto text-[11px] text-[var(--color-error)]">
                        Omnipus data — cannot mount
                      </span>
                    )}
                    {entry.mountable !== false && entry.broad && (
                      <span className="ml-auto text-[11px] text-[var(--color-warning)]">broad</span>
                    )}
                  </button>
                ))}
              </div>
            </div>
          )}

          {error && (
            <p className="text-sm text-[var(--color-error)]" data-testid="library-add-mount-error">
              {error}
            </p>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={isPending}>
            Cancel
          </Button>
          <Button
            onClick={() => onConfirm(trimmed)}
            disabled={!canSubmit}
            data-testid="library-add-mount-confirm"
          >
            {isPending ? 'Adding…' : 'Add folder'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
