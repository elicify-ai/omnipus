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
import { fetchHostFolders, type HostFolderListing, type HostFolderEntry } from '@/lib/api'

/** Lexically canonicalize a typed host path — collapse `//` runs, drop `.`
 *  segments, resolve `..` segments, strip trailing slashes — with NO
 *  filesystem access.
 *
 *  Claude review 2026-09-14, cut-list: the pre-submit verdict gate used to
 *  match the TYPED string against the folder listing, so "/tmp/" (or
 *  "/tmp/.", or "/a/../tmp") dodged the breadth banner and the "Add anyway"
 *  second click entirely while naming the same folder as "/tmp". The gate —
 *  and the submit — now run on the canonical spelling, so every way of
 *  writing a folder gets the same verdict. Lexical only, deliberately: the
 *  SERVER stays the authority on what the path resolves to through symlinks
 *  (a system directory is still refused there even if this normalization
 *  cannot see it); this exists so spelling cannot route around the gate. */
export function canonicalizeHostPath(input: string): string {
  const trimmed = input.trim()
  if (trimmed === '') return ''
  const absolute = trimmed.startsWith('/')
  const segments: string[] = []
  for (const segment of trimmed.split('/')) {
    if (segment === '' || segment === '.') continue
    if (segment === '..') {
      if (segments.length > 0 && segments[segments.length - 1] !== '..') {
        segments.pop()
      } else if (!absolute) {
        // A relative path cannot climb above where it started; keep the
        // (leading) ".." rather than silently resolving it against the root.
        segments.push('..')
      }
      continue
    }
    segments.push(segment)
  }
  const joined = segments.join('/')
  return absolute ? `/${joined}` : joined
}

interface LibraryAddMountDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Confirmed target path. The parent owns the create call and its errors. */
  onConfirm: (hostPath: string) => void
  isPending: boolean
  /** Server-side failure text, surfaced verbatim rather than re-worded. */
  error?: string
  /**
   * D-117 response half: the server REFUSED the grant (403). The reason it
   * gave, rendered as the refused banner. Distinct from `error` on purpose —
   * a policy refusal and a transport failure must not read the same, and the
   * parent decides which prop a failure lands in (it sees the status code).
   */
  refusal?: string
  /**
   * D-117 response half: the server CREATED the mount (201) but its body
   * carries `warning` — a broad grant it let through. Shown in THIS dialog
   * (which the parent holds open) rather than a toast: a toast auto-dismisses
   * and this is the one place the operator is already reading about the grant
   * they just made.
   */
  createdWarning?: string
  /** Fired when the operator acknowledges `createdWarning` (the "Done" click). */
  onAcknowledgeWarning?: () => void
}

export function LibraryAddMountDialog({
  open,
  onOpenChange,
  onConfirm,
  isPending,
  error,
  refusal,
  createdWarning,
  onAcknowledgeWarning,
}: LibraryAddMountDialogProps) {
  const [path, setPath] = useState('')
  const [browsing, setBrowsing] = useState(false)
  const [listing, setListing] = useState<HostFolderListing | null>(null)
  // The verdict for the CURRENTLY TYPED/SELECTED path, captured when a row is
  // clicked.
  //
  // It cannot be re-derived from `listing`: clicking a mountable row both sets
  // the path AND navigates into it, so by the next render `listing` holds that
  // folder's CHILDREN — which never contain the selected path itself. The
  // lookup therefore returned undefined and the "broad grant" / "scoped to this
  // folder" banner never appeared for a browsed selection. Only the refusal
  // banner survived, and only because a refused row does not navigate.
  const [selectedVerdict, setSelectedVerdict] = useState<HostFolderEntry | null>(null)
  const [listError, setListError] = useState<string>()
  const [loading, setLoading] = useState(false)
  // UAT D-127 (2026-09-13): the server's refusal is for the path that was
  // SUBMITTED. It used to stay on screen beside a corrected path right up
  // until the next attempt succeeded; now it is shown only while the field
  // still holds the path it was about to.
  const [attemptedPath, setAttemptedPath] = useState<string>()
  // UAT D-117, dialog half (2026-09-13): a TYPED path carries no verdict —
  // only a browsed row does — so `/tmp` mounted in one click with nothing
  // said about its breadth. Before submitting an unverified path the dialog
  // now asks the server for the verdict on that exact folder; a broad one
  // is shown and needs a second, explicit click ("Add anyway"), a refused
  // one is refused here. If the lookup itself fails the submit proceeds and
  // the server's own check (W5's backend refusal) is the authority.
  const [verifying, setVerifying] = useState(false)
  const [broadAcknowledged, setBroadAcknowledged] = useState(false)

  // Reset on every open so a previous attempt's path and error never bleed
  // into a fresh one — this dialog grants disk access, and a stale prefill is
  // the kind of thing someone confirms without re-reading.
  useEffect(() => {
    if (open) {
      setPath('')
      setBrowsing(false)
      setListing(null)
      setListError(undefined)
      setSelectedVerdict(null)
      setAttemptedPath(undefined)
      setVerifying(false)
      setBroadAcknowledged(false)
      // D-117 response half: the parent owns these (it owns the create call);
      // clearing LOCAL attempt state here is enough — the parent clears its
      // own the same way it always cleared `error`, in onOpenChange.
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
  // Prefer the remembered verdict; fall back to the listing for the case where
  // the path matches a row that is still on screen (a refused row, which does
  // not navigate). Both this lookup and everything below run on the
  // CANONICAL spelling, so "/tmp/" cannot dodge the gate that "/tmp" hits.
  const canonical = canonicalizeHostPath(path)
  const selected = selectedVerdict ?? listing?.entries.find((e) => e.path === canonical)
  // D-117 response half: once the server has ANSWERED, the dialog stops being
  // a form and becomes the verdict. `createdWarning` (201 + warning) is the
  // terminal state — the mount exists, so there is nothing left to submit;
  // only Done. `refusal` (403) is scoped to the attempted path exactly like
  // `error` (D-127): it describes THAT path, so editing the path retires it.
  const showingCreatedWarning = createdWarning !== undefined
  const canSubmit =
    !showingCreatedWarning &&
    canonical.length > 0 &&
    !isPending &&
    !verifying &&
    selected?.mountable !== false
  const needsBroadAck = selected?.broad === true && selected.mountable !== false && !broadAcknowledged
  const showServerError = error !== undefined && attemptedPath === canonical
  const showRefusal = refusal !== undefined && attemptedPath === canonical

  /** Resolve the verdict for a typed path from its parent's listing. Returns
   *  the entry when the server lists it, undefined when it does not (or the
   *  lookup failed) — never throws. */
  async function lookUpVerdict(target: string): Promise<HostFolderEntry | undefined> {
    const cut = target.lastIndexOf('/')
    const parent = cut <= 0 ? '/' : target.slice(0, cut)
    try {
      const parentListing = await fetchHostFolders(parent)
      return parentListing.entries.find((e) => e.path === target)
    } catch {
      return undefined
    }
  }

  async function handleConfirm() {
    if (!canSubmit) return
    if (needsBroadAck) {
      // "Add anyway": the warning has been on screen since the previous
      // click; this click is the acknowledgement AND the submit.
      setBroadAcknowledged(true)
      setAttemptedPath(canonical)
      onConfirm(canonical)
      return
    }
    let verdict = selected
    if (verdict === undefined) {
      setVerifying(true)
      verdict = await lookUpVerdict(canonical)
      setVerifying(false)
      if (verdict !== undefined) {
        setSelectedVerdict(verdict)
        if (verdict.mountable === false) return
        if (verdict.broad) return
      }
    }
    setAttemptedPath(canonical)
    onConfirm(canonical)
  }

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

        <div className="flex flex-col gap-[var(--space-2-5)]">
          <label htmlFor="mount-path" className="text-[length:var(--type-utility-xs-size)] uppercase tracking-wide text-[var(--color-muted)]">
            Folder on your Mac
          </label>
          <Input
            id="mount-path"
            value={path}
            onChange={(e) => {
              setPath(e.target.value)
              setSelectedVerdict(null)
              setBroadAcknowledged(false)
            }}
            placeholder="/Users/you/Documents/projects/my-repo"
            className="font-mono text-[length:var(--type-body-compact-size)]"
            data-testid="library-add-mount-path"
          />

          {selected?.mountable === false && (
            <p
              className="flex items-start gap-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-error)]"
              data-testid="library-add-mount-refused"
            >
              <Prohibit size={16} className="mt-[var(--space-0-5)] shrink-0" />
              {selected.reason ?? 'This folder cannot be mounted.'}
            </p>
          )}
          {selected?.broad && selected.mountable !== false && (
            <p
              className="flex items-start gap-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-warning)]"
              data-testid="library-add-mount-broad"
            >
              <Warning size={16} className="mt-[var(--space-0-5)] shrink-0" />
              {selected.reason ?? 'This is a broad grant.'}
            </p>
          )}
          {selected && selected.mountable && !selected.broad && (
            <p className="flex items-start gap-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-success)]">
              <CheckCircle size={16} className="mt-[var(--space-0-5)] shrink-0" />
              Scoped to this folder and what is inside it.
            </p>
          )}

          {/* D-117 response half — the SERVER's own verdict, distinct from the
              pre-submission banners above (which reflect a folder listing).
              A 403's reason and a 201's broad-grant warning are the two
              answers that must never live in a toast. */}
          {showRefusal && (
            <p
              className="flex items-start gap-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-error)]"
              data-testid="library-add-mount-dialog-refused"
            >
              <Prohibit size={16} className="mt-[var(--space-0-5)] shrink-0" />
              {refusal}
            </p>
          )}
          {showingCreatedWarning && (
            <p
              className="flex items-start gap-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-warning)]"
              data-testid="library-add-mount-dialog-broad"
            >
              <Warning size={16} className="mt-[var(--space-0-5)] shrink-0" />
              {createdWarning}
            </p>
          )}

          <div className="flex items-center gap-[var(--space-2)]">
            <Button type="button" variant="outline" size="sm" onClick={toggleBrowse}>
              {browsing ? 'Hide browser' : 'Browse…'}
            </Button>
            <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
              A web page cannot open the Mac folder picker, so Omnipus lists your folders instead.
            </span>
          </div>

          {browsing && (
            <div className="rounded border border-[var(--color-border)] overflow-hidden">
              <div className="flex items-center gap-[var(--space-2)] px-[var(--space-2)] py-[var(--space-1)] border-b border-[var(--color-border)] bg-[var(--color-surface-2)]">
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
                <span className="truncate font-mono text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
                  {listing?.path ?? '…'}
                </span>
                {loading && <SpinnerGap size={14} className="animate-spin ml-auto" />}
              </div>

              <div className="max-h-56 overflow-y-auto" data-testid="library-add-mount-browser">
                {listError && <p className="p-[var(--space-2-5)] text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">{listError}</p>}
                {!listError && listing?.entries.length === 0 && (
                  <p className="p-[var(--space-2-5)] text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">No folders here.</p>
                )}
                {listing?.entries.map((entry) => (
                  <Button
                    key={entry.path}
                    variant="ghost"
                    onClick={() => {
                      setPath(entry.path)
                      setSelectedVerdict(entry)
                      if (entry.mountable) void load(entry.path)
                    }}
                    data-testid={`library-add-mount-row-${entry.name}`}
                    className={`h-auto w-full justify-start rounded-none gap-[var(--space-2)] px-[var(--space-2-5)] py-[var(--space-1)] text-left font-[var(--font-weight-regular)] text-[length:var(--type-body-compact-size)] border-b border-[var(--color-border)] last:border-b-0 hover:bg-[var(--color-surface-2)] ${
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
                      <span className="ml-auto text-[length:var(--type-caption-size)] text-[var(--color-error)]">
                        Omnipus data — cannot mount
                      </span>
                    )}
                    {entry.mountable !== false && entry.broad && (
                      <span className="ml-auto text-[length:var(--type-caption-size)] text-[var(--color-warning)]">broad</span>
                    )}
                  </Button>
                ))}
              </div>
            </div>
          )}

          {showServerError && (
            <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]" data-testid="library-add-mount-error">
              {error}
            </p>
          )}
        </div>

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={isPending || showingCreatedWarning}
          >
            {showingCreatedWarning ? 'Close' : 'Cancel'}
          </Button>
          <Button
            onClick={() =>
              showingCreatedWarning ? onAcknowledgeWarning?.() : void handleConfirm()
            }
            disabled={showingCreatedWarning ? false : !canSubmit}
            data-testid="library-add-mount-confirm"
          >
            {showingCreatedWarning
              ? 'Done'
              : isPending
                ? 'Adding…'
                : verifying
                  ? 'Checking…'
                  : needsBroadAck
                    ? 'Add anyway'
                    : 'Add folder'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
