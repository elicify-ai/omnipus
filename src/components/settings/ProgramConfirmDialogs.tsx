// ProgramConfirmDialogs — "Disconnect" and "Delete" for a program row (spec
// cli-credentials S1: US-7 AS-3, US-4 AS-4, FR-019, FR-023; UI table row
// "Disconnect confirm").
//
// A plain Dialog, not ConfirmDialog: a refused or failed action must keep the
// dialog open with its message and the action available again, and
// ConfirmDialog's action closes the dialog before the answer arrives.
import { useEffect, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import type { ProgramEntry, ProgramList } from '@/lib/api/generated/openapi-types'
import { deleteProgram, disconnectProgram, programsQueryKeys } from '@/lib/api'
import { disconnectNote, programErrorMessage } from '@/lib/programs'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Callout, CalloutDescription } from '@/components/ui/callout'
import { replaceProgramEntry } from './ProgramPasteDialog'

function ConfirmShell({
  open,
  title,
  description,
  actionLabel,
  error,
  busy,
  onConfirm,
  onOpenChange,
  testId,
}: {
  open: boolean
  title: string
  description: string
  actionLabel: string
  error: string | null
  busy: boolean
  onConfirm: () => void
  onOpenChange: (open: boolean) => void
  testId: string
}) {
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!busy) onOpenChange(next) }}>
      <DialogContent data-testid={testId} aria-busy={busy || undefined}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        {error && (
          <Callout variant="error" data-testid="program-dialog-error">
            <CalloutDescription>{error}</CalloutDescription>
          </Callout>
        )}
        <DialogFooter>
          <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button type="button" variant="destructive" actionState={busy ? 'pending' : 'idle'} onClick={onConfirm}>
            {actionLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function ProgramDisconnectDialog({ entry, onOpenChange }: { entry: ProgramEntry | null; onOpenChange: (open: boolean) => void }) {
  const queryClient = useQueryClient()
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { if (entry) setError(null) }, [entry])
  const disconnect = useMutation({
    // One attempt per action: the person retries, nothing is re-sent on its own.
    retry: false,
    mutationFn: (id: string) => disconnectProgram(id),
    onSuccess: (saved) => {
      queryClient.setQueryData<ProgramList>(programsQueryKeys.list, (list) => replaceProgramEntry(list, saved))
      void queryClient.invalidateQueries({ queryKey: programsQueryKeys.list })
      onOpenChange(false)
    },
    onError: () => {
      setError('Could not disconnect. Try again.')
      void queryClient.invalidateQueries({ queryKey: programsQueryKeys.list })
    },
  })
  if (!entry) return null
  return (
    <ConfirmShell
      open
      testId="program-disconnect-dialog"
      title={`Disconnect ${entry.display_name}?`}
      description={`${disconnectNote(entry.display_name)} Agents keep their ${entry.display_name} switch, but their commands get no token until a new one is saved.`}
      actionLabel="Disconnect"
      error={error}
      busy={disconnect.isPending}
      onConfirm={() => { setError(null); disconnect.mutate(entry.id) }}
      onOpenChange={onOpenChange}
    />
  )
}

export function ProgramDeleteDialog({ entry, onOpenChange }: { entry: ProgramEntry | null; onOpenChange: (open: boolean) => void }) {
  const queryClient = useQueryClient()
  const [error, setError] = useState<string | null>(null)
  useEffect(() => { if (entry) setError(null) }, [entry])
  const remove = useMutation({
    // One attempt per action: the person retries, nothing is re-sent on its own.
    retry: false,
    mutationFn: (id: string) => deleteProgram(id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: programsQueryKeys.list })
      onOpenChange(false)
    },
    onError: (failure) => {
      // 409 names the agents it is still on for (S-35).
      setError(programErrorMessage(failure, 'The program was not deleted. Try again.'))
      void queryClient.invalidateQueries({ queryKey: programsQueryKeys.list })
    },
  })
  if (!entry) return null
  return (
    <ConfirmShell
      open
      testId="program-delete-dialog"
      title={`Delete ${entry.display_name}?`}
      description={`This removes ${entry.display_name} and its saved token from Omnipus. A program that is switched on for an agent cannot be deleted.`}
      actionLabel="Delete"
      error={error}
      busy={remove.isPending}
      onConfirm={() => { setError(null); remove.mutate(entry.id) }}
      onOpenChange={onOpenChange}
    />
  )
}
