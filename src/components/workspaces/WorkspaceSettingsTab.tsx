import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowSquareOut, Archive, ArrowCounterClockwise, Trash, ArrowsClockwise } from '@phosphor-icons/react'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Button } from '@/components/ui/button'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { useAutoSave } from '@/hooks/useAutoSave'
import { useUiStore } from '@/store/ui'
import {
  updateWorkspace,
  deleteWorkspace,
  workspacesQueryKeys,
  isApiError,
  fetchWorkspaceInstructions,
  updateWorkspaceInstructions,
} from '@/lib/api'
import type { Workspace } from '@/lib/api'

interface WorkspaceSettingsTabProps {
  workspace: Workspace
}

// Item 4 / item 10: same inline auth-token read the rest of the app uses
// (sessionStorage preferred, localStorage fallback — see WorkspaceTeamTab's
// readAuthToken for the sibling pattern; the hook's own built-in flush now
// rides the CSRF header instead, post-ADR-044). No shared accessor exists
// (getAuthHeaders is module-private in src/lib/api.ts), so this is the
// established convention rather than a new one.
function readAuthToken(): string | undefined {
  if (typeof sessionStorage === 'undefined') return undefined
  return (
    sessionStorage.getItem('omnipus_auth_token') ??
    localStorage.getItem('omnipus_auth_token') ??
    undefined
  )
}

/**
 * Workspace Settings tab — properties with the S1 auto-save pattern.
 * Name / description / repository auto-save on debounce; archive + delete are
 * explicit destructive actions; owner + team are surfaced read-only here (team
 * is edited on the Team tab — the delegation-graph editor).
 */
export function WorkspaceSettingsTab({ workspace }: WorkspaceSettingsTabProps) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)

  const [name, setName] = useState(workspace.name)
  const [description, setDescription] = useState(workspace.description ?? '')
  const [repository, setRepository] = useState(workspace.repository ?? '')
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [instructionsContent, setInstructionsContent] = useState('')

  // Draft-ownership rule (see useAutoSave's `onSaved` doc comment): a dirty
  // field is never overwritten by server data. Each auto-saved field group
  // below gets its own dirty flag — set on every onChange, cleared only when
  // that group's `onSaved` confirms the save snapshot still equals the live
  // draft — and the matching hydration effect skips entirely while dirty.
  // Two independent groups here (name/description/repository vs.
  // instructions) since they save through two separate useAutoSave
  // instances and can go dirty/settle independently.
  const identityDirtyRef = useRef(false)
  const markIdentityDirty = () => { identityDirtyRef.current = true }
  const instructionsDirtyRef = useRef(false)
  const markInstructionsDirty = () => { instructionsDirtyRef.current = true }

  // D3 residual (2nd site) fix: reactive readiness flag for the identity
  // group's useAutoSave, mirroring `instructionsHydrated` below. The
  // previous RCA on this group called it "safe by construction" because it
  // is prop-seeded (`useState(workspace.name)`) rather than query-fetched —
  // true for the very FIRST mount (no async gap: `workspace` is already
  // fully resolved by the time this component renders), which is why this
  // starts `true`, unlike `instructionsHydrated` (which starts `false`
  // because its data source, `fetchWorkspaceInstructions`, always has a
  // real network gap even on first mount).
  //
  // That "safe by construction" analysis does NOT hold across a WORKSPACE
  // SWITCH, though — this component persists across switches (no `key` prop
  // at the route), and `name`/`description`/`repository` are re-synced by
  // the `useEffect` below, which only runs AFTER the commit where
  // `workspace.id` (and thus `formData.workspaceId`) already changed. That
  // produces one real committed render with the NEW workspace's id paired
  // with the OLD workspace's still-stale name/description/repository state
  // — a self-inconsistent snapshot useAutoSave's change-detection reads as
  // "different from the last thing I saw" relative to the fully-settled OLD
  // snapshot. The FOLLOWING render (once the effect re-syncs the fields to
  // the new workspace) is ALSO read as "different" relative to that
  // mismatched intermediate snapshot — two back-to-back "changes" with no
  // real user edit in either, which arms the debounce and fires a spurious
  // `updateWorkspace` PUT echoing the new workspace's own (already correct)
  // identity fields back to itself. Confirmed by tracing the effect/commit
  // order, not assumed — see the REVERT-PROOF test for this group.
  //
  // Reset via the SAME mid-render "adjusting state during render" pattern
  // used for `instructionsHydrated` below (see `prevWorkspaceIdForReset`) —
  // not a separate `useEffect` — so the very FIRST committed render after a
  // workspace switch already carries `identityHydrated = false`, and
  // useAutoSave's `wasDisabledRef` re-arm logic captures the freshly-synced
  // fields as its new baseline instead of treating them as an edit.
  //
  // UX decision (deliberately NOT mirroring `disabled={!instructionsHydrated}`
  // on the Instructions `<Textarea>` below): the Name/Description/Repository
  // inputs are NOT disabled while `identityHydrated` is false. Unlike
  // Instructions, there is no real network-loading window here worth
  // surfacing to the user — `workspace` is already fully loaded data by the
  // time this tab renders, and the "unhydrated" window this flag closes is
  // a same-tick React commit/effect ordering artifact (resolves within a
  // frame, no visible delay). Disabling the fields for that frame on EVERY
  // workspace switch would be a visible, user-perceptible flicker/lock on
  // ordinary navigation — worse UX than the invisible extra background PUT
  // it would prevent. This flag exists purely to gate useAutoSave's internal
  // bookkeeping, not to gate the UI.
  const [identityHydrated, setIdentityHydrated] = useState(true)

  // D3 / UAT spurious-PUT fix: reactive readiness flag for the instructions
  // group's useAutoSave. Unlike `identity` (prop-seeded — `workspace.name`
  // etc. are already loaded by the time this component renders, no async
  // gap), `instructions` has its OWN dedicated GET
  // (`fetchWorkspaceInstructions`) that resolves strictly after mount, so
  // the textarea starts at the hardcoded `''` default and hydrates later —
  // the exact shape of the D3 bug. `instructionsHydrated` is set at the END
  // of the instructions hydration effect below, so useAutoSave's baseline
  // is captured from the hydrated content, not the default. Reset on
  // workspace switch (this component persists across workspace navigation
  // — see the dirty-flag reset effect immediately below) so the NEXT
  // workspace doesn't inherit "ready" from the previous one.
  const [instructionsHydrated, setInstructionsHydrated] = useState(false)

  // Switching to a different workspace (identity change) always resets both
  // dirty flags — this is a different record, not a refresh of the same one,
  // so any "unsaved" local edits belonged to the PREVIOUS workspace and must
  // not block re-hydration for the new one.
  //
  // D3 residual-race fix (found empirically while verifying the useAutoSave
  // re-arm fix against repeated workspace switches — see useAutoSave.ts's
  // `wasDisabledRef`): this reset USED to live in a `useEffect(..., [workspace.id])`,
  // which only runs one render AFTER the `workspace.id` prop actually
  // changes. `instructionsFormData` below embeds `workspaceId: workspace.id`
  // directly (Item 10's cross-workspace-timer tag) — so the FIRST render
  // after a workspace switch already carries the NEW id inside useAutoSave's
  // tracked `data`, while `disabled` (driven by `instructionsHydrated`,
  // still not yet reset by the effect) was STILL false for that one render.
  // useAutoSave saw "data changed" (the id differs) while still enabled and
  // fired a save carrying the NEW workspace's id with the OLD (stale,
  // pre-switch) `instructionsContent` — silently overwriting the new
  // workspace's instructions with the previous workspace's stale draft. That
  // is strictly worse than the ordinary D3 echo (it's not even an echo of
  // the new workspace's own data — it PUTs someone else's stale content).
  //
  // Fixed with React's documented "adjusting state when a prop changes"
  // pattern (calling the setter conditionally DURING render, not inside an
  // effect: https://react.dev/learn/you-might-not-need-an-effect#adjusting-some-state-when-a-prop-changes).
  // React treats a state update issued mid-render as "discard this render,
  // re-render immediately with the new state" — so the reset lands in the
  // SAME commit `workspace.id` is first observed, and there is no
  // intermediate commit where the id has changed but `disabled` has not.
  const [prevWorkspaceIdForReset, setPrevWorkspaceIdForReset] = useState(workspace.id)
  if (workspace.id !== prevWorkspaceIdForReset) {
    setPrevWorkspaceIdForReset(workspace.id)
    identityDirtyRef.current = false
    instructionsDirtyRef.current = false
    setInstructionsHydrated(false)
    // D3 residual (2nd site) fix: re-arm the identity group's readiness gate
    // in the SAME mid-render reset — see `identityHydrated`'s doc comment
    // above for why this must happen here rather than in a separate effect.
    setIdentityHydrated(false)
  }

  // Re-hydrate local form state when the workspace identity changes (navigating
  // between workspaces while the Settings tab stays mounted) or when the record
  // is refreshed from the server — but never while the operator has unsaved
  // local edits in this field group (identityDirtyRef).
  useEffect(() => {
    if (identityDirtyRef.current) return
    setName(workspace.name)
    setDescription(workspace.description ?? '')
    setRepository(workspace.repository ?? '')
    // D3 residual (2nd site) fix: flip the reactive readiness flag in the
    // SAME commit the re-synced fields land (see `identityHydrated`'s doc
    // comment above) — batched with the three setters above into one
    // render, so useAutoSave never observes a commit where `disabled` is
    // already false but the data is still the stale pre-switch snapshot.
    setIdentityHydrated(true)
  }, [workspace.id, workspace.name, workspace.description, workspace.repository])

  // Fetch and track workspace instructions (AGENT.md content).
  const {
    data: instructionsData,
    isError: instructionsError,
    refetch: refetchInstructions,
  } = useQuery({
    queryKey: workspacesQueryKeys.instructions(workspace.id),
    queryFn: () => fetchWorkspaceInstructions(workspace.id),
  })

  useEffect(() => {
    if (instructionsData === undefined) return
    // Draft-ownership rule: never overwrite an in-progress local edit with
    // server data. Note this check is intentionally NOT what gates
    // `instructionsHydrated` below — the readiness flag must flip true the
    // moment the GET resolves regardless of dirtiness, or a user who starts
    // typing before the GET lands would leave useAutoSave disabled forever
    // (its own baseline-capture logic never gets a chance to run, so
    // `hasPendingChanges()` would never see anything pending either — a
    // silent-data-loss regression the disabled `<Textarea>` below actually
    // prevents by making that race impossible: the field is inert until
    // `instructionsHydrated` is true).
    if (!instructionsDirtyRef.current) {
      setInstructionsContent(instructionsData.content)
    }
    setInstructionsHydrated(true)
  }, [instructionsData])

  // Item 10 (cross-workspace timer, cheap variant): tag the autosave data
  // with the workspace it was captured for. A debounce timer armed while
  // editing THIS workspace's instructions can still be outstanding if the
  // operator switches to a different workspace before it fires; `saveFn`
  // below no-ops when `data.workspaceId` no longer matches the CURRENT
  // `workspace.id` prop rather than writing this workspace's stale draft
  // over whichever workspace is now active. See the identity `formData`
  // below for the twin fix in this same file.
  const instructionsFormData = useMemo(
    () => ({ workspaceId: workspace.id, content: instructionsContent }),
    [workspace.id, instructionsContent],
  )

  // Item 4 (pagehide flush): keepalive-flush the instructions PUT on tab
  // close / page hide / backgrounding, mirroring AgentProfile's `flushUrl`
  // pattern. A plain `flushUrl` can't be used here because the tracked
  // `data` carries `workspaceId` (for the item-10 guard above), which is
  // NOT part of the wire body (`{content}` only, per
  // `updateWorkspaceInstructions`) — `beaconFlush` lets us keep watching
  // the tagged object for isCurrent/dirty purposes while still sending the
  // correct wire shape.
  const instructionsBeaconFlush = useCallback(() => {
    const token = readAuthToken()
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    if (token) headers['Authorization'] = `Bearer ${token}`
    void fetch(`/api/v1/workspaces/${encodeURIComponent(workspace.id)}/instructions`, {
      method: 'PUT',
      keepalive: true,
      headers,
      body: JSON.stringify({ content: instructionsContent }),
    }).catch((err) => {
      // Match the useAutoSave.ts unmount-flush precedent: a failed
      // best-effort flush is at least discoverable in the console rather
      // than silently dropped.
      console.error('[WorkspaceSettingsTab] instructions beacon flush failed:', err)
    })
  }, [workspace.id, instructionsContent])

  const { status: instructionsSaveStatus, error: instructionsSaveError } = useAutoSave(
    instructionsFormData,
    async (data) => {
      // Item 10: a stale timer from a workspace we've since navigated away
      // from — no-op instead of writing its draft into the CURRENT workspace.
      if (data.workspaceId !== workspace.id) return
      await updateWorkspaceInstructions(workspace.id, data.content)
      await queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.instructions(workspace.id) })
    },
    {
      // D3 / UAT spurious-PUT fix: `instructionsError` alone left the
      // group enabled for the ENTIRE duration of the initial GET (no
      // readiness gate at all) — useAutoSave captured the hardcoded ''
      // default as its baseline on mount, so the later hydration commit
      // looked like a genuine edit and fired a spurious PUT echoing the
      // fetched content straight back. `!instructionsHydrated` closes that
      // window; the `<Textarea>` below is also disabled until hydrated so
      // there is no way for a user edit to race the GET and get silently
      // treated as "already saved" instead.
      disabled: instructionsError || !instructionsHydrated,
      // Long-form surface — raised from the 500ms default so a normal
      // typing cadence in a multi-paragraph instructions doc doesn't fire a
      // save (and its own invalidate/refetch echo) on nearly every pause.
      debounceMs: 1500,
      onSaved: (_saved, isCurrent) => {
        // Draft-ownership rule — see useAutoSave onSaved docs; clear only
        // when isCurrent.
        if (isCurrent) instructionsDirtyRef.current = false
      },
      beaconFlush: instructionsBeaconFlush,
    },
  )

  const isDefault = workspace.is_default === true
  const isArchived = workspace.status === 'archived'

  // Auto-save the editable text fields. Name is required — an empty (trimmed)
  // name is not sent (the hook skips when the payload is unchanged; we guard
  // empties here).
  //
  // Item 2 (trim-vs-raw isCurrent gap): this carries RAW (untrimmed) values —
  // trimming happens inside saveFn, on the WIRE payload only. useAutoSave's
  // `isCurrent` deep-compares this object before vs. after the save's
  // round-trip; if it compared TRIMMED values, typing a trailing space
  // ("New name ") would trim-equal the already-saved "New name", clearing
  // `identityDirtyRef` on a raw draft that still differs from the server.
  // The hydration effect above writes the RAW `workspace.name` prop straight
  // into state, so a refetch landing right after that premature clear would
  // silently snap the trailing space out from under the operator's cursor
  // mid-edit. Comparing raw-to-raw closes the gap.
  //
  // Item 10: also carries workspaceId — see the instructions block above for
  // the cross-workspace-timer rationale (twin fix, same file).
  const formData = useMemo(
    () => ({
      workspaceId: workspace.id,
      name,
      description,
      repository,
    }),
    [workspace.id, name, description, repository],
  )

  const { status, error, lastSavedAt } = useAutoSave(
    formData,
    async (data) => {
      // Item 10: a stale timer from a workspace we've since navigated away
      // from — no-op instead of writing its draft into the CURRENT workspace.
      if (data.workspaceId !== workspace.id) return
      const trimmedName = data.name.trim()
      if (!trimmedName) {
        throw new Error('Workspace name is required')
      }
      await updateWorkspace(workspace.id, {
        name: trimmedName,
        description: data.description.trim(),
        repository: data.repository.trim(),
      })
      // Item 6 (no-op invalidation): `workspacesQueryKeys.list()` called
      // with no args produces ['workspaces', undefined], which matches ZERO
      // registered queries — every real list query is registered as
      // ['workspaces', {status: 'active'|'archived'}] (see
      // workspacesQueryKeys.list's builder in src/lib/api.ts), and TanStack
      // Query's partial-match invalidation requires the filter key to be a
      // prefix of the stored key at every index — `undefined` at index 1
      // never matches a defined params object there, so this call was a
      // silent no-op. `['workspaces']` (length 1) IS a valid prefix of
      // every 'workspaces'-keyed query — list, detail, delegation,
      // instructions alike — so it actually invalidates on save.
      await queryClient.invalidateQueries({ queryKey: ['workspaces'] })
    },
    {
      debounceMs: 600,
      // D3 residual (2nd site) fix: see `identityHydrated`'s doc comment
      // above for why this group — despite being prop-seeded, not
      // query-fetched — still needs a readiness gate to survive a
      // workspace switch without firing a spurious echo PUT.
      disabled: !identityHydrated,
      onSaved: (_saved, isCurrent) => {
        // See instructions' onSaved above — same draft-ownership rule,
        // scoped to this field group's own dirty flag.
        if (isCurrent) identityDirtyRef.current = false
      },
    },
  )

  const archiveMutation = useMutation({
    mutationFn: () =>
      updateWorkspace(workspace.id, { status: isArchived ? 'active' : 'archived' }),
    onSuccess: async () => {
      // Item 6: see the identity saveFn's comment above — `.list()` with no
      // args matches zero queries; `['workspaces']` prefix-matches all of them.
      await queryClient.invalidateQueries({ queryKey: ['workspaces'] })
      addToast({
        message: isArchived ? 'Workspace restored' : 'Workspace archived',
        variant: 'success',
      })
    },
    onError: (err) =>
      addToast({
        message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed',
        variant: 'error',
      }),
  })

  const deleteMutation = useMutation({
    mutationFn: () => deleteWorkspace(workspace.id),
    onSuccess: async () => {
      // Item 6: see the identity saveFn's comment above.
      await queryClient.invalidateQueries({ queryKey: ['workspaces'] })
      addToast({ message: 'Workspace deleted', variant: 'success' })
      setConfirmDelete(false)
      void navigate({ to: '/' })
    },
    onError: (err) => {
      addToast({
        message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed',
        variant: 'error',
      })
      setConfirmDelete(false)
    },
  })


  return (
    <div className="absolute inset-0 overflow-y-auto">
      <div className="max-w-2xl mx-auto px-6 py-6 flex flex-col gap-6">
        {/* Header with autosave indicator */}
        <div className="flex items-center justify-between">
          <h2 className="font-headline text-lg font-bold text-[var(--color-secondary)]">
            Workspace settings
          </h2>
          <AutoSaveIndicator status={status} error={error} lastSavedAt={lastSavedAt} />
        </div>

        {/* Name */}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="ws-name">Name</Label>
          <Input
            id="ws-name"
            value={name}
            onChange={(e) => { markIdentityDirty(); setName(e.target.value) }}
            maxLength={200}
            placeholder="Workspace name"
            className="bg-[var(--color-surface-2)]"
          />
          {name.trim().length === 0 && (
            <span className="text-xs text-[var(--color-error)]">Name is required.</span>
          )}
        </div>

        {/* Description */}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="ws-description">Description</Label>
          <Textarea
            id="ws-description"
            value={description}
            onChange={(e) => { markIdentityDirty(); setDescription(e.target.value) }}
            maxLength={2000}
            rows={3}
            placeholder="What is this workspace for?"
            className="bg-[var(--color-surface-2)] resize-none"
          />
        </div>

        {/* Repository */}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="ws-repository">Repository</Label>
          <div className="flex items-center gap-2">
            <Input
              id="ws-repository"
              value={repository}
              onChange={(e) => { markIdentityDirty(); setRepository(e.target.value) }}
              placeholder="https://github.com/org/repo"
              className="bg-[var(--color-surface-2)] flex-1"
            />
            {repository.trim() && /^https?:\/\//.test(repository.trim()) && (
              <a tabIndex={0}
                href={repository.trim()}
                target="_blank"
                rel="noopener noreferrer"
                className="flex items-center justify-center h-9 w-9 rounded-md border border-[var(--color-border)] text-[var(--color-muted)] hover:text-[var(--color-accent)] hover:border-[var(--color-accent)]/40 transition-colors"
                aria-label="Open repository in a new tab"
                title="Open repository"
              >
                <ArrowSquareOut size={16} />
              </a>
            )}
          </div>
        </div>

        {/* Workspace / Project Instructions */}
        <div className="flex flex-col gap-1.5">
          <div className="flex items-center justify-between">
            <Label htmlFor="ws-instructions">
              Workspace / Project Instructions
            </Label>
            {!instructionsError && (
              <AutoSaveIndicator status={instructionsSaveStatus} error={instructionsSaveError} />
            )}
          </div>
          <p className="text-xs text-[var(--color-muted)]">
            Applied to every agent working in this workspace, on top of their persona. Like a project CLAUDE.md.
          </p>
          {instructionsError ? (
            <div className="flex flex-col items-center gap-3 py-4 text-center rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)]">
              <p className="text-sm text-[var(--color-error)]">Could not load project instructions.</p>
              <Button
                size="sm"
                variant="outline"
                onClick={() => void refetchInstructions()}
                className="gap-1.5"
              >
                <ArrowsClockwise size={13} />
                Retry
              </Button>
            </div>
          ) : (
            <Textarea
              id="ws-instructions"
              value={instructionsContent}
              onChange={(e) => { markInstructionsDirty(); setInstructionsContent(e.target.value) }}
              placeholder={"# Project Instructions\n\nDescribe conventions, tech stack, coding standards, and team preferences that every agent should follow in this workspace."}
              rows={10}
              className="bg-[var(--color-surface-2)] text-xs font-mono resize-none"
              // D3 fix: inert until hydrated — closes the race where a very
              // fast edit lands before `fetchWorkspaceInstructions` resolves
              // and gets silently adopted as useAutoSave's "already saved"
              // baseline instead of actually being persisted (see
              // `instructionsHydrated`'s doc comment above).
              disabled={!instructionsHydrated}
            />
          )}
        </div>

        {/* Danger zone */}
        <div className="mt-2 flex flex-col gap-3 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4">
          <span className="text-xs font-semibold uppercase tracking-widest text-[var(--color-muted)]">
            Manage
          </span>
          <div className="flex flex-wrap items-center gap-2">
            {(!isDefault || isArchived) && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => archiveMutation.mutate()}
                disabled={archiveMutation.isPending}
                className="gap-1.5"
              >
                {isArchived ? <ArrowCounterClockwise size={14} /> : <Archive size={14} />}
                {isArchived ? 'Restore workspace' : 'Archive workspace'}
              </Button>
            )}

            {!isDefault && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => setConfirmDelete(true)}
                className="gap-1.5 border-[var(--color-error)]/40 text-[var(--color-error)] hover:bg-[var(--color-error)]/10"
              >
                <Trash size={14} />
                Delete workspace
              </Button>
            )}
          </div>
          {isDefault && (
            <span className="text-xs text-[var(--color-muted)]">
              The default workspace cannot be archived or deleted.
            </span>
          )}
        </div>
      </div>

      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this workspace?</AlertDialogTitle>
            <AlertDialogDescription>
              This permanently deletes “{workspace.name}” and cascade-deletes its tasks and
              session links. This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => deleteMutation.mutate()}
              disabled={deleteMutation.isPending}
              className="bg-[var(--color-error)] text-white hover:bg-[var(--color-error)]/90"
            >
              {deleteMutation.isPending ? 'Deleting…' : 'Delete'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
