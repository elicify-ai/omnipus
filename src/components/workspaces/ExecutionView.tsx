import type { Task, Plan } from '@/lib/api'
import { cn } from '@/lib/utils'
import { TaskCard } from './TaskCard'

// ExecutionView is legacy — it was used before the unified Task model replaced
// the old BoardTask+GTDTask split. The Calendar view supersedes it in Sprint 2.
// Retained for backward compatibility with any route still referencing it.

const EXECUTION_COLUMNS = [
  { status: 'next',        label: 'Queued',      headerClassName: 'text-blue-400' },
  { status: 'in_progress', label: 'Running',     headerClassName: 'text-yellow-400' },
  { status: 'done',        label: 'Completed',   headerClassName: 'text-green-400' },
  { status: 'failed',      label: 'Failed',      headerClassName: 'text-red-400' },
] as const

interface ExecutionViewProps {
  tasks: Task[]
  plans: Plan[]
  onTaskClick: (task: Task) => void
}

export function ExecutionView({ tasks, plans, onTaskClick }: ExecutionViewProps) {
  return (
    <div className="flex gap-2 p-4 overflow-x-auto min-h-0 flex-1">
      {EXECUTION_COLUMNS.map((col) => {
        const colTasks = tasks.filter((t) => t.status === col.status)
        return (
          <div
            key={col.status}
            aria-label={`${col.label} column, ${colTasks.length} tasks`}
            className="flex flex-col min-w-[200px] flex-1 max-h-full rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-1)]"
          >
            <div className="flex items-center gap-2 px-3 py-2.5 border-b border-[var(--color-border)] flex-shrink-0">
              <span className={cn('text-xs font-semibold uppercase tracking-wider', col.headerClassName)}>
                {col.label}
              </span>
              <span className="ml-auto text-[10px] text-[var(--color-muted)] bg-[var(--color-surface-2)] rounded px-1.5 py-0.5">
                {colTasks.length}
              </span>
            </div>
            <div className="flex flex-col gap-2 p-2 flex-1 overflow-y-auto">
              {colTasks.map((task) => (
                <TaskCard
                  key={task.id}
                  task={task}
                  plans={plans}
                  onClick={() => onTaskClick(task)}
                />
              ))}
              {colTasks.length === 0 && (
                <p className="text-[10px] text-[var(--color-muted)] text-center py-4">No tasks</p>
              )}
            </div>
          </div>
        )
      })}
    </div>
  )
}
