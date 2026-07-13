import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetFooter,
} from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { DatePicker } from '@/components/ui/date-picker'
import { createMilestone, milestonesQueryKeys, isApiError } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { parseLocalDate, formatLocalDate } from '@/lib/calendar/eventMapping'

interface CreateMilestoneSlideOverProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  workspaceId: string
}

interface FormState {
  name: string
  description: string
  due_date: string
}

const INITIAL_FORM: FormState = { name: '', description: '', due_date: '' }

export function CreateMilestoneSlideOver({
  open,
  onOpenChange,
  workspaceId,
}: CreateMilestoneSlideOverProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [form, setForm] = useState<FormState>(INITIAL_FORM)
  const [nameError, setNameError] = useState('')

  const mutation = useMutation({
    mutationFn: () =>
      createMilestone(workspaceId, {
        name: form.name.trim(),
        description: form.description.trim() || undefined,
        due_date: form.due_date || undefined,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: milestonesQueryKeys.list(workspaceId) })
      addToast({ message: 'Milestone created', variant: 'success' })
      resetAndClose()
    },
    onError: (err) => {
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to create milestone'
      addToast({ message: msg, variant: 'error' })
    },
  })

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!form.name.trim()) {
      setNameError('Name is required')
      return
    }
    setNameError('')
    mutation.mutate()
  }

  function resetAndClose() {
    setForm(INITIAL_FORM)
    setNameError('')
    onOpenChange(false)
  }

  function handleOpenChange(next: boolean) {
    if (!next) resetAndClose()
    else onOpenChange(next)
  }

  return (
    <Sheet open={open} onOpenChange={handleOpenChange}>
      <SheetContent side="right" className="w-full sm:max-w-md flex flex-col p-0">
        <SheetHeader className="px-6 pr-14">
          <SheetTitle>
            New milestone
          </SheetTitle>
        </SheetHeader>

        <form onSubmit={handleSubmit} className="flex flex-col flex-1 gap-5 px-6 py-4">
          {/* Name */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cm-name" className="text-[var(--color-secondary)]">
              Name <span className="text-[var(--color-error)]">*</span>
            </Label>
            <Input
              id="cm-name"
              value={form.name}
              onChange={(e) => { setForm((s) => ({ ...s, name: e.target.value })); setNameError('') }}
              placeholder="v1.0 Launch"
              autoFocus
              maxLength={200}
              aria-invalid={!!nameError}
              aria-describedby={nameError ? 'cm-name-error' : undefined}
            />
            {nameError && (
              <p id="cm-name-error" className="text-xs text-[var(--color-error)]">{nameError}</p>
            )}
          </div>

          {/* Due date */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cm-due" className="text-[var(--color-secondary)]">
              Due date
            </Label>
            <DatePicker
              id="cm-due"
              value={parseLocalDate(form.due_date)}
              onChange={(d) => setForm((s) => ({ ...s, due_date: d ? formatLocalDate(d) : '' }))}
            />
          </div>

          {/* Description */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cm-desc" className="text-[var(--color-secondary)]">
              Description
            </Label>
            <Textarea
              id="cm-desc"
              value={form.description}
              onChange={(e) => setForm((s) => ({ ...s, description: e.target.value }))}
              placeholder="Optional milestone description"
              rows={3}
              maxLength={2000}
            />
          </div>

          <div className="flex-1" />

          <SheetFooter className="flex-row gap-2 px-0 py-2">
            <Button
              type="button"
              variant="ghost"
              onClick={() => handleOpenChange(false)}
              disabled={mutation.isPending}
              className="flex-1"
            >
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={mutation.isPending}
              className="flex-1 bg-[var(--color-accent)] text-[var(--color-primary)] hover:bg-[var(--color-accent)]/90"
            >
              {mutation.isPending ? 'Creating…' : 'Create milestone'}
            </Button>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  )
}
