import { useState, useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash, X, CaretDown } from '@phosphor-icons/react'
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
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Checkbox } from '@/components/ui/checkbox'
import { SmartSelect } from '@/components/ui/smart-select'
import { DateTimePicker } from '@/components/ui/date-time-picker'
import {
  createTask,
  updateTask,
  fetchAgents,
  isWorker,
  fetchMilestones,
  fetchTasks,
  tasksQueryKeys,
  workspacesQueryKeys,
  milestonesQueryKeys,
  isApiError,
} from '@/lib/api'
import type { Milestone, Task, TaskTrigger, TaskCreateRequest, Todo } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { cn } from '@/lib/utils'
import { PRIORITY_BADGE } from './TaskCard'
import {
  type TriggerKind,
  buildTrigger,
  datetimeLocalToMs,
  datetimeLocalToIso,
  datetimeLocalToDate,
  dateToDatetimeLocal,
} from './taskFormFields'

interface CreateTaskSlideOverProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Pre-fill the workspace selector */
  workspaceId: string
  /** Pre-fill the milestone selector (from active filter pill) */
  milestoneId?: string | null
  /** Pre-fill the due date field when the slide-over opens (datetime-local value, e.g. "2026-06-22T00:00") */
  initialDue?: string
}

interface FormState {
  title: string
  prompt: string
  priority: number
  milestoneId: string
  agentId: string
  // Trigger
  triggerKind: TriggerKind
  triggerAt: string // datetime-local value (once)
  triggerEveryMinutes: string // minutes (every)
  triggerCron: string // cron expr (recurring)
  // Dependencies
  blockedBy: string[]
  // Due
  due: string // datetime-local value
  // Todos
  todos: string[]
}

const INITIAL_FORM: FormState = {
  title: '',
  prompt: '',
  priority: 3,
  milestoneId: '__none__',
  agentId: '__none__',
  triggerKind: 'manual',
  triggerAt: '',
  triggerEveryMinutes: '60',
  triggerCron: '0 9 * * MON',
  blockedBy: [],
  due: '',
  todos: [],
}

export function CreateTaskSlideOver({
  open,
  onOpenChange,
  workspaceId,
  milestoneId,
  initialDue,
}: CreateTaskSlideOverProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)

  const [form, setForm] = useState<FormState>({
    ...INITIAL_FORM,
    milestoneId: milestoneId ?? '__none__',
  })
  const [titleError, setTitleError] = useState('')
  const [triggerError, setTriggerError] = useState('')
  const [newTodo, setNewTodo] = useState('')

  // Sync milestone and due pre-fill when the slide-over opens or the caller's props change
  useEffect(() => {
    if (open) {
      setForm((f) => ({
        ...f,
        milestoneId: milestoneId ?? '__none__',
        due: initialDue ?? f.due,
      }))
    }
  }, [open, milestoneId, initialDue])

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

  // Existing tasks in this workspace — candidate dependencies (depends-on / blocked_by).
  const { data: wsTasks = [] } = useQuery({
    queryKey: tasksQueryKeys.list({ workspace_id: workspaceId, surface: 'user' }),
    queryFn: () => fetchTasks({ workspace_id: workspaceId, surface: 'user' }),
    staleTime: 10_000,
    enabled: !!workspaceId && open,
  })

  // Only top-level tasks are eligible dependencies (subtasks nest under parents).
  const depCandidates: Task[] = wsTasks.filter((t) => !t.parent_task_id)

  function buildBody(): TaskCreateRequest {
    const body: TaskCreateRequest = {
      title: form.title.trim(),
      action: 'llm',
      prompt: form.prompt.trim() || undefined,
      priority: form.priority,
      workspace_id: workspaceId,
      surface: 'user',
      milestone_id: form.milestoneId === '__none__' ? undefined : form.milestoneId || undefined,
      agent_id: form.agentId === '__none__' ? undefined : form.agentId || undefined,
    }

    const trigger = currentTrigger()
    // Omit manual triggers — they are the implicit default; sending {type:'manual',config:{}} is harmless
    // but keeping the body lean avoids noise. We DO send non-manual triggers.
    if (trigger && trigger.type !== 'manual') {
      body.trigger = trigger
    }

    if (form.blockedBy.length > 0) {
      body.blocked_by = form.blockedBy
    }

    if (form.due) {
      const iso = datetimeLocalToIso(form.due)
      if (iso) body.due = iso
    }

    const todos = currentTodos()
    if (todos.length > 0) {
      body.todos = todos
    }

    return body
  }

  function currentTrigger(): TaskTrigger | null {
    if (form.triggerKind === 'once') {
      const at = datetimeLocalToMs(form.triggerAt)
      return buildTrigger('once', { at_ms: at ?? undefined })
    }
    if (form.triggerKind === 'every') {
      const minutes = parseInt(form.triggerEveryMinutes, 10)
      const everyMs = Number.isFinite(minutes) ? minutes * 60_000 : undefined
      return buildTrigger('every', { every_ms: everyMs })
    }
    if (form.triggerKind === 'recurring') {
      return buildTrigger('recurring', { cron_expr: form.triggerCron.trim() })
    }
    return buildTrigger('manual', {})
  }

  function currentTodos(): Todo[] {
    return form.todos
      .map((t) => t.trim())
      .filter((t) => t.length > 0)
      .map((text) => ({ text, status: 'pending' as const }))
  }

  /** Client-side validation of the chosen trigger; returns an error string or ''. */
  function validateTrigger(): string {
    if (form.triggerKind === 'once') {
      if (!form.triggerAt) return 'Pick a date and time for the one-time trigger.'
      const at = datetimeLocalToMs(form.triggerAt)
      if (at == null) return 'Invalid trigger date/time.'
    }
    if (form.triggerKind === 'every') {
      const minutes = parseInt(form.triggerEveryMinutes, 10)
      if (!Number.isFinite(minutes) || minutes < 1) return 'Interval must be at least 1 minute.'
    }
    if (form.triggerKind === 'recurring') {
      if (!form.triggerCron.trim()) return 'Enter a cron expression for the recurring trigger.'
    }
    return ''
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
    const tErr = validateTrigger()
    if (tErr) {
      setTriggerError(tErr)
      return
    }
    setTriggerError('')
    if (runNow) {
      createAndRunMutation.mutate()
    } else {
      createMutation.mutate()
    }
  }

  function resetAndClose() {
    setForm({ ...INITIAL_FORM, milestoneId: milestoneId ?? '__none__' })
    setTitleError('')
    setTriggerError('')
    setNewTodo('')
    onOpenChange(false)
  }

  function handleOpenChange(next: boolean) {
    if (!next) resetAndClose()
    else onOpenChange(next)
  }

  function toggleDep(id: string) {
    setForm((s) => ({
      ...s,
      blockedBy: s.blockedBy.includes(id)
        ? s.blockedBy.filter((x) => x !== id)
        : [...s.blockedBy, id],
    }))
  }

  function addTodo() {
    const text = newTodo.trim()
    if (!text) return
    setForm((s) => ({ ...s, todos: [...s.todos, text] }))
    setNewTodo('')
  }

  function removeTodo(idx: number) {
    setForm((s) => ({ ...s, todos: s.todos.filter((_, i) => i !== idx) }))
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
                <SelectItem value="1" className="text-xs text-[color:var(--color-error)]">P1 — Critical</SelectItem>
                <SelectItem value="2" className="text-xs text-[color:var(--color-warning)]">P2 — High</SelectItem>
                <SelectItem value="3" className="text-xs text-[color:var(--color-warning)]">P3 — Medium</SelectItem>
                <SelectItem value="4" className="text-xs text-[color:var(--color-accent)]">P4 — Low</SelectItem>
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
                // Subagent workers are valid assignees when they belong to the
                // workspace's team — the backend enforces team membership, not
                // worker-vs-main kind (see validateTaskAgentID). subagent_3p
                // (external-CLI) workers are still unconditionally rejected
                // server-side: task execution isn't wired through the
                // external-CLI dispatch path yet, so they are excluded here
                // too (offering them would be a guaranteed-400 dead end). A
                // " · Worker" suffix keeps the delegation-only kind visually
                // distinguishable (mirrors AddAgentPicker's " · leaf" convention).
                ...agents
                  .filter((a) => a.type !== 'subagent_3p')
                  .map((a) => ({
                    value: a.id,
                    label: isWorker(a) ? `${a.name} · Worker` : a.name,
                    className: 'text-xs',
                  })),
              ]}
            />
          </div>

          {/* Trigger */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ct-trigger" className="text-[var(--color-secondary)]">
              Trigger
            </Label>
            <Select
              value={form.triggerKind}
              onValueChange={(v) => { setForm((s) => ({ ...s, triggerKind: v as TriggerKind })); setTriggerError('') }}
            >
              <SelectTrigger id="ct-trigger" className="bg-[var(--color-surface-2)] border-[var(--color-border)] text-[var(--color-secondary)]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="manual" className="text-xs">None (manual)</SelectItem>
                <SelectItem value="once" className="text-xs">Once (at a time)</SelectItem>
                <SelectItem value="every" className="text-xs">Every (interval)</SelectItem>
                <SelectItem value="recurring" className="text-xs">Recurring (cron)</SelectItem>
              </SelectContent>
            </Select>

            {form.triggerKind === 'once' && (
              <DateTimePicker
                aria-label="Trigger date and time"
                value={datetimeLocalToDate(form.triggerAt)}
                onChange={(d) => {
                  setForm((s) => ({ ...s, triggerAt: dateToDatetimeLocal(d) }))
                  setTriggerError('')
                }}
                className="mt-1"
              />
            )}
            {form.triggerKind === 'every' && (
              <div className="mt-1 flex items-center gap-2">
                <Input
                  aria-label="Interval in minutes"
                  type="number"
                  min={1}
                  value={form.triggerEveryMinutes}
                  onChange={(e) => { setForm((s) => ({ ...s, triggerEveryMinutes: e.target.value })); setTriggerError('') }}
                  className="text-xs w-28"
                />
                <span className="text-xs text-[var(--color-muted)]">minutes</span>
              </div>
            )}
            {form.triggerKind === 'recurring' && (
              <Input
                aria-label="Cron expression"
                value={form.triggerCron}
                onChange={(e) => { setForm((s) => ({ ...s, triggerCron: e.target.value })); setTriggerError('') }}
                placeholder="0 9 * * MON"
                className="mt-1 text-xs font-mono"
              />
            )}
            {triggerError && (
              <p className="text-xs text-[var(--color-error)]">{triggerError}</p>
            )}
          </div>

          {/* Depends on (blocked_by) */}
          <div className="flex flex-col gap-1.5">
            <Label className="text-[var(--color-secondary)]">Depends on</Label>
            <Popover>
              <PopoverTrigger asChild>
                <Button
                  type="button"
                  variant="outline"
                  className="justify-between h-9 text-xs bg-[var(--color-surface-2)] border-[var(--color-border)] text-[var(--color-secondary)] font-normal"
                  disabled={depCandidates.length === 0}
                >
                  <span className="truncate">
                    {depCandidates.length === 0
                      ? 'No other tasks yet'
                      : form.blockedBy.length === 0
                        ? 'No dependencies'
                        : `${form.blockedBy.length} task${form.blockedBy.length === 1 ? '' : 's'} selected`}
                  </span>
                  <CaretDown size={12} className="shrink-0 opacity-70" />
                </Button>
              </PopoverTrigger>
              <PopoverContent className="w-[var(--radix-popover-trigger-width)] max-h-64 overflow-y-auto p-1" align="start">
                {depCandidates.map((t) => {
                  const checked = form.blockedBy.includes(t.id)
                  return (
                    <button
                      key={t.id}
                      type="button"
                      onClick={() => toggleDep(t.id)}
                      className="w-full flex items-center gap-2 px-2 py-1.5 rounded text-xs text-left hover:bg-[var(--color-surface-2)] transition-colors"
                    >
                      <Checkbox checked={checked} className="pointer-events-none" />
                      <span className="flex-1 truncate text-[var(--color-secondary)]">{t.title}</span>
                    </button>
                  )
                })}
              </PopoverContent>
            </Popover>
            {form.blockedBy.length > 0 && (
              <div className="flex flex-wrap gap-1.5 mt-1">
                {form.blockedBy.map((id) => {
                  const t = depCandidates.find((x) => x.id === id)
                  return (
                    <span
                      key={id}
                      className="inline-flex items-center gap-1 rounded-full bg-[var(--color-surface-2)] border border-[var(--color-border)] px-2 py-0.5 text-[10px] text-[var(--color-secondary)]"
                    >
                      <span className="max-w-[120px] truncate">{t?.title ?? id}</span>
                      <button
                        type="button"
                        onClick={() => toggleDep(id)}
                        aria-label={`Remove dependency ${t?.title ?? id}`}
                        className="text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
                      >
                        <X size={9} />
                      </button>
                    </span>
                  )
                })}
              </div>
            )}
          </div>

          {/* Due date */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ct-due" className="text-[var(--color-secondary)]">
              Due date
            </Label>
            <DateTimePicker
              id="ct-due"
              aria-label="Due date"
              value={datetimeLocalToDate(form.due)}
              onChange={(d) => setForm((s) => ({ ...s, due: dateToDatetimeLocal(d) }))}
            />
          </div>

          {/* Todos */}
          <div className="flex flex-col gap-1.5">
            <Label className="text-[var(--color-secondary)]">Checklist</Label>
            <div className="flex items-center gap-2">
              <Input
                aria-label="New checklist item"
                value={newTodo}
                onChange={(e) => setNewTodo(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    addTodo()
                  }
                }}
                placeholder="Add a checklist item…"
                maxLength={500}
                className="text-xs flex-1"
              />
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-9 px-2 shrink-0"
                onClick={addTodo}
                aria-label="Add checklist item"
                disabled={!newTodo.trim()}
              >
                <Plus size={13} />
              </Button>
            </div>
            {form.todos.length > 0 && (
              <ul className="space-y-1 mt-1">
                {form.todos.map((text, idx) => (
                  <li
                    key={idx}
                    className="flex items-center gap-2 px-2 py-1.5 rounded-md bg-[var(--color-surface-2)] text-xs"
                  >
                    <span className="flex-1 text-[var(--color-secondary)] truncate">{text}</span>
                    <button
                      type="button"
                      onClick={() => removeTodo(idx)}
                      aria-label={`Remove checklist item ${text}`}
                      className="shrink-0 text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors"
                    >
                      <Trash size={12} />
                    </button>
                  </li>
                ))}
              </ul>
            )}
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
