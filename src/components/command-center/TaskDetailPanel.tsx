import { useState, useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import {
  fetchAgents,
  isWorker,
  fetchSubtasks,
  fetchMilestones,
  fetchWorkspaces,
  updateTask,
  deleteTask,
  isApiError,
  milestonesQueryKeys,
  workspacesQueryKeys,
  tasksQueryKeys,
} from '@/lib/api'
import type { Task, TaskUpdateRequest, Todo } from '@/lib/api'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { SmartSelect } from '@/components/ui/smart-select'
import { Textarea } from '@/components/ui/textarea'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
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
  Trash,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

// ── Status config ──────────────────────────────────────────────────────────────

// 7-state unified lifecycle
const STATUS_OPTIONS: { value: Task['status']; label: string; color: string }[] = [
  { value: 'inbox',       label: 'Inbox',       color: 'text-[var(--color-muted)]' },
  { value: 'next',        label: 'Next',        color: 'text-blue-400' },
  { value: 'planning',    label: 'Planning',    color: 'text-purple-400' },
  { value: 'in_progress', label: 'In Progress', color: 'text-yellow-400' },
  { value: 'blocked',     label: 'Blocked',     color: 'text-orange-400' },
  { value: 'done',        label: 'Done',        color: 'text-green-400' },
  { value: 'failed',      label: 'Failed',      color: 'text-red-400' },
]

const STATUS_BADGE: Record<string, string> = {
  inbox:       'text-[var(--color-muted)] bg-white/5',
  next:        'text-blue-400 bg-blue-400/10',
  planning:    'text-purple-400 bg-purple-400/10',
  in_progress: 'text-yellow-400 bg-yellow-400/10',
  blocked:     'text-orange-400 bg-orange-400/10',
  done:        'text-green-400 bg-green-400/10',
  failed:      'text-red-400 bg-red-400/10',
}

const PRIORITY_CONFIG: Record<number, { label: string; color: string }> = {
  1: { label: 'P1 — Critical',  color: 'text-red-400' },
  2: { label: 'P2 — High',      color: 'text-orange-400' },
  3: { label: 'P3 — Medium',    color: 'text-yellow-400' },
  4: { label: 'P4 — Low',       color: 'text-blue-400' },
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

  useEffect(() => {
    setPromptDraft(task?.prompt ?? '')
    setEditingPrompt(false)
    setSelectedWorkspaceId(task?.workspace_id ?? '')
  }, [task?.id, task?.prompt, task?.workspace_id])

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

  const { mutate: doUpdate } = useMutation({
    mutationFn: (data: TaskUpdateRequest) => {
      if (!task) return Promise.reject(new Error('No task selected'))
      return updateTask(task.id, data)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
      queryClient.invalidateQueries({ queryKey: ['workspaces'] })
    },
    onError: (err: unknown) =>
      addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to update task', variant: 'error' }),
  })

  // "Start" = PATCH status to in_progress (no /start endpoint in unified model)
  const { mutate: doStart, isPending: isStarting } = useMutation({
    mutationFn: () => {
      if (!task) return Promise.reject(new Error('No task selected'))
      return updateTask(task.id, { status: 'in_progress' })
    },
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
      queryClient.invalidateQueries({ queryKey: ['workspaces'] })
      if (data.session_id) {
        void navigate({ to: '/sessions/$sessionId', params: { sessionId: data.session_id } })
      } else {
        addToast({ message: 'Task started.', variant: 'success' })
      }
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
    const todos = (task.todos ?? []).map((t, i) =>
      i === index ? { ...t, done: !t.done } : t,
    )
    doUpdate({ todos })
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
  const doneTodos = todos.filter((t: Todo) => t.done).length

  return (
    <div className="space-y-5">
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
              className="absolute top-2 right-2 opacity-0 group-hover:opacity-100 transition-opacity p-1 rounded text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-1)]"
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
          <Badge className="h-8 text-xs bg-yellow-400/10 text-yellow-400 border-transparent rounded-md px-2 inline-flex items-center">
            In Progress
          </Badge>
        ) : (
          <SmartSelect
            value={task.status}
            onValueChange={(val) => doUpdate({ status: val as Task['status'] })}
            triggerClassName="h-8 text-xs"
            items={STATUS_OPTIONS.map((o) => ({
              value: o.value,
              label: o.label,
              className: cn('text-xs', o.color),
            }))}
          />
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

      {/* Todos checklist */}
      {todos.length > 0 && (
        <Field label={`Todos (${doneTodos}/${todos.length})`}>
          <div className="space-y-1">
            {todos.map((todo: Todo, idx: number) => (
              <button
                key={idx}
                type="button"
                onClick={() => handleToggleTodo(idx)}
                className="w-full flex items-center gap-2 px-2 py-1.5 rounded-md bg-[var(--color-surface-2)] text-xs hover:bg-[var(--color-surface-1)] transition-colors text-left"
              >
                {todo.done ? (
                  <CheckSquare size={13} className="shrink-0 text-green-400" />
                ) : (
                  <Square size={13} className="shrink-0 text-[var(--color-muted)]" />
                )}
                <span className={cn('flex-1 text-[var(--color-secondary)]', todo.done && 'line-through text-[var(--color-muted)]')}>
                  {todo.text}
                </span>
              </button>
            ))}
          </div>
        </Field>
      )}

      {/* Trigger */}
      {task.trigger && (
        <Field label="Trigger">
          <p className="text-xs text-[var(--color-muted)]">
            {task.trigger.type === 'manual' && 'Manual (drag to run)'}
            {task.trigger.type === 'once' && `Once — ${task.trigger.config?.at_ms ? new Date(task.trigger.config.at_ms).toLocaleString() : '(unset)'}`}
            {task.trigger.type === 'every' && `Every ${task.trigger.config?.every_ms ? Math.round(task.trigger.config.every_ms / 60000) + 'm' : '(unset)'}`}
            {task.trigger.type === 'recurring' && `Recurring — ${task.trigger.config?.cron_expr ?? '(no cron)'}`}
          </p>
        </Field>
      )}

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
          className="w-full gap-2 text-xs h-8 border-red-500/30 text-red-400 hover:bg-red-500/10"
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
          <div className={cn('relative', isFailed && 'ring-1 ring-red-500/30 rounded-md')}>
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
          className="w-full gap-2 text-xs h-8 text-red-400 hover:bg-red-500/10 hover:text-red-400"
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
