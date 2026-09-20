// TaskChecklistField — the editable todos checklist, extracted from
// TaskDetailPanel so the calendar's recurring-task EDIT slide-over (the
// ONLY surface for a recurring task's checklist — Board/List exclude
// recurring tasks entirely, US-3) can reuse the exact same logic/JSX
// instead of reinventing it.
//
// Single source of truth: TaskDetailPanel imports and renders this same
// component for its own Checklist section — see TaskDetailPanel.tsx.

import { useEffect, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { setTaskTodos, isApiError, tasksQueryKeys } from '@/lib/api'
import type { Task, Todo } from '@/lib/api'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { useUiStore } from '@/store/ui'
import { CheckSquare, Square, CircleHalf, Trash, Plus } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

export interface TaskChecklistFieldProps {
  /**
   * Task-bound mode: every edit is persisted immediately via
   * PUT /tasks/{id}/todos. Used by TaskDetailPanel and the calendar EDIT
   * slide-over, where the task already exists.
   */
  task?: Task
  /**
   * Controlled/buffered mode (calendar CREATE flow): the parent owns the
   * array and no task exists yet, so edits are buffered locally and the parent
   * folds `value` into its create request. Providing `onChange` selects this
   * mode; `task` is then ignored for reads.
   */
  value?: Todo[]
  onChange?: (todos: Todo[]) => void
  /** Disables add/toggle/remove interactions (read-only checklist). */
  disabled?: boolean
}

export function TaskChecklistField({ task, value, onChange, disabled = false }: TaskChecklistFieldProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const [newTodo, setNewTodo] = useState('')

  // Controlled/buffered mode when the parent supplies an onChange (create flow);
  // otherwise task-bound (persist each edit immediately).
  const controlled = onChange != null

  // Reset the in-progress "new item" draft when a different task is shown.
  useEffect(() => {
    setNewTodo('')
  }, [task?.id])

  // Todos checklist — replace atomically via PUT /tasks/{id}/todos (task-bound
  // mode only; never invoked while controlled, so task is guaranteed present).
  const { mutate: doSetTodos } = useMutation({
    mutationFn: (todos: Todo[]) => setTaskTodos(task!.id, todos),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
    },
    onError: (err: unknown) =>
      addToast({
        message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to update checklist',
        variant: 'error',
      }),
  })

  // Single write path: buffer to the parent when controlled, else persist.
  function commit(todos: Todo[]) {
    if (controlled) onChange!(todos)
    else doSetTodos(todos)
  }

  const todos = controlled ? (value ?? []) : (task?.todos ?? [])

  function handleToggleTodo(index: number) {
    commit(
      todos.map((t, i) => {
        if (i !== index) return t
        // Cycle: completed → pending; anything else → completed.
        // in_progress is shown distinctly but clicking it marks it completed.
        const next = t.status === 'completed' ? 'pending' : 'completed'
        return { ...t, status: next } as Todo
      }),
    )
  }

  function handleAddTodo() {
    const text = newTodo.trim()
    if (!text) return
    commit([...todos, { text, status: 'pending' as const }])
    setNewTodo('')
  }

  function handleRemoveTodo(index: number) {
    commit(todos.filter((_, i) => i !== index))
  }

  const doneTodos = todos.filter((t: Todo) => t.status === 'completed').length

  return (
    <div className="space-y-[var(--space-1)]">
      <p className="text-[length:var(--type-caption-size)] font-semibold uppercase tracking-wider text-[var(--color-muted)]">
        {/* GOAL-FR-057 — relabelled Todos (visible text only; the three
            aria-labels below stay byte-identical, C-62/C-79). */}
        {`Todos${todos.length > 0 ? ` (${doneTodos}/${todos.length})` : ''}`}
      </p>
      <div className="space-y-[var(--space-1)]">
        {todos.map((todo: Todo, idx: number) => (
          <div
            key={idx}
            className="w-full flex items-center gap-[var(--space-2)] px-[var(--space-2)] py-[var(--space-1)] rounded-md bg-[var(--color-surface-2)] text-[length:var(--type-utility-xs-size)]"
          >
            {/* Button, not the catalogued Checkbox primitive — a decorative
                Checkbox-echo (same call as TaskDetailPanel.tsx's and
                CreateTaskSlideOver.tsx's dependency-picker rows). The
                catalogued Checkbox's Indicator hardcodes a single check
                glyph for both `checked` and `indeterminate`
                (`src/components/ui/checkbox.tsx`, out of this lane's scope),
                which would collapse this row's distinct completed
                (CheckSquare) vs. in-progress (CircleHalf) glyphs into the
                same icon — a real information loss, not a cosmetic one. */}
            <Button
              variant="ghost"
              onClick={() => handleToggleTodo(idx)}
              disabled={disabled}
              aria-label={`Toggle ${todo.text}`}
              role="checkbox"
              aria-checked={
                todo.status === 'completed' ? true : todo.status === 'in_progress' ? 'mixed' : false
              }
              className="h-auto flex-1 justify-start gap-[var(--space-2)] p-0 text-left hover:bg-transparent hover:opacity-80"
            >
              {todo.status === 'completed' ? (
                <CheckSquare size={13} className="shrink-0 text-[color:var(--color-success)]" />
              ) : todo.status === 'in_progress' ? (
                <CircleHalf size={13} className="shrink-0 text-[color:var(--color-status-in-progress)]" />
              ) : (
                <Square size={13} className="shrink-0 text-[var(--color-muted)]" />
              )}
              <span className={cn(
                'flex-1 text-[var(--color-secondary)]',
                todo.status === 'completed' ? 'line-through text-[var(--color-muted)]' : undefined,
                todo.status === 'in_progress' ? 'text-[color:var(--color-status-in-progress)]' : undefined,
              )}>
                {todo.text}
              </span>
            </Button>
            <IconButton
              onClick={() => handleRemoveTodo(idx)}
              disabled={disabled}
              aria-label={`Remove checklist item ${todo.text}`}
              variant="ghost"
              size="sm"
              className="h-auto w-auto shrink-0 p-0 text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-error)]"
            >
              <Trash size={12} />
            </IconButton>
          </div>
        ))}
      </div>
      <div className="flex items-center gap-[var(--space-2)] mt-[var(--space-1)]">
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
          placeholder="Add a todo…"
          maxLength={500}
          disabled={disabled}
          className="text-[length:var(--type-utility-xs-size)] flex-1 h-8"
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-8 px-[var(--space-2)] shrink-0"
          onClick={handleAddTodo}
          aria-label="Add checklist item"
          disabled={disabled || !newTodo.trim()}
        >
          <Plus size={13} />
        </Button>
      </div>
    </div>
  )
}
