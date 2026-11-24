// ProgramPasteDialog — Settings → Programs → "Paste a token" / "Replace token"
// (spec cli-credentials S1: US-1, US-7 AS-1, FR-003, FR-043; UI table row
// "Paste-a-token dialog").
//
// The token lives only in this dialog's own input. It goes straight into the
// one save request and is cleared when the request answers, when the dialog
// closes and when it unmounts; it is never put in a store, a query cache or a
// log. The save carries the generation the person saw (`expected_generation`)
// so a token changed elsewhere since the page loaded is refused, not
// overwritten. A lost answer is never re-sent: the list is read again.
import { useEffect, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import type { ProgramEntry, ProgramList } from '@/lib/api/generated/openapi-types'
import { programsQueryKeys, saveProgramCredential } from '@/lib/api'
import {
  PROGRAM_SAVE_UNKNOWN,
  PROGRAM_SECRET_PROMISE,
  isLostAnswer,
  normalizeProgramToken,
  programErrorMessage,
  programTokenError,
} from '@/lib/programs'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

/** Puts one entry the gateway returned into the cached list. */
export function replaceProgramEntry(list: ProgramList | undefined, entry: ProgramEntry): ProgramList | undefined {
  if (!list) return list
  return { ...list, entries: list.entries.map((current) => (current.id === entry.id ? entry : current)) }
}

export function ProgramPasteDialog({
  entry,
  onOpenChange,
}: {
  /** The entry the token is for; null closes the dialog. */
  entry: ProgramEntry | null
  onOpenChange: (open: boolean) => void
}) {
  const queryClient = useQueryClient()
  const [value, setValue] = useState('')
  const [error, setError] = useState<string | null>(null)
  const inputRef = useRef<HTMLInputElement | null>(null)
  const open = entry !== null

  // Closing or unmounting wipes the value, including from the DOM node.
  useEffect(() => {
    if (!open) {
      setValue('')
      setError(null)
      if (inputRef.current) inputRef.current.value = ''
    }
  }, [open])
  useEffect(() => () => {
    if (inputRef.current) inputRef.current.value = ''
  }, [])

  const save = useMutation({
    // One attempt per action: the person retries, nothing is re-sent on its own.
    retry: false,
    mutationFn: (payload: { id: string; value: string; generation: number }) =>
      saveProgramCredential(payload.id, { value: payload.value, expected_generation: payload.generation }),
    onSuccess: (saved) => {
      queryClient.setQueryData<ProgramList>(programsQueryKeys.list, (list) => replaceProgramEntry(list, saved))
      void queryClient.invalidateQueries({ queryKey: programsQueryKeys.list })
      onOpenChange(false)
    },
    onError: (failure) => {
      setError(isLostAnswer(failure) ? PROGRAM_SAVE_UNKNOWN : programErrorMessage(failure, 'The token was not saved.'))
      // The list shows the gateway's current state after any failure.
      void queryClient.invalidateQueries({ queryKey: programsQueryKeys.list })
    },
    onSettled: () => {
      // The value leaves the input the moment the save answers, whatever the outcome.
      setValue('')
      if (inputRef.current) inputRef.current.value = ''
    },
  })

  if (!entry) return null
  const busy = save.isPending

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (busy) return
    const ruleError = programTokenError(value)
    if (ruleError) {
      setError(ruleError)
      inputRef.current?.focus()
      return
    }
    setError(null)
    save.mutate({ id: entry.id, value: normalizeProgramToken(value), generation: entry.connection.generation })
  }

  const replacing = entry.connection.state !== 'not_connected'
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!busy) onOpenChange(next) }}>
      <DialogContent data-testid="program-paste-dialog" aria-busy={busy || undefined}>
        <DialogHeader>
          <DialogTitle>{replacing ? `Replace the token for ${entry.display_name}` : `Paste a token for ${entry.display_name}`}</DialogTitle>
          <DialogDescription>{PROGRAM_SECRET_PROMISE}</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-[var(--space-3)]" noValidate>
          <Field label={`Token for ${entry.display_name}`} error={error ?? undefined} required>
            <Input
              ref={inputRef}
              type="password"
              autoComplete="new-password"
              spellCheck={false}
              autoFocus
              disabled={busy}
              value={value}
              data-testid="program-token-input"
              onChange={(event) => {
                setValue(event.target.value)
                if (error) setError(null)
              }}
            />
          </Field>
          {replacing && (
            <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
              The new token is used from the next command. Its account is shown after its check.
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" actionState={busy ? 'pending' : 'idle'}>
              Save
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
