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
  buildTaskAssigneeItems,
  fetchTasks,
  tasksQueryKeys,
  workspacesQueryKeys,
  isApiError,
} from '@/lib/api'
import type { Task, TaskTrigger, TaskCreateRequest, Todo, AcceptanceCriterion } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useAuthStore } from '@/store/auth'
import { useWorkspaceTeamIds } from '@/hooks/useWorkspaceTeamIds'
import { cn } from '@/lib/utils'
import { PRIORITY_BADGE } from './TaskCard'
import { TagInput } from './TagInput'
import { AcceptanceCriteriaEditor } from './AcceptanceCriteriaEditor'
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
  /** Pre-fill the plan grouping (ADR-049 — from the active Board plan filter) */
  planId?: string | null
  /** Pre-fill the due date field when the slide-over opens (datetime-local value, e.g. "2026-06-22T00:00") */
  initialDue?: string
}

interface FormState {
  title: string
  prompt: string
  priority: number
  agentId: string
  // Trigger — FR-011/D3: the generic create form offers only manual/once;
  // recurring triggers (every/recurring) are calendar-only and built
  // exclusively via the calendar's event slide-over.
  triggerKind: TriggerKind
  triggerAt: string // datetime-local value (once)
  // Dependencies
  blockedBy: string[]
  // Due
  due: string // datetime-local value
  // Todos
  todos: string[]
  // Tags (ADR-049 — replaces milestone grouping)
  tags: string[]
  // Acceptance criteria (ADR-049 — Definition of Done)
  criteria: AcceptanceCriterion[]
}

const INITIAL_FORM: FormState = {
  title: '',
  prompt: '',
  priority: 3,
  agentId: '__none__',
  triggerKind: 'manual',
  triggerAt: '',
  blockedBy: [],
  due: '',
  todos: [],
  tags: [],
  criteria: [],
}

export function CreateTaskSlideOver({
  open,
  onOpenChange,
  workspaceId,
  planId,
  initialDue,
}: CreateTaskSlideOverProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const username = useAuthStore((s) => s.username)

  const [form, setForm] = useState<FormState>(INITIAL_FORM)
  const [titleError, setTitleError] = useState('')
  const [triggerError, setTriggerError] = useState('')
  const [newTodo, setNewTodo] = useState('')

  // Sync due pre-fill when the slide-over opens or the caller's prop changes
  useEffect(() => {
    if (open) {
      setForm((f) => ({
        ...f,
        due: initialDue ?? f.due,
      }))
    }
  }, [open, initialDue])

  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 60_000,
  })

  // Fix B: the assignee picker is scoped to this workspace's TEAM (core_team ∪
  // delegation edges), mirroring the backend's validateTaskAgentID gate —
  // see buildTaskAssigneeItems / useWorkspaceTeamIds for the fallback rules.
  // F1: `teamError` surfaces a failed team-set fetch as an inline hint next
  // to the picker (the hook itself logs the failure — see useWorkspaceTeamIds).
  const { teamIds, isLoading: teamLoading, isError: teamError } = useWorkspaceTeamIds(workspaceId)

  // Existing tasks in this workspace — candidate dependencies (depends-on / blocked_by).
  const { data: wsTasks = [] } = useQuery({
    queryKey: tasksQueryKeys.list({ workspace_id: workspaceId, surface: 'user' }),
    queryFn: () => fetchTasks({ workspace_id: workspaceId, surface: 'user' }),
    staleTime: 10_000,
    enabled: !!workspaceId && open,
  })

  // Eligible dependencies are top-level tasks (subtasks nest under parents)
  // that belong to the SAME plan as the task being created — a `blocked_by`
  // edge must stay inside one plan's DAG (cross-plan deps aren't meaningful;
  // the plan engine + graph treat each plan as a self-contained DAG). `planId`
  // is the plan this new task will join; `null`/absent = the plan-less "Loose"
  // group, whose members may still depend on one another.
  const depCandidates: Task[] = wsTasks.filter(
    (t) => !t.parent_task_id && (t.plan_id || null) === (planId || null),
  )

  function buildBody(): TaskCreateRequest {
    const body: TaskCreateRequest = {
      title: form.title.trim(),
      action: 'llm',
      prompt: form.prompt.trim() || undefined,
      priority: form.priority,
      workspace_id: workspaceId,
      surface: 'user',
      plan_id: planId ?? undefined,
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

    if (form.tags.length > 0) {
      body.tags = form.tags
    }

    if (form.criteria.length > 0) {
      body.criteria = form.criteria
    }

    return body
  }

  function currentTrigger(): TaskTrigger | null {
    if (form.triggerKind === 'once') {
      const at = datetimeLocalToMs(form.triggerAt)
      return buildTrigger('once', { at_ms: at ?? undefined })
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
    setForm(INITIAL_FORM)
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
      <SheetContent side="right" className="w-full sm:max-w-md flex flex-col p-0">
        <SheetHeader className="px-6 pr-14">
          <SheetTitle>
            New task
          </SheetTitle>
        </SheetHeader>

        <div className="flex flex-col flex-1 gap-5 px-6 py-4 overflow-y-auto">
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

          {/* Tags (ADR-049 — replaces the milestone selector) */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ct-tags" className="text-[var(--color-secondary)]">
              Tags
            </Label>
            <TagInput
              id="ct-tags"
              ariaLabel="Add tag"
              tags={form.tags}
              onChange={(tags) => setForm((s) => ({ ...s, tags }))}
            />
          </div>

          {/* Acceptance criteria (ADR-049 — Definition of Done, SD-C13) */}
          <div className="flex flex-col gap-1.5">
            <Label className="text-[var(--color-secondary)]">Acceptance criteria</Label>
            <AcceptanceCriteriaEditor
              criteria={form.criteria}
              onChange={(criteria) => setForm((s) => ({ ...s, criteria }))}
              currentAuthor={{ kind: 'user', id: username ?? 'operator' }}
              emptyHint="No criteria added — this task will be judged against its title and description (D5)."
            />
          </div>

          {/* Agent */}
          <div className="flex flex-col gap-1.5">
            <Label className="text-[var(--color-secondary)]">Agent</Label>
            <SmartSelect
              value={form.agentId}
              onValueChange={(v) => setForm((s) => ({ ...s, agentId: v }))}
              placeholder={teamLoading ? 'Loading team…' : 'Unassigned'}
              disabled={teamLoading}
              triggerClassName="h-9 text-sm"
              ariaLabel="Agent"
              items={[
                { value: '__none__', label: 'Unassigned', className: 'text-xs' },
                // While the team-set query is in flight, don't feed the full
                // unscoped global roster into the picker — SmartSelect swaps
                // its underlying implementation (and thus its accessible
                // role: implicit "button" vs. explicit "combobox") based on
                // item COUNT (SEARCHABLE_THRESHOLD=5 in smart-select.tsx). A
                // real install's global agent roster is commonly >5 while a
                // workspace's own team is commonly <=5, so feeding the
                // unscoped roster in here made the control's accessible
                // identity flip the instant the query resolved — breaking
                // role-based automation/assistive-tech interaction with a
                // control that was ALREADY disabled and offering nothing
                // selectable. Scoping to an empty team set keeps the item
                // count — and therefore the rendered branch — stable across
                // the loading→resolved transition for the common (<=5-member
                // team) case.
                ...(teamLoading
                  ? []
                  : buildTaskAssigneeItems(agents, {
                      teamScope: teamIds ? { kind: 'scoped', ids: teamIds } : { kind: 'unscoped' },
                    })),
              ]}
            />
            {/* F1: a failed team-set fetch degrades to the unscoped roster —
                surface that degrade instead of leaving it indistinguishable
                from a healthy, unrestricted workspace. */}
            {teamError && (
              <p className="text-xs text-[var(--color-muted)]">
                Team list unavailable — showing all agents
              </p>
            )}
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
                    <button tabIndex={0}
                      key={t.id}
                      type="button"
                      onClick={() => toggleDep(t.id)}
                      aria-pressed={checked}
                      className="w-full flex items-center gap-2 px-2 py-1.5 rounded text-xs text-left hover:bg-[var(--color-surface-2)] transition-colors"
                    >
                      {/* The row button carries the checked state via
                          aria-pressed — this Checkbox is a decorative visual
                          echo, not a second control (button-in-button was a
                          nested-interactive violation with a duplicate,
                          no-op tab stop). */}
                      <Checkbox
                        checked={checked}
                        tabIndex={-1}
                        aria-hidden="true"
                        className="pointer-events-none"
                      />
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
                      <button tabIndex={0}
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
                    <button tabIndex={0}
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

        <SheetFooter className="flex-row gap-2 px-6 py-4 flex-shrink-0">
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
