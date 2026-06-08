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
import { createProject, projectsQueryKeys, isApiError } from '@/lib/api'
import { useUiStore } from '@/store/ui'

interface NewProjectSlideOverProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

interface FormState {
  name: string
  description: string
  repository: string
}

const INITIAL_FORM: FormState = { name: '', description: '', repository: '' }

export function NewProjectSlideOver({ open, onOpenChange }: NewProjectSlideOverProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [form, setForm] = useState<FormState>(INITIAL_FORM)
  const [fieldErrors, setFieldErrors] = useState<Partial<FormState>>({})

  const mutation = useMutation({
    mutationFn: () =>
      createProject({
        name: form.name.trim(),
        description: form.description.trim() || undefined,
        repository: form.repository.trim() || undefined,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: projectsQueryKeys.list() })
      addToast({ message: 'Project created', variant: 'success' })
      setForm(INITIAL_FORM)
      setFieldErrors({})
      onOpenChange(false)
    },
    onError: (err) => {
      const msg = isApiError(err) ? err.message : 'Unexpected error'
      addToast({ message: `Failed to create project: ${msg}`, variant: 'error' })
    },
  })

  function validate(): boolean {
    const errors: Partial<FormState> = {}
    if (!form.name.trim()) {
      errors.name = 'Name is required'
    } else if (form.name.trim().length > 200) {
      errors.name = 'Name must be 200 characters or fewer'
    }
    if (form.description.length > 2000) {
      errors.description = 'Description must be 2000 characters or fewer'
    }
    if (form.repository.length > 500) {
      errors.repository = 'Repository URL must be 500 characters or fewer'
    }
    setFieldErrors(errors)
    return Object.keys(errors).length === 0
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!validate()) return
    mutation.mutate()
  }

  function handleOpenChange(next: boolean) {
    if (!next) {
      setForm(INITIAL_FORM)
      setFieldErrors({})
    }
    onOpenChange(next)
  }

  return (
    <Sheet open={open} onOpenChange={handleOpenChange}>
      <SheetContent side="right" className="w-full sm:max-w-md flex flex-col">
        <SheetHeader>
          <SheetTitle className="font-headline text-[var(--color-secondary)]">
            New project
          </SheetTitle>
        </SheetHeader>

        <form onSubmit={handleSubmit} className="flex flex-col flex-1 gap-5 py-4">
          {/* Name */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-project-name" className="text-[var(--color-secondary)]">
              Name <span className="text-[var(--color-error)]">*</span>
            </Label>
            <Input
              id="new-project-name"
              value={form.name}
              onChange={(e) => setForm((s) => ({ ...s, name: e.target.value }))}
              placeholder="My project"
              maxLength={200}
              autoFocus
              aria-invalid={!!fieldErrors.name}
              aria-describedby={fieldErrors.name ? 'new-project-name-error' : undefined}
            />
            {fieldErrors.name && (
              <p id="new-project-name-error" className="text-xs text-[var(--color-error)]">
                {fieldErrors.name}
              </p>
            )}
          </div>

          {/* Description */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-project-desc" className="text-[var(--color-secondary)]">
              Description
            </Label>
            <Textarea
              id="new-project-desc"
              value={form.description}
              onChange={(e) => setForm((s) => ({ ...s, description: e.target.value }))}
              placeholder="Optional project description"
              rows={3}
              maxLength={2000}
              aria-invalid={!!fieldErrors.description}
              aria-describedby={fieldErrors.description ? 'new-project-desc-error' : undefined}
            />
            {fieldErrors.description && (
              <p id="new-project-desc-error" className="text-xs text-[var(--color-error)]">
                {fieldErrors.description}
              </p>
            )}
          </div>

          {/* Repository */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-project-repo" className="text-[var(--color-secondary)]">
              Repository
            </Label>
            <Input
              id="new-project-repo"
              value={form.repository}
              onChange={(e) => setForm((s) => ({ ...s, repository: e.target.value }))}
              placeholder="https://github.com/..."
              maxLength={500}
              aria-invalid={!!fieldErrors.repository}
              aria-describedby={fieldErrors.repository ? 'new-project-repo-error' : undefined}
            />
            {fieldErrors.repository && (
              <p id="new-project-repo-error" className="text-xs text-[var(--color-error)]">
                {fieldErrors.repository}
              </p>
            )}
          </div>

          <div className="flex-1" />

          <SheetFooter className="flex-row gap-2 pt-2">
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
              {mutation.isPending ? 'Creating…' : 'Create project'}
            </Button>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  )
}
