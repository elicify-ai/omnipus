import { useState, useEffect, useRef } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import {
  fetchAgents,
  isWorker,
  fetchSubtasks,
  fetchMilestones,
  fetchWorkspaces,
  fetchTasks,
  updateTask,
  deleteTask,
  setTaskTodos,
  setTaskDependencies,
  isApiError,
  milestonesQueryKeys,
  workspacesQueryKeys,
  tasksQueryKeys,
} from '@/lib/api'
import type { Task, TaskUpdateRequest, Todo, TaskTrigger } from '@/lib/api'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { SmartSelect } from '@/components/ui/smart-select'
import { Textarea } from '@/components/ui/textarea'
import { Input } from '@/components/ui/input'
import { DateTimePicker } from '@/components/ui/date-time-picker'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Checkbox } from '@/components/ui/checkbox'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import type { AutoSaveStatus } from '@/hooks/useAutoSave'
import { canDropTransition } from '@/components/workspaces/BoardView'
import { useUiStore } from '@/store/ui'
import {
  Play,
  Copy,
  PencilSimple,
  Check,
  Robot,
  X,
  ChatCircle,
  ArrowCounterClockwise,
  CheckSquare,
  Square,
  CircleHalf,
  Trash,
  Plus,
  CaretDown,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import {
  type TriggerKind,
  buildTrigger,
  toDatetimeLocalValue,
  datetimeLocalToMs,
  datetimeLocalToIso,
} from '@/components/workspaces/taskFormFields'

// ── Status config ──────────────────────────────────────────────────────────────

// User-settable status options (blocked is excluded — it is backend-derived and read-only)
// Theme-token colours. `text-[color:…]` keeps these as inline-var text colours
// (no raw Tailwind palette) so the panel tracks "The Sovereign Deep" tokens.
const STATUS_OPTIONS: { value: Task['status']; label: string; color: string }[] = [
  { value: 'inbox',       label: 'Inbox',       color: 'text-[var(--color-muted)]' },
  { value: 'next',        label: 'Next',        color: 'text-[color:var(--color-accent)]' },
  { value: 'planning',    label: 'Planning',    color: 'text-[color:var(--color-muted)]' },
  { value: 'in_progress', label: 'In Progress', color: 'text-[color:var(--color-warning)]' },
  { value: 'done',        label: 'Done',        color: 'text-[color:var(--color-success)]' },
  { value: 'failed',      label: 'Failed',      color: 'text-[color:var(--color-error)]' },
]

const STATUS_BADGE: Record<string, string> = {
  inbox:       'text-[var(--color-muted)] bg-white/5',
  next:        'text-[color:var(--color-accent)] bg-[var(--color-accent)]/10',
  planning:    'text-[color:var(--color-muted)] bg-white/5',
  in_progress: 'text-[color:var(--color-warning)] bg-[var(--color-warning)]/10',
  blocked:     'text-[color:var(--color-warning)] bg-[var(--color-warning)]/10',
  done:        'text-[color:var(--color-success)] bg-[var(--color-success)]/10',
  failed:      'text-[color:var(--color-error)] bg-[var(--color-error)]/10',
}

const PRIORITY_CONFIG: Record<number, { label: string; color: string }> = {
  1: { label: 'P1 — Critical',  color: 'text-[color:var(--color-error)]' },
  2: { label: 'P2 — High',      color: 'text-[color:var(--color-warning)]' },
  3: { label: 'P3 — Medium',    color: 'text-[color:var(--color-warning)]' },
  4: { label: 'P4 — Low',       color: 'text-[color:var(--color-accent)]' },
  5: { label: 'P5 — Minimal',   color: 'text-[var(--color-muted)]' },
}

// ── Props ──────────────────────────────────────────────────────────────────────

interface TaskDetailPanelProps {
  task: Task | null
  onClose: () => void
  onTaskSelect?: (task: Task) => void
  /** @deprecated Kept for callers that used the old two-mode API; ignored — all tasks use the unified type now. */
  taskMode?: string
}

// ── Component ──────────────────────────────────────────────────────────────────

export function TaskDetailPanel({ task, onClose, onTaskSelect }: TaskDetailPanelProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const [editingPrompt, setEditingPrompt] = useState(false)
  const [promptDraft, setPromptDraft] = useState('')
  const [selectedWorkspaceId, setSelectedWorkspaceId] = useState(task?.workspace_id ?? '')
  const [newTodo, setNewTodo] = useState('')
  // Inline field errors — surfaced instead of silently discarding invalid input.
  const [triggerError, setTriggerError] = useState('')
  const [dueError, setDueError] = useState('')
  const [statusError, setStatusError] = useState('')
  // DateTimePicker is fully controlled (value/onChange) — day, hour, and minute
  // picks each fire a separate onChange that must compose on top of the prior
  // pick, so these hold the in-progress edit and are re-synced from the task
  // whenever the server value changes (e.g. after a successful autosave PATCH).
  const [triggerAtDraft, setTriggerAtDraft] = useState<Date | null>(
    typeof task?.trigger?.config?.at_ms === 'number' ? new Date(task.trigger.config.at_ms) : null,
  )
  const [dueDraft, setDueDraft] = useState<Date | null>(task?.due ? new Date(task.due) : null)
  // Autosave indicator — every field change fires an immediate mutation; this
  // mirrors the AgentProfile / Gateway pattern so the user sees Saving…/Saved.
  const [saveStatus, setSaveStatus] = useState<AutoSaveStatus>('idle')
  const [saveError, setSaveError] = useState<string | undefined>(undefined)

  useEffect(() => {
    setPromptDraft(task?.prompt ?? '')
    setEditingPrompt(false)
    setSelectedWorkspaceId(task?.workspace_id ?? '')
    setNewTodo('')
    setTriggerError('')
    setDueError('')
    setStatusError('')
    setTriggerAtDraft(typeof task?.trigger?.config?.at_ms === 'number' ? new Date(task.trigger.config.at_ms) : null)
    setDueDraft(task?.due ? new Date(task.due) : null)
    setSaveStatus('idle')
    setSaveError(undefined)
  }, [task?.id, task?.prompt, task?.workspace_id, task?.due, task?.trigger?.config?.at_ms])

  const { data: agents = [] } = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })

  const { data: workspaces = [] } = useQuery({
    queryKey: workspacesQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchWorkspaces({ status: 'active' }),
    staleTime: 30_000,
  })

  const { data: milestones = [] } = useQuery({
    queryKey: milestonesQueryKeys.list(selectedWorkspaceId),
    queryFn: () => fetchMilestones(selectedWorkspaceId),
    enabled: !!selectedWorkspaceId,
    staleTime: 30_000,
  })

  const { data: subtasks = [] } = useQuery({
    queryKey: tasksQueryKeys.subtasks(task?.id ?? ''),
    queryFn: () => fetchSubtasks(task!.id),
    enabled: task != null && !task.parent_task_id,
  })

  // Sibling tasks in the same workspace — candidate dependencies (exclude self).
  const { data: wsTasks = [] } = useQuery({
    queryKey: tasksQueryKeys.list({ workspace_id: task?.workspace_id ?? '', surface: 'user' }),
    queryFn: () => fetchTasks({ workspace_id: task!.workspace_id, surface: 'user' }),
    enabled: task != null && !!task.workspace_id,
    staleTime: 10_000,
  })
  const depCandidates: Task[] = wsTasks.filter(
    (t) => t.id !== task?.id && !t.parent_task_id,
  )

  // Reset the "Saved" indicator back to idle after a short fade so it does not
  // linger between unrelated edits.
  const savedFadeRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => { if (savedFadeRef.current) clearTimeout(savedFadeRef.current) }, [])

  const { mutate: doUpdate } = useMutation({
    mutationFn: (data: TaskUpdateRequest) => {
      if (!task) return Promise.reject(new Error('No task selected'))
      return updateTask(task.id, data)
    },
    onMutate: () => {
      if (savedFadeRef.current) clearTimeout(savedFadeRef.current)
      setSaveError(undefined)
      setSaveStatus('saving')
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
      queryClient.invalidateQueries({ queryKey: ['workspaces'] })
      setSaveStatus('saved')
      savedFadeRef.current = setTimeout(() => setSaveStatus((s) => (s === 'saved' ? 'idle' : s)), 2000)
    },
    onError: (err: unknown) => {
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to update task'
      setSaveStatus('error')
      setSaveError(msg)
      addToast({ message: msg, variant: 'error' })
    },
  })

  // Todos checklist — replace atomically via PUT /tasks/{id}/todos
  const { mutate: doSetTodos } = useMutation({
    mutationFn: (todos: Todo[]) => {
      if (!task) return Promise.reject(new Error('No task selected'))
      return setTaskTodos(task.id, todos)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
    },
    onError: (err: unknown) =>
      addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to update checklist', variant: 'error' }),
  })

  // Dependencies — replace atomically via PUT /tasks/{id}/dependencies
  const { mutate: doSetDeps } = useMutation({
    mutationFn: (blockedBy: string[]) => {
      if (!task) return Promise.reject(new Error('No task selected'))
      return setTaskDependencies(task.id, blockedBy)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
    },
    onError: (err: unknown) =>
      addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to update dependencies', variant: 'error' }),
  })

  // "Start" = PATCH status to in_progress (no /start endpoint in unified model)
  const { mutate: doStart, isPending: isStarting } = useMutation({
    mutationFn: () => {
      if (!task) return Promise.reject(new Error('No task selected'))
      return updateTask(task.id, { status: 'in_progress' })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
      queryClient.invalidateQueries({ queryKey: ['workspaces'] })
      addToast({ message: 'Task started.', variant: 'success' })
    },
    onError: (err: unknown) =>
      addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to start task', variant: 'error' }),
  })

  // Retry = move failed task back to next
  const { mutate: doRetry, isPending: isRetrying } = useMutation({
    mutationFn: () => {
      if (!task) return Promise.reject(new Error('No task selected'))
      return updateTask(task.id, { status: 'next' })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
      queryClient.invalidateQueries({ queryKey: ['workspaces'] })
      addToast({ message: 'Task retried — moved to Next.', variant: 'success' })
    },
    onError: (err: unknown) =>
      addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to retry task', variant: 'error' }),
  })

  // Delete task
  const { mutate: doDelete, isPending: isDeleting } = useMutation({
    mutationFn: () => {
      if (!task) return Promise.reject(new Error('No task selected'))
      return deleteTask(task.id)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
      queryClient.invalidateQueries({ queryKey: ['workspaces'] })
      addToast({ message: 'Task deleted.', variant: 'success' })
      onClose()
    },
    onError: (err: unknown) =>
      addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to delete task', variant: 'error' }),
  })

  function handleSavePrompt() {
    const trimmed = promptDraft.trim()
    if (trimmed !== (task?.prompt ?? '').trim()) {
      doUpdate({ prompt: trimmed || undefined })
    }
    setEditingPrompt(false)
  }

  function handleToggleTodo(index: number) {
    if (!task) return
    const todos = (task.todos ?? []).map((t, i) => {
      if (i !== index) return t
      // Cycle: completed → pending; anything else → completed.
      // in_progress is shown distinctly but clicking it marks it completed.
      const next = t.status === 'completed' ? 'pending' : 'completed'
      return { ...t, status: next } as Todo
    })
    doSetTodos(todos)
  }

  function handleAddTodo() {
    if (!task) return
    const text = newTodo.trim()
    if (!text) return
    const todos = [...(task.todos ?? []), { text, status: 'pending' as const }]
    doSetTodos(todos)
    setNewTodo('')
  }

  function handleRemoveTodo(index: number) {
    if (!task) return
    const todos = (task.todos ?? []).filter((_, i) => i !== index)
    doSetTodos(todos)
  }

  function handleToggleDep(id: string) {
    if (!task) return
    const current = task.blocked_by ?? []
    const next = current.includes(id)
      ? current.filter((x) => x !== id)
      : [...current, id]
    doSetDeps(next)
  }

  function handleTriggerKindChange(kind: TriggerKind) {
    if (!task) return
    // Preserve existing config where it still applies, otherwise sensible defaults.
    const cfg = task.trigger?.config ?? {}
    let trigger: TaskTrigger
    if (kind === 'once') {
      trigger = buildTrigger('once', { at_ms: typeof cfg.at_ms === 'number' ? cfg.at_ms : Date.now() + 3_600_000 })
    } else if (kind === 'every') {
      trigger = buildTrigger('every', { every_ms: typeof cfg.every_ms === 'number' ? cfg.every_ms : 3_600_000 })
    } else if (kind === 'recurring') {
      trigger = buildTrigger('recurring', { cron_expr: typeof cfg.cron_expr === 'string' && cfg.cron_expr ? cfg.cron_expr : '0 9 * * MON' })
    } else {
      trigger = buildTrigger('manual', {})
    }
    doUpdate({ trigger })
  }

  function handleTriggerAtChange(value: string) {
    const at = datetimeLocalToMs(value)
    if (at == null) {
      setTriggerError('Pick a valid date and time for the one-time trigger.')
      return
    }
    setTriggerError('')
    doUpdate({ trigger: buildTrigger('once', { at_ms: at }) })
  }

  function handleTriggerEveryChange(minutes: number) {
    if (!Number.isFinite(minutes) || minutes < 1) {
      setTriggerError('Interval must be at least 1 minute.')
      return
    }
    setTriggerError('')
    doUpdate({ trigger: buildTrigger('every', { every_ms: minutes * 60_000 }) })
  }

  function handleTriggerCronChange(cron: string) {
    if (!cron.trim()) {
      setTriggerError('Enter a cron expression for the recurring trigger.')
      return
    }
    setTriggerError('')
    doUpdate({ trigger: buildTrigger('recurring', { cron_expr: cron.trim() }) })
  }

  function handleDueChange(value: string) {
    if (!value) {
      // Clearing the due date — the backend exposes an unambiguous `clear_due`
      // flag (an empty `due` string is not a valid date-time and is rejected).
      setDueError('')
      doUpdate({ clear_due: true })
      return
    }
    const iso = datetimeLocalToIso(value)
    if (!iso) {
      setDueError('Pick a valid date and time.')
      return
    }
    setDueError('')
    doUpdate({ due: iso })
  }

  // Done-terminal guard — mirror the board DnD `canDropTransition` so the panel
  // and the kanban agree on which status transitions the backend will accept.
  // (Can't leave `done` or `blocked`; can't enter `blocked`.)
  function isStatusDisabled(target: Task['status']): boolean {
    if (!task) return false
    if (target === task.status) return false
    return !canDropTransition(task.status, target).ok
  }

  function handleStatusChange(target: Task['status']) {
    if (!task) return
    if (target === task.status) return
    const verdict = canDropTransition(task.status, target)
    if (!verdict.ok) {
      setStatusError(verdict.reason ?? 'That status change is not allowed.')
      return
    }
    setStatusError('')
    doUpdate({ status: target })
  }

  async function handleCopyResult() {
    if (!task?.result) return
    try {
      await navigator.clipboard.writeText(task.result)
      addToast({ message: 'Result copied to clipboard.', variant: 'success' })
    } catch {
      addToast({ message: 'Failed to copy to clipboard.', variant: 'error' })
    }
  }

  async function handleCopyPath(path: string) {
    try {
      await navigator.clipboard.writeText(path)
      addToast({ message: 'Path copied.', variant: 'success' })
    } catch {
      addToast({ message: 'Failed to copy path.', variant: 'error' })
    }
  }

  function formatDate(iso?: string) {
    if (!iso) return '—'
    return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(iso))
  }

  if (!task) return null

  const isStartable = task.status === 'inbox' || task.status === 'next' || task.status === 'planning'
  const isFailed = task.status === 'failed'
  const isRunning = task.status === 'in_progress'
  const showResult = (task.status === 'done' || task.status === 'failed') && !!task.result
  const todos = task.todos ?? []
  const doneTodos = todos.filter((t: Todo) => t.status === 'completed').length
  const blockedBy = task.blocked_by ?? []
  const triggerKind: TriggerKind = task.trigger?.type ?? 'manual'
  const triggerCfg = task.trigger?.config ?? {}

  return (
    <div className="space-y-5">
      {/* Autosave indicator — every field change saves immediately. */}
      <div className="flex justify-end min-h-[14px]" data-testid="task-detail-autosave">
        <AutoSaveIndicator status={saveStatus} error={saveError} />
      </div>

      {/* Title */}
      <Field label="Title">
        <p className="text-sm font-medium text-[var(--color-secondary)]">{task.title}</p>
      </Field>

      {/* Prompt */}
      <Field label="Prompt / Instructions">
        {editingPrompt ? (
          <div className="space-y-1.5">
            <Textarea
              value={promptDraft}
              onChange={(e) => setPromptDraft(e.target.value)}
              className="text-xs min-h-[80px] font-mono"
              autoFocus
              maxLength={10000}
            />
            <div className="flex gap-1.5">
              <Button size="sm" className="h-6 px-2 text-[10px] gap-1" onClick={handleSavePrompt}>
                <Check size={10} weight="bold" /> Save
              </Button>
              <Button
                variant="ghost"
                size="sm"
                className="h-6 px-2 text-[10px] gap-1"
                onClick={() => { setPromptDraft(task.prompt ?? ''); setEditingPrompt(false) }}
              >
                <X size={10} /> Cancel
              </Button>
            </div>
          </div>
        ) : (
          <div className="relative group">
            <pre className="text-xs font-mono text-[var(--color-secondary)] bg-[var(--color-surface-2)] rounded-md p-3 whitespace-pre-wrap break-words leading-relaxed">
              {task.prompt || <span className="text-[var(--color-muted)]">No prompt set.</span>}
            </pre>
            <button
              type="button"
              onClick={() => setEditingPrompt(true)}
              className="absolute top-2 right-2 opacity-0 group-hover:opacity-100 [@media(hover:none)]:opacity-100 transition-opacity p-1 rounded text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-1)]"
              aria-label="Edit prompt"
            >
              <PencilSimple size={12} />
            </button>
          </div>
        )}
      </Field>

      {/* Priority */}
      <Field label="Priority">
        <SmartSelect
          value={String(task.priority ?? 3)}
          onValueChange={(val) => doUpdate({ priority: parseInt(val, 10) })}
          triggerClassName="h-8 text-xs"
          items={[1, 2, 3, 4, 5].map((p) => ({
            value: String(p),
            label: PRIORITY_CONFIG[p]?.label ?? `P${p}`,
            className: cn('text-xs', PRIORITY_CONFIG[p]?.color),
          }))}
        />
      </Field>

      {/* Status */}
      <Field label="Status">
        {isRunning ? (
          <Badge className="h-8 text-xs bg-[var(--color-warning)]/10 text-[color:var(--color-warning)] border-transparent rounded-md px-2 inline-flex items-center">
            In Progress
          </Badge>
        ) : task.status === 'blocked' ? (
          // blocked is backend-derived (unmet dependency) — show read-only, not selectable
          <Badge className="h-8 text-xs bg-[var(--color-warning)]/10 text-[color:var(--color-warning)] border-transparent rounded-md px-2 inline-flex items-center">
            Blocked (dependency unmet)
          </Badge>
        ) : task.status === 'done' ? (
          // Done is terminal — mirror canDropTransition (board DnD forbids
          // leaving done). Show a read-only badge instead of a dropdown that
          // offers transitions the backend rejects.
          <Badge
            data-testid="status-done-terminal"
            className="h-8 text-xs bg-[var(--color-success)]/10 text-[color:var(--color-success)] border-transparent rounded-md px-2 inline-flex items-center"
          >
            Done (final)
          </Badge>
        ) : (
          <SmartSelect
            value={task.status}
            onValueChange={(val) => handleStatusChange(val as Task['status'])}
            triggerClassName="h-8 text-xs"
            // Done is terminal and blocked is backend-derived — exclude both as
            // selectable targets (mirrors canDropTransition's "can't enter
            // blocked / can't leave done"). The current status stays selectable.
            items={STATUS_OPTIONS.filter(
              (o) => o.value === task.status || !isStatusDisabled(o.value),
            ).map((o) => ({
              value: o.value,
              label: o.label,
              className: cn('text-xs', o.color),
            }))}
          />
        )}
        {statusError && (
          <p className="text-xs text-[var(--color-error)] mt-1.5">{statusError}</p>
        )}
      </Field>

      {/* Workspace */}
      <Field label="Workspace">
        <p className="text-xs text-[var(--color-muted)]">
          {task.workspace_id
            ? (workspaces.find((p) => p.id === task.workspace_id)?.name ?? task.workspace_id)
            : '—'}
        </p>
      </Field>

      {/* Milestone */}
      <Field label="Milestone">
        <SmartSelect
          value={task.milestone_id ?? '__none__'}
          onValueChange={(val) => {
            doUpdate({ milestone_id: val === '__none__' ? '' : val })
          }}
          placeholder="No milestone"
          triggerClassName="h-8 text-xs"
          items={[
            { value: '__none__', label: 'No milestone', className: 'text-xs' },
            ...milestones.map((m) => ({ value: m.id, label: m.name, className: 'text-xs' })),
          ]}
        />
      </Field>

      {/* Agent */}
      <Field label="Agent">
        <SmartSelect
          value={task.agent_id ?? '__none__'}
          onValueChange={(val) => doUpdate({ agent_id: val === '__none__' ? '' : val })}
          placeholder="Unassigned"
          triggerClassName="h-8 text-xs"
          items={[
            { value: '__none__', label: 'Unassigned', className: 'text-xs' },
            ...agents
              .filter((a) => !isWorker(a))
              .map((a) => ({ value: a.id, label: a.name, className: 'text-xs' })),
          ]}
        />
      </Field>

      {/* Trigger (editable) */}
      <Field label="Trigger">
        <SmartSelect
          value={triggerKind}
          onValueChange={(val) => handleTriggerKindChange(val as TriggerKind)}
          triggerClassName="h-8 text-xs"
          items={[
            { value: 'manual', label: 'None (manual)', className: 'text-xs' },
            { value: 'once', label: 'Once (at a time)', className: 'text-xs' },
            { value: 'every', label: 'Every (interval)', className: 'text-xs' },
            { value: 'recurring', label: 'Recurring (cron)', className: 'text-xs' },
          ]}
        />
        {triggerKind === 'once' && (
          <DateTimePicker
            aria-label="Trigger date and time"
            value={triggerAtDraft}
            onChange={(d) => {
              setTriggerAtDraft(d)
              handleTriggerAtChange(d ? toDatetimeLocalValue(d.getTime()) : '')
            }}
            className="mt-1.5"
          />
        )}
        {triggerKind === 'every' && (
          <div className="mt-1.5 flex items-center gap-2">
            <Input
              aria-label="Interval in minutes"
              type="number"
              min={1}
              defaultValue={typeof triggerCfg.every_ms === 'number' ? String(Math.round(triggerCfg.every_ms / 60_000)) : '60'}
              onBlur={(e) => handleTriggerEveryChange(parseInt(e.target.value, 10))}
              className="text-xs w-28"
            />
            <span className="text-xs text-[var(--color-muted)]">minutes</span>
          </div>
        )}
        {triggerKind === 'recurring' && (
          <Input
            aria-label="Cron expression"
            defaultValue={typeof triggerCfg.cron_expr === 'string' ? triggerCfg.cron_expr : '0 9 * * MON'}
            onBlur={(e) => handleTriggerCronChange(e.target.value)}
            placeholder="0 9 * * MON"
            className="mt-1.5 text-xs font-mono"
          />
        )}
        {triggerError && (
          <p className="text-xs text-[var(--color-error)] mt-1.5">{triggerError}</p>
        )}
      </Field>

      {/* Depends on (blocked_by, editable) */}
      <Field label="Depends on">
        <Popover>
          <PopoverTrigger asChild>
            <Button
              type="button"
              variant="outline"
              className="justify-between h-8 text-xs bg-[var(--color-surface-2)] border-[var(--color-border)] text-[var(--color-secondary)] font-normal w-full"
              disabled={depCandidates.length === 0}
            >
              <span className="truncate">
                {depCandidates.length === 0
                  ? 'No other tasks'
                  : blockedBy.length === 0
                    ? 'No dependencies'
                    : `${blockedBy.length} task${blockedBy.length === 1 ? '' : 's'} selected`}
              </span>
              <CaretDown size={12} className="shrink-0 opacity-70" />
            </Button>
          </PopoverTrigger>
          <PopoverContent className="w-[var(--radix-popover-trigger-width)] max-h-64 overflow-y-auto p-1" align="start">
            {depCandidates.map((t) => {
              const checked = blockedBy.includes(t.id)
              return (
                <button
                  key={t.id}
                  type="button"
                  onClick={() => handleToggleDep(t.id)}
                  className="w-full flex items-center gap-2 px-2 py-1.5 rounded text-xs text-left hover:bg-[var(--color-surface-1)] transition-colors"
                >
                  <Checkbox checked={checked} className="pointer-events-none" />
                  <span className="flex-1 truncate text-[var(--color-secondary)]">{t.title}</span>
                </button>
              )
            })}
          </PopoverContent>
        </Popover>
        {blockedBy.length > 0 && (
          <div className="flex flex-wrap gap-1.5 mt-1.5">
            {blockedBy.map((id) => {
              const dep = depCandidates.find((x) => x.id === id) ?? wsTasks.find((x) => x.id === id)
              return (
                <span
                  key={id}
                  className="inline-flex items-center gap-1 rounded-full bg-[var(--color-surface-2)] border border-[var(--color-border)] px-2 py-0.5 text-[10px] text-[var(--color-secondary)]"
                >
                  <span className="max-w-[120px] truncate">{dep?.title ?? id}</span>
                  <button
                    type="button"
                    onClick={() => handleToggleDep(id)}
                    aria-label={`Remove dependency ${dep?.title ?? id}`}
                    className="text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
                  >
                    <X size={9} />
                  </button>
                </span>
              )
            })}
          </div>
        )}
      </Field>

      {/* Due date (editable) */}
      <Field label="Due date">
        <DateTimePicker
          aria-label="Due date"
          value={dueDraft}
          onChange={(d) => {
            setDueDraft(d)
            handleDueChange(d ? toDatetimeLocalValue(d.getTime()) : '')
          }}
        />
        {dueError && (
          <p className="text-xs text-[var(--color-error)] mt-1.5">{dueError}</p>
        )}
      </Field>

      {/* Todos checklist (editable: add / toggle / remove) */}
      <Field label={`Checklist${todos.length > 0 ? ` (${doneTodos}/${todos.length})` : ''}`}>
        <div className="space-y-1">
          {todos.map((todo: Todo, idx: number) => (
            <div
              key={idx}
              className="w-full flex items-center gap-2 px-2 py-1.5 rounded-md bg-[var(--color-surface-2)] text-xs"
            >
              <button
                type="button"
                onClick={() => handleToggleTodo(idx)}
                aria-label={`Toggle ${todo.text}`}
                className="flex items-center gap-2 flex-1 text-left hover:opacity-80 transition-opacity"
              >
                {todo.status === 'completed' ? (
                  <CheckSquare size={13} className="shrink-0 text-[color:var(--color-success)]" />
                ) : todo.status === 'in_progress' ? (
                  <CircleHalf size={13} className="shrink-0 text-[color:var(--color-warning)]" />
                ) : (
                  <Square size={13} className="shrink-0 text-[var(--color-muted)]" />
                )}
                <span className={cn(
                  'flex-1 text-[var(--color-secondary)]',
                  todo.status === 'completed' && 'line-through text-[var(--color-muted)]',
                  todo.status === 'in_progress' && 'text-[color:var(--color-warning)]',
                )}>
                  {todo.text}
                </span>
              </button>
              <button
                type="button"
                onClick={() => handleRemoveTodo(idx)}
                aria-label={`Remove checklist item ${todo.text}`}
                className="shrink-0 text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors"
              >
                <Trash size={12} />
              </button>
            </div>
          ))}
        </div>
        <div className="flex items-center gap-2 mt-1.5">
          <Input
            aria-label="New checklist item"
            value={newTodo}
            onChange={(e) => setNewTodo(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                handleAddTodo()
              }
            }}
            placeholder="Add a checklist item…"
            maxLength={500}
            className="text-xs flex-1 h-8"
          />
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-8 px-2 shrink-0"
            onClick={handleAddTodo}
            aria-label="Add checklist item"
            disabled={!newTodo.trim()}
          >
            <Plus size={13} />
          </Button>
        </div>
      </Field>

      {/* Start button — inbox / next / planning tasks */}
      {isStartable && (
        <Button
          className="w-full gap-2 text-xs h-8"
          onClick={() => doStart()}
          disabled={isStarting}
        >
          <Play size={13} weight="fill" />
          {isStarting ? 'Starting…' : 'Start Task'}
        </Button>
      )}

      {/* Retry button — failed tasks */}
      {isFailed && (
        <Button
          variant="outline"
          className="w-full gap-2 text-xs h-8 border-[var(--color-error)]/30 text-[color:var(--color-error)] hover:bg-[var(--color-error)]/10"
          onClick={() => doRetry()}
          disabled={isRetrying}
        >
          <ArrowCounterClockwise size={13} />
          {isRetrying ? 'Retrying…' : 'Retry'}
        </Button>
      )}

      {/* Open in Chat — when session_id is set */}
      {task.session_id && (
        <Button
          variant="outline"
          size="sm"
          className="w-full gap-2 text-xs h-8"
          onClick={() => {
            void navigate({ to: '/sessions/$sessionId', params: { sessionId: task.session_id! } })
            onClose()
          }}
        >
          <ChatCircle size={13} />
          Open in Chat
        </Button>
      )}

      {/* Result section — done or failed */}
      {showResult && task.result && (
        <Field label="Result">
          <div className={cn('relative', isFailed && 'ring-1 ring-[var(--color-error)]/30 rounded-md')}>
            <pre className="text-xs font-mono text-[var(--color-secondary)] bg-[var(--color-surface-2)] rounded-md p-3 max-h-[200px] overflow-y-auto whitespace-pre-wrap break-words leading-relaxed">
              {task.result}
            </pre>
            <button
              type="button"
              onClick={handleCopyResult}
              className="absolute top-2 right-2 flex items-center gap-1 px-1.5 py-0.5 text-[10px] rounded text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-1)] transition-colors"
              aria-label="Copy result"
            >
              <Copy size={11} /> Copy
            </button>
          </div>
        </Field>
      )}

      {/* Artifacts */}
      {(task.artifacts?.length ?? 0) > 0 && (
        <Field label="Artifacts">
          <div className="space-y-1">
            {task.artifacts!.map((path) => (
              <div key={path} className="flex items-center gap-2 px-2 py-1.5 rounded-md bg-[var(--color-surface-2)] text-xs">
                <span className="flex-1 font-mono text-[var(--color-secondary)] truncate">{path}</span>
                <button
                  type="button"
                  onClick={() => handleCopyPath(path)}
                  className="shrink-0 text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors"
                  aria-label={`Copy path: ${path}`}
                >
                  <Copy size={11} />
                </button>
              </div>
            ))}
          </div>
        </Field>
      )}

      {/* Sub-tasks (children via parent_task_id) */}
      {subtasks.length > 0 && (
        <Field label={`Sub-tasks (${subtasks.length})`}>
          <div className="space-y-1">
            {subtasks.map((sub) => (
              <button
                key={sub.id}
                type="button"
                onClick={() => onTaskSelect?.(sub)}
                className="w-full flex items-center gap-2 px-2 py-1.5 rounded-md bg-[var(--color-surface-2)] text-xs hover:bg-[var(--color-surface-1)] transition-colors text-left"
              >
                <Badge
                  variant="outline"
                  className={cn('text-[9px] px-1 py-0 shrink-0 border-0', STATUS_BADGE[sub.status] ?? '')}
                >
                  {sub.status}
                </Badge>
                <span className="flex-1 text-[var(--color-secondary)] truncate">{sub.title}</span>
                {sub.agent_name && (
                  <span className="shrink-0 text-[var(--color-muted)] flex items-center gap-0.5">
                    <Robot size={10} /> {sub.agent_name}
                  </span>
                )}
              </button>
            ))}
          </div>
        </Field>
      )}

      {/* Metadata */}
      <div className="pt-2 border-t border-[var(--color-border)] space-y-1.5">
        {task.created_by && <MetaRow label="Created by" value={task.created_by} />}
        <MetaRow label="Created" value={formatDate(task.created_at)} />
        <MetaRow label="Updated" value={formatDate(task.updated_at)} />
        <MetaRow label="Started" value={formatDate(task.started_at)} />
        <MetaRow label="Completed" value={formatDate(task.completed_at)} />
      </div>

      {/* Delete button (danger zone) */}
      <div className="pt-2 border-t border-[var(--color-border)]">
        <Button
          variant="ghost"
          size="sm"
          className="w-full gap-2 text-xs h-8 text-[color:var(--color-error)] hover:bg-[var(--color-error)]/10 hover:text-[color:var(--color-error)]"
          onClick={() => doDelete()}
          disabled={isDeleting}
        >
          <Trash size={13} />
          {isDeleting ? 'Deleting…' : 'Delete task'}
        </Button>
      </div>
    </div>
  )
}

// ── Workflow Task Detail Panel (legacy wrapper) ────────────────────────────────
// This export is kept for backward-compat with WorkflowTaskDetailPanel callers
// in the sessions route. Both old "workflow" and "gtd" modes now use the unified
// Task type — the mode distinction is gone.

export function WorkflowTaskDetailPanel({
  task,
  onClose,
  onTaskSelect,
}: {
  task: Task | null
  onClose: () => void
  onTaskSelect?: (task: Task) => void
}) {
  return (
    <Sheet open={task != null} onOpenChange={(open) => { if (!open) onClose() }}>
      <SheetContent side="right" className="w-full sm:w-[380px] md:w-[460px] overflow-y-auto">
        <SheetHeader className="mb-5">
          <SheetTitle className="pr-6 leading-snug">{task?.title ?? ''}</SheetTitle>
        </SheetHeader>

        {task && (
          <TaskDetailPanel task={task} onClose={onClose} onTaskSelect={onTaskSelect} />
        )}
      </SheetContent>
    </Sheet>
  )
}

// ── Field ──────────────────────────────────────────────────────────────────────

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <p className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">{label}</p>
      {children}
    </div>
  )
}

// ── MetaRow ────────────────────────────────────────────────────────────────────

function MetaRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center gap-2 text-xs">
      <span className="text-[var(--color-muted)] w-[80px] shrink-0">{label}</span>
      <span className="text-[var(--color-secondary)]">{value}</span>
    </div>
  )
}
