import { useEffect, useMemo, useRef, useState } from 'react'
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

  const { status: instructionsSaveStatus, error: instructionsSaveError } = useAutoSave(
    instructionsContent,
    async (content) => {
      await updateWorkspaceInstructions(workspace.id, content)
      await queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.instructions(workspace.id) })
    },
    {
      disabled: instructionsError,
      // Long-form surface — raised from the 500ms default so a normal
      // typing cadence in a multi-paragraph instructions doc doesn't fire a
      // save (and its own invalidate/refetch echo) on nearly every pause.
      debounceMs: 1500,
      onSaved: (_saved, isCurrent) => {
        // Only clear dirty when the save snapshot still equals the live
        // draft — if the operator kept typing during the PUT round-trip,
        // `isCurrent` is false and the flag stays armed so the hydration
        // guard above keeps rejecting the stale echo until the queued
        // re-save (useAutoSave's own serialization) persists the newer text.
        if (isCurrent) instructionsDirtyRef.current = false
      },
    },
  )

  const isDefault = workspace.is_default === true
  const isArchived = workspace.status === 'archived'

  // Auto-save the editable text fields. Name is required — an empty name is not
  // sent (the hook skips when the payload is unchanged; we guard empties here).
  const formData = useMemo(
    () => ({
      name: name.trim(),
      description: description.trim(),
      repository: repository.trim(),
    }),
    [name, description, repository],
  )

  const { status, error, lastSavedAt } = useAutoSave(
    formData,
    async (data) => {
      if (!data.name) {
        throw new Error('Workspace name is required')
      }
      await updateWorkspace(workspace.id, {
        name: data.name,
        description: data.description,
        repository: data.repository,
      })
      await queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.list() })
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
      await queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.list() })
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
      await queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.list() })
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
