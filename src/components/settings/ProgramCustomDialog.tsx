// ProgramCustomDialog — create or edit a custom program (spec cli-credentials
// S1: US-4, FR-019, Dataset D-2; UI table row "Custom program form").
//
// The gateway owns every rule of D-2 and answers 400 with the rule's own
// message or 409 naming the conflict; this form shows that answer as is and
// keeps what the person typed so it can be corrected.
import { useEffect, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import type { ProgramCustomRequest, ProgramEntry } from '@/lib/api/generated/openapi-types'
import { createProgram, programsQueryKeys, updateProgram } from '@/lib/api'
import { programErrorMessage, splitList } from '@/lib/programs'
import { isApiError } from '@/lib/api-error'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field } from '@/components/ui/field'
import { Callout, CalloutDescription } from '@/components/ui/callout'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

interface FormState { // not-wire-format: the form's typed text before it becomes a ProgramCustomRequest
  displayName: string
  words: string
  variables: string
  sites: string
  gitHost: string
  gitUsername: string
  checkWord: string
  checkArgs: string
  checkTimeout: string
}

const EMPTY: FormState = {
  displayName: '',
  words: '',
  variables: '',
  sites: '',
  gitHost: '',
  gitUsername: '',
  checkWord: '',
  checkArgs: '',
  checkTimeout: '',
}

function formFromEntry(entry: ProgramEntry): FormState {
  return {
    displayName: entry.display_name,
    words: entry.program_words.join(', '),
    variables: entry.token_variables.join(', '),
    sites: entry.network_sites.join(', '),
    gitHost: entry.git?.host ?? '',
    gitUsername: entry.git?.username ?? '',
    checkWord: entry.identity_check?.program_word ?? '',
    checkArgs: (entry.identity_check?.args ?? []).join('\n'),
    checkTimeout: entry.identity_check?.timeout_seconds !== undefined ? String(entry.identity_check.timeout_seconds) : '',
  }
}

/** The request body: optional parts are sent only when the person filled them in. */
function requestFromForm(form: FormState, revision: number | undefined): ProgramCustomRequest {
  const body: ProgramCustomRequest = {
    display_name: form.displayName.trim(),
    program_words: splitList(form.words),
    token_variables: splitList(form.variables),
    network_sites: splitList(form.sites),
  }
  if (form.gitHost.trim() !== '') {
    body.git = { host: form.gitHost.trim(), ...(form.gitUsername.trim() !== '' ? { username: form.gitUsername.trim() } : {}) }
  }
  if (form.checkWord.trim() !== '') {
    // Arguments are literals, one per line, never shell-parsed (ProgramIdentityCheck).
    const args = form.checkArgs.split('\n').filter((line) => line.length > 0)
    const timeout = Number.parseInt(form.checkTimeout.trim(), 10)
    body.identity_check = {
      program_word: form.checkWord.trim(),
      args,
      ...(form.checkTimeout.trim() !== '' && Number.isFinite(timeout) ? { timeout_seconds: timeout } : {}),
    }
  }
  if (revision !== undefined) body.revision = revision
  return body
}

export function ProgramCustomDialog({
  open,
  entry,
  onOpenChange,
}: {
  open: boolean
  /** The custom entry being edited; null creates a new one. */
  entry: ProgramEntry | null
  onOpenChange: (open: boolean) => void
}) {
  const queryClient = useQueryClient()
  const [form, setForm] = useState<FormState>(EMPTY)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (open) {
      setForm(entry ? formFromEntry(entry) : EMPTY)
      setError(null)
    }
  }, [open, entry])

  const save = useMutation({
    // One attempt per action: the person retries, nothing is re-sent on its own.
    retry: false,
    mutationFn: (body: ProgramCustomRequest) => (entry ? updateProgram(entry.id, body) : createProgram(body)),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: programsQueryKeys.list })
      onOpenChange(false)
    },
    onError: (failure) => {
      setError(programErrorMessage(failure, 'The program was not saved.'))
      // A stale revision means the list moved on: show the current one.
      if (isApiError(failure) && failure.status === 409) void queryClient.invalidateQueries({ queryKey: programsQueryKeys.list })
    },
  })
  const busy = save.isPending

  const set = (key: keyof FormState) => (event: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => {
    const next = event.target.value
    setForm((prev) => ({ ...prev, [key]: next }))
  }

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (busy) return
    setError(null)
    save.mutate(requestFromForm(form, entry?.revision))
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!busy) onOpenChange(next) }}>
      <DialogContent data-testid="program-custom-dialog" aria-busy={busy || undefined}>
        <DialogHeader>
          <DialogTitle>{entry ? `Edit ${entry.display_name}` : 'Add a custom program'}</DialogTitle>
          <DialogDescription>
            Agents switched on for this program get its token in the variables you name, and its commands may reach its network sites.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-[var(--space-3)]" noValidate>
          <Field label="Display name" required>
            <Input value={form.displayName} disabled={busy} maxLength={64} onChange={set('displayName')} data-testid="program-display-name" />
          </Field>
          <Field label="Program words" description="The names the program is run by, separated by commas — for example: acme" required>
            <Input value={form.words} disabled={busy} onChange={set('words')} data-testid="program-words" />
          </Field>
          <Field label="Token variables" description="The environment variables the token is put in, separated by commas — for example: ACME_TOKEN" required>
            <Input value={form.variables} disabled={busy} onChange={set('variables')} data-testid="program-variables" />
          </Field>
          <Field label="Network sites" description="Host names the program reaches, separated by commas. *.example.com covers its subdomains." optionalLabel="(optional)">
            <Input value={form.sites} disabled={busy} onChange={set('sites')} data-testid="program-sites" />
          </Field>
          <Field label="Git site" description="A host name such as gitlab.com. git then uses this token for that site." optionalLabel="(optional)">
            <Input value={form.gitHost} disabled={busy} onChange={set('gitHost')} data-testid="program-git-host" />
          </Field>
          {form.gitHost.trim() !== '' && (
            <Field label="Git user name" description="Leave empty for x-access-token." optionalLabel="(optional)">
              <Input value={form.gitUsername} disabled={busy} onChange={set('gitUsername')} data-testid="program-git-username" />
            </Field>
          )}
          <Field label="Account check program" description="One of the program words. Without it, a saved token is shown as saved, not checked." optionalLabel="(optional)">
            <Input value={form.checkWord} disabled={busy} onChange={set('checkWord')} data-testid="program-check-word" />
          </Field>
          {form.checkWord.trim() !== '' && (
            <>
              <Field label="Account check arguments" description="One argument per line, used exactly as written. The first line it prints is the account name." optionalLabel="(optional)">
                <Textarea value={form.checkArgs} disabled={busy} rows={3} onChange={set('checkArgs')} data-testid="program-check-args" />
              </Field>
              <Field label="Account check time limit in seconds" description="From 1 to 30. Leave empty for 20." optionalLabel="(optional)">
                <Input value={form.checkTimeout} disabled={busy} inputMode="numeric" onChange={set('checkTimeout')} data-testid="program-check-timeout" />
              </Field>
            </>
          )}
          {error && (
          <Callout variant="error" data-testid="program-dialog-error">
            <CalloutDescription>{error}</CalloutDescription>
          </Callout>
        )}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" actionState={busy ? 'pending' : 'idle'}>
              {entry ? 'Save' : 'Create'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
