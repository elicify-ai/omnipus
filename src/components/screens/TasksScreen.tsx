import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Plus,
  X,
  FolderOpen,
  Trash,
} from '@phosphor-icons/react'
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
import {
  fetchBoardTasks,
  createBoardTask,
  deleteBoardTask,
  boardTasksQueryKeys,
  fetchProjects,
  projectsQueryKeys,
  isApiError,
} from '@/lib/api'
import type { BoardTask } from '@/lib/api'
import { useProjectsStore } from '@/store/projectsStore'
import { useUiStore } from '@/store/ui'
import { cn } from '@/lib/utils'

// ── Column definitions ────────────────────────────────────────────────────────

type BoardStatus = BoardTask['status']

const COLUMNS: { status: BoardStatus; label: string }[] = [
  { status: 'inbox',   label: 'Inbox' },
  { status: 'next',    label: 'Next' },
  { status: 'active',  label: 'Active' },
  { status: 'waiting', label: 'Waiting' },
  { status: 'done',    label: 'Done' },
]

// ── Create Task slide-over ────────────────────────────────────────────────────

interface CreateTaskSlideOverProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

interface CreateTaskForm {
  name: string
  description: string
  status: BoardStatus
  project_id: string
}

function CreateTaskSlideOver({ open, onOpenChange }: CreateTaskSlideOverProps) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const activeProjectId = useProjectsStore((s) => s.activeProjectId)

  const { data: projects = [] } = useQuery({
    queryKey: projectsQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchProjects({ status: 'active' }),
    staleTime: 30_000,
  })

  const [form, setForm] = useState<CreateTaskForm>({
    name: '',
    description: '',
    status: 'inbox',
    project_id: activeProjectId ?? '',
  })
  const [nameError, setNameError] = useState('')

  const mutation = useMutation({
    mutationFn: () =>
      createBoardTask({
        name: form.name.trim(),
        description: form.description.trim() || undefined,
        status: form.status,
        project_id: form.project_id || undefined,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['board-tasks'] })
      addToast({ message: 'Task created', variant: 'success' })
      setForm({ name: '', description: '', status: 'inbox', project_id: activeProjectId ?? '' })
      setNameError('')
      onOpenChange(false)
    },
    onError: (err) => {
      const msg = isApiError(err) ? err.message : 'Unexpected error'
      addToast({ message: `Failed to create task: ${msg}`, variant: 'error' })
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

  function handleOpenChange(next: boolean) {
    if (!next) {
      setForm({ name: '', description: '', status: 'inbox', project_id: activeProjectId ?? '' })
      setNameError('')
    }
    onOpenChange(next)
  }

  return (
    <Sheet open={open} onOpenChange={handleOpenChange}>
      <SheetContent side="right" className="w-full sm:max-w-md flex flex-col">
        <SheetHeader>
          <SheetTitle className="font-headline text-[var(--color-secondary)]">
            New task
          </SheetTitle>
        </SheetHeader>

        <form onSubmit={handleSubmit} className="flex flex-col flex-1 gap-5 py-4">
          {/* Name */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-task-name" className="text-[var(--color-secondary)]">
              Name <span className="text-[var(--color-error)]">*</span>
            </Label>
            <Input
              id="new-task-name"
              value={form.name}
              onChange={(e) => setForm((s) => ({ ...s, name: e.target.value }))}
              placeholder="Task name"
              autoFocus
              aria-invalid={!!nameError}
              aria-describedby={nameError ? 'new-task-name-error' : undefined}
            />
            {nameError && (
              <p id="new-task-name-error" className="text-xs text-[var(--color-error)]">
                {nameError}
              </p>
            )}
          </div>

          {/* Description */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-task-desc" className="text-[var(--color-secondary)]">
              Description
            </Label>
            <Textarea
              id="new-task-desc"
              value={form.description}
              onChange={(e) => setForm((s) => ({ ...s, description: e.target.value }))}
              placeholder="Optional description"
              rows={3}
            />
          </div>

          {/* Status */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-task-status" className="text-[var(--color-secondary)]">
              Status
            </Label>
            <Select
              value={form.status}
              onValueChange={(v) => setForm((s) => ({ ...s, status: v as BoardStatus }))}
            >
              <SelectTrigger
                id="new-task-status"
                className="bg-[var(--color-surface-2)] border-[var(--color-border)] text-[var(--color-secondary)]"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {COLUMNS.map((col) => (
                  <SelectItem key={col.status} value={col.status}>
                    {col.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {/* Project */}
          {projects.length > 0 && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="new-task-project" className="text-[var(--color-secondary)]">
                Project
              </Label>
              <Select
                value={form.project_id}
                onValueChange={(v) => setForm((s) => ({ ...s, project_id: v }))}
              >
                <SelectTrigger
                  id="new-task-project"
                  className="bg-[var(--color-surface-2)] border-[var(--color-border)] text-[var(--color-secondary)]"
                >
                  <SelectValue placeholder="No project" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="">No project</SelectItem>
                  {projects.map((p) => (
                    <SelectItem key={p.id} value={p.id}>
                      {p.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}

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
              {mutation.isPending ? 'Creating…' : 'Create task'}
            </Button>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  )
}

// ── Task card ────────────────────────────────────────────────────────────────

interface TaskCardProps {
  task: BoardTask
  onDelete: (id: string) => void
  isDeleting: boolean
}

function TaskCard({ task, onDelete, isDeleting }: TaskCardProps) {
  const [expanded, setExpanded] = useState(false)

  return (
    <div
      className={cn(
        'rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-3 cursor-pointer transition-colors hover:border-[var(--color-border)]/70',
        expanded && 'border-[var(--color-accent)]/30',
      )}
      onClick={() => setExpanded((v) => !v)}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') setExpanded((v) => !v) }}
      aria-expanded={expanded}
    >
      <p className="text-sm font-medium text-[var(--color-secondary)] leading-snug">
        {task.name}
      </p>

      {!expanded && task.description && (
        <p className="mt-1 text-xs text-[var(--color-muted)] truncate">
          {task.description}
        </p>
      )}

      {task.agent_id && (
        <span className="mt-1.5 inline-block rounded-full bg-[var(--color-surface-2)] px-2 py-0.5 text-[10px] text-[var(--color-muted)]">
          {task.agent_id}
        </span>
      )}

      {expanded && (
        <div className="mt-2 border-t border-[var(--color-border)] pt-2" onClick={(e) => e.stopPropagation()}>
          {task.description && (
            <p className="text-xs text-[var(--color-muted)] whitespace-pre-wrap mb-2">
              {task.description}
            </p>
          )}
          <button
            type="button"
            onClick={() => onDelete(task.id)}
            disabled={isDeleting}
            className="flex items-center gap-1 text-xs text-[var(--color-error)] hover:underline disabled:opacity-50"
            aria-label={`Delete task ${task.name}`}
          >
            <Trash size={12} />
            Delete task
          </button>
        </div>
      )}
    </div>
  )
}

// ── Column ────────────────────────────────────────────────────────────────────

interface ColumnProps {
  label: string
  tasks: BoardTask[]
  onDelete: (id: string) => void
  deletingIds: Set<string>
}

function Column({ label, tasks, onDelete, deletingIds }: ColumnProps) {
  return (
    <div className="flex flex-col min-w-[220px] flex-1 bg-[var(--color-surface-1)] rounded-xl border border-[var(--color-border)]">
      {/* Column header */}
      <div className="flex items-center gap-2 px-3 py-2.5 border-b border-[var(--color-border)]">
        <span className="text-sm font-medium text-[var(--color-secondary)]">{label}</span>
        <span className="rounded-full bg-[var(--color-surface-2)] px-1.5 py-0.5 text-[10px] font-semibold text-[var(--color-muted)]">
          {tasks.length}
        </span>
      </div>

      {/* Task cards */}
      <div className="flex flex-col gap-2 p-2 flex-1 overflow-y-auto">
        {tasks.length === 0 ? (
          <p className="text-xs text-[var(--color-muted)] text-center py-4">No tasks</p>
        ) : (
          tasks.map((task) => (
            <TaskCard
              key={task.id}
              task={task}
              onDelete={onDelete}
              isDeleting={deletingIds.has(task.id)}
            />
          ))
        )}
      </div>

      {/* Column status indicator for screen readers */}
      <span className="sr-only">{label} column, {tasks.length} tasks</span>
    </div>
  )
}

// ── TasksScreen ───────────────────────────────────────────────────────────────

export function TasksScreen() {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const { activeProjectId, setActiveProjectId } = useProjectsStore()
  const [createTaskOpen, setCreateTaskOpen] = useState(false)
  const [deletingIds, setDeletingIds] = useState<Set<string>>(new Set())

  // Fetch projects for filter pill label
  const { data: projects = [] } = useQuery({
    queryKey: projectsQueryKeys.list({ status: 'active' }),
    queryFn: () => fetchProjects({ status: 'active' }),
    staleTime: 30_000,
  })
  const activeProject = activeProjectId ? projects.find((p) => p.id === activeProjectId) : null

  // Board tasks query — filtered by active project if set
  const { data: tasks = [], isError: tasksError, isLoading } = useQuery({
    queryKey: boardTasksQueryKeys.list({ project_id: activeProjectId ?? undefined }),
    queryFn: () => fetchBoardTasks({ project_id: activeProjectId ?? undefined }),
    refetchInterval: 15_000,
    staleTime: 10_000,
  })

  // Delete mutation
  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteBoardTask(id),
    onMutate: (id) => setDeletingIds((prev) => new Set([...prev, id])),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['board-tasks'] })
      addToast({ message: 'Task deleted', variant: 'success' })
    },
    onError: (err) => {
      const msg = isApiError(err) ? err.message : 'Unexpected error'
      addToast({ message: `Failed to delete task: ${msg}`, variant: 'error' })
    },
    onSettled: (_data, _err, id) => setDeletingIds((prev) => { const n = new Set(prev); n.delete(id); return n }),
  })

  return (
    <div className="absolute inset-0 overflow-y-auto pb-[env(safe-area-inset-bottom)]">
      {/* Header */}
      <div className="flex items-center gap-3 px-4 py-3 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] sticky top-0 z-10">
        <h2 className="font-headline text-lg font-semibold text-[var(--color-secondary)] flex-shrink-0">
          Tasks
        </h2>

        {/* Active project filter pill */}
        {activeProject && (
          <div
            role="status"
            aria-label={`Filtered by project: ${activeProject.name}`}
            className="flex items-center gap-1.5 rounded-full border border-[var(--color-accent)]/40 bg-[var(--color-surface-2)] px-3 py-1 text-xs text-[var(--color-accent)]"
          >
            <FolderOpen size={12} />
            <span className="max-w-[140px] truncate">{activeProject.name}</span>
            <button
              type="button"
              onClick={() => setActiveProjectId(null)}
              aria-label="Clear project filter"
              className="ml-0.5 rounded-full hover:bg-[var(--color-accent)]/20 p-0.5 transition-colors"
            >
              <X size={10} />
            </button>
          </div>
        )}

        <div className="flex-1" />

        {/* New task button */}
        <button
          type="button"
          onClick={() => setCreateTaskOpen(true)}
          className="flex items-center gap-1.5 rounded-lg bg-[var(--color-accent)] px-3 py-1.5 text-xs font-medium text-[var(--color-primary)] hover:bg-[var(--color-accent)]/90 transition-colors"
        >
          <Plus size={14} />
          New task
        </button>
      </div>

      {/* Error banner */}
      {tasksError && (
        <div className="px-4 py-2 text-xs text-[var(--color-error)] border-b border-[var(--color-border)]">
          Failed to load tasks. Check your connection and try again.
        </div>
      )}

      {/* Kanban board */}
      {isLoading ? (
        <div className="flex gap-3 p-4 overflow-x-auto">
          {COLUMNS.map((col) => (
            <div
              key={col.status}
              className="flex flex-col min-w-[220px] flex-1 rounded-xl border border-[var(--color-border)] animate-pulse"
            >
              <div className="h-10 border-b border-[var(--color-border)] bg-[var(--color-surface-2)]" />
              <div className="flex flex-col gap-2 p-2">
                {[1, 2].map((i) => (
                  <div key={i} className="h-14 rounded-lg bg-[var(--color-surface-2)]" />
                ))}
              </div>
            </div>
          ))}
        </div>
      ) : (
        <div className="flex gap-3 p-4 overflow-x-auto min-h-[calc(100vh-9rem)]">
          {COLUMNS.map((col) => (
            <Column
              key={col.status}
              label={col.label}
              tasks={tasks.filter((t) => t.status === col.status)}
              onDelete={(id) => deleteMutation.mutate(id)}
              deletingIds={deletingIds}
            />
          ))}
        </div>
      )}

      {/* Create task slide-over */}
      <CreateTaskSlideOver
        open={createTaskOpen}
        onOpenChange={setCreateTaskOpen}
      />
    </div>
  )
}
