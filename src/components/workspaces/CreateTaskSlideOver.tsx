import { useState, useEffect } from 'react'
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
import { SmartSelect } from '@/components/ui/smart-select'
import {
  createTask,
  updateTask,
  fetchAgents,
  isWorker,
  fetchMilestones,
  tasksQueryKeys,
  workspacesQueryKeys,
  milestonesQueryKeys,
  isApiError,
} from '@/lib/api'
import type { Milestone } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { cn } from '@/lib/utils'
import { PRIORITY_BADGE } from './TaskCard'

interface CreateTaskSlideOverProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Pre-fill the workspace selector */
  workspaceId: string
  /** Pre-fill the milestone selector (from active filter pill) */
  milestoneId?: string | null
}

interface FormState {
  title: string
  prompt: string
  priority: number
  milestoneId: string
  agentId: string
}

const INITIAL_FORM: FormState = {
  title: '',
  prompt: '',
  priority: 3,
  milestoneId: '__none__',
  agentId: '__none__',
}

export function CreateTaskSlideOver({
  open,
  onOpenChange,
  workspaceId,
  milestoneId,
}: CreateTaskSlideOverProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)

  const [form, setForm] = useState<FormState>({
    ...INITIAL_FORM,
    milestoneId: milestoneId ?? '__none__',
  })
  const [titleError, setTitleError] = useState('')

  // Sync milestone pre-fill when active filter changes
  useEffect(() => {
    if (open) {
      setForm((f) => ({ ...f, milestoneId: milestoneId ?? '__none__' }))
    }
  }, [open, milestoneId])

  const { data: milestones = [] } = useQuery({
    queryKey: milestonesQueryKeys.list(workspaceId),
    queryFn: () => fetchMilestones(workspaceId),
    staleTime: 30_000,
    enabled: !!workspaceId,
  })

  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 60_000,
  })

  function buildBody() {
    return {
      title: form.title.trim(),
      action: 'llm' as const,
      prompt: form.prompt.trim() || undefined,
      priority: form.priority,
      workspace_id: workspaceId,
      surface: 'user' as const,
      milestone_id: form.milestoneId === '__none__' ? undefined : form.milestoneId || undefined,
      agent_id: form.agentId === '__none__' ? undefined : form.agentId || undefined,
    }
  }

  // Create only — lands in inbox
  const createMutation = useMutation({
    mutationFn: () => createTask(buildBody()),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
      queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.list() })
      addToast({ message: 'Task created', variant: 'success' })
      resetAndClose()
    },
    onError: (err) => {
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to create task'
      addToast({ message: msg, variant: 'error' })
    },
  })

  // Create & Run now — create then PATCH to in_progress
  const createAndRunMutation = useMutation({
    mutationFn: async () => {
      const task = await createTask(buildBody())
      return updateTask(task.id, { status: 'in_progress' })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
      queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.list() })
      addToast({ message: 'Task created and started', variant: 'success' })
      resetAndClose()
    },
    onError: (err) => {
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to create task'
      addToast({ message: msg, variant: 'error' })
    },
  })

  function handleSubmit(runNow: boolean) {
    if (!form.title.trim()) {
      setTitleError('Title is required')
      return
    }
    setTitleError('')
    if (runNow) {
      createAndRunMutation.mutate()
    } else {
      createMutation.mutate()
    }
  }

  function resetAndClose() {
    setForm({ ...INITIAL_FORM, milestoneId: milestoneId ?? '__none__' })
    setTitleError('')
    onOpenChange(false)
  }

  function handleOpenChange(next: boolean) {
    if (!next) resetAndClose()
    else onOpenChange(next)
  }

  const isPending = createMutation.isPending || createAndRunMutation.isPending
  const priorityBadge = PRIORITY_BADGE[form.priority] ?? PRIORITY_BADGE[3]

  return (
    <Sheet open={open} onOpenChange={handleOpenChange}>
      <SheetContent side="right" className="w-full sm:max-w-md flex flex-col">
        <SheetHeader>
          <SheetTitle className="font-headline text-[var(--color-secondary)]">
            New task
          </SheetTitle>
        </SheetHeader>

        <div className="flex flex-col flex-1 gap-5 py-4 overflow-y-auto">
          {/* Title */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ct-title" className="text-[var(--color-secondary)]">
              Title <span className="text-[var(--color-error)]">*</span>
            </Label>
            <Input
              id="ct-title"
              value={form.title}
              onChange={(e) => { setForm((s) => ({ ...s, title: e.target.value })); setTitleError('') }}
              placeholder="Task title"
              autoFocus
              maxLength={200}
              aria-invalid={!!titleError}
              aria-describedby={titleError ? 'ct-title-error' : undefined}
            />
            {titleError && (
              <p id="ct-title-error" className="text-xs text-[var(--color-error)]">{titleError}</p>
            )}
          </div>

          {/* Prompt */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ct-prompt" className="text-[var(--color-secondary)]">
              Prompt
            </Label>
            <Textarea
              id="ct-prompt"
              value={form.prompt}
              onChange={(e) => setForm((s) => ({ ...s, prompt: e.target.value }))}
              placeholder="Describe what the agent should do…"
              rows={4}
              maxLength={10000}
              className="text-xs font-mono resize-none"
            />
          </div>

          {/* Priority */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ct-priority" className="text-[var(--color-secondary)]">
              Priority
              <span className={cn('ml-2 rounded border px-1.5 py-0.5 text-[10px] font-bold', priorityBadge.className)}>
                {priorityBadge.label}
              </span>
            </Label>
            <Select
              value={String(form.priority)}
              onValueChange={(v) => setForm((s) => ({ ...s, priority: parseInt(v, 10) }))}
            >
              <SelectTrigger id="ct-priority" className="bg-[var(--color-surface-2)] border-[var(--color-border)] text-[var(--color-secondary)]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="1" className="text-xs text-red-400">P1 — Critical</SelectItem>
                <SelectItem value="2" className="text-xs text-orange-400">P2 — High</SelectItem>
                <SelectItem value="3" className="text-xs text-yellow-400">P3 — Medium</SelectItem>
                <SelectItem value="4" className="text-xs text-blue-400">P4 — Low</SelectItem>
                <SelectItem value="5" className="text-xs text-[var(--color-muted)]">P5 — Minimal</SelectItem>
              </SelectContent>
            </Select>
          </div>

          {/* Milestone */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ct-milestone" className="text-[var(--color-secondary)]">
              Milestone
            </Label>
            {milestones.length === 0 ? (
              <p className="text-xs text-[var(--color-muted)]">
                No milestones — create one first
              </p>
            ) : (
              <Select
                value={form.milestoneId}
                onValueChange={(v) => setForm((s) => ({ ...s, milestoneId: v }))}
              >
                <SelectTrigger id="ct-milestone" className="bg-[var(--color-surface-2)] border-[var(--color-border)] text-[var(--color-secondary)]">
                  <SelectValue placeholder="No milestone" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__none__">No milestone</SelectItem>
                  {milestones.map((m: Milestone) => (
                    <SelectItem key={m.id} value={m.id} className="text-xs">
                      {m.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </div>

          {/* Agent */}
          <div className="flex flex-col gap-1.5">
            <Label className="text-[var(--color-secondary)]">Agent</Label>
            <SmartSelect
              value={form.agentId}
              onValueChange={(v) => setForm((s) => ({ ...s, agentId: v }))}
              placeholder="Unassigned"
              triggerClassName="h-9 text-sm"
              items={[
                { value: '__none__', label: 'Unassigned', className: 'text-xs' },
                ...agents
                  .filter((a) => !isWorker(a))
                  .map((a) => ({ value: a.id, label: a.name, className: 'text-xs' })),
              ]}
            />
          </div>
        </div>

        <SheetFooter className="flex-row gap-2 pt-2 flex-shrink-0">
          <Button
            type="button"
            variant="ghost"
            onClick={() => handleOpenChange(false)}
            disabled={isPending}
            className="flex-1"
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="outline"
            onClick={() => handleSubmit(false)}
            disabled={isPending}
            className="flex-1"
          >
            {isPending ? 'Creating…' : 'Create'}
          </Button>
          <Button
            type="button"
            onClick={() => handleSubmit(true)}
            disabled={isPending}
            className="flex-1 bg-[var(--color-accent)] text-[var(--color-primary)] hover:bg-[var(--color-accent)]/90"
          >
            {isPending ? 'Creating…' : 'Create & Run'}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
