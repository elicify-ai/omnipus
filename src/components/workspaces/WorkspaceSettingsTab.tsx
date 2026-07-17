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

  // Switching to a different workspace (identity change) always resets both
  // dirty flags — this is a different record, not a refresh of the same one,
  // so any "unsaved" local edits belonged to the PREVIOUS workspace and must
  // not block re-hydration for the new one. Declared before the hydration
  // effects below so it runs first within the same commit when workspace.id
  // changes (React runs effects in declaration order).
  useEffect(() => {
    identityDirtyRef.current = false
    instructionsDirtyRef.current = false
  }, [workspace.id])

  // Re-hydrate local form state when the workspace identity changes (navigating
  // between workspaces while the Settings tab stays mounted) or when the record
  // is refreshed from the server — but never while the operator has unsaved
  // local edits in this field group (identityDirtyRef).
  useEffect(() => {
    if (identityDirtyRef.current) return
    setName(workspace.name)
    setDescription(workspace.description ?? '')
    setRepository(workspace.repository ?? '')
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
    if (instructionsDirtyRef.current) return
    if (instructionsData !== undefined) {
      setInstructionsContent(instructionsData.content)
    }
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
      disabled: instructionsError,
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
