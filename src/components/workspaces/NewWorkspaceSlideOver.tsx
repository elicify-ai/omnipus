import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { X } from '@phosphor-icons/react'
import { createWorkspace, fetchAgents, isWorker, isApiError, workspacesQueryKeys } from '@/lib/api'
import { useUiStore } from '@/store/ui'

interface NewWorkspaceSlideOverProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

interface FormState {
  name: string
  description: string
  repository: string
  core_team: string[]
}

const INITIAL_FORM: FormState = { name: '', description: '', repository: '', core_team: [] }

export function NewWorkspaceSlideOver({ open, onOpenChange }: NewWorkspaceSlideOverProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [form, setForm] = useState<FormState>(INITIAL_FORM)
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<keyof FormState, string>>>({})

  const { data: agents = [], isLoading: agentsLoading, isError: agentsError } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 60_000,
  })

  const mutation = useMutation({
    mutationFn: () =>
      createWorkspace({
        name: form.name.trim(),
        description: form.description.trim() || undefined,
        repository: form.repository.trim() || undefined,
        core_team: form.core_team.length > 0 ? form.core_team : undefined,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.list() })
      addToast({ message: 'Workspace created', variant: 'success' })
      setForm(INITIAL_FORM)
      setFieldErrors({})
      onOpenChange(false)
    },
    onError: (err) => {
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Unexpected error'
      addToast({ message: `Failed to create workspace: ${msg}`, variant: 'error' })
    },
  })

  function validate(): boolean {
    const errors: Partial<Record<keyof FormState, string>> = {}
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
            New workspace
          </SheetTitle>
        </SheetHeader>

        <form onSubmit={handleSubmit} className="flex flex-col flex-1 gap-5 py-4">
          {/* Name */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-workspace-name" className="text-[var(--color-secondary)]">
              Name <span className="text-[var(--color-error)]">*</span>
            </Label>
            <Input
              id="new-workspace-name"
              value={form.name}
              onChange={(e) => setForm((s) => ({ ...s, name: e.target.value }))}
              placeholder="My workspace"
              maxLength={200}
              autoFocus
              aria-invalid={!!fieldErrors.name}
              aria-describedby={fieldErrors.name ? 'new-workspace-name-error' : undefined}
            />
            {fieldErrors.name && (
              <p id="new-workspace-name-error" className="text-xs text-[var(--color-error)]">
                {fieldErrors.name}
              </p>
            )}
          </div>

          {/* Description */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-workspace-desc" className="text-[var(--color-secondary)]">
              Description
            </Label>
            <Textarea
              id="new-workspace-desc"
              value={form.description}
              onChange={(e) => setForm((s) => ({ ...s, description: e.target.value }))}
              placeholder="Optional workspace description"
              rows={3}
              maxLength={2000}
              aria-invalid={!!fieldErrors.description}
              aria-describedby={fieldErrors.description ? 'new-workspace-desc-error' : undefined}
            />
            {fieldErrors.description && (
              <p id="new-workspace-desc-error" className="text-xs text-[var(--color-error)]">
                {fieldErrors.description}
              </p>
            )}
          </div>

          {/* Repository */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-workspace-repo" className="text-[var(--color-secondary)]">
              Repository
            </Label>
            <Input
              id="new-workspace-repo"
              value={form.repository}
              onChange={(e) => setForm((s) => ({ ...s, repository: e.target.value }))}
              placeholder="https://github.com/..."
              maxLength={500}
              aria-invalid={!!fieldErrors.repository}
              aria-describedby={fieldErrors.repository ? 'new-workspace-repo-error' : undefined}
            />
            {fieldErrors.repository && (
              <p id="new-workspace-repo-error" className="text-xs text-[var(--color-error)]">
                {fieldErrors.repository}
              </p>
            )}
          </div>

          {/* Core team — agent multi-select (US-10 AC #5) */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-workspace-core-team" className="text-[var(--color-secondary)]">
              Core team
            </Label>
            {/* Selected agent chips */}
            {form.core_team.length > 0 && (
              <div className="flex flex-wrap gap-1.5">
                {form.core_team.map((agentId) => {
                  const agent = agents.find((a) => a.id === agentId)
                  return (
                    <span
                      key={agentId}
                      className="flex items-center gap-1 rounded-full bg-[var(--color-surface-2)] border border-[var(--color-border)] px-2 py-0.5 text-xs text-[var(--color-secondary)]"
                    >
                      {agent?.name ?? agentId}
                      <button
                        type="button"
                        onClick={() => setForm((s) => ({ ...s, core_team: s.core_team.filter((id) => id !== agentId) }))}
                        aria-label={`Remove ${agent?.name ?? agentId} from core team`}
                        className="rounded-full text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors"
                      >
                        <X size={10} />
                      </button>
                    </span>
                  )
                })}
              </div>
            )}
            {agentsError ? (
              <>
                <p className="text-xs text-[var(--color-error)]">
                  Could not load agents — enter agent IDs manually
                </p>
                <Input
                  id="new-workspace-core-team"
                  value={form.core_team.join(', ')}
                  onChange={(e) =>
                    setForm((s) => ({
                      ...s,
                      core_team: e.target.value
                        .split(',')
                        .map((id) => id.trim())
                        .filter(Boolean),
                    }))
                  }
                  placeholder="agent-id-1, agent-id-2"
                  className="bg-[var(--color-surface-2)] border-[var(--color-border)] text-[var(--color-secondary)]"
                />
              </>
            ) : agentsLoading ? (
              <Select disabled value="">
                <SelectTrigger
                  id="new-workspace-core-team"
                  className="bg-[var(--color-surface-2)] border-[var(--color-border)] text-[var(--color-secondary)]"
                >
                  <SelectValue placeholder="Loading agents…" />
                </SelectTrigger>
                <SelectContent />
              </Select>
            ) : (
              <Select
                value=""
                onValueChange={(agentId) => {
                  if (!form.core_team.includes(agentId)) {
                    setForm((s) => ({ ...s, core_team: [...s.core_team, agentId] }))
                  }
                }}
              >
                <SelectTrigger
                  id="new-workspace-core-team"
                  className="bg-[var(--color-surface-2)] border-[var(--color-border)] text-[var(--color-secondary)]"
                >
                  <SelectValue placeholder="Add agent to core team" />
                </SelectTrigger>
                <SelectContent>
                  {agents
                    .filter((a) => a.type !== 'system') // legacy/system agents aren't team-addable (mirrors AddAgentPicker)
                    .filter((a) => !form.core_team.includes(a.id))
                    .map((agent) => (
                      <SelectItem key={agent.id} value={agent.id}>
                        {agent.name}
                        {isWorker(agent) ? ' · leaf' : ''}
                      </SelectItem>
                    ))}
                </SelectContent>
              </Select>
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
              {mutation.isPending ? 'Creating…' : 'Create workspace'}
            </Button>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  )
}
