import { Plus } from '@phosphor-icons/react'
import { TaskCard } from './TaskCard'
import { cn } from '@/lib/utils'
import type { BoardTask } from '@/lib/api'
import type { MilestoneWithProgress } from '@/lib/api'
import { MILESTONE_FILTER_UNSCHEDULED } from './MilestoneFilterPills'

type BoardStatus = BoardTask['status']

interface ColumnConfig {
  status: BoardStatus
  label: string
  headerClassName: string
}

const COLUMNS: ColumnConfig[] = [
  { status: 'inbox',   label: 'Inbox',   headerClassName: 'text-[var(--color-muted)]' },
  { status: 'next',    label: 'Next',    headerClassName: 'text-blue-400' },
  { status: 'active',  label: 'Active',  headerClassName: 'text-yellow-400' },
  { status: 'waiting', label: 'Waiting', headerClassName: 'text-purple-400' },
  { status: 'done',    label: 'Done',    headerClassName: 'text-green-400' },
  { status: 'failed',  label: 'Failed',  headerClassName: 'text-red-400' },
]

interface BoardViewProps {
  tasks: BoardTask[]
  milestones: MilestoneWithProgress[]
  activeMilestoneId: string | null
  onTaskClick: (task: BoardTask) => void
  onNewTask: (status?: BoardStatus) => void
}

export function BoardView({ tasks, milestones, activeMilestoneId, onTaskClick, onNewTask }: BoardViewProps) {
  // Apply milestone filter
  const filteredTasks = filterByMilestone(tasks, activeMilestoneId)

  return (
    <div className="flex gap-2 p-4 overflow-x-auto min-h-0 flex-1">
      {COLUMNS.map((col) => (
        <BoardColumn
          key={col.status}
          config={col}
          tasks={filteredTasks.filter((t) => t.status === col.status)}
          milestones={milestones}
          onTaskClick={onTaskClick}
          onNewTask={() => onNewTask(col.status)}
        />
      ))}
    </div>
  )
}

function filterByMilestone(tasks: BoardTask[], activeMilestoneId: string | null): BoardTask[] {
  if (activeMilestoneId === null) return tasks
  if (activeMilestoneId === MILESTONE_FILTER_UNSCHEDULED) {
    return tasks.filter((t) => !t.milestone_id)
  }
  return tasks.filter((t) => t.milestone_id === activeMilestoneId)
}

interface BoardColumnProps {
  config: ColumnConfig
  tasks: BoardTask[]
  milestones: MilestoneWithProgress[]
  onTaskClick: (task: BoardTask) => void
  onNewTask: () => void
}

function BoardColumn({ config, tasks, milestones, onTaskClick, onNewTask }: BoardColumnProps) {
  return (
    <div
      className="flex flex-col min-w-[180px] flex-1 bg-[var(--color-surface-1)] rounded-xl border border-[var(--color-border)] max-h-full"
      aria-label={`${config.label} column`}
    >
      {/* Column header */}
      <div className="flex items-center justify-between gap-2 px-3 py-2.5 border-b border-[var(--color-border)] flex-shrink-0">
        <div className="flex items-center gap-2">
          <span className={cn('text-sm font-semibold', config.headerClassName)}>
            {config.label}
          </span>
          <span className="rounded-full bg-[var(--color-surface-2)] px-1.5 py-0.5 text-[10px] font-semibold text-[var(--color-muted)]">
            {tasks.length}
          </span>
        </div>
        <button
          type="button"
          onClick={onNewTask}
          aria-label={`New ${config.label} task`}
          className="p-1 rounded text-[var(--color-muted)] hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-2)] transition-colors"
        >
          <Plus size={12} />
        </button>
      </div>

      {/* Task cards */}
      <div className="flex flex-col gap-2 p-2 flex-1 overflow-y-auto">
        {tasks.length === 0 ? (
          <button
            type="button"
            onClick={onNewTask}
            className="mx-auto mt-3 flex items-center gap-1 text-xs text-[var(--color-muted)] hover:text-[var(--color-accent)] transition-colors"
          >
            <Plus size={11} />
            Add your first task
          </button>
        ) : (
          tasks.map((task) => (
            <TaskCard
              key={task.id}
              task={task}
              milestones={milestones}
              onClick={() => onTaskClick(task)}
            />
          ))
        )}
      </div>

    </div>
  )
}
