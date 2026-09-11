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
  fetchPlans,
  tasksQueryKeys,
  workspacesQueryKeys,
  plansQueryKeys,
  isApiError,
} from '@/lib/api'
import type { Task, TaskCreateRequest, Todo, AcceptanceCriterion } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useAuthStore } from '@/store/auth'
import { useWorkspaceTeamIds } from '@/hooks/useWorkspaceTeamIds'
import { cn } from '@/lib/utils'
import { PRIORITY_BADGE } from './TaskCard'
import { TagInput } from './TagInput'
import { AcceptanceCriteriaEditor } from './AcceptanceCriteriaEditor'
import { DefinitionOfDoneEditor } from './DefinitionOfDoneEditor'
import { datetimeLocalToIso, datetimeLocalToDate, dateToDatetimeLocal } from './taskFormFields'

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
  // Plan (GOAL-FR-059) — defaults to the inherited `planId` prop (the
  // board's active plan filter) but is now an explicit, changeable picker
  // rather than a silent inherit. '__none__' = no plan.
  planId: string
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
  // Definition of Done (GOAL-FR-003/FR-048) — DISTINCT from `criteria`:
  // standing quality gates judged on every attempt, never mixed in.
  dod: AcceptanceCriterion[]
}

const INITIAL_FORM: FormState = {
  title: '',
  prompt: '',
  priority: 3,
  agentId: '__none__',
  planId: '__none__',
  blockedBy: [],
  due: '',
  todos: [],
  tags: [],
  criteria: [],
  dod: [],
}

/**
 * Keep only the `blocked_by` selections that are still legal under `nextPlanId`.
 *
 * A `blocked_by` edge has to stay inside one plan's DAG (the plan engine and
 * the graph both treat a plan as a self-contained DAG), so the dependency
 * picker only ever lists top-level tasks belonging to the SELECTED plan. When
 * the plan changes, any previously-selected dependency from the old plan
 * vanishes from both the picker and the chip row — the operator can no longer
 * see it or remove it — yet it would still ride along in the POST body and
 * create a cross-plan edge. Re-validating here is what makes the visible
 * selection and the submitted selection the same thing again.
 *
 * Exported for its own test: this is the whole fix, and asserting it through
 * the form alone would let a caller that forgets to invoke it still pass.
 */
export function reconcileBlockedByForPlan(
  blockedBy: string[],
  nextPlanId: string | null,
  tasks: Pick<Task, 'id' | 'parent_task_id' | 'plan_id'>[],
): string[] {
  if (blockedBy.length === 0) return blockedBy
  return blockedBy.filter((id) =>
    tasks.some((t) => t.id === id && !t.parent_task_id && (t.plan_id || null) === nextPlanId),
  )
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
  const [goalError, setGoalError] = useState('')
  const [criteriaError, setCriteriaError] = useState('')
  const [dodError, setDodError] = useState('')
  const [newTodo, setNewTodo] = useState('')

  // Sync due date + inherited plan pre-fill when the slide-over opens or the
  // caller's props change (GOAL-FR-059 — the plan picker defaults to the
  // board's active plan filter, same lifecycle as the due-date pre-fill).
  useEffect(() => {
    if (open) {
      setForm((f) => {
        const nextPlan = planId ? planId : f.planId
        const nextDue = initialDue ?? f.due
        if (nextPlan === f.planId && nextDue === f.due) return f
        // This effect is the OTHER way the selected plan changes (the board's
        // active plan filter moving while the slide-over is open), and it
        // strands a dependency selection exactly the way the picker did. Drop
        // the selection outright here rather than re-validating: a task
        // belongs to exactly one plan, so no dependency chosen under the old
        // plan can be legal under the new one, and the task list this form
        // would validate against is not a dependency of this effect.
        return {
          ...f,
          due: nextDue,
          planId: nextPlan,
          blockedBy: nextPlan === f.planId ? f.blockedBy : [],
        }
      })
    }
  }, [open, initialDue, planId])

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

  // Plans in this workspace (GOAL-FR-059) — the create form now offers the
  // same Plan picker the detail panel has, instead of silently inheriting
  // the board's active plan filter with no way to change or clear it here.
  const { data: plans = [] } = useQuery({
    queryKey: plansQueryKeys.list(workspaceId),
    queryFn: () => fetchPlans(workspaceId),
    staleTime: 10_000,
    enabled: !!workspaceId && open,
  })

  // Eligible dependencies are top-level tasks (subtasks nest under parents)
  // that belong to the SAME plan as the task being created — a `blocked_by`
  // edge must stay inside one plan's DAG (cross-plan deps aren't meaningful;
  // the plan engine + graph treat each plan as a self-contained DAG).
  // `form.planId` is the plan this new task will join; '__none__' = the
  // plan-less "Loose" group, whose members may still depend on one another.
  const effectivePlanId = form.planId === '__none__' ? null : form.planId
  const depCandidates: Task[] = wsTasks.filter(
    (t) => !t.parent_task_id && (t.plan_id || null) === effectivePlanId,
  )

  function buildBody(): TaskCreateRequest {
    // GOAL-FR-060: a normal task has no timer — it starts by a human
    // pressing Start, an agent starting it, or a plan reaching it. The
    // create form no longer offers a Trigger control, so the body never
    // carries one; a task lands here manual by construction, matching the
    // implicit default the server already applies.
    const body: TaskCreateRequest = {
      title: form.title.trim(),
      action: 'llm',
      prompt: form.prompt.trim(),
      priority: form.priority,
      workspace_id: workspaceId,
      surface: 'user',
      plan_id: effectivePlanId ?? undefined,
      agent_id: form.agentId === '__none__' ? undefined : form.agentId || undefined,
      // GOAL-FR-047: both lists are now mandatory — handleSubmit refuses
      // submission before this is ever called when either is empty.
      criteria: form.criteria,
      dod: form.dod,
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

    return body
  }

  function currentTodos(): Todo[] {
    return form.todos
      .map((t) => t.trim())
      .filter((t) => t.length > 0)
      .map((text) => ({ text, status: 'pending' as const }))
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

  // GOAL-FR-047/FR-053/FR-056: Title, Goal, at least one acceptance
  // criterion and at least one definition-of-done item are all mandatory at
  // creation — checked independently so a refusal says which is missing,
  // rather than stopping at the first failure.
  function handleSubmit(runNow: boolean) {
    let blocked = false

    if (!form.title.trim()) {
      setTitleError('Title is required')
      blocked = true
    } else {
      setTitleError('')
    }

    if (!form.prompt.trim()) {
      setGoalError('Goal is required')
      blocked = true
    } else {
      setGoalError('')
    }

    if (form.criteria.length === 0) {
      setCriteriaError('Add at least one acceptance criterion.')
      blocked = true
    } else {
      setCriteriaError('')
    }

    if (form.dod.length === 0) {
      setDodError('Add at least one definition-of-done item.')
      blocked = true
    } else {
      setDodError('')
    }

    if (blocked) return

    if (runNow) {
      createAndRunMutation.mutate()
    } else {
      createMutation.mutate()
    }
  }

  function resetAndClose() {
    setForm(INITIAL_FORM)
    setTitleError('')
    setGoalError('')
    setCriteriaError('')
    setDodError('')
    setNewTodo('')
    onOpenChange(false)
  }

  function handleOpenChange(next: boolean) {
    if (!next) resetAndClose()
    else onOpenChange(next)
  }

  // Plan changes must reconcile the dependency selection, not leave it
  // stranded. `depCandidates` below is filtered to the SELECTED plan (a
  // `blocked_by` edge has to stay inside one plan's DAG), so a dependency
  // picked under the previous plan disappears from the picker AND from the
  // chip row the moment the plan changes — while still riding along in
  // `form.blockedBy` to the POST body. The operator could neither see it nor
  // remove it, and the task was created with a cross-plan edge. Re-validate
  // against the new plan and drop whatever is no longer eligible.
  function handlePlanChange(next: string) {
    setForm((s) => {
      if (s.planId === next) return s
      if (s.blockedBy.length === 0) return { ...s, planId: next }
      const nextPlanId = next === '__none__' ? null : next
      return {
        ...s,
        planId: next,
        blockedBy: reconcileBlockedByForPlan(s.blockedBy, nextPlanId, wsTasks),
      }
    })
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

          {/* Goal (GOAL-FR-056 — was "Prompt"; this becomes the goal record
              once the task starts its own session). Required. */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ct-prompt" className="text-[var(--color-secondary)]">
              Goal <span className="text-[var(--color-error)]">*</span>
            </Label>
            <Textarea
              id="ct-prompt"
              value={form.prompt}
              onChange={(e) => { setForm((s) => ({ ...s, prompt: e.target.value })); setGoalError('') }}
              placeholder="What should this task achieve?"
              rows={4}
              maxLength={10000}
              className="text-xs font-mono resize-none"
              aria-invalid={!!goalError}
              aria-describedby={goalError ? 'ct-goal-error' : undefined}
            />
            {goalError && (
              <p id="ct-goal-error" className="text-xs text-[var(--color-error)]">{goalError}</p>
            )}
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

          {/* Plan (GOAL-FR-059) — defaults to the inherited board plan
              filter, but is now a real, changeable picker instead of a
              silent inherit-only value. */}
          <div className="flex flex-col gap-1.5">
            <Label className="text-[var(--color-secondary)]">Plan</Label>
            <SmartSelect
              value={form.planId}
              onValueChange={handlePlanChange}
              placeholder="No plan"
              triggerClassName="h-9 text-sm"
              ariaLabel="Plan"
              items={[
                { value: '__none__', label: 'No plan', className: 'text-xs' },
                ...plans.map((p) => ({ value: p.id, label: p.title, className: 'text-xs' })),
              ]}
            />
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

          {/* Acceptance criteria (ADR-049 — Definition of Done, SD-C13).
              GOAL-FR-047/FR-053: required — the D5 soft-tier empty hint is
              retired; a plain instruction replaces it (C-80: the `emptyHint`
              prop itself survives on AcceptanceCriteriaEditor, only this
              call site's attribute is removed). */}
          <div className="flex flex-col gap-1.5">
            <Label className="text-[var(--color-secondary)]">
              Acceptance criteria <span className="text-[var(--color-error)]">*</span>
            </Label>
            <AcceptanceCriteriaEditor
              criteria={form.criteria}
              onChange={(criteria) => { setForm((s) => ({ ...s, criteria })); setCriteriaError('') }}
              currentAuthor={{ kind: 'user', id: username ?? 'operator' }}
            />
            <p className="text-xs text-[var(--color-muted)]">Add at least one.</p>
            {criteriaError && (
              <p className="text-xs text-[var(--color-error)]">{criteriaError}</p>
            )}
          </div>

          {/* Definition of Done (GOAL-FR-003/FR-048/FR-053) — a second,
              DISTINCT list of standing quality gates, required at creation
              same as Acceptance criteria. `DefinitionOfDoneEditor` (U1) is a
              thin wrapper around `AcceptanceCriteriaEditor` and already
              supplies its own label, asterisk and standing helper line. */}
          <div className="flex flex-col gap-1.5">
            <DefinitionOfDoneEditor
              dod={form.dod}
              onChange={(dod) => { setForm((s) => ({ ...s, dod })); setDodError('') }}
              currentAuthor={{ kind: 'user', id: username ?? 'operator' }}
            />
            {dodError && (
              <p className="text-xs text-[var(--color-error)]">{dodError}</p>
            )}
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

          {/* Trigger — REMOVED (GOAL-FR-060). A normal task has no timer: it
              starts by a human pressing Start, an agent starting it, or a
              plan reaching it, none of which is a schedule. Time-based
              starts are the calendar's own job (its event slide-over keeps
              the full trigger editor); the board/list already exclude every
              scheduled task, so a task reaching this form is manual by
              definition. Scope limit: only the two CONTROLS are removed —
              the `trigger` model field, wire type, `isScheduledTrigger` /
              `buildTrigger` helpers and the calendar's own editor all stay
              (joint delivery plan U4 row). */}

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
            <Label className="text-[var(--color-secondary)]">Todos</Label>
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
                placeholder="Add a todo…"
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
