import { Plus } from '@phosphor-icons/react'
import { TaskCard } from './TaskCard'
import { AltitudeToggle } from './AltitudeToggle'
import { cn } from '@/lib/utils'
import type { Task, Agent, Milestone } from '@/lib/api'
import type { BoardAltitude } from '@/store/workspacesStore'
import { MILESTONE_FILTER_UNSCHEDULED } from './MilestoneFilterPills'

type TaskStatus = Task['status']

interface ColumnConfig {
  status: TaskStatus
  label: string
  headerClassName: string
}

// 7-state lifecycle: inbox → next → planning → in_progress → blocked → done → failed
const COLUMNS: ColumnConfig[] = [
  { status: 'inbox',       label: 'Inbox',       headerClassName: 'text-[var(--color-muted)]' },
  { status: 'next',        label: 'Next',        headerClassName: 'text-blue-400' },
  { status: 'planning',    label: 'Planning',    headerClassName: 'text-purple-400' },
  { status: 'in_progress', label: 'In Progress', headerClassName: 'text-yellow-400' },
  { status: 'blocked',     label: 'Blocked',     headerClassName: 'text-orange-400' },
  { status: 'done',        label: 'Done',        headerClassName: 'text-green-400' },
  { status: 'failed',      label: 'Failed',      headerClassName: 'text-red-400' },
]

interface BoardViewProps {
  tasks: Task[]
  milestones: Milestone[]
  agents?: Agent[]
  activeMilestoneId: string | null
  altitude: BoardAltitude
  onAltitudeChange: (next: BoardAltitude) => void
  onTaskClick: (task: Task) => void
  onNewTask: (status?: TaskStatus) => void
}

export function BoardView({
  tasks,
  milestones,
  agents = [],
  activeMilestoneId,
  altitude,
  onAltitudeChange,
  onTaskClick,
  onNewTask,
}: BoardViewProps) {
  // Filter out non-user-surface tasks (e.g. heartbeat tasks are hidden from general views)
  const userTasks = tasks.filter((t) => t.surface === 'user' || t.surface === undefined)
  // Apply milestone filter
  const filteredTasks = filterByMilestone(userTasks, activeMilestoneId)
  // Board shows only top-level tasks — subtasks (parent_task_id present) are
  // NEVER rendered as standalone cards. They nest under their parent.
  const rootTasks = filteredTasks.filter((t) => !t.parent_task_id)

  return (
    <div className="flex flex-col flex-1 min-h-0 overflow-hidden">
      {/* Board toolbar: altitude toggle */}
      <div className="flex items-center justify-end px-4 py-1.5 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] flex-shrink-0">
        <AltitudeToggle value={altitude} onChange={onAltitudeChange} />
      </div>

      {/* Columns */}
      <div className="flex gap-2 p-4 overflow-x-auto min-h-0 flex-1">
        {COLUMNS.map((col) => (
          <BoardColumn
            key={col.status}
            config={col}
            tasks={rootTasks.filter((t) => t.status === col.status)}
            milestones={milestones}
            agents={agents}
            altitude={altitude}
            onTaskClick={onTaskClick}
            onNewTask={() => onNewTask(col.status)}
          />
        ))}
      </div>
    </div>
  )
}

function filterByMilestone(tasks: Task[], activeMilestoneId: string | null): Task[] {
  if (activeMilestoneId === null) return tasks
  if (activeMilestoneId === MILESTONE_FILTER_UNSCHEDULED) {
    return tasks.filter((t) => !t.milestone_id)
  }
  return tasks.filter((t) => t.milestone_id === activeMilestoneId)
}

interface BoardColumnProps {
  config: ColumnConfig
  tasks: Task[]
  milestones: Milestone[]
  agents: Agent[]
  altitude: BoardAltitude
  onTaskClick: (task: Task) => void
  onNewTask: () => void
}

function BoardColumn({
  config,
  tasks,
  milestones,
  agents,
  altitude,
  onTaskClick,
  onNewTask,
}: BoardColumnProps) {
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
              agents={agents}
              altitude={altitude}
              onClick={() => onTaskClick(task)}
              onChildClick={onTaskClick}
            />
          ))
        )}
      </div>

    </div>
  )
}
