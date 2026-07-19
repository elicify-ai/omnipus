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
import { useUiStore } from '@/store/ui'
import { CheckSquare, Square, CircleHalf, Trash, Plus } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

export interface TaskChecklistFieldProps {
  task: Task
  /** Disables add/toggle/remove interactions (read-only checklist). */
  disabled?: boolean
}

export function TaskChecklistField({ task, disabled = false }: TaskChecklistFieldProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const [newTodo, setNewTodo] = useState('')

  // Reset the in-progress "new item" draft when a different task is shown.
  useEffect(() => {
    setNewTodo('')
  }, [task.id])

  // Todos checklist — replace atomically via PUT /tasks/{id}/todos
  const { mutate: doSetTodos } = useMutation({
    mutationFn: (todos: Todo[]) => setTaskTodos(task.id, todos),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: tasksQueryKeys.list() })
    },
    onError: (err: unknown) =>
      addToast({
        message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to update checklist',
        variant: 'error',
      }),
  })

  function handleToggleTodo(index: number) {
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
    const text = newTodo.trim()
    if (!text) return
    const todos = [...(task.todos ?? []), { text, status: 'pending' as const }]
    doSetTodos(todos)
    setNewTodo('')
  }

  function handleRemoveTodo(index: number) {
    const todos = (task.todos ?? []).filter((_, i) => i !== index)
    doSetTodos(todos)
  }

  const todos = task.todos ?? []
  const doneTodos = todos.filter((t: Todo) => t.status === 'completed').length

  return (
    <div className="space-y-1.5">
      <p className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">
        {`Checklist${todos.length > 0 ? ` (${doneTodos}/${todos.length})` : ''}`}
      </p>
      <div className="space-y-1">
        {todos.map((todo: Todo, idx: number) => (
          <div
            key={idx}
            className="w-full flex items-center gap-2 px-2 py-1.5 rounded-md bg-[var(--color-surface-2)] text-xs"
          >
            <button tabIndex={0}
              type="button"
              onClick={() => handleToggleTodo(idx)}
              disabled={disabled}
              aria-label={`Toggle ${todo.text}`}
              role="checkbox"
              aria-checked={
                todo.status === 'completed' ? true : todo.status === 'in_progress' ? 'mixed' : false
              }
              className="flex items-center gap-2 flex-1 text-left hover:opacity-80 transition-opacity disabled:opacity-50 disabled:pointer-events-none"
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
            <button tabIndex={0}
              type="button"
              onClick={() => handleRemoveTodo(idx)}
              disabled={disabled}
              aria-label={`Remove checklist item ${todo.text}`}
              className="shrink-0 text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors disabled:opacity-50 disabled:pointer-events-none"
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
          disabled={disabled}
          className="text-xs flex-1 h-8"
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-8 px-2 shrink-0"
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
