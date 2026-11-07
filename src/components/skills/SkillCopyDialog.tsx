// SkillCopyDialog — "Make a copy" of an installed skill (T9, skills spec r15
// §8.6 and §13). A built-in skill is read-only; copying it is how a person
// gets an editable version. User-owned skills can be copied too.
//
// The name is checked live against the slug rule, the installed names and
// the built-in names (all case-insensitive, §5.13). The server stays the
// authority: POST /skills/{name}/copy repeats every check under its lock, and
// its refusal text is shown here when the list was stale.

import { useEffect, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { copySkill, isApiError, type Skill } from '@/lib/api'
import { ConfigurationSaveError } from '@/lib/api/configuration'
import { useUiStore } from '@/store/ui'

// SkillCopyRequest.new_id rule (contracts C-3: namePattern, maxLength 64).
const SKILL_NAME_PATTERN = /^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$/
const SKILL_NAME_MAX_LENGTH = 64

export const COPY_NAME_INVALID = 'Use letters, digits and single hyphens, up to 64 characters'
export const COPY_NAME_TAKEN = 'This name is already used'
export const COPY_NAME_BUILTIN = "This is a built-in skill's name"

/** Returns the live validation text for a proposed copy name, or null when valid. */
export function copyNameProblem(name: string, skills: readonly Skill[]): string | null {
  if (name.length > SKILL_NAME_MAX_LENGTH || !SKILL_NAME_PATTERN.test(name)) return COPY_NAME_INVALID
  const key = name.toLowerCase()
  const match = skills.find((skill) => skill.id.toLowerCase() === key)
  if (!match) return null
  return match.source === 'builtin' ? COPY_NAME_BUILTIN : COPY_NAME_TAKEN
}

interface SkillCopyDialogProps {
  /** The skill being copied; null keeps the dialog closed. */
  source: Skill | null
  /** Every installed skill, for the live name check. */
  skills: readonly Skill[]
  onOpenChange: (open: boolean) => void
}

export function SkillCopyDialog({ source, skills, onOpenChange }: SkillCopyDialogProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [name, setName] = useState('')
  const [submitError, setSubmitError] = useState<string | null>(null)

  // Every open starts from the spec default, `<slug>-copy`, with no leftover error.
  useEffect(() => {
    if (!source) return
    setName(`${source.id}-copy`)
    setSubmitError(null)
  }, [source])

  const mutation = useMutation({
    mutationFn: ({ from, to }: { from: string; to: string }) => copySkill(from, to),
    onMutate: () => setSubmitError(null),
    onSuccess: (_copy, { to }) => {
      void queryClient.invalidateQueries({ queryKey: ['skills'] })
      addToast({
        message: `Created '${to}'. No agent is granted it yet; grant it to others in their settings.`,
        variant: 'success',
      })
      onOpenChange(false)
    },
    onError: (err: unknown) => {
      // A storage failure after publication leaves a copy on disk: refresh the
      // list so the person sees what exists, and say the copy is incomplete.
      if (err instanceof ConfigurationSaveError && err.state.persistence_status !== 'none') {
        void queryClient.invalidateQueries({ queryKey: ['skills'] })
        addToast({ message: `Copy incomplete: ${err.state.message ?? err.message}`, variant: 'error' })
        onOpenChange(false)
        return
      }
      setSubmitError(
        isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Could not create the copy.',
      )
    },
  })

  const problem = name.length === 0 ? null : copyNameProblem(name, skills)
  const canSubmit = source !== null && name.length > 0 && problem === null && !mutation.isPending

  function handleSubmit() {
    if (!canSubmit || !source) return
    mutation.mutate({ from: source.id, to: name })
  }

  return (
    <Dialog open={source !== null} onOpenChange={(open) => { if (!mutation.isPending) onOpenChange(open) }}>
      <DialogContent data-testid="skill-copy-dialog" className="max-w-md">
        <DialogHeader>
          <DialogTitle>Make a copy of {source?.name}</DialogTitle>
          <DialogDescription>
            Your copy will not update with Omnipus. Grant it to agents in their settings.
          </DialogDescription>
        </DialogHeader>
        <Field label="Name of your copy" error={problem ?? submitError ?? undefined}>
          <Input
            value={name}
            autoFocus
            maxLength={SKILL_NAME_MAX_LENGTH + 1}
            autoComplete="off"
            spellCheck={false}
            data-testid="skill-copy-name-input"
            onChange={(e) => {
              setName(e.target.value)
              setSubmitError(null)
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter') handleSubmit()
            }}
          />
        </Field>
        <DialogFooter>
          <Button variant="outline" disabled={mutation.isPending} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={!canSubmit} data-testid="skill-copy-confirm">
            {mutation.isPending ? 'Creating…' : 'Create copy'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
